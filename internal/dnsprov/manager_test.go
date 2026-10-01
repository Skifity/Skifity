package dnsprov

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// The Manager against an in-memory provider and a real database and keyring:
// the whole of "the panel only ever touches records it created", end to end.

type world struct {
	t       *testing.T
	db      *store.DB
	keyring *crypto.Keyring
	m       *Manager
	dns     *Memory
	team    store.Team
	app     store.App
	address string
}

const memToken = "mem-test-token-not-real-0000000000000000"

func newWorld(t *testing.T, kind string) *world {
	t.Helper()
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, db: db, keyring: keyring, dns: NewMemory(kind, memToken, "example.com", "shop.example.com"), address: "203.0.113.10"}
	w.m = &Manager{DB: db, Keyring: keyring, Log: slog.New(slog.DiscardHandler), Open: w.dns.Opener(),
		Address: func(context.Context, string) string { return w.address }}

	w.team = store.Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &w.team); err != nil {
		t.Fatal(err)
	}
	project := store.Project{TeamID: w.team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := store.Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	w.app = store.App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &w.app); err != nil {
		t.Fatal(err)
	}
	creds := Credentials{Token: memToken}
	if kind == Route53 {
		creds = Credentials{AccessKeyID: "AKIAEXAMPLENOTREAL00", SecretAccessKey: memToken}
	}
	if _, err := w.m.Connect(ctx, w.team.ID, kind, "", creds); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return w
}

func (w *world) domain(hostname string) store.Domain {
	w.t.Helper()
	d := store.Domain{AppID: w.app.ID, Hostname: hostname, Path: "/", TLS: true}
	if err := w.db.CreateDomain(w.t.Context(), &d); err != nil {
		w.t.Fatal(err)
	}
	return d
}

func (w *world) state(d store.Domain) store.DomainDNS {
	w.t.Helper()
	row, _, err := w.db.GetDomainDNS(w.t.Context(), d.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return row
}

func problemCode(err error) string {
	var p *errdoc.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func TestAConnectionIsCheckedAndItsCredentialsSealed(t *testing.T) {
	w := newWorld(t, Cloudflare)
	ctx := t.Context()
	providers, _ := w.db.ListDNSProviders(ctx, w.team.ID)
	if len(providers) != 1 || providers[0].Name != "Cloudflare" || len(providers[0].Zones) != 2 {
		t.Fatalf("the connection is %+v", providers)
	}
	sealed, _ := w.db.DNSProviderCredentials(ctx, w.team.ID, providers[0].ID)
	if strings.Contains(sealed, memToken) || !strings.HasPrefix(sealed, "SKF") {
		t.Errorf("the token is stored as %q", sealed)
	}
	// Sealed under this connection: a copy in another row does not open.
	if _, err := w.keyring.Open(sealed, store.DNSProviderContext(w.team.ID, "dnsp_other")); err == nil {
		t.Error("the credentials open under another connection's context")
	}

	if _, err := w.m.Connect(ctx, w.team.ID, Cloudflare, "x", Credentials{Token: "0123456789abcdef0123456789abcdef01234"}); problemCode(err) != "dns.cloudflare_global_key" {
		t.Errorf("a Global API Key answered %v", err)
	}
	if _, err := w.m.Connect(ctx, w.team.ID, Cloudflare, "x", Credentials{Token: "wrong-token-000000000000000000000000000000"}); problemCode(err) != "dns.provider_refused" {
		t.Errorf("a wrong token answered %v", err)
	}
	empty := NewMemory(Cloudflare, memToken)
	w.m.Open = empty.Opener()
	if _, err := w.m.Connect(ctx, w.team.ID, Cloudflare, "y", Credentials{Token: memToken}); problemCode(err) != "dns.no_zones" {
		t.Errorf("a token that sees no zones answered %v", err)
	}
	if list, _ := w.db.ListDNSProviders(ctx, w.team.ID); len(list) != 1 {
		t.Errorf("a refused connection was saved: %d connections", len(list))
	}
}

func TestADomainsRecordIsCreatedTaggedAndKept(t *testing.T) {
	w := newWorld(t, Cloudflare)
	ctx := t.Context()
	if err := w.db.SetSetting(ctx, settings.KeyClusterIPv6, "2001:db8::10", false, "test"); err != nil {
		t.Fatal(err)
	}
	d := w.domain("blog.example.com")
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	records := w.dns.All("blog.example.com")
	if len(records) != 2 {
		t.Fatalf("the name has %+v", records)
	}
	id, _, _ := w.db.GetSetting(ctx, settings.KeyPanelID)
	for _, r := range records {
		if r.Note != Marker(id) || r.Proxied {
			t.Errorf("the record is %+v: not tagged as this panel's, or proxied", r)
		}
	}
	if state := w.state(d); state.State != store.DNSStateCreated || !state.Manage {
		t.Errorf("the domain's state is %+v", state)
	}
	books, _ := w.db.DNSRecordsForDomain(ctx, d.ID)
	if len(books) != 2 {
		t.Errorf("the books have %d records", len(books))
	}

	// Asked again, nothing changes: no second record, no rewrite.
	created := w.dns.Asked["Create"]
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	if w.dns.Asked["Create"] != created || len(w.dns.All("blog.example.com")) != 2 {
		t.Error("asking again made another record")
	}
}

func TestSomebodyElsesRecordIsNeverOverwritten(t *testing.T) {
	w := newWorld(t, Cloudflare)
	ctx := t.Context()
	w.dns.Put(Record{Type: "A", Name: "blog.example.com", Content: "198.51.100.7"})
	d := w.domain("blog.example.com")

	err := w.m.Ensure(ctx, w.team.ID, d)
	if problemCode(err) != "dns.record_conflict" || !strings.Contains(err.Error(), "198.51.100.7") {
		t.Fatalf("a record in the way answered %v", err)
	}
	if records := w.dns.All("blog.example.com"); len(records) != 1 || records[0].Content != "198.51.100.7" {
		t.Errorf("the record in the way was touched: %+v", records)
	}
	state := w.state(d)
	if state.State != store.DNSStateRefused || !strings.Contains(state.Problem, "dns.record_conflict") {
		t.Errorf("the refusal was not kept on the domain: %+v", state)
	}

	// Once it is removed, trying again creates the panel's own.
	w.dns.mu.Lock()
	w.dns.records = map[string][]Record{}
	w.dns.mu.Unlock()
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	if w.state(d).State != store.DNSStateCreated {
		t.Error("the way clear, the record was not created")
	}
}

func TestARecordThatAlreadyPointsHereIsLeftToWhoeverMadeIt(t *testing.T) {
	w := newWorld(t, Hetzner)
	ctx := t.Context()
	theirs := w.dns.Put(Record{Type: "A", Name: "blog.example.com", Content: "203.0.113.10"})
	d := w.domain("blog.example.com")
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	if w.state(d).State != store.DNSStateElsewhere {
		t.Errorf("the state is %+v", w.state(d))
	}
	if err := w.m.Release(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if records := w.dns.All("blog.example.com"); len(records) != 1 || records[0].ID != theirs.ID {
		t.Errorf("removing the domain removed somebody else's record: %+v", records)
	}
}

func TestRemovingADomainRemovesOnlyTheRecordThePanelMade(t *testing.T) {
	w := newWorld(t, DigitalOcean)
	ctx := t.Context()
	w.dns.Put(Record{Type: "TXT", Name: "blog.example.com", Content: "v=spf1 -all"})
	d := w.domain("blog.example.com")
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	if err := w.m.Release(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	records := w.dns.All("blog.example.com")
	if len(records) != 1 || records[0].Type != "TXT" {
		t.Errorf("after removing the domain the name has %+v", records)
	}
	if books, _ := w.db.DNSRecordsForDomain(ctx, d.ID); len(books) != 0 {
		t.Errorf("the books still have %d records", len(books))
	}
}

func TestARecordSomebodyChangedIsLeftWhenTheDomainGoes(t *testing.T) {
	w := newWorld(t, Route53)
	ctx := t.Context()
	d := w.domain("blog.example.com")
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	w.dns.Edit("blog.example.com", "A", "198.51.100.99")
	if err := w.m.Release(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if records := w.dns.All("blog.example.com"); len(records) != 1 || records[0].Content != "198.51.100.99" {
		t.Errorf("a record somebody changed was deleted: %+v", records)
	}
}

// The domain deleted however it is — with its app, its environment — leaves
// its record in the books, and the sync takes it off the provider.
func TestTheSyncRemovesTheRecordsOfDomainsThatAreGone(t *testing.T) {
	w := newWorld(t, Cloudflare)
	ctx := t.Context()
	d := w.domain("blog.example.com")
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	if err := w.db.DeleteApp(ctx, w.app.ID); err != nil {
		t.Fatal(err)
	}
	w.m.Sync(ctx, false)
	if records := w.dns.All("blog.example.com"); len(records) != 0 {
		t.Errorf("the record of a deleted app's domain is still there: %+v", records)
	}
	if orphans, _ := w.db.OrphanDNSRecords(ctx); len(orphans) != 0 {
		t.Errorf("the books still have %d orphans", len(orphans))
	}
}

// The cluster moved: the sync notices from its own books, without asking the
// provider, and points the panel's records at the new address.
func TestAnAddressChangeUpdatesTheRecords(t *testing.T) {
	w := newWorld(t, Cloudflare)
	ctx := t.Context()
	d := w.domain("blog.example.com")
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	listed := w.dns.Asked["Records"]
	w.m.Sync(ctx, false)
	if w.dns.Asked["Records"] != listed {
		t.Error("a sync with nothing to do asked the provider anyway")
	}

	w.address = "198.51.100.20"
	w.m.Sync(ctx, false)
	records := w.dns.All("blog.example.com")
	if len(records) != 1 || records[0].Content != "198.51.100.20" {
		t.Fatalf("after the address changed the name has %+v", records)
	}
	if books, _ := w.db.DNSRecordsForDomain(ctx, d.ID); len(books) != 1 || books[0].Content != "198.51.100.20" {
		t.Errorf("the books say %+v", books)
	}

	// The hook does the same at once, for every record.
	w.address = "198.51.100.30"
	w.m.AddressChanged(ctx)
	if records := w.dns.All("blog.example.com"); records[0].Content != "198.51.100.30" {
		t.Errorf("AddressChanged left %+v", records)
	}
}

// A tunnel is the way in: at Cloudflare the record is a proxied CNAME to it,
// replacing the panel's own A record; elsewhere it cannot work, and says so.
func TestATunnelIsACNAMEAtCloudflare(t *testing.T) {
	w := newWorld(t, Cloudflare)
	ctx := t.Context()
	d := w.domain("blog.example.com")
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	token := base64.StdEncoding.EncodeToString(mustJSON(map[string]string{
		"a": "account", "t": "6ff42ae2-765d-4adf-8112-31c55c1551ef", "s": "c2VjcmV0",
	}))
	sealed, err := w.keyring.Seal([]byte(token), settings.Context(settings.KeyCloudflareTunnelToken))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.db.SetSetting(ctx, settings.KeyCloudflareTunnelToken, sealed, true, "test"); err != nil {
		t.Fatal(err)
	}
	if err := w.db.SetComponent(ctx, store.ClusterComponent{Name: "cloudflare-tunnel", Status: "installed"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := settings.LookupComponent(tunnelComponent); !ok {
		t.Fatalf("%q is not the tunnel's component", tunnelComponent)
	}

	w.m.AddressChanged(ctx)
	records := w.dns.All("blog.example.com")
	if len(records) != 1 || records[0].Type != "CNAME" || !records[0].Proxied ||
		records[0].Content != "6ff42ae2-765d-4adf-8112-31c55c1551ef.cfargotunnel.com" {
		t.Fatalf("with a tunnel the name has %+v", records)
	}

	if _, err := Wants(w.m.Target(ctx, w.team.ID), Hetzner, "blog.example.com", "example.com"); err != nil {
		t.Errorf("with a public address too, a zone elsewhere should get it: %v", err)
	}
	if _, err := Wants(Target{Tunnel: "x.cfargotunnel.com"}, Hetzner, "blog.example.com", "example.com"); problemCode(err) != "dns.tunnel_needs_cloudflare" {
		t.Errorf("a tunnel with no address at Hetzner answered %v", err)
	}
	if _, err := Wants(Target{Name: "lb.example.net"}, DigitalOcean, "example.com", "example.com"); problemCode(err) != "dns.apex_cname" {
		t.Errorf("a CNAME at a zone's own name answered %v", err)
	}
	if _, err := Wants(Target{}, Cloudflare, "a.example.com", "example.com"); problemCode(err) != "dns.no_address" {
		t.Errorf("no address answered %v", err)
	}
}

// An automatic address in a connected zone is given its record, unless a
// wildcard record already sends it here.
func TestAnAutomaticAddressIsCoveredByTheWildcardOrGivenARecord(t *testing.T) {
	w := newWorld(t, Cloudflare)
	ctx := t.Context()
	auto := store.Domain{AppID: w.app.ID, Hostname: "web-production.apps.example.com", Path: "/", TLS: true, Auto: true}
	if err := w.db.CreateDomain(ctx, &auto); err != nil {
		t.Fatal(err)
	}
	w.dns.Put(Record{Type: "A", Name: "*.apps.example.com", Content: "203.0.113.10"})
	w.m.Sync(ctx, false)
	if len(w.dns.All(auto.Hostname)) != 0 || w.state(auto).State != store.DNSStateElsewhere {
		t.Errorf("under a wildcard that points here, the name got %+v (%+v)", w.dns.All(auto.Hostname), w.state(auto))
	}

	other := store.Domain{AppID: w.app.ID, Hostname: "api-production.apps.example.org", Path: "/", Auto: true}
	if err := w.db.CreateDomain(ctx, &other); err != nil {
		t.Fatal(err)
	}
	w.dns.mu.Lock()
	w.dns.records = map[string][]Record{}
	w.dns.mu.Unlock()
	w.m.Sync(ctx, true)
	if records := w.dns.All(auto.Hostname); len(records) != 1 || records[0].Content != "203.0.113.10" {
		t.Errorf("without the wildcard the automatic address has %+v", records)
	}
	if _, has, _ := w.db.GetDomainDNS(ctx, other.ID); has {
		t.Error("an address in no connected zone was taken on")
	}
}

// Switched off, the panel forgets its record and leaves it where it is.
func TestSwitchingOffLeavesTheRecord(t *testing.T) {
	w := newWorld(t, Cloudflare)
	ctx := t.Context()
	d := w.domain("blog.example.com")
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	if err := w.m.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.dns.All("blog.example.com")) != 1 {
		t.Error("switching off deleted the record")
	}
	w.address = "198.51.100.20"
	w.m.Sync(ctx, true)
	if records := w.dns.All("blog.example.com"); records[0].Content != "203.0.113.10" {
		t.Errorf("a record the panel stopped keeping was changed: %+v", records)
	}
	// Switched back on, the record is the panel's own again, and follows the
	// address.
	if err := w.m.Ensure(ctx, w.team.ID, d); err != nil {
		t.Fatal(err)
	}
	if records := w.dns.All("blog.example.com"); len(records) != 1 || records[0].Content != "198.51.100.20" {
		t.Errorf("switched back on, the name has %+v", records)
	}

	// And off again, removing the domain leaves the record too.
	if err := w.m.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.db.DeleteDomain(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	w.m.Sync(ctx, false)
	if len(w.dns.All("blog.example.com")) != 1 {
		t.Error("a record the panel had stopped keeping was deleted with its domain")
	}
	if orphans, _ := w.db.OrphanDNSRecords(ctx); len(orphans) != 0 {
		t.Errorf("the books kept %d records nobody keeps", len(orphans))
	}
}

// The zone is the longest across every connection.
func TestTheLongestZoneAcrossConnectionsWins(t *testing.T) {
	providers := []store.DNSProvider{
		{ID: "a", Zones: []store.DNSZone{{ID: "1", Name: "example.co.uk"}}},
		{ID: "b", Zones: []store.DNSZone{{ID: "2", Name: "shop.example.co.uk"}}},
	}
	if p, z, ok := ZoneFor(providers, "www.shop.example.co.uk"); !ok || p.ID != "b" || z.ID != "2" {
		t.Errorf("matched %s/%s", p.ID, z.ID)
	}
	if p, _, ok := ZoneFor(providers, "blog.example.co.uk"); !ok || p.ID != "a" {
		t.Errorf("matched %s", p.ID)
	}
	if _, _, ok := ZoneFor(providers, "example.com"); ok {
		t.Error("matched a zone nobody connected")
	}
}

func TestCloudflaresAddressesAreKnown(t *testing.T) {
	for _, address := range []string{"104.16.132.229", "172.67.1.1", "2606:4700::6810:84e5", "::ffff:104.16.0.1"} {
		if !CloudflareAddress(address) {
			t.Errorf("%s is Cloudflare's", address)
		}
	}
	for _, address := range []string{"203.0.113.10", "2001:db8::10", "not an address"} {
		if CloudflareAddress(address) {
			t.Errorf("%s is not Cloudflare's", address)
		}
	}
}

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
