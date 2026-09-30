package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppSample is what an app used in one minute.
type AppSample struct {
	At       time.Time `json:"at"`
	CPUM     int64     `json:"cpu_m"`
	MemoryMB int64     `json:"memory_mb"`
	// CPUPeakPct and MemoryPeakPct are the busiest instance as a share of its
	// own limit, which is what gets an instance throttled or killed.
	CPUPeakPct    int `json:"cpu_peak_pct"`
	MemoryPeakPct int `json:"memory_peak_pct"`
	Ready         int `json:"ready"`
	Desired       int `json:"desired"`
	// Restarts is the total across the app's instances, as Kubernetes counts
	// them: it only goes up, until an instance is replaced.
	Restarts int `json:"restarts"`
}

// RecordAppSample keeps one minute of an app's usage.
func (db *DB) RecordAppSample(ctx context.Context, appID string, s AppSample) error {
	_, err := db.Exec(ctx, `INSERT INTO app_samples
		(app_id, at, cpu_m, memory_mb, cpu_peak_pct, memory_peak_pct, ready, desired, restarts)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (app_id, at) DO UPDATE SET cpu_m = excluded.cpu_m, memory_mb = excluded.memory_mb,
			cpu_peak_pct = excluded.cpu_peak_pct, memory_peak_pct = excluded.memory_peak_pct,
			ready = excluded.ready, desired = excluded.desired, restarts = excluded.restarts`,
		appID, FormatTime(s.At.UTC().Truncate(time.Minute)), s.CPUM, s.MemoryMB, s.CPUPeakPct, s.MemoryPeakPct,
		s.Ready, s.Desired, s.Restarts)
	if err != nil {
		return fmt.Errorf("record an app sample: %w", err)
	}
	return nil
}

// AppSamples returns an app's samples since a time, oldest first.
func (db *DB) AppSamples(ctx context.Context, appID string, since time.Time) ([]AppSample, error) {
	rows, err := db.QueryContext(ctx, `SELECT at, cpu_m, memory_mb, cpu_peak_pct, memory_peak_pct, ready, desired, restarts
		FROM app_samples WHERE app_id = ? AND at >= ? ORDER BY at`, appID, FormatTime(since.UTC()))
	if err != nil {
		return nil, fmt.Errorf("read an app's samples: %w", err)
	}
	defer rows.Close()
	out := []AppSample{}
	for rows.Next() {
		var s AppSample
		var at string
		if err := rows.Scan(&at, &s.CPUM, &s.MemoryMB, &s.CPUPeakPct, &s.MemoryPeakPct, &s.Ready, &s.Desired, &s.Restarts); err != nil {
			return nil, fmt.Errorf("scan an app sample: %w", err)
		}
		s.At, _ = ParseTime(at)
		out = append(out, s)
	}
	return out, rows.Err()
}

// PruneAppSamples forgets samples older than a time.
func (db *DB) PruneAppSamples(ctx context.Context, before time.Time) error {
	if _, err := db.Exec(ctx, `DELETE FROM app_samples WHERE at < ?`, FormatTime(before.UTC())); err != nil {
		return fmt.Errorf("prune app samples: %w", err)
	}
	return nil
}

// AppAlerts are the thresholds an app is watched against. Zero is off.
type AppAlerts struct {
	// MemoryPct fires when the busiest instance has used this share of its
	// memory limit for three minutes running.
	MemoryPct int `json:"memory_pct"`
	// CPUPct is the same for its CPU limit.
	CPUPct int `json:"cpu_pct"`
	// Restarts fires when the app's instances restart this many times in ten
	// minutes.
	Restarts int `json:"restarts"`
	// Firing is which of them are going off now.
	Firing []string `json:"firing"`
}

// DefaultAppAlerts is what an app is watched against until somebody says.
// CPU is off: a busy app is using what it was given, and throttling is not
// an outage the way running out of memory is.
func DefaultAppAlerts() AppAlerts {
	return AppAlerts{MemoryPct: 90, Restarts: 3, Firing: []string{}}
}

// GetAppAlerts returns an app's thresholds, or the defaults.
func (db *DB) GetAppAlerts(ctx context.Context, appID string) (AppAlerts, error) {
	a := DefaultAppAlerts()
	var firing string
	err := db.QueryRowContext(ctx, `SELECT memory_pct, cpu_pct, restarts, firing FROM app_alerts WHERE app_id = ?`,
		appID).Scan(&a.MemoryPct, &a.CPUPct, &a.Restarts, &firing)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, fmt.Errorf("read an app's alerts: %w", err)
	}
	if firing != "" {
		a.Firing = strings.Split(firing, ",")
	}
	return a, nil
}

// SetAppAlerts stores an app's thresholds, keeping what is firing.
func (db *DB) SetAppAlerts(ctx context.Context, appID string, a AppAlerts) error {
	_, err := db.Exec(ctx, `INSERT INTO app_alerts (app_id, memory_pct, cpu_pct, restarts) VALUES (?,?,?,?)
		ON CONFLICT (app_id) DO UPDATE SET memory_pct = excluded.memory_pct, cpu_pct = excluded.cpu_pct,
			restarts = excluded.restarts`, appID, a.MemoryPct, a.CPUPct, a.Restarts)
	if err != nil {
		return fmt.Errorf("store an app's alerts: %w", err)
	}
	return nil
}

// SetAlertsFiring records which of an app's alerts are going off.
func (db *DB) SetAlertsFiring(ctx context.Context, appID string, firing []string) error {
	defaults := DefaultAppAlerts()
	_, err := db.Exec(ctx, `INSERT INTO app_alerts (app_id, memory_pct, cpu_pct, restarts, firing) VALUES (?,?,?,?,?)
		ON CONFLICT (app_id) DO UPDATE SET firing = excluded.firing`,
		appID, defaults.MemoryPct, defaults.CPUPct, defaults.Restarts, strings.Join(firing, ","))
	if err != nil {
		return fmt.Errorf("record firing alerts: %w", err)
	}
	return nil
}
