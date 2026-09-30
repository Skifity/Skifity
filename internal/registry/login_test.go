package registry

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A registry asking for a bearer token, the way Docker Hub and ghcr.io do,
// and one asking for basic authentication, the way a plain registry:2 does.

func TestALoginIsCheckedTheWayTheRegistryAsks(t *testing.T) {
	var tokenHits int
	var bearer *httptest.Server
	bearer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+bearer.URL+`/token",service="registry.test"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/token":
			tokenHits++
			user, pass, _ := r.BasicAuth()
			if r.URL.Query().Get("service") != "registry.test" || r.URL.Query().Get("account") != user {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if user != "robot" || pass != "right-password" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"token":"t"}`))
		}
	}))
	defer bearer.Close()
	host := strings.TrimPrefix(bearer.URL, "https://")

	if err := CheckLogin(t.Context(), bearer.Client(), host, "robot", "right-password"); err != nil {
		t.Errorf("good credentials were refused: %v", err)
	}
	if err := CheckLogin(t.Context(), bearer.Client(), host, "robot", "wrong"); !errors.Is(err, ErrLoginRefused) {
		t.Errorf("wrong credentials answered %v, want a refusal", err)
	}
	if tokenHits != 2 {
		t.Errorf("the token service was asked %d times", tokenHits)
	}

	basic := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); ok && user == "ci" && pass == "right-password" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Registry"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer basic.Close()
	host = strings.TrimPrefix(basic.URL, "https://")
	if err := CheckLogin(t.Context(), basic.Client(), host, "ci", "right-password"); err != nil {
		t.Errorf("good basic credentials were refused: %v", err)
	}
	if err := CheckLogin(t.Context(), basic.Client(), host, "ci", "wrong"); !errors.Is(err, ErrLoginRefused) {
		t.Errorf("wrong basic credentials answered %v", err)
	}
}

// A token service on plain HTTP would be sent the password in the clear.
func TestATokenServiceOverPlainHTTPIsNotSentThePassword(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="http://attacker.test/token",service="x"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	err := CheckLogin(t.Context(), server.Client(), strings.TrimPrefix(server.URL, "https://"), "u", "p")
	if err == nil || errors.Is(err, ErrLoginRefused) || !strings.Contains(err.Error(), "https") {
		t.Errorf("a plain-HTTP token service answered %v", err)
	}
}

func TestAChallengeIsRead(t *testing.T) {
	scheme, params := parseChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:a/b:pull"`)
	if scheme != "bearer" || params["realm"] != "https://auth.docker.io/token" ||
		params["service"] != "registry.docker.io" || params["scope"] != "repository:a/b:pull" {
		t.Errorf("read %q %v", scheme, params)
	}
}
