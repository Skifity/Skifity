package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ServerSample is what a server used in one minute.
type ServerSample struct {
	At               time.Time `json:"at"`
	CPUM             int64     `json:"cpu_m"`
	CPUCapacityM     int64     `json:"cpu_capacity_m"`
	MemoryMB         int64     `json:"memory_mb"`
	MemoryCapacityMB int64     `json:"memory_capacity_mb"`
	// DiskUsedMB is capacity minus what is available: what the kubelet
	// measures its eviction threshold against. Zero capacity means the disk
	// could not be read that minute.
	DiskUsedMB     int64 `json:"disk_used_mb"`
	DiskCapacityMB int64 `json:"disk_capacity_mb"`
	Pods           int   `json:"pods"`
}

// Percent is a share of a capacity, and zero when the capacity is not known.
func Percent(used, capacity int64) int {
	if capacity <= 0 {
		return 0
	}
	return int(used * 100 / capacity)
}

// RecordServerSample keeps one minute of a server's usage.
func (db *DB) RecordServerSample(ctx context.Context, serverID string, s ServerSample) error {
	_, err := db.Exec(ctx, `INSERT INTO server_samples
		(server_id, at, cpu_m, cpu_capacity_m, memory_mb, memory_capacity_mb, disk_used_mb, disk_capacity_mb, pods)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (server_id, at) DO UPDATE SET cpu_m = excluded.cpu_m, cpu_capacity_m = excluded.cpu_capacity_m,
			memory_mb = excluded.memory_mb, memory_capacity_mb = excluded.memory_capacity_mb,
			disk_used_mb = excluded.disk_used_mb, disk_capacity_mb = excluded.disk_capacity_mb, pods = excluded.pods`,
		serverID, FormatTime(s.At.UTC().Truncate(time.Minute)), s.CPUM, s.CPUCapacityM, s.MemoryMB,
		s.MemoryCapacityMB, s.DiskUsedMB, s.DiskCapacityMB, s.Pods)
	if err != nil {
		return fmt.Errorf("record a server sample: %w", err)
	}
	return nil
}

// ServerSamples returns a server's samples since a time, oldest first.
func (db *DB) ServerSamples(ctx context.Context, serverID string, since time.Time) ([]ServerSample, error) {
	rows, err := db.QueryContext(ctx, `SELECT at, cpu_m, cpu_capacity_m, memory_mb, memory_capacity_mb,
		disk_used_mb, disk_capacity_mb, pods
		FROM server_samples WHERE server_id = ? AND at >= ? ORDER BY at`, serverID, FormatTime(since.UTC()))
	if err != nil {
		return nil, fmt.Errorf("read a server's samples: %w", err)
	}
	defer rows.Close()
	out := []ServerSample{}
	for rows.Next() {
		var s ServerSample
		var at string
		if err := rows.Scan(&at, &s.CPUM, &s.CPUCapacityM, &s.MemoryMB, &s.MemoryCapacityMB,
			&s.DiskUsedMB, &s.DiskCapacityMB, &s.Pods); err != nil {
			return nil, fmt.Errorf("scan a server sample: %w", err)
		}
		s.At, _ = ParseTime(at)
		out = append(out, s)
	}
	return out, rows.Err()
}

// LatestServerSample is a server's most recent sample with a disk reading, or
// ErrNotFound.
func (db *DB) LatestServerSample(ctx context.Context, serverID string) (ServerSample, error) {
	var s ServerSample
	var at string
	err := db.QueryRowContext(ctx, `SELECT at, cpu_m, cpu_capacity_m, memory_mb, memory_capacity_mb,
		disk_used_mb, disk_capacity_mb, pods
		FROM server_samples WHERE server_id = ? AND disk_capacity_mb > 0 ORDER BY at DESC LIMIT 1`, serverID).
		Scan(&at, &s.CPUM, &s.CPUCapacityM, &s.MemoryMB, &s.MemoryCapacityMB, &s.DiskUsedMB, &s.DiskCapacityMB, &s.Pods)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, fmt.Errorf("read a server's latest sample: %w", err)
	}
	s.At, _ = ParseTime(at)
	return s, nil
}

// PruneServerSamples forgets samples older than a time.
func (db *DB) PruneServerSamples(ctx context.Context, before time.Time) error {
	if _, err := db.Exec(ctx, `DELETE FROM server_samples WHERE at < ?`, FormatTime(before.UTC())); err != nil {
		return fmt.Errorf("prune server samples: %w", err)
	}
	return nil
}

// ServerAlerts are the thresholds a server is watched against. Zero is off.
type ServerAlerts struct {
	// DiskPct fires when the disk has been this full for three minutes.
	DiskPct int `json:"disk_pct"`
	// MemoryPct and CPUPct are the same for the server's memory and CPU.
	MemoryPct int `json:"memory_pct"`
	CPUPct    int `json:"cpu_pct"`
	// Firing is which of them are going off now.
	Firing []string `json:"firing"`
}

// DefaultServerAlerts is what a server is watched against until somebody
// says. The disk at 85%, five points before the kubelet's own default of
// evicting at 90; memory at 90%; CPU off, because a busy server is doing its
// job.
func DefaultServerAlerts() ServerAlerts {
	return ServerAlerts{DiskPct: 85, MemoryPct: 90, Firing: []string{}}
}

// GetServerAlerts returns a server's thresholds, or the defaults.
func (db *DB) GetServerAlerts(ctx context.Context, serverID string) (ServerAlerts, error) {
	a := DefaultServerAlerts()
	var firing string
	err := db.QueryRowContext(ctx, `SELECT disk_pct, memory_pct, cpu_pct, firing FROM server_alerts WHERE server_id = ?`,
		serverID).Scan(&a.DiskPct, &a.MemoryPct, &a.CPUPct, &firing)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, fmt.Errorf("read a server's alerts: %w", err)
	}
	if firing != "" {
		a.Firing = strings.Split(firing, ",")
	}
	return a, nil
}

// SetServerAlerts stores a server's thresholds, keeping what is firing.
func (db *DB) SetServerAlerts(ctx context.Context, serverID string, a ServerAlerts) error {
	_, err := db.Exec(ctx, `INSERT INTO server_alerts (server_id, disk_pct, memory_pct, cpu_pct) VALUES (?,?,?,?)
		ON CONFLICT (server_id) DO UPDATE SET disk_pct = excluded.disk_pct, memory_pct = excluded.memory_pct,
			cpu_pct = excluded.cpu_pct`, serverID, a.DiskPct, a.MemoryPct, a.CPUPct)
	if err != nil {
		return fmt.Errorf("store a server's alerts: %w", err)
	}
	return nil
}

// SetServerAlertsFiring records which of a server's alerts are going off.
func (db *DB) SetServerAlertsFiring(ctx context.Context, serverID string, firing []string) error {
	defaults := DefaultServerAlerts()
	_, err := db.Exec(ctx, `INSERT INTO server_alerts (server_id, disk_pct, memory_pct, cpu_pct, firing) VALUES (?,?,?,?,?)
		ON CONFLICT (server_id) DO UPDATE SET firing = excluded.firing`,
		serverID, defaults.DiskPct, defaults.MemoryPct, defaults.CPUPct, strings.Join(firing, ","))
	if err != nil {
		return fmt.Errorf("record a server's firing alerts: %w", err)
	}
	return nil
}
