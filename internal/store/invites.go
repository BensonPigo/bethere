package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrInvalidInvite is a code that is wrong, used or expired; the three are
// never told apart (US-M-17.6).
var ErrInvalidInvite = errors.New("no unused, unexpired invite with this code")

// InviteLife is how long an invite code stays usable (ADR-0036).
const InviteLife = 30 * 24 * time.Hour

// CreateInvite stores a new invite code's hash and returns its id, the
// number the create-event log refers to (US-M-17.1).
func (s *Store) CreateInvite(ctx context.Context, codeHash []byte, now time.Time) (int64, error) {
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `INSERT INTO invites (code_hash, expires_at) VALUES (?, ?) RETURNING id`,
			codeHash, toNS(now.Add(InviteLife))).Scan(&id)
	})
	return id, err
}

// inviteMiss turns a lookup error into ErrInvalidInvite only for a
// confirmed miss: one that raced a cancellation is the cancellation.
func inviteMiss(ctx context.Context, err error) error {
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return ErrInvalidInvite
}
