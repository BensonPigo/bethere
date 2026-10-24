package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
)

const (
	pathUpdate        = "/api/cues/update"
	pathMove          = "/api/cues/move"
	pathDeleteCue     = "/api/cues/delete"
	pathImportPreview = "/api/rundown/import/preview"
	pathImport        = "/api/rundown/import"
	pathAddRole       = "/api/roles/add"
	pathRename        = "/api/roles/rename"
	pathDeleteRole    = "/api/roles/delete"
	pathPreviewRole   = "/api/roles/preview"
)

const origin = "https://bethere.example"

// dayBefore puts the clock on the day before the event, when every edit
// of the preparation is open (ADR-0032).
func (e *env) dayBefore() { e.setClock(at(9, 0).AddDate(0, 0, -1)) }

func updateBody(v, cueID int64, start time.Time, title string) map[string]any {
	return map[string]any{"version": v, "cueId": cueID, "start": start.UTC().Format(time.RFC3339),
		"durationMin": 25, "isFixed": true, "title": title, "notePublic": "新備註"}
}

func (e *env) addRole(name, view string, checksIn bool) resp {
	e.t.Helper()
	return e.post(pathAddRole, e.master, map[string]any{"name": name, "view": view, "checksIn": checksIn, "origin": origin})
}

// seqs is the cue titles in seq order, failing unless seq runs 1..n.
func (e *env) seqs() []string {
	e.t.Helper()
	rows, err := e.db().Query(`SELECT seq, title FROM cues WHERE event_id = ? ORDER BY seq`, e.eventID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var (
			seq   int
			title string
		)
		rows.Scan(&seq, &title)
		if seq != len(titles)+1 {
			e.t.Errorf("seq %d at position %d: not 1..n", seq, len(titles)+1)
		}
		titles = append(titles, title)
	}
	return titles
}

// lastLog is the payload of the latest log entry of a type.
func (e *env) lastLog(typ string) map[string]any {
	e.t.Helper()
	var p string
	if err := e.db().QueryRow(`SELECT payload FROM action_logs WHERE type = ? ORDER BY id DESC LIMIT 1`, typ).Scan(&p); err != nil {
		e.t.Fatalf("no %s log: %v", typ, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(p), &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) masterTokenID() float64 {
	e.t.Helper()
	var id int64
	e.db().QueryRow(`SELECT id FROM access_tokens WHERE token_hash = ?`, auth.HashToken(e.master)).Scan(&id)
	return float64(id)
}

// ---------------------------------------------------------------- US-M-10

func TestUS_M_10_2_UpdateCue(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	v := e.version()
	r := e.post(pathUpdate, e.master, updateBody(v, e.cue["迎賓"], at(10, 40), "  賓客入場  "))
	r.wantResult(t, "ok")
	c := byTitle(t, r.snapshot(t), "賓客入場")
	if c["scheduledStart"] != "2027-06-06T02:40:00.000Z" || c["durationMin"] != 25.0 || c["isFixed"] != true ||
		c["notePublic"] != "新備註" || c["offsetMin"] != 0.0 {
		t.Errorf("cue = %v", c)
	}
	if e.version() != v+1 {
		t.Errorf("version %d → %d", v, e.version())
	}
	if l := e.lastLog("rundown.update"); l["byTokenId"] != e.masterTokenID() {
		t.Errorf("log = %v", l)
	}
	// The limits of a cue added by the Master (US-M-10.2).
	for _, bad := range []map[string]any{
		updateBody(e.version(), e.cue["迎賓"], at(10, 0), ""),
		updateBody(e.version(), e.cue["迎賓"], at(10, 0), strings.Repeat("字", 101)),
		updateBody(e.version(), e.cue["迎賓"], at(0, 0).Add(-time.Minute), "x"),
		updateBody(e.version(), e.cue["迎賓"], at(0, 0).AddDate(0, 0, 1).Add(4*time.Hour), "x"),
	} {
		e.post(pathUpdate, e.master, bad).wantError(t, http.StatusUnprocessableEntity, "invalid_cue")
	}
	long := updateBody(e.version(), e.cue["迎賓"], at(10, 0), "x")
	long["durationMin"] = 1441
	e.post(pathUpdate, e.master, long).wantError(t, http.StatusUnprocessableEntity, "invalid_cue")
	// Live, the structure is the shift's (US-M-10.7).
	e.live()
	v = e.version()
	e.post(pathUpdate, e.master, updateBody(v, e.cue["敬酒"], at(13, 30), "敬酒")).
		wantError(t, http.StatusUnprocessableEntity, "not_draft")
	if e.version() != v {
		t.Error("a live update changed the event")
	}
}

func TestUS_M_10_3_MoveCue(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	e.post(pathMove, e.master, map[string]any{"version": e.version(), "cueId": e.cue["敬酒"], "afterCueId": nil}).wantResult(t, "ok")
	if got := e.seqs(); !slices.Equal(got, []string{"敬酒", "新娘化妝", "迎賓", "一進預備"}) {
		t.Errorf("after moving first: %v", got)
	}
	e.post(pathMove, e.master, map[string]any{"version": e.version(), "cueId": e.cue["敬酒"], "afterCueId": e.cue["迎賓"]}).
		wantResult(t, "ok")
	if got := e.seqs(); !slices.Equal(got, []string{"新娘化妝", "迎賓", "敬酒", "一進預備"}) {
		t.Errorf("after moving: %v", got)
	}
	// The order no longer matches the times: shown, not refused (US-M-10.6).
	if o := e.snap(e.master)["outOfOrder"].([]any); len(o) == 0 {
		t.Error("no outOfOrder warning")
	}
	e.post(pathMove, e.master, map[string]any{"version": e.version(), "cueId": e.cue["敬酒"], "afterCueId": e.cue["敬酒"]}).
		wantError(t, http.StatusUnprocessableEntity, "invalid_position")
	e.live()
	e.post(pathMove, e.master, map[string]any{"version": e.version(), "cueId": e.cue["敬酒"], "afterCueId": nil}).
		wantError(t, http.StatusUnprocessableEntity, "not_draft")
}

func TestUS_M_10_4_DeleteCue(t *testing.T) {
	e := newEnv(t)
	// Deleting one cue stays open on the event day, before the first
	// advance (ADR-0032).
	v := e.version()
	r := e.post(pathDeleteCue, e.master, map[string]any{"version": v, "cueId": e.cue["新娘化妝"]})
	r.wantResult(t, "ok")
	if got := e.seqs(); !slices.Equal(got, []string{"迎賓", "一進預備", "敬酒"}) {
		t.Errorf("cues = %v", got)
	}
	if n := e.count(`SELECT count(*) FROM cue_roles WHERE cue_id = ?`, e.cue["新娘化妝"]); n != 0 {
		t.Errorf("%d assignments left", n)
	}
	// Everything the cue had is in the log, so it can be put back by hand.
	l := e.lastLog("rundown.delete")
	cue := l["cue"].(map[string]any)
	if cue["title"] != "新娘化妝" || cue["durationMin"] != 120.0 || l["byTokenId"] != e.masterTokenID() {
		t.Errorf("log = %v", l)
	}
	notes := map[float64]any{}
	for _, a := range cue["roles"].([]any) {
		m := a.(map[string]any)
		notes[m["roleId"].(float64)] = m["roleNote"]
	}
	if notes[float64(e.stylistRole)] != "新娘房" || notes[float64(e.stylistB)] != "B專屬備註" {
		t.Errorf("logged assignments = %v", cue["roles"])
	}
	// The Stylist's own snapshot no longer has it.
	if _, ok := e.snap(e.stylist)["cues"].([]any); !ok {
		t.Fatal("no cues")
	}
	e.live()
	e.post(pathDeleteCue, e.master, map[string]any{"version": e.version(), "cueId": e.cue["敬酒"]}).
		wantError(t, http.StatusUnprocessableEntity, "not_draft")
}

func TestUS_M_10_5_InsertFirstInDraft(t *testing.T) {
	e := newEnv(t)
	// 0 is not an id; null is the first place (api.md section 3).
	e.post(pathInsert, e.master, insertBody(e.version(), 0, at(7, 0))).wantError(t, http.StatusBadRequest, "bad_request")
	body := insertBody(e.version(), 0, at(7, 0))
	body["afterCueId"] = nil
	e.post(pathInsert, e.master, body).wantResult(t, "ok")
	if got := e.seqs(); got[0] != "長輩致詞" || len(got) != 5 {
		t.Errorf("cues = %v", got)
	}
	e.live()
	body["version"] = e.version()
	e.post(pathInsert, e.master, body).wantError(t, http.StatusUnprocessableEntity, "invalid_position")
}

func TestUS_M_10_8_DraftCueWithAnOffsetIsRefused(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE cues SET offset_min = 3 WHERE id = ?`, e.cue["迎賓"])
	v := e.version()
	e.post(pathUpdate, e.master, updateBody(v, e.cue["迎賓"], at(10, 40), "x")).wantError(t, http.StatusInternalServerError, "internal")
	if e.version() != v || e.count(`SELECT count(*) FROM cues WHERE title = 'x'`) != 0 {
		t.Error("the cue was written")
	}
}

// A skip whose cue is deleted or replaced can no longer be undone, and no
// button is offered (api.md 5.2).
func TestM2cEditsMakeTheSkipUndoStale(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	e.skip(e.cue["迎賓"]).wantResult(t, "ok")
	if u := e.snap(e.master)["undo"].(map[string]any); u["available"] != true {
		t.Fatalf("undo = %v", u)
	}
	e.post(pathDeleteCue, e.master, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"]}).wantResult(t, "ok")
	if u := e.snap(e.master)["undo"].(map[string]any); u["available"] != false || u["description"] != nil {
		t.Errorf("undo = %v", u)
	}
	e.write(pathUndo, e.master).wantError(t, http.StatusUnprocessableEntity, "undo_stale")
}

// ---------------------------------------------------------------- US-M-11, US-M-13

func TestUS_M_11_AddRoleWithLinkAndQR(t *testing.T) {
	e := newEnv(t)
	v := e.version()
	r := e.addRole("  伴娘  ", "Glance", true)
	r.wantResult(t, "ok")
	wantKeys(t, "add response", r.body, "result", "snapshot", "roleId", "token", "qr")
	tok := r.body["token"].(string)
	// The QR code is the very link the screen shows (US-M-13.2).
	want, err := qrcode.Encode(origin+"/#t="+tok, qrcode.Medium, 256)
	if err != nil {
		t.Fatal(err)
	}
	if r.body["qr"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(want) {
		t.Error("qr is not the link's QR code")
	}
	s := e.snap(tok)
	if role := s["role"].(map[string]any); role["name"] != "伴娘" || role["type"] != "Media" || role["view"] != "Glance" ||
		role["checksIn"] != true {
		t.Errorf("role = %v", role)
	}
	item := e.roleItem(t, r.snapshot(t), int64(r.body["roleId"].(float64)))
	if item["view"] != "Glance" || item["checksIn"] != true || item["hasLink"] != true {
		t.Errorf("role item = %v", item)
	}
	if e.version() != v {
		t.Errorf("version %d → %d", v, e.version())
	}
	// The token, QR and origin appear in that one response only.
	var logs string
	e.db().QueryRow(`SELECT group_concat(type || ' ' || payload, ';') FROM action_logs`).Scan(&logs)
	if l := leaks(logs+e.get(e.master).Body.String(), tok); len(l) > 0 || strings.Contains(logs, origin) {
		t.Errorf("credential or origin in the log or snapshot: %v", l)
	}
	if l := e.lastLog("role.add"); l["byTokenId"] != e.masterTokenID() || l["name"] != "伴娘" {
		t.Errorf("log = %v", l)
	}
	// The screens map to types; the dashboard never checks in.
	for view, typ := range map[string]string{"HandsFree": "Stylist", "Dashboard": "Host"} {
		r := e.addRole("角色"+view, view, false)
		r.wantResult(t, "ok")
		if got := e.snap(r.body["token"].(string))["role"].(map[string]any); got["type"] != typ || got["checksIn"] != false {
			t.Errorf("%s: role = %v", view, got)
		}
	}
	// Refusals (api.md section 6).
	for _, c := range []struct {
		name, view string
		checksIn   bool
		code       string
	}{
		{"伴娘", "Glance", true, "duplicate_name"},
		{"總召", "Glance", true, "duplicate_name"},
		{"", "Glance", true, "invalid_role"},
		{strings.Repeat("名", 51), "Glance", true, "invalid_role"},
		{"伴;娘", "Glance", true, "invalid_role"},
		{"伴​娘", "Glance", true, "invalid_role"},
		{"新人二", "Dashboard", true, "invalid_role"},
		{"總召二", "Console", false, "invalid_role"},
	} {
		e.addRole(c.name, c.view, c.checksIn).wantError(t, http.StatusUnprocessableEntity, c.code)
	}
	for i := e.count(`SELECT count(*) FROM roles WHERE event_id = ?`, e.eventID); i < domain.MaxRoles; i++ {
		e.addRole("角色"+string(rune('A'+i)), "Glance", true).wantResult(t, "ok")
	}
	e.addRole("第五十一", "Glance", true).wantError(t, http.StatusUnprocessableEntity, "too_many_roles")
}

// An origin that is not this site's own is refused before anything is
// written: the QR code would send the token elsewhere (security review P2).
func TestUS_M_13_OriginMustBeThisSite(t *testing.T) {
	e := newEnv(t)
	for _, bad := range []string{
		"https://bethere.example@evil.example", "https://bethere.example/path", "https://bethere.example/",
		"https://bethere.example?q", "https://bethere.example#f", `https:\\evil.example`, "HTTPS://bethere.example",
		"https://Bethere.example", "http://bethere.example", "javascript:alert(1)", "https://bethere.example:0",
		"https://bethere.example:99999", "https://" + strings.Repeat("a", 200) + ".example", "https://",
	} {
		r := e.post(pathAddRole, e.master, map[string]any{"name": "伴娘", "view": "Glance", "checksIn": true, "origin": bad})
		r.wantError(t, http.StatusBadRequest, "bad_request")
		e.post(pathReissue, e.master, map[string]any{"roleId": e.stylistRole, "origin": bad}).wantError(t, http.StatusBadRequest, "bad_request")
	}
	if n := e.count(`SELECT count(*) FROM roles WHERE name = '伴娘'`); n != 0 {
		t.Errorf("%d roles created with a bad origin", n)
	}
	e.get(e.stylist).wantResult(t, "ok") // never reissued
	for _, good := range []string{"http://localhost:5173", "http://127.0.0.1:18090", "https://e2e.34-80-7-111.sslip.io"} {
		r := e.post(pathReissue, e.master, map[string]any{"roleId": e.stylistB, "origin": good})
		r.wantResult(t, "ok")
		wantKeys(t, "reissue with origin", r.body, "result", "snapshot", "token", "qr")
	}
}

func TestUS_M_11_RoleWithoutCheckIn(t *testing.T) {
	e := newEnv(t)
	r := e.addRole("伴郎", "Glance", false)
	r.wantResult(t, "ok")
	tok, id := r.body["token"].(string), int64(r.body["roleId"].(float64))
	if e.snap(tok)["role"].(map[string]any)["checksIn"] != false || e.roleItem(t, e.snap(e.master), id)["checksIn"] != false {
		t.Error("checksIn is not false")
	}
	e.checkIn(tok, true).wantError(t, http.StatusUnprocessableEntity, "not_checkin_role")
	e.proxyCheckIn(id, true).wantError(t, http.StatusUnprocessableEntity, "not_checkin_role")
	e.assign(e.cue["迎賓"], assignment(id, "門口")).wantResult(t, "ok")
	if c := e.snap(tok)["cues"].([]any); len(c) != 1 {
		t.Errorf("cues = %v", c)
	}
}

func TestUS_M_11_4_Rename(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.post(pathRename, e.master, map[string]any{"roleId": e.stylistRole, "name": " 化妝師小美 "}).wantResult(t, "ok")
	if n := e.snap(e.stylist)["role"].(map[string]any)["name"]; n != "化妝師小美" {
		t.Errorf("name = %v", n)
	}
	e.post(pathRename, e.master, map[string]any{"roleId": e.masterRole, "name": "總召阿明"}).wantResult(t, "ok")
	e.post(pathRename, e.master, map[string]any{"roleId": e.stylistB, "name": "總召阿明"}).
		wantError(t, http.StatusUnprocessableEntity, "duplicate_name")
	// Renaming to its own name is not a duplicate.
	e.post(pathRename, e.master, map[string]any{"roleId": e.stylistB, "name": "新秘B"}).wantResult(t, "ok")
	if l := e.lastLog("role.rename"); l["byTokenId"] != e.masterTokenID() {
		t.Errorf("log = %v", l)
	}
}

func TestUS_M_11_5_DeleteRole(t *testing.T) {
	e := newEnv(t)
	// On the event day, deleting a role is locked (ADR-0032).
	v := e.version()
	e.post(pathDeleteRole, e.master, map[string]any{"version": v, "roleId": e.stylistB}).
		wantError(t, http.StatusUnprocessableEntity, "locked_on_event_day")
	e.setClock(at(0, 0).Add(-time.Minute)) // 23:59 the day before
	e.post(pathDeleteRole, e.master, map[string]any{"version": v, "roleId": e.masterRole}).
		wantError(t, http.StatusUnprocessableEntity, "master_role")
	e.post(pathDeleteRole, e.master, map[string]any{"version": v, "roleId": e.stylistB}).wantResult(t, "ok")
	e.get(e.stylistBTok).wantResult(t, "invalid")
	if e.version() != v+1 || e.count(`SELECT count(*) FROM cue_roles WHERE role_id = ?`, e.stylistB) != 0 {
		t.Error("assignments or version")
	}
	l := e.lastLog("role.delete")
	if l["name"] != "新秘B" || l["type"] != "Stylist" || l["byTokenId"] != e.masterTokenID() || len(l["assignments"].([]any)) != 2 {
		t.Errorf("log = %v", l)
	}
	var logs string
	e.db().QueryRow(`SELECT group_concat(payload, ';') FROM action_logs`).Scan(&logs)
	if lk := leaks(logs, e.stylistBTok); len(lk) > 0 {
		t.Errorf("token in the log: %v", lk)
	}
	// A role whose link is revoked is deleted too, and the Master is never
	// told "invalid" about it.
	e.post(pathRevoke, e.master, map[string]any{"roleId": e.stylistRole}).wantResult(t, "ok")
	e.post(pathDeleteRole, e.master, map[string]any{"version": e.version(), "roleId": e.stylistRole}).wantResult(t, "ok")
	e.setClock(at(8, 0))
	e.mustWrite(pathAdvance)
	e.post(pathDeleteRole, e.master, map[string]any{"version": e.version(), "roleId": e.masterRole}).
		wantError(t, http.StatusUnprocessableEntity, "not_draft")
}

// ---------------------------------------------------------------- US-M-12

const pasted = "seq\ttitle\tstart\tduration_min\tfixed\troles\trole_note\tnote\n" +
	"20\t迎賓\t10:30\t30\t\t攝影;新秘\t大門口\t賓客入場\n" +
	"10\t新娘化妝\t08:00\t120\t\t新秘\t新娘房\t\n" +
	"30\t一進\t11:15\t15\tY\t攝影\t側門\t\n"

func (e *env) importBody(text string, newRoles ...map[string]any) map[string]any {
	if newRoles == nil {
		newRoles = []map[string]any{}
	}
	return map[string]any{"version": e.version(), "text": text, "newRoles": newRoles}
}

func newRole(name string) map[string]any {
	return map[string]any{"name": name, "view": "Glance", "checksIn": true}
}

func TestUS_M_12_PreviewAndReplace(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	r := e.post(pathImportPreview, e.master, map[string]any{"text": pasted})
	r.wantResult(t, "ok")
	p := r.body["preview"].(map[string]any)
	wantKeys(t, "preview", p, "fileError", "rows", "missingRoles", "tooManyRoles", "replaces")
	if p["replaces"] != 4.0 || p["tooManyRoles"] != false || !slices.Equal(anyStrings(p["missingRoles"]), []string{"攝影"}) {
		t.Errorf("preview = %v", p)
	}
	rows := p["rows"].([]any)
	if len(rows) != 3 || rows[0].(map[string]any)["start"] != "10:30" || len(rows[0].(map[string]any)["errors"].([]any)) != 0 {
		t.Errorf("rows = %v", rows)
	}
	v := e.version()
	if e.count(`SELECT count(*) FROM action_logs WHERE type LIKE 'rundown.%'`) != 0 || e.version() != v {
		t.Error("the preview wrote")
	}
	// newRoles must be exactly the missing roles (US-M-12.6).
	e.post(pathImport, e.master, e.importBody(pasted)).wantError(t, http.StatusUnprocessableEntity, "invalid_import")
	e.post(pathImport, e.master, e.importBody(pasted, newRole("攝影"), newRole("音控"))).
		wantError(t, http.StatusUnprocessableEntity, "invalid_import")
	e.post(pathImport, e.master, e.importBody(pasted, newRole("攝影"), newRole(" 攝影"))).wantError(t, http.StatusBadRequest, "bad_request")
	dash := newRole("攝影")
	dash["view"], dash["checksIn"] = "Dashboard", false
	e.post(pathImport, e.master, e.importBody(pasted, dash)).wantError(t, http.StatusUnprocessableEntity, "invalid_role")
	if got := e.seqs(); len(got) != 4 || e.version() != v {
		t.Fatalf("a refused import changed the rundown: %v", got)
	}
	e.post(pathImport, e.master, e.importBody(pasted, newRole("攝影"))).wantResult(t, "ok")
	// Replaced, ordered by the file's seq and renumbered 1..n.
	if got := e.seqs(); !slices.Equal(got, []string{"新娘化妝", "迎賓", "一進"}) {
		t.Errorf("cues = %v", got)
	}
	var photo int64
	e.db().QueryRow(`SELECT id FROM roles WHERE name = '攝影' AND type = 'Media'`).Scan(&photo)
	if photo == 0 || e.count(`SELECT count(*) FROM access_tokens WHERE role_id = ?`, photo) != 0 {
		t.Errorf("攝影 = %d, created without a link", photo)
	}
	if item := e.roleItem(t, e.snap(e.master), photo); item["hasLink"] != false {
		t.Errorf("role item = %v", item)
	}
	if e.version() != v+1 {
		t.Errorf("version %d → %d", v, e.version())
	}
	// Everything replaced is in the log (ADR-0032).
	l := e.lastLog("rundown.import")
	if len(l["replaced"].([]any)) != 4 || l["rows"] != 3.0 || l["byTokenId"] != e.masterTokenID() {
		t.Errorf("log = %v", l)
	}
	// The Stylist sees its new cues; its old roleNote of 敬酒 is gone.
	if c := e.snap(e.stylist)["cues"].([]any); len(c) != 2 {
		t.Errorf("stylist cues = %v", c)
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

// Role names are matched against every role, the Master included; a
// Master or Host name is a bad row and never a role to create (security
// review P1).
func TestUS_M_12_RoleMatching(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	e.addRoleWithToken(domain.Host)
	text := "seq,title,start,duration_min,roles\n" +
		"1,a,08:00,10,總召\n" +
		"2,b,09:00,10,角色Host\n" +
		"3,c,10:00,10," + strings.Repeat("名", 51) + "\n" +
		"4,d,11:00,10,新秘\n" +
		"5," + strings.Repeat("題", 101) + ",12:00,10,\n"
	p := e.post(pathImportPreview, e.master, map[string]any{"text": text}).body["preview"].(map[string]any)
	rows := p["rows"].([]any)
	for i, wantBad := range []bool{true, true, true, false, true} {
		if bad := len(rows[i].(map[string]any)["errors"].([]any)) > 0; bad != wantBad {
			t.Errorf("row %d errors = %v", i+1, rows[i].(map[string]any)["errors"])
		}
	}
	if m := p["missingRoles"].([]any); len(m) != 0 {
		t.Errorf("missingRoles = %v", m)
	}
	e.post(pathImport, e.master, e.importBody(text)).wantError(t, http.StatusUnprocessableEntity, "invalid_import")
	// A text that cannot be read at all.
	p = e.post(pathImportPreview, e.master, map[string]any{"text": "a,b\n1,2\n"}).body["preview"].(map[string]any)
	if p["fileError"] == nil || len(p["rows"].([]any)) != 0 {
		t.Errorf("preview = %v", p)
	}
	// Creating the missing roles would pass the limit.
	for i := e.count(`SELECT count(*) FROM roles WHERE event_id = ?`, e.eventID); i < domain.MaxRoles; i++ {
		e.addRole("角色"+string(rune('A'+i)), "Glance", true).wantResult(t, "ok")
	}
	p = e.post(pathImportPreview, e.master, map[string]any{"text": "seq,title,start,duration_min,roles\n1,a,08:00,10,新角色\n"}).
		body["preview"].(map[string]any)
	if p["tooManyRoles"] != true {
		t.Errorf("tooManyRoles = %v", p["tooManyRoles"])
	}
}

func TestUS_M_12_4_ImportLockedOnTheEventDayAndLive(t *testing.T) {
	e := newEnv(t)
	text := "seq,title,start,duration_min\n1,a,08:00,10\n"
	e.post(pathImport, e.master, e.importBody(text)).wantError(t, http.StatusUnprocessableEntity, "locked_on_event_day")
	e.post(pathImportPreview, e.master, map[string]any{"text": text}).wantResult(t, "ok")
	e.live()
	e.post(pathImport, e.master, e.importBody(text)).wantError(t, http.StatusUnprocessableEntity, "not_draft")
	e.post(pathImportPreview, e.master, map[string]any{"text": text}).wantError(t, http.StatusUnprocessableEntity, "not_draft")
	if got := e.seqs(); len(got) != 4 {
		t.Errorf("cues = %v", got)
	}
}

func TestUS_M_12_5_ImportLimits(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	var sb strings.Builder
	sb.WriteString("seq,title,start,duration_min\n")
	for i := 1; i <= 201; i++ {
		sb.WriteString(jsonInt(int64(i)) + ",a,08:00,10\n")
	}
	p := e.post(pathImportPreview, e.master, map[string]any{"text": sb.String()}).body["preview"].(map[string]any)
	if p["fileError"] == nil {
		t.Error("201 rows accepted")
	}
	e.post(pathImport, e.master, e.importBody(sb.String())).wantError(t, http.StatusUnprocessableEntity, "invalid_import")
	// 100 KB of text is in, even with every character escaped in JSON.
	big := "seq,title,start,duration_min,note\n1,a,08:00,10,\"" + strings.Repeat("\t", 100<<10-60) + "\"\n"
	e.post(pathImportPreview, e.master, map[string]any{"text": big}).wantResult(t, "ok")
	e.post(pathImportPreview, e.master, map[string]any{"text": big + strings.Repeat("x", 100)}).wantResult(t, "ok")
}

// ---------------------------------------------------------------- US-M-14

func TestUS_M_14_PreviewIsTheRolesOwnSnapshot(t *testing.T) {
	e := newEnv(t)
	e.live()
	mediaTok := e.addRole("攝影", "Glance", true).body["token"].(string)
	media := int64(e.snap(e.master)["roles"].([]any)[3].(map[string]any)["id"].(float64))
	hostTok := e.addRoleWithToken(domain.Host)
	var host int64
	e.db().QueryRow(`SELECT id FROM roles WHERE type = 'Host'`).Scan(&host)
	e.skip(e.cue["迎賓"]).wantResult(t, "ok")
	for name, c := range map[string]struct {
		id  int64
		tok string
	}{"stylist": {e.stylistRole, e.stylist}, "media": {media, mediaTok}, "host": {host, hostTok}} {
		logs := e.count(`SELECT count(*) FROM action_logs`)
		before := e.roleItem(t, e.snap(e.master), c.id)["lastSeenAt"]
		r := e.post(pathPreviewRole, e.master, map[string]any{"roleId": c.id})
		r.wantResult(t, "ok")
		wantKeys(t, name+" preview response", r.body, "result", "preview")
		preview := r.body["preview"].(map[string]any)
		own := e.snap(c.tok)
		delete(preview, "serverTime")
		delete(own, "serverTime")
		if jsonBody(preview) != jsonBody(own) {
			t.Errorf("%s preview = %s\nown = %s", name, jsonBody(preview), jsonBody(own))
		}
		if e.count(`SELECT count(*) FROM action_logs`) != logs {
			t.Errorf("%s: the preview wrote a log", name)
		}
		_ = before
	}
	// The target is not marked seen: a role never opened stays never seen.
	never := e.addRole("伴娘", "HandsFree", true)
	id := int64(never.body["roleId"].(float64))
	e.post(pathPreviewRole, e.master, map[string]any{"roleId": id}).wantResult(t, "ok")
	if item := e.roleItem(t, e.snap(e.master), id); item["lastSeenAt"] != nil {
		t.Errorf("preview marked the role seen: %v", item)
	}
	e.post(pathPreviewRole, e.master, map[string]any{"roleId": e.masterRole}).wantError(t, http.StatusUnprocessableEntity, "master_role")
	// A role without any link (created by an import) can be previewed.
	e.exec(`INSERT INTO roles (event_id, name, type) VALUES (?, '無連結', 'Stylist')`, e.eventID)
	var noLink int64
	e.db().QueryRow(`SELECT id FROM roles WHERE name = '無連結'`).Scan(&noLink)
	e.post(pathPreviewRole, e.master, map[string]any{"roleId": noLink}).wantResult(t, "ok")
	// A type not opened yet has no snapshot: 404, not 500.
	e.addRoleWithToken(domain.Runner)
	var runner int64
	e.db().QueryRow(`SELECT id FROM roles WHERE type = 'Runner'`).Scan(&runner)
	e.post(pathPreviewRole, e.master, map[string]any{"roleId": runner}).wantError(t, http.StatusNotFound, "not_found")
}

// ---------------------------------------------------------------- ownership, capabilities

func TestM2cIDsOfAnotherEventAre404(t *testing.T) {
	for _, day := range []int{7, 1} { // later, and already ended
		e := newEnv(t)
		f := e.otherEvent(day)
		e.dayBefore()
		e.exec(`UPDATE roles SET name = '撞名' WHERE id = ?`, f.stylist)
		v := e.version()
		for _, id := range []int64{f.masterRole, f.host, f.stylist, 9999} {
			e.post(pathDeleteRole, e.master, map[string]any{"version": v, "roleId": id}).wantError(t, http.StatusNotFound, "not_found")
			e.post(pathRename, e.master, map[string]any{"roleId": id, "name": "撞名"}).wantError(t, http.StatusNotFound, "not_found")
			e.post(pathPreviewRole, e.master, map[string]any{"roleId": id}).wantError(t, http.StatusNotFound, "not_found")
		}
		for _, body := range []struct {
			path string
			body map[string]any
		}{
			{pathUpdate, updateBody(v, f.doneCue, at(10, 0), "x")},
			{pathMove, map[string]any{"version": v, "cueId": f.doneCue, "afterCueId": nil}},
			{pathMove, map[string]any{"version": v, "cueId": e.cue["迎賓"], "afterCueId": f.doneCue}},
			{pathMove, map[string]any{"version": v, "cueId": f.doneCue, "afterCueId": f.doneCue}},
			{pathDeleteCue, map[string]any{"version": v, "cueId": f.doneCue}},
		} {
			e.post(body.path, e.master, body.body).wantError(t, http.StatusNotFound, "not_found")
		}
		// Another event's role name is not a duplicate here.
		e.addRole("撞名", "Glance", true).wantResult(t, "ok")
		if e.version() != v {
			t.Error("a foreign id changed the event")
		}
	}
}

func TestM2cCapabilityTableEveryCell(t *testing.T) {
	paths := []string{pathUpdate, pathMove, pathDeleteCue, pathImportPreview, pathImport, pathAddRole, pathRename,
		pathDeleteRole, pathPreviewRole}
	for _, typ := range allRoleTypes {
		for _, p := range paths {
			t.Run(string(typ)+p, func(t *testing.T) {
				e := newEnv(t)
				e.dayBefore()
				tok := map[domain.RoleType]string{domain.Master: e.master, domain.Stylist: e.stylist}[typ]
				if tok == "" {
					tok = e.addRoleWithToken(typ)
				}
				v := e.version()
				roles := e.count(`SELECT count(*) FROM roles`)
				body := map[string]map[string]any{
					pathUpdate:        updateBody(v, e.cue["迎賓"], at(10, 40), "x"),
					pathMove:          {"version": v, "cueId": e.cue["敬酒"], "afterCueId": nil},
					pathDeleteCue:     {"version": v, "cueId": e.cue["迎賓"]},
					pathImportPreview: {"text": "seq,title,start,duration_min\n1,a,08:00,10\n"},
					pathImport:        {"version": v, "text": "seq,title,start,duration_min\n1,a,08:00,10\n", "newRoles": []any{}},
					pathAddRole:       {"name": "伴娘", "view": "Glance", "checksIn": true, "origin": origin},
					pathRename:        {"roleId": e.stylistB, "name": "x"},
					pathDeleteRole:    {"version": v, "roleId": e.stylistB},
					pathPreviewRole:   {"roleId": e.stylistB},
				}[p]
				r := e.post(p, tok, body)
				if typ == domain.Master {
					r.wantResult(t, "ok")
					return
				}
				r.wantError(t, http.StatusForbidden, "forbidden")
				if e.version() != v || e.count(`SELECT count(*) FROM roles`) != roles ||
					e.count(`SELECT count(*) FROM action_logs WHERE type LIKE 'rundown.%' OR type LIKE 'role.%'`) != 0 {
					t.Error("a refused call changed the event")
				}
				e.get(e.stylistBTok).wantResult(t, "ok")
			})
		}
	}
}

// Every write of the preparation logs which Master link made it, and
// none logs a credential (access-control.md 3.4, security review P1).
func TestM2cWritesLogTheMastersLink(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	e.post(pathUpdate, e.master, updateBody(e.version(), e.cue["迎賓"], at(10, 40), "x")).wantResult(t, "ok")
	e.post(pathMove, e.master, map[string]any{"version": e.version(), "cueId": e.cue["敬酒"], "afterCueId": nil}).wantResult(t, "ok")
	id := int64(e.addRole("伴娘", "Glance", true).body["roleId"].(float64))
	e.post(pathRename, e.master, map[string]any{"roleId": id, "name": "伴娘二"}).wantResult(t, "ok")
	e.post(pathDeleteRole, e.master, map[string]any{"version": e.version(), "roleId": id}).wantResult(t, "ok")
	e.post(pathDeleteCue, e.master, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"]}).wantResult(t, "ok")
	e.post(pathImport, e.master, e.importBody("seq,title,start,duration_min\n1,a,08:00,10\n")).wantResult(t, "ok")
	for _, typ := range []string{"rundown.update", "rundown.move", "role.add", "role.rename", "role.delete", "rundown.delete", "rundown.import"} {
		if l := e.lastLog(typ); l["byTokenId"] != e.masterTokenID() {
			t.Errorf("%s byTokenId = %v", typ, l["byTokenId"])
		}
	}
	var logs string
	e.db().QueryRow(`SELECT group_concat(payload, ';') FROM action_logs`).Scan(&logs)
	if l := leaks(logs, e.master, e.stylist, e.stylistBTok); len(l) > 0 {
		t.Errorf("credential in the log: %v", l)
	}
}
