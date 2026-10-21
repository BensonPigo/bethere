package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

var (
	ErrMasterRole = errors.New("the Master role is created with the event; its link is only replaced with the recovery code")
	// ErrInvalidToken and ErrEventEnded are the only lookup results that
	// may be shown to a client as "invalid" or "ended" (access-control.md
	// 3.3); any other error means the answer is unknown.
	ErrInvalidToken        = errors.New("no active token with this hash")
	ErrEventEnded          = errors.New("event has ended")
	ErrInvalidRecoveryCode = errors.New("no event with this recovery code")
	ErrRoleType            = errors.New("unknown role type")
)

// AddRole adds a non-Master role to an event.
func (s *Store) AddRole(ctx context.Context, eventID int64, name string, typ domain.RoleType, actor Actor, now time.Time) (int64, error) {
	if typ == domain.Master {
		return 0, ErrMasterRole
	}
	if !typ.Valid() {
		return 0, ErrRoleType
	}
	var roleID int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO roles (event_id, name, type) VALUES (?, ?, ?)`, eventID, name, typ)
		if err != nil {
			return err
		}
		if roleID, err = res.LastInsertId(); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, "add-role", map[string]any{"roleId": roleID}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
	return roleID, err
}

// IssueToken gives a role a new link, revoking its current one in the same
// transaction (access-control.md 3.1). The Master's link is replaced only
// by someone holding that event's recovery code (P5); other permission
// checks belong to the caller.
func (s *Store) IssueToken(ctx context.Context, roleID int64, tokenHash []byte, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		eventID, typ, err := roleOf(ctx, tx, roleID)
		if err != nil {
			return err
		}
		// The Master's link only with the recovery code; any other link
		// only by a role (access-control.md section 2, P5).
		kind := actorRole
		if typ == domain.Master {
			if actor.kind != actorRecoveryCode {
				return ErrMasterRole
			}
			kind = actorRecoveryCode
		}
		if _, _, err := guardAs(ctx, tx, eventID, actor, kind, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at = ? WHERE role_id = ? AND revoked_at IS NULL`,
			toNS(now), roleID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO access_tokens (role_id, token_hash, created_at) VALUES (?, ?, ?)`,
			roleID, tokenHash, toNS(now)); err != nil {
			return err
		}
		if err := logAction(ctx, tx, eventID, actor, "issue-token", map[string]any{"roleId": roleID}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// RevokeToken revokes a non-Master role's current link. It returns
// ErrNotFound when the role has none. The Master's link is never revoked
// alone, so an event always keeps one (access-control.md section 2).
func (s *Store) RevokeToken(ctx context.Context, roleID int64, actor Actor, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		eventID, typ, err := roleOf(ctx, tx, roleID)
		if err != nil {
			return err
		}
		if typ == domain.Master {
			return ErrMasterRole
		}
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at = ? WHERE role_id = ? AND revoked_at IS NULL`,
			toNS(now), roleID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		if err := logAction(ctx, tx, eventID, actor, "revoke-token", map[string]any{"roleId": roleID}, now); err != nil {
			return err
		}
		_, err = bumpVersion(ctx, tx, eventID)
		return err
	})
}

// TokenOwner is the role an active token belongs to, and the Actor that
// role writes as.
type TokenOwner struct {
	RoleID   int64
	EventID  int64
	RoleType domain.RoleType
	RoleName string
	Actor    Actor
}

// LookupToken resolves a token in the order of access-control.md 3.3:
// ErrInvalidToken when no active token has this hash, ErrEventEnded when
// its event has ended, otherwise the owner.
func (s *Store) LookupToken(ctx context.Context, tokenHash []byte, now time.Time) (TokenOwner, error) {
	// One read transaction, so the event cannot vanish between the queries.
	tx, err := s.r.BeginTx(ctx, nil)
	if err != nil {
		return TokenOwner{}, err
	}
	defer tx.Rollback()
	o, _, _, err := resolveToken(ctx, tx, tokenHash, now)
	return o, err
}

// resolveToken is the lookup of access-control.md 3.3 inside the caller's
// transaction, so LookupToken and Snapshot share one rule and one order:
// ErrInvalidToken only when the query succeeded and found no active token,
// then ErrEventEnded, otherwise the owner with its event and version. Any
// other failure, a cancelled or expired context included, is returned as
// is and never as ErrInvalidToken (T3b).
func resolveToken(ctx context.Context, tx *sql.Tx, tokenHash []byte, now time.Time) (TokenOwner, domain.Event, int64, error) {
	var o TokenOwner
	err := tx.QueryRowContext(ctx, `SELECT r.id, r.event_id, r.type, r.name
		FROM access_tokens t JOIN roles r ON r.id = t.role_id
		WHERE t.token_hash = ? AND t.revoked_at IS NULL`, tokenHash).
		Scan(&o.RoleID, &o.EventID, &o.RoleType, &o.RoleName)
	if errors.Is(err, sql.ErrNoRows) {
		// database/sql reports a context cancelled before the row was read
		// as the context's error, not ErrNoRows; checking ctx as well keeps
		// a miss that raced with a cancellation from being trusted.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return TokenOwner{}, domain.Event{}, 0, ctxErr
		}
		return TokenOwner{}, domain.Event{}, 0, ErrInvalidToken
	}
	if err != nil {
		return TokenOwner{}, domain.Event{}, 0, err
	}
	e, v, err := loadEvent(ctx, tx, o.EventID)
	if err != nil {
		return TokenOwner{}, domain.Event{}, 0, err
	}
	if e.Ended(now) {
		return TokenOwner{}, domain.Event{}, 0, ErrEventEnded
	}
	o.Actor = Actor{kind: actorRole, roleID: o.RoleID, eventID: o.EventID, cred: credential(tokenHash)}
	return o, e, v, nil
}

// LookupRecoveryCode proves a recovery code and returns the actor that may
// use it: bound to that code's event, and the only way to obtain one.
func (s *Store) LookupRecoveryCode(ctx context.Context, codeHash []byte) (Actor, int64, error) {
	var eventID int64
	err := s.r.QueryRowContext(ctx, `SELECT id FROM events WHERE recovery_code_hash = ?`, codeHash).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, 0, ErrInvalidRecoveryCode
	}
	if err != nil {
		return Actor{}, 0, err
	}
	return Actor{kind: actorRecoveryCode, eventID: eventID, cred: credential(codeHash)}, eventID, nil
}

// credential keeps its own copy, so the caller changing the slice later
// does not change the actor.
func credential(h []byte) *[]byte {
	c := slices.Clone(h)
	return &c
}

func roleOf(ctx context.Context, tx *sql.Tx, roleID int64) (int64, domain.RoleType, error) {
	var (
		eventID int64
		typ     domain.RoleType
	)
	err := tx.QueryRowContext(ctx, `SELECT event_id, type FROM roles WHERE id = ?`, roleID).Scan(&eventID, &typ)
	return eventID, typ, notFound(err)
}
