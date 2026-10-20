package registryguard

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var key = bytes.Repeat([]byte("k"), 32)

func TestATokenIsForOneRepositoryUntilItExpires(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	token := Sign(key, "acme-shop-production/web", now.Add(time.Hour))

	repository, err := Verify(key, token, now)
	if err != nil || repository != "acme-shop-production/web" {
		t.Fatalf("Verify = %q, %v", repository, err)
	}
	if _, err := Verify(key, token, now.Add(time.Hour)); err == nil {
		t.Error("a token was accepted after it expired")
	}
	if _, err := Verify(bytes.Repeat([]byte("x"), 32), token, now); err == nil {
		t.Error("a token signed with another key was accepted")
	}

	// Changing what it is for breaks the signature.
	forged := strings.Replace(token, token[len(tokenPrefix):strings.LastIndex(token, ".")],
		Sign(key, "other-team-production/web", now.Add(time.Hour))[len(tokenPrefix):strings.LastIndex(token, ".")], 1)
	if _, err := Verify(key, forged, now); err == nil {
		t.Error("a token whose repository was changed was accepted")
	}
	for _, junk := range []string{"", "v1.", "v1.a.b", "v2." + token[3:], "Bearer x"} {
		if _, err := Verify(key, junk, now); err == nil {
			t.Errorf("%q was accepted as a token", junk)
		}
	}
}

// upstream records what reached the registry.
type upstream struct {
	requests []string
	auth     []string
}

func newGuard(t *testing.T) (*Guard, *upstream) {
	t.Helper()
	seen := &upstream{}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.requests = append(seen.requests, r.Method+" "+r.URL.Path+" host="+r.Host)
		seen.auth = append(seen.auth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(registry.Close)
	target, _ := url.Parse(registry.URL)
	g, err := New(key, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return g, seen
}

func ask(g *Guard, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://skifity-registry.skifity-builds.svc.cluster.local:5000"+path, nil)
	if token != "" {
		r.SetBasicAuth(User, token)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func TestOnlyABuildsOwnRepositoryCanBeWritten(t *testing.T) {
	g, seen := newGuard(t)
	ours := Sign(key, "acme-shop-production/web", time.Now().Add(time.Hour))
	panel := Sign(key, AnyRepository, time.Now().Add(time.Hour))

	cases := []struct {
		name, method, path, token string
		want                      int
	}{
		// Reading is open: every server's container runtime pulls this way.
		{"the version check", http.MethodGet, "/v2/", "", http.StatusAccepted},
		{"pulling a manifest", http.MethodGet, "/v2/other-team-production/api/manifests/abc", "", http.StatusAccepted},
		{"checking a blob", http.MethodHead, "/v2/other-team-production/api/blobs/sha256:00", "", http.StatusAccepted},
		{"listing tags", http.MethodGet, "/v2/other-team-production/api/tags/list", "", http.StatusAccepted},

		// Writing needs a token, for that repository.
		{"an anonymous upload", http.MethodPost, "/v2/acme-shop-production/web/blobs/uploads/", "", http.StatusUnauthorized},
		{"an anonymous manifest", http.MethodPut, "/v2/acme-shop-production/web/manifests/v2", "", http.StatusUnauthorized},
		{"its own upload", http.MethodPost, "/v2/acme-shop-production/web/blobs/uploads/", ours, http.StatusAccepted},
		{"the rest of its upload", http.MethodPatch, "/v2/acme-shop-production/web/blobs/uploads/9f8e", ours, http.StatusAccepted},
		{"its own manifest", http.MethodPut, "/v2/acme-shop-production/web/manifests/buildcache", ours, http.StatusAccepted},
		{"another team's manifest", http.MethodPut, "/v2/other-team-production/api/manifests/latest", ours, http.StatusForbidden},
		{"an app whose name starts like its own", http.MethodPut, "/v2/acme-shop-production/web-admin/manifests/v1", ours, http.StatusForbidden},
		{"deleting its own image", http.MethodDelete, "/v2/acme-shop-production/web/manifests/sha256:00", ours, http.StatusForbidden},
		{"listing every repository", http.MethodGet, "/v2/_catalog", "", http.StatusUnauthorized},

		// The panel's own token sweeps.
		{"the panel deleting", http.MethodDelete, "/v2/other-team-production/api/manifests/sha256:00", panel, http.StatusAccepted},
		{"the panel listing", http.MethodGet, "/v2/_catalog", panel, http.StatusAccepted},
	}
	for _, c := range cases {
		if w := ask(g, c.method, c.path, c.token); w.Code != c.want {
			t.Errorf("%s: %s %s answered %d, want %d", c.name, c.method, c.path, w.Code, c.want)
		}
	}

	// What reached the registry carried no token, and kept the host the
	// client asked for, so the registry's upload addresses point back here.
	for i, request := range seen.requests {
		if seen.auth[i] != "" {
			t.Errorf("the registry was sent the token for %s", request)
		}
		if !strings.Contains(request, "host=skifity-registry.skifity-builds.svc.cluster.local:5000") {
			t.Errorf("the registry was asked as another host: %s", request)
		}
	}
}

func TestARefusalIsOneARegistryClientUnderstands(t *testing.T) {
	g, _ := newGuard(t)
	w := ask(g, http.MethodPost, "/v2/acme-shop-production/web/blobs/uploads/", "")
	if got := w.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
		t.Errorf("no challenge was sent (%q), so a client never offers its credentials", got)
	}
	if !strings.Contains(w.Body.String(), `"code":"UNAUTHORIZED"`) {
		t.Errorf("the body is not a registry error: %s", w.Body.String())
	}
	other := Sign(key, "acme-shop-production/web", time.Now().Add(time.Hour))
	w = ask(g, http.MethodPut, "/v2/other-team-production/api/manifests/latest", other)
	if !strings.Contains(w.Body.String(), `"code":"DENIED"`) {
		t.Errorf("a token for the wrong repository did not say so: %s", w.Body.String())
	}
}

func TestAShortKeyIsRefused(t *testing.T) {
	target, _ := url.Parse("http://127.0.0.1:5080")
	if _, err := New([]byte("short"), target, nil); err == nil {
		t.Error("a five-byte key was accepted")
	}
}
