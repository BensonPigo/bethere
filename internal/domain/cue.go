package domain

import (
	"slices"
	"time"
)

type Status string

const (
	Pending    Status = "Pending"
	InProgress Status = "InProgress"
	Completed  Status = "Completed"
	Skipped    Status = "Skipped"
)

// Cue is one rundown item. ScheduledStart only changes when the rundown
// is edited; shifts accumulate in OffsetMin. Zero ActualStart/ActualEnd
// mean "not yet".
type Cue struct {
	ID             int64
	Seq            int
	Title          string
	ScheduledStart time.Time
	DurationMin    int
	IsFixed        bool
	OffsetMin      int
	Status         Status
	ActualStart    time.Time
	ActualEnd      time.Time
}

func (c Cue) EffectiveStart() time.Time {
	return c.ScheduledStart.Add(time.Duration(c.OffsetMin) * time.Minute)
}

func (c Cue) EffectiveEnd() time.Time {
	return c.EffectiveStart().Add(time.Duration(c.DurationMin) * time.Minute)
}

// sameState reports whether two versions of a cue agree on everything an
// Op can change.
func sameState(a, b Cue) bool {
	return a.ID == b.ID && a.Status == b.Status && a.OffsetMin == b.OffsetMin &&
		a.ActualStart.Equal(b.ActualStart) && a.ActualEnd.Equal(b.ActualEnd)
}

// withStateOf returns c carrying src's operation-owned fields; fields an
// Op never touches, such as a title edited live (access-control.md section 4), are kept.
func withStateOf(c, src Cue) Cue {
	c.Status, c.OffsetMin, c.ActualStart, c.ActualEnd = src.Status, src.OffsetMin, src.ActualStart, src.ActualEnd
	return c
}

// bySeq returns a copy of cues ordered by Seq.
func bySeq(cues []Cue) []Cue {
	out := slices.Clone(cues)
	slices.SortStableFunc(out, func(a, b Cue) int { return a.Seq - b.Seq })
	return out
}

func find(cues []Cue, id int64) (Cue, bool) {
	for _, c := range cues {
		if c.ID == id {
			return c, true
		}
	}
	return Cue{}, false
}
