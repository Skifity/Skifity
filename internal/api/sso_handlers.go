package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"skifity/internal/auth"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Single sign-on, the two halves of it.
//
// Both are open routes, like signing in with a password: somebody who has no
// account yet has no credentials to present. What makes them safe is what comes
// back — a state this panel issued, in a cookie this browser was given, used
// once, and an ID token whose signature, issuer, audience, expiry and nonce all
// check out. internal/auth/oidc.go does the second half; this file does the
// first and the plumbing around it.

// ssoState is what the callback needs and the browser carries between the two
// halves. It rides in one short-lived HttpOnly cookie rather than in the
// panel's database, because it belongs to one browser and one attempt, and a
// table of them is a table somebody has to expire.
type ssoState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Next     string `json:"next,omitempty"`
	// Link is the account a signed-in person asked to link this provider
	// identity to, from their account page. Empty for an ordinary sign-in.
	Link string `json:"link,omitempty"`
}

// ssoConfig reads what an operator configured, decrypting the client secret.
func (s *Server) ssoConfig(r *http.Request) (auth.OIDCConfig, error) {
	read := func(key string) (string, error) {
		value, encrypted, err := s.db.GetSetting(r.Context(), key)
		if err != nil || value == "" || !encrypted {
			return value, err
		}
		plaintext, err := s.keyring.Open(value, settings.Context(key))
		if err != nil {
			return "", err
		}
		return string(plaintext), nil
	}
	var config auth.OIDCConfig
	for target, key := range map[*string]string{
		&config.Issuer:       settings.KeySSOIssuer,
		&config.ClientID:     settings.KeySSOClientID,
		&config.ClientSecret: settings.KeySSOClientSecret,
		&config.ButtonLabel:  settings.KeySSOButtonLabel,
	} {
		value, err := read(key)
		if err != nil {
			return auth.OIDCConfig{}, err
		}
		*target = strings.TrimSpace(value)
	}
	domains, err := read(settings.KeySSODomains)
	if err != nil {
		return auth.OIDCConfig{}, err
	}
	for _, d := range strings.Split(domains, ",") {
		if d = strings.TrimSpace(d); d != "" {
			config.AllowedDomains = append(config.AllowedDomains, d)
		}
	}
	autoCreate, err := read(settings.KeySSOAutoCreate)
	if err != nil {
		return auth.OIDCConfig{}, err
	}
	config.AutoCreate = autoCreate == "true"
	return config, nil
}

// ssoRedirectURL is the address the provider sends the browser back to. It has
// to match the one registered with the provider exactly, so it is built from
// the panel's configured URL rather than from the request's Host — which a
// caller controls, and which would otherwise let somebody register their own
// hostname as a redirect target.
func (s *Server) ssoRedirectURL(r *http.Request) (string, error) {
	configured, _, err := s.db.GetSetting(r.Context(), settings.KeyPanelURL)
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(strings.TrimSpace(configured), "/")
	if base == "" {
		return "", errdoc.New("sso.no_panel_url", "This panel does not know its own address").
			WithCause("Single sign-on sends people back to a fixed address, and the Panel URL setting is empty.").
			WithImpact("Sign-on cannot start.").
			WithFix("Set the Panel URL under Settings, then register that address followed by /api/auth/sso/callback with your provider.").
			WithStatus(http.StatusBadRequest)
	}
	return base + "/api/auth/sso/callback", nil
}

func (s *Server) handleSSOStart(w http.ResponseWriter, r *http.Request) {
	config, err := s.ssoConfig(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !config.Enabled() {
		writeError(w, r, errdoc.New("sso.not_configured", "Single sign-on is not set up").
			WithCause("No identity provider is configured on this panel.").
			WithImpact("Sign in with an email address and password instead.").
			WithFix("An administrator can configure a provider under Settings, Sign-in.").
			WithStatus(http.StatusNotFound))
		return
	}
	redirectURL, err := s.ssoRedirectURL(r)
	if err != nil {
		writeError(w, r, err)
		return
	}

	authURL, state, nonce, verifier, err := auth.NewOIDC(config).Start(r.Context(), redirectURL)
	if err != nil {
		writeError(w, r, errdoc.New("sso.provider_unreachable", "The identity provider could not be reached").
			WithCause("%s", err.Error()).
			WithImpact("Nobody can sign in with single sign-on until this is fixed.").
			WithFix("Check the issuer URL under Settings, Sign-in, and that this panel can reach it.").
			WithStatus(http.StatusBadGateway).Retry())
		return
	}

	encoded, err := json.Marshal(ssoState{State: state, Nonce: nonce, Verifier: verifier, Next: safeNext(r)})
	if err != nil {
		writeError(w, r, err)
		return
	}
	http.SetCookie(w, s.auth.SSOStateCookie(base64URL(encoded)))
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	// Whatever happens, the browser is going somewhere: this is a redirect
	// target, not an API call, and a JSON error body in the address bar helps
	// nobody. Failures land back on the sign-in page with a code in the query.
	fail := func(reason string) {
		http.SetCookie(w, s.auth.ClearCookie(auth.SSOStateCookieName))
		http.Redirect(w, r, "/?sso_error="+url.QueryEscape(reason), http.StatusFound)
	}

	// Under whichever name this panel sets it: on https that is the __Host-
	// prefixed one, which a page on a sibling subdomain cannot write. Reading
	// the bare name as well would let an app deployed here hand the browser a
	// state, a nonce and a verifier of the attacker's choosing, and sign the
	// victim into the attacker's account.
	value := s.auth.ReadCookie(r, auth.SSOStateCookieName)
	if value == "" {
		fail("expired")
		return
	}
	raw, err := decodeBase64URL(value)
	if err != nil {
		fail("expired")
		return
	}
	var pending ssoState
	if err := json.Unmarshal(raw, &pending); err != nil {
		fail("expired")
		return
	}
	// The cookie is spent whether or not the rest works, so a state cannot be
	// replayed by resending the callback.
	http.SetCookie(w, s.auth.ClearCookie(auth.SSOStateCookieName))

	if provider := r.URL.Query().Get("error"); provider != "" {
		s.log.Warn("the identity provider refused a sign-in", "error", provider)
		fail("refused")
		return
	}
	if returned := r.URL.Query().Get("state"); returned == "" || returned != pending.State {
		fail("state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		fail("refused")
		return
	}

	config, err := s.ssoConfig(r)
	if err != nil || !config.Enabled() {
		fail("not_configured")
		return
	}
	redirectURL, err := s.ssoRedirectURL(r)
	if err != nil {
		fail("not_configured")
		return
	}

	identity, err := auth.NewOIDC(config).Exchange(r.Context(), redirectURL, code, pending.Verifier, pending.Nonce)
	if err != nil {
		// The reason is for the operator's log, not for the address bar: it
		// distinguishes "this person is not allowed" from "the provider is
		// misconfigured", and saying which to an anonymous browser is a way to
		// enumerate who has an account.
		s.log.Warn("a single sign-on attempt was refused", "error", err)
		fail("refused")
		return
	}

	if pending.Link != "" {
		s.finishSSOLink(w, r, pending, identity, fail)
		return
	}

	user, reason := s.ssoAccount(r, identity, config)
	if reason != "" {
		fail(reason)
		return
	}
	if user.Disabled {
		fail("disabled")
		return
	}

	result, err := s.auth.IssueSession(r.Context(), user, clientIPFrom(r.Context()), r.UserAgent())
	if err != nil {
		s.log.Error("could not start a session for a single sign-on user", "error", err)
		fail("server")
		return
	}
	http.SetCookie(w, s.auth.SessionCookie(result.Token, result.ExpiresAt))
	http.SetCookie(w, s.auth.CSRFCookie(result.CSRFToken, result.ExpiresAt))
	s.audit(r.WithContext(withUser(r, user)), "", "auth.login_sso", "user", user.ID, user.Email)

	http.Redirect(w, r, defaultString(pending.Next, "/"), http.StatusFound)
}

// ssoAccount works out which account a provider identity signs into.
//
// The provider's issuer and subject are what a returning person is matched on.
// An email address is not an identity: some providers let a user set their
// own, and one that sends no email_verified has not vouched for it. Matching on
// the address alone meant anybody who could make an identity with the owner's
// address could sign in as the owner — the bug behind CVE-2023-3128 in Grafana
// and CVE-2026-86117 in Coolify.
//
// So an address only finds an existing account the first time, and only when
// that is safe:
//
//   - the account has no password, so it only ever existed through single
//     sign-on and there is nothing of anybody else's to take over; or
//   - the provider said, in so many words, that it verified the address.
//
// Anything else is refused, and the person can link the provider from their
// account page after signing in with their password — which proves the
// account is theirs in a way an email claim cannot.
//
// The answer is an account, or a reason for the sign-in page.
func (s *Server) ssoAccount(r *http.Request, identity auth.Identity, config auth.OIDCConfig) (store.User, string) {
	user, err := s.db.UserByIdentity(r.Context(), identity.Issuer, identity.Subject)
	if err == nil {
		return user, ""
	}
	if !errors.Is(err, store.ErrNotFound) {
		s.log.Error("could not look up a single sign-on identity", "error", err)
		return store.User{}, "server"
	}

	user, err = s.db.GetUserByEmail(r.Context(), identity.Email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if !config.AutoCreate {
			s.log.Warn("single sign-on refused somebody with no account here", "email", identity.Email)
			return store.User{}, "no_account"
		}
		// No password hash: this account exists only through the provider, and
		// Login refuses an empty hash rather than treating it as a match.
		created := store.User{Email: identity.Email, Name: identity.Name}
		if err := s.db.CreateUser(r.Context(), &created); err != nil {
			s.log.Error("could not create an account for a single sign-on user", "error", err)
			return store.User{}, "server"
		}
		user = created
	case err != nil:
		s.log.Error("could not look up a single sign-on user", "error", err)
		return store.User{}, "server"
	case user.PasswordHash != "" && !identity.EmailVerified:
		s.log.Warn("single sign-on found an account with a password by an address the provider did not verify; "+
			"the person can link the provider from their account page after signing in with their password",
			"email", identity.Email, "issuer", identity.Issuer)
		return store.User{}, "link_required"
	}

	if err := s.db.LinkIdentity(r.Context(), user.ID, identity.Issuer, identity.Subject, identity.Email); err != nil {
		s.log.Error("could not link a single sign-on identity", "error", err)
		return store.User{}, "server"
	}
	s.audit(r.WithContext(withUser(r, user)), "", "auth.sso_linked", "user", user.ID, identity.Email)
	return user, ""
}

// finishSSOLink links a provider identity to the account that asked for it
// from the account page.
//
// The browser has to still be signed in as that account. The request that
// started this was behind requireRecentAuth, so somebody typed the password a
// moment ago; this checks the same person came back.
func (s *Server) finishSSOLink(w http.ResponseWriter, r *http.Request, pending ssoState, identity auth.Identity, fail func(string)) {
	token := s.auth.ReadCookie(r, auth.SessionCookieName)
	user, _, err := s.auth.Authenticate(r.Context(), token)
	if token == "" || err != nil || user.ID != pending.Link {
		fail("state")
		return
	}
	switch err := s.db.LinkIdentity(r.Context(), user.ID, identity.Issuer, identity.Subject, identity.Email); {
	case errors.Is(err, store.ErrIdentityTaken):
		http.Redirect(w, r, "/account?sso=taken", http.StatusFound)
		return
	case err != nil:
		s.log.Error("could not link a single sign-on identity", "error", err)
		http.Redirect(w, r, "/account?sso=failed", http.StatusFound)
		return
	}
	s.audit(r.WithContext(withUser(r, user)), "", "auth.sso_linked", "user", user.ID, identity.Email)
	http.Redirect(w, r, "/account?sso=linked", http.StatusFound)
}

// ssoLinkView is what the account page shows about single sign-on.
type ssoLinkView struct {
	// Available is whether a provider is configured at all.
	Available bool   `json:"available"`
	Label     string `json:"label,omitempty"`
	// Linked are the provider accounts this account can sign in with.
	Linked []store.Identity `json:"linked"`
	// CanUnlink is false for an account with no password, which would have no
	// way in left.
	CanUnlink bool `json:"can_unlink"`
}

func (s *Server) handleGetSSOLink(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	linked, err := s.db.IdentitiesForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := s.ssoStatus(r)
	writeJSON(w, http.StatusOK, ssoLinkView{
		Available: status.Enabled, Label: status.Label, Linked: linked,
		CanUnlink: user.PasswordHash != "",
	})
}

// handleStartSSOLink begins linking the configured provider to the signed-in
// account. It answers with the provider's address rather than redirecting,
// because it is a POST from the account page behind requireRecentAuth.
func (s *Server) handleStartSSOLink(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	config, err := s.ssoConfig(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !config.Enabled() {
		writeError(w, r, errdoc.New("sso.not_configured", "Single sign-on is not set up").
			WithCause("No identity provider is configured on this panel.").
			WithImpact("There is nothing to link.").
			WithFix("An administrator can configure a provider under Settings, Sign-in.").
			WithStatus(http.StatusNotFound))
		return
	}
	redirectURL, err := s.ssoRedirectURL(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	authURL, state, nonce, verifier, err := auth.NewOIDC(config).Start(r.Context(), redirectURL)
	if err != nil {
		writeError(w, r, errdoc.New("sso.provider_unreachable", "The identity provider could not be reached").
			WithCause("%s", err.Error()).
			WithImpact("Nothing was linked.").
			WithFix("Check the issuer URL under Settings, Sign-in, and that this panel can reach it.").
			WithStatus(http.StatusBadGateway).Retry())
		return
	}
	encoded, err := json.Marshal(ssoState{State: state, Nonce: nonce, Verifier: verifier, Link: user.ID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	http.SetCookie(w, s.auth.SSOStateCookie(base64URL(encoded)))
	writeJSON(w, http.StatusOK, map[string]string{"url": authURL})
}

// handleUnlinkSSO removes the provider identities linked to the signed-in
// account. Refused for an account with no password, which would be left with
// no way to sign in.
func (s *Server) handleUnlinkSSO(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	if user.PasswordHash == "" {
		writeError(w, r, errdoc.New("sso.last_way_in", "Single sign-on is this account's only way in").
			WithCause("This account has no password, so unlinking the provider would leave it with no way to sign in.").
			WithImpact("Nothing was changed.").
			WithFix("Set a password on this page first, then unlink.").
			WithStatus(http.StatusConflict))
		return
	}
	if err := s.db.UnlinkIdentities(r.Context(), user.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "auth.sso_unlinked", "user", user.ID, user.Email)
	w.WriteHeader(http.StatusNoContent)
}

// safeNext is where to land after signing in, when the sign-in page asked for
// somewhere. Only a path on this panel: an absolute URL here is an open
// redirect, which is how a phishing page borrows a real domain.
func safeNext(r *http.Request) string {
	next := r.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return ""
	}
	return next
}

// ssoStatus is what the sign-in page needs to decide whether to show a button.
type ssoStatus struct {
	Enabled bool   `json:"enabled"`
	Label   string `json:"label,omitempty"`
}

func (s *Server) ssoStatus(r *http.Request) ssoStatus {
	config, err := s.ssoConfig(r)
	if err != nil || !config.Enabled() {
		return ssoStatus{}
	}
	return ssoStatus{Enabled: true, Label: config.Label()}
}

// base64URL and decodeBase64URL move the pending state through a cookie, which
// may not carry a raw JSON body.
func base64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decodeBase64URL(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
