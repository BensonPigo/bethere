package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

var (
	// ErrInvalidAdmin is a token that is not the current admin link,
	// a role's link included (US-O-9.1).
	ErrInvalidAdmin = errors.New("not the admin link")
	// ErrTooManyInvites is the admin page's daily limit (US-O-10.3).
	ErrTooManyInvites = errors.New("the admin page made its codes for today")
)

// AdminInvitesPerDay bounds what a leaked admin link can make: codes in
// any 24 hours (access-control.md section 1).
const AdminInvitesPerDay = 20

// EventEnded is the overview's status of an event past its end; it is
// worked out from the date, never stored (data-model.md, Event).
const EventEnded domain.EventStatus = "Ended"

// IssueAdminLink makes tokenHash the only admin link: the last one stops
// working in the same transaction (US-O-9.1).
func (s *Store) IssueAdminLink(ctx context.Context, tokenHash []byte) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM admin_tokens`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO admin_tokens (token_hash) VALUES (?)`, tokenHash)
		return err
	})
}

// CheckAdmin proves the admin link. ErrInvalidAdmin only for a confirmed
// miss, never for a cancelled or failing query.
func (s *Store) CheckAdmin(ctx context.Context, tokenHash []byte) error {
	return checkAdmin(ctx, s.r, tokenHash)
}

func checkAdmin(ctx context.Context, q querier, tokenHash []byte) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM admin_tokens WHERE token_hash = ?`, tokenHash).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrInvalidAdmin
	}
	return err
}

// CreateAdminInvite stores a code made on the admin page (US-O-10) and
// returns its id. The link is proved and the day's count taken in the
// write transaction, so two requests at once never pass the limit.
func (s *Store) CreateAdminInvite(ctx context.Context, adminHash, codeHash []byte, now time.Time) (int64, error) {
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) (err error) {
		if err := checkAdmin(ctx, tx, adminHash); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM admin_invites WHERE at > ?`,
			toNS(now.Add(-24*time.Hour))).Scan(&n); err != nil {
			return err
		}
		if n >= AdminInvitesPerDay {
			return ErrTooManyInvites
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_invites (at) VALUES (?)`, toNS(now)); err != nil {
			return err
		}
		id, err = insertInvite(ctx, tx, codeHash, now)
		return err
	})
	return id, err
}

// AdminOverview is what the admin page lists (US-O-9.2): no token, code
// or hash, no role, cue or guest.
type AdminOverview struct {
	Invites []AdminInvite
	Events  []AdminEvent
}

type AdminInvite struct {
	ID        int64
	ExpiresAt time.Time
}

type AdminEvent struct {
	ID        int64
	Name      string
	Date      domain.Date
	Status    domain.EventStatus // Draft, Live or EventEnded
	CreatedAt time.Time
}

// AdminOverview reads the unused codes and every event, newest first, in
// one read transaction that proves the admin link first.
func (s *Store) AdminOverview(ctx context.Context, adminHash []byte, now time.Time) (AdminOverview, error) {
	var o AdminOverview
	tx, err := s.r.BeginTx(ctx, nil)
	if err != nil {
		return o, err
	}
	defer tx.Rollback()
	if err := checkAdmin(ctx, tx, adminHash); err != nil {
		return o, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, expires_at FROM invites WHERE expires_at > ? ORDER BY id DESC`, toNS(now))
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var (
			inv     AdminInvite
			expires int64
		)
		if err := rows.Scan(&inv.ID, &expires); err != nil {
			rows.Close()
			return o, err
		}
		inv.ExpiresAt = fromNS(expires)
		o.Invites = append(o.Invites, inv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return o, err
	}

	rows, err = tx.QueryContext(ctx, `SELECT id, created_at FROM events ORDER BY id DESC`)
	if err != nil {
		return o, err
	}
	var created []AdminEvent
	for rows.Next() {
		var (
			ev AdminEvent
			at int64
		)
		if err := rows.Scan(&ev.ID, &at); err != nil {
			rows.Close()
			return o, err
		}
		ev.CreatedAt = fromNS(at)
		created = append(created, ev)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return o, err
	}
	for _, ev := range created {
		e, _, err := loadEvent(ctx, tx, ev.ID)
		if err != nil {
			return o, err
		}
		ev.Name, ev.Date, ev.Status = e.Name, e.Date, e.Status
		if e.Ended(now) {
			ev.Status = EventEnded
		}
		o.Events = append(o.Events, ev)
	}
	return o, nil
}
