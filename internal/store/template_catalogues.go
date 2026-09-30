package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TemplateCatalogue is one of a team's own template catalogues, as anybody in
// the team may see it: never the value of the header it is fetched with, and
// never the copy itself.
type TemplateCatalogue struct {
	ID     string `json:"id"`
	TeamID string `json:"team_id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	// AuthHeaderName is the name of the header the address is asked with,
	// such as Authorization or PRIVATE-TOKEN. Its value is sealed and is not
	// part of this, or of anything any API answers.
	AuthHeaderName string `json:"auth_header_name,omitempty"`
	// FetchedAt is when the copy templates are installed from was
	// downloaded; zero when there has never been one.
	FetchedAt time.Time `json:"fetched_at"`
	// AttemptedAt is the last time a refresh was tried, whether or not it
	// worked, and LastError why it did not.
	AttemptedAt time.Time `json:"attempted_at"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TemplateCatalogueRow carries what the API never answers: the sealed header
// value, and the fingerprint of the copy.
type TemplateCatalogueRow struct {
	TemplateCatalogue
	SealedAuth string
	BodySHA256 string
}

// TemplateIcon is one logo of a catalogue's templates.
type TemplateIcon struct {
	TemplateID  string
	ContentType string
	Body        []byte
}

// TemplateCatalogueContext is what a catalogue's header value is sealed
// under: the team, the catalogue and its address. A row copied to another
// team does not open, and neither does one whose address was changed by hand,
// so the token is never sent anywhere the person who typed it did not send it.
func TemplateCatalogueContext(teamID, catalogueID, address string) string {
	return "template_catalogue:" + teamID + ":" + catalogueID + ":" + address
}

const templateCatalogueColumns = `id, team_id, name, url, auth_header, auth_value_enc, body_sha256,
	fetched_at, attempted_at, last_error, created_by, created_at, updated_at`

func scanTemplateCatalogue(row interface{ Scan(...any) error }) (TemplateCatalogueRow, error) {
	var c TemplateCatalogueRow
	var fetched, attempted, created, updated string
	if err := row.Scan(&c.ID, &c.TeamID, &c.Name, &c.URL, &c.AuthHeaderName, &c.SealedAuth, &c.BodySHA256,
		&fetched, &attempted, &c.LastError, &c.CreatedBy, &created, &updated); err != nil {
		return c, err
	}
	c.FetchedAt, _ = ParseTime(fetched)
	c.AttemptedAt, _ = ParseTime(attempted)
	c.CreatedAt, _ = ParseTime(created)
	c.UpdatedAt, _ = ParseTime(updated)
	return c, nil
}

// CreateTemplateCatalogue records a team's catalogue with the first copy of
// it, which the caller has already downloaded and read: a catalogue is not
// kept until it has worked once. sealedAuth is the header value sealed under
// TemplateCatalogueContext, or "" for none. c.ID must be set, because the
// seal names it.
func (db *DB) CreateTemplateCatalogue(ctx context.Context, c *TemplateCatalogue, sealedAuth string,
	body []byte, sha string, icons []TemplateIcon) error {
	if c.ID == "" {
		return errors.New("a template catalogue needs its id before it is sealed and saved")
	}
	now := Now()
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO template_catalogues
			(id, team_id, name, url, auth_header, auth_value_enc, body, body_sha256,
			 fetched_at, attempted_at, last_error, created_by, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,'',?,?,?)`,
			c.ID, c.TeamID, c.Name, c.URL, c.AuthHeaderName, sealedAuth, body, sha,
			now, now, c.CreatedBy, now, now); err != nil {
			return mapError(err)
		}
		return replaceIcons(ctx, tx, c.ID, icons)
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: this team already has a catalogue called %s or at that address", ErrConflict, c.Name)
		}
		return fmt.Errorf("save the template catalogue %s: %w", c.Name, err)
	}
	c.FetchedAt, _ = ParseTime(now)
	c.AttemptedAt, c.CreatedAt, c.UpdatedAt = c.FetchedAt, c.FetchedAt, c.FetchedAt
	return nil
}

// ListTemplateCatalogues returns a team's catalogues by name, without their
// copies.
func (db *DB) ListTemplateCatalogues(ctx context.Context, teamID string) ([]TemplateCatalogueRow, error) {
	return db.queryTemplateCatalogues(ctx, `SELECT `+templateCatalogueColumns+`
		FROM template_catalogues WHERE team_id = ? ORDER BY name`, teamID)
}

// ListAllTemplateCatalogues returns every team's catalogues, for the daily
// refresh.
func (db *DB) ListAllTemplateCatalogues(ctx context.Context) ([]TemplateCatalogueRow, error) {
	return db.queryTemplateCatalogues(ctx, `SELECT `+templateCatalogueColumns+`
		FROM template_catalogues ORDER BY attempted_at`)
}

func (db *DB) queryTemplateCatalogues(ctx context.Context, query string, args ...any) ([]TemplateCatalogueRow, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list template catalogues: %w", err)
	}
	defer rows.Close()
	out := []TemplateCatalogueRow{}
	for rows.Next() {
		c, err := scanTemplateCatalogue(rows)
		if err != nil {
			return nil, fmt.Errorf("read a template catalogue: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetTemplateCatalogue returns one of a team's catalogues, or ErrNotFound —
// including for a catalogue that exists and belongs to another team.
func (db *DB) GetTemplateCatalogue(ctx context.Context, teamID, id string) (TemplateCatalogueRow, error) {
	c, err := scanTemplateCatalogue(db.QueryRowContext(ctx, `SELECT `+templateCatalogueColumns+`
		FROM template_catalogues WHERE team_id = ? AND id = ?`, teamID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("read the template catalogue %s: %w", id, err)
	}
	return c, nil
}

// GetTemplateCatalogueByID returns a catalogue whichever team it belongs to,
// for the one route that names a catalogue and not its team. The caller
// authorizes the team it answers with before using anything else in it.
func (db *DB) GetTemplateCatalogueByID(ctx context.Context, id string) (TemplateCatalogueRow, error) {
	c, err := scanTemplateCatalogue(db.QueryRowContext(ctx, `SELECT `+templateCatalogueColumns+`
		FROM template_catalogues WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("read the template catalogue %s: %w", id, err)
	}
	return c, nil
}

// TemplateCatalogueCopy returns the last good copy of a catalogue and its
// fingerprint.
func (db *DB) TemplateCatalogueCopy(ctx context.Context, id string) ([]byte, string, error) {
	var body []byte
	var sha string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(body, x''), body_sha256 FROM template_catalogues WHERE id = ?`, id).
		Scan(&body, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("read the copy of the template catalogue %s: %w", id, err)
	}
	return body, sha, nil
}

// SaveTemplateCatalogueCopy replaces a catalogue's copy and its logos with
// what a refresh downloaded, in one step: a reader sees the old copy and its
// logos or the new ones, never one with the other.
func (db *DB) SaveTemplateCatalogueCopy(ctx context.Context, id string, body []byte, sha string, icons []TemplateIcon) error {
	now := Now()
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE template_catalogues SET body = ?, body_sha256 = ?,
			fetched_at = ?, attempted_at = ?, last_error = '', updated_at = ? WHERE id = ?`,
			body, sha, now, now, now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// Removed while it was being fetched.
			return ErrNotFound
		}
		return replaceIcons(ctx, tx, id, icons)
	})
	if err != nil {
		return fmt.Errorf("save the copy of the template catalogue %s: %w", id, err)
	}
	return nil
}

func replaceIcons(ctx context.Context, tx *sql.Tx, catalogueID string, icons []TemplateIcon) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM template_catalogue_icons WHERE catalogue_id = ?`, catalogueID); err != nil {
		return err
	}
	for _, icon := range icons {
		if _, err := tx.ExecContext(ctx, `INSERT INTO template_catalogue_icons
			(catalogue_id, template_id, content_type, body) VALUES (?,?,?,?)`,
			catalogueID, icon.TemplateID, icon.ContentType, icon.Body); err != nil {
			return err
		}
	}
	return nil
}

// RecordTemplateCatalogueFailure notes a refresh that did not work, and
// leaves the copy as it was.
func (db *DB) RecordTemplateCatalogueFailure(ctx context.Context, id, reason string) error {
	if _, err := db.Exec(ctx, `UPDATE template_catalogues SET attempted_at = ?, last_error = ? WHERE id = ?`,
		Now(), reason, id); err != nil {
		return fmt.Errorf("record the failed refresh of the template catalogue %s: %w", id, err)
	}
	return nil
}

// DeleteTemplateCatalogue removes one of a team's catalogues and its logos.
// Apps installed from it keep running; they are ordinary apps.
func (db *DB) DeleteTemplateCatalogue(ctx context.Context, teamID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM template_catalogues WHERE team_id = ? AND id = ?`, teamID, id)
	if err != nil {
		return fmt.Errorf("delete the template catalogue %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTemplateCatalogueIcons returns a catalogue's logos by template id, with
// their bytes only when asked for.
func (db *DB) ListTemplateCatalogueIcons(ctx context.Context, catalogueID string, withBodies bool) (map[string]TemplateIcon, error) {
	body := `x''`
	if withBodies {
		body = `body`
	}
	rows, err := db.QueryContext(ctx, `SELECT template_id, content_type, `+body+`
		FROM template_catalogue_icons WHERE catalogue_id = ?`, catalogueID)
	if err != nil {
		return nil, fmt.Errorf("list the logos of the template catalogue %s: %w", catalogueID, err)
	}
	defer rows.Close()
	out := map[string]TemplateIcon{}
	for rows.Next() {
		var icon TemplateIcon
		if err := rows.Scan(&icon.TemplateID, &icon.ContentType, &icon.Body); err != nil {
			return nil, fmt.Errorf("read a logo: %w", err)
		}
		out[icon.TemplateID] = icon
	}
	return out, rows.Err()
}

// GetTemplateCatalogueIcon returns one logo, or ErrNotFound.
func (db *DB) GetTemplateCatalogueIcon(ctx context.Context, catalogueID, templateID string) (TemplateIcon, error) {
	icon := TemplateIcon{TemplateID: templateID}
	err := db.QueryRowContext(ctx, `SELECT content_type, body FROM template_catalogue_icons
		WHERE catalogue_id = ? AND template_id = ?`, catalogueID, templateID).Scan(&icon.ContentType, &icon.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return icon, ErrNotFound
	}
	if err != nil {
		return icon, fmt.Errorf("read a logo: %w", err)
	}
	return icon, nil
}
