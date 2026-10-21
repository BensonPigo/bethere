package domain

import (
	"errors"
	"testing"
	"time"
)

var taipei = mustLoad("Asia/Taipei")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// at returns 2027-06-06 hh:mm in Taipei, the first event day.
func at(hh, mm int) time.Time { return time.Date(2027, 6, 6, hh, mm, 0, 0, taipei) }

func cue(id int64, hh, mm, dur int, status Status) Cue {
	return Cue{ID: id, Seq: int(id), ScheduledStart: at(hh, mm), DurationMin: dur, Status: status}
}

func fixed(c Cue) Cue { c.IsFixed = true; return c }

func offset(c Cue, min int) Cue { c.OffsetMin = min; return c }

func byID(cues []Cue) map[int64]Cue {
	m := make(map[int64]Cue, len(cues))
	for _, c := range cues {
		m[c.ID] = c
	}
	return m
}

func offsets(cues []Cue) map[int64]int {
	m := make(map[int64]int, len(cues))
	for _, c := range cues {
		m[c.ID] = c.OffsetMin
	}
	return m
}

func ids(changes []Change) []int64 {
	out := make([]int64, len(changes))
	for i, ch := range changes {
		out[i] = ch.After.ID
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEffectiveTimes(t *testing.T) {
	c := offset(cue(1, 11, 15, 30, Pending), 10)
	if got := c.EffectiveStart(); !got.Equal(at(11, 25)) {
		t.Errorf("EffectiveStart = %v, want 11:25", got)
	}
	if got := c.EffectiveEnd(); !got.Equal(at(11, 55)) {
		t.Errorf("EffectiveEnd = %v, want 11:55", got)
	}
}

// Rundown used by the shift tests (data-model.md 4.2):
//
//	1 09:30 Completed
//	2 10:00 InProgress
//	3 10:40 Pending, 30 min
//	4 11:15 Pending, 20 min
//	5 12:00 Pending, Fixed
//	6 12:30 Pending
func shiftRundown() []Cue {
	return []Cue{
		cue(1, 9, 30, 30, Completed),
		cue(2, 10, 0, 40, InProgress),
		cue(3, 10, 40, 30, Pending),
		cue(4, 11, 15, 20, Pending),
		fixed(cue(5, 12, 0, 30, Pending)),
		cue(6, 12, 30, 30, Pending),
	}
}

func TestPlanShift(t *testing.T) {
	now := at(10, 20)
	tests := []struct {
		name      string
		cues      []Cue
		req       ShiftRequest
		now       time.Time
		wantIDs   []int64
		wantDelta int
		wantErr   error
	}{
		{
			name:      "positive shift skips fixed, in-progress and completed cues",
			cues:      shiftRundown(),
			req:       ShiftRequest{FromCueID: 1, DeltaMin: 10},
			now:       now,
			wantIDs:   []int64{3, 4, 6},
			wantDelta: 10,
		},
		{
			name:      "starts at the chosen cue",
			cues:      shiftRundown(),
			req:       ShiftRequest{FromCueID: 4, DeltaMin: 10},
			now:       now,
			wantIDs:   []int64{4, 6},
			wantDelta: 10,
		},
		{
			name:      "excluded cues are left alone",
			cues:      shiftRundown(),
			req:       ShiftRequest{FromCueID: 3, DeltaMin: 5, Excluded: []int64{4}},
			now:       now,
			wantIDs:   []int64{3, 6},
			wantDelta: 5,
		},
		{
			name:      "in-progress cue as starting point is not shifted",
			cues:      shiftRundown(),
			req:       ShiftRequest{FromCueID: 2, DeltaMin: 10},
			now:       now,
			wantIDs:   []int64{3, 4, 6},
			wantDelta: 10,
		},
		{
			name: "positive shift may leave a cue earlier than now",
			cues: []Cue{
				cue(1, 10, 0, 10, Pending),
				cue(2, 11, 0, 10, Pending),
			},
			req:       ShiftRequest{FromCueID: 1, DeltaMin: 5},
			now:       at(10, 30),
			wantIDs:   []int64{1, 2},
			wantDelta: 5,
		},
		{
			name: "negative shift excludes cues already overdue",
			cues: []Cue{
				cue(1, 10, 0, 10, Pending), // overdue at 10:30
				cue(2, 11, 0, 10, Pending),
				cue(3, 11, 30, 10, Pending),
			},
			req:       ShiftRequest{FromCueID: 1, DeltaMin: -10},
			now:       at(10, 30),
			wantIDs:   []int64{2, 3},
			wantDelta: -10,
		},
		{
			name: "negative shift landing exactly on now is allowed",
			cues: []Cue{
				cue(1, 10, 40, 10, Pending),
			},
			req:       ShiftRequest{FromCueID: 1, DeltaMin: -10},
			now:       at(10, 30),
			wantIDs:   []int64{1},
			wantDelta: -10,
		},
		{
			name: "negative shift before now is rejected as a whole",
			cues: []Cue{
				cue(1, 10, 35, 10, Pending),
				cue(2, 11, 0, 10, Pending),
			},
			req:     ShiftRequest{FromCueID: 1, DeltaMin: -10},
			now:     at(10, 30),
			wantErr: ErrShiftTooEarly,
		},
		{
			name:    "zero delta is rejected",
			cues:    shiftRundown(),
			req:     ShiftRequest{FromCueID: 3, DeltaMin: 0},
			now:     now,
			wantErr: ErrInvalidDelta,
		},
		{
			name:    "unknown starting cue is rejected",
			cues:    shiftRundown(),
			req:     ShiftRequest{FromCueID: 99, DeltaMin: 10},
			now:     now,
			wantErr: ErrCueNotFound,
		},
		{
			name: "order follows seq, not slice order",
			cues: []Cue{
				cue(3, 11, 0, 10, Pending),
				cue(1, 10, 0, 10, Pending),
				cue(2, 10, 30, 10, Pending),
			},
			req:       ShiftRequest{FromCueID: 2, DeltaMin: 10},
			now:       at(9, 0),
			wantIDs:   []int64{2, 3},
			wantDelta: 10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := offsets(tt.cues)
			plan, err := PlanShift(tt.cues, tt.req, tt.now)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got := ids(plan.Op.Changes); !equalIDs(got, tt.wantIDs) {
				t.Fatalf("shifted cues = %v, want %v", got, tt.wantIDs)
			}
			for _, ch := range plan.Op.Changes {
				if d := ch.After.OffsetMin - ch.Before.OffsetMin; d != tt.wantDelta {
					t.Errorf("cue %d applied %d, want %d", ch.After.ID, d, tt.wantDelta)
				}
			}
			if got := offsets(tt.cues); len(got) != len(before) || got[1] != before[1] {
				t.Errorf("PlanShift mutated its input")
			}
		})
	}
}

func TestPlanShiftTooEarlyNamesTheCue(t *testing.T) {
	cues := []Cue{cue(1, 10, 50, 10, Pending), cue(2, 10, 35, 10, Pending)}
	cues[1].Seq = 2
	_, err := PlanShift(cues, ShiftRequest{FromCueID: 1, DeltaMin: -10}, at(10, 30))
	var tooEarly *ShiftTooEarlyError
	if !errors.As(err, &tooEarly) {
		t.Fatalf("err = %v, want *ShiftTooEarlyError", err)
	}
	if tooEarly.CueID != 2 || !tooEarly.NewStart.Equal(at(10, 25)) {
		t.Errorf("got cue %d at %v, want cue 2 at 10:25", tooEarly.CueID, tooEarly.NewStart)
	}
}

func TestPlanShiftReportsFixedConflictsWithoutBlocking(t *testing.T) {
	// After +30, cue 4 runs 11:45-12:05, past the fixed 12:00.
	plan, err := PlanShift(shiftRundown(), ShiftRequest{FromCueID: 3, DeltaMin: 30}, at(10, 20))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want exactly one", plan.Conflicts)
	}
	c := plan.Conflicts[0]
	if c.CueID != 4 || c.FixedCueID != 5 || c.OverrunMin != 5 {
		t.Errorf("conflict = %+v, want cue 4 over fixed 5 by 5 min", c)
	}
}

func TestConflicts(t *testing.T) {
	tests := []struct {
		name string
		cues []Cue
		want []Conflict
	}{
		{
			name: "no overrun",
			cues: []Cue{cue(1, 11, 0, 60, Pending), fixed(cue(2, 12, 0, 30, Pending))},
		},
		{
			name: "ending exactly at the fixed start is fine",
			cues: []Cue{offset(cue(1, 11, 0, 50, Pending), 10), fixed(cue(2, 12, 0, 30, Pending))},
		},
		{
			name: "only the nearest following fixed cue counts",
			cues: []Cue{
				cue(1, 11, 0, 70, Pending),
				fixed(cue(2, 12, 0, 30, Pending)),
				fixed(cue(3, 12, 5, 30, Pending)),
			},
			want: []Conflict{{CueID: 1, FixedCueID: 2, OverrunMin: 10}, {CueID: 2, FixedCueID: 3, OverrunMin: 25}},
		},
		{
			name: "a skipped fixed cue is not a constraint",
			cues: []Cue{
				cue(1, 11, 0, 70, Pending),
				fixed(cue(2, 12, 0, 30, Skipped)),
				fixed(cue(3, 13, 0, 30, Pending)),
			},
		},
		{
			name: "completed and skipped cues are ignored",
			cues: []Cue{
				cue(1, 11, 0, 90, Completed),
				cue(2, 11, 0, 90, Skipped),
				fixed(cue(3, 12, 0, 30, Pending)),
			},
		},
		{
			name: "in-progress cue counts",
			cues: []Cue{cue(1, 11, 0, 65, InProgress), fixed(cue(2, 12, 0, 30, Pending))},
			want: []Conflict{{CueID: 1, FixedCueID: 2, OverrunMin: 5}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Conflicts(tt.cues)
			if len(got) != len(tt.want) {
				t.Fatalf("Conflicts = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Conflicts[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestShiftUndoRestoresOffsetsExactly(t *testing.T) {
	cues := shiftRundown()
	cues[3] = offset(cues[3], 5) // cue 4 already carries an earlier shift
	plan, err := PlanShift(cues, ShiftRequest{FromCueID: 3, DeltaMin: 10, Excluded: []int64{6}}, at(10, 20))
	if err != nil {
		t.Fatal(err)
	}
	shifted := plan.Op.ApplyTo(cues)
	if got := byID(shifted)[4].OffsetMin; got != 15 {
		t.Fatalf("cue 4 offset after shift = %d, want 15", got)
	}
	undo, err := Undo(shifted, plan.Op)
	if err != nil {
		t.Fatal(err)
	}
	restored := undo.ApplyTo(shifted)
	if got, want := offsets(restored), offsets(cues); len(got) != len(want) {
		t.Fatalf("restored %v, want %v", got, want)
	} else {
		for id, w := range want {
			if got[id] != w {
				t.Errorf("cue %d offset = %d, want %d", id, got[id], w)
			}
		}
	}
}

func TestAdvance(t *testing.T) {
	now := at(10, 42)
	tests := []struct {
		name    string
		cues    []Cue
		want    map[int64]Status
		wantErr error
	}{
		{
			name: "starts the first pending cue when nothing is in progress",
			cues: []Cue{cue(1, 9, 30, 30, Pending), cue(2, 10, 0, 30, Pending)},
			want: map[int64]Status{1: InProgress, 2: Pending},
		},
		{
			name: "completes the current cue and starts the next pending one",
			cues: []Cue{
				cue(1, 9, 30, 30, InProgress),
				cue(2, 10, 0, 30, Skipped),
				cue(3, 10, 40, 30, Pending),
			},
			want: map[int64]Status{1: Completed, 2: Skipped, 3: InProgress},
		},
		{
			name: "completes the last cue",
			cues: []Cue{cue(1, 9, 30, 30, Completed), cue(2, 10, 0, 30, InProgress)},
			want: map[int64]Status{1: Completed, 2: Completed},
		},
		{
			name:    "nothing left to advance",
			cues:    []Cue{cue(1, 9, 30, 30, Completed), cue(2, 10, 0, 30, Skipped)},
			wantErr: ErrNothingToAdvance,
		},
		{
			name:    "more than one cue in progress is a broken invariant",
			cues:    []Cue{cue(1, 9, 30, 30, InProgress), cue(2, 10, 0, 30, InProgress)},
			wantErr: ErrMultipleInProgress,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, err := Advance(tt.cues, now)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			got := byID(op.ApplyTo(tt.cues))
			for id, want := range tt.want {
				if got[id].Status != want {
					t.Errorf("cue %d status = %s, want %s", id, got[id].Status, want)
				}
			}
		})
	}
}

func TestAdvanceRecordsActualTimes(t *testing.T) {
	cues := []Cue{cue(1, 9, 30, 30, InProgress), cue(2, 10, 0, 30, Pending)}
	cues[0].ActualStart = at(9, 33)
	now := at(10, 2)
	got := byID(mustAdvance(t, cues, now).ApplyTo(cues))
	if !got[1].ActualStart.Equal(at(9, 33)) || !got[1].ActualEnd.Equal(now) {
		t.Errorf("cue 1 actual = %v..%v, want 09:33..10:02", got[1].ActualStart, got[1].ActualEnd)
	}
	if !got[2].ActualStart.Equal(now) || !got[2].ActualEnd.IsZero() {
		t.Errorf("cue 2 actual = %v..%v, want 10:02..unset", got[2].ActualStart, got[2].ActualEnd)
	}
}

func mustAdvance(t *testing.T, cues []Cue, now time.Time) Op {
	t.Helper()
	op, err := Advance(cues, now)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestAdvanceUndoRestoresBothCues(t *testing.T) {
	cues := []Cue{cue(1, 9, 30, 30, InProgress), cue(2, 10, 0, 30, Pending)}
	cues[0].ActualStart = at(9, 33)
	op := mustAdvance(t, cues, at(10, 2))
	advanced := op.ApplyTo(cues)
	undo, err := Undo(advanced, op)
	if err != nil {
		t.Fatal(err)
	}
	got := byID(undo.ApplyTo(advanced))
	if got[1].Status != InProgress || !got[1].ActualStart.Equal(at(9, 33)) || !got[1].ActualEnd.IsZero() {
		t.Errorf("cue 1 = %+v, want InProgress since 09:33", got[1])
	}
	if got[2].Status != Pending || !got[2].ActualStart.IsZero() {
		t.Errorf("cue 2 = %+v, want Pending with no actual start", got[2])
	}
}

func TestSkip(t *testing.T) {
	cues := []Cue{cue(1, 9, 30, 30, InProgress), cue(2, 10, 0, 30, Pending), cue(3, 10, 30, 30, Completed)}
	op, err := Skip(cues, 2)
	if err != nil {
		t.Fatal(err)
	}
	skipped := op.ApplyTo(cues)
	if got := byID(skipped)[2].Status; got != Skipped {
		t.Fatalf("cue 2 status = %s, want Skipped", got)
	}
	for _, id := range []int64{1, 3} {
		if _, err := Skip(cues, id); !errors.Is(err, ErrNotPending) {
			t.Errorf("Skip(%d) err = %v, want ErrNotPending", id, err)
		}
	}
	if _, err := Skip(cues, 99); !errors.Is(err, ErrCueNotFound) {
		t.Errorf("Skip(99) err = %v, want ErrCueNotFound", err)
	}

	// data-model.md M10: a skip can be undone back to Pending.
	undo, err := Undo(skipped, op)
	if err != nil {
		t.Fatal(err)
	}
	if got := byID(undo.ApplyTo(skipped))[2].Status; got != Pending {
		t.Errorf("after undo cue 2 status = %s, want Pending", got)
	}
}

func TestUndoRejectsStaleOperation(t *testing.T) {
	cues := []Cue{cue(1, 9, 30, 30, Pending), cue(2, 10, 0, 30, Pending)}
	op, err := Skip(cues, 2)
	if err != nil {
		t.Fatal(err)
	}
	// The op was never applied, so the cues do not match its After state.
	if _, err := Undo(cues, op); !errors.Is(err, ErrUndoStale) {
		t.Errorf("err = %v, want ErrUndoStale", err)
	}
	if _, err := Undo(nil, op); !errors.Is(err, ErrUndoStale) {
		t.Errorf("missing cue: err = %v, want ErrUndoStale", err)
	}
}

func TestEventEndsAt(t *testing.T) {
	e := Event{Date: Date{2027, time.June, 6}, Location: taipei}
	want := time.Date(2027, 6, 7, 4, 0, 0, 0, taipei)
	if got := e.EndsAt(); !got.Equal(want) {
		t.Errorf("EndsAt = %v, want %v", got, want)
	}
	if got := e.EndsAt().UTC(); got != time.Date(2027, 6, 6, 20, 0, 0, 0, time.UTC) {
		t.Errorf("EndsAt in UTC = %v, want 2027-06-06T20:00Z", got)
	}
	if e.Ended(want.Add(-time.Second)) {
		t.Error("Ended one second before endsAt")
	}
	if !e.Ended(want) {
		t.Error("not Ended exactly at endsAt")
	}
	if got, want := e.PurgeAt(), time.Date(2027, 8, 26, 4, 0, 0, 0, taipei); !got.Equal(want) {
		t.Errorf("PurgeAt = %v, want %v (endsAt + 80 days)", got, want)
	}
}

func TestEventStart(t *testing.T) {
	draft := Event{Date: Date{2027, time.June, 6}, Location: taipei, Status: Draft}
	tests := []struct {
		name    string
		event   Event
		now     time.Time
		wantErr error
	}{
		{name: "on the event day", event: draft, now: at(8, 0)},
		{name: "just after local midnight", event: draft, now: at(0, 0)},
		{name: "the day before", event: draft, now: at(0, 0).Add(-time.Second), wantErr: ErrNotEventDay},
		{name: "after midnight but before endsAt", event: draft, now: time.Date(2027, 6, 7, 1, 0, 0, 0, taipei), wantErr: ErrNotEventDay},
		{name: "after the event ended", event: draft, now: time.Date(2027, 6, 7, 4, 0, 0, 0, taipei), wantErr: ErrEnded},
		{name: "already live", event: Event{Date: draft.Date, Location: taipei, Status: Live}, now: at(9, 0), wantErr: ErrNotDraft},
		{
			// 2027-06-05 23:00 UTC is already 06-06 07:00 in Taipei.
			name:  "event day is judged in the event's time zone",
			event: draft,
			now:   time.Date(2027, 6, 5, 23, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.event.Start(tt.now)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.Status != Live || !got.LiveStartedAt.Equal(tt.now) {
				t.Errorf("got %+v, want Live since %v", got, tt.now)
			}
		})
	}
}

func TestEventRequireLive(t *testing.T) {
	date := Date{2027, time.June, 6}
	if err := (Event{Date: date, Location: taipei, Status: Live}).RequireLive(at(12, 0)); err != nil {
		t.Errorf("live event: %v", err)
	}
	if err := (Event{Date: date, Location: taipei, Status: Draft}).RequireLive(at(12, 0)); !errors.Is(err, ErrNotLive) {
		t.Errorf("draft event: err = %v, want ErrNotLive", err)
	}
	ended := time.Date(2027, 6, 7, 4, 0, 0, 0, taipei)
	if err := (Event{Date: date, Location: taipei, Status: Live}).RequireLive(ended); !errors.Is(err, ErrEnded) {
		t.Errorf("ended event: err = %v, want ErrEnded", err)
	}
}

func TestEventReschedule(t *testing.T) {
	e := Event{Date: Date{2027, time.June, 6}, Location: taipei, Status: Draft}
	cues := []Cue{cue(1, 9, 30, 30, Pending), offset(cue(2, 23, 30, 30, Pending), 5)}
	now := time.Date(2027, 5, 1, 12, 0, 0, 0, taipei)

	got, moved, err := e.Reschedule(Date{2027, time.June, 13}, cues, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Date != (Date{2027, time.June, 13}) {
		t.Errorf("Date = %v, want 2027-06-13", got.Date)
	}
	m := byID(moved)
	if !m[1].ScheduledStart.Equal(time.Date(2027, 6, 13, 9, 30, 0, 0, taipei)) {
		t.Errorf("cue 1 = %v, want 06-13 09:30", m[1].ScheduledStart)
	}
	if !m[2].ScheduledStart.Equal(time.Date(2027, 6, 13, 23, 30, 0, 0, taipei)) || m[2].OffsetMin != 5 {
		t.Errorf("cue 2 = %v %+d, want 06-13 23:30 +5", m[2].ScheduledStart, m[2].OffsetMin)
	}
	if !cues[0].ScheduledStart.Equal(at(9, 30)) {
		t.Error("Reschedule mutated its input")
	}

	if _, _, err := (Event{Date: e.Date, Location: taipei, Status: Live}).Reschedule(Date{2027, time.June, 13}, cues, now); !errors.Is(err, ErrNotDraft) {
		t.Errorf("live event: err = %v, want ErrNotDraft", err)
	}
	// The new end must still be in the future.
	if _, _, err := e.Reschedule(Date{2027, time.April, 30}, cues, now); !errors.Is(err, ErrEnded) {
		t.Errorf("past date: err = %v, want ErrEnded", err)
	}
	// An event that already ended cannot be revived.
	after := time.Date(2027, 6, 7, 4, 0, 0, 0, taipei)
	if _, _, err := e.Reschedule(Date{2027, time.June, 20}, cues, after); !errors.Is(err, ErrEnded) {
		t.Errorf("ended event: err = %v, want ErrEnded", err)
	}
}

func TestUndoKeepsLiveTitleEdits(t *testing.T) {
	cues := []Cue{cue(1, 9, 30, 30, Pending)}
	cues[0].Title = "一進預備"
	op, err := Skip(cues, 1)
	if err != nil {
		t.Fatal(err)
	}
	skipped := op.ApplyTo(cues)
	skipped[0].Title = "一進預備（延後）" // edited live (P8) after the skip
	undo, err := Undo(skipped, op)
	if err != nil {
		t.Fatal(err)
	}
	got := undo.ApplyTo(skipped)[0]
	if got.Status != Pending || got.Title != "一進預備（延後）" {
		t.Errorf("got %q %s, want the edited title back in Pending", got.Title, got.Status)
	}
}
