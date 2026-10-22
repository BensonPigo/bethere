package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

const (
	pathCheckIn      = "/api/checkin"
	pathProxyCheckIn = "/api/roles/checkin"
	pathSkip         = "/api/cues/skip"
	pathInsert       = "/api/cues/insert"
	pathAssign       = "/api/cues/roles"
	pathEdit         = "/api/cues/edit"
	pathReissue      = "/api/roles/reissue"
	pathRevoke       = "/api/roles/revoke"
)

func jsonBody(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func (e *env) post(path, token string, v map[string]any) resp {
	e.t.Helper()
	return e.do(http.MethodPost, path, token, jsonBody(v))
}

func (e *env) checkIn(token string, on bool) resp {
	e.t.Helper()
	return e.post(pathCheckIn, token, map[string]any{"checkedIn": on})
}

func (e *env) proxyCheckIn(roleID int64, on bool) resp {
	e.t.Helper()
	return e.post(pathProxyCheckIn, e.master, map[string]any{"roleId": roleID, "checkedIn": on})
}

func (e *env) skip(cueID int64) resp {
	e.t.Helper()
	return e.post(pathSkip, e.master, map[string]any{"version": e.version(), "cueId": cueID})
}

func assignment(roleID int64, note any) map[string]any {
	return map[string]any{"roleId": roleID, "roleNote": note}
}

// insert adds 長輩致詞 at 12:00 for 10 minutes after the given cue.
func (e *env) insert(afterCueID int64, roles ...map[string]any) resp {
	e.t.Helper()
	return e.post(pathInsert, e.master, insertBody(e.version(), afterCueID, at(12, 0), roles...))
}

func insertBody(version, afterCueID int64, start time.Time, roles ...map[string]any) map[string]any {
	if roles == nil {
		roles = []map[string]any{}
	}
	return map[string]any{"version": version, "afterCueId": afterCueID, "start": start.UTC().Format(time.RFC3339),
		"durationMin": 10, "title": "長輩致詞", "notePublic": nil, "roles": roles}
}

func (e *env) assign(cueID int64, roles ...map[string]any) resp {
	e.t.Helper()
	if roles == nil {
		roles = []map[string]any{}
	}
	return e.post(pathAssign, e.master, map[string]any{"version": e.version(), "cueId": cueID, "roles": roles})
}

func (e *env) roleItem(t *testing.T, s map[string]any, id int64) map[string]any {
	t.Helper()
	for _, r := range s["roles"].([]any) {
		if m := r.(map[string]any); m["id"] == float64(id) {
			return m
		}
	}
	t.Fatalf("role %d not in %v", id, s["roles"])
	return nil
}

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db().QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// foreign is another event in the same database, for the ownership rules
// of api.md section 6: its Master, Host and Stylist roles and a cue that
// has already finished.
type foreign struct {
	masterRole, host, stylist, doneCue int64
}

// otherEvent creates that event on 2027-06-<day>; a day before the 6th
// has ended by the env's clock.
func (e *env) otherEvent(day int) foreign {
	e.t.Helper()
	created := at(0, 0).AddDate(0, -1, 0)
	tok := newToken(e.t)
	id, err := e.st.CreateEvent(bg, store.NewEvent{
		Name: "別場", Date: domain.Date{Year: 2027, Month: time.June, Day: day}, TimeZone: "Asia/Taipei",
		MasterName: "別場總召", MasterTokenHash: auth.HashToken(tok), RecoveryCodeHash: auth.HashToken(newToken(e.t)),
	}, created)
	if err != nil {
		e.t.Fatal(err)
	}
	owner, err := e.st.LookupToken(bg, auth.HashToken(tok), created)
	if err != nil {
		e.t.Fatal(err)
	}
	f := foreign{masterRole: owner.RoleID}
	if f.host, err = e.st.AddRole(bg, id, "別場新人", domain.Host, owner.Actor, created); err != nil {
		e.t.Fatal(err)
	}
	if f.stylist, err = e.st.AddRole(bg, id, "別場新秘", domain.Stylist, owner.Actor, created); err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.ImportRundown(bg, id, []store.ImportRow{
		{Line: 2, Seq: 1, Title: "別場環節", StartHour: 9, DurationMin: 10, Roles: []string{"別場新秘"}},
	}, owner.Actor, created); err != nil {
		e.t.Fatal(err)
	}
	cues, err := e.st.Cues(bg, id)
	if err != nil {
		e.t.Fatal(err)
	}
	f.doneCue = cues[0].ID
	e.exec(`UPDATE cues SET status = 'Completed' WHERE id = ?`, f.doneCue)
	return f
}

// ---------------------------------------------------------------- US-X-5, US-M-1

func TestUS_X_5_OwnCheckIn(t *testing.T) {
	e := newEnv(t)
	v := e.version()
	r := e.checkIn(e.stylist, true)
	r.wantResult(t, "ok")
	if s := r.snapshot(t); s["myCheckIn"] != true {
		t.Errorf("myCheckIn = %v", s["myCheckIn"])
	}
	if e.roleItem(t, e.snap(e.master), e.stylistRole)["checkedIn"] != true {
		t.Error("Master does not see the check-in (US-X-5.6)")
	}
	// Setting the same state again writes nothing (US-X-5.4).
	e.checkIn(e.stylist, true).wantResult(t, "ok")
	if n := e.count(`SELECT count(*) FROM check_ins WHERE role_id = ?`, e.stylistRole); n != 1 {
		t.Errorf("%d check-in rows, want 1", n)
	}
	if n := e.count(`SELECT count(*) FROM check_ins WHERE role_id = ? AND by_role_id = ?`, e.stylistRole, e.stylistRole); n != 1 {
		t.Error("own check-in not by the role itself")
	}
	// Undo, then check in again.
	if s := e.checkIn(e.stylist, false).snapshot(t); s["myCheckIn"] != false {
		t.Error("check-out did not take")
	}
	if s := e.checkIn(e.stylist, true).snapshot(t); s["myCheckIn"] != true {
		t.Error("check-in again did not take")
	}
	// Other roles are untouched.
	if e.snap(e.stylistBTok)["myCheckIn"] != false {
		t.Error("新秘B checked in by 新秘")
	}
	// No version change (US-M-1.9, ADR-0023).
	if e.version() != v {
		t.Errorf("version %d → %d", v, e.version())
	}
}

// US-X-5.3: the role is the token's alone.
func TestUS_X_5_3_OwnCheckInCannotNameAnotherRole(t *testing.T) {
	e := newEnv(t)
	e.post(pathCheckIn, e.stylist, map[string]any{"checkedIn": true, "roleId": e.stylistB}).
		wantError(t, http.StatusBadRequest, "bad_request")
	for _, body := range []string{`{}`, `{"checkedIn":1}`, `{"checkedIn":"true"}`, `{"checkedIn":null}`,
		`{"checkedIn":true,"checkedIn":false}`, `{"CheckedIn":true}`, `{"checkedIn":true,"version":1}`} {
		e.do(http.MethodPost, pathCheckIn, e.stylist, body).wantError(t, http.StatusBadRequest, "bad_request")
	}
	if n := e.count(`SELECT count(*) FROM check_ins`); n != 0 {
		t.Errorf("%d check-ins written", n)
	}
	host := e.addRoleWithToken(domain.Host)
	for _, tok := range []string{e.master, host} {
		e.checkIn(tok, true).wantError(t, http.StatusForbidden, "forbidden")
	}
}

// US-X-5.5: in draft and live; after the end the caller's own event ended.
func TestUS_X_5_5_CheckInDraftLiveAndEnded(t *testing.T) {
	e := newEnv(t)
	e.checkIn(e.stylist, true).wantResult(t, "ok") // draft
	e.live()
	e.checkIn(e.stylist, false).wantResult(t, "ok")
	e.setClock(time.Date(2027, 6, 7, 4, 0, 0, 0, taipei))
	e.checkIn(e.stylist, true).wantResult(t, "ended")
}

// US-M-1.9: a check-in between the Master's read and write is no conflict.
func TestUS_M_1_9_CheckInDoesNotConflictWithTheMaster(t *testing.T) {
	e := newEnv(t)
	e.live()
	v := e.version()
	e.checkIn(e.stylist, true).wantResult(t, "ok")
	e.proxyCheckIn(e.stylistB, true).wantResult(t, "ok")
	e.do(http.MethodPost, pathAdvance, e.master, versionBody(v)).wantResult(t, "ok")
}

func TestUS_M_1_8_ProxyCheckIn(t *testing.T) {
	e := newEnv(t)
	r := e.proxyCheckIn(e.stylistRole, true)
	r.wantResult(t, "ok")
	if e.roleItem(t, r.snapshot(t), e.stylistRole)["checkedIn"] != true {
		t.Error("proxy check-in not in the Master snapshot")
	}
	if n := e.count(`SELECT count(*) FROM check_ins WHERE role_id = ? AND by_role_id = ?`, e.stylistRole, e.masterRole); n != 1 {
		t.Error("byRoleId is not the Master")
	}
	if e.snap(e.stylist)["myCheckIn"] != true {
		t.Error("role does not see the proxy check-in")
	}
	e.proxyCheckIn(e.stylistRole, false).wantResult(t, "ok")
	if e.snap(e.stylist)["myCheckIn"] != false {
		t.Error("proxy check-out did not take")
	}
	if n := e.count(`SELECT count(*) FROM action_logs WHERE type = 'checkin.set'`); n != 2 {
		t.Errorf("%d check-in log rows, want 2", n)
	}
	// Only roles with cues of their own check in.
	e.addRoleWithToken(domain.Host)
	var hostRole int64
	e.db().QueryRow(`SELECT id FROM roles WHERE type = 'Host'`).Scan(&hostRole)
	for _, id := range []int64{e.masterRole, hostRole} {
		e.proxyCheckIn(id, true).wantError(t, http.StatusUnprocessableEntity, "not_checkin_role")
	}
	// Roles may not check others in.
	e.post(pathProxyCheckIn, e.stylist, map[string]any{"roleId": e.stylistB, "checkedIn": true}).
		wantError(t, http.StatusForbidden, "forbidden")
}

// US-M-1.6: when each role's link was last used, to the minute, and
// whether it has one.
func TestUS_M_1_6_RolesCarryLastSeenAndLink(t *testing.T) {
	e := newEnv(t)
	e.setClock(at(9, 0).Add(42 * time.Second))
	e.get(e.stylist)
	e.setClock(at(9, 5))
	s := e.snap(e.master)
	st := e.roleItem(t, s, e.stylistRole)
	if st["lastSeenAt"] != "2027-06-06T01:00:00.000Z" || st["hasLink"] != true {
		t.Errorf("新秘 = %v, want last seen 09:00 to the minute, has a link", st)
	}
	if b := e.roleItem(t, s, e.stylistB); b["lastSeenAt"] != nil {
		t.Errorf("新秘B never opened its link: %v", b)
	}
	e.post(pathRevoke, e.master, map[string]any{"roleId": e.stylistRole}).wantResult(t, "ok")
	st = e.roleItem(t, e.snap(e.master), e.stylistRole)
	if st["hasLink"] != false || st["lastSeenAt"] != nil {
		t.Errorf("after revoke 新秘 = %v, want no link and no last use", st)
	}
}

// ---------------------------------------------------------------- US-X-6

// US-X-6.4, US-X-6.5: Media and StageAV get the role snapshot with the
// same keys as Stylist, and the title of the cue in progress only.
func TestUS_X_6_MediaAndStageAVSnapshots(t *testing.T) {
	e := newEnv(t)
	media, av := e.addRoleWithToken(domain.Media), e.addRoleWithToken(domain.StageAV)
	e.live()
	stylistKeys := keys(e.snap(e.stylist))
	for _, tok := range []string{media, av} {
		s := e.snap(tok)
		if got := keys(s); strings.Join(got, ",") != strings.Join(stylistKeys, ",") {
			t.Errorf("keys = %v, want %v", got, stylistKeys)
		}
		if s["role"].(map[string]any)["view"] != "Glance" {
			t.Errorf("view = %v", s["role"])
		}
		// 新娘化妝 is in progress and not theirs: its title only.
		if s["currentTitle"] != "新娘化妝" {
			t.Errorf("currentTitle = %v", s["currentTitle"])
		}
		body := e.get(tok).Body.String()
		for _, hidden := range []string{"新娘房", "B專屬備註", "一進預備", "敬酒", `"activeCueId"`} {
			if strings.Contains(body, hidden) {
				t.Errorf("response contains %q: %s", hidden, body)
			}
		}
	}
	e.mustWrite(pathAdvance) // 迎賓, assigned to both by addRoleWithToken
	if s := e.snap(media); s["currentTitle"] != "迎賓" || byTitle(t, s, "迎賓")["status"] != "InProgress" {
		t.Errorf("media snapshot = %v", s)
	}
}

// ---------------------------------------------------------------- US-M-6

func TestUS_M_6_Skip(t *testing.T) {
	e := newEnv(t)
	// Draft: the morning of the event day, before the first advance (US-M-6.3).
	v := e.version()
	r := e.skip(e.cue["迎賓"])
	r.wantResult(t, "ok")
	s := r.snapshot(t)
	if byTitle(t, s, "迎賓")["status"] != "Skipped" || e.version() != v+1 {
		t.Fatalf("skip = %v, version %d → %d", byTitle(t, s, "迎賓"), v, e.version())
	}
	if d := s["undo"].(map[string]any)["description"]; d != "撤銷：略過『迎賓』" {
		t.Errorf("undo description = %v", d)
	}
	if n := e.count(`SELECT count(*) FROM action_logs WHERE type = 'cue.skip'`); n != 1 {
		t.Errorf("%d cue.skip rows", n)
	}
	// Undo in draft (US-M-6.4).
	s = e.mustWrite(pathUndo)
	if byTitle(t, s, "迎賓")["status"] != "Pending" {
		t.Error("undo did not restore the cue")
	}
	e.skip(e.cue["迎賓"]).wantResult(t, "ok")

	// Advance passes over it (US-M-6.2).
	e.live()                     // 新娘化妝
	s = e.mustWrite(pathAdvance) // not 迎賓
	if byTitle(t, s, "一進預備")["status"] != "InProgress" {
		t.Errorf("advance did not pass over the skipped cue: %v", cues(t, s))
	}
	// Only pending cues (US-M-6.3).
	for _, title := range []string{"新娘化妝", "一進預備", "迎賓"} {
		e.skip(e.cue[title]).wantError(t, http.StatusUnprocessableEntity, "not_pending")
	}
	// A stale version (US-M-6.6), another role, an unknown cue.
	e.post(pathSkip, e.master, map[string]any{"version": e.version() - 1, "cueId": e.cue["敬酒"]}).
		wantError(t, http.StatusConflict, "version_conflict")
	e.post(pathSkip, e.stylist, map[string]any{"version": e.version(), "cueId": e.cue["敬酒"]}).
		wantError(t, http.StatusForbidden, "forbidden")
	e.skip(9999).wantError(t, http.StatusNotFound, "not_found")
}

// US-M-6.5: a skipped cue leaves the role's next cue.
func TestUS_M_6_5_SkippedCueIsNotNext(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.skip(e.cue["一進預備"]).wantResult(t, "ok")
	if c := byTitle(t, e.snap(e.stylist), "一進預備"); c["status"] != "Skipped" {
		t.Errorf("新秘 sees 一進預備 as %v", c["status"])
	}
}

// ---------------------------------------------------------------- US-M-7

func TestUS_M_7_ReissueAndRevoke(t *testing.T) {
	e := newEnv(t)
	v := e.version()
	r := e.post(pathReissue, e.master, map[string]any{"roleId": e.stylistRole})
	r.wantResult(t, "ok")
	wantKeys(t, "reissue response", r.body, "result", "snapshot", "token")
	tok, _ := r.body["token"].(string)
	if auth.CheckToken(tok) != nil {
		t.Fatalf("token = %q", tok)
	}
	// The old link is invalid at once, the new one works (US-M-7.1, 7.3).
	e.get(e.stylist).wantResult(t, "invalid")
	if s := e.snap(tok); s["role"].(map[string]any)["name"] != "新秘" {
		t.Errorf("new link opens %v", s["role"])
	}
	// The token is in that one response only: not in the snapshots, the
	// log or the version (US-M-7.6).
	if l := leaks(jsonBody(r.snapshot(t))+e.get(e.master).Body.String(), tok); len(l) > 0 {
		t.Errorf("new token in the Master snapshot: %v", l)
	}
	var logs string
	e.db().QueryRow(`SELECT group_concat(type || ' ' || actor || ' ' || payload, ';') FROM action_logs`).Scan(&logs)
	if l := leaks(logs, tok, e.stylist, e.master); len(l) > 0 {
		t.Errorf("credential in the log: %v", l)
	}
	if !strings.Contains(logs, "token.reissue") {
		t.Errorf("no token.reissue log: %s", logs)
	}
	if e.version() != v {
		t.Errorf("version %d → %d", v, e.version())
	}

	// Revoke (US-M-7.2), twice: a role without a link is revoked already.
	for range 2 {
		e.post(pathRevoke, e.master, map[string]any{"roleId": e.stylistRole}).wantResult(t, "ok")
	}
	e.get(tok).wantResult(t, "invalid")
	// A role whose link is revoked can be reissued, and the Master is
	// never told "invalid" about the target (security review P1).
	r = e.post(pathReissue, e.master, map[string]any{"roleId": e.stylistRole})
	r.wantResult(t, "ok")
	e.snap(r.body["token"].(string))
	// Host links too; never the Master's (US-M-7.4).
	var hostRole int64
	e.addRoleWithToken(domain.Host)
	e.db().QueryRow(`SELECT id FROM roles WHERE type = 'Host'`).Scan(&hostRole)
	e.post(pathRevoke, e.master, map[string]any{"roleId": hostRole}).wantResult(t, "ok")
	for _, p := range []string{pathReissue, pathRevoke} {
		e.post(p, e.master, map[string]any{"roleId": e.masterRole}).wantError(t, http.StatusUnprocessableEntity, "master_role")
		e.post(p, e.stylistBTok, map[string]any{"roleId": e.stylistRole}).wantError(t, http.StatusForbidden, "forbidden")
		e.post(p, e.master, map[string]any{"roleId": e.stylistRole, "version": 1}).wantError(t, http.StatusBadRequest, "bad_request")
	}
	e.snap(e.master)
}

// The log tells apart which Master link changed a link (access-control.md 3.4).
func TestLinkChangesLogTheMastersLink(t *testing.T) {
	e := newEnv(t)
	e.post(pathRevoke, e.master, map[string]any{"roleId": e.stylistRole}).wantResult(t, "ok")
	newMaster := e.replaceMasterLink()
	e.post(pathRevoke, newMaster, map[string]any{"roleId": e.stylistB}).wantResult(t, "ok")
	rows, err := e.db().Query(`SELECT payload FROM action_logs WHERE type = 'token.revoke' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []float64
	for rows.Next() {
		var p string
		rows.Scan(&p)
		var m map[string]any
		json.Unmarshal([]byte(p), &m)
		ids = append(ids, m["byTokenId"].(float64))
	}
	if len(ids) != 2 || ids[0] == ids[1] || ids[0] == 0 {
		t.Errorf("byTokenId = %v, want two different links", ids)
	}
}

// ---------------------------------------------------------------- US-M-8, US-X-7

func TestUS_M_8_1_InsertCue(t *testing.T) {
	e := newEnv(t)
	e.live()
	v := e.version()
	r := e.insert(e.cue["一進預備"], assignment(e.stylistRole, "麥克風"))
	r.wantResult(t, "ok")
	s := r.snapshot(t)
	var order []string
	for _, c := range cues(t, s) {
		order = append(order, c["title"].(string))
	}
	if strings.Join(order, ",") != "新娘化妝,迎賓,一進預備,長輩致詞,敬酒" {
		t.Errorf("order = %v", order)
	}
	c := byTitle(t, s, "長輩致詞")
	if c["seq"] != 4.0 || c["scheduledStart"] != "2027-06-06T04:00:00.000Z" || c["offsetMin"] != 0.0 ||
		c["status"] != "Pending" || byTitle(t, s, "敬酒")["seq"] != 5.0 || e.version() != v+1 {
		t.Errorf("inserted = %v", c)
	}
	// US-M-8.2 and US-X-7.1: the assigned role sees it with a notice.
	rs := e.snap(e.stylist)
	if n := byTitle(t, rs, "長輩致詞"); n == nil || n["roleNote"] != "麥克風" {
		t.Errorf("新秘 sees %v", n)
	}
	n := notice(t, rs)
	if n == nil || n["kind"] != "assign" || n["title"] != "長輩致詞" || n["effectiveStart"] != "2027-06-06T04:00:00.000Z" {
		t.Fatalf("notice = %v", n)
	}
	wantKeys(t, "assign notice", n, "kind", "at", "cueId", "title", "effectiveStart")
	if notice(t, e.snap(e.stylistBTok)) != nil {
		t.Error("新秘B notified of a cue not given to it")
	}
	// Not an undo step (US-M-8.4): undo reverses the advance before it.
	if d := s["undo"].(map[string]any)["description"]; d != "撤銷：推進『新娘化妝』" {
		t.Errorf("undo = %v", d)
	}
}

// US-M-8.1 in draft: the event day before the first advance.
func TestUS_M_8_1_InsertInDraft(t *testing.T) {
	e := newEnv(t)
	e.insert(e.cue["敬酒"]).wantResult(t, "ok")
	if c := byTitle(t, e.snap(e.master), "長輩致詞"); c == nil || c["seq"] != 5.0 {
		t.Errorf("draft insert = %v", c)
	}
}

func TestUS_M_8_InsertRefusals(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.mustWrite(pathAdvance) // 新娘化妝 done, 迎賓 in progress
	v := e.version()
	// Not before a cue that has started (invalid_position).
	for _, after := range []string{"新娘化妝"} {
		e.insert(e.cue[after]).wantError(t, http.StatusUnprocessableEntity, "invalid_position")
	}
	e.insert(e.cue["迎賓"]).wantResult(t, "ok")
	v = e.version()
	body := func(change func(map[string]any)) map[string]any {
		b := insertBody(v, e.cue["一進預備"], at(12, 0))
		change(b)
		return b
	}
	// invalid_cue, never "ended" (security review P1).
	for name, change := range map[string]func(map[string]any){
		"at the end":    func(b map[string]any) { b["start"] = "2027-06-06T20:00:00Z" }, // 04:00 the next day
		"day before":    func(b map[string]any) { b["start"] = "2027-06-05T15:59:00Z" },
		"blank title":   func(b map[string]any) { b["title"] = "   " },
		"long title":    func(b map[string]any) { b["title"] = strings.Repeat("字", 101) },
		"long note":     func(b map[string]any) { b["notePublic"] = strings.Repeat("字", 501) },
		"long duration": func(b map[string]any) { b["durationMin"] = 1441 },
		"long roleNote": func(b map[string]any) {
			b["roles"] = []map[string]any{assignment(e.stylistRole, strings.Repeat("字", 501))}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e.post(pathInsert, e.master, body(change)).wantError(t, http.StatusUnprocessableEntity, "invalid_cue")
		})
	}
	// Only roles with cues of their own (invalid_assignment).
	e.post(pathInsert, e.master, body(func(b map[string]any) {
		b["roles"] = []map[string]any{assignment(e.masterRole, nil)}
	})).wantError(t, http.StatusUnprocessableEntity, "invalid_assignment")
	// Malformed bodies.
	for name, change := range map[string]func(map[string]any){
		"no roles":      func(b map[string]any) { delete(b, "roles") },
		"no notePublic": func(b map[string]any) { delete(b, "notePublic") },
		"bad start":     func(b map[string]any) { b["start"] = "12:00" },
		"neg duration":  func(b map[string]any) { b["durationMin"] = -1 },
		"duplicate role": func(b map[string]any) {
			b["roles"] = []map[string]any{assignment(e.stylistRole, nil), assignment(e.stylistRole, nil)}
		},
		"role extra key": func(b map[string]any) {
			b["roles"] = []map[string]any{{"roleId": e.stylistRole, "roleNote": nil, "x": 1}}
		},
		"role no note": func(b map[string]any) { b["roles"] = []map[string]any{{"roleId": e.stylistRole}} },
		"title number": func(b map[string]any) { b["title"] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			e.post(pathInsert, e.master, body(change)).wantError(t, http.StatusBadRequest, "bad_request")
		})
	}
	if e.version() != v {
		t.Error("a refused insert changed the event")
	}
}

func TestUS_M_8_3_AssignPendingCues(t *testing.T) {
	e := newEnv(t)
	e.live()
	// Give 迎賓 to 新秘B with a note.
	r := e.assign(e.cue["迎賓"], assignment(e.stylistB, "接待"))
	r.wantResult(t, "ok")
	if c := byTitle(t, e.snap(e.stylistBTok), "迎賓"); c == nil || c["roleNote"] != "接待" {
		t.Fatalf("新秘B sees %v", c)
	}
	n := notice(t, e.snap(e.stylistBTok))
	if n == nil || n["kind"] != "assign" || n["cueId"] != float64(e.cue["迎賓"]) {
		t.Errorf("notice = %v", n)
	}
	// Changing only the note is no new notice.
	at1 := n["at"]
	e.setClock(at(8, 1))
	e.assign(e.cue["迎賓"], assignment(e.stylistB, "改")).wantResult(t, "ok")
	if n := notice(t, e.snap(e.stylistBTok)); n["at"] != at1 {
		t.Errorf("note change made a new notice: %v", n)
	}
	// Removing tells the role nothing and drops the old notice (US-X-7.3).
	e.assign(e.cue["迎賓"]).wantResult(t, "ok")
	s := e.snap(e.stylistBTok)
	if byTitle(t, s, "迎賓") != nil || notice(t, s) != nil {
		t.Errorf("after removal: cue %v, notice %v", byTitle(t, s, "迎賓"), notice(t, s))
	}
	if strings.Contains(e.get(e.stylistBTok).Body.String(), "接待") {
		t.Error("removed cue's note still shown")
	}
	// Only pending cues: 新娘化妝 is in progress.
	e.assign(e.cue["新娘化妝"], assignment(e.stylistB, nil)).wantError(t, http.StatusUnprocessableEntity, "not_pending")
	// Not an undo step.
	if d := e.snap(e.master)["undo"].(map[string]any)["description"]; d != "撤銷：推進『新娘化妝』" {
		t.Errorf("undo = %v", d)
	}
	e.post(pathAssign, e.master, map[string]any{"version": e.version() - 1, "cueId": e.cue["迎賓"], "roles": []any{}}).
		wantError(t, http.StatusConflict, "version_conflict")
}

// US-X-7.4: the newer of a shift and an assignment.
func TestUS_X_7_4_NewerNoticeWins(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.assign(e.cue["迎賓"], assignment(e.stylistRole, nil)).wantResult(t, "ok")
	if n := notice(t, e.snap(e.stylist)); n["kind"] != "assign" {
		t.Fatalf("notice = %v", n)
	}
	e.setClock(at(8, 1))
	e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 5)).wantResult(t, "ok")
	if n := notice(t, e.snap(e.stylist)); n["kind"] != "shift" {
		t.Errorf("notice = %v, want the newer shift", n)
	}
	e.setClock(at(8, 2))
	e.assign(e.cue["敬酒"], assignment(e.stylistB, nil), assignment(e.stylistRole, nil)).wantResult(t, "ok")
	if n := notice(t, e.snap(e.stylist)); n["kind"] != "assign" || n["cueId"] != float64(e.cue["敬酒"]) {
		t.Errorf("notice = %v, want the newer assignment", n)
	}
}

// ADR-0021: inserting a cue keeps an earlier shift undoable.
func TestUS_M_8_4_ShiftStaysUndoableAfterInsert(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 10)).wantResult(t, "ok")
	e.insert(e.cue["一進預備"]).wantResult(t, "ok")
	s := e.mustWrite(pathUndo)
	if c := byTitle(t, s, "迎賓"); c["offsetMin"] != 0.0 {
		t.Errorf("undo after insert: 迎賓 = %v", c)
	}
}

func TestUS_M_8_6_OutOfOrderWarning(t *testing.T) {
	e := newEnv(t)
	e.live()
	if o := e.snap(e.master)["outOfOrder"].([]any); len(o) != 0 {
		t.Errorf("outOfOrder = %v", o)
	}
	// After 一進預備 (11:15) at 11:00: earlier than the cue before it.
	e.post(pathInsert, e.master, insertBody(e.version(), e.cue["一進預備"], at(11, 0))).wantResult(t, "ok")
	s := e.snap(e.master)
	o := s["outOfOrder"].([]any)
	if len(o) != 1 || o[0].(map[string]any)["cueId"] != byTitle(t, s, "長輩致詞")["id"] ||
		o[0].(map[string]any)["prevCueId"] != float64(e.cue["一進預備"]) {
		t.Errorf("outOfOrder = %v", o)
	}
}

func TestUS_M_8_7_EditTitleAndNote(t *testing.T) {
	e := newEnv(t)
	e.live()
	v := e.version()
	r := e.post(pathEdit, e.master, map[string]any{"version": v, "cueId": e.cue["新娘化妝"], "title": " 新娘妝髮 ", "notePublic": "改到 B 房"})
	r.wantResult(t, "ok")
	c := byTitle(t, r.snapshot(t), "新娘妝髮")
	if c == nil || c["notePublic"] != "改到 B 房" || c["scheduledStart"] != "2027-06-06T00:00:00.000Z" || e.version() != v+1 {
		t.Errorf("edited = %v", c)
	}
	e.post(pathEdit, e.master, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"], "title": "", "notePublic": nil}).
		wantError(t, http.StatusUnprocessableEntity, "invalid_cue")
	for _, extra := range []string{"scheduledStart", "durationMin", "isFixed"} {
		e.post(pathEdit, e.master, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"], "title": "x", "notePublic": nil, extra: 1}).
			wantError(t, http.StatusBadRequest, "bad_request")
	}
	// The undo of the advance still works after a title edit (ADR-0015).
	e.mustWrite(pathUndo)
}

// ---------------------------------------------------------------- ownership (security review P1)

// Another event's ids, ended or not, are a 404 without a result, never a
// type or state error and never "ended" (api.md section 3, 6).
func TestM2bIDsOfAnotherEventAre404(t *testing.T) {
	for _, day := range []int{7, 1} { // live later, and already ended
		e := newEnv(t)
		e.live()
		f := e.otherEvent(day)
		v := e.version()
		for _, id := range []int64{f.masterRole, f.host, f.stylist, 9999} {
			for _, p := range []string{pathReissue, pathRevoke} {
				e.post(p, e.master, map[string]any{"roleId": id}).wantError(t, http.StatusNotFound, "not_found")
			}
			e.proxyCheckIn(id, true).wantError(t, http.StatusNotFound, "not_found")
			e.assign(e.cue["迎賓"], assignment(id, nil)).wantError(t, http.StatusNotFound, "not_found")
			e.insert(e.cue["一進預備"], assignment(id, nil)).wantError(t, http.StatusNotFound, "not_found")
		}
		e.skip(f.doneCue).wantError(t, http.StatusNotFound, "not_found")
		e.assign(f.doneCue).wantError(t, http.StatusNotFound, "not_found")
		e.insert(f.doneCue).wantError(t, http.StatusNotFound, "not_found")
		e.post(pathEdit, e.master, map[string]any{"version": v, "cueId": f.doneCue, "title": "x", "notePublic": nil}).
			wantError(t, http.StatusNotFound, "not_found")
		if e.version() != v {
			t.Error("a foreign id changed the event")
		}
		e.snap(e.master)
	}
}

// ---------------------------------------------------------------- section 4

// Every cell of the M2b rows of the capability table (api.md section 4).
func TestM2bCapabilityTableEveryCell(t *testing.T) {
	paths := []string{pathCheckIn, pathProxyCheckIn, pathSkip, pathInsert, pathAssign, pathEdit, pathReissue, pathRevoke}
	for _, typ := range allRoleTypes {
		for _, p := range paths {
			t.Run(string(typ)+p, func(t *testing.T) {
				e := newEnv(t)
				tok := map[domain.RoleType]string{domain.Master: e.master, domain.Stylist: e.stylist}[typ]
				if tok == "" {
					tok = e.addRoleWithToken(typ)
				}
				v := e.version()
				body := map[string]map[string]any{
					pathCheckIn:      {"checkedIn": true},
					pathProxyCheckIn: {"roleId": e.stylistB, "checkedIn": true},
					pathSkip:         {"version": v, "cueId": e.cue["迎賓"]},
					pathInsert:       insertBody(v, e.cue["敬酒"], at(14, 0)),
					pathAssign:       {"version": v, "cueId": e.cue["迎賓"], "roles": []any{}},
					pathEdit:         {"version": v, "cueId": e.cue["迎賓"], "title": "x", "notePublic": nil},
					pathReissue:      {"roleId": e.stylistB},
					pathRevoke:       {"roleId": e.stylistB},
				}[p]
				r := e.post(p, tok, body)
				ok := typ == domain.Master && p != pathCheckIn || typ.HasOwnCues() && p == pathCheckIn
				if ok {
					r.wantResult(t, "ok")
					return
				}
				r.wantError(t, http.StatusForbidden, "forbidden")
				if e.version() != v || e.count(`SELECT count(*) FROM check_ins`) != 0 ||
					e.count(`SELECT count(*) FROM action_logs WHERE type LIKE 'token.%' OR type LIKE 'rundown.%'`) != 0 {
					t.Error("a refused call changed the event")
				}
				e.get(e.stylistBTok).wantResult(t, "ok")
			})
		}
	}
}
