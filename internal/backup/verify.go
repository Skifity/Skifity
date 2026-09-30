package backup

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"

	"skifity/internal/errdoc"
	"skifity/internal/runsafe"
	"skifity/internal/sealed"
	"skifity/internal/store"
)

// Verifying a backup: downloaded, opened, and read to its end.
//
// A backup nobody has tried to restore is a hope. Every backup here is a
// gzip — a database dump, a tar of a volume, the panel's own database — so
// reading one through proves the part a restore depends on and that a check
// of its size cannot: the archive is whole, its checksum matches, and, when it
// is sealed, the passphrase opens it and no chunk is missing or changed.
// What it does not prove is that the database would accept the dump, which
// only a restore does.

// VerifyBackup checks a backup in the background and records the outcome on
// it, which the backup lists show.
func (m *Manager) VerifyBackup(ctx context.Context, backupID string) error {
	backup, err := m.db.GetBackup(ctx, backupID)
	if err != nil {
		return err
	}
	if backup.Status != "succeeded" || backup.Location == "" {
		return errdoc.BadRequest("Only a backup that finished can be verified.")
	}
	storage, err := LoadStorage(ctx, m.db, m.keyring)
	if err != nil {
		return err
	}
	passphrase := ""
	if backup.Encrypted {
		if passphrase, err = Passphrase(ctx, m.db, m.keyring); err != nil {
			return err
		}
		if passphrase == "" {
			return errdoc.BackupPassphraseMissing()
		}
	}
	// A backup already waiting or being read is not read twice: the second
	// request is the first one's answer.
	if !m.claimVerification(backup.ID) {
		return nil
	}
	go func() {
		ctx := context.WithoutCancel(ctx)
		defer m.releaseVerification(backup.ID)
		defer runsafe.Recover(m.log, "verifying backup "+backup.ID, func(err error) {
			_ = m.db.RecordVerification(ctx, backup.ID, err.Error())
		})
		m.verifyTurn.Lock()
		defer m.verifyTurn.Unlock()
		outcome := ""
		if _, err := m.readThrough(ctx, storage, backup, passphrase); err != nil {
			outcome = err.Error()
		}
		if err := m.db.RecordVerification(ctx, backup.ID, outcome); err != nil {
			m.log.Warn("could not record a verification", "backup", backup.ID, "error", err)
		}
		if backup.TargetType == "database" {
			m.publish(ctx, backup.TargetID)
		}
		m.log.Info("backup verified", "backup", backup.ID, "ok", outcome == "", "detail", outcome)
	}()
	return nil
}

// claimVerification reports whether a backup's verification may start, and
// false while one of it is already waiting or running.
func (m *Manager) claimVerification(backupID string) bool {
	m.verifyMu.Lock()
	defer m.verifyMu.Unlock()
	if m.verifying[backupID] {
		return false
	}
	if m.verifying == nil {
		m.verifying = map[string]bool{}
	}
	m.verifying[backupID] = true
	return true
}

func (m *Manager) releaseVerification(backupID string) {
	m.verifyMu.Lock()
	defer m.verifyMu.Unlock()
	delete(m.verifying, backupID)
}

func (m *Manager) readThrough(ctx context.Context, storage *Storage, backup store.Backup, passphrase string) (int64, error) {
	object, err := storage.Get(ctx, backup.Location)
	if err != nil {
		return 0, err
	}
	defer object.Close()
	return ReadThrough(object, backup.Encrypted, passphrase)
}

// ReadThrough reads a backup to its end, opening it first when it is sealed,
// and returns how many bytes the archive held.
func ReadThrough(r io.Reader, isSealed bool, passphrase string) (int64, error) {
	source := r
	if isSealed {
		opened, write := io.Pipe()
		go func() {
			var err error
			defer func() { _ = write.CloseWithError(err) }()
			defer runsafe.Recover(nil, "opening a backup to verify it", func(e error) { err = e })
			err = sealed.Open(write, r, passphrase)
		}()
		// Whatever happens below, the goroutine is not left writing into a
		// pipe nobody reads.
		defer func() { _ = opened.Close() }()
		source = opened
	}
	archive, err := gzip.NewReader(source)
	if err != nil {
		return 0, explainRead(err)
	}
	n, err := io.Copy(io.Discard, archive)
	if err != nil {
		return n, explainRead(err)
	}
	if err := archive.Close(); err != nil {
		return n, explainRead(err)
	}
	if n == 0 {
		return 0, errors.New("the archive is empty")
	}
	return n, nil
}

func explainRead(err error) error {
	switch {
	case errors.Is(err, sealed.ErrWrongPassphrase), errors.Is(err, sealed.ErrDamaged):
		return err
	case errors.Is(err, gzip.ErrChecksum), errors.Is(err, gzip.ErrHeader), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("the archive is damaged: %w", err)
	}
	return err
}
