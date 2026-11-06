package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
)

const (
	pathAdminOrgs     = "/api/admin/organizations"
	pathCompanyLogin  = "/api/company/login"
	pathCompanyLogout = "/api/company/logout"
	pathCompany       = "/api/company"
	pathMemberAdd     = "/api/company/members/add"
	pathMemberRemove  = "/api/company/members/remove"
	pathCompanyEvents = "/api/company/events"
	pathOpenConsole   = "/api/company/events/open"
)

// company makes the company 甲 with the member a@x.tw from the admin page
// and logs that member in; it returns the admin link and the login link.
func (e *env) company() (admin, session string) {
	e.t.Helper()
	admin = e.adminLink()
	e.post(pathAdminOrgs, admin, map[string]any{"name": "甲", "email": "a@x.tw"}).wantResult(e.t, "ok")
	return admin, e.login("a@x.tw")
}

func (e *env) login(email string) string {
	e.t.Helper()
	r := e.post(pathCompanyLogin, "", map[string]any{"email": email})
	r.wantResult(e.t, "ok")
	tok, _ := r.body["loginToken"].(string)
	if auth.CheckToken(tok) != nil {
		e.t.Fatalf("loginToken = %q", tok)
	}
	return tok
}

// companyEvent creates an event on the company page and returns its id and
// Master token.
func (e *env) companyEvent(session, name string) (int64, string) {
	e.t.Helper()
	r := e.post(pathCompanyEvents, session, map[string]any{"name": name, "date": "2027-06-20"})
	r.wantResult(e.t, "ok")
	wantKeys(e.t, "new event", r.body, "result", "eventId", "masterToken", "recoveryCode")
	id, _ := r.body["eventId"].(float64)
	tok, _ := r.body["masterToken"].(string)
	return int64(id), tok
}

func (e *env) openConsole(session string, eventID int64) resp {
	e.t.Helper()
	return e.post(pathOpenConsole, session, map[string]any{"eventId": eventID})
}

func (e *env) console(session string, eventID int64) string {
	e.t.Helper()
	r := e.openConsole(session, eventID)
	r.wantResult(e.t, "ok")
	wantKeys(e.t, "console", r.body, "result", "consoleToken")
	tok, _ := r.body["consoleToken"].(string)
	return tok
}

func memberIDs(t *testing.T, r resp) map[string]int64 {
	t.Helper()
	ms, _ := r.body["members"].([]any)
	out := map[string]int64{}
	for _, m := range ms {
		mm := m.(map[string]any)
		wantKeys(t, "member", mm, "id", "email")
		out[mm["email"].(string)] = int64(mm["id"].(float64))
	}
	return out
}

// US-O-11.1, 11.2: the admin page creates a company; a stranger is the same
// 404 whatever the body; the name and email are checked.
func TestUS_O_11_1_AdminCreatesACompany(t *testing.T) {
	e := newEnv(t)
	admin := e.adminLink()
	for _, tok := range []string{"", newToken(t), e.master} {
		e.do(http.MethodPost, pathAdminOrgs, tok, `{"x":1}`).wantError(t, http.StatusNotFound, "not_found")
	}
	e.do(http.MethodPost, pathAdminOrgs, admin, `{"name":"甲"}`).wantError(t, http.StatusBadRequest, "bad_request")
	for _, c := range []struct {
		name, email, code string
	}{
		{"", "a@x.tw", "invalid_organization"},
		{strings.Repeat("甲", 51), "a@x.tw", "invalid_organization"},
		{"甲", "a@x", "invalid_email"},
		{"甲", "王@x.tw", "invalid_email"},
		{"甲", "a@x.tw, b@x.tw", "invalid_email"},
	} {
		e.post(pathAdminOrgs, admin, map[string]any{"name": c.name, "email": c.email}).
			wantError(t, http.StatusUnprocessableEntity, c.code)
	}
	r := e.post(pathAdminOrgs, admin, map[string]any{"name": strings.Repeat("甲", 50), "email": "  A@X.tw "})
	r.wantResult(t, "ok")
	wantKeys(t, "organization", r.body, "result", "id")
	e.post(pathAdminOrgs, admin, map[string]any{"name": "乙", "email": "a@x.tw"}).wantError(t, http.StatusUnprocessableEntity, "email_taken")
	// The email was stored trimmed and in lower case (US-O-11.2).
	e.login("a@x.tw")
}

// US-O-11.5: the daily limit of the admin page.
func TestUS_O_11_5_TooManyOrganizations(t *testing.T) {
	e := newEnv(t)
	admin := e.adminLink()
	for i := range 5 {
		e.post(pathAdminOrgs, admin, map[string]any{"name": "甲", "email": fmt.Sprintf("o%d@x.tw", i)}).wantResult(t, "ok")
	}
	e.post(pathAdminOrgs, admin, map[string]any{"name": "甲", "email": "over@x.tw"}).
		wantError(t, http.StatusUnprocessableEntity, "too_many_organizations")
}

// US-O-11.3: the overview lists the companies without any email, and each
// event's company or null.
func TestUS_O_11_3_OverviewHasCompanies(t *testing.T) {
	e := newEnv(t)
	admin, session := e.company()
	e.companyEvent(session, "甲婚宴")
	r := e.do(http.MethodGet, pathAdminOverview, admin, "")
	r.wantResult(t, "ok")
	orgs, _ := r.body["organizations"].([]any)
	if len(orgs) != 1 {
		t.Fatalf("organizations = %v", orgs)
	}
	org := orgs[0].(map[string]any)
	wantKeys(t, "organization", org, "id", "name", "members", "events", "createdAt")
	if org["name"] != "甲" || org["members"] != 1.0 || org["events"] != 1.0 {
		t.Errorf("organization = %v", org)
	}
	got := map[string]any{}
	for _, ev := range r.body["events"].([]any) {
		m := ev.(map[string]any)
		got[m["name"].(string)] = m["organization"]
	}
	if got["甲婚宴"] != "甲" || got["婚宴"] != nil {
		t.Errorf("event companies = %v", got)
	}
	if strings.Contains(r.Body.String(), "@") {
		t.Errorf("the overview carries an email: %s", r.Body.String())
	}
}

// US-CO-1.2, 1.3: logging in with an email; one of no company or not an
// email answers the same.
func TestUS_CO_1_2_Login(t *testing.T) {
	e := newEnv(t)
	e.company()
	for _, email := range []string{"nobody@x.tw", "not-an-email", ""} {
		e.post(pathCompanyLogin, "", map[string]any{"email": email}).wantError(t, http.StatusUnprocessableEntity, "not_member")
	}
	e.do(http.MethodPost, pathCompanyLogin, "", `{"email":"a@x.tw","x":1}`).wantError(t, http.StatusBadRequest, "bad_request")
	session := e.login(" A@x.TW ")
	r := e.do(http.MethodGet, pathCompany, session, "")
	r.wantResult(t, "ok")
	wantKeys(t, "company", r.body, "result", "organization", "me", "members", "events")
	if r.body["organization"].(map[string]any)["name"] != "甲" || memberIDs(t, r)["a@x.tw"] != int64(r.body["me"].(float64)) {
		t.Errorf("company = %v", r.body)
	}
}

// US-CO-1.5: every refused login link is the same 404, before the body.
func TestUS_CO_1_5_OnlyALoginLinkOpensTheCompanyPage(t *testing.T) {
	e := newEnv(t)
	admin, session := e.company()
	_, master := e.companyEvent(session, "甲婚宴")
	for _, tok := range []string{"", newToken(t), e.master, e.stylist, admin, master} {
		e.do(http.MethodGet, pathCompany, tok, "").wantError(t, http.StatusNotFound, "not_found")
		for _, p := range []string{pathCompanyLogout, pathMemberAdd, pathMemberRemove, pathCompanyEvents, pathOpenConsole} {
			e.do(http.MethodPost, p, tok, `{"x":1}`).wantError(t, http.StatusNotFound, "not_found")
		}
	}
	// A login link is no role's link.
	e.get(session).wantResult(t, "invalid")
	e.do(http.MethodPost, pathMemberAdd, session, `{"x":1}`).wantError(t, http.StatusBadRequest, "bad_request")
}

// US-CO-1.4: logging out ends this link only.
func TestUS_CO_1_4_Logout(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	other := e.login("a@x.tw")
	e.do(http.MethodPost, pathCompanyLogout, session, `{}`).wantResult(t, "ok")
	e.do(http.MethodGet, pathCompany, session, "").wantError(t, http.StatusNotFound, "not_found")
	e.do(http.MethodGet, pathCompany, other, "").wantResult(t, "ok")
}

// US-CO-2.1–2.4: adding and removing members.
func TestUS_CO_2_AddAndRemoveMembers(t *testing.T) {
	e := newEnv(t)
	admin, session := e.company()
	e.post(pathAdminOrgs, admin, map[string]any{"name": "乙", "email": "z@x.tw"}).wantResult(t, "ok")
	e.post(pathMemberAdd, session, map[string]any{"email": "bad"}).wantError(t, http.StatusUnprocessableEntity, "invalid_email")
	e.post(pathMemberAdd, session, map[string]any{"email": "z@x.tw"}).wantError(t, http.StatusUnprocessableEntity, "email_taken")
	r := e.post(pathMemberAdd, session, map[string]any{"email": "B@x.tw"})
	r.wantResult(t, "ok")
	ids := memberIDs(t, r)
	if len(ids) != 2 || ids["b@x.tw"] == 0 {
		t.Fatalf("members = %v", ids)
	}
	b := e.login("b@x.tw")
	ev, _ := e.companyEvent(session, "甲婚宴")
	console := e.console(b, ev)
	e.post(pathMemberRemove, session, map[string]any{"memberId": ids["a@x.tw"]}).
		wantError(t, http.StatusUnprocessableEntity, "cannot_remove_self")
	other := e.login("z@x.tw")
	zID := memberIDs(t, e.do(http.MethodGet, pathCompany, other, ""))["z@x.tw"]
	e.post(pathMemberRemove, session, map[string]any{"memberId": zID}).wantError(t, http.StatusNotFound, "not_found")
	r = e.post(pathMemberRemove, session, map[string]any{"memberId": ids["b@x.tw"]})
	r.wantResult(t, "ok")
	if got := memberIDs(t, r); len(got) != 1 {
		t.Errorf("members after removal = %v", got)
	}
	e.do(http.MethodGet, pathCompany, b, "").wantError(t, http.StatusNotFound, "not_found")
	e.get(console).wantResult(t, "invalid")
}

// US-CO-2.5: the member limit's answer.
func TestUS_CO_2_5_TooManyMembers(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	for i := 1; i < 50; i++ {
		e.post(pathMemberAdd, session, map[string]any{"email": fmt.Sprintf("m%d@x.tw", i)}).wantResult(t, "ok")
	}
	e.post(pathMemberAdd, session, map[string]any{"email": "over@x.tw"}).wantError(t, http.StatusUnprocessableEntity, "too_many_members")
}

// US-CO-3.1–3.5: the company's events.
func TestUS_CO_3_CompanyEvents(t *testing.T) {
	e := newEnv(t)
	admin, session := e.company()
	for _, c := range []map[string]any{
		{"name": "", "date": "2027-06-20"},
		{"name": "甲", "date": "2027-06-05"}, // before today
		{"name": "甲", "date": "2027/06/20"},
	} {
		e.post(pathCompanyEvents, session, c).wantError(t, http.StatusUnprocessableEntity, "invalid_event")
	}
	ev, master := e.companyEvent(session, "甲婚宴")
	snap := e.snap(master)
	if snap["event"].(map[string]any)["name"] != "甲婚宴" {
		t.Errorf("the new event's Master snapshot = %v", snap["event"])
	}
	r := e.do(http.MethodGet, pathCompany, session, "")
	evs, _ := r.body["events"].([]any)
	if len(evs) != 1 {
		t.Fatalf("events = %v", evs)
	}
	got := evs[0].(map[string]any)
	wantKeys(t, "event", got, "id", "name", "date", "status")
	if got["id"] != float64(ev) || got["name"] != "甲婚宴" || got["date"] != "2027-06-20" || got["status"] != "Draft" {
		t.Errorf("event = %v", got)
	}
	e.post(pathAdminOrgs, admin, map[string]any{"name": "乙", "email": "z@x.tw"}).wantResult(t, "ok")
	other := e.login("z@x.tw")
	for _, id := range []int64{ev, e.eventID, 999} {
		e.openConsole(other, id).wantError(t, http.StatusNotFound, "not_found")
	}
	for range 19 {
		e.companyEvent(session, "甲")
	}
	e.post(pathCompanyEvents, session, map[string]any{"name": "甲", "date": "2027-06-20"}).
		wantError(t, http.StatusUnprocessableEntity, "too_many_events")
}

// US-CO-4.1, 4.8: a console link is the event's Master; its snapshot is the
// Master link's, and it writes as the Master without becoming any role's
// link in use.
func TestUS_CO_4_8_ConsoleLinkIsTheMaster(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	ev, master := e.companyEvent(session, "甲婚宴")
	console := e.console(session, ev)
	viaMaster := e.get(master) // marks the Master link seen before both reads
	viaMaster = e.get(master)
	viaConsole := e.get(console)
	viaConsole.wantResult(t, "ok")
	if a, b := strings.Replace(viaConsole.Body.String(), viaConsole.Header().Get("X-Server-Time"), "", 1),
		strings.Replace(viaMaster.Body.String(), viaMaster.Header().Get("X-Server-Time"), "", 1); a != b {
		t.Errorf("snapshots differ:\nconsole %s\nmaster  %s", a, b)
	}
	if roles := e.snap(master)["roles"].([]any); roles[0].(map[string]any)["lastSeenAt"] == nil {
		t.Fatalf("the Master link's own use is not recorded: %v", roles)
	}
	r := e.post(pathAddRole, console, map[string]any{"name": "攝影", "view": "Glance", "checksIn": true, "origin": origin})
	r.wantResult(t, "ok")
	// A preview of no role through the console link is 404, not 500.
	e.post(pathPreviewRole, console, map[string]any{"roleId": 999999}).wantError(t, http.StatusNotFound, "not_found")
	var actor string
	if err := e.db().QueryRow(`SELECT actor FROM action_logs WHERE event_id = ? AND type = 'role.add'`, ev).Scan(&actor); err != nil ||
		!strings.HasPrefix(actor, "member:") {
		t.Errorf("logged actor = %q, %v", actor, err)
	}
}

// US-CO-4.5: replacing the Master link with the recovery code ends the
// event's console links.
func TestUS_CO_4_5_RecoveryEndsConsoles(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	r := e.post(pathCompanyEvents, session, map[string]any{"name": "甲婚宴", "date": "2027-06-20"})
	r.wantResult(t, "ok")
	console := e.console(session, int64(r.body["eventId"].(float64)))
	e.post("/api/recover/master", "", map[string]any{"recoveryCode": r.body["recoveryCode"]}).wantResult(t, "ok")
	e.get(console).wantResult(t, "invalid")
}

// US-CO-1.7: in maintenance the company page is closed; an open console
// passes as the Master's link does.
func TestUS_CO_1_7_MaintenanceClosesTheCompanyPage(t *testing.T) {
	e := newEnv(t)
	admin, session := e.company()
	ev, _ := e.companyEvent(session, "甲婚宴")
	console := e.console(session, ev)
	e.maintenance()
	for _, r := range []resp{
		e.post(pathCompanyLogin, "", map[string]any{"email": "a@x.tw"}),
		e.do(http.MethodGet, pathCompany, session, ""),
		e.openConsole(session, ev),
		e.post(pathAdminOrgs, admin, map[string]any{"name": "乙", "email": "z@x.tw"}),
	} {
		r.wantError(t, http.StatusServiceUnavailable, "maintenance")
	}
	e.get(console).wantResult(t, "ok")
}

// US-O-12.6: while member links are closed every one is 503, never
// "invalid"; role links work as ever.
func TestUS_O_12_6_ClosedMemberLinksAre503(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	ev, master := e.companyEvent(session, "甲婚宴")
	console := e.console(session, ev)
	e.st.CloseMemberAccess()
	for what, r := range map[string]resp{
		"login":    e.post(pathCompanyLogin, "", map[string]any{"email": "a@x.tw"}),
		"company":  e.do(http.MethodGet, pathCompany, session, ""),
		"open":     e.openConsole(session, ev),
		"snapshot": e.get(console),
		"write":    e.post(pathAddRole, console, map[string]any{"name": "攝影", "view": "Glance", "checksIn": true, "origin": origin}),
	} {
		if r.Code != http.StatusServiceUnavailable || r.body["error"] != "maintenance" {
			t.Errorf("%s: %d %s, want 503 maintenance", what, r.Code, r.Body.String())
		}
	}
	e.get(master).wantResult(t, "ok")
	e.get(e.master).wantResult(t, "ok")
}

// A console link's own use never shows as the Master link's (api.md 10.2).
func TestUS_CO_4_3_ConsoleUseIsNoRolesLastSeen(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	ev, master := e.companyEvent(session, "甲婚宴")
	console := e.console(session, ev)
	e.get(console).wantResult(t, "ok")
	e.get(console).wantResult(t, "ok")
	// Only the console link was used: no role, the Master included, has a lastSeenAt.
	owner, err := e.st.LookupToken(bg, auth.HashToken(master), e.clock())
	if err != nil || owner.RoleType != domain.Master {
		t.Fatal(err)
	}
	r := e.get(console)
	for _, role := range r.snapshot(t)["roles"].([]any) {
		if seen := role.(map[string]any)["lastSeenAt"]; seen != nil {
			t.Errorf("role %v lastSeenAt = %v", role.(map[string]any)["name"], seen)
		}
	}
}

// US-CO-1.7, US-O-11.4: in maintenance a Master's or console link in the
// header opens neither the login (which reads no header), the company
// page nor the admin page.
func TestUS_CO_1_7_MaintenanceIgnoresAMastersHeader(t *testing.T) {
	e := newEnv(t)
	admin, session := e.company()
	ev, master := e.companyEvent(session, "甲婚宴")
	console := e.console(session, ev)
	e.maintenance()
	for _, tok := range []string{e.master, master, console, admin, session} {
		for _, r := range []resp{
			e.post(pathCompanyLogin, tok, map[string]any{"email": "a@x.tw"}),
			e.do(http.MethodGet, pathCompany, tok, ""),
			e.post(pathOpenConsole, tok, map[string]any{"eventId": ev}),
			e.post(pathAdminOrgs, tok, map[string]any{"name": "乙", "email": "z@x.tw"}),
			e.do(http.MethodGet, pathAdminOverview, tok, ""),
		} {
			r.wantError(t, http.StatusServiceUnavailable, "maintenance")
		}
	}
	var n int
	if err := e.db().QueryRow(`SELECT count(*) FROM member_sessions`).Scan(&n); err != nil || n != 1 {
		t.Errorf("login links = %d (%v), want only the one made before maintenance", n, err)
	}
	e.get(e.master).wantResult(t, "ok")
	e.post("/api/recover/master", "", map[string]any{"recoveryCode": "x"}).wantError(t, http.StatusUnprocessableEntity, "wrong_recovery_code")
}

// US-O-12.6: while member links are closed every company endpoint is 503
// before anything else is checked, a bad email or a missing header too.
func TestUS_O_12_6_ClosedBeforeAnyCheck(t *testing.T) {
	e := newEnv(t)
	e.company()
	e.st.CloseMemberAccess()
	for _, r := range []resp{
		e.post(pathCompanyLogin, "", map[string]any{"email": "not an email"}),
		e.do(http.MethodPost, pathCompanyLogin, "", `{"x":1}`),
		e.do(http.MethodGet, pathCompany, "", ""),
		e.do(http.MethodPost, pathMemberAdd, newToken(t), `{}`),
	} {
		r.wantError(t, http.StatusServiceUnavailable, "maintenance")
	}
	admin := e.adminLink()
	e.do(http.MethodGet, pathAdminOverview, admin, "").wantResult(t, "ok")
}

// US-CO-2.6: adding and removing a member logs both numbers, never an
// email or a link.
func TestUS_CO_2_6_MemberChangesAreLogged(t *testing.T) {
	e := newEnv(t)
	_, session := e.company()
	r := e.post(pathMemberAdd, session, map[string]any{"email": "b@x.tw"})
	ids := memberIDs(t, r)
	e.post(pathMemberRemove, session, map[string]any{"memberId": ids["b@x.tw"]}).wantResult(t, "ok")
	e.post(pathMemberRemove, session, map[string]any{"memberId": ids["a@x.tw"]}).
		wantError(t, http.StatusUnprocessableEntity, "cannot_remove_self")
	logs := e.logs.String()
	for _, want := range []string{
		fmt.Sprintf(`"msg":"member.add","by":%d,"member":%d`, ids["a@x.tw"], ids["b@x.tw"]),
		fmt.Sprintf(`"msg":"member.remove","by":%d,"member":%d`, ids["a@x.tw"], ids["b@x.tw"]),
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("no %s in %s", want, logs)
		}
	}
	if strings.Count(logs, `"msg":"member.`) != 2 {
		t.Errorf("a refused removal was logged: %s", logs)
	}
	if strings.Contains(logs, "@") || strings.Contains(logs, session) {
		t.Errorf("the log carries an email or a link: %s", logs)
	}
}
