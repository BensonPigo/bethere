package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/rundown"
	"github.com/BensonPigo/bethere/internal/store"
)

// The Master's preparation before the event (api.md section 3, M2c): the
// draft rundown editor, roles added on screen with their QR codes, the
// pasted import and the role preview.

func (a *API) updateCue(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.UpdateCue(ctx, o.EventID, b.version, store.CueUpdate{
		CueID: b.cueID, Start: b.start, DurationMin: b.durationMin, IsFixed: b.isFixed,
		Title: b.title, NotePublic: b.notePublic,
	}, o.Actor, now)
}

func (a *API) moveCue(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.MoveCue(ctx, o.EventID, b.version, b.cueID, b.afterCueID, o.Actor, now)
}

func (a *API) deleteCue(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.DeleteCue(ctx, o.EventID, b.version, b.cueID, o.Actor, now)
}

// importRundown replaces the whole rundown with the pasted text. A text
// that cannot be read at all goes to the store with no rows, so the
// version, draft and event-day checks still come first (api.md section 3).
func (a *API) importRundown(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	rows, err := rundown.ParseText(b.text)
	if err != nil {
		rows = nil
	}
	return link{}, a.store.ReplaceRundown(ctx, o.EventID, b.version, rows, b.newRoles, o.Actor, now)
}

// addRole creates a role with its first link (US-M-11.1, US-M-11.2). The
// token and its QR code exist before anything is written, so a QR code
// that cannot be made writes nothing (api.md section 3).
func (a *API) addRole(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	token, err := auth.NewToken()
	if err != nil {
		return link{}, err
	}
	qr, err := linkQR(b.origin, true, token)
	if err != nil {
		return link{}, err
	}
	id, err := a.store.CreateRole(ctx, o.EventID, store.NewRole{
		Name: b.name, View: domain.View(b.view), ChecksIn: b.checksIn, Job: domain.Job(b.job),
	}, auth.HashToken(token), o.Actor, now)
	if err != nil {
		return link{}, err
	}
	return link{token: token, qr: qr, roleID: id}, nil
}

func (a *API) renameRole(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.RenameRole(ctx, o.EventID, b.roleID, b.name, o.Actor, now)
}

func (a *API) deleteRole(ctx context.Context, o store.TokenOwner, b body, now time.Time) (link, error) {
	return link{}, a.store.DeleteRole(ctx, o.EventID, b.version, b.roleID, o.Actor, now)
}

// errBadOrigin is an origin the server will not put in a QR code; it is a
// malformed request (400).
var errBadOrigin = errors.New("bad origin")

// checkOrigin accepts only what location.origin is for this site: https
// with a lowercase host, or http on this computer, and nothing else
// (api.md section 3「QR Code」). A user part, path, query or fragment would
// let the QR code send the token to another site.
func checkOrigin(origin string) bool {
	if len(origin) > 200 {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.Opaque != "" || u.String() != origin {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return false
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return host == "localhost" || host == "127.0.0.1"
	}
	return false
}

// errQR never carries what was encoded: the link holds a token
// (tech-stack.md 2.1「日誌」).
var errQR = errors.New("qr code could not be made")

// linkQR is the PNG data URI of origin + "/#t=" + token, the very link
// the screen shows as text; "" when the origin was left out of a reissue.
// An origin that is given, an empty one included, must be this site's
// (M2c verification P1). It is made in memory and never stored (US-M-13.2).
func linkQR(origin string, given bool, token string) (string, error) {
	if !given {
		return "", nil
	}
	if !checkOrigin(origin) {
		return "", errBadOrigin
	}
	png, err := qrcode.Encode(origin+"/#t="+token, qrcode.Medium, 256)
	if err != nil {
		return "", errQR
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// importPreviewResponse is the pasted import checked without writing
// (US-M-12.2).
type importPreviewResponse struct {
	Result  string            `json:"result"` // "ok"
	Preview importPreviewView `json:"preview"`
}

type importPreviewView struct {
	FileError    *string         `json:"fileError"` // the text as a whole cannot be read; rows is then empty
	Rows         []importRowView `json:"rows"`
	MissingRoles []string        `json:"missingRoles"`
	TooManyRoles bool            `json:"tooManyRoles"`
	Replaces     int             `json:"replaces"`
}

type importRowView struct {
	Line        int      `json:"line"`
	Seq         int      `json:"seq"`
	Title       string   `json:"title"`
	Start       string   `json:"start"` // HH:MM as written, on the event day
	DurationMin int      `json:"durationMin"`
	IsFixed     bool     `json:"isFixed"`
	Roles       []string `json:"roles"`
	RoleNote    *string  `json:"roleNote"`
	NotePublic  *string  `json:"notePublic"`
	Errors      []string `json:"errors"`
}

// previewImport checks a pasted rundown against the caller's event as it
// is now, in the read of Snapshot (api.md section 7), and writes nothing.
func (a *API) previewImport(w http.ResponseWriter, r *http.Request) {
	d, ok := a.readAsMaster(w, r, capEditRundown)
	if !ok {
		return
	}
	b, ok := readBody(w, r, importPreviewFields)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if d.Event.Status != domain.Draft {
		a.writeFailed(w, r, domain.ErrNotDraft)
		return
	}
	view := importPreviewView{Rows: []importRowView{}, MissingRoles: []string{}, Replaces: len(d.Cues)}
	rows, err := rundown.ParseText(b.text)
	if err != nil {
		msg := err.Error()
		view.FileError = &msg
		writeJSON(w, http.StatusOK, importPreviewResponse{Result: "ok", Preview: view})
		return
	}
	check := store.CheckImport(d.Event, rows, d.Roles, len(d.Cues))
	for i, row := range rows {
		rv := importRowView{
			Line: row.Line, Seq: row.Seq, Title: row.Title, DurationMin: row.DurationMin, IsFixed: row.Fixed,
			Roles: append([]string{}, row.Roles...), RoleNote: textOrNil(row.RoleNote), NotePublic: textOrNil(row.Note),
			Start:  clockText(row.StartHour, row.StartMinute),
			Errors: append([]string{}, check.Problems[i]...),
		}
		view.Rows = append(view.Rows, rv)
	}
	view.MissingRoles = append(view.MissingRoles, check.MissingRoles...)
	view.TooManyRoles = check.TooManyRoles
	writeJSON(w, http.StatusOK, importPreviewResponse{Result: "ok", Preview: view})
}

func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func clockText(h, m int) string {
	return strconv.Itoa(100 + h)[1:] + ":" + strconv.Itoa(100 + m)[1:]
}

// rolePreviewResponse carries another role's snapshot under "preview",
// never "snapshot": a client stores only "snapshot" as its own, so the
// Master's device can never take it for its own (api.md section 3).
type rolePreviewResponse struct {
	Result  string `json:"result"` // "ok"
	Preview any    `json:"preview"`
}

// previewRole shows the Master what one role's own snapshot holds
// (US-M-14). It writes nothing, marks only the caller as seen, and builds
// the role's snapshot with the one snapshot function from data that
// carries no token of that role.
func (a *API) previewRole(w http.ResponseWriter, r *http.Request) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	// The body is read after the capability check, like every endpoint;
	// the role id it names is needed for the read, so the token is looked
	// up first on its own.
	lookupCtx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	now := a.now()
	owner, err := a.store.LookupToken(lookupCtx, hash, now)
	cancel()
	if err != nil {
		a.lookupFailed(w, r, err)
		return
	}
	a.seen(owner, now)
	if !allowed(capLinks, owner.RoleType) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	b, ok := readBody(w, r, linkFields)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	a.sendPreview(w, r, hash, b.roleID)
}

// sendPreview reads the caller and roleID's snapshot data in one read
// again and answers previewRole.
func (a *API) sendPreview(w http.ResponseWriter, r *http.Request, hash []byte, roleID int64) {
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	now := a.now()
	caller, d, err := a.store.PreviewSnapshot(ctx, hash, roleID, now)
	if !a.previewAllowed(w, r, caller, err) {
		return
	}
	if err := previewable(d.Owner.RoleType); err != nil {
		a.writeFailed(w, r, err)
		return
	}
	snap, err := a.buildSnapshot(d, now)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rolePreviewResponse{Result: "ok", Preview: snap})
}

// previewAllowed answers a preview's failed read or a caller without the
// capability, and reports whether the preview may go on.
func (a *API) previewAllowed(w http.ResponseWriter, r *http.Request, caller store.TokenOwner, err error) bool {
	if unresolved(caller, err) {
		a.lookupFailed(w, r, err)
		return false
	}
	if !allowed(capLinks, caller.RoleType) {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	if err != nil {
		a.writeFailed(w, r, err)
		return false
	}
	return true
}

// unresolved is a read that failed before the caller was known: a
// console link's caller has no TokenID but does have a role (api.md 10.2).
func unresolved(caller store.TokenOwner, err error) bool {
	return err != nil && caller.RoleID == 0
}

// previewable is why a role of type t has no preview: the Master's own
// console, or a type not opened yet, which has no snapshot and answers
// the same 404 as no role.
func previewable(t domain.RoleType) error {
	if t == domain.Master {
		return store.ErrMasterRole
	}
	if !t.HasOwnCues() && t != domain.Host {
		return store.ErrRoleNotFound
	}
	return nil
}

// readAsMaster resolves the token and reads the caller's snapshot data in
// one read (api.md section 7), marks the caller seen and checks the
// capability; on failure it has answered already.
func (a *API) readAsMaster(w http.ResponseWriter, r *http.Request, c capability) (store.SnapshotData, bool) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return store.SnapshotData{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	now := a.now()
	d, err := a.store.Snapshot(ctx, hash, now)
	if err != nil {
		a.lookupFailed(w, r, err)
		return store.SnapshotData{}, false
	}
	a.seen(d.Owner, now)
	if !allowed(c, d.Owner.RoleType) {
		writeError(w, http.StatusForbidden, "forbidden")
		return store.SnapshotData{}, false
	}
	return d, true
}
