package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skifity/internal/auth"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
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

// A lost phone with the recovery codes gone used to be a lockout: turning
// two-factor off needs a code, and for the panel's only administrator nobody
// else could do it. reset-2fa is the way back, from the server: the secret,
// the codes and every session go, the activity log says so, and the password
// on its own signs in again.
func TestAnAdminCanResetTwoFactorFromTheServer(t *testing.T) {
	const password = "a long enough passphrase"
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	service := auth.NewService(db, keyring, time.Hour, false)

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	user := store.User{Email: "lost@example.test", Name: "Lost", PasswordHash: hash}
	if err := db.CreateUser(t.Context(), &user); err != nil {
		t.Fatal(err)
	}
	team := store.Team{Name: "Acme", Slug: "acme", RequireStrongAuth: true}
	if err := db.CreateTeam(t.Context(), &team); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTeam(t.Context(), &team); err != nil {
		t.Fatal(err)
	}
	if err := db.AddMember(t.Context(), team.ID, user.ID, store.RoleOwner); err != nil {
		t.Fatal(err)
	}
	secret, _, err := service.SetupTOTP(t.Context(), &user, "Skifity")
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConfirmTOTP(t.Context(), &user, code, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.StoreRecoveryCodes(t.Context(), user.ID, []string{"AAAA-BBBB-CCCC", "DDDD-EEEE-FFFF"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.IssueSession(t.Context(), user, "10.0.0.1", "test", auth.MethodTOTP); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(t.Context(), user.Email, password, "", "10.0.0.1", "test"); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("before the reset, a password alone answered %v", err)
	}
	db.Close()

	var out bytes.Buffer
	if err := adminResetTwoFactor(t.Context(), []string{"LOST@example.test", "--database", path, "--json"}, &out); err != nil {
		t.Fatalf("reset-2fa: %v", err)
	}
	var answer map[string]any
	if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if answer["two_factor_was_on"] != true || answer["sessions_ended"] != true {
		t.Errorf("reset-2fa answered %v", answer)
	}
	if teams, _ := answer["strong_auth_teams"].([]any); len(teams) != 1 || teams[0] != "Acme" {
		t.Errorf("reset-2fa did not name the team that requires a second factor: %v", answer)
	}

	db, err = store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	after, err := db.GetUser(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPEnabled || after.TOTPSecretEnc != "" {
		t.Errorf("two-factor is still there: enabled %v, secret %q", after.TOTPEnabled, after.TOTPSecretEnc)
	}
	if left, _ := db.CountRecoveryCodes(t.Context(), user.ID); left != 0 {
		t.Errorf("%d recovery codes are left", left)
	}
	if sessions, _ := db.ListSessions(t.Context(), user.ID); len(sessions) != 0 {
		t.Errorf("%d sessions are still signed in", len(sessions))
	}
	var audited int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events
		WHERE action = 'account.two_factor_reset' AND target_id = ? AND team_id = ? AND actor_label = ?`,
		user.ID, team.ID, adminActor).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Errorf("the activity log has %d entries for the reset, want 1", audited)
	}

	// And the password alone signs in now.
	service = auth.NewService(db, keyring, time.Hour, false)
	if _, err := service.Login(t.Context(), user.Email, password, "", "10.0.0.1", "test"); err != nil {
		t.Errorf("after the reset, the password alone answered %v", err)
	}

	// An address nobody uses changes nothing, and says what to run instead.
	err = adminResetTwoFactor(t.Context(), []string{"nobody@example.test", "--database", path}, &out)
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "admin.no_such_user" {
		t.Errorf("an unknown address answered %v", err)
	}
}

// When the only administrator has left, root on the server is the way back:
// make somebody else one, then disable the account that went. The panel's last
// enabled administrator is kept here as it is in the panel, and each change is
// in the activity log.
func TestAnAdminCanBeMadeAndAnAccountDisabledFromTheServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	gone := store.User{Email: "gone@example.test", PasswordHash: "x", IsAdmin: true}
	stays := store.User{Email: "stays@example.test", PasswordHash: "x"}
	for _, user := range []*store.User{&gone, &stays} {
		if err := db.CreateUser(t.Context(), user); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	run := func(command, email string) (map[string]any, error) {
		t.Helper()
		var out bytes.Buffer
		err := adminChangeAccount(t.Context(), command, []string{email, "--database", path, "--json"}, &out)
		if err != nil {
			return nil, err
		}
		var answer map[string]any
		if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
			t.Fatalf("decode %q: %v", out.String(), err)
		}
		return answer, nil
	}
	isProblem := func(err error, code string) bool {
		var problem *errdoc.Problem
		return errors.As(err, &problem) && problem.Code == code
	}

	// The only administrator cannot be disabled or demoted yet.
	for _, command := range []string{"disable", "revoke-admin"} {
		if _, err := run(command, gone.Email); !isProblem(err, "account.last_admin") {
			t.Errorf("%s on the only administrator answered %v", command, err)
		}
	}
	if answer, err := run("make-admin", stays.Email); err != nil || answer["is_admin"] != true || answer["changed"] != true {
		t.Fatalf("make-admin answered %v, %v", answer, err)
	}
	if answer, err := run("make-admin", stays.Email); err != nil || answer["changed"] != false {
		t.Errorf("make-admin a second time answered %v, %v", answer, err)
	}
	if answer, err := run("disable", gone.Email); err != nil || answer["disabled"] != true {
		t.Fatalf("disable answered %v, %v", answer, err)
	}
	// A disabled administrator is no administrator: the other is now the last.
	if _, err := run("revoke-admin", stays.Email); !isProblem(err, "account.last_admin") {
		t.Errorf("revoke-admin on the last enabled administrator answered %v", err)
	}
	if _, err := run("enable", "nobody@example.test"); !isProblem(err, "admin.no_such_user") {
		t.Errorf("an unknown address answered %v", err)
	}

	db, err = store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var actions []string
	rows, err := db.QueryContext(t.Context(), `SELECT action FROM audit_events WHERE actor_label = ? ORDER BY at, rowid`, adminActor)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, " ") != "account.admin_granted account.disabled" {
		t.Errorf("the activity log has %v, want one entry for each change made", actions)
	}
}
