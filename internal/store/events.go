package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

const dateLayout = "2006-01-02"

var (
	ErrCrossEvent = errors.New("actor, cue or role belongs to another event")
	ErrNoActor    = errors.New("this actor may not make this change")
	ErrTimeZone   = errors.New("event time zone is not supported")
	ErrNotEnded   = errors.New("event has not ended")
)

// Actor is who makes a change. It comes only from proving a credential:
// LookupToken (a role's link) or LookupRecoveryCode (an event's recovery
// code). It carries that credential's hash, and every write checks again
// that the credential is still valid and belongs to the event written, so
// a revoked link, a reset code or a stale id (after a purge) cannot act.
// The server actor exists only to log event creation.
type Actor struct {
	kind    actorKind
	roleID  int64   // actorRole
	tokenID int64   // actorRole: access_tokens.id of the link, logged by link changes (api.md section 3)
	eventID int64   // event the credential was proved for
	cred    *[]byte // SHA-256 of the token or recovery code; a pointer so no fmt verb prints it
}

type actorKind uint8

const (
	actorNone actorKind = iota
	actorRole
	actorRecoveryCode
	actorServer
)

// GoString keeps %#v from printing the credential hash (tech-stack.md 2.1「日誌」).
func (a Actor) GoString() string { return "store.Actor(" + a.String() + ")" }

// String is how the actor appears in the action log.
func (a Actor) String() string {
	switch a.kind {
	case actorRole:
		return fmt.Sprintf("role:%d", a.roleID)
	case actorRecoveryCode:
		return "recovery-code"
	case actorServer:
		return "server"
	}
	return ""
}

type NewEvent struct {
	Name             string
	Date             domain.Date
	TimeZone         string // IANA name, fixed for the event's life
	MasterName       string
	MasterTokenHash  []byte
	RecoveryCodeHash []byte
}

// CreateEvent creates a draft event with its single Master role and that
// role's token (access-control.md section 2).
func (s *Store) CreateEvent(ctx context.Context, ne NewEvent, now time.Time) (int64, error) {
	if !ne.Date.Valid() {
		return 0, domain.ErrInvalidDate
	}
	if !domain.SupportedTimeZone(ne.TimeZone) {
		return 0, ErrTimeZone
	}
	var eventID int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO events
			(name, event_date, timezone, status, recovery_code_hash, created_at)
			VALUES (?, ?, ?, 'Draft', ?, ?)`,
			ne.Name, ne.Date.String(),
			ne.TimeZone, ne.RecoveryCodeHash, toNS(now))
		if err != nil {
			return err
		}
		if eventID, err = res.LastInsertId(); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `INSERT INTO roles (event_id, name, type) VALUES (?, ?, ?)`,
			eventID, ne.MasterName, domain.Master)
		if err != nil {
			return err
		}
		roleID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO access_tokens (role_id, token_hash, created_at) VALUES (?, ?, ?)`,
			roleID, ne.MasterTokenHash, toNS(now)); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, Actor{kind: actorServer}, "create-event", nil, now)
	})
	return eventID, err
}

// Event returns an event and its current version.
func (s *Store) Event(ctx context.Context, id int64) (domain.Event, int64, error) {
	return loadEvent(ctx, s.r, id)
}

func loadEvent(ctx context.Context, q querier, id int64) (domain.Event, int64, error) {
	var (
		e        domain.Event
		date, tz string
		status   string
		started  sql.NullInt64
		version  int64
	)
	err := q.QueryRowContext(ctx, `SELECT id, name, event_date, timezone, status, live_started_at, version
		FROM events WHERE id = ?`, id).
		Scan(&e.ID, &e.Name, &date, &tz, &status, &started, &version)
	if err != nil {
		return e, 0, notFound(err)
	}
	day, err := time.Parse(dateLayout, date)
	if err != nil {
		return e, 0, err
	}
	if e.Location, err = time.LoadLocation(tz); err != nil {
		return e, 0, err
	}
	e.Date = domain.Date{Year: day.Year(), Month: day.Month(), Day: day.Day()}
	e.Status = domain.EventStatus(status)
	e.LiveStartedAt = fromNullNS(started)
	return e, version, nil
}

// guard loads the event for a write by a role and enforces what holds for
// every write: the event has not ended, and the actor is a still-valid
// link of this event (access-control.md section 5, rules 4 and 6).
func guard(ctx context.Context, tx *sql.Tx, eventID int64, actor Actor, now time.Time) (domain.Event, int64, error) {
	return guardAs(ctx, tx, eventID, actor, actorRole, now)
}

// guardAs is guard for an actor of the given kind; only the recovery
// code's own uses ask for actorRecoveryCode (access-control.md section 2).
//
// Who acts is settled before the event is read: a cue or role id of
// another event must fail as cross-event, never as that event having
// ended, which a client would take as its own event ending (api.md
// section 6).
func guardAs(ctx context.Context, tx *sql.Tx, eventID int64, actor Actor, kind actorKind, now time.Time) (domain.Event, int64, error) {
	if actor.kind != kind || actor.cred == nil {
		return domain.Event{}, 0, ErrNoActor
	}
	if actor.eventID != eventID {
		return domain.Event{}, 0, ErrCrossEvent
	}
	e, v, err := loadEvent(ctx, tx, eventID)
	if err != nil {
		return e, v, err
	}
	if e.Ended(now) {
		return e, v, domain.ErrEnded
	}
	return e, v, checkActor(ctx, tx, eventID, actor, kind)
}

func checkActor(ctx context.Context, tx *sql.Tx, eventID int64, actor Actor, kind actorKind) error {
	if actor.kind != kind || actor.cred == nil {
		return ErrNoActor
	}
	if actor.eventID != eventID {
		return ErrCrossEvent
	}
	var (
		query = `SELECT 1 FROM access_tokens t JOIN roles r ON r.id = t.role_id
			WHERE t.token_hash = ? AND t.revoked_at IS NULL AND r.id = ? AND r.event_id = ?`
		args    = []any{*actor.cred, actor.roleID, eventID}
		invalid = ErrInvalidToken
	)
	if actor.kind == actorRecoveryCode {
		query = `SELECT 1 FROM events WHERE recovery_code_hash = ? AND id = ?`
		args, invalid = []any{*actor.cred, eventID}, ErrInvalidRecoveryCode
	}
	var one int
	err := tx.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid
	}
	return err
}

// RecordRecoveryExport logs the read-only export of the after-event review
// (access-control.md section 2, use 3): recovery code only, after the
// event ended. It is the one write allowed then, and only appends to the
// action log.
func (s *Store) RecordRecoveryExport(ctx context.Context, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := checkActor(ctx, tx, actor.eventID, actor, actorRecoveryCode); err != nil {
			return err
		}
		e, _, err := loadEvent(ctx, tx, actor.eventID)
		if err != nil {
			return err
		}
		if !e.Ended(now) {
			return ErrNotEnded
		}
		return logAction(ctx, tx, actor.eventID, actor, "recovery-export", nil, now)
	})
}

// guardVersion is guard plus the client's version check
// (sync-and-offline.md section 3), for Master writes to the event and its cues.
func guardVersion(ctx context.Context, tx *sql.Tx, eventID, expectVersion int64, actor Actor, now time.Time) (domain.Event, error) {
	e, v, err := guard(ctx, tx, eventID, actor, now)
	if err != nil {
		return e, err
	}
	if v != expectVersion {
		return e, ErrVersionConflict
	}
	return e, nil
}

// StartEvent moves a draft event to live on its own. The API starts an
// event with its first advance instead (ADR-0020); this stays for tools
// and tests.
func (s *Store) StartEvent(ctx context.Context, eventID, expectVersion int64, actor Actor, now time.Time) (int64, error) {
	var version int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		e, err := guardVersion(ctx, tx, eventID, expectVersion, actor, now)
		if err != nil {
			return err
		}
		if _, err := startEvent(ctx, tx, e, actor, now); err != nil {
			return err
		}
		version, err = bumpVersion(ctx, tx, eventID)
		return err
	})
	return version, err
}

// startEvent applies domain Event.Start (event day only) and logs it,
// inside the caller's write transaction.
func startEvent(ctx context.Context, tx *sql.Tx, e domain.Event, actor Actor, now time.Time) (domain.Event, error) {
	e, err := e.Start(now)
	if err != nil {
		return e, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE events SET status = ?, live_started_at = ? WHERE id = ?`,
		e.Status, toNS(e.LiveStartedAt), e.ID); err != nil {
		return e, err
	}
	return e, logAction(ctx, tx, e.ID, actor, "start-event", nil, now)
}

// RescheduleEvent changes a draft event's date with its recovery code,
// moving every cue by the same number of days in one transaction
// (access-control.md section 2, use 2). The rules are domain.Reschedule.
func (s *Store) RescheduleEvent(ctx context.Context, actor Actor, to domain.Date, now time.Time) (int64, error) {
	var version int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		e, _, err := guardAs(ctx, tx, actor.eventID, actor, actorRecoveryCode, now)
		if err != nil {
			return err
		}
		cues, err := loadCues(ctx, tx, e.ID)
		if err != nil {
			return err
		}
		_, movedCues, err := e.Reschedule(to, cues, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET event_date = ? WHERE id = ?`,
			to.String(), e.ID); err != nil {
			return err
		}
		for _, c := range movedCues {
			if _, err := tx.ExecContext(ctx, `UPDATE cues SET scheduled_start = ? WHERE id = ?`, toNS(c.ScheduledStart), c.ID); err != nil {
				return err
			}
		}
		if err := logAction(ctx, tx, e.ID, actor, "reschedule",
			map[string]string{"from": e.Date.String(), "to": to.String()}, now); err != nil {
			return err
		}
		version, err = bumpVersion(ctx, tx, e.ID)
		return err
	})
	return version, err
}

// PurgeEnded deletes every event whose retention ran out (tech-stack.md
// tech-stack.md 2.1「資料刪除」) and returns how many. CreateEvent only stores dates and zones that
// read back, so an unreadable event means the database was edited by hand;
// it is skipped and reported in the error while the others are deleted.
func (s *Store) PurgeEnded(ctx context.Context, now time.Time) (int, error) {
	var (
		n       int
		skipped []error
	)
	err := s.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM events`)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			e, _, err := loadEvent(ctx, tx, id)
			if err != nil {
				skipped = append(skipped, fmt.Errorf("event %d: %w", id, err))
				continue
			}
			if now.Before(e.PurgeAt()) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, errors.Join(skipped...)
}

func bumpVersion(ctx context.Context, tx *sql.Tx, eventID int64) (int64, error) {
	var v int64
	err := tx.QueryRowContext(ctx, `UPDATE events SET version = version + 1 WHERE id = ? RETURNING version`, eventID).Scan(&v)
	return v, notFound(err)
}

func logAction(ctx context.Context, tx *sql.Tx, eventID int64, actor Actor, typ string, payload any, now time.Time) error {
	body := []byte("{}")
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO action_logs (event_id, at, actor, type, payload) VALUES (?, ?, ?, ?, ?)`,
		eventID, toNS(now), actor.String(), typ, string(body))
	return err
}
