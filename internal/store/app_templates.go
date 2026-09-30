package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AppTemplate is which template an app was installed from.
type AppTemplate struct {
	AppID      string `json:"app_id"`
	TemplateID string `json:"template_id"`
	// Service is which of the template's services this app is.
	Service string `json:"service"`
	// InstalledImage is the image the template set, at install or at the last
	// update through the template.
	InstalledImage string `json:"installed_image"`
	InstalledAt    string `json:"installed_at"`
	// UpdateStatus is "" at rest, "backing_up" while an update's backups run,
	// and "failed" when one did not finish, with UpdateError saying why.
	UpdateStatus string `json:"update_status,omitempty"`
	UpdateTo     string `json:"update_to,omitempty"`
	UpdateError  string `json:"update_error,omitempty"`
}

// Template update states.
const (
	TemplateUpdateBackingUp = "backing_up"
	TemplateUpdateFailed    = "failed"
)

// RecordAppTemplate remembers which template an app came from.
func (db *DB) RecordAppTemplate(ctx context.Context, t AppTemplate) error {
	_, err := db.Exec(ctx, `INSERT INTO app_templates (app_id, template_id, service, installed_image, installed_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT (app_id) DO UPDATE SET template_id = excluded.template_id, service = excluded.service,
			installed_image = excluded.installed_image, installed_at = excluded.installed_at`,
		t.AppID, t.TemplateID, t.Service, t.InstalledImage, Now())
	if err != nil {
		return fmt.Errorf("record the template of %s: %w", t.AppID, err)
	}
	return nil
}

// GetAppTemplate returns the template an app came from, or ErrNotFound.
func (db *DB) GetAppTemplate(ctx context.Context, appID string) (AppTemplate, error) {
	t := AppTemplate{AppID: appID}
	err := db.QueryRowContext(ctx, `SELECT template_id, service, installed_image, installed_at,
		update_status, update_to, update_error FROM app_templates WHERE app_id = ?`, appID).
		Scan(&t.TemplateID, &t.Service, &t.InstalledImage, &t.InstalledAt, &t.UpdateStatus, &t.UpdateTo, &t.UpdateError)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, fmt.Errorf("read the template of %s: %w", appID, err)
	}
	return t, nil
}

// SetTemplateUpdate records where an update through the template stands.
func (db *DB) SetTemplateUpdate(ctx context.Context, appID, status, to, reason string) error {
	if _, err := db.Exec(ctx, `UPDATE app_templates SET update_status = ?, update_to = ?, update_error = ? WHERE app_id = ?`,
		status, to, reason, appID); err != nil {
		return fmt.Errorf("record the template update of %s: %w", appID, err)
	}
	return nil
}

// FailInterruptedTemplateUpdates marks every update that was waiting for its
// backups when the panel stopped as failed. The goroutine that would have
// finished it went with the restart, and one left at backing_up refuses every
// later update of that app for good.
func (db *DB) FailInterruptedTemplateUpdates(ctx context.Context, reason string) (int64, error) {
	result, err := db.Exec(ctx, `UPDATE app_templates SET update_status = ?, update_error = ? WHERE update_status = ?`,
		TemplateUpdateFailed, reason, TemplateUpdateBackingUp)
	if err != nil {
		return 0, fmt.Errorf("fail interrupted template updates: %w", err)
	}
	return result.RowsAffected()
}

// FinishTemplateUpdate records that an app now runs the template's image.
func (db *DB) FinishTemplateUpdate(ctx context.Context, appID, image string) error {
	if _, err := db.Exec(ctx, `UPDATE app_templates SET installed_image = ?, installed_at = ?,
		update_status = '', update_to = '', update_error = '' WHERE app_id = ?`, image, Now(), appID); err != nil {
		return fmt.Errorf("finish the template update of %s: %w", appID, err)
	}
	return nil
}
