package auth

import "testing"

// TestPanelRelyingParty: which addresses a browser would offer a passkey at,
// and who the relying party is then. The default install's address is the
// second list's third entry.
func TestPanelRelyingParty(t *testing.T) {
	for address, want := range map[string]RelyingParty{
		"https://panel.example.test":        {ID: "panel.example.test", Origin: "https://panel.example.test"},
		"https://Panel.Example.test/":       {ID: "panel.example.test", Origin: "https://panel.example.test"},
		"https://panel.example.test:443":    {ID: "panel.example.test", Origin: "https://panel.example.test"},
		"https://panel.example.test:8443/x": {ID: "panel.example.test", Origin: "https://panel.example.test:8443"},
		"http://localhost:5173":             {ID: "localhost", Origin: "http://localhost:5173"},
	} {
		got, ok := PanelRelyingParty(address, "Skifity")
		if !ok || got.ID != want.ID || got.Origin != want.Origin || got.Name != "Skifity" {
			t.Errorf("%q gave %+v, %v; want %+v", address, got, ok, want)
		}
	}
	for _, address := range []string{
		"", "panel.example.test", "http://panel.example.test", "http://203-0-113-10.sslip.io",
		"https://203.0.113.10", "https://[2001:db8::1]", "http://127.0.0.1:8080", "ftp://panel.example.test",
	} {
		if got, ok := PanelRelyingParty(address, "Skifity"); ok {
			t.Errorf("%q gave %+v; a browser would not offer a passkey there", address, got)
		}
	}
}

// TestAnAAGUIDSurvivesTheDatabase: the authenticator's model id is written as
// a UUID and read back into the sixteen bytes the library compares.
func TestAnAAGUIDSurvivesTheDatabase(t *testing.T) {
	raw := []byte{0xad, 0xce, 0x00, 0x02, 0x35, 0xbc, 0xc6, 0x0a, 0x64, 0x8b, 0x0b, 0x25, 0xf1, 0xf0, 0x55, 0x03}
	text := aaguidString(raw)
	if text != "adce0002-35bc-c60a-648b-0b25f1f05503" {
		t.Fatalf("written as %q", text)
	}
	if back := aaguidBytes(text); string(back) != string(raw) {
		t.Fatalf("read back as %x", back)
	}
	if len(aaguidBytes("")) != 16 || aaguidString(nil) != "" {
		t.Fatal("an authenticator with no AAGUID is not sixteen zero bytes")
	}
}
