package auth

import "testing"

// What a provider's claims say about the person signing in. The one that
// matters is email_verified: absent is not true. A provider that sends nothing
// has not vouched for the address, and treating silence as a yes is what let
// an address alone find somebody else's account.

func verified(v bool) *bool { return &v }

func TestAnAbsentEmailVerifiedIsNotAVerifiedAddress(t *testing.T) {
	o := NewOIDC(OIDCConfig{})
	identity, err := o.identityFrom("https://idp.example.test", "sub-1", idClaims{Email: "Owner@Example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.EmailVerified {
		t.Fatal("a token with no email_verified claim was read as a verified address")
	}
	if identity.Issuer != "https://idp.example.test" || identity.Subject != "sub-1" {
		t.Errorf("the identity is %q / %q", identity.Issuer, identity.Subject)
	}
	if identity.Email != "owner@example.test" {
		t.Errorf("the address was read as %q", identity.Email)
	}
}

func TestAVerifiedAddressIsReadAsVerified(t *testing.T) {
	o := NewOIDC(OIDCConfig{})
	identity, err := o.identityFrom("https://idp.example.test", "sub-1",
		idClaims{Email: "owner@example.test", EmailVerified: verified(true)})
	if err != nil || !identity.EmailVerified {
		t.Fatalf("a verified address came back as %+v, %v", identity, err)
	}
}

// A provider that says it did not verify the address is refused outright.
func TestAnAddressTheProviderDidNotVerifyIsRefused(t *testing.T) {
	o := NewOIDC(OIDCConfig{})
	if _, err := o.identityFrom("https://idp.example.test", "sub-1",
		idClaims{Email: "owner@example.test", EmailVerified: verified(false)}); err == nil {
		t.Fatal("an address the provider said it had not verified was accepted")
	}
}
