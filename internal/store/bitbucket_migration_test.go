package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 0054 rebuilds git_sources so a connection can be to Bitbucket. Every app
// that builds from a connection points at it with ON DELETE SET NULL, so a
// rebuild that dropped the old table with foreign keys on would quietly
// disconnect every app from its repository's credentials: the next build of a
// private repository fails, and nothing says why.
func TestBitbucketConnectionsKeepEveryAppConnected(t *testing.T) {
	ctx := t.Context()
	sqlDB, err := sql.Open("sqlite", fileDSN(filepath.Join(t.TempDir(), "panel.db")))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	db := &DB{DB: sqlDB, path: "panel.db"}
	t.Cleanup(func() { db.Close() })

	if err := db.migrateUpTo(ctx, 53); err != nil {
		t.Fatalf("migrate to 53: %v", err)
	}
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	prj := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &prj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	env := Environment{ProjectID: prj.ID, Name: "Production", Slug: "production", Kind: EnvStandard, Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	github := GitSource{TeamID: team.ID, Kind: "github_pat", Name: "work", ConfigEnc: "SKF1.sealed"}
	if err := db.CreateGitSource(ctx, &github); err != nil {
		t.Fatalf("CreateGitSource: %v", err)
	}
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", SourceType: "git", GitSourceID: github.ID,
		RepoURL: "https://github.com/acme/web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	early := GitSource{TeamID: team.ID, Kind: "bitbucket", Name: "early"}
	if err := db.CreateGitSource(ctx, &early); err == nil {
		t.Fatal("the schema before 0054 already takes a Bitbucket connection, so this is not testing the rebuild")
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := db.GetApp(ctx, app.ID)
	if err != nil || got.GitSourceID != github.ID {
		t.Fatalf("the rebuild disconnected the app from its Git connection: %q, %v", got.GitSourceID, err)
	}
	kept, err := db.GetGitSource(ctx, github.ID)
	if err != nil || kept.ConfigEnc != "SKF1.sealed" || kept.Kind != "github_pat" || kept.Name != "work" {
		t.Fatalf("the connection did not survive the rebuild intact: %+v, %v", kept, err)
	}
	bitbucket := GitSource{TeamID: team.ID, Kind: "bitbucket", Name: "bitbucket", BaseURL: "https://bitbucket.org"}
	if err := db.CreateGitSource(ctx, &bitbucket); err != nil {
		t.Fatalf("a Bitbucket connection is still refused: %v", err)
	}

	// And the cascades work again: removing the connection disconnects the
	// app, as it did before.
	var on int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("foreign keys are %d after the rebuild (%v)", on, err)
	}
	if err := db.DeleteGitSource(ctx, github.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := db.GetApp(ctx, app.ID); err != nil || got.GitSourceID != "" {
		t.Fatalf("removing the connection left the app pointing at it: %q, %v", got.GitSourceID, err)
	}
}
