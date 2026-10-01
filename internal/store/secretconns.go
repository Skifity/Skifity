package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// SecretConnection is how a team's panel signs in to a secret manager it
// already runs, so a variable can be read from there instead of stored here.
//
// Settings are what is not secret — an address, a mount, a region — and come
// back from the API as they are. What the connection signs in with is sealed
// and is never part of this struct.
type SecretConnection struct {
	ID       string            `json:"id"`
	TeamID   string            `json:"team_id"`
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Settings map[string]string `json:"settings"`
	// AllowedPaths are the path prefixes a reference through this connection
	// may name, as internal/secretmgr normalises them for the kind. A
	// reference is allowed at a prefix or under it, at the kind's separator.
	// Empty is every path the credentials can read.
	AllowedPaths []string `json:"allowed_paths"`
	// AllowedProjectIDs are the projects whose variables, an app's own or a
	// shared one, may read through this connection. Empty is every project.
	// The id of a project deleted since stays, so the list never empties
	// itself into "every project".
	AllowedProjectIDs []string `json:"allowed_project_ids"`
	// RefreshMinutes is how often the apps that read this connection are
	// refreshed on their own. Zero, the default, is never.
	RefreshMinutes int       `json:"refresh_minutes"`
	NextRefreshAt  time.Time `json:"next_refresh_at,omitzero"`
	LastRefreshAt  time.Time `json:"last_refresh_at,omitzero"`
	// RefreshFailures counts the periodic refreshes in a row that failed;
	// the wait before the next one doubles with each.
	RefreshFailures int       `json:"refresh_failures"`
	LastError       string    `json:"last_error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SecretConnectionRow carries the sealed credentials alongside.
type SecretConnectionRow struct {
	SecretConnection
	SealedCredentials string
}

// SecretConnectionContext is what a connection's credentials are sealed under.
func SecretConnectionContext(id string) string { return "secret_manager:" + id }

// ReferenceDigestContext is what the digest of a referenced variable's value
// is sealed under: the app and the variable.
func ReferenceDigestContext(appID, key string) string {
	return "reference_digest:" + appID + ":" + key
}

const secretConnectionColumns = `id, team_id, name, kind, settings, credentials_enc, refresh_minutes,
	next_refresh_at, last_refresh_at, refresh_failures, last_error, created_at, updated_at,
	allowed_paths, allowed_project_ids`

func scanSecretConnection(scan func(...any) error) (SecretConnectionRow, error) {
	var r SecretConnectionRow
	var settings, next, last, created, updated, paths, projects string
	if err := scan(&r.ID, &r.TeamID, &r.Name, &r.Kind, &settings, &r.SealedCredentials, &r.RefreshMinutes,
		&next, &last, &r.RefreshFailures, &r.LastError, &created, &updated, &paths, &projects); err != nil {
		return r, err
	}
	r.Settings = map[string]string{}
	if settings != "" {
		if err := json.Unmarshal([]byte(settings), &r.Settings); err != nil {
			return r, fmt.Errorf("read the settings of the secret manager %s: %w", r.Name, err)
		}
	}
	// A list that cannot be read fails the read rather than coming back
	// empty: empty is no limit, and a limit is not lifted by a bad row.
	var err error
	if r.AllowedPaths, err = readList(paths); err != nil {
		return r, fmt.Errorf("read the paths the secret manager %s is limited to: %w", r.Name, err)
	}
	if r.AllowedProjectIDs, err = readList(projects); err != nil {
		return r, fmt.Errorf("read the projects the secret manager %s is limited to: %w", r.Name, err)
	}
	r.NextRefreshAt, _ = ParseTime(next)
	r.LastRefreshAt, _ = ParseTime(last)
	r.CreatedAt, _ = ParseTime(created)
	r.UpdatedAt, _ = ParseTime(updated)
	return r, nil
}

// readList reads a JSON list column. It never answers nil, so the API always
// answers a list.
func readList(column string) ([]string, error) {
	out := []string{}
	if column == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(column), &out); err != nil {
		return nil, err
	}
	return nonNilList(out), nil
}

// listColumn is a list as its column holds it.
func listColumn(list []string) (string, error) {
	encoded, err := json.Marshal(nonNilList(list))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func nonNilList(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func timeColumn(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return FormatTime(t)
}

// CreateSecretConnection stores a new connection. sealed is its credentials,
// sealed under SecretConnectionContext of c.ID, so the caller chooses the id.
func (db *DB) CreateSecretConnection(ctx context.Context, c *SecretConnection, sealed string) error {
	if c.ID == "" {
		return errors.New("a secret manager connection needs its id before its credentials can be sealed")
	}
	settings, err := json.Marshal(nonNilSettings(c.Settings))
	if err != nil {
		return fmt.Errorf("record the settings of %s: %w", c.Name, err)
	}
	c.AllowedPaths, c.AllowedProjectIDs = nonNilList(c.AllowedPaths), nonNilList(c.AllowedProjectIDs)
	paths, err := listColumn(c.AllowedPaths)
	if err != nil {
		return fmt.Errorf("record the paths %s is limited to: %w", c.Name, err)
	}
	projects, err := listColumn(c.AllowedProjectIDs)
	if err != nil {
		return fmt.Errorf("record the projects %s is limited to: %w", c.Name, err)
	}
	now := Now()
	if _, err := db.Exec(ctx, `INSERT INTO secret_connections (`+secretConnectionColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.TeamID, c.Name, c.Kind, string(settings), sealed, c.RefreshMinutes,
		timeColumn(c.NextRefreshAt), "", 0, "", now, now, paths, projects); err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this team already has a secret manager called %s", ErrConflict, c.Name)
		}
		return fmt.Errorf("save the secret manager %s: %w", c.Name, err)
	}
	c.CreatedAt, _ = ParseTime(now)
	c.UpdatedAt = c.CreatedAt
	return nil
}

func nonNilSettings(settings map[string]string) map[string]string {
	if settings == nil {
		return map[string]string{}
	}
	return settings
}

// GetSecretConnection reads one connection by id.
func (db *DB) GetSecretConnection(ctx context.Context, id string) (SecretConnectionRow, error) {
	row := db.QueryRowContext(ctx, `SELECT `+secretConnectionColumns+` FROM secret_connections WHERE id = ?`, id)
	r, err := scanSecretConnection(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// FindSecretConnection reads one of a team's connections by its id or its
// name. A connection of another team is not found.
func (db *DB) FindSecretConnection(ctx context.Context, teamID, idOrName string) (SecretConnectionRow, error) {
	row := db.QueryRowContext(ctx, `SELECT `+secretConnectionColumns+` FROM secret_connections
		WHERE team_id = ? AND (id = ? OR name = ?) ORDER BY id = ? DESC LIMIT 1`,
		teamID, idOrName, idOrName, idOrName)
	r, err := scanSecretConnection(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// ListSecretConnections returns a team's connections by name.
func (db *DB) ListSecretConnections(ctx context.Context, teamID string) ([]SecretConnectionRow, error) {
	return db.querySecretConnections(ctx, `SELECT `+secretConnectionColumns+` FROM secret_connections
		WHERE team_id = ? ORDER BY name`, teamID)
}

// SecretConnectionsWithRefresh returns every connection with a periodic
// refresh switched on, whichever team it belongs to. The caller decides which
// are due: the times are compared as times, not as the strings they are
// stored as.
func (db *DB) SecretConnectionsWithRefresh(ctx context.Context) ([]SecretConnectionRow, error) {
	return db.querySecretConnections(ctx, `SELECT `+secretConnectionColumns+` FROM secret_connections
		WHERE refresh_minutes > 0 ORDER BY id`)
}

func (db *DB) querySecretConnections(ctx context.Context, query string, args ...any) ([]SecretConnectionRow, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list the secret managers: %w", err)
	}
	defer rows.Close()
	out := []SecretConnectionRow{}
	for rows.Next() {
		r, err := scanSecretConnection(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("read a secret manager: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateSecretConnection writes a connection's settings, limits and refresh
// interval, and its credentials when sealed is not empty. Empty keeps the
// ones stored.
func (db *DB) UpdateSecretConnection(ctx context.Context, c *SecretConnection, sealed string) error {
	settings, err := json.Marshal(nonNilSettings(c.Settings))
	if err != nil {
		return fmt.Errorf("record the settings of %s: %w", c.Name, err)
	}
	c.AllowedPaths, c.AllowedProjectIDs = nonNilList(c.AllowedPaths), nonNilList(c.AllowedProjectIDs)
	paths, err := listColumn(c.AllowedPaths)
	if err != nil {
		return fmt.Errorf("record the paths %s is limited to: %w", c.Name, err)
	}
	projects, err := listColumn(c.AllowedProjectIDs)
	if err != nil {
		return fmt.Errorf("record the projects %s is limited to: %w", c.Name, err)
	}
	now := Now()
	res, err := db.Exec(ctx, `UPDATE secret_connections SET settings = ?, refresh_minutes = ?, next_refresh_at = ?,
		refresh_failures = ?, last_error = ?, allowed_paths = ?, allowed_project_ids = ?,
		credentials_enc = CASE WHEN ? = '' THEN credentials_enc ELSE ? END, updated_at = ?
		WHERE id = ? AND team_id = ?`,
		string(settings), c.RefreshMinutes, timeColumn(c.NextRefreshAt), c.RefreshFailures, c.LastError,
		paths, projects, sealed, sealed, now, c.ID, c.TeamID)
	if err != nil {
		return fmt.Errorf("save the secret manager %s: %w", c.Name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	c.UpdatedAt, _ = ParseTime(now)
	return nil
}

// RecordSecretRefresh notes how a periodic refresh of a connection went and
// when the next one is due.
func (db *DB) RecordSecretRefresh(ctx context.Context, id string, at, next time.Time, failures int, lastError string) error {
	if _, err := db.Exec(ctx, `UPDATE secret_connections SET last_refresh_at = ?, next_refresh_at = ?,
		refresh_failures = ?, last_error = ? WHERE id = ?`,
		timeColumn(at), timeColumn(next), failures, lastError, id); err != nil {
		return fmt.Errorf("record the refresh of a secret manager: %w", err)
	}
	return nil
}

// ClaimSecretRefresh moves a connection's next refresh to next, so a run that
// is slow is not started again by the minute after it.
func (db *DB) ClaimSecretRefresh(ctx context.Context, id string, next time.Time) error {
	if _, err := db.Exec(ctx, `UPDATE secret_connections SET next_refresh_at = ? WHERE id = ?`,
		timeColumn(next), id); err != nil {
		return fmt.Errorf("claim the refresh of a secret manager: %w", err)
	}
	return nil
}

// DeleteSecretConnection removes one of a team's connections.
func (db *DB) DeleteSecretConnection(ctx context.Context, teamID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM secret_connections WHERE team_id = ? AND id = ?`, teamID, id)
	if err != nil {
		return fmt.Errorf("delete a secret manager: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SecretConnectionUse is one variable that reads a connection.
type SecretConnectionUse struct {
	// Scope is "app" for an app's own variable and "project" for a shared one.
	Scope string `json:"scope"`
	// OwnerID and Owner are the app or the project, by id and by name.
	OwnerID string `json:"owner_id"`
	Owner   string `json:"owner"`
	Key     string `json:"key"`
	// ProjectID is the project the variable is read in: the app's, or the
	// one sharing it. Path is the secret it names. Together they are what a
	// connection's limits are checked against.
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
}

// Label is how the use is written for a person: app/KEY.
func (u SecretConnectionUse) Label() string { return u.Owner + "/" + u.Key }

// SecretConnectionUses lists the variables that read a connection, app ones
// first.
func (db *DB) SecretConnectionUses(ctx context.Context, connectionID string) ([]SecretConnectionUse, error) {
	// The environment is joined loosely: a use whose project cannot be found
	// is still a use, and removing the connection is still refused for it.
	rows, err := db.QueryContext(ctx, `SELECT 'app', a.id, a.name, v.key, COALESCE(e.project_id, ''), v.ref_path
			FROM app_variables v
			JOIN apps a ON a.id = v.app_id
			LEFT JOIN environments e ON e.id = a.environment_id
			WHERE v.ref_connection_id = ?
		UNION ALL
		SELECT 'project', p.id, p.name, s.key, p.id, s.ref_path FROM shared_variables s
			JOIN projects p ON p.id = s.project_id WHERE s.ref_connection_id = ?`, connectionID, connectionID)
	if err != nil {
		return nil, fmt.Errorf("find the variables that read a secret manager: %w", err)
	}
	defer rows.Close()
	out := []SecretConnectionUse{}
	for rows.Next() {
		var u SecretConnectionUse
		if err := rows.Scan(&u.Scope, &u.OwnerID, &u.Owner, &u.Key, &u.ProjectID, &u.Path); err != nil {
			return nil, fmt.Errorf("read a variable that reads a secret manager: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope == "app"
		}
		return out[i].Label() < out[j].Label()
	})
	return out, nil
}

// CountSecretConnectionUses counts the variables that read each of a team's
// connections, by connection id.
func (db *DB) CountSecretConnectionUses(ctx context.Context, teamID string) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.id,
			(SELECT COUNT(*) FROM app_variables v WHERE v.ref_connection_id = c.id) +
			(SELECT COUNT(*) FROM shared_variables s WHERE s.ref_connection_id = c.id)
		FROM secret_connections c WHERE c.team_id = ?`, teamID)
	if err != nil {
		return nil, fmt.Errorf("count the variables that read the secret managers: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("count the variables that read a secret manager: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

// AppsReadingSecretConnection lists the apps that read a connection, through
// a variable of their own or one shared by their project, at most limit of
// them, oldest first.
func (db *DB) AppsReadingSecretConnection(ctx context.Context, connectionID string, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id FROM apps a WHERE a.id IN (
			SELECT v.app_id FROM app_variables v WHERE v.ref_connection_id = ?
			UNION
			SELECT a2.id FROM apps a2
				JOIN environments e ON e.id = a2.environment_id
				JOIN shared_variables s ON s.project_id = e.project_id
				WHERE s.ref_connection_id = ?)
		ORDER BY a.created_at LIMIT ?`, connectionID, connectionID, limit)
	if err != nil {
		return nil, fmt.Errorf("find the apps that read a secret manager: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read an app that reads a secret manager: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ReferenceDigests returns what each of an app's referenced variables held
// when it last reached the app, sealed, by variable.
func (db *DB) ReferenceDigests(ctx context.Context, appID string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, digest_enc FROM app_reference_digests WHERE app_id = ?`, appID)
	if err != nil {
		return nil, fmt.Errorf("read what an app's referenced variables held: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, sealed string
		if err := rows.Scan(&key, &sealed); err != nil {
			return nil, fmt.Errorf("read what a referenced variable held: %w", err)
		}
		out[key] = sealed
	}
	return out, rows.Err()
}

// SetReferenceDigests replaces what an app's referenced variables held with
// digests, sealed and by variable. A variable left out is forgotten.
func (db *DB) SetReferenceDigests(ctx context.Context, appID string, digests map[string]string) error {
	now := Now()
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_reference_digests WHERE app_id = ?`, appID); err != nil {
			return fmt.Errorf("forget what an app's referenced variables held: %w", err)
		}
		for key, sealed := range digests {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_reference_digests (app_id, key, digest_enc, updated_at)
				VALUES (?,?,?,?)`, appID, key, sealed, now); err != nil {
				return fmt.Errorf("record what %s held: %w", key, err)
			}
		}
		return nil
	})
}
