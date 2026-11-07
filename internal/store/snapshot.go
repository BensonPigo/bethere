package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// SnapshotData is everything a caller's snapshot is built from, read in
// the same transaction that resolved the caller's token (api.md section 7,
// section 7 items 2–4). The store does not decide what each role type may
// see (access-control.md section 5, rule 6): it returns the whole event
// and the caller filters by Owner.RoleType.
type SnapshotData struct {
	Owner   TokenOwner
	Event   domain.Event
	Version int64
	Cues    []SnapshotCue // ordered by seq
	Roles   []RoleInfo    // every role of the event, by display order then id
	// LastOp is the latest cue operation if it has not been undone yet and
	// this release can read it (ADR-0015); nil otherwise. It is what
	// UndoLastCueOp would reverse.
	LastOp *domain.Op
	// Shifts are the latest shifts and undone shifts, newest first, for
	// the change notices of each role (screen-design.md 4.4).
	Shifts []ShiftRecord
	// Assigns are the latest inserts and assignment changes that showed a
	// cue to new roles, newest first, for the "new cue" notice (ADR-0021).
	Assigns []AssignRecord
	// The four jobs (api.md section 9): acks by cue then role, tasks newest
	// first, guests and checklist items in order. Guests, tasks and the
	// checklist are read only for the owner types whose snapshot can hold
	// them, to keep the poll small; the API still decides what is shown.
	Acks      map[int64]map[int64]time.Time
	Tasks     []Task
	Guests    []Guest
	Checklist []ChecklistItem
}

// AssignRecord is one logged insert or assignment change: when, which cue,
// and which roles it newly showed the cue to. Removals are not kept: a role
// taken off a cue gets no notice (ADR-0021).
type AssignRecord struct {
	At    time.Time
	CueID int64
	Added []int64
}

// ShiftRecord is one logged shift, or the undo of one: when, and every cue
// it moved with its offset before and after. Never the log's own id.
type ShiftRecord struct {
	At      time.Time
	Changes []domain.Change
}

// recentShifts bounds how far back the change notice looks.
const recentShifts = 50

// SnapshotCue is a cue with its public note and its role assignments.
type SnapshotCue struct {
	domain.Cue
	NotePublic *string   // nil when the cue has none
	Roles      []CueRole // by role display order then id
}

// CueRole is one assignment of a cue to a role (CueRole), with the role's
// name and that role's own note.
type CueRole struct {
	RoleID   int64
	RoleName string
	RoleNote *string // nil when none
}

// RoleInfo is a role of the event; never its token or token hash.
type RoleInfo struct {
	ID       int64
	Name     string
	Type     domain.RoleType
	View     domain.View // resolved like TokenOwner.View
	ChecksIn bool        // resolved like TokenOwner.ChecksIn (M2c)
	// ActiveTokenID is the id of the role's active link, 0 when it has
	// none: the key of its lastSeenAt (api.md section 7 item 5).
	ActiveTokenID int64
	CheckedIn     bool // an active, not undone check-in (data-model.md CheckIn)
}

// afterTokenResolved, when set by a test, runs inside Snapshot's read
// transaction between resolving the token and reading the event's data.
var afterTokenResolved func()

// Snapshot resolves a token exactly like LookupToken (ErrInvalidToken,
// then ErrEventEnded) and, in the same read transaction, reads what the
// owner's snapshot is built from. A token revoked or an event ended or
// purged after the transaction began therefore cannot pair a valid lookup
// with data read later (api.md section 7 item 2).
func (s *Store) Snapshot(ctx context.Context, tokenHash []byte, now time.Time) (SnapshotData, error) {
	tx, err := s.r.BeginTx(ctx, nil)
	if err != nil {
		return SnapshotData{}, err
	}
	defer tx.Rollback()
	owner, e, v, err := resolveToken(ctx, tx, tokenHash, now, s.consoles())
	if err != nil {
		return SnapshotData{}, err
	}
	if afterTokenResolved != nil {
		afterTokenResolved()
	}
	return loadSnapshotData(ctx, tx, owner, e, v)
}

// loadSnapshotData reads everything of the event a snapshot is built
// from, for owner, inside the caller's read transaction.
func loadSnapshotData(ctx context.Context, tx *sql.Tx, owner TokenOwner, e domain.Event, v int64) (SnapshotData, error) {
	d := SnapshotData{Owner: owner, Event: e, Version: v}
	var err error
	if d.Cues, err = loadSnapshotCues(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	if d.Roles, err = loadRoles(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	if d.LastOp, err = lastUndoableOp(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	if d.Shifts, err = loadShifts(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	if d.Assigns, err = loadAssigns(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	if d.Acks, err = loadAcks(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	t := owner.RoleType
	if t == domain.Master || t == domain.Runner || t == domain.Reception {
		if d.Tasks, err = loadTasks(ctx, tx, e.ID); err != nil {
			return SnapshotData{}, err
		}
	}
	if t == domain.Master || t == domain.Reception {
		if d.Guests, err = loadGuests(ctx, tx, e.ID); err != nil {
			return SnapshotData{}, err
		}
	}
	if t == domain.Master || t == domain.Cashier {
		if d.Checklist, err = loadChecklist(ctx, tx, e.ID); err != nil {
			return SnapshotData{}, err
		}
	}
	return d, nil
}

// PreviewSnapshot reads, in one read transaction, the caller's owner (the
// token resolved exactly as Snapshot does) and the data of roleID's
// snapshot as that role would get it (US-M-14). The role is looked up in
// the caller's event only. Its owner carries no token and no credential:
// TokenID is 0 and Actor is the zero value, so nothing can act or be
// marked seen as it (api.md section 3「角色預覽」).
func (s *Store) PreviewSnapshot(ctx context.Context, tokenHash []byte, roleID int64, now time.Time) (TokenOwner, SnapshotData, error) {
	tx, err := s.r.BeginTx(ctx, nil)
	if err != nil {
		return TokenOwner{}, SnapshotData{}, err
	}
	defer tx.Rollback()
	caller, e, v, err := resolveToken(ctx, tx, tokenHash, now, s.consoles())
	if err != nil {
		return TokenOwner{}, SnapshotData{}, err
	}
	target := TokenOwner{RoleID: roleID, EventID: e.ID}
	var (
		view      sql.NullString
		noCheckIn bool
	)
	err = tx.QueryRowContext(ctx, `SELECT type, name, view, checks_in IS NOT NULL FROM roles WHERE id = ? AND event_id = ?`,
		roleID, e.ID).Scan(&target.RoleType, &target.RoleName, &view, &noCheckIn)
	if errors.Is(err, sql.ErrNoRows) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return caller, SnapshotData{}, ctxErr
		}
		return caller, SnapshotData{}, ErrRoleNotFound
	}
	if err != nil {
		return caller, SnapshotData{}, err
	}
	target.View, target.ChecksIn = resolveView(target.RoleType, view), target.RoleType.ChecksIn(noCheckIn)
	d, err := loadSnapshotData(ctx, tx, target, e, v)
	return caller, d, err
}

func loadSnapshotCues(ctx context.Context, tx *sql.Tx, eventID int64) ([]SnapshotCue, error) {
	cues, err := loadCues(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotCue, len(cues))
	index := make(map[int64]int, len(cues))
	for i, c := range cues {
		out[i].Cue = c
		index[c.ID] = i
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, note_public FROM cues WHERE event_id = ?`, eventID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			id   int64
			note sql.NullString
		)
		if err := rows.Scan(&id, &note); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[id]; ok && note.Valid {
			out[i].NotePublic = &note.String
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.QueryContext(ctx, `SELECT cr.cue_id, r.id, r.name, cr.role_note
		FROM cue_roles cr JOIN cues c ON c.id = cr.cue_id JOIN roles r ON r.id = cr.role_id
		WHERE c.event_id = ? AND r.event_id = ?
		ORDER BY r.display_order, r.id`, eventID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cueID int64
			cr    CueRole
			note  sql.NullString
		)
		if err := rows.Scan(&cueID, &cr.RoleID, &cr.RoleName, &note); err != nil {
			return nil, err
		}
		if note.Valid {
			cr.RoleNote = &note.String
		}
		if i, ok := index[cueID]; ok {
			out[i].Roles = append(out[i].Roles, cr)
		}
	}
	return out, rows.Err()
}

func loadRoles(ctx context.Context, q querier, eventID int64) ([]RoleInfo, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.id, r.name, r.type, r.view, r.checks_in IS NOT NULL,
			COALESCE((SELECT t.id FROM access_tokens t WHERE t.role_id = r.id AND t.revoked_at IS NULL), 0),
			EXISTS (SELECT 1 FROM check_ins c WHERE c.role_id = r.id AND c.undone_at IS NULL)
		FROM roles r WHERE r.event_id = ? ORDER BY r.display_order, r.id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []RoleInfo
	for rows.Next() {
		var (
			r         RoleInfo
			view      sql.NullString
			noCheckIn bool
		)
		if err := rows.Scan(&r.ID, &r.Name, &r.Type, &view, &noCheckIn, &r.ActiveTokenID, &r.CheckedIn); err != nil {
			return nil, err
		}
		r.View, r.ChecksIn = resolveView(r.Type, view), r.Type.ChecksIn(noCheckIn)
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// lastUndoableOp finds the operation UndoLastCueOp would reverse, by the
// same rule: the latest cue operation, unless it was already undone. One
// this release cannot read is reported as nothing to undo here; undoing it
// fails with ErrUnknownOp.
func lastUndoableOp(ctx context.Context, q querier, eventID int64) (*domain.Op, error) {
	var (
		typ, payload string
		undone       sql.NullInt64
	)
	err := q.QueryRowContext(ctx, `SELECT type, payload, undone_at FROM action_logs
		WHERE event_id = ? AND type LIKE 'cue.%' ORDER BY id DESC LIMIT 1`, eventID).
		Scan(&typ, &payload, &undone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if undone.Valid {
		return nil, nil
	}
	op, err := decodeOp(typ, payload)
	if errors.Is(err, ErrUnknownOp) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// loadShifts reads the latest shifts and undos of shifts. A row this
// release cannot read is skipped, never an error: a release rolled back
// past a newer log format must still serve every snapshot (ADR-0015).
func loadShifts(ctx context.Context, q querier, eventID int64) ([]ShiftRecord, error) {
	rows, err := q.QueryContext(ctx, `SELECT at, type, payload FROM action_logs
		WHERE event_id = ? AND (type = ? OR (type = 'undo' AND payload LIKE '%"shift":%'))
		ORDER BY id DESC LIMIT ?`,
		eventID, opLogPrefix+string(domain.OpShift), recentShifts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShiftRecord
	for rows.Next() {
		var (
			at           int64
			typ, payload string
		)
		if err := rows.Scan(&at, &typ, &payload); err != nil {
			return nil, err
		}
		if typ == "undo" {
			// An undo row is {"of": <log id>, "op": <the reversing op>}.
			var u struct {
				Op json.RawMessage `json:"op"`
			}
			if json.Unmarshal([]byte(payload), &u) != nil {
				continue
			}
			payload = string(u.Op)
		}
		op, err := decodeOp(opLogPrefix+string(domain.OpShift), payload)
		if err != nil || op.Shift == nil {
			continue // unreadable, or the undo of an advance or skip
		}
		out = append(out, ShiftRecord{At: fromNS(at), Changes: op.Changes})
	}
	return out, rows.Err()
}

// loadAssigns reads the latest inserts and assignment changes that added
// a role. Like loadShifts, a row this release cannot read is skipped.
func loadAssigns(ctx context.Context, q querier, eventID int64) ([]AssignRecord, error) {
	rows, err := q.QueryContext(ctx, `SELECT at, payload FROM action_logs
		WHERE event_id = ? AND type IN (?, ?) ORDER BY id DESC LIMIT ?`,
		eventID, logInsert, logAssign, recentShifts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssignRecord
	for rows.Next() {
		var (
			at      int64
			payload string
		)
		if err := rows.Scan(&at, &payload); err != nil {
			return nil, err
		}
		var r assignRecord
		if json.Unmarshal([]byte(payload), &r) != nil || len(r.Added) == 0 {
			continue
		}
		out = append(out, AssignRecord{At: fromNS(at), CueID: r.CueID, Added: r.Added})
	}
	return out, rows.Err()
}
