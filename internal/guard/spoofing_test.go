package guard

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"skifity/internal/edgerules"
)

// blockEverything protects one hostname and refuses every request to it, so
// any request that comes back allowed got past the firewall rather than
// through it.
func blockEverything(host string) Config {
	return Config{Sets: map[string]Protected{
		host: {RuleSet: edgerules.RuleSet{
			Default: edgerules.ActionAllow,
			Rules: []edgerules.Rule{{
				ID: "r1", Name: "everybody", Action: edgerules.ActionBlock, Enabled: true,
			}},
		}},
	}}
}

func guardFor(t *testing.T, host string) *Guard {
	t.Helper()
	return newGuard(t, blockEverything(host))
}

// The whole firewall could be stepped around with one header: name a hostname
// nobody protects, and there are no rules left to fail.
func TestAForwardedHostFromAnUntrustedPeerIsNotBelieved(t *testing.T) {
	g := guardFor(t, "shop.example.test")

	r := httptest.NewRequest(http.MethodGet, "http://shop.example.test/", nil)
	r.RemoteAddr = "203.0.113.9:40000" // not one of ours
	r.Host = "shop.example.test"
	r.Header.Set("X-Forwarded-Host", "nobody-protects-this.example.test")

	decision, judged, decided := g.Decide(r)
	if !decided {
		t.Fatal("the guard refused to decide")
	}
	if judged.Host != "shop.example.test" {
		t.Errorf("the rules were applied to %q, the hostname the client asked for", judged.Host)
	}
	if decision.Action != edgerules.ActionBlock {
		t.Error("a header from an untrusted peer chose which rules applied, and got past all of them")
	}
}

// A second header line hides the proxy's value behind the client's, because
// Get answers with the first.
func TestASecondForwardedHostLineDoesNotWin(t *testing.T) {
	g := guardFor(t, "shop.example.test")

	r := httptest.NewRequest(http.MethodGet, "http://shop.example.test/", nil)
	r.RemoteAddr = "10.42.0.7:40000" // the ingress, which we trust
	r.Host = "ingress.internal"
	// The client's line arrives first; the proxy appends its own.
	r.Header.Add("X-Forwarded-Host", "nobody-protects-this.example.test")
	r.Header.Add("X-Forwarded-Host", "shop.example.test")

	decision, judged, _ := g.Decide(r)
	if judged.Host != "shop.example.test" {
		t.Errorf("the rules were applied to %q rather than what the proxy said", judged.Host)
	}
	if decision.Action != edgerules.ActionBlock {
		t.Error("the client's own header line won over the proxy's")
	}
}

// The same shape, for the address every rule is judged by.
func TestASecondForwardedForLineDoesNotChooseTheAddress(t *testing.T) {
	trust := edgerules.Trust{Proxies: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}}
	headers := http.Header{}
	headers.Add("X-Forwarded-For", "198.51.100.1") // the client's own writing
	headers.Add("X-Forwarded-For", "203.0.113.5")  // what the proxy really saw

	got := edgerules.ClientIP(netip.MustParseAddr("10.42.0.7"), headers, trust)
	if got.String() != "203.0.113.5" {
		t.Errorf("judged as %s; the address the proxy recorded is 203.0.113.5", got)
	}
}

// The ordinary path has to keep working: one header, from the ingress.
func TestTheOrdinaryForwardedRequestIsStillJudgedOnWhatTheProxySaid(t *testing.T) {
	g := guardFor(t, "shop.example.test")

	r := httptest.NewRequest(http.MethodGet, "http://ingress.internal/authorize", nil)
	r.RemoteAddr = "10.42.0.7:40000"
	r.Host = "ingress.internal"
	r.Header.Set("X-Forwarded-Host", "shop.example.test")
	r.Header.Set("X-Forwarded-Uri", "/checkout?next=/admin")
	r.Header.Set("X-Forwarded-Method", http.MethodPost)

	decision, judged, decided := g.Decide(r)
	if !decided || decision.Action != edgerules.ActionBlock {
		t.Fatalf("the protected host was not matched: %+v", judged)
	}
	if judged.Path != "/checkout" {
		t.Errorf("the path was %q; the query string is not part of it", judged.Path)
	}
	if judged.Method != http.MethodPost {
		t.Errorf("the method was %q", judged.Method)
	}
}
