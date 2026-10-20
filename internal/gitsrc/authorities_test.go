package gitsrc

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The panel's requests to Git hosts trust the operator's own authorities
// beside the public ones once they are saved, and the public ones alone again
// once they are cleared.
func TestTheGitClientTrustsTheOperatorsAuthorities(t *testing.T) {
	previous := client.Load()
	t.Cleanup(func() { client.Store(previous) })

	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	authority := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))

	verifies := func() bool {
		transport, ok := client.Load().Transport.(*http.Transport)
		if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
			return false
		}
		_, err := server.Certificate().Verify(x509.VerifyOptions{Roots: transport.TLSClientConfig.RootCAs})
		return err == nil
	}

	if err := TrustAuthorities(authority); err != nil {
		t.Fatalf("TrustAuthorities: %v", err)
	}
	if !verifies() {
		t.Error("a certificate the operator's authority signed is not trusted")
	}
	if err := TrustAuthorities("not a certificate"); err == nil {
		t.Error("a bundle with no certificate in it was taken")
	}
	if !verifies() {
		t.Error("a bundle that could not be read replaced the one that could")
	}
	if err := TrustAuthorities(""); err != nil {
		t.Fatalf("clearing the authorities: %v", err)
	}
	if verifies() {
		t.Error("the operator's authority is still trusted after it was cleared")
	}
}
