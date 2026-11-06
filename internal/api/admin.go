package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/store"
)

// The admin page's endpoints (US-O-9, US-O-10, ADR-0038). A missing or
// malformed header, an unknown token, a role's link and a replaced admin
// link all answer the same 404 not_found; Gate closes them in maintenance.

type adminOverviewResponse struct {
	Result        string               `json:"result"` // "ok"
	Invites       []adminInviteSummary `json:"invites"`
	Events        []adminEventSummary  `json:"events"`
	Organizations []adminOrgSummary    `json:"organizations"`
}

// adminOrgSummary is a company without any email (US-O-11.3).
type adminOrgSummary struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Members   int       `json:"members"`
	Events    int       `json:"events"`
	CreatedAt time.Time `json:"createdAt"`
}

type adminInviteSummary struct {
	ID        int64     `json:"id"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type adminEventSummary struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Date      string    `json:"date"`
	Status    string    `json:"status"` // Draft, Live or Ended
	CreatedAt time.Time `json:"createdAt"`
	// Organization is the company's name, null for an event of none.
	Organization *string `json:"organization"`
}

// adminInviteResponse carries the new code in this response only (US-O-10.2).
type adminInviteResponse struct {
	Result    string    `json:"result"` // "ok"
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// adminFailed answers a store error: a confirmed miss is the 404 every
// other refusal gets, anything else an internal error.
func (a *API) adminFailed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrInvalidAdmin) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.internal(w, r, err)
}

// adminOverview is GET /api/admin/overview (US-O-9.2).
func (a *API) adminOverview(w http.ResponseWriter, r *http.Request) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	o, err := a.store.AdminOverview(ctx, hash, a.now())
	if err != nil {
		a.adminFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, overviewResponse(o))
}

// overviewResponse shapes the overview: lists are never null.
func overviewResponse(o store.AdminOverview) adminOverviewResponse {
	resp := adminOverviewResponse{Result: "ok", Invites: []adminInviteSummary{}, Events: []adminEventSummary{},
		Organizations: []adminOrgSummary{}}
	for _, inv := range o.Invites {
		resp.Invites = append(resp.Invites, adminInviteSummary{ID: inv.ID, ExpiresAt: inv.ExpiresAt})
	}
	for _, ev := range o.Events {
		resp.Events = append(resp.Events, adminEventOf(ev))
	}
	for _, org := range o.Organizations {
		resp.Organizations = append(resp.Organizations, adminOrgSummary{
			ID: org.ID, Name: org.Name, Members: org.Members, Events: org.Events, CreatedAt: org.CreatedAt,
		})
	}
	return resp
}

// adminEventOf is one event of the overview; its company is null when it
// has none (US-O-11.3).
func adminEventOf(ev store.AdminEvent) adminEventSummary {
	var org *string
	if ev.Organization != "" {
		org = &ev.Organization
	}
	return adminEventSummary{
		ID: ev.ID, Name: ev.Name, Date: ev.Date.String(), Status: string(ev.Status), CreatedAt: ev.CreatedAt,
		Organization: org,
	}
}

// adminBody proves the admin link before reading the body, so a
// stranger's request is the same 404 whatever it sends; on failure it has
// answered already.
func (a *API) adminBody(w http.ResponseWriter, r *http.Request, hash []byte, fields fieldSet) (body, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	err := a.store.CheckAdmin(ctx, hash)
	cancel()
	if err != nil {
		a.adminFailed(w, r, err)
		return body{}, false
	}
	b, ok := readBody(w, r, fields)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
	}
	return b, ok
}

// adminInvite is POST /api/admin/invites (US-O-10): an empty body, a new
// code as create-invite makes, within the daily limit (US-O-10.3).
func (a *API) adminInvite(w http.ResponseWriter, r *http.Request) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if _, ok := a.adminBody(w, r, hash, fieldSet{}); !ok {
		return
	}
	code, err := auth.NewRecoveryCode()
	if err != nil {
		a.internal(w, r, err)
		return
	}
	codeHash, err := auth.HashRecoveryCode(code)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	now := a.now()
	wctx, wcancel := context.WithTimeout(r.Context(), createTimeout)
	defer wcancel()
	id, err := a.store.CreateAdminInvite(wctx, hash, codeHash, now)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, adminInviteResponse{Result: "ok", ID: id, Code: code, ExpiresAt: now.Add(store.InviteLife)})
	case errors.Is(err, store.ErrTooManyInvites):
		writeError(w, http.StatusUnprocessableEntity, "too_many_invites")
	default:
		a.adminFailed(w, r, err)
	}
}
