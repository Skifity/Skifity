package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Invitations: how a second person gets onto this panel.
//
// See migrations/0005_invitations.sql for why this exists at all. The short
// version is that adding somebody to a team refused any address that had never
// signed in, and nothing could create an account, so the advice in that error
// message had nowhere to go.

// Invitation is a pending offer to join a team.
type Invitation struct {
	ID         string    `json:"id"`
	TeamID     string    `json:"team_id"`
	Email      string    `json:"email"`
	Role       Role      `json:"role"`
	InvitedBy  string    `json:"invited_by,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
	AcceptedAt time.Time `json:"accepted_at,omitzero"`
	CreatedAt  time.Time `json:"created_at"`
}

// Expired reports whether an invitation can still be used.
func (i Invitation) Expired() bool { return time.Now().After(i.ExpiresAt) }

const invitationColumns = `id, team_id, email, role, invited_by, expires_at, accepted_at, created_at`

func scanInvitation(row interface{ Scan(...any) error }) (Invitation, error) {
	var i Invitation
	var invitedBy sql.NullString
	var expires, created string
	var accepted sql.NullString
	if err := row.Scan(&i.ID, &i.TeamID, &i.Email, &i.Role, &invitedBy, &expires, &accepted, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return i, ErrNotFound
		}
		return i, fmt.Errorf("scan invitation: %w", err)
	}
	i.InvitedBy = invitedBy.String
	i.ExpiresAt, _ = ParseTime(expires)
	i.AcceptedAt = scanTime(accepted)
	i.CreatedAt, _ = ParseTime(created)
	return i, nil
}

// CreateInvitation records one, replacing any pending invitation for the same
// address in the same team: inviting somebody twice should leave one link that
// works, not two.
func (db *DB) CreateInvitation(ctx context.Context, i *Invitation, tokenHash string) error {
	if i.ID == "" {
		i.ID = NewID("inv")
	}
	i.CreatedAt = time.Now()
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM team_invitations WHERE team_id = ? AND email = ? AND accepted_at IS NULL`,
			i.TeamID, i.Email); err != nil {
			return fmt.Errorf("replace a pending invitation: %w", err)
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO team_invitations (id, team_id, email, role, token_hash, invited_by, expires_at, created_at)
			 VALUES (?,?,?,?,?,?,?,?)`,
			i.ID, i.TeamID, i.Email, string(i.Role), tokenHash,
			nullIfEmpty(i.InvitedBy), FormatTime(i.ExpiresAt), FormatTime(i.CreatedAt))
		if err != nil {
			return fmt.Errorf("create invitation: %w", err)
		}
		return nil
	})
}

// InvitationByTokenHash finds one by the hash of the token in the link.
func (db *DB) InvitationByTokenHash(ctx context.Context, hash string) (Invitation, error) {
	return scanInvitation(db.QueryRowContext(ctx,
		`SELECT `+invitationColumns+` FROM team_invitations WHERE token_hash = ?`, hash))
}

// ListInvitations returns a team's pending invitations, newest first.
func (db *DB) ListInvitations(ctx context.Context, teamID string) ([]Invitation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+invitationColumns+` FROM team_invitations
		 WHERE team_id = ? AND accepted_at IS NULL ORDER BY created_at DESC`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()
	out := []Invitation{}
	for rows.Next() {
		i, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// AcceptInvitation marks one used and adds the membership, in one transaction.
//
// Both or neither: an invitation marked accepted with no membership behind it
// is a person who cannot get in and cannot be invited again.
func (db *DB) AcceptInvitation(ctx context.Context, invitationID, teamID, userID string, role Role) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		// The WHERE clause is the guard: an invitation that another request
		// accepted a moment ago updates no rows, and this refuses rather than
		// adding a second membership.
		result, err := tx.ExecContext(ctx,
			`UPDATE team_invitations SET accepted_at = ? WHERE id = ? AND accepted_at IS NULL`,
			Now(), invitationID)
		if err != nil {
			return fmt.Errorf("accept invitation: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memberships (team_id, user_id, role, created_at) VALUES (?,?,?,?)
			 ON CONFLICT (team_id, user_id) DO UPDATE SET role = excluded.role`,
			teamID, userID, string(role), Now()); err != nil {
			return fmt.Errorf("add the member an invitation was for: %w", err)
		}
		return nil
	})
}

// DeleteInvitation revokes one.
func (db *DB) DeleteInvitation(ctx context.Context, teamID, id string) error {
	result, err := db.Exec(ctx,
		`DELETE FROM team_invitations WHERE id = ? AND team_id = ?`, id, teamID)
	if err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneInvitations removes the ones nobody used. Called by the same sweep that
// prunes sessions and login attempts.
func (db *DB) PruneInvitations(ctx context.Context) (int64, error) {
	result, err := db.Exec(ctx,
		`DELETE FROM team_invitations WHERE accepted_at IS NULL AND expires_at < ?`, Now())
	if err != nil {
		return 0, fmt.Errorf("prune invitations: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
