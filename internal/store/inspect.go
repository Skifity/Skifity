package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// Inspection is what can be said about a database file without changing it.
type Inspection struct {
	// SchemaVersion is the highest migration applied to it.
	SchemaVersion int
	// Known is the highest migration this build has, so a caller can say
	// whether the file is older, the same, or from a later version.
	Known int
	// Integrity is SQLite's integrity_check: "ok", or what is wrong.
	Integrity string
	// Users is how many accounts it holds.
	Users int
	// SealedKey and SealedValue are one encrypted setting, to try a master
	// key against. Empty when nothing is sealed yet.
	SealedKey, SealedValue string
}

// Inspect reads a database file without migrating it.
//
// Opening a file with Open brings it up to this build's schema, which is the
// right thing for the panel and the wrong thing for a backup somebody is about
// to restore: restoring the copy taken before an upgrade, with the upgraded
// binary, would upgrade the copy again on the way in, and the version it was
// meant for could then no longer open it.
func Inspect(ctx context.Context, path string) (Inspection, error) {
	var out Inspection
	if _, err := os.Stat(path); err != nil {
		return out, err
	}
	migrations, err := loadMigrations()
	if err != nil {
		return out, err
	}
	for _, m := range migrations {
		out.Known = max(out.Known, m.version)
	}

	sqlDB, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return out, fmt.Errorf("open %s: %w", path, err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&out.Integrity); err != nil {
		return out, fmt.Errorf("it is not a SQLite database: %w", err)
	}
	var version sql.NullInt64
	if err := sqlDB.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return out, fmt.Errorf("it has no migration history, so it is not a panel's database: %w", err)
	}
	out.SchemaVersion = int(version.Int64)
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&out.Users); err != nil {
		return out, fmt.Errorf("read its accounts: %w", err)
	}
	err = sqlDB.QueryRowContext(ctx,
		`SELECT key, value FROM settings WHERE encrypted = 1 AND value != '' LIMIT 1`).Scan(&out.SealedKey, &out.SealedValue)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("read its settings: %w", err)
	}
	return out, nil
}
