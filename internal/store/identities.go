package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Identity binds an account here to one person at an identity provider.
//
// Issuer and Subject are the provider's own, permanent name for that person.
// Email is only what the provider said when the link was made, kept so the
// account page can say which provider account is linked.
type Identity struct {
	Issuer    string `json:"issuer"`
	Subject   string `json:"-"`
	UserID    string `json:"-"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
}

// ErrIdentityTaken is returned when a provider identity is already linked to a
// different account.
var ErrIdentityTaken = errors.New("that identity is already linked to another account")

// UserByIdentity finds the account a provider identity is linked to.
func (db *DB) UserByIdentity(ctx context.Context, issuer, subject string) (User, error) {
	return scanUser(db.QueryRowContext(ctx, `SELECT `+prefixColumns("u", userColumns)+`
		FROM users u JOIN user_identities i ON i.user_id = u.id
		WHERE i.issuer = ? AND i.subject = ?`, issuer, subject))
}

// LinkIdentity binds a provider identity to an account. Linking one that is
// already this account's is not an error; linking one that is another
// account's is ErrIdentityTaken.
func (db *DB) LinkIdentity(ctx context.Context, userID, issuer, subject, email string) error {
	var owner string
	err := db.QueryRowContext(ctx,
		`SELECT user_id FROM user_identities WHERE issuer = ? AND subject = ?`, issuer, subject).Scan(&owner)
	switch {
	case err == nil && owner == userID:
		return nil
	case err == nil:
		return ErrIdentityTaken
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("look up the identity: %w", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO user_identities (issuer, subject, user_id, email, created_at)
		VALUES (?,?,?,?,?)`, issuer, subject, userID, email, Now()); err != nil {
		return fmt.Errorf("link the identity: %w", err)
	}
	return nil
}

// IdentitiesForUser lists the provider identities linked to an account.
func (db *DB) IdentitiesForUser(ctx context.Context, userID string) ([]Identity, error) {
	rows, err := db.QueryContext(ctx, `SELECT issuer, subject, user_id, email, created_at
		FROM user_identities WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	defer rows.Close()
	out := []Identity{}
	for rows.Next() {
		var identity Identity
		if err := rows.Scan(&identity.Issuer, &identity.Subject, &identity.UserID,
			&identity.Email, &identity.CreatedAt); err != nil {
			return nil, fmt.Errorf("list identities: %w", err)
		}
		out = append(out, identity)
	}
	return out, rows.Err()
}

// UnlinkIdentities removes every provider identity linked to an account.
func (db *DB) UnlinkIdentities(ctx context.Context, userID string) error {
	if _, err := db.Exec(ctx, `DELETE FROM user_identities WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("unlink identities: %w", err)
	}
	return nil
}
