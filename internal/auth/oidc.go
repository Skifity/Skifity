package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"skifity/internal/crypto"
	"skifity/internal/netguard"
)

// Single sign-on, over OpenID Connect.
//
// The authorization code flow with PKCE, against whatever the operator points
// it at: Okta, Entra, Authentik, Keycloak, Google, Zitadel. SAML is not here —
// it is a second protocol, a second parser and a second class of signature bug,
// and every identity provider a self-hosted panel is likely to meet speaks
// OIDC.
//
// Three things about this are not negotiable, because each is an auth bypass
// when it is wrong:
//
//   - The ID token's signature is verified against the provider's JWKS, and so
//     are its issuer, audience and expiry. That is `oidc.IDTokenVerifier`, not
//     code written here: a hand-rolled JWT check is the most common way to end
//     up accepting `alg: none` or a token issued for somebody else's client.
//   - The nonce is generated per sign-in and must come back inside the ID
//     token, which is what stops one being replayed.
//   - The state is generated per sign-in, kept in a cookie the browser sends
//     back, and used once. Without it the callback accepts a code an attacker
//     obtained elsewhere.
//
// The issuer is a setting, which makes it the fourth address an administrator
// types that the panel's own process then connects to — so, like the other
// three, it dials through internal/netguard.

// OIDCConfig is what an operator has configured.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	ButtonLabel  string
	// AllowedDomains restricts which email domains may sign in. Empty means
	// any domain the provider vouches for, which is the right default only
	// when the provider is not a public one.
	AllowedDomains []string
	// AutoCreate makes a first sign-in create an account. With it off, somebody
	// the provider knows and this panel does not is refused, which is what an
	// operator who invites people by hand wants.
	AutoCreate bool
}

// Enabled reports whether enough is configured to offer the button at all.
func (c OIDCConfig) Enabled() bool {
	return c.Issuer != "" && c.ClientID != "" && c.ClientSecret != ""
}

// Label is what the sign-in button says.
func (c OIDCConfig) Label() string {
	if c.ButtonLabel != "" {
		return c.ButtonLabel
	}
	return "Sign in with single sign-on"
}

// Allows reports whether an email address is in an allowed domain.
func (c OIDCConfig) Allows(email string) bool {
	if len(c.AllowedDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(strings.ToLower(email), "@")
	if !ok {
		return false
	}
	for _, allowed := range c.AllowedDomains {
		if domain == strings.ToLower(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

// ErrSSONotConfigured is returned when a sign-in is attempted with no provider.
var ErrSSONotConfigured = errors.New("single sign-on is not configured")

// Identity is what the provider said about the person signing in.
type Identity struct {
	Subject string
	Email   string
	Name    string
}

// OIDC talks to one provider. It is built per request from the settings, with
// the discovery document cached: discovery is one HTTP round trip and the
// document changes about as often as the provider does.
type OIDC struct {
	config OIDCConfig

	mu        sync.Mutex
	provider  *oidc.Provider
	cachedFor string
	cachedAt  time.Time
}

// discoveryTTL bounds how long a discovery document is reused. Long enough that
// signing in is one round trip rather than two, short enough that a provider
// rotating its endpoints is picked up the same day.
const discoveryTTL = time.Hour

// NewOIDC builds a client for the configured provider.
func NewOIDC(config OIDCConfig) *OIDC { return &OIDC{config: config} }

// Config returns what this was built from.
func (o *OIDC) Config() OIDCConfig { return o.config }

// client is the HTTP client every call to the provider goes through: the
// discovery document, the JWKS, and the token exchange. netguard refuses an
// issuer that resolves to the metadata service or to loopback, on the address
// it is about to dial rather than on the hostname.
var ssoClient = netguard.Client(20 * time.Second)

func (o *OIDC) provider2(ctx context.Context) (*oidc.Provider, error) {
	if !o.config.Enabled() {
		return nil, ErrSSONotConfigured
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil && o.cachedFor == o.config.Issuer && time.Since(o.cachedAt) < discoveryTTL {
		return o.provider, nil
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, ssoClient), o.config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("read the provider's configuration at %s: %w", o.config.Issuer, err)
	}
	o.provider, o.cachedFor, o.cachedAt = provider, o.config.Issuer, time.Now()
	return provider, nil
}

func (o *OIDC) oauth(provider *oidc.Provider, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     o.config.ClientID,
		ClientSecret: o.config.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
}

// Start returns the URL to send the browser to, and the state and nonce that
// have to come back with it.
func (o *OIDC) Start(ctx context.Context, redirectURL string) (authURL, state, nonce, verifier string, err error) {
	provider, err := o.provider2(ctx)
	if err != nil {
		return "", "", "", "", err
	}
	if state, err = RandomState(); err != nil {
		return "", "", "", "", err
	}
	if nonce, err = RandomState(); err != nil {
		return "", "", "", "", err
	}
	verifier = oauth2.GenerateVerifier()
	authURL = o.oauth(provider, redirectURL).AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	return authURL, state, nonce, verifier, nil
}

// Exchange turns the code the provider sent back into a verified identity.
//
// nonce is the one issued at Start. A token whose nonce does not match it is
// refused: that is the difference between this sign-in and a replay of one.
func (o *OIDC) Exchange(ctx context.Context, redirectURL, code, verifier, nonce string) (Identity, error) {
	provider, err := o.provider2(ctx)
	if err != nil {
		return Identity{}, err
	}
	ctx = oidc.ClientContext(ctx, ssoClient)
	token, err := o.oauth(provider, redirectURL).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("exchange the sign-in code: %w", err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, errors.New("the provider returned no ID token, so there is nothing to verify")
	}

	// Signature, issuer, audience and expiry, by the library rather than by
	// anything written here.
	idToken, err := provider.Verifier(&oidc.Config{ClientID: o.config.ClientID}).Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("verify the ID token: %w", err)
	}
	if idToken.Nonce != nonce {
		return Identity{}, errors.New("the ID token is for a different sign-in attempt")
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
		PreferredName string `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("read the ID token's claims: %w", err)
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		return Identity{}, errors.New("the provider did not send an email address, which is how an account is matched here")
	}
	// A provider that says it did not verify the address is not vouching for
	// it, and an unverified address is somebody else's account.
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		return Identity{}, fmt.Errorf("the provider has not verified %s", email)
	}
	if !o.config.Allows(email) {
		return Identity{}, fmt.Errorf("%s is not in a domain this panel accepts", email)
	}

	name := claims.Name
	if name == "" {
		name = claims.PreferredName
	}
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	return Identity{Subject: idToken.Subject, Email: email, Name: name}, nil
}

// RandomState is a value that has to come back unchanged.
func RandomState() (string, error) { return crypto.RandomToken(32) }

// SSOStateCookieName carries the state, nonce and PKCE verifier between the
// two halves of a sign-in. It is short-lived, HttpOnly and SameSite=Lax —
// Lax rather than Strict because the provider redirects the browser back here
// from another site, and Strict would not send it.
const SSOStateCookieName = "skifity_sso"

// SSOStateCookie holds the three values the callback needs.
func (s *Service) SSOStateCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     s.CookieName(SSOStateCookieName),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((10 * time.Minute).Seconds()),
	}
}
