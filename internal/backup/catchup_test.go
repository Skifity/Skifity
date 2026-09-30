package backup

import (
	"context"
	"strings"
	"testing"
	"time"

	"skifity/internal/notify"
	"skifity/internal/store"
)

// A backup due while the panel was stopped.
//
// The minute tick catches up only on minutes it lived through, so a panel
// restarted across 03:00 skipped the nightly backup until 03:00 the next night,
// and nothing said so.

// catchUpDatabase is a running database in the harness's team, with a nightly
// policy last changed well before now.
func catchUpDatabase(t *testing.T, db *store.DB, teamID, name string) store.Database {
	t.Helper()
	ctx := context.Background()
	project := store.Project{TeamID: teamID, Name: name, Slug: name}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := store.Environment{ProjectID: project.ID, Name: "Production", Slug: "production",
		Kind: "standard", Namespace: "t-" + name + "-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	record := store.Database{EnvironmentID: env.ID, Name: name, Slug: name, Engine: "postgres", Status: "running"}
	if err := db.CreateDatabase(ctx, &record); err != nil {
		t.Fatal(err)
	}
	policy := store.BackupPolicy{TargetType: "database", TargetID: record.ID, Schedule: "0 3 * * *",
		Retention: 7, Enabled: true}
	if err := db.SetBackupPolicy(ctx, &policy); err != nil {
		t.Fatal(err)
	}
	backdate(t, db, "backup_policies", policy.ID, time.Now().Add(-30*24*time.Hour))
	return record
}

func backdate(t *testing.T, db *store.DB, table, id string, at time.Time) {
	t.Helper()
	column := "updated_at"
	if table == "backups" {
		column = "created_at"
	}
	if _, err := db.Exec(context.Background(),
		`UPDATE `+table+` SET `+column+` = ? WHERE id = ?`, store.FormatTime(at), id); err != nil {
		t.Fatal(err)
	}
}

func TestABackupDueWhileThePanelWasDownIsTakenLateAndSaid(t *testing.T) {
	m, db, _, notifier, teamID := panelHarness(t)
	ctx := context.Background()

	// 10:00, so today's 03:00 is the time each was last due.
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(10 * time.Hour)
	due := now.Truncate(24 * time.Hour).Add(3 * time.Hour)

	// Missed: its last backup is from the night before.
	missed := catchUpDatabase(t, db, teamID, "shop")
	old := store.Backup{TargetType: "database", TargetID: missed.ID, Status: "succeeded"}
	if err := db.CreateBackup(ctx, &old); err != nil {
		t.Fatal(err)
	}
	backdate(t, db, "backups", old.ID, due.Add(-24*time.Hour))

	// Not missed: one was taken at 03:00, even though it failed — that was
	// reported when it failed.
	taken := catchUpDatabase(t, db, teamID, "blog")
	tonight := store.Backup{TargetType: "database", TargetID: taken.ID, Status: "failed"}
	if err := db.CreateBackup(ctx, &tonight); err != nil {
		t.Fatal(err)
	}
	backdate(t, db, "backups", tonight.ID, due.Add(20*time.Second))

	// Not missed: its schedule was set after 03:00, so it was never due.
	fresh := catchUpDatabase(t, db, teamID, "wiki")
	policy, err := db.GetBackupPolicy(ctx, "database", fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	backdate(t, db, "backup_policies", policy.ID, due.Add(time.Hour))

	got := m.CatchUp(ctx, now)
	if len(got) != 1 || got[0].Policy.TargetID != missed.ID || !got[0].Due.Equal(due) {
		t.Fatalf("caught up on %+v, want only shop's %s backup", got, due)
	}

	// The harness has no cluster, so the late backup cannot start; the team
	// is told it was missed and why it could not be taken now.
	var notices []recordedNotice
	for _, n := range notifier.notices {
		if n.event == notify.EventBackupMissed {
			notices = append(notices, n)
		}
	}
	if len(notices) != 1 || notices[0].team != teamID || !strings.Contains(notices[0].title, "shop") {
		t.Fatalf("notices = %+v, want one backup.missed about shop", notifier.notices)
	}

	// And a second restart does not catch up on the same one again once a
	// backup has been started for it.
	late := store.Backup{TargetType: "database", TargetID: missed.ID, Status: "running"}
	if err := db.CreateBackup(ctx, &late); err != nil {
		t.Fatal(err)
	}
	backdate(t, db, "backups", late.ID, now)
	if again := m.CatchUp(ctx, now.Add(5*time.Minute)); len(again) != 0 {
		t.Fatalf("caught up twice: %+v", again)
	}
}

// Now's own minute belongs to the tick, which takes it on time: catching up
// on it as well would take the same backup twice.
func TestCatchingUpLeavesThisMinuteToTheTick(t *testing.T) {
	now := time.Date(2026, 3, 4, 3, 0, 30, 0, time.UTC)
	policy := store.BackupPolicy{Schedule: "0 3 * * *", UpdatedAt: now.Add(-48 * time.Hour)}
	due, ok := lastDue(policy, now)
	if !ok || !due.Equal(time.Date(2026, 3, 3, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("last due = %s, %v; want yesterday's", due, ok)
	}
	// A schedule nothing matched within the horizon is not missed.
	policy.Schedule = "0 3 29 2 *"
	if _, ok := lastDue(policy, now); ok {
		t.Fatal("a schedule that was not due in the last week was caught up on")
	}
}
