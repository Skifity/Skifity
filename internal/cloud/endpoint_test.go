package cloud

import "testing"

// The panel's Hetzner client is the real address with the guarded client.
// There is no other way to build one outside a test, and no setting or
// request field reaches either value.
func TestThePanelTalksToHetznerAndNowhereElse(t *testing.T) {
	provider, err := Open(KindHetzner, "not-a-real-token")
	if err != nil {
		t.Fatal(err)
	}
	h, ok := provider.(*Hetzner)
	if !ok {
		t.Fatalf("Open answered %T", provider)
	}
	if h.endpoint != "https://api.hetzner.cloud/v1" {
		t.Errorf("the endpoint is %q", h.endpoint)
	}
	if h.client != hetznerClient {
		t.Error("the client is not the guarded one")
	}
	if _, err := Open("vultr", "x"); err == nil {
		t.Error("a provider this build does not have was opened")
	}
}

// The same for DigitalOcean: its real address, through the guarded client.
func TestThePanelTalksToDigitalOceanAndNowhereElse(t *testing.T) {
	provider, err := Open(KindDigitalOcean, "not-a-real-token")
	if err != nil {
		t.Fatal(err)
	}
	d, ok := provider.(*DigitalOcean)
	if !ok {
		t.Fatalf("Open answered %T", provider)
	}
	if d.endpoint != "https://api.digitalocean.com/v2" {
		t.Errorf("the endpoint is %q", d.endpoint)
	}
	if d.client != digitalOceanClient {
		t.Error("the client is not the guarded one")
	}
}
