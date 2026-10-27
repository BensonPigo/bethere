// Package store keeps BeThere's state in SQLite (tech-stack.md 2.1; ADR-0019).
//
// Writes go through a single connection that takes the write lock at
// BEGIN; reads use a separate read-only pool, so polling never queues
// behind a slow write (tech-stack.md 2.1「連線」). Lookups distinguish "no such token" from any
// other failure, because only a confirmed miss may be reported to a
// client as an invalid link (tech-stack.md 2.1「逾時與錯誤」). The store never creates or serves an
// empty database except on an explicit first deployment (tech-stack.md 2.1「不自動建庫」).
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
	"regexp"
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

// Open opens the database at path (tech-stack.md 2.1「不自動建庫」):
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

// Healthy runs one read-only query on the read pool, so the health check
// is not held up by the writer (tech-stack.md 2.1「健康檢查」).
func (s *Store) Healthy(ctx context.Context) error {
	var n int
	return s.r.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&n)
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
// DSN so each pooled connection gets it. A Windows path (C:\x) becomes
// /C:/x, the form SQLite's URI parser takes for a drive letter.
func dsn(abs, mode string, params ...string) string {
	common := []string{"mode=" + mode, "_pragma=busy_timeout(5000)", "_pragma=foreign_keys(1)"}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: strings.Join(append(common, params...), "&")}
	return u.String()
}

// migrate applies numbered migrations above current (the database's
// user_version). A database newer than this binary is left alone so a
// rollback can still start (ADR-0019). Released migrations are frozen; schema
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
		if err := applyMigration(ctx, db, string(body), n); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// rebuildMarker starts a migration that rebuilds a table. With foreign keys
// on, dropping a table deletes every row that references it through ON
// DELETE CASCADE, and PRAGMA foreign_key_check cannot notice rows that are
// gone; such a migration runs with foreign keys off instead (ADR-0019).
const rebuildMarker = "-- bethere:foreign_keys=off"

// dropTable finds a DROP TABLE however it is spaced or cased.
var dropTable = regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)

var (
	errUnmarkedDrop = errors.New("migration drops a table but does not start with " + rebuildMarker)
	errForeignKeys  = errors.New("could not switch foreign keys as required")
	errRowsChanged  = errors.New("migration changed the row count of an existing table")
	errDanglingRefs = errors.New("migration left rows whose foreign keys point nowhere")
)

// applyMigration runs one migration and sets user_version in one
// transaction, on one pinned connection so the foreign key setting applies
// to it. A rebuild must keep every existing table's row count and leave no
// broken reference, or nothing is committed.
func applyMigration(ctx context.Context, db *sql.DB, body string, version int) (err error) {
	rebuild := strings.HasPrefix(body, rebuildMarker)
	if !rebuild && dropTable.MatchString(body) {
		return errUnmarkedDrop
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if rebuild {
		if err := setForeignKeys(ctx, conn, false); err != nil {
			return err
		}
		// Back on before the connection returns to the pool; if that
		// fails the error stops startup and connect closes the pool.
		defer func() {
			if fkErr := setForeignKeys(ctx, conn, true); err == nil {
				err = fkErr
			}
		}()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var before map[string]int64
	if rebuild {
		if before, err = rowCounts(ctx, tx); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if rebuild {
		after, err := rowCounts(ctx, tx)
		if err != nil {
			return err
		}
		for table, n := range before {
			if m, ok := after[table]; !ok || m != n {
				return fmt.Errorf("%w: %s had %d rows, now %d", errRowsChanged, table, n, m)
			}
		}
		rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		dangling := rows.Next()
		rows.Close()
		if dangling {
			return errDanglingRefs
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}

// setForeignKeys switches the pragma and reads it back: the pragma is a
// silent no-op inside a transaction.
func setForeignKeys(ctx context.Context, conn *sql.Conn, on bool) error {
	want := 0
	if on {
		want = 1
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA foreign_keys = %d", want)); err != nil {
		return err
	}
	var got int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&got); err != nil {
		return err
	}
	if got != want {
		return errForeignKeys
	}
	return nil
}

// rowCounts counts the rows of every table but SQLite's own.
func rowCounts(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(tables))
	for _, t := range tables {
		var n int64
		// Table names come from sqlite_master, not from input.
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM "`+strings.ReplaceAll(t, `"`, `""`)+`"`).Scan(&n); err != nil {
			return nil, err
		}
		counts[t] = n
	}
	return counts, nil
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
