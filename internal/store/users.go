package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const userColumns = `id, email, name, password_hash, totp_secret_enc, totp_enabled, is_admin,
	disabled, locale, theme, recovery_saved, created_at, updated_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created, updated string
	var lastLogin sql.NullString
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.TOTPSecretEnc, &u.TOTPEnabled,
		&u.IsAdmin, &u.Disabled, &u.Locale, &u.Theme, &u.RecoverySaved, &created, &updated, &lastLogin)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, ErrNotFound
		}
		return u, fmt.Errorf("scan user: %w", err)
	}
	u.CreatedAt, _ = ParseTime(created)
	u.UpdatedAt, _ = ParseTime(updated)
	u.LastLoginAt = scanTime(lastLogin)
	return u, nil
}

// CreateUser inserts a user. Email is stored as given but compared case-insensitively.
func (db *DB) CreateUser(ctx context.Context, u *User) error {
	if u.ID == "" {
		u.ID = NewID("usr")
	}
	now := Now()
	u.Email = strings.TrimSpace(u.Email)
	_, err := db.Exec(ctx, `INSERT INTO users
		(id, email, name, password_hash, totp_secret_enc, totp_enabled, is_admin, disabled, locale, theme, recovery_saved, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Email, u.Name, u.PasswordHash, u.TOTPSecretEnc, u.TOTPEnabled, u.IsAdmin,
		u.Disabled, u.Locale, defaultStr(u.Theme, "system"), u.RecoverySaved, now, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: a user with the email %s already exists", ErrConflict, u.Email)
		}
		return fmt.Errorf("create user: %w", err)
	}
	u.CreatedAt, _ = ParseTime(now)
	u.UpdatedAt = u.CreatedAt
	return nil
}

// GetUser looks a user up by id.
func (db *DB) GetUser(ctx context.Context, id string) (User, error) {
	return scanUser(db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// GetUserByEmail looks a user up by email, case-insensitively.
func (db *DB) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? COLLATE NOCASE`, strings.TrimSpace(email)))
}

// ListUsers returns every user, oldest first.
func (db *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers reports how many users exist, which is how the panel knows whether
// first-run setup has already happened.
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// UpdateUser writes the mutable fields of a user.
func (db *DB) UpdateUser(ctx context.Context, u *User) error {
	now := Now()
	res, err := db.Exec(ctx, `UPDATE users SET
		email = ?, name = ?, password_hash = ?, totp_secret_enc = ?, totp_enabled = ?,
		is_admin = ?, disabled = ?, locale = ?, theme = ?, recovery_saved = ?, updated_at = ?
		WHERE id = ?`,
		u.Email, u.Name, u.PasswordHash, u.TOTPSecretEnc, u.TOTPEnabled, u.IsAdmin,
		u.Disabled, u.Locale, u.Theme, u.RecoverySaved, now, u.ID)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	u.UpdatedAt, _ = ParseTime(now)
	return nil
}

// UpdatePasswordHash writes only the password hash.
//
// Separate from UpdateUser, which writes eleven columns from whatever the
// caller last read. Upgrading a hash happens in the middle of an ordinary
// sign-in, minutes after the row was read, so a full-row write there would put
// back the name, locale, theme — and whether the account is disabled — as they
// were when the sign-in began. An administrator disabling somebody while they
// are signing in should not have it undone by the sign-in.
func (db *DB) UpdatePasswordHash(ctx context.Context, id, hash string) error {
	res, err := db.Exec(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, Now(), id)
	if err != nil {
		return fmt.Errorf("update password hash: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchUserLogin records a successful sign-in.
func (db *DB) TouchUserLogin(ctx context.Context, id string) error {
	_, err := db.Exec(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, Now(), id)
	if err != nil {
		return fmt.Errorf("record login: %w", err)
	}
	return nil
}

// DeleteUser removes a user and, by cascade, their sessions and tokens.
func (db *DB) DeleteUser(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- sessions ---

// CreateSession stores a login. Only the hash of the token is kept, so a stolen
// database cannot be used to impersonate a signed-in user.
func (db *DB) CreateSession(ctx context.Context, s *Session) error {
	if s.ID == "" {
		s.ID = NewID("ses")
	}
	now := Now()
	reauth := ""
	if !s.ReauthAt.IsZero() {
		reauth = FormatTime(s.ReauthAt)
	}
	_, err := db.Exec(ctx, `INSERT INTO sessions
		(id, user_id, token_hash, csrf_hash, ip, user_agent, created_at, last_seen_at, expires_at, reauth_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.UserID, s.TokenHash, s.CSRFHash, s.IP, s.UserAgent, now, now, FormatTime(s.ExpiresAt), reauth)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	s.CreatedAt, _ = ParseTime(now)
	s.LastSeenAt = s.CreatedAt
	return nil
}

// GetSessionByHash finds a live session. Expired sessions are reported as missing.
func (db *DB) GetSessionByHash(ctx context.Context, hash string) (Session, error) {
	var s Session
	var created, lastSeen, expires, reauth string
	err := db.QueryRowContext(ctx, `SELECT id, user_id, token_hash, csrf_hash, ip, user_agent, created_at, last_seen_at, expires_at, reauth_at
		FROM sessions WHERE token_hash = ?`, hash).
		Scan(&s.ID, &s.UserID, &s.TokenHash, &s.CSRFHash, &s.IP, &s.UserAgent, &created, &lastSeen, &expires, &reauth)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s, ErrNotFound
		}
		return s, fmt.Errorf("get session: %w", err)
	}
	s.CreatedAt, _ = ParseTime(created)
	s.LastSeenAt, _ = ParseTime(lastSeen)
	s.ExpiresAt, _ = ParseTime(expires)
	if reauth != "" {
		s.ReauthAt, _ = ParseTime(reauth)
	}
	if time.Now().After(s.ExpiresAt) {
		return Session{}, ErrNotFound
	}
	return s, nil
}

// MarkSessionReauthenticated records that the person behind this session has
// just proved who they are again, with a password or a second factor.
func (db *DB) MarkSessionReauthenticated(ctx context.Context, id string, at time.Time) error {
	_, err := db.Exec(ctx, `UPDATE sessions SET reauth_at = ? WHERE id = ?`, FormatTime(at), id)
	if err != nil {
		return fmt.Errorf("record re-authentication: %w", err)
	}
	return nil
}

// TouchSession extends a session's life on activity.
func (db *DB) TouchSession(ctx context.Context, id string, expires time.Time) error {
	_, err := db.Exec(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
		Now(), FormatTime(expires), id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// ListSessions returns a user's live sessions, newest first, so they can see and
// revoke other devices.
func (db *DB) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, user_id, token_hash, csrf_hash, ip, user_agent, created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC`, userID, Now())
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var created, lastSeen, expires string
		if err := rows.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.CSRFHash, &s.IP, &s.UserAgent, &created, &lastSeen, &expires); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.CreatedAt, _ = ParseTime(created)
		s.LastSeenAt, _ = ParseTime(lastSeen)
		s.ExpiresAt, _ = ParseTime(expires)
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSession signs one session out.
func (db *DB) DeleteSession(ctx context.Context, id string) error {
	_, err := db.Exec(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions signs a user out everywhere. Called on password change.
func (db *DB) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := db.Exec(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// PurgeExpiredSessions clears out sessions nobody can use any more.
func (db *DB) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	res, err := db.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, Now())
	if err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// --- API tokens ---

// CreateAPIToken stores a token for the CLI or MCP.
func (db *DB) CreateAPIToken(ctx context.Context, t *APIToken) error {
	if t.ID == "" {
		t.ID = NewID("tok")
	}
	now := Now()
	var expires any
	if !t.ExpiresAt.IsZero() {
		expires = FormatTime(t.ExpiresAt)
	}
	_, err := db.Exec(ctx, `INSERT INTO api_tokens
		(id, user_id, team_id, name, token_hash, prefix, scopes, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		t.ID, t.UserID, t.TeamID, t.Name, t.TokenHash, t.Prefix, t.Scopes, now, expires)
	if err != nil {
		return fmt.Errorf("create api token: %w", err)
	}
	t.CreatedAt, _ = ParseTime(now)
	return nil
}

// GetAPITokenByHash finds a live token.
func (db *DB) GetAPITokenByHash(ctx context.Context, hash string) (APIToken, error) {
	var t APIToken
	var created string
	var lastUsed, expires sql.NullString
	err := db.QueryRowContext(ctx, `SELECT id, user_id, team_id, name, token_hash, prefix, scopes, created_at, last_used_at, expires_at
		FROM api_tokens WHERE token_hash = ?`, hash).
		Scan(&t.ID, &t.UserID, &t.TeamID, &t.Name, &t.TokenHash, &t.Prefix, &t.Scopes, &created, &lastUsed, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, ErrNotFound
		}
		return t, fmt.Errorf("get api token: %w", err)
	}
	t.CreatedAt, _ = ParseTime(created)
	t.LastUsedAt = scanTime(lastUsed)
	t.ExpiresAt = scanTime(expires)
	if !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt) {
		return APIToken{}, ErrNotFound
	}
	return t, nil
}

// ListAPITokens returns a user's tokens, newest first.
func (db *DB) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, user_id, team_id, name, token_hash, prefix, scopes, created_at, last_used_at, expires_at
		FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var created string
		var lastUsed, expires sql.NullString
		if err := rows.Scan(&t.ID, &t.UserID, &t.TeamID, &t.Name, &t.TokenHash, &t.Prefix, &t.Scopes, &created, &lastUsed, &expires); err != nil {
			return nil, fmt.Errorf("scan api token: %w", err)
		}
		t.CreatedAt, _ = ParseTime(created)
		t.LastUsedAt = scanTime(lastUsed)
		t.ExpiresAt = scanTime(expires)
		out = append(out, t)
	}
	return out, rows.Err()
}

// TouchAPIToken records when a token was last used, so stale ones are visible.
func (db *DB) TouchAPIToken(ctx context.Context, id string) error {
	_, err := db.Exec(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, Now(), id)
	if err != nil {
		return fmt.Errorf("touch api token: %w", err)
	}
	return nil
}

// DeleteAPIToken revokes a token.
func (db *DB) DeleteAPIToken(ctx context.Context, id, userID string) error {
	res, err := db.Exec(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete api token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- login attempts (rate limiting) ---

// RecordLoginAttempt appends an attempt for rate limiting and for the audit trail.
func (db *DB) RecordLoginAttempt(ctx context.Context, identifier, ip string, success bool) error {
	_, err := db.Exec(ctx, `INSERT INTO login_attempts (identifier, ip, success, at) VALUES (?,?,?,?)`,
		strings.ToLower(identifier), ip, success, Now())
	if err != nil {
		return fmt.Errorf("record login attempt: %w", err)
	}
	return nil
}

// CountFailedLogins counts failures for an identifier or an IP since a time.
// Counting both stops one attacker from spraying many accounts from one address
// and one account from being locked by an attacker from many addresses.
func (db *DB) CountFailedLogins(ctx context.Context, identifier, ip string, since time.Time) (byIdentifier, byIP int, err error) {
	s := FormatTime(since)
	if identifier != "" {
		if err = db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM login_attempts WHERE identifier = ? AND success = 0 AND at > ?`,
			strings.ToLower(identifier), s).Scan(&byIdentifier); err != nil {
			return 0, 0, fmt.Errorf("count failed logins by identifier: %w", err)
		}
	}
	if ip != "" {
		if err = db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM login_attempts WHERE ip = ? AND success = 0 AND at > ?`,
			ip, s).Scan(&byIP); err != nil {
			return 0, 0, fmt.Errorf("count failed logins by ip: %w", err)
		}
	}
	return byIdentifier, byIP, nil
}

// ClearLoginAttempts wipes the failure history after a successful sign-in.
func (db *DB) ClearLoginAttempts(ctx context.Context, identifier string) error {
	_, err := db.Exec(ctx, `DELETE FROM login_attempts WHERE identifier = ? AND success = 0`,
		strings.ToLower(identifier))
	if err != nil {
		return fmt.Errorf("clear login attempts: %w", err)
	}
	return nil
}

// PurgeOldLoginAttempts keeps the table from growing without bound.
func (db *DB) PurgeOldLoginAttempts(ctx context.Context, before time.Time) error {
	_, err := db.Exec(ctx, `DELETE FROM login_attempts WHERE at < ?`, FormatTime(before))
	if err != nil {
		return fmt.Errorf("purge login attempts: %w", err)
	}
	return nil
}

func defaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// --- two-factor recovery codes ---

// ReplaceRecoveryCodes stores a fresh set of hashed codes, forgetting any the
// user had before.
//
// Replacing rather than adding is the safe direction: the codes on screen are
// the only ones the user has written down, so anything older is a set nobody
// can produce and everybody would still accept.
func (db *DB) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes []string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("clear recovery codes: %w", err)
		}
		now := Now()
		for _, hash := range hashes {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO recovery_codes (id, user_id, code_hash, created_at) VALUES (?,?,?,?)`,
				NewID("rec"), userID, hash, now); err != nil {
				return fmt.Errorf("store a recovery code: %w", err)
			}
		}
		return nil
	})
}

// UseRecoveryCode consumes one unused code and reports whether it was there.
//
// The update is the check: doing it in one statement means two logins racing
// with the same code cannot both win.
func (db *DB) UseRecoveryCode(ctx context.Context, userID, hash string) (bool, error) {
	res, err := db.Exec(ctx,
		`UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`,
		Now(), userID, hash)
	if err != nil {
		return false, fmt.Errorf("use a recovery code: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CountRecoveryCodes returns how many codes are left unused, which is what the
// account page shows.
func (db *DB) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	return n, nil
}

// DeleteRecoveryCodes forgets a user's codes, which happens when two-factor is
// turned off.
func (db *DB) DeleteRecoveryCodes(ctx context.Context, userID string) error {
	if _, err := db.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	return nil
}

// SpendTOTPCounter records that a one-time password step has been used, and
// reports whether it was still unused.
//
// The check and the write are one statement on purpose. Two sign-ins arriving
// with the same code in the same instant would both pass a read-then-write, and
// the whole point of the counter is that the second one loses.
func (db *DB) SpendTOTPCounter(ctx context.Context, userID string, counter uint64) (bool, error) {
	res, err := db.Exec(ctx,
		`UPDATE users SET totp_last_counter = ? WHERE id = ? AND totp_last_counter < ?`,
		counter, userID, counter)
	if err != nil {
		return false, fmt.Errorf("record the two-factor code as used: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record the two-factor code as used: %w", err)
	}
	return n == 1, nil
}
