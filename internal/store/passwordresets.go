package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreatePasswordReset records a reset link for a user, and forgets any the
// user had before: only the newest link works.
func (db *DB) CreatePasswordReset(ctx context.Context, userID, tokenHash string, expires time.Time) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM password_resets WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("forget earlier reset links: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO password_resets (token_hash, user_id, created_at, expires_at)
			VALUES (?,?,?,?)`, tokenHash, userID, Now(), FormatTime(expires)); err != nil {
			return fmt.Errorf("record a reset link: %w", err)
		}
		return nil
	})
}

// UsePasswordReset spends a reset link and returns whose it was. A link that
// was used, has expired or never existed is ErrNotFound, and a link is spent
// by exactly one caller however many arrive at once.
func (db *DB) UsePasswordReset(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	var userID string
	err := db.QueryRowContext(ctx, `UPDATE password_resets SET used_at = ?
		WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?
		RETURNING user_id`, FormatTime(now), tokenHash, FormatTime(now)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("use a reset link: %w", err)
	}
	return userID, nil
}
