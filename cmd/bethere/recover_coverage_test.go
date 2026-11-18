package main

import (
	"context"
	"database/sql"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/store"

	_ "modernc.org/sqlite"
)

// execSQL runs statements on the database file outside the program, as a
// fault of the disk or a hand edit would.
func execSQL(t *testing.T, db string, stmts ...string) {
	t.Helper()
	d, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func codeStillWorks(t *testing.T, db, code string) bool {
	t.Helper()
	h, err := auth.HashRecoveryCode(code)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, _, err = st.LookupRecoveryCode(context.Background(), h)
	return err == nil
}

// US-O-4.4: when storing fails after the new code was printed, the command
// exits with an error that says the printed code is void, and the code in
// hand (paper or link) still works.
func TestUS_O_4_4_WriteFailureVoidsThePrintedCode(t *testing.T) {
	for _, input := range []string{"link", "code"} {
		t.Run(input, func(t *testing.T) {
			db := newDB(t)
			link, token, old := newEvent(t, db)
			execSQL(t, db, `CREATE TRIGGER no_reset BEFORE UPDATE OF recovery_code_hash ON events
				BEGIN SELECT RAISE(ABORT, 'database or disk is full'); END`)
			in := old
			if input == "link" {
				in = link
			}
			r := runWith(in+"\n", nil, "reset-recovery-code", "-db", db)
			if r.code != 1 {
				t.Fatalf("exit %d, want 1; stderr %q", r.code, r.stderr)
			}
			if !strings.Contains(r.stderr, "does not work") || !strings.Contains(r.stderr, "nothing was changed") {
				t.Errorf("stderr %q does not say the printed code is void", r.stderr)
			}
			printed := codeRE.FindStringSubmatch(r.stdout)
			if printed == nil {
				t.Fatalf("no code printed in %q", r.stdout)
			}
			if strings.Contains(r.stdout, "Reset:") {
				t.Errorf("claims success: %q", r.stdout)
			}
			if codeStillWorks(t, db, printed[1]) {
				t.Error("the printed code works although storing failed")
			}
			if !codeStillWorks(t, db, old) {
				t.Error("the code in hand stopped working")
			}
			st, err := store.Open(context.Background(), db, false)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err := st.LookupToken(context.Background(), auth.HashToken(token), time.Now()); err != nil {
				t.Errorf("the Master link stopped working: %v", err)
			}
		})
	}
}

// US-O-4.4: an ended event is refused with an error that says nothing was
// changed and the printed code does not work; the old code stays as it was.
func TestUS_O_4_4_EndedEventIsRefused(t *testing.T) {
	db := newDB(t)
	_, _, old := newEvent(t, db)
	execSQL(t, db, `UPDATE events SET event_date = '2020-01-01'`)
	r := runWith(old+"\n", nil, "reset-recovery-code", "-db", db)
	if r.code != 1 || !strings.Contains(r.stderr, "has ended") || !strings.Contains(r.stderr, "does not work") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	printed := codeRE.FindStringSubmatch(r.stdout)
	if printed == nil || codeStillWorks(t, db, printed[1]) {
		t.Errorf("printed %v must not work", printed)
	}
	if !codeStillWorks(t, db, old) {
		t.Error("the old code stopped working")
	}
}

// US-O-13.3: the load test is never part of the real executable: none of
// its packages is a dependency of cmd/bethere.
func TestUS_O_13_3_LoadTestIsNotInTheRealBinary(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("no go tool: %v", err)
	}
	out, err := exec.Command(goBin, "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	deps := string(out)
	if !strings.Contains(deps, "github.com/BensonPigo/bethere/internal/store") {
		t.Fatalf("the dependency list looks wrong, it does not even hold the store:\n%s", deps)
	}
	for _, line := range strings.Split(deps, "\n") {
		if strings.Contains(line, "loadtest") {
			t.Errorf("cmd/bethere depends on %s", line)
		}
	}
}
