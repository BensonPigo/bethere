// Package api serves the token-authenticated JSON API of api.md (M1):
// GET /api/snapshot and the Master's writes POST /api/event/start,
// /api/cues/advance and /api/cues/undo.
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
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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
	StartEvent(ctx context.Context, eventID, expectVersion int64, actor store.Actor, now time.Time) (int64, error)
	ApplyCueOp(ctx context.Context, eventID, expectVersion int64, actor store.Actor, now time.Time,
		build func([]domain.Cue) (domain.Op, error)) (int64, domain.Op, error)
	UndoLastCueOp(ctx context.Context, eventID, expectVersion int64, actor store.Actor, now time.Time) (int64, domain.Op, error)
}

const (
	// MaxBodyBytes is the request body limit (api.md section 1).
	MaxBodyBytes = 16 << 10
	// ReadTimeout bounds the database reads of a lookup or snapshot (T3b).
	ReadTimeout = 3 * time.Second
)

// Config wires the API. Now is the clock; serve passes time.Now and
// nothing else (A13).
type Config struct {
	Store  Store
	Now    func() time.Time
	Logger *slog.Logger
}

// API holds the handlers and the in-memory lastSeenAt of every token
// (T7).
type API struct {
	store  Store
	now    func() time.Time
	logger *slog.Logger

	mu       sync.Mutex
	lastSeen map[string]time.Time // key: token hash as a string; never logged or exposed in M1
}

func New(cfg Config) *API {
	if cfg.Store == nil || cfg.Now == nil || cfg.Logger == nil {
		panic("api: Store, Now and Logger are required")
	}
	return &API{store: cfg.Store, now: cfg.Now, logger: cfg.Logger, lastSeen: map[string]time.Time{}}
}

// Register adds the M1 endpoints to mux (api.md section 3).
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/snapshot", a.getSnapshot)
	mux.HandleFunc("POST /api/event/start", a.write(capStartEvent, a.startEvent))
	mux.HandleFunc("POST /api/cues/advance", a.write(capAdvance, a.advance))
	mux.HandleFunc("POST /api/cues/undo", a.write(capUndo, a.undo))
}

// LastSeen returns when a token hash last passed authentication, for V4
// in M2; it is not part of any M1 response.
func (a *API) LastSeen(tokenHash []byte) (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.lastSeen[string(tokenHash)]
	return t, ok
}

// seen records a successful authentication (T7): called only after the
// store confirmed the token and its event.
func (a *API) seen(tokenHash []byte, at time.Time) {
	a.mu.Lock()
	a.lastSeen[string(tokenHash)] = at
	a.mu.Unlock()
}

// capability is one row of the table in api.md section 4.
type capability int

const (
	capReadSnapshot capability = iota
	capStartEvent
	capAdvance
	capUndo
)

// allowed is the table of api.md section 4 for M1. It lists what is
// allowed; every other combination, including any role type not opened in
// M1 (Media, StageAV, Host, P1 types, anything unknown), is denied.
func allowed(c capability, t domain.RoleType) bool {
	switch c {
	case capReadSnapshot:
		return t == domain.Master || t == domain.Stylist
	case capStartEvent, capAdvance, capUndo:
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
	// The lookup and the snapshot are one read (A9); the capability check
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
	a.seen(hash, now)
	if !allowed(capReadSnapshot, d.Owner.RoleType) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	snap, err := buildSnapshot(d, now)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okResponse{Result: "ok", Snapshot: snap})
}

// writeFn performs one write as owner with the client's version.
type writeFn func(ctx context.Context, owner store.TokenOwner, version int64, now time.Time) error

func (a *API) write(c capability, fn writeFn) http.HandlerFunc {
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
		a.seen(hash, now)
		if !allowed(c, owner.RoleType) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		version, ok := readVersion(w, r)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request")
			return
		}
		err = fn(r.Context(), owner, version, a.now())
		switch {
		case err == nil:
			a.respondSnapshot(w, r, hash, http.StatusOK, "")
		case errors.Is(err, store.ErrVersionConflict):
			a.respondSnapshot(w, r, hash, http.StatusConflict, "version_conflict")
		default:
			a.writeFailed(w, r, err)
		}
	}
}

func (a *API) startEvent(ctx context.Context, o store.TokenOwner, version int64, now time.Time) error {
	_, err := a.store.StartEvent(ctx, o.EventID, version, o.Actor, now)
	return err
}

func (a *API) advance(ctx context.Context, o store.TokenOwner, version int64, now time.Time) error {
	_, _, err := a.store.ApplyCueOp(ctx, o.EventID, version, o.Actor, now,
		func(cues []domain.Cue) (domain.Op, error) { return domain.Advance(cues, now) })
	return err
}

func (a *API) undo(ctx context.Context, o store.TokenOwner, version int64, now time.Time) error {
	_, _, err := a.store.UndoLastCueOp(ctx, o.EventID, version, o.Actor, now)
	return err
}

// respondSnapshot reads the caller's snapshot again after a write or a
// version conflict (api.md section 7 item 4) through the same function as
// GET. Only an "ok" read is sent with the write's status; "invalid" or
// "ended" is sent as that result instead, and a failed read as 500, so a
// response never carries both an error and a result.
func (a *API) respondSnapshot(w http.ResponseWriter, r *http.Request, hash []byte, status int, code string) {
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
	snap, err := buildSnapshot(d, now)
	if err != nil {
		a.internal(w, r, err)
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
	a.internal(w, r, err)
}

// internal logs the error with the route only, never headers, bodies,
// tokens or hashes (T13), and answers a fixed code.
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

// readVersion reads the body of a write: one JSON object of at most
// MaxBodyBytes with exactly the field "version", an integer of 1 or more
// (api.md section 1, F11).
//
// The body is read token by token rather than decoded into a struct,
// because encoding/json matches field names case-insensitively and keeps
// the last of repeated names: {"VERSION":1} or {"version":"x","version":1}
// must be refused as unknown or malformed, not accepted.
func readVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return 0, false
	}
	var (
		version int64
		found   bool
	)
	for dec.More() {
		key, err := dec.Token()
		if err != nil || key != "version" || found {
			return 0, false
		}
		// A number token; null, strings, booleans, objects and arrays
		// are all refused.
		val, err := dec.Token()
		n, isNumber := val.(json.Number)
		if err != nil || !isNumber {
			return 0, false
		}
		if version, found = parsePositive([]byte(n)); !found {
			return 0, false
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return 0, false
	}
	// Nothing but white space may follow the object.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return 0, false
	}
	return version, found
}

// parsePositive accepts only a JSON integer written as ASCII digits whose
// value is 1 or more and fits in int64, so 1.0, 1e0 and -1 are refused.
func parsePositive(raw []byte) (int64, bool) {
	if len(raw) == 0 || len(raw) > 18 {
		return 0, false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}
