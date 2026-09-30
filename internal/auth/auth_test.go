package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"skifity/internal/crypto"
	"skifity/internal/store"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("the hash contains the password")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %q", hash)
	}
	if err := VerifyPassword("correct horse battery staple", hash); err != nil {
		t.Fatalf("VerifyPassword with the right password: %v", err)
	}
	if err := VerifyPassword("correct horse battery stapler", hash); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("VerifyPassword with a wrong password gave %v, want ErrPasswordMismatch", err)
	}
}

func TestPasswordHashesAreSalted(t *testing.T) {
	a, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if a == b {
		t.Fatal("hashing the same password twice produced the same hash, so it is not salted")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=19456,t=2,p=4$c2FsdA$aGFzaA",  // wrong variant
		"$argon2id$v=16$m=19456,t=2,p=4$c2FsdA$aGFzaA", // wrong version
		"$argon2id$v=19$nonsense$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=4$!!!$aGFzaA",
	} {
		err := VerifyPassword("whatever", bad)
		if err == nil {
			t.Fatalf("VerifyPassword accepted the malformed hash %q", bad)
		}
		if errors.Is(err, ErrPasswordMismatch) {
			t.Fatalf("a malformed hash %q was reported as a wrong password, which hides a corrupt database", bad)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	current, err := HashPassword("something long enough")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if NeedsRehash(current) {
		t.Fatal("a freshly created hash was marked as needing a rehash")
	}
	if !NeedsRehash("$argon2id$v=19$m=4096,t=1,p=1$c2FsdA$aGFzaA") {
		t.Fatal("a hash with weaker parameters was not marked for upgrade")
	}
	if !NeedsRehash("not a hash at all") {
		t.Fatal("an unreadable hash should be replaced")
	}
}

func TestPasswordPolicy(t *testing.T) {
	p := DefaultPasswordPolicy()
	for _, bad := range []string{"", "short", "password1234", "PASSWORD123", "aaaaaaaaaaaaaaaa"} {
		if err := p.Check(bad); err == nil {
			t.Fatalf("policy accepted %q", bad)
		}
	}
	for _, good := range []string{"a reasonable passphrase", "Tr0ub4dor&3xyzzy!", "十分に長いパスワードです"} {
		if err := p.Check(good); err != nil {
			t.Fatalf("policy rejected %q: %v", good, err)
		}
	}
	// A very long password would make every login expensive.
	if err := p.Check(strings.Repeat("a", 2000)); err == nil {
		t.Fatal("policy accepted a 2000-character password")
	}
}

func TestTOTPMatchesRFC6238Vector(t *testing.T) {
	// RFC 6238 appendix B uses the ASCII secret "12345678901234567890".
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, tc := range cases {
		got, err := TOTPCode(secret, time.Unix(tc.unix, 0))
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		if got != tc.want {
			t.Fatalf("code at %d is %s, want %s (RFC 6238 test vector)", tc.unix, got, tc.want)
		}
	}
}

func TestVerifyTOTPAcceptsClockDrift(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	now := time.Now()

	code, err := TOTPCode(secret, now)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if _, err := VerifyTOTP(secret, code, now); err != nil {
		t.Fatalf("the current code was rejected: %v", err)
	}
	// A phone 30 seconds fast or slow must still work.
	for _, drift := range []time.Duration{-30 * time.Second, 30 * time.Second} {
		drifted, err := TOTPCode(secret, now.Add(drift))
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		if _, err := VerifyTOTP(secret, drifted, now); err != nil {
			t.Fatalf("a code %s out of date was rejected: %v", drift, err)
		}
	}
	// Two minutes out is a replay, not drift.
	stale, _ := TOTPCode(secret, now.Add(-2*time.Minute))
	if _, err := VerifyTOTP(secret, stale, now); !errors.Is(err, ErrInvalidTOTP) {
		t.Fatalf("a two-minute-old code was accepted: %v", err)
	}
}

func TestVerifyTOTPRejectsBadInput(t *testing.T) {
	secret, _ := GenerateTOTPSecret()
	now := time.Now()
	for _, bad := range []string{"", "123", "abcdef", "1234567"} {
		if _, err := VerifyTOTP(secret, bad, now); !errors.Is(err, ErrInvalidTOTP) {
			t.Fatalf("VerifyTOTP(%q) gave %v, want ErrInvalidTOTP", bad, err)
		}
	}
	// A code typed with the space authenticator apps display must work.
	code, _ := TOTPCode(secret, now)
	spaced := code[:3] + " " + code[3:]
	if _, err := VerifyTOTP(secret, spaced, now); err != nil {
		t.Fatalf("a code typed with a space was rejected: %v", err)
	}
}

func TestTOTPURIIsScannable(t *testing.T) {
	uri := TOTPURI("Skifity", "owner@example.test", "JBSWY3DPEHPK3PXP")
	for _, want := range []string{"otpauth://totp/", "secret=JBSWY3DPEHPK3PXP", "issuer=Skifity", "digits=6", "period=30", "algorithm=SHA1"} {
		if !strings.Contains(uri, want) {
			t.Fatalf("otpauth URI is missing %q: %s", want, uri)
		}
	}
}

func TestGenerateRecoveryCodesAreUnique(t *testing.T) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("got %d codes, want 10", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Fatalf("duplicate recovery code %q", c)
		}
		seen[c] = true
		if len(c) != 14 || c[4] != '-' || c[9] != '-' {
			t.Fatalf("recovery code %q is not in the XXXX-XXXX-XXXX shape", c)
		}
		// These are hashed with a fast hash on purpose, so their length is the
		// only thing protecting them in a stolen database. Twelve base32
		// characters is sixty bits.
		if len(NormaliseRecoveryCode(c)) < 12 {
			t.Fatalf("recovery code %q carries too little entropy", c)
		}
	}
}

func TestHashTokenIsStableAndOneWay(t *testing.T) {
	a := HashToken("tok_abc123")
	if a != HashToken("tok_abc123") {
		t.Fatal("HashToken is not deterministic, so no session would ever be found")
	}
	if a == HashToken("tok_abc124") {
		t.Fatal("two different tokens hashed to the same value")
	}
	if strings.Contains(a, "abc123") {
		t.Fatal("the hash contains the token")
	}
}

// Sign-in rate limiting is a safe default the product promises, and an
// untested limit is a limit that quietly stops working.
func TestSignInIsRateLimited(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()

	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create a keyring: %v", err)
	}
	service := NewService(db, keyring, time.Hour, false)

	const email = "owner@example.test"
	const password = "a reasonable passphrase"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash the password: %v", err)
	}
	user := store.User{Email: email, Name: "Owner", PasswordHash: hash, IsAdmin: true}
	if err := db.CreateUser(ctx, &user); err != nil {
		t.Fatalf("create the user: %v", err)
	}

	limit := DefaultLockout().MaxPerAccount

	// Wrong password, up to the limit: each is refused as a bad credential and
	// never leaks whether the account exists.
	for attempt := 0; attempt < limit; attempt++ {
		_, err := service.Login(ctx, email, "not the password", "", "198.51.100.10", "test")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d gave %v, want ErrInvalidCredentials", attempt+1, err)
		}
	}

	// One more, and the account is locked rather than checked.
	if _, err := service.Login(ctx, email, "not the password", "", "198.51.100.10", "test"); !errors.Is(err, ErrLockedOut) {
		t.Fatalf("after %d failures the answer was %v, want ErrLockedOut", limit, err)
	}

	// The *right* password must be refused too. A limit that lets the correct
	// password through is not a limit: an attacker who guesses it on the next
	// attempt walks in.
	if _, err := service.Login(ctx, email, password, "", "198.51.100.10", "test"); !errors.Is(err, ErrLockedOut) {
		t.Fatalf("a locked-out account accepted the right password: %v", err)
	}

	// Another address must not be punished for this one's failures, or one
	// attacker could lock everybody out of a shared account on purpose.
	second := store.User{Email: "other@example.test", Name: "Other", PasswordHash: hash}
	if err := db.CreateUser(ctx, &second); err != nil {
		t.Fatalf("create the second user: %v", err)
	}
	if _, err := service.Login(ctx, second.Email, password, "", "203.0.113.20", "test"); err != nil {
		t.Fatalf("an unrelated account from another address was refused: %v", err)
	}
}

// An unknown account and a wrong password must be indistinguishable, or the
// sign-in form becomes a way to enumerate who has an account here.
func TestUnknownAccountLooksLikeAWrongPassword(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()

	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create a keyring: %v", err)
	}
	service := NewService(db, keyring, time.Hour, false)

	hash, err := HashPassword("a reasonable passphrase")
	if err != nil {
		t.Fatalf("hash the password: %v", err)
	}
	user := store.User{Email: "owner@example.test", Name: "Owner", PasswordHash: hash}
	if err := db.CreateUser(ctx, &user); err != nil {
		t.Fatalf("create the user: %v", err)
	}

	_, wrongPassword := service.Login(ctx, user.Email, "wrong", "", "198.51.100.30", "test")
	_, noSuchAccount := service.Login(ctx, "nobody@example.test", "wrong", "", "198.51.100.31", "test")

	if !errors.Is(wrongPassword, ErrInvalidCredentials) || !errors.Is(noSuchAccount, ErrInvalidCredentials) {
		t.Fatalf("expected both to be ErrInvalidCredentials, got %v and %v", wrongPassword, noSuchAccount)
	}
	if wrongPassword.Error() != noSuchAccount.Error() {
		t.Errorf("the two failures read differently: %q and %q", wrongPassword, noSuchAccount)
	}
}

// A token's scopes were stored from the day tokens were added and never once
// consulted, so a token marked read-only could do everything its owner could.
func TestTokenScopesAreEnforced(t *testing.T) {
	for _, tc := range []struct {
		scopes, method string
		want           bool
	}{
		// No scope is full access, which is what every existing token has.
		{"", http.MethodGet, true},
		{"", http.MethodPost, true},
		{"   ", http.MethodDelete, true},

		{ScopeRead, http.MethodGet, true},
		{ScopeRead, http.MethodHead, true},
		{ScopeRead, http.MethodOptions, true},
		{ScopeRead, http.MethodPost, false},
		{ScopeRead, http.MethodPatch, false},
		{ScopeRead, http.MethodPut, false},
		{ScopeRead, http.MethodDelete, false},

		{ScopeWrite, http.MethodDelete, true},
		{ScopeWrite, http.MethodGet, true},
		{ScopeRead + "," + ScopeWrite, http.MethodDelete, true},
		{" read , write ", http.MethodPost, true},

		// A scope nobody enforces must not quietly mean full access.
		{"admin", http.MethodGet, false},
		{"readonly", http.MethodGet, false},
	} {
		if got := TokenAllows(tc.scopes, tc.method, "/api/apps/app_1"); got != tc.want {
			t.Errorf("TokenAllows(%q, %s) = %v, want %v", tc.scopes, tc.method, got, tc.want)
		}
	}
}

// A scope the panel would not enforce must be refused when the token is made,
// or somebody ends up with a token they believe is narrower than it is.
func TestUnknownScopesAreRefused(t *testing.T) {
	for _, scopes := range []string{"", "read", "write", "read,write", " read , write "} {
		if err := ValidateScopes(scopes); err != nil {
			t.Errorf("ValidateScopes(%q): %v", scopes, err)
		}
	}
	for _, scopes := range []string{"admin", "read,admin", "deploy", "*"} {
		if err := ValidateScopes(scopes); err == nil {
			t.Errorf("ValidateScopes(%q) accepted a scope nothing enforces", scopes)
		}
	}
}

// TestAnAccountWithNoPasswordCannotBeSignedIntoWithOne.
//
// Single sign-on creates accounts with no password hash at all. If an empty
// hash were ever treated as a match — or as "no password set, so anything
// works" — every account created through a provider would be open to anyone who
// knew the address. This is the shape of the bug, so it is the shape of the
// test: an account with an empty hash, and every password somebody might try.
func TestAnAccountWithNoPasswordCannotBeSignedIntoWithOne(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()

	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create a keyring: %v", err)
	}
	service := NewService(db, keyring, time.Hour, false)

	// What the single sign-on callback creates: no PasswordHash.
	user := store.User{Email: "sso@example.test", Name: "SSO"}
	if err := db.CreateUser(ctx, &user); err != nil {
		t.Fatalf("create the user: %v", err)
	}
	if user.PasswordHash != "" {
		t.Fatalf("the account was created with a password hash %q", user.PasswordHash)
	}

	for _, password := range []string{"", " ", "password", "$argon2id$v=19$m=19456,t=2,p=4$aaaa$bbbb"} {
		_, err := service.Login(ctx, user.Email, password, "", "198.51.100.10", "test")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("signing in as %s with %q gave %v, want ErrInvalidCredentials",
				user.Email, password, err)
		}
	}

	// And the hash verifier itself, so the guard does not depend on Login
	// happening to reach it.
	for _, hash := range []string{"", "not a hash", "$argon2id$", "$2y$10$something"} {
		if err := VerifyPassword("anything", hash); err == nil {
			t.Errorf("VerifyPassword accepted the stored hash %q", hash)
		}
	}
}

// A scope naming a resource is the whole point: two plugins that both need to
// write must not end up holding each other's powers.
func TestAResourceScopeReachesOnlyItsOwnResource(t *testing.T) {
	cases := []struct {
		name   string
		scopes string
		method string
		path   string
		want   bool
	}{
		{"reading apps with apps:read", "apps:read", http.MethodGet, "/api/apps/app_1", true},
		{"writing apps with apps:read", "apps:read", http.MethodPost, "/api/apps/app_1/deploy", false},
		{"reading servers with apps:read", "apps:read", http.MethodGet, "/api/servers", false},
		{"deleting a server with backups:write", "backups:write", http.MethodDelete, "/api/servers/srv_1", false},
		{"writing backups with backups:write", "backups:write", http.MethodPost, "/api/databases/db_1/backups", true},
		{"two scopes, each in its place", "apps:read,backups:write",
			http.MethodPost, "/api/databases/db_1/backups", true},
		{"two scopes, neither covering this", "apps:read,backups:write",
			http.MethodPost, "/api/servers", false},

		// A nested collection belongs to itself: a token that may read
		// deployments and not apps still gets the deployments.
		{"deployments under an app", "deployments:read", http.MethodGet,
			"/api/apps/app_1/deployments", true},
		{"the app itself", "deployments:read", http.MethodGet, "/api/apps/app_1", false},
		{"apps under an environment", "apps:read", http.MethodGet,
			"/api/environments/env_1/apps", true},

		// An environment is part of a project once its nested collections are
		// taken out.
		{"the environment itself", "projects:read", http.MethodGet, "/api/environments/env_1", true},

		// The prefix must not swallow a longer name.
		{"a route that merely starts the same way", "apps:read", http.MethodGet, "/api/appstore", false},

		// Fail closed: a route no scope covers is refused to a scoped token
		// rather than falling into whichever scope happened to be nearest.
		{"a route no scope names", "apps:write", http.MethodPost, "/api/something-new", false},

		// The older, unscoped form still means what it always did.
		{"plain write, anywhere", "write", http.MethodDelete, "/api/servers/srv_1", true},
		{"no scopes at all", "", http.MethodDelete, "/api/servers/srv_1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TokenAllows(tc.scopes, tc.method, tc.path); got != tc.want {
				t.Errorf("TokenAllows(%q, %s, %s) = %v, want %v",
					tc.scopes, tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// A scope that names something the panel cannot enforce must be refused when
// the token is made, or somebody hands out a token they believe is narrow.
func TestAScopeNamingSomethingUnknownIsRefused(t *testing.T) {
	for _, scopes := range []string{"apps:read", "apps:read,backups:write", "servers:write"} {
		if err := ValidateScopes(scopes); err != nil {
			t.Errorf("ValidateScopes(%q): %v", scopes, err)
		}
	}
	for _, scopes := range []string{
		"apps:admin",      // not an action
		"secrets:read",    // not a resource
		"keyring:read",    // especially not this one
		"*:write",         // no wildcard: full access is an empty scope list
		"apps:read:extra", // not a shape
	} {
		if err := ValidateScopes(scopes); err == nil {
			t.Errorf("ValidateScopes(%q) was accepted", scopes)
		}
	}
}

// A one-time password is one-time. The window is three thirty-second steps
// wide, so a code somebody read over a shoulder, off a screen share or out of a
// proxy log is otherwise good for another ninety seconds.
func TestATwoFactorCodeCannotBeUsedTwice(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()

	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create a keyring: %v", err)
	}
	service := NewService(db, keyring, time.Hour, false)

	const email = "owner@example.test"
	const password = "a reasonable passphrase"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash the password: %v", err)
	}
	user := store.User{Email: email, Name: "Owner", PasswordHash: hash, IsAdmin: true}
	if err := db.CreateUser(ctx, &user); err != nil {
		t.Fatalf("create the user: %v", err)
	}

	secret, _, err := service.SetupTOTP(ctx, &user, "Skifity")
	if err != nil {
		t.Fatalf("set up two-factor: %v", err)
	}
	code, err := TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate a code: %v", err)
	}
	if err := service.ConfirmTOTP(ctx, &user, code); err != nil {
		t.Fatalf("confirm two-factor: %v", err)
	}

	// The code that switched two-factor on is spent, so it cannot be the code
	// that gets past it.
	if _, err := service.Login(ctx, email, password, code, "198.51.100.10", "test"); !errors.Is(err, ErrInvalidTOTP) {
		t.Fatalf("the confirmation code signed somebody in: %v", err)
	}

	// A code from the next step works once...
	next, err := TOTPCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("generate the next code: %v", err)
	}
	if _, err := service.Login(ctx, email, password, next, "198.51.100.10", "test"); err != nil {
		t.Fatalf("a fresh code was refused: %v", err)
	}
	// ...and not twice.
	if _, err := service.Login(ctx, email, password, next, "198.51.100.10", "test"); !errors.Is(err, ErrInvalidTOTP) {
		t.Fatalf("the same code was accepted a second time: %v", err)
	}
}

// oldHashOf makes a valid argon2id hash with parameters this build considers
// out of date, so the upgrade path can be exercised with a real password.
func oldHashOf(t *testing.T, password string) string {
	t.Helper()
	salt := []byte("sixteen-byte-slt")
	const weakMemory, weakTime, weakParallelism = 4096, 1, 1
	sum := argon2.IDKey([]byte(password), salt, weakTime, weakMemory, weakParallelism, argonKeyLen)
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, weakMemory, weakTime, weakParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum))
	if !NeedsRehash(encoded) {
		t.Fatalf("this build does not consider %q out of date, so the upgrade path is not exercised", encoded)
	}
	if err := VerifyPassword(password, encoded); err != nil {
		t.Fatalf("the old-parameter hash does not verify: %v", err)
	}
	return encoded
}

// Signing in with an out-of-date hash replaces it.
//
// The write used to be thrown away unread, so a rehash that never landed looked
// exactly like one that did and the parameters would never actually move.
func TestSigningInUpgradesAnOutOfDateHash(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()
	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create a keyring: %v", err)
	}
	service := NewService(db, keyring, time.Hour, false)

	const email = "person@example.test"
	const password = "a reasonable passphrase"
	user := store.User{Email: email, Name: "Person", PasswordHash: oldHashOf(t, password), Locale: "en"}
	if err := db.CreateUser(ctx, &user); err != nil {
		t.Fatalf("create the user: %v", err)
	}

	if _, err := service.Login(ctx, email, password, "", "203.0.113.1", "test"); err != nil {
		t.Fatalf("sign in: %v", err)
	}

	after, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("read the user back: %v", err)
	}
	if NeedsRehash(after.PasswordHash) {
		t.Error("the hash was not upgraded, and nothing said so")
	}
	if err := VerifyPassword(password, after.PasswordHash); err != nil {
		t.Fatalf("the upgraded hash does not accept the password: %v", err)
	}
}

// Upgrading a hash writes the hash and nothing else.
//
// It happens in the middle of a sign-in, on a row read before the password was
// even checked. Writing all eleven columns from that copy meant the sign-in put
// back the name, locale, theme — and whether the account is disabled — as they
// were when it started, undoing whatever had changed in between.
func TestUpgradingAHashTouchesNothingElse(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()

	user := store.User{Email: "person@example.test", Name: "Person", PasswordHash: "placeholder", Locale: "en"}
	if err := db.CreateUser(ctx, &user); err != nil {
		t.Fatalf("create the user: %v", err)
	}
	// The copy a sign-in would be holding: read before anything changed.
	stale, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("read the user: %v", err)
	}

	// Somebody else changes the row while the sign-in is in flight.
	changed := stale
	changed.Locale = "id"
	changed.Name = "Renamed"
	changed.Disabled = true
	if err := db.UpdateUser(ctx, &changed); err != nil {
		t.Fatalf("change the user: %v", err)
	}

	// What the sign-in does with its stale copy in hand.
	if err := db.UpdatePasswordHash(ctx, stale.ID, "the-upgraded-hash"); err != nil {
		t.Fatalf("upgrade the hash: %v", err)
	}

	after, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("read the user back: %v", err)
	}
	if after.PasswordHash != "the-upgraded-hash" {
		t.Errorf("the hash was not written: %q", after.PasswordHash)
	}
	if !after.Disabled {
		t.Error("the account was put back to enabled")
	}
	if after.Locale != "id" || after.Name != "Renamed" {
		t.Errorf("the other change was undone: locale %q, name %q", after.Locale, after.Name)
	}
}
