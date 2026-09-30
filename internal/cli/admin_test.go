package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/store"
)

// Resetting a password from the server leaves the account's passkeys alone
// and says so, unless it is asked to remove them. A reset replaces a forgotten
// password; a passkey is another way in that the password never opened, and
// taking it away without a word would take the one credential that cannot be
// phished. When the reset is because somebody else got in, they may have added
// a passkey of their own — which is what --remove-passkeys is for.
func TestAnAdminResetKeepsPasskeysUnlessToldOtherwise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	user := store.User{Email: "owner@example.test", Name: "Owner", PasswordHash: "x"}
	if err := db.CreateUser(t.Context(), &user); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pky_1", "pky_2"} {
		if err := db.CreatePasskey(t.Context(), &store.Passkey{
			ID: id, UserID: user.ID, CredentialID: "cred-" + id, PublicKeyEnc: "SKF1.x",
			RPID: "panel.example.test", Name: id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	reset := func(extra ...string) map[string]any {
		t.Helper()
		var out bytes.Buffer
		// The address first and the flags after it, as the usage line and the
		// documentation write it.
		args := append([]string{"owner@example.test", "--database", path,
			"--password", "an entirely different passphrase", "--json"}, extra...)
		if err := adminResetPassword(t.Context(), args, &out); err != nil {
			t.Fatalf("reset-password %v: %v", extra, err)
		}
		var answer map[string]any
		if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
			t.Fatalf("decode %q: %v", out.String(), err)
		}
		return answer
	}

	answer := reset()
	if answer["passkeys_left"] != float64(2) || answer["passkeys_removed"] != float64(0) {
		t.Fatalf("a plain reset answered %v", answer)
	}
	answer = reset("--remove-passkeys")
	if answer["passkeys_left"] != float64(0) || answer["passkeys_removed"] != float64(2) {
		t.Fatalf("a reset with --remove-passkeys answered %v", answer)
	}

	// And in words, the note that they are still there.
	db, err = store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreatePasskey(t.Context(), &store.Passkey{
		ID: "pky_3", UserID: user.ID, CredentialID: "cred-3", PublicKeyEnc: "SKF1.x", RPID: "panel.example.test", Name: "phone",
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var out bytes.Buffer
	if err := adminResetPassword(t.Context(), []string{"--database", path,
		"--password", "yet another passphrase here", "owner@example.test"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the account has 1 passkey") || !strings.Contains(out.String(), "--remove-passkeys") {
		t.Fatalf("the reset did not say the passkey is still there:\n%s", out.String())
	}
}
