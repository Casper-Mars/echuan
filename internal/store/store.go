// Package store owns SQLite persistence: connection setup, migrations,
// transaction helpers and the readiness probes.
//
// Layout:
//
//	write pool  — one connection, DSN forces WAL, foreign_keys, busy_timeout,
//	              synchronous=NORMAL and BEGIN IMMEDIATE transactions
//	read pool   — N connections, PRAGMA query_only=1 so a read handle can
//	              never mutate the database
//
// Migrations are applied by Migrate before the read pool is started, so every
// read connection only ever observes a schema that is at least as new as the
// code that opened it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/Casper-Mars/echuan/migrations"
)

// DriverName is the database/sql driver registered by modernc.org/sqlite.
const DriverName = "sqlite"

// defaultReadPoolSize bounds concurrent readers when Config.ReadPoolSize is
// not set.
const defaultReadPoolSize = 4

// busyTimeoutMS is how long SQLite waits for a competing lock before it
// returns SQLITE_BUSY.
const busyTimeoutMS = 5000

// ErrClosed is returned by operations on a store whose Close already ran.
var ErrClosed = errors.New("store: closed")

// Config describes how to open the database.
type Config struct {
	// Path is the SQLite database file; its parent directory is created when
	// missing.
	Path string
	// ReadPoolSize is the maximum number of concurrent read connections.
	// Values <= 0 fall back to 4.
	ReadPoolSize int
	// Logger receives migration progress. A discard logger is used when nil.
	Logger *slog.Logger
}

// Store owns the write and read connection pools for one SQLite file.
type Store struct {
	path         string
	readPoolSize int
	logger       *slog.Logger

	write *sql.DB

	mu   sync.RWMutex
	read *sql.DB

	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

// Open creates (or opens) the database file and starts the write pool.
//
// The write pool is deliberately limited to a single connection: SQLite allows
// only one writer anyway, and serialising writes turns would-be SQLITE_BUSY
// loops into a plain queue. The read pool is started separately with
// OpenReadPool so that callers can migrate before any reader connects.
func Open(cfg Config) (*Store, error) {
	if cfg.Path == "" {
		return nil, errors.New("store: database path is empty")
	}
	if cfg.ReadPoolSize <= 0 {
		cfg.ReadPoolSize = defaultReadPoolSize
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create data directory %s: %w", dir, err)
	}

	db, err := sql.Open(DriverName, writeDSN(cfg.Path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", cfg.Path, err)
	}
	// One writer, kept for the process lifetime: no connection churn means the
	// PRAGMAs applied through the DSN stay in effect for every transaction.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: connect %s: %w", cfg.Path, err)
	}

	return &Store{
		path:         cfg.Path,
		readPoolSize: cfg.ReadPoolSize,
		logger:       cfg.Logger,
		write:        db,
	}, nil
}

// OpenReadPool starts the read-only connection pool. It must be called after
// the schema reaches the latest version (Open + Migrate) so that every reader
// opens an up-to-date database.
func (s *Store) OpenReadPool(ctx context.Context) error {
	if s.closed.Load() {
		return ErrClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.read != nil {
		return nil
	}

	db, err := sql.Open(DriverName, readDSN(s.path))
	if err != nil {
		return fmt.Errorf("store: open read pool: %w", err)
	}
	db.SetMaxOpenConns(s.readPoolSize)
	db.SetMaxIdleConns(s.readPoolSize)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("store: connect read pool: %w", err)
	}
	s.read = db
	return nil
}

// Close closes the read pool and then the write pool. It is idempotent.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)

		s.mu.Lock()
		read := s.read
		s.read = nil
		s.mu.Unlock()

		if read != nil {
			s.closeErr = read.Close()
		}
		if s.write != nil {
			if err := s.write.Close(); err != nil && s.closeErr == nil {
				s.closeErr = err
			}
		}
	})
	return s.closeErr
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Write returns the single-connection write pool.
func (s *Store) Write() *sql.DB { return s.write }

// Read returns the read pool, or the write pool when no read pool was started
// (single-connection mode used by small tools and tests).
func (s *Store) Read() *sql.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.read != nil {
		return s.read
	}
	return s.write
}

// Ping verifies that the write pool and, when running, the read pool answer.
func (s *Store) Ping(ctx context.Context) error {
	if s.closed.Load() || s.write == nil {
		return ErrClosed
	}
	if err := s.write.PingContext(ctx); err != nil {
		return fmt.Errorf("store: ping write pool: %w", err)
	}

	s.mu.RLock()
	read := s.read
	s.mu.RUnlock()
	if read != nil {
		if err := read.PingContext(ctx); err != nil {
			return fmt.Errorf("store: ping read pool: %w", err)
		}
	}
	return nil
}

// MigrationVersion returns PRAGMA user_version, i.e. the schema version
// currently stored in the database file.
func (s *Store) MigrationVersion(ctx context.Context) (int, error) {
	if s.closed.Load() || s.write == nil {
		return 0, ErrClosed
	}
	var version int
	if err := s.Read().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("store: read user_version: %w", err)
	}
	return version, nil
}

// Tx runs fn inside a write transaction.
//
// The write DSN sets _txlock=immediate, so the transaction takes the write
// lock up front instead of failing later on a lock upgrade. fn returning an
// error, or panicking, rolls the transaction back; a panic is re-raised after
// the rollback so the caller's normal panic handling still applies.
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	if s.closed.Load() || s.write == nil {
		return ErrClosed
	}

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("store: commit transaction: %w", err)
	}
	return nil
}

// LatestMigrationVersion returns the highest migration version embedded in the
// binary. The result is computed once per process.
func LatestMigrationVersion() (int, error) {
	return latestVersion()
}

var latestVersion = sync.OnceValues(func() (int, error) {
	return migrations.Latest(migrations.FS)
})

// writeDSN builds the writer connection string.
func writeDSN(path string) string {
	return fileDSN(path,
		"_pragma=busy_timeout("+strconv.Itoa(busyTimeoutMS)+")",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(1)",
		"_txlock=immediate",
	)
}

// readDSN builds the reader connection string. query_only makes accidental
// writes fail instead of silently competing with the writer.
func readDSN(path string) string {
	return fileDSN(path,
		"_pragma=busy_timeout("+strconv.Itoa(busyTimeoutMS)+")",
		"_pragma=foreign_keys(1)",
		"_query_only=1",
	)
}

// fileDSN renders a file: URI so that spaces and other special characters in
// the database path survive both SQLite's URI parser and the driver's own
// query-parameter parser.
func fileDSN(path string, params ...string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	dsn := u.String()
	if len(params) > 0 {
		dsn += "?" + strings.Join(params, "&")
	}
	return dsn
}
