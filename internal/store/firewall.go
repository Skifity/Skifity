package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AppFirewall is an app's rules as they are stored.
//
// Rules is the JSON an edgerules.RuleSet marshals to. The store does not parse
// it: validation belongs where somebody is looking at the form and can be told
// what is wrong, and a second parser here would be a second place for the two
// to disagree.
type AppFirewall struct {
	AppID     string `json:"app_id"`
	Enabled   bool   `json:"enabled"`
	Rules     string `json:"rules"`
	UpdatedAt string `json:"updated_at,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// GetAppFirewall returns an app's rules, or an empty one when it has none.
//
// No ErrNotFound: every app has a firewall, and one nobody has written to is
// switched off with no rules. A caller that had to tell those apart would get
// it wrong somewhere.
func (db *DB) GetAppFirewall(ctx context.Context, appID string) (AppFirewall, error) {
	firewall := AppFirewall{AppID: appID, Rules: "{}"}
	err := db.QueryRowContext(ctx,
		`SELECT enabled, rules, updated_at, updated_by FROM app_firewalls WHERE app_id = ?`, appID).
		Scan(&firewall.Enabled, &firewall.Rules, &firewall.UpdatedAt, &firewall.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return firewall, nil
	}
	if err != nil {
		return firewall, fmt.Errorf("read the firewall for %s: %w", appID, err)
	}
	return firewall, nil
}

// SetAppFirewall writes an app's rules.
func (db *DB) SetAppFirewall(ctx context.Context, f AppFirewall, actorID string) error {
	if f.Rules == "" {
		f.Rules = "{}"
	}
	_, err := db.Exec(ctx, `
		INSERT INTO app_firewalls (app_id, enabled, rules, updated_at, updated_by)
		VALUES (?,?,?,?,?)
		ON CONFLICT(app_id) DO UPDATE SET
			enabled = excluded.enabled,
			rules = excluded.rules,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		f.AppID, f.Enabled, f.Rules, Now(), actorID)
	if err != nil {
		return fmt.Errorf("save the firewall for %s: %w", f.AppID, err)
	}
	return nil
}

// ProtectedHostname is one hostname the guard has to judge.
type ProtectedHostname struct {
	Hostname string
	AppID    string
	Rules    string
}

// ProtectedHostnames lists every hostname whose app has the firewall switched
// on, which is exactly what the guard's configuration holds.
//
// A join rather than two queries: what the guard needs is hostnames, and an app
// with three domains is three entries. Building that in Go from two lists would
// be the same work with somewhere for a domain to go missing.
func (db *DB) ProtectedHostnames(ctx context.Context) ([]ProtectedHostname, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT d.hostname, f.app_id, f.rules
		FROM app_firewalls f
		JOIN domains d ON d.app_id = f.app_id
		WHERE f.enabled = 1
		ORDER BY d.hostname`)
	if err != nil {
		return nil, fmt.Errorf("list the protected hostnames: %w", err)
	}
	defer rows.Close()

	var out []ProtectedHostname
	for rows.Next() {
		var p ProtectedHostname
		if err := rows.Scan(&p.Hostname, &p.AppID, &p.Rules); err != nil {
			return nil, fmt.Errorf("scan a protected hostname: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProtectedNamespaces lists the namespaces holding at least one app whose
// firewall is switched on, which is where the guard's middleware has to exist.
//
// Distinct namespaces rather than apps: a middleware is one object per
// namespace however many protected apps are in it, and applying the same object
// once per app would be the same work done four times.
func (db *DB) ProtectedNamespaces(ctx context.Context) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT e.namespace
		FROM app_firewalls f
		JOIN apps a ON a.id = f.app_id
		JOIN environments e ON e.id = a.environment_id
		WHERE f.enabled = 1
		ORDER BY e.namespace`)
	if err != nil {
		return nil, fmt.Errorf("list the protected namespaces: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var namespace string
		if err := rows.Scan(&namespace); err != nil {
			return nil, fmt.Errorf("scan a protected namespace: %w", err)
		}
		out = append(out, namespace)
	}
	return out, rows.Err()
}

// AppHasFirewall reports whether an app's Ingress needs the guard's middleware.
func (db *DB) AppHasFirewall(ctx context.Context, appID string) (bool, error) {
	var enabled bool
	err := db.QueryRowContext(ctx,
		`SELECT enabled FROM app_firewalls WHERE app_id = ?`, appID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the firewall for %s: %w", appID, err)
	}
	return enabled, nil
}
