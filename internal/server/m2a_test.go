package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// live starts the event and puts 新娘化妝 in progress at 08:00.
func (e *env) live() {
	e.t.Helper()
	e.setClock(at(8, 0))
	e.mustWrite(pathAdvance)
}

func (e *env) preview(token, body string) resp {
	e.t.Helper()
	return e.do(http.MethodPost, pathPreview, token, body)
}

func previewOf(t *testing.T, r resp) map[string]any {
	t.Helper()
	r.wantResult(t, "ok")
	p, ok := r.body["preview"].(map[string]any)
	if !ok {
		t.Fatalf("no preview in %s", r.Body.String())
	}
	return p
}

func movedIDs(p map[string]any) map[float64]bool {
	out := map[float64]bool{}
	for _, c := range p["changes"].([]any) {
		out[c.(map[string]any)["cueId"].(float64)] = true
	}
	return out
}

// ---------------------------------------------------------------- US-M-2

func TestUS_M_2_1_PreviewWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.live()
	v := e.version()
	p := previewOf(t, e.preview(e.master, shiftBody(0, e.cue["迎賓"], 10)))
	wantKeys(t, "preview", p, "changes", "conflicts", "tooEarly")
	// 迎賓 and 敬酒 move; 一進預備 is fixed; 新娘化妝 is in progress.
	if got := movedIDs(p); len(got) != 2 || !got[float64(e.cue["迎賓"])] || !got[float64(e.cue["敬酒"])] {
		t.Errorf("moved = %v", got)
	}
	for _, c := range p["changes"].([]any) {
		wantKeys(t, "change", c.(map[string]any), "cueId", "fromStart", "toStart")
	}
	if e.version() != v {
		t.Error("preview changed the event")
	}
	// 迎賓 10:30+10+30 min ends 11:10, before the fixed 11:15: no conflict; +20 has one.
	if c := previewOf(t, e.preview(e.master, shiftBody(0, e.cue["迎賓"], 20)))["conflicts"].([]any); len(c) != 1 {
		t.Errorf("conflicts at +20 = %v, want one (US-M-2.5)", c)
	}
}

func TestUS_M_2_2_UntickedCuesAreNotMoved(t *testing.T) {
	e := newEnv(t)
	e.live()
	p := previewOf(t, e.preview(e.master, shiftBody(0, e.cue["迎賓"], 10, e.cue["敬酒"])))
	if got := movedIDs(p); len(got) != 1 || !got[float64(e.cue["迎賓"])] {
		t.Errorf("moved = %v, want only 迎賓", got)
	}
}

func TestUS_M_2_3_ApplyShiftsOncePerVersion(t *testing.T) {
	e := newEnv(t)
	e.live()
	v := e.version()
	s := e.do(http.MethodPost, pathShift, e.master, shiftBody(v, e.cue["迎賓"], 10, e.cue["敬酒"]))
	s.wantResult(t, "ok")
	snap := s.snapshot(t)
	if got := byTitle(t, snap, "迎賓")["offsetMin"]; got != 10.0 {
		t.Errorf("迎賓 offset = %v", got)
	}
	for _, title := range []string{"新娘化妝", "一進預備", "敬酒"} {
		if got := byTitle(t, snap, title)["offsetMin"]; got != 0.0 {
			t.Errorf("%s offset = %v, want 0", title, got)
		}
	}
	if e.version() != v+1 {
		t.Errorf("version = %d, want %d", e.version(), v+1)
	}
	// US-M-2.7: the same version again is a conflict.
	e.do(http.MethodPost, pathShift, e.master, shiftBody(v, e.cue["迎賓"], 10)).wantError(t, http.StatusConflict, "version_conflict")
	// US-M-4.6: undo names the shift and restores it.
	if d := snap["undo"].(map[string]any)["description"]; d != "撤銷：+10 分，影響 1 個環節" {
		t.Errorf("undo description = %v", d)
	}
	if got := byTitle(t, e.mustWrite(pathUndo), "迎賓")["offsetMin"]; got != 0.0 {
		t.Errorf("迎賓 offset after undo = %v", got)
	}
}

func TestUS_M_2_4_TooEarlyIsShownAndRefused(t *testing.T) {
	e := newEnv(t)
	e.live()
	e.setClock(at(10, 25)) // 迎賓 at 10:30; −10 would be 10:20, before now
	p := previewOf(t, e.preview(e.master, shiftBody(0, e.cue["迎賓"], -10)))
	te, ok := p["tooEarly"].(map[string]any)
	if !ok || te["cueId"] != float64(e.cue["迎賓"]) {
		t.Errorf("tooEarly = %v", p["tooEarly"])
	}
	v := e.version()
	e.do(http.MethodPost, pathShift, e.master, shiftBody(v, e.cue["迎賓"], -10)).wantError(t, http.StatusUnprocessableEntity, "shift_too_early")
	if e.version() != v {
		t.Error("a refused shift changed the event")
	}
}

func TestUS_M_2_6_DeltaBounds(t *testing.T) {
	e := newEnv(t)
	e.live()
	for _, d := range []int{0, 1441, -1441} {
		e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], d)).
			wantError(t, http.StatusUnprocessableEntity, "invalid_delta")
		e.preview(e.master, shiftBody(0, e.cue["迎賓"], d)).wantError(t, http.StatusUnprocessableEntity, "invalid_delta")
	}
}

func TestShiftOfAnUnknownOrForeignCueIs404(t *testing.T) {
	e := newEnv(t)
	e.live()
	other := newEnv(t) // another database; its ids are unknown here
	for _, id := range []int64{9999, other.cue["敬酒"] + 100} {
		e.preview(e.master, shiftBody(0, id, 5)).wantError(t, http.StatusNotFound, "not_found")
		e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), id, 5)).wantError(t, http.StatusNotFound, "not_found")
	}
}

func TestShiftAndPreviewAreMasterOnlyAndLiveOnly(t *testing.T) {
	e := newEnv(t)
	host := e.addRoleWithToken(domain.Host)
	e.preview(e.master, shiftBody(0, e.cue["迎賓"], 5)).wantError(t, http.StatusUnprocessableEntity, "not_live")
	e.live()
	for _, tok := range []string{e.stylist, host} {
		e.preview(tok, shiftBody(0, e.cue["迎賓"], 5)).wantError(t, http.StatusForbidden, "forbidden")
		e.do(http.MethodPost, pathShift, tok, shiftBody(e.version(), e.cue["迎賓"], 5)).wantError(t, http.StatusForbidden, "forbidden")
	}
	e.preview(newToken(t), shiftBody(0, e.cue["迎賓"], 5)).wantResult(t, "invalid")
}

// The shift body is as strict as the version body (api.md section 1).
func TestShiftBodyIsStrict(t *testing.T) {
	e := newEnv(t)
	e.live()
	v, from := jsonInt(e.version()), jsonInt(e.cue["迎賓"])
	many := make([]string, 201)
	for i := range many {
		many[i] = "1"
	}
	for _, body := range []string{
		`{}`, `{"version":` + v + `}`,
		`{"version":` + v + `,"fromCueId":` + from + `}`,
		`{"version":` + v + `,"deltaMin":5}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":-0}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":05}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5.0}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":"5"}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":null}`,
		`{"version":` + v + `,"fromCueId":-1,"deltaMin":5}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5,"excluded":null}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5,"excluded":"1"}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5,"excluded":["1"]}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5,"excluded":[0]}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5,"excluded":[` + strings.Join(many, ",") + `]}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5,"DeltaMin":5}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5,"deltaMin":5}`,
		`{"version":` + v + `,"fromCueId":` + from + `,"deltaMin":5,"eventId":1}`,
	} {
		if r := e.do(http.MethodPost, pathShift, e.master, body); r.Code != http.StatusBadRequest {
			short := body
			if len(short) > 70 {
				short = short[:70] + "…"
			}
			t.Errorf("body %s: %d", short, r.Code)
		}
	}
	// A preview takes no version.
	e.preview(e.master, `{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5}`).wantError(t, http.StatusBadRequest, "bad_request")
	if e.version() != e.version() || byTitle(t, e.snap(e.master), "迎賓")["offsetMin"] != 0.0 {
		t.Error("a refused body changed the event")
	}
}

// ---------------------------------------------------------------- US-M-5

func TestUS_M_5_1_MasterSnapshotCarriesOverrun(t *testing.T) {
	e := newEnv(t)
	if got := e.snap(e.master)["overrunMin"]; got != 0.0 {
		t.Errorf("draft overrun = %v", got)
	}
	e.live()
	e.setClock(at(10, 37)) // 迎賓 was due at 10:30
	if got := e.snap(e.master)["overrunMin"]; got != 7.0 {
		t.Errorf("overrun = %v, want 7", got)
	}
	e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 10)).wantResult(t, "ok")
	if got := e.snap(e.master)["overrunMin"]; got != 0.0 {
		t.Errorf("overrun after shifting = %v, want 0 (US-M-5.4)", got)
	}
}

// ---------------------------------------------------------------- US-H-1

func TestUS_H_1_HostDashboardHoldsOnlyTheDelayAndCurrentTitle(t *testing.T) {
	e := newEnv(t)
	host := e.addRoleWithToken(domain.Host)
	d := e.snap(host)
	wantKeys(t, "Host snapshot", d, "eventVersion", "serverTime", "eventStatus", "role", "eventDay", "delayMin", "currentTitle")
	wantKeys(t, "role", d["role"].(map[string]any), "type", "name", "view", "checksIn")
	if d["role"].(map[string]any)["view"] != "Dashboard" || d["delayMin"] != 0.0 || d["currentTitle"] != nil {
		t.Errorf("draft dashboard = %v", d)
	}
	e.live()
	e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 10)).wantResult(t, "ok")
	e.setClock(at(10, 45)) // 迎賓 now due 10:40: 10 shifted + 5 overrun
	r := e.get(host)
	d = r.snapshot(t)
	if d["delayMin"] != 15.0 || d["currentTitle"] != "新娘化妝" {
		t.Errorf("dashboard = delay %v, title %v; want 15, 新娘化妝", d["delayMin"], d["currentTitle"])
	}
	// No other cue, note or role reaches the hosts (US-H-1.3).
	for _, s := range []string{"迎賓", "敬酒", "一進預備", "新娘房", "賓客入場", "側門", "B敬酒", "新秘"} {
		if strings.Contains(r.Body.String(), s) {
			t.Errorf("Host response contains %q", s)
		}
	}
}

// ---------------------------------------------------------------- US-ST-2

func notice(t *testing.T, s map[string]any) map[string]any {
	t.Helper()
	n, _ := s["notice"].(map[string]any)
	return n
}

func TestUS_ST_2_NoticeOnlyForTheCallersOwnCues(t *testing.T) {
	e := newEnv(t)
	e.live()
	if n := notice(t, e.snap(e.stylist)); n != nil {
		t.Errorf("notice before any shift = %v", n)
	}
	// 敬酒 belongs to 新秘B only: 新秘 gets no notice (US-ST-2.3).
	e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["敬酒"], 10)).wantResult(t, "ok")
	if n := notice(t, e.snap(e.stylist)); n != nil {
		t.Errorf("新秘 notice for 新秘B's cue = %v", n)
	}
	nb := notice(t, e.snap(e.stylistBTok))
	if nb == nil || nb["deltaMin"] != 10.0 {
		t.Fatalf("新秘B notice = %v", nb)
	}
	wantKeys(t, "notice", nb, "kind", "at", "deltaMin", "changes")

	// Undoing the shift is a notice the other way (US-ST-2.7).
	e.mustWrite(pathUndo)
	nb = notice(t, e.snap(e.stylistBTok))
	if nb == nil || nb["deltaMin"] != -10.0 {
		t.Fatalf("notice after undo = %v, want deltaMin −10", nb)
	}
	changes := nb["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("changes = %v", changes)
	}
	c := changes[0].(map[string]any)
	if c["cueId"] != float64(e.cue["敬酒"]) || c["fromStart"] != "2027-06-06T05:10:00.000Z" || c["toStart"] != "2027-06-06T05:00:00.000Z" {
		t.Errorf("change = %v", c)
	}
}

func TestUS_ST_2_NoticeListsOnlyTheCallersCues(t *testing.T) {
	e := newEnv(t)
	// Make 迎賓 a 新秘 cue too; a shift from 迎賓 then moves 迎賓 (新秘) and 敬酒 (新秘B).
	e.exec(`INSERT INTO cue_roles (cue_id, role_id, role_note) VALUES (?, ?, 'x')`, e.cue["迎賓"], e.stylistRole)
	e.live()
	e.do(http.MethodPost, pathShift, e.master, shiftBody(e.version(), e.cue["迎賓"], 5)).wantResult(t, "ok")
	r := e.get(e.stylist)
	n := notice(t, r.snapshot(t))
	if n == nil {
		t.Fatal("no notice")
	}
	for _, c := range n["changes"].([]any) {
		if id := c.(map[string]any)["cueId"]; id != float64(e.cue["迎賓"]) {
			t.Errorf("notice lists cue %v, not 新秘's", id)
		}
	}
	if raw, _ := json.Marshal(n); strings.Contains(string(raw), jsonInt(e.cue["敬酒"])+",") {
		t.Errorf("notice mentions 敬酒: %s", raw)
	}
}

// ---------------------------------------------------------------- US-X-4, ADR-0020

func TestUS_X_4_SnapshotCarriesTheRolesView(t *testing.T) {
	e := newEnv(t)
	if v := e.snap(e.stylist)["role"].(map[string]any)["view"]; v != "HandsFree" {
		t.Errorf("stylist view = %v", v)
	}
	e.exec(`UPDATE roles SET view = 'Glance' WHERE id = ?`, e.stylistRole)
	if v := e.snap(e.stylist)["role"].(map[string]any)["view"]; v != "Glance" {
		t.Errorf("stylist view after setting = %v", v)
	}
	if v := e.snap(e.master)["role"].(map[string]any)["view"]; v != "Console" {
		t.Errorf("master view = %v", v)
	}
}

func TestUS_X_2_DraftSnapshotCarriesTheEventDayAndOwnCues(t *testing.T) {
	e := newEnv(t)
	e.setClock(at(0, 0).AddDate(0, 0, -3).Add(9 * time.Hour))
	s := e.snap(e.stylist)
	if d := s["eventDay"].(map[string]any); d["date"] != "2027-06-06" || d["timezone"] != "Asia/Taipei" {
		t.Errorf("eventDay = %v", d)
	}
	if s["eventStatus"] != "Draft" || len(cues(t, s)) != 2 {
		t.Errorf("draft stylist snapshot = %v with %d cues", s["eventStatus"], len(cues(t, s)))
	}
}
