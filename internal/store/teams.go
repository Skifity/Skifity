package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"skifity/internal/kube"
)

// --- teams ---

// CreateTeam inserts a team.
func (db *DB) CreateTeam(ctx context.Context, t *Team) error {
	if t.ID == "" {
		t.ID = NewID("team")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO teams (id, name, slug, created_at) VALUES (?,?,?,?)`,
		t.ID, t.Name, t.Slug, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: a team with the name %s already exists", ErrConflict, t.Name)
		}
		return fmt.Errorf("create team: %w", err)
	}
	t.CreatedAt, _ = ParseTime(now)
	return nil
}

// GetTeam looks a team up by id.
func (db *DB) GetTeam(ctx context.Context, id string) (Team, error) {
	var t Team
	var created string
	err := db.QueryRowContext(ctx, `SELECT id, name, slug, created_at FROM teams WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Slug, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, ErrNotFound
		}
		return t, fmt.Errorf("get team: %w", err)
	}
	t.CreatedAt, _ = ParseTime(created)
	return t, nil
}

// ListTeamsForUser returns the teams a user belongs to, with their role in each.
func (db *DB) ListTeamsForUser(ctx context.Context, userID string) ([]Team, error) {
	rows, err := db.QueryContext(ctx, `SELECT t.id, t.name, t.slug, t.created_at, m.role
		FROM teams t JOIN memberships m ON m.team_id = t.id
		WHERE m.user_id = ? ORDER BY t.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		var t Team
		var created string
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &created, &t.Role); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		t.CreatedAt, _ = ParseTime(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTeam renames a team.
func (db *DB) UpdateTeam(ctx context.Context, t *Team) error {
	res, err := db.Exec(ctx, `UPDATE teams SET name = ?, slug = ? WHERE id = ?`, t.Name, t.Slug, t.ID)
	if err != nil {
		return fmt.Errorf("update team: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- memberships ---

// AddMember grants a user a role in a team, or changes the role they already have.
func (db *DB) AddMember(ctx context.Context, teamID, userID string, role Role) error {
	if !role.Valid() {
		return fmt.Errorf("%q is not a valid role", role)
	}
	_, err := db.Exec(ctx, `INSERT INTO memberships (team_id, user_id, role, created_at) VALUES (?,?,?,?)
		ON CONFLICT (team_id, user_id) DO UPDATE SET role = excluded.role`,
		teamID, userID, role, Now())
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

// GetMembership returns a user's role in a team.
func (db *DB) GetMembership(ctx context.Context, teamID, userID string) (Membership, error) {
	var m Membership
	var created string
	err := db.QueryRowContext(ctx, `SELECT team_id, user_id, role, created_at FROM memberships
		WHERE team_id = ? AND user_id = ?`, teamID, userID).
		Scan(&m.TeamID, &m.UserID, &m.Role, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return m, ErrNotFound
		}
		return m, fmt.Errorf("get membership: %w", err)
	}
	m.CreatedAt, _ = ParseTime(created)
	return m, nil
}

// ListMembers returns a team's members with their user records.
func (db *DB) ListMembers(ctx context.Context, teamID string) ([]struct {
	User User `json:"user"`
	Role Role `json:"role"`
}, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+prefixColumns("u", userColumns)+`, m.role
		FROM users u JOIN memberships m ON m.user_id = u.id
		WHERE m.team_id = ? ORDER BY u.created_at`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	out := []struct {
		User User `json:"user"`
		Role Role `json:"role"`
	}{}
	for rows.Next() {
		var u User
		var role Role
		var created, updated string
		var lastLogin sql.NullString
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.TOTPSecretEnc, &u.TOTPEnabled,
			&u.IsAdmin, &u.Disabled, &u.Locale, &u.Theme, &u.RecoverySaved, &created, &updated, &lastLogin, &role); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		u.CreatedAt, _ = ParseTime(created)
		u.UpdatedAt, _ = ParseTime(updated)
		u.LastLoginAt = scanTime(lastLogin)
		out = append(out, struct {
			User User `json:"user"`
			Role Role `json:"role"`
		}{User: u, Role: role})
	}
	return out, rows.Err()
}

// CountOwners reports how many owners a team has, so the last one cannot be
// removed or demoted and lock everyone out.
func (db *DB) CountOwners(ctx context.Context, teamID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memberships WHERE team_id = ? AND role = 'owner'`, teamID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return n, nil
}

// RemoveMember revokes a user's access to a team.
func (db *DB) RemoveMember(ctx context.Context, teamID, userID string) error {
	res, err := db.Exec(ctx, `DELETE FROM memberships WHERE team_id = ? AND user_id = ?`, teamID, userID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- projects ---

const projectColumns = `id, team_id, name, slug, description, created_at, updated_at`

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var created, updated string
	err := row.Scan(&p.ID, &p.TeamID, &p.Name, &p.Slug, &p.Description, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, fmt.Errorf("scan project: %w", err)
	}
	p.CreatedAt, _ = ParseTime(created)
	p.UpdatedAt, _ = ParseTime(updated)
	return p, nil
}

// CreateProject inserts a project.
func (db *DB) CreateProject(ctx context.Context, p *Project) error {
	if p.ID == "" {
		p.ID = NewID("prj")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO projects (id, team_id, name, slug, description, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`, p.ID, p.TeamID, p.Name, p.Slug, p.Description, now, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this team already has a project named %s", ErrConflict, p.Name)
		}
		return fmt.Errorf("create project: %w", err)
	}
	p.CreatedAt, _ = ParseTime(now)
	p.UpdatedAt = p.CreatedAt
	return nil
}

// GetProject looks a project up by id.
func (db *DB) GetProject(ctx context.Context, id string) (Project, error) {
	return scanProject(db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id))
}

// ListProjects returns a team's projects, oldest first.
func (db *DB) ListProjects(ctx context.Context, teamID string) ([]Project, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE team_id = ? ORDER BY created_at`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProject writes a project's mutable fields.
func (db *DB) UpdateProject(ctx context.Context, p *Project) error {
	now := Now()
	res, err := db.Exec(ctx, `UPDATE projects SET name = ?, slug = ?, description = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Slug, p.Description, now, p.ID)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	p.UpdatedAt, _ = ParseTime(now)
	return nil
}

// DeleteProject removes a project and everything under it.
func (db *DB) DeleteProject(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- environments ---

const envColumns = `id, project_id, name, slug, kind, namespace, source_ref, pod_security, created_at`

func scanEnvironment(row interface{ Scan(...any) error }) (Environment, error) {
	var e Environment
	var created string
	err := row.Scan(&e.ID, &e.ProjectID, &e.Name, &e.Slug, &e.Kind, &e.Namespace, &e.SourceRef,
		&e.PodSecurity, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e, ErrNotFound
		}
		return e, fmt.Errorf("scan environment: %w", err)
	}
	e.CreatedAt, _ = ParseTime(created)
	return e, nil
}

// CreateEnvironment inserts an environment. Namespace must already be computed
// and unique; see kube.NamespaceFor.
func (db *DB) CreateEnvironment(ctx context.Context, e *Environment) error {
	if e.ID == "" {
		e.ID = NewID("env")
	}
	if e.Kind == "" {
		e.Kind = EnvStandard
	}
	if e.PodSecurity == "" {
		e.PodSecurity = string(kube.PodSecurityRestricted)
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO environments (id, project_id, name, slug, kind, namespace, source_ref, pod_security, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		e.ID, e.ProjectID, e.Name, e.Slug, e.Kind, e.Namespace, e.SourceRef, e.PodSecurity, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this project already has an environment named %s", ErrConflict, e.Name)
		}
		return fmt.Errorf("create environment: %w", err)
	}
	e.CreatedAt, _ = ParseTime(now)
	return nil
}

// GetEnvironment looks an environment up by id.
func (db *DB) GetEnvironment(ctx context.Context, id string) (Environment, error) {
	return scanEnvironment(db.QueryRowContext(ctx, `SELECT `+envColumns+` FROM environments WHERE id = ?`, id))
}

// FindEnvironmentBySourceRef finds the preview environment created for a branch
// or pull request, so a second push updates it instead of creating another.
func (db *DB) FindEnvironmentBySourceRef(ctx context.Context, projectID, sourceRef string) (Environment, error) {
	return scanEnvironment(db.QueryRowContext(ctx,
		`SELECT `+envColumns+` FROM environments WHERE project_id = ? AND source_ref = ? AND kind = 'preview'`,
		projectID, sourceRef))
}

// ListEnvironments returns a project's environments.
func (db *DB) ListEnvironments(ctx context.Context, projectID string) ([]Environment, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+envColumns+` FROM environments WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListPreviewEnvironments returns every preview environment, for the cleanup job.
func (db *DB) ListPreviewEnvironments(ctx context.Context) ([]Environment, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+envColumns+` FROM environments WHERE kind = 'preview' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list preview environments: %w", err)
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetEnvironmentPodSecurity changes how strictly an environment confines its pods.
//
// The column has a CHECK constraint, so a value the panel does not offer is
// refused by SQLite rather than reaching a namespace label, where Kubernetes
// would take "strict" as an unknown level and quietly enforce nothing.
func (db *DB) SetEnvironmentPodSecurity(ctx context.Context, id, level string) error {
	res, err := db.Exec(ctx, `UPDATE environments SET pod_security = ? WHERE id = ?`, level, id)
	if err != nil {
		return fmt.Errorf("set the pod security level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteEnvironment removes an environment and everything in it.
func (db *DB) DeleteEnvironment(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM environments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete environment: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TeamIDForEnvironment walks environment -> project -> team. Authorization needs
// this on nearly every request, so it is one query rather than three.
func (db *DB) TeamIDForEnvironment(ctx context.Context, envID string) (string, error) {
	var teamID string
	err := db.QueryRowContext(ctx, `SELECT p.team_id FROM environments e
		JOIN projects p ON p.id = e.project_id WHERE e.id = ?`, envID).Scan(&teamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("resolve team for environment: %w", err)
	}
	return teamID, nil
}

// TeamIDForApp walks app -> environment -> project -> team.
func (db *DB) TeamIDForApp(ctx context.Context, appID string) (string, error) {
	var teamID string
	err := db.QueryRowContext(ctx, `SELECT p.team_id FROM apps a
		JOIN environments e ON e.id = a.environment_id
		JOIN projects p ON p.id = e.project_id WHERE a.id = ?`, appID).Scan(&teamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("resolve team for app: %w", err)
	}
	return teamID, nil
}

// TeamIDForDatabase walks database -> environment -> project -> team.
func (db *DB) TeamIDForDatabase(ctx context.Context, dbID string) (string, error) {
	var teamID string
	err := db.QueryRowContext(ctx, `SELECT p.team_id FROM databases d
		JOIN environments e ON e.id = d.environment_id
		JOIN projects p ON p.id = e.project_id WHERE d.id = ?`, dbID).Scan(&teamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("resolve team for database: %w", err)
	}
	return teamID, nil
}

// prefixColumns qualifies a column list with a table alias, so a join can reuse
// the same constant the plain select uses.
func prefixColumns(alias, columns string) string {
	var out []byte
	for part := range splitColumns(columns) {
		if len(out) > 0 {
			out = append(out, ", "...)
		}
		out = append(out, alias...)
		out = append(out, '.')
		out = append(out, part...)
	}
	return string(out)
}

// splitColumns yields each trimmed column name from a comma-separated list.
func splitColumns(columns string) func(func(string) bool) {
	return func(yield func(string) bool) {
		start := 0
		for i := 0; i <= len(columns); i++ {
			if i == len(columns) || columns[i] == ',' {
				part := trimSpaceAndNewlines(columns[start:i])
				if part != "" && !yield(part) {
					return
				}
				start = i + 1
			}
		}
	}
}

func trimSpaceAndNewlines(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}

// StalePreviewEnvironments returns preview environments that nothing has
// deployed to since a point in time.
//
// Previews are made by a webhook and were only ever removed by another one. A
// pull request that is merged while the panel is down, a repository whose
// webhook is deleted, a branch renamed on the server — each leaves a namespace
// running forever, and a self-hosted cluster quietly runs out of memory.
//
// The clock is the most recent deployment in the environment, falling back to
// when the environment was made, so a preview that is still being pushed to
// survives however long the pull request stays open.
func (db *DB) StalePreviewEnvironments(ctx context.Context, before time.Time) ([]Environment, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+prefixColumns("e", envColumns)+` FROM environments e
		WHERE e.kind = 'preview'
		AND COALESCE(
			(SELECT MAX(d.created_at) FROM deployments d
			 JOIN apps a ON a.id = d.app_id
			 WHERE a.environment_id = e.id),
			e.created_at
		) < ?
		ORDER BY e.created_at`, FormatTime(before))
	if err != nil {
		return nil, fmt.Errorf("list stale preview environments: %w", err)
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
