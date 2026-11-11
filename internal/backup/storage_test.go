package backup

import (
	"log/slog"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/events"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Storage says whether backups have somewhere to go from the settings alone:
// it is asked on every database created, so it never reaches the bucket.
func TestStorageIsReadFromTheSettingsAlone(t *testing.T) {
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, _ := crypto.GenerateKey()
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatal(err)
	}
	m := New(db, keyring, events.NewHub(4), nil, nil, slog.New(slog.DiscardHandler))
	set := func(key, value string) {
		t.Helper()
		if err := db.SetSetting(ctx, key, value, false, "test"); err != nil {
			t.Fatal(err)
		}
	}

	if got, err := m.Storage(ctx); err != nil || got.Configured || got.Sealed {
		t.Fatalf("with nothing set: %+v (%v)", got, err)
	}

	// Everything but the secret key is not enough.
	set(settings.KeyS3Endpoint, "https://s3.invalid")
	set(settings.KeyS3Bucket, "backups")
	set(settings.KeyS3AccessKey, "access")
	if got, err := m.Storage(ctx); err != nil || got.Configured {
		t.Fatalf("with no secret key: %+v (%v)", got, err)
	}

	// A host that does not resolve is still configured: nothing is dialled.
	set(settings.KeyS3SecretKey, "secret-key-for-tests")
	if got, err := m.Storage(ctx); err != nil || !got.Configured || got.Sealed {
		t.Fatalf("with every setting: %+v (%v)", got, err)
	}

	set(settings.KeyBackupPassphrase, "a passphrase for tests")
	if got, err := m.Storage(ctx); err != nil || !got.Sealed {
		t.Fatalf("with a passphrase: %+v (%v)", got, err)
	}

	// An endpoint that is not an address is storage that is not set up, not
	// a failure to answer.
	set(settings.KeyS3Endpoint, "https://")
	if got, err := m.Storage(ctx); err != nil || got.Configured {
		t.Fatalf("with an endpoint that is not an address: %+v (%v)", got, err)
	}
}
