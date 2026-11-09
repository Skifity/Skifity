package kube

import "testing"

// What cert-manager and Let's Encrypt say, as they say it, and the fix each
// one gets.
func TestACertificateFailureGetsTheFixForItsCause(t *testing.T) {
	for _, tc := range []struct {
		name, reason, want string
	}{
		{"rate limited", `The certificate request has failed to complete and will be retried: Failed to wait for ` +
			`order resource "web-tls-1-123" to become ready: order is in "errored" state: Failed to create ` +
			`Order: 429 urn:ietf:params:acme:error:rateLimited: Error creating new order :: too many ` +
			`certificates (5) already issued for this exact set of domains in the last 168h0m0s`, "rate_limited"},
		{"failed authorizations", "429 urn:ietf:params:acme:error:rateLimited: too many failed authorizations recently",
			"rate_limited"},
		{"caa", `Error accepting authorization: acme: authorization error for shop.example.com: 403 ` +
			`urn:ietf:params:acme:error:caa: CAA record for shop.example.com prevents issuance`, "caa"},
		{"no record", `acme: authorization error for shop.example.com: 400 urn:ietf:params:acme:error:dns: ` +
			`DNS problem: NXDOMAIN looking up A for shop.example.com`, "dns"},
		{"self check, no record", `Waiting for HTTP-01 challenge propagation: failed to perform self check GET ` +
			`request 'http://shop.example.com/.well-known/acme-challenge/x': Get "http://shop.example.com/` +
			`.well-known/acme-challenge/x": dial tcp: lookup shop.example.com on 10.43.0.10:53: no such host`, "dns"},
		{"port 80 closed", `acme: authorization error for shop.example.com: 400 urn:ietf:params:acme:error:connection: ` +
			`203.0.113.7: Fetching http://shop.example.com/.well-known/acme-challenge/x: Timeout during connect ` +
			`(likely firewall problem)`, "http01"},
		{"proxy answers", `Waiting for HTTP-01 challenge propagation: wrong status code '403', expected '200'`, "http01"},
		// What the Ready condition says while the challenge is stuck, which is
		// most of the time: the commonest cause's fix.
		{"still issuing", "Issuing certificate as Secret does not exist", "dns"},
		{"nothing said", "", "dns"},
	} {
		if got := ExplainCertificate(tc.reason); got != certificateFixes[tc.want] {
			t.Errorf("%s: got %q, want the %s fix", tc.name, got, tc.want)
		}
	}
}
