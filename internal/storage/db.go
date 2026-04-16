// Package storage provides the SQLite-backed persistence layer. It
// implements the Store interfaces declared by the memory, session, and
// skills packages, sharing a single database connection.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// DB wraps a *sql.DB with the concrete store implementations. One DB value
// backs all three domains (observations, sessions, skills).
type DB struct {
	sql *sql.DB
}

// Open returns a ready-to-use DB at path. Parent directories are created if
// absent, WAL mode is enabled, and pending migrations are applied.
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("storage: empty database path")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("storage: create dir %q: %w", dir, err)
		}
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open sqlite: %w", err)
	}
	// SQLite is a single file; more than one writer is pointless and causes
	// busy retries. One writer + many readers is the supported shape, and
	// modernc.org/sqlite serialises writes internally.
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("storage: ping: %w", err)
	}

	if err := migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("storage: migrate: %w", err)
	}

	return &DB{sql: sqlDB}, nil
}

// Close releases the database handle.
func (d *DB) Close() error {
	return d.sql.Close()
}

// SQL returns the underlying *sql.DB. Exposed for tests and advanced tooling
// (e.g., export/import). Normal code should use the Store interfaces.
func (d *DB) SQL() *sql.DB {
	return d.sql
}
