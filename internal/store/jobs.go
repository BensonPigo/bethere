package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// The four jobs (api.md section 9, ADR-0035): a venue lead's acks, the
// task pool, the guest list and the cashier's checklist. None of them is a
// cue or changes the event version (ADR-0023); each is logged, never as a
// cue operation. Which role type may call which is the API's check
// (access-control.md section 5, rule 6), except where a rule depends on
// who acts (a runner's own claim). Every id is looked up in the caller's
// event before any other check, so another event's id is a plain
// ErrItemNotFound (api.md 9.8).

var (
	// ErrNotAssigned is an ack of a cue not assigned to the caller.
	ErrNotAssigned = errors.New("the cue is not assigned to this role")
	// ErrTaskTaken is a claim, release or finish of a task another runner holds.
	ErrTaskTaken = errors.New("another role has claimed this task")
	// ErrInvalidTaskState is a task change the rules do not allow.
	ErrInvalidTaskState = errors.New("this task change is not allowed")
	// ErrTooManyTasks refuses a task past MaxTasks.
	ErrTooManyTasks = errors.New("the event has the most tasks allowed")
	// ErrInvalidTask is a task title that is empty or too long.
	ErrInvalidTask = errors.New("invalid task title")
	// ErrGuestsArrived refuses replacing a guest list someone has arrived on.
	ErrGuestsArrived = errors.New("a guest has already arrived")
	// ErrChecklistStarted refuses replacing a checklist with a done item.
	ErrChecklistStarted = errors.New("a checklist item is already done")
	// ErrInvalidChecklist is a checklist that cannot be written as is.
	ErrInvalidChecklist = errors.New("invalid checklist")
	// ErrItemNotFound is a task, guest or checklist item id that is not in
	// the caller's event, another event's included (a 404, api.md 9.8).
	ErrItemNotFound = errors.New("task, guest or checklist item not found in this event")
)

// Limits of api.md section 9.
const (
	MaxTasks          = 200
	MaxGuests         = 1000
	MaxChecklistItems = 30
	RecentDoneTasks   = 20
)

// Task states (data-model.md Task).
const (
	TaskOpen    = "open"
	TaskClaimed = "claimed"
	TaskDone    = "done"
)

// SetAck sets whether roleID has acknowledged cueID (api.md 9.3). Setting
// the state it already has writes nothing; the first ack's time is kept.
func (s *Store) SetAck(ctx context.Context, eventID, cueID, roleID int64, acked bool, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		if _, err := cueIn(ctx, tx, eventID, cueID); err != nil {
			return err
		}
		var assigned, current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cue_roles WHERE cue_id = ? AND role_id = ?),
			EXISTS (SELECT 1 FROM cue_acks WHERE cue_id = ? AND role_id = ?)`, cueID, roleID, cueID, roleID).
			Scan(&assigned, &current); err != nil {
			return err
		}
		if !assigned {
			return ErrNotAssigned
		}
		if current == acked {
			return nil
		}
		var err error
		if acked {
			_, err = tx.ExecContext(ctx, `INSERT INTO cue_acks (cue_id, role_id, at) VALUES (?, ?, ?)`, cueID, roleID, toNS(now))
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM cue_acks WHERE cue_id = ? AND role_id = ?`, cueID, roleID)
		}
		if err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, "ack.set", map[string]any{"cueId": cueID, "roleId": roleID, "acked": acked}, now)
	})
}

// AddTask adds an open task created by the actor's role (api.md 9.4).
func (s *Store) AddTask(ctx context.Context, eventID int64, title string, actor Actor, now time.Time) (int64, error) {
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		title, ok := domain.CleanTitle(title)
		if !ok {
			return ErrInvalidTask
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE event_id = ?`, eventID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxTasks {
			return ErrTooManyTasks
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO tasks (event_id, title, created_by_role_id, created_at, state, updated_at)
			VALUES (?, ?, ?, ?, 'open', ?)`, eventID, title, actor.roleID, toNS(now), toNS(now))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, "task.add", map[string]any{"taskId": id, "title": title}, now)
	})
	return id, err
}

type taskRow struct {
	state     string
	claimedBy sql.NullInt64
}

func taskIn(ctx context.Context, tx *sql.Tx, eventID, taskID int64) (taskRow, error) {
	var t taskRow
	err := tx.QueryRowContext(ctx, `SELECT state, claimed_by_role_id FROM tasks WHERE id = ? AND event_id = ?`,
		taskID, eventID).Scan(&t.state, &t.claimedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrItemNotFound
	}
	return t, err
}

// SetTaskState moves a task (api.md 9.4). asMaster is decided by the
// caller from the role type: the Master may set any state but claimed; a
// runner may claim an open task, and release or finish its own.
func (s *Store) SetTaskState(ctx context.Context, eventID, taskID int64, state string, asMaster bool, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		t, err := taskIn(ctx, tx, eventID, taskID)
		if err != nil {
			return err
		}
		claimer := t.claimedBy
		switch {
		case state != TaskOpen && state != TaskClaimed && state != TaskDone:
			return ErrInvalidTaskState
		case asMaster && state == TaskClaimed:
			return ErrInvalidTaskState
		case asMaster && state == TaskOpen:
			claimer = sql.NullInt64{}
		case asMaster:
		case t.state == TaskOpen && state == TaskClaimed:
			claimer = sql.NullInt64{Int64: actor.roleID, Valid: true}
		case t.state == TaskClaimed && t.claimedBy.Int64 != actor.roleID:
			return ErrTaskTaken
		case t.state == TaskClaimed && state == TaskOpen:
			claimer = sql.NullInt64{}
		case t.state == TaskClaimed && state == TaskDone:
		case t.state == state:
			return nil
		default:
			return ErrInvalidTaskState
		}
		if t.state == state && claimer == t.claimedBy {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET state = ?, claimed_by_role_id = ?, updated_at = ? WHERE id = ?`,
			state, claimer, toNS(now), taskID); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, "task.set", map[string]any{"taskId": taskID, "state": state}, now)
	})
}

// DeleteTask removes a task, logging it in full (ADR-0032).
func (s *Store) DeleteTask(ctx context.Context, eventID, taskID int64, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		var (
			title, state       string
			createdBy, claimed sql.NullInt64
			createdAt          int64
		)
		err := tx.QueryRowContext(ctx, `SELECT title, state, created_by_role_id, claimed_by_role_id, created_at
			FROM tasks WHERE id = ? AND event_id = ?`, taskID, eventID).Scan(&title, &state, &createdBy, &claimed, &createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, taskID); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, "task.delete", map[string]any{
			"taskId": taskID, "title": title, "state": state, "createdByRoleId": nullID(createdBy),
			"claimedByRoleId": nullID(claimed), "createdAt": createdAt,
		}, now)
	})
}

func nullID(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

// GuestRow is one guest of a pasted list (api.md 9.5).
type GuestRow struct {
	Name  string
	Table string
	Note  *string
}

// ReplaceGuests replaces the guest list. rows is nil when the text could
// not be read at all, so the event-day and arrival checks come first.
func (s *Store) ReplaceGuests(ctx context.Context, eventID int64, rows []GuestRow, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, _, err := guard(ctx, tx, eventID, actor, now)
		if err != nil {
			return err
		}
		if e.DayStarted(now) {
			return ErrLockedOnEventDay
		}
		var arrived bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM guests WHERE event_id = ? AND arrived_at IS NOT NULL)`,
			eventID).Scan(&arrived); err != nil {
			return err
		}
		if arrived {
			return ErrGuestsArrived
		}
		if rows == nil || len(rows) > MaxGuests {
			return ErrInvalidImport
		}
		old, err := loadGuests(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM guests WHERE event_id = ?`, eventID); err != nil {
			return err
		}
		for i, g := range rows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO guests (event_id, seq, name, table_label, note) VALUES (?, ?, ?, ?, ?)`,
				eventID, i+1, g.Name, g.Table, nullNote(g.Note)); err != nil {
				return err
			}
		}
		return logAction(ctx, tx, eventID, actor, "guests.import", map[string]any{"count": len(rows), "replaced": old}, now)
	})
}

// SetArrived sets whether a guest has arrived; the same state writes nothing.
func (s *Store) SetArrived(ctx context.Context, eventID, guestID int64, arrived bool, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		var at sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT arrived_at FROM guests WHERE id = ? AND event_id = ?`, guestID, eventID).Scan(&at)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if at.Valid == arrived {
			return nil
		}
		var v any
		if arrived {
			v = toNS(now)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE guests SET arrived_at = ? WHERE id = ?`, v, guestID); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, "guest.arrive", map[string]any{"guestId": guestID, "arrived": arrived}, now)
	})
}

// ReplaceChecklist replaces the checklist with titles (api.md 9.6).
func (s *Store) ReplaceChecklist(ctx context.Context, eventID int64, titles []string, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		e, _, err := guard(ctx, tx, eventID, actor, now)
		if err != nil {
			return err
		}
		if e.DayStarted(now) {
			return ErrLockedOnEventDay
		}
		var started bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM checklist_items WHERE event_id = ? AND done_at IS NOT NULL)`,
			eventID).Scan(&started); err != nil {
			return err
		}
		if started {
			return ErrChecklistStarted
		}
		if titles == nil || len(titles) > MaxChecklistItems {
			return ErrInvalidChecklist
		}
		clean := make([]string, len(titles))
		for i, t := range titles {
			c, ok := domain.CleanTitle(t)
			if !ok {
				return ErrInvalidChecklist
			}
			clean[i] = c
		}
		old, err := loadChecklist(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM checklist_items WHERE event_id = ?`, eventID); err != nil {
			return err
		}
		for i, t := range clean {
			if _, err := tx.ExecContext(ctx, `INSERT INTO checklist_items (event_id, seq, title) VALUES (?, ?, ?)`,
				eventID, i+1, t); err != nil {
				return err
			}
		}
		return logAction(ctx, tx, eventID, actor, "checklist.replace", map[string]any{"count": len(clean), "replaced": old}, now)
	})
}

// CheckItem sets whether a checklist item is done, and by the actor's role.
func (s *Store) CheckItem(ctx context.Context, eventID, itemID int64, done bool, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		var at sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT done_at FROM checklist_items WHERE id = ? AND event_id = ?`, itemID, eventID).Scan(&at)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if at.Valid == done {
			return nil
		}
		var v, by any
		if done {
			v, by = toNS(now), actor.roleID
		}
		if _, err := tx.ExecContext(ctx, `UPDATE checklist_items SET done_at = ?, done_by_role_id = ? WHERE id = ?`,
			v, by, itemID); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, "checklist.check", map[string]any{"itemId": itemID, "done": done}, now)
	})
}

// Task, Guest and ChecklistItem are what snapshots are built from; role
// ids, not names, so the API decides what each caller sees.
type Task struct {
	ID        int64
	Title     string
	CreatedBy int64 // 0 when the role was deleted
	CreatedAt time.Time
	ClaimedBy int64 // 0 when open or the role was deleted
	State     string
	UpdatedAt time.Time
}

type Guest struct {
	ID      int64
	Name    string
	Table   string
	Note    *string
	Arrived bool
}

type ChecklistItem struct {
	ID     int64
	Title  string
	Done   bool
	DoneBy int64 // 0 when not done or the role was deleted
}

func loadTasks(ctx context.Context, q querier, eventID int64) ([]Task, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, title, created_by_role_id, created_at, claimed_by_role_id, state, updated_at
		FROM tasks WHERE event_id = ? ORDER BY created_at DESC, id DESC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var (
			t                  Task
			created, updated   int64
			createdBy, claimed sql.NullInt64
		)
		if err := rows.Scan(&t.ID, &t.Title, &createdBy, &created, &claimed, &t.State, &updated); err != nil {
			return nil, err
		}
		t.CreatedBy, t.ClaimedBy = createdBy.Int64, claimed.Int64
		t.CreatedAt, t.UpdatedAt = time.Unix(0, created).UTC(), time.Unix(0, updated).UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

func loadGuests(ctx context.Context, q querier, eventID int64) ([]Guest, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, table_label, note, arrived_at IS NOT NULL
		FROM guests WHERE event_id = ? ORDER BY seq, id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Guest{}
	for rows.Next() {
		var (
			g    Guest
			note sql.NullString
		)
		if err := rows.Scan(&g.ID, &g.Name, &g.Table, &note, &g.Arrived); err != nil {
			return nil, err
		}
		if note.Valid {
			g.Note = &note.String
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func loadChecklist(ctx context.Context, q querier, eventID int64) ([]ChecklistItem, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, title, done_at IS NOT NULL, done_by_role_id
		FROM checklist_items WHERE event_id = ? ORDER BY seq, id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChecklistItem{}
	for rows.Next() {
		var (
			it ChecklistItem
			by sql.NullInt64
		)
		if err := rows.Scan(&it.ID, &it.Title, &it.Done, &by); err != nil {
			return nil, err
		}
		it.DoneBy = by.Int64
		out = append(out, it)
	}
	return out, rows.Err()
}

// loadAcks returns, per cue, when each role acknowledged it; only for
// roles still assigned to the cue.
func loadAcks(ctx context.Context, q querier, eventID int64) (map[int64]map[int64]time.Time, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.cue_id, a.role_id, a.at FROM cue_acks a
		JOIN cues c ON c.id = a.cue_id
		JOIN cue_roles cr ON cr.cue_id = a.cue_id AND cr.role_id = a.role_id
		WHERE c.event_id = ?`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[int64]time.Time{}
	for rows.Next() {
		var cue, role, at int64
		if err := rows.Scan(&cue, &role, &at); err != nil {
			return nil, err
		}
		if out[cue] == nil {
			out[cue] = map[int64]time.Time{}
		}
		out[cue][role] = time.Unix(0, at).UTC()
	}
	return out, rows.Err()
}
