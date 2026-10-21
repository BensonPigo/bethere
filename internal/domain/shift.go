package domain

import (
	"fmt"
	"slices"
	"time"
)

type ShiftRequest struct {
	FromCueID int64
	DeltaMin  int
	Excluded  []int64
}

// Conflict means a cue is expected to end after the next fixed cue starts.
// Conflicts are shown, never enforced (data-model.md M6).
type Conflict struct {
	CueID      int64
	FixedCueID int64
	OverrunMin int
}

type ShiftPlan struct {
	Op        Op
	Conflicts []Conflict // over the whole rundown after the shift
}

type ShiftTooEarlyError struct {
	CueID    int64
	NewStart time.Time
}

func (e *ShiftTooEarlyError) Error() string {
	return fmt.Sprintf("cue %d would start at %s, before now", e.CueID, e.NewStart.Format(time.RFC3339))
}

func (e *ShiftTooEarlyError) Unwrap() error { return ErrShiftTooEarly }

// PlanShift computes a time shift without applying it, so the same code
// serves the preview and the real write (tech-stack.md T5). Rules follow
// data-model.md 4.2.
func PlanShift(cues []Cue, req ShiftRequest, now time.Time) (ShiftPlan, error) {
	if req.DeltaMin == 0 {
		return ShiftPlan{}, ErrInvalidDelta
	}
	from, ok := find(cues, req.FromCueID)
	if !ok {
		return ShiftPlan{}, ErrCueNotFound
	}

	op := Op{Kind: OpShift}
	for _, c := range bySeq(cues) {
		if c.Seq < from.Seq || c.Status != Pending || c.IsFixed || slices.Contains(req.Excluded, c.ID) {
			continue
		}
		if req.DeltaMin < 0 && c.EffectiveStart().Before(now) {
			continue // already overdue; pulling it earlier means nothing
		}
		shifted := c
		shifted.OffsetMin += req.DeltaMin
		if req.DeltaMin < 0 && shifted.EffectiveStart().Before(now) {
			return ShiftPlan{}, &ShiftTooEarlyError{CueID: c.ID, NewStart: shifted.EffectiveStart()}
		}
		op.Changes = append(op.Changes, Change{Before: c, After: shifted})
	}
	return ShiftPlan{Op: op, Conflicts: Conflicts(op.ApplyTo(cues))}, nil
}

// Conflicts lists pending or in-progress cues whose effective end is past
// the start of the nearest following fixed cue that has not been skipped.
func Conflicts(cues []Cue) []Conflict {
	ordered := bySeq(cues)
	var out []Conflict
	for i, c := range ordered {
		if c.Status != Pending && c.Status != InProgress {
			continue
		}
		for _, next := range ordered[i+1:] {
			if !next.IsFixed || next.Status == Skipped {
				continue
			}
			if over := c.EffectiveEnd().Sub(next.ScheduledStart); over > 0 {
				out = append(out, Conflict{CueID: c.ID, FixedCueID: next.ID, OverrunMin: int(over / time.Minute)})
			}
			break
		}
	}
	return out
}
