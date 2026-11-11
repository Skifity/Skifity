package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// A backup schedule goes with its database or volume, whichever way that
// goes: deleted itself, or taken by a cascade from its app, its environment,
// its project or its team. backup_policies has no foreign key to cascade
// from, and before 0064 nothing removed a schedule, so a deleted database's
// came round every night.
func TestABackupScheduleGoesWithWhatItBacksUp(t *testing.T) {
	ctx := t.Context()
	db, err := OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	newEnv := func(slug string) Environment {
		env := Environment{ProjectID: project.ID, Name: slug, Slug: slug, Kind: EnvStandard, Namespace: "acme-shop-" + slug}
		if err := db.CreateEnvironment(ctx, &env); err != nil {
			t.Fatal(err)
		}
		return env
	}
	newDatabase := func(env Environment, name string) Database {
		record := Database{EnvironmentID: env.ID, Name: name, Slug: name, Engine: "postgres"}
		if err := db.CreateDatabase(ctx, &record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	newVolume := func(env Environment, appSlug string) (App, Volume) {
		app := App{EnvironmentID: env.ID, Name: appSlug, Slug: appSlug, Replicas: 1}
		if err := db.CreateApp(ctx, &app); err != nil {
			t.Fatal(err)
		}
		volume := Volume{AppID: app.ID, Name: "data", MountPath: "/data", SizeGB: 1}
		if err := db.CreateVolume(ctx, &volume); err != nil {
			t.Fatal(err)
		}
		return app, volume
	}
	schedule := func(targetType, targetID string) {
		if err := db.SetBackupPolicy(ctx, &BackupPolicy{TargetType: targetType, TargetID: targetID,
			Schedule: "0 3 * * *", Retention: 7, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	gone := func(what, targetType, targetID string) {
		t.Helper()
		if _, err := db.GetBackupPolicy(ctx, targetType, targetID); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: the schedule is still there (%v)", what, err)
		}
	}

	production := newEnv("production")
	kept := newDatabase(production, "kept")
	schedule("database", kept.ID)

	deleted := newDatabase(production, "orders")
	schedule("database", deleted.ID)
	if err := db.DeleteDatabase(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	gone("a deleted database", "database", deleted.ID)

	_, volume := newVolume(production, "web")
	schedule("volume", volume.ID)
	if err := db.DeleteVolume(ctx, volume.AppID, volume.ID); err != nil {
		t.Fatal(err)
	}
	gone("a deleted volume", "volume", volume.ID)

	app, appVolume := newVolume(production, "blog")
	schedule("volume", appVolume.ID)
	if err := db.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	gone("the volume of a deleted app", "volume", appVolume.ID)

	staging := newEnv("staging")
	stagingDatabase := newDatabase(staging, "orders")
	schedule("database", stagingDatabase.ID)
	_, stagingVolume := newVolume(staging, "web")
	schedule("volume", stagingVolume.ID)
	if err := db.DeleteEnvironment(ctx, staging.ID); err != nil {
		t.Fatal(err)
	}
	gone("the database of a deleted environment", "database", stagingDatabase.ID)
	gone("the volume of a deleted environment", "volume", stagingVolume.ID)

	if _, err := db.GetBackupPolicy(ctx, "database", kept.ID); err != nil {
		t.Errorf("a database still there lost its schedule: %v", err)
	}
}

// 0064 deletes the schedules already left behind, and keeps every other.
func TestTheSchedulesLeftBehindAreDeletedOnUpgrade(t *testing.T) {
	ctx := t.Context()
	sqlDB, err := sql.Open("sqlite", fileDSN(filepath.Join(t.TempDir(), "panel.db")))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	db := &DB{DB: sqlDB, path: "panel.db"}
	t.Cleanup(func() { db.Close() })
	if err := db.migrateUpTo(ctx, 63); err != nil {
		t.Fatalf("migrate to 63: %v", err)
	}

	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Kind: EnvStandard,
		Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO databases (id, environment_id, name, slug, engine, created_at, updated_at)
		VALUES ('db_live', ?, 'live', 'live', 'postgres', ?, ?)`, env.ID, Now(), Now()); err != nil {
		t.Fatal(err)
	}
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO volumes (id, app_id, name, mount_path, created_at)
		VALUES ('vol_live', ?, 'data', '/data', ?)`, app.ID, Now()); err != nil {
		t.Fatal(err)
	}
	for _, target := range [][2]string{
		{"database", "db_live"}, {"database", "db_gone"},
		{"volume", "vol_live"}, {"volume", "vol_gone"},
	} {
		if _, err := db.Exec(ctx, `INSERT INTO backup_policies (id, target_type, target_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`, NewID("bkp"), target[0], target[1], Now(), Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO backups (id, target_type, target_id, status, error_message, created_at)
		VALUES ('bak_skipped', 'database', 'db_live', 'skipped', 'stopped', ?)`, Now()); err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, target := range [][2]string{{"database", "db_live"}, {"volume", "vol_live"}} {
		if _, err := db.GetBackupPolicy(ctx, target[0], target[1]); err != nil {
			t.Errorf("the schedule of %s went: %v", target[1], err)
		}
	}
	for _, target := range [][2]string{{"database", "db_gone"}, {"volume", "vol_gone"}} {
		if _, err := db.GetBackupPolicy(ctx, target[0], target[1]); !errors.Is(err, ErrNotFound) {
			t.Errorf("the schedule of %s, which is gone, is still there: %v", target[1], err)
		}
	}
	// A skip before 0064 was always a stopped database's.
	if skipped, err := db.GetBackup(ctx, "bak_skipped"); err != nil || skipped.ErrorCode != "database.stopped" {
		t.Errorf("the skipped backup is %+v, %v", skipped, err)
	}
}
