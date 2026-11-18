package server

// Coverage of the Master's operations on the day (US-M-2 to US-M-9), added
// from the test audit: the edges the first tests left out.

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// covStale sends body with an old version to a Master write and checks the
// answer of US-M-10.9 / US-M-8.5: 409 with the Master's latest snapshot, and
// nothing written.
func (e *env) covStale(t *testing.T, name, path string, body map[string]any) {
	t.Helper()
	v := e.version()
	logs := e.count(`SELECT count(*) FROM action_logs`)
	cues := e.count(`SELECT count(*) FROM cues`)
	roles := e.count(`SELECT count(*) FROM roles`)
	assigns := e.count(`SELECT count(*) FROM cue_roles`)
	r := e.post(path, e.master, body)
	r.wantError(t, http.StatusConflict, "version_conflict")
	wantKeys(t, name+" 409", r.body, "error", "snapshot")
	s := r.snapshot(t)
	if s["eventVersion"] != float64(v) {
		t.Errorf("%s: 409 snapshot version = %v, want the latest %d", name, s["eventVersion"], v)
	}
	if _, ok := s["roles"]; !ok {
		t.Errorf("%s: 409 snapshot is not the Master's own", name)
	}
	if e.version() != v || e.count(`SELECT count(*) FROM action_logs`) != logs || e.count(`SELECT count(*) FROM cues`) != cues ||
		e.count(`SELECT count(*) FROM roles`) != roles || e.count(`SELECT count(*) FROM cue_roles`) != assigns {
		t.Errorf("%s: a stale write changed the event", name)
	}
}

// US-M-2.3: a skipped cue is not moved by a shift, whether it is the
// starting cue or in the middle.
func TestUS_M_2_3_ShiftDoesNotMoveSkippedCues(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.skip(e.cue["迎賓"]).wantResult(t, "ok")
	// Starting at the skipped cue: it stays at 0, 一進預備 is fixed, 敬酒 moves.
	p := previewOf(t, e.preview(e.master, shiftBody(0, e.cue["迎賓"], 5)))
	if got := movedIDs(p); len(got) != 1 || !got[float64(e.cue["敬酒"])] {
		t.Errorf("moved = %v, want only 敬酒", got)
	}
	s := e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 5))
	s.wantResult(t, "ok")
	snap := s.snapshot(t)
	if got := byTitle(t, snap, "迎賓")["offsetMin"]; got != 0.0 {
		t.Errorf("skipped 迎賓 offset = %v, want 0", got)
	}
	if got := byTitle(t, snap, "敬酒")["offsetMin"]; got != 5.0 {
		t.Errorf("敬酒 offset = %v, want 5", got)
	}
	// Starting before it, the skipped cue in the middle is passed over.
	e2 := newEnv(t)
	e2.live()
	e2.skip(e2.cue["一進預備"]).wantResult(t, "ok")
	p = previewOf(t, e2.preview(e2.master, shiftBody(0, e2.cue["迎賓"], 5)))
	if got := movedIDs(p); len(got) != 2 || !got[float64(e2.cue["迎賓"])] || !got[float64(e2.cue["敬酒"])] {
		t.Errorf("moved = %v, want 迎賓 and 敬酒", got)
	}
}

// US-M-2 edge: the shift may start at a cue that is already completed.
func TestUS_M_2_ShiftMayStartAtACompletedCue(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.mustWrite(pathAdvance) // 新娘化妝 completed, 迎賓 in progress
	p := previewOf(t, e.preview(e.master, shiftBody(0, e.cue["新娘化妝"], 5)))
	if got := movedIDs(p); len(got) != 1 || !got[float64(e.cue["敬酒"])] {
		t.Errorf("moved = %v, want only 敬酒 (迎賓 is in progress, 一進預備 fixed)", got)
	}
	s := e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["新娘化妝"], 5))
	s.wantResult(t, "ok")
	snap := s.snapshot(t)
	if got := byTitle(t, snap, "新娘化妝")["offsetMin"]; got != 0.0 {
		t.Errorf("completed cue offset = %v, want 0", got)
	}
	if got := byTitle(t, snap, "敬酒")["offsetMin"]; got != 5.0 {
		t.Errorf("敬酒 offset = %v, want 5", got)
	}
}

// US-M-2.6: exactly +1440 and -1440 minutes are accepted.
func TestUS_M_2_6_ShiftOf1440MinutesIsAccepted(t *testing.T) {
	e := newEnv(t)
	e.live()
	for _, d := range []int{1441, -1441} {
		e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], d)).
			wantError(t, http.StatusUnprocessableEntity, "invalid_delta")
	}
	s := e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 1440))
	s.wantResult(t, "ok")
	if got := byTitle(t, s.snapshot(t), "敬酒")["offsetMin"]; got != 1440.0 {
		t.Errorf("敬酒 offset = %v, want 1440", got)
	}
	// US-M-2 edge: +1440 moves a cue past midnight and past the event's end
	// without breaking anything; it can be undone.
	if got := byTitle(t, e.mustWrite(pathUndo), "敬酒")["offsetMin"]; got != 0.0 {
		t.Errorf("offset after undo = %v", got)
	}
	// -1440 is accepted when no cue ends up before now.
	e.setClock(at(0, 0).AddDate(0, 0, -3))
	s = e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], -1440))
	s.wantResult(t, "ok")
	if got := byTitle(t, s.snapshot(t), "迎賓")["offsetMin"]; got != -1440.0 {
		t.Errorf("迎賓 offset = %v, want -1440", got)
	}
}

// US-M-3 edge: once Live, an advance is not tied to the event day: after
// midnight and before 04:00 it still works.
func TestUS_M_3_LiveAdvanceAfterMidnightStillWorks(t *testing.T) {
	e := newEnv(t)
	e.setClock(at(23, 59))
	e.mustWrite(pathAdvance) // started on the event day
	next := func(h, m int) time.Time { return time.Date(2027, 6, 17, h, m, 0, 0, taipei) }
	e.setClock(next(0, 1))
	s := e.mustWrite(pathAdvance)
	if byTitle(t, s, "新娘化妝")["status"] != "Completed" || byTitle(t, s, "迎賓")["status"] != "InProgress" {
		t.Errorf("after midnight: %v / %v", byTitle(t, s, "新娘化妝")["status"], byTitle(t, s, "迎賓")["status"])
	}
	e.setClock(next(3, 59))
	e.mustWrite(pathAdvance)
	// 04:00 is the end.
	e.setClock(next(4, 0))
	e.get(e.master).wantResult(t, "ended")
}

// US-M-4 edge: after an undo the Master can go on, each write moving the
// version by one, and a second device still on an older version is refused.
func TestUS_M_4_AdvanceAfterUndoAndAnotherDeviceIsStale(t *testing.T) {
	e := newEnv(t)
	e.live()
	deviceB := e.version() // device B shows this version
	v := e.version()
	e.mustWrite(pathAdvance)
	e.mustWrite(pathUndo)
	if got := byTitle(t, e.snap(e.master), "新娘化妝")["status"]; got != "InProgress" {
		t.Fatalf("after undo: 新娘化妝 is %v", got)
	}
	s := e.mustWrite(pathAdvance)
	if e.version() != v+3 {
		t.Errorf("version = %d, want %d: advance, undo and advance one each", e.version(), v+3)
	}
	if byTitle(t, s, "新娘化妝")["status"] != "Completed" || byTitle(t, s, "迎賓")["status"] != "InProgress" {
		t.Error("the advance after the undo did not apply")
	}
	// Device B, still on the version before all three, is refused for
	// advance and undo alike, with the latest snapshot.
	for name, path := range map[string]string{"advance": pathAdvance, "undo": pathUndo} {
		r := e.do(http.MethodPost, path, e.master, versionBody(deviceB))
		r.wantError(t, http.StatusConflict, "version_conflict")
		if r.snapshot(t)["eventVersion"] != float64(e.version()) {
			t.Errorf("%s: 409 snapshot is not the latest", name)
		}
	}
	if e.version() != v+3 {
		t.Error("a stale write changed the event")
	}
}

// US-M-4.8: the undo of a shift is refused once a cue it moved was changed
// by something else, and nothing is written.
func TestUS_M_4_8_UndoOfAShiftIsRefusedWhenACueChanged(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 5)).wantResult(t, "ok")
	e.exec(`UPDATE cues SET status = 'Skipped' WHERE id = ?`, e.cue["敬酒"])
	v := e.version()
	e.write(pathUndo, e.master).wantError(t, http.StatusUnprocessableEntity, "undo_stale")
	if e.version() != v {
		t.Error("a refused undo changed the event")
	}
	s := e.snap(e.master)
	if byTitle(t, s, "迎賓")["offsetMin"] != 5.0 || byTitle(t, s, "敬酒")["offsetMin"] != 5.0 {
		t.Error("a refused undo moved the cues back")
	}
	if u := s["undo"].(map[string]any); u["available"] != false {
		t.Errorf("undo = %v, want no button for an undo that would be refused", u)
	}
}

// US-M-8.5: insert, title edit and assignment with an old version are 409
// and write nothing.
func TestUS_M_8_5_StaleVersionOnInsertEditAndAssign(t *testing.T) {
	e := newEnv(t)
	e.live()
	stale := e.version() - 1
	e.covStale(t, "insert", pathInsert, insertBody(stale, e.cue["迎賓"], at(12, 0), assignment(e.stylistB, nil)))
	e.covStale(t, "edit", pathEdit, map[string]any{"version": stale, "cueId": e.cue["迎賓"], "title": "改了", "notePublic": nil})
	e.covStale(t, "assign", pathAssign, map[string]any{"version": stale, "cueId": e.cue["迎賓"], "roles": []any{assignment(e.stylistB, "x")}})
	if byTitle(t, e.snap(e.master), "迎賓") == nil {
		t.Error("the title was changed by a stale edit")
	}
	// The same bodies with the current version go through.
	e.insert(e.cue["迎賓"], assignment(e.stylistB, nil)).wantResult(t, "ok")
}

// US-M-8.3: a cue that is completed or skipped cannot be given to anyone
// else, and its assignments stay as they were.
func TestUS_M_8_3_AssignCompletedOrSkippedCueIsRefused(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.skip(e.cue["敬酒"]).wantResult(t, "ok")
	e.mustWrite(pathAdvance) // 新娘化妝 completed, 迎賓 in progress
	v := e.version()
	assigns := e.count(`SELECT count(*) FROM cue_roles`)
	for _, title := range []string{"新娘化妝", "敬酒", "迎賓"} {
		e.assign(e.cue[title], assignment(e.stylistRole, "改派")).wantError(t, http.StatusUnprocessableEntity, "not_pending")
		e.assign(e.cue[title]).wantError(t, http.StatusUnprocessableEntity, "not_pending")
	}
	if e.version() != v || e.count(`SELECT count(*) FROM cue_roles`) != assigns {
		t.Error("a refused assignment changed the event")
	}
	if c := byTitle(t, e.snap(e.stylistBTok), "新娘化妝"); c == nil || c["roleNote"] != "B專屬備註" {
		t.Errorf("新秘B's note of the completed cue = %v", c)
	}
}

// US-M-8.4: a successful insert and a successful assignment each write
// their own log entry, naming the cue and the roles.
func TestUS_M_8_4_InsertAndAssignAreLogged(t *testing.T) {
	e := newEnv(t)
	e.live()
	if e.count(`SELECT count(*) FROM action_logs WHERE type IN ('rundown.insert', 'rundown.assign')`) != 0 {
		t.Fatal("logs before any write")
	}
	e.insert(e.cue["敬酒"], assignment(e.stylistB, "新"), assignment(e.stylistRole, nil)).wantResult(t, "ok")
	var inserted int64
	e.db().QueryRow(`SELECT id FROM cues WHERE title = '長輩致詞'`).Scan(&inserted)
	l := e.lastLog("rundown.insert")
	if l["cueId"] != float64(inserted) || len(l["added"].([]any)) != 2 {
		t.Errorf("rundown.insert log = %v", l)
	}
	if n := e.count(`SELECT count(*) FROM action_logs WHERE type = 'rundown.insert'`); n != 1 {
		t.Errorf("%d insert logs, want 1", n)
	}
	// 敬酒 had 新秘B only; now 新秘 only.
	e.assign(e.cue["敬酒"], assignment(e.stylistRole, "換人")).wantResult(t, "ok")
	l = e.lastLog("rundown.assign")
	added, removed := l["added"].([]any), l["removed"].([]any)
	if l["cueId"] != float64(e.cue["敬酒"]) || len(added) != 1 || added[0] != float64(e.stylistRole) ||
		len(removed) != 1 || removed[0] != float64(e.stylistB) {
		t.Errorf("rundown.assign log = %v", l)
	}
	if n := e.count(`SELECT count(*) FROM action_logs WHERE type = 'rundown.assign'`); n != 1 {
		t.Errorf("%d assign logs, want 1", n)
	}
}

// US-M-8.1 / US-M-8.7: the limits of an inserted cue are inclusive; the
// title edit shares the title and note limits.
func TestUS_M_8_1_InsertBoundariesAreAccepted(t *testing.T) {
	e := newEnv(t)
	e.live()
	last := e.cue["敬酒"]
	nextDay := func(h, m int) time.Time { return time.Date(2027, 6, 17, h, m, 0, 0, taipei) }
	body := func(change func(map[string]any)) map[string]any {
		b := insertBody(e.version(), last, at(12, 0))
		change(b)
		return b
	}
	for name, c := range map[string]struct {
		change func(map[string]any)
		ok     bool
	}{
		"start at 00:00 of the event day": {func(b map[string]any) { b["start"] = at(0, 0).UTC().Format(time.RFC3339) }, true},
		"start next day 03:59":            {func(b map[string]any) { b["start"] = nextDay(3, 59).UTC().Format(time.RFC3339) }, true},
		"start next day 04:00":            {func(b map[string]any) { b["start"] = nextDay(4, 0).UTC().Format(time.RFC3339) }, false},
		"start a minute before the day":   {func(b map[string]any) { b["start"] = at(0, 0).Add(-time.Minute).UTC().Format(time.RFC3339) }, false},
		"duration 0":                      {func(b map[string]any) { b["durationMin"] = 0 }, true},
		"duration 1":                      {func(b map[string]any) { b["durationMin"] = 1 }, true},
		"duration 1440":                   {func(b map[string]any) { b["durationMin"] = 1440 }, true},
		"duration 1441":                   {func(b map[string]any) { b["durationMin"] = 1441 }, false},
		"title of 100 characters":         {func(b map[string]any) { b["title"] = strings.Repeat("字", 100) }, true},
		"title of 101 characters":         {func(b map[string]any) { b["title"] = strings.Repeat("字", 101) }, false},
		"note of 500 characters":          {func(b map[string]any) { b["notePublic"] = strings.Repeat("字", 500) }, true},
		"note of 501 characters":          {func(b map[string]any) { b["notePublic"] = strings.Repeat("字", 501) }, false},
	} {
		v := e.version()
		r := e.post(pathInsert, e.master, body(c.change))
		if c.ok {
			r.wantResult(t, "ok")
			if e.version() != v+1 {
				t.Errorf("%s: version %d → %d", name, v, e.version())
			}
			continue
		}
		r.wantError(t, http.StatusUnprocessableEntity, "invalid_cue")
		if e.version() != v {
			t.Errorf("%s: a refused insert changed the event", name)
		}
	}
	// Negative length is malformed, not a cue.
	e.post(pathInsert, e.master, body(func(b map[string]any) { b["durationMin"] = -1 })).wantError(t, http.StatusBadRequest, "bad_request")

	// The title edit: 100 characters in, 101 out; the note 500 in, 501 out.
	edit := func(title string, note any) resp {
		return e.post(pathEdit, e.master, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"], "title": title, "notePublic": note})
	}
	edit(strings.Repeat("字", 100), strings.Repeat("字", 500)).wantResult(t, "ok")
	edit(strings.Repeat("字", 101), nil).wantError(t, http.StatusUnprocessableEntity, "invalid_cue")
	edit("x", strings.Repeat("字", 501)).wantError(t, http.StatusUnprocessableEntity, "invalid_cue")
}

// US-M-8.6: an inserted cue that starts before the one ahead is shown with
// that cue, and a skipped cue ahead is looked past.
func TestUS_M_8_6_OutOfOrderLooksPastASkippedCue(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.skip(e.cue["敬酒"]).wantResult(t, "ok")
	// 12:00 is before 敬酒 (13:00, skipped) but after 一進預備 (11:15): fine.
	e.insert(e.cue["敬酒"]).wantResult(t, "ok")
	if o := e.snap(e.master)["outOfOrder"].([]any); len(o) != 0 {
		t.Errorf("outOfOrder = %v, want none: the cue ahead is skipped", o)
	}
	// 10:00 is before 一進預備 (11:15), the cue ahead once 敬酒 is looked past.
	e.post(pathInsert, e.master, insertBody(e.version(), e.cue["敬酒"], at(10, 0))).wantResult(t, "ok")
	var early int64
	e.db().QueryRow(`SELECT id FROM cues WHERE seq = 5`).Scan(&early)
	o := e.snap(e.master)["outOfOrder"].([]any)
	if len(o) != 1 {
		t.Fatalf("outOfOrder = %v, want one", o)
	}
	w := o[0].(map[string]any)
	if w["cueId"] != float64(early) || w["prevCueId"] != float64(e.cue["一進預備"]) {
		t.Errorf("warning = %v, want the 10:00 cue against 一進預備", w)
	}
}

// US-M-9.2: under maintenance the Master shifts, skips and inserts as
// usual; everyone else is refused with 503.
func TestUS_M_9_2_MasterOperatesUnderMaintenance(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.maintenance()
	v := e.version()
	e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 5)).wantResult(t, "ok")
	e.skip(e.cue["敬酒"]).wantResult(t, "ok")
	e.insert(e.cue["迎賓"]).wantResult(t, "ok")
	e.assign(e.cue["迎賓"], assignment(e.stylistB, nil)).wantResult(t, "ok")
	e.proxyCheckIn(e.stylistRole, true).wantResult(t, "ok")
	if e.version() != v+4 {
		t.Errorf("version %d → %d, want four writes", v, e.version())
	}
	// A role's link is refused for the same writes.
	v = e.version()
	e.do(http.MethodPost, pathShift, e.stylist, shiftBody(e.version(), e.cue["迎賓"], 5)).wantMaintenance(t, "stylist shift")
	e.post(pathSkip, e.stylist, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"]}).wantMaintenance(t, "stylist skip")
	e.post(pathInsert, e.stylist, insertBody(e.version(), e.cue["迎賓"], at(12, 0))).wantMaintenance(t, "stylist insert")
	if e.version() != v {
		t.Error("a refused request changed the event")
	}
}

// US-M-7.1 / US-M-13.2: the QR of a reissue is the QR of the new link, not
// of the old one; and US-M-7.6: the log says which role and when.
func TestUS_M_7_ReissueQRIsTheNewLinkAndTheLogHasRoleAndTime(t *testing.T) {
	e := newEnv(t)
	e.setClock(at(8, 30))
	r := e.post(pathReissue, e.master, map[string]any{"roleId": e.stylistRole, "origin": origin})
	r.wantResult(t, "ok")
	wantKeys(t, "reissue", r.body, "result", "snapshot", "token", "qr")
	tok := r.body["token"].(string)
	qrOf := func(token string) string {
		png, err := qrcode.Encode(origin+"/#t="+token, qrcode.Medium, 256)
		if err != nil {
			t.Fatal(err)
		}
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	if r.body["qr"] != qrOf(tok) {
		t.Error("the QR is not the new link's")
	}
	if r.body["qr"] == qrOf(e.stylist) {
		t.Error("the QR is the old link's")
	}
	e.snap(tok) // the new link works
	// The log names the role and the Master's link, at the time of the write.
	l := e.lastLog("token.reissue")
	if l["roleId"] != float64(e.stylistRole) || l["byTokenId"] != e.masterTokenID() {
		t.Errorf("log = %v", l)
	}
	var loggedAt int64
	e.db().QueryRow(`SELECT at FROM action_logs WHERE type = 'token.reissue' ORDER BY id DESC LIMIT 1`).Scan(&loggedAt)
	if loggedAt != e.clock().UnixNano() {
		t.Errorf("logged at %d, want %d", loggedAt, e.clock().UnixNano())
	}
	e.setClock(at(9, 15))
	e.post(pathRevoke, e.master, map[string]any{"roleId": e.stylistRole}).wantResult(t, "ok")
	l = e.lastLog("token.revoke")
	if l["roleId"] != float64(e.stylistRole) {
		t.Errorf("revoke log = %v", l)
	}
	e.db().QueryRow(`SELECT at FROM action_logs WHERE type = 'token.revoke' ORDER BY id DESC LIMIT 1`).Scan(&loggedAt)
	if loggedAt != e.clock().UnixNano() {
		t.Errorf("revoke logged at %d, want %d", loggedAt, e.clock().UnixNano())
	}
}
