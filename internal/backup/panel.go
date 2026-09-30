package backup

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"

	"skifity/internal/cron"
	"skifity/internal/errdoc"
	"skifity/internal/notify"
	"skifity/internal/sealed"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Backing up the panel itself.
//
// Everything else the panel looks after it could already back up, and its own
// database it could not: `skifity admin backup-db` wrote a copy to a path on
// the same disk, by hand, when somebody remembered. The disk the panel's
// database is on is the one that fails, and a copy beside it goes with it. Five
// of the products in docs/research/competitors name this as the gap.
//
// So the panel copies its own database to the bucket its other backups go to,
// on a schedule, once there is a bucket. What is uploaded is the database and
// never the master key: every secret in the copy is sealed with that key, so a
// bucket that leaks hands over rows of ciphertext, and restoring needs the key
// from wherever it was kept — somewhere other than the bucket.

// PanelTarget is the target type and id the panel's own backups are recorded
// under, beside the databases and volumes.
const PanelTarget = "panel"

// DefaultPanelSchedule is when the panel backs itself up if nobody said:
// every day at 03:17, off the hour everything else chooses.
const DefaultPanelSchedule = "17 3 * * *"

// DefaultPanelKeep is how many panel backups are kept if nobody said.
const DefaultPanelKeep = 14

// panelMu keeps two panel backups from running at once: a manual one pressed
// while the scheduled one is uploading would write the same snapshot twice.
var panelMu sync.Mutex

// BackupPanel copies the panel's database to backup storage.
func (m *Manager) BackupPanel(ctx context.Context, kind string) (store.Backup, error) {
	storage, err := LoadStorage(ctx, m.db, m.keyring)
	if err != nil {
		return store.Backup{}, err
	}
	if !panelMu.TryLock() {
		return store.Backup{}, errdoc.Conflict("A backup of this panel is already running.",
			"Wait for it to finish; it is listed under Settings, then Backup storage.")
	}
	defer panelMu.Unlock()

	passphrase, err := Passphrase(ctx, m.db, m.keyring)
	if err != nil {
		return store.Backup{}, err
	}
	record := store.Backup{TargetType: PanelTarget, TargetID: PanelTarget, Status: "running", Kind: kind,
		Encrypted: passphrase != ""}
	if err := m.db.CreateBackup(ctx, &record); err != nil {
		return store.Backup{}, err
	}

	key := PanelObjectKey(time.Now(), record.ID)
	size, err := m.uploadPanel(ctx, storage, key, passphrase)
	if err != nil {
		m.log.Error("the panel could not back itself up", "error", err)
		_ = m.db.FinishBackup(ctx, record.ID, "failed", "", 0, err.Error())
		m.notifyAdmins(ctx, notify.Message{
			Title: "This panel could not back itself up",
			Body:  err.Error() + "\n\nThe copy in the bucket is older than it should be.",
			Level: "error",
			Path:  "/settings",
		})
		record.Status, record.ErrorMessage = "failed", err.Error()
		return record, err
	}
	if err := m.db.FinishBackup(ctx, record.ID, "succeeded", key, size, ""); err != nil {
		return record, err
	}
	record.Status, record.Location, record.SizeBytes = "succeeded", key, size
	m.log.Info("the panel backed itself up", "key", key, "bytes", size)

	// Only after a copy that worked, so retention can never leave nothing.
	m.retainPanel(ctx, storage)
	return record, nil
}

// uploadPanel snapshots the database, compresses it and uploads it.
//
// The files go beside the database rather than in /tmp, because the panel's
// root filesystem is read-only and the data directory is the one place it can
// write — and the one with room for a copy of the database.
func (m *Manager) uploadPanel(ctx context.Context, storage *Storage, key, passphrase string) (int64, error) {
	dir := filepath.Dir(m.db.Path())
	snapshot := filepath.Join(dir, fmt.Sprintf(".panel-backup-%d.db", time.Now().UnixNano()))
	defer os.Remove(snapshot)
	if err := m.db.Snapshot(ctx, snapshot); err != nil {
		return 0, fmt.Errorf("copy the database: %w", err)
	}

	compressed, err := os.CreateTemp(dir, ".panel-backup-*.gz")
	if err != nil {
		return 0, fmt.Errorf("prepare the upload: %w", err)
	}
	defer os.Remove(compressed.Name())
	defer compressed.Close()

	source, err := os.Open(snapshot)
	if err != nil {
		return 0, fmt.Errorf("read the copy: %w", err)
	}
	defer source.Close()
	zw := gzip.NewWriter(compressed)
	if _, err := io.Copy(zw, source); err != nil {
		return 0, fmt.Errorf("compress the copy: %w", err)
	}
	if err := zw.Close(); err != nil {
		return 0, fmt.Errorf("compress the copy: %w", err)
	}
	// Sealed in place, here rather than in a job: this is the panel's own
	// process, and the file never leaves it in the clear.
	if passphrase != "" {
		if err := compressed.Sync(); err != nil {
			return 0, err
		}
		if err := sealed.SealFile(compressed.Name(), passphrase); err != nil {
			return 0, fmt.Errorf("seal the copy: %w", err)
		}
		// The file under that name is the sealed one now.
		compressed.Close()
		if compressed, err = os.Open(compressed.Name()); err != nil {
			return 0, err
		}
		defer compressed.Close()
		if _, err := compressed.Seek(0, io.SeekEnd); err != nil {
			return 0, err
		}
	}
	size, err := compressed.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if _, err := compressed.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := storage.Put(ctx, key, compressed, size); err != nil {
		return 0, err
	}
	return size, nil
}

// retainPanel deletes panel backups beyond the number to keep.
func (m *Manager) retainPanel(ctx context.Context, storage *Storage) {
	keep := DefaultPanelKeep
	if value, _, err := m.db.GetSetting(ctx, settings.KeyPanelBackupKeep); err == nil && value != "" {
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			keep = n
		}
	}
	expired, err := m.db.ExpiredBackups(ctx, PanelTarget, PanelTarget, keep)
	if err != nil {
		m.log.Warn("could not list old panel backups", "error", err)
		return
	}
	m.forget(ctx, storage, expired)
}

// panelDue reports whether the panel's own backup is due in any of the minutes.
//
// An empty schedule is the default one, and "off" is off. Backup storage that
// is not configured is not a failure: there is simply nowhere to put it yet.
func (m *Manager) panelDue(ctx context.Context, minutes []time.Time) bool {
	schedule, _, err := m.db.GetSetting(ctx, settings.KeyPanelBackupSchedule)
	if err != nil {
		return false
	}
	schedule = strings.TrimSpace(schedule)
	switch schedule {
	case "off":
		return false
	case "":
		schedule = DefaultPanelSchedule
	}
	for _, minute := range minutes {
		if cron.DueNow(schedule, minute.UTC()) {
			return true
		}
	}
	return false
}

// runScheduledPanel takes the panel's scheduled backup.
func (m *Manager) runScheduledPanel(ctx context.Context) {
	if _, err := m.BackupPanel(ctx, "scheduled"); err != nil {
		var problem *errdoc.Problem
		if errors.As(err, &problem) && problem.Code == errdoc.StorageNotConfigured().Code {
			return
		}
		m.log.Error("the scheduled backup of this panel failed", "error", err)
	}
}

// notifyAdmins tells the teams the panel's administrators belong to.
//
// Notification channels belong to teams and this is not a team's problem: it
// is the panel's. The people who can fix it are its administrators, and their
// teams' channels are where they already listen.
func (m *Manager) notifyAdmins(ctx context.Context, msg notify.Message) {
	if m.notifier == nil {
		return
	}
	users, err := m.db.ListUsers(ctx)
	if err != nil {
		return
	}
	told := map[string]bool{}
	for _, user := range users {
		if !user.IsAdmin || user.Disabled {
			continue
		}
		teams, err := m.db.ListTeamsForUser(ctx, user.ID)
		if err != nil {
			continue
		}
		for _, team := range teams {
			if told[team.ID] {
				continue
			}
			told[team.ID] = true
			m.notifier.Notify(ctx, team.ID, notify.EventBackupFailed, msg)
		}
	}
}

// PanelObjectKey is where one panel backup is stored: readable in a bucket
// listing, so an operator can find the newest one without the panel — which is
// exactly the situation a restore is for. The backup's id keeps two taken in
// the same second from being one object, which retention would then delete
// out from under the newer one.
func PanelObjectKey(at time.Time, id string) string {
	return fmt.Sprintf("skifity/panel/%s-%s-panel.db.gz", at.UTC().Format("20060102-150405"), sanitise(id))
}

// Put uploads one object of a known size.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if _, err := s.client.PutObject(ctx, s.bucket, key, r, size,
		minio.PutObjectOptions{ContentType: "application/gzip"}); err != nil {
		return fmt.Errorf("upload to %s: %w", s.bucket, err)
	}
	return nil
}
