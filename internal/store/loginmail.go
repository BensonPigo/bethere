package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Login mails (M5, ADR-0040; api.md 10.2). The login endpoint only reads
// (MemberByEmail); the mail queue's single worker writes a login mail when
// the limits allow it (PrepareLoginMail); the page trades the mail's link
// for a login link once (ConfirmLoginMail).

// ErrInvalidLoginMail is a login mail's link that is unknown, used,
// expired, cleared by a restore or of a removed member (US-CO-1.10); they
// are never told apart.
var ErrInvalidLoginMail = errors.New("not a valid login mail link")

// The numbers of US-CO-1.10 and 1.11.
const (
	LoginMailLife           = 15 * time.Minute
	MailWindow              = 24 * time.Hour
	MailsPerMemberDay       = 10
	MailsPerOrganizationDay = 100
	MailsPerDay             = 400
)

// MailOutcome is what PrepareLoginMail did.
type MailOutcome uint8

const (
	MailWritten  MailOutcome = iota // a login mail to send
	MailLimited                     // a limit of US-CO-1.11 holds it back
	MailNoMember                    // the member was removed meanwhile
)

// MemberByEmail is the member with this email, from one read of the read
// pool (US-CO-1.3); ErrNotMember when there is none. The email is cleaned
// by the caller.
func (s *Store) MemberByEmail(ctx context.Context, email string) (int64, error) {
	if err := s.membersOpen(); err != nil {
		return 0, err
	}
	var id int64
	err := s.r.QueryRowContext(ctx, `SELECT id FROM members WHERE email = ?`, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, confirmedMiss(ctx, ErrNotMember)
	}
	return id, err
}

// mailTarget is the member a login mail goes to, with their company.
type mailTarget struct {
	email string
	orgID int64
}

// PrepareLoginMail writes a login mail for the member with mailHash as its
// link, and returns where to send it (api.md 10.2). The limits are read
// from the read pool, so a held-back mail never takes the write lock. The
// queue's single worker is the only writer of login mails, so nothing can
// add one between the read and the write; a member removed meanwhile makes
// the insert fail on its foreign key.
func (s *Store) PrepareLoginMail(ctx context.Context, memberID int64, mailHash []byte, now time.Time) (string, MailOutcome, error) {
	t, outcome, err := mailAllowed(ctx, s.r, memberID, now)
	if err != nil || outcome != MailWritten {
		return "", outcome, err
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO login_mails (member_id, organization_id, token_hash, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`, memberID, t.orgID, mailHash, toNS(now), toNS(now.Add(LoginMailLife))); err != nil {
		return "", 0, err
	}
	return t.email, MailWritten, nil
}

// mailAllowed finds the member and checks the three limits of US-CO-1.11
// over the login mails of the last MailWindow.
func mailAllowed(ctx context.Context, q querier, memberID int64, now time.Time) (mailTarget, MailOutcome, error) {
	var t mailTarget
	err := q.QueryRowContext(ctx, `SELECT email, organization_id FROM members WHERE id = ?`, memberID).Scan(&t.email, &t.orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, MailNoMember, nil
	}
	if err != nil {
		return t, 0, err
	}
	var member, org, all int
	if err := q.QueryRowContext(ctx, `SELECT
			coalesce(sum(member_id = ?), 0), coalesce(sum(organization_id = ?), 0), count(*)
		FROM login_mails WHERE created_at > ?`, memberID, t.orgID, toNS(now.Add(-MailWindow))).
		Scan(&member, &org, &all); err != nil {
		return t, 0, err
	}
	if member >= MailsPerMemberDay || org >= MailsPerOrganizationDay || all >= MailsPerDay {
		return t, MailLimited, nil
	}
	return t, MailWritten, nil
}

// ConfirmLoginMail trades a login mail's link for a login link, once
// (US-CO-1.10): unused, unexpired, of a member still there. Two requests
// with the same link leave one login link.
func (s *Store) ConfirmLoginMail(ctx context.Context, mailHash, sessionHash []byte, now time.Time) error {
	if err := s.membersOpen(); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var memberID int64
		err := tx.QueryRowContext(ctx, `UPDATE login_mails SET used_at = ?
			WHERE token_hash = ? AND used_at IS NULL AND expires_at > ? AND member_id IS NOT NULL
			RETURNING member_id`, toNS(now), mailHash, toNS(now)).Scan(&memberID)
		if errors.Is(err, sql.ErrNoRows) {
			return confirmedMiss(ctx, ErrInvalidLoginMail)
		}
		if err != nil {
			return err
		}
		return insertSession(ctx, tx, memberID, sessionHash, now)
	})
}
