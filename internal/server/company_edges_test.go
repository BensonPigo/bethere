package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/store"
)

// farEvent makes an event of the company on a date far enough ahead that
// it stays open while a login link runs out.
func (e *env) farEvent(session, name string) (int64, string) {
	e.t.Helper()
	r := e.post(pathCompanyEvents, session, map[string]any{"name": name, "date": "2029-06-16"})
	r.wantResult(e.t, "ok")
	id, _ := r.body["eventId"].(float64)
	tok, _ := r.body["masterToken"].(string)
	return int64(id), tok
}

func (e *env) generation() int {
	e.t.Helper()
	return e.count(`SELECT n FROM data_generation`)
}

// US-CO-2.4, 1.4, 1.10 (token boundary): a member removed and added back
// with the same email holds none of the old links: login link, console link
// and the login mail's link all fail; a new login still works.
func TestUS_CO_2_4_ReAddedMemberOldLinksFail(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	r := e.post(pathMemberAdd, session, map[string]any{"email": "b@x.tw"})
	r.wantResult(t, "ok")
	oldID := memberIDs(t, r)["b@x.tw"]
	oldLogin := e.login("b@x.tw")
	ev, _ := e.companyEvent(session, "甲婚宴")
	oldConsole := e.console(oldLogin, ev)
	oldMail := e.mailLink("b@x.tw")
	e.post(pathMemberRemove, session, map[string]any{"memberId": oldID}).wantResult(t, "ok")
	r = e.post(pathMemberAdd, session, map[string]any{"email": "b@x.tw"})
	r.wantResult(t, "ok")
	if newID := memberIDs(t, r)["b@x.tw"]; newID == oldID {
		t.Errorf("the member number %d was given again", oldID)
	}
	e.do(http.MethodGet, pathCompany, oldLogin, "").wantError(t, http.StatusNotFound, "not_found")
	e.do(http.MethodPost, pathMemberAdd, oldLogin, `{"email":"c@x.tw"}`).wantError(t, http.StatusNotFound, "not_found")
	e.openConsole(oldLogin, ev).wantError(t, http.StatusNotFound, "not_found")
	e.get(oldConsole).wantResult(t, "invalid")
	e.post(pathAddRole, oldConsole, map[string]any{"name": "攝影", "view": "Glance", "checksIn": true, "origin": origin}).
		wantResult(t, "invalid")
	e.confirm(oldMail).wantError(t, http.StatusNotFound, "not_found")
	// The new member logs in with a new mail.
	e.do(http.MethodGet, pathCompany, e.login("b@x.tw"), "").wantResult(t, "ok")
}

// US-CO-1.4: logging out, or a login link running out, leaves an opened
// console link working, in use and in the snapshot.
func TestUS_CO_1_4_LogoutAndExpiryDoNotCloseAConsole(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	ev, _ := e.farEvent(session, "甲婚宴")
	console := e.console(session, ev)
	e.do(http.MethodPost, pathCompanyLogout, session, `{}`).wantResult(t, "ok")
	e.get(console).wantResult(t, "ok")
	e.post(pathAddRole, console, map[string]any{"name": "攝影", "view": "Glance", "checksIn": true, "origin": origin}).wantResult(t, "ok")

	again := e.login("a@x.tw")
	console2 := e.console(again, ev)
	e.setClock(e.clock().Add(store.SessionLife + time.Hour))
	e.do(http.MethodGet, pathCompany, again, "").wantError(t, http.StatusNotFound, "not_found")
	e.get(console).wantResult(t, "ok")
	e.get(console2).wantResult(t, "ok")
	e.post(pathAddRole, console2, map[string]any{"name": "攝影二", "view": "Glance", "checksIn": true, "origin": origin}).wantResult(t, "ok")
}

// US-CO-1.5: a console link is no login link: the company page and the
// confirm refuse it like any other link.
func TestUS_CO_1_5_ConsoleLinkOpensNoCompanyEndpoint(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	ev, _ := e.companyEvent(session, "甲婚宴")
	console := e.console(session, ev)
	e.do(http.MethodGet, pathCompany, console, "").wantError(t, http.StatusNotFound, "not_found")
	for _, p := range []string{pathCompanyLogout, pathMemberAdd, pathMemberRemove, pathCompanyEvents, pathOpenConsole} {
		e.do(http.MethodPost, p, console, `{"x":1}`).wantError(t, http.StatusNotFound, "not_found")
		e.do(http.MethodPost, p, console, `{}`).wantError(t, http.StatusNotFound, "not_found")
	}
	n := e.count(`SELECT count(*) FROM member_sessions`)
	e.do(http.MethodPost, pathLoginConfirm, console, `{}`).wantError(t, http.StatusNotFound, "not_found")
	if got := e.count(`SELECT count(*) FROM member_sessions`); got != n {
		t.Errorf("a console link at the confirm made a login link: %d -> %d", n, got)
	}
}

// US-CO-1.7: in maintenance the company's writes are 503, even with a
// valid login link, and nothing is written.
func TestUS_CO_1_7_MaintenanceClosesEveryCompanyWrite(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	r := e.post(pathMemberAdd, session, map[string]any{"email": "b@x.tw"})
	bID := memberIDs(t, r)["b@x.tw"]
	e.maintenance()
	gen := e.generation()
	for path, body := range map[string]map[string]any{
		pathCompanyLogout: {},
		pathMemberAdd:     {"email": "c@x.tw"},
		pathMemberRemove:  {"memberId": bID},
		pathCompanyEvents: {"name": "甲婚宴", "date": "2027-06-30"},
	} {
		e.post(path, session, body).wantError(t, http.StatusServiceUnavailable, "maintenance")
	}
	if got := e.generation(); got != gen {
		t.Errorf("something was written in maintenance: data generation %d -> %d", gen, got)
	}
	if n := e.count(`SELECT count(*) FROM members`); n != 2 {
		t.Errorf("members = %d, want 2", n)
	}
}

// US-CO-1.3: a login reads only: the same answer for a member, a stranger
// and a member the mail limit holds back, and no row of any table changes
// before the queue runs.
func TestUS_CO_1_3_LoginWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.company() // one mail
	gen := e.generation()
	mails, sessions := e.count(`SELECT count(*) FROM login_mails`), e.count(`SELECT count(*) FROM member_sessions`)
	member := e.post(pathCompanyLogin, "", map[string]any{"email": "a@x.tw"})
	stranger := e.post(pathCompanyLogin, "", map[string]any{"email": "nobody@x.tw"})
	member.wantResult(t, "ok")
	if member.Body.String() != stranger.Body.String() {
		t.Errorf("member %q, stranger %q", member.Body.String(), stranger.Body.String())
	}
	if got := e.generation(); got != gen ||
		e.count(`SELECT count(*) FROM login_mails`) != mails || e.count(`SELECT count(*) FROM member_sessions`) != sessions {
		t.Error("a login wrote a row before the queue ran")
	}
	// A member who has had the ten mails of the day gets the same answer.
	e.mailq.SendWaiting(bg)
	for e.count(`SELECT count(*) FROM login_mails`) < store.MailsPerMemberDay {
		e.mailLink("a@x.tw")
	}
	limited := e.post(pathCompanyLogin, "", map[string]any{"email": "a@x.tw"})
	if limited.Body.String() != stranger.Body.String() || limited.Code != stranger.Code {
		t.Errorf("limited member %d %q, stranger %d %q", limited.Code, limited.Body.String(), stranger.Code, stranger.Body.String())
	}
	sent := len(e.outbox.sent())
	e.mailq.SendWaiting(bg)
	if len(e.outbox.sent()) != sent {
		t.Error("a mail was sent past the limit")
	}
}

// US-CO-2.1: an email in another case is the same email.
func TestUS_CO_2_1_EmailInAnotherCaseIsTaken(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	for _, email := range []string{"A@x.tw", " A@X.TW ", "a@x.tw"} {
		e.post(pathMemberAdd, session, map[string]any{"email": email}).wantError(t, http.StatusUnprocessableEntity, "email_taken")
	}
	if n := e.count(`SELECT count(*) FROM members`); n != 1 {
		t.Errorf("members = %d, want 1", n)
	}
}

// US-CO-2.4: a removed member's requests sent while they are removed leave
// nothing after the removal; every request after it is 404.
func TestUS_CO_2_4_RemovalRacingAddAndNewEvent(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	r := e.post(pathMemberAdd, session, map[string]any{"email": "b@x.tw"})
	bID := memberIDs(t, r)["b@x.tw"]
	b := e.login("b@x.tw")
	var (
		wg      sync.WaitGroup
		removed atomic.Bool
		added   atomic.Int32
		made    atomic.Int32
	)
	start := make(chan struct{})
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			after := removed.Load()
			rec := e.do(http.MethodPost, pathMemberAdd, b, jsonBody(map[string]any{"email": fmt.Sprintf("race%d@x.tw", i)}))
			switch {
			case rec.Code == http.StatusOK && after:
				t.Errorf("an add after the removal succeeded")
			case rec.Code == http.StatusOK:
				added.Add(1)
			case rec.Code != http.StatusNotFound:
				t.Errorf("add: %d %s", rec.Code, rec.Body.String())
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			after := removed.Load()
			rec := e.do(http.MethodPost, pathCompanyEvents, b, jsonBody(map[string]any{"name": "競態", "date": "2027-06-30"}))
			switch {
			case rec.Code == http.StatusOK && after:
				t.Errorf("a new event after the removal succeeded")
			case rec.Code == http.StatusOK:
				made.Add(1)
			case rec.Code != http.StatusNotFound:
				t.Errorf("new event: %d %s", rec.Code, rec.Body.String())
			}
		}()
	}
	close(start)
	e.post(pathMemberRemove, session, map[string]any{"memberId": bID}).wantResult(t, "ok")
	removed.Store(true)
	wg.Wait()
	e.do(http.MethodPost, pathMemberAdd, b, `{"email":"late@x.tw"}`).wantError(t, http.StatusNotFound, "not_found")
	if n := e.count(`SELECT count(*) FROM members`); n != 1+int(added.Load()) {
		t.Errorf("members = %d, want a@x.tw and the %d added before the removal", n, added.Load())
	}
	if n := e.count(`SELECT count(*) FROM events WHERE organization_id IS NOT NULL`); n != int(made.Load()) {
		t.Errorf("company events = %d, want the %d made before the removal", n, made.Load())
	}
}

// parallel sends n requests at once and returns the statuses and codes.
func (e *env) parallel(n int, send func(i int) resp) []resp {
	var wg sync.WaitGroup
	out := make([]resp, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			out[i] = send(i)
		}()
	}
	close(start)
	wg.Wait()
	return out
}

func wantOneOK(t *testing.T, rs []resp, status int, code string) {
	t.Helper()
	ok := 0
	for _, r := range rs {
		switch {
		case r.Code == http.StatusOK && r.body["result"] == "ok":
			ok++
		case r.Code != status || r.body["error"] != code:
			t.Errorf("a loser got %d %s, want %d %s", r.Code, r.Body.String(), status, code)
		}
	}
	if ok != 1 {
		t.Errorf("%d requests succeeded, want exactly 1", ok)
	}
}

// US-CO-2.1: one email added at once is added once.
func TestUS_CO_2_1_ConcurrentAddOfOneEmail(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	rs := e.parallel(6, func(int) resp {
		return e.do(http.MethodPost, pathMemberAdd, session, `{"email":"dup@x.tw"}`)
	})
	wantOneOK(t, rs, http.StatusUnprocessableEntity, "email_taken")
}

// US-CO-2.5: the 50th seat given at once goes to one.
func TestUS_CO_2_5_ConcurrentAddOfTheLastSeat(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	for i := 1; i < 49; i++ {
		e.post(pathMemberAdd, session, map[string]any{"email": fmt.Sprintf("m%d@x.tw", i)}).wantResult(t, "ok")
	}
	rs := e.parallel(5, func(i int) resp {
		return e.do(http.MethodPost, pathMemberAdd, session, jsonBody(map[string]any{"email": fmt.Sprintf("last%d@x.tw", i)}))
	})
	wantOneOK(t, rs, http.StatusUnprocessableEntity, "too_many_members")
	if n := e.count(`SELECT count(*) FROM members`); n != 50 {
		t.Errorf("members = %d, want 50", n)
	}
}

// US-CO-3.4: the company's 20th event of the day made at once is made once.
func TestUS_CO_3_4_ConcurrentLastEvent(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	for range 19 {
		e.companyEvent(session, "甲")
	}
	rs := e.parallel(5, func(int) resp {
		return e.do(http.MethodPost, pathCompanyEvents, session, `{"name":"最後","date":"2027-06-30"}`)
	})
	wantOneOK(t, rs, http.StatusUnprocessableEntity, "too_many_events")
	if n := e.count(`SELECT count(*) FROM events WHERE organization_id IS NOT NULL`); n != 20 {
		t.Errorf("company events = %d, want 20", n)
	}
}

// US-O-11.2, 11.5: one email given to two companies at once makes one.
func TestUS_O_11_2_ConcurrentCompaniesOfOneEmail(t *testing.T) {
	e := newEnv(t)
	admin := e.adminLink()
	rs := e.parallel(4, func(i int) resp {
		return e.do(http.MethodPost, pathAdminOrgs, admin, jsonBody(map[string]any{"name": fmt.Sprintf("公司%d", i), "email": "same@x.tw"}))
	})
	wantOneOK(t, rs, http.StatusUnprocessableEntity, "email_taken")
	if n := e.count(`SELECT count(*) FROM organizations`); n != 1 {
		t.Errorf("organizations = %d, want 1", n)
	}
}

// US-CO-4.2: the console link and the Master link are the same event's
// version: a write on a stale version is 409 with the latest snapshot from
// whichever of the two comes second; opening a console changes no other link.
func TestUS_CO_4_2_ConsoleAndMasterShareTheVersion(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	ev, master := e.companyEvent(session, "甲婚宴")
	links := e.count(`SELECT count(*) FROM access_tokens`)
	roles := e.count(`SELECT count(*) FROM roles`)
	console := e.console(session, ev)
	if got := e.count(`SELECT count(*) FROM access_tokens`); got != links {
		t.Errorf("opening a console changed the role links: %d -> %d", links, got)
	}
	if got := e.count(`SELECT count(*) FROM roles`); got != roles {
		t.Errorf("opening a console changed the roles: %d -> %d", roles, got)
	}
	var ids []float64
	for _, name := range []string{"甲", "乙", "丙", "丁"} {
		r := e.post(pathAddRole, master, map[string]any{"name": name, "view": "Glance", "checksIn": true, "origin": origin})
		r.wantResult(t, "ok")
	}
	for _, role := range e.snap(master)["roles"].([]any) {
		if m := role.(map[string]any); m["type"] != "Master" {
			ids = append(ids, m["id"].(float64))
		}
	}
	seen := e.snap(master)["eventVersion"].(float64) // both devices show this version
	del := func(tok string, id float64, v float64) resp {
		return e.post(pathDeleteRole, tok, map[string]any{"version": v, "roleId": id})
	}
	del(console, ids[0], seen).wantResult(t, "ok")
	r := del(master, ids[1], seen)
	r.wantError(t, http.StatusConflict, "version_conflict")
	wantKeys(t, "409", r.body, "error", "snapshot")
	latest := r.snapshot(t)["eventVersion"].(float64)
	if latest <= seen {
		t.Errorf("the 409 snapshot's version %v is not newer than %v", latest, seen)
	}
	del(master, ids[1], latest).wantResult(t, "ok")
	// And the other way round: the Master first, the console second.
	r = del(console, ids[2], latest)
	r.wantError(t, http.StatusConflict, "version_conflict")
	del(console, ids[2], r.snapshot(t)["eventVersion"].(float64)).wantResult(t, "ok")
}

// US-CO-3.2: the company's new event follows the invite route's rules: a
// name of 1 to 50 characters; today in Taipei through two years ahead.
func TestUS_CO_3_2_NewEventRules(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	for _, c := range []struct{ name, date string }{
		{strings.Repeat("婚", 51), "2027-06-30"},
		{"   ", "2027-06-30"},
		{"婚\n禮", "2027-06-30"},
		{"婚禮", "2029-06-17"}, // one day past two years
		{"婚禮", "2027-02-30"},
		{"婚禮", "2027-6-30"},
	} {
		e.post(pathCompanyEvents, session, map[string]any{"name": c.name, "date": c.date}).
			wantError(t, http.StatusUnprocessableEntity, "invalid_event")
	}
	if n := e.count(`SELECT count(*) FROM events WHERE organization_id IS NOT NULL`); n != 0 {
		t.Errorf("a refused event was written: %d", n)
	}
	e.post(pathCompanyEvents, session, map[string]any{"name": strings.Repeat("婚", 50), "date": "2029-06-16"}).wantResult(t, "ok")
	// The zone is Taipei's: at 00:30 on the 6th there it is still the 5th by
	// UTC; the 5th is refused all the same, the 6th taken.
	e.setClock(at(0, 30))
	e.post(pathCompanyEvents, session, map[string]any{"name": "昨天", "date": "2027-06-15"}).wantError(t, http.StatusUnprocessableEntity, "invalid_event")
	r := e.post(pathCompanyEvents, session, map[string]any{"name": "今天", "date": "2027-06-16"})
	r.wantResult(t, "ok")
	snap := e.snap(r.body["masterToken"].(string))
	if ev := snap["event"].(map[string]any); ev["timezone"] != "Asia/Taipei" || ev["date"] != "2027-06-16" {
		t.Errorf("event = %v", ev)
	}
}
