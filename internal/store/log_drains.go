package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LogDrain is somewhere a team's apps' logs are shipped to, as anybody in the
// team may see it: never its secrets.
type LogDrain struct {
	ID     string `json:"id"`
	TeamID string `json:"team_id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	// Settings are the drain's form without its secrets.
	Settings map[string]string `json:"-"`
	Enabled  bool              `json:"enabled"`
	// Scoped limits the drain to Projects. A scoped drain whose projects are
	// all gone sends nothing, rather than everything.
	Scoped        bool     `json:"scoped"`
	Projects      []string `json:"projects"`
	IncludeBuilds bool     `json:"include_builds"`
	// TestedAt is the last test the panel sent through it, and TestError why
	// that failed when it did.
	TestedAt  time.Time `json:"tested_at,omitzero"`
	TestError string    `json:"test_error,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LogDrainRow carries what no API answers: the sealed secrets.
type LogDrainRow struct {
	LogDrain
	SealedSecrets string
}

// LogDrainContext is what a drain's secrets are sealed under: the team, the
// drain and the address it sends to. A row copied to another team does not
// open, and neither does one whose address was changed by hand, so a
// credential is never sent anywhere the person who typed it did not send it.
func LogDrainContext(teamID, drainID, destination string) string {
	return "log_drain:" + teamID + ":" + drainID + ":" + destination
}

const logDrainColumns = `id, team_id, name, kind, settings, secrets_enc, enabled, scoped, include_builds,
	tested_at, test_error, created_by, created_at, updated_at`

func scanLogDrain(row interface{ Scan(...any) error }) (LogDrainRow, error) {
	var d LogDrainRow
	var settings, tested, created, updated string
	if err := row.Scan(&d.ID, &d.TeamID, &d.Name, &d.Kind, &settings, &d.SealedSecrets, &d.Enabled, &d.Scoped,
		&d.IncludeBuilds, &tested, &d.TestError, &d.CreatedBy, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return d, ErrNotFound
		}
		return d, fmt.Errorf("read a log drain: %w", err)
	}
	if err := json.Unmarshal([]byte(settings), &d.Settings); err != nil {
		return d, fmt.Errorf("read the settings of the log drain %s: %w", d.ID, err)
	}
	if d.Settings == nil {
		d.Settings = map[string]string{}
	}
	d.Projects = []string{}
	d.TestedAt, _ = ParseTime(tested)
	d.CreatedAt, _ = ParseTime(created)
	d.UpdatedAt, _ = ParseTime(updated)
	return d, nil
}

// CreateLogDrain stores a drain that has passed its test. d.ID must be set,
// because the seal names it.
func (db *DB) CreateLogDrain(ctx context.Context, d *LogDrainRow) error {
	if d.ID == "" {
		return errors.New("a log drain needs its id before it is sealed and saved")
	}
	settings, err := json.Marshal(d.Settings)
	if err != nil {
		return err
	}
	now := Now()
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO log_drains (`+logDrainColumns+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, d.TeamID, d.Name, d.Kind, string(settings), d.SealedSecrets, d.Enabled, d.Scoped, d.IncludeBuilds,
			formatOptional(d.TestedAt), d.TestError, d.CreatedBy, now, now); err != nil {
			return mapError(err)
		}
		return setDrainProjects(ctx, tx, &d.LogDrain)
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this team already has a log drain called %s", ErrConflict, d.Name)
		}
		return fmt.Errorf("save the log drain %s: %w", d.Name, err)
	}
	d.CreatedAt, _ = ParseTime(now)
	d.UpdatedAt = d.CreatedAt
	return nil
}

// UpdateLogDrain writes everything about a drain but its team and its kind.
func (db *DB) UpdateLogDrain(ctx context.Context, d *LogDrainRow) error {
	settings, err := json.Marshal(d.Settings)
	if err != nil {
		return err
	}
	now := Now()
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE log_drains SET name = ?, settings = ?, secrets_enc = ?, enabled = ?,
			scoped = ?, include_builds = ?, tested_at = ?, test_error = ?, updated_at = ? WHERE id = ? AND team_id = ?`,
			d.Name, string(settings), d.SealedSecrets, d.Enabled, d.Scoped, d.IncludeBuilds,
			formatOptional(d.TestedAt), d.TestError, now, d.ID, d.TeamID)
		if err != nil {
			return mapError(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return setDrainProjects(ctx, tx, &d.LogDrain)
	})
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this team already has a log drain called %s", ErrConflict, d.Name)
		}
		return fmt.Errorf("update the log drain %s: %w", d.ID, err)
	}
	d.UpdatedAt, _ = ParseTime(now)
	return nil
}

// RecordLogDrainTest notes a test sent through a drain, and why it failed
// when it did. It is not a change to the drain.
func (db *DB) RecordLogDrainTest(ctx context.Context, teamID, id string, at time.Time, failure string) error {
	if _, err := db.Exec(ctx, `UPDATE log_drains SET tested_at = ?, test_error = ? WHERE id = ? AND team_id = ?`,
		FormatTime(at), failure, id, teamID); err != nil {
		return fmt.Errorf("record the test of the log drain %s: %w", id, err)
	}
	return nil
}

// setDrainProjects replaces the projects a drain is limited to. A project of
// another team is skipped rather than stored, so the list can only ever
// narrow what the team's own logs reach.
func setDrainProjects(ctx context.Context, tx *sql.Tx, d *LogDrain) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM log_drain_projects WHERE drain_id = ?`, d.ID); err != nil {
		return fmt.Errorf("clear the log drain's projects: %w", err)
	}
	if !d.Scoped {
		return nil
	}
	for _, projectID := range d.Projects {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO log_drain_projects (drain_id, project_id)
			SELECT ?, id FROM projects WHERE id = ? AND team_id = ?`, d.ID, projectID, d.TeamID); err != nil {
			return fmt.Errorf("limit the log drain to a project: %w", err)
		}
	}
	return nil
}

// ListLogDrains returns a team's drains by name, each with its projects.
func (db *DB) ListLogDrains(ctx context.Context, teamID string) ([]LogDrainRow, error) {
	return db.queryLogDrains(ctx, `SELECT `+logDrainColumns+` FROM log_drains WHERE team_id = ? ORDER BY name`, teamID)
}

// ListEnabledLogDrains returns every team's enabled drains, for the collector.
func (db *DB) ListEnabledLogDrains(ctx context.Context) ([]LogDrainRow, error) {
	return db.queryLogDrains(ctx, `SELECT `+logDrainColumns+` FROM log_drains WHERE enabled = 1 ORDER BY id`)
}

func (db *DB) queryLogDrains(ctx context.Context, query string, args ...any) ([]LogDrainRow, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list log drains: %w", err)
	}
	defer rows.Close()
	out := []LogDrainRow{}
	index := map[string]int{}
	for rows.Next() {
		d, err := scanLogDrain(rows)
		if err != nil {
			return nil, err
		}
		index[d.ID] = len(out)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}

	// The projects of all of them in one query, by name.
	ids := make([]any, 0, len(out))
	for _, d := range out {
		ids = append(ids, d.ID)
	}
	projects, err := db.QueryContext(ctx, `SELECT dp.drain_id, dp.project_id FROM log_drain_projects dp
		JOIN projects p ON p.id = dp.project_id
		WHERE dp.drain_id IN (`+placeholders(len(ids))+`) ORDER BY p.name`, ids...)
	if err != nil {
		return nil, fmt.Errorf("list the log drains' projects: %w", err)
	}
	defer projects.Close()
	for projects.Next() {
		var drainID, projectID string
		if err := projects.Scan(&drainID, &projectID); err != nil {
			return nil, fmt.Errorf("read a log drain's project: %w", err)
		}
		out[index[drainID]].Projects = append(out[index[drainID]].Projects, projectID)
	}
	return out, projects.Err()
}

// GetLogDrain returns one of a team's drains, or ErrNotFound — including for
// a drain that exists and belongs to another team.
func (db *DB) GetLogDrain(ctx context.Context, teamID, id string) (LogDrainRow, error) {
	drains, err := db.queryLogDrains(ctx, `SELECT `+logDrainColumns+` FROM log_drains WHERE team_id = ? AND id = ?`, teamID, id)
	if err != nil {
		return LogDrainRow{}, err
	}
	if len(drains) == 0 {
		return LogDrainRow{}, ErrNotFound
	}
	return drains[0], nil
}

// DeleteLogDrain removes one of a team's drains.
func (db *DB) DeleteLogDrain(ctx context.Context, teamID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM log_drains WHERE team_id = ? AND id = ?`, teamID, id)
	if err != nil {
		return fmt.Errorf("delete the log drain %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LogDrainName is a name the collector puts beside an id in each line: a
// team's, a project's, an environment's by its namespace, or an app's.
type LogDrainName struct {
	Kind, ID, Name string
}

// LogDrainNames returns the names of everything the given teams have.
func (db *DB) LogDrainNames(ctx context.Context, teamIDs []string) ([]LogDrainName, error) {
	out := []LogDrainName{}
	if len(teamIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(teamIDs))
	for i, id := range teamIDs {
		args[i] = id
	}
	in := placeholders(len(args))
	queries := []struct{ kind, query string }{
		{"team", `SELECT id, name FROM teams WHERE id IN (` + in + `)`},
		{"project", `SELECT id, name FROM projects WHERE team_id IN (` + in + `)`},
		{"environment", `SELECT e.namespace, e.name FROM environments e
			JOIN projects p ON p.id = e.project_id WHERE p.team_id IN (` + in + `)`},
		{"app", `SELECT a.id, a.name FROM apps a JOIN environments e ON e.id = a.environment_id
			JOIN projects p ON p.id = e.project_id WHERE p.team_id IN (` + in + `)`},
	}
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, q.query, args...)
		if err != nil {
			return nil, fmt.Errorf("list the names of every %s: %w", q.kind, err)
		}
		for rows.Next() {
			name := LogDrainName{Kind: q.kind}
			if err := rows.Scan(&name.ID, &name.Name); err != nil {
				rows.Close()
				return nil, fmt.Errorf("read the name of a %s: %w", q.kind, err)
			}
			out = append(out, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func formatOptional(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return FormatTime(t)
}
