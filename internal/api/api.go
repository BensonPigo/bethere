// Package api serves the token-authenticated JSON API of api.md:
// GET /api/snapshot, the Master's writes POST /api/cues/advance (which
// also starts the event on its day, ADR-0020), /api/cues/undo and
// /api/cues/shift, and the read-only POST /api/cues/shift/preview.
//
// Every request runs in the order of api.md section 7: parse the
// Authorization header, resolve the token, check the capability table
// (section 4), parse the body, write, and read the caller's snapshot
// again. Only a confirmed "no such token" or "event ended" becomes a
// result of "invalid" or "ended"; every other failure is an error without
// a result, which a client never treats as a reason to clear its data.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// Store is what the API needs from internal/store.
type Store interface {
	LookupToken(ctx context.Context, tokenHash []byte, now time.Time) (store.TokenOwner, error)
	Snapshot(ctx context.Context, tokenHash []byte, now time.Time) (store.SnapshotData, error)
	Advance(ctx context.Context, eventID, expectVersion int64, actor store.Actor, now time.Time) (int64, domain.Op, error)
	ApplyCueOp(ctx context.Context, eventID, expectVersion int64, actor store.Actor, now time.Time,
		build func([]domain.Cue) (domain.Op, error)) (int64, domain.Op, error)
	UndoLastCueOp(ctx context.Context, eventID, expectVersion int64, actor store.Actor, now time.Time) (int64, domain.Op, error)
	SetCheckIn(ctx context.Context, eventID, roleID int64, checkedIn bool, actor store.Actor, now time.Time) error
	Skip(ctx context.Context, eventID, expectVersion, cueID int64, actor store.Actor, now time.Time) (int64, domain.Op, error)
	InsertCue(ctx context.Context, eventID, expectVersion int64, nc store.NewCue, actor store.Actor, now time.Time) (int64, error)
	SetCueRoles(ctx context.Context, eventID, expectVersion, cueID int64, roles []store.Assignment, actor store.Actor, now time.Time) error
	EditCue(ctx context.Context, eventID, expectVersion, cueID int64, title string, notePublic *string, actor store.Actor, now time.Time) error
	ReissueLink(ctx context.Context, eventID, roleID int64, tokenHash []byte, actor store.Actor, now time.Time) error
	RevokeLink(ctx context.Context, eventID, roleID int64, actor store.Actor, now time.Time) error
}

const (
	// MaxBodyBytes is the request body limit (api.md section 1).
	MaxBodyBytes = 16 << 10
	// ReadTimeout bounds the database reads of a lookup or snapshot (tech-stack.md 2.1「逾時與錯誤」).
	ReadTimeout = 3 * time.Second
)

// Config wires the API. Now is the clock; serve passes time.Now and
// nothing else (api.md section 1).
type Config struct {
	Store  Store
	Now    func() time.Time
	Logger *slog.Logger
}

// API holds the handlers and the in-memory lastSeenAt of every token
// (tech-stack.md 2.1「lastSeenAt」).
type API struct {
	store  Store
	now    func() time.Time
	logger *slog.Logger

	mu sync.Mutex
	// lastSeen is keyed by access_tokens.id, never the hash: the Master's
	// snapshot asks only for each role's active link, so a revoked link's
	// last use never shows as its replacement's (api.md section 7 item 5).
	lastSeen map[int64]time.Time
}

func New(cfg Config) *API {
	if cfg.Store == nil || cfg.Now == nil || cfg.Logger == nil {
		panic("api: Store, Now and Logger are required")
	}
	return &API{store: cfg.Store, now: cfg.Now, logger: cfg.Logger, lastSeen: map[int64]time.Time{}}
}

// Register adds the endpoints to mux (api.md section 3).
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/snapshot", a.getSnapshot)
	mux.HandleFunc("POST /api/cues/advance", a.write(capAdvance, versionOnly, a.advance))
	mux.HandleFunc("POST /api/cues/undo", a.write(capUndo, versionOnly, a.undo))
	mux.HandleFunc("POST /api/cues/shift", a.write(capShift, shiftFields(true), a.shift))
	mux.HandleFunc("POST /api/cues/shift/preview", a.previewShift)
	mux.HandleFunc("POST /api/checkin", a.write(capCheckIn, ownCheckInFields, a.checkIn))
	mux.HandleFunc("POST /api/roles/checkin", a.write(capProxyCheckIn, proxyCheckInFields, a.proxyCheckIn))
	mux.HandleFunc("POST /api/cues/skip", a.write(capEditRundown, skipFields, a.skip))
	mux.HandleFunc("POST /api/cues/insert", a.write(capEditRundown, insertFields, a.insert))
	mux.HandleFunc("POST /api/cues/roles", a.write(capEditRundown, assignFields, a.assign))
	mux.HandleFunc("POST /api/cues/edit", a.write(capEditRundown, editFields, a.edit))
	mux.HandleFunc("POST /api/roles/reissue", a.write(capLinks, linkFields, a.reissue))
	mux.HandleFunc("POST /api/roles/revoke", a.write(capLinks, linkFields, a.revoke))
}

// LastSeen returns when the link with this access_tokens.id last passed
// authentication; 0 (no link) is never seen.
func (a *API) LastSeen(tokenID int64) (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.lastSeen[tokenID]
	return t, ok && tokenID != 0
}

// seen records a successful authentication (tech-stack.md 2.1「lastSeenAt」): called only after the
// store confirmed the token and its event.
func (a *API) seen(o store.TokenOwner, at time.Time) {
	a.mu.Lock()
	a.lastSeen[o.TokenID] = at
	a.mu.Unlock()
}

// capability is one row of the table in api.md section 4.
type capability int

const (
	capReadSnapshot capability = iota
	capAdvance
	capUndo
	capShift // the shift and its preview
	capCheckIn
	capProxyCheckIn
	capEditRundown // skip, insert, assign, edit
	capLinks       // reissue, revoke
)

// allowed is the table of api.md section 4. It lists what is allowed;
// every other combination, including any role type not opened yet (P1
// types, anything unknown), is denied.
func allowed(c capability, t domain.RoleType) bool {
	switch c {
	case capReadSnapshot:
		return t == domain.Master || t.HasOwnCues() || t == domain.Host
	case capCheckIn:
		return t.HasOwnCues()
	case capAdvance, capUndo, capShift, capProxyCheckIn, capEditRundown, capLinks:
		return t == domain.Master
	}
	return false
}

func (a *API) getSnapshot(w http.ResponseWriter, r *http.Request) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	// The lookup and the snapshot are one read (api.md section 7); the capability check
	// needs the role type, so it follows the read and comes before
	// anything is shaped or sent.
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	now := a.now()
	d, err := a.store.Snapshot(ctx, hash, now)
	if err != nil {
		a.lookupFailed(w, r, err)
		return
	}
	a.seen(d.Owner, now)
	if !allowed(capReadSnapshot, d.Owner.RoleType) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	snap, err := a.buildSnapshot(d, now)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okResponse{Result: "ok", Snapshot: snap})
}

// writeFn performs one write as owner with the client's version and body.
// A reissue returns the new link's token, sent once with the snapshot;
// every other write returns "".
type writeFn func(ctx context.Context, owner store.TokenOwner, b body, now time.Time) (token string, err error)

func (a *API) write(c capability, fields fieldSet, fn writeFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := bearerHash(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request")
			return
		}
		lookupCtx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
		now := a.now()
		owner, err := a.store.LookupToken(lookupCtx, hash, now)
		cancel()
		if err != nil {
			a.lookupFailed(w, r, err)
			return
		}
		a.seen(owner, now)
		if !allowed(c, owner.RoleType) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		b, ok := readBody(w, r, fields)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request")
			return
		}
		token, err := fn(r.Context(), owner, b, a.now())
		switch {
		case err == nil:
			a.respondSnapshot(w, r, hash, http.StatusOK, "", token)
		case errors.Is(err, store.ErrVersionConflict):
			a.respondSnapshot(w, r, hash, http.StatusConflict, "version_conflict", "")
		default:
			a.writeFailed(w, r, err)
		}
	}
}

func (a *API) advance(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	_, _, err := a.store.Advance(ctx, o.EventID, b.version, o.Actor, now)
	return "", err
}

func (a *API) undo(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	_, _, err := a.store.UndoLastCueOp(ctx, o.EventID, b.version, o.Actor, now)
	return "", err
}

// checkIn is a role's own check-in: the role comes from the token alone
// (access-control.md section 5, rule 3).
func (a *API) checkIn(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	return "", a.store.SetCheckIn(ctx, o.EventID, o.RoleID, b.checkedIn, o.Actor, now)
}

// proxyCheckIn is the Master checking a role in or out (US-M-1.8).
func (a *API) proxyCheckIn(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	return "", a.store.SetCheckIn(ctx, o.EventID, b.roleID, b.checkedIn, o.Actor, now)
}

func (a *API) skip(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	_, _, err := a.store.Skip(ctx, o.EventID, b.version, b.cueID, o.Actor, now)
	return "", err
}

func (a *API) insert(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	_, err := a.store.InsertCue(ctx, o.EventID, b.version, store.NewCue{
		AfterCueID: b.afterCueID, Start: b.start, DurationMin: b.durationMin,
		Title: b.title, NotePublic: b.notePublic, Roles: b.roles,
	}, o.Actor, now)
	return "", err
}

func (a *API) assign(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	return "", a.store.SetCueRoles(ctx, o.EventID, b.version, b.cueID, b.roles, o.Actor, now)
}

func (a *API) edit(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	return "", a.store.EditCue(ctx, o.EventID, b.version, b.cueID, b.title, b.notePublic, o.Actor, now)
}

// reissue gives a role a new link; its token is in this response only
// (US-M-7.1). If reading the snapshot afterwards fails, the token is lost
// and the Master reissues again; the old link is gone either way.
func (a *API) reissue(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	token, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	if err := a.store.ReissueLink(ctx, o.EventID, b.roleID, auth.HashToken(token), o.Actor, now); err != nil {
		return "", err
	}
	return token, nil
}

func (a *API) revoke(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	return "", a.store.RevokeLink(ctx, o.EventID, b.roleID, o.Actor, now)
}

// respondSnapshot reads the caller's snapshot again after a write or a
// version conflict (api.md section 7 item 4) through the same function as
// GET. Only an "ok" read is sent with the write's status; "invalid" or
// "ended" is sent as that result instead, and a failed read as 500, so a
// response never carries both an error and a result.
func (a *API) respondSnapshot(w http.ResponseWriter, r *http.Request, hash []byte, status int, code, token string) {
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	now := a.now()
	d, err := a.store.Snapshot(ctx, hash, now)
	if err != nil {
		a.lookupFailed(w, r, err)
		return
	}
	// The caller passed a write capability moments ago; its role type
	// cannot change, but the snapshot is still only ever its own.
	if !allowed(capReadSnapshot, d.Owner.RoleType) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	snap, err := a.buildSnapshot(d, now)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if code == "" && token != "" {
		writeJSON(w, status, reissueResponse{Result: "ok", Snapshot: snap, Token: token})
		return
	}
	if code == "" {
		writeJSON(w, status, okResponse{Result: "ok", Snapshot: snap})
		return
	}
	writeJSON(w, status, conflictResponse{Error: code, Snapshot: snap})
}

// lookupFailed maps a failed token lookup (api.md section 6): a confirmed
// miss is "invalid", an ended event "ended", anything else 500.
func (a *API) lookupFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidToken):
		writeJSON(w, http.StatusOK, resultResponse{Result: "invalid"})
	case errors.Is(err, store.ErrEventEnded):
		writeJSON(w, http.StatusOK, resultResponse{Result: "ended"})
	default:
		a.internal(w, r, err)
	}
}

// domainErrors are the rule refusals of api.md section 6, sent as 422.
var domainErrors = []struct {
	err  error
	code string
}{
	{domain.ErrNotDraft, "not_draft"},
	{domain.ErrNotLive, "not_live"},
	{domain.ErrNotEventDay, "not_event_day"},
	{domain.ErrNothingToAdvance, "nothing_to_advance"},
	{store.ErrNothingToUndo, "nothing_to_undo"},
	{domain.ErrUndoStale, "undo_stale"},
	{domain.ErrShiftTooEarly, "shift_too_early"},
	{domain.ErrInvalidDelta, "invalid_delta"},
	{store.ErrNotCheckInRole, "not_checkin_role"},
	{domain.ErrNotPending, "not_pending"},
	{store.ErrInvalidPosition, "invalid_position"},
	{store.ErrInvalidCue, "invalid_cue"},
	{store.ErrInvalidAssignment, "invalid_assignment"},
	{store.ErrMasterRole, "master_role"},
}

// writeFailed maps a failed write (api.md section 6). The writes of M1
// touch only the caller's own event, so domain.ErrEnded is that event
// ending. ErrNoActor, ErrCrossEvent, ErrNotFound, ErrUnknownOp,
// ErrMultipleInProgress, database errors and timeouts all fall to 500.
func (a *API) writeFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidToken):
		writeJSON(w, http.StatusOK, resultResponse{Result: "invalid"})
		return
	case errors.Is(err, domain.ErrEnded), errors.Is(err, store.ErrEventEnded):
		writeJSON(w, http.StatusOK, resultResponse{Result: "ended"})
		return
	}
	for _, d := range domainErrors {
		if errors.Is(err, d.err) {
			writeError(w, http.StatusUnprocessableEntity, d.code)
			return
		}
	}
	// A cue id from the request that is not in the caller's event, another
	// event's included: the same 404 as one that does not exist, without a
	// result (api.md section 6). store.ErrNotFound stays a 500.
	if errors.Is(err, domain.ErrCueNotFound) || errors.Is(err, store.ErrRoleNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.internal(w, r, err)
}

// internal logs the error with the route only, never headers, bodies,
// tokens or hashes (tech-stack.md 2.1「日誌」), and answers a fixed code.
func (a *API) internal(w http.ResponseWriter, r *http.Request, err error) {
	a.logger.Error("api_error", "method", r.Method, "path", r.URL.Path, "err", err.Error())
	writeError(w, http.StatusInternalServerError, "internal")
}

// bearerHash reads exactly one "Authorization: Bearer <token>" header with
// a well-formed token and returns the token's hash. Anything else is a
// malformed request (400), never an invalid link.
func bearerHash(r *http.Request) ([]byte, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return nil, false
	}
	const scheme = "Bearer "
	v := values[0]
	if len(v) != len(scheme)+auth.TokenLen || v[:len(scheme)] != scheme {
		return nil, false
	}
	token := v[len(scheme):]
	if auth.CheckToken(token) != nil {
		return nil, false
	}
	return auth.HashToken(token), true
}
