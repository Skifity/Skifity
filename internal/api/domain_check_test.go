package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// No test in this package asks real DNS: a domain added anywhere in them is
// looked up here, and here nothing exists.
func init() { systemResolver = fakeDNS{} }

// fakeDNS answers from maps. A name it does not know does not exist, which
// is what DNS says about a record nobody has created yet.
type fakeDNS struct {
	addresses map[string][]string
	cnames    map[string]string
	broken    bool
}

func (f fakeDNS) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if f.broken {
		return nil, &net.DNSError{Err: "i/o timeout", Name: host, IsTimeout: true}
	}
	name := canonicalName(host)
	if target, ok := f.cnames[name]; ok {
		name = target
	}
	values, ok := f.addresses[name]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		out = append(out, net.IPAddr{IP: net.ParseIP(value)})
	}
	return out, nil
}

func (f fakeDNS) LookupCNAME(_ context.Context, host string) (string, error) {
	if f.broken {
		return "", &net.DNSError{Err: "i/o timeout", Name: host, IsTimeout: true}
	}
	name := canonicalName(host)
	if target, ok := f.cnames[name]; ok {
		return target + ".", nil
	}
	if _, ok := f.addresses[name]; ok {
		return name + ".", nil
	}
	return "", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// What DNS says, against where the cluster is, in each of the ways it can go.
func TestADomainsDNSIsComparedWithTheCluster(t *testing.T) {
	cluster := []string{"203.0.113.10", "203.0.113.11"}
	resolver := fakeDNS{
		addresses: map[string][]string{
			"here.example.com":      {"203.0.113.10"},
			"second.example.com":    {"203.0.113.11"},
			"elsewhere.example.com": {"198.51.100.7"},
			// An AAAA left over from the old host next to the right A.
			"partly.example.com": {"203.0.113.10", "2001:db8::1"},
			"lb.example.net":     {"192.0.2.50"},
			"apps.example.com":   {"203.0.113.10"},
		},
		cnames: map[string]string{
			"www.example.com":  "apps.example.com",
			"shop.example.com": "lb.example.net",
		},
	}

	for _, tc := range []struct {
		hostname string
		expected []string
		status   string
		found    string
	}{
		{"here.example.com", cluster, DNSHere, "A 203.0.113.10 here"},
		// Traefik answers on every server, so any of them is here.
		{"second.example.com", cluster, DNSHere, "A 203.0.113.11 here"},
		{"elsewhere.example.com", cluster, DNSElsewhere, "A 198.51.100.7"},
		{"partly.example.com", cluster, DNSPartly, "A 203.0.113.10 here, AAAA 2001:db8::1"},
		{"nothing.example.com", cluster, DNSMissing, ""},
		// A CNAME is followed, and said.
		{"www.example.com", cluster, DNSHere, "CNAME apps.example.com, A 203.0.113.10 here"},
		// Behind a load balancer whose address is a name: a CNAME to it is
		// exactly what was asked for.
		{"shop.example.com", []string{"lb.example.net"}, DNSHere, "CNAME lb.example.net, A 192.0.2.50 here"},
		// The panel not knowing its own address is not the domain's fault,
		// and not a guess either way.
		{"elsewhere.example.com", nil, DNSUnknown, "A 198.51.100.7"},
	} {
		check, err := checkDNS(t.Context(), resolver, tc.hostname, tc.expected)
		if err != nil {
			t.Fatalf("%s: %v", tc.hostname, err)
		}
		if check.Status != tc.status || check.PointsHere != (tc.status == DNSHere) {
			t.Errorf("%s: %s (points here %t), want %s", tc.hostname, check.Status, check.PointsHere, tc.status)
		}
		var found []string
		for _, record := range check.Found {
			line := record.Type + " " + record.Value
			if record.Here {
				line += " here"
			}
			found = append(found, line)
		}
		if got := strings.Join(found, ", "); got != tc.found {
			t.Errorf("%s: found %q, want %q", tc.hostname, got, tc.found)
		}
	}
}

// A resolver that does not answer is not "no record": it is a failure, said
// as one, and it names the hostname so the person can check it themselves.
func TestADNSCheckThatGetsNoAnswerSaysSo(t *testing.T) {
	_, err := checkDNS(t.Context(), fakeDNS{broken: true}, "blog.example.com", []string{"203.0.113.10"})
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "domain.dns_lookup_failed" {
		t.Fatalf("a timed-out lookup answered %v", err)
	}
	if !problem.Retryable || !strings.Contains(problem.Fix, "blog.example.com") {
		t.Errorf("the problem does not offer a retry or name the hostname: %+v", problem)
	}
}

// Adding a domain says what its DNS says, and never refuses the domain for
// it: people add the domain first and the record second. Asking again later
// answers from DNS as it is then.
func TestADomainsDNSIsCheckedWhenAddedAndWhenAsked(t *testing.T) {
	h := newHarness(t)
	h.withCluster(addressedCluster{address: "203.0.113.10"})
	owner := h.newTenant("acme")
	app := h.app(owner, "web")

	dns := &fakeDNS{addresses: map[string][]string{"blog.example.com": {"198.51.100.7"}}}
	h.api.resolver = resolverFunc(func() Resolver { return *dns })

	code, body := h.do(owner, "POST", "/api/apps/"+app.ID+"/domains", map[string]any{"hostname": "blog.example.com"})
	if code != http.StatusCreated {
		t.Fatalf("a domain whose DNS points elsewhere was not added: %d %s", code, body)
	}
	var added struct {
		ID       string    `json:"id"`
		Hostname string    `json:"hostname"`
		DNS      *DNSCheck `json:"dns"`
	}
	if err := json.Unmarshal([]byte(body), &added); err != nil {
		t.Fatal(err)
	}
	if added.Hostname != "blog.example.com" || added.DNS == nil || added.DNS.Status != DNSElsewhere {
		t.Fatalf("adding the domain did not say its DNS points elsewhere: %s", body)
	}
	if len(added.DNS.Expected) == 0 || added.DNS.Expected[0] != "203.0.113.10" {
		t.Errorf("the check does not say where the domain should point: %+v", added.DNS.Expected)
	}

	// The record is changed, and the button asked again.
	dns.addresses["blog.example.com"] = []string{"203.0.113.10"}
	code, body = h.do(owner, "POST", "/api/apps/"+app.ID+"/domains/"+added.ID+"/check", nil)
	if code != http.StatusOK {
		t.Fatalf("check: %d %s", code, body)
	}
	var check DNSCheck
	if err := json.Unmarshal([]byte(body), &check); err != nil {
		t.Fatal(err)
	}
	if check.Status != DNSHere || !check.PointsHere {
		t.Fatalf("a record pointing here now reads as %s", body)
	}

	// A resolver that does not answer fails the check, and not the domain.
	dns.broken = true
	code, body = h.do(owner, "POST", "/api/apps/"+app.ID+"/domains/"+added.ID+"/check", nil)
	if code != http.StatusBadGateway || !strings.Contains(body, "domain.dns_lookup_failed") {
		t.Errorf("a lookup that timed out answered %d %s", code, body)
	}
	code, body = h.do(owner, "POST", "/api/apps/"+app.ID+"/domains", map[string]any{"hostname": "docs.example.com"})
	if code != http.StatusCreated || strings.Contains(body, `"dns"`) {
		t.Errorf("adding a domain while DNS does not answer: %d %s", code, body)
	}
}

// Every ready server's own address counts as here, not only the one the
// Domains tab offers.
func TestAnyReadyServerIsWhereADomainCanPoint(t *testing.T) {
	h := newHarness(t)
	h.withCluster(addressedCluster{address: "203.0.113.10"})
	owner := h.newTenant("acme")
	app := h.app(owner, "web")
	for i, ip := range []string{"203.0.113.10", "203.0.113.20"} {
		server := store.Server{TeamID: owner.team.ID, Name: "node-" + string(rune('a'+i)), Host: ip, ExternalIP: ip,
			SSHPort: 22, SSHUser: "root", Role: "worker", Status: store.ServerReady}
		if err := h.db.CreateServer(t.Context(), &server); err != nil {
			t.Fatal(err)
		}
	}
	h.api.resolver = fakeDNS{addresses: map[string][]string{"blog.example.com": {"203.0.113.20"}}}

	code, body := h.do(owner, "POST", "/api/apps/"+app.ID+"/domains", map[string]any{"hostname": "blog.example.com"})
	if code != http.StatusCreated {
		t.Fatalf("add: %d %s", code, body)
	}
	var added struct {
		DNS *DNSCheck `json:"dns"`
	}
	if err := json.Unmarshal([]byte(body), &added); err != nil || added.DNS == nil {
		t.Fatalf("no check in %s", body)
	}
	if added.DNS.Status != DNSHere {
		t.Errorf("a record naming the second server reads as %s", added.DNS.Status)
	}
	if strings.Join(added.DNS.Expected, ",") != "203.0.113.10,203.0.113.20" {
		t.Errorf("expected %v: the address the tab offers first, then each server's", added.DNS.Expected)
	}
}

// The domain has to be the app's: another app's domain id in this app's path
// is not found, and neither is another team's.
func TestADNSCheckIsOnlyForTheAppsOwnDomains(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	web := h.app(acme, "web")
	backend := h.app(acme, "api")
	theirs := h.app(other, "shop")

	domain := store.Domain{AppID: backend.ID, Hostname: "api.example.com", Path: "/", TLS: true, Status: "pending"}
	if err := h.db.CreateDomain(t.Context(), &domain); err != nil {
		t.Fatal(err)
	}
	if code, body := h.do(acme, "POST", "/api/apps/"+web.ID+"/domains/"+domain.ID+"/check", nil); code != http.StatusNotFound {
		t.Errorf("another app's domain through this app's path answered %d %s", code, body)
	}
	if code, _ := h.do(other, "POST", "/api/apps/"+backend.ID+"/domains/"+domain.ID+"/check", nil); code != http.StatusNotFound {
		t.Errorf("another team checked acme's domain: %d", code)
	}
	if code, _ := h.do(other, "POST", "/api/apps/"+theirs.ID+"/domains/"+domain.ID+"/check", nil); code != http.StatusNotFound {
		t.Errorf("another team checked acme's domain through its own app: %d", code)
	}
	if code, body := h.do(acme, "POST", "/api/apps/"+backend.ID+"/domains/"+domain.ID+"/check", nil); code != http.StatusOK {
		t.Errorf("the app's own domain could not be checked: %d %s", code, body)
	}
}

// resolverFunc reads the resolver each time, so a test can change what DNS
// says between two requests.
type resolverFunc func() Resolver

func (f resolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f().LookupIPAddr(ctx, host)
}

func (f resolverFunc) LookupCNAME(ctx context.Context, host string) (string, error) {
	return f().LookupCNAME(ctx, host)
}
