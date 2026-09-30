package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// --- databases ---

const databaseColumns = `id, environment_id, name, slug, engine, engine_version, status, status_detail,
	instances, storage_gb, cpu_request_m, mem_request_mb, credentials_enc, created_at, updated_at`

func scanDatabase(row interface{ Scan(...any) error }) (Database, error) {
	var d Database
	var created, updated string
	err := row.Scan(&d.ID, &d.EnvironmentID, &d.Name, &d.Slug, &d.Engine, &d.EngineVersion, &d.Status,
		&d.StatusDetail, &d.Instances, &d.StorageGB, &d.CPURequestM, &d.MemRequestMB, &d.CredentialsEnc,
		&created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return d, ErrNotFound
		}
		return d, fmt.Errorf("scan database: %w", err)
	}
	d.CreatedAt, _ = ParseTime(created)
	d.UpdatedAt, _ = ParseTime(updated)
	return d, nil
}

// CreateDatabase inserts a managed database.
func (db *DB) CreateDatabase(ctx context.Context, d *Database) error {
	if d.ID == "" {
		d.ID = NewID("db")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO databases
		(id, environment_id, name, slug, engine, engine_version, status, status_detail, instances,
		 storage_gb, cpu_request_m, mem_request_mb, credentials_enc, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.EnvironmentID, d.Name, d.Slug, d.Engine, d.EngineVersion, defaultStr(d.Status, "creating"),
		d.StatusDetail, max(d.Instances, 1), max(d.StorageGB, 1), d.CPURequestM, d.MemRequestMB,
		d.CredentialsEnc, now, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this environment already has a database named %s", ErrConflict, d.Name)
		}
		return fmt.Errorf("create database: %w", err)
	}
	d.CreatedAt, _ = ParseTime(now)
	d.UpdatedAt = d.CreatedAt
	return nil
}

// GetDatabase looks a database up by id.
func (db *DB) GetDatabase(ctx context.Context, id string) (Database, error) {
	return scanDatabase(db.QueryRowContext(ctx, `SELECT `+databaseColumns+` FROM databases WHERE id = ?`, id))
}

// ListDatabases returns an environment's databases.
func (db *DB) ListDatabases(ctx context.Context, envID string) ([]Database, error) {
	return db.queryDatabases(ctx, `SELECT `+databaseColumns+` FROM databases WHERE environment_id = ? ORDER BY created_at`, envID)
}

// ListAllDatabases returns every database, for the backup scheduler.
func (db *DB) ListAllDatabases(ctx context.Context) ([]Database, error) {
	return db.queryDatabases(ctx, `SELECT `+databaseColumns+` FROM databases ORDER BY created_at`)
}

func (db *DB) queryDatabases(ctx context.Context, query string, args ...any) ([]Database, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDatabase writes a database's mutable fields.
func (db *DB) UpdateDatabase(ctx context.Context, d *Database) error {
	now := Now()
	res, err := db.Exec(ctx, `UPDATE databases SET name=?, engine_version=?, status=?, status_detail=?,
		instances=?, storage_gb=?, cpu_request_m=?, mem_request_mb=?, credentials_enc=?, updated_at=? WHERE id=?`,
		d.Name, d.EngineVersion, d.Status, d.StatusDetail, d.Instances, d.StorageGB, d.CPURequestM,
		d.MemRequestMB, d.CredentialsEnc, now, d.ID)
	if err != nil {
		return fmt.Errorf("update database: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	d.UpdatedAt, _ = ParseTime(now)
	return nil
}

// SetDatabaseStatus updates just the status pair.
func (db *DB) SetDatabaseStatus(ctx context.Context, id, status, detail string) error {
	_, err := db.Exec(ctx, `UPDATE databases SET status = ?, status_detail = ?, updated_at = ? WHERE id = ?`,
		status, detail, Now(), id)
	if err != nil {
		return fmt.Errorf("set database status: %w", err)
	}
	return nil
}

// DeleteDatabase removes a database record.
func (db *DB) DeleteDatabase(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM databases WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete database: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- database links ---

// LinkDatabase injects a database's connection string into an app as varName.
func (db *DB) LinkDatabase(ctx context.Context, databaseID, appID, varName string) error {
	_, err := db.Exec(ctx, `INSERT INTO database_links (database_id, app_id, var_name, created_at)
		VALUES (?,?,?,?) ON CONFLICT (database_id, app_id) DO UPDATE SET var_name = excluded.var_name`,
		databaseID, appID, varName, Now())
	if err != nil {
		return fmt.Errorf("link database: %w", err)
	}
	return nil
}

// UnlinkDatabase removes the injection.
func (db *DB) UnlinkDatabase(ctx context.Context, databaseID, appID string) error {
	res, err := db.Exec(ctx, `DELETE FROM database_links WHERE database_id = ? AND app_id = ?`, databaseID, appID)
	if err != nil {
		return fmt.Errorf("unlink database: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListLinksForApp returns the databases linked into one app.
func (db *DB) ListLinksForApp(ctx context.Context, appID string) ([]DatabaseLink, error) {
	return db.queryLinks(ctx, `SELECT database_id, app_id, var_name, created_at FROM database_links WHERE app_id = ?`, appID)
}

// ListLinksForDatabase returns the apps a database is linked into.
func (db *DB) ListLinksForDatabase(ctx context.Context, databaseID string) ([]DatabaseLink, error) {
	return db.queryLinks(ctx, `SELECT database_id, app_id, var_name, created_at FROM database_links WHERE database_id = ?`, databaseID)
}

func (db *DB) queryLinks(ctx context.Context, query string, args ...any) ([]DatabaseLink, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list database links: %w", err)
	}
	defer rows.Close()
	out := []DatabaseLink{}
	for rows.Next() {
		var l DatabaseLink
		var created string
		if err := rows.Scan(&l.DatabaseID, &l.AppID, &l.VarName, &created); err != nil {
			return nil, fmt.Errorf("scan database link: %w", err)
		}
		l.CreatedAt, _ = ParseTime(created)
		out = append(out, l)
	}
	return out, rows.Err()
}

// --- backups ---

// SetBackupPolicy creates or replaces the schedule for a target.
func (db *DB) SetBackupPolicy(ctx context.Context, p *BackupPolicy) error {
	if p.ID == "" {
		p.ID = NewID("bkp")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO backup_policies
		(id, target_type, target_id, schedule, retention, destination, enabled, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (target_type, target_id) DO UPDATE SET schedule = excluded.schedule,
			retention = excluded.retention, destination = excluded.destination,
			enabled = excluded.enabled, updated_at = excluded.updated_at`,
		p.ID, p.TargetType, p.TargetID, p.Schedule, p.Retention, defaultStr(p.Destination, "s3"),
		p.Enabled, now, now)
	if err != nil {
		return fmt.Errorf("set backup policy: %w", err)
	}
	p.UpdatedAt, _ = ParseTime(now)
	return nil
}

// GetBackupPolicy returns a target's schedule.
func (db *DB) GetBackupPolicy(ctx context.Context, targetType, targetID string) (BackupPolicy, error) {
	var p BackupPolicy
	var created, updated string
	err := db.QueryRowContext(ctx, `SELECT id, target_type, target_id, schedule, retention, destination,
		enabled, created_at, updated_at FROM backup_policies WHERE target_type = ? AND target_id = ?`,
		targetType, targetID).
		Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Schedule, &p.Retention, &p.Destination, &p.Enabled, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, fmt.Errorf("get backup policy: %w", err)
	}
	p.CreatedAt, _ = ParseTime(created)
	p.UpdatedAt, _ = ParseTime(updated)
	return p, nil
}

// ListEnabledBackupPolicies feeds the scheduler.
func (db *DB) ListEnabledBackupPolicies(ctx context.Context) ([]BackupPolicy, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, target_type, target_id, schedule, retention, destination,
		enabled, created_at, updated_at FROM backup_policies WHERE enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("list backup policies: %w", err)
	}
	defer rows.Close()
	out := []BackupPolicy{}
	for rows.Next() {
		var p BackupPolicy
		var created, updated string
		if err := rows.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Schedule, &p.Retention, &p.Destination,
			&p.Enabled, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan backup policy: %w", err)
		}
		p.CreatedAt, _ = ParseTime(created)
		p.UpdatedAt, _ = ParseTime(updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateBackup records a backup attempt.
func (db *DB) CreateBackup(ctx context.Context, b *Backup) error {
	if b.ID == "" {
		b.ID = NewID("bak")
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO backups
		(id, target_type, target_id, status, kind, location, size_bytes, error_message, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		b.ID, b.TargetType, b.TargetID, defaultStr(b.Status, "running"), defaultStr(b.Kind, "scheduled"),
		b.Location, b.SizeBytes, b.ErrorMessage, now)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	b.CreatedAt, _ = ParseTime(now)
	return nil
}

// FinishBackup records the outcome of a backup.
func (db *DB) FinishBackup(ctx context.Context, id, status, location string, size int64, errMsg string) error {
	_, err := db.Exec(ctx, `UPDATE backups SET status = ?, location = ?, size_bytes = ?, error_message = ?,
		finished_at = ? WHERE id = ?`, status, location, size, errMsg, Now(), id)
	if err != nil {
		return fmt.Errorf("finish backup: %w", err)
	}
	return nil
}

func scanBackups(rows *sql.Rows) ([]Backup, error) {
	out := []Backup{}
	for rows.Next() {
		var b Backup
		var created string
		var finished sql.NullString
		if err := rows.Scan(&b.ID, &b.TargetType, &b.TargetID, &b.Status, &b.Kind, &b.Location,
			&b.SizeBytes, &b.ErrorMessage, &created, &finished); err != nil {
			return nil, fmt.Errorf("scan backup: %w", err)
		}
		b.CreatedAt, _ = ParseTime(created)
		b.FinishedAt = scanTime(finished)
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListUnfinishedBackups finds backups interrupted by a panel restart.
//
// A backup row is written as "running" and finished by the goroutine taking it.
// If the panel stops in between, nothing finishes it: the row says a backup is
// in progress forever, and the panel is a Deployment that restarts for an
// upgrade or a drained node like anything else.
func (db *DB) ListUnfinishedBackups(ctx context.Context) ([]Backup, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, target_type, target_id, status, kind, location, size_bytes,
		error_message, created_at, finished_at FROM backups WHERE status = 'running' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list unfinished backups: %w", err)
	}
	defer rows.Close()
	return scanBackups(rows)
}

// ListDatabasesBeingCreated finds databases interrupted while being provisioned.
//
// The same shape as an unfinished backup, with a worse consequence: a database
// stuck at "creating" cannot be backed up — the manager refuses a target that
// is not running — so it is not only misleading, it is unusable.
func (db *DB) ListDatabasesBeingCreated(ctx context.Context) ([]Database, error) {
	return db.queryDatabases(ctx, `SELECT `+databaseColumns+` FROM databases
		WHERE status IN ('creating','starting') ORDER BY created_at`)
}

// GetBackup looks a backup up by id.
func (db *DB) GetBackup(ctx context.Context, id string) (Backup, error) {
	var b Backup
	var created string
	var finished sql.NullString
	err := db.QueryRowContext(ctx, `SELECT id, target_type, target_id, status, kind, location, size_bytes,
		error_message, created_at, finished_at FROM backups WHERE id = ?`, id).
		Scan(&b.ID, &b.TargetType, &b.TargetID, &b.Status, &b.Kind, &b.Location, &b.SizeBytes,
			&b.ErrorMessage, &created, &finished)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return b, ErrNotFound
		}
		return b, fmt.Errorf("get backup: %w", err)
	}
	b.CreatedAt, _ = ParseTime(created)
	b.FinishedAt = scanTime(finished)
	return b, nil
}

// ListBackups returns a target's backups, newest first.
func (db *DB) ListBackups(ctx context.Context, targetType, targetID string, limit int) ([]Backup, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT id, target_type, target_id, status, kind, location, size_bytes,
		error_message, created_at, finished_at FROM backups
		WHERE target_type = ? AND target_id = ? ORDER BY created_at DESC LIMIT ?`, targetType, targetID, limit)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	defer rows.Close()
	return scanBackups(rows)
}

// ExpiredBackups lists successful backups beyond the retention count, which the
// retention job then deletes from object storage.
func (db *DB) ExpiredBackups(ctx context.Context, targetType, targetID string, keep int) ([]Backup, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, target_type, target_id, status, kind, location, size_bytes,
		error_message, created_at, finished_at FROM backups
		WHERE target_type = ? AND target_id = ? AND status = 'succeeded'
		ORDER BY created_at DESC LIMIT -1 OFFSET ?`, targetType, targetID, keep)
	if err != nil {
		return nil, fmt.Errorf("list expired backups: %w", err)
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		var b Backup
		var created string
		var finished sql.NullString
		if err := rows.Scan(&b.ID, &b.TargetType, &b.TargetID, &b.Status, &b.Kind, &b.Location,
			&b.SizeBytes, &b.ErrorMessage, &created, &finished); err != nil {
			return nil, fmt.Errorf("scan expired backup: %w", err)
		}
		b.CreatedAt, _ = ParseTime(created)
		b.FinishedAt = scanTime(finished)
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBackup removes a backup record once its object is gone.
func (db *DB) DeleteBackup(ctx context.Context, id string) error {
	_, err := db.Exec(ctx, `DELETE FROM backups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	return nil
}
