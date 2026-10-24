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
		i := -1 // first
		if nc.AfterCueID != 0 {
			if i = slices.IndexFunc(cues, func(c domain.Cue) bool { return c.ID == nc.AfterCueID }); i < 0 {
				return domain.ErrCueNotFound
			}
		}
		if err := checkAssignments(ctx, tx, eventID, nc.Roles); err != nil {
			return err
		}
		if i < 0 && e.Status != domain.Draft {
			return ErrInvalidPosition
		}
		title, ok := domain.CleanTitle(nc.Title)
		if !ok || !domain.ValidNote(nc.NotePublic) || nc.DurationMin < 0 || nc.DurationMin > domain.MaxDurationMin ||
			!e.ValidCueStart(nc.Start) {
			return ErrInvalidCue
		}
		afterSeq := 0
		if i >= 0 {
			afterSeq = cues[i].Seq
		}
		for _, c := range cues[i+1:] {
			if c.Status != domain.Pending && c.Status != domain.Skipped {
				return ErrInvalidPosition
			}
		}
		// Two steps, so the UNIQUE (event_id, seq) never sees two equal
		// seqs halfway through the update.
		if _, err := tx.ExecContext(ctx, `UPDATE cues SET seq = -(seq + 1) WHERE event_id = ? AND seq > ?`,
			eventID, afterSeq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cues SET seq = -seq WHERE event_id = ? AND seq < 0`, eventID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO cues (event_id, seq, title, scheduled_start, duration_min, note_public)
			VALUES (?, ?, ?, ?, ?, ?)`, eventID, afterSeq+1, title, toNS(nc.Start), nc.DurationMin, nullNote(nc.NotePublic))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		added := make([]int64, 0, len(nc.Roles))
		for _, a := range nc.Roles {
			if err := upsertCueRole(ctx, tx, id, a); err != nil {
				return err
			}
			added = append(added, a.RoleID)
		}
		if err := logAction(ctx, tx, eventID, actor, logInsert, assignRecord{CueID: id, Added: added}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
	return id, err
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
		current := map[int64]bool{}
		rows, err := tx.QueryContext(ctx, `SELECT role_id FROM cue_roles WHERE cue_id = ?`, cueID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			current[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rec := assignRecord{CueID: cueID, Added: []int64{}, Removed: []int64{}}
		keep := map[int64]bool{}
		for _, a := range roles {
			keep[a.RoleID] = true
			if !current[a.RoleID] {
				rec.Added = append(rec.Added, a.RoleID)
			}
			if err := upsertCueRole(ctx, tx, cueID, a); err != nil {
				return err
			}
		}
		for id := range current {
			if keep[id] {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM cue_roles WHERE cue_id = ? AND role_id = ?`, cueID, id); err != nil {
				return err
			}
			rec.Removed = append(rec.Removed, id)
		}
		slices.Sort(rec.Removed)
		if err := logAction(ctx, tx, eventID, actor, logAssign, rec, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
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
