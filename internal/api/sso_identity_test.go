package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"skifity/internal/auth"
	"skifity/internal/store"
)

// Which account a single sign-on signs into.
//
// An email address is not an identity. Some providers let a user type their
// own, and one that sends no email_verified has not vouched for it, so an
// address alone finding an account with a password in it is an account
// takeover: whoever can make an identity with the owner's address becomes the
// owner. That is the bug behind CVE-2023-3128 in Grafana and CVE-2026-86117 in
// Coolify, and it was this panel's behaviour too.

const issuer = "https://idp.example.test"

func ssoRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback", nil)
}

// The attack: an identity at the provider carrying the owner's address, with
// nothing to say the provider checked it.
func TestAnUnverifiedAddressDoesNotSignIntoAnAccountWithAPassword(t *testing.T) {
	h := newHarness(t)
	owner := h.person(t, "owner@example.test", "a long enough password")

	_, reason := h.api.ssoAccount(ssoRequest(), auth.Identity{
		Issuer: issuer, Subject: "attacker", Email: "owner@example.test",
	}, auth.OIDCConfig{})
	if reason != "link_required" {
		t.Fatalf("an unverified address signed into the owner's account (reason %q)", reason)
	}
	if linked, _ := h.db.IdentitiesForUser(t.Context(), owner.ID); len(linked) != 0 {
		t.Fatalf("the attacker's identity was linked to the owner: %+v", linked)
	}
}

// A provider that says it verified the address is trusted the first time, and
// from then on the identity, not the address, is what is matched.
func TestAVerifiedAddressLinksTheAccountOnce(t *testing.T) {
	h := newHarness(t)
	owner := h.person(t, "owner@example.test", "a long enough password")

	user, reason := h.api.ssoAccount(ssoRequest(), auth.Identity{
		Issuer: issuer, Subject: "owner-at-idp", Email: "owner@example.test", EmailVerified: true,
	}, auth.OIDCConfig{})
	if reason != "" || user.ID != owner.ID {
		t.Fatalf("a verified address did not sign into its account: %q", reason)
	}

	// The same person, after changing their address at the provider.
	user, reason = h.api.ssoAccount(ssoRequest(), auth.Identity{
		Issuer: issuer, Subject: "owner-at-idp", Email: "renamed@example.test",
	}, auth.OIDCConfig{})
	if reason != "" || user.ID != owner.ID {
		t.Fatalf("a returning identity was not recognised after an address change: %q", reason)
	}
}

// Once linked, a different identity claiming the same address gets nowhere.
func TestAReturningSignInIsMatchedOnTheIdentityNotTheAddress(t *testing.T) {
	h := newHarness(t)
	owner := h.person(t, "owner@example.test", "a long enough password")
	other := h.person(t, "other@example.test", "a long enough password")
	if err := h.db.LinkIdentity(t.Context(), other.ID, issuer, "other-at-idp", "other@example.test"); err != nil {
		t.Fatal(err)
	}

	// The provider now says this identity has the owner's address. Matching
	// on the address would hand over the owner's account.
	user, reason := h.api.ssoAccount(ssoRequest(), auth.Identity{
		Issuer: issuer, Subject: "other-at-idp", Email: "owner@example.test", EmailVerified: true,
	}, auth.OIDCConfig{})
	if reason != "" || user.ID != other.ID {
		t.Fatalf("an identity signed into %s, want its own account %s (owner is %s)", user.ID, other.ID, owner.ID)
	}
}

// An account made by single sign-on has no password and nothing of anybody
// else's in it, and people who signed in before identities were recorded must
// not be locked out. Their address is enough, once.
func TestAnAccountWithNoPasswordIsLinkedByItsAddress(t *testing.T) {
	h := newHarness(t)
	existing := store.User{Email: "sso@example.test", Name: "sso"}
	if err := h.db.CreateUser(t.Context(), &existing); err != nil {
		t.Fatal(err)
	}

	user, reason := h.api.ssoAccount(ssoRequest(), auth.Identity{
		Issuer: issuer, Subject: "sso-at-idp", Email: "sso@example.test",
	}, auth.OIDCConfig{})
	if reason != "" || user.ID != existing.ID {
		t.Fatalf("an account made by single sign-on could not sign in: %q", reason)
	}
	if linked, _ := h.db.IdentitiesForUser(t.Context(), existing.ID); len(linked) != 1 {
		t.Fatalf("the identity was not recorded: %+v", linked)
	}
}

func TestAnIdentityBelongsToOneAccount(t *testing.T) {
	h := newHarness(t)
	a := h.person(t, "a@example.test", "a long enough password")
	b := h.person(t, "b@example.test", "a long enough password")
	if err := h.db.LinkIdentity(t.Context(), a.ID, issuer, "sub", "a@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := h.db.LinkIdentity(t.Context(), a.ID, issuer, "sub", "a@example.test"); err != nil {
		t.Errorf("linking an identity to the account it is already linked to failed: %v", err)
	}
	if err := h.db.LinkIdentity(t.Context(), b.ID, issuer, "sub", "b@example.test"); !errors.Is(err, store.ErrIdentityTaken) {
		t.Fatalf("an identity was linked to a second account: %v", err)
	}
}

// Linking from the account page: the browser that comes back from the
// provider has to still be signed in as the account that asked.
func TestLinkingFromTheAccountPageNeedsTheSameSignedInAccount(t *testing.T) {
	h := newHarness(t)
	owner := h.person(t, "owner@example.test", "a long enough password")
	h.person(t, "other@example.test", "a long enough password")
	other := h.signIn(t, "other@example.test", "a long enough password")

	identity := auth.Identity{Issuer: issuer, Subject: "owner-at-idp", Email: "whatever@example.test"}
	failed := ""
	fail := func(reason string) { failed = reason }

	// Somebody else's browser finishing the owner's link is refused.
	request := ssoRequest()
	for _, cookie := range other.cookies {
		request.AddCookie(cookie)
	}
	h.api.finishSSOLink(httptest.NewRecorder(), request, ssoState{Link: owner.ID}, identity, fail)
	if failed != "state" {
		t.Fatalf("a link finished in another account's browser was not refused (%q)", failed)
	}
	if linked, _ := h.db.IdentitiesForUser(t.Context(), owner.ID); len(linked) != 0 {
		t.Fatalf("the identity was linked anyway: %+v", linked)
	}

	// The owner's own browser links it, whatever address the provider sends.
	mine := h.signIn(t, "owner@example.test", "a long enough password")
	request = ssoRequest()
	for _, cookie := range mine.cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	failed = ""
	h.api.finishSSOLink(response, request, ssoState{Link: owner.ID}, identity, fail)
	if failed != "" || !strings.Contains(response.Header().Get("Location"), "sso=linked") {
		t.Fatalf("the owner's own link failed (%q, %s)", failed, response.Header().Get("Location"))
	}
	if user, err := h.db.UserByIdentity(t.Context(), issuer, "owner-at-idp"); err != nil || user.ID != owner.ID {
		t.Fatalf("the identity is not the owner's: %v", err)
	}
}

// Linking adds a way into an account, so it asks for the password again, and
// an API token can never do it.
func TestLinkingIsNotSomethingATokenCanDo(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	if status, body := h.do(acme, http.MethodPost, "/api/me/sso/link", nil); status != http.StatusForbidden ||
		!strings.Contains(body, "auth.needs_person") {
		t.Fatalf("an API token started a link: %d\n%s", status, body)
	}
}

// Unlinking the provider from an account with no password would leave it with
// no way in.
func TestTheLastWayInCannotBeUnlinked(t *testing.T) {
	h := newHarness(t)
	existing := store.User{Email: "sso@example.test", Name: "sso"}
	if err := h.db.CreateUser(t.Context(), &existing); err != nil {
		t.Fatal(err)
	}
	if err := h.db.LinkIdentity(t.Context(), existing.ID, issuer, "sub", "sso@example.test"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/me/sso", nil)
	request = request.WithContext(withUser(request, existing))
	response := httptest.NewRecorder()
	h.api.handleUnlinkSSO(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("unlinking the only way in answered %d", response.Code)
	}
	if linked, _ := h.db.IdentitiesForUser(t.Context(), existing.ID); len(linked) != 1 {
		t.Fatal("the identity was removed anyway")
	}
}
