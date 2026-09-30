package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const deploymentColumns = `id, app_id, number, status, trigger, rollback_of, commit_sha, commit_message,
	commit_author, image, build_fingerprint, runtime_spec, error_code, error_message, error_hint, created_by,
	created_at, started_at, finished_at`

func scanDeployment(row interface{ Scan(...any) error }) (Deployment, error) {
	var d Deployment
	var created string
	var started, finished sql.NullString
	err := row.Scan(&d.ID, &d.AppID, &d.Number, &d.Status, &d.Trigger, &d.RollbackOf, &d.CommitSHA,
		&d.CommitMessage, &d.CommitAuthor, &d.Image, &d.BuildFingerprint, &d.RuntimeSpec, &d.ErrorCode,
		&d.ErrorMessage, &d.ErrorHint, &d.CreatedBy, &created, &started, &finished)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return d, ErrNotFound
		}
		return d, fmt.Errorf("scan deployment: %w", err)
	}
	d.CreatedAt, _ = ParseTime(created)
	d.StartedAt = scanTime(started)
	d.FinishedAt = scanTime(finished)
	return d, nil
}

// CreateDeployment inserts a deployment, assigning the next number for the app.
// The number is allocated inside the transaction so two concurrent deploys cannot
// claim the same one.
func (db *DB) CreateDeployment(ctx context.Context, d *Deployment) error {
	if d.ID == "" {
		d.ID = NewID("dep")
	}
	if d.Status == "" {
		d.Status = DeployQueued
	}
	if d.RuntimeSpec == "" {
		d.RuntimeSpec = "{}"
	}
	now := Now()
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		var next sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(number) FROM deployments WHERE app_id = ?`, d.AppID).Scan(&next); err != nil {
			return fmt.Errorf("find next deployment number: %w", err)
		}
		d.Number = int(next.Int64) + 1
		_, err := tx.ExecContext(ctx, `INSERT INTO deployments
			(id, app_id, number, status, trigger, rollback_of, commit_sha, commit_message, commit_author,
			 image, build_fingerprint, runtime_spec, created_by, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, d.AppID, d.Number, d.Status, defaultStr(d.Trigger, "manual"), d.RollbackOf, d.CommitSHA,
			d.CommitMessage, d.CommitAuthor, d.Image, d.BuildFingerprint, d.RuntimeSpec, d.CreatedBy, now)
		if err != nil {
			return fmt.Errorf("insert deployment: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.CreatedAt, _ = ParseTime(now)
	return nil
}

// GetDeployment looks a deployment up by id.
func (db *DB) GetDeployment(ctx context.Context, id string) (Deployment, error) {
	return scanDeployment(db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id = ?`, id))
}

// ListDeployments returns an app's deploy history, newest first.
func (db *DB) ListDeployments(ctx context.Context, appID string, limit int) ([]Deployment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT `+deploymentColumns+` FROM deployments
		WHERE app_id = ? ORDER BY number DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LatestSuccessfulDeployment is what rollback and "current version" read.
func (db *DB) LatestSuccessfulDeployment(ctx context.Context, appID string) (Deployment, error) {
	return scanDeployment(db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments
		WHERE app_id = ? AND status = 'succeeded' ORDER BY number DESC LIMIT 1`, appID))
}

// LatestDeployment returns the most recent deployment whatever its state.
func (db *DB) LatestDeployment(ctx context.Context, appID string) (Deployment, error) {
	return scanDeployment(db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments
		WHERE app_id = ? ORDER BY number DESC LIMIT 1`, appID))
}

// FindDeploymentByFingerprint finds a successful build with the same inputs, so a
// redeploy can reuse its image instead of building again (ADR-0007).
func (db *DB) FindDeploymentByFingerprint(ctx context.Context, appID, fingerprint string) (Deployment, error) {
	if fingerprint == "" {
		return Deployment{}, ErrNotFound
	}
	return scanDeployment(db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments
		WHERE app_id = ? AND build_fingerprint = ? AND status = 'succeeded' AND image != ''
		ORDER BY number DESC LIMIT 1`, appID, fingerprint))
}

// UpdateDeploymentStatus moves a deployment forward and records failure details.
//
// A deployment that has already finished never moves again. The guard is in the
// statement rather than in the caller because the caller is a goroutine that
// may be several seconds behind the world: a build superseded by a newer deploy
// is still running when it writes its next status, and without this it would
// put itself back to "building" and carry on as though it had not been
// replaced.
func (db *DB) UpdateDeploymentStatus(ctx context.Context, id string, status DeploymentStatus, errCode, errMsg, hint string) error {
	var started, finished any
	switch status {
	case DeployBuilding, DeployDeploying:
		started = Now()
	}
	if status.Terminal() {
		finished = Now()
	}
	_, err := db.Exec(ctx, `UPDATE deployments SET status = ?, error_code = ?, error_message = ?, error_hint = ?,
		started_at = COALESCE(started_at, ?), finished_at = COALESCE(?, finished_at)
		WHERE id = ? AND status IN ('queued','building','deploying')`,
		status, errCode, errMsg, hint, started, finished, id)
	if err != nil {
		return fmt.Errorf("update deployment status: %w", err)
	}
	return nil
}

// SetDeploymentImage records the image a build produced.
func (db *DB) SetDeploymentImage(ctx context.Context, id, image string) error {
	_, err := db.Exec(ctx, `UPDATE deployments SET image = ? WHERE id = ?`, image, id)
	if err != nil {
		return fmt.Errorf("set deployment image: %w", err)
	}
	return nil
}

// SupersedeRunningDeployments marks older in-flight deploys as superseded when a
// newer one starts, and returns which ones it marked.
//
// The ids matter: marking a row does not stop the goroutine that was building
// it, and two builds finishing in an unpredictable order is the thing this is
// supposed to prevent. The caller cancels them.
func (db *DB) SupersedeRunningDeployments(ctx context.Context, appID, exceptID string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM deployments WHERE app_id = ? AND id != ? AND status IN ('queued','building','deploying')`,
		appID, exceptID)
	if err != nil {
		return nil, fmt.Errorf("find deployments to supersede: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan deployment to supersede: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find deployments to supersede: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	if _, err := db.Exec(ctx, `UPDATE deployments SET status = 'superseded', finished_at = ?
		WHERE app_id = ? AND id != ? AND status IN ('queued','building','deploying')`,
		Now(), appID, exceptID); err != nil {
		return nil, fmt.Errorf("supersede deployments: %w", err)
	}
	return ids, nil
}

// ListUnfinishedDeployments finds deploys interrupted by a panel restart.
func (db *DB) ListUnfinishedDeployments(ctx context.Context) ([]Deployment, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+deploymentColumns+` FROM deployments
		WHERE status IN ('queued','building','deploying') ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list unfinished deployments: %w", err)
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- build logs ---

// AppendBuildLog stores one line of build output. Lines are numbered so the UI
// can resume a stream after a reconnect without duplicating or losing lines.
func (db *DB) AppendBuildLog(ctx context.Context, deploymentID, stream, line string) (int, error) {
	var seq int
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		var next sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(seq) FROM build_logs WHERE deployment_id = ?`, deploymentID).Scan(&next); err != nil {
			return fmt.Errorf("find next log sequence: %w", err)
		}
		seq = int(next.Int64) + 1
		_, err := tx.ExecContext(ctx, `INSERT INTO build_logs (id, deployment_id, seq, stream, line, at)
			VALUES (?,?,?,?,?,?)`, NewID("log"), deploymentID, seq, stream, line, Now())
		if err != nil {
			return fmt.Errorf("insert build log: %w", err)
		}
		return nil
	})
	return seq, err
}

// AppendBuildLogs stores a batch of lines in one transaction, which matters when
// a build emits thousands of them.
func (db *DB) AppendBuildLogs(ctx context.Context, deploymentID string, lines []LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	return db.Tx(ctx, func(tx *sql.Tx) error {
		var next sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(seq) FROM build_logs WHERE deployment_id = ?`, deploymentID).Scan(&next); err != nil {
			return fmt.Errorf("find next log sequence: %w", err)
		}
		seq := int(next.Int64)
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO build_logs (id, deployment_id, seq, stream, line, at) VALUES (?,?,?,?,?,?)`)
		if err != nil {
			return fmt.Errorf("prepare build log insert: %w", err)
		}
		defer stmt.Close()
		for _, l := range lines {
			seq++
			at := l.At
			if at.IsZero() {
				at = time.Now()
			}
			if _, err := stmt.ExecContext(ctx, NewID("log"), deploymentID, seq,
				defaultStr(l.Stream, "stdout"), l.Line, FormatTime(at)); err != nil {
				return fmt.Errorf("insert build log: %w", err)
			}
		}
		return nil
	})
}

// ListBuildLogs returns the lines after sinceSeq, which is how a reconnecting SSE
// client catches up.
func (db *DB) ListBuildLogs(ctx context.Context, deploymentID string, sinceSeq, limit int) ([]LogLine, error) {
	if limit <= 0 || limit > 5000 {
		limit = 2000
	}
	rows, err := db.QueryContext(ctx, `SELECT seq, stream, line, at FROM build_logs
		WHERE deployment_id = ? AND seq > ? ORDER BY seq LIMIT ?`, deploymentID, sinceSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list build logs: %w", err)
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		var at string
		if err := rows.Scan(&l.Seq, &l.Stream, &l.Line, &at); err != nil {
			return nil, fmt.Errorf("scan build log: %w", err)
		}
		l.At, _ = ParseTime(at)
		out = append(out, l)
	}
	return out, rows.Err()
}

// PruneBuildLogs keeps only the newest deployments' logs for an app, because
// build output is by far the largest thing the panel stores.
func (db *DB) PruneBuildLogs(ctx context.Context, appID string, keepDeployments int) error {
	_, err := db.Exec(ctx, `DELETE FROM build_logs WHERE deployment_id IN (
		SELECT id FROM deployments WHERE app_id = ? ORDER BY number DESC LIMIT -1 OFFSET ?)`,
		appID, keepDeployments)
	if err != nil {
		return fmt.Errorf("prune build logs: %w", err)
	}
	return nil
}

// MarkRollbackTargets fills in CanRollback across a list of an app's
// deployments, newest first.
//
// keep is how many images the registry keeps. Only a deployment that succeeded
// counts towards it, because a failed one never pushed an image, and only the
// most recent of those can still be pulled.
func MarkRollbackTargets(deployments []Deployment, keep int) {
	if keep < 1 {
		keep = 1
	}
	seen := 0
	for i := range deployments {
		if deployments[i].Status != DeploySucceeded || deployments[i].Image == "" {
			continue
		}
		seen++
		deployments[i].CanRollback = seen <= keep
	}
}

// WithinRollbackWindow reports whether a deployment is one of the most recent
// keep successful deployments of its app, which is the same question as whether
// its image still exists.
func (db *DB) WithinRollbackWindow(ctx context.Context, appID, deploymentID string, keep int) (bool, error) {
	if keep < 1 {
		keep = 1
	}
	var rank int
	err := db.QueryRowContext(ctx, `
		SELECT rn FROM (
			SELECT id, ROW_NUMBER() OVER (ORDER BY number DESC) AS rn
			FROM deployments
			WHERE app_id = ? AND status = 'succeeded' AND image <> ''
		) WHERE id = ?`, appID, deploymentID).Scan(&rank)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check whether a deployment can still be rolled back to: %w", err)
	}
	return rank <= keep, nil
}
