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
	ErrRoleView            = errors.New("this role type cannot use this screen")
)

// AddRole adds a non-Master role to an event with its type's default
// screen.
func (s *Store) AddRole(ctx context.Context, eventID int64, name string, typ domain.RoleType, actor Actor, now time.Time) (int64, error) {
	return s.AddRoleWithView(ctx, eventID, name, typ, "", actor, now)
}

// AddRoleWithView adds a non-Master role that opens the given screen; an
// empty view means the type's default (ADR-0025). Types and views are
// checked here, not by the schema (ADR-0016).
func (s *Store) AddRoleWithView(ctx context.Context, eventID int64, name string, typ domain.RoleType, view domain.View,
	actor Actor, now time.Time) (int64, error) {
	if typ == domain.Master {
		return 0, ErrMasterRole
	}
	if !typ.Valid() {
		return 0, ErrRoleType
	}
	if view != "" && !typ.AllowsView(view) {
		return 0, ErrRoleView
	}
	var roleID int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO roles (event_id, name, type, view) VALUES (?, ?, ?, ?)`,
			eventID, name, typ, sql.NullString{String: string(view), Valid: view != ""})
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

// IssueToken gives a non-Master role a new link, revoking its current one
// in the same transaction (access-control.md 3.1). The Master's link is
// replaced only with ReplaceMasterLink; other permission checks belong to
// the caller.
func (s *Store) IssueToken(ctx context.Context, roleID int64, tokenHash []byte, actor Actor, now time.Time) error {
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
	TokenID  int64 // access_tokens.id, the key of lastSeenAt (api.md section 7 item 5); never the hash
	RoleID   int64
	EventID  int64
	RoleType domain.RoleType
	RoleName string
	View     domain.View // resolved: the stored screen or the type's default
	ChecksIn bool        // resolved: the type checks in and the Master did not choose "no check-in" (M2c)
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
// is and never as ErrInvalidToken (tech-stack.md 2.1「逾時與錯誤」).
func resolveToken(ctx context.Context, tx *sql.Tx, tokenHash []byte, now time.Time) (TokenOwner, domain.Event, int64, error) {
	var (
		o         TokenOwner
		view      sql.NullString
		noCheckIn bool
	)
	err := tx.QueryRowContext(ctx, `SELECT t.id, r.id, r.event_id, r.type, r.name, r.view, r.checks_in IS NOT NULL
		FROM access_tokens t JOIN roles r ON r.id = t.role_id
		WHERE t.token_hash = ? AND t.revoked_at IS NULL`, tokenHash).
		Scan(&o.TokenID, &o.RoleID, &o.EventID, &o.RoleType, &o.RoleName, &view, &noCheckIn)
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
	o.View = resolveView(o.RoleType, view)
	o.ChecksIn = o.RoleType.ChecksIn(noCheckIn)
	e, v, err := loadEvent(ctx, tx, o.EventID)
	if err != nil {
		return TokenOwner{}, domain.Event{}, 0, err
	}
	if e.Ended(now) {
		return TokenOwner{}, domain.Event{}, 0, ErrEventEnded
	}
	o.Actor = Actor{kind: actorRole, roleID: o.RoleID, tokenID: o.TokenID, eventID: o.EventID, cred: credential(tokenHash)}
	return o, e, v, nil
}

// LookupRecoveryCode proves a recovery code and returns the actor that may
// use it: bound to that code's event, and the only way to obtain one.
func (s *Store) LookupRecoveryCode(ctx context.Context, codeHash []byte) (Actor, int64, error) {
	var eventID int64
	err := s.r.QueryRowContext(ctx, `SELECT id FROM events WHERE recovery_code_hash = ?`, codeHash).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		// As in resolveToken: a miss that raced with a cancellation is not trusted.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Actor{}, 0, ctxErr
		}
		return Actor{}, 0, ErrInvalidRecoveryCode
	}
	if err != nil {
		return Actor{}, 0, err
	}
	return Actor{kind: actorRecoveryCode, eventID: eventID, cred: credential(codeHash)}, eventID, nil
}

// ReplaceMasterLink is the recovery code's first use (access-control.md
// section 2, US-O-3.3): the event of that code gets a new Master link and
// its old one stops working, in one transaction. The code is proved
// before the end is checked, so only its holder learns the event ended.
// Like ReissueLink it leaves the event version alone (api.md section 3)
// and logs as token.*. ErrInvalidRecoveryCode only when no event has this
// code.
func (s *Store) ReplaceMasterLink(ctx context.Context, codeHash, tokenHash []byte, now time.Time) error {
	actor, eventID, err := s.LookupRecoveryCode(ctx, codeHash)
	if err != nil {
		return err
	}
	return s.replaceMasterLink(ctx, actor, eventID, tokenHash, now)
}

// replaceMasterLink is the write, with the actor proved before it: the
// code is checked again inside, so one reset in between is refused.
func (s *Store) replaceMasterLink(ctx context.Context, actor Actor, eventID int64, tokenHash []byte, now time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := checkActor(ctx, tx, eventID, actor, actorRecoveryCode); err != nil {
			return err
		}
		e, _, err := loadEvent(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if e.Ended(now) {
			return domain.ErrEnded
		}
		var roleID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM roles WHERE event_id = ? AND type = ?`, eventID, domain.Master).
			Scan(&roleID); err != nil {
			return notFound(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at = ? WHERE role_id = ? AND revoked_at IS NULL`,
			toNS(now), roleID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO access_tokens (role_id, token_hash, created_at) VALUES (?, ?, ?)`,
			roleID, tokenHash, toNS(now)); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, "token.recover", map[string]any{"roleId": roleID}, now)
	})
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

// resolveView is the stored screen, or the type's default when none is
// stored or the stored one does not suit the type (a hand-edited row).
func resolveView(t domain.RoleType, stored sql.NullString) domain.View {
	if v := domain.View(stored.String); stored.Valid && t.AllowsView(v) {
		return v
	}
	return t.DefaultView()
}

// ReissueLink gives a non-Master role of eventID a new link, revoking its
// current one if any, for the Master in the console (US-M-7.1). Unlike
// IssueToken it takes the caller's event and checks that the role is in
// it before anything about the role (api.md section 3, ownership first),
// and it leaves the event version alone: a link is not a cue (ADR-0023).
// The role's own link being revoked already is not an error: it describes
// the target, never the caller.
func (s *Store) ReissueLink(ctx context.Context, eventID, roleID int64, tokenHash []byte, actor Actor, now time.Time) error {
	return s.changeLink(ctx, eventID, roleID, actor, now, "token.reissue", func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO access_tokens (role_id, token_hash, created_at) VALUES (?, ?, ?)`,
			roleID, tokenHash, toNS(now))
		return err
	})
}

// RevokeLink revokes a non-Master role's link (US-M-7.2). A role without
// an active link is revoked already, so it succeeds (api.md section 3).
func (s *Store) RevokeLink(ctx context.Context, eventID, roleID int64, actor Actor, now time.Time) error {
	return s.changeLink(ctx, eventID, roleID, actor, now, "token.revoke", func(*sql.Tx) error { return nil })
}

// changeLink revokes the role's active link and then runs then. The log
// records which link of the Master acted (byTokenId, never a hash), so
// after the Master's link is replaced with the recovery code the changes
// made with a leaked old link can be told apart (access-control.md 3.4).
func (s *Store) changeLink(ctx context.Context, eventID, roleID int64, actor Actor, now time.Time, logType string,
	then func(*sql.Tx) error) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, _, err := guard(ctx, tx, eventID, actor, now); err != nil {
			return err
		}
		typ, err := roleIn(ctx, tx, eventID, roleID)
		if err != nil {
			return err
		}
		if typ == domain.Master {
			return ErrMasterRole
		}
		if _, err := tx.ExecContext(ctx, `UPDATE access_tokens SET revoked_at = ? WHERE role_id = ? AND revoked_at IS NULL`,
			toNS(now), roleID); err != nil {
			return err
		}
		if err := then(tx); err != nil {
			return err
		}
		return logAction(ctx, tx, eventID, actor, logType, map[string]any{"roleId": roleID, "byTokenId": actor.tokenID}, now)
	})
}
