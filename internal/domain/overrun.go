package domain

import "time"

// OverrunPromptMin is when the console suggests a shift and the hosts'
// dashboard stops saying all is well (ADR-0022). A starting value, to be
// tuned after the M2a rehearsal.
const OverrunPromptMin = 5

// nextPending is the pending cue with the smallest seq.
func nextPending(cues []Cue) (Cue, bool) {
	for _, c := range bySeq(cues) {
		if c.Status == Pending {
			return c, true
		}
	}
	return Cue{}, false
}

// OverrunMin is how many whole minutes have passed since the next pending
// cue should have started; 0 when it has not (data-model.md 4.3). It is
// measured against the next pending cue, not the end of the one in
// progress, because a cue in progress is never shifted: once the Master
// shifts the rest, the overrun is back to 0.
func OverrunMin(cues []Cue, now time.Time) int {
	c, ok := nextPending(cues)
	if !ok {
		return 0
	}
	if late := now.Sub(c.EffectiveStart()); late > 0 {
		return int(late / time.Minute)
	}
	return 0
}

// DisplayOffsetMin is the "+N 分" shown everywhere: the offset of the cue
// in progress, or of the next pending cue when none is (data-model.md 4.3).
func DisplayOffsetMin(cues []Cue) int {
	for _, c := range cues {
		if c.Status == InProgress {
			return c.OffsetMin
		}
	}
	if c, ok := nextPending(cues); ok {
		return c.OffsetMin
	}
	return 0
}

// DelayMin is the hosts' "延誤 N 分": how late the next pending cue is
// against the rundown, that is its shift plus the overrun the Master has
// not shifted yet (ADR-0026). It is measured on the next pending cue, the
// same one as the overrun, because the cue in progress keeps the offset it
// had when it started. With nothing pending, the shown offset is used.
func DelayMin(cues []Cue, now time.Time) int {
	if c, ok := nextPending(cues); ok {
		return c.OffsetMin + OverrunMin(cues, now)
	}
	return DisplayOffsetMin(cues)
}

// SuggestedShiftMin rounds an overrun up to the next multiple of 5, the
// shift the console prefills (7 → 10).
func SuggestedShiftMin(overrunMin int) int {
	if overrunMin <= 0 {
		return 0
	}
	return (overrunMin + 4) / 5 * 5
}
