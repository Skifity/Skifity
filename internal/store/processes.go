package store

import (
	"context"
	"fmt"
)

// AppProcess is one of an app's other processes: the same image and
// variables as the app, a command of its own, no port.
type AppProcess struct {
	AppID     string `json:"app_id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Instances int    `json:"instances"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ListProcesses returns an app's processes by name.
func (db *DB) ListProcesses(ctx context.Context, appID string) ([]AppProcess, error) {
	rows, err := db.QueryContext(ctx, `SELECT app_id, name, command, instances, created_at, updated_at
		FROM app_processes WHERE app_id = ? ORDER BY name`, appID)
	if err != nil {
		return nil, fmt.Errorf("list the processes of %s: %w", appID, err)
	}
	defer rows.Close()
	out := []AppProcess{}
	for rows.Next() {
		var p AppProcess
		if err := rows.Scan(&p.AppID, &p.Name, &p.Command, &p.Instances, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read a process of %s: %w", appID, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetProcess creates a process or changes its command and instances.
func (db *DB) SetProcess(ctx context.Context, p *AppProcess) error {
	now := Now()
	if _, err := db.Exec(ctx, `INSERT INTO app_processes (app_id, name, command, instances, created_at, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT (app_id, name) DO UPDATE SET command = excluded.command, instances = excluded.instances,
			updated_at = excluded.updated_at`,
		p.AppID, p.Name, p.Command, p.Instances, now, now); err != nil {
		return fmt.Errorf("save the process %s of %s: %w", p.Name, p.AppID, err)
	}
	return db.QueryRowContext(ctx, `SELECT created_at, updated_at FROM app_processes WHERE app_id = ? AND name = ?`,
		p.AppID, p.Name).Scan(&p.CreatedAt, &p.UpdatedAt)
}

// DeleteProcess removes a process, or answers ErrNotFound.
func (db *DB) DeleteProcess(ctx context.Context, appID, name string) error {
	result, err := db.Exec(ctx, `DELETE FROM app_processes WHERE app_id = ? AND name = ?`, appID, name)
	if err != nil {
		return fmt.Errorf("remove the process %s of %s: %w", name, appID, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
