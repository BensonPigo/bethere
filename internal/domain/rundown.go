package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Limits of the text and length of a cue added or edited by the Master
// (api.md section 3).
const (
	MaxTitleRunes  = 100
	MaxNoteRunes   = 500
	MaxDurationMin = 24 * 60
)

// CleanTitle trims a cue title and reports whether it is 1–MaxTitleRunes
// characters.
func CleanTitle(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	return s, utf8.ValidString(s) && n >= 1 && n <= MaxTitleRunes
}

// ValidNote reports whether an optional note fits; nil is no note.
func ValidNote(s *string) bool {
	return s == nil || (utf8.ValidString(*s) && utf8.RuneCountInString(*s) <= MaxNoteRunes)
}

// ValidCueStart reports whether t is a start a cue added to e may have:
// from 00:00 of the event day in its time zone up to, not including, the
// event's end (api.md section 3). It is a rule about the cue, never the
// event ending: a late start is an invalid cue, not "ended".
func (e Event) ValidCueStart(t time.Time) bool {
	dayStart := time.Date(e.Date.Year, e.Date.Month, e.Date.Day, 0, 0, 0, 0, e.Location)
	return !t.Before(dayStart) && t.Before(e.EndsAt())
}

// OrderWarning is a pending cue that starts before the cue ahead of it.
type OrderWarning struct {
	CueID, PrevCueID int64
}

// OutOfOrder lists pending cues whose effective start is earlier than that
// of the nearest cue before them that was not skipped. Unticked shifts and
// cues added live can cause it; it is shown, never refused (data-model.md
// 4.2 step 5, ADR-0021).
func OutOfOrder(cues []Cue) []OrderWarning {
	var out []OrderWarning
	var prev *Cue
	ordered := bySeq(cues)
	for i := range ordered {
		c := &ordered[i]
		if c.Status == Skipped {
			continue
		}
		if prev != nil && c.Status == Pending && c.EffectiveStart().Before(prev.EffectiveStart()) {
			out = append(out, OrderWarning{CueID: c.ID, PrevCueID: prev.ID})
		}
		prev = c
	}
	return out
}
