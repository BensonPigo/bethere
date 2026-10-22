package store

import (
	"errors"
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
