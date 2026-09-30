package store

import (
	"context"
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
	prj, env := seedAtVersion15(t, db)
	app := App{ID: NewID("app"), EnvironmentID: env.ID, Name: "web", Slug: "web", SourceType: "git", RepoURL: "https://example.test/a.git"}
	if err := insertAppAtVersion15(ctx, db, app); err != nil {
		t.Fatalf("insert the app: %v", err)
	}
	v := Variable{AppID: app.ID, Key: "PORT"}
	if err := db.SetVariable(ctx, &v, "SKF1.sealed"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	d := Deployment{ID: NewID("dep"), AppID: app.ID}
	if err := insertDeploymentAtVersion15(ctx, db, d); err != nil {
		t.Fatalf("insert the deployment: %v", err)
	}
	// Before 0016 there is no such source.
	early := App{ID: NewID("app"), EnvironmentID: env.ID, Name: "early", Slug: "early", SourceType: "upload"}
	if err := insertAppAtVersion15(ctx, db, early); err == nil {
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

// insertAppAtVersion15 writes an app by hand, for the same reason: CreateApp
// writes every column apps has today.
func insertAppAtVersion15(ctx context.Context, db *DB, a App) error {
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO apps
		(id, environment_id, name, slug, source_type, repo_url, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		a.ID, a.EnvironmentID, a.Name, a.Slug, a.SourceType, a.RepoURL, now, now)
	return err
}

// insertDeploymentAtVersion15 writes a deployment the way the schema before
// 0016 takes one, for the reason the environment below is written by hand.
func insertDeploymentAtVersion15(ctx context.Context, db *DB, d Deployment) error {
	_, err := db.Exec(ctx, `INSERT INTO deployments (id, app_id, number, status, created_at)
		VALUES (?,?,1,'queued',?)`, d.ID, d.AppID, Now())
	return err
}

// seedAtVersion15 makes a team, a project and an environment in the schema as
// it was before 0016. The environment is written by hand because the
// repository function writes every column the table has today, and a column
// added by a later migration does not exist yet at this point.
func seedAtVersion15(t *testing.T, db *DB) (Project, Environment) {
	t.Helper()
	ctx := t.Context()
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	prj := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &prj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	env := Environment{ID: NewID("env"), ProjectID: prj.ID, Name: "Production", Slug: "production",
		Kind: EnvStandard, Namespace: "acme-shop-production", PodSecurity: "restricted"}
	if _, err := db.Exec(ctx, `INSERT INTO environments
		(id, project_id, name, slug, kind, namespace, source_ref, pod_security, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		env.ID, env.ProjectID, env.Name, env.Slug, env.Kind, env.Namespace, "", env.PodSecurity, Now()); err != nil {
		t.Fatalf("insert the environment: %v", err)
	}
	return prj, env
}
