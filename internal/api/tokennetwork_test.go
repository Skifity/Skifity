package api

import (
	"net/http"
	"strings"
	"testing"

	"skifity/internal/settings"
)

// A token is refused from outside the networks it is limited to, valid or
// not; a browser session is not limited, and an empty list limits nothing.
func TestAPITokensAreLimitedToTheNetworksListed(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	path := "/api/teams/" + acme.team.ID

	if status, body := h.do(acme, http.MethodGet, path, nil); status != http.StatusOK {
		t.Fatalf("with no list a token answered %d: %s", status, body)
	}

	// The test server is 127.0.0.1, which this list does not include.
	if err := h.db.SetSetting(t.Context(), settings.KeyAPIAllowedNetworks, "10.0.0.0/8\n203.0.113.7", false, "test"); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(acme, http.MethodGet, path, nil)
	if status != http.StatusForbidden {
		t.Fatalf("a token from outside the list answered %d: %s", status, body)
	}
	if want := `"code":"auth.token_network"`; !strings.Contains(body, want) {
		t.Errorf("the refusal does not say why: %s", body)
	}

	// A browser session signed in with a password is not limited.
	h.person(t, "ops@example.test", "correct horse battery")
	browser := h.signIn(t, "ops@example.test", "correct horse battery")
	if status, body := h.send(t, browser, http.MethodGet, "/api/me", "", nil); status != http.StatusOK {
		t.Errorf("a browser session was limited too: %d %s", status, body)
	}

	if err := h.db.SetSetting(t.Context(), settings.KeyAPIAllowedNetworks, "127.0.0.0/8", false, "test"); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(acme, http.MethodGet, path, nil); status != http.StatusOK {
		t.Errorf("a token from a network on the list answered %d: %s", status, body)
	}
}

func TestTheNetworkListIsReadAsAddressesAndNetworks(t *testing.T) {
	list := settings.Networks("10.0.0.0/8, 203.0.113.7\n2001:db8::/32\n\n")
	if len(list) != 3 {
		t.Fatalf("read %v", list)
	}
	for ip, want := range map[string]bool{
		"10.1.2.3": true, "203.0.113.7": true, "203.0.113.8": false, "2001:db8::1": true, "not-an-ip": false,
	} {
		if got := settings.InNetworks(ip, list); got != want {
			t.Errorf("%s in the list: %v, want %v", ip, got, want)
		}
	}
}
