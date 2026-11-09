package store

// The database file and its connections: opening, initialising, migrating,
// the single writer and the readers (tech-stack.md 2.1).

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

func TestOpenRefusesMissingOrExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bethere.db")
	if _, err := Open(ctx, path, false); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("missing file without init: err = %v, want ErrNoDatabase", err)
	}
	s, err := Open(ctx, path, true)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	s.Close()
	if _, err := Open(ctx, path, true); !errors.Is(err, ErrDatabaseExists) {
		t.Fatalf("init with existing file: err = %v, want ErrDatabaseExists", err)
	}
	s, err = Open(ctx, path, false)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s.Close()
}

func TestMigrationsAndPragmas(t *testing.T) {
	s, path := newStore(t)
	var version int
	if err := s.w.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 8 {
		t.Fatalf("user_version = %d (%v), want 8", version, err)
	}
	for _, p := range []struct {
		db   string
		name string
		want string
	}{
		{"w", "journal_mode", "wal"},
		{"w", "secure_delete", "1"},
		{"w", "foreign_keys", "1"},
		{"w", "busy_timeout", "5000"},
		{"r", "query_only", "1"},
		{"r", "foreign_keys", "1"},
		{"r", "busy_timeout", "5000"},
	} {
		db := s.w
		if p.db == "r" {
			db = s.r
		}
		var got string
		if err := db.QueryRow("PRAGMA " + p.name).Scan(&got); err != nil || got != p.want {
			t.Errorf("%s PRAGMA %s = %q (%v), want %q", p.db, p.name, got, err, p.want)
		}
	}
	if _, err := s.r.Exec(`INSERT INTO events (name, event_date, timezone, status, recovery_code_hash, created_at)
		VALUES ('x', '2027-06-06', 'UTC', 'Draft', x'00', 0)`); err == nil {
		t.Error("read-only pool accepted a write")
	}

	// A database from a newer binary still opens (ADR-0019).
	if _, err := s.w.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(ctx, path, false)
	if err != nil {
		t.Fatalf("open newer database: %v", err)
	}
	s2.Close()
}

// Polling reads must not wait for a write transaction (tech-stack.md 2.1「連線」).
func TestReadsDoNotWaitForWriter(t *testing.T) {
	f := newFixture(t)
	tx, err := f.s.w.BeginTx(ctx, nil) // _txlock=immediate: holds the write lock now
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE cues SET title = 'changing' WHERE id = ?`, f.cueIDs[0]); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := f.s.LookupToken(ctx, hash("master"), at(12, 0))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read during write: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read blocked behind the write transaction")
	}
	cues, err := f.s.Cues(ctx, f.eventID)
	if err != nil || cues[0].Title != "cue" {
		t.Errorf("read saw uncommitted data: %+v (%v)", cues[0], err)
	}
}

func TestMigrationNumbersMustBeContiguous(t *testing.T) {
	s, _ := newStore(t)
	gap := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte("SELECT 1")},
		"migrations/0003_c.sql": {Data: []byte("SELECT 1")},
	}
	if err := migrate(ctx, s.w, gap, 0); err == nil {
		t.Error("migrations with a numbering gap were accepted")
	}
}

func TestWriterIsASingleConnection(t *testing.T) {
	s, _ := newStore(t)
	if got := s.w.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("writer MaxOpenConnections = %d, want 1", got)
	}
}

// Another process (a server-side subcommand) may write between this
// store's read and write. Taking the write lock at BEGIN makes the store
// wait and then see that write, so a stale client gets a version conflict
// instead of a "database is busy" failure (tech-stack.md 2.1「連線」).
func TestWriteWaitsForAnotherProcess(t *testing.T) {
	f := newFixture(t)
	f.start(t)
	v := f.version(t)
	other, err := Open(ctx, f.path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	tx, err := other.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE events SET version = version + 1 WHERE id = ?`, f.eventID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 0), advance(at(10, 0)))
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrVersionConflict) {
		t.Errorf("err = %v, want ErrVersionConflict", err)
	}
}

// Paths with URI metacharacters must open exactly that file (round 1:
// "?" and "#" cut the name short and created another, empty database).
func TestOpenUsesTheExactPath(t *testing.T) {
	for _, name := range []string{"data?v1.db", "h#x.db", "p%20x.db", "with space.db", "婚禮.db"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.Contains(name, "?") {
				t.Skip(`"?" is not allowed in Windows file names`)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			s, err := Open(ctx, path, true)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			if _, err := s.CreateEvent(ctx, NewEvent{
				Name: "x", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
				MasterName: "總召", MasterTokenHash: hash("tok"), RecoveryCodeHash: hash("rec"),
			}, at(0, 0)); err != nil {
				t.Fatal(err)
			}
			s.Close()
			// The data is in the named file itself, not somewhere the URI
			// resolved to outside dir.
			if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
				t.Fatalf("named file missing or empty: %v", err)
			}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if n := e.Name(); n != name && n != name+"-wal" && n != name+"-shm" {
					t.Errorf("stray file %q created", n)
				}
			}
			s, err = Open(ctx, path, false)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer s.Close()
			if _, err := s.LookupToken(ctx, hash("tok"), at(12, 0)); err != nil {
				t.Errorf("token not found after reopen: %v", err)
			}
		})
	}
}

// SQLite itself must refuse to create a missing file, closing the gap
// between the existence check and the first connection.
func TestDSNNeverCreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone.db")
	db, err := sql.Open("sqlite", dsn(path, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err == nil {
		t.Error("opening a missing file succeeded")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("file was created: %v", err)
	}

	// Positive control: the same DSN opens an existing database, so the
	// case above cannot pass merely because the URI is malformed.
	_, existing := newStore(t)
	ok, err := sql.Open("sqlite", dsn(existing, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer ok.Close()
	if err := ok.Ping(); err != nil {
		t.Errorf("opening an existing file failed: %v", err)
	}
}

// Unix paths must produce exactly the DSN they did before Windows support;
// Windows drive paths gain the leading slash SQLite expects.
func TestDSNForm(t *testing.T) {
	const q = "?mode=rw&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if got, want := dsn("/tmp/x.db", "rw"), "file:///tmp/x.db"+q; got != want {
		t.Errorf("unix: got %q, want %q", got, want)
	}
	if runtime.GOOS == "windows" {
		if got, want := dsn(`C:\x\y.db`, "rw"), "file:///C:/x/y.db"+q; got != want {
			t.Errorf("windows: got %q, want %q", got, want)
		}
	}
}

// A file left by a failed restore is refused with a message that warns
// against BETHERE_INIT, and is not touched (rounds 1 and 2).
func TestOpenRefusesUninitializedFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "bethere.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty+"-wal", []byte("leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ctx, empty, false)
	if !errors.Is(err, ErrEmptyDatabase) || !strings.Contains(err.Error(), "do NOT set BETHERE_INIT") {
		t.Errorf("err = %v, want ErrEmptyDatabase with a warning", err)
	}
	if info, err := os.Stat(empty); err != nil || info.Size() != 0 {
		t.Errorf("database file changed: %v, %v", info, err)
	}
	if b, err := os.ReadFile(empty + "-wal"); err != nil || string(b) != "leftover" {
		t.Errorf("leftover WAL changed: %q, %v", b, err)
	}
}

func TestFailedInitRemovesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bethere.db")
	bad := fstest.MapFS{"migrations/0001_bad.sql": {Data: []byte("THIS IS NOT SQL")}}
	if _, err := open(ctx, path, true, bad); err == nil {
		t.Fatal("init with a broken migration succeeded")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("database file left behind: %v", err)
	}
}

// A database that cannot be written must stop the service at startup, not
// at the Master's first live operation (round 2).
func TestOpenRefusesAReadOnlyDatabase(t *testing.T) {
	// No other handle to the file may stay open in this process, or SQLite
	// reuses its descriptor and the permission change goes unnoticed.
	path := filepath.Join(t.TempDir(), "bethere.db")
	s, err := Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	if s, err := Open(ctx, path, false); err == nil {
		s.Close()
		t.Error("read-only database opened")
	} else if !strings.Contains(err.Error(), "not writable") {
		t.Errorf("err = %v, want a not-writable error", err)
	}
}

// /healthz asks the database through the read pool (tech-stack.md 2.1「健康檢查」).
func TestHealthyReadsTheDatabase(t *testing.T) {
	st, _ := newStore(t)
	if err := st.Healthy(t.Context()); err != nil {
		t.Fatalf("healthy store: %v", err)
	}
	st.r.Close()
	if err := st.Healthy(t.Context()); err == nil {
		t.Fatal("a closed read pool must not be healthy")
	}
}
