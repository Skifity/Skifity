package serverapp

import (
	"context"
	"log/slog"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// prepareMasterKey loads the master key, and makes one only where making one is
// safe.
//
// A missing key file used to be answered by a fresh key, always. On a first
// start that is exactly right. On a server whose database already holds secrets
// it is the quietest way there is to lose every one of them: the panel starts,
// signs everybody in, and then cannot read a single stored token, password or
// backup, and says nothing about why. So a key that is gone while the database
// is not is a refusal, and a key that is there is tried against something the
// database sealed, which catches a key file replaced by the wrong one.
//
// allowNew is the way out for somebody who has lost the key and its recovery
// key and means to go on without the secrets.
func prepareMasterKey(ctx context.Context, path string, db *store.DB, allowNew bool, log *slog.Logger) (*crypto.Keyring, error) {
	exists, err := crypto.KeyFileExists(path)
	if err != nil {
		return nil, err
	}
	key, sealed, err := db.SealedSample(ctx)
	if err != nil {
		return nil, err
	}

	if !exists {
		accounts, err := db.CountUsers(ctx)
		if err != nil {
			return nil, err
		}
		if (accounts > 0 || sealed != "") && !allowNew {
			return nil, errdoc.MasterKeyMissing(path)
		}
		if accounts > 0 || sealed != "" {
			log.Warn("starting with a new master key, as asked: every secret the old one sealed is unreadable",
				"path", path)
		}
	}

	ring, err := crypto.InitKeyring(path)
	if err != nil {
		return nil, err
	}
	if exists && sealed != "" {
		if _, err := ring.Open(sealed, settings.Context(key)); err != nil {
			return nil, errdoc.MasterKeyMismatch(path)
		}
	}
	return ring, nil
}
