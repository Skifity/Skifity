package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// --- settings ---
//
// Everything an operator configures at runtime lives here: the wildcard domain,
// the GitHub App, S3 credentials, SMTP, the DNS provider. Nothing in this table
// requires editing code or restarting the panel.

// GetSetting reads one setting. Missing settings return "" and no error, because
// "not configured yet" is the normal state for most of them.
func (db *DB) GetSetting(ctx context.Context, key string) (value string, encrypted bool, err error) {
	err = db.QueryRowContext(ctx, `SELECT value, encrypted FROM settings WHERE key = ?`, key).
		Scan(&value, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get setting %s: %w", key, err)
	}
	return value, encrypted, nil
}

// SetSetting writes one setting. value must already be sealed when encrypted is true.
func (db *DB) SetSetting(ctx context.Context, key, value string, encrypted bool, updatedBy string) error {
	_, err := db.Exec(ctx, `INSERT INTO settings (key, value, encrypted, updated_at, updated_by)
		VALUES (?,?,?,?,?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, encrypted = excluded.encrypted,
			updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		key, value, encrypted, Now(), updatedBy)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// DeleteSetting clears a setting entirely, which is how "disconnect" works.
func (db *DB) DeleteSetting(ctx context.Context, key string) error {
	_, err := db.Exec(ctx, `DELETE FROM settings WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete setting %s: %w", key, err)
	}
	return nil
}

// ListSettings returns every setting. Encrypted values come back sealed; the API
// layer replaces them with a "configured" flag rather than decrypting them.
func (db *DB) ListSettings(ctx context.Context) (map[string]struct {
	Value     string
	Encrypted bool
}, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value, encrypted FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()
	out := map[string]struct {
		Value     string
		Encrypted bool
	}{}
	for rows.Next() {
		var k, v string
		var enc bool
		if err := rows.Scan(&k, &v, &enc); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		out[k] = struct {
			Value     string
			Encrypted bool
		}{Value: v, Encrypted: enc}
	}
	return out, rows.Err()
}

// --- audit ---

// RecordAudit appends an audit entry. Audit writes must never fail a request, so
// callers log the error and carry on.
func (db *DB) RecordAudit(ctx context.Context, e *AuditEvent) error {
	if e.ID == "" {
		e.ID = NewID("aud")
	}
	if e.Metadata == "" {
		e.Metadata = "{}"
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO audit_events
		(id, team_id, actor_id, actor_label, action, target_type, target_id, target_label, ip, user_agent, metadata, at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.TeamID, e.ActorID, e.ActorLabel, e.Action, e.TargetType, e.TargetID, e.TargetLabel,
		e.IP, e.UserAgent, e.Metadata, now)
	if err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	e.At, _ = ParseTime(now)
	return nil
}

// ListAudit returns a team's audit trail, newest first, optionally filtered.
func (db *DB) ListAudit(ctx context.Context, teamID, action, targetID string, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Panel-wide events are recorded with no team, because there is one set of
	// settings, one keyring and one panel however many teams share them. They
	// were written and then invisible: this list filtered on the team alone, so
	// every password change, two-factor change, settings change, key rotation
	// and panel upgrade went into the table and came out of nothing.
	//
	// They are included here, narrowed to the ones this team's own people did,
	// plus the ones nobody did — so a team's admin does not learn that somebody
	// in another team exists from the audit log.
	query := `SELECT id, team_id, actor_id, actor_label, action, target_type, target_id, target_label,
		ip, user_agent, metadata, at FROM audit_events
		WHERE (team_id = ?
		  OR (team_id = '' AND (actor_id = ''
		      OR actor_id IN (SELECT user_id FROM memberships WHERE team_id = ?))))`
	args := []any{teamID, teamID}
	if action != "" {
		query += ` AND action = ?`
		args = append(args, action)
	}
	if targetID != "" {
		query += ` AND target_id = ?`
		args = append(args, targetID)
	}
	query += ` ORDER BY at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		var at string
		if err := rows.Scan(&e.ID, &e.TeamID, &e.ActorID, &e.ActorLabel, &e.Action, &e.TargetType,
			&e.TargetID, &e.TargetLabel, &e.IP, &e.UserAgent, &e.Metadata, &at); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		e.At, _ = ParseTime(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PurgeOldAudit trims the audit trail to a retention window.
func (db *DB) PurgeOldAudit(ctx context.Context, before time.Time) error {
	_, err := db.Exec(ctx, `DELETE FROM audit_events WHERE at < ?`, FormatTime(before))
	if err != nil {
		return fmt.Errorf("purge audit events: %w", err)
	}
	return nil
}

// --- git sources ---

// CreateGitSource stores a connection to a Git host.
func (db *DB) CreateGitSource(ctx context.Context, g *GitSource) error {
	if g.ID == "" {
		g.ID = NewID("git")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO git_sources (id, team_id, kind, name, base_url, account, config_enc, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, g.ID, g.TeamID, g.Kind, g.Name, g.BaseURL, g.Account, g.ConfigEnc, now, now)
	if err != nil {
		return fmt.Errorf("create git source: %w", err)
	}
	g.CreatedAt, _ = ParseTime(now)
	g.UpdatedAt = g.CreatedAt
	return nil
}

// GetGitSource looks a git source up by id.
func (db *DB) GetGitSource(ctx context.Context, id string) (GitSource, error) {
	var g GitSource
	var created, updated string
	err := db.QueryRowContext(ctx, `SELECT id, team_id, kind, name, base_url, account, config_enc, created_at, updated_at
		FROM git_sources WHERE id = ?`, id).
		Scan(&g.ID, &g.TeamID, &g.Kind, &g.Name, &g.BaseURL, &g.Account, &g.ConfigEnc, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return g, ErrNotFound
		}
		return g, fmt.Errorf("get git source: %w", err)
	}
	g.CreatedAt, _ = ParseTime(created)
	g.UpdatedAt, _ = ParseTime(updated)
	return g, nil
}

// ListGitSources returns a team's git connections.
func (db *DB) ListGitSources(ctx context.Context, teamID string) ([]GitSource, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, team_id, kind, name, base_url, account, config_enc, created_at, updated_at
		FROM git_sources WHERE team_id = ? ORDER BY created_at`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list git sources: %w", err)
	}
	defer rows.Close()
	out := []GitSource{}
	for rows.Next() {
		var g GitSource
		var created, updated string
		if err := rows.Scan(&g.ID, &g.TeamID, &g.Kind, &g.Name, &g.BaseURL, &g.Account, &g.ConfigEnc, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan git source: %w", err)
		}
		g.CreatedAt, _ = ParseTime(created)
		g.UpdatedAt, _ = ParseTime(updated)
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateGitSource writes a git source's mutable fields.
func (db *DB) UpdateGitSource(ctx context.Context, g *GitSource) error {
	now := Now()
	res, err := db.Exec(ctx, `UPDATE git_sources SET name=?, base_url=?, account=?, config_enc=?, updated_at=? WHERE id=?`,
		g.Name, g.BaseURL, g.Account, g.ConfigEnc, now, g.ID)
	if err != nil {
		return fmt.Errorf("update git source: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	g.UpdatedAt, _ = ParseTime(now)
	return nil
}

// DeleteGitSource removes a git connection.
func (db *DB) DeleteGitSource(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM git_sources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete git source: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- notification channels ---

// CreateNotificationChannel stores a delivery target.
func (db *DB) CreateNotificationChannel(ctx context.Context, c *NotificationChannel) error {
	if c.ID == "" {
		c.ID = NewID("ntf")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO notification_channels (id, team_id, kind, name, config_enc, events, enabled, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, c.ID, c.TeamID, c.Kind, c.Name, c.ConfigEnc, c.Events, c.Enabled, now, now)
	if err != nil {
		return fmt.Errorf("create notification channel: %w", err)
	}
	c.CreatedAt, _ = ParseTime(now)
	c.UpdatedAt = c.CreatedAt
	return nil
}

// ListNotificationChannels returns a team's channels.
func (db *DB) ListNotificationChannels(ctx context.Context, teamID string) ([]NotificationChannel, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, team_id, kind, name, config_enc, events, enabled, created_at, updated_at
		FROM notification_channels WHERE team_id = ? ORDER BY created_at`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()
	out := []NotificationChannel{}
	for rows.Next() {
		var c NotificationChannel
		var created, updated string
		if err := rows.Scan(&c.ID, &c.TeamID, &c.Kind, &c.Name, &c.ConfigEnc, &c.Events, &c.Enabled, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan notification channel: %w", err)
		}
		c.CreatedAt, _ = ParseTime(created)
		c.UpdatedAt, _ = ParseTime(updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetNotificationChannel looks a channel up by id.
func (db *DB) GetNotificationChannel(ctx context.Context, id string) (NotificationChannel, error) {
	var c NotificationChannel
	var created, updated string
	err := db.QueryRowContext(ctx, `SELECT id, team_id, kind, name, config_enc, events, enabled, created_at, updated_at
		FROM notification_channels WHERE id = ?`, id).
		Scan(&c.ID, &c.TeamID, &c.Kind, &c.Name, &c.ConfigEnc, &c.Events, &c.Enabled, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c, ErrNotFound
		}
		return c, fmt.Errorf("get notification channel: %w", err)
	}
	c.CreatedAt, _ = ParseTime(created)
	c.UpdatedAt, _ = ParseTime(updated)
	return c, nil
}

// UpdateNotificationChannel writes a channel's mutable fields.
func (db *DB) UpdateNotificationChannel(ctx context.Context, c *NotificationChannel) error {
	now := Now()
	res, err := db.Exec(ctx, `UPDATE notification_channels SET name=?, config_enc=?, events=?, enabled=?, updated_at=? WHERE id=?`,
		c.Name, c.ConfigEnc, c.Events, c.Enabled, now, c.ID)
	if err != nil {
		return fmt.Errorf("update notification channel: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	c.UpdatedAt, _ = ParseTime(now)
	return nil
}

// DeleteNotificationChannel removes a channel.
func (db *DB) DeleteNotificationChannel(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM notification_channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- cluster components ---

// GetComponent reports whether an optional add-on is installed.
func (db *DB) GetComponent(ctx context.Context, name string) (ClusterComponent, error) {
	var c ClusterComponent
	var installed sql.NullString
	err := db.QueryRowContext(ctx, `SELECT name, status, version, installed_at, detail FROM cluster_components WHERE name = ?`, name).
		Scan(&c.Name, &c.Status, &c.Version, &installed, &c.Detail)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ClusterComponent{Name: name, Status: "absent"}, nil
		}
		return c, fmt.Errorf("get component %s: %w", name, err)
	}
	c.InstalledAt = scanTime(installed)
	return c, nil
}

// SetComponent records an add-on's installation state.
func (db *DB) SetComponent(ctx context.Context, c ClusterComponent) error {
	var installed any
	if !c.InstalledAt.IsZero() {
		installed = FormatTime(c.InstalledAt)
	}
	_, err := db.Exec(ctx, `INSERT INTO cluster_components (name, status, version, installed_at, detail)
		VALUES (?,?,?,?,?)
		ON CONFLICT (name) DO UPDATE SET status = excluded.status, version = excluded.version,
			installed_at = COALESCE(excluded.installed_at, cluster_components.installed_at),
			detail = excluded.detail`,
		c.Name, c.Status, c.Version, installed, c.Detail)
	if err != nil {
		return fmt.Errorf("set component %s: %w", c.Name, err)
	}
	return nil
}

// ListComponents returns every add-on the panel knows about.
func (db *DB) ListComponents(ctx context.Context) ([]ClusterComponent, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, status, version, installed_at, detail FROM cluster_components ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list components: %w", err)
	}
	defer rows.Close()
	out := []ClusterComponent{}
	for rows.Next() {
		var c ClusterComponent
		var installed sql.NullString
		if err := rows.Scan(&c.Name, &c.Status, &c.Version, &installed, &c.Detail); err != nil {
			return nil, fmt.Errorf("scan component: %w", err)
		}
		c.InstalledAt = scanTime(installed)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountApps is how many apps exist across every team. Used by the metrics page,
// which is about the panel rather than about one tenant.
func (db *DB) CountApps(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count apps: %w", err)
	}
	return n, nil
}

// CountServersByStatus is how many servers are in each state.
//
// By status rather than a total, because "three servers" is not the number
// anybody wants to alert on and "one of them is not ready" is.
func (db *DB) CountServersByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, `SELECT status, COUNT(*) FROM servers GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("count servers: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[status] = count
	}
	return out, rows.Err()
}
