package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

var (
	// ErrRoleNotFound means a role id from a request is not in the
	// caller's event: it does not exist or belongs to another event, which
	// are not told apart (api.md section 6).
	ErrRoleNotFound = errors.New("role not found in this event")
	// ErrNotCheckInRole means the role cannot check in: only the types with
	// cues of their own can (access-control.md section 4).
	ErrNotCheckInRole = errors.New("this role type does not check in")
)

// roleIn returns the type of a role of eventID. It runs before any check
// of the role's type, so another event's role id is never told apart
// from one that does not exist (api.md section 3, ownership first).
func roleIn(ctx context.Context, tx *sql.Tx, eventID, roleID int64) (domain.RoleType, error) {
	var typ domain.RoleType
	err := tx.QueryRowContext(ctx, `SELECT type FROM roles WHERE id = ? AND event_id = ?`, roleID, eventID).Scan(&typ)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRoleNotFound
	}
	return typ, err
}

// SetCheckIn sets whether a role of eventID is checked in (US-X-5,
// US-M-1.8): by the role itself, or by the Master for it. Setting the
// state it already has writes nothing (US-X-5.4). It never changes the
// event version, so a check-in never turns the Master's next write into a
// conflict (ADR-0023). Which actor may set which role is the caller's
// check (access-control.md section 5, rule 3).
func (s *Store) SetCheckIn(ctx context.Context, eventID, roleID int64, checkedIn bool, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		typ, err := roleIn(ctx, tx, eventID, roleID)
		if err != nil {
			return err
		}
		if !typ.HasOwnCues() {
			return ErrNotCheckInRole
		}
		var current bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM check_ins WHERE role_id = ? AND undone_at IS NULL)`,
			roleID).Scan(&current); err != nil {
			return err
		}
		if current == checkedIn {
			return nil
		}
		if checkedIn {
			_, err = tx.ExecContext(ctx, `INSERT INTO check_ins (role_id, at, by_role_id) VALUES (?, ?, ?)`,
				roleID, toNS(now), actor.roleID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE check_ins SET undone_at = ? WHERE role_id = ? AND undone_at IS NULL`,
				toNS(now), roleID)
		}
		if err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, "checkin.set", map[string]any{"roleId": roleID, "checkedIn": checkedIn}, now)
	})
}
