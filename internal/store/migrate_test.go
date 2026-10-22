package store

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// onlyFirst is the schema as M1 released it.
func onlyFirst(t *testing.T) fstest.MapFS {
	t.Helper()
	body, err := fs.ReadFile(migrations, "migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{"migrations/0001_init.sql": {Data: body}}
}

func count(t *testing.T, s *Store, table string) int64 {
	t.Helper()
	var n int64
	if err := s.w.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestMigration0002KeepsEveryRowAndTheIDCounter upgrades an M1 database
// with links, assignments and check-ins. The rebuild of roles must lose
// nothing (data-model boundary, process section 5) and must not give a
// deleted role's id again (access-control.md section 5, rule 6).
func TestMigration0002KeepsEveryRowAndTheIDCounter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m1.db")
	s, err := open(ctx, path, true, onlyFirst(t))
	if err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.w.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO events (id, name, event_date, timezone, status, recovery_code_hash, created_at)
		VALUES (1, 'e', '2027-06-06', 'Asia/Taipei', 'Draft', ?, 0)`, hash("r"))
	for id, typ := range map[int]string{1: "Master", 2: "Stylist", 3: "Media"} {
		mustExec(`INSERT INTO roles (id, event_id, name, type) VALUES (?, 1, ?, ?)`, id, typ, typ)
	}
	mustExec(`INSERT INTO access_tokens (role_id, token_hash, created_at) VALUES (1, ?, 0), (2, ?, 0)`, hash("m"), hash("s"))
	mustExec(`INSERT INTO cues (id, event_id, seq, title, scheduled_start, duration_min) VALUES (1, 1, 1, 'c', 0, 10)`)
	mustExec(`INSERT INTO cue_roles (cue_id, role_id, role_note) VALUES (1, 2, 'note')`)
	mustExec(`INSERT INTO check_ins (role_id, at, by_role_id) VALUES (2, 0, 2)`)
	mustExec(`DELETE FROM roles WHERE id = 3`) // the highest id, deleted
	s.Close()

	s, err = Open(ctx, path, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()
	for table, want := range map[string]int64{"roles": 2, "access_tokens": 2, "cue_roles": 1, "check_ins": 1, "events": 1} {
		if got := count(t, s, table); got != want {
			t.Errorf("%s: %d rows, want %d", table, got, want)
		}
	}
	var fk int
	if err := s.w.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys after migration = %d (%v), want 1", fk, err)
	}
	res, err := s.w.ExecContext(ctx, `INSERT INTO roles (event_id, name, type, view) VALUES (1, 'n', 'Florist', 'Glance')`)
	if err != nil {
		t.Fatalf("type is no longer limited by the schema: %v", err)
	}
	if id, _ := res.LastInsertId(); id <= 3 {
		t.Errorf("new role id = %d, want > 3: a deleted id was given again", id)
	}
	// References and their cascade still work after the rename.
	if _, err := s.w.ExecContext(ctx, `DELETE FROM roles WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if got := count(t, s, "access_tokens"); got != 1 {
		t.Errorf("access_tokens after deleting role 2 = %d, want 1 (cascade)", got)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO events (name, event_date, timezone, status, recovery_code_hash, created_at, organization_id)
		VALUES ('e2', '2027-06-07', 'Asia/Taipei', 'Draft', ?, 0, NULL)`, hash("r2")); err != nil {
		t.Errorf("organization_id: %v", err)
	}
}

func TestMigrationRefusesAnUnmarkedDrop(t *testing.T) {
	fsys := onlyFirst(t)
	fsys["migrations/0002_drop.sql"] = &fstest.MapFile{Data: []byte("DROP TABLE check_ins;")}
	if _, err := open(ctx, filepath.Join(t.TempDir(), "x.db"), true, fsys); !errors.Is(err, errUnmarkedDrop) {
		t.Fatalf("err = %v, want errUnmarkedDrop", err)
	}
}

func TestRebuildThatLosesRowsIsRolledBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	s, err := open(ctx, path, true, onlyFirst(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO events (id, name, event_date, timezone, status, recovery_code_hash, created_at)
		VALUES (1, 'e', '2027-06-06', 'Asia/Taipei', 'Draft', ?, 0)`, hash("r")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	fsys := onlyFirst(t)
	fsys["migrations/0002_lossy.sql"] = &fstest.MapFile{Data: []byte(rebuildMarker + "\nDELETE FROM events;")}
	if _, err := open(ctx, path, false, fsys); !errors.Is(err, errRowsChanged) {
		t.Fatalf("err = %v, want errRowsChanged", err)
	}
	s, err = open(ctx, path, false, onlyFirst(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := count(t, s, "events"); got != 1 {
		t.Errorf("events = %d, want 1: nothing may be committed", got)
	}
}

// ADR-0019: rolling back to the M1 release must still open the database.
func TestM1ReleaseOpensTheMigratedDatabase(t *testing.T) {
	f := newFixture(t) // migrated to the latest schema
	f.s.Close()
	s, err := open(ctx, f.path, false, onlyFirst(t))
	if err != nil {
		t.Fatalf("M1 release: %v", err)
	}
	defer s.Close()
	if _, err := s.LookupToken(ctx, hash("master"), at(12, 0)); err != nil {
		t.Errorf("lookup with the M1 release: %v", err)
	}
}
