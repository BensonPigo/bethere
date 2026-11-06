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
// LookupToken (a role's link, or a member's console link acting as the
// event's Master, api.md 10.2) or LookupRecoveryCode (an event's recovery
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
	// actorMember: the member, and the console link they act with (0 when
	// they create an event on the company page).
	memberID  int64
	consoleID int64
}

type actorKind uint8

const (
	actorNone actorKind = iota
	actorRole
	actorRecoveryCode
	actorServer
	actorMember // a member of a company: with a console link, the event's Master (api.md 10.2)
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
	case actorMember:
		if a.consoleID != 0 {
			return fmt.Sprintf("member:%d/console:%d", a.memberID, a.consoleID)
		}
		return fmt.Sprintf("member:%d", a.memberID)
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
	// InviteHash, when set, is the invite code that pays for the event on
	// /new (US-M-17.5): it must be unused and unexpired, and is deleted with
	// the event's creation. The developer's create-event leaves it nil.
	InviteHash []byte
}

// CreateEvent creates a draft event with its single Master role and that
// role's token (access-control.md section 2). With an invite, a code that
// is wrong, used or expired is ErrInvalidInvite and nothing is written.
func (s *Store) CreateEvent(ctx context.Context, ne NewEvent, now time.Time) (int64, error) {
	if err := checkNewEvent(ne); err != nil {
		return 0, err
	}
	if ne.InviteHash != nil {
		// A miss is answered from the read pool, so guessing never takes
		// the single writer the event day's writes need.
		var one int
		err := s.r.QueryRowContext(ctx, `SELECT 1 FROM invites WHERE code_hash = ? AND expires_at > ?`,
			ne.InviteHash, toNS(now)).Scan(&one)
		if err != nil {
			return 0, inviteMiss(ctx, err)
		}
	}
	var eventID int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		var logged any
		if ne.InviteHash != nil {
			// The read above checked the expiry at this same now; deleting
			// here is what lets only one of two requests with a code win.
			var inviteID int64
			err := tx.QueryRowContext(ctx, `DELETE FROM invites WHERE code_hash = ? RETURNING id`,
				ne.InviteHash).Scan(&inviteID)
			if err != nil {
				return inviteMiss(ctx, err)
			}
			logged = map[string]int64{"inviteId": inviteID}
		}
		var err error
		eventID, err = insertEvent(ctx, tx, ne, 0, Actor{kind: actorServer}, logged, now)
		return err
	})
	return eventID, err
}

// checkNewEvent is what every new event must have, whoever creates it.
func checkNewEvent(ne NewEvent) error {
	if !ne.Date.Valid() {
		return domain.ErrInvalidDate
	}
	if !domain.SupportedTimeZone(ne.TimeZone) {
		return ErrTimeZone
	}
	return nil
}

// insertEvent writes a draft event, its Master role and that role's link,
// and logs the creation, inside the caller's write transaction. orgID 0
// is an event of no company (ADR-0039).
func insertEvent(ctx context.Context, tx *sql.Tx, ne NewEvent, orgID int64, actor Actor, logged any, now time.Time) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO events
		(name, event_date, timezone, status, recovery_code_hash, created_at, organization_id)
		VALUES (?, ?, ?, 'Draft', ?, ?, ?)`,
		ne.Name, ne.Date.String(), ne.TimeZone, ne.RecoveryCodeHash, toNS(now),
		sql.NullInt64{Int64: orgID, Valid: orgID != 0})
	if err != nil {
		return 0, err
	}
	eventID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := insertMaster(ctx, tx, eventID, ne, now); err != nil {
		return 0, err
	}
	return eventID, logAction(ctx, tx, eventID, actor, "create-event", logged, now)
}

// insertMaster writes a new event's Master role and its link.
func insertMaster(ctx context.Context, tx *sql.Tx, eventID int64, ne NewEvent, now time.Time) error {
	res, err := tx.ExecContext(ctx, `INSERT INTO roles (event_id, name, type) VALUES (?, ?, ?)`,
		eventID, ne.MasterName, domain.Master)
	if err != nil {
		return err
	}
	roleID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO access_tokens (role_id, token_hash, created_at) VALUES (?, ?, ?)`,
		roleID, ne.MasterTokenHash, toNS(now))
	return err
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
	if err := actorFor(eventID, actor, kind); err != nil {
		return domain.Event{}, 0, err
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

// actsAs reports whether actor may act where kind is asked for: a member's
// console link acts as the event's Master wherever a role may, never where
// only the recovery code may (api.md 10.2).
func actsAs(actor Actor, kind actorKind) bool {
	return actor.kind == kind || kind == actorRole && actor.kind == actorMember && actor.consoleID != 0
}

// actorFor is whether actor, without the database, may act on eventID
// where kind is asked for.
func actorFor(eventID int64, actor Actor, kind actorKind) error {
	if !actsAs(actor, kind) || actor.cred == nil {
		return ErrNoActor
	}
	if actor.eventID != eventID {
		return ErrCrossEvent
	}
	return nil
}

func checkActor(ctx context.Context, tx *sql.Tx, eventID int64, actor Actor, kind actorKind) error {
	if err := actorFor(eventID, actor, kind); err != nil {
		return err
	}
	if actor.kind == actorMember {
		return checkConsole(ctx, tx, eventID, actor)
	}
	return checkCredential(ctx, tx, eventID, actor)
}

// checkCredential proves a role's link or the recovery code again.
func checkCredential(ctx context.Context, tx *sql.Tx, eventID int64, actor Actor) error {
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
		// As in resolveToken: a miss that raced with a cancellation is not trusted.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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
// 2.1「資料刪除」) and returns their ids. CreateEvent only stores dates and zones that
// read back, so an unreadable event means the database was edited by hand;
// it is skipped and reported in the error while the others are deleted.
func (s *Store) PurgeEnded(ctx context.Context, now time.Time) ([]int64, error) {
	var (
		deleted []int64
		skipped []error
	)
	err := s.write(ctx, func(tx *sql.Tx) error {
		ids, err := allEventIDs(ctx, tx)
		if err != nil {
			return err
		}
		deleted, skipped, err = purgeEvents(ctx, tx, ids, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	skipped = append(skipped, s.purgeExpired(ctx, now)...)
	return deleted, errors.Join(skipped...)
}

// allEventIDs lists every event inside the caller's transaction.
func allEventIDs(ctx context.Context, tx *sql.Tx) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM events`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// purgeEvents deletes those of ids whose retention ran out; an unreadable
// one is skipped and reported.
func purgeEvents(ctx context.Context, tx *sql.Tx, ids []int64, now time.Time) (deleted []int64, skipped []error, err error) {
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
			return nil, nil, err
		}
		deleted = append(deleted, id)
	}
	return deleted, skipped, nil
}

// purgeExpired deletes the expired invite codes (US-M-17.11) and login
// links (data-model.md「婚顧公司」), each in a write of its own so a failure
// here never keeps an event past its retention.
func (s *Store) purgeExpired(ctx context.Context, now time.Time) []error {
	var errs []error
	for _, table := range []string{"invites", "member_sessions"} {
		// Table names are constants, not input.
		if _, err := s.w.ExecContext(ctx, `DELETE FROM `+table+` WHERE expires_at <= ?`, toNS(now)); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", table, err))
		}
	}
	return errs
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
