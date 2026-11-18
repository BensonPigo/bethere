package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// referencingTables lists, from sqlite_master and the foreign keys, every
// table whose rows belong to an event: those that name events directly and
// those that name one of such tables (US-O-5.1, NFR-5). It is computed from
// the schema so a table added later cannot be forgotten by this test.
func referencingTables(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.r.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		all = append(all, n)
	}
	rows.Close()
	parents := map[string][]string{}
	for _, tbl := range all {
		fk, err := s.r.Query(`SELECT "table" FROM pragma_foreign_key_list(?)`, tbl)
		if err != nil {
			t.Fatal(err)
		}
		for fk.Next() {
			var p string
			if err := fk.Scan(&p); err != nil {
				t.Fatal(err)
			}
			parents[tbl] = append(parents[tbl], p)
		}
		fk.Close()
	}
	owned := map[string]bool{"events": true}
	for changed := true; changed; {
		changed = false
		for _, tbl := range all {
			if owned[tbl] {
				continue
			}
			for _, p := range parents[tbl] {
				if owned[p] {
					owned[tbl] = true
					changed = true
				}
			}
		}
	}
	var out []string
	for tbl := range owned {
		out = append(out, tbl)
	}
	slices.Sort(out)
	return out
}

func rowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.r.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// fillEverything gives the fixture's event at least one row in every table
// that belongs to an event, each carrying a recognisable string.
func fillEverything(t *testing.T, f fixture, secret string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.s.w.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := f.s.AssignRole(ctx, f.cueIDs[0], f.stylist, secret+"-note", f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO check_ins (role_id, at, by_role_id) VALUES (?, 1, ?)`, f.stylist, f.master)
	exec(`INSERT INTO cue_acks (cue_id, role_id, at) VALUES (?, ?, 1)`, f.cueIDs[0], f.stylist)
	exec(`INSERT INTO tasks (event_id, title, created_at, updated_at) VALUES (?, ?, 1, 1)`, f.eventID, secret+"-task")
	exec(`INSERT INTO guests (event_id, seq, name, table_label) VALUES (?, 1, ?, 'A1')`, f.eventID, secret+"-guest")
	exec(`INSERT INTO checklist_items (event_id, seq, title) VALUES (?, 1, ?)`, f.eventID, secret+"-item")
	exec(`INSERT INTO organizations (name, created_at) VALUES (?, 1)`, "公司")
	exec(`INSERT INTO members (organization_id, email, created_at) VALUES (1, ?, 1)`, "m@x.tw")
	exec(`INSERT INTO console_tokens (member_id, event_id, token_hash, created_at) VALUES (1, ?, ?, 1)`, f.eventID, hash(secret+"-console"))
}

// US-O-5.1, NFR-5: the purge leaves no row of an ended event in any table
// that belongs to it; the list of tables is found in the schema.
func TestUS_O_5_1_PurgeLeavesNoRowInAnyEventTable(t *testing.T) {
	f := newFixture(t)
	fillEverything(t, f, "秘密")
	tables := referencingTables(t, f.s)
	want := []string{"access_tokens", "action_logs", "check_ins", "checklist_items", "console_tokens", "cue_acks",
		"cue_roles", "cues", "events", "guests", "roles", "tasks"}
	if !slices.Equal(tables, want) {
		t.Fatalf("tables that belong to an event = %v, want %v: a new one needs a row in fillEverything and a place here", tables, want)
	}
	for _, tbl := range tables {
		if rowCount(t, f.s, tbl) == 0 {
			t.Fatalf("test setup: %s has no row before the purge", tbl)
		}
	}
	e, _, _ := f.s.Event(ctx, f.eventID)
	if ids, err := f.s.PurgeEnded(ctx, e.PurgeAt()); err != nil || !slices.Equal(ids, []int64{f.eventID}) {
		t.Fatalf("purge = %v, %v", ids, err)
	}
	for _, tbl := range tables {
		if n := rowCount(t, f.s, tbl); n != 0 {
			t.Errorf("%s has %d rows left", tbl, n)
		}
	}
}

// US-O-5.C1 (the part that runs here): after the purge and a checkpoint the
// event name and a guest's name are in neither the database file nor the
// WAL. The same search finds them before the purge, so it can find them.
func TestUS_O_5_C1_PurgedStringsAreGoneFromTheFiles(t *testing.T) {
	f := newFixture(t)
	const eventName, guest = "獨特活動名稱XYZ", "賓客甲乙丙"
	fillEverything(t, f, "機密字串")
	if _, err := f.s.w.Exec(`UPDATE events SET name = ?`, eventName); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.w.Exec(`UPDATE guests SET name = ?`, guest); err != nil {
		t.Fatal(err)
	}
	needles := []string{eventName, guest, "機密字串"}
	search := func() []string {
		if _, err := f.s.w.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
		var found []string
		for _, file := range []string{f.path, f.path + "-wal"} {
			b, err := os.ReadFile(file)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range needles {
				if bytes.Contains(b, []byte(n)) && !slices.Contains(found, n) {
					found = append(found, n)
				}
			}
		}
		return found
	}
	if before := search(); len(before) != len(needles) {
		t.Fatalf("test setup: before the purge the files hold %v, want all of %v", before, needles)
	}
	e, _, _ := f.s.Event(ctx, f.eventID)
	if _, err := f.s.PurgeEnded(ctx, e.PurgeAt()); err != nil {
		t.Fatal(err)
	}
	if after := search(); len(after) != 0 {
		t.Errorf("still in the database or WAL file after the purge and a checkpoint: %v", after)
	}
}

// A purge that fails half way leaves every event whole (NFR-5, data never
// half deleted): the failing delete is made with a trigger of the test's
// own; the event deleted before it comes back with all its rows.
func TestUS_O_5_1_FailedPurgeLeavesNothingHalfDeleted(t *testing.T) {
	f := newFixture(t)
	fillEverything(t, f, "甲")
	second, err := f.s.CreateEvent(ctx, NewEvent{Name: "乙", Date: domain.Date{Year: 2027, Month: time.June, Day: 6},
		TimeZone: "Asia/Taipei", MasterName: "總召", MasterTokenHash: hash("m2"), RecoveryCodeHash: hash("r2")}, at(0, 0).AddDate(0, -1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.w.Exec(`CREATE TRIGGER fail_purge BEFORE DELETE ON events WHEN old.id = ` + fmt.Sprint(second) +
		` BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	tables := referencingTables(t, f.s)
	before := map[string]int{}
	for _, tbl := range tables {
		before[tbl] = rowCount(t, f.s, tbl)
	}
	e, _, _ := f.s.Event(ctx, f.eventID)
	ids, err := f.s.PurgeEnded(ctx, e.PurgeAt())
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("purge = %v, %v; want the failure", ids, err)
	}
	if len(ids) != 0 {
		t.Errorf("reported %v deleted although the transaction failed", ids)
	}
	for _, tbl := range tables {
		if n := rowCount(t, f.s, tbl); n != before[tbl] {
			t.Errorf("%s: %d rows after the failed purge, was %d (half deleted)", tbl, n, before[tbl])
		}
	}
	if _, _, err := f.s.Event(ctx, f.eventID); err != nil {
		t.Errorf("the first event is gone after a failed purge: %v", err)
	}
	// Once the fault is gone, the next run deletes both.
	if _, err := f.s.w.Exec(`DROP TRIGGER fail_purge`); err != nil {
		t.Fatal(err)
	}
	if ids, err := f.s.PurgeEnded(ctx, e.PurgeAt()); err != nil || len(ids) != 2 {
		t.Errorf("rerun = %v, %v; want both deleted", ids, err)
	}
}
