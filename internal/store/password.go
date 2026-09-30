package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AppPassword is the username and password hash in front of an app.
//
// Hash is a bcrypt hash, the form Traefik's basicAuth middleware reads. The
// password itself is never stored and never leaves the request that set it.
type AppPassword struct {
	AppID     string `json:"app_id"`
	Username  string `json:"username"`
	Hash      string `json:"-"`
	UpdatedAt string `json:"updated_at,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// GetAppPassword returns an app's password, and false when it has none.
func (db *DB) GetAppPassword(ctx context.Context, appID string) (AppPassword, bool, error) {
	password := AppPassword{AppID: appID}
	err := db.QueryRowContext(ctx,
		`SELECT username, password_hash, updated_at, updated_by FROM app_passwords WHERE app_id = ?`, appID).
		Scan(&password.Username, &password.Hash, &password.UpdatedAt, &password.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return AppPassword{AppID: appID}, false, nil
	}
	if err != nil {
		return password, false, fmt.Errorf("read the password for %s: %w", appID, err)
	}
	return password, true, nil
}

// SetAppPassword stores an app's username and password hash, replacing any.
func (db *DB) SetAppPassword(ctx context.Context, p AppPassword, actorID string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO app_passwords (app_id, username, password_hash, updated_at, updated_by)
		VALUES (?,?,?,?,?)
		ON CONFLICT(app_id) DO UPDATE SET
			username = excluded.username,
			password_hash = excluded.password_hash,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		p.AppID, p.Username, p.Hash, Now(), actorID)
	if err != nil {
		return fmt.Errorf("save the password for %s: %w", p.AppID, err)
	}
	return nil
}

// DeleteAppPassword removes an app's password. Removing one that is not there
// is not an error.
func (db *DB) DeleteAppPassword(ctx context.Context, appID string) error {
	if _, err := db.Exec(ctx, `DELETE FROM app_passwords WHERE app_id = ?`, appID); err != nil {
		return fmt.Errorf("remove the password for %s: %w", appID, err)
	}
	return nil
}
