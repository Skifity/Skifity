package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"skifity/internal/cluster"
	"skifity/internal/kube"
	"skifity/internal/notify"
	"skifity/internal/plugins"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// A scheduled backup that could not start used to be a line in the log and
// nothing else: no row on the Backups tab, no notification, and on the next
// start a night the panel believed it had slept through.

// heardEvents is a plugin dispatcher that counts the events it is asked to
// deliver, and delivers them to nobody.
type heardEvents struct {
	mu     sync.Mutex
	events []string
}

func (h *heardEvents) dispatcher() plugins.Dispatcher {
	return plugins.Dispatcher{Targets: func(_ context.Context, event, _ string) ([]plugins.Target, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.events = append(h.events, event)
		return nil, nil
	}}
}

func (h *heardEvents) count(event string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.events {
		if e == event {
			n++
		}
	}
	return n
}

func (n *recordingNotifier) count(event string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, notice := range n.notices {
		if notice.event == event {
			c++
		}
	}
	return c
}

// scheduledHarness is panelHarness with a cluster whose every job succeeds,
// and plugins that are counted.
func scheduledHarness(t *testing.T) (*Manager, *store.DB, *recordingNotifier, *heardEvents, string) {
	t.Helper()
	m, db, _, notifier, teamID := panelHarness(t)
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("get", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		get := action.(k8stesting.GetAction)
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: get.GetName(), Namespace: get.GetNamespace()}}
		job.Status.Succeeded = 1
		return true, job, nil
	})
	dynamic := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamic.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(action.(k8stesting.PatchAction).GetPatch()); err != nil {
			return true, nil, err
		}
		return true, obj, nil
	})
	m.cluster = cluster.New(kube.NewClientWith(clientset, dynamic, ""), db, m.keyring, slog.New(slog.DiscardHandler))
	heard := &heardEvents{}
	m.Plugins = heard.dispatcher()
	return m, db, notifier, heard, teamID
}

// withoutStorage takes the harness's bucket away.
func withoutStorage(t *testing.T, db *store.DB) {
	t.Helper()
	for _, key := range []string{settings.KeyS3Endpoint, settings.KeyS3Bucket, settings.KeyS3AccessKey,
		settings.KeyS3SecretKey, settings.KeyS3Region, settings.KeyS3PathStyle} {
		if err := db.DeleteSetting(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
}

// dueAt is a schedule that matches one minute ten minutes ago, and that
// minute.
func dueAt() (string, time.Time) {
	due := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	return fmt.Sprintf("%d %d * * *", due.Minute(), due.Hour()), due
}

// scheduledDatabase is a database in the harness's team, stored in a state,
// with a schedule due at the minute dueAt names.
func scheduledDatabase(t *testing.T, db *store.DB, teamID, name, status string) store.Database {
	t.Helper()
	record := catchUpDatabase(t, db, teamID, name)
	if err := db.SetDatabaseStatus(t.Context(), record.ID, status, ""); err != nil {
		t.Fatal(err)
	}
	record.Status = status
	schedule, _ := dueAt()
	policy, err := db.GetBackupPolicy(t.Context(), "database", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy.Schedule = schedule
	if err := db.SetBackupPolicy(t.Context(), &policy); err != nil {
		t.Fatal(err)
	}
	backdate(t, db, "backup_policies", policy.ID, time.Now().Add(-30*24*time.Hour))
	return record
}

func tick(m *Manager) {
	_, due := dueAt()
	m.RunScheduledAt(context.Background(), []time.Time{due})
}

// settled waits for a backup the tick started to finish, so nothing is still
// writing when the test's database is closed.
func settled(t *testing.T, db *store.DB, targetType, targetID string) []store.Backup {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		backups, err := db.ListBackups(t.Context(), targetType, targetID, 10)
		if err != nil {
			t.Fatal(err)
		}
		running := false
		for _, b := range backups {
			running = running || b.Status == "running"
		}
		if !running {
			return backups
		}
		if time.Now().After(deadline) {
			t.Fatalf("a backup is still running: %+v", backups)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAScheduledBackupThatCannotStartIsWrittenDownAndTold(t *testing.T) {
	m, db, notifier, heard, teamID := scheduledHarness(t)
	withoutStorage(t, db)
	shop := scheduledDatabase(t, db, teamID, "shop", "running")

	tick(m)

	backups, err := db.ListBackups(t.Context(), "database", shop.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("%d backups, want the one that could not start: %+v", len(backups), backups)
	}
	failed := backups[0]
	if failed.Status != "failed" || failed.Kind != "scheduled" || failed.ErrorCode != "backup.storage_not_configured" ||
		failed.ErrorMessage == "" || failed.FinishedAt.IsZero() {
		t.Fatalf("the backup that could not start is %+v", failed)
	}
	if got := notifier.count(notify.EventBackupFailed); got != 1 {
		t.Errorf("%d backup.failed notifications, want 1: %+v", got, notifier.notices)
	}
	if got := heard.count(plugins.EventBackupFailed); got != 1 {
		t.Errorf("plugins heard backup.failed %d times, want 1", got)
	}
}

func TestAScheduledBackupReadsTheDatabasesStateAfresh(t *testing.T) {
	t.Run("degraded is backed up", func(t *testing.T) {
		m, db, _, _, teamID := scheduledHarness(t)
		m.Status = func(context.Context, string) (string, string, error) {
			t.Error("a degraded database's state was read again, when it answers as it is")
			return "", "", nil
		}
		shop := scheduledDatabase(t, db, teamID, "shop", "degraded")
		tick(m)
		backups := settled(t, db, "database", shop.ID)
		if len(backups) != 1 || backups[0].Status != "succeeded" {
			t.Fatalf("a degraded database was not backed up: %+v", backups)
		}
	})

	t.Run("stored failed, running now", func(t *testing.T) {
		m, db, _, _, teamID := scheduledHarness(t)
		shop := scheduledDatabase(t, db, teamID, "shop", "failed")
		m.Status = func(_ context.Context, id string) (string, string, error) {
			if id != shop.ID {
				t.Errorf("asked about %s", id)
			}
			return "running", "", nil
		}
		tick(m)
		backups := settled(t, db, "database", shop.ID)
		if len(backups) != 1 || backups[0].Status != "succeeded" {
			t.Fatalf("a database running again was not backed up: %+v", backups)
		}
		if stored, _ := db.GetDatabase(t.Context(), shop.ID); stored.Status != "running" {
			t.Errorf("the state read is not kept: %q", stored.Status)
		}
	})

	t.Run("failed, and still failed", func(t *testing.T) {
		m, db, notifier, _, teamID := scheduledHarness(t)
		shop := scheduledDatabase(t, db, teamID, "shop", "starting")
		m.Status = func(context.Context, string) (string, string, error) {
			return "failed", "the instance cannot start", nil
		}
		tick(m)
		backups := settled(t, db, "database", shop.ID)
		if len(backups) != 1 || backups[0].Status != "failed" || backups[0].ErrorCode != "backup.database_not_running" {
			t.Fatalf("a failed database's backup is %+v", backups)
		}
		if stored, _ := db.GetDatabase(t.Context(), shop.ID); stored.Status != "failed" ||
			stored.StatusDetail != "the instance cannot start" {
			t.Errorf("the state read is not kept: %+v", stored)
		}
		if got := notifier.count(notify.EventBackupFailed); got != 1 {
			t.Errorf("%d backup.failed notifications, want 1", got)
		}
	})

	t.Run("a cluster that does not answer changes nothing", func(t *testing.T) {
		m, db, _, _, teamID := scheduledHarness(t)
		shop := scheduledDatabase(t, db, teamID, "shop", "starting")
		m.Status = func(context.Context, string) (string, string, error) { return "unknown", "no answer", nil }
		tick(m)
		if stored, _ := db.GetDatabase(t.Context(), shop.ID); stored.Status != "starting" {
			t.Errorf("an unanswered question was stored as the answer: %q", stored.Status)
		}
	})
}

// A dump taken halfway through a restore is half of two databases.
func TestAScheduledBackupWaitsForWhateverIsBeingDoneToTheDatabase(t *testing.T) {
	m, db, notifier, heard, teamID := scheduledHarness(t)
	shop := scheduledDatabase(t, db, teamID, "shop", "running")
	restore := store.Operation{TeamID: teamID, Kind: "database.restore", TargetType: "database", TargetID: shop.ID}
	if err := db.CreateOperation(t.Context(), &restore, []string{"prepare", "restore", "verify"}); err != nil {
		t.Fatal(err)
	}

	tick(m)

	backups := settled(t, db, "database", shop.ID)
	if len(backups) != 1 || backups[0].Status != "skipped" || backups[0].ErrorCode != SkippedBusy ||
		backups[0].ErrorMessage == "" {
		t.Fatalf("the backup during a restore is %+v, want one skipped with the reason", backups)
	}
	if len(notifier.notices) != 0 || heard.count(plugins.EventBackupFailed) != 0 {
		t.Errorf("a skipped backup was told as a failure: %+v", notifier.notices)
	}

	// Being created is busy too, as the API has it.
	blog := scheduledDatabase(t, db, teamID, "blog", "creating")
	tick(m)
	if backups := settled(t, db, "database", blog.ID); len(backups) != 1 || backups[0].ErrorCode != SkippedBusy {
		t.Fatalf("the backup of a database being created is %+v", backups)
	}
}

func TestAVolumesScheduledBackupThatCannotStartIsWrittenDownToo(t *testing.T) {
	m, db, notifier, heard, teamID := scheduledHarness(t)
	withoutStorage(t, db)
	shop := catchUpDatabase(t, db, teamID, "shop")
	app := store.App{EnvironmentID: shop.EnvironmentID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	volume := store.Volume{AppID: app.ID, Name: "uploads", MountPath: "/uploads", SizeGB: 1}
	if err := db.CreateVolume(t.Context(), &volume); err != nil {
		t.Fatal(err)
	}
	schedule, _ := dueAt()
	policy := store.BackupPolicy{TargetType: "volume", TargetID: volume.ID, Schedule: schedule, Retention: 7, Enabled: true}
	if err := db.SetBackupPolicy(t.Context(), &policy); err != nil {
		t.Fatal(err)
	}
	// The database's own schedule is not due.
	if err := db.SetBackupPolicy(t.Context(), &store.BackupPolicy{TargetType: "database", TargetID: shop.ID,
		Schedule: "0 3 29 2 *", Retention: 7, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	tick(m)

	backups, err := db.ListBackups(t.Context(), "volume", volume.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0].Status != "failed" || backups[0].ErrorCode != "backup.storage_not_configured" {
		t.Fatalf("the volume's backup is %+v", backups)
	}
	if got := notifier.count(notify.EventBackupFailed); got != 1 {
		t.Errorf("%d backup.failed notifications, want 1: %+v", got, notifier.notices)
	}
	if got := heard.count(plugins.EventBackupFailed); got != 1 {
		t.Errorf("plugins heard backup.failed %d times, want 1", got)
	}
}

// A schedule found for a database that is no longer there — deleted between
// the tick reading the schedules and running them — goes, and nobody is told
// a backup of nothing failed.
func TestTheScheduleOfSomethingGoneIsDeletedNotRun(t *testing.T) {
	m, db, notifier, heard, _ := scheduledHarness(t)
	schedule, _ := dueAt()
	orphan := store.BackupPolicy{TargetType: "database", TargetID: "db_gone", Schedule: schedule, Retention: 7, Enabled: true}
	if err := db.SetBackupPolicy(t.Context(), &orphan); err != nil {
		t.Fatal(err)
	}

	if _, err := m.runScheduled(t.Context(), orphan); !errors.Is(err, errTargetGone) {
		t.Fatalf("runScheduled = %v, want errTargetGone", err)
	}
	tick(m)

	if _, err := db.GetBackupPolicy(t.Context(), "database", "db_gone"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the schedule is still there: %v", err)
	}
	if backups, _ := db.ListBackups(t.Context(), "database", "db_gone", 10); len(backups) != 0 {
		t.Errorf("a backup of nothing was written down: %+v", backups)
	}
	if len(notifier.notices) != 0 || len(heard.events) != 0 {
		t.Errorf("somebody was told about a backup of nothing: %+v %v", notifier.notices, heard.events)
	}
}

// The failed row is what the next start reads: a night with an attempt in it
// was not slept through.
func TestAFailedScheduledAttemptIsNotCaughtUpOnAsMissed(t *testing.T) {
	m, db, notifier, _, teamID := scheduledHarness(t)
	withoutStorage(t, db)
	shop := scheduledDatabase(t, db, teamID, "shop", "running")

	tick(m)
	if got := notifier.count(notify.EventBackupFailed); got != 1 {
		t.Fatalf("%d backup.failed notifications from the tick, want 1", got)
	}
	if missed := m.CatchUp(t.Context(), time.Now()); len(missed) != 0 {
		t.Fatalf("caught up on %+v after a failed attempt", missed)
	}
	if got := notifier.count(notify.EventBackupMissed); got != 0 {
		t.Errorf("%d backup.missed notifications, want none", got)
	}
	if backups, _ := db.ListBackups(t.Context(), "database", shop.ID, 10); len(backups) != 1 {
		t.Errorf("%d backups, want the one failed attempt", len(backups))
	}
}

// A missed backup that cannot be taken late is written down as well, so a
// second restart does not report the same night twice; channels are told
// once, as missed, and plugins as the failed backup it is.
func TestAMissedBackupThatCannotStartIsWrittenDownOnce(t *testing.T) {
	m, db, notifier, heard, teamID := scheduledHarness(t)
	withoutStorage(t, db)
	shop := scheduledDatabase(t, db, teamID, "shop", "running")

	if missed := m.CatchUp(t.Context(), time.Now()); len(missed) != 1 {
		t.Fatalf("caught up on %+v, want the one", missed)
	}
	if missed := m.CatchUp(t.Context(), time.Now()); len(missed) != 0 {
		t.Fatalf("caught up on the same night twice: %+v", missed)
	}
	if got := notifier.count(notify.EventBackupMissed); got != 1 {
		t.Errorf("%d backup.missed notifications, want 1", got)
	}
	if got := notifier.count(notify.EventBackupFailed); got != 0 {
		t.Errorf("%d backup.failed notifications beside the missed one", got)
	}
	if got := heard.count(plugins.EventBackupFailed); got != 1 {
		t.Errorf("plugins heard backup.failed %d times, want 1", got)
	}
	if backups, _ := db.ListBackups(t.Context(), "database", shop.ID, 10); len(backups) != 1 || backups[0].Status != "failed" {
		t.Errorf("the late attempt is %+v", backups)
	}
}
