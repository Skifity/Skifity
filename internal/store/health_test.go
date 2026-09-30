package store

import (
	"database/sql"
	"testing"
)

// An app that existed before its health check could be chosen keeps the one
// it was rendered with: HTTP where it had a path, a connect where it did not,
// two minutes to start and three seconds to answer. Upgrading changes no
// Deployment.
func TestHealthChecksStartAsWhatEachAppAlreadyHad(t *testing.T) {
	ctx := t.Context()
	// Without foreign keys, so an app row can be written as the schema had
	// it without a team, a project and an environment written the same way.
	sqlDB, err := sql.Open("sqlite", ":memory:?_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	db := &DB{DB: sqlDB, path: ":memory:"}
	t.Cleanup(func() { db.Close() })

	if err := db.migrateUpTo(ctx, 41); err != nil {
		t.Fatalf("migrate to 41: %v", err)
	}
	// A migration that rebuilds a table turns them back on when it is done.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	now := Now()
	for _, row := range []struct{ id, slug, path string }{
		{"app_checked", "checked", "/healthz"},
		{"app_connected", "connected", ""},
	} {
		if _, err := db.Exec(ctx, `INSERT INTO apps
			(id, environment_id, name, slug, source_type, repo_url, health_path, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			row.id, "env_1", row.slug, row.slug, "git", "https://example.test/a.git", row.path, now, now); err != nil {
			t.Fatalf("insert %s: %v", row.slug, err)
		}
	}

	// Up to this migration only: a later one that rebuilds a table checks
	// every foreign key, and these rows have no environment.
	if err := db.migrateUpTo(ctx, 42); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for id, want := range map[string]string{"app_checked": "http", "app_connected": "tcp"} {
		var check string
		var start, timeout int
		if err := db.QueryRowContext(ctx, `SELECT health_check, health_start_seconds, health_timeout_seconds
			FROM apps WHERE id = ?`, id).Scan(&check, &start, &timeout); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if check != want || start != 120 || timeout != 3 {
			t.Errorf("%s came out of the migration as %q, %ds, %ds; want %q, 120s, 3s",
				id, check, start, timeout, want)
		}
	}
}

// An app made by anything that does not know the settings exist — a
// template, a blueprint, an older client — gets the same defaults.
func TestAnAppMadeWithoutHealthSettingsGetsTheDefaults(t *testing.T) {
	db := testDB(t)
	_, _, _, env := seedTeam(t, db)
	for slug, path := range map[string]string{"checked": "/up", "connected": ""} {
		want := map[string]string{"/up": "http", "": "tcp"}[path]
		app := App{EnvironmentID: env.ID, Name: slug, Slug: slug, SourceType: "image", Image: "nginx", HealthPath: path}
		if err := db.CreateApp(t.Context(), &app); err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		got, err := db.GetApp(t.Context(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.HealthCheck != want || got.HealthStartSeconds != 120 || got.HealthTimeoutSeconds != 3 {
			t.Errorf("path %q: made as %q, %ds, %ds", path, got.HealthCheck, got.HealthStartSeconds, got.HealthTimeoutSeconds)
		}
	}
}

// The rollback dialog says a health check will change, from what to what, and
// a version recorded before the settings existed goes back to the defaults it
// ran with rather than to zero.
func TestARollbackSaysItPutsTheHealthCheckBack(t *testing.T) {
	now := App{HealthPath: "/healthz", HealthCheck: "none", HealthStartSeconds: 600, HealthTimeoutSeconds: 10}

	changes, err := RollbackChanges(now, `{"health_path":"/healthz","health_check":"http","health_start_seconds":120,"health_timeout_seconds":3}`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SpecChange{}
	for _, c := range changes {
		got[c.Field] = c
	}
	for field, want := range map[string][2]string{
		"health_check":           {"none", "http"},
		"health_start_seconds":   {"600", "120"},
		"health_timeout_seconds": {"10", "3"},
	} {
		if c, ok := got[field]; !ok || c.From != want[0] || c.To != want[1] {
			t.Errorf("%s: %+v, want %s → %s", field, c, want[0], want[1])
		}
	}

	// Recorded before: the same answer from what that version really had.
	changes, err = RollbackChanges(now, `{"health_path":"/healthz"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.To == "0" || (c.Field == "health_check" && c.To == "") {
			t.Errorf("an old version would put %s back as %q", c.Field, c.To)
		}
	}
}
