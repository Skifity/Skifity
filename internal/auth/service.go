package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"skifity/internal/crypto"
	"skifity/internal/store"
)

// SessionCookieName is the cookie the panel uses for browser sessions.
const SessionCookieName = "skifity_session"

// CSRFCookieName carries the CSRF token to the frontend so it can echo it.
const CSRFCookieName = "skifity_csrf"

// CSRFHeaderName is where the frontend echoes the CSRF token back.
const CSRFHeaderName = "X-Skifity-CSRF"

// HostPrefix is the "__Host-" cookie prefix, and it is the only defence there
// is against a page on a sibling subdomain writing this panel's cookies.
//
// A cookie is not isolated by origin. Anything served from a sibling name —
// app.example.com writing one for .example.com — lands in the browser's jar
// next to the panel's own, with the same name, and the panel cannot tell them
// apart. For most products that is a remote risk. For this one it is the
// product: Skifity hosts other people's applications, and an operator who
// points a wildcard at the cluster and puts the panel on the same domain has
// given every app a way to write the panel's session cookie. That is session
// fixation — the victim silently signed in as the attacker, typing secrets
// into an account somebody else can read.
//
// A browser refuses to store a __Host- cookie that carries a Domain attribute
// at all, so a sibling cannot write one. It also requires Secure and Path=/,
// which is why it can only be used when the panel is on HTTPS.
const HostPrefix = "__Host-"

// TokenPrefix marks an API token so it can be spotted in a log or a leak scan.
const TokenPrefix = "skf_"

// Lockout describes the sign-in rate limit.
//
// Two separate limits: one per account so one person cannot be locked out by an
// attacker hammering a different account, and one per address so an attacker
// cannot spray many accounts from one machine.
type Lockout struct {
	Window          time.Duration
	MaxPerAccount   int
	MaxPerIPAddress int
}

// DefaultLockout is what the panel enforces.
func DefaultLockout() Lockout {
	return Lockout{Window: 15 * time.Minute, MaxPerAccount: 5, MaxPerIPAddress: 20}
}

// ErrLockedOut is returned when the rate limit has been hit.
var ErrLockedOut = errors.New("too many failed sign-in attempts")

// ErrInvalidCredentials is the single error returned for any authentication
// failure, so an attacker cannot tell an unknown account from a wrong password.
var ErrInvalidCredentials = errors.New("email or password is not correct")

// ErrTOTPRequired is returned when a password was right but a second factor is
// needed. It is safe to distinguish: the password was already correct.
var ErrTOTPRequired = errors.New("a two-factor code is required")

// MaxSessionLife is how long a session may live however much it is used.
//
// The sliding expiry answers "has this person been away too long", and on its
// own it never answers "how long has this cookie been valid" — a session that
// is used once a day renews forever, so a token stolen in January is still
// good in December. This is the ceiling: past it, sign in again.
const MaxSessionLife = 30 * 24 * time.Hour

// ReauthWindow is how long proving who you are counts for.
//
// Long enough to turn two-factor off and read the recovery codes without being
// asked twice; short enough that a session borrowed from an unlocked laptop
// cannot do either of those things.
const ReauthWindow = 5 * time.Minute

// maxConcurrentHashes bounds how many Argon2 hashes run at once.
//
// Each one holds 19 MiB while it runs, and every sign-in attempt starts one —
// including the attempts for accounts that do not exist, which have to hash
// anyway so that timing does not say which addresses are real. On the 1 GB
// server this product targets, an unauthenticated caller could otherwise turn
// a handful of requests per second into every byte of memory on the machine.
// Requests past this wait rather than being refused, which keeps a real
// sign-in working while a flood is in progress.
const maxConcurrentHashes = 4

// Service performs authentication against the store.
type Service struct {
	db         *store.DB
	keyring    *crypto.Keyring
	lockout    Lockout
	sessionTTL time.Duration
	// secureCookies is false only in development over plain http.
	secureCookies bool
	// hashes admits a bounded number of password hashes at a time.
	hashes chan struct{}
}

// NewService builds an auth service.
func NewService(db *store.DB, keyring *crypto.Keyring, sessionTTL time.Duration, secureCookies bool) *Service {
	return &Service{
		db:            db,
		keyring:       keyring,
		lockout:       DefaultLockout(),
		sessionTTL:    sessionTTL,
		secureCookies: secureCookies,
		hashes:        make(chan struct{}, maxConcurrentHashes),
	}
}

// hash and verify run the expensive Argon2 work through the gate above. The
// context is honoured while waiting, so a caller that has gone away does not
// keep a slot somebody else could use.
func (s *Service) enterHash(ctx context.Context) (release func(), err error) {
	select {
	case s.hashes <- struct{}{}:
		return func() { <-s.hashes }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) hashPassword(ctx context.Context, password string) (string, error) {
	release, err := s.enterHash(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return HashPassword(password)
}

func (s *Service) verifyPassword(ctx context.Context, password, encoded string) error {
	release, err := s.enterHash(ctx)
	if err != nil {
		return err
	}
	defer release()
	return VerifyPassword(password, encoded)
}

// LoginResult is what a successful sign-in produces.
type LoginResult struct {
	User      store.User
	Token     string
	CSRFToken string
	ExpiresAt time.Time
	// UsedRecoveryCode is true when the second factor was a recovery code
	// rather than the authenticator, so the panel can say one is gone.
	UsedRecoveryCode  bool
	RecoveryCodesLeft int
}

// Login verifies credentials and issues a session.
//
// totpCode may be empty on the first call; when the account has two-factor
// enabled, ErrTOTPRequired comes back and the caller asks for a code.
func (s *Service) Login(ctx context.Context, email, password, totpCode, ip, userAgent string) (LoginResult, error) {
	if err := s.checkLockout(ctx, email, ip); err != nil {
		return LoginResult{}, err
	}

	user, err := s.db.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Hash anyway so an unknown account takes as long as a known one.
			// Without this, response timing tells an attacker which emails exist.
			_, _ = s.hashPassword(ctx, password)
			s.recordFailure(ctx, email, ip)
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("look up user: %w", err)
	}

	// An account created through single sign-on has no password hash at all.
	// That is not a malformed hash to report — reporting it tells an anonymous
	// caller which addresses belong to provider-only accounts, which is the
	// enumeration TestUnknownAccountLooksLikeAWrongPassword exists to prevent.
	// It costs the same hash and the same failure as any other wrong password.
	if user.PasswordHash == "" {
		_, _ = s.hashPassword(ctx, password)
		s.recordFailure(ctx, email, ip)
		return LoginResult{}, ErrInvalidCredentials
	}
	if err := s.verifyPassword(ctx, password, user.PasswordHash); err != nil {
		s.recordFailure(ctx, email, ip)
		if errors.Is(err, ErrPasswordMismatch) {
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, err
	}
	if user.Disabled {
		s.recordFailure(ctx, email, ip)
		return LoginResult{}, ErrInvalidCredentials
	}

	usedRecoveryCode := false
	if user.TOTPEnabled {
		if totpCode == "" {
			return LoginResult{}, ErrTOTPRequired
		}
		used, err := s.spendSecondFactor(ctx, user, totpCode)
		if err != nil {
			s.recordFailure(ctx, email, ip)
			return LoginResult{}, err
		}
		usedRecoveryCode = used
	}

	// Upgrade a hash made with older parameters now that we have the password.
	//
	// Only the hash is written. This runs in the middle of a sign-in, on a row
	// read before the password was even checked, so writing the whole row would
	// put back the name, locale, theme and whether the account is disabled as
	// they were when the sign-in started — an administrator disabling somebody
	// mid-sign-in would find the sign-in had undone it.
	//
	// A failure is not the sign-in's failure: the password was right and the
	// old hash still works. It is logged rather than dropped, because a rehash
	// that never lands means the parameters never actually move.
	if NeedsRehash(user.PasswordHash) {
		if rehashed, err := s.hashPassword(ctx, password); err == nil {
			if err := s.db.UpdatePasswordHash(ctx, user.ID, rehashed); err != nil {
				slog.Default().Warn("a password hash could not be upgraded",
					"user", user.ID, "error", err)
			}
		}
	}

	result, err := s.issueSession(ctx, user, ip, userAgent)
	if err != nil {
		return LoginResult{}, err
	}

	_ = s.db.RecordLoginAttempt(ctx, email, ip, true)
	_ = s.db.ClearLoginAttempts(ctx, email)
	_ = s.db.TouchUserLogin(ctx, user.ID)
	if usedRecoveryCode {
		result.UsedRecoveryCode = true
		result.RecoveryCodesLeft, _ = s.db.CountRecoveryCodes(ctx, user.ID)
	}
	return result, nil
}

// IssueSession creates a session without checking a password. Used right after
// first-run setup, where the setup token has already proved authority.
func (s *Service) IssueSession(ctx context.Context, user store.User, ip, userAgent string) (LoginResult, error) {
	return s.issueSession(ctx, user, ip, userAgent)
}

func (s *Service) issueSession(ctx context.Context, user store.User, ip, userAgent string) (LoginResult, error) {
	token, err := crypto.RandomToken(32)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate session token: %w", err)
	}
	csrfToken, err := crypto.RandomToken(32)
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate csrf token: %w", err)
	}
	now := time.Now()
	expires := now.Add(s.sessionTTL)
	session := store.Session{
		UserID:    user.ID,
		TokenHash: HashToken(token),
		// Only the hash. The CSRF token is a credential like any other: a
		// database somebody reads must not let them forge a request with it.
		CSRFHash:  HashToken(csrfToken),
		IP:        ip,
		UserAgent: truncate(userAgent, 255),
		ExpiresAt: expires,
		// Signing in is proving who you are, so the step-up window starts now.
		ReauthAt: now,
	}
	if err := s.db.CreateSession(ctx, &session); err != nil {
		return LoginResult{}, fmt.Errorf("create session: %w", err)
	}
	return LoginResult{User: user, Token: token, CSRFToken: csrfToken, ExpiresAt: expires}, nil
}

// Authenticate resolves a session token to its user and extends the session.
func (s *Service) Authenticate(ctx context.Context, token string) (store.User, store.Session, error) {
	session, err := s.db.GetSessionByHash(ctx, HashToken(token))
	if err != nil {
		return store.User{}, store.Session{}, err
	}
	user, err := s.db.GetUser(ctx, session.UserID)
	if err != nil {
		return store.User{}, store.Session{}, err
	}
	if user.Disabled {
		// Disabling an account must take effect immediately, not at expiry.
		_ = s.db.DeleteSession(ctx, session.ID)
		return store.User{}, store.Session{}, store.ErrNotFound
	}
	// However much a session is used, it stops at the ceiling. Without this the
	// sliding expiry below renews forever and a token stolen in January is
	// still good in December.
	deadline := session.CreatedAt.Add(MaxSessionLife)
	if !session.CreatedAt.IsZero() && time.Now().After(deadline) {
		_ = s.db.DeleteSession(ctx, session.ID)
		return store.User{}, store.Session{}, store.ErrNotFound
	}
	// Sliding expiry, refreshed at most once a minute to avoid a write per request.
	if time.Since(session.LastSeenAt) > time.Minute {
		next := time.Now().Add(s.sessionTTL)
		if !session.CreatedAt.IsZero() && next.After(deadline) {
			next = deadline
		}
		_ = s.db.TouchSession(ctx, session.ID, next)
	}
	return user, session, nil
}

// CheckCSRF reports whether a token echoed by the frontend belongs to this
// session.
//
// The comparison is against the session rather than against a second cookie.
// Double-submit assumes no other page can write this panel's cookies, and on a
// panel that hosts applications on sibling subdomains that assumption does not
// hold — see HostPrefix. A session whose row predates this column has no hash
// and is refused rather than waved through.
func (s *Service) CheckCSRF(session store.Session, presented string) bool {
	if session.CSRFHash == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashToken(presented)), []byte(session.CSRFHash)) == 1
}

// Reauthenticate re-checks a password, and a second factor when the account has
// one, for an action that a borrowed session must not be able to take on its
// own. It does not issue anything: it stamps the session that is already here.
func (s *Service) Reauthenticate(ctx context.Context, user store.User, sessionID, ip, password, totpCode string) error {
	// Somebody holding a session and guessing the password is guessing against
	// the same limit as somebody at the sign-in page. Without this, stepping up
	// is an unmetered oracle for the password of an account whose session has
	// already been taken — which is exactly the case this exists for.
	if err := s.checkLockout(ctx, user.Email, ip); err != nil {
		return err
	}
	if user.PasswordHash == "" {
		// An account that only exists through the identity provider has no
		// password to re-check, so the second factor is all there is. Asking
		// for one it does not have would lock it out of its own settings.
		if !user.TOTPEnabled {
			return ErrNoSecondProof
		}
	} else if err := s.verifyPassword(ctx, password, user.PasswordHash); err != nil {
		s.recordFailure(ctx, user.Email, ip)
		return ErrInvalidCredentials
	}
	if user.TOTPEnabled {
		if err := s.checkSecondFactor(ctx, user, totpCode); err != nil {
			if !errors.Is(err, ErrTOTPRequired) {
				s.recordFailure(ctx, user.Email, ip)
			}
			return err
		}
	}
	// A step-up that succeeded says the person is who they say they are, so it
	// clears the failures the same way signing in does.
	_ = s.db.ClearLoginAttempts(ctx, user.Email)
	return s.db.MarkSessionReauthenticated(ctx, sessionID, time.Now())
}

// RecentlyAuthenticated reports whether this session has proved who it is
// inside the step-up window.
func (s *Service) RecentlyAuthenticated(session store.Session) bool {
	return !session.ReauthAt.IsZero() && time.Since(session.ReauthAt) < ReauthWindow
}

// AuthenticateToken resolves an API token to its user.
func (s *Service) AuthenticateToken(ctx context.Context, token string) (store.User, store.APIToken, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return store.User{}, store.APIToken{}, store.ErrNotFound
	}
	apiToken, err := s.db.GetAPITokenByHash(ctx, HashToken(token))
	if err != nil {
		return store.User{}, store.APIToken{}, err
	}
	user, err := s.db.GetUser(ctx, apiToken.UserID)
	if err != nil {
		return store.User{}, store.APIToken{}, err
	}
	if user.Disabled {
		return store.User{}, store.APIToken{}, store.ErrNotFound
	}
	// Recorded once a minute at most, for the same reason as sessions.
	if time.Since(apiToken.LastUsedAt) > time.Minute {
		_ = s.db.TouchAPIToken(ctx, apiToken.ID)
	}
	return user, apiToken, nil
}

// CreateAPIToken issues a token and returns the only copy of its plaintext.
func (s *Service) CreateAPIToken(ctx context.Context, userID, teamID, name, scopes string, ttl time.Duration) (store.APIToken, string, error) {
	raw, err := crypto.RandomToken(32)
	if err != nil {
		return store.APIToken{}, "", fmt.Errorf("generate api token: %w", err)
	}
	token := TokenPrefix + raw
	record := store.APIToken{
		UserID:    userID,
		TeamID:    teamID,
		Name:      name,
		TokenHash: HashToken(token),
		// The prefix is shown in the UI so a token can be identified without
		// storing anything that would let it be used.
		Prefix: token[:min(len(token), 12)],
		Scopes: scopes,
	}
	if ttl > 0 {
		record.ExpiresAt = time.Now().Add(ttl)
	}
	if err := s.db.CreateAPIToken(ctx, &record); err != nil {
		return store.APIToken{}, "", err
	}
	return record, token, nil
}

// Logout ends one session.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.db.DeleteSession(ctx, sessionID)
}

// SetupTOTP generates a secret for a user and returns it with its otpauth URI.
// The secret is not enabled until a code from it is confirmed.
func (s *Service) SetupTOTP(ctx context.Context, user *store.User, issuer string) (secret, uri string, err error) {
	secret, err = GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	sealed, err := s.keyring.Seal([]byte(secret), totpContext(user.ID))
	if err != nil {
		return "", "", fmt.Errorf("encrypt two-factor secret: %w", err)
	}
	user.TOTPSecretEnc = sealed
	user.TOTPEnabled = false
	if err := s.db.UpdateUser(ctx, user); err != nil {
		return "", "", err
	}
	return secret, TOTPURI(issuer, user.Email, secret), nil
}

// StoreRecoveryCodes keeps the hashes of the codes shown to a user.
//
// They are stored when the setup screen shows them, not when two-factor is
// confirmed: the screen is the only place they exist in readable form, and a
// user who writes them down and then closes the tab has to be able to use
// them. They do nothing until two-factor is on.
func (s *Service) StoreRecoveryCodes(ctx context.Context, userID string, codes []string) error {
	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return s.db.ReplaceRecoveryCodes(ctx, userID, hashes)
}

// RecoveryCodesLeft reports how many unused codes a user has.
func (s *Service) RecoveryCodesLeft(ctx context.Context, userID string) (int, error) {
	return s.db.CountRecoveryCodes(ctx, userID)
}

// ConfirmTOTP enables two-factor once the user proves they can generate a code.
func (s *Service) ConfirmTOTP(ctx context.Context, user *store.User, code string) error {
	if user.TOTPSecretEnc == "" {
		return errors.New("two-factor setup has not been started")
	}
	secret, err := s.keyring.Open(user.TOTPSecretEnc, totpContext(user.ID))
	if err != nil {
		return fmt.Errorf("read two-factor secret: %w", err)
	}
	counter, err := VerifyTOTP(string(secret), code, time.Now())
	if err != nil {
		return err
	}
	// Spent here too, so the code that switched two-factor on cannot be the
	// code that gets past it a moment later.
	if _, err := s.db.SpendTOTPCounter(ctx, user.ID, counter); err != nil {
		return err
	}
	user.TOTPEnabled = true
	return s.db.UpdateUser(ctx, user)
}

// DisableTOTP turns two-factor off and forgets the secret.
func (s *Service) DisableTOTP(ctx context.Context, user *store.User) error {
	user.TOTPEnabled = false
	user.TOTPSecretEnc = ""
	if err := s.db.UpdateUser(ctx, user); err != nil {
		return err
	}
	// The codes only ever existed to get past this secret. Keeping them would
	// leave a second way in that the user believes they have turned off.
	return s.db.DeleteRecoveryCodes(ctx, user.ID)
}

// ChangePassword sets a new password and signs every other session out.
func (s *Service) ChangePassword(ctx context.Context, user *store.User, current, next, keepSessionID string) error {
	if err := s.verifyPassword(ctx, current, user.PasswordHash); err != nil {
		return ErrInvalidCredentials
	}
	if err := DefaultPasswordPolicy().Check(next); err != nil {
		return err
	}
	hash, err := s.hashPassword(ctx, next)
	if err != nil {
		return err
	}
	user.PasswordHash = hash
	if err := s.db.UpdateUser(ctx, user); err != nil {
		return err
	}
	// A password change must invalidate anything an attacker already holds.
	sessions, err := s.db.ListSessions(ctx, user.ID)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.ID == keepSessionID {
			continue
		}
		_ = s.db.DeleteSession(ctx, sess.ID)
	}
	return nil
}

// ErrNoSecondProof is returned when an account has neither a password nor a
// second factor to re-check, which is the one case where a step-up cannot be
// asked for at all.
var ErrNoSecondProof = errors.New("this account has nothing to prove itself with")

// spendSecondFactor verifies one two-factor code and consumes it, or one of the
// recovery codes written down when two-factor was turned on. It reports whether
// a recovery code was the one spent.
//
// Shared by signing in and by stepping up, so the replay protection cannot be
// right in one path and missing from the other.
func (s *Service) spendSecondFactor(ctx context.Context, user store.User, code string) (usedRecovery bool, err error) {
	secret, err := s.keyring.Open(user.TOTPSecretEnc, totpContext(user.ID))
	if err != nil {
		return false, fmt.Errorf("read two-factor secret: %w", err)
	}
	counter, verifyErr := VerifyTOTP(string(secret), code, time.Now())
	if verifyErr == nil {
		// A code is good for one use. The window is three steps wide, so
		// without this a code somebody read over a shoulder or out of a screen
		// share works again for up to ninety seconds.
		spent, spendErr := s.db.SpendTOTPCounter(ctx, user.ID, counter)
		if spendErr != nil {
			return false, fmt.Errorf("record the two-factor code: %w", spendErr)
		}
		if !spent {
			return false, ErrInvalidTOTP
		}
		return false, nil
	}
	// A phone is lost often enough that the codes written down when two-factor
	// was turned on have to actually work. They are tried second, so a real
	// code is never spent by a mistyped one.
	used, recoveryErr := s.db.UseRecoveryCode(ctx, user.ID, HashRecoveryCode(code))
	if recoveryErr != nil {
		return false, fmt.Errorf("check recovery codes: %w", recoveryErr)
	}
	if !used {
		return false, verifyErr
	}
	return true, nil
}

func (s *Service) checkSecondFactor(ctx context.Context, user store.User, code string) error {
	if code == "" {
		return ErrTOTPRequired
	}
	_, err := s.spendSecondFactor(ctx, user, code)
	return err
}

func (s *Service) checkLockout(ctx context.Context, email, ip string) error {
	since := time.Now().Add(-s.lockout.Window)
	byAccount, byIP, err := s.db.CountFailedLogins(ctx, email, ip, since)
	if err != nil {
		return fmt.Errorf("check sign-in rate limit: %w", err)
	}
	if byAccount >= s.lockout.MaxPerAccount || byIP >= s.lockout.MaxPerIPAddress {
		return ErrLockedOut
	}
	return nil
}

func (s *Service) recordFailure(ctx context.Context, email, ip string) {
	_ = s.db.RecordLoginAttempt(ctx, email, ip, false)
}

// LockoutWindow is how long a locked-out caller must wait.
func (s *Service) LockoutWindow() time.Duration { return s.lockout.Window }

// CookieName is the name a cookie is actually set and read under.
//
// On HTTPS every one of the panel's cookies carries the __Host- prefix, which
// a browser will not store if the cookie names a Domain — so no page on a
// sibling subdomain can write one. Over plain http the prefix cannot be used,
// because it also requires Secure; that is development and the sslip.io
// address, and it is the reason the documentation says to put a domain on the
// panel before anybody depends on it.
//
// One name is set and exactly one is accepted. Accepting the unprefixed name
// as a fallback on HTTPS would hand back everything the prefix just bought:
// the attacker would simply write that one instead.
func (s *Service) CookieName(base string) string {
	if s.secureCookies {
		return HostPrefix + base
	}
	return base
}

// ReadCookie returns the value of one of the panel's cookies, under whichever
// name this panel is using.
func (s *Service) ReadCookie(r *http.Request, base string) string {
	cookie, err := r.Cookie(s.CookieName(base))
	if err != nil || cookie.Value == "" {
		return ""
	}
	return cookie.Value
}

// SessionCookie builds the cookie for a session token.
func (s *Service) SessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:  s.CookieName(SessionCookieName),
		Value: token,
		// Path=/ and no Domain, which __Host- requires and which are what the
		// panel wants anyway.
		Path: "/",
		// HttpOnly stops any script from reading the session, which is the
		// difference between an XSS bug and an account takeover.
		HttpOnly: true,
		Secure:   s.secureCookies,
		// Lax rather than Strict so that following a link from an email into
		// the panel does not look signed out.
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	}
}

// CSRFCookie builds the double-submit cookie. It is deliberately readable by
// the frontend so it can echo the value in a header.
func (s *Service) CSRFCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     s.CookieName(CSRFCookieName),
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	}
}

// ClearCookie expires a cookie by name.
func (s *Service) ClearCookie(base string) *http.Cookie {
	name := s.CookieName(base)
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: base == SessionCookieName,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// totpContext binds a two-factor secret to the user it belongs to, so a stolen
// ciphertext cannot be moved onto another account.
func totpContext(userID string) string { return "totp:" + userID }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
