package domain

import (
	"slices"
	"time"
)

type EventStatus string

// Ended is derived from the date, never stored (data-model.md, Event).
const (
	Draft EventStatus = "Draft"
	Live  EventStatus = "Live"
)

// Date is a calendar day in the event's time zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// timeZones are the event time zones BeThere accepts. Only names that the
// embedded tzdata holds verbatim are listed, so an event reads back on any
// host (tech-stack.md 2.1「時間」); a host's own zoneinfo may accept variants such as
// "asia/taipei" that others cannot load. Add zones here when needed.
var timeZones = []string{"Asia/Taipei"}

func SupportedTimeZone(name string) bool { return slices.Contains(timeZones, name) }

// MinYear and MaxYear bound event dates and cue times. Times are stored
// as Unix nanoseconds, which end in 2262; well inside that, day arithmetic
// never overflows either.
const (
	MinYear = 2000
	MaxYear = 2199
)

// Valid reports whether d is a real calendar day within MinYear–MaxYear.
func (d Date) Valid() bool {
	if d.Year < MinYear || d.Year > MaxYear {
		return false
	}
	y, m, day := d.midnightUTC().Date()
	return y == d.Year && m == d.Month && day == d.Day
}

// String formats d as YYYY-MM-DD, the form stored in the database.
func (d Date) String() string { return d.midnightUTC().Format("2006-01-02") }

func (d Date) midnightUTC() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// InRange reports whether t falls within MinYear–MaxYear.
func InRange(t time.Time) bool { return t.Year() >= MinYear && t.Year() <= MaxYear }

type Event struct {
	ID            int64
	Name          string
	Date          Date
	Location      *time.Location // fixed at creation
	Status        EventStatus
	LiveStartedAt time.Time
}

// retentionDays is how long an ended event is kept (tech-stack.md 2.1「資料刪除」).
const retentionDays = 80

// EndsAt is 04:00 local time on the day after the event.
func (e Event) EndsAt() time.Time {
	return time.Date(e.Date.Year, e.Date.Month, e.Date.Day+1, 4, 0, 0, 0, e.Location)
}

func (e Event) Ended(now time.Time) bool { return !now.Before(e.EndsAt()) }

// PurgeAt is when the daily cleanup deletes the event.
func (e Event) PurgeAt() time.Time { return e.EndsAt().AddDate(0, 0, retentionDays) }

// Start moves a draft event to live; only allowed on the event day in the
// event's own time zone.
func (e Event) Start(now time.Time) (Event, error) {
	if e.Ended(now) {
		return e, ErrEnded
	}
	if e.Status != Draft {
		return e, ErrNotDraft
	}
	if y, m, d := now.In(e.Location).Date(); (Date{y, m, d}) != e.Date {
		return e, ErrNotEventDay
	}
	e.Status = Live
	e.LiveStartedAt = now
	return e, nil
}

// RequireLive guards every live operation: advance, skip, shift, undo.
func (e Event) RequireLive(now time.Time) error {
	if e.Ended(now) {
		return ErrEnded
	}
	if e.Status != Live {
		return ErrNotLive
	}
	return nil
}

// Reschedule changes the event date with the recovery code. Draft only;
// both the old and new end must be in the future so an ended event cannot
// be revived. Every cue moves by the same number of days, keeping its
// local wall-clock time (access-control.md section 2).
func (e Event) Reschedule(to Date, cues []Cue, now time.Time) (Event, []Cue, error) {
	if !to.Valid() {
		return e, nil, ErrInvalidDate
	}
	if e.Ended(now) {
		return e, nil, ErrEnded
	}
	if e.Status != Draft {
		return e, nil, ErrNotDraft
	}
	moved := e
	moved.Date = to
	if moved.Ended(now) {
		return e, nil, ErrEnded
	}
	days := int((to.midnightUTC().Unix() - e.Date.midnightUTC().Unix()) / (24 * 60 * 60))
	out := make([]Cue, len(cues))
	for i, c := range cues {
		c.ScheduledStart = c.ScheduledStart.In(e.Location).AddDate(0, 0, days)
		out[i] = c
	}
	return moved, out, nil
}
