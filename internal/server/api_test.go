package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/api"
	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

const (
	pathSnapshot = "/api/snapshot"
	pathAdvance  = "/api/cues/advance"
	pathUndo     = "/api/cues/undo"
	pathShift    = "/api/cues/shift"
	pathPreview  = "/api/cues/shift/preview"
)

// start makes the event live without moving any cue, as M1's separate
// start did; the API now starts it with the first advance (ADR-0020).
func (e *env) start() {
	e.t.Helper()
	owner, err := e.st.LookupToken(bg, auth.HashToken(e.master), e.clock())
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.st.StartEvent(bg, e.eventID, e.version(), owner.Actor, e.clock()); err != nil {
		e.t.Fatal(err)
	}
}

// write sends a Master write with the current version; a shift moves
// 迎賓 and what follows by 5 minutes.
func (e *env) write(path, token string) resp {
	e.t.Helper()
	if path == pathShift {
		return e.do(http.MethodPost, path, token, shiftBody(e.version(), e.cue["迎賓"], 5))
	}
	return e.do(http.MethodPost, path, token, versionBody(e.version()))
}

func (e *env) mustWrite(path string) map[string]any {
	e.t.Helper()
	r := e.write(path, e.master)
	r.wantResult(e.t, "ok")
	return r.snapshot(e.t)
}

func (e *env) get(token string) resp {
	e.t.Helper()
	return e.do(http.MethodGet, pathSnapshot, token, "")
}

func (e *env) snap(token string) map[string]any {
	e.t.Helper()
	r := e.get(token)
	r.wantResult(e.t, "ok")
	return r.snapshot(e.t)
}

// ---------------------------------------------------------------- US-X-1

func TestUS_X_1_1_TheLinkDecidesTheView(t *testing.T) {
	e := newEnv(t)
	m := e.snap(e.master)
	if role := m["role"].(map[string]any); role["type"] != "Master" || role["name"] != "總召" {
		t.Errorf("Master role = %v", role)
	}
	if _, ok := m["roles"]; !ok {
		t.Error("Master snapshot has no role list")
	}
	s := e.snap(e.stylist)
	if role := s["role"].(map[string]any); role["type"] != "Stylist" || role["name"] != "新秘" {
		t.Errorf("Stylist role = %v", role)
	}
}

func TestUS_X_1_2_StylistSeesOnlyItsOwnCuesAndNotes(t *testing.T) {
	e := newEnv(t)
	e.setClock(at(9, 0))
	e.start()
	e.mustWrite(pathAdvance) // 新娘化妝 in progress (assigned)
	e.mustWrite(pathAdvance) // 迎賓 in progress (not assigned to 新秘)

	r := e.get(e.stylist)
	r.wantResult(t, "ok")
	s := r.snapshot(t)
	var titles []string
	for _, c := range cues(t, s) {
		titles = append(titles, c["title"].(string))
	}
	if strings.Join(titles, ",") != "新娘化妝,一進預備" {
		t.Errorf("stylist cues = %v, want only 新娘化妝, 一進預備", titles)
	}
	if n := byTitle(t, s, "新娘化妝")["roleNote"]; n != "新娘房" {
		t.Errorf("own roleNote = %v", n)
	}
	body := r.Body.String()
	// Of the unassigned cue in progress only its title, once, as
	// currentTitle (US-X-6.4): not its note, id or time.
	if s["currentTitle"] != "迎賓" || strings.Count(body, "迎賓") != 1 {
		t.Errorf("currentTitle = %v; 迎賓 appears %d times", s["currentTitle"], strings.Count(body, "迎賓"))
	}
	// Another role's cue, note and name; the rest of the unassigned cue in
	// progress (api.md 5.3); the role list; any other role's ids.
	for _, hidden := range []string{"B專屬備註", "B敬酒", "敬酒", "新秘B", "賓客入場", `"roles"`, `"current"`, `"activeCueId"`, `"undo"`, "總召"} {
		if strings.Contains(body, hidden) {
			t.Errorf("stylist response contains %q: %s", hidden, body)
		}
	}
	if l := leaks(body, e.master, e.stylist, e.stylistBTok); len(l) > 0 {
		t.Errorf("credential material in response: %v", l)
	}
	// 新秘B sees its own note on the shared cue, not 新秘's.
	if n := byTitle(t, e.snap(e.stylistBTok), "新娘化妝")["roleNote"]; n != "B專屬備註" {
		t.Errorf("新秘B roleNote = %v", n)
	}
}

func TestUS_X_1_3_UnknownOrRevokedLinkIsInvalid(t *testing.T) {
	e := newEnv(t)
	e.get(newToken(t)).wantResult(t, "invalid")
	h := e.fs.Store
	owner, err := h.LookupToken(bg, auth.HashToken(e.master), e.clock())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.RevokeToken(bg, e.stylistRole, owner.Actor, e.clock()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{pathSnapshot, pathAdvance, pathUndo, pathShift} {
		method := http.MethodPost
		if p == pathSnapshot {
			method = http.MethodGet
		}
		e.do(method, p, e.stylist, versionBody(1)).wantResult(t, "invalid")
	}
	old := e.master
	e.replaceMasterLink()
	e.get(old).wantResult(t, "invalid")
	e.do(http.MethodPost, pathAdvance, old, versionBody(e.version())).wantResult(t, "invalid")
}

func TestUS_X_1_4_EndedEventAnswersEndedWithNoData(t *testing.T) {
	e := newEnv(t)
	ev, _, _ := e.st.Event(bg, e.eventID)
	e.setClock(ev.EndsAt())
	for _, tok := range []string{e.master, e.stylist} {
		r := e.get(tok)
		r.wantResult(t, "ended")
		if strings.Contains(r.Body.String(), "婚宴") {
			t.Errorf("ended response carries event data: %s", r.Body.String())
		}
		for _, p := range []string{pathAdvance, pathUndo, pathShift} {
			e.do(http.MethodPost, p, tok, versionBody(e.version())).wantResult(t, "ended")
		}
	}
	// An unknown token after the end is still "invalid" (invalid first).
	e.get(newToken(t)).wantResult(t, "invalid")
	e.setClock(ev.EndsAt().Add(-time.Nanosecond))
	e.get(e.master).wantResult(t, "ok")
}

func TestUS_X_1_5_StylistCannotActAsMasterAndStaysLinked(t *testing.T) {
	e := newEnv(t)
	v := e.version()
	for _, p := range []string{pathAdvance, pathUndo, pathShift} {
		e.do(http.MethodPost, p, e.stylist, versionBody(v)).wantError(t, http.StatusForbidden, "forbidden")
	}
	if e.version() != v {
		t.Error("a refused Stylist write changed the event")
	}
	// The refusal is not "invalid": the same link still works.
	e.get(e.stylist).wantResult(t, "ok")
}

func TestUS_X_1_6_ServerErrorsAreNeverInvalidOrEnded(t *testing.T) {
	e := newEnv(t)
	e.fs.snapshotErr = errors.New("disk I/O error")
	e.get(e.master).wantError(t, http.StatusInternalServerError, "internal")
	e.fs.snapshotErr = nil
	e.fs.lookupErr = sql.ErrConnDone
	e.write(pathAdvance, e.master).wantError(t, http.StatusInternalServerError, "internal")
	e.fs.lookupErr = nil

	// The real store, closed underneath the server.
	e.st.Close()
	e.get(e.master).wantError(t, http.StatusInternalServerError, "internal")
	e.do(http.MethodPost, pathAdvance, e.master, versionBody(1)).wantError(t, http.StatusInternalServerError, "internal")
}

// ---------------------------------------------------------------- US-M-1

func TestUS_M_1_1_MasterSeesTheWholeRundownInOrder(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE cues SET offset_min = 10 WHERE id = ?`, e.cue["一進預備"])
	s := e.snap(e.master)
	list := cues(t, s)
	var titles []string
	for _, c := range list {
		titles = append(titles, c["title"].(string))
	}
	if strings.Join(titles, ",") != "新娘化妝,迎賓,一進預備,敬酒" {
		t.Errorf("titles = %v", titles)
	}
	c := byTitle(t, s, "一進預備")
	if c["scheduledStart"] != "2027-06-06T03:15:00.000Z" || c["effectiveStart"] != "2027-06-06T03:25:00.000Z" ||
		c["isFixed"] != true || c["status"] != "Pending" || c["durationMin"] != 15.0 || c["offsetMin"] != 10.0 || c["seq"] != 3.0 {
		t.Errorf("一進預備 = %v", c)
	}
	if n := byTitle(t, s, "迎賓")["notePublic"]; n != "賓客入場" {
		t.Errorf("notePublic = %v", n)
	}
	if n := byTitle(t, s, "敬酒")["notePublic"]; n != nil {
		t.Errorf("blank notePublic = %v, want null", n)
	}
	roles := byTitle(t, s, "新娘化妝")["roles"].([]any)
	if len(roles) != 2 {
		t.Fatalf("cue roles = %v", roles)
	}
	r0, r1 := roles[0].(map[string]any), roles[1].(map[string]any)
	if r0["name"] != "新秘" || r0["roleNote"] != "新娘房" || r1["name"] != "新秘B" || r1["roleNote"] != "B專屬備註" ||
		r0["roleId"] != float64(e.stylistRole) {
		t.Errorf("cue roles = %v", roles)
	}
	ev := s["event"].(map[string]any)
	if ev["name"] != "婚宴" || ev["date"] != "2027-06-06" || ev["timezone"] != "Asia/Taipei" {
		t.Errorf("event = %v", ev)
	}
	if s["eventStatus"] != "Draft" || s["eventVersion"] != float64(e.version()) || s["serverTime"] != "2027-06-06T01:00:00.000Z" {
		t.Errorf("status %v version %v time %v", s["eventStatus"], s["eventVersion"], s["serverTime"])
	}
	if l := leaks(e.get(e.master).Body.String(), e.master, e.stylist, e.stylistBTok); len(l) > 0 {
		t.Errorf("credential material in Master snapshot: %v", l)
	}
}

func TestUS_M_1_2_ActiveCueWithStartAndLength(t *testing.T) {
	e := newEnv(t)
	if s := e.snap(e.master); s["activeCueId"] != nil {
		t.Errorf("activeCueId before start = %v, want null", s["activeCueId"])
	}
	e.start()
	e.setClock(at(8, 2))
	s := e.mustWrite(pathAdvance)
	if s["activeCueId"] != float64(e.cue["新娘化妝"]) {
		t.Errorf("activeCueId = %v", s["activeCueId"])
	}
	c := byTitle(t, s, "新娘化妝")
	if c["status"] != "InProgress" || c["actualStart"] != "2027-06-06T00:02:00.000Z" || c["actualEnd"] != nil || c["durationMin"] != 120.0 {
		t.Errorf("active cue = %v", c)
	}
}

// Another Master device sees the change on its next poll (NFR-2).
func TestUS_M_1_4_AnotherMasterDeviceSeesTheChange(t *testing.T) {
	e := newEnv(t)
	e.start()
	before := e.snap(e.master)["eventVersion"]
	e.mustWrite(pathAdvance)  // device A
	after := e.snap(e.master) // device B polls with the same link
	if after["eventVersion"] == before || byTitle(t, after, "新娘化妝")["status"] != "InProgress" {
		t.Errorf("device B sees version %v (before %v), cue %v", after["eventVersion"], before, byTitle(t, after, "新娘化妝"))
	}
}

// ---------------------------------------------------------------- US-M-3

// ADR-0020: the first advance on the event day starts the event; there is
// no separate start.
func TestUS_M_3_6_FirstAdvanceStartsTheEventOnItsDay(t *testing.T) {
	e := newEnv(t)
	e.setClock(at(0, 0).Add(-time.Minute))
	e.write(pathAdvance, e.master).wantError(t, http.StatusUnprocessableEntity, "not_event_day") // US-M-3.7
	if s := e.snap(e.master); s["eventStatus"] != "Draft" {
		t.Fatalf("refused advance changed the event: %v", s["eventStatus"])
	}
	e.setClock(at(8, 1))
	s := e.mustWrite(pathAdvance)
	if s["eventStatus"] != "Live" || byTitle(t, s, "新娘化妝")["status"] != "InProgress" {
		t.Errorf("after first advance: %v, %v", s["eventStatus"], byTitle(t, s, "新娘化妝")["status"])
	}
	if st := e.snap(e.stylist)["eventStatus"]; st != "Live" {
		t.Errorf("stylist eventStatus = %v", st)
	}
	// US-M-3.8: undoing it keeps the event live.
	s = e.mustWrite(pathUndo)
	if s["eventStatus"] != "Live" || byTitle(t, s, "新娘化妝")["status"] != "Pending" {
		t.Errorf("after undo: %v, %v", s["eventStatus"], byTitle(t, s, "新娘化妝")["status"])
	}
}

// US-M-3.9: the old start endpoint is gone.
func TestUS_M_3_9_StartEndpointIsGone(t *testing.T) {
	e := newEnv(t)
	r := e.do(http.MethodPost, "/api/event/start", e.master, versionBody(e.version()))
	if r.Code != http.StatusNotFound && r.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/event/start: %d", r.Code)
	}
	if e.snap(e.master)["eventStatus"] != "Draft" {
		t.Error("the old endpoint started the event")
	}
}

func TestUS_M_3_2_AdvanceMovesExactlyOneCue(t *testing.T) {
	e := newEnv(t)
	e.setClock(at(0, 0).Add(-time.Minute))
	e.write(pathAdvance, e.master).wantError(t, http.StatusUnprocessableEntity, "not_event_day")
	e.setClock(at(9, 0))
	e.start()
	e.setClock(at(8, 0))
	e.mustWrite(pathAdvance)
	e.setClock(at(10, 5))
	s := e.mustWrite(pathAdvance)
	first, second := byTitle(t, s, "新娘化妝"), byTitle(t, s, "迎賓")
	if first["status"] != "Completed" || first["actualEnd"] != "2027-06-06T02:05:00.000Z" {
		t.Errorf("completed cue = %v", first)
	}
	if second["status"] != "InProgress" || second["actualStart"] != "2027-06-06T02:05:00.000Z" {
		t.Errorf("started cue = %v", second)
	}
	inProgress := 0
	for _, c := range cues(t, s) {
		if c["status"] == "InProgress" {
			inProgress++
		}
	}
	if inProgress != 1 || s["activeCueId"] != float64(e.cue["迎賓"]) {
		t.Errorf("%d cues in progress, active %v", inProgress, s["activeCueId"])
	}
	e.mustWrite(pathAdvance)
	e.mustWrite(pathAdvance)
	s = e.mustWrite(pathAdvance) // completes the last cue
	if s["activeCueId"] != nil || byTitle(t, s, "敬酒")["status"] != "Completed" {
		t.Errorf("after the last cue: active %v", s["activeCueId"])
	}
	e.write(pathAdvance, e.master).wantError(t, http.StatusUnprocessableEntity, "nothing_to_advance")
}

func TestUS_M_3_4_StaleDeviceIsRefusedWithTheLatestSnapshot(t *testing.T) {
	e := newEnv(t)
	e.start()
	seen := e.version()                                                                 // both devices show this version
	e.do(http.MethodPost, pathAdvance, e.master, versionBody(seen)).wantResult(t, "ok") // device A
	r := e.do(http.MethodPost, pathAdvance, e.master, versionBody(seen))                // device B, stale
	r.wantError(t, http.StatusConflict, "version_conflict")
	wantKeys(t, "409", r.body, "error", "snapshot")
	s := r.snapshot(t)
	if s["eventVersion"] != float64(e.version()) || byTitle(t, s, "新娘化妝")["status"] != "InProgress" {
		t.Errorf("409 snapshot = version %v", s["eventVersion"])
	}
	if _, ok := s["roles"]; !ok {
		t.Error("409 snapshot is not the Master's own snapshot")
	}
	if got := e.snap(e.master); byTitle(t, got, "迎賓")["status"] != "Pending" {
		t.Error("the stale advance was applied")
	}
}

func TestUS_M_3_5_EveryAdvanceIsLogged(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.setClock(at(8, 1))
	e.mustWrite(pathAdvance)
	e.setClock(at(10, 30))
	e.mustWrite(pathAdvance)
	rows, err := e.db().Query(`SELECT actor, at, type, payload FROM action_logs WHERE type = 'cue.advance' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		var (
			actor, typ, payload string
			atNS                int64
		)
		if err := rows.Scan(&actor, &atNS, &typ, &payload); err != nil {
			t.Fatal(err)
		}
		n++
		if actor != fmt.Sprintf("role:%d", e.masterRole) {
			t.Errorf("actor %q", actor)
		}
		want := []time.Time{at(8, 1), at(10, 30)}[n-1]
		if !time.Unix(0, atNS).Equal(want) {
			t.Errorf("at %v, want %v", time.Unix(0, atNS), want)
		}
		if !strings.Contains(payload, fmt.Sprintf(`"cueId":%d`, e.cue["新娘化妝"])) {
			t.Errorf("payload %s does not name the changed cue", payload)
		}
	}
	if n != 2 {
		t.Errorf("%d advance entries, want 2", n)
	}
}

// ---------------------------------------------------------------- US-M-4

func TestUS_M_4_1_UndoRestoresStatesAndTimes(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.setClock(at(8, 0))
	e.mustWrite(pathAdvance)
	e.setClock(at(10, 31))
	e.mustWrite(pathAdvance)
	s := e.mustWrite(pathUndo)
	first, second := byTitle(t, s, "新娘化妝"), byTitle(t, s, "迎賓")
	if first["status"] != "InProgress" || first["actualStart"] != "2027-06-06T00:00:00.000Z" || first["actualEnd"] != nil {
		t.Errorf("completed → in progress: %v", first)
	}
	if second["status"] != "Pending" || second["actualStart"] != nil {
		t.Errorf("in progress → pending: %v", second)
	}
	if s["activeCueId"] != float64(e.cue["新娘化妝"]) {
		t.Errorf("activeCueId = %v", s["activeCueId"])
	}
}

func TestUS_M_4_2_UndoDescribesTheOperation(t *testing.T) {
	e := newEnv(t)
	e.start()
	s := e.mustWrite(pathAdvance)
	u := s["undo"].(map[string]any)
	wantKeys(t, "undo", u, "available", "description")
	if u["available"] != true || u["description"] != "撤銷：推進『新娘化妝』" {
		t.Errorf("undo = %v", u)
	}
	s = e.mustWrite(pathAdvance)
	if d := s["undo"].(map[string]any)["description"]; d != "撤銷：推進『迎賓』" {
		t.Errorf("description = %v", d)
	}
}

func TestUS_M_4_3_OnlyTheLatestOperationOnce(t *testing.T) {
	e := newEnv(t)
	s := e.snap(e.master)
	if u := s["undo"].(map[string]any); u["available"] != false || u["description"] != nil {
		t.Errorf("draft undo = %v", u)
	}
	// Undo runs in draft too since M2b (US-M-6.4); there is nothing yet.
	e.write(pathUndo, e.master).wantError(t, http.StatusUnprocessableEntity, "nothing_to_undo")
	e.start()
	s = e.snap(e.master)
	if s["undo"].(map[string]any)["available"] != false {
		t.Error("undo offered with nothing to undo")
	}
	e.write(pathUndo, e.master).wantError(t, http.StatusUnprocessableEntity, "nothing_to_undo")
	e.mustWrite(pathAdvance)
	e.mustWrite(pathAdvance)
	s = e.mustWrite(pathUndo)
	if s["undo"].(map[string]any)["available"] != false {
		t.Error("undo still offered after undoing")
	}
	e.write(pathUndo, e.master).wantError(t, http.StatusUnprocessableEntity, "nothing_to_undo")
	if byTitle(t, e.snap(e.master), "新娘化妝")["status"] != "InProgress" {
		t.Error("the earlier advance was undone too")
	}
}

func TestUS_M_4_4_UndoChecksTheVersionAndIsLogged(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.mustWrite(pathAdvance)
	stale := e.version() - 1
	r := e.do(http.MethodPost, pathUndo, e.master, versionBody(stale))
	r.wantError(t, http.StatusConflict, "version_conflict")
	if byTitle(t, r.snapshot(t), "新娘化妝")["status"] != "InProgress" {
		t.Error("stale undo was applied")
	}
	e.mustWrite(pathUndo)
	var actor string
	if err := e.db().QueryRow(`SELECT actor FROM action_logs WHERE type = 'undo'`).Scan(&actor); err != nil ||
		actor != fmt.Sprintf("role:%d", e.masterRole) {
		t.Errorf("undo log actor %q (%v)", actor, err)
	}
}

func TestUS_M_4_5_StylistTargetFollowsUndo(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.mustWrite(pathAdvance) // 新娘化妝 in progress
	e.mustWrite(pathAdvance) // 新娘化妝 done
	if st := byTitle(t, e.snap(e.stylist), "新娘化妝")["status"]; st != "Completed" {
		t.Fatalf("stylist sees %v", st)
	}
	e.mustWrite(pathUndo)
	if st := byTitle(t, e.snap(e.stylist), "新娘化妝")["status"]; st != "InProgress" {
		t.Errorf("after undo the stylist sees %v, want InProgress", st)
	}
}

// ---------------------------------------------------------------- US-ST-1

// The stylist's countdown target is the first assigned pending cue; the
// snapshot carries its effective start and the server time (sync-and-offline.md section 4).
func TestUS_ST_1_1_SnapshotCarriesTheCountdownData(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE cues SET offset_min = -5 WHERE id = ?`, e.cue["一進預備"])
	e.setClock(at(10, 0).Add(123 * time.Millisecond))
	s := e.snap(e.stylist)
	if s["serverTime"] != "2027-06-06T02:00:00.123Z" {
		t.Errorf("serverTime = %v", s["serverTime"])
	}
	c := byTitle(t, s, "一進預備")
	if c["effectiveStart"] != "2027-06-06T03:10:00.000Z" || c["offsetMin"] != -5.0 || c["status"] != "Pending" ||
		c["seq"] != 3.0 || c["durationMin"] != 15.0 || c["isFixed"] != true || c["roleNote"] != "側門" || c["notePublic"] != nil {
		t.Errorf("cue = %v", c)
	}
}

func TestUS_ST_1_2_AdvanceMovesTheStylistsTarget(t *testing.T) {
	e := newEnv(t)
	e.start()
	firstPending := func() string {
		for _, c := range cues(t, e.snap(e.stylist)) {
			if c["status"] == "Pending" {
				return c["title"].(string)
			}
		}
		return ""
	}
	if got := firstPending(); got != "新娘化妝" {
		t.Errorf("target = %q", got)
	}
	e.mustWrite(pathAdvance)
	if got := firstPending(); got != "一進預備" {
		t.Errorf("after advance target = %q, want 一進預備", got)
	}
}

func TestUS_ST_1_4_AssignedCueInProgressIsShown(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.mustWrite(pathAdvance)
	e.mustWrite(pathAdvance)
	e.mustWrite(pathAdvance) // 一進預備 in progress
	if st := byTitle(t, e.snap(e.stylist), "一進預備")["status"]; st != "InProgress" {
		t.Errorf("status = %v", st)
	}
	e.mustWrite(pathAdvance)
	e.mustWrite(pathAdvance) // all done
	for _, c := range cues(t, e.snap(e.stylist)) {
		if c["status"] != "Completed" {
			t.Errorf("%v", c)
		}
	}
}

// Draft also gets the full role snapshot (api.md 5.6).
func TestUS_ST_1_5_DraftGivesTheFullRoleSnapshot(t *testing.T) {
	e := newEnv(t)
	s := e.snap(e.stylist)
	if s["eventStatus"] != "Draft" || len(cues(t, s)) != 2 {
		t.Errorf("draft stylist snapshot = %v", s)
	}
}

// ---------------------------------------------------------------- section 4

var allRoleTypes = []domain.RoleType{
	domain.Master, domain.Stylist, domain.Media, domain.StageAV, domain.Host,
	domain.VenueLead, domain.Reception, domain.Cashier, domain.Runner,
}

// Every cell of the capability table (api.md section 4) for M1: Master
// may do everything, Stylist may only read its snapshot, every other type
// in the database gets 403 on every endpoint, and a refused write changes
// nothing. Each cell runs on its own database.
func TestCapabilityTableEveryCell(t *testing.T) {
	for _, typ := range allRoleTypes {
		for _, ep := range []string{pathSnapshot, pathAdvance, pathUndo, pathShift} {
			t.Run(string(typ)+ep, func(t *testing.T) {
				e := newEnv(t)
				tok := map[domain.RoleType]string{domain.Master: e.master, domain.Stylist: e.stylist}[typ]
				if tok == "" {
					tok = e.addRoleWithToken(typ)
				}
				// Put the event where the Master's call succeeds.
				switch ep {
				case pathAdvance, pathShift:
					e.start()
				case pathUndo:
					e.start()
					e.mustWrite(pathAdvance)
				}
				v := e.version()
				var r resp
				switch ep {
				case pathSnapshot:
					r = e.get(tok)
				case pathShift:
					r = e.do(http.MethodPost, ep, tok, shiftBody(v, e.cue["迎賓"], 5))
				default:
					r = e.do(http.MethodPost, ep, tok, versionBody(v))
				}
				switch {
				case typ == domain.Master:
					r.wantResult(t, "ok")
					if _, ok := r.snapshot(t)["roles"]; !ok {
						t.Error("not a Master snapshot")
					}
				case (typ.HasOwnCues() || typ == domain.Host) && ep == pathSnapshot:
					r.wantResult(t, "ok")
					if _, ok := r.snapshot(t)["roles"]; ok {
						t.Errorf("%s got a Master snapshot", typ)
					}
				default:
					r.wantError(t, http.StatusForbidden, "forbidden")
					if e.version() != v {
						t.Error("a refused call changed the event")
					}
					if strings.Contains(r.Body.String(), "婚宴") {
						t.Error("403 carries event data")
					}
				}
			})
		}
	}
}

// ---------------------------------------------------------------- section 5

// The key sets of every snapshot shape are fixed (tech-stack.md 2.2「型別」).
func TestSnapshotKeySets(t *testing.T) {
	e := newEnv(t)
	e.start()
	r := e.write(pathAdvance, e.master)
	wantKeys(t, "ok response", r.body, "result", "snapshot")
	m := r.snapshot(t)
	wantKeys(t, "Master snapshot", m, "eventVersion", "serverTime", "eventStatus", "role", "eventDay", "event", "cues",
		"activeCueId", "undo", "roles", "conflicts", "overrunMin", "outOfOrder")
	wantKeys(t, "role", m["role"].(map[string]any), "type", "name", "view")
	wantKeys(t, "eventDay", m["eventDay"].(map[string]any), "date", "timezone")
	wantKeys(t, "event", m["event"].(map[string]any), "name", "date", "timezone")
	wantKeys(t, "undo", m["undo"].(map[string]any), "available", "description")
	for _, c := range cues(t, m) {
		wantKeys(t, "Master cue", c, "id", "seq", "title", "scheduledStart", "effectiveStart", "durationMin", "isFixed",
			"offsetMin", "status", "actualStart", "actualEnd", "notePublic", "roles")
		for _, cr := range c["roles"].([]any) {
			wantKeys(t, "Master cue role", cr.(map[string]any), "roleId", "name", "roleNote")
		}
	}
	roles := m["roles"].([]any)
	if len(roles) != 3 {
		t.Errorf("roles = %v", roles)
	}
	for _, ro := range roles {
		wantKeys(t, "Master role list item", ro.(map[string]any), "id", "name", "type", "checkedIn", "lastSeenAt", "hasLink")
	}

	s := e.get(e.stylist)
	wantKeys(t, "ok response", s.body, "result", "snapshot")
	ss := s.snapshot(t)
	wantKeys(t, "Stylist snapshot", ss, "eventVersion", "serverTime", "eventStatus", "role", "eventDay", "cues", "notice",
		"myCheckIn", "currentTitle")
	wantKeys(t, "role", ss["role"].(map[string]any), "type", "name", "view")
	for _, c := range cues(t, ss) {
		wantKeys(t, "Stylist cue", c, "id", "seq", "title", "effectiveStart", "durationMin", "isFixed", "offsetMin",
			"status", "notePublic", "roleNote")
	}
	// A stylist with no assigned cue gets an empty list, not null.
	e.exec(`DELETE FROM cue_roles WHERE role_id = ?`, e.stylistRole)
	if c, ok := e.snap(e.stylist)["cues"].([]any); !ok || len(c) != 0 {
		t.Errorf("no assigned cues: %v", c)
	}
}

// ---------------------------------------------------------------- section 6

// Every row of the error table (api.md section 6), injected at the store
// boundary for every write endpoint.
func TestErrorTableEveryRow(t *testing.T) {
	rows := []struct {
		err    error
		status int
		result string // "" for an error response
		code   string
	}{
		{store.ErrInvalidToken, 200, "invalid", ""},
		{domain.ErrEnded, 200, "ended", ""},
		{store.ErrEventEnded, 200, "ended", ""},
		{store.ErrVersionConflict, 409, "", "version_conflict"},
		{domain.ErrNotDraft, 422, "", "not_draft"},
		{domain.ErrNotLive, 422, "", "not_live"},
		{domain.ErrNotEventDay, 422, "", "not_event_day"},
		{domain.ErrNothingToAdvance, 422, "", "nothing_to_advance"},
		{store.ErrNothingToUndo, 422, "", "nothing_to_undo"},
		{domain.ErrUndoStale, 422, "", "undo_stale"},
		{store.ErrNoActor, 500, "", "internal"},
		{store.ErrCrossEvent, 500, "", "internal"},
		{store.ErrNotFound, 500, "", "internal"},
		{domain.ErrMultipleInProgress, 500, "", "internal"},
		{store.ErrUnknownOp, 500, "", "internal"},
		{domain.ErrEmptyOp, 500, "", "internal"},
		{sql.ErrConnDone, 500, "", "internal"},
		{errors.New("SQLITE_BUSY: database is locked"), 500, "", "internal"},
		{context.DeadlineExceeded, 500, "", "internal"},
		{context.Canceled, 500, "", "internal"},
		{errors.New("anything else"), 500, "", "internal"},
		{fmt.Errorf("cue 7: %w", store.ErrNotFound), 500, "", "internal"},
	}
	e := newEnv(t)
	for _, row := range rows {
		for _, ep := range []string{pathAdvance, pathUndo, pathShift} {
			e.fs.writeErr = row.err
			r := e.write(ep, e.master)
			if row.result != "" {
				r.wantResult(t, row.result)
			} else {
				r.wantError(t, row.status, row.code)
			}
		}
	}
	e.fs.writeErr = nil

	// Lookup side: invalid, ended, and failures.
	for _, row := range []struct {
		err    error
		status int
		result string
	}{
		{store.ErrInvalidToken, 200, "invalid"},
		{store.ErrEventEnded, 200, "ended"},
		{sql.ErrConnDone, 500, ""},
		{context.DeadlineExceeded, 500, ""},
		{store.ErrNotFound, 500, ""},
	} {
		e.fs.lookupErr, e.fs.snapshotErr = row.err, row.err
		for _, ep := range []string{pathSnapshot, pathAdvance, pathUndo, pathShift} {
			var r resp
			if ep == pathSnapshot {
				r = e.get(e.master)
			} else {
				r = e.write(ep, e.master)
			}
			if row.result != "" {
				r.wantResult(t, row.result)
			} else {
				r.wantError(t, row.status, "internal")
			}
		}
	}
}

// The same rows through the real store, where they can be produced.
func TestErrorTableThroughTheStore(t *testing.T) {
	e := newEnv(t)
	// undo_stale: a cue changed outside the logged operation.
	e.start()
	e.mustWrite(pathAdvance)
	e.exec(`UPDATE cues SET actual_start = actual_start + 1 WHERE id = ?`, e.cue["新娘化妝"])
	e.write(pathUndo, e.master).wantError(t, http.StatusUnprocessableEntity, "undo_stale")
	// Unknown operation format: 500, never a rule refusal.
	e.exec(`INSERT INTO action_logs (event_id, at, actor, type, payload) VALUES (?, 0, 'server', 'cue.reorder', '{"v":1}')`, e.eventID)
	e.write(pathUndo, e.master).wantError(t, http.StatusInternalServerError, "internal")

	// The token revoked between lookup and write: the write re-checks it.
	e2 := newEnv(t)
	e2.fs.beforeWrite = func() { e2.replaceMasterLink() }
	e2.write(pathAdvance, e2.master).wantResult(t, "invalid")
	if e2.snap(e2.replaceMasterLink())["eventStatus"] != "Draft" {
		t.Error("write of a revoked link was applied")
	}

	// The event ends between lookup and write: "ended", nothing written.
	e3 := newEnv(t)
	ev, _, _ := e3.st.Event(bg, e3.eventID)
	e3.fs.writeNow = func(time.Time) time.Time { return ev.EndsAt() }
	e3.write(pathAdvance, e3.master).wantResult(t, "ended")
	e3.fs.writeNow = nil
	if e3.snap(e3.master)["eventStatus"] != "Draft" {
		t.Error("write after the end was applied")
	}
}

// ---------------------------------------------------------------- section 7

// After a successful write the caller's snapshot is read again; a link
// revoked or an event ended in between is answered with that result, and
// a failed read with 500, never both an error and a result (item 4).
func TestRereadAfterWrite(t *testing.T) {
	e := newEnv(t)
	e.fs.afterWrite = func() { e.replaceMasterLink() }
	e.write(pathAdvance, e.master).wantResult(t, "invalid")

	e2 := newEnv(t)
	ev, _, _ := e2.st.Event(bg, e2.eventID)
	e2.fs.afterWrite = func() { e2.setClock(ev.EndsAt()) }
	e2.write(pathAdvance, e2.master).wantResult(t, "ended")

	e3 := newEnv(t)
	v := e3.version()
	e3.fs.afterWrite = func() { e3.fs.snapshotErr = errors.New("disk I/O error") }
	e3.write(pathAdvance, e3.master).wantError(t, http.StatusInternalServerError, "internal")
	if e3.version() != v+1 {
		t.Error("the write did not take effect")
	}
}

func TestRereadAfterConflict(t *testing.T) {
	e := newEnv(t)
	stale := e.version() - 1
	e.fs.beforeWrite = func() { e.replaceMasterLink() }
	e.fs.writeErr = store.ErrVersionConflict
	e.do(http.MethodPost, pathAdvance, e.master, versionBody(stale)).wantResult(t, "invalid")

	e2 := newEnv(t)
	e2.fs.snapshotErr = nil
	e2.fs.writeErr = store.ErrVersionConflict
	e2.fs.beforeWrite = func() { e2.fs.snapshotErr = errors.New("disk I/O error") }
	e2.write(pathAdvance, e2.master).wantError(t, http.StatusInternalServerError, "internal")
}

// Order: header (400) → token (invalid) → capability (403) → body (400)
// → write (409, 422) (item 3).
func TestProcessingOrder(t *testing.T) {
	e := newEnv(t)
	bad := `{"version":"x"}`
	e.do(http.MethodPost, pathAdvance, "short", bad).wantError(t, http.StatusBadRequest, "bad_request")
	e.do(http.MethodPost, pathAdvance, newToken(t), bad).wantResult(t, "invalid")
	e.do(http.MethodPost, pathAdvance, e.stylist, bad).wantError(t, http.StatusForbidden, "forbidden")
	e.do(http.MethodPost, pathAdvance, e.stylist, strings.Repeat("x", api.MaxBodyBytes+1)).wantError(t, http.StatusForbidden, "forbidden")
	e.do(http.MethodPost, pathAdvance, e.master, bad).wantError(t, http.StatusBadRequest, "bad_request")
	e.do(http.MethodPost, pathAdvance, e.master, versionBody(e.version()+5)).wantError(t, http.StatusConflict, "version_conflict")
	e.setClock(at(0, 0).Add(-time.Minute))
	e.write(pathAdvance, e.master).wantError(t, http.StatusUnprocessableEntity, "not_event_day")
}

// Context errors on lookups and writes are 500, never "invalid" (tech-stack.md 2.1「逾時與錯誤」).
func TestUS_X_1_6_ContextErrorsAreNeverInvalid(t *testing.T) {
	e := newEnv(t)
	for _, mk := range []func() (context.Context, context.CancelFunc){
		func() (context.Context, context.CancelFunc) { c, f := context.WithCancel(bg); f(); return c, f },
		func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(bg, time.Now().Add(-time.Second))
		},
	} {
		for _, tok := range []string{e.master, newToken(t)} {
			c, cancel := mk()
			r := e.doReq(e.request(http.MethodGet, pathSnapshot, tok, "").WithContext(c))
			r.wantError(t, http.StatusInternalServerError, "internal")
			r = e.doReq(e.request(http.MethodPost, pathAdvance, tok, versionBody(e.version())).WithContext(c))
			r.wantError(t, http.StatusInternalServerError, "internal")
			cancel()
		}
		// Valid lookup, then the write's context ends.
		e.fs.writeCtx = func(context.Context) context.Context { c, _ := mk(); return c }
		e.write(pathAdvance, e.master).wantError(t, http.StatusInternalServerError, "internal")
		e.fs.writeCtx = nil
	}
	if e.snap(e.master)["eventStatus"] != "Draft" {
		t.Error("a write with an ended context was applied")
	}
}

// Every read of a lookup or snapshot has the 3 s timeout (tech-stack.md 2.1「逾時與錯誤」).
func TestReadsHaveThreeSecondTimeout(t *testing.T) {
	e := newEnv(t)
	e.get(e.master)
	e.write(pathAdvance, e.master)
	if len(e.fs.deadlines) != 3 {
		t.Fatalf("%d reads recorded, want 3 (snapshot; lookup and re-read)", len(e.fs.deadlines))
	}
	for i, d := range e.fs.deadlines {
		if d <= 0 || d > api.ReadTimeout {
			t.Errorf("read %d: deadline in %v, want within %v", i, d, api.ReadTimeout)
		}
	}
}

// ---------------------------------------------------------------- section 1

func TestAuthorizationHeaderIsStrict(t *testing.T) {
	e := newEnv(t)
	tok := e.master
	for name, set := range map[string]func(*http.Request){
		"missing": func(r *http.Request) {},
		"twice": func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer "+tok)
			r.Header.Add("Authorization", "Bearer "+tok)
		},
		"lower-case":      func(r *http.Request) { r.Header.Set("Authorization", "bearer "+tok) },
		"two spaces":      func(r *http.Request) { r.Header.Set("Authorization", "Bearer  "+tok) },
		"basic":           func(r *http.Request) { r.Header.Set("Authorization", "Basic "+tok) },
		"no scheme":       func(r *http.Request) { r.Header.Set("Authorization", tok) },
		"short token":     func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok[:21]) },
		"padded token":    func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok+"==") },
		"trailing space":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok+" ") },
		"std alphabet":    func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok[:21]+"+") },
		"two tokens":      func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok+", Bearer "+tok) },
		"query not taken": func(r *http.Request) { r.URL.RawQuery = "t=" + tok },
		"cookie not taken": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "t", Value: tok})
		},
	} {
		for _, p := range []string{pathSnapshot, pathAdvance} {
			method := http.MethodGet
			if p == pathAdvance {
				method = http.MethodPost
			}
			r := e.request(method, p, "", versionBody(e.version()))
			set(r)
			if got := e.doReq(r); got.Code != http.StatusBadRequest || got.body["error"] != "bad_request" {
				t.Errorf("%s %s: %d %s", name, p, got.Code, got.Body.String())
			}
		}
	}
	if e.snap(e.master)["eventStatus"] != "Draft" {
		t.Error("a malformed request changed the event")
	}
}

// api.md section 1, version and body rules: one JSON object of at most 16 KB with exactly
// "version", an integer of 1 or more.
func TestWriteBodyIsStrict(t *testing.T) {
	e := newEnv(t)
	v := jsonInt(e.version())
	for _, body := range []string{
		"", " ", "null", "{}", "[]", "1", `"x"`,
		`{"version":null}`, `{"version":"` + v + `"}`, `{"version":` + v + `.0}`, `{"version":1e0}`,
		`{"version":0}`, `{"version":-1}`, `{"version":true}`, `{"version":[1]}`, `{"version":{}}`,
		`{"version":99999999999999999999}`,
		`{"version":` + v + `,"x":1}`, `{"Version":` + v + `}`, `{"VERSION":` + v + `}`,
		`{"version":` + v + `,"version":` + v + `}`,
		`{"version":` + v + `}{"version":` + v + `}`, `{"version":` + v + `} x`, `{"version":` + v,
		`{"version":` + v + `,"eventId":1}`, `{"version":` + v + `,"roleId":1}`,
		`{"version":` + v + `,"pad":"` + strings.Repeat("x", api.MaxBodyBytes) + `"}`,
	} {
		r := e.do(http.MethodPost, pathAdvance, e.master, body)
		if r.Code != http.StatusBadRequest || r.body["error"] != "bad_request" {
			short := body
			if len(short) > 60 {
				short = short[:60] + "…"
			}
			t.Errorf("body %q: %d %s", short, r.Code, r.Body.String())
		}
	}
	if e.snap(e.master)["eventStatus"] != "Draft" {
		t.Fatal("a refused body changed the event")
	}
	// White space around a valid object is fine.
	e.do(http.MethodPost, pathAdvance, e.master, " \n{ \"version\" : "+v+" }\n ").wantResult(t, "ok")
}

// ------------------------------------- no-store; /api/report is gone

func TestEveryAPIFailureIsNoStoreWithoutResult(t *testing.T) {
	e := newEnv(t)
	// do() checks no-store and the absence of "result" on each of these.
	for _, c := range []struct {
		method, path, token, body string
		status                    int
	}{
		{http.MethodGet, "/api/nope", "", "", 404},
		{http.MethodGet, "/api/snapshot/extra", e.master, "", 404},
		{http.MethodPost, pathSnapshot, e.master, "", 405},
		{http.MethodGet, pathAdvance, e.master, "", 404}, // GET / catches every GET path
		{http.MethodDelete, pathAdvance, e.master, "", 405},
		{http.MethodPost, pathAdvance, e.master, strings.Repeat(" ", api.MaxBodyBytes+1) + versionBody(1), 400},
		{http.MethodGet, pathSnapshot, "", "", 400},
		{http.MethodPost, pathAdvance, e.stylist, versionBody(1), 403},
		{http.MethodPost, pathAdvance, e.master, versionBody(99), 409},
	} {
		if r := e.do(c.method, c.path, c.token, c.body); r.Code != c.status {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, r.Code, c.status)
		}
	}
	e.fs.snapshotErr = errors.New("boom")
	if r := e.get(e.master); r.Code != 500 {
		t.Errorf("500 case: %d", r.Code)
	}
	if r := e.do(http.MethodGet, "/api/ping", "", ""); r.Code != 200 {
		t.Errorf("ping: %d", r.Code)
	}
}

func TestReportEndpointIsGone(t *testing.T) {
	e := newEnv(t)
	for _, m := range []string{http.MethodPost, http.MethodGet} {
		r := e.do(m, "/api/report", "", `{"env":"line-ios","hasToken":true}`)
		if r.Code != http.StatusNotFound && r.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/report: %d", m, r.Code)
		}
	}
	if strings.Contains(e.logs.String(), "line-ios") {
		t.Error("report body was logged")
	}
}

// Logs never contain headers, bodies, tokens or hashes (tech-stack.md 2.1「日誌」), including
// when requests fail.
func TestLogsCarryNoCredentials(t *testing.T) {
	e := newEnv(t)
	e.get(e.master)
	e.write(pathAdvance, e.master)
	e.do(http.MethodPost, pathAdvance, e.master, `{"version":"secret-body"}`)
	e.fs.snapshotErr = errors.New("disk I/O error")
	e.get(e.master)
	e.get(e.stylist)
	e.fs.snapshotErr = nil
	e.fs.writeErr = errors.New("database is locked")
	e.write(pathAdvance, e.master)
	logs := e.logs.String()
	if !strings.Contains(logs, "api_error") {
		t.Fatalf("expected the failures to be logged: %s", logs)
	}
	for _, s := range []string{"Authorization", "Bearer", "secret-body"} {
		if strings.Contains(logs, s) {
			t.Errorf("log contains %q", s)
		}
	}
	if l := leaks(logs, e.master, e.stylist); len(l) > 0 {
		t.Errorf("log contains credential material %v: %s", l, logs)
	}
}

// Unexpected state is an error, not a guess (section 2.2).
func TestTwoCuesInProgressIs500(t *testing.T) {
	e := newEnv(t)
	e.start()
	e.mustWrite(pathAdvance)
	// The schema forbids two cues in progress; drop the index to fake a
	// corrupted database.
	e.exec(`DROP INDEX cues_one_in_progress`)
	e.exec(`UPDATE cues SET status = 'InProgress' WHERE id = ?`, e.cue["迎賓"])
	e.get(e.master).wantError(t, http.StatusInternalServerError, "internal")
	e.write(pathAdvance, e.master).wantError(t, http.StatusInternalServerError, "internal")
	// The stylist view does not depend on it and stays available.
	e.get(e.stylist).wantResult(t, "ok")
}
