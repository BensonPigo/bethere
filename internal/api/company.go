package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// The company page's endpoints (api.md section 10, ADR-0039). A login link
// goes in the Authorization header like a role's link; a missing or
// malformed header, an unknown, expired or logged-out link, a removed
// member's, a role's and the admin's all answer the same 404 not_found,
// before the body is read. Gate closes them in maintenance: none of them
// is a Master's request.

var (
	emailFields         = fieldSet{"email": text} // the login and a new member
	logoutFields        = fieldSet{}
	removeMemberFields  = fieldSet{"memberId": positiveInt}
	companyEventFields  = fieldSet{"name": text, "date": text}
	openConsoleFields   = fieldSet{"eventId": positiveInt}
	organizationFields  = fieldSet{"name": text, "email": text}
	maxCompanyNameRunes = maxEventNameRunes // US-O-11.1: the rule of an event's name
)

type loginResponse struct {
	Result     string `json:"result"` // "ok"
	LoginToken string `json:"loginToken"`
}

type companyResponse struct {
	Result       string           `json:"result"` // "ok"
	Organization companyName      `json:"organization"`
	Me           int64            `json:"me"`
	Members      []companyMember  `json:"members"`
	Events       []companyEventIn `json:"events"`
}

type companyName struct {
	Name string `json:"name"`
}

type companyMember struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

type companyEventIn struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Date   string `json:"date"`
	Status string `json:"status"` // Draft, Live or Ended
}

// companyEventResponse carries the new Master link's token and the
// recovery code, in this response only (US-CO-3.3).
type companyEventResponse struct {
	Result       string `json:"result"` // "ok"
	EventID      int64  `json:"eventId"`
	MasterToken  string `json:"masterToken"`
	RecoveryCode string `json:"recoveryCode"`
}

type consoleResponse struct {
	Result       string `json:"result"` // "ok"
	ConsoleToken string `json:"consoleToken"`
}

type organizationResponse struct {
	Result string `json:"result"` // "ok"
	ID     int64  `json:"id"`
}

// companyErrors are the refusals of api.md 10.3.
var companyErrors = []struct {
	err    error
	status int
	code   string
}{
	{store.ErrInvalidSession, http.StatusNotFound, "not_found"},
	{store.ErrNotFound, http.StatusNotFound, "not_found"},
	{store.ErrMembersClosed, http.StatusServiceUnavailable, "maintenance"},
	{store.ErrNotMember, http.StatusUnprocessableEntity, "not_member"},
	{store.ErrEmailTaken, http.StatusUnprocessableEntity, "email_taken"},
	{store.ErrTooManyMembers, http.StatusUnprocessableEntity, "too_many_members"},
	{store.ErrCannotRemoveSelf, http.StatusUnprocessableEntity, "cannot_remove_self"},
	{store.ErrTooManyEvents, http.StatusUnprocessableEntity, "too_many_events"},
	{store.ErrTooManyOrganizations, http.StatusUnprocessableEntity, "too_many_organizations"},
	{store.ErrInvalidAdmin, http.StatusNotFound, "not_found"},
	{errInvalidEmail, http.StatusUnprocessableEntity, "invalid_email"},
}

// companyFailed answers a store error of section 10; anything not listed
// is an internal error.
func (a *API) companyFailed(w http.ResponseWriter, r *http.Request, err error) {
	for _, c := range companyErrors {
		if errors.Is(err, c.err) {
			writeError(w, c.status, c.code)
			return
		}
	}
	a.internal(w, r, err)
}

// login is POST /api/company/login (US-CO-1.2): the email in the body,
// never a header. An email of no company or not an email answers the same.
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, emailFields)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	email, ok := domain.CleanEmail(b.email)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "not_member")
		return
	}
	token, err := auth.NewToken()
	if err != nil {
		a.internal(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), createTimeout)
	defer cancel()
	if err := a.store.Login(ctx, email, auth.HashToken(token), a.now()); err != nil {
		a.companyFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{Result: "ok", LoginToken: token})
}

// session proves the login link before anything else and returns its
// hash; on failure it has answered already.
func (a *API) session(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	if err := a.store.CheckSession(ctx, hash, a.now()); err != nil {
		a.companyFailed(w, r, err)
		return nil, false
	}
	return hash, true
}

// sessionBody is session and then the body; on failure it has answered.
func (a *API) sessionBody(w http.ResponseWriter, r *http.Request, fields fieldSet) ([]byte, body, bool) {
	hash, ok := a.session(w, r)
	if !ok {
		return nil, body{}, false
	}
	b, ok := readBody(w, r, fields)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return nil, body{}, false
	}
	return hash, b, true
}

// logout is POST /api/company/logout (US-CO-1.4): this link only.
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	hash, _, ok := a.sessionBody(w, r, logoutFields)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), createTimeout)
	defer cancel()
	if err := a.store.Logout(ctx, hash); err != nil {
		a.companyFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resultResponse{Result: "ok"})
}

// getCompany is GET /api/company.
func (a *API) getCompany(w http.ResponseWriter, r *http.Request) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.respondCompany(w, r, hash)
}

// respondCompany reads and sends the caller's company page.
func (a *API) respondCompany(w http.ResponseWriter, r *http.Request, hash []byte) {
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	c, err := a.store.Company(ctx, hash, a.now())
	if err != nil {
		a.companyFailed(w, r, err)
		return
	}
	resp := companyResponse{Result: "ok", Organization: companyName{Name: c.Name}, Me: c.Me,
		Members: []companyMember{}, Events: []companyEventIn{}}
	for _, m := range c.Members {
		resp.Members = append(resp.Members, companyMember{ID: m.ID, Email: m.Email})
	}
	for _, e := range c.Events {
		resp.Events = append(resp.Events, companyEventIn{ID: e.ID, Name: e.Name, Date: e.Date.String(), Status: string(e.Status)})
	}
	writeJSON(w, http.StatusOK, resp)
}

// companyChange runs one change of the company page and answers with the
// page as it is now.
func (a *API) companyChange(fields fieldSet, change func(ctx context.Context, hash []byte, b body, now time.Time) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, b, ok := a.sessionBody(w, r, fields)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), createTimeout)
		err := change(ctx, hash, b, a.now())
		cancel()
		if err != nil {
			a.companyFailed(w, r, err)
			return
		}
		a.respondCompany(w, r, hash)
	}
}

var errInvalidEmail = errors.New("not one plain email")

// addMember is POST /api/company/members/add (US-CO-2.1). The service log
// names both members by number, never by email or link (US-CO-2.6).
func (a *API) addMember(ctx context.Context, hash []byte, b body, now time.Time) error {
	email, ok := domain.CleanEmail(b.email)
	if !ok {
		return errInvalidEmail
	}
	by, added, err := a.store.AddMember(ctx, hash, email, now)
	if err == nil {
		a.logger.Info("member.add", "by", by, "member", added)
	}
	return err
}

// removeMember is POST /api/company/members/remove (US-CO-2.3, 2.6).
func (a *API) removeMember(ctx context.Context, hash []byte, b body, now time.Time) error {
	by, err := a.store.RemoveMember(ctx, hash, b.memberID, now)
	if err == nil {
		a.logger.Info("member.remove", "by", by, "member", b.memberID)
	}
	return err
}

// companyEvent is POST /api/company/events (US-CO-3.2): the checks and
// the fixed zone and Master name of an invited event, without a code.
func (a *API) companyEvent(w http.ResponseWriter, r *http.Request) {
	hash, b, ok := a.sessionBody(w, r, companyEventFields)
	if !ok {
		return
	}
	now := a.now()
	name, nameOK := domain.CleanLabel(b.name, maxEventNameRunes)
	date, dateOK := newEventDate(b.date, now)
	if !nameOK || !dateOK {
		writeError(w, http.StatusUnprocessableEntity, "invalid_event")
		return
	}
	token, code, codeHash, err := newEventSecrets()
	if err != nil {
		a.internal(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), createTimeout)
	defer cancel()
	id, err := a.store.CreateCompanyEvent(ctx, hash, store.NewEvent{
		Name: name, Date: date, TimeZone: newEventZone, MasterName: newEventMaster,
		MasterTokenHash: auth.HashToken(token), RecoveryCodeHash: codeHash,
	}, now)
	if err != nil {
		a.companyFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, companyEventResponse{Result: "ok", EventID: id, MasterToken: token, RecoveryCode: code})
}

// newEventSecrets makes a new event's Master token and recovery code.
func newEventSecrets() (token, code string, codeHash []byte, err error) {
	if token, err = auth.NewToken(); err != nil {
		return "", "", nil, err
	}
	if code, err = auth.NewRecoveryCode(); err != nil {
		return "", "", nil, err
	}
	codeHash, err = auth.HashRecoveryCode(code)
	return token, code, codeHash, err
}

// openConsole is POST /api/company/events/open (US-CO-4.1): a new console
// link of the caller for one event of their company.
func (a *API) openConsole(w http.ResponseWriter, r *http.Request) {
	hash, b, ok := a.sessionBody(w, r, openConsoleFields)
	if !ok {
		return
	}
	token, err := auth.NewToken()
	if err != nil {
		a.internal(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), createTimeout)
	defer cancel()
	if err := a.store.OpenConsole(ctx, hash, b.eventID, auth.HashToken(token), a.now()); err != nil {
		a.companyFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, consoleResponse{Result: "ok", ConsoleToken: token})
}

// adminOrganization is POST /api/admin/organizations (US-O-11.1).
func (a *API) adminOrganization(w http.ResponseWriter, r *http.Request) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	b, ok := a.adminBody(w, r, hash, organizationFields)
	if !ok {
		return
	}
	name, ok := domain.CleanLabel(b.name, maxCompanyNameRunes)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_organization")
		return
	}
	email, ok := domain.CleanEmail(b.email)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_email")
		return
	}
	wctx, wcancel := context.WithTimeout(r.Context(), createTimeout)
	defer wcancel()
	id, err := a.store.CreateOrganization(wctx, hash, name, email, a.now())
	if err != nil {
		a.companyFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, organizationResponse{Result: "ok", ID: id})
}
