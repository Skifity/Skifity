// Package store owns the panel's SQLite database: connection setup, schema
// migrations, id generation and the repositories built on top.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned by every repository lookup that finds nothing. Callers
// compare with errors.Is so the HTTP layer can turn it into a 404 in one place.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a unique constraint rejects a write, so the API
// can answer 409 with a useful message instead of a raw SQLite error.
var ErrConflict = errors.New("already exists")

// DB wraps *sql.DB with the panel's conventions.
type DB struct {
	*sql.DB
	path string
	// SQLite allows exactly one writer. Serialising writes in the process turns
	// "database is locked" into a short wait instead of an error the user sees.
	writeMu sync.Mutex
}

// Open opens (creating if needed) the panel database and applies migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create database directory %s: %w", dir, err)
		}
	}

	sqlDB, err := sql.Open("sqlite", fileDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	// One writer, a few readers. SQLite gains nothing from a large pool.
	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("connect to database %s: %w", path, err)
	}

	db := &DB{DB: sqlDB, path: path}
	if err := db.Migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// fileDSN is how every file-backed database is opened.
func fileDSN(path string) string {
	// WAL keeps readers from blocking the writer, busy_timeout absorbs the
	// remaining contention, and foreign_keys makes the schema's ON DELETE
	// CASCADE rules actually run.
	return path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_time_format=sqlite"
}

// OpenMemory opens a private in-memory database. Used by tests.
func OpenMemory(ctx context.Context) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		return nil, fmt.Errorf("open in-memory database: %w", err)
	}
	// A single connection, because every connection to ":memory:" gets its own
	// database.
	sqlDB.SetMaxOpenConns(1)
	db := &DB{DB: sqlDB, path: ":memory:"}
	if err := db.Migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Path is the file this database lives in, for diagnostics and backups.
func (db *DB) Path() string { return db.path }

// Migrate applies every migration that has not run yet, in order, each in its own
// transaction so a failure leaves the database on the last good version.
func (db *DB) Migrate(ctx context.Context) error {
	return db.migrateUpTo(ctx, math.MaxInt)
}

// migrateUpTo applies migrations up to and including a version, so a test can
// put rows into an older schema and watch a later migration carry them.
func (db *DB) migrateUpTo(ctx context.Context, last int) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name    TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if applied[m.version] || m.version > last {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

// SchemaVersion reports the highest applied migration, for /api/health.
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return int(v.Int64), nil
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		numPart, rest, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s is not named <version>_<name>.sql", e.Name())
		}
		version, err := strconv.Atoi(numPart)
		if err != nil {
			return nil, fmt.Errorf("migration %s has a non-numeric version: %w", e.Name(), err)
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: version, name: rest, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("two migrations share version %d", out[i].version)
		}
	}
	return out, nil
}

// rebuildMarker opts a migration into SQLite's procedure for changing a table
// in a way ALTER TABLE cannot, such as a CHECK constraint: make a new table,
// copy the rows, drop the old one, rename the new one into its place.
//
// That procedure needs foreign keys off, and not as a nicety. With them on,
// dropping the old table is a DELETE of every row in it, and every ON DELETE
// CASCADE in the schema fires: rebuilding apps would take every deployment,
// domain, variable and volume record with it, inside a migration that then
// reports success. foreign_keys cannot change inside a transaction, so it is
// switched off on one connection, around the transaction, and the rows are
// checked with foreign_key_check before anything commits.
const rebuildMarker = "-- migrate: rebuilds a table"

func (db *DB) applyMigration(ctx context.Context, m migration) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	if strings.HasPrefix(m.sql, rebuildMarker) {
		return db.applyRebuild(ctx, m)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, Now()); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return tx.Commit()
}

// applyRebuild runs a migration marked with rebuildMarker. The caller holds
// writeMu.
func (db *DB) applyRebuild(ctx context.Context, m migration) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("take a connection: %w", err)
	}
	// Returning it to the pool cannot fail in a way that changes the outcome.
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("switch foreign keys off: %w", err)
	}
	defer func() {
		// Back on before the connection returns to the pool, where every other
		// query relies on the cascades. A connection that cannot be put back
		// is not returned at all.
		if _, onErr := conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`); onErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			if err == nil {
				err = fmt.Errorf("switch foreign keys back on: %w", onErr)
			}
		}
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	// With the checks off, nothing stopped a row from pointing at nothing.
	// This is where that would be found, before it is committed.
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check foreign keys: %w", err)
	}
	broken := rows.Next()
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("check foreign keys: %w", err)
	}
	rows.Close()
	if broken {
		return errors.New("the rebuilt table leaves rows pointing at nothing")
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, Now()); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return tx.Commit()
}

// Tx runs fn inside a write transaction, serialised against other writers.
func (db *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Exec runs a write statement, serialised against other writers.
func (db *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	res, err := db.ExecContext(ctx, query, args...)
	return res, mapError(err)
}

// mapError turns SQLite's error strings into the package's sentinel errors.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed"):
		return fmt.Errorf("%w: %s", ErrConflict, strings.TrimPrefix(msg, "constraint failed: "))
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	}
	return err
}

// Now returns the current time in the text format every timestamp column uses.
func Now() string { return FormatTime(time.Now().UTC()) }

// FormatTime renders a time the way the database stores it.
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ParseTime reads a timestamp column.
func ParseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

// idAlphabet is Crockford-ish base32 without padding: no ambiguous characters
// once uppercased, and safe in URLs and Kubernetes names.
var idAlphabet = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// NewID returns a sortable, prefixed, random id such as "app_01j9q8z4k7m2rt".
//
// The first 6 bytes are the millisecond timestamp, so ids sort by creation time
// and a database index on them stays dense. The remaining 10 bytes are random.
func NewID(prefix string) string {
	buf := make([]byte, 16)
	ms := uint64(time.Now().UTC().UnixMilli())
	binary.BigEndian.PutUint64(buf[0:8], ms<<16)
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand does not fail in practice; falling back to a
		// timestamp-only id keeps the panel running rather than crashing.
		binary.BigEndian.PutUint64(buf[8:], uint64(time.Now().UnixNano()))
	}
	return prefix + "_" + idAlphabet.EncodeToString(buf)[:20]
}

// NullString converts an empty string to SQL NULL, for nullable columns.
func NullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// scanTime reads a nullable timestamp column.
func scanTime(v sql.NullString) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	t, err := ParseTime(v.String)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Snapshot writes a consistent copy of the database to a path.
//
// Not a file copy. The database runs in WAL mode, so a committed transaction
// can live in panel.db-wal and not yet in panel.db: copying the one file, which
// is what the documentation used to tell people to do, silently loses whatever
// had not been checkpointed. VACUUM INTO takes the copy through SQLite itself,
// which means it is consistent, complete, and compacted, and it can be done
// while the panel is running.
func (db *DB) Snapshot(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("a snapshot needs somewhere to write to")
	}
	if _, err := os.Stat(path); err == nil {
		// VACUUM INTO refuses an existing file, and saying so is friendlier
		// than passing SQLite's message through.
		return fmt.Errorf("%s already exists; choose a path that does not", path)
	}
	// The path is a literal because VACUUM INTO does not take a bound
	// parameter, so a quote in it would end the statement.
	if strings.ContainsAny(path, `'"`) {
		return fmt.Errorf("a snapshot path cannot contain quotes")
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO '`+path+`'`); err != nil {
		return fmt.Errorf("write a snapshot to %s: %w", path, err)
	}
	return nil
}
