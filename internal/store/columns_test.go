package store

import (
	"testing"
)

// Every column list, qualified the way a join qualifies it, run against the
// real schema. prefixColumns once cut the COALESCE in appColumns in two at its
// comma, and the two queries built on it failed on every call for as long as
// they existed, with nothing in the tests to say so.
func TestEveryColumnListQualifies(t *testing.T) {
	db, err := OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for table, columns := range map[string]string{
		"apps":             appColumns,
		"databases":        databaseColumns,
		"deployments":      deploymentColumns,
		"team_invitations": invitationColumns,
		"plugins":          pluginColumns,
		"servers":          serverColumns,
		"projects":         projectColumns,
		"environments":     envColumns,
		"users":            userColumns,
	} {
		for _, query := range []string{
			`SELECT ` + columns + ` FROM ` + table + ` LIMIT 0`,
			`SELECT ` + prefixColumns("x", columns) + ` FROM ` + table + ` x LIMIT 0`,
		} {
			rows, err := db.QueryContext(t.Context(), query)
			if err != nil {
				t.Errorf("%s: %v\n%s", table, err, query)
				continue
			}
			if err := rows.Err(); err != nil {
				t.Errorf("%s: %v", table, err)
			}
			rows.Close()
		}
	}
}

func TestPrefixColumnsQualifiesTheColumnInsideAnExpression(t *testing.T) {
	got := prefixColumns("a", "id, COALESCE(git_source_id,''), name")
	if want := "a.id, COALESCE(a.git_source_id,''), a.name"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The two queries that were broken, doing what they are for.
func TestTheQueriesBuiltOnAQualifiedAppListAnswer(t *testing.T) {
	db, err := OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := Environment{ProjectID: project.ID, Name: "production", Slug: "production", Namespace: "shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	deployed := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	fresh := App{EnvironmentID: env.ID, Name: "worker", Slug: "worker", Replicas: 1}
	for _, app := range []*App{&deployed, &fresh} {
		if err := db.CreateApp(ctx, app); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetAppStatus(ctx, deployed.ID, "running"); err != nil {
		t.Fatal(err)
	}

	apps, err := db.ListAppsForProject(ctx, project.ID)
	if err != nil || len(apps) != 2 {
		t.Fatalf("ListAppsForProject: %d apps, %v", len(apps), err)
	}
	running, err := db.ListDeployedApps(ctx)
	if err != nil || len(running) != 1 || running[0].ID != deployed.ID ||
		running[0].TeamID != team.ID || running[0].Namespace != env.Namespace {
		t.Fatalf("ListDeployedApps: %+v, %v", running, err)
	}
}
