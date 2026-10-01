package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/dnsprov"
	"skifity/internal/store"
)

// A team's DNS provider, through the router the panel serves: who may connect
// one, that its token never comes back, and that adding and removing a domain
// creates and removes the record — the panel's own, and never anybody
// else's. The provider is in memory; internal/dnsprov tests each real one's
// API shapes against a server that speaks them.

const fakeDNSToken = "cf-test-token-not-real-000000000000000000"

func (h *harness) withDNS(kind string, zones ...string) *dnsprov.Memory {
	h.t.Helper()
	memory := dnsprov.NewMemory(kind, fakeDNSToken, zones...)
	h.api.dns.Open = memory.Opener()
	return memory
}

func connectDNS(t *testing.T, h *harness, as tenant) string {
	t.Helper()
	status, body := h.do(as, http.MethodPost, "/api/teams/"+as.team.ID+"/dns-providers",
		map[string]any{"kind": "cloudflare", "token": fakeDNSToken})
	if status != http.StatusCreated {
		t.Fatalf("connecting answered %d\n%s", status, body)
	}
	var created struct {
		ID    string `json:"id"`
		Zones []struct {
			Name string `json:"name"`
		} `json:"zones"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || len(created.Zones) == 0 {
		t.Fatalf("the connection answered %s", body)
	}
	return created.ID
}

func TestADNSProviderIsConnectedByAnAdministratorAndNeverShowsItsToken(t *testing.T) {
	h := newHarness(t)
	h.withDNS(dnsprov.Cloudflare, "example.com")
	acme := h.newTenant("acme")
	member := h.newMember(acme, "dev", store.RoleMember)
	viewer := h.newMember(acme, "viewer", store.RoleViewer)

	if status, _ := h.do(member, http.MethodPost, "/api/teams/"+acme.team.ID+"/dns-providers",
		map[string]any{"kind": "cloudflare", "token": fakeDNSToken}); status != http.StatusForbidden {
		t.Errorf("a member connecting a provider answered %d, want 403", status)
	}
	id := connectDNS(t, h, acme)

	for _, as := range []tenant{acme, member, viewer} {
		status, body := h.do(as, http.MethodGet, "/api/teams/"+acme.team.ID+"/dns-providers", nil)
		if status != http.StatusOK || !strings.Contains(body, "example.com") {
			t.Errorf("listing answered %d\n%s", status, body)
		}
		if strings.Contains(body, fakeDNSToken) || strings.Contains(body, "credentials") {
			t.Errorf("the list carries the token:\n%s", body)
		}
	}
	sealed, err := h.db.DNSProviderCredentials(t.Context(), acme.team.ID, id)
	if err != nil || strings.Contains(sealed, fakeDNSToken) {
		t.Errorf("the token is stored as %q (%v)", sealed, err)
	}

	// Zones and the test ask the provider: a member may list zones, only an
	// administrator tests or removes.
	if status, body := h.do(member, http.MethodGet, "/api/teams/"+acme.team.ID+"/dns-providers/"+id+"/zones", nil); status != http.StatusOK ||
		!strings.Contains(body, `"total":1`) {
		t.Errorf("a member listing zones answered %d\n%s", status, body)
	}
	if status, _ := h.do(member, http.MethodPost, "/api/teams/"+acme.team.ID+"/dns-providers/"+id+"/test", map[string]any{}); status != http.StatusForbidden {
		t.Errorf("a member testing a connection answered %d", status)
	}
	if status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/dns-providers/"+id+"/test", map[string]any{}); status != http.StatusOK {
		t.Errorf("testing answered %d\n%s", status, body)
	}

	// Another team's connection is not there for them.
	other := h.newTenant("other")
	if status, _ := h.do(other, http.MethodDelete, "/api/teams/"+other.team.ID+"/dns-providers/"+id, nil); status != http.StatusNotFound {
		t.Errorf("another team removing the connection answered %d", status)
	}
	if status, _ := h.do(acme, http.MethodDelete, "/api/teams/"+acme.team.ID+"/dns-providers/"+id, nil); status != http.StatusOK {
		t.Errorf("removing answered %d", status)
	}
}

// The provider's address is its own: nothing a person sends can point the
// panel's requests somewhere else, and the panel's own manager dials the
// real providers through the guard.
func TestADNSProviderCannotBePointedAtAnotherAddress(t *testing.T) {
	h := newHarness(t)
	if h.api.dns.Open != nil {
		t.Fatal("the panel's DNS manager is not the one that dials through internal/netguard")
	}
	acme := h.newTenant("acme")
	for _, field := range []string{"base_url", "endpoint", "url"} {
		status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/dns-providers",
			map[string]any{"kind": "cloudflare", "token": fakeDNSToken, field: "http://169.254.169.254/"})
		if status != http.StatusBadRequest || !strings.Contains(body, field) {
			t.Errorf("a connection with %s answered %d\n%s", field, status, body)
		}
	}
	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/dns-providers",
		map[string]any{"kind": "cloudflare", "token": "0123456789abcdef0123456789abcdef01234"})
	if status != http.StatusBadRequest || !strings.Contains(body, "dns.cloudflare_global_key") {
		t.Errorf("a Global API Key answered %d\n%s", status, body)
	}
}

type managedAnswer struct {
	ID         string `json:"id"`
	Hostname   string `json:"hostname"`
	ManagedDNS *struct {
		Provider string `json:"provider"`
		Zone     string `json:"zone"`
		Manage   bool   `json:"manage"`
		State    string `json:"state"`
		Records  []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"records"`
		Problem *struct {
			Code string `json:"code"`
		} `json:"problem"`
	} `json:"managed_dns"`
}

func TestAddingADomainCreatesItsRecordAndRemovingItRemovesIt(t *testing.T) {
	h := newHarness(t)
	h.withCluster(addressedCluster{address: "203.0.113.10"})
	memory := h.withDNS(dnsprov.Cloudflare, "example.com")
	acme := h.newTenant("acme")
	connectDNS(t, h, acme)
	app := h.app(acme, "web")
	// Somebody's mail record at the same name is no business of the panel's.
	memory.Put(dnsprov.Record{Type: "TXT", Name: "blog.example.com", Content: "v=spf1 -all"})

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/domains", map[string]any{"hostname": "blog.example.com"})
	if status != http.StatusCreated {
		t.Fatalf("adding answered %d\n%s", status, body)
	}
	var added managedAnswer
	if err := json.Unmarshal([]byte(body), &added); err != nil || added.ManagedDNS == nil {
		t.Fatalf("the answer says nothing about the record: %s", body)
	}
	if added.ManagedDNS.State != "created" || len(added.ManagedDNS.Records) != 1 || added.ManagedDNS.Records[0].Content != "203.0.113.10" {
		t.Errorf("the record is %+v", added.ManagedDNS)
	}
	if records := memory.All("blog.example.com"); len(records) != 2 {
		t.Fatalf("the provider has %+v", records)
	}

	// The list says so too, without asking the provider.
	asked := memory.Asked["Records"]
	_, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/domains", nil)
	if !strings.Contains(body, `"state":"created"`) || memory.Asked["Records"] != asked {
		t.Errorf("the list is %s", body)
	}

	if status, body := h.do(acme, http.MethodDelete, "/api/apps/"+app.ID+"/domains/"+added.ID, nil); status != http.StatusOK {
		t.Fatalf("removing answered %d\n%s", status, body)
	}
	records := memory.All("blog.example.com")
	if len(records) != 1 || records[0].Type != "TXT" {
		t.Errorf("after removing the domain the provider has %+v", records)
	}
}

func TestADomainWhoseNameIsTakenIsAddedAndItsRecordRefused(t *testing.T) {
	h := newHarness(t)
	h.withCluster(addressedCluster{address: "203.0.113.10"})
	memory := h.withDNS(dnsprov.Cloudflare, "example.com")
	acme := h.newTenant("acme")
	connectDNS(t, h, acme)
	app := h.app(acme, "web")
	theirs := memory.Put(dnsprov.Record{Type: "A", Name: "blog.example.com", Content: "198.51.100.7"})

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/domains", map[string]any{"hostname": "blog.example.com"})
	if status != http.StatusCreated {
		t.Fatalf("adding answered %d\n%s", status, body)
	}
	var added managedAnswer
	_ = json.Unmarshal([]byte(body), &added)
	if added.ManagedDNS == nil || added.ManagedDNS.State != "refused" || added.ManagedDNS.Problem == nil ||
		added.ManagedDNS.Problem.Code != "dns.record_conflict" {
		t.Fatalf("the refusal is not in the answer: %s", body)
	}
	if records := memory.All("blog.example.com"); len(records) != 1 || records[0].Content != "198.51.100.7" {
		t.Errorf("somebody else's record was touched: %+v", records)
	}

	// Try again answers the refusal while it stands, and works once the way
	// is clear.
	path := "/api/apps/" + app.ID + "/domains/" + added.ID
	if status, body := h.do(acme, http.MethodPatch, path, map[string]any{"manage_dns": true}); status != http.StatusConflict ||
		!strings.Contains(body, "198.51.100.7") {
		t.Errorf("trying again answered %d\n%s", status, body)
	}
	memory.Edit("blog.example.com", "A", "203.0.113.10")
	if status, body := h.do(acme, http.MethodPatch, path, map[string]any{"manage_dns": true}); status != http.StatusOK ||
		!strings.Contains(body, `"state":"elsewhere"`) {
		t.Errorf("with somebody's record already pointing here, trying again answered %d\n%s", status, body)
	}
	// Removing the domain leaves their record alone.
	h.do(acme, http.MethodDelete, path, nil)
	if records := memory.All("blog.example.com"); len(records) != 1 || records[0].ID != theirs.ID {
		t.Errorf("removing the domain removed somebody else's record: %+v", records)
	}
}

func TestTheSwitchIsHonoured(t *testing.T) {
	h := newHarness(t)
	h.withCluster(addressedCluster{address: "203.0.113.10"})
	memory := h.withDNS(dnsprov.Cloudflare, "example.com")
	acme := h.newTenant("acme")
	app := h.app(acme, "web")

	// Asked for where no zone is connected: refused, and nothing added.
	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/domains",
		map[string]any{"hostname": "blog.example.com", "manage_dns": true})
	if status != http.StatusBadRequest || !strings.Contains(body, "dns.no_zone") {
		t.Errorf("manage_dns with no zone answered %d\n%s", status, body)
	}
	if domains, _ := h.db.ListDomains(t.Context(), app.ID); len(domains) != 0 {
		t.Errorf("the domain was added anyway: %+v", domains)
	}

	connectDNS(t, h, acme)
	status, body = h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/domains",
		map[string]any{"hostname": "blog.example.com", "manage_dns": false})
	if status != http.StatusCreated || !strings.Contains(body, `"state":"off"`) {
		t.Errorf("manage_dns false answered %d\n%s", status, body)
	}
	if len(memory.All("blog.example.com")) != 0 {
		t.Error("a record was created when the switch said not to")
	}

	// A member limited to their project adds domains there too, and is
	// answered about the record the same way.
	limited := h.limitedMember(acme, "contractor", store.RoleMember, acme.project.ID)
	status, body = h.do(limited, http.MethodPost, "/api/apps/"+app.ID+"/domains", map[string]any{"hostname": "shop.example.com"})
	if status != http.StatusCreated || !strings.Contains(body, `"state":"created"`) {
		t.Errorf("a limited member adding a domain answered %d\n%s", status, body)
	}
	if status, _ := h.do(limited, http.MethodGet, "/api/teams/"+acme.team.ID+"/dns-providers", nil); status != http.StatusOK {
		t.Errorf("a limited member listing the connections answered %d", status)
	}
}

// Behind Cloudflare's proxy, DNS answers with Cloudflare's addresses; the
// check says so rather than "elsewhere", and confirms the origin from the
// record the panel keeps there.
func TestAProxiedDomainIsNotElsewhere(t *testing.T) {
	h := newHarness(t)
	h.withCluster(addressedCluster{address: "203.0.113.10"})
	h.withDNS(dnsprov.Cloudflare, "example.com")
	h.api.resolver = fakeDNS{addresses: map[string][]string{
		"blog.example.com":  {"104.21.32.1", "172.67.150.2"},
		"other.example.com": {"104.21.32.1"},
	}}
	acme := h.newTenant("acme")
	connectDNS(t, h, acme)
	app := h.app(acme, "web")

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/domains", map[string]any{"hostname": "blog.example.com"})
	if status != http.StatusCreated {
		t.Fatalf("adding answered %d\n%s", status, body)
	}
	var added struct {
		ID  string   `json:"id"`
		DNS DNSCheck `json:"dns"`
	}
	_ = json.Unmarshal([]byte(body), &added)
	if added.DNS.Status != DNSProxied || !added.DNS.OriginConfirmed || !added.DNS.PointsHere {
		t.Errorf("a proxied domain whose record the panel keeps was checked as %+v", added.DNS)
	}

	status, body = h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/domains",
		map[string]any{"hostname": "other.example.com", "manage_dns": false})
	if status != http.StatusCreated {
		t.Fatalf("adding answered %d\n%s", status, body)
	}
	var other struct {
		DNS DNSCheck `json:"dns"`
	}
	_ = json.Unmarshal([]byte(body), &other)
	if other.DNS.Status != DNSProxied || other.DNS.OriginConfirmed || other.DNS.PointsHere {
		t.Errorf("a proxied domain the panel cannot see behind was checked as %+v", other.DNS)
	}
}
