// Package dbx opens the SQLite database and runs schema migrations.
//
// The driver is modernc.org/sqlite: a pure-Go SQLite. This is a hard requirement,
// not a preference — the production binary is cross-compiled from a Windows
// workstation with CGO_ENABLED=0 for linux/arm64, which rules out any cgo driver.
package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"
)

// Options configures database opening.
type Options struct {
	Path   string
	Logger *slog.Logger
	// ReadOnly opens the database without migrations, used by the backup verifier.
	ReadOnly bool
}

// DB wraps *sql.DB with the project's conventions.
type DB struct {
	*sql.DB
	path string
	log  *slog.Logger
}

// Open connects to SQLite, applies pragmas and returns a ready handle.
//
// Pragma choices, all deliberate for an unattended appliance:
//
//	journal_mode=WAL   readers never block the writer; survives power loss
//	synchronous=NORMAL correct under WAL, far fewer fsyncs than FULL (SD card wear)
//	busy_timeout=5000  wait rather than fail when the writer holds the lock
//	foreign_keys=ON    SQLite defaults this OFF; we rely on cascade deletes
func Open(o Options) (*DB, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	mode := "rwc"
	if o.ReadOnly {
		mode = "ro"
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&mode=%s", o.Path, mode)

	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite handles one writer at a time. Capping the pool at a small number
	// avoids a pile-up of goroutines all blocked on the write lock, and keeps
	// memory predictable on a 4 GB device.
	sdb.SetMaxOpenConns(4)
	sdb.SetMaxIdleConns(2)
	sdb.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := sdb.PingContext(ctx); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if !o.ReadOnly {
		for _, p := range []string{
			"PRAGMA journal_mode=WAL",
			"PRAGMA synchronous=NORMAL",
			"PRAGMA temp_store=MEMORY",
			// 8 MiB page cache. Negative = KiB. Enough for our working set,
			// small enough to leave room for Chromium.
			"PRAGMA cache_size=-8000",
		} {
			if _, err := sdb.ExecContext(ctx, p); err != nil {
				sdb.Close()
				return nil, fmt.Errorf("%s: %w", p, err)
			}
		}
	}

	return &DB{DB: sdb, path: o.Path, log: o.Logger}, nil
}

// Path returns the database file path.
func (d *DB) Path() string { return d.path }

// Tx runs fn inside a transaction, committing on nil error and rolling back
// otherwise. A panic inside fn rolls back and re-panics.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				d.log.Error("rollback failed", "error", rbErr)
			}
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Checkpoint flushes the WAL into the main database file. Called before backups
// so the copied file is self-contained.
func (d *DB) Checkpoint(ctx context.Context) error {
	_, err := d.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// IntegrityCheck verifies the database is not corrupt. Used by /health and the
// backup restore path.
func (d *DB) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := d.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	return nil
}
