package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
