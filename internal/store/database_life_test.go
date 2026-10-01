package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// 0056 rebuilds the backups table to take a skipped backup, and every row and
// column it had survives it; a database's memory limit is its engine's.
func TestTheBackupsTableIsRebuiltWithEveryRow(t *testing.T) {
	ctx := t.Context()
	sqlDB, err := sql.Open("sqlite", fileDSN(filepath.Join(t.TempDir(), "panel.db")))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	db := &DB{DB: sqlDB, path: "panel.db"}
	t.Cleanup(func() { db.Close() })
	if err := db.migrateUpTo(ctx, 54); err != nil {
		t.Fatalf("migrate to 54: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO backups (id, target_type, target_id, status, kind, location, size_bytes,
		error_message, created_at, finished_at, encrypted, verified_at, verify_error)
		VALUES ('bak_1', 'database', 'db_1', 'succeeded', 'manual', 'skifity/x.gz', 42, '', ?, ?, 1, ?, '')`,
		Now(), Now(), Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO backups (id, target_type, target_id, status, created_at)
		VALUES ('bak_2', 'database', 'db_1', 'skipped', ?)`, Now()); err == nil {
		t.Fatal("the schema before 0056 already takes a skipped backup, so this is not testing the rebuild")
	}
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Kind: EnvStandard, Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []string{"clickhouse", "postgres"} {
		if _, err := db.Exec(ctx, `INSERT INTO databases (id, environment_id, name, slug, engine, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?)`, "db_"+engine, env.ID, engine, engine, engine, Now(), Now()); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	backup, err := db.GetBackup(ctx, "bak_1")
	if err != nil || backup.SizeBytes != 42 || !backup.Encrypted || backup.VerifiedAt.IsZero() || backup.Location != "skifity/x.gz" {
		t.Fatalf("the backup after the rebuild: %+v, %v", backup, err)
	}
	skipped := Backup{TargetType: "database", TargetID: "db_1", ErrorMessage: "stopped"}
	if err := db.RecordSkippedBackup(ctx, &skipped); err != nil {
		t.Fatalf("a skipped backup after 0056: %v", err)
	}
	for engine, want := range map[string]int{"clickhouse": 2048, "postgres": 1024} {
		record, err := db.GetDatabase(ctx, "db_"+engine)
		if err != nil || record.MemLimitMB != want || record.CPULimitM != 0 {
			t.Errorf("%s after 0056: %+v, %v", engine, record, err)
		}
	}
}

func lifeDatabase(t *testing.T) (*DB, Database) {
	t.Helper()
	db := testDB(t)
	ctx := t.Context()
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Kind: EnvStandard, Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	record := Database{EnvironmentID: env.ID, Name: "orders", Slug: "orders", Engine: "mysql", Status: "running",
		CredentialsEnc: "SKF1.old", CPURequestM: 100, MemRequestMB: 256, MemLimitMB: 1024, StorageGB: 5}
	if err := db.CreateDatabase(ctx, &record); err != nil {
		t.Fatal(err)
	}
	return db, record
}

// The new credentials wait beside the old ones, one change at a time, and
// become the database's own in one statement.
func TestNewCredentialsWaitBesideTheOldOnes(t *testing.T) {
	db, record := lifeDatabase(t)
	ctx := t.Context()
	if err := db.BeginDatabaseCredentials(ctx, record.ID, "SKF1.next"); err != nil {
		t.Fatal(err)
	}
	if err := db.BeginDatabaseCredentials(ctx, record.ID, "SKF1.other"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second change over the first: %v", err)
	}
	waiting, err := db.ListDatabasesChangingPassword(ctx)
	if err != nil || len(waiting) != 1 || waiting[0].CredentialsNextEnc != "SKF1.next" {
		t.Fatalf("the databases changing password: %+v, %v", waiting, err)
	}
	// A resize meanwhile writes only what it changes.
	if err := db.SetDatabaseResources(ctx, record.ID, 250, 0, 512, 2048, 20); err != nil {
		t.Fatal(err)
	}
	if err := db.CommitDatabaseCredentials(ctx, record.ID, "SKF1.new"); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetDatabase(ctx, record.ID)
	if got.CredentialsEnc != "SKF1.new" || got.CredentialsNextEnc != "" {
		t.Errorf("after the change: %q waiting %q", got.CredentialsEnc, got.CredentialsNextEnc)
	}
	if got.CPURequestM != 250 || got.MemLimitMB != 2048 || got.StorageGB != 20 || got.CPULimitM != 0 {
		t.Errorf("the resize was lost: %+v", got)
	}
	if err := db.BeginDatabaseCredentials(ctx, record.ID, "SKF1.again"); err != nil {
		t.Fatal(err)
	}
	if err := db.AbandonDatabaseCredentials(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetDatabase(ctx, record.ID)
	if got.CredentialsEnc != "SKF1.new" || got.CredentialsNextEnc != "" {
		t.Errorf("after giving up: %q waiting %q", got.CredentialsEnc, got.CredentialsNextEnc)
	}
	if err := db.BeginDatabaseCredentials(ctx, "db_missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a database that does not exist: %v", err)
	}
}

// A skipped backup is a status the table takes since 0056, and a target
// keeps only its newest.
func TestOnlyTheNewestSkippedBackupIsKept(t *testing.T) {
	db, record := lifeDatabase(t)
	ctx := t.Context()
	taken := Backup{TargetType: "database", TargetID: record.ID, Status: "succeeded", Kind: "scheduled"}
	if err := db.CreateBackup(ctx, &taken); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		skipped := Backup{TargetType: "database", TargetID: record.ID, Kind: "scheduled", ErrorMessage: "stopped"}
		if err := db.RecordSkippedBackup(ctx, &skipped); err != nil {
			t.Fatal(err)
		}
		if skipped.Status != "skipped" || skipped.FinishedAt.IsZero() {
			t.Errorf("recorded as %+v", skipped)
		}
	}
	backups, err := db.ListBackups(ctx, "database", record.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]int{}
	for _, backup := range backups {
		statuses[backup.Status]++
	}
	if statuses["skipped"] != 1 || statuses["succeeded"] != 1 {
		t.Errorf("the backups are %v", statuses)
	}
	// A skipped one is no backup: retention counts only what succeeded.
	expired, err := db.ExpiredBackups(ctx, "database", record.ID, 0, true)
	if err != nil || len(expired) != 1 || expired[0].ID != taken.ID {
		t.Errorf("retention sees %+v, %v", expired, err)
	}
}

func TestADatabasesOperationsAreItsHistory(t *testing.T) {
	db, record := lifeDatabase(t)
	ctx := t.Context()
	teamID, err := db.TeamIDForDatabase(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"database.restore", "database.import"} {
		op := Operation{TeamID: teamID, Kind: kind, TargetType: "database", TargetID: record.ID}
		if err := db.CreateOperation(ctx, &op, []string{"prepare"}); err != nil {
			t.Fatal(err)
		}
		if err := db.SetOperationStatus(ctx, op.ID, OpSucceeded, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	other := Operation{TeamID: teamID, Kind: "server.add", TargetType: "server", TargetID: "srv_1"}
	if err := db.CreateOperation(ctx, &other, []string{"connect"}); err != nil {
		t.Fatal(err)
	}
	history, err := db.ListOperationsForTarget(ctx, "database", record.ID, 10)
	if err != nil || len(history) != 2 || len(history[0].Steps) != 1 {
		t.Fatalf("the history is %+v, %v", history, err)
	}
	if running, err := db.OperationRunningFor(ctx, "database", record.ID); err != nil || running {
		t.Errorf("finished operations read as running: %v %v", running, err)
	}
	if running, _ := db.OperationRunningFor(ctx, "server", "srv_1"); !running {
		t.Error("a pending operation does not read as running")
	}
}
