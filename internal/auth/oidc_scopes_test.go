package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// A provider's discovery document, with whichever scopes it says it has.
func discoveryServer(t *testing.T, scopes []string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 server.URL,
			"authorization_endpoint": server.URL + "/auth",
			"token_endpoint":         server.URL + "/token",
			"jwks_uri":               server.URL + "/keys",
			"scopes_supported":       scopes,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

// Dex and Okta name a person's groups in the ID token only when the groups
// scope is asked for, and list that scope in their discovery document. Asked
// for "openid profile email" alone, Dex sent no groups, and "Groups to teams"
// took everybody out of the teams it names. Google and Entra list no such
// scope and refuse one they do not know, so they are not asked.
func TestASignInAsksForGroupsWhereTheProviderHasThem(t *testing.T) {
	was := ssoClient
	ssoClient = &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(func() { ssoClient = was })

	for _, tc := range []struct {
		name       string
		scopes     []string
		wantGroups bool
	}{
		{"dex", []string{"openid", "email", "groups", "profile", "offline_access"}, true},
		{"google", []string{"openid", "email", "profile"}, false},
		{"nothing listed", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := discoveryServer(t, tc.scopes)
			o := NewOIDC(OIDCConfig{Issuer: server.URL, ClientID: "skifity", ClientSecret: "not-a-real-secret"})
			authURL, _, _, _, err := o.Start(t.Context(), "https://panel.example.test/api/auth/sso/callback")
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			parsed, err := url.Parse(authURL)
			if err != nil {
				t.Fatal(err)
			}
			asked := strings.Fields(parsed.Query().Get("scope"))
			for _, want := range []string{"openid", "profile", "email"} {
				if !slices.Contains(asked, want) {
					t.Errorf("the sign-in asked for %v, without %s", asked, want)
				}
			}
			if slices.Contains(asked, "groups") != tc.wantGroups {
				t.Errorf("the sign-in asked for %v from a provider listing %v", asked, tc.scopes)
			}
		})
	}
}
