package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
)

// The recovery key was shown once, and nothing could read it back. These are
// the two halves of that: it opens only the database it belongs to, and it
// leaves what it replaces where it can be found.

func recoveryKeyOf(t *testing.T, ring *crypto.Keyring) string {
	t.Helper()
	key, err := ring.RecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestARecoveryKeyBringsTheMasterKeyBack(t *testing.T) {
	dir, target, keyPath, keyring := restoreHarness(t)
	recovery := recoveryKeyOf(t, keyring)

	// The key file is lost.
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := adminRestoreKey(t.Context(), []string{"--master-key", keyPath, "--database", target},
		strings.NewReader(recovery+"\n"), &out)
	if err != nil {
		t.Fatalf("restore-key: %v\n%s", err, out.String())
	}
	back, err := crypto.LoadKeyring(keyPath)
	if err != nil {
		t.Fatalf("the key file was not written: %v", err)
	}
	// It is the key the database was sealed with: the proof is that it opens it.
	if got := recoveryKeyOf(t, back); got != recovery {
		t.Errorf("the restored key is not the one that was lost")
	}
	if !strings.Contains(out.String(), "opens the panel's secrets") {
		t.Errorf("it did not say the key was checked against the database:\n%s", out.String())
	}
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the key file's permissions are %v, want 0600 (%v)", info.Mode().Perm(), err)
	}
	_ = dir
}

func TestARecoveryKeyForAnotherPanelChangesNothing(t *testing.T) {
	_, target, keyPath, _ := restoreHarness(t)
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	stranger, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "other.key"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = adminRestoreKey(t.Context(), []string{"--master-key", keyPath, "--database", target, "--yes"},
		strings.NewReader(recoveryKeyOf(t, stranger)+"\n"), &out)
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "admin.recovery_key_wrong" {
		t.Fatalf("a key that opens nothing was accepted: %v", err)
	}
	after, _ := os.ReadFile(keyPath)
	if !bytes.Equal(before, after) {
		t.Error("the key that was there was replaced by one that does not open the database")
	}
}

func TestRestoringAKeyOverOneThatIsThereNeedsYesAndKeepsTheOldOne(t *testing.T) {
	dir, target, keyPath, keyring := restoreHarness(t)
	recovery := recoveryKeyOf(t, keyring)

	var out bytes.Buffer
	err := adminRestoreKey(t.Context(), []string{"--master-key", keyPath, "--database", target},
		strings.NewReader(recovery+"\n"), &out)
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "admin.master_key_exists" {
		t.Fatalf("a key that is there was overwritten without being asked: %v", err)
	}

	out.Reset()
	if err := adminRestoreKey(t.Context(), []string{"--master-key", keyPath, "--database", target, "--yes"},
		strings.NewReader(recovery+"\n"), &out); err != nil {
		t.Fatalf("restore-key --yes: %v", err)
	}
	kept, _ := filepath.Glob(filepath.Join(dir, "master.key.before-restore-*"))
	if len(kept) != 1 {
		t.Errorf("the key that was replaced is not kept beside the new one: %v", kept)
	}
}

func TestARecoveryKeyThatCannotBeReadSaysSoAndChangesNothing(t *testing.T) {
	_, target, keyPath, _ := restoreHarness(t)
	for _, input := range []string{"", "\n", "this is not a recovery key\n", "SKIFITY-RECOVERY-v1-k1-AAAA\n"} {
		var out bytes.Buffer
		err := adminRestoreKey(t.Context(), []string{"--master-key", keyPath, "--database", target, "--yes"},
			strings.NewReader(input), &out)
		if err == nil {
			t.Errorf("%q was accepted", input)
		}
	}
	if _, err := crypto.LoadKeyring(keyPath); err != nil {
		t.Errorf("the key that was there no longer loads: %v", err)
	}
}
