package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AppTraffic is the requests that reached an app in one minute, as the
// ingress counted them. See migrations/0045_app_traffic.sql.
type AppTraffic struct {
	At       time.Time `json:"at"`
	Requests int64     `json:"requests"`
	// The requests by how they were answered. Informational (1xx) answers —
	// a WebSocket being upgraded — are in Requests and in none of these.
	Status2xx int64 `json:"status_2xx"`
	Status3xx int64 `json:"status_3xx"`
	Status4xx int64 `json:"status_4xx"`
	Status5xx int64 `json:"status_5xx"`
	// P50Ms and P95Ms are estimated from the ingress's latency histogram, in
	// milliseconds; nil when no request was timed in the minute.
	P50Ms *float64 `json:"p50_ms"`
	P95Ms *float64 `json:"p95_ms"`
	// Partial marks a minute some of whose traffic could not be counted, so
	// its numbers are a floor rather than the whole.
	Partial bool `json:"partial,omitempty"`
}

// RecordAppTraffic keeps one minute of an app's requests.
func (db *DB) RecordAppTraffic(ctx context.Context, appID string, t AppTraffic) error {
	_, err := db.Exec(ctx, `INSERT INTO app_traffic
		(app_id, at, requests, status_2xx, status_3xx, status_4xx, status_5xx, p50_ms, p95_ms, partial)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (app_id, at) DO UPDATE SET requests = excluded.requests,
			status_2xx = excluded.status_2xx, status_3xx = excluded.status_3xx,
			status_4xx = excluded.status_4xx, status_5xx = excluded.status_5xx,
			p50_ms = excluded.p50_ms, p95_ms = excluded.p95_ms, partial = excluded.partial`,
		appID, FormatTime(t.At.UTC().Truncate(time.Minute)), t.Requests,
		t.Status2xx, t.Status3xx, t.Status4xx, t.Status5xx, nullFloat(t.P50Ms), nullFloat(t.P95Ms), t.Partial)
	if err != nil {
		return fmt.Errorf("record an app's traffic: %w", err)
	}
	return nil
}

// AppTrafficSince returns an app's minutes of traffic since a time, oldest
// first.
func (db *DB) AppTrafficSince(ctx context.Context, appID string, since time.Time) ([]AppTraffic, error) {
	rows, err := db.QueryContext(ctx, `SELECT at, requests, status_2xx, status_3xx, status_4xx, status_5xx,
			p50_ms, p95_ms, partial
		FROM app_traffic WHERE app_id = ? AND at >= ? ORDER BY at`, appID, FormatTime(since.UTC()))
	if err != nil {
		return nil, fmt.Errorf("read an app's traffic: %w", err)
	}
	defer rows.Close()
	out := []AppTraffic{}
	for rows.Next() {
		var t AppTraffic
		var at string
		var p50, p95 sql.NullFloat64
		if err := rows.Scan(&at, &t.Requests, &t.Status2xx, &t.Status3xx, &t.Status4xx, &t.Status5xx,
			&p50, &p95, &t.Partial); err != nil {
			return nil, fmt.Errorf("scan an app's traffic: %w", err)
		}
		t.At, _ = ParseTime(at)
		if p50.Valid {
			t.P50Ms = &p50.Float64
		}
		if p95.Valid {
			t.P95Ms = &p95.Float64
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func nullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}
