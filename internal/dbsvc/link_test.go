package dbsvc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"skifity/internal/api"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// linkHarness is an app and two databases in one environment, with no
// cluster: linking is rows and a sealed variable.
func linkHarness(t *testing.T) (*Manager, *store.DB, store.App, store.Database, store.Database) {
	t.Helper()
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, _ := crypto.GenerateKey()
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatal(err)
	}
	team := store.Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := store.Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := store.Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Kind: "standard", Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	app := store.App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}
	database := func(name string) store.Database {
		record := store.Database{EnvironmentID: env.ID, Name: name, Slug: name, Engine: EnginePostgres, Status: "running"}
		if err := db.CreateDatabase(ctx, &record); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(api.DatabaseCredentials{URL: "postgres://app:pw@" + name + ":5432/app"})
		sealed, err := keyring.Seal(raw, credentialsContext(record.ID))
		if err != nil {
			t.Fatal(err)
		}
		record.CredentialsEnc = sealed
		if err := db.UpdateDatabase(ctx, &record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	m := New(db, keyring, nil, nil, nil, slog.New(slog.DiscardHandler))
	return m, db, app, database("orders"), database("analytics")
}

func variableKeys(t *testing.T, db *store.DB, appID string) map[string]bool {
	t.Helper()
	rows, err := db.ListVariables(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, row := range rows {
		keys[row.Key] = true
	}
	return keys
}

// Linking a database again under another name moved the link and left the
// old variable — the full connection string — behind, belonging to no link:
// kept by every preview, and by the app after the database was unlinked.
func TestLinkingUnderANewNameTakesTheOldVariableAway(t *testing.T) {
	m, db, app, orders, _ := linkHarness(t)
	ctx := t.Context()
	if err := m.Link(ctx, orders.ID, app.ID, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	if err := m.Link(ctx, orders.ID, app.ID, "ORDERS_URL"); err != nil {
		t.Fatal(err)
	}
	if keys := variableKeys(t, db, app.ID); keys["DATABASE_URL"] || !keys["ORDERS_URL"] {
		t.Fatalf("the app's variables after renaming the link: %v", keys)
	}
}

// A second database linked under the first one's name overwrote its
// connection string, and unlinking either took the other's away.
func TestTwoDatabasesCannotShareOneVariable(t *testing.T) {
	m, db, app, orders, analytics := linkHarness(t)
	ctx := t.Context()
	if err := m.Link(ctx, orders.ID, app.ID, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	err := m.Link(ctx, analytics.ID, app.ID, "DATABASE_URL")
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "database.variable_taken" {
		t.Fatalf("a second database under the same name: %v", err)
	}
	links, _ := db.ListLinksForApp(ctx, app.ID)
	if len(links) != 1 || links[0].DatabaseID != orders.ID {
		t.Fatalf("the links are %+v", links)
	}
	if err := m.Link(ctx, analytics.ID, app.ID, "ANALYTICS_URL"); err != nil {
		t.Fatalf("under a name of its own: %v", err)
	}
}
