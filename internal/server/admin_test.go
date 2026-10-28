package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/store"
)

const (
	pathAdminOverview = "/api/admin/overview"
	pathAdminInvites  = "/api/admin/invites"
)

// adminLink issues the admin link as issue-admin-link does.
func (e *env) adminLink() string {
	e.t.Helper()
	tok := newToken(e.t)
	if err := e.st.IssueAdminLink(bg, auth.HashToken(tok)); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// US-O-9.1: no header, a malformed one, an unknown token, a role's link
// and a replaced admin link all get the same 404 on both endpoints; the
// admin link is no role's link either.
func TestUS_O_9_1_OnlyTheAdminLinkOpensTheAdminPage(t *testing.T) {
	e := newEnv(t)
	old := e.adminLink()
	admin := e.adminLink()
	for _, tok := range []string{"", newToken(t), e.master, e.stylist, old} { // none, unknown, two roles', replaced
		e.do(http.MethodGet, pathAdminOverview, tok, "").wantError(t, http.StatusNotFound, "not_found")
		e.do(http.MethodPost, pathAdminInvites, tok, "{}").wantError(t, http.StatusNotFound, "not_found")
		e.do(http.MethodPost, pathAdminInvites, tok, `{"x":1}`).wantError(t, http.StatusNotFound, "not_found")
	}
	r := e.request(http.MethodGet, pathAdminOverview, "", "")
	r.Header.Set("Authorization", "Bearer not-a-token")
	e.doReq(r).wantError(t, http.StatusNotFound, "not_found")
	e.get(admin).wantResult(t, "invalid")
}

// US-O-9.2: the overview lists the unused codes and the events, with no
// token, code or hash.
func TestUS_O_9_2_Overview(t *testing.T) {
	e := newEnv(t)
	admin := e.adminLink()
	e.post(pathAdminInvites, admin, map[string]any{}).wantResult(t, "ok")
	r := e.do(http.MethodGet, pathAdminOverview, admin, "")
	r.wantResult(t, "ok")
	wantKeys(t, "overview", r.body, "result", "invites", "events")
	invites, _ := r.body["invites"].([]any)
	events, _ := r.body["events"].([]any)
	if len(invites) != 1 || len(events) != 1 {
		t.Fatalf("invites %v, events %v", invites, events)
	}
	wantKeys(t, "invite", invites[0].(map[string]any), "id", "expiresAt")
	ev := events[0].(map[string]any)
	wantKeys(t, "event", ev, "id", "name", "date", "status", "createdAt")
	if ev["name"] != "婚宴" || ev["date"] != "2027-06-06" || ev["status"] != "Draft" {
		t.Errorf("event = %v", ev)
	}
}

// US-O-10.1, 10.2: a code from the admin page creates an event at /new;
// it is shown in that one response and reaches no log.
func TestUS_O_10_2_AdminCodeCreatesAnEvent(t *testing.T) {
	e := newEnv(t)
	admin := e.adminLink()
	r := e.post(pathAdminInvites, admin, map[string]any{})
	r.wantResult(t, "ok")
	wantKeys(t, "invite", r.body, "result", "id", "code", "expiresAt")
	code, _ := r.body["code"].(string)
	e.do(http.MethodPost, pathAdminInvites, admin, `{"x":1}`).wantError(t, http.StatusBadRequest, "bad_request")
	c := e.createEvent("", eventFields(code, "小明的婚禮", "2027-06-06"))
	c.wantResult(t, "ok")
	for _, secret := range []string{admin, code} {
		if strings.Contains(e.logs.String(), secret) {
			t.Errorf("the log carries a secret")
		}
	}
}

// US-O-10.3: the daily limit answers 422 too_many_invites.
func TestUS_O_10_3_DailyLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.adminLink()
	for range store.AdminInvitesPerDay {
		e.post(pathAdminInvites, admin, map[string]any{}).wantResult(t, "ok")
	}
	e.post(pathAdminInvites, admin, map[string]any{}).wantError(t, http.StatusUnprocessableEntity, "too_many_invites")
}

// US-O-9.3: under maintenance the admin page is closed like any role.
func TestUS_O_9_3_AdminPageClosedInMaintenance(t *testing.T) {
	e := newEnv(t)
	admin := e.adminLink()
	e.maintenance()
	e.do(http.MethodGet, pathAdminOverview, admin, "").wantMaintenance(t, "overview")
	e.do(http.MethodPost, pathAdminInvites, admin, "{}").wantMaintenance(t, "invites")
}
