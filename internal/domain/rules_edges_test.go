package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// US-M-2.3: a skipped cue is never moved by a shift.
func TestUS_M_2_3_ShiftLeavesSkippedCuesAlone(t *testing.T) {
	cues := []Cue{
		cue(1, 10, 0, 30, InProgress),
		cue(2, 10, 30, 30, Skipped),
		cue(3, 11, 0, 30, Pending),
	}
	plan, err := PlanShift(cues, ShiftRequest{FromCueID: 1, DeltaMin: 10}, at(10, 5))
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(plan.Op.Changes); !equalIDs(got, []int64{3}) {
		t.Errorf("shifted %v, want only the pending cue 3", got)
	}
	if got := offsets(plan.Op.ApplyTo(cues)); got[2] != 0 || got[3] != 10 {
		t.Errorf("offsets = %v, want skipped cue 2 at 0 and cue 3 at 10", got)
	}
}

// US-M-2 edge: the starting cue may already be completed or skipped; the
// shift then moves the pending cues from there on, and the starting cue
// itself stays as it is.
func TestUS_M_2_ShiftFromACompletedOrSkippedCue(t *testing.T) {
	for _, status := range []Status{Completed, Skipped} {
		cues := []Cue{
			cue(1, 9, 0, 30, status),
			cue(2, 10, 0, 30, Pending),
			cue(3, 11, 0, 30, Pending),
		}
		plan, err := PlanShift(cues, ShiftRequest{FromCueID: 1, DeltaMin: 5}, at(9, 30))
		if err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if got := ids(plan.Op.Changes); !equalIDs(got, []int64{2, 3}) {
			t.Errorf("%s as the starting cue: shifted %v, want [2 3]", status, got)
		}
		if got := byID(plan.Op.ApplyTo(cues))[1]; got.OffsetMin != 0 || got.Status != status {
			t.Errorf("starting cue = %+v, want untouched", got)
		}
	}
}

// US-M-2.6: exactly +1440 and -1440 are accepted, one more is not.
func TestUS_M_2_6_ShiftLimitIsInclusive(t *testing.T) {
	cues := []Cue{cue(1, 10, 0, 30, Pending)}
	long := at(10, 0).AddDate(0, 0, -3) // far enough before the cue for -1440
	for _, d := range []int{1440, -1440} {
		plan, err := PlanShift(cues, ShiftRequest{FromCueID: 1, DeltaMin: d}, long)
		if err != nil {
			t.Fatalf("delta %d: %v", d, err)
		}
		if got := plan.Op.Changes[0].After.OffsetMin; got != d {
			t.Errorf("delta %d: offset = %d", d, got)
		}
	}
	for _, d := range []int{1441, -1441, 0} {
		if _, err := PlanShift(cues, ShiftRequest{FromCueID: 1, DeltaMin: d}, long); !errors.Is(err, ErrInvalidDelta) {
			t.Errorf("delta %d: err = %v, want ErrInvalidDelta", d, err)
		}
	}
	// +1440 carries a cue over midnight and into the next day without error.
	plan, err := PlanShift([]Cue{cue(1, 23, 30, 30, Pending)}, ShiftRequest{FromCueID: 1, DeltaMin: 1440}, at(9, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Op.Changes[0].After.EffectiveStart(); !got.Equal(at(23, 30).AddDate(0, 0, 1)) {
		t.Errorf("start = %v, want 23:30 the next day", got)
	}
}

// US-M-8.6 / US-M-10.6: the order warning.
func TestUS_M_8_6_OutOfOrderRules(t *testing.T) {
	t.Run("a pending cue earlier than the one ahead warns, naming it", func(t *testing.T) {
		got := OutOfOrder([]Cue{cue(1, 10, 0, 10, Pending), cue(2, 9, 0, 10, Pending)})
		if len(got) != 1 || got[0] != (OrderWarning{CueID: 2, PrevCueID: 1}) {
			t.Errorf("got %v", got)
		}
	})
	t.Run("the cue compared against is the last one not skipped", func(t *testing.T) {
		// Cue 2 is skipped: cue 3 is judged against cue 1, not cue 2.
		got := OutOfOrder([]Cue{cue(1, 10, 0, 10, Pending), cue(2, 12, 0, 10, Skipped), cue(3, 11, 0, 10, Pending)})
		if len(got) != 0 {
			t.Errorf("a skipped previous cue made a warning: %v", got)
		}
		got = OutOfOrder([]Cue{cue(1, 11, 0, 10, Pending), cue(2, 9, 0, 10, Skipped), cue(3, 10, 0, 10, Pending)})
		if len(got) != 1 || got[0] != (OrderWarning{CueID: 3, PrevCueID: 1}) {
			t.Errorf("got %v, want cue 3 against cue 1", got)
		}
	})
	t.Run("a skipped cue is never warned about itself", func(t *testing.T) {
		if got := OutOfOrder([]Cue{cue(1, 10, 0, 10, Pending), cue(2, 9, 0, 10, Skipped)}); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("only pending cues warn", func(t *testing.T) {
		for _, st := range []Status{InProgress, Completed} {
			if got := OutOfOrder([]Cue{cue(1, 10, 0, 10, Pending), cue(2, 9, 0, 10, st)}); len(got) != 0 {
				t.Errorf("%s warned: %v", st, got)
			}
		}
	})
	t.Run("equal start times do not warn", func(t *testing.T) {
		if got := OutOfOrder([]Cue{cue(1, 10, 0, 10, Pending), cue(2, 10, 0, 10, Pending)}); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("one minute earlier does", func(t *testing.T) {
		if got := OutOfOrder([]Cue{cue(1, 10, 0, 10, Pending), cue(2, 9, 59, 10, Pending)}); len(got) != 1 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("a fixed cue is only shown, like any other", func(t *testing.T) {
		got := OutOfOrder([]Cue{cue(1, 10, 0, 10, Pending), fixed(cue(2, 9, 0, 10, Pending))})
		if len(got) != 1 || got[0].CueID != 2 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("effective start counts the shift, not the schedule", func(t *testing.T) {
		got := OutOfOrder([]Cue{offset(cue(1, 10, 0, 10, Pending), 30), cue(2, 10, 10, 10, Pending)})
		if len(got) != 1 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("seq order decides, not the slice order", func(t *testing.T) {
		a, b := cue(1, 10, 0, 10, Pending), cue(2, 9, 0, 10, Pending)
		if got := OutOfOrder([]Cue{b, a}); len(got) != 1 || got[0].CueID != 2 {
			t.Errorf("got %v", got)
		}
	})
}

// US-M-6.2 / US-M-3.2: skipping the last pending cue, then advancing,
// completes the one in progress and starts nothing.
func TestUS_M_6_AdvanceAfterSkippingTheLastPendingCue(t *testing.T) {
	cues := []Cue{cue(1, 10, 0, 30, InProgress), cue(2, 10, 30, 30, Pending)}
	skip, err := Skip(cues, 2)
	if err != nil {
		t.Fatal(err)
	}
	cues = skip.ApplyTo(cues)
	op, err := Advance(cues, at(10, 35))
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Changes) != 1 || op.Changes[0].After.ID != 1 || op.Changes[0].After.Status != Completed {
		t.Fatalf("changes = %+v, want only cue 1 completed", op.Changes)
	}
	got := byID(op.ApplyTo(cues))
	if got[2].Status != Skipped || !got[2].ActualStart.IsZero() {
		t.Errorf("cue 2 = %+v, want still skipped", got[2])
	}
	// With everything done or skipped, there is nothing left to advance.
	if _, err := Advance(op.ApplyTo(cues), at(10, 40)); !errors.Is(err, ErrNothingToAdvance) {
		t.Errorf("err = %v, want ErrNothingToAdvance", err)
	}
	// All skipped, none in progress: also nothing to advance.
	all := []Cue{cue(1, 10, 0, 30, Skipped), cue(2, 10, 30, 30, Skipped)}
	if _, err := Advance(all, at(10, 0)); !errors.Is(err, ErrNothingToAdvance) {
		t.Errorf("all skipped: err = %v, want ErrNothingToAdvance", err)
	}
}

// US-M-5 edge: after the overdue next cue is skipped, the overrun is
// measured from the cue after it.
func TestUS_M_5_OverrunReferenceMovesWhenTheNextCueIsSkipped(t *testing.T) {
	cues := []Cue{
		cue(1, 10, 0, 30, InProgress),
		cue(2, 10, 30, 15, Pending),
		cue(3, 11, 0, 15, Pending),
	}
	now := at(10, 45)
	if got := OverrunMin(cues, now); got != 15 {
		t.Fatalf("before the skip: overrun = %d, want 15", got)
	}
	skip, err := Skip(cues, 2)
	if err != nil {
		t.Fatal(err)
	}
	after := skip.ApplyTo(cues)
	if got := OverrunMin(after, now); got != 0 {
		t.Errorf("after the skip: overrun = %d, want 0 (cue 3 starts 11:00)", got)
	}
	if got := OverrunMin(after, at(11, 20)); got != 20 {
		t.Errorf("at 11:20: overrun = %d, want 20 from cue 3", got)
	}
}

// US-M-4.8: a shift cannot be undone once one of the cues it moved has
// moved on since (advanced, skipped or shifted again).
func TestUS_M_4_8_UndoOfAShiftIsRefusedWhenACueMovedOn(t *testing.T) {
	cues := []Cue{cue(1, 10, 0, 30, Completed), cue(2, 10, 30, 30, Pending), cue(3, 11, 0, 30, Pending)}
	plan, err := PlanShift(cues, ShiftRequest{FromCueID: 2, DeltaMin: 10}, at(10, 10))
	if err != nil {
		t.Fatal(err)
	}
	shifted := plan.Op.ApplyTo(cues)

	advance := mustAdvance(t, shifted, at(10, 40))
	skip, err := Skip(shifted, 3)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PlanShift(shifted, ShiftRequest{FromCueID: 2, DeltaMin: 5}, at(10, 10))
	if err != nil {
		t.Fatal(err)
	}
	for name, op := range map[string]Op{"advanced": advance, "skipped": skip, "shifted again": again.Op} {
		if _, err := Undo(op.ApplyTo(shifted), plan.Op); !errors.Is(err, ErrUndoStale) {
			t.Errorf("%s: err = %v, want ErrUndoStale", name, err)
		}
	}
	// Untouched, the same undo works.
	if _, err := Undo(shifted, plan.Op); err != nil {
		t.Errorf("undo of the latest shift: %v", err)
	}
}

// US-M-8.1 / US-M-10.6: a cue may start from 00:00 of the event day up to,
// not including, 04:00 the next day.
func TestUS_M_8_1_ValidCueStartBoundaries(t *testing.T) {
	e := Event{Date: Date{2027, time.June, 16}, Location: taipei}
	next := func(h, m int) time.Time { return time.Date(2027, 6, 17, h, m, 0, 0, taipei) }
	for _, c := range []struct {
		name string
		t    time.Time
		want bool
	}{
		{"day start", at(0, 0), true},
		{"a minute before day start", at(0, 0).Add(-time.Minute), false},
		{"a nanosecond before day start", at(0, 0).Add(-time.Nanosecond), false},
		{"next day 03:59", next(3, 59), true},
		{"next day 04:00", next(4, 0), false},
		{"next day 04:01", next(4, 1), false},
	} {
		if got := e.ValidCueStart(c.t); got != c.want {
			t.Errorf("%s: ValidCueStart = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUS_M_8_1_CleanTitleAndNoteBoundaries(t *testing.T) {
	for _, c := range []struct {
		title string
		want  bool
	}{
		{"", false}, {"   ", false}, {"字", true}, {strings.Repeat("字", 100), true}, {strings.Repeat("字", 101), false},
		{"  " + strings.Repeat("字", 100) + "  ", true},
	} {
		if _, ok := CleanTitle(c.title); ok != c.want {
			t.Errorf("CleanTitle(%d runes) = %v, want %v", len([]rune(c.title)), ok, c.want)
		}
	}
	s500, s501, empty := strings.Repeat("字", 500), strings.Repeat("字", 501), ""
	if !ValidNote(nil) || !ValidNote(&empty) || !ValidNote(&s500) || ValidNote(&s501) {
		t.Error("ValidNote boundaries: nil, empty and 500 pass, 501 fails")
	}
}

// US-M-11.1 / US-M-11.4: a role name is 1-50 characters without ";",
// control or zero-width characters, after trimming.
func TestUS_M_11_4_CleanRoleNameBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"only spaces", "   ", false},
		{"one", "伴", true},
		{"fifty", strings.Repeat("名", 50), true},
		{"fifty-one", strings.Repeat("名", 51), false},
		{"padded fifty", " " + strings.Repeat("名", 50) + " ", true},
		{"semicolon", "伴;娘", false},
		{"control", "伴\x07娘", false},
		{"newline inside", "伴\n娘", false},
		{"zero width space", "伴​娘", false},
		{"bidi override", "伴‮娘", false},
		{"invalid utf8", "伴\xffa", false},
	} {
		if _, ok := CleanRoleName(c.in); ok != c.want {
			t.Errorf("%s: CleanRoleName ok = %v, want %v", c.name, ok, c.want)
		}
	}
	if got, _ := CleanRoleName("  伴娘 "); got != "伴娘" {
		t.Errorf("trimmed name = %q", got)
	}
}
