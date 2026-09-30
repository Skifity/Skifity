package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"skifity/internal/auth"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Single sign-on is the one place where somebody with no account here reaches
// code that can create one, so what it refuses matters more than what it
// accepts. The exchange itself needs a real provider and is covered by the
// library that does it; these are the guards around it, which are ours.

// callback makes a request to the callback without following the redirect, and
// returns where it sent the browser and whether a session cookie came back.
func (h *harness) callback(t *testing.T, query string, state *http.Cookie) (location string, signedIn bool) {
	t.Helper()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	jar, _ := cookiejar.New(nil)
	client.Jar = jar

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		h.server.URL+"/api/auth/sso/callback?"+query, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if state != nil {
		req.AddCookie(state)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			signedIn = true
		}
	}
	return resp.Header.Get("Location"), signedIn
}

// pendingCookie builds the cookie the start half would have set.
func pendingCookie(t *testing.T, state, nonce string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(ssoState{State: state, Nonce: nonce, Verifier: "v"})
	if err != nil {
		t.Fatalf("encode state: %v", err)
	}
	return &http.Cookie{Name: auth.SSOStateCookieName, Value: base64.RawURLEncoding.EncodeToString(body)}
}

func TestASingleSignOnCallbackRefusesWhatItDidNotIssue(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct {
		name   string
		query  string
		cookie *http.Cookie
		want   string
	}{
		{"no cookie at all", "code=c&state=s", nil, "expired"},
		{"a cookie that is not ours", "code=c&state=s",
			&http.Cookie{Name: auth.SSOStateCookieName, Value: "not base64 at all"}, "expired"},
		{"a state that does not match", "code=c&state=somebody-elses",
			pendingCookie(t, "ours", "n"), "state"},
		{"no state at all", "code=c", pendingCookie(t, "ours", "n"), "state"},
		{"a code with no state", "code=c&state=", pendingCookie(t, "ours", "n"), "state"},
		{"the provider said no", "error=access_denied&state=ours",
			pendingCookie(t, "ours", "n"), "refused"},
		{"a matching state and no code", "state=ours", pendingCookie(t, "ours", "n"), "refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location, signedIn := h.callback(t, tc.query, tc.cookie)
			if signedIn {
				t.Fatal("a session was issued")
			}
			if !strings.Contains(location, "sso_error="+tc.want) {
				t.Errorf("sent the browser to %q, want an sso_error of %q", location, tc.want)
			}
		})
	}
}

// TestASingleSignOnStateIsSpentOnFirstUse: the cookie is cleared before the
// code is exchanged, so resending the same callback cannot replay it.
func TestASingleSignOnStateIsSpentOnFirstUse(t *testing.T) {
	h := newHarness(t)
	cookie := pendingCookie(t, "ours", "n")

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet,
		h.server.URL+"/api/auth/sso/callback?code=c&state=ours", nil)
	req.AddCookie(cookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()

	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == auth.SSOStateCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the pending state cookie was not cleared, so the same callback can be sent again")
	}
}

// TestSingleSignOnIsOffUntilItIsConfigured: the button is not offered, and the
// route that starts a sign-in says so rather than failing obscurely.
func TestSingleSignOnIsOffUntilItIsConfigured(t *testing.T) {
	h := newHarness(t)

	status, body := h.do(tenant{}, http.MethodGet, "/api/meta", nil)
	if status != http.StatusOK {
		t.Fatalf("meta answered %d\n%s", status, body)
	}
	var meta struct {
		SSO ssoStatus `json:"sso"`
	}
	if err := json.Unmarshal([]byte(body), &meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if meta.SSO.Enabled {
		t.Error("single sign-on is advertised with nothing configured")
	}

	if status, body := h.do(tenant{}, http.MethodGet, "/api/auth/sso/start", nil); status != http.StatusNotFound {
		t.Errorf("starting a sign-in answered %d with no provider, want 404\n%s", status, body)
	}
}

// TestSingleSignOnNeedsThePanelToKnowItsOwnAddress: the redirect URI has to
// match what is registered with the provider exactly, so it comes from the
// Panel URL setting rather than from the request's Host — which a caller
// controls, and which would otherwise let somebody register their own hostname
// as a redirect target.
func TestSingleSignOnNeedsThePanelToKnowItsOwnAddress(t *testing.T) {
	h := newHarness(t)
	h.setSSO(t, "https://login.example.test", "client", "secret")

	status, body := h.do(tenant{}, http.MethodGet, "/api/auth/sso/start", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("starting a sign-in with no Panel URL answered %d, want 400\n%s", status, body)
	}
	if !strings.Contains(body, "sso.no_panel_url") {
		t.Errorf("the failure does not say the Panel URL is missing:\n%s", body)
	}
}

// TestAnAccountIsOnlyCreatedWhenTheOperatorAskedForIt: with auto-create off,
// somebody the provider knows and this panel does not is refused.
func TestAnAllowedDomainIsRequiredWhenOneIsSet(t *testing.T) {
	config := auth.OIDCConfig{AllowedDomains: []string{"example.com", " Example.ORG "}}
	for email, want := range map[string]bool{
		"a@example.com":      true,
		"a@EXAMPLE.com":      true,
		"a@example.org":      true,
		"a@example.com.evil": false,
		"a@elsewhere.test":   false,
		"nobody":             false,
	} {
		if got := config.Allows(email); got != want {
			t.Errorf("Allows(%q) = %v, want %v", email, got, want)
		}
	}
	// With no list, the provider decides.
	if !(auth.OIDCConfig{}).Allows("anyone@anywhere.test") {
		t.Error("an empty domain list refused an address")
	}
}

// setSSO configures a provider without going through the settings API, which
// needs an administrator and is not what these tests are about.
func (h *harness) setSSO(t *testing.T, issuer, clientID, secret string) {
	t.Helper()
	sealed, err := h.keyring.Seal([]byte(secret), settings.Context(settings.KeySSOClientSecret))
	if err != nil {
		t.Fatalf("seal the client secret: %v", err)
	}
	for key, value := range map[string]string{
		settings.KeySSOIssuer:   issuer,
		settings.KeySSOClientID: clientID,
	} {
		if err := h.db.SetSetting(t.Context(), key, value, false, "test"); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	if err := h.db.SetSetting(t.Context(), settings.KeySSOClientSecret, sealed, true, "test"); err != nil {
		t.Fatalf("set the client secret: %v", err)
	}
}

var _ = url.QueryEscape
var _ = store.User{}
