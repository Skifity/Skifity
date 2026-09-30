package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Plugin is an installed plugin.
type Plugin struct {
	ID string `json:"id"`
	// Manifest is the document as published, kept verbatim because it is what
	// an administrator read and agreed to.
	Manifest     string    `json:"manifest"`
	Version      string    `json:"version"`
	SourceURL    string    `json:"source_url,omitempty"`
	Status       string    `json:"status"`
	StatusDetail string    `json:"status_detail,omitempty"`
	Enabled      bool      `json:"enabled"`
	TokenID      string    `json:"-"`
	HMACSealed   string    `json:"-"`
	InstalledAt  time.Time `json:"installed_at"`
	InstalledBy  string    `json:"installed_by,omitempty"`
}

const pluginColumns = `id, manifest, version, source_url, status, status_detail,
	enabled, token_id, hmac_sealed, installed_at, installed_by`

func scanPlugin(row interface{ Scan(...any) error }) (Plugin, error) {
	var p Plugin
	var installed string
	err := row.Scan(&p.ID, &p.Manifest, &p.Version, &p.SourceURL, &p.Status, &p.StatusDetail,
		&p.Enabled, &p.TokenID, &p.HMACSealed, &installed, &p.InstalledBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, fmt.Errorf("scan plugin: %w", err)
	}
	p.InstalledAt, _ = ParseTime(installed)
	return p, nil
}

// CreatePlugin records an installation.
func (db *DB) CreatePlugin(ctx context.Context, p *Plugin) error {
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO plugins
		(id, manifest, version, source_url, status, status_detail, enabled, token_id, hmac_sealed, installed_at, installed_by)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Manifest, p.Version, p.SourceURL, defaultStr(p.Status, "installing"),
		p.StatusDetail, p.Enabled, p.TokenID, p.HMACSealed, now, p.InstalledBy)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: %s is already installed", ErrConflict, p.ID)
		}
		return fmt.Errorf("record the plugin: %w", err)
	}
	p.InstalledAt, _ = ParseTime(now)
	return nil
}

// GetPlugin reads one.
func (db *DB) GetPlugin(ctx context.Context, id string) (Plugin, error) {
	return scanPlugin(db.QueryRowContext(ctx,
		`SELECT `+pluginColumns+` FROM plugins WHERE id = ?`, id))
}

// ListPlugins returns every installed plugin.
func (db *DB) ListPlugins(ctx context.Context) ([]Plugin, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+pluginColumns+` FROM plugins ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list plugins: %w", err)
	}
	defer rows.Close()

	out := []Plugin{}
	for rows.Next() {
		p, err := scanPlugin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPluginStatus records what happened to a plugin.
func (db *DB) SetPluginStatus(ctx context.Context, id, status, detail string) error {
	_, err := db.Exec(ctx,
		`UPDATE plugins SET status = ?, status_detail = ? WHERE id = ?`, status, detail, id)
	if err != nil {
		return fmt.Errorf("record the plugin's status: %w", err)
	}
	return nil
}

// SetPluginEnabled switches a plugin off without removing it.
//
// Separate from uninstalling, because turning a plugin off for ten minutes to
// find out whether it is the cause of something is the first thing anybody
// does, and losing its settings and its token to do that is not acceptable.
func (db *DB) SetPluginEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := db.Exec(ctx, `UPDATE plugins SET enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return fmt.Errorf("switch the plugin: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePlugin removes a plugin and its settings.
func (db *DB) DeletePlugin(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM plugins WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove the plugin: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PluginSetting is one value a plugin declared and an operator filled in.
type PluginSetting struct {
	Key       string
	Value     string
	Encrypted bool
}

// ListPluginSettings returns a plugin's settings as stored.
func (db *DB) ListPluginSettings(ctx context.Context, pluginID string) ([]PluginSetting, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT key, value, encrypted FROM plugin_settings WHERE plugin_id = ? ORDER BY key`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("read the plugin's settings: %w", err)
	}
	defer rows.Close()

	out := []PluginSetting{}
	for rows.Next() {
		var s PluginSetting
		if err := rows.Scan(&s.Key, &s.Value, &s.Encrypted); err != nil {
			return nil, fmt.Errorf("scan a plugin setting: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetPluginSetting writes one value.
func (db *DB) SetPluginSetting(ctx context.Context, pluginID string, s PluginSetting) error {
	_, err := db.Exec(ctx, `
		INSERT INTO plugin_settings (plugin_id, key, value, encrypted) VALUES (?,?,?,?)
		ON CONFLICT(plugin_id, key) DO UPDATE SET
			value = excluded.value, encrypted = excluded.encrypted`,
		pluginID, s.Key, s.Value, s.Encrypted)
	if err != nil {
		return fmt.Errorf("save the plugin setting %q: %w", s.Key, err)
	}
	return nil
}

// DeletePluginSetting clears one value, which is how an integration inside a
// plugin is disconnected.
func (db *DB) DeletePluginSetting(ctx context.Context, pluginID, key string) error {
	_, err := db.Exec(ctx,
		`DELETE FROM plugin_settings WHERE plugin_id = ? AND key = ?`, pluginID, key)
	if err != nil {
		return fmt.Errorf("clear the plugin setting %q: %w", key, err)
	}
	return nil
}
