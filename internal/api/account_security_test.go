package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skifity/internal/auth"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// The account page's security actions, against the router the panel serves:
// a new set of recovery codes, a first password for an account that has none,
// and the recovery key an administrator is asked to save again after the
// master key changes.

// sessionFor signs a person in the way something other than the password form
// does — the identity provider, a second factor — and returns the browser.
func (h *harness) sessionFor(t *testing.T, user store.User, method string) (browser, store.Session) {
	t.Helper()
	result, err := h.auth.IssueSession(t.Context(), user, "10.0.0.1", "test", method)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	session, err := h.db.GetSessionByHash(t.Context(), auth.HashToken(result.Token))
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	return browser{
		cookies: []*http.Cookie{{Name: auth.SessionCookieName, Value: result.Token}},
		csrf:    result.CSRFToken,
	}, session
}

// withTwoFactor turns two-factor on for an account, as confirming the first
// code does, and gives it a set of recovery codes.
func (h *harness) withTwoFactor(t *testing.T, user *store.User) []string {
	t.Helper()
	secret, _, err := h.auth.SetupTOTP(t.Context(), user, "Skifity")
	if err != nil {
		t.Fatalf("set up two-factor: %v", err)
	}
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("make a code: %v", err)
	}
	if err := h.auth.ConfirmTOTP(t.Context(), user, code, ""); err != nil {
		t.Fatalf("confirm two-factor: %v", err)
	}
	codes, err := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err != nil {
		t.Fatalf("make recovery codes: %v", err)
	}
	if err := h.auth.StoreRecoveryCodes(t.Context(), user.ID, codes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}
	return codes
}

// TestNewRecoveryCodesReplaceTheOldOnes: "Get new codes" was sent to the route
// that starts two-factor setup, which refuses an account that has it on, so it
// failed every time — for the person it exists for, who has none left.
func TestNewRecoveryCodesReplaceTheOldOnes(t *testing.T) {
	const password = "correct horse battery staple"
	h := newHarness(t)
	user := h.person(t, "codes@example.test", password)
	old := h.withTwoFactor(t, &user)
	b, _ := h.sessionFor(t, user, auth.MethodTOTP)

	status, body := h.send(t, b, http.MethodPost, "/api/me/totp/recovery-codes", b.csrf, nil)
	if status != http.StatusOK {
		t.Fatalf("new recovery codes answered %d: %s", status, body)
	}
	var answer struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(answer.RecoveryCodes) != auth.RecoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(answer.RecoveryCodes), auth.RecoveryCodeCount)
	}
	if left, _ := h.auth.RecoveryCodesLeft(t.Context(), user.ID); left != auth.RecoveryCodeCount {
		t.Errorf("%d codes are stored, want %d", left, auth.RecoveryCodeCount)
	}
	if used, _ := h.db.UseRecoveryCode(t.Context(), user.ID, auth.HashRecoveryCode(old[0])); used {
		t.Error("a code from the old set still works")
	}
	if used, _ := h.db.UseRecoveryCode(t.Context(), user.ID, auth.HashRecoveryCode(answer.RecoveryCodes[0])); !used {
		t.Error("a code from the new set does not work")
	}

	// They get past the second factor, so a borrowed session must not be able
	// to mint a set of its own.
	h.spendReauth(t, "codes@example.test")
	status, body = h.send(t, b, http.MethodPost, "/api/me/totp/recovery-codes", b.csrf, nil)
	if status != http.StatusForbidden || !strings.Contains(body, "auth.reauth_required") {
		t.Errorf("without a step-up, new codes answered %d: %s", status, body)
	}

	// And an account with no second factor has nothing for them to get past.
	plain := h.person(t, "plain@example.test", password)
	pb, _ := h.sessionFor(t, plain, auth.MethodPassword)
	status, body = h.send(t, pb, http.MethodPost, "/api/me/totp/recovery-codes", pb.csrf, nil)
	if status != http.StatusConflict || !strings.Contains(body, "auth.totp_off") {
		t.Errorf("new codes for an account without two-factor answered %d: %s", status, body)
	}
	if left, _ := h.auth.RecoveryCodesLeft(t.Context(), plain.ID); left != 0 {
		t.Errorf("an account without two-factor was given %d codes", left)
	}
}

// TestAnAccountWithoutAPasswordCanSetItsFirst: the page told an account that
// signs in through the identity provider to set a password, and the only form
// wanted the current one — which it does not have — and counted every attempt
// as a wrong guess towards a lockout.
func TestAnAccountWithoutAPasswordCanSetItsFirst(t *testing.T) {
	const password = "a perfectly long passphrase"
	h := newHarness(t)
	user := store.User{Email: "sso@example.test", Name: "sso"}
	if err := h.db.CreateUser(t.Context(), &user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	b, session := h.sessionFor(t, user, auth.MethodSSO)
	elsewhere, _ := h.sessionFor(t, user, auth.MethodSSO)

	if has := hasPassword(t, h, b); has {
		t.Fatal("an account with no password says it has one")
	}

	// The form that changes a password says why it cannot, and does not count
	// the attempt against the account.
	status, body := h.send(t, b, http.MethodPost, "/api/me/password", b.csrf,
		map[string]string{"current_password": "", "new_password": password})
	if status != http.StatusConflict || !strings.Contains(body, "auth.password_missing") {
		t.Errorf("changing a password the account does not have answered %d: %s", status, body)
	}
	failed, _, err := h.db.CountFailedLogins(t.Context(), user.Email, "", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("count failures: %v", err)
	}
	if failed != 0 {
		t.Errorf("%d failed sign-ins were recorded for asking", failed)
	}

	// A first password wants a recent sign-in, which is what stands in for
	// the current one.
	h.spendReauth(t, user.Email)
	status, body = h.send(t, b, http.MethodPut, "/api/me/password", b.csrf,
		map[string]string{"new_password": password})
	if status != http.StatusForbidden || !strings.Contains(body, "auth.reauth_required") {
		t.Fatalf("a first password without a recent sign-in answered %d: %s", status, body)
	}
	if err := h.db.MarkSessionReauthenticated(t.Context(), session.ID, time.Now()); err != nil {
		t.Fatalf("step up: %v", err)
	}

	status, _ = h.send(t, b, http.MethodPut, "/api/me/password", b.csrf,
		map[string]string{"new_password": "short"})
	if status != http.StatusBadRequest {
		t.Errorf("a password the policy refuses answered %d", status)
	}

	status, body = h.send(t, b, http.MethodPut, "/api/me/password", b.csrf,
		map[string]string{"new_password": password})
	if status != http.StatusOK {
		t.Fatalf("setting a first password answered %d: %s", status, body)
	}
	if !hasPassword(t, h, b) {
		t.Error("after setting one, the account says it has no password")
	}
	if status, _ := h.send(t, elsewhere, http.MethodGet, "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Errorf("another session survived a new password: %d", status)
	}
	h.signIn(t, user.Email, password)

	// Once there is one, this is not a way around the form that asks for it.
	status, body = h.send(t, b, http.MethodPut, "/api/me/password", b.csrf,
		map[string]string{"new_password": "another long passphrase"})
	if status != http.StatusConflict || !strings.Contains(body, "auth.password_exists") {
		t.Errorf("replacing a password without the current one answered %d: %s", status, body)
	}
}

func hasPassword(t *testing.T, h *harness, b browser) bool {
	t.Helper()
	status, body := h.send(t, b, http.MethodGet, "/api/me", "", nil)
	if status != http.StatusOK {
		t.Fatalf("/api/me answered %d: %s", status, body)
	}
	var me struct {
		HasPassword *bool `json:"has_password"`
	}
	if err := json.Unmarshal([]byte(body), &me); err != nil || me.HasPassword == nil {
		t.Fatalf("/api/me does not say whether there is a password: %s", body)
	}
	return *me.HasPassword
}

// TestRotatingTheMasterKeyAsksForTheRecoveryKeyAgain: the recovery key is the
// master key, so after a rotation the copy every administrator saved opens
// nothing that was rewrapped. Nothing said so, and the reminder that asks for
// it to be saved never came back.
func TestRotatingTheMasterKeyAsksForTheRecoveryKeyAgain(t *testing.T) {
	const password = "correct horse battery staple"
	h := newHarness(t)
	h.api.cfg.MasterKeyPath = filepath.Join(t.TempDir(), "master.key")

	rotator := h.person(t, "rotator@example.test", password)
	other := h.person(t, "other-admin@example.test", password)
	member := h.person(t, "member@example.test", password)
	for _, id := range []string{rotator.ID, other.ID} {
		if _, err := h.db.Exec(t.Context(), `UPDATE users SET is_admin = 1 WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{rotator.ID, other.ID, member.ID} {
		if _, err := h.db.Exec(t.Context(), `UPDATE users SET recovery_saved = 1 WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	before, err := h.keyring.RecoveryKey()
	if err != nil {
		t.Fatalf("read the recovery key: %v", err)
	}

	b := h.signIn(t, rotator.Email, password)
	status, body := h.send(t, b, http.MethodPost, "/api/security/rotate-key", b.csrf, nil)
	if status != http.StatusOK {
		t.Fatalf("rotating answered %d: %s", status, body)
	}
	var answer struct {
		RecoveryKey string `json:"recovery_key"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.RecoveryKey == "" || answer.RecoveryKey == before {
		t.Fatal("the answer does not carry the new recovery key")
	}

	for _, check := range []struct {
		user  store.User
		saved bool
	}{{rotator, false}, {other, false}, {member, true}} {
		stored, err := h.db.GetUser(t.Context(), check.user.ID)
		if err != nil {
			t.Fatalf("read %s: %v", check.user.Email, err)
		}
		if stored.RecoverySaved != check.saved {
			t.Errorf("%s: recovery_saved is %v after a rotation, want %v",
				check.user.Email, stored.RecoverySaved, check.saved)
		}
	}
}

// TestASettingSaysWhatUnsetMeans: a switch the panel treats as on until
// somebody turns it off was drawn off, because the answer only carried what
// was stored — beside help text saying "On by default".
func TestASettingSaysWhatUnsetMeans(t *testing.T) {
	h := newHarness(t)
	admin := adminTenant(h, "ops")
	if err := h.db.SetSetting(t.Context(), settings.KeyScanBlockCritical, "true", false, admin.user.ID); err != nil {
		t.Fatalf("store a setting: %v", err)
	}

	status, body := h.do(admin, http.MethodGet, "/api/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("listing settings answered %d: %s", status, body)
	}
	var list struct {
		Items []settingView `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	views := map[string]settingView{}
	for _, view := range list.Items {
		views[view.Key] = view
	}

	for key, want := range map[string]string{
		settings.KeyScanEnabled: "true", settings.KeyGeoEnabled: "true", settings.KeySMTPTLS: "true",
		settings.KeyBuilderDefault: "railpack",
	} {
		view := views[key]
		if view.Default != want || view.Value != "" {
			t.Errorf("%s, unset: default %q and value %q, want default %q and no value",
				key, view.Default, view.Value, want)
		}
	}
	if view := views[settings.KeyScanBlockCritical]; view.Value != "true" || view.Default != "false" {
		t.Errorf("a stored switch reads value %q, default %q", view.Value, view.Default)
	}
}
