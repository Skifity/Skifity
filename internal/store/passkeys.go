package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Passkey is a WebAuthn credential that signs an account in.
//
// The private key is on the person's device and never here. PublicKeyEnc is
// the public key, sealed to the account and the credential id so a row copied
// onto another account does not open; see internal/auth/passkey.go.
type Passkey struct {
	ID     string `json:"id"`
	UserID string `json:"-"`
	// CredentialID is the authenticator's own id for the credential,
	// base64url without padding.
	CredentialID string `json:"-"`
	PublicKeyEnc string `json:"-"`
	// RPID is the hostname the passkey was made for. A browser offers it to
	// that hostname and no other.
	RPID              string   `json:"rp_id"`
	SignCount         uint32   `json:"-"`
	AAGUID            string   `json:"-"`
	Transports        []string `json:"-"`
	AttestationFormat string   `json:"-"`
	UserVerified      bool     `json:"-"`
	// BackupEligible is a passkey the platform or password manager may copy
	// to the person's other devices — a synced passkey. BackupState is
	// whether it has been.
	BackupEligible bool      `json:"backup_eligible"`
	BackupState    bool      `json:"backup_state"`
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"created_at"`
	LastUsedAt     time.Time `json:"last_used_at,omitzero"`
}

const passkeyColumns = `id, user_id, credential_id, public_key_enc, rp_id, sign_count, aaguid,
	transports, attestation_format, user_verified, backup_eligible, backup_state, name,
	created_at, last_used_at`

func scanPasskey(row interface{ Scan(...any) error }) (Passkey, error) {
	var p Passkey
	var transports, created string
	var lastUsed sql.NullString
	var count int64
	err := row.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.PublicKeyEnc, &p.RPID, &count, &p.AAGUID,
		&transports, &p.AttestationFormat, &p.UserVerified, &p.BackupEligible, &p.BackupState, &p.Name,
		&created, &lastUsed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, fmt.Errorf("scan passkey: %w", err)
	}
	p.SignCount = uint32(min(max(count, 0), int64(^uint32(0))))
	p.Transports = splitList(transports)
	p.CreatedAt, _ = ParseTime(created)
	p.LastUsedAt = scanTime(lastUsed)
	return p, nil
}

func splitList(value string) []string {
	out := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// CreatePasskey stores a passkey. The caller chooses the id, because the
// public key is sealed to it before the row exists. A credential id that is
// already stored is ErrConflict.
func (db *DB) CreatePasskey(ctx context.Context, p *Passkey) error {
	if p.ID == "" {
		return errors.New("create passkey: the id is chosen by the caller")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO passkeys
		(id, user_id, credential_id, public_key_enc, rp_id, sign_count, aaguid, transports,
		 attestation_format, user_verified, backup_eligible, backup_state, name, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.UserID, p.CredentialID, p.PublicKeyEnc, p.RPID, int64(p.SignCount), p.AAGUID,
		strings.Join(p.Transports, ","), p.AttestationFormat, p.UserVerified, p.BackupEligible,
		p.BackupState, p.Name, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: that passkey is already stored", ErrConflict)
		}
		return fmt.Errorf("create passkey: %w", err)
	}
	p.CreatedAt, _ = ParseTime(now)
	return nil
}

// ListPasskeys returns an account's passkeys, oldest first.
func (db *DB) ListPasskeys(ctx context.Context, userID string) ([]Passkey, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+passkeyColumns+`
		FROM passkeys WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	defer rows.Close()
	out := []Passkey{}
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPasskey finds one of an account's passkeys. Somebody else's is
// ErrNotFound, exactly like one that does not exist.
func (db *DB) GetPasskey(ctx context.Context, id, userID string) (Passkey, error) {
	return scanPasskey(db.QueryRowContext(ctx, `SELECT `+passkeyColumns+`
		FROM passkeys WHERE id = ? AND user_id = ?`, id, userID))
}

// PasskeyByCredentialID finds the passkey a signed answer names.
func (db *DB) PasskeyByCredentialID(ctx context.Context, credentialID string) (Passkey, error) {
	return scanPasskey(db.QueryRowContext(ctx, `SELECT `+passkeyColumns+`
		FROM passkeys WHERE credential_id = ?`, credentialID))
}

// CountPasskeys says how many passkeys an account has.
func (db *DB) CountPasskeys(ctx context.Context, userID string) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM passkeys WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count passkeys: %w", err)
	}
	return n, nil
}

// RenamePasskey changes the name a person gave one of their passkeys.
func (db *DB) RenamePasskey(ctx context.Context, id, userID, name string) error {
	res, err := db.Exec(ctx, `UPDATE passkeys SET name = ? WHERE id = ? AND user_id = ?`, name, id, userID)
	if err != nil {
		return fmt.Errorf("rename passkey: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrLastPasskey is removing the passkey an account cannot do without.
var ErrLastPasskey = errors.New("that is the account's last passkey")

// DeletePasskey removes one of an account's passkeys.
//
// keepOne refuses to remove the account's last one, with ErrLastPasskey. The
// count is in the statement rather than read first, so two removals arriving
// together cannot each see the other's passkey and leave none.
func (db *DB) DeletePasskey(ctx context.Context, id, userID string, keepOne bool) error {
	query := `DELETE FROM passkeys WHERE id = ? AND user_id = ?`
	args := []any{id, userID}
	if keepOne {
		query += ` AND (SELECT COUNT(*) FROM passkeys WHERE user_id = ?) > 1`
		args = append(args, userID)
	}
	res, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete passkey: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := db.GetPasskey(ctx, id, userID); err != nil {
		return err
	}
	return ErrLastPasskey
}

// DeleteUserPasskeys removes every passkey an account has, and says how many
// there were. Only `skifity admin reset-password --remove-passkeys` does this.
func (db *DB) DeleteUserPasskeys(ctx context.Context, userID string) (int, error) {
	res, err := db.Exec(ctx, `DELETE FROM passkeys WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("delete passkeys: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// RecordPasskeyUse stores what a sign-in with a passkey reported, and reports
// whether its signature counter moved forward.
//
// The comparison is in the statement: the counter is written only when the new
// value is higher than the stored one, or when both are zero — an authenticator
// that does not count, which synced passkeys generally do not. Two sign-ins
// racing with the same copied key cannot both win: the second one finds the
// counter already where it was going, and is refused.
func (db *DB) RecordPasskeyUse(ctx context.Context, id string, signCount uint32, backupState bool, at time.Time) (bool, error) {
	count := int64(signCount)
	res, err := db.Exec(ctx, `UPDATE passkeys SET sign_count = ?, backup_state = ?, last_used_at = ?
		WHERE id = ? AND (sign_count < ? OR (sign_count = 0 AND ? = 0))`,
		count, backupState, FormatTime(at), id, count, count)
	if err != nil {
		return false, fmt.Errorf("record a passkey sign-in: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record a passkey sign-in: %w", err)
	}
	return n == 1, nil
}

// PasskeyChallenge is one half-finished passkey ceremony.
type PasskeyChallenge struct {
	// ChallengeHash is the hash of the challenge the browser was sent, which
	// is what comes back signed and what the row is found by.
	ChallengeHash string
	// Kind is PasskeyRegister or PasskeySignIn.
	Kind   string
	UserID string
	// Binding is what the ceremony belongs to: the session adding a passkey,
	// or the hash of the cookie the signing-in browser holds.
	Binding string
	IP      string
	// Data is the library's own record of the ceremony, as JSON.
	Data      string
	ExpiresAt time.Time
}

// The two kinds of ceremony.
const (
	PasskeyRegister = "register"
	PasskeySignIn   = "login"
)

// SavePasskeyChallenge records a ceremony's challenge until it is finished.
func (db *DB) SavePasskeyChallenge(ctx context.Context, c PasskeyChallenge) error {
	_, err := db.Exec(ctx, `INSERT INTO passkey_challenges
		(challenge_hash, kind, user_id, binding, ip, data, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		c.ChallengeHash, c.Kind, c.UserID, c.Binding, attemptAddress(c.IP), c.Data, Now(), FormatTime(c.ExpiresAt))
	if err != nil {
		return fmt.Errorf("record a passkey challenge: %w", err)
	}
	return nil
}

// TakePasskeyChallenge removes a challenge and returns it, so it can be used
// exactly once however many answers arrive for it. One that has expired is
// removed all the same and is ErrNotFound, like one that never existed.
func (db *DB) TakePasskeyChallenge(ctx context.Context, challengeHash, kind string, now time.Time) (PasskeyChallenge, error) {
	var c PasskeyChallenge
	var expires string
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `DELETE FROM passkey_challenges
			WHERE challenge_hash = ? AND kind = ?
			RETURNING challenge_hash, kind, user_id, binding, ip, data, expires_at`,
			challengeHash, kind).
			Scan(&c.ChallengeHash, &c.Kind, &c.UserID, &c.Binding, &c.IP, &c.Data, &expires)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return PasskeyChallenge{}, ErrNotFound
	}
	if err != nil {
		return PasskeyChallenge{}, fmt.Errorf("take a passkey challenge: %w", err)
	}
	c.ExpiresAt, _ = ParseTime(expires)
	if !now.Before(c.ExpiresAt) {
		return PasskeyChallenge{}, ErrNotFound
	}
	return c, nil
}

// CountOpenPasskeySignIns says how many sign-in challenges an address holds
// that have not expired, so one caller cannot fill the table with them.
func (db *DB) CountOpenPasskeySignIns(ctx context.Context, ip string, now time.Time) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM passkey_challenges
		WHERE kind = ? AND ip = ? AND expires_at > ?`,
		PasskeySignIn, attemptAddress(ip), FormatTime(now)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count passkey challenges: %w", err)
	}
	return n, nil
}

// PurgeExpiredPasskeyChallenges forgets ceremonies nobody finished.
func (db *DB) PurgeExpiredPasskeyChallenges(ctx context.Context, now time.Time) error {
	if _, err := db.Exec(ctx, `DELETE FROM passkey_challenges WHERE expires_at <= ?`, FormatTime(now)); err != nil {
		return fmt.Errorf("purge passkey challenges: %w", err)
	}
	return nil
}
