package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"
)

// Drift statuses: how an app's objects in the cluster compare with what the
// panel applies.
const (
	// DriftInSync is every object as the panel applied it.
	DriftInSync = "in_sync"
	// DriftDrifted is an object that somebody changed.
	DriftDrifted = "drifted"
	// DriftMissing is an object that somebody deleted.
	DriftMissing = "missing"
	// DriftNotDeployed is an app with nothing deployed to compare.
	DriftNotDeployed = "not_deployed"
	// DriftApplying is an app the panel is changing right now — a deploy,
	// a rollback, a sync — which is compared once that has finished.
	DriftApplying = "applying"
)

// AppDrift is what the watcher last found for one app, and what to do about
// it. Items is the list it found, as the JSON the API answers with.
type AppDrift struct {
	AppID       string
	Status      string
	Items       string
	Fingerprint string
	// Notified is the fingerprint somebody was last told about, so the same
	// drift is announced once however many passes find it.
	Notified   string
	Since      time.Time
	CheckedAt  time.Time
	AutoRepair bool
	// Applied is what the last apply wrote, "Kind/name" to the fingerprint
	// each object carried, and AppliedKnown whether that was recorded at all.
	Applied      map[string]string
	AppliedKnown bool
}

// GetAppDrift returns what was last found for an app. An app never checked
// is in sync, with putting things back automatically off.
func (db *DB) GetAppDrift(ctx context.Context, appID string) (AppDrift, error) {
	d := AppDrift{AppID: appID, Status: DriftInSync, Items: "[]"}
	var since, checked, applied string
	var auto int
	err := db.QueryRowContext(ctx, `SELECT status, items, fingerprint, notified, since, checked_at, auto_repair, applied
		FROM app_drift WHERE app_id = ?`, appID).
		Scan(&d.Status, &d.Items, &d.Fingerprint, &d.Notified, &since, &checked, &auto, &applied)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, fmt.Errorf("read the drift of %s: %w", appID, err)
	}
	d.Since, _ = ParseTime(since)
	d.CheckedAt, _ = ParseTime(checked)
	d.AutoRepair = auto != 0
	if applied != "" {
		if err := json.Unmarshal([]byte(applied), &d.Applied); err != nil {
			return d, fmt.Errorf("read what was applied for %s: %w", appID, err)
		}
		d.AppliedKnown = true
	}
	return d, nil
}

// RecordApplied records the objects an apply wrote. replace is an apply of
// everything the app has, which is the whole list from then on; otherwise the
// objects are added to it — a new version's processes, applied once the app
// serves it, or the Secrets a one-off command needs.
func (db *DB) RecordApplied(ctx context.Context, appID string, objects map[string]string, replace bool) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		current := map[string]string{}
		if !replace {
			var raw string
			err := tx.QueryRowContext(ctx, `SELECT applied FROM app_drift WHERE app_id = ?`, appID).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("read what was applied for %s: %w", appID, err)
			}
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &current); err != nil {
					return fmt.Errorf("read what was applied for %s: %w", appID, err)
				}
			}
		}
		maps.Copy(current, objects)
		encoded, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("record what was applied for %s: %w", appID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_drift (app_id, applied) VALUES (?,?)
			ON CONFLICT (app_id) DO UPDATE SET applied = excluded.applied`, appID, string(encoded)); err != nil {
			return fmt.Errorf("record what was applied for %s: %w", appID, err)
		}
		return nil
	})
}

// RecordAppDrift stores what a check found. The moment an app stopped
// matching is kept for as long as it does not match, and forgotten once it
// does.
func (db *DB) RecordAppDrift(ctx context.Context, appID, status, items, fingerprint string, at time.Time) error {
	since := ""
	if status != DriftInSync && status != DriftNotDeployed {
		since = FormatTime(at)
	}
	_, err := db.Exec(ctx, `INSERT INTO app_drift (app_id, status, items, fingerprint, since, checked_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT (app_id) DO UPDATE SET
			status = excluded.status, items = excluded.items, fingerprint = excluded.fingerprint,
			checked_at = excluded.checked_at,
			since = CASE
				WHEN excluded.since = '' THEN ''
				WHEN app_drift.since = '' THEN excluded.since
				ELSE app_drift.since
			END`,
		appID, status, items, fingerprint, since, FormatTime(at))
	if err != nil {
		return fmt.Errorf("record the drift of %s: %w", appID, err)
	}
	return nil
}

// SetDriftNotified records which drift somebody was told about. Empty means
// nobody has been told about anything, so the next drift is announced.
func (db *DB) SetDriftNotified(ctx context.Context, appID, fingerprint string) error {
	_, err := db.Exec(ctx, `INSERT INTO app_drift (app_id, notified) VALUES (?,?)
		ON CONFLICT (app_id) DO UPDATE SET notified = excluded.notified`, appID, fingerprint)
	if err != nil {
		return fmt.Errorf("record the drift notification of %s: %w", appID, err)
	}
	return nil
}

// SetDriftAutoRepair turns putting an app's objects back by itself on or off.
func (db *DB) SetDriftAutoRepair(ctx context.Context, appID string, on bool) error {
	value := 0
	if on {
		value = 1
	}
	_, err := db.Exec(ctx, `INSERT INTO app_drift (app_id, auto_repair) VALUES (?,?)
		ON CONFLICT (app_id) DO UPDATE SET auto_repair = excluded.auto_repair`, appID, value)
	if err != nil {
		return fmt.Errorf("store whether %s is put back automatically: %w", appID, err)
	}
	return nil
}
