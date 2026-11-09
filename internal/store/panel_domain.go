package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Where moving the panel to a domain of its own stands.
const (
	// PanelDomainMoving is cert-manager being installed, the route being
	// written and the certificate being waited for. The panel still answers
	// where it did.
	PanelDomainMoving = "moving"
	// PanelDomainRestarting is the certificate issued and the panel told to
	// restart at the domain. The panel that started this is about to stop;
	// the one that starts next finishes the move (see SettlePanelDomain).
	PanelDomainRestarting = "restarting"
	// PanelDomainLive is the panel answering at the domain.
	PanelDomainLive = "live"
	// PanelDomainFailed is a move that stopped, with why. The panel still
	// answers where it did before it was asked.
	PanelDomainFailed = "failed"
)

// PanelDomain is the domain the panel was put on from Settings.
type PanelDomain struct {
	Domain string `json:"domain"`
	// Previous are the hosts the panel answered on before, oldest first. The
	// route keeps answering on them, so a webhook registered at one still
	// arrives.
	Previous []string `json:"previous"`
	State    string   `json:"state"`
	// Problem is why the move stopped, as JSON, when it did.
	Problem   json.RawMessage `json:"problem,omitempty"`
	StartedBy string          `json:"started_by,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// GetPanelDomain reads the panel's domain, or ErrNotFound when it was never
// moved from Settings.
func (db *DB) GetPanelDomain(ctx context.Context) (PanelDomain, error) {
	var d PanelDomain
	var previous, problem, started, updated string
	err := db.QueryRowContext(ctx, `SELECT domain, previous, state, problem, started_by, started_at, updated_at
		FROM panel_domain WHERE id = 1`).
		Scan(&d.Domain, &previous, &d.State, &problem, &d.StartedBy, &started, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PanelDomain{}, ErrNotFound
	}
	if err != nil {
		return PanelDomain{}, fmt.Errorf("get the panel's domain: %w", err)
	}
	if err := json.Unmarshal([]byte(previous), &d.Previous); err != nil {
		return PanelDomain{}, fmt.Errorf("read the panel's earlier addresses: %w", err)
	}
	if problem != "" && json.Valid([]byte(problem)) {
		d.Problem = json.RawMessage(problem)
	}
	d.StartedAt, _ = ParseTime(started)
	d.UpdatedAt, _ = ParseTime(updated)
	return d, nil
}

// StartPanelDomain records a move to a domain, replacing whatever was there.
func (db *DB) StartPanelDomain(ctx context.Context, d PanelDomain) error {
	previous := d.Previous
	if previous == nil {
		previous = []string{}
	}
	encoded, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	now := Now()
	_, err = db.Exec(ctx, `INSERT INTO panel_domain (id, domain, previous, state, problem, started_by, started_at, updated_at)
		VALUES (1,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET domain = excluded.domain, previous = excluded.previous,
			state = excluded.state, problem = excluded.problem, started_by = excluded.started_by,
			started_at = excluded.started_at, updated_at = excluded.updated_at`,
		d.Domain, string(encoded), d.State, string(d.Problem), d.StartedBy, now, now)
	if err != nil {
		return fmt.Errorf("record the move of the panel to %s: %w", d.Domain, err)
	}
	return nil
}

// SetPanelDomainState moves the panel's domain on to another state, with why
// when it stopped. It changes nothing when there is no row, and reports
// ErrNotFound.
func (db *DB) SetPanelDomainState(ctx context.Context, state string, problem json.RawMessage) error {
	result, err := db.Exec(ctx, `UPDATE panel_domain SET state = ?, problem = ?, updated_at = ? WHERE id = 1`,
		state, string(problem), Now())
	if err != nil {
		return fmt.Errorf("record the panel's domain as %s: %w", state, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePanelDomain forgets the panel's domain: it was taken off, or the
// installer put the panel somewhere else.
func (db *DB) DeletePanelDomain(ctx context.Context) error {
	if _, err := db.Exec(ctx, `DELETE FROM panel_domain WHERE id = 1`); err != nil {
		return fmt.Errorf("forget the panel's domain: %w", err)
	}
	return nil
}

// HostnameIsAnAppsDomain reports whether any app, in any team, has the
// hostname as one of its domains.
func (db *DB) HostnameIsAnAppsDomain(ctx context.Context, hostname string) (bool, error) {
	var found int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM domains WHERE hostname = ? LIMIT 1`, hostname).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look for an app on %s: %w", hostname, err)
	}
	return true, nil
}
