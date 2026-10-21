// Package domain holds BeThere's rules with no storage or HTTP: event
// lifecycle, cue state transitions, time shifting and undo
// (docs/design/data-model.md section 4).
//
// Functions never mutate their inputs. Cue operations return an Op that
// records every touched cue before and after; the caller persists the
// After states in one transaction and keeps the Op so the latest one can
// be undone exactly.
package domain

import (
	"errors"

	_ "time/tzdata" // event time zones must resolve on any host (tech-stack.md T12)
)

var (
	ErrCueNotFound        = errors.New("cue not found")
	ErrInvalidDelta       = errors.New("shift delta must be non-zero and within a day")
	ErrShiftTooEarly      = errors.New("shift would move a cue before now")
	ErrNothingToAdvance   = errors.New("no cue left to advance")
	ErrMultipleInProgress = errors.New("more than one cue in progress")
	ErrNotPending         = errors.New("cue is not pending")
	ErrUndoStale          = errors.New("cues changed since the operation")
	ErrEmptyOp            = errors.New("operation changes no cue")
	ErrNotDraft           = errors.New("event is not in draft")
	ErrNotLive            = errors.New("event is not live")
	ErrEnded              = errors.New("event has ended")
	ErrNotEventDay        = errors.New("not the event day")
	ErrInvalidDate        = errors.New("date must be a real day with a four-digit year")
)
