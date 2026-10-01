package store

import (
	"errors"
	"testing"
)

// A connection's credentials are sealed, on the list rotation walks, and
// read by one query; another team reaches none of it.
func TestADNSProvidersCredentialsAreSealedAndRotated(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, _ := seedTeam(t, db)

	provider := DNSProvider{ID: NewID("dnsp"), TeamID: team.ID, Kind: "cloudflare", Name: "Cloudflare",
		Zones: []DNSZone{{ID: "z1", Name: "example.com"}}}
	if err := db.CreateDNSProvider(ctx, &provider, "SKF1.sealed-token"); err != nil {
		t.Fatal(err)
	}
	listed, err := db.ListDNSProviders(ctx, team.ID)
	if err != nil || len(listed) != 1 || len(listed[0].Zones) != 1 || listed[0].Zones[0].Name != "example.com" {
		t.Fatalf("listed %+v (%v)", listed, err)
	}

	refs, err := db.ListSealedSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range refs {
		if ref.Table == "dns_providers" && ref.ID == provider.ID && ref.Sealed == "SKF1.sealed-token" {
			found = true
			if written, err := db.UpdateSealed(ctx, ref, "SKF1.rewrapped"); err != nil || !written {
				t.Errorf("the rewrapped credentials were not written back: %v, %v", written, err)
			}
		}
	}
	if !found {
		t.Fatal("a master key rotation would leave a DNS provider's credentials behind")
	}
	if sealed, _ := db.DNSProviderCredentials(ctx, team.ID, provider.ID); sealed != "SKF1.rewrapped" {
		t.Errorf("after rotation the credentials are %q", sealed)
	}

	other := Team{Name: "Other", Slug: "other"}
	if err := db.CreateTeam(ctx, &other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetDNSProvider(ctx, other.ID, provider.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another team read the connection: %v", err)
	}
	if _, err := db.DNSProviderCredentials(ctx, other.ID, provider.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another team read the credentials: %v", err)
	}
	if _, err := db.DeleteDNSProvider(ctx, other.ID, provider.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another team deleted the connection: %v", err)
	}
	dup := DNSProvider{ID: NewID("dnsp"), TeamID: team.ID, Kind: "hetzner", Name: "Cloudflare"}
	if err := db.CreateDNSProvider(ctx, &dup, "SKF1.x"); !errors.Is(err, ErrConflict) {
		t.Errorf("a second connection of the same name was %v, want ErrConflict", err)
	}
}

// When a domain goes — however it goes — its records stay in the books with
// no domain, for the sync to take them off the provider. When a connection
// goes, its books go with it, and the answer says how many records were left.
func TestTheBooksOutliveTheDomainAndNotTheConnection(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, env := seedTeam(t, db)
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}
	domain := Domain{AppID: app.ID, Hostname: "blog.example.com", Path: "/", TLS: true}
	if err := db.CreateDomain(ctx, &domain); err != nil {
		t.Fatal(err)
	}
	provider := DNSProvider{ID: NewID("dnsp"), TeamID: team.ID, Kind: "cloudflare", Name: "Cloudflare"}
	if err := db.CreateDNSProvider(ctx, &provider, "SKF1.sealed"); err != nil {
		t.Fatal(err)
	}
	record := DNSRecord{TeamID: team.ID, ProviderID: provider.ID, DomainID: domain.ID, ZoneID: "z1",
		ZoneName: "example.com", Hostname: "blog.example.com", Type: "a", Content: "203.0.113.10", RemoteID: "r1"}
	if err := db.SaveDNSRecord(ctx, &record); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDomainDNS(ctx, DomainDNS{DomainID: domain.ID, Manage: true, State: DNSStateCreated}); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.DNSRecordsForDomain(ctx, domain.ID); len(rows) != 1 || rows[0].Type != "A" {
		t.Fatalf("the domain's records are %+v", rows)
	}
	domains, err := db.DomainsForDNS(ctx)
	if err != nil || len(domains) != 1 || domains[0].DNS == nil || domains[0].TeamID != team.ID {
		t.Fatalf("the sync sees %+v (%v)", domains, err)
	}

	if err := db.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	orphans, err := db.OrphanDNSRecords(ctx)
	if err != nil || len(orphans) != 1 || orphans[0].RemoteID != "r1" {
		t.Fatalf("after the app went, the orphans are %+v (%v)", orphans, err)
	}
	if _, has, _ := db.GetDomainDNS(ctx, domain.ID); has {
		t.Error("the domain's switch outlived the domain")
	}

	left, err := db.DeleteDNSProvider(ctx, team.ID, provider.ID)
	if err != nil || left != 1 {
		t.Errorf("removing the connection said %d records were left (%v)", left, err)
	}
	if orphans, _ := db.OrphanDNSRecords(ctx); len(orphans) != 0 {
		t.Errorf("the books kept %d records of a connection that is gone", len(orphans))
	}
}

// Every panel names itself once, in the migration, for the note its records
// carry.
func TestThePanelHasANameForItsRecords(t *testing.T) {
	db := testDB(t)
	id, _, err := db.GetSetting(t.Context(), "maintenance.panel_id")
	if err != nil || len(id) != 12 {
		t.Fatalf("the panel's id is %q (%v)", id, err)
	}
}
