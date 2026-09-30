package store

import (
	"context"
	"fmt"
	"time"
)

// RegistryCredential is how a team pulls from a private registry. The
// password is never part of it once stored.
type RegistryCredential struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"team_id"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RegistryCredentialRow carries the sealed password alongside.
type RegistryCredentialRow struct {
	RegistryCredential
	SealedPassword string
}

// RegistryContext is what a registry password is sealed under: the team and
// the host, so a row copied to another team or host does not open.
func RegistryContext(teamID, host string) string { return "registry:" + teamID + ":" + host }

// SetRegistryCredential creates a team's credential for a host, or replaces
// the one it has. sealed is the password sealed under RegistryContext.
func (db *DB) SetRegistryCredential(ctx context.Context, c *RegistryCredential, sealed string) error {
	if c.ID == "" {
		c.ID = NewID("reg")
	}
	now := Now()
	if _, err := db.Exec(ctx, `INSERT INTO registry_credentials
		(id, team_id, name, host, username, password_enc, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (team_id, host) DO UPDATE SET name = excluded.name, username = excluded.username,
			password_enc = excluded.password_enc, updated_at = excluded.updated_at`,
		c.ID, c.TeamID, c.Name, c.Host, c.Username, sealed, now, now); err != nil {
		return fmt.Errorf("save the credentials for %s: %w", c.Host, err)
	}
	c.UpdatedAt, _ = ParseTime(now)
	return nil
}

// ListRegistryCredentials returns a team's credentials, with the sealed
// passwords, by host.
func (db *DB) ListRegistryCredentials(ctx context.Context, teamID string) ([]RegistryCredentialRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, team_id, name, host, username, password_enc, created_at, updated_at
		FROM registry_credentials WHERE team_id = ? ORDER BY host`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list the registry credentials of %s: %w", teamID, err)
	}
	defer rows.Close()
	out := []RegistryCredentialRow{}
	for rows.Next() {
		var r RegistryCredentialRow
		var created, updated string
		if err := rows.Scan(&r.ID, &r.TeamID, &r.Name, &r.Host, &r.Username, &r.SealedPassword,
			&created, &updated); err != nil {
			return nil, fmt.Errorf("read a registry credential: %w", err)
		}
		r.CreatedAt, _ = ParseTime(created)
		r.UpdatedAt, _ = ParseTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRegistryCredential removes one of a team's credentials.
func (db *DB) DeleteRegistryCredential(ctx context.Context, teamID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM registry_credentials WHERE team_id = ? AND id = ?`, teamID, id)
	if err != nil {
		return fmt.Errorf("delete a registry credential: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
