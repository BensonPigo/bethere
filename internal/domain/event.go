package domain

import "time"

type EventStatus string

// Ended is derived from the date, never stored (data-model.md M9).
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

type Event struct {
	ID            int64
	Name          string
	Date          Date
	Location      *time.Location // fixed at creation
	Status        EventStatus
	LiveStartedAt time.Time
}

// retentionDays is how long an ended event is kept (tech-stack.md T10).
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
	days := int(time.Date(to.Year, to.Month, to.Day, 0, 0, 0, 0, time.UTC).
		Sub(time.Date(e.Date.Year, e.Date.Month, e.Date.Day, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
	out := make([]Cue, len(cues))
	for i, c := range cues {
		c.ScheduledStart = c.ScheduledStart.In(e.Location).AddDate(0, 0, days)
		out[i] = c
	}
	return moved, out, nil
}
