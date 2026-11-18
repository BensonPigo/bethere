package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// The Master's edits of the rundown during the event day (US-M-8,
// ADR-0021). They run in draft and live alike, since the event stays a
// draft until the first advance (ADR-0020). None of them is a cue
// operation: their log types do not start with "cue.", so "undo the last
// step" never reverses them.

var (
	// ErrInvalidPosition means a new cue would come before a cue that has
	// already started or finished (api.md section 3).
	ErrInvalidPosition = errors.New("a cue can only be added after the cues that have started")
	// ErrInvalidAssignment means a cue would be shown to a role that has no
	// cues of its own: the Master sees all, Host none (api.md section 3).
	ErrInvalidAssignment = errors.New("only Stylist, Media and StageAV roles can be assigned cues")
)

// Assignment is one role a cue is shown to, with that role's note.
type Assignment struct {
	RoleID   int64
	RoleNote *string
}

// NewCue is a cue the Master adds: it starts at Start as typed, with no
// offset (ADR-0021), after the cue AfterCueID. AfterCueID 0 puts it first,
// in draft only (M2c, api.md section 3).
type NewCue struct {
	AfterCueID  int64
	Start       time.Time
	DurationMin int
	Title       string
	NotePublic  *string
	Roles       []Assignment
}

// InsertCue adds a cue after another one and shifts the seq of the cues
// after it by one, keeping seq unique and gapless (data-model.md 4.6).
func (s *Store) InsertCue(ctx context.Context, eventID, expectVersion int64, nc NewCue, actor Actor, now time.Time) (int64, error) {
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		cues, err := loadCues(ctx, tx, eventID)
		if err != nil {
			return err
		}
		// Every id belongs to this event before anything about it is checked.
		i, err := afterIndex(cues, nc.AfterCueID)
		if err != nil {
			return err
		}
		if err := checkAssignments(ctx, tx, eventID, nc.Roles); err != nil {
			return err
		}
		title, err := checkNewCue(e, cues, i, nc)
		if err != nil {
			return err
		}
		var added []int64
		if id, added, err = insertCueAfter(ctx, tx, eventID, seqAfter(cues, i), title, nc); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, logInsert, assignRecord{CueID: id, Added: added}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
	return id, err
}

// afterIndex is the index of the cue a new one goes after, or -1 to put it
// first.
func afterIndex(cues []domain.Cue, afterCueID int64) (int, error) {
	if afterCueID == 0 {
		return -1, nil
	}
	if i := slices.IndexFunc(cues, func(c domain.Cue) bool { return c.ID == afterCueID }); i >= 0 {
		return i, nil
	}
	return 0, domain.ErrCueNotFound
}

// seqAfter is the seq a new cue goes after: that of cues[i], or 0 first.
func seqAfter(cues []domain.Cue, i int) int {
	if i < 0 {
		return 0
	}
	return cues[i].Seq
}

// checkNewCue checks where a new cue goes and its fields, and returns its
// cleaned title. A cue goes first in draft only, and never before a cue
// that has started or finished.
func checkNewCue(e domain.Event, cues []domain.Cue, i int, nc NewCue) (string, error) {
	if i < 0 && e.Status != domain.Draft {
		return "", ErrInvalidPosition
	}
	title, ok := newCueFields(e, nc)
	if !ok {
		return "", ErrInvalidCue
	}
	for _, c := range cues[i+1:] {
		if c.Status != domain.Pending && c.Status != domain.Skipped {
			return "", ErrInvalidPosition
		}
	}
	return title, nil
}

// newCueFields is the cleaned title, and whether every field of a new cue
// is within its limits.
func newCueFields(e domain.Event, nc NewCue) (string, bool) {
	title, ok := domain.CleanTitle(nc.Title)
	return title, ok && domain.ValidNote(nc.NotePublic) && nc.DurationMin >= 0 && nc.DurationMin <= domain.MaxDurationMin &&
		e.ValidCueStart(nc.Start)
}

// insertCueAfter makes room after afterSeq and writes the cue with its
// roles. The id is returned once the cue row is written, even when a role
// fails after it.
func insertCueAfter(ctx context.Context, tx *sql.Tx, eventID int64, afterSeq int, title string, nc NewCue) (int64, []int64, error) {
	// Two steps, so the UNIQUE (event_id, seq) never sees two equal
	// seqs halfway through the update.
	if _, err := tx.ExecContext(ctx, `UPDATE cues SET seq = -(seq + 1) WHERE event_id = ? AND seq > ?`,
		eventID, afterSeq); err != nil {
		return 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cues SET seq = -seq WHERE event_id = ? AND seq < 0`, eventID); err != nil {
		return 0, nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO cues (event_id, seq, title, scheduled_start, duration_min, note_public)
		VALUES (?, ?, ?, ?, ?, ?)`, eventID, afterSeq+1, title, toNS(nc.Start), nc.DurationMin, nullNote(nc.NotePublic))
	if err != nil {
		return 0, nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return id, nil, err
	}
	added := make([]int64, 0, len(nc.Roles))
	for _, a := range nc.Roles {
		if err := upsertCueRole(ctx, tx, id, a); err != nil {
			return id, nil, err
		}
		added = append(added, a.RoleID)
	}
	return id, added, nil
}

// SetCueRoles makes roles the complete assignment list of a pending cue
// (US-M-8.3): roles not in it are removed, the others added or given the
// new note.
func (s *Store) SetCueRoles(ctx context.Context, eventID, expectVersion, cueID int64, roles []Assignment, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now); err != nil {
			return err
		}
		c, err := cueIn(ctx, tx, eventID, cueID)
		if err != nil {
			return err
		}
		if err := checkAssignments(ctx, tx, eventID, roles); err != nil {
			return err
		}
		if c.Status != domain.Pending {
			return domain.ErrNotPending
		}
		current, err := cueRoleIDs(ctx, tx, cueID)
		if err != nil {
			return err
		}
		rec, err := replaceCueRoles(ctx, tx, cueID, roles, current)
		if err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, logAssign, rec, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// cueRoleIDs is the set of roles a cue is assigned to.
func cueRoleIDs(ctx context.Context, tx *sql.Tx, cueID int64) (map[int64]bool, error) {
	current := map[int64]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT role_id FROM cue_roles WHERE cue_id = ?`, cueID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		current[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return current, nil
}

// replaceCueRoles writes roles over the current assignments of a cue and
// records which were added and removed.
func replaceCueRoles(ctx context.Context, tx *sql.Tx, cueID int64, roles []Assignment, current map[int64]bool) (assignRecord, error) {
	rec := assignRecord{CueID: cueID, Added: []int64{}, Removed: []int64{}}
	keep := map[int64]bool{}
	for _, a := range roles {
		keep[a.RoleID] = true
		if !current[a.RoleID] {
			rec.Added = append(rec.Added, a.RoleID)
		}
		if err := upsertCueRole(ctx, tx, cueID, a); err != nil {
			return rec, err
		}
	}
	for id := range current {
		if keep[id] {
			continue
		}
		if err := unassign(ctx, tx, cueID, id); err != nil {
			return rec, err
		}
		rec.Removed = append(rec.Removed, id)
	}
	slices.Sort(rec.Removed)
	return rec, nil
}

// unassign removes a role from a cue; its ack goes with the assignment
// (api.md 9.3).
func unassign(ctx context.Context, tx *sql.Tx, cueID, roleID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM cue_roles WHERE cue_id = ? AND role_id = ?`, cueID, roleID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM cue_acks WHERE cue_id = ? AND role_id = ?`, cueID, roleID)
	return err
}

// EditCue changes a cue's title and public note, in any status; never its
// times, length or fixed flag (access-control.md section 4).
func (s *Store) EditCue(ctx context.Context, eventID, expectVersion, cueID int64, title string, notePublic *string,
	actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now); err != nil {
			return err
		}
		if _, err := cueIn(ctx, tx, eventID, cueID); err != nil {
			return err
		}
		clean, ok := domain.CleanTitle(title)
		if !ok || !domain.ValidNote(notePublic) {
			return ErrInvalidCue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cues SET title = ?, note_public = ? WHERE id = ? AND event_id = ?`,
			clean, nullNote(notePublic), cueID, eventID); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, "rundown.edit", map[string]any{"cueId": cueID}, now); err != nil {
			return err
		}
		_, err := bumpVersion(ctx, tx, eventID)
		return err
	})
}

// The log of an insert or an assignment change: which roles were newly
// shown the cue, read back for the change notice (api.md 5.3).
const (
	logInsert = "rundown.insert"
	logAssign = "rundown.assign"
)

type assignRecord struct {
	CueID   int64   `json:"cueId"`
	Added   []int64 `json:"added"`
	Removed []int64 `json:"removed,omitempty"`
}

// cueIn reads a cue of eventID; another event's cue is the same
// domain.ErrCueNotFound as one that does not exist (api.md section 6).
func cueIn(ctx context.Context, tx *sql.Tx, eventID, cueID int64) (domain.Cue, error) {
	cues, err := loadCues(ctx, tx, eventID)
	if err != nil {
		return domain.Cue{}, err
	}
	for _, c := range cues {
		if c.ID == cueID {
			return c, nil
		}
	}
	return domain.Cue{}, domain.ErrCueNotFound
}

// checkAssignments checks every role id is in eventID first, then that
// each can be assigned cues, so another event's Master or Host is a 404
// and never an invalid assignment (api.md section 3, ownership first).
func checkAssignments(ctx context.Context, tx *sql.Tx, eventID int64, roles []Assignment) error {
	types := make([]domain.RoleType, len(roles))
	for i, a := range roles {
		typ, err := roleIn(ctx, tx, eventID, a.RoleID)
		if err != nil {
			return err
		}
		types[i] = typ
	}
	for i, a := range roles {
		if !types[i].HasOwnCues() {
			return ErrInvalidAssignment
		}
		if !domain.ValidNote(a.RoleNote) {
			return ErrInvalidCue
		}
	}
	return nil
}

func upsertCueRole(ctx context.Context, tx *sql.Tx, cueID int64, a Assignment) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cue_roles (cue_id, role_id, role_note) VALUES (?, ?, ?)
		ON CONFLICT (cue_id, role_id) DO UPDATE SET role_note = excluded.role_note`, cueID, a.RoleID, nullNote(a.RoleNote))
	return err
}

func nullNote(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
