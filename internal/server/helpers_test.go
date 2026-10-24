package server

// Helpers and endpoint paths shared by the server tests; the environment
// itself is in fixture_test.go.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

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

func notice(t *testing.T, s map[string]any) map[string]any {
	t.Helper()
	n, _ := s["notice"].(map[string]any)
	return n
}

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

func (e *env) getIfNoneMatch(token string, tags ...string) resp {
	e.t.Helper()
	r := e.request(http.MethodGet, pathSnapshot, token, "")
	for _, tag := range tags {
		r.Header.Add("If-None-Match", tag)
	}
	return e.doReq(r)
}

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

func (e *env) importBody(text string, newRoles ...map[string]any) map[string]any {
	if newRoles == nil {
		newRoles = []map[string]any{}
	}
	return map[string]any{"version": e.version(), "text": text, "newRoles": newRoles}
}

func newRole(name string) map[string]any {
	return map[string]any{"name": name, "view": "Glance", "checksIn": true}
}

func anyStrings(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

const (
	pathAck          = "/api/cues/ack"
	pathAddTask      = "/api/tasks/add"
	pathSetTask      = "/api/tasks/set"
	pathDeleteTask   = "/api/tasks/delete"
	pathImportGuests = "/api/guests/import"
	pathArrive       = "/api/guests/arrive"
	pathChecklist    = "/api/checklist/items"
	pathCheckItem    = "/api/checklist/check"
)

// jobRole adds a role of typ assigned to 迎賓, with a link; returns the
// token and the role id.
func (e *env) jobRole(typ domain.RoleType) (string, int64) {
	e.t.Helper()
	tok := e.addRoleWithToken(typ)
	var id int64
	e.db().QueryRow(`SELECT id FROM roles WHERE type = ? ORDER BY id DESC LIMIT 1`, string(typ)).Scan(&id)
	return tok, id
}

func list(t *testing.T, snap map[string]any, key string) []map[string]any {
	t.Helper()
	v, ok := snap[key].([]any)
	if !ok {
		t.Fatalf("%s = %v, want a list", key, snap[key])
	}
	out := make([]map[string]any, len(v))
	for i, x := range v {
		out[i] = x.(map[string]any)
	}
	return out
}
