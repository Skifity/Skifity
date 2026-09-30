package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DeployLock stops an app's deploys and rollbacks until it is lifted.
type DeployLock struct {
	AppID  string `json:"app_id"`
	Reason string `json:"reason"`
	// LockedBy is who locked it, by address, so the person to ask is named.
	LockedBy string    `json:"locked_by"`
	LockedAt time.Time `json:"locked_at"`
}

// LockDeploys locks an app's deploys, or changes the reason of a lock that is
// already there.
func (db *DB) LockDeploys(ctx context.Context, lock *DeployLock) error {
	now := Now()
	if _, err := db.Exec(ctx, `INSERT INTO app_deploy_locks (app_id, reason, locked_by, locked_at) VALUES (?,?,?,?)
		ON CONFLICT (app_id) DO UPDATE SET reason = excluded.reason, locked_by = excluded.locked_by, locked_at = excluded.locked_at`,
		lock.AppID, lock.Reason, lock.LockedBy, now); err != nil {
		return fmt.Errorf("lock deploys: %w", err)
	}
	lock.LockedAt, _ = ParseTime(now)
	return nil
}

// UnlockDeploys lifts a lock. Lifting one that is not there is not an error.
func (db *DB) UnlockDeploys(ctx context.Context, appID string) error {
	if _, err := db.Exec(ctx, `DELETE FROM app_deploy_locks WHERE app_id = ?`, appID); err != nil {
		return fmt.Errorf("unlock deploys: %w", err)
	}
	return nil
}

// GetDeployLock returns an app's lock, or ErrNotFound when it has none.
func (db *DB) GetDeployLock(ctx context.Context, appID string) (DeployLock, error) {
	var lock DeployLock
	var at string
	err := db.QueryRowContext(ctx, `SELECT app_id, reason, locked_by, locked_at FROM app_deploy_locks WHERE app_id = ?`,
		appID).Scan(&lock.AppID, &lock.Reason, &lock.LockedBy, &at)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return lock, ErrNotFound
		}
		return lock, fmt.Errorf("read the deploy lock: %w", err)
	}
	lock.LockedAt, _ = ParseTime(at)
	return lock, nil
}
