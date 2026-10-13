// Package db opens the SQLite database (ADR-02), runs goose migrations and
// exposes the sqlc-generated queries behind repository interfaces so the
// storage engine can be swapped later (PostgreSQL) without touching callers.
//
// Two pools are used: a single-connection writer (BEGIN IMMEDIATE transactions
// via _txlock=immediate so writers queue instead of failing with SQLITE_BUSY
// on upgrade) and a small reader pool. WAL lets readers proceed while a write
// transaction is open.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// DB wraps the writer and reader pools.
type DB struct {
	path string
	w    *sql.DB
	r    *sql.DB
	rq   *sqlc.Queries
}

// Options tunes Open.
type Options struct {
	ReadConns int           // reader pool size; default max(2, min(8, 2×NumCPU))
	BusyMS    int           // PRAGMA busy_timeout; default 5000
	IdleTime  time.Duration // SetConnMaxIdleTime; default 5m
}

const (
	defaultBusyMS  = 5000
	cacheSizeKiB   = -16000 // PRAGMA cache_size (negative = KiB), 16 MB per connection
	defaultIdleDur = 5 * time.Minute
)

// Open opens (creating if needed) the database at path with the pragmas from
// SPEC §3 (WAL, synchronous=NORMAL, foreign_keys=ON, busy_timeout, temp_store
// =MEMORY, cache_size). It does not run migrations; see MigrateUp.
func Open(path string, opts ...Options) (*DB, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.ReadConns <= 0 {
		o.ReadConns = max(2, min(8, 2*runtime.NumCPU()))
	}
	if o.BusyMS <= 0 {
		o.BusyMS = defaultBusyMS
	}
	if o.IdleTime <= 0 {
		o.IdleTime = defaultIdleDur
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("db: resolve path: %w", err)
	}

	w, err := sql.Open("sqlite", dsn(abs, o.BusyMS, true))
	if err != nil {
		return nil, fmt.Errorf("db: open writer: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxIdleTime(0) // keep the single writer connection warm

	r, err := sql.Open("sqlite", dsn(abs, o.BusyMS, false))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("db: open reader: %w", err)
	}
	r.SetMaxOpenConns(o.ReadConns)
	r.SetMaxIdleConns(o.ReadConns)
	r.SetConnMaxIdleTime(o.IdleTime)

	d := &DB{path: abs, w: w, r: r, rq: sqlc.New(r)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Ping(ctx); err != nil {
		_ = d.Close()
		return nil, err
	}
	var mode string
	if err := w.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("db: read journal_mode: %w", err)
	}
	if mode != "wal" {
		_ = d.Close()
		return nil, fmt.Errorf("db: journal_mode is %q, want wal (is %s on a filesystem that supports locking?)", mode, abs)
	}
	return d, nil
}

// dsn builds the modernc.org/sqlite DSN. Pragmas are applied on every new
// connection of the pool. The writer additionally uses _txlock=immediate so a
// transaction takes the write lock at BEGIN and waits (busy_timeout) instead of
// failing later on lock upgrade.
func dsn(abs string, busyMS int, writer bool) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyMS))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "temp_store(MEMORY)")
	q.Add("_pragma", fmt.Sprintf("cache_size(%d)", cacheSizeKiB))
	if writer {
		q.Set("_txlock", "immediate")
	}
	return "file:" + abs + "?" + q.Encode()
}

// Path returns the absolute database file path.
func (d *DB) Path() string { return d.path }

// Writer exposes the single-connection writer pool (used by goose and backup).
func (d *DB) Writer() *sql.DB { return d.w }

// Reader exposes the reader pool.
func (d *DB) Reader() *sql.DB { return d.r }

// Close closes both pools.
func (d *DB) Close() error {
	return errors.Join(d.r.Close(), d.w.Close())
}

// Ping checks both pools with SELECT 1.
func (d *DB) Ping(ctx context.Context) error {
	var one int
	if err := d.w.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("db: writer ping: %w", err)
	}
	if err := d.r.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("db: reader ping: %w", err)
	}
	return nil
}

// Queries returns read-only queries bound to the reader pool. Writes must go
// through WithTx.
func (d *DB) Queries() *sqlc.Queries { return d.rq }

// WithTx runs fn inside an IMMEDIATE write transaction on the writer pool and
// commits when fn returns nil; any error (or panic) rolls back.
func (d *DB) WithTx(ctx context.Context, fn func(q *sqlc.Queries) error) (err error) {
	tx, err := d.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
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
	if err = fn(sqlc.New(tx)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// ErrNotFound is returned by repository getters when no row matches.
var ErrNotFound = sql.ErrNoRows

// IsNotFound reports whether err is a missing-row error.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
