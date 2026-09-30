package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Putting a backup of the panel's own database back.

// panelDatabase makes a panel database at path with one account, and a secret
// sealed with keyring when one is given.
func panelDatabase(t *testing.T, path, email string, keyring *crypto.Keyring) {
	t.Helper()
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user := store.User{Email: email, Name: "Admin", PasswordHash: "x", IsAdmin: true}
	if err := db.CreateUser(t.Context(), &user); err != nil {
		t.Fatal(err)
	}
	if keyring != nil {
		sealed, err := keyring.Seal([]byte("secret-key-for-tests"), settings.Context(settings.KeyS3SecretKey))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SetSetting(t.Context(), settings.KeyS3SecretKey, sealed, true, "test"); err != nil {
			t.Fatal(err)
		}
	}
}

// gzipped writes a copy of the file at path, compressed the way the panel
// uploads it, and returns where.
func gzipped(t *testing.T, path string) string {
	t.Helper()
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := db.Snapshot(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	db.Close()
	plain, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	out := filepath.Join(t.TempDir(), "20260930-031700-bak-9f2c-panel.db.gz")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

// emailsIn lists the accounts in the database at path.
func emailsIn(t *testing.T, path string) []string {
	t.Helper()
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users, err := db.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, user := range users {
		out = append(out, user.Email)
	}
	return out
}

// restoreHarness is a live panel database and a key file beside it.
func restoreHarness(t *testing.T) (dir, target, keyPath string, keyring *crypto.Keyring) {
	t.Helper()
	dir = t.TempDir()
	keyPath = filepath.Join(dir, "master.key")
	keyring, err := crypto.InitKeyring(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(dir, "skifity.db")
	panelDatabase(t, target, "current@example.test", keyring)
	return dir, target, keyPath, keyring
}

func TestRestoringPutsTheBackupInPlaceAndKeepsWhatItReplaced(t *testing.T) {
	dir, target, keyPath, keyring := restoreHarness(t)
	source := filepath.Join(t.TempDir(), "backup.db")
	panelDatabase(t, source, "from-backup@example.test", keyring)
	backup := gzipped(t, source)

	var out bytes.Buffer
	err := adminRestoreDatabase(context.Background(),
		[]string{backup, "--database", target, "--master-key", keyPath, "--yes"}, &out)
	if err != nil {
		t.Fatalf("restore-db: %v\n%s", err, out.String())
	}

	if got := emailsIn(t, target); len(got) != 1 || got[0] != "from-backup@example.test" {
		t.Fatalf("the database holds %v after the restore, want the backup's account", got)
	}
	kept, _ := filepath.Glob(filepath.Join(dir, "skifity.db.before-restore-*"))
	if len(kept) == 0 {
		t.Fatal("the database that was replaced is gone")
	}
	var previous string
	for _, path := range kept {
		if !strings.HasSuffix(path, "-wal") && !strings.HasSuffix(path, "-shm") {
			previous = path
		}
	}
	if got := emailsIn(t, previous); len(got) != 1 || got[0] != "current@example.test" {
		t.Fatalf("what was kept holds %v, want the database it replaced", got)
	}
	if strings.Contains(out.String(), "do not open") {
		t.Fatalf("a backup sealed with this very key was said not to open with it:\n%s", out.String())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".restore-*")); len(leftovers) != 0 {
		t.Fatalf("the candidate was left behind: %v", leftovers)
	}
}

// Without --yes it only says what it would do, and leaves no file behind.
func TestRestoringWithoutYesChangesNothing(t *testing.T) {
	dir, target, keyPath, keyring := restoreHarness(t)
	source := filepath.Join(t.TempDir(), "backup.db")
	panelDatabase(t, source, "from-backup@example.test", keyring)
	backup := gzipped(t, source)
	before, _ := os.ReadDir(dir)

	var out bytes.Buffer
	if err := adminRestoreDatabase(context.Background(),
		[]string{"--database", target, "--master-key", keyPath, backup}, &out); err != nil {
		t.Fatalf("restore-db: %v", err)
	}
	if !strings.Contains(out.String(), "--yes") || !strings.Contains(out.String(), "1 accounts") ||
		!strings.Contains(out.String(), "scale deployment/"+version.Binary+"-panel --replicas=0") {
		t.Fatalf("the plan does not say what it found and how to go on:\n%s", out.String())
	}
	if got := emailsIn(t, target); len(got) != 1 || got[0] != "current@example.test" {
		t.Fatalf("the database changed without --yes: %v", got)
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("files appeared without --yes: %d before, %d after", len(before), len(after))
	}
}

// What is not a whole panel database is refused before anything is touched.
func TestRestoringRefusesWhatIsNotAWholePanelDatabase(t *testing.T) {
	dir, target, keyPath, _ := restoreHarness(t)

	empty := filepath.Join(t.TempDir(), "empty.db")
	db, err := store.Open(t.Context(), empty)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	var truncated bytes.Buffer
	zw := gzip.NewWriter(&truncated)
	_, _ = zw.Write(bytes.Repeat([]byte("SQLite format 3\x00"), 64))
	_ = zw.Close()
	cut := filepath.Join(t.TempDir(), "cut.db.gz")
	_ = os.WriteFile(cut, truncated.Bytes()[:truncated.Len()/2], 0o600)

	garbage := filepath.Join(t.TempDir(), "garbage.db")
	_ = os.WriteFile(garbage, []byte("this is not a database at all"), 0o600)

	cases := []struct {
		name, file, code string
	}{
		{"no accounts", empty, "admin.restore_not_a_panel"},
		{"cut off", cut, "admin.restore_unreadable"},
		{"not SQLite", garbage, "admin.restore_not_a_panel"},
		{"not there", filepath.Join(dir, "missing.db.gz"), "admin.restore_unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := adminRestoreDatabase(context.Background(),
				[]string{"--database", target, "--master-key", keyPath, "--yes", tc.file}, &out)
			var problem *errdoc.Problem
			if !errors.As(err, &problem) || problem.Code != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
			if got := emailsIn(t, target); len(got) != 1 || got[0] != "current@example.test" {
				t.Fatalf("the database changed after a refusal: %v", got)
			}
			if leftovers, _ := filepath.Glob(filepath.Join(dir, ".restore-*")); len(leftovers) != 0 {
				t.Fatalf("the candidate was left behind: %v", leftovers)
			}
			if kept, _ := filepath.Glob(filepath.Join(dir, "*.before-restore-*")); len(kept) != 0 {
				t.Fatalf("the database was moved aside for a backup that was refused: %v", kept)
			}
		})
	}
}

// A backup taken with a different master key restores, but says so: a panel
// started with the wrong key has every secret unreadable, which otherwise
// looks like a much stranger failure than it is.
func TestRestoringSaysWhenTheMasterKeyIsNotTheBackupsOne(t *testing.T) {
	_, target, keyPath, _ := restoreHarness(t)
	other, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "other.key"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "backup.db")
	panelDatabase(t, source, "from-backup@example.test", other)

	var out bytes.Buffer
	if err := adminRestoreDatabase(context.Background(),
		[]string{"--database", target, "--master-key", keyPath, gzipped(t, source)}, &out); err != nil {
		t.Fatalf("restore-db: %v", err)
	}
	if !strings.Contains(out.String(), "do not open with the master key at "+keyPath) {
		t.Fatalf("a backup sealed with another key was not called out:\n%s", out.String())
	}
}
