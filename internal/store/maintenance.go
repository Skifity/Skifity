package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Maintenance is an app answering visitors with a page instead of itself.
type Maintenance struct {
	AppID string `json:"app_id"`
	// Message is shown to visitors as it is written, in whatever language
	// the team wrote it in.
	Message string `json:"message"`
	// Allow are addresses and ranges that still reach the app.
	Allow     []string `json:"allow"`
	StartedBy string   `json:"started_by"`
	StartedAt string   `json:"started_at"`
}

// GetMaintenance returns an app's maintenance, or ErrNotFound when it is not
// in maintenance.
func (db *DB) GetMaintenance(ctx context.Context, appID string) (Maintenance, error) {
	m := Maintenance{AppID: appID}
	var allow string
	err := db.QueryRowContext(ctx,
		`SELECT message, allow, started_by, started_at FROM app_maintenance WHERE app_id = ?`, appID).
		Scan(&m.Message, &allow, &m.StartedBy, &m.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, fmt.Errorf("read the maintenance of %s: %w", appID, err)
	}
	m.Allow = splitLines(allow)
	return m, nil
}

// StartMaintenance puts an app into maintenance, or changes the message and
// addresses of one already in it. It keeps who started it and when.
func (db *DB) StartMaintenance(ctx context.Context, m *Maintenance) error {
	if existing, err := db.GetMaintenance(ctx, m.AppID); err == nil {
		m.StartedBy, m.StartedAt = existing.StartedBy, existing.StartedAt
	} else {
		m.StartedAt = Now()
	}
	_, err := db.Exec(ctx, `
		INSERT INTO app_maintenance (app_id, message, allow, started_by, started_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(app_id) DO UPDATE SET message = excluded.message, allow = excluded.allow`,
		m.AppID, m.Message, strings.Join(m.Allow, "\n"), m.StartedBy, m.StartedAt)
	if err != nil {
		return fmt.Errorf("start the maintenance of %s: %w", m.AppID, err)
	}
	return nil
}

// EndMaintenance takes an app out of maintenance. Ending one that was not
// started is not an error.
func (db *DB) EndMaintenance(ctx context.Context, appID string) error {
	if _, err := db.Exec(ctx, `DELETE FROM app_maintenance WHERE app_id = ?`, appID); err != nil {
		return fmt.Errorf("end the maintenance of %s: %w", appID, err)
	}
	return nil
}

// AppInMaintenance reports whether an app's Ingress needs the guard in front
// of it for maintenance.
func (db *DB) AppInMaintenance(ctx context.Context, appID string) (bool, error) {
	_, err := db.GetMaintenance(ctx, appID)
	switch {
	case errors.Is(err, ErrNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

// MaintenanceHostname is one hostname the guard answers with the page.
type MaintenanceHostname struct {
	Hostname string
	Maintenance
}

// MaintenanceHostnames lists every hostname of every app in maintenance.
func (db *DB) MaintenanceHostnames(ctx context.Context) ([]MaintenanceHostname, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT d.hostname, m.app_id, m.message, m.allow, m.started_by, m.started_at
		FROM app_maintenance m
		JOIN domains d ON d.app_id = m.app_id
		ORDER BY d.hostname`)
	if err != nil {
		return nil, fmt.Errorf("list the hostnames in maintenance: %w", err)
	}
	defer rows.Close()

	var out []MaintenanceHostname
	for rows.Next() {
		var h MaintenanceHostname
		var allow string
		if err := rows.Scan(&h.Hostname, &h.AppID, &h.Message, &allow, &h.StartedBy, &h.StartedAt); err != nil {
			return nil, fmt.Errorf("scan a hostname in maintenance: %w", err)
		}
		h.Allow = splitLines(allow)
		out = append(out, h)
	}
	return out, rows.Err()
}

func splitLines(text string) []string {
	out := []string{}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
