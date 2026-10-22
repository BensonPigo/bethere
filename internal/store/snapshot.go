package store

import (
	"context"
	"database/sql"
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
}

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
	ID   int64
	Name string
	Type domain.RoleType
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
	owner, e, v, err := resolveToken(ctx, tx, tokenHash, now)
	if err != nil {
		return SnapshotData{}, err
	}
	if afterTokenResolved != nil {
		afterTokenResolved()
	}
	d := SnapshotData{Owner: owner, Event: e, Version: v}
	if d.Cues, err = loadSnapshotCues(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	if d.Roles, err = loadRoles(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	if d.LastOp, err = lastUndoableOp(ctx, tx, e.ID); err != nil {
		return SnapshotData{}, err
	}
	return d, nil
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
	rows, err := q.QueryContext(ctx, `SELECT id, name, type FROM roles WHERE event_id = ? ORDER BY display_order, id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []RoleInfo
	for rows.Next() {
		var r RoleInfo
		if err := rows.Scan(&r.ID, &r.Name, &r.Type); err != nil {
			return nil, err
		}
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
