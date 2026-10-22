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
// (tech-stack.md 2.2「型別」); adding a field here is a contract change.

type roleView struct {
	Type domain.RoleType `json:"type"`
	Name string          `json:"name"`
	View domain.View     `json:"view"` // the screen to open (ADR-0025)
}

// eventDayView lets a draft count down on the event day, offline too
// (ADR-0020).
type eventDayView struct {
	Date     string `json:"date"`
	Timezone string `json:"timezone"`
}

type conflictView struct {
	CueID      int64 `json:"cueId"`
	FixedCueID int64 `json:"fixedCueId"`
	OverrunMin int   `json:"overrunMin"`
}

func conflictViews(cs []domain.Conflict) []conflictView {
	out := make([]conflictView, 0, len(cs))
	for _, c := range cs {
		out = append(out, conflictView{CueID: c.CueID, FixedCueID: c.FixedCueID, OverrunMin: c.OverrunMin})
	}
	return out
}

// masterSnapshot is api.md 5.1 + 5.2.
type masterSnapshot struct {
	EventVersion int64              `json:"eventVersion"`
	ServerTime   string             `json:"serverTime"`
	EventStatus  domain.EventStatus `json:"eventStatus"`
	Role         roleView           `json:"role"`
	EventDay     eventDayView       `json:"eventDay"`
	Event        eventView          `json:"event"`
	Cues         []masterCue        `json:"cues"`
	ActiveCueID  *int64             `json:"activeCueId"`
	Undo         undoView           `json:"undo"`
	Roles        []roleListItem     `json:"roles"`
	Conflicts    []conflictView     `json:"conflicts"`
	OverrunMin   int                `json:"overrunMin"` // data-model.md 4.3
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
// "current" field (api.md 5.3).
type roleSnapshot struct {
	EventVersion int64              `json:"eventVersion"`
	ServerTime   string             `json:"serverTime"`
	EventStatus  domain.EventStatus `json:"eventStatus"`
	Role         roleView           `json:"role"`
	EventDay     eventDayView       `json:"eventDay"`
	Cues         []roleCue          `json:"cues"`
	Notice       *noticeView        `json:"notice"`
}

// noticeView is the latest shift, or undo of one, that moved at least one
// of the caller's own cues; it lists only those cues (screen-design.md
// 4.4). The client shows it once, telling notices apart by At.
type noticeView struct {
	Kind     string    `json:"kind"` // "shift"
	At       string    `json:"at"`
	DeltaMin int       `json:"deltaMin"`
	Changes  []cueMove `json:"changes"`
}

// hostSnapshot is api.md 5.1 + 5.4: the hosts' dashboard holds no cue
// list, no notes and no roles, only the delay and the current cue's title.
type hostSnapshot struct {
	EventVersion int64              `json:"eventVersion"`
	ServerTime   string             `json:"serverTime"`
	EventStatus  domain.EventStatus `json:"eventStatus"`
	Role         roleView           `json:"role"`
	EventDay     eventDayView       `json:"eventDay"`
	DelayMin     int                `json:"delayMin"`
	CurrentTitle *string            `json:"currentTitle"`
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
// decided by the caller's own role type alone (api.md section 7). Callers have already
// passed the capability check; a type without a snapshot is refused here
// again rather than falling into any default.
func buildSnapshot(d store.SnapshotData, now time.Time) (any, error) {
	switch d.Owner.RoleType {
	case domain.Master:
		return buildMaster(d, now)
	case domain.Stylist:
		return buildRole(d, now), nil
	case domain.Host:
		return buildHost(d, now), nil
	}
	return nil, errNoSnapshot
}

func ownRole(d store.SnapshotData) roleView {
	return roleView{Type: d.Owner.RoleType, Name: d.Owner.RoleName, View: d.Owner.View}
}

func eventDay(e domain.Event) eventDayView {
	return eventDayView{Date: e.Date.String(), Timezone: e.Location.String()}
}

func plainCues(d store.SnapshotData) []domain.Cue {
	out := make([]domain.Cue, len(d.Cues))
	for i, c := range d.Cues {
		out[i] = c.Cue
	}
	return out
}

// buildHost is the dashboard of V3: the delay (data-model.md 4.3) and the
// title of the cue in progress, whoever it is assigned to.
func buildHost(d store.SnapshotData, now time.Time) hostSnapshot {
	s := hostSnapshot{
		EventVersion: d.Version,
		ServerTime:   FormatTime(now),
		EventStatus:  d.Event.Status,
		Role:         ownRole(d),
		EventDay:     eventDay(d.Event),
	}
	if d.Event.Status != domain.Live {
		return s
	}
	s.DelayMin = domain.DelayMin(plainCues(d), now)
	for _, c := range d.Cues {
		if c.Status == domain.InProgress {
			title := c.Title
			s.CurrentTitle = &title
		}
	}
	return s
}

func buildMaster(d store.SnapshotData, now time.Time) (masterSnapshot, error) {
	s := masterSnapshot{
		EventVersion: d.Version,
		ServerTime:   FormatTime(now),
		EventStatus:  d.Event.Status,
		Role:         ownRole(d),
		EventDay:     eventDay(d.Event),
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
	cues := plainCues(d)
	s.Conflicts = conflictViews(domain.Conflicts(cues))
	if d.Event.Status == domain.Live {
		s.OverrunMin = domain.OverrunMin(cues, now)
	}
	if d.Event.Status == domain.Live && d.LastOp != nil {
		desc := describeUndo(*d.LastOp, titles)
		s.Undo = undoView{Available: true, Description: &desc}
	}
	return s, nil
}

// buildRole keeps only the cues assigned to the caller's role, each with
// that role's own note; an unassigned cue, in progress or not, is not
// shown at all (US-X-1.2, api.md 5.3).
func buildRole(d store.SnapshotData, now time.Time) roleSnapshot {
	s := roleSnapshot{
		EventVersion: d.Version,
		ServerTime:   FormatTime(now),
		EventStatus:  d.Event.Status,
		Role:         ownRole(d),
		EventDay:     eventDay(d.Event),
		Cues:         []roleCue{},
	}
	mine := map[int64]domain.Cue{}
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
			mine[c.ID] = c.Cue
			break
		}
	}
	s.Notice = noticeFor(d.Shifts, mine)
	return s
}

// noticeFor picks the latest shift that moved one of the caller's cues
// and keeps only those cues. Times use each cue's current scheduled start;
// the log holds offsets only.
func noticeFor(shifts []store.ShiftRecord, mine map[int64]domain.Cue) *noticeView {
	for _, sh := range shifts {
		var n noticeView
		for _, ch := range sh.Changes {
			c, ok := mine[ch.After.ID]
			if !ok {
				continue
			}
			before, after := c, c
			before.OffsetMin, after.OffsetMin = ch.Before.OffsetMin, ch.After.OffsetMin
			n.Changes = append(n.Changes, cueMove{
				CueID: c.ID, FromStart: FormatTime(before.EffectiveStart()), ToStart: FormatTime(after.EffectiveStart()),
			})
			n.DeltaMin = ch.After.OffsetMin - ch.Before.OffsetMin
		}
		if len(n.Changes) > 0 {
			n.Kind, n.At = "shift", FormatTime(sh.At)
			return &n
		}
	}
	return nil
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
