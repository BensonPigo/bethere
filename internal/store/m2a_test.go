package store

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

func (f fixture) logTypes(t *testing.T) []string {
	t.Helper()
	rows, err := f.s.r.QueryContext(ctx, `SELECT type FROM action_logs WHERE event_id = ? ORDER BY id`, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestUS_M_3_6_FirstAdvanceOnTheEventDayStartsTheEvent(t *testing.T) {
	f := newFixture(t)
	v := f.version(t)
	nv, op, err := f.s.Advance(ctx, f.eventID, v, f.masterBy, at(10, 1))
	if err != nil {
		t.Fatal(err)
	}
	if nv != v+1 {
		t.Errorf("version = %d, want %d (one bump for start and advance)", nv, v+1)
	}
	e, _, err := f.s.Event(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != domain.Live || !e.LiveStartedAt.Equal(at(10, 1)) {
		t.Errorf("event = %s started %v, want Live at 10:01", e.Status, e.LiveStartedAt)
	}
	if got := f.statuses(t); !slices.Equal(got, []domain.Status{domain.InProgress, domain.Pending, domain.Pending}) {
		t.Errorf("statuses = %v", got)
	}
	if len(op.Changes) != 1 {
		t.Errorf("op changes = %d, want 1", len(op.Changes))
	}
	types := f.logTypes(t)
	if n := len(types); n < 2 || types[n-2] != "start-event" || types[n-1] != "cue.advance" {
		t.Errorf("log ends with %v, want start-event then cue.advance", types)
	}
	// A later advance does not start anything again.
	if _, _, err := f.s.Advance(ctx, f.eventID, nv, f.masterBy, at(10, 40)); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, typ := range f.logTypes(t) {
		if typ == "start-event" {
			n++
		}
	}
	if n != 1 {
		got := f.logTypes(t)
		t.Errorf("start-event logged twice: %v", got)
	}
}

func TestUS_M_3_7_AdvanceBeforeTheEventDayIsRefused(t *testing.T) {
	f := newFixture(t)
	v := f.version(t)
	_, _, err := f.s.Advance(ctx, f.eventID, v, f.masterBy, at(0, 0).Add(-time.Minute))
	if !errors.Is(err, domain.ErrNotEventDay) {
		t.Fatalf("err = %v, want ErrNotEventDay", err)
	}
	e, nv, err := f.s.Event(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != domain.Draft || nv != v {
		t.Errorf("event = %s v%d, want Draft v%d unchanged", e.Status, nv, v)
	}
}

func TestUS_M_3_6_StartRollsBackWhenNothingCanAdvance(t *testing.T) {
	s, _ := newStore(t)
	id, err := s.CreateEvent(ctx, NewEvent{
		Name: "空", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: hash("m"), RecoveryCodeHash: hash("r"),
	}, at(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.LookupToken(ctx, hash("m"), at(9, 0))
	if err != nil {
		t.Fatal(err)
	}
	_, v, _ := s.Event(ctx, id)
	if _, _, err := s.Advance(ctx, id, v, owner.Actor, at(9, 0)); !errors.Is(err, domain.ErrNothingToAdvance) {
		t.Fatalf("err = %v, want ErrNothingToAdvance", err)
	}
	if e, _, _ := s.Event(ctx, id); e.Status != domain.Draft {
		t.Errorf("status = %s, want Draft: the start must roll back with the advance", e.Status)
	}
}

func TestUS_M_3_8_UndoingTheFirstAdvanceKeepsTheEventLive(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.s.Advance(ctx, f.eventID, f.version(t), f.masterBy, at(10, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.UndoLastCueOp(ctx, f.eventID, v, f.masterBy, at(10, 2)); err != nil {
		t.Fatal(err)
	}
	e, _, err := f.s.Event(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != domain.Live {
		t.Errorf("status = %s, want Live", e.Status)
	}
	if got := f.statuses(t); !slices.Equal(got, []domain.Status{domain.Pending, domain.Pending, domain.Pending}) {
		t.Errorf("statuses = %v", got)
	}
}

// api.md section 6: an id of another, already ended event must fail as
// cross-event, never as "ended", which would wipe the caller's own data.
func TestCrossEventIDOfAnEndedEventIsCrossEventNotEnded(t *testing.T) {
	f := newFixture(t)
	early := time.Date(2027, 5, 1, 9, 0, 0, 0, taipei)
	other, err := f.s.CreateEvent(ctx, NewEvent{
		Name: "舊", Date: domain.Date{Year: 2027, Month: time.May, Day: 1}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: hash("old-master"), RecoveryCodeHash: hash("old-recovery"),
	}, early.AddDate(0, 0, -7))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := f.s.LookupToken(ctx, hash("old-master"), early)
	if err != nil {
		t.Fatal(err)
	}
	oldCue, err := f.s.AddCue(ctx, other, domain.Cue{Seq: 1, Title: "x", ScheduledStart: early, DurationMin: 10}, owner.Actor, early)
	if err != nil {
		t.Fatal(err)
	}
	// Now the other event has ended; the caller's own event has not.
	if err := f.s.AssignRole(ctx, oldCue, f.stylist, "", f.masterBy, at(10, 0)); !errors.Is(err, ErrCrossEvent) {
		t.Errorf("err = %v, want ErrCrossEvent", err)
	}
}

func TestUS_X_4_RoleViewIsStoredCheckedAndDefaulted(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.AddRoleWithView(ctx, f.eventID, "新人", domain.Host, domain.Glance, f.masterBy, at(0, 0)); !errors.Is(err, ErrRoleView) {
		t.Errorf("Host with Glance: err = %v, want ErrRoleView", err)
	}
	if _, err := f.s.AddRoleWithView(ctx, f.eventID, "花藝", domain.RoleType("Florist"), "", f.masterBy, at(0, 0)); !errors.Is(err, ErrRoleType) {
		t.Errorf("unknown type: err = %v, want ErrRoleType", err)
	}
	glance, err := f.s.AddRoleWithView(ctx, f.eventID, "新秘助理", domain.Stylist, domain.Glance, f.masterBy, at(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	host, err := f.s.AddRole(ctx, f.eventID, "新人", domain.Host, f.masterBy, at(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for role, want := range map[int64]domain.View{glance: domain.Glance, host: domain.Dashboard, f.stylist: domain.HandsFree} {
		tok := hash(fmt.Sprint("tok", role))
		if err := f.s.IssueToken(ctx, role, tok, f.masterBy, at(1, 0)); err != nil {
			t.Fatal(err)
		}
		o, err := f.s.LookupToken(ctx, tok, at(12, 0))
		if err != nil {
			t.Fatal(err)
		}
		if o.View != want {
			t.Errorf("role %d view = %s, want %s", role, o.View, want)
		}
	}
	if o, _ := f.s.LookupToken(ctx, hash("master"), at(12, 0)); o.View != domain.Console {
		t.Errorf("master view = %s, want Console", o.View)
	}
}

func TestSnapshotListsShiftsAndUndosNewestFirst(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueToken(ctx, f.stylist, hash("st"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	v, _, err := f.s.Advance(ctx, f.eventID, f.version(t), f.masterBy, at(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	shift := func(cues []domain.Cue) (domain.Op, error) {
		plan, err := domain.PlanShift(cues, domain.ShiftRequest{FromCueID: f.cueIDs[1], DeltaMin: 10}, at(10, 5))
		return plan.Op, err
	}
	if v, _, err = f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 5), shift); err != nil {
		t.Fatal(err)
	}
	if v, _, err = f.s.UndoLastCueOp(ctx, f.eventID, v, f.masterBy, at(10, 6)); err != nil {
		t.Fatal(err)
	}
	// An unreadable row (a newer release's format) is skipped, not an error.
	if _, err := f.s.w.ExecContext(ctx, `INSERT INTO action_logs (event_id, at, actor, type, payload)
		VALUES (?, ?, 'x', 'cue.shift', '{"v":99}')`, f.eventID, toNS(at(10, 7))); err != nil {
		t.Fatal(err)
	}
	d, err := f.s.Snapshot(ctx, hash("st"), at(10, 8))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Shifts) != 2 {
		t.Fatalf("shifts = %d, want 2 (the undo, then the shift)", len(d.Shifts))
	}
	undo, sh := d.Shifts[0], d.Shifts[1]
	if !undo.At.Equal(at(10, 6)) || !sh.At.Equal(at(10, 5)) {
		t.Errorf("times = %v, %v", undo.At, sh.At)
	}
	for _, ch := range sh.Changes {
		if ch.After.OffsetMin-ch.Before.OffsetMin != 10 {
			t.Errorf("shift moved cue %d by %d", ch.After.ID, ch.After.OffsetMin-ch.Before.OffsetMin)
		}
	}
	for _, ch := range undo.Changes {
		if ch.After.OffsetMin-ch.Before.OffsetMin != -10 {
			t.Errorf("undo moved cue %d by %d, want -10", ch.After.ID, ch.After.OffsetMin-ch.Before.OffsetMin)
		}
	}
}
