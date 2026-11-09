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
	Invites       []AdminInvite
	Events        []AdminEvent
	Organizations []AdminOrganization // no email (US-O-11.3)
}

type AdminOrganization struct {
	ID        int64
	Name      string
	Members   int
	Events    int
	CreatedAt time.Time
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
	// Organization is the company's name; "" for an event of none.
	Organization string
}

// AdminOverview reads the unused codes, every event and every company,
// newest first, in one read transaction that proves the admin link first.
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
	if o.Invites, err = adminInvites(ctx, tx, now); err != nil {
		return o, err
	}
	if o.Events, err = adminEvents(ctx, tx, now); err != nil {
		return o, err
	}
	o.Organizations, err = adminOrganizations(ctx, tx)
	return o, err
}

// adminInvites lists the codes still usable, newest first.
func adminInvites(ctx context.Context, tx *sql.Tx, now time.Time) ([]AdminInvite, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, expires_at FROM invites WHERE expires_at > ? ORDER BY id DESC`, toNS(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminInvite
	for rows.Next() {
		var (
			inv     AdminInvite
			expires int64
		)
		if err := rows.Scan(&inv.ID, &expires); err != nil {
			return nil, err
		}
		inv.ExpiresAt = fromNS(expires)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// adminEvents lists every event, newest first, with its company and its
// status as of now.
func adminEvents(ctx context.Context, tx *sql.Tx, now time.Time) ([]AdminEvent, error) {
	created, err := adminEventRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	var out []AdminEvent
	for _, ev := range created {
		e, _, err := loadEvent(ctx, tx, ev.ID)
		if err != nil {
			return nil, err
		}
		ev.Name, ev.Date, ev.Status = e.Name, e.Date, e.Status
		if e.Ended(now) {
			ev.Status = EventEnded
		}
		out = append(out, ev)
	}
	return out, nil
}

// adminEventRows reads every event's id, creation and company, newest first.
func adminEventRows(ctx context.Context, tx *sql.Tx) ([]AdminEvent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.id, e.created_at, coalesce(o.name, '')
		FROM events e LEFT JOIN organizations o ON o.id = e.organization_id ORDER BY e.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminEvent
	for rows.Next() {
		var (
			ev AdminEvent
			at int64
		)
		if err := rows.Scan(&ev.ID, &at, &ev.Organization); err != nil {
			return nil, err
		}
		ev.CreatedAt = fromNS(at)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// adminOrganizations lists every company, newest first, with how many
// members and events it has.
func adminOrganizations(ctx context.Context, tx *sql.Tx) ([]AdminOrganization, error) {
	rows, err := tx.QueryContext(ctx, `SELECT o.id, o.name, o.created_at,
		(SELECT count(*) FROM members m WHERE m.organization_id = o.id),
		(SELECT count(*) FROM events e WHERE e.organization_id = o.id)
		FROM organizations o ORDER BY o.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminOrganization
	for rows.Next() {
		var (
			org AdminOrganization
			at  int64
		)
		if err := rows.Scan(&org.ID, &org.Name, &at, &org.Members, &org.Events); err != nil {
			return nil, err
		}
		org.CreatedAt = fromNS(at)
		out = append(out, org)
	}
	return out, rows.Err()
}
