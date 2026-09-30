package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SetWatchedCommit records that a push to commit was checked for an app and
// skipped while it ran deploymentID. See migration 0034.
func (db *DB) SetWatchedCommit(ctx context.Context, appID, commit, deploymentID string) error {
	if _, err := db.Exec(ctx, `INSERT INTO app_watched_commits (app_id, commit_sha, deployment_id, updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT (app_id) DO UPDATE SET commit_sha = excluded.commit_sha,
			deployment_id = excluded.deployment_id, updated_at = excluded.updated_at`,
		appID, commit, deploymentID, Now()); err != nil {
		return fmt.Errorf("record the commit app %s was last checked against: %w", appID, err)
	}
	return nil
}

// WatchedCommit is the last push an app was skipped for and the deployment it
// ran then, or empty strings when there is none.
func (db *DB) WatchedCommit(ctx context.Context, appID string) (commit, deploymentID string, err error) {
	err = db.QueryRowContext(ctx, `SELECT commit_sha, deployment_id FROM app_watched_commits WHERE app_id = ?`, appID).
		Scan(&commit, &deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("read the commit app %s was last checked against: %w", appID, err)
	}
	return commit, deploymentID, nil
}

// passedCommitMemory is how long a passed commit is remembered. Deliveries
// out of order arrive within minutes; this is generous.
const passedCommitMemory = 30 * 24 * time.Hour

// MarkCommitPassed records that a deployed push moved an app from commit to
// by, and forgets that by was ever passed: it is what runs now. See migration
// 0037.
func (db *DB) MarkCommitPassed(ctx context.Context, appID, commit, by string) error {
	if _, err := db.Exec(ctx, `INSERT INTO app_passed_commits (app_id, commit_sha, passed_by, created_at)
		VALUES (?,?,?,?) ON CONFLICT (app_id, commit_sha) DO UPDATE SET passed_by = excluded.passed_by,
			created_at = excluded.created_at`, appID, commit, by, Now()); err != nil {
		return fmt.Errorf("record the commit %s moved past: %w", appID, err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM app_passed_commits WHERE app_id = ? AND (commit_sha = ? OR created_at < ?)`,
		appID, by, FormatTime(time.Now().Add(-passedCommitMemory))); err != nil {
		return fmt.Errorf("forget the commits %s moved past: %w", appID, err)
	}
	return nil
}

// CommitPassed reports whether a deployed push has moved an app past commit.
func (db *DB) CommitPassed(ctx context.Context, appID, commit string) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_passed_commits WHERE app_id = ? AND commit_sha = ?`,
		appID, commit).Scan(&n); err != nil {
		return false, fmt.Errorf("read the commits %s moved past: %w", appID, err)
	}
	return n > 0, nil
}
