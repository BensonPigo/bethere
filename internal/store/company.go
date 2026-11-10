package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// Wedding planner companies (M4, ADR-0039; access-control.md section 7).
// A member proves who they are with a login link (a session) on the
// company page only; a console link takes a member into one event of
// their company as its Master. Every company write proves the session
// again inside its write transaction, so a member removed at the same
// moment leaves nothing behind (US-CO-2.4).

var (
	// ErrInvalidSession is a login link that is unknown, expired, logged
	// out, of a removed member or of an email-only release (US-CO-1.5,
	// US-CO-1.13); they are never told apart.
	ErrInvalidSession = errors.New("not a valid login link")
	// ErrNotMember is an email of no company (US-CO-1.3, US-O-12.8).
	ErrNotMember = errors.New("no member with this email")
	// ErrEmailTaken is an email already of some company (US-O-11.2).
	ErrEmailTaken = errors.New("this email is a member already")
	// ErrTooManyMembers is a company at MembersPerOrganization (US-CO-2.5).
	ErrTooManyMembers = errors.New("the company has its most members")
	// ErrCannotRemoveSelf keeps a company from losing its last member on
	// its own page (US-CO-2.3): the remover is proved inside the removal,
	// so two members removing each other leave one.
	ErrCannotRemoveSelf = errors.New("a member cannot remove themselves")
	// ErrTooManyEvents is a company's daily limit (US-CO-3.4).
	ErrTooManyEvents = errors.New("the company made its events for today")
	// ErrTooManyOrganizations is the admin page's daily limit (US-O-11.5).
	ErrTooManyOrganizations = errors.New("the admin page made its companies for today")
	// ErrMembersClosed is every member's link while the reset after a
	// restore is not done (US-O-12.6): the API answers 503, never "invalid".
	ErrMembersClosed = errors.New("member links are closed until the reset after a restore is done")
)

// The numbers of US-CO-1.4, 1.8, 2.5, 3.4, 4.3 and US-O-11.5.
const (
	SessionLife              = 30 * 24 * time.Hour
	SessionsPerMember        = 20
	ConsolesPerMemberEvent   = 10
	MembersPerOrganization   = 50
	EventsPerOrganizationDay = 20
	OrganizationsPerDay      = 5
)

// CloseMemberAccess refuses every login and console link from now on, for
// a run whose reset after a restore failed (US-O-12.6).
func (s *Store) CloseMemberAccess() { s.membersClosed.Store(true) }

// MembersClosed reports whether member links are closed for this run.
func (s *Store) MembersClosed() bool { return s.membersClosed.Load() }

// ResetMemberAccess deletes every login and console link and clears every
// login mail's link in one write (US-O-12.5); serve calls it before taking
// requests after a restore. The login mails stay, so the limits keep
// counting them (US-CO-1.11).
func (s *Store) ResetMemberAccess(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`DELETE FROM member_sessions`,
			`DELETE FROM console_tokens`,
			`UPDATE login_mails SET token_hash = NULL`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
}

// membersOpen returns ErrMembersClosed while member links are closed.
func (s *Store) membersOpen() error {
	if s.membersClosed.Load() {
		return ErrMembersClosed
	}
	return nil
}

// CreateOrganization is the admin page's new company with its first member
// (US-O-11). The admin link is proved and the day's count taken in the
// write transaction. The name and email are cleaned by the caller.
func (s *Store) CreateOrganization(ctx context.Context, adminHash []byte, name, email string, now time.Time) (int64, error) {
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := checkAdmin(ctx, tx, adminHash); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM organizations WHERE created_at > ?`,
			toNS(now.Add(-24*time.Hour))).Scan(&n); err != nil {
			return err
		}
		if n >= OrganizationsPerDay {
			return ErrTooManyOrganizations
		}
		if err := emailFree(ctx, tx, email); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO organizations (name, created_at) VALUES (?, ?) RETURNING id`,
			name, toNS(now)).Scan(&id); err != nil {
			return err
		}
		_, err := insertMember(ctx, tx, id, email, now)
		return err
	})
	return id, err
}

// emailFree is ErrEmailTaken when any company has this email.
func emailFree(ctx context.Context, tx *sql.Tx, email string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM members WHERE email = ?`, email).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrEmailTaken
	}
	return nil
}

// insertMember adds email to the company within MembersPerOrganization
// and returns the new member's number.
func insertMember(ctx context.Context, tx *sql.Tx, orgID int64, email string, now time.Time) (int64, error) {
	if err := emailFree(ctx, tx, email); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM members WHERE organization_id = ?`, orgID).Scan(&n); err != nil {
		return 0, err
	}
	if n >= MembersPerOrganization {
		return 0, ErrTooManyMembers
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO members (organization_id, email, created_at) VALUES (?, ?, ?) RETURNING id`,
		orgID, email, toNS(now)).Scan(&id)
	return id, err
}

// IssueLoginLink stores a new login link for the member with this email,
// for the developer's command when mail is down (US-O-12.8). The email is
// cleaned by the caller.
func (s *Store) IssueLoginLink(ctx context.Context, email string, sessionHash []byte, now time.Time) error {
	if err := s.membersOpen(); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var memberID int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM members WHERE email = ?`, email).Scan(&memberID)
		if errors.Is(err, sql.ErrNoRows) {
			return confirmedMiss(ctx, ErrNotMember)
		}
		if err != nil {
			return err
		}
		return insertSession(ctx, tx, memberID, sessionHash, now)
	})
}

// insertSession stores a verified login link for the member, deleting
// their oldest once they hold SessionsPerMember (US-CO-1.8).
func insertSession(ctx context.Context, tx *sql.Tx, memberID int64, sessionHash []byte, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM member_sessions WHERE member_id = ? AND id NOT IN
		(SELECT id FROM member_sessions WHERE member_id = ? AND expires_at > ? AND verified = 1 ORDER BY id DESC LIMIT ?)`,
		memberID, memberID, toNS(now), SessionsPerMember-1); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO member_sessions (member_id, token_hash, created_at, expires_at, verified)
		VALUES (?, ?, ?, ?, 1)`, memberID, sessionHash, toNS(now), toNS(now.Add(SessionLife)))
	return err
}

// Logout deletes this login link only (US-CO-1.4).
func (s *Store) Logout(ctx context.Context, sessionHash []byte) error {
	if err := s.membersOpen(); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM member_sessions WHERE token_hash = ? AND verified = 1`, sessionHash)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return confirmedMiss(ctx, ErrInvalidSession)
		}
		return nil
	})
}

// confirmedMiss is miss unless the context ended, so a miss that raced
// a cancellation is never trusted (as in resolveToken).
func confirmedMiss(ctx context.Context, miss error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return miss
}

// member is who a login link belongs to.
type member struct {
	id, orgID int64
}

// sessionMember proves a login link inside the caller's transaction.
func sessionMember(ctx context.Context, q querier, sessionHash []byte, now time.Time) (member, error) {
	var m member
	err := q.QueryRowContext(ctx, `SELECT m.id, m.organization_id FROM member_sessions s JOIN members m ON m.id = s.member_id
		WHERE s.token_hash = ? AND s.expires_at > ? AND s.verified = 1`, sessionHash, toNS(now)).Scan(&m.id, &m.orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return m, confirmedMiss(ctx, ErrInvalidSession)
	}
	return m, err
}

// Company is what the company page shows (api.md 10.1).
type Company struct {
	Name    string
	Me      int64
	Members []Member
	Events  []CompanyEvent
}

type Member struct {
	ID    int64
	Email string
}

type CompanyEvent struct {
	ID     int64
	Name   string
	Date   domain.Date
	Status domain.EventStatus // Draft, Live or EventEnded
}

// CheckSession proves a login link from the read pool, so a stranger's
// request is refused before its body is read (api.md 10.1).
func (s *Store) CheckSession(ctx context.Context, sessionHash []byte, now time.Time) error {
	if err := s.membersOpen(); err != nil {
		return err
	}
	_, err := sessionMember(ctx, s.r, sessionHash, now)
	return err
}

// Company reads the caller's company in one read transaction that proves
// the login link first: its members, and its events not ended by date
// from the nearest, then the ended ones from the latest (US-CO-3.1).
func (s *Store) Company(ctx context.Context, sessionHash []byte, now time.Time) (Company, error) {
	var c Company
	if err := s.membersOpen(); err != nil {
		return c, err
	}
	tx, err := s.r.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	return readCompany(ctx, tx, sessionHash, now)
}

func readCompany(ctx context.Context, tx *sql.Tx, sessionHash []byte, now time.Time) (Company, error) {
	var c Company
	m, err := sessionMember(ctx, tx, sessionHash, now)
	if err != nil {
		return c, err
	}
	c.Me = m.id
	if err := tx.QueryRowContext(ctx, `SELECT name FROM organizations WHERE id = ?`, m.orgID).Scan(&c.Name); err != nil {
		return c, err
	}
	if c.Members, err = loadMembers(ctx, tx, m.orgID); err != nil {
		return c, err
	}
	c.Events, err = loadCompanyEvents(ctx, tx, m.orgID, now)
	return c, err
}

func loadMembers(ctx context.Context, tx *sql.Tx, orgID int64) ([]Member, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, email FROM members WHERE organization_id = ? ORDER BY id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var mb Member
		if err := rows.Scan(&mb.ID, &mb.Email); err != nil {
			return nil, err
		}
		out = append(out, mb)
	}
	return out, rows.Err()
}

func loadCompanyEvents(ctx context.Context, tx *sql.Tx, orgID int64, now time.Time) ([]CompanyEvent, error) {
	ids, err := companyEventIDs(ctx, tx, orgID)
	if err != nil {
		return nil, err
	}
	var open, ended []CompanyEvent
	for _, id := range ids {
		e, _, err := loadEvent(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		ce := CompanyEvent{ID: e.ID, Name: e.Name, Date: e.Date, Status: e.Status}
		if e.Ended(now) {
			ce.Status = EventEnded
			ended = append([]CompanyEvent{ce}, ended...)
			continue
		}
		open = append(open, ce)
	}
	return append(open, ended...), nil
}

// companyEventIDs lists the company's events by date, then by id.
func companyEventIDs(ctx context.Context, tx *sql.Tx, orgID int64) ([]int64, error) {
	return queryIDs(ctx, tx, `SELECT id FROM events WHERE organization_id = ? ORDER BY event_date, id`, orgID)
}

// companyWrite runs fn in one write transaction after proving the login
// link inside it (access-control.md section 7, 歸屬與競態).
func (s *Store) companyWrite(ctx context.Context, sessionHash []byte, now time.Time, fn func(*sql.Tx, member) error) error {
	if err := s.membersOpen(); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		m, err := sessionMember(ctx, tx, sessionHash, now)
		if err != nil {
			return err
		}
		return fn(tx, m)
	})
}

// AddMember adds email to the caller's company (US-CO-2.1) and returns
// who added and who was added, for the service log (US-CO-2.6).
func (s *Store) AddMember(ctx context.Context, sessionHash []byte, email string, now time.Time) (by, added int64, err error) {
	err = s.companyWrite(ctx, sessionHash, now, func(tx *sql.Tx, m member) (err error) {
		by = m.id
		added, err = insertMember(ctx, tx, m.orgID, email, now)
		return err
	})
	return by, added, err
}

// RemoveMember removes another member of the caller's company; their links
// go with them (US-CO-2.3, 2.4). A member of another company or none is
// ErrNotFound.
//
// It returns who removed, for the service log (US-CO-2.6).
func (s *Store) RemoveMember(ctx context.Context, sessionHash []byte, memberID int64, now time.Time) (by int64, err error) {
	err = s.companyWrite(ctx, sessionHash, now, func(tx *sql.Tx, m member) error {
		by = m.id
		if memberID == m.id {
			return ErrCannotRemoveSelf
		}
		return deleteMember(ctx, tx, m.orgID, memberID)
	})
	return by, err
}

// deleteMember deletes a member of orgID; their links go with them
// through ON DELETE CASCADE.
func deleteMember(ctx context.Context, tx *sql.Tx, orgID, memberID int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM members WHERE id = ? AND organization_id = ?`, memberID, orgID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateCompanyEvent creates a draft event of the caller's company, as
// CreateEvent does without an invite, within EventsPerOrganizationDay
// (US-CO-3.2, 3.4). The count has only a lower bound: a clock set back
// makes it stricter, never looser.
func (s *Store) CreateCompanyEvent(ctx context.Context, sessionHash []byte, ne NewEvent, now time.Time) (int64, error) {
	if err := checkNewEvent(ne); err != nil {
		return 0, err
	}
	var eventID int64
	err := s.companyWrite(ctx, sessionHash, now, func(tx *sql.Tx, m member) (err error) {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE organization_id = ? AND created_at > ?`,
			m.orgID, toNS(now.Add(-24*time.Hour))).Scan(&n); err != nil {
			return err
		}
		if n >= EventsPerOrganizationDay {
			return ErrTooManyEvents
		}
		eventID, err = insertEvent(ctx, tx, ne, m.orgID, Actor{kind: actorMember, memberID: m.id}, nil, now)
		return err
	})
	return eventID, err
}

// OpenConsole stores a new console link of the caller for one event of
// their company; their oldest for that event is deleted once they hold
// ConsolesPerMemberEvent (US-CO-4.3). An event of another company or none
// is ErrNotFound. An ended event still gets one: it answers "ended".
func (s *Store) OpenConsole(ctx context.Context, sessionHash []byte, eventID int64, consoleHash []byte, now time.Time) error {
	return s.companyWrite(ctx, sessionHash, now, func(tx *sql.Tx, m member) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE id = ? AND organization_id = ?`,
			eventID, m.orgID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM console_tokens WHERE member_id = ? AND event_id = ? AND id NOT IN
			(SELECT id FROM console_tokens WHERE member_id = ? AND event_id = ? ORDER BY id DESC LIMIT ?)`,
			m.id, eventID, m.id, eventID, ConsolesPerMemberEvent-1); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO console_tokens (member_id, event_id, token_hash, created_at, verified) VALUES (?, ?, ?, ?, 1)`,
			m.id, eventID, consoleHash, toNS(now))
		return err
	})
}

// resolveConsole is the second half of resolveToken (api.md 10.2): a
// console link of a member still in the company that owns the event,
// resolved to that event's Master, and whether this release made it
// (US-CO-1.13). ErrNoRows passes up as is.
func resolveConsole(ctx context.Context, tx *sql.Tx, tokenHash []byte) (TokenOwner, bool, error) {
	var (
		o                   TokenOwner
		consoleID, memberID int64
		verified            sql.NullInt64
	)
	err := tx.QueryRowContext(ctx, `SELECT c.id, c.member_id, c.verified, r.id, r.event_id, r.name
		FROM console_tokens c
		JOIN members m ON m.id = c.member_id
		JOIN events e ON e.id = c.event_id AND e.organization_id = m.organization_id
		JOIN roles r ON r.event_id = c.event_id AND r.type = ?
		WHERE c.token_hash = ?`, domain.Master, tokenHash).
		Scan(&consoleID, &memberID, &verified, &o.RoleID, &o.EventID, &o.RoleName)
	if err != nil {
		return o, false, err
	}
	o.RoleType = domain.Master
	o.View = domain.Master.DefaultView()
	o.Actor = Actor{kind: actorMember, roleID: o.RoleID, eventID: o.EventID, memberID: memberID, consoleID: consoleID,
		cred: credential(tokenHash)}
	return o, verified.Int64 == 1, nil
}

// checkConsole proves a member's console link again inside a write.
func checkConsole(ctx context.Context, tx *sql.Tx, eventID int64, actor Actor) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM console_tokens c
		JOIN members m ON m.id = c.member_id
		JOIN events e ON e.id = c.event_id AND e.organization_id = m.organization_id
		WHERE c.token_hash = ? AND c.id = ? AND c.member_id = ? AND c.event_id = ? AND c.verified = 1`,
		*actor.cred, actor.consoleID, actor.memberID, eventID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return confirmedMiss(ctx, ErrInvalidToken)
	}
	return err
}

// FindMember is the company of email, for the developer's command
// (US-O-12.1); ErrNotMember when it has none.
func (s *Store) FindMember(ctx context.Context, email string) (int64, string, error) {
	var (
		id   int64
		name string
	)
	err := s.r.QueryRowContext(ctx, `SELECT o.id, o.name FROM members m JOIN organizations o ON o.id = m.organization_id
		WHERE m.email = ?`, email).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", confirmedMiss(ctx, ErrNotMember)
	}
	return id, name, err
}

// AddMemberTo adds email to a company, for the developer's command
// (US-O-12.2): the same rules as on the company page.
func (s *Store) AddMemberTo(ctx context.Context, orgID int64, email string, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM organizations WHERE id = ?`, orgID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		_, err := insertMember(ctx, tx, orgID, email, now)
		return err
	})
}

// RemoveMemberByEmail removes the member with email, a company's last one
// too, for the developer's command (US-O-12.3), and returns the company.
func (s *Store) RemoveMemberByEmail(ctx context.Context, email string) (int64, string, error) {
	var (
		orgID    int64
		name     string
		memberID int64
	)
	err := s.write(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT m.id, o.id, o.name FROM members m JOIN organizations o ON o.id = m.organization_id
			WHERE m.email = ?`, email).Scan(&memberID, &orgID, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return confirmedMiss(ctx, ErrNotMember)
		}
		if err != nil {
			return err
		}
		return deleteMember(ctx, tx, orgID, memberID)
	})
	return orgID, name, err
}
