package api

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// The JSON shapes of api.md section 5. Every key is always present; a
// value that does not exist is null. The key sets are fixed by tests
// (tech-stack.md 2.2「型別」); adding a field here is a contract change.

type roleView struct {
	Type     domain.RoleType `json:"type"`
	Name     string          `json:"name"`
	View     domain.View     `json:"view"`     // the screen to open (ADR-0025)
	ChecksIn bool            `json:"checksIn"` // whether it checks in (M2c)
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
	OutOfOrder   []orderView        `json:"outOfOrder"` // US-M-8.6
	// The four jobs (api.md 9.7): always arrays for the Master.
	Tasks     []taskView  `json:"tasks"`
	Guests    []guestView `json:"guests"`
	Checklist []checkView `json:"checklist"`
}

// taskView, guestView and checkView are api.md 9.4–9.6; roles by name,
// null when the role was deleted.
type taskView struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	CreatedBy *string `json:"createdBy"`
	CreatedAt string  `json:"createdAt"`
	ClaimedBy *string `json:"claimedBy"`
	State     string  `json:"state"`
}

type guestView struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Table   string  `json:"table"`
	Note    *string `json:"note"`
	Arrived bool    `json:"arrived"`
}

type checkView struct {
	ID     int64   `json:"id"`
	Title  string  `json:"title"`
	Done   bool    `json:"done"`
	DoneBy *string `json:"doneBy"`
}

type orderView struct {
	CueID     int64 `json:"cueId"`
	PrevCueID int64 `json:"prevCueId"`
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
	AckedAt  *string `json:"ackedAt"` // a venue lead's ack (api.md 9.3)
}

type undoView struct {
	Available   bool    `json:"available"`
	Description *string `json:"description"`
}

// roleListItem never carries a token or its hash (5.2, 5.5).
type roleListItem struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Type       domain.RoleType `json:"type"`
	View       domain.View     `json:"view"`     // M2c
	ChecksIn   bool            `json:"checksIn"` // M2c
	CheckedIn  bool            `json:"checkedIn"`
	LastSeenAt *string         `json:"lastSeenAt"` // to the minute, so it changes the ETag at most once a minute
	HasLink    bool            `json:"hasLink"`
}

// roleSnapshot is api.md 5.1 + 5.3: only the caller's own cues, and of
// the cue in progress only its title (US-X-6.4).
type roleSnapshot struct {
	EventVersion int64              `json:"eventVersion"`
	ServerTime   string             `json:"serverTime"`
	EventStatus  domain.EventStatus `json:"eventStatus"`
	Role         roleView           `json:"role"`
	EventDay     eventDayView       `json:"eventDay"`
	Cues         []roleCue          `json:"cues"`
	Notice       *noticeView        `json:"notice"`
	MyCheckIn    bool               `json:"myCheckIn"`
	CurrentTitle *string            `json:"currentTitle"`
	DelayMin     int                `json:"delayMin"` // the hosts' delay, for the MC too (api.md 9.1)
	// Only for the job that uses each (api.md 9.4–9.6); null otherwise.
	Tasks     *[]taskView  `json:"tasks"`
	Guests    *[]guestView `json:"guests"`
	Checklist *[]checkView `json:"checklist"`
}

// noticeView is the newer of two notices (api.md 5.3, US-X-7.4), about
// cues still assigned to the caller only: the latest shift, or undo of
// one, that moved its cues, listing only those ("shift"); or the latest
// time one of its cues was newly assigned to it ("assign", ADR-0021). The
// client shows it once, telling notices apart by At.
type noticeView struct {
	Kind string `json:"kind"` // "shift" or "assign"
	At   string `json:"at"`
	// shift
	DeltaMin int       `json:"deltaMin,omitempty"`
	Changes  []cueMove `json:"changes,omitempty"`
	// assign
	CueID          int64  `json:"cueId,omitempty"`
	Title          string `json:"title,omitempty"`
	EffectiveStart string `json:"effectiveStart,omitempty"`
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
	AckedAt        *string       `json:"ackedAt"` // own ack, venue leads only (api.md 9.3)
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
func (a *API) buildSnapshot(d store.SnapshotData, now time.Time) (any, error) {
	switch {
	case d.Owner.RoleType == domain.Master:
		return buildMaster(d, now, a.LastSeen)
	case d.Owner.RoleType.HasOwnCues():
		return buildRole(d, now), nil
	case d.Owner.RoleType == domain.Host:
		return buildHost(d, now), nil
	}
	return nil, errNoSnapshot
}

// currentTitle is the title of the cue in progress, whoever it is
// assigned to, and nothing else of it (US-H-1.1, US-X-6.4).
func currentTitle(d store.SnapshotData) *string {
	for _, c := range d.Cues {
		if c.Status == domain.InProgress {
			title := c.Title
			return &title
		}
	}
	return nil
}

func ownRole(d store.SnapshotData) roleView {
	return roleView{Type: d.Owner.RoleType, Name: d.Owner.RoleName, View: d.Owner.View, ChecksIn: d.Owner.ChecksIn}
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
	s.CurrentTitle = currentTitle(d)
	return s
}

func buildMaster(d store.SnapshotData, now time.Time, lastSeen func(tokenID int64) (time.Time, bool)) (masterSnapshot, error) {
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
			mc.Roles = append(mc.Roles, masterCueRole{RoleID: r.RoleID, Name: r.RoleName, RoleNote: r.RoleNote,
				AckedAt: ackedAt(d, c.ID, r.RoleID)})
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
		item := roleListItem{ID: r.ID, Name: r.Name, Type: r.Type, View: r.View, ChecksIn: r.ChecksIn,
			CheckedIn: r.CheckedIn, HasLink: r.ActiveTokenID != 0}
		if t, ok := lastSeen(r.ActiveTokenID); ok {
			item.LastSeenAt = optTime(t.Truncate(time.Minute))
		}
		s.Roles = append(s.Roles, item)
	}
	cues := plainCues(d)
	s.Conflicts = conflictViews(domain.Conflicts(cues))
	s.OutOfOrder = []orderView{}
	for _, o := range domain.OutOfOrder(cues) {
		s.OutOfOrder = append(s.OutOfOrder, orderView{CueID: o.CueID, PrevCueID: o.PrevCueID})
	}
	if d.Event.Status == domain.Live {
		s.OverrunMin = domain.OverrunMin(cues, now)
	}
	names := roleNames(d)
	s.Tasks = taskViews(d.Tasks, names, func(store.Task) bool { return true })
	s.Guests = guestViews(d.Guests)
	s.Checklist = checkViews(d.Checklist, names)
	// In draft the only cue operation is a skip, which can be undone too
	// (US-M-6.4). An operation whose cues were deleted or replaced since
	// (M2c) can no longer be undone and offers no button (api.md 5.2).
	if d.LastOp != nil && undoable(cues, *d.LastOp) {
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
		CurrentTitle: currentTitle(d),
	}
	for _, r := range d.Roles {
		if r.ID == d.Owner.RoleID {
			s.MyCheckIn = r.CheckedIn
		}
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
			if d.Owner.RoleType == domain.VenueLead {
				s.Cues[len(s.Cues)-1].AckedAt = ackedAt(d, c.ID, d.Owner.RoleID)
			}
			mine[c.ID] = c.Cue
			break
		}
	}
	s.Notice = noticeFor(d.Shifts, d.Assigns, d.Owner.RoleID, mine)
	if d.Event.Status == domain.Live {
		s.DelayMin = domain.DelayMin(plainCues(d), now)
	}
	names, me := roleNames(d), d.Owner.RoleID
	switch d.Owner.RoleType {
	case domain.Runner:
		done := 0
		v := taskViews(d.Tasks, names, func(t store.Task) bool {
			if t.State != store.TaskDone {
				return true
			}
			if t.ClaimedBy == me && done < store.RecentDoneTasks {
				done++
				return true
			}
			return false
		})
		s.Tasks = &v
	case domain.Reception:
		v := taskViews(d.Tasks, names, func(t store.Task) bool { return t.CreatedBy == me })
		g := guestViews(d.Guests)
		s.Tasks, s.Guests = &v, &g
	case domain.Cashier:
		c := checkViews(d.Checklist, names)
		s.Checklist = &c
	}
	return s
}

// noticeFor is the newer of the shift and the assignment notice.
func noticeFor(shifts []store.ShiftRecord, assigns []store.AssignRecord, roleID int64, mine map[int64]domain.Cue) *noticeView {
	shift, assign := shiftNotice(shifts, mine), assignNotice(assigns, roleID, mine)
	switch {
	case assign == nil && shift == nil:
		return nil
	case shift == nil, assign != nil && assign.at.After(shift.at):
		return assign.view
	}
	return shift.view
}

type datedNotice struct {
	at   time.Time
	view *noticeView
}

// assignNotice is the latest record that newly showed the caller a cue it
// still has: a cue taken off it again tells it nothing (api.md 5.3).
func assignNotice(assigns []store.AssignRecord, roleID int64, mine map[int64]domain.Cue) *datedNotice {
	for _, a := range assigns {
		c, ok := mine[a.CueID]
		if !ok || !slices.Contains(a.Added, roleID) {
			continue
		}
		return &datedNotice{at: a.At, view: &noticeView{
			Kind: "assign", At: FormatTime(a.At), CueID: c.ID, Title: c.Title, EffectiveStart: FormatTime(c.EffectiveStart()),
		}}
	}
	return nil
}

// shiftNotice picks the latest shift that moved one of the caller's cues
// and keeps only those cues. Times use each cue's current scheduled start;
// the log holds offsets only.
func shiftNotice(shifts []store.ShiftRecord, mine map[int64]domain.Cue) *datedNotice {
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
			return &datedNotice{at: sh.At, view: &n}
		}
	}
	return nil
}

// undoable runs the undo on the current cues without writing: the same
// check UndoLastCueOp makes, so the button is offered only when it works.
func undoable(cues []domain.Cue, op domain.Op) bool {
	_, err := domain.Undo(cues, op)
	return err == nil
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

// ackedAt is when roleID acknowledged cueID, or null.
func ackedAt(d store.SnapshotData, cueID, roleID int64) *string {
	if at, ok := d.Acks[cueID][roleID]; ok {
		return optTime(at)
	}
	return nil
}

func roleNames(d store.SnapshotData) map[int64]string {
	names := make(map[int64]string, len(d.Roles))
	for _, r := range d.Roles {
		names[r.ID] = r.Name
	}
	return names
}

func nameOf(names map[int64]string, id int64) *string {
	if n, ok := names[id]; ok && id != 0 {
		return &n
	}
	return nil
}

// taskViews keeps the tasks the caller may see, newest first.
func taskViews(ts []store.Task, names map[int64]string, keep func(store.Task) bool) []taskView {
	out := []taskView{}
	for _, t := range ts {
		if !keep(t) {
			continue
		}
		out = append(out, taskView{ID: t.ID, Title: t.Title, CreatedBy: nameOf(names, t.CreatedBy),
			CreatedAt: FormatTime(t.CreatedAt), ClaimedBy: nameOf(names, t.ClaimedBy), State: t.State})
	}
	return out
}

func guestViews(gs []store.Guest) []guestView {
	out := make([]guestView, 0, len(gs))
	for _, g := range gs {
		out = append(out, guestView{ID: g.ID, Name: g.Name, Table: g.Table, Note: g.Note, Arrived: g.Arrived})
	}
	return out
}

func checkViews(items []store.ChecklistItem, names map[int64]string) []checkView {
	out := make([]checkView, 0, len(items))
	for _, it := range items {
		out = append(out, checkView{ID: it.ID, Title: it.Title, Done: it.Done, DoneBy: nameOf(names, it.DoneBy)})
	}
	return out
}
