package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// dayBefore is the day before the fixture's wedding, when every edit of the
// preparation is open.
var dayBefore = at(9, 0).AddDate(0, 0, -1)

// jobActor adds a role of typ with a link and returns its id and actor.
func (f fixture) jobActor(t *testing.T, name string, typ domain.RoleType) (int64, Actor) {
	t.Helper()
	id, err := f.s.AddRole(ctx, f.eventID, name, typ, f.masterBy, dayBefore)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.IssueToken(ctx, id, hash("tok-"+name), f.masterBy, dayBefore); err != nil {
		t.Fatal(err)
	}
	owner, err := f.s.LookupToken(ctx, hash("tok-"+name), dayBefore)
	if err != nil {
		t.Fatal(err)
	}
	return id, owner.Actor
}

// US-RN-1.2: two runners pressing "I'll take it" at once leave exactly one
// claimer; the other is told the task is taken.
func TestUS_RN_1_2_ConcurrentClaimLeavesOneClaimer(t *testing.T) {
	f := newFixture(t)
	idA, actA := f.jobActor(t, "機動A", domain.Runner)
	idB, actB := f.jobActor(t, "機動B", domain.Runner)
	for round := range 5 {
		task, err := f.s.AddTask(ctx, f.eventID, fmt.Sprintf("補水%d", round), f.masterBy, dayBefore)
		if err != nil {
			t.Fatal(err)
		}
		errs := onlyOne(2, func(i int) error {
			act := actA
			if i == 1 {
				act = actB
			}
			return f.s.SetTaskState(ctx, f.eventID, task, TaskClaimed, false, act, dayBefore)
		})
		won := 0
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, ErrTaskTaken):
				t.Errorf("round %d: the loser got %v, want ErrTaskTaken", round, err)
			}
		}
		if won != 1 {
			t.Fatalf("round %d: %d claimers succeeded, want exactly 1", round, won)
		}
		var claimer int64
		if err := f.s.r.QueryRow(`SELECT claimed_by_role_id FROM tasks WHERE id = ?`, task).Scan(&claimer); err != nil {
			t.Fatal(err)
		}
		winner := idA
		if errs[1] == nil {
			winner = idB
		}
		if claimer != winner {
			t.Errorf("round %d: claimed by %d, but the winner was %d", round, claimer, winner)
		}
	}
}

// US-RN-1.2: a runner claiming a task of its own again changes nothing and
// writes no log; a done task cannot be claimed.
func TestUS_RN_1_2_ClaimAgainAndClaimDone(t *testing.T) {
	f := newFixture(t)
	_, act := f.jobActor(t, "機動A", domain.Runner)
	task, err := f.s.AddTask(ctx, f.eventID, "補水", f.masterBy, dayBefore)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetTaskState(ctx, f.eventID, task, TaskClaimed, false, act, dayBefore); err != nil {
		t.Fatal(err)
	}
	logs := func() int {
		var n int
		if err := f.s.r.QueryRow(`SELECT count(*) FROM action_logs WHERE type = 'task.set'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := logs()
	later := dayBefore.Add(time.Hour)
	if err := f.s.SetTaskState(ctx, f.eventID, task, TaskClaimed, false, act, later); err != nil {
		t.Errorf("claiming a task of its own again: %v", err)
	}
	if logs() != before {
		t.Error("a repeated claim wrote a log entry")
	}
	var updated int64
	if err := f.s.r.QueryRow(`SELECT updated_at FROM tasks WHERE id = ?`, task).Scan(&updated); err != nil || updated != toNS(dayBefore) {
		t.Errorf("updated_at = %d (%v), a repeated claim moved it", updated, err)
	}
	if err := f.s.SetTaskState(ctx, f.eventID, task, TaskDone, false, act, later); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetTaskState(ctx, f.eventID, task, TaskClaimed, false, act, later); !errors.Is(err, ErrInvalidTaskState) {
		t.Errorf("claiming a done task: err = %v, want ErrInvalidTaskState", err)
	}
}

// US-RN-1.3, US-M-16.1: the Master marks an open task done, and a claimed
// one done keeps its claimer.
func TestUS_RN_1_3_MasterMarksDone(t *testing.T) {
	f := newFixture(t)
	id, act := f.jobActor(t, "機動A", domain.Runner)
	open, _ := f.s.AddTask(ctx, f.eventID, "開著的", f.masterBy, dayBefore)
	claimed, _ := f.s.AddTask(ctx, f.eventID, "認領的", f.masterBy, dayBefore)
	if err := f.s.SetTaskState(ctx, f.eventID, claimed, TaskClaimed, false, act, dayBefore); err != nil {
		t.Fatal(err)
	}
	for _, task := range []int64{open, claimed} {
		if err := f.s.SetTaskState(ctx, f.eventID, task, TaskDone, true, f.masterBy, dayBefore); err != nil {
			t.Fatalf("Master done on %d: %v", task, err)
		}
	}
	tasks, err := loadTasks(ctx, f.s.r, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]Task{}
	for _, tk := range tasks {
		got[tk.ID] = tk
	}
	if got[open].State != TaskDone || got[open].ClaimedBy != 0 {
		t.Errorf("open task marked done = %+v, want done with no claimer", got[open])
	}
	if got[claimed].State != TaskDone || got[claimed].ClaimedBy != id {
		t.Errorf("claimed task marked done = %+v, want done by the runner %d", got[claimed], id)
	}
	if err := f.s.SetTaskState(ctx, f.eventID, open, TaskClaimed, true, f.masterBy, dayBefore); !errors.Is(err, ErrInvalidTaskState) {
		t.Errorf("Master claiming: err = %v, want ErrInvalidTaskState", err)
	}
}

// US-M-16.1 (api.md 9.4): at most 200 tasks, a title of 1 to 100 characters.
func TestUS_M_16_1_TaskLimits(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.AddTask(ctx, f.eventID, strings.Repeat("任", 100), f.masterBy, dayBefore); err != nil {
		t.Errorf("a title of 100 characters: %v", err)
	}
	for _, bad := range []string{strings.Repeat("任", 101), "", "   "} {
		if _, err := f.s.AddTask(ctx, f.eventID, bad, f.masterBy, dayBefore); !errors.Is(err, ErrInvalidTask) {
			t.Errorf("title %q: err = %v, want ErrInvalidTask", bad, err)
		}
	}
	for i := 1; i < 200; i++ { // the 200th is the last
		if _, err := f.s.AddTask(ctx, f.eventID, fmt.Sprintf("任務%d", i), f.masterBy, dayBefore); err != nil {
			t.Fatalf("task %d: %v", i+1, err)
		}
	}
	if _, err := f.s.AddTask(ctx, f.eventID, "第201個", f.masterBy, dayBefore); !errors.Is(err, ErrTooManyTasks) {
		t.Errorf("201st task: err = %v, want ErrTooManyTasks", err)
	}
	if n := count(t, f.s, "tasks"); n != 200 {
		t.Errorf("tasks = %d, want 200", n)
	}
}

// guestRows is n valid guests.
func guestRows(n int) []GuestRow {
	rows := make([]GuestRow, n)
	for i := range rows {
		rows[i] = GuestRow{Name: fmt.Sprintf("賓客%d", i), Table: "1"}
	}
	return rows
}

// US-RC-1.5 (api.md 9.5): 1000 guests are written, 1001 are not and leave
// the old list.
func TestUS_RC_1_5_GuestLimit(t *testing.T) {
	f := newFixture(t)
	if err := f.s.ReplaceGuests(ctx, f.eventID, guestRows(2), f.masterBy, dayBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ReplaceGuests(ctx, f.eventID, guestRows(MaxGuests+1), f.masterBy, dayBefore); !errors.Is(err, ErrInvalidImport) {
		t.Errorf("1001 guests: err = %v, want ErrInvalidImport", err)
	}
	if n := count(t, f.s, "guests"); n != 2 {
		t.Errorf("guests after a refused import = %d, want the 2 before", n)
	}
	if err := f.s.ReplaceGuests(ctx, f.eventID, nil, f.masterBy, dayBefore); !errors.Is(err, ErrInvalidImport) {
		t.Errorf("no guests: err = %v, want ErrInvalidImport", err)
	}
	if err := f.s.ReplaceGuests(ctx, f.eventID, guestRows(MaxGuests), f.masterBy, dayBefore); err != nil {
		t.Fatalf("1000 guests: %v", err)
	}
	if n := count(t, f.s, "guests"); n != 1000 {
		t.Errorf("guests = %d, want 1000", n)
	}
}

// US-RC-1.2: the reception cancels an arrival of its own.
func TestUS_RC_1_2_ReceptionCancelsAnArrival(t *testing.T) {
	f := newFixture(t)
	_, rec := f.jobActor(t, "迎賓", domain.Reception)
	if err := f.s.ReplaceGuests(ctx, f.eventID, guestRows(1), f.masterBy, dayBefore); err != nil {
		t.Fatal(err)
	}
	guests, err := loadGuests(ctx, f.s.r, f.eventID)
	if err != nil || len(guests) != 1 {
		t.Fatalf("guests = %v, %v", guests, err)
	}
	id := guests[0].ID
	if err := f.s.SetArrived(ctx, f.eventID, id, true, rec, dayBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetArrived(ctx, f.eventID, id, false, rec, dayBefore); err != nil {
		t.Fatal(err)
	}
	if guests, _ = loadGuests(ctx, f.s.r, f.eventID); guests[0].Arrived {
		t.Error("the guest is still arrived after the reception cancelled")
	}
	// Nobody is arrived any more: the list can be replaced again.
	if err := f.s.ReplaceGuests(ctx, f.eventID, guestRows(3), f.masterBy, dayBefore); err != nil {
		t.Errorf("replace after the cancellation: %v", err)
	}
}

// checklistOf is n valid titles.
func checklistOf(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("項目%d", i+1)
	}
	return out
}

// US-CA-1.1 (api.md 9.6): 30 items are written, 31 are not; a title of 100
// characters is, 101 is not; a list of items none done is replaced.
func TestUS_CA_1_1_ChecklistLimitsAndReplace(t *testing.T) {
	f := newFixture(t)
	if err := f.s.ReplaceChecklist(ctx, f.eventID, checklistOf(MaxChecklistItems), f.masterBy, dayBefore); err != nil {
		t.Fatalf("30 items: %v", err)
	}
	if err := f.s.ReplaceChecklist(ctx, f.eventID, checklistOf(MaxChecklistItems+1), f.masterBy, dayBefore); !errors.Is(err, ErrInvalidChecklist) {
		t.Errorf("31 items: err = %v, want ErrInvalidChecklist", err)
	}
	if err := f.s.ReplaceChecklist(ctx, f.eventID, []string{strings.Repeat("項", 101)}, f.masterBy, dayBefore); !errors.Is(err, ErrInvalidChecklist) {
		t.Errorf("a title of 101 characters: err = %v, want ErrInvalidChecklist", err)
	}
	if n := count(t, f.s, "checklist_items"); n != 30 {
		t.Errorf("items after refused replacements = %d, want 30", n)
	}
	if err := f.s.ReplaceChecklist(ctx, f.eventID, []string{strings.Repeat("項", 100), "二"}, f.masterBy, dayBefore); err != nil {
		t.Fatalf("replacing a list none of which is done: %v", err)
	}
	items, err := loadChecklist(ctx, f.s.r, f.eventID)
	if err != nil || len(items) != 2 || items[1].Title != "二" {
		t.Errorf("checklist = %+v, %v", items, err)
	}
}

// US-CA-1.2: two cashiers ticking one item in turn keep the first one's
// name and time; ticking again changes nothing; once every tick is taken
// back the whole list can be replaced.
func TestUS_CA_1_2_FirstTickIsKept(t *testing.T) {
	f := newFixture(t)
	idA, actA := f.jobActor(t, "禮金甲", domain.Cashier)
	_, actB := f.jobActor(t, "禮金乙", domain.Cashier)
	if err := f.s.ReplaceChecklist(ctx, f.eventID, []string{"總金額確認", "簽名"}, f.masterBy, dayBefore); err != nil {
		t.Fatal(err)
	}
	items, _ := loadChecklist(ctx, f.s.r, f.eventID)
	first, second := items[0].ID, items[1].ID
	doneAt := func(id int64) (int64, int64) {
		var at, by int64
		if err := f.s.r.QueryRow(`SELECT coalesce(done_at, 0), coalesce(done_by_role_id, 0) FROM checklist_items WHERE id = ?`, id).Scan(&at, &by); err != nil {
			t.Fatal(err)
		}
		return at, by
	}
	t1, t2 := dayBefore.Add(time.Minute), dayBefore.Add(2*time.Minute)
	if err := f.s.CheckItem(ctx, f.eventID, first, true, actA, t1); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CheckItem(ctx, f.eventID, first, true, actB, t2); err != nil {
		t.Fatal(err)
	}
	if at, by := doneAt(first); at != toNS(t1) || by != idA {
		t.Errorf("done at %d by %d, want the first tick: %d by %d", at, by, toNS(t1), idA)
	}
	var logs int
	f.s.r.QueryRow(`SELECT count(*) FROM action_logs WHERE type = 'checklist.check'`).Scan(&logs)
	if logs != 1 {
		t.Errorf("checklist.check logs = %d, want 1: a repeated tick writes nothing", logs)
	}
	// A tick taken back clears the time and the name; ticking again is a new one.
	if err := f.s.CheckItem(ctx, f.eventID, first, false, actB, t2); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CheckItem(ctx, f.eventID, second, true, actA, t2); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ReplaceChecklist(ctx, f.eventID, []string{"新的"}, f.masterBy, t2); !errors.Is(err, ErrChecklistStarted) {
		t.Errorf("replace with one item done: err = %v, want ErrChecklistStarted", err)
	}
	if err := f.s.CheckItem(ctx, f.eventID, second, false, actA, t2); err != nil {
		t.Fatal(err)
	}
	if at, by := doneAt(second); at != 0 || by != 0 {
		t.Errorf("a ticked-off item still has done_at %d, by %d", at, by)
	}
	if err := f.s.ReplaceChecklist(ctx, f.eventID, []string{"新的"}, f.masterBy, t2); err != nil {
		t.Errorf("replace after every tick was taken back: %v", err)
	}
}

// api.md 9.4: setting a task to the state it is in, by the same holder,
// writes nothing and logs nothing.
func TestUS_RN_1_3_MarkingDoneTwiceWritesOnce(t *testing.T) {
	f := newFixture(t)
	task, _ := f.s.AddTask(ctx, f.eventID, "搬花", f.masterBy, dayBefore)
	for i := 0; i < 2; i++ {
		if err := f.s.SetTaskState(ctx, f.eventID, task, TaskDone, true, f.masterBy, dayBefore); err != nil {
			t.Fatalf("done #%d: %v", i+1, err)
		}
	}
	if n := countRows(t, f.s, `SELECT count(*) FROM action_logs WHERE event_id = ? AND type = 'task.set'`, f.eventID); n != 1 {
		t.Errorf("%d task.set logs, want 1", n)
	}
}
