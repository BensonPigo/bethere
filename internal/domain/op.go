package domain

type OpKind string

const (
	OpAdvance OpKind = "advance"
	OpSkip    OpKind = "skip"
	OpShift   OpKind = "shift"
)

// Change is one cue as it was before and after an operation.
type Change struct {
	Before, After Cue
}

// Op is a single undoable operation. Undo yields another Op of the same
// kind with each Change reversed. Shift keeps what the Master asked for,
// for the after-event review (C3).
type Op struct {
	Kind    OpKind
	Changes []Change
	Shift   *ShiftRequest
}

// ApplyTo returns a copy of cues with every changed cue replaced by its
// After state.
func (op Op) ApplyTo(cues []Cue) []Cue {
	out := make([]Cue, len(cues))
	for i, c := range cues {
		out[i] = c
		for _, ch := range op.Changes {
			if ch.After.ID == c.ID {
				out[i] = ch.After
			}
		}
	}
	return out
}

// Undo reverses op, which must be the latest operation (data-model.md
// M7). Every cue it touched must still be exactly in its After state;
// otherwise something else changed it and the undo is refused.
func Undo(cues []Cue, op Op) (Op, error) {
	if len(op.Changes) == 0 {
		return Op{}, ErrEmptyOp
	}
	rev := Op{Kind: op.Kind, Shift: op.Shift, Changes: make([]Change, len(op.Changes))}
	for i, ch := range op.Changes {
		cur, ok := find(cues, ch.After.ID)
		if !ok || !sameState(cur, ch.After) {
			return Op{}, ErrUndoStale
		}
		rev.Changes[i] = Change{Before: cur, After: withStateOf(cur, ch.Before)}
	}
	return rev, nil
}
