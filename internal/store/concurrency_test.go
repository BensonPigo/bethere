package store

// Races between devices and failures halfway through a transaction, added
// from the test audit. A failure is made by a trigger that aborts the last
// write of the transaction, so the test shows that everything written before
// it is rolled back, without changing the production code.

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// race runs fn on n goroutines released together and returns their errors.
func race(n int, fn func(i int) error) []error {
	var (
		wg    sync.WaitGroup
		ready sync.WaitGroup
		start = make(chan struct{})
		errs  = make([]error, n)
	)
	wg.Add(n)
	ready.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			errs[i] = fn(i)
		}()
	}
	ready.Wait()
	close(start)
	wg.Wait()
	return errs
}

// wantOneWinner checks that exactly one call succeeded and the others failed
// with loser.
func wantOneWinner(t *testing.T, what string, errs []error, loser error) {
	t.Helper()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, loser):
			t.Errorf("%s: a loser got %v, want %v", what, err, loser)
		}
	}
	if wins != 1 {
		t.Errorf("%s: %d calls succeeded, want exactly 1 (%v)", what, wins, errs)
	}
}

func (f fixture) inProgress(t *testing.T) int {
	t.Helper()
	n := 0
	for _, st := range f.statuses(t) {
		if st == domain.InProgress {
			n++
		}
	}
	return n
}

// US-M-1 edge: two Master devices advancing from the same version: one wins,
// the other is told its version is old, and one cue is in progress.
func TestUS_M_1_ConcurrentAdvanceFromTheSameVersionHasOneWinner(t *testing.T) {
	f := newFixture(t)
	for round := 1; round <= 3; round++ {
		v := f.version(t)
		errs := race(8, func(int) error {
			_, _, err := f.s.Advance(ctx, f.eventID, v, f.masterBy, at(10, round))
			return err
		})
		wantOneWinner(t, fmt.Sprintf("round %d", round), errs, ErrVersionConflict)
		if got := f.version(t); got != v+1 {
			t.Errorf("round %d: version %d → %d, want +1", round, v, got)
		}
		if n := f.inProgress(t); n != 1 {
			t.Errorf("round %d: %d cues in progress, want 1", round, n)
		}
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE event_id = ? AND type = 'cue.advance'`, f.eventID); n != 3 {
		t.Errorf("%d advance logs, want 3: one per winning call", n)
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE event_id = ? AND type = 'start-event'`, f.eventID); n != 1 {
		t.Errorf("%d start-event logs, want 1", n)
	}
}

// US-M-8 edge: two devices inserting a cue from the same version: one wins and
// seq stays 1..n.
func TestUS_M_8_ConcurrentInsertFromTheSameVersionHasOneWinner(t *testing.T) {
	f := newFixture(t)
	v := f.version(t)
	errs := race(8, func(i int) error {
		_, err := f.s.InsertCue(ctx, f.eventID, v, NewCue{
			AfterCueID: f.cueIDs[0], Start: at(10, 20), DurationMin: 10, Title: fmt.Sprintf("插入%d", i),
		}, f.masterBy, at(0, 0))
		return err
	})
	wantOneWinner(t, "insert", errs, ErrVersionConflict)
	cues, err := f.s.Cues(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 4 {
		t.Fatalf("%d cues, want 4", len(cues))
	}
	for i, c := range cues {
		if c.Seq != i+1 {
			t.Errorf("cue %d has seq %d", i, c.Seq)
		}
	}
	if got := f.version(t); got != v+1 {
		t.Errorf("version %d → %d, want +1", v, got)
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE event_id = ? AND type = 'rundown.insert'`, f.eventID); n != 1 {
		t.Errorf("%d insert logs, want 1", n)
	}
}

// US-M-11 edge: two requests adding the same name: one role. Two requests
// adding the 50th role: one of them.
func TestUS_M_11_ConcurrentAddRoleKeepsNamesUniqueAndTheLimit(t *testing.T) {
	f := newFixture(t)
	roles := func() int {
		return countRows(t, f.s, `SELECT count(*) FROM roles WHERE event_id = ?`, f.eventID)
	}
	before := roles()
	errs := race(8, func(i int) error {
		_, err := f.s.CreateRole(ctx, f.eventID, NewRole{Name: "伴娘", View: domain.Glance, ChecksIn: true},
			hash(fmt.Sprintf("same-%d", i)), f.masterBy, at(0, 0))
		return err
	})
	wantOneWinner(t, "same name", errs, ErrDuplicateName)
	if got := roles(); got != before+1 {
		t.Errorf("%d roles, want %d", got, before+1)
	}

	for i := 0; roles() < domain.MaxRoles-1; i++ {
		if _, err := f.s.AddRole(ctx, f.eventID, fmt.Sprintf("填充%d", i), domain.Media, f.masterBy, at(0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	errs = race(8, func(i int) error {
		_, err := f.s.CreateRole(ctx, f.eventID, NewRole{Name: fmt.Sprintf("最後%d", i), View: domain.Glance, ChecksIn: true},
			hash(fmt.Sprintf("last-%d", i)), f.masterBy, at(0, 0))
		return err
	})
	wantOneWinner(t, "last place", errs, ErrTooManyRoles)
	if got := roles(); got != domain.MaxRoles {
		t.Errorf("%d roles, want exactly %d", got, domain.MaxRoles)
	}
}

// failNext makes the next write of a log of typ fail, as the last step of a
// transaction, and returns the function that removes the trigger.
func failNext(t *testing.T, path, typ string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TRIGGER inject_failure BEFORE INSERT ON action_logs WHEN NEW.type = '` + typ + `'
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := db.Exec(`DROP TRIGGER inject_failure`); err != nil {
			t.Fatal(err)
		}
	}
}

// US-M-7.1: a reissue whose new link cannot be written leaves the old link
// working: the revoke and the new link are one transaction.
func TestUS_M_7_1_ReissueFailureKeepsTheOldLink(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueToken(ctx, f.stylist, hash("old"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	other, err := f.s.AddRole(ctx, f.eventID, "新秘B", domain.Stylist, f.masterBy, at(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.IssueToken(ctx, other, hash("taken"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	logs := countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE event_id = ?`, f.eventID)
	// A link another role already has cannot be inserted: the failure comes
	// after the old link was revoked.
	if err := f.s.ReissueLink(ctx, f.eventID, f.stylist, hash("taken"), f.masterBy, at(2, 0)); err == nil {
		t.Fatal("reissue with a link in use succeeded")
	}
	if _, err := f.s.LookupToken(ctx, hash("old"), at(12, 0)); err != nil {
		t.Errorf("the old link after a failed reissue: %v", err)
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE event_id = ?`, f.eventID); n != logs {
		t.Errorf("%d logs written by a failed reissue", n-logs)
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM access_tokens WHERE role_id = ? AND revoked_at IS NULL`, f.stylist); n != 1 {
		t.Errorf("%d active links, want exactly the old one", n)
	}
	// The same call with a fresh link works afterwards.
	if err := f.s.ReissueLink(ctx, f.eventID, f.stylist, hash("new"), f.masterBy, at(3, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.LookupToken(ctx, hash("old"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("old link after the reissue: %v", err)
	}
	// A failing log write, the last step, undoes the revoke and the new link too.
	remove := failNext(t, f.path, "token.reissue")
	if err := f.s.ReissueLink(ctx, f.eventID, f.stylist, hash("newer"), f.masterBy, at(4, 0)); err == nil {
		t.Fatal("reissue succeeded with the log failing")
	}
	if _, err := f.s.LookupToken(ctx, hash("new"), at(12, 0)); err != nil {
		t.Errorf("the link before the failed reissue: %v", err)
	}
	if _, err := f.s.LookupToken(ctx, hash("newer"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("the new link of a failed reissue works: %v", err)
	}
	remove()
}

// US-M-12.3, US-M-12.6: if the import fails at its last step, the old rundown,
// its assignments and the roles are exactly as before, and no role is created.
func TestUS_M_12_3_ReplaceRundownFailureRollsBackEverything(t *testing.T) {
	f := newFixture(t)
	if err := f.s.AssignRole(ctx, f.cueIDs[0], f.stylist, "新娘房", f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	dayBefore := at(0, 0).Add(-time.Minute)
	rows := []ImportRow{
		{Line: 2, Seq: 1, Title: "新流程一", StartHour: 8, DurationMin: 30, Roles: []string{"新秘", "攝影"}, RoleNote: "備註"},
		{Line: 3, Seq: 2, Title: "新流程二", StartHour: 9, DurationMin: 30, Roles: []string{"攝影"}},
	}
	newRoles := []NewRole{{Name: "攝影", View: domain.Glance, ChecksIn: true}}

	cuesBefore, err := f.s.Cues(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	v := f.version(t)
	counts := func() [4]int {
		return [4]int{
			countRows(t, f.s, `SELECT count(*) FROM cues WHERE event_id = ?`, f.eventID),
			countRows(t, f.s, `SELECT count(*) FROM roles WHERE event_id = ?`, f.eventID),
			countRows(t, f.s, `SELECT count(*) FROM cue_roles`),
			countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE event_id = ?`, f.eventID),
		}
	}
	before := counts()

	remove := failNext(t, f.path, "rundown.import")
	if err := f.s.ReplaceRundown(ctx, f.eventID, v, rows, newRoles, f.masterBy, dayBefore); err == nil {
		t.Fatal("import succeeded with its log failing")
	}
	cuesAfter, err := f.s.Cues(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cuesBefore, cuesAfter) {
		t.Errorf("cues changed:\nbefore %+v\nafter  %+v", cuesBefore, cuesAfter)
	}
	if after := counts(); after != before {
		t.Errorf("cues, roles, assignments, logs = %v, want %v", after, before)
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM roles WHERE name = '攝影'`); n != 0 {
		t.Error("the new role was created")
	}
	if got := f.version(t); got != v {
		t.Errorf("version %d → %d", v, got)
	}

	// Without the failure the same call goes through, so it was the failure.
	remove()
	if err := f.s.ReplaceRundown(ctx, f.eventID, v, rows, newRoles, f.masterBy, dayBefore); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM roles WHERE name = '攝影'`); n != 1 {
		t.Error("the role was not created")
	}
	if got := f.version(t); got != v+1 {
		t.Errorf("version %d → %d, want +1", v, got)
	}
}

// US-M-11.5: if deleting a role fails at its last step, the role, its link,
// assignments and check-ins are all still there.
func TestUS_M_11_5_DeleteRoleFailureRollsBackEverything(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.AssignRole(ctx, f.cueIDs[0], f.stylist, "新娘房", f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetCheckIn(ctx, f.eventID, f.stylist, true, f.masterBy, at(2, 0)); err != nil {
		t.Fatal(err)
	}
	dayBefore := at(0, 0).Add(-time.Minute)
	v := f.version(t)
	count := func(q string) int { return countRows(t, f.s, q, f.stylist) }
	const (
		qRole    = `SELECT count(*) FROM roles WHERE id = ?`
		qLink    = `SELECT count(*) FROM access_tokens WHERE role_id = ? AND revoked_at IS NULL`
		qAssign  = `SELECT count(*) FROM cue_roles WHERE role_id = ?`
		qCheckIn = `SELECT count(*) FROM check_ins WHERE role_id = ?`
	)
	for q, want := range map[string]int{qRole: 1, qLink: 1, qAssign: 1, qCheckIn: 1} {
		if got := count(q); got != want {
			t.Fatalf("setup: %q = %d", q, got)
		}
	}

	remove := failNext(t, f.path, "role.delete")
	if err := f.s.DeleteRole(ctx, f.eventID, v, f.stylist, f.masterBy, dayBefore); err == nil {
		t.Fatal("delete succeeded with its log failing")
	}
	for q, name := range map[string]string{qRole: "role", qLink: "link", qAssign: "assignment", qCheckIn: "check-in"} {
		if got := count(q); got != 1 {
			t.Errorf("%s: %d left after a failed delete, want 1", name, got)
		}
	}
	if _, err := f.s.LookupToken(ctx, hash("stylist"), at(12, 0)); err != nil {
		t.Errorf("the role's link after a failed delete: %v", err)
	}
	if got := f.version(t); got != v {
		t.Errorf("version %d → %d", v, got)
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE type = 'role.delete'`); n != 0 {
		t.Error("a failed delete was logged")
	}

	remove()
	if err := f.s.DeleteRole(ctx, f.eventID, v, f.stylist, f.masterBy, dayBefore); err != nil {
		t.Fatal(err)
	}
	for q, name := range map[string]string{qRole: "role", qLink: "link", qAssign: "assignment", qCheckIn: "check-in"} {
		if got := count(q); got != 0 {
			t.Errorf("%s: %d left after the delete", name, got)
		}
	}
}

// US-M-3.6 edge: on the event day a draft whose cues are all skipped has
// nothing to advance, and the advance must not leave the event started.
func TestUS_M_3_6_AdvanceOfADraftWithEveryCueSkippedStartsNothing(t *testing.T) {
	f := newFixture(t)
	for _, id := range f.cueIDs {
		if _, _, err := f.s.Skip(ctx, f.eventID, f.version(t), id, f.masterBy, at(8, 0)); err != nil {
			t.Fatal(err)
		}
	}
	v := f.version(t)
	if _, _, err := f.s.Advance(ctx, f.eventID, v, f.masterBy, at(10, 1)); !errors.Is(err, domain.ErrNothingToAdvance) {
		t.Fatalf("err = %v, want ErrNothingToAdvance", err)
	}
	e, got, err := f.s.Event(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != domain.Draft || got != v {
		t.Errorf("event = %s v%d, want Draft v%d unchanged", e.Status, got, v)
	}
	for _, typ := range []string{"start-event", "cue.advance"} {
		if n := countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE event_id = ? AND type = ?`, f.eventID, typ); n != 0 {
			t.Errorf("%d %s logs after a refused advance", n, typ)
		}
	}
}
