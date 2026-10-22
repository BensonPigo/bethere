package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

var (
	ErrInvalidCue = errors.New("cue needs a scheduled start within the supported years")
	// ErrUnknownOp means the latest cue operation was written by a newer
	// release; an older release must not undo past it (ADR-0015).
	ErrUnknownOp = errors.New("latest cue operation has an unknown format")
)

// AddCue appends a cue to an event. Status, offset and actual times start
// empty.
func (s *Store) AddCue(ctx context.Context, eventID int64, c domain.Cue, actor Actor, now time.Time) (int64, error) {
	if !domain.InRange(c.ScheduledStart) {
		return 0, ErrInvalidCue
	}
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO cues (event_id, seq, title, scheduled_start, duration_min, is_fixed)
			VALUES (?, ?, ?, ?, ?, ?)`,
			eventID, c.Seq, c.Title, toNS(c.ScheduledStart), c.DurationMin, c.IsFixed)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, "add-cue", map[string]any{"cueId": id}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
	return id, err
}

// AssignRole shows a cue to a role, with that role's note (CueRole).
func (s *Store) AssignRole(ctx context.Context, cueID, roleID int64, note string, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var eventID int64
		if err := tx.QueryRowContext(ctx, `SELECT event_id FROM cues WHERE id = ?`, cueID).Scan(&eventID); err != nil {
			return notFound(err)
		}
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		roleEventID, _, err := roleOf(ctx, tx, roleID)
		if err != nil {
			return err
		}
		if roleEventID != eventID {
			return ErrCrossEvent
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cue_roles (cue_id, role_id, role_note) VALUES (?, ?, ?)
			ON CONFLICT (cue_id, role_id) DO UPDATE SET role_note = excluded.role_note`,
			cueID, roleID, note); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, "assign-role", map[string]any{"cueId": cueID, "roleId": roleID}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// Cues returns an event's cues ordered by seq.
func (s *Store) Cues(ctx context.Context, eventID int64) ([]domain.Cue, error) {
	return loadCues(ctx, s.r, eventID)
}

func loadCues(ctx context.Context, q querier, eventID int64) ([]domain.Cue, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, seq, title, scheduled_start, duration_min, is_fixed,
		offset_min, status, actual_start, actual_end
		FROM cues WHERE event_id = ? ORDER BY seq`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cues []domain.Cue
	for rows.Next() {
		var (
			c                   domain.Cue
			start               int64
			status              string
			actualStart, actual sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &c.Seq, &c.Title, &start, &c.DurationMin, &c.IsFixed,
			&c.OffsetMin, &status, &actualStart, &actual); err != nil {
			return nil, err
		}
		c.ScheduledStart = fromNS(start)
		c.Status = domain.Status(status)
		c.ActualStart = fromNullNS(actualStart)
		c.ActualEnd = fromNullNS(actual)
		cues = append(cues, c)
	}
	return cues, rows.Err()
}

// ApplyCueOp runs a live cue operation (advance, skip, shift) in one
// transaction: check the client's version, build the Op from the current
// cues with the domain rules, write every After state, log the Op for
// undo and bump the version (data-model.md 4.2 step 6). An Op that
// changes nothing is not written.
func (s *Store) ApplyCueOp(ctx context.Context, eventID, expectVersion int64, actor Actor, now time.Time,
	build func([]domain.Cue) (domain.Op, error)) (int64, domain.Op, error) {
	version := expectVersion
	var op domain.Op
	err := s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		if err := e.RequireLive(now); err != nil {
			return err
		}
		cues, err := loadCues(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if op, err = build(cues); err != nil {
			return err
		}
		if len(op.Changes) == 0 {
			return nil
		}
		if err := writeChanges(ctx, tx, eventID, op); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, opLogPrefix+string(op.Kind), encodeOp(op), now); err != nil {
			return err
		}
		version, err = bumpVersion(ctx, tx, eventID)
		return err
	})
	if err != nil {
		return 0, domain.Op{}, err
	}
	return version, op, nil
}

// UndoLastCueOp reverses the latest cue operation, once (data-model.md
// data-model.md 4.1). Other actions in between, such as reissuing a link, do not
// count as steps.
func (s *Store) UndoLastCueOp(ctx context.Context, eventID, expectVersion int64, actor Actor, now time.Time) (int64, domain.Op, error) {
	var (
		version int64
		undo    domain.Op
	)
	err := s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		if err := e.RequireLive(now); err != nil {
			return err
		}
		var (
			logID   int64
			typ     string
			payload string
			undone  sql.NullInt64
		)
		err = tx.QueryRowContext(ctx, `SELECT id, type, payload, undone_at FROM action_logs
			WHERE event_id = ? AND type LIKE 'cue.%' ORDER BY id DESC LIMIT 1`, eventID).
			Scan(&logID, &typ, &payload, &undone)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && undone.Valid) {
			return ErrNothingToUndo
		}
		if err != nil {
			return err
		}
		op, err := decodeOp(typ, payload)
		if err != nil {
			return err
		}
		cues, err := loadCues(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if undo, err = domain.Undo(cues, op); err != nil {
			return err
		}
		if err := writeChanges(ctx, tx, eventID, undo); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE action_logs SET undone_at = ? WHERE id = ?`, toNS(now), logID); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, "undo",
			map[string]any{"of": logID, "op": encodeOp(undo)}, now); err != nil {
			return err
		}
		version, err = bumpVersion(ctx, tx, eventID)
		return err
	})
	if err != nil {
		return 0, domain.Op{}, err
	}
	return version, undo, nil
}

// writeChanges stores the fields an Op owns for every changed cue. Cues
// leaving InProgress are written first, so the one-in-progress index
// never sees two at once.
func writeChanges(ctx context.Context, tx *sql.Tx, eventID int64, op domain.Op) error {
	changes := slices.Clone(op.Changes)
	slices.SortStableFunc(changes, func(a, b domain.Change) int {
		return boolInt(a.After.Status == domain.InProgress) - boolInt(b.After.Status == domain.InProgress)
	})
	for _, ch := range changes {
		c := ch.After
		res, err := tx.ExecContext(ctx, `UPDATE cues SET status = ?, offset_min = ?, actual_start = ?, actual_end = ?
			WHERE id = ? AND event_id = ?`,
			c.Status, c.OffsetMin, nullNS(c.ActualStart), nullNS(c.ActualEnd), c.ID, eventID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("cue %d: %w", c.ID, ErrNotFound)
		}
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The action log payload of a cue operation (ADR-0015). It holds only the
// fields an Op owns, never titles or other editable text, and is
// versioned: a release that does not know the version refuses to undo it.
const (
	opLogPrefix = "cue."
	opFormatV1  = 1
)

// The kind lives only in the log type ("cue.shift").
type opRecord struct {
	V       int            `json:"v"`
	Shift   *shiftRecord   `json:"shift,omitempty"`
	Changes []changeRecord `json:"changes"`
}

type shiftRecord struct {
	FromCueID int64   `json:"fromCueId"`
	DeltaMin  int     `json:"deltaMin"`
	Excluded  []int64 `json:"excluded,omitempty"`
}

type changeRecord struct {
	CueID  int64    `json:"cueId"`
	Before cueState `json:"before"`
	After  cueState `json:"after"`
}

type cueState struct {
	Status      string `json:"status"`
	OffsetMin   int    `json:"offsetMin"`
	ActualStart int64  `json:"actualStart,omitempty"` // Unix ns; 0 = unset
	ActualEnd   int64  `json:"actualEnd,omitempty"`
}

func encodeOp(op domain.Op) opRecord {
	r := opRecord{V: opFormatV1, Changes: make([]changeRecord, len(op.Changes))}
	if op.Shift != nil {
		r.Shift = &shiftRecord{FromCueID: op.Shift.FromCueID, DeltaMin: op.Shift.DeltaMin, Excluded: op.Shift.Excluded}
	}
	for i, ch := range op.Changes {
		r.Changes[i] = changeRecord{CueID: ch.After.ID, Before: stateOf(ch.Before), After: stateOf(ch.After)}
	}
	return r
}

func stateOf(c domain.Cue) cueState {
	s := cueState{Status: string(c.Status), OffsetMin: c.OffsetMin}
	if !c.ActualStart.IsZero() {
		s.ActualStart = c.ActualStart.UnixNano()
	}
	if !c.ActualEnd.IsZero() {
		s.ActualEnd = c.ActualEnd.UnixNano()
	}
	return s
}

func (s cueState) cue(id int64) domain.Cue {
	c := domain.Cue{ID: id, Status: domain.Status(s.Status), OffsetMin: s.OffsetMin}
	if s.ActualStart != 0 {
		c.ActualStart = fromNS(s.ActualStart)
	}
	if s.ActualEnd != 0 {
		c.ActualEnd = fromNS(s.ActualEnd)
	}
	return c
}

// decodeOp reads a logged cue operation. Another kind or format version
// is refused (ADR-0015); unknown optional fields are ignored, and a bad
// status is stopped by the schema's CHECK when written back.
func decodeOp(typ, payload string) (domain.Op, error) {
	kind := domain.OpKind(strings.TrimPrefix(typ, opLogPrefix))
	if kind != domain.OpAdvance && kind != domain.OpSkip && kind != domain.OpShift {
		return domain.Op{}, ErrUnknownOp
	}
	var r opRecord
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return domain.Op{}, fmt.Errorf("%w: %v", ErrUnknownOp, err)
	}
	if r.V != opFormatV1 {
		return domain.Op{}, ErrUnknownOp
	}
	op := domain.Op{Kind: kind, Changes: make([]domain.Change, len(r.Changes))}
	if r.Shift != nil {
		op.Shift = &domain.ShiftRequest{FromCueID: r.Shift.FromCueID, DeltaMin: r.Shift.DeltaMin, Excluded: r.Shift.Excluded}
	}
	for i, ch := range r.Changes {
		op.Changes[i] = domain.Change{Before: ch.Before.cue(ch.CueID), After: ch.After.cue(ch.CueID)}
	}
	return op, nil
}
