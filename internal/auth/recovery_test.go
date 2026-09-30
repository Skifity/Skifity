package auth

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skifity/internal/crypto"
	"skifity/internal/store"
)

// recoveryFixture is an account with two-factor turned on and its recovery
// codes written down.
func recoveryFixture(t *testing.T) (*Service, *store.DB, store.User, string, []string) {
	t.Helper()
	ctx := t.Context()

	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create a keyring: %v", err)
	}
	service := NewService(db, keyring, time.Hour, false)

	const password = "a reasonable passphrase"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash the password: %v", err)
	}
	user := store.User{Email: "owner@example.test", Name: "Owner", PasswordHash: hash}
	if err := db.CreateUser(ctx, &user); err != nil {
		t.Fatalf("create the user: %v", err)
	}

	secret, _, err := service.SetupTOTP(ctx, &user, "Skifity")
	if err != nil {
		t.Fatalf("SetupTOTP: %v", err)
	}
	codes, err := GenerateRecoveryCodes(8)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if err := service.StoreRecoveryCodes(ctx, user.ID, codes); err != nil {
		t.Fatalf("StoreRecoveryCodes: %v", err)
	}

	code, err := TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if err := service.ConfirmTOTP(ctx, &user, code); err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}

	return service, db, user, password, codes
}

// TestRecoveryCodeSignsYouIn is the whole point: a phone in a taxi must not be
// the end of an account. The codes were shown, written down, and until now
// thrown away by the panel that printed them.
func TestRecoveryCodeSignsYouIn(t *testing.T) {
	service, _, user, password, codes := recoveryFixture(t)
	ctx := t.Context()

	result, err := service.Login(ctx, user.Email, password, codes[0], "198.51.100.10", "test")
	if err != nil {
		t.Fatalf("signing in with a recovery code: %v", err)
	}
	if !result.UsedRecoveryCode {
		t.Error("the sign-in did not report that a recovery code was spent")
	}
	if result.RecoveryCodesLeft != 7 {
		t.Errorf("%d codes left, want 7", result.RecoveryCodesLeft)
	}
}

// TestRecoveryCodeWorksOnce: a code that can be replayed is a password that
// never expires, written on a piece of paper.
func TestRecoveryCodeWorksOnce(t *testing.T) {
	service, _, user, password, codes := recoveryFixture(t)
	ctx := t.Context()

	if _, err := service.Login(ctx, user.Email, password, codes[0], "198.51.100.10", "test"); err != nil {
		t.Fatalf("first use: %v", err)
	}
	_, err := service.Login(ctx, user.Email, password, codes[0], "198.51.100.10", "test")
	if err == nil {
		t.Fatal("the same recovery code was accepted twice")
	}
	if errors.Is(err, ErrLockedOut) {
		t.Fatalf("expected the code to be rejected, not the account locked: %v", err)
	}
}

// TestRecoveryCodesTolerateHowPeopleTypeThem: these are read off paper, so the
// dashes get left out and the case is whatever the keyboard was in.
func TestRecoveryCodesTolerateHowPeopleTypeThem(t *testing.T) {
	service, _, user, password, codes := recoveryFixture(t)
	ctx := t.Context()

	typed := strings.ToLower(strings.ReplaceAll(codes[0], "-", " "))
	if _, err := service.Login(ctx, user.Email, password, typed, "198.51.100.10", "test"); err != nil {
		t.Fatalf("signing in with %q: %v", typed, err)
	}
}

// TestWrongCodeIsStillRejected: accepting recovery codes must not turn the
// second factor into a formality.
func TestWrongCodeIsStillRejected(t *testing.T) {
	service, _, user, password, _ := recoveryFixture(t)
	ctx := t.Context()

	if _, err := service.Login(ctx, user.Email, password, "AAAA-BBBB-CCCC", "198.51.100.10", "test"); err == nil {
		t.Fatal("a made-up recovery code signed the user in")
	}
	if _, err := service.Login(ctx, user.Email, password, "000000", "198.51.100.10", "test"); err == nil {
		t.Fatal("a wrong six-digit code signed the user in")
	}
}

// TestTurningTwoFactorOffForgetsTheCodes: leaving them behind would be a
// second way in that the user believes they have closed.
func TestTurningTwoFactorOffForgetsTheCodes(t *testing.T) {
	service, db, user, _, _ := recoveryFixture(t)
	ctx := t.Context()

	if err := service.DisableTOTP(ctx, &user); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}
	left, err := db.CountRecoveryCodes(ctx, user.ID)
	if err != nil {
		t.Fatalf("CountRecoveryCodes: %v", err)
	}
	if left != 0 {
		t.Fatalf("%d recovery codes survived two-factor being turned off", left)
	}
}

// TestNewCodesReplaceTheOldOnes: the codes on screen are the only set the user
// has written down, so an older set is one nobody can produce and everybody
// would still accept.
func TestNewCodesReplaceTheOldOnes(t *testing.T) {
	service, _, user, password, codes := recoveryFixture(t)
	ctx := t.Context()

	fresh, err := GenerateRecoveryCodes(8)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if err := service.StoreRecoveryCodes(ctx, user.ID, fresh); err != nil {
		t.Fatalf("StoreRecoveryCodes: %v", err)
	}

	if _, err := service.Login(ctx, user.Email, password, codes[0], "198.51.100.10", "test"); err == nil {
		t.Fatal("a code from the replaced set still worked")
	}
	if _, err := service.Login(ctx, user.Email, password, fresh[0], "203.0.113.20", "test"); err != nil {
		t.Fatalf("a code from the current set was refused: %v", err)
	}
}

// TestRecoveryCodesAreStoredHashed: the database is the thing that gets
// stolen, and a readable code in it is a second factor that is not one.
func TestRecoveryCodesAreStoredHashed(t *testing.T) {
	_, db, user, _, codes := recoveryFixture(t)

	var stored string
	err := db.QueryRowContext(t.Context(),
		`SELECT code_hash FROM recovery_codes WHERE user_id = ? LIMIT 1`, user.ID).Scan(&stored)
	if err != nil {
		t.Fatalf("read a stored recovery code: %v", err)
	}
	for _, code := range codes {
		if strings.Contains(stored, NormaliseRecoveryCode(code)) {
			t.Fatal("a recovery code is stored in a form somebody could read and use")
		}
	}
}
