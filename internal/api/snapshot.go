package api

import (
	"errors"
	"fmt"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// The JSON shapes of api.md section 5. Every key is always present; a
// value that does not exist is null. The key sets are fixed by tests
// (T17); adding a field here is a contract change.

type roleView struct {
	Type domain.RoleType `json:"type"`
	Name string          `json:"name"`
}

// masterSnapshot is api.md 5.1 + 5.2.
type masterSnapshot struct {
	EventVersion int64              `json:"eventVersion"`
	ServerTime   string             `json:"serverTime"`
	EventStatus  domain.EventStatus `json:"eventStatus"`
	Role         roleView           `json:"role"`
	Event        eventView          `json:"event"`
	Cues         []masterCue        `json:"cues"`
	ActiveCueID  *int64             `json:"activeCueId"`
	Undo         undoView           `json:"undo"`
	Roles        []roleListItem     `json:"roles"`
}

type eventView struct {
	Name     string `json:"name"`
	Date     string `json:"date"`
	Timezone string `json:"timezone"`
}

type masterCue struct {
	ID             int64           `json:"id"`
	Seq            int             `json:"seq"`
	Title          string          `json:"title"`
	ScheduledStart string          `json:"scheduledStart"`
	EffectiveStart string          `json:"effectiveStart"`
	DurationMin    int             `json:"durationMin"`
	IsFixed        bool            `json:"isFixed"`
	OffsetMin      int             `json:"offsetMin"`
	Status         domain.Status   `json:"status"`
	ActualStart    *string         `json:"actualStart"`
	ActualEnd      *string         `json:"actualEnd"`
	NotePublic     *string         `json:"notePublic"`
	Roles          []masterCueRole `json:"roles"`
}

type masterCueRole struct {
	RoleID   int64   `json:"roleId"`
	Name     string  `json:"name"`
	RoleNote *string `json:"roleNote"`
}

type undoView struct {
	Available   bool    `json:"available"`
	Description *string `json:"description"`
}

// roleListItem never carries a token, its hash or usage (5.2, 5.5).
type roleListItem struct {
	ID   int64           `json:"id"`
	Name string          `json:"name"`
	Type domain.RoleType `json:"type"`
}

// roleSnapshot is api.md 5.1 + 5.3: only the caller's own cues, and no
// "current" field (A5).
type roleSnapshot struct {
	EventVersion int64              `json:"eventVersion"`
	ServerTime   string             `json:"serverTime"`
	EventStatus  domain.EventStatus `json:"eventStatus"`
	Role         roleView           `json:"role"`
	Cues         []roleCue          `json:"cues"`
}

type roleCue struct {
	ID             int64         `json:"id"`
	Seq            int           `json:"seq"`
	Title          string        `json:"title"`
	EffectiveStart string        `json:"effectiveStart"`
	DurationMin    int           `json:"durationMin"`
	IsFixed        bool          `json:"isFixed"`
	OffsetMin      int           `json:"offsetMin"`
	Status         domain.Status `json:"status"`
	NotePublic     *string       `json:"notePublic"`
	RoleNote       *string       `json:"roleNote"`
}

var (
	errNoSnapshot         = errors.New("role type has no snapshot")
	errMultipleInProgress = domain.ErrMultipleInProgress
)

// FormatTime is the one time format of the API: UTC, RFC 3339 with
// milliseconds, e.g. 2027-06-06T03:15:00.123Z (api.md section 1).
func FormatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func optTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := FormatTime(t)
	return &s
}

// buildSnapshot is the only place a snapshot is shaped; which shape is
// decided by the caller's own role type alone (A14). Callers have already
// passed the capability check; a type without a snapshot is refused here
// again rather than falling into any default.
func buildSnapshot(d store.SnapshotData, now time.Time) (any, error) {
	switch d.Owner.RoleType {
	case domain.Master:
		return buildMaster(d, now)
	case domain.Stylist:
		return buildRole(d, now), nil
	}
	return nil, errNoSnapshot
}

func buildMaster(d store.SnapshotData, now time.Time) (masterSnapshot, error) {
	s := masterSnapshot{
		EventVersion: d.Version,
		ServerTime:   FormatTime(now),
		EventStatus:  d.Event.Status,
		Role:         roleView{Type: d.Owner.RoleType, Name: d.Owner.RoleName},
		Event: eventView{
			Name:     d.Event.Name,
			Date:     d.Event.Date.String(),
			Timezone: d.Event.Location.String(),
		},
		Cues:  make([]masterCue, 0, len(d.Cues)),
		Roles: make([]roleListItem, 0, len(d.Roles)),
	}
	titles := make(map[int64]string, len(d.Cues))
	for _, c := range d.Cues {
		mc := masterCue{
			ID:             c.ID,
			Seq:            c.Seq,
			Title:          c.Title,
			ScheduledStart: FormatTime(c.ScheduledStart),
			EffectiveStart: FormatTime(c.EffectiveStart()),
			DurationMin:    c.DurationMin,
			IsFixed:        c.IsFixed,
			OffsetMin:      c.OffsetMin,
			Status:         c.Status,
			ActualStart:    optTime(c.ActualStart),
			ActualEnd:      optTime(c.ActualEnd),
			NotePublic:     c.NotePublic,
			Roles:          make([]masterCueRole, 0, len(c.Roles)),
		}
		for _, r := range c.Roles {
			mc.Roles = append(mc.Roles, masterCueRole{RoleID: r.RoleID, Name: r.RoleName, RoleNote: r.RoleNote})
		}
		if c.Status == domain.InProgress {
			if s.ActiveCueID != nil {
				return masterSnapshot{}, errMultipleInProgress
			}
			id := c.ID
			s.ActiveCueID = &id
		}
		titles[c.ID] = c.Title
		s.Cues = append(s.Cues, mc)
	}
	for _, r := range d.Roles {
		s.Roles = append(s.Roles, roleListItem{ID: r.ID, Name: r.Name, Type: r.Type})
	}
	if d.Event.Status == domain.Live && d.LastOp != nil {
		desc := describeUndo(*d.LastOp, titles)
		s.Undo = undoView{Available: true, Description: &desc}
	}
	return s, nil
}

// buildRole keeps only the cues assigned to the caller's role, each with
// that role's own note; an unassigned cue, in progress or not, is not
// shown at all (US-X-1.2, A5).
func buildRole(d store.SnapshotData, now time.Time) roleSnapshot {
	s := roleSnapshot{
		EventVersion: d.Version,
		ServerTime:   FormatTime(now),
		EventStatus:  d.Event.Status,
		Role:         roleView{Type: d.Owner.RoleType, Name: d.Owner.RoleName},
		Cues:         []roleCue{},
	}
	for _, c := range d.Cues {
		for _, r := range c.Roles {
			if r.RoleID != d.Owner.RoleID {
				continue
			}
			s.Cues = append(s.Cues, roleCue{
				ID:             c.ID,
				Seq:            c.Seq,
				Title:          c.Title,
				EffectiveStart: FormatTime(c.EffectiveStart()),
				DurationMin:    c.DurationMin,
				IsFixed:        c.IsFixed,
				OffsetMin:      c.OffsetMin,
				Status:         c.Status,
				NotePublic:     c.NotePublic,
				RoleNote:       r.RoleNote,
			})
			break
		}
	}
	return s
}

// describeUndo is the server-made text of the undo button (US-M-4.2), e.g.
// 撤銷：推進『一進預備』. An advance names the cue it started, or the cue it
// completed when it started none (the last cue).
func describeUndo(op domain.Op, titles map[int64]string) string {
	switch op.Kind {
	case domain.OpAdvance:
		var started, completed int64
		for _, ch := range op.Changes {
			switch ch.After.Status {
			case domain.InProgress:
				started = ch.After.ID
			case domain.Completed:
				completed = ch.After.ID
			}
		}
		id := started
		if id == 0 {
			id = completed
		}
		return fmt.Sprintf("撤銷：推進『%s』", titles[id])
	case domain.OpSkip:
		if len(op.Changes) > 0 {
			return fmt.Sprintf("撤銷：略過『%s』", titles[op.Changes[0].After.ID])
		}
	case domain.OpShift:
		if op.Shift != nil {
			return fmt.Sprintf("撤銷：%+d 分，影響 %d 個環節", op.Shift.DeltaMin, len(op.Changes))
		}
	}
	return "撤銷上一步"
}
