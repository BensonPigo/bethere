package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// The Master's preparation before the event (M2c, ADR-0031): the draft
// rundown editor, the roles added on screen and the pasted import. Like
// the live edits none of them is a cue operation, so "undo the last step"
// never reverses them (ADR-0021). Every log records which Master link
// acted (byTokenId, never a hash), so changes made with a leaked link can
// be found after the link is replaced (access-control.md 3.4). What a
// delete or a replacing import removes is logged in full, since none of
// them can be undone (ADR-0032).

var (
	// ErrInvalidRole is a role name or screen the Master may not choose
	// (api.md section 3「新增角色」).
	ErrInvalidRole = errors.New("invalid role name or screen")
	// ErrDuplicateName is a role name another role of the event has.
	ErrDuplicateName = errors.New("another role of this event has this name")
	// ErrTooManyRoles refuses a role past domain.MaxRoles.
	ErrTooManyRoles = errors.New("the event has the most roles allowed")
	// ErrInvalidImport is a pasted rundown that cannot be written as is.
	ErrInvalidImport = errors.New("invalid import")
	// ErrLockedOnEventDay refuses replacing the rundown or deleting a role
	// from the event day on (ADR-0032).
	ErrLockedOnEventDay = errors.New("locked on the event day")
	// errDraftOffset is a draft cue with an offset: shifts run only live,
	// so it cannot happen and the edit is refused (api.md section 3).
	errDraftOffset = errors.New("a draft cue has an offset")
)

// The log types of the preparation (api.md section 3, M2c).
const (
	logUpdate     = "rundown.update"
	logMove       = "rundown.move"
	logDelete     = "rundown.delete"
	logImport     = "rundown.import"
	logRoleAdd    = "role.add"
	logRoleRename = "role.rename"
	logRoleDelete = "role.delete"
)

// NewRole is a role the Master adds: a name, a screen and whether it
// checks in. Its type follows from the screen (domain.TypeForView).
type NewRole struct {
	Name     string
	View     domain.View
	ChecksIn bool
}

// check cleans the name and resolves the type; imported roles are shown
// cues, so they may not use the dashboard (api.md section 3).
func (nr NewRole) check(cueScreenOnly bool) (string, domain.RoleType, error) {
	name, ok := domain.CleanRoleName(nr.Name)
	typ, okView := domain.TypeForView(nr.View)
	if !ok || !okView || (typ == domain.Host && (nr.ChecksIn || cueScreenOnly)) {
		return "", "", ErrInvalidRole
	}
	return name, typ, nil
}

// CreateRole adds a role and its first link in one transaction (US-M-11.1,
// US-M-11.2). It does not change the event version: a role is not a cue
// (ADR-0023).
func (s *Store) CreateRole(ctx context.Context, eventID int64, nr NewRole, tokenHash []byte, actor Actor, now time.Time) (int64, error) {
	var roleID int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		name, typ, err := nr.check(false)
		if err != nil {
			return err
		}
		if err := checkNewNames(ctx, tx, eventID, []string{name}); err != nil {
			return err
		}
		if roleID, err = insertRole(ctx, tx, eventID, name, typ, nr); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO access_tokens (role_id, token_hash, created_at) VALUES (?, ?, ?)`,
			roleID, tokenHash, toNS(now)); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, logRoleAdd,
			map[string]any{"roleId": roleID, "name": name, "byTokenId": actor.tokenID}, now)
	})
	return roleID, err
}

// RenameRole renames a role of eventID, the Master included, in draft or
// live (US-M-11.4).
func (s *Store) RenameRole(ctx context.Context, eventID, roleID int64, name string, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		// Ownership before the name is compared with anyone's, so another
		// event's names cannot be probed (api.md section 3).
		if _, err := roleIn(ctx, tx, eventID, roleID); err != nil {
			return err
		}
		clean, ok := domain.CleanRoleName(name)
		if !ok {
			return ErrInvalidRole
		}
		var taken bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE event_id = ? AND name = ? AND id <> ?)`,
			eventID, clean, roleID).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrDuplicateName
		}
		if _, err := tx.ExecContext(ctx, `UPDATE roles SET name = ? WHERE id = ? AND event_id = ?`, clean, roleID, eventID); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, logRoleRename,
			map[string]any{"roleId": roleID, "name": clean, "byTokenId": actor.tokenID}, now)
	})
}

// deletedRole is everything a deleted role had, logged in full (ADR-0032).
type deletedRole struct {
	RoleID      int64          `json:"roleId"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	View        *string        `json:"view"`
	NoCheckIn   bool           `json:"noCheckIn"`
	Assignments []loggedAssign `json:"assignments"`
	ByTokenID   int64          `json:"byTokenId"`
}

type loggedAssign struct {
	CueID    int64   `json:"cueId,omitempty"`
	RoleID   int64   `json:"roleId,omitempty"`
	RoleNote *string `json:"roleNote"`
}

// DeleteRole deletes a role of eventID before the event day (US-M-11.5,
// ADR-0032). Its links, assignments and check-ins go with it through the
// foreign keys, so a phone holding its link gets "invalid" next. It moves
// the version: assignments change.
func (s *Store) DeleteRole(ctx context.Context, eventID, expectVersion, roleID int64, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		typ, err := roleIn(ctx, tx, eventID, roleID)
		if err != nil {
			return err
		}
		if err := requirePrepWindow(e, now); err != nil {
			return err
		}
		if typ == domain.Master {
			return ErrMasterRole
		}
		rec := deletedRole{RoleID: roleID, Type: string(typ), Assignments: []loggedAssign{}, ByTokenID: actor.tokenID}
		var view sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT name, view, checks_in IS NOT NULL FROM roles WHERE id = ?`, roleID).
			Scan(&rec.Name, &view, &rec.NoCheckIn); err != nil {
			return err
		}
		if view.Valid {
			rec.View = &view.String
		}
		rows, err := tx.QueryContext(ctx, `SELECT cue_id, role_note FROM cue_roles WHERE role_id = ? ORDER BY cue_id`, roleID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var (
				a    loggedAssign
				note sql.NullString
			)
			if err := rows.Scan(&a.CueID, &note); err != nil {
				rows.Close()
				return err
			}
			if note.Valid {
				a.RoleNote = &note.String
			}
			rec.Assignments = append(rec.Assignments, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE id = ? AND event_id = ?`, roleID, eventID); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, logRoleDelete, rec, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// CueUpdate is the whole editable part of a draft cue (US-M-10.2).
type CueUpdate struct {
	CueID       int64
	Start       time.Time
	DurationMin int
	IsFixed     bool
	Title       string
	NotePublic  *string
}

// UpdateCue replaces a draft cue's start, length, fixed flag, title and
// public note (US-M-10.2). Shifts run only live, so a draft cue has no
// offset and Start is its scheduled start (api.md section 3).
func (s *Store) UpdateCue(ctx context.Context, eventID, expectVersion int64, u CueUpdate, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		c, err := cueIn(ctx, tx, eventID, u.CueID)
		if err != nil {
			return err
		}
		if e.Status != domain.Draft {
			return domain.ErrNotDraft
		}
		title, ok := domain.CleanTitle(u.Title)
		if !ok || !domain.ValidNote(u.NotePublic) || u.DurationMin < 0 || u.DurationMin > domain.MaxDurationMin ||
			!e.ValidCueStart(u.Start) {
			return ErrInvalidCue
		}
		if c.OffsetMin != 0 {
			return errDraftOffset
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cues SET scheduled_start = ?, duration_min = ?, is_fixed = ?, title = ?, note_public = ?
			WHERE id = ? AND event_id = ?`,
			toNS(u.Start), u.DurationMin, boolInt(u.IsFixed), title, nullNote(u.NotePublic), u.CueID, eventID); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, logUpdate,
			map[string]any{"cueId": u.CueID, "byTokenId": actor.tokenID}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// MoveCue puts a draft cue right after afterCueID, or first when it is 0
// (US-M-10.3). Times stay; an order that no longer matches them is shown
// by outOfOrder (US-M-10.6).
func (s *Store) MoveCue(ctx context.Context, eventID, expectVersion, cueID, afterCueID int64, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		cues, err := loadCues(ctx, tx, eventID)
		if err != nil {
			return err
		}
		has := func(id int64) bool { return slices.ContainsFunc(cues, func(c domain.Cue) bool { return c.ID == id }) }
		if !has(cueID) || (afterCueID != 0 && !has(afterCueID)) {
			return domain.ErrCueNotFound
		}
		if e.Status != domain.Draft {
			return domain.ErrNotDraft
		}
		if afterCueID == cueID {
			return ErrInvalidPosition
		}
		order := make([]int64, 0, len(cues))
		if afterCueID == 0 {
			order = append(order, cueID)
		}
		for _, c := range cues {
			if c.ID == cueID {
				continue
			}
			order = append(order, c.ID)
			if c.ID == afterCueID {
				order = append(order, cueID)
			}
		}
		if err := renumber(ctx, tx, eventID, order); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, logMove,
			map[string]any{"cueId": cueID, "afterCueId": afterCueID, "byTokenId": actor.tokenID}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// loggedCue is everything a deleted or replaced cue had (ADR-0032).
type loggedCue struct {
	CueID          int64          `json:"cueId"`
	Seq            int            `json:"seq"`
	Title          string         `json:"title"`
	ScheduledStart time.Time      `json:"scheduledStart"`
	DurationMin    int            `json:"durationMin"`
	IsFixed        bool           `json:"isFixed"`
	OffsetMin      int            `json:"offsetMin"`
	Status         domain.Status  `json:"status"`
	NotePublic     *string        `json:"notePublic"`
	Roles          []loggedAssign `json:"roles"`
}

// DeleteCue deletes a draft cue with its assignments and closes the gap in
// seq (US-M-10.4). Live, a cue is skipped instead.
func (s *Store) DeleteCue(ctx context.Context, eventID, expectVersion, cueID int64, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		if _, err := cueIn(ctx, tx, eventID, cueID); err != nil {
			return err
		}
		if e.Status != domain.Draft {
			return domain.ErrNotDraft
		}
		all, err := logCues(ctx, tx, eventID)
		if err != nil {
			return err
		}
		var gone loggedCue
		order := make([]int64, 0, len(all))
		for _, c := range all {
			if c.CueID == cueID {
				gone = c
				continue
			}
			order = append(order, c.CueID)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cues WHERE id = ? AND event_id = ?`, cueID, eventID); err != nil {
			return err
		}
		if err := renumber(ctx, tx, eventID, order); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, logDelete,
			map[string]any{"cue": gone, "byTokenId": actor.tokenID}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// ImportCheck is what a pasted rundown would do to an event, without
// writing (US-M-12.2): each row's problems, the role names it mentions
// that the event does not have, and whether creating them would pass the
// role limit.
type ImportCheck struct {
	Problems     [][]string // per row, in row order; empty when the row is fine
	MissingRoles []string   // in order of first mention, each once
	TooManyRoles bool
	Replaces     int // the event's cues now
}

// OK reports whether the import can be confirmed.
func (c ImportCheck) OK() bool {
	if c.TooManyRoles {
		return false
	}
	for _, p := range c.Problems {
		if len(p) > 0 {
			return false
		}
	}
	return true
}

// CheckImport runs the import's rules on the event's roles as they are
// now (US-M-12.2). The data comes from the caller's snapshot read.
func CheckImport(e domain.Event, rows []ImportRow, roles []RoleInfo, cues int) ImportCheck {
	v := validateRows(e, rows, roles, true)
	return ImportCheck{
		Problems:     v.problems,
		MissingRoles: v.missing,
		TooManyRoles: len(roles)+len(v.missing) > domain.MaxRoles,
		Replaces:     cues,
	}
}

// ReplaceRundown replaces a draft event's whole rundown before its day
// (US-M-12.3, US-M-12.4, US-M-12.6, ADR-0032): newRoles must name exactly
// the roles the rows mention that do not exist yet, and are created
// without links. All or nothing; one log holding everything replaced.
func (s *Store) ReplaceRundown(ctx context.Context, eventID, expectVersion int64, rows []ImportRow, newRoles []NewRole,
	actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		if err := requirePrepWindow(e, now); err != nil {
			return err
		}
		roles, err := loadRoles(ctx, tx, eventID)
		if err != nil {
			return err
		}
		v := validateRows(e, rows, roles, true)
		for _, p := range v.problems {
			if len(p) > 0 {
				return ErrInvalidImport
			}
		}
		// The names checked by the preview may be stale: another device
		// may have added a role since (api.md section 3).
		names := make([]string, len(newRoles))
		types := make([]domain.RoleType, len(newRoles))
		for i, nr := range newRoles {
			if names[i], types[i], err = nr.check(true); err != nil {
				return err
			}
		}
		if !sameNames(names, v.missing) {
			return ErrInvalidImport
		}
		if err := checkNewNames(ctx, tx, eventID, names); err != nil {
			return err
		}
		replaced, err := logCues(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cues WHERE event_id = ?`, eventID); err != nil {
			return err
		}
		byName := map[string]int64{}
		for _, r := range roles {
			byName[r.Name] = r.ID
		}
		created := make([]map[string]any, len(newRoles))
		for i, nr := range newRoles {
			id, err := insertRole(ctx, tx, eventID, names[i], types[i], nr)
			if err != nil {
				return err
			}
			byName[names[i]] = id
			created[i] = map[string]any{"roleId": id, "name": names[i]}
		}
		for i, c := range v.cues {
			res, err := tx.ExecContext(ctx, `INSERT INTO cues (event_id, seq, title, scheduled_start, duration_min, is_fixed, note_public)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				eventID, i+1, c.Title, toNS(c.ScheduledStart), c.DurationMin, boolInt(c.IsFixed), nullText(v.rows[i].Note))
			if err != nil {
				return err
			}
			cueID, err := res.LastInsertId()
			if err != nil {
				return err
			}
			for _, name := range v.rows[i].Roles {
				if _, err := tx.ExecContext(ctx, `INSERT INTO cue_roles (cue_id, role_id, role_note) VALUES (?, ?, ?)`,
					cueID, byName[name], nullText(v.rows[i].RoleNote)); err != nil {
					return err
				}
			}
		}
		if err := logAction(ctx, tx, eventID, actor, logImport, map[string]any{
			"replaced": replaced, "rows": len(v.cues), "newRoles": created, "byTokenId": actor.tokenID,
		}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// requirePrepWindow is the window of the destructive edits: a draft, and
// before the event day (ADR-0032). Not being a draft is told first.
func requirePrepWindow(e domain.Event, now time.Time) error {
	if e.Status != domain.Draft {
		return domain.ErrNotDraft
	}
	if e.DayStarted(now) {
		return ErrLockedOnEventDay
	}
	return nil
}

// checkNewNames checks names against the caller's event only, then the
// role limit (api.md section 3).
func checkNewNames(ctx context.Context, tx *sql.Tx, eventID int64, names []string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM roles WHERE event_id = ?`, eventID).Scan(&count); err != nil {
		return err
	}
	for _, name := range names {
		var taken bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE event_id = ? AND name = ?)`,
			eventID, name).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrDuplicateName
		}
	}
	if count+len(names) > domain.MaxRoles {
		return ErrTooManyRoles
	}
	return nil
}

func insertRole(ctx context.Context, tx *sql.Tx, eventID int64, name string, typ domain.RoleType, nr NewRole) (int64, error) {
	// Only NULL (follow the type) or 0 (no check-in) is stored (migration 0003).
	var checksIn any
	if typ.HasOwnCues() && !nr.ChecksIn {
		checksIn = 0
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO roles (event_id, name, type, view, checks_in) VALUES (?, ?, ?, ?, ?)`,
		eventID, name, typ, string(nr.View), checksIn)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// renumber gives the cues of order the seqs 1..n in that order, in two
// steps so UNIQUE (event_id, seq) never sees two equal seqs.
func renumber(ctx context.Context, tx *sql.Tx, eventID int64, order []int64) error {
	for i, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE cues SET seq = ? WHERE id = ? AND event_id = ?`, -(i + 1), id, eventID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE cues SET seq = -seq WHERE event_id = ? AND seq < 0`, eventID)
	return err
}

// logCues reads every cue of the event with its notes and assignments,
// by seq, for the logs of what was removed.
func logCues(ctx context.Context, tx *sql.Tx, eventID int64) ([]loggedCue, error) {
	cues, err := loadSnapshotCues(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]loggedCue, len(cues))
	for i, c := range cues {
		out[i] = loggedCue{
			CueID: c.ID, Seq: c.Seq, Title: c.Title, ScheduledStart: c.ScheduledStart, DurationMin: c.DurationMin,
			IsFixed: c.IsFixed, OffsetMin: c.OffsetMin, Status: c.Status, NotePublic: c.NotePublic,
			Roles: make([]loggedAssign, 0, len(c.Roles)),
		}
		for _, r := range c.Roles {
			out[i].Roles = append(out[i].Roles, loggedAssign{RoleID: r.RoleID, RoleNote: r.RoleNote})
		}
	}
	return out, nil
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
