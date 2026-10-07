package serverapp

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// seededPanel is a database with an account and one secret sealed with ring.
func seededPanel(t *testing.T, ring *crypto.Keyring) *store.DB {
	t.Helper()
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	user := store.User{Email: "admin@example.test", Name: "Admin", PasswordHash: "x", IsAdmin: true}
	if err := db.CreateUser(t.Context(), &user); err != nil {
		t.Fatal(err)
	}
	if ring != nil {
		value, err := ring.Seal([]byte("a stored token"), settings.Context(settings.KeyS3SecretKey))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SetSetting(t.Context(), settings.KeyS3SecretKey, value, true, "test"); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func problemCode(err error) string {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}

// TestAMissingKeyIsNotMadeAgainOverASealedDatabase: the panel used to answer a
// missing key file with a new key, always. With secrets in the database that
// starts fine and loses every one of them, saying nothing.
func TestAMissingKeyIsNotMadeAgainOverASealedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	original, err := crypto.InitKeyring(path)
	if err != nil {
		t.Fatal(err)
	}
	db := seededPanel(t, original)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	_, err = prepareMasterKey(t.Context(), path, db, false, quiet())
	if problemCode(err) != "crypto.master_key_missing" {
		t.Fatalf("a key that is gone was answered with %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a new key was written although the panel refused to start")
	}

	// Somebody who has lost it for good can say so, once.
	ring, err := prepareMasterKey(t.Context(), path, db, true, quiet())
	if err != nil || ring == nil {
		t.Fatalf("starting over was refused although it was asked for: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Error("starting over did not write the new key")
	}
}

func TestAFirstStartStillMakesItsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ring, err := prepareMasterKey(t.Context(), path, db, false, quiet())
	if err != nil || ring == nil {
		t.Fatalf("a first start has no key and no data, and must make one: %v", err)
	}
	// And the next start finds it.
	again, err := prepareMasterKey(t.Context(), path, db, false, quiet())
	if err != nil || again.ActiveID() != ring.ActiveID() {
		t.Fatalf("the second start did not find the key the first made: %v", err)
	}
}

// TestTheWrongKeyFileIsNotStartedOn: a key file replaced by another install's.
func TestTheWrongKeyFileIsNotStartedOn(t *testing.T) {
	dir := t.TempDir()
	right, err := crypto.InitKeyring(filepath.Join(dir, "right.key"))
	if err != nil {
		t.Fatal(err)
	}
	db := seededPanel(t, right)

	wrongPath := filepath.Join(dir, "wrong.key")
	if _, err := crypto.InitKeyring(wrongPath); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareMasterKey(t.Context(), wrongPath, db, false, quiet()); problemCode(err) != "crypto.master_key_mismatch" {
		t.Fatalf("the wrong key was started on: %v", err)
	}
	// Allowing a new key is not a way round a wrong one: the file is there.
	if _, err := prepareMasterKey(t.Context(), wrongPath, db, true, quiet()); problemCode(err) != "crypto.master_key_mismatch" {
		t.Fatalf("a wrong key got in because new keys were allowed: %v", err)
	}
	if _, err := prepareMasterKey(t.Context(), filepath.Join(dir, "right.key"), db, false, quiet()); err != nil {
		t.Fatalf("the right key was refused: %v", err)
	}
}

// A database with no sealed secrets cannot say which key is right, and an
// existing key is taken as it is, as it always was.
func TestADatabaseWithNothingSealedAcceptsTheKeyItIsGiven(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if _, err := crypto.InitKeyring(path); err != nil {
		t.Fatal(err)
	}
	db := seededPanel(t, nil)
	if _, err := prepareMasterKey(t.Context(), path, db, false, quiet()); err != nil {
		t.Fatalf("a key was refused with nothing to check it against: %v", err)
	}
}
