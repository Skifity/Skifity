package cluster

import (
	"io"
	"log/slog"
	"testing"

	"skifity/internal/settings"
	"skifity/internal/store"
)

// twoProjects builds a team with two projects, each holding an app of the given
// slug in Production.
func twoProjects(t *testing.T, appSlug string) (*Cluster, *store.DB, string, []store.App, []store.Environment) {
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

	var apps []store.App
	var envs []store.Environment
	for _, projectSlug := range []string{"shop", "blog"} {
		project := store.Project{TeamID: team.ID, Name: projectSlug, Slug: projectSlug}
		if err := db.CreateProject(t.Context(), &project); err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		env := store.Environment{
			ProjectID: project.ID, Name: "Production", Slug: "production",
			Namespace: "acme-" + projectSlug + "-production",
		}
		if err := db.CreateEnvironment(t.Context(), &env); err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		app := store.App{
			EnvironmentID: env.ID, Name: appSlug, Slug: appSlug,
			SourceType: "git", Port: 3000,
		}
		if err := db.CreateApp(t.Context(), &app); err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		apps = append(apps, app)
		envs = append(envs, env)
	}

	c := New(nil, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return c, db, team.ID, apps, envs
}

func onlyDomain(t *testing.T, db *store.DB, appID string) store.Domain {
	t.Helper()
	domains, err := db.ListDomains(t.Context(), appID)
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 1 {
		t.Fatalf("expected exactly one domain; got %d", len(domains))
	}
	return domains[0]
}

// The defect: "web", "api" and "app" are what people call things, so two
// projects wanting the same address is the usual case and not a rare one. The
// second app used to get no address at all — the unique constraint refused the
// row and the deployer logged a warning nobody reads.
func TestTwoAppsWantingTheSameAddressBothGetOne(t *testing.T) {
	c, db, teamID, apps, envs := twoProjects(t, "web")
	if err := db.SetSetting(t.Context(), settings.KeyWildcardDomain, "apps.example.com", false, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	for i := range apps {
		if err := c.EnsureAutoDomain(t.Context(), apps[i], envs[i], teamID); err != nil {
			t.Fatalf("app %d got no address: %v", i, err)
		}
	}

	first := onlyDomain(t, db, apps[0].ID)
	second := onlyDomain(t, db, apps[1].ID)

	if first.Hostname != "web.apps.example.com" {
		t.Errorf("the first app should keep the readable name; got %q", first.Hostname)
	}
	if second.Hostname == first.Hostname {
		t.Fatal("both apps were given the same address")
	}
	if second.Hostname == "" {
		t.Fatal("the second app has no address")
	}
}

// An address must not move under the user. The app that took the longer name
// because a sibling held the short one must keep it when that sibling goes.
func TestAnAddressDoesNotChangeOnALaterDeploy(t *testing.T) {
	c, db, teamID, apps, envs := twoProjects(t, "web")
	if err := db.SetSetting(t.Context(), settings.KeyWildcardDomain, "apps.example.com", false, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	for i := range apps {
		if err := c.EnsureAutoDomain(t.Context(), apps[i], envs[i], teamID); err != nil {
			t.Fatalf("EnsureAutoDomain: %v", err)
		}
	}
	second := onlyDomain(t, db, apps[1].ID)

	// The first app, and with it the short name, is deleted.
	if err := db.DeleteApp(t.Context(), apps[0].ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if err := c.EnsureAutoDomain(t.Context(), apps[1], envs[1], teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}
	if got := onlyDomain(t, db, apps[1].ID); got.Hostname != second.Hostname {
		t.Errorf("the address moved from %q to %q on an unrelated deploy", second.Hostname, got.Hostname)
	}

	// And running it again changes nothing at all, which is what every deploy
	// after the first one does.
	if err := c.EnsureAutoDomain(t.Context(), apps[1], envs[1], teamID); err != nil {
		t.Fatalf("EnsureAutoDomain: %v", err)
	}
	if got := onlyDomain(t, db, apps[1].ID); got.Hostname != second.Hostname {
		t.Errorf("a repeated deploy moved the address to %q", got.Hostname)
	}
}

// The same has to hold with no wildcard domain, where the address is an
// sslip.io name built from the cluster's IP.
func TestTheSslipFallbackAlsoAvoidsACollision(t *testing.T) {
	c, db, teamID, apps, envs := twoProjects(t, "api")
	if err := db.SetSetting(t.Context(), settings.KeyClusterIP, "203.0.113.10", false, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	for i := range apps {
		if err := c.EnsureAutoDomain(t.Context(), apps[i], envs[i], teamID); err != nil {
			t.Fatalf("app %d got no address: %v", i, err)
		}
	}
	first := onlyDomain(t, db, apps[0].ID)
	second := onlyDomain(t, db, apps[1].ID)
	if first.Hostname == second.Hostname {
		t.Fatalf("both apps were given %q", first.Hostname)
	}
	for _, d := range []store.Domain{first, second} {
		if d.TLS {
			t.Errorf("%q asked for a certificate; sslip.io names deliberately do not", d.Hostname)
		}
	}
}
