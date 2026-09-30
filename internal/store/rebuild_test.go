package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A migration that rebuilds a table drops the old one, and with foreign keys on
// that drop is a delete of every row, with every ON DELETE CASCADE firing. The
// migration would report success and every app's deployments, variables and
// domains would be gone.
//
// This puts rows into the schema as it was before 0016, runs 0016, and checks
// they are all still there — then checks the cascades work again afterwards,
// because a connection handed back to the pool with foreign keys still off is
// the same bug waiting for the next delete.
func TestRebuildingTheAppsTableKeepsWhatReferencesIt(t *testing.T) {
	ctx := t.Context()
	sqlDB, err := sql.Open("sqlite", fileDSN(filepath.Join(t.TempDir(), "panel.db")))
	if err != nil {
		t.Fatal(err)
	}
	// One connection, so the one the rebuild used is the one every later query
	// gets, and a pragma left off would show.
	sqlDB.SetMaxOpenConns(1)
	db := &DB{DB: sqlDB, path: "panel.db"}
	t.Cleanup(func() { db.Close() })

	if err := db.migrateUpTo(ctx, 15); err != nil {
		t.Fatalf("migrate to 15: %v", err)
	}
	_, _, prj, env := seedTeam(t, db)
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1, RepoURL: "https://example.test/a.git"}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	v := Variable{AppID: app.ID, Key: "PORT"}
	if err := db.SetVariable(ctx, &v, "SKF1.sealed"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	d := Deployment{AppID: app.ID}
	if err := db.CreateDeployment(ctx, &d); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	// Before 0016 there is no such source.
	early := App{EnvironmentID: env.ID, Name: "early", Slug: "early", SourceType: "upload", Replicas: 1}
	if err := db.CreateApp(ctx, &early); err == nil {
		t.Fatal("the schema before 0016 already accepts an upload, so this test is not testing the rebuild")
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := db.GetApp(ctx, app.ID)
	if err != nil || got.RepoURL != app.RepoURL || got.Slug != "web" {
		t.Fatalf("the app did not survive the rebuild intact: %+v, %v", got, err)
	}
	if _, err := db.GetDeployment(ctx, d.ID); err != nil {
		t.Fatalf("the rebuild took the app's deployment with it: %v", err)
	}
	if vars, err := db.ListVariables(ctx, app.ID); err != nil || len(vars) != 1 {
		t.Fatalf("the rebuild took the app's variables with it: %d, %v", len(vars), err)
	}

	uploaded := App{EnvironmentID: env.ID, Name: "folder", Slug: "folder", SourceType: "upload", Replicas: 1}
	if err := db.CreateApp(ctx, &uploaded); err != nil {
		t.Fatalf("an app from an upload is still refused: %v", err)
	}

	var on int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("foreign keys are %d after the rebuild (%v); every cascade is off", on, err)
	}
	if err := db.DeleteProject(ctx, prj.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetDeployment(ctx, d.ID); err == nil {
		t.Fatal("deleting the project no longer removes its deployments")
	}
}
