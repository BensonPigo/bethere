// Package store keeps BeThere's state in SQLite (tech-stack.md T3–T4).
//
// Writes go through a single connection that takes the write lock at
// BEGIN; reads use a separate read-only pool, so polling never queues
// behind a slow write (T3a). Lookups distinguish "no such token" from any
// other failure, because only a confirmed miss may be reported to a
// client as an invalid link (T3b). The store never creates or serves an
// empty database except on an explicit first deployment (T3c).
//
// The store checks who acts (a still-valid credential of the same event);
// what each role type may do (access-control.md section 4) is checked by
// the caller.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNoDatabase = errors.New("database file not found; set BETHERE_INIT=1 only on the very first deployment")
	// ErrEmptyDatabase is a file that exists but holds no schema, the usual
	// trace of a failed restore. Setting BETHERE_INIT would serve an empty
	// database and wipe every device's offline copy (ADR-0012).
	ErrEmptyDatabase   = errors.New("database file exists but is empty or has no schema; the restore probably failed: do NOT set BETHERE_INIT, restore again")
	ErrDatabaseExists  = errors.New("BETHERE_INIT is set but the database already exists")
	ErrNotFound        = errors.New("not found")
	ErrVersionConflict = errors.New("event version changed")
	ErrNothingToUndo   = errors.New("nothing to undo")
)

type Store struct {
	w *sql.DB // single writer connection
	r *sql.DB // read-only pool
}

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens the database at path (T3c):
//   - without init the file must exist and carry a migrated schema; a
//     missing, empty or schema-less file is refused, so a failed restore
//     never comes up as an empty database;
//   - with init the file must not exist; it is created exclusively and
//     removed again if setup fails.
func Open(ctx context.Context, path string, init bool) (*Store, error) {
	return open(ctx, path, init, migrations)
}

func open(ctx context.Context, path string, init bool, fsys fs.FS) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if init {
		f, err := os.OpenFile(abs, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			return nil, ErrDatabaseExists
		}
		if err != nil {
			return nil, err
		}
		f.Close()
	} else if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoDatabase
	} else if err != nil {
		return nil, err
	}

	s, err := connect(ctx, abs, init, fsys)
	if err != nil && init {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(abs + suffix)
		}
	}
	return s, err
}

func connect(ctx context.Context, abs string, init bool, fsys fs.FS) (*Store, error) {
	version := 0
	if !init {
		// Look before touching: a read-only probe leaves a bad file exactly
		// as it was, for whoever investigates the failed restore.
		v, err := probeVersion(ctx, abs)
		if err != nil {
			return nil, err
		}
		if v == 0 {
			return nil, ErrEmptyDatabase
		}
		version = v
	}

	w, err := sql.Open("sqlite", dsn(abs, "rw", "_txlock=immediate",
		"_pragma=journal_mode(WAL)", "_pragma=secure_delete(ON)"))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := migrate(ctx, w, fsys, version); err != nil {
		w.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Prove the database is writable now rather than at the first live
	// operation (ADR-0012: fail at startup).
	if err := writeProbe(ctx, w); err != nil {
		w.Close()
		return nil, fmt.Errorf("database not writable: %w", err)
	}

	r, err := sql.Open("sqlite", dsn(abs, "rw", "_pragma=query_only(1)"))
	if err != nil {
		w.Close()
		return nil, err
	}
	if err := r.PingContext(ctx); err != nil {
		w.Close()
		r.Close()
		return nil, err
	}
	return &Store{w: w, r: r}, nil
}

func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

// writeProbe rewrites user_version with its own value, which needs the
// file to be writable but changes nothing.
func writeProbe(ctx context.Context, w *sql.DB) error {
	var v int
	if err := w.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	_, err := w.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v))
	return err
}

// probeVersion reads user_version without changing anything on disk. An
// empty file is answered without SQLite, which would otherwise delete a
// leftover WAL next to it.
func probeVersion(ctx context.Context, abs string) (int, error) {
	if info, err := os.Stat(abs); err != nil {
		return 0, err
	} else if info.Size() == 0 {
		return 0, nil
	}
	db, err := sql.Open("sqlite", dsn(abs, "ro"))
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v int
	err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// dsn builds a file: URI with the path escaped, so "?", "#" or "%" in it
// cannot cut the file name short. mode is "ro" or "rw", never "rwc":
// SQLite itself refuses to create a missing file. Every PRAGMA is in the
// DSN so each pooled connection gets it.
func dsn(abs, mode string, params ...string) string {
	common := []string{"mode=" + mode, "_pragma=busy_timeout(5000)", "_pragma=foreign_keys(1)"}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: strings.Join(append(common, params...), "&")}
	return u.String()
}

// migrate applies numbered migrations above current (the database's
// user_version). A database newer than this binary is left alone so a
// rollback can still start (T4). Released migrations are frozen; schema
// changes always go in a new file.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS, current int) error {
	names, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return err
	}
	for i, name := range names {
		n := i + 1
		prefix, _, _ := strings.Cut(strings.TrimPrefix(name, "migrations/"), "_")
		if num, err := strconv.Atoi(prefix); err != nil || num != n {
			return fmt.Errorf("migration %s: expected number %04d", name, n)
		}
		if n <= current {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// write runs fn in one write transaction.
func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func toNS(t time.Time) int64 { return t.UnixNano() }

// nullNS stores a zero time as NULL.
func nullNS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixNano()
}

func fromNS(v int64) time.Time { return time.Unix(0, v).UTC() }

func fromNullNS(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return fromNS(v.Int64)
}

// notFound maps sql.ErrNoRows to ErrNotFound and leaves every other error
// as is.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
