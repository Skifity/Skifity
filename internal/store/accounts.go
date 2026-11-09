package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// The accounts on the panel, as its administrator sees them: every person, the
// teams they are in, how they sign in, and what would stand in the way of
// removing them.

// AccountTeam is one team an account is in.
type AccountTeam struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Role              Role   `json:"role"`
	RequireStrongAuth bool   `json:"require_strong_auth"`
}

// AccountBlockers is what has to change before an account can be deleted.
type AccountBlockers struct {
	// SoleOwnerOf are the teams nobody else owns. Deleting the account would
	// leave each with nobody who can administer it.
	SoleOwnerOf []AccountTeam `json:"sole_owner_of"`
	// Plugins are the plugins the account installed, by id. Each runs with an
	// API token of the account's, which goes with it: deleting it would stop
	// them without a word.
	Plugins []string `json:"plugins"`
}

// Any reports whether anything stands in the way.
func (b AccountBlockers) Any() bool { return len(b.SoleOwnerOf) > 0 || len(b.Plugins) > 0 }

// AccountBlockedError is deleting an account something still depends on.
type AccountBlockedError struct{ Blockers AccountBlockers }

func (e *AccountBlockedError) Error() string {
	var parts []string
	if len(e.Blockers.SoleOwnerOf) > 0 {
		names := make([]string, 0, len(e.Blockers.SoleOwnerOf))
		for _, team := range e.Blockers.SoleOwnerOf {
			names = append(names, team.Name)
		}
		parts = append(parts, "the only owner of "+strings.Join(names, ", "))
	}
	if len(e.Blockers.Plugins) > 0 {
		parts = append(parts, "the installer of "+strings.Join(e.Blockers.Plugins, ", "))
	}
	return "the account is " + strings.Join(parts, " and ")
}

// Account is one person on the panel.
type Account struct {
	User  User          `json:"user"`
	Teams []AccountTeam `json:"teams"`
	// Passkeys and Tokens count what the account signs in with besides a
	// password, which deleting it removes.
	Passkeys int `json:"passkeys"`
	Tokens   int `json:"tokens"`
	// SSOIssuers are the identity providers it is linked to.
	SSOIssuers []string        `json:"sso_issuers"`
	Blockers   AccountBlockers `json:"blockers"`
}

// ListAccounts returns every account, oldest first, with its teams, how it
// signs in, and its blockers. A handful of queries for the whole panel rather
// than a handful per account.
func (db *DB) ListAccounts(ctx context.Context) ([]Account, error) {
	users, err := db.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, len(users))
	index := make(map[string]int, len(users))
	for i, user := range users {
		accounts[i] = Account{
			User: user, Teams: []AccountTeam{}, SSOIssuers: []string{},
			Blockers: AccountBlockers{SoleOwnerOf: []AccountTeam{}, Plugins: []string{}},
		}
		index[user.ID] = i
	}

	type membership struct {
		userID string
		team   AccountTeam
	}
	var memberships []membership
	owners := map[string]int{}
	if err := db.eachRow(ctx, `SELECT m.user_id, t.id, t.name, m.role, t.require_strong_auth
		FROM memberships m JOIN teams t ON t.id = m.team_id ORDER BY t.name COLLATE NOCASE`, nil,
		func(rows *sql.Rows) error {
			var m membership
			if err := rows.Scan(&m.userID, &m.team.ID, &m.team.Name, &m.team.Role, &m.team.RequireStrongAuth); err != nil {
				return err
			}
			memberships = append(memberships, m)
			if m.team.Role == RoleOwner {
				owners[m.team.ID]++
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	for _, m := range memberships {
		i, ok := index[m.userID]
		if !ok {
			continue
		}
		accounts[i].Teams = append(accounts[i].Teams, m.team)
		if m.team.Role == RoleOwner && owners[m.team.ID] == 1 {
			accounts[i].Blockers.SoleOwnerOf = append(accounts[i].Blockers.SoleOwnerOf, m.team)
		}
	}

	counts := []struct {
		query string
		args  []any
		into  func(*Account, int)
	}{
		{`SELECT user_id, COUNT(*) FROM passkeys GROUP BY user_id`, nil,
			func(a *Account, n int) { a.Passkeys = n }},
		// Live tokens only: an expired one opens nothing, and nothing is lost
		// with it.
		{`SELECT user_id, COUNT(*) FROM api_tokens WHERE expires_at IS NULL OR expires_at > ? GROUP BY user_id`,
			[]any{Now()}, func(a *Account, n int) { a.Tokens = n }},
	}
	for _, count := range counts {
		if err := db.eachRow(ctx, count.query, count.args, func(rows *sql.Rows) error {
			var userID string
			var n int
			if err := rows.Scan(&userID, &n); err != nil {
				return err
			}
			if i, ok := index[userID]; ok {
				count.into(&accounts[i], n)
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("count what accounts sign in with: %w", err)
		}
	}

	pairs := []struct {
		query string
		into  func(*Account, string)
	}{
		{`SELECT user_id, issuer FROM user_identities ORDER BY created_at`,
			func(a *Account, issuer string) { a.SSOIssuers = append(a.SSOIssuers, issuer) }},
		{`SELECT installed_by, id FROM plugins ORDER BY id`,
			func(a *Account, plugin string) { a.Blockers.Plugins = append(a.Blockers.Plugins, plugin) }},
	}
	for _, pair := range pairs {
		if err := db.eachRow(ctx, pair.query, nil, func(rows *sql.Rows) error {
			var userID, value string
			if err := rows.Scan(&userID, &value); err != nil {
				return err
			}
			if i, ok := index[userID]; ok {
				pair.into(&accounts[i], value)
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
	}
	return accounts, nil
}

// queryer is what reads run against: the database, or a transaction.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// eachRow runs a query and hands each row to fn.
func (db *DB) eachRow(ctx context.Context, query string, args []any, fn func(*sql.Rows) error) error {
	return eachRowOf(ctx, db, query, args, fn)
}

func eachRowOf(ctx context.Context, q queryer, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// AccountBlockersFor reports what stands in the way of deleting one account.
func (db *DB) AccountBlockersFor(ctx context.Context, id string) (AccountBlockers, error) {
	return accountBlockers(ctx, db, id)
}

func accountBlockers(ctx context.Context, q queryer, id string) (AccountBlockers, error) {
	blockers := AccountBlockers{SoleOwnerOf: []AccountTeam{}, Plugins: []string{}}
	if err := eachRowOf(ctx, q, `SELECT t.id, t.name, m.role, t.require_strong_auth
		FROM memberships m JOIN teams t ON t.id = m.team_id
		WHERE m.user_id = ? AND m.role = 'owner'
		  AND (SELECT COUNT(*) FROM memberships o WHERE o.team_id = m.team_id AND o.role = 'owner') = 1
		ORDER BY t.name COLLATE NOCASE`, []any{id}, func(rows *sql.Rows) error {
		var team AccountTeam
		if err := rows.Scan(&team.ID, &team.Name, &team.Role, &team.RequireStrongAuth); err != nil {
			return err
		}
		blockers.SoleOwnerOf = append(blockers.SoleOwnerOf, team)
		return nil
	}); err != nil {
		return blockers, fmt.Errorf("find the teams only this account owns: %w", err)
	}
	if err := eachRowOf(ctx, q, `SELECT id FROM plugins WHERE installed_by = ? ORDER BY id`, []any{id},
		func(rows *sql.Rows) error {
			var plugin string
			if err := rows.Scan(&plugin); err != nil {
				return err
			}
			blockers.Plugins = append(blockers.Plugins, plugin)
			return nil
		}); err != nil {
		return blockers, fmt.Errorf("find the plugins this account installed: %w", err)
	}
	return blockers, nil
}

// DeleteAccount removes an account, and by cascade its memberships, sessions,
// tokens, passkeys, recovery codes and linked identities. What it did stays in
// the activity log, under its address.
//
// It is refused with an *AccountBlockedError while the account is a team's
// only owner or the installer of a plugin, and with ErrLastAdmin for the last
// enabled administrator. The checks and the delete are one transaction, so a
// second owner leaving the team in the same instant cannot slip between them.
func (db *DB) DeleteAccount(ctx context.Context, id string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		blockers, err := accountBlockers(ctx, tx, id)
		if err != nil {
			return err
		}
		if blockers.Any() {
			return &AccountBlockedError{Blockers: blockers}
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND `+keepsAnAdmin, id)
		if err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
		if exists == 0 {
			return ErrNotFound
		}
		return ErrLastAdmin
	})
}

// IsAccountBlocked reports whether err is a delete refused for its blockers,
// and what they are.
func IsAccountBlocked(err error) (AccountBlockers, bool) {
	var blocked *AccountBlockedError
	if errors.As(err, &blocked) {
		return blocked.Blockers, true
	}
	return AccountBlockers{}, false
}
