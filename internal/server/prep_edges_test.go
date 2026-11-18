package server

// The edges of the preparation before the event (US-M-10 to US-M-13):
// version conflicts of every editing endpoint, the inclusive limits, role names and the size limits of the import.

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// US-M-10.9: every editing endpoint refuses an old version with 409 and the
// Master's latest snapshot, and writes nothing, so the first device to save
// wins and the second is never overwritten.
func TestUS_M_10_9_StaleVersionIsRefusedByEveryEditingEndpoint(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	stale := e.version()
	// Another device saved first.
	e.post(pathUpdate, e.master, updateBody(stale, e.cue["敬酒"], at(13, 5), "敬酒（改）")).wantResult(t, "ok")
	if e.version() != stale+1 {
		t.Fatalf("version = %d", e.version())
	}
	seqs := e.seqs()
	text := "seq,title,start,duration_min,roles\n1,a,08:00,10,新角色\n"

	for _, c := range []struct {
		name, path string
		body       map[string]any
	}{
		{"update", pathUpdate, updateBody(stale, e.cue["敬酒"], at(14, 0), "被蓋掉")},
		{"move", pathMove, map[string]any{"version": stale, "cueId": e.cue["敬酒"], "afterCueId": nil}},
		{"delete", pathDeleteCue, map[string]any{"version": stale, "cueId": e.cue["迎賓"]}},
		{"import", pathImport, map[string]any{"version": stale, "text": text, "newRoles": []any{newRole("新角色")}}},
		{"delete role", pathDeleteRole, map[string]any{"version": stale, "roleId": e.stylistB}},
	} {
		e.covStale(t, c.name, c.path, c.body)
		if got := e.seqs(); !slices.Equal(got, seqs) {
			t.Errorf("%s: cues = %v, want %v", c.name, got, seqs)
		}
	}
	if n := e.count(`SELECT count(*) FROM cues WHERE title = '敬酒（改）' AND duration_min = 25`); n != 1 {
		t.Error("the first device's edit was overwritten")
	}
	if n := e.count(`SELECT count(*) FROM roles WHERE name = '新角色'`); n != 0 {
		t.Error("a stale import created a role")
	}
	if n := e.count(`SELECT count(*) FROM roles WHERE id = ?`, e.stylistB); n != 1 {
		t.Error("a stale delete removed the role")
	}
	// The same bodies with the latest version go through.
	e.post(pathMove, e.master, map[string]any{"version": e.version(), "cueId": e.cue["敬酒"], "afterCueId": nil}).wantResult(t, "ok")
}

// US-M-10.2 / US-M-10.6: the limits of an edited cue are inclusive, and a
// skipped cue can be edited in Draft too.
func TestUS_M_10_2_UpdateBoundariesAreAccepted(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	nextDay := func(h, m int) time.Time { return time.Date(2027, 6, 17, h, m, 0, 0, taipei) }
	cue := e.cue["迎賓"]
	update := func(change func(map[string]any)) resp {
		b := updateBody(e.version(), cue, at(10, 30), "迎賓")
		change(b)
		return e.post(pathUpdate, e.master, b)
	}
	ok := []struct {
		name   string
		change func(map[string]any)
	}{
		{"start at 00:00 of the event day", func(b map[string]any) { b["start"] = at(0, 0).UTC().Format(time.RFC3339) }},
		{"start next day 03:59", func(b map[string]any) { b["start"] = nextDay(3, 59).UTC().Format(time.RFC3339) }},
		{"duration 0", func(b map[string]any) { b["durationMin"] = 0 }},
		{"duration 1", func(b map[string]any) { b["durationMin"] = 1 }},
		{"duration 1440", func(b map[string]any) { b["durationMin"] = 1440 }},
		{"title of 100 characters", func(b map[string]any) { b["title"] = strings.Repeat("字", 100) }},
		{"note of 500 characters", func(b map[string]any) { b["notePublic"] = strings.Repeat("字", 500) }},
		{"no note", func(b map[string]any) { b["notePublic"] = nil }},
	}
	for _, c := range ok {
		v := e.version()
		update(c.change).wantResult(t, "ok")
		if e.version() != v+1 {
			t.Errorf("%s: version %d → %d", c.name, v, e.version())
		}
	}
	bad := []struct {
		name   string
		change func(map[string]any)
	}{
		{"start next day 04:00", func(b map[string]any) { b["start"] = nextDay(4, 0).UTC().Format(time.RFC3339) }},
		{"start a minute before the day", func(b map[string]any) { b["start"] = at(0, 0).Add(-time.Minute).UTC().Format(time.RFC3339) }},
		{"duration 1441", func(b map[string]any) { b["durationMin"] = 1441 }},
		{"title of 101 characters", func(b map[string]any) { b["title"] = strings.Repeat("字", 101) }},
		{"note of 501 characters", func(b map[string]any) { b["notePublic"] = strings.Repeat("字", 501) }},
	}
	for _, c := range bad {
		v := e.version()
		update(c.change).wantError(t, http.StatusUnprocessableEntity, "invalid_cue")
		if e.version() != v {
			t.Errorf("%s: a refused update changed the event", c.name)
		}
	}
	update(func(b map[string]any) { b["durationMin"] = -1 }).wantError(t, http.StatusBadRequest, "bad_request")

	// A skipped cue is still a draft cue.
	e.skip(e.cue["敬酒"]).wantResult(t, "ok")
	e.post(pathUpdate, e.master, updateBody(e.version(), e.cue["敬酒"], at(13, 30), "敬酒二")).wantResult(t, "ok")
	if n := e.count(`SELECT count(*) FROM cues WHERE title = '敬酒二' AND status = 'Skipped'`); n != 1 {
		t.Error("the skipped cue was not edited")
	}
}

// US-M-10.2: in Draft the Master can assign a cue to roles and give each its
// note; a skipped cue cannot be given to anyone.
func TestUS_M_10_2_AssignInDraft(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	v := e.version()
	e.assign(e.cue["迎賓"], assignment(e.stylistB, "接待")).wantResult(t, "ok")
	if e.version() != v+1 {
		t.Errorf("version %d → %d", v, e.version())
	}
	if c := byTitle(t, e.snap(e.stylistBTok), "迎賓"); c == nil || c["roleNote"] != "接待" {
		t.Errorf("新秘B sees %v", c)
	}
	e.assign(e.cue["迎賓"]).wantResult(t, "ok")
	if c := byTitle(t, e.snap(e.stylistBTok), "迎賓"); c != nil {
		t.Errorf("after removal 新秘B still sees %v", c)
	}
	e.skip(e.cue["迎賓"]).wantResult(t, "ok")
	e.assign(e.cue["迎賓"], assignment(e.stylistB, "x")).wantError(t, http.StatusUnprocessableEntity, "not_pending")
}

// US-M-10.3: moving a cue to the end, or to where it already is, keeps seq
// 1..n.
func TestUS_M_10_3_MoveToTheEndAndToItsOwnPlace(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	e.post(pathMove, e.master, map[string]any{"version": e.version(), "cueId": e.cue["新娘化妝"], "afterCueId": e.cue["敬酒"]}).
		wantResult(t, "ok")
	if got := e.seqs(); !slices.Equal(got, []string{"迎賓", "一進預備", "敬酒", "新娘化妝"}) {
		t.Errorf("after moving to the end: %v", got)
	}
	// 迎賓 is already first; put it right after nothing: its place again.
	e.post(pathMove, e.master, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"], "afterCueId": nil}).wantResult(t, "ok")
	if got := e.seqs(); !slices.Equal(got, []string{"迎賓", "一進預備", "敬酒", "新娘化妝"}) {
		t.Errorf("after moving to its own place: %v", got)
	}
}

// US-M-11.4 / US-M-11.7: a rename that is no name is refused and changes
// nothing; a good one is accepted in Draft and Live and never moves the
// event version.
func TestUS_M_11_4_RenameValidation(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	v := e.version()
	renames := e.count(`SELECT count(*) FROM action_logs WHERE type = 'role.rename'`)
	for name, bad := range map[string]string{
		"empty":            "",
		"only spaces":      "   ",
		"51 characters":    strings.Repeat("名", 51),
		"control":          "伴\x07娘",
		"newline":          "伴\n娘",
		"zero width space": "伴​娘",
		"semicolon":        "伴;娘",
	} {
		e.post(pathRename, e.master, map[string]any{"roleId": e.stylistRole, "name": bad}).
			wantError(t, http.StatusUnprocessableEntity, "invalid_role")
		if n := e.snap(e.stylist)["role"].(map[string]any)["name"]; n != "新秘" {
			t.Errorf("%s: name became %v", name, n)
		}
	}
	if e.count(`SELECT count(*) FROM action_logs WHERE type = 'role.rename'`) != renames {
		t.Error("a refused rename was logged")
	}
	// Trimmed, it is still the other role's name.
	e.post(pathRename, e.master, map[string]any{"roleId": e.stylistRole, "name": " 新秘B "}).
		wantError(t, http.StatusUnprocessableEntity, "duplicate_name")

	// 50 characters in Draft: accepted, no new version.
	long := strings.Repeat("名", 50)
	e.post(pathRename, e.master, map[string]any{"roleId": e.stylistRole, "name": long}).wantResult(t, "ok")
	if n := e.snap(e.stylist)["role"].(map[string]any)["name"]; n != long {
		t.Errorf("name = %v", n)
	}
	if e.version() != v {
		t.Errorf("a rename moved the version: %d → %d", v, e.version())
	}
	// And live.
	e.live()
	v = e.version()
	e.post(pathRename, e.master, map[string]any{"roleId": e.stylistRole, "name": "化妝師"}).wantResult(t, "ok")
	if e.version() != v {
		t.Errorf("a live rename moved the version: %d → %d", v, e.version())
	}
}

// US-M-11.1: a role name of exactly 50 characters is accepted.
func TestUS_M_11_1_RoleNameOf50CharactersIsAccepted(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("名", 50)
	r := e.addRole(long, "Glance", true)
	r.wantResult(t, "ok")
	if n := e.snap(r.body["token"].(string))["role"].(map[string]any)["name"]; n != long {
		t.Errorf("name = %v", n)
	}
	e.addRole(long, "Glance", true).wantError(t, http.StatusUnprocessableEntity, "duplicate_name")
}

// US-M-11.5: deleting a role also removes its check-ins, and the event
// version moves.
func TestUS_M_11_5_DeleteRoleRemovesItsCheckIns(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	e.checkIn(e.stylistBTok, true).wantResult(t, "ok")
	e.proxyCheckIn(e.stylistB, false).wantResult(t, "ok")
	e.proxyCheckIn(e.stylistB, true).wantResult(t, "ok")
	if n := e.count(`SELECT count(*) FROM check_ins WHERE role_id = ?`, e.stylistB); n != 2 {
		t.Fatalf("%d check-ins before, want 2", n)
	}
	other := e.count(`SELECT count(*) FROM check_ins WHERE role_id = ?`, e.stylistRole)
	e.post(pathDeleteRole, e.master, map[string]any{"version": e.version(), "roleId": e.stylistB}).wantResult(t, "ok")
	if n := e.count(`SELECT count(*) FROM check_ins WHERE role_id = ?`, e.stylistB); n != 0 {
		t.Errorf("%d check-ins left of the deleted role", n)
	}
	if n := e.count(`SELECT count(*) FROM check_ins WHERE role_id = ?`, e.stylistRole); n != other {
		t.Error("another role's check-ins changed")
	}
}

// sizedImport is a valid rundown of exactly size bytes: rows with long
// notes, then blank lines, which the reader skips.
func sizedImport(t *testing.T, size int) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("seq,title,start,duration_min,note\n")
	note := strings.Repeat("n", 500)
	for i := 1; i <= 190; i++ {
		row := strconv.Itoa(i) + ",a,08:00,1," + note + "\n"
		if sb.Len()+len(row) > size {
			break
		}
		sb.WriteString(row)
	}
	if sb.Len() > size {
		t.Fatalf("text of %d bytes does not fit %d", sb.Len(), size)
	}
	sb.WriteString(strings.Repeat("\n", size-sb.Len()))
	return sb.String()
}

// US-M-12.5: the text is at most 100 KB, counted as decoded bytes; one byte
// over is refused with the reason, and exactly 100 KB goes through, in the
// preview and in the import.
func TestUS_M_12_5_TextOf100KBIsAcceptedAndOneByteMoreIsNot(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	const limit = 100 << 10
	exact, over := sizedImport(t, limit), sizedImport(t, limit+1)
	if len(exact) != limit || len(over) != limit+1 {
		t.Fatalf("sizes %d, %d", len(exact), len(over))
	}
	p := e.post(pathImportPreview, e.master, map[string]any{"text": exact}).body["preview"].(map[string]any)
	if p["fileError"] != nil || len(p["rows"].([]any)) == 0 {
		t.Errorf("exactly 100 KB: fileError = %v, %d rows", p["fileError"], len(p["rows"].([]any)))
	}
	p = e.post(pathImportPreview, e.master, map[string]any{"text": over}).body["preview"].(map[string]any)
	if p["fileError"] == nil || !strings.Contains(p["fileError"].(string), "100 KB") || len(p["rows"].([]any)) != 0 {
		t.Errorf("one byte over: fileError = %v, %d rows", p["fileError"], len(p["rows"].([]any)))
	}
	v := e.version()
	e.post(pathImport, e.master, e.importBody(over)).wantError(t, http.StatusUnprocessableEntity, "invalid_import")
	if e.version() != v || len(e.seqs()) != 4 {
		t.Error("an oversized import changed the rundown")
	}
	e.post(pathImport, e.master, e.importBody(exact)).wantResult(t, "ok")
	if n := len(e.seqs()); n < 190 {
		t.Errorf("%d cues imported, want the 190 rows of the text", n)
	}
}

// US-M-12.5: exactly 200 rows go through the endpoint.
func TestUS_M_12_5_200RowsAreAccepted(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	var sb strings.Builder
	sb.WriteString("seq,title,start,duration_min\n")
	for i := 1; i <= 200; i++ {
		sb.WriteString(strconv.Itoa(i) + ",a,08:00,1\n")
	}
	e.post(pathImport, e.master, e.importBody(sb.String())).wantResult(t, "ok")
	if n := len(e.seqs()); n != 200 {
		t.Errorf("%d cues, want 200", n)
	}
}
