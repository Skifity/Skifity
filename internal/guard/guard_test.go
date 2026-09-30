package guard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"skifity/internal/edgerules"
)

func writeConfig(t *testing.T, dir string, config Config) string {
	t.Helper()
	path := filepath.Join(dir, "rules.json")
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newGuard(t *testing.T, config Config) *Guard {
	t.Helper()
	dir := t.TempDir()
	g := New(Options{
		ConfigPath:  writeConfig(t, dir, config),
		DataDir:     dir,
		PodCIDR:     "10.42.0.0/16",
		ServiceCIDR: "10.43.0.0/16",
	})
	t.Cleanup(g.Close)
	if err := g.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	return g
}

// forwarded builds the request Traefik actually sends: the original details in
// X-Forwarded-* headers, from a pod address.
func forwarded(host, path, clientIP string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	r.RemoteAddr = "10.42.1.5:34567"
	r.Header.Set("X-Forwarded-Host", host)
	r.Header.Set("X-Forwarded-Uri", path)
	r.Header.Set("X-Forwarded-Method", "GET")
	r.Header.Set("X-Forwarded-For", clientIP)
	return r
}

func allowlist() Config {
	return Config{Sets: map[string]Protected{
		"shop.example.com": {
			AppID: "app_1",
			RuleSet: edgerules.RuleSet{
				Default: edgerules.ActionBlock,
				Rules: []edgerules.Rule{{
					ID: "r1", Name: "the office", Action: edgerules.ActionAllow, Enabled: true,
					Expr: edgerules.Expr{Test: &edgerules.Test{
						Field: edgerules.FieldIP, Op: edgerules.OpIn,
						Values: []string{"203.0.113.0/24"},
					}},
				}},
			},
		},
	}}
}

// The whole point, end to end through the HTTP handler Traefik talks to.
func TestAnAllowlistAllowsAndBlocks(t *testing.T) {
	handler := newGuard(t, allowlist()).Handler()

	cases := []struct {
		name   string
		client string
		want   int
	}{
		{"somebody in the office", "203.0.113.7", http.StatusOK},
		{"somebody else", "198.51.100.9", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, forwarded("shop.example.com", "/", tc.client))
			if w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

// The attack this component exists to survive: a visitor writing their own
// X-Forwarded-For to claim an address on the allowlist. The ingress appends the
// real one on the right, and that is the one judged.
func TestAVisitorCannotChooseTheAddressItIsJudgedBy(t *testing.T) {
	handler := newGuard(t, allowlist()).Handler()

	r := forwarded("shop.example.com", "/", "203.0.113.7, 198.51.100.9")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("a forged header got in with %d", w.Code)
	}

	// And the honest case still works.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, forwarded("shop.example.com", "/", "203.0.113.7"))
	if w.Code != http.StatusOK {
		t.Errorf("the office was refused with %d", w.Code)
	}
}

// A hostname nobody asked to protect must not be broken by a middleware that
// outlived its rules.
func TestAnUnprotectedHostnameIsLetThrough(t *testing.T) {
	handler := newGuard(t, allowlist()).Handler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, forwarded("blog.example.com", "/", "198.51.100.9"))
	if w.Code != http.StatusOK {
		t.Errorf("an unprotected hostname was refused with %d", w.Code)
	}
}

// A guard that has loaded nothing cannot judge anything, and a process whose
// whole purpose is to refuse must refuse rather than wave everything through.
func TestNothingLoadedRefuses(t *testing.T) {
	g := New(Options{ConfigPath: filepath.Join(t.TempDir(), "absent.json")})
	t.Cleanup(g.Close)
	if err := g.Reload(); err == nil {
		t.Fatal("reading a file that is not there reported success")
	}

	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, forwarded("shop.example.com", "/", "203.0.113.7"))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", w.Code)
	}

	// Readiness says the same, so a rollout never puts this replica in front of
	// traffic it cannot judge.
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("healthz said %d while no rules were loaded", w.Code)
	}
}

// An earlier draft let a rule set choose to fail open. It could not work: the
// choice lived inside the file the guard had failed to read, and Traefik
// decides what to do with an unreachable authorizer before this process is
// reached at all. The knob is gone; this is what is left, and it is honest.
func TestThereIsNoFailOpenBecauseThereCannotBe(t *testing.T) {
	dir := t.TempDir()
	g := New(Options{ConfigPath: writeConfig(t, dir, allowlist()), DataDir: dir})
	t.Cleanup(g.Close)
	// Deliberately not reloaded: the guard that cannot decide.
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, forwarded("shop.example.com", "/", "203.0.113.7"))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("a guard with no rules answered %d, want 503", w.Code)
	}
}

// A rules file it cannot parse leaves the previous rules in force. An empty
// rule set would be a firewall that turns itself off when somebody writes a bad
// byte, which is the one behaviour a firewall must never have.
func TestUnreadableRulesLeaveThePreviousOnesInForce(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, allowlist())
	g := New(Options{ConfigPath: path, DataDir: dir, PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16"})
	t.Cleanup(g.Close)
	if err := g.Reload(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := g.Reload(); err == nil {
		t.Fatal("unreadable rules were accepted")
	}

	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, forwarded("shop.example.com", "/", "198.51.100.9"))
	if w.Code != http.StatusForbidden {
		t.Errorf("the firewall turned itself off: %d", w.Code)
	}
}

// A rule on the path must read the path, not the path with its query string:
// a rule about "/admin" that "?next=/admin" could satisfy means nothing.
func TestTheQueryStringIsNotPartOfThePath(t *testing.T) {
	config := Config{Sets: map[string]Protected{"shop.example.com": {
		RuleSet: edgerules.RuleSet{
			Default: edgerules.ActionAllow,
			Rules: []edgerules.Rule{{
				Name: "keep people out of the admin area", Action: edgerules.ActionBlock, Enabled: true,
				Expr: edgerules.Expr{Test: &edgerules.Test{
					Field: edgerules.FieldPath, Op: edgerules.OpStartsWith, Values: []string{"/admin"},
				}},
			}},
		},
	}}}
	handler := newGuard(t, config).Handler()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, forwarded("shop.example.com", "/admin/users", "198.51.100.9"))
	if w.Code != http.StatusForbidden {
		t.Errorf("the admin area was reachable: %d", w.Code)
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, forwarded("shop.example.com", "/login?next=/admin", "198.51.100.9"))
	if w.Code != http.StatusOK {
		t.Errorf("a query string blocked an ordinary page: %d", w.Code)
	}
}

// A blocked visitor must not be told which rule stopped them: that is telling
// them what to change.
func TestABlockedVisitorIsToldNothingUseful(t *testing.T) {
	handler := newGuard(t, allowlist()).Handler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, forwarded("shop.example.com", "/", "198.51.100.9"))
	if body := w.Body.String(); len(body) > 40 || contains(body, "office") || contains(body, "203.0.113") {
		t.Errorf("the refusal described the rule: %q", body)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// The hostname is matched without its port and without case, because a Host
// header carries both and a rule is written with neither.
func TestTheHostnameIsMatchedLoosely(t *testing.T) {
	handler := newGuard(t, allowlist()).Handler()
	for _, host := range []string{"shop.example.com", "SHOP.example.com", "shop.example.com:443", "shop.example.com."} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, forwarded(host, "/", "198.51.100.9"))
		if w.Code != http.StatusForbidden {
			t.Errorf("%q was not recognised as the protected host: %d", host, w.Code)
		}
	}
}
