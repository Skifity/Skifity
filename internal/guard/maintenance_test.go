package guard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func inMaintenance(allow ...string) Config {
	config := allowlist()
	shop := config.Sets["shop.example.com"]
	shop.Maintenance = &Maintenance{Message: "Back at 14:00.\n<b>Sorry</b> for the wait.", Allow: allow}
	config.Sets["shop.example.com"] = shop
	config.Sets["blog.example.com"] = Protected{AppID: "app_2",
		Maintenance: &Maintenance{Message: "Moving house."}}
	return config
}

// A visitor gets the page, with the status and header that tell a browser and
// a crawler it is temporary; the message is the team's, escaped.
func TestAnAppInMaintenanceAnswersWithThePage(t *testing.T) {
	handler := newGuard(t, inMaintenance()).Handler()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, forwarded("blog.example.com", "/posts/1", "198.51.100.9"))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != MaintenanceRetryAfter {
		t.Fatalf("got %d with Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if !strings.Contains(w.Body.String(), "<p>Moving house.</p>") || !strings.Contains(w.Body.String(), "<title>Moving house.</title>") {
		t.Fatalf("the page does not carry the message:\n%s", w.Body.String())
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, forwarded("shop.example.com", "/", "203.0.113.7"))
	body := w.Body.String()
	if strings.Contains(body, "<b>") || !strings.Contains(body, "&lt;b&gt;Sorry&lt;/b&gt;") {
		t.Fatalf("the message was not escaped:\n%s", body)
	}
}

// The firewall comes first: somebody it refuses is refused, not shown a page
// that says the site exists and when it is back.
func TestTheFirewallStillDecidesFirst(t *testing.T) {
	handler := newGuard(t, inMaintenance()).Handler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, forwarded("shop.example.com", "/", "198.51.100.9"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("somebody the firewall refuses got %d", w.Code)
	}
}

// Whoever is doing the work reaches the app; everybody else gets the page.
// An allow list belongs to its own app, and an entry that does not parse
// allows nobody.
func TestAnAllowedAddressReachesTheApp(t *testing.T) {
	config := inMaintenance("203.0.113.7", "not an address")
	blog := config.Sets["blog.example.com"]
	blog.Maintenance.Allow = []string{"2001:db8::/32"}
	config.Sets["blog.example.com"] = blog
	handler := newGuard(t, config).Handler()

	cases := []struct {
		host, client string
		want         int
	}{
		{"shop.example.com", "203.0.113.7", http.StatusOK},
		{"shop.example.com", "203.0.113.8", http.StatusServiceUnavailable},
		{"blog.example.com", "2001:db8::1", http.StatusOK},
		{"blog.example.com", "2001:db9::1", http.StatusServiceUnavailable},
		{"blog.example.com", "203.0.113.7", http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, forwarded(c.host, "/", c.client))
		if w.Code != c.want {
			t.Errorf("%s on %s: got %d, want %d", c.client, c.host, w.Code, c.want)
		}
	}
}

func TestParseAllowed(t *testing.T) {
	for entry, want := range map[string]string{
		"203.0.113.7":        "203.0.113.7/32",
		" 203.0.113.0/24 ":   "203.0.113.0/24",
		"203.0.113.9/24":     "203.0.113.0/24",
		"::ffff:203.0.113.7": "203.0.113.7/32",
		"2001:db8::/32":      "2001:db8::/32",
	} {
		got, err := ParseAllowed(entry)
		if err != nil || got.String() != want {
			t.Errorf("%q: %v, %v; want %s", entry, got, err, want)
		}
	}
	for _, bad := range []string{"", "office", "203.0.113.0/33", "example.com"} {
		if _, err := ParseAllowed(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
