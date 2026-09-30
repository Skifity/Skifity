package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type addressedCluster struct {
	Cluster
	address string
}

func (c addressedCluster) PublicAddress(context.Context, string) string { return c.address }

// The panel says where to point a domain, because it knows.
//
// The Domains tab asked people to create a DNS record and then rendered the
// address as "Unknown": it interpolated t("common.unknown") where the IP goes,
// on the screen that exists to answer exactly that question, while
// docs/quick-start.md told them "the panel shows the DNS record". The address
// has been a setting since there were settings, and the same value is what an
// app's automatic subdomain resolves to.
func TestADomainSaysWhereToPointIt(t *testing.T) {
	h := newHarness(t)
	h.withCluster(addressedCluster{address: "203.0.113.10"})
	owner := h.newTenant("acme")
	app := h.app(owner, "web")

	code, body := h.do(owner, "POST", "/api/apps/"+app.ID+"/domains",
		map[string]any{"hostname": "blog.example.com"})
	if code != 200 && code != 201 {
		t.Fatalf("add a domain: %d %s", code, body)
	}

	code, body = h.do(owner, "GET", "/api/apps/"+app.ID+"/domains", nil)
	if code != 200 {
		t.Fatalf("list domains: %d %s", code, body)
	}
	var list struct {
		Items []struct {
			Hostname  string `json:"hostname"`
			Auto      bool   `json:"auto"`
			DNSTarget string `json:"dns_target"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}

	found := false
	for _, domain := range list.Items {
		if domain.Hostname != "blog.example.com" {
			continue
		}
		found = true
		if domain.DNSTarget != "203.0.113.10" {
			t.Errorf("the domain says to point at %q, not the address the panel gives its own apps", domain.DNSTarget)
		}
	}
	if !found {
		t.Fatalf("the domain that was just added is not in the list: %s", body)
	}
	if strings.Contains(body, "unknown") {
		t.Errorf("the list still carries the word the interface used to print instead of an address:\n%s", body)
	}
}

// Without a cluster the panel says nothing rather than something wrong.
func TestADomainWithNoKnownAddressSaysNothing(t *testing.T) {
	h := newHarness(t)
	h.withCluster(addressedCluster{address: ""})
	owner := h.newTenant("acme")
	app := h.app(owner, "web")

	if code, body := h.do(owner, "POST", "/api/apps/"+app.ID+"/domains",
		map[string]any{"hostname": "blog.example.com"}); code != 200 && code != 201 {
		t.Fatalf("add a domain: %d %s", code, body)
	}
	_, body := h.do(owner, "GET", "/api/apps/"+app.ID+"/domains", nil)
	if strings.Contains(body, "dns_target") {
		t.Errorf("an address the panel does not have was sent anyway:\n%s", body)
	}
}
