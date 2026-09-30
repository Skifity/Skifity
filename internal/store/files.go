package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AppFile is a file an app's containers read, mounted read-only at Path.
type AppFile struct {
	ID    string `json:"id"`
	AppID string `json:"app_id"`
	Path  string `json:"path"`
	// Content is filled in only for a file that is not secret, and only where
	// a caller asked for it; a secret file's content is never sent back.
	Content    string    `json:"content,omitempty"`
	Size       int       `json:"size"`
	IsSecret   bool      `json:"is_secret"`
	Executable bool      `json:"executable"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// FileRow carries the sealed content alongside the model, because callers
// decide whether to open it.
type FileRow struct {
	AppFile
	Sealed string
}

// SetFile creates the file at f.Path or replaces the one there. sealed must
// already be sealed, under the app and the path (see FileContext).
func (db *DB) SetFile(ctx context.Context, f *AppFile, sealed string) error {
	if f.ID == "" {
		f.ID = NewID("file")
	}
	now := Now()
	if _, err := db.Exec(ctx, `INSERT INTO app_files (id, app_id, path, content_enc, size, is_secret, executable,
			created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (app_id, path) DO UPDATE SET content_enc = excluded.content_enc, size = excluded.size,
			is_secret = excluded.is_secret, executable = excluded.executable, updated_at = excluded.updated_at`,
		f.ID, f.AppID, f.Path, sealed, f.Size, f.IsSecret, f.Executable, now, now); err != nil {
		return fmt.Errorf("set the file %s: %w", f.Path, err)
	}
	// A file replaced keeps the id it had, not the one made up above.
	if err := db.QueryRowContext(ctx, `SELECT id FROM app_files WHERE app_id = ? AND path = ?`,
		f.AppID, f.Path).Scan(&f.ID); err != nil {
		return fmt.Errorf("read back the file %s: %w", f.Path, err)
	}
	f.UpdatedAt, _ = ParseTime(now)
	return nil
}

// FileContext is what a file's content is sealed under: the app and the path,
// so a row copied to another app, or to another path, does not open.
func FileContext(appID, path string) string { return "file:" + appID + ":" + path }

// ListFiles returns an app's files, with their sealed content, by path.
func (db *DB) ListFiles(ctx context.Context, appID string) ([]FileRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, app_id, path, content_enc, size, is_secret, executable,
		created_at, updated_at FROM app_files WHERE app_id = ? ORDER BY path`, appID)
	if err != nil {
		return nil, fmt.Errorf("list the files of %s: %w", appID, err)
	}
	defer rows.Close()
	out := []FileRow{}
	for rows.Next() {
		r, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetFileByPath returns the app's file at a path.
func (db *DB) GetFileByPath(ctx context.Context, appID, path string) (FileRow, error) {
	row := db.QueryRowContext(ctx, `SELECT id, app_id, path, content_enc, size, is_secret, executable,
		created_at, updated_at FROM app_files WHERE app_id = ? AND path = ?`, appID, path)
	r, err := scanFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FileRow{}, ErrNotFound
	}
	return r, err
}

// DeleteFile removes one of an app's files.
func (db *DB) DeleteFile(ctx context.Context, appID, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM app_files WHERE app_id = ? AND id = ?`, appID, id)
	if err != nil {
		return fmt.Errorf("delete a file: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanFile(row interface{ Scan(...any) error }) (FileRow, error) {
	var r FileRow
	var created, updated string
	if err := row.Scan(&r.ID, &r.AppID, &r.Path, &r.Sealed, &r.Size, &r.IsSecret, &r.Executable,
		&created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FileRow{}, err
		}
		return FileRow{}, fmt.Errorf("read a file: %w", err)
	}
	r.CreatedAt, _ = ParseTime(created)
	r.UpdatedAt, _ = ParseTime(updated)
	return r, nil
}
