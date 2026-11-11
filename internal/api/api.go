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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// Store is what the API needs from internal/store.
type Store interface {
	Healthy(ctx context.Context) error
	LookupToken(ctx context.Context, tokenHash []byte, now time.Time) (store.TokenOwner, error)
	Snapshot(ctx context.Context, tokenHash []byte, now time.Time) (store.SnapshotData, error)
	// PollSnapshot is Snapshot for GET /api/snapshot, with the event data
	// taken from memory while the data generation is unchanged (ADR-0041).
	PollSnapshot(ctx context.Context, tokenHash []byte, now time.Time) (store.SnapshotData, error)
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
	// The preparation (M2c).
	UpdateCue(ctx context.Context, eventID, expectVersion int64, u store.CueUpdate, actor store.Actor, now time.Time) error
	MoveCue(ctx context.Context, eventID, expectVersion, cueID, afterCueID int64, actor store.Actor, now time.Time) error
	DeleteCue(ctx context.Context, eventID, expectVersion, cueID int64, actor store.Actor, now time.Time) error
	ReplaceRundown(ctx context.Context, eventID, expectVersion int64, rows []store.ImportRow, newRoles []store.NewRole,
		actor store.Actor, now time.Time) error
	CreateRole(ctx context.Context, eventID int64, nr store.NewRole, tokenHash []byte, actor store.Actor, now time.Time) (int64, error)
	RenameRole(ctx context.Context, eventID, roleID int64, name string, actor store.Actor, now time.Time) error
	DeleteRole(ctx context.Context, eventID, expectVersion, roleID int64, actor store.Actor, now time.Time) error
	PreviewSnapshot(ctx context.Context, tokenHash []byte, roleID int64, now time.Time) (store.TokenOwner, store.SnapshotData, error)
	// The four jobs (api.md section 9).
	SetAck(ctx context.Context, eventID, cueID, roleID int64, acked bool, actor store.Actor, now time.Time) error
	AddTask(ctx context.Context, eventID int64, title string, actor store.Actor, now time.Time) (int64, error)
	SetTaskState(ctx context.Context, eventID, taskID int64, state string, asMaster bool, actor store.Actor, now time.Time) error
	DeleteTask(ctx context.Context, eventID, taskID int64, actor store.Actor, now time.Time) error
	ReplaceGuests(ctx context.Context, eventID int64, rows []store.GuestRow, actor store.Actor, now time.Time) error
	SetArrived(ctx context.Context, eventID, guestID int64, arrived bool, actor store.Actor, now time.Time) error
	ReplaceChecklist(ctx context.Context, eventID int64, titles []string, actor store.Actor, now time.Time) error
	CheckItem(ctx context.Context, eventID, itemID int64, done bool, actor store.Actor, now time.Time) error
	// The recovery code (access-control.md section 2).
	ReplaceMasterLink(ctx context.Context, codeHash, tokenHash []byte, now time.Time) error
	// An invited event (US-M-17).
	CreateEvent(ctx context.Context, ne store.NewEvent, now time.Time) (int64, error)
	// The admin page (US-O-9, US-O-10).
	CheckAdmin(ctx context.Context, tokenHash []byte) error
	AdminOverview(ctx context.Context, adminHash []byte, now time.Time) (store.AdminOverview, error)
	CreateAdminInvite(ctx context.Context, adminHash, codeHash []byte, now time.Time) (int64, error)
	// Wedding planner companies (api.md section 10).
	CreateOrganization(ctx context.Context, adminHash []byte, name, email string, now time.Time) (int64, error)
	MemberByEmail(ctx context.Context, email string) (int64, error)
	ConfirmLoginMail(ctx context.Context, mailHash, sessionHash []byte, now time.Time) error
	Logout(ctx context.Context, sessionHash []byte) error
	CheckSession(ctx context.Context, sessionHash []byte, now time.Time) error
	Company(ctx context.Context, sessionHash []byte, now time.Time) (store.Company, error)
	AddMember(ctx context.Context, sessionHash []byte, email string, now time.Time) (by, added int64, err error)
	RemoveMember(ctx context.Context, sessionHash []byte, memberID int64, now time.Time) (by int64, err error)
	CreateCompanyEvent(ctx context.Context, sessionHash []byte, ne store.NewEvent, now time.Time) (int64, error)
	OpenConsole(ctx context.Context, sessionHash []byte, eventID int64, consoleHash []byte, now time.Time) error
	MembersClosed() bool
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
	// Maintenance lets only the Master through (Gate, US-O-2).
	Maintenance bool
	// LoginMail takes a member's number for a login mail; nil is no usable
	// mail setup, and the login answers mail_unavailable (US-CO-1.12).
	LoginMail Enqueuer
}

// Enqueuer is the login mail queue (loginmail.Queue).
type Enqueuer interface {
	Enqueue(memberID int64)
}

// API holds the handlers and the in-memory lastSeenAt of every token
// (tech-stack.md 2.1「lastSeenAt」).
type API struct {
	store       Store
	now         func() time.Time
	logger      *slog.Logger
	maintenance bool
	loginMail   Enqueuer
	routes      []string

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
	return &API{store: cfg.Store, now: cfg.Now, logger: cfg.Logger, maintenance: cfg.Maintenance, loginMail: cfg.LoginMail,
		lastSeen: map[int64]time.Time{}}
}

// Register adds the endpoints to mux (api.md section 3).
func (a *API) Register(mux *http.ServeMux) {
	handle := func(pattern string, h http.HandlerFunc) {
		a.routes = append(a.routes, pattern)
		mux.HandleFunc(pattern, h)
	}
	handle("GET /api/snapshot", a.getSnapshot)
	handle("POST /api/cues/advance", a.write(capAdvance, versionOnly, a.advance))
	handle("POST /api/cues/undo", a.write(capUndo, versionOnly, a.undo))
	handle("POST /api/cues/shift", a.write(capShift, shiftFields(true), a.shift))
	handle("POST /api/cues/shift/preview", a.previewShift)
	handle("POST /api/checkin", a.write(capCheckIn, ownCheckInFields, a.checkIn))
	handle("POST /api/roles/checkin", a.write(capProxyCheckIn, proxyCheckInFields, a.proxyCheckIn))
	handle("POST /api/cues/skip", a.write(capEditRundown, skipFields, a.skip))
	handle("POST /api/cues/insert", a.write(capEditRundown, insertFields, a.insert))
	handle("POST /api/cues/roles", a.write(capEditRundown, assignFields, a.assign))
	handle("POST /api/cues/edit", a.write(capEditRundown, editFields, a.edit))
	handle("POST /api/roles/reissue", a.write(capLinks, reissueFields, a.reissue))
	handle("POST /api/roles/revoke", a.write(capLinks, linkFields, a.revoke))
	// The preparation (M2c).
	handle("POST /api/cues/update", a.write(capEditRundown, updateFields, a.updateCue))
	handle("POST /api/cues/move", a.write(capEditRundown, moveFields, a.moveCue))
	handle("POST /api/cues/delete", a.write(capEditRundown, deleteCueFields, a.deleteCue))
	handle("POST /api/rundown/import/preview", a.previewImport)
	handle("POST /api/rundown/import", a.write(capEditRundown, importFields, a.importRundown))
	handle("POST /api/roles/add", a.write(capLinks, addRoleFields, a.addRole))
	handle("POST /api/roles/rename", a.write(capLinks, renameFields, a.renameRole))
	handle("POST /api/roles/delete", a.write(capLinks, deleteRoleFields, a.deleteRole))
	handle("POST /api/roles/preview", a.previewRole)
	// The four jobs (api.md section 9).
	handle("POST /api/cues/ack", a.write(capAck, ackFields, a.ack))
	handle("POST /api/tasks/add", a.write(capAddTask, addTaskFields, a.addTask))
	handle("POST /api/tasks/set", a.write(capSetTask, setTaskFields, a.setTask))
	handle("POST /api/tasks/delete", a.write(capManageJobs, deleteTaskFields, a.deleteTask))
	handle("POST /api/guests/import", a.write(capManageJobs, guestImportFields, a.importGuests))
	handle("POST /api/guests/arrive", a.write(capArrive, arriveFields, a.arrive))
	handle("POST /api/checklist/items", a.write(capManageJobs, checklistFields, a.replaceChecklist))
	handle("POST /api/checklist/check", a.write(capCheckItem, checkItemFields, a.checkItem))
	// The recovery code, without a token (US-O-3).
	handle("POST /api/recover/master", a.recoverMaster)
	// An invited event, without a token (US-M-17).
	handle("POST /api/events", a.createEvent)
	// The admin page, with the admin link only (US-O-9, US-O-10).
	handle("GET /api/admin/overview", a.adminOverview)
	handle("POST /api/admin/invites", a.adminInvite)
	handle("POST /api/admin/organizations", a.adminOrganization)
	// The company page, with a member's login link (api.md section 10).
	handle("POST /api/company/login", a.login)
	handle("POST /api/company/login/confirm", a.confirmLogin)
	handle("POST /api/company/logout", a.logout)
	handle("GET /api/company", a.getCompany)
	handle("POST /api/company/members/add", a.companyChange(emailFields, a.addMember))
	handle("POST /api/company/members/remove", a.companyChange(removeMemberFields, a.removeMember))
	handle("POST /api/company/events", a.companyEvent)
	handle("POST /api/company/events/open", a.openConsole)
}

// Routes lists the patterns Register added, so tests can try every one.
func (a *API) Routes() []string { return slices.Clone(a.routes) }

// LastSeen returns when the link with this access_tokens.id last passed
// authentication; 0 (no link) is never seen.
func (a *API) LastSeen(tokenID int64) (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.lastSeen[tokenID]
	return t, ok && tokenID != 0
}

// seen records a successful authentication (tech-stack.md 2.1「lastSeenAt」): called only after the
// store confirmed the token and its event. A member's console link has no
// access_tokens.id (TokenID 0), which LastSeen never reports (api.md 10.2).
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
	capEditRundown // skip, insert, assign, edit; M2c: update, move, delete, the import and its preview
	capLinks       // reissue, revoke; M2c: add, rename, delete and preview a role
	// The four jobs (api.md section 9): each listed type by type, never
	// derived from HasOwnCues.
	capAck        // a venue lead's ack
	capAddTask    // the Master's task, or the reception calling a runner
	capSetTask    // claim, release, finish; the Master any state
	capManageJobs // delete a task, import guests, set the checklist
	capArrive     // a guest's arrival
	capCheckItem  // a checklist item
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
	case capAdvance, capUndo, capShift, capProxyCheckIn, capEditRundown, capLinks, capManageJobs:
		return t == domain.Master
	case capAck:
		return t == domain.VenueLead
	case capAddTask, capArrive:
		return t == domain.Master || t == domain.Reception
	case capSetTask:
		return t == domain.Master || t == domain.Runner
	case capCheckItem:
		return t == domain.Master || t == domain.Cashier
	}
	return false
}

func (a *API) getSnapshot(w http.ResponseWriter, r *http.Request) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	d, now, ok := a.poll(w, r, hash)
	if !ok {
		return
	}
	a.sendSnapshot(w, r, d, now)
}

// poll reads the caller's snapshot data and checks the capability; on
// false the answer has been written. The lookup and the snapshot are one
// read (api.md section 7); the capability check needs the role type, so
// it follows the read and comes before anything is shaped or sent.
func (a *API) poll(w http.ResponseWriter, r *http.Request, hash []byte) (store.SnapshotData, time.Time, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	now := a.now()
	d, err := a.store.PollSnapshot(ctx, hash, now)
	if err != nil {
		a.lookupFailed(w, r, err)
		return d, now, false
	}
	a.seen(d.Owner, now)
	if !allowed(capReadSnapshot, d.Owner.RoleType) {
		writeError(w, http.StatusForbidden, "forbidden")
		return d, now, false
	}
	return d, now, true
}

// sendSnapshot shapes the caller's own snapshot and sends it.
func (a *API) sendSnapshot(w http.ResponseWriter, r *http.Request, d store.SnapshotData, now time.Time) {
	snap, err := a.buildSnapshot(d, now)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	body, err := json.Marshal(okResponse{Result: "ok", Snapshot: snap})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeTagged(w, r, body, now)
}

// writeTagged sends body with its ETag, or 304 when the caller has it. The
// ETag is compared only now, after the token, the capability and the
// caller's own snapshot: a 304 never skips a check and only ever means
// "the same as what you got last" (api.md 5.7, ADR-0023).
func writeTagged(w http.ResponseWriter, r *http.Request, body []byte, now time.Time) {
	serverTime := FormatTime(now)
	tag := etag(body, serverTime)
	w.Header().Set("ETag", tag)
	w.Header().Set("X-Server-Time", serverTime)
	if inm := r.Header.Values("If-None-Match"); len(inm) == 1 && inm[0] == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(append(body, '\n'))
}

// etag is the strong ETag of an "ok" snapshot response: the base64url
// SHA-256 of its JSON with serverTime set to "" (api.md 5.7). The shapes
// are structs, so the key order is fixed and the same content always has
// the same tag. Only a single exact match is a 304; "*", lists, repeated
// headers and weak tags never equal it.
func etag(body []byte, serverTime string) string {
	unstamped := bytes.Replace(body, []byte(`"serverTime":"`+serverTime+`"`), []byte(`"serverTime":""`), 1)
	sum := sha256.Sum256(unstamped)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
}

// writeFn performs one write as owner with the client's version and body.
// A reissue or a new role returns the new link, sent once with the
// snapshot; every other write returns the zero link.
type writeFn func(ctx context.Context, owner store.TokenOwner, b body, now time.Time) (link, error)

// link is a new link of a role: its token, its QR code when an origin was
// given, and the role's id when the role is new (api.md section 3).
type link struct {
	token  string
	qr     string
	roleID int64
}

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
		l, err := fn(r.Context(), owner, b, a.now())
		switch {
		case err == nil:
			a.respondSnapshot(w, r, hash, http.StatusOK, "", l)
		case errors.Is(err, store.ErrVersionConflict):
			a.respondSnapshot(w, r, hash, http.StatusConflict, "version_conflict", link{})
		case errors.Is(err, errBadOrigin):
			writeError(w, http.StatusBadRequest, "bad_request")
		default:
			a.writeFailed(w, r, err)
		}
	}
}

func (a *API) advance(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	_, _, err := a.store.Advance(ctx, o.EventID, b.version, o.Actor, now)
	return link{}, err
}

func (a *API) undo(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	_, _, err := a.store.UndoLastCueOp(ctx, o.EventID, b.version, o.Actor, now)
	return link{}, err
}

// checkIn is a role's own check-in: the role comes from the token alone
// (access-control.md section 5, rule 3).
func (a *API) checkIn(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.SetCheckIn(ctx, o.EventID, o.RoleID, b.checkedIn, o.Actor, now)
}

// proxyCheckIn is the Master checking a role in or out (US-M-1.8).
func (a *API) proxyCheckIn(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.SetCheckIn(ctx, o.EventID, b.roleID, b.checkedIn, o.Actor, now)
}

func (a *API) skip(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	_, _, err := a.store.Skip(ctx, o.EventID, b.version, b.cueID, o.Actor, now)
	return link{}, err
}

func (a *API) insert(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	_, err := a.store.InsertCue(ctx, o.EventID, b.version, store.NewCue{
		AfterCueID: b.afterCueID, Start: b.start, DurationMin: b.durationMin,
		Title: b.title, NotePublic: b.notePublic, Roles: b.roles,
	}, o.Actor, now)
	return link{}, err
}

func (a *API) assign(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.SetCueRoles(ctx, o.EventID, b.version, b.cueID, b.roles, o.Actor, now)
}

func (a *API) edit(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.EditCue(ctx, o.EventID, b.version, b.cueID, b.title, b.notePublic, o.Actor, now)
}

// reissue gives a role a new link; its token, and its QR code when the
// client sent its origin (M2c), are in this response only (US-M-7.1,
// US-M-13.1). If reading the snapshot afterwards fails, the token is lost
// and the Master reissues again; the old link is gone either way.
func (a *API) reissue(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	token, err := auth.NewToken()
	if err != nil {
		return link{}, err
	}
	qr, err := linkQR(b.origin, b.originGiven, token)
	if err != nil {
		return link{}, err
	}
	if err := a.store.ReissueLink(ctx, o.EventID, b.roleID, auth.HashToken(token), o.Actor, now); err != nil {
		return link{}, err
	}
	return link{token: token, qr: qr}, nil
}

func (a *API) revoke(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.RevokeLink(ctx, o.EventID, b.roleID, o.Actor, now)
}

// respondSnapshot reads the caller's snapshot again after a write or a
// version conflict (api.md section 7 item 4) through the same function as
// GET. Only an "ok" read is sent with the write's status; "invalid" or
// "ended" is sent as that result instead, and a failed read as 500, so a
// response never carries both an error and a result.
func (a *API) respondSnapshot(w http.ResponseWriter, r *http.Request, hash []byte, status int, code string, l link) {
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
	if code == "" && l.token != "" {
		res := linkResponse{Result: "ok", Snapshot: snap, Token: l.token}
		if l.roleID != 0 {
			res.RoleID = &l.roleID
		}
		if l.qr != "" {
			res.QR = &l.qr
		}
		writeJSON(w, status, res)
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
	case errors.Is(err, store.ErrMembersClosed):
		// A console link while the reset after a restore is not done:
		// offline to the client, never "invalid" (US-O-12.6).
		writeError(w, http.StatusServiceUnavailable, "maintenance")
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
	{store.ErrInvalidRole, "invalid_role"},
	{store.ErrDuplicateName, "duplicate_name"},
	{store.ErrTooManyRoles, "too_many_roles"},
	{store.ErrInvalidImport, "invalid_import"},
	{store.ErrLockedOnEventDay, "locked_on_event_day"},
	{store.ErrNotAssigned, "not_assigned"},
	{store.ErrTaskTaken, "task_taken"},
	{store.ErrInvalidTaskState, "invalid_task_state"},
	{store.ErrTooManyTasks, "too_many_tasks"},
	{store.ErrInvalidTask, "invalid_task"},
	{store.ErrGuestsArrived, "guests_arrived"},
	{store.ErrInvalidChecklist, "invalid_checklist"},
	{store.ErrChecklistStarted, "checklist_started"},
}

// writeFailed maps a failed write (api.md section 6). The writes of M1
// touch only the caller's own event, so domain.ErrEnded is that event
// ending. ErrNoActor, ErrCrossEvent, ErrNotFound, ErrUnknownOp,
// ErrMultipleInProgress, database errors and timeouts all fall to 500.
func (a *API) writeFailed(w http.ResponseWriter, r *http.Request, err error) {
	if writeResult(w, err) {
		return
	}
	for _, d := range domainErrors {
		if errors.Is(err, d.err) {
			writeError(w, http.StatusUnprocessableEntity, d.code)
			return
		}
	}
	if missingID(err) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.internal(w, r, err)
}

// writeResult answers a write that found its credential invalid, its
// event ended or member links closed (US-O-12.6), and reports whether it
// did.
func writeResult(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrInvalidToken):
		writeJSON(w, http.StatusOK, resultResponse{Result: "invalid"})
	case errors.Is(err, domain.ErrEnded), errors.Is(err, store.ErrEventEnded):
		writeJSON(w, http.StatusOK, resultResponse{Result: "ended"})
	case errors.Is(err, store.ErrMembersClosed):
		writeError(w, http.StatusServiceUnavailable, "maintenance")
	default:
		return false
	}
	return true
}

// missingID is an id from the request that is not in the caller's event,
// another event's included: the same 404 as one that does not exist,
// without a result (api.md section 6). store.ErrNotFound stays a 500.
func missingID(err error) bool {
	return errors.Is(err, domain.ErrCueNotFound) || errors.Is(err, store.ErrRoleNotFound) || errors.Is(err, store.ErrItemNotFound)
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
