package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// --- databases ---

const databaseColumns = `id, environment_id, name, slug, engine, engine_version, status, status_detail,
	instances, storage_gb, cpu_request_m, mem_request_mb, cpu_limit_m, mem_limit_mb, credentials_enc,
	credentials_next_enc, created_at, updated_at`

func scanDatabase(row interface{ Scan(...any) error }) (Database, error) {
	var d Database
	var created, updated string
	err := row.Scan(&d.ID, &d.EnvironmentID, &d.Name, &d.Slug, &d.Engine, &d.EngineVersion, &d.Status,
		&d.StatusDetail, &d.Instances, &d.StorageGB, &d.CPURequestM, &d.MemRequestMB, &d.CPULimitM,
		&d.MemLimitMB, &d.CredentialsEnc, &d.CredentialsNextEnc, &created, &updated)
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
		 storage_gb, cpu_request_m, mem_request_mb, cpu_limit_m, mem_limit_mb, credentials_enc, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.EnvironmentID, d.Name, d.Slug, d.Engine, d.EngineVersion, defaultStr(d.Status, "creating"),
		d.StatusDetail, max(d.Instances, 1), max(d.StorageGB, 1), d.CPURequestM, d.MemRequestMB,
		d.CPULimitM, d.MemLimitMB, d.CredentialsEnc, now, now)
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
		instances=?, storage_gb=?, cpu_request_m=?, mem_request_mb=?, cpu_limit_m=?, mem_limit_mb=?,
		credentials_enc=?, updated_at=? WHERE id=?`,
		d.Name, d.EngineVersion, d.Status, d.StatusDetail, d.Instances, d.StorageGB, d.CPURequestM,
		d.MemRequestMB, d.CPULimitM, d.MemLimitMB, d.CredentialsEnc, now, d.ID)
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

// SetDatabaseResources records what a database reserves, may use and has on
// its disk, and nothing else of its row.
//
// Not UpdateDatabase, which writes every mutable field from the copy it is
// given: a resize reads the row, spends a while with the cluster, and would
// then write back the credentials it read — over a password change that
// finished in the meantime.
func (db *DB) SetDatabaseResources(ctx context.Context, id string, cpuRequestM, cpuLimitM, memRequestMB, memLimitMB, storageGB int) error {
	res, err := db.Exec(ctx, `UPDATE databases SET cpu_request_m = ?, cpu_limit_m = ?, mem_request_mb = ?,
		mem_limit_mb = ?, storage_gb = ?, updated_at = ? WHERE id = ?`,
		cpuRequestM, cpuLimitM, memRequestMB, memLimitMB, storageGB, Now(), id)
	if err != nil {
		return fmt.Errorf("set database resources: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// BeginDatabaseCredentials records the credentials a password change is about
// to give a database, sealed, before the database is asked to take them.
//
// Only one change at a time: a database that already has credentials waiting
// answers ErrConflict, so the column that keeps the new password safe is also
// what stops a second change from starting over the first.
func (db *DB) BeginDatabaseCredentials(ctx context.Context, id, sealed string) error {
	res, err := db.Exec(ctx, `UPDATE databases SET credentials_next_enc = ?, updated_at = ?
		WHERE id = ? AND credentials_next_enc = ''`, sealed, Now(), id)
	if err != nil {
		return fmt.Errorf("begin a password change: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := db.GetDatabase(ctx, id); err != nil {
			return err
		}
		return fmt.Errorf("%w: a password change is already under way", ErrConflict)
	}
	return nil
}

// CommitDatabaseCredentials makes the new credentials the database's own and
// forgets the waiting copy, in one statement. sealed is the same credentials
// sealed for credentials_enc: each column is sealed to its own context, so a
// value cannot simply move from one to the other.
func (db *DB) CommitDatabaseCredentials(ctx context.Context, id, sealed string) error {
	res, err := db.Exec(ctx, `UPDATE databases SET credentials_enc = ?, credentials_next_enc = '',
		updated_at = ? WHERE id = ?`, sealed, Now(), id)
	if err != nil {
		return fmt.Errorf("commit the new credentials: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AbandonDatabaseCredentials forgets credentials a password change did not
// give the database after all.
func (db *DB) AbandonDatabaseCredentials(ctx context.Context, id string) error {
	if _, err := db.Exec(ctx, `UPDATE databases SET credentials_next_enc = '', updated_at = ? WHERE id = ?`,
		Now(), id); err != nil {
		return fmt.Errorf("abandon the new credentials: %w", err)
	}
	return nil
}

// ListDatabasesChangingPassword finds databases a restart caught in the middle
// of a password change.
func (db *DB) ListDatabasesChangingPassword(ctx context.Context) ([]Database, error) {
	return db.queryDatabases(ctx, `SELECT `+databaseColumns+` FROM databases
		WHERE credentials_next_enc != '' ORDER BY created_at`)
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
		(id, target_type, target_id, status, kind, location, size_bytes, error_message, encrypted, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.TargetType, b.TargetID, defaultStr(b.Status, "running"), defaultStr(b.Kind, "scheduled"),
		b.Location, b.SizeBytes, b.ErrorMessage, b.Encrypted, now)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	b.CreatedAt, _ = ParseTime(now)
	return nil
}

// RecordSkippedBackup notes that a scheduled backup did not run, and why:
// the database was stopped, so there was nothing to copy. Only the newest
// skip is kept for a target, because a database stopped for a month would
// otherwise fill its list with thirty rows saying the same thing.
func (db *DB) RecordSkippedBackup(ctx context.Context, b *Backup) error {
	if b.ID == "" {
		b.ID = NewID("bak")
	}
	now := Now()
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM backups WHERE target_type = ? AND target_id = ?
			AND status = 'skipped'`, b.TargetType, b.TargetID); err != nil {
			return fmt.Errorf("forget the earlier skipped backup: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backups
			(id, target_type, target_id, status, kind, location, size_bytes, error_message, encrypted,
			 created_at, finished_at)
			VALUES (?,?,?,'skipped',?,'',0,?,0,?,?)`,
			b.ID, b.TargetType, b.TargetID, defaultStr(b.Kind, "scheduled"), b.ErrorMessage, now, now); err != nil {
			return fmt.Errorf("record a skipped backup: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	b.Status = "skipped"
	b.CreatedAt, _ = ParseTime(now)
	b.FinishedAt = b.CreatedAt
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

// backupColumns is what every query reading backups selects, in the order
// scanBackup reads it.
const backupColumns = `id, target_type, target_id, status, kind, location, size_bytes,
		error_message, created_at, finished_at, encrypted, verified_at, verify_error`

func scanBackup(row interface{ Scan(...any) error }) (Backup, error) {
	var b Backup
	var created string
	var finished, verified sql.NullString
	if err := row.Scan(&b.ID, &b.TargetType, &b.TargetID, &b.Status, &b.Kind, &b.Location,
		&b.SizeBytes, &b.ErrorMessage, &created, &finished, &b.Encrypted, &verified, &b.VerifyError); err != nil {
		return b, err
	}
	b.CreatedAt, _ = ParseTime(created)
	b.FinishedAt = scanTime(finished)
	b.VerifiedAt = scanTime(verified)
	return b, nil
}

func scanBackups(rows *sql.Rows) ([]Backup, error) {
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, fmt.Errorf("scan backup: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RecordVerification stores the outcome of opening a backup and reading it
// through: when it worked, or why it did not.
func (db *DB) RecordVerification(ctx context.Context, id, verifyError string) error {
	if _, err := db.Exec(ctx, `UPDATE backups SET verified_at = ?, verify_error = ? WHERE id = ?`,
		Now(), verifyError, id); err != nil {
		return fmt.Errorf("record the verification of %s: %w", id, err)
	}
	return nil
}

// ListUnfinishedBackups finds backups interrupted by a panel restart.
//
// A backup row is written as "running" and finished by the goroutine taking it.
// If the panel stops in between, nothing finishes it: the row says a backup is
// in progress forever, and the panel is a Deployment that restarts for an
// upgrade or a drained node like anything else.
func (db *DB) ListUnfinishedBackups(ctx context.Context) ([]Backup, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups WHERE status = 'running' ORDER BY created_at`)
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
	b, err := scanBackup(db.QueryRowContext(ctx, `SELECT `+backupColumns+` FROM backups WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return b, ErrNotFound
		}
		return b, fmt.Errorf("get backup: %w", err)
	}
	return b, nil
}

// ListBackups returns a target's backups, newest first.
func (db *DB) ListBackups(ctx context.Context, targetType, targetID string, limit int) ([]Backup, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups
		WHERE target_type = ? AND target_id = ? ORDER BY created_at DESC LIMIT ?`, targetType, targetID, limit)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	defer rows.Close()
	return scanBackups(rows)
}

// ExpiredBackups lists successful backups beyond the retention count, which the
// retention job then deletes from object storage.
//
// Scheduled backups and the rest — taken by hand, or before an update or an
// upgrade — are counted apart. Counted together, taking a few by hand in a
// row deleted the scheduled copy from before whatever went wrong, which is
// the one somebody was about to need.
func (db *DB) ExpiredBackups(ctx context.Context, targetType, targetID string, keep int, scheduled bool) ([]Backup, error) {
	kind := `kind = 'scheduled'`
	if !scheduled {
		kind = `kind != 'scheduled'`
	}
	rows, err := db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups
		WHERE target_type = ? AND target_id = ? AND status = 'succeeded' AND `+kind+`
		ORDER BY created_at DESC LIMIT -1 OFFSET ?`, targetType, targetID, keep)
	if err != nil {
		return nil, fmt.Errorf("list expired backups: %w", err)
	}
	defer rows.Close()
	return scanBackups(rows)
}

// BackupObjectShared reports whether a backup other than id points at the
// same object. Backups taken before each had a key of its own could share one,
// and deleting it for one of them deleted it for both.
func (db *DB) BackupObjectShared(ctx context.Context, id, location string) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups WHERE location = ? AND id != ?`, location, id).Scan(&n); err != nil {
		return false, fmt.Errorf("check whether a backup's object is shared: %w", err)
	}
	return n > 0, nil
}

// DeleteBackup removes a backup record once its object is gone.
func (db *DB) DeleteBackup(ctx context.Context, id string) error {
	_, err := db.Exec(ctx, `DELETE FROM backups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	return nil
}
