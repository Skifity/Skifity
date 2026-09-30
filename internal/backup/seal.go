package backup

import (
	"context"
	"errors"
	"fmt"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/sealed"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Sealing backups: the format is internal/sealed; this is when it is used.

// Passphrase reads the backup passphrase, or "" when backups are not sealed.
func Passphrase(ctx context.Context, db *store.DB, keyring *crypto.Keyring) (string, error) {
	value, encrypted, err := db.GetSetting(ctx, settings.KeyBackupPassphrase)
	if err != nil || value == "" {
		return "", err
	}
	if !encrypted {
		return value, nil
	}
	plain, err := keyring.Open(value, settings.Context(settings.KeyBackupPassphrase))
	if err != nil {
		return "", fmt.Errorf("read the backup passphrase: %w", err)
	}
	return string(plain), nil
}

// sealPlan is how a backup job treats what it moves: sealed with this
// passphrase by this image, or left as it is when both are empty.
type sealPlan struct {
	passphrase string
	image      string
}

// sealing decides whether a new backup is sealed. With a passphrase set and
// no image of the panel to seal with, it refuses: a backup meant to be sealed
// is never uploaded in the clear instead.
func (m *Manager) sealing(ctx context.Context) (sealPlan, error) {
	passphrase, err := Passphrase(ctx, m.db, m.keyring)
	if err != nil || passphrase == "" {
		return sealPlan{}, err
	}
	image, err := m.panelImage(ctx)
	if err != nil {
		return sealPlan{}, err
	}
	return sealPlan{passphrase: passphrase, image: image}, nil
}

// opening decides how a backup is restored, and for a sealed one checks the
// passphrase against its header before anything is stopped or overwritten.
func (m *Manager) opening(ctx context.Context, storage *Storage, backup store.Backup) (sealPlan, error) {
	if !backup.Encrypted {
		return sealPlan{}, nil
	}
	passphrase, err := Passphrase(ctx, m.db, m.keyring)
	if err != nil {
		return sealPlan{}, err
	}
	if passphrase == "" {
		return sealPlan{}, errdoc.BackupPassphraseMissing()
	}
	header, err := storage.ReadHeader(ctx, backup.Location, sealed.HeaderSize)
	if err != nil {
		return sealPlan{}, err
	}
	switch err := sealed.CheckPassphrase(header, passphrase); {
	case errors.Is(err, sealed.ErrWrongPassphrase):
		return sealPlan{}, errdoc.BackupWrongPassphrase()
	case err != nil:
		return sealPlan{}, errdoc.BackupDamaged(err.Error())
	}
	image, err := m.panelImage(ctx)
	if err != nil {
		return sealPlan{}, err
	}
	return sealPlan{passphrase: passphrase, image: image}, nil
}

// panelImage is the image sealing runs, with a failure to find it said as a
// backup's problem: it was the firewall's, whose words are about installing a
// firewall, and every sealed backup of a panel outside its cluster failed
// saying so.
func (m *Manager) panelImage(ctx context.Context) (string, error) {
	image, err := m.cluster.PanelImage(ctx)
	if err != nil {
		detail := err.Error()
		var problem *errdoc.Problem
		if errors.As(err, &problem) && problem.Cause != "" {
			detail = problem.Cause
		}
		return "", errdoc.BackupNoPanelImage(detail)
	}
	return image, nil
}
