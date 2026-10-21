package domain

import "time"

// Advance completes the cue in progress and starts the next pending cue
// after it; with nothing in progress it starts the first pending cue
// (data-model.md 4.1).
func Advance(cues []Cue, now time.Time) (Op, error) {
	ordered := bySeq(cues)
	var active *Cue
	for i := range ordered {
		if ordered[i].Status == InProgress {
			if active != nil {
				return Op{}, ErrMultipleInProgress
			}
			active = &ordered[i]
		}
	}

	op := Op{Kind: OpAdvance}
	afterSeq := -1 << 31
	if active != nil {
		done := *active
		done.Status = Completed
		done.ActualEnd = now
		op.Changes = append(op.Changes, Change{Before: *active, After: done})
		afterSeq = active.Seq
	}
	for _, c := range ordered {
		if c.Status == Pending && c.Seq > afterSeq {
			started := c
			started.Status = InProgress
			started.ActualStart = now
			op.Changes = append(op.Changes, Change{Before: c, After: started})
			break
		}
	}
	if len(op.Changes) == 0 {
		return Op{}, ErrNothingToAdvance
	}
	return op, nil
}

// Skip marks a pending cue as skipped. Cues are never deleted live (C1).
func Skip(cues []Cue, id int64) (Op, error) {
	c, ok := find(cues, id)
	if !ok {
		return Op{}, ErrCueNotFound
	}
	if c.Status != Pending {
		return Op{}, ErrNotPending
	}
	skipped := c
	skipped.Status = Skipped
	return Op{Kind: OpSkip, Changes: []Change{{Before: c, After: skipped}}}, nil
}
