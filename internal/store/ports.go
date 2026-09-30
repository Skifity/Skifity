package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppPort is a port an app takes connections on from outside the cluster,
// other than HTTP, which goes through its domains.
type AppPort struct {
	ID    string `json:"id"`
	AppID string `json:"app_id"`
	// Port is the one the container listens on.
	Port int `json:"port"`
	// Protocol is tcp or udp.
	Protocol string `json:"protocol"`
	// PublicPort is the one opened on every server.
	PublicPort int       `json:"public_port"`
	CreatedAt  time.Time `json:"created_at"`
}

// ErrPortTaken is a public port and protocol another app already has.
var ErrPortTaken = errors.New("that public port is taken")

// AddPort opens one of an app's ports.
func (db *DB) AddPort(ctx context.Context, p *AppPort) error {
	if p.ID == "" {
		p.ID = NewID("port")
	}
	now := Now()
	if _, err := db.Exec(ctx, `INSERT INTO app_ports (id, app_id, port, protocol, public_port, created_at)
		VALUES (?,?,?,?,?,?)`, p.ID, p.AppID, p.Port, p.Protocol, p.PublicPort, now); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrPortTaken
		}
		return fmt.Errorf("open a port: %w", err)
	}
	p.CreatedAt, _ = ParseTime(now)
	return nil
}

// ListPorts returns an app's ports.
func (db *DB) ListPorts(ctx context.Context, appID string) ([]AppPort, error) {
	return db.queryPorts(ctx, `WHERE app_id = ? ORDER BY public_port, protocol`, appID)
}

// PortHolder returns the port holding a public port and protocol, if any.
func (db *DB) PortHolder(ctx context.Context, publicPort int, protocol string) (AppPort, error) {
	ports, err := db.queryPorts(ctx, `WHERE public_port = ? AND protocol = ?`, publicPort, protocol)
	if err != nil {
		return AppPort{}, err
	}
	if len(ports) == 0 {
		return AppPort{}, ErrNotFound
	}
	return ports[0], nil
}

// CountPortsInEnvironment is how many public ports an environment's apps
// have, which is how many load balancers its quota allows.
func (db *DB) CountPortsInEnvironment(ctx context.Context, environmentID string) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT p.app_id) FROM app_ports p
		JOIN apps a ON a.id = p.app_id WHERE a.environment_id = ?`, environmentID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count the ports of %s: %w", environmentID, err)
	}
	return n, nil
}

// DeletePort closes one of an app's ports.
func (db *DB) DeletePort(ctx context.Context, appID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM app_ports WHERE app_id = ? AND id = ?`, appID, id)
	if err != nil {
		return fmt.Errorf("close a port: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) queryPorts(ctx context.Context, where string, args ...any) ([]AppPort, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, app_id, port, protocol, public_port, created_at
		FROM app_ports `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list ports: %w", err)
	}
	defer rows.Close()
	out := []AppPort{}
	for rows.Next() {
		var p AppPort
		var created string
		if err := rows.Scan(&p.ID, &p.AppID, &p.Port, &p.Protocol, &p.PublicPort, &created); err != nil {
			return nil, fmt.Errorf("read a port: %w", err)
		}
		p.CreatedAt, _ = ParseTime(created)
		out = append(out, p)
	}
	return out, rows.Err()
}
