package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// US-CO-1.10: a login mail's link is traded once for a login link, within
// 15 minutes, while its member is there; every refusal is the same 404.
func TestUS_CO_1_10_ConfirmOnce(t *testing.T) {
	e := newEnv(t)
	e.company()
	mail := e.mailLink("a@x.tw")
	session := e.confirm(mail).loginToken(t)
	e.do(http.MethodGet, pathCompany, session, "").wantResult(t, "ok")
	e.confirm(mail).wantError(t, http.StatusNotFound, "not_found")
	// A login mail's link is no login link and no role's link.
	e.do(http.MethodGet, pathCompany, e.mailLink("a@x.tw"), "").wantError(t, http.StatusNotFound, "not_found")
	e.get(e.mailLink("a@x.tw")).wantResult(t, "invalid")
	// Not a login mail's link: none, a role's, a login link.
	for _, tok := range []string{"", "x", newToken(t), e.master, session} {
		e.confirm(tok).wantError(t, http.StatusNotFound, "not_found")
	}
	e.do(http.MethodPost, pathLoginConfirm, e.mailLink("a@x.tw"), `{"x":1}`).wantError(t, http.StatusBadRequest, "bad_request")
}

// US-CO-1.10: 15 minutes, then the link is refused.
func TestUS_CO_1_10_ConfirmWithinFifteenMinutes(t *testing.T) {
	e := newEnv(t)
	e.company()
	start := e.clock()
	early, late := e.mailLink("a@x.tw"), e.mailLink("a@x.tw")
	e.setClock(start.Add(15*time.Minute - time.Second))
	e.confirm(early).loginToken(t)
	e.setClock(start.Add(15 * time.Minute))
	e.confirm(late).wantError(t, http.StatusNotFound, "not_found")
}

// US-CO-1.10: a removed member's login mail no longer works.
func TestUS_CO_1_10_RemovedMembersMailIsRefused(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	e.post(pathMemberAdd, session, map[string]any{"email": "b@x.tw"}).wantResult(t, "ok")
	mail := e.mailLink("b@x.tw")
	r := e.do(http.MethodGet, pathCompany, session, "")
	e.post(pathMemberRemove, session, map[string]any{"memberId": memberIDs(t, r)["b@x.tw"]}).wantResult(t, "ok")
	e.confirm(mail).wantError(t, http.StatusNotFound, "not_found")
}

// api.md 10.2: a member already waiting is not queued twice.
func TestUS_CO_1_11_AMemberWaitsOnce(t *testing.T) {
	e := newEnv(t)
	e.company() // one mail
	for i := 0; i < 3; i++ {
		e.post(pathCompanyLogin, "", map[string]any{"email": "a@x.tw"}).wantResult(t, "ok")
	}
	e.mailq.SendWaiting(bg)
	if n := len(e.outbox.sent()); n != 2 {
		t.Errorf("mails = %d, want 1 more", n)
	}
}

// US-CO-1.11: ten mails a member a day; the eleventh is not sent and the
// answer is the same.
func TestUS_CO_1_11_TenMailsAMemberADay(t *testing.T) {
	e := newEnv(t)
	e.company()                // the first
	for i := 2; i <= 10; i++ { // US-CO-1.11: 10
		e.mailLink("a@x.tw")
	}
	e.post(pathCompanyLogin, "", map[string]any{"email": "a@x.tw"}).wantResult(t, "ok")
	e.mailq.SendWaiting(bg)
	if n := len(e.outbox.sent()); n != 10 {
		t.Errorf("mails = %d, want 10", n)
	}
}

// US-CO-1.12: without a usable mail setup every email of the right form
// gets mail_unavailable.
func TestUS_CO_1_12_NoMailSetup(t *testing.T) {
	e := newEnv(t)
	e.company()
	e.h = New(Config{
		Static: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>BeThere</title>")}},
		Store:  e.fs, Now: e.clock, Version: "test-1", Logger: slog.New(slog.NewJSONHandler(e.logs, nil)),
	})
	for _, email := range []string{"a@x.tw", "nobody@x.tw"} {
		e.post(pathCompanyLogin, "", map[string]any{"email": email}).wantError(t, http.StatusServiceUnavailable, "mail_unavailable")
	}
	e.post(pathCompanyLogin, "", map[string]any{"email": "bad"}).wantError(t, http.StatusUnprocessableEntity, "invalid_email")
}

// US-CO-1.12: a failed send is logged by its kind only, never the
// address or the link, and still counts.
func TestUS_CO_1_12_FailedSendIsCountedNotLogged(t *testing.T) {
	e := newEnv(t)
	e.company()
	e.outbox.err = errors.New("dial tcp: connection refused to a@x.tw")
	e.post(pathCompanyLogin, "", map[string]any{"email": "a@x.tw"}).wantResult(t, "ok")
	e.mailq.SendWaiting(bg)
	var n int
	if err := e.db().QueryRow(`SELECT count(*) FROM login_mails`).Scan(&n); err != nil || n != 2 { // the company's first and the failed one
		t.Errorf("login mails = %d (%v), want the failed one kept", n, err)
	}
	if strings.Contains(e.logs.String(), "a@x.tw") || strings.Contains(e.logs.String(), "#t=") {
		t.Errorf("the log carries an address or a link: %s", e.logs.String())
	}
}

// US-CO-1.7: in maintenance the confirm is closed too.
func TestUS_CO_1_7_MaintenanceClosesTheConfirm(t *testing.T) {
	e := newEnv(t)
	e.company()
	mail := e.mailLink("a@x.tw")
	e.maintenance()
	e.confirm(mail).wantError(t, http.StatusServiceUnavailable, "maintenance")
}
