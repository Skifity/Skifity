package cluster

import (
	"io"
	"log/slog"
	"testing"

	"skifity/internal/settings"
	"skifity/internal/store"
)

// autoDomainFixture builds the smallest world an app can live in: a team with
// one ready server, a project, an environment and an app.
func autoDomainFixture(t *testing.T) (*Cluster, *store.DB, store.App, store.Environment, string) {
	t.Helper()

	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	team := store.Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(t.Context(), &team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	project := store.Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(t.Context(), &project); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	env := store.Environment{
		ProjectID: project.ID, Name: "Production", Slug: "production",
		Namespace: "acme-shop-production",
	}
	if err := db.CreateEnvironment(t.Context(), &env); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	app := store.App{EnvironmentID: env.ID, Name: "Web", Slug: "web", SourceType: "git", Port: 3000}
	if err := db.CreateApp(t.Context(), &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	server := store.Server{
		TeamID: team.ID, Name: "web-1", Host: "203.0.113.10", SSHPort: 22, SSHUser: "root",
		Role: "control-plane", Status: store.ServerReady, ExternalIP: "203.0.113.10",
	}
	if err := db.CreateServer(t.Context(), &server); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	// No Kubernetes client: giving an app its address is a decision made from
	// settings and the database, and nothing here talks to a cluster.
	c := New(nil, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return c, db, app, env, team.ID
}

func autoDomainOf(t *testing.T, db *store.DB, appID string) store.Domain {
	t.Helper()
	domains, err := db.ListDomains(t.Context(), appID)
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	for _, domain := range domains {
		if domain.Auto {
			return domain
		}
	}
	t.Fatalf("the app has no automatic domain; it has %d domains", len(domains))
	return store.Domain{}
}

// TestAutoDomainFallsBackToSslip is the promise a fresh install makes: deploy
// an app on a server with nothing configured and it has a working address.
func TestAutoDomainFallsBackToSslip(t *testing.T) {
	c, db, app, env, teamID := autoDomainFixture(t)

	if err := c.EnsureAutoDomain(t.Context(), app, env, teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}

	domain := autoDomainOf(t, db, app.ID)
	if domain.Hostname != "web.203-0-113-10.sslip.io" {
		t.Fatalf("hostname is %q, want the sslip.io address of the server", domain.Hostname)
	}
	// ADR-0015: every install in the world shares sslip.io's rate limit, so no
	// certificate is asked for on that address.
	if domain.TLS {
		t.Error("a certificate was requested for the sslip.io address")
	}
	if domain.Status != "active" {
		t.Errorf("status is %q; with no certificate to wait for the address works immediately", domain.Status)
	}
}

// TestAutoDomainUsesTheWildcard covers the operator who owns a domain: their
// apps get a name under it, with HTTPS.
func TestAutoDomainUsesTheWildcard(t *testing.T) {
	c, db, app, env, teamID := autoDomainFixture(t)
	if err := db.SetSetting(t.Context(), settings.KeyWildcardDomain, "*.apps.example.com", false, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	if err := c.EnsureAutoDomain(t.Context(), app, env, teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}

	domain := autoDomainOf(t, db, app.ID)
	if domain.Hostname != "web.apps.example.com" {
		t.Fatalf("hostname is %q, want a name under the wildcard domain", domain.Hostname)
	}
	if !domain.TLS {
		t.Error("no certificate was requested for a domain the operator owns")
	}
}

// TestAutoDomainMovesWhenTheWildcardArrives: the settings that decide the
// address are usually filled in after the app already exists.
func TestAutoDomainMovesWhenTheWildcardArrives(t *testing.T) {
	c, db, app, env, teamID := autoDomainFixture(t)
	if err := c.EnsureAutoDomain(t.Context(), app, env, teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}
	if err := db.SetSetting(t.Context(), settings.KeyWildcardDomain, "apps.example.com", false, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := c.EnsureAutoDomain(t.Context(), app, env, teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}

	domains, err := db.ListDomains(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 1 {
		t.Fatalf("the app has %d domains, want the one automatic domain moved rather than a second one", len(domains))
	}
	if domains[0].Hostname != "web.apps.example.com" || !domains[0].TLS {
		t.Fatalf("automatic domain is %q (tls %v), want web.apps.example.com with a certificate",
			domains[0].Hostname, domains[0].TLS)
	}
}

// TestAutoDomainLeavesCustomDomainsAlone: a hostname somebody typed in is
// theirs, and a settings change must never rewrite it.
func TestAutoDomainLeavesCustomDomainsAlone(t *testing.T) {
	c, db, app, env, teamID := autoDomainFixture(t)
	custom := store.Domain{AppID: app.ID, Hostname: "shop.example.com", TLS: true}
	if err := db.CreateDomain(t.Context(), &custom); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	if err := c.EnsureAutoDomain(t.Context(), app, env, teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}

	domains, err := db.ListDomains(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 2 {
		t.Fatalf("the app has %d domains, want the custom one plus a new automatic one", len(domains))
	}
	for _, domain := range domains {
		if !domain.Auto && domain.Hostname != "shop.example.com" {
			t.Fatalf("the custom domain became %q", domain.Hostname)
		}
	}
}

// TestNoAddressMeansNoDomain: a hostname that resolves nowhere is worse than
// none, so a cluster with no known public address gives out no address.
func TestNoAddressMeansNoDomain(t *testing.T) {
	c, db, app, env, teamID := autoDomainFixture(t)
	servers, err := db.ListServers(t.Context(), teamID)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	for _, server := range servers {
		if err := db.DeleteServer(t.Context(), server.ID); err != nil {
			t.Fatalf("DeleteServer: %v", err)
		}
	}

	if err := c.EnsureAutoDomain(t.Context(), app, env, teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}

	domains, err := db.ListDomains(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 0 {
		t.Fatalf("invented %d domains with no address to point them at", len(domains))
	}
}

// TestClusterIPSettingWins: an operator behind a load balancer knows an
// address the panel cannot see from its own nodes.
func TestClusterIPSettingWins(t *testing.T) {
	c, db, app, env, teamID := autoDomainFixture(t)
	if err := db.SetSetting(t.Context(), settings.KeyClusterIP, "198.51.100.7", false, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	if err := c.EnsureAutoDomain(t.Context(), app, env, teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}

	if got := autoDomainOf(t, db, app.ID).Hostname; got != "web.198-51-100-7.sslip.io" {
		t.Fatalf("hostname is %q, want the configured cluster address", got)
	}
}
