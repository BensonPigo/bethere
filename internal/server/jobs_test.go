package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BensonPigo/bethere/internal/domain"
)

// The four jobs and the MC's delay (api.md section 9, ADR-0035).

func TestUS_X_10_RoleSnapshotsCarryTheDelay(t *testing.T) {
	e := newEnv(t)
	media := e.addRoleWithToken(domain.Media)
	if d := e.snap(media)["delayMin"]; d != float64(0) {
		t.Errorf("draft delayMin = %v, want 0", d)
	}
	e.mustWrite(pathAdvance)
	e.setClock(at(10, 50)) // 迎賓 was due at 10:30: 20 minutes over
	host := e.addRoleWithToken(domain.Host)
	if got, want := e.snap(media)["delayMin"], e.snap(host)["delayMin"]; got != want || got != float64(20) {
		t.Errorf("media delayMin = %v, host %v, want both 20", got, want)
	}
}

func TestUS_M_15_AddRoleWithAJob(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	for _, c := range []struct {
		job, view string
		want      domain.RoleType
	}{
		{"VenueLead", "Glance", domain.VenueLead},
		{"Runner", "HandsFree", domain.Runner},
		{"Reception", "Glance", domain.Reception},
		{"Cashier", "Glance", domain.Cashier},
		{"general", "Glance", domain.Media},
	} {
		r := e.post(pathAddRole, e.master, map[string]any{"name": c.job + c.view, "view": c.view, "checksIn": true,
			"origin": "https://bethere.example", "job": c.job})
		r.wantResult(t, "ok")
		var typ string
		e.db().QueryRow(`SELECT type FROM roles WHERE name = ?`, c.job+c.view).Scan(&typ)
		if typ != string(c.want) {
			t.Errorf("%s/%s: type %s, want %s", c.job, c.view, typ, c.want)
		}
	}
	// US-M-15.2: a job takes a cue screen only; an unknown job is refused.
	for _, b := range []map[string]any{
		{"name": "x1", "view": "Dashboard", "checksIn": false, "origin": "https://bethere.example", "job": "Runner"},
		{"name": "x2", "view": "Glance", "checksIn": true, "origin": "https://bethere.example", "job": "Master"},
		{"name": "x3", "view": "Glance", "checksIn": true, "origin": "https://bethere.example", "job": "Host"},
	} {
		e.post(pathAddRole, e.master, b).wantError(t, http.StatusUnprocessableEntity, "invalid_role")
	}
}

func TestUS_VL_1_Ack(t *testing.T) {
	e := newEnv(t)
	lead, leadID := e.jobRole(domain.VenueLead)
	v := e.version()
	r := e.post(pathAck, lead, map[string]any{"cueId": e.cue["迎賓"], "acked": true})
	r.wantResult(t, "ok")
	mine := byTitle(t, r.snapshot(t), "迎賓")
	if mine["ackedAt"] == nil {
		t.Errorf("own ackedAt = nil after ack")
	}
	first := mine["ackedAt"]
	e.setClock(at(9, 5))
	e.post(pathAck, lead, map[string]any{"cueId": e.cue["迎賓"], "acked": true}).wantResult(t, "ok")
	if got := byTitle(t, e.snap(lead), "迎賓")["ackedAt"]; got != first {
		t.Errorf("a repeated ack moved the time: %v, want %v", got, first)
	}
	if e.version() != v {
		t.Error("an ack changed the event version (ADR-0023)")
	}
	masterAck := func() any {
		for _, cr := range byTitle(t, e.snap(e.master), "迎賓")["roles"].([]any) {
			if m := cr.(map[string]any); m["roleId"] == float64(leadID) {
				return m["ackedAt"]
			}
		}
		t.Fatal("venue lead not on 迎賓")
		return nil
	}
	if masterAck() == nil {
		t.Error("the Master does not see the ack")
	}
	// US-VL-1.2: a cue of this event not assigned to the lead.
	e.post(pathAck, lead, map[string]any{"cueId": e.cue["敬酒"], "acked": true}).
		wantError(t, http.StatusUnprocessableEntity, "not_assigned")
	// US-VL-1.3: taking the lead off the cue drops the ack, also when put back.
	e.post(pathAssign, e.master, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"], "roles": []any{}}).wantResult(t, "ok")
	e.post(pathAssign, e.master, map[string]any{"version": e.version(), "cueId": e.cue["迎賓"],
		"roles": []any{map[string]any{"roleId": leadID, "roleNote": nil}}}).wantResult(t, "ok")
	if got := masterAck(); got != nil {
		t.Errorf("ack after reassign = %v, want nil", got)
	}
	// US-VL-1.4: un-ack.
	e.post(pathAck, lead, map[string]any{"cueId": e.cue["迎賓"], "acked": true}).wantResult(t, "ok")
	e.post(pathAck, lead, map[string]any{"cueId": e.cue["迎賓"], "acked": false}).wantResult(t, "ok")
	if masterAck() != nil {
		t.Error("ack still shown after un-ack")
	}
	// Other cue roles never carry an ack.
	media := e.addRoleWithToken(domain.Media)
	if got := byTitle(t, e.snap(media), "迎賓")["ackedAt"]; got != nil {
		t.Errorf("media ackedAt = %v", got)
	}
}

func TestUS_RN_1_TaskPool(t *testing.T) {
	e := newEnv(t)
	runA, runAID := e.jobRole(domain.Runner)
	runB, _ := e.jobRole(domain.Runner)
	r := e.post(pathAddTask, e.master, map[string]any{"title": "  第 5 桌加兒童椅 "})
	r.wantResult(t, "ok")
	tasks := list(t, r.snapshot(t), "tasks")
	if len(tasks) != 1 || tasks[0]["title"] != "第 5 桌加兒童椅" || tasks[0]["state"] != "open" || tasks[0]["createdBy"] != "總召" {
		t.Fatalf("tasks = %v", tasks)
	}
	id := tasks[0]["id"]
	v := e.version()
	set := func(tok string, state string) resp {
		return e.post(pathSetTask, tok, map[string]any{"taskId": id, "state": state})
	}
	r = set(runA, "claimed")
	r.wantResult(t, "ok")
	if got := list(t, r.snapshot(t), "tasks")[0]; got["state"] != "claimed" || got["claimedBy"] != "角色Runner" {
		t.Errorf("after claim: %v", got)
	}
	// US-RN-1.2: another runner cannot take, release or finish it.
	for _, s := range []string{"claimed", "open", "done"} {
		set(runB, s).wantError(t, http.StatusUnprocessableEntity, "task_taken")
	}
	set(runA, "open").wantResult(t, "ok")
	set(runA, "done").wantError(t, http.StatusUnprocessableEntity, "invalid_task_state")
	set(runB, "claimed").wantResult(t, "ok")
	set(runB, "done").wantResult(t, "ok")
	set(runB, "open").wantError(t, http.StatusUnprocessableEntity, "invalid_task_state")
	set(runA, "bogus").wantError(t, http.StatusUnprocessableEntity, "invalid_task_state")
	// US-RN-1.3: the Master reopens it, never claims for someone.
	set(e.master, "claimed").wantError(t, http.StatusUnprocessableEntity, "invalid_task_state")
	r = set(e.master, "open")
	r.wantResult(t, "ok")
	if got := list(t, r.snapshot(t), "tasks")[0]; got["state"] != "open" || got["claimedBy"] != nil {
		t.Errorf("after Master reopen: %v", got)
	}
	if e.version() != v {
		t.Error("a task changed the event version")
	}
	// US-RN-1.4: a runner sees open and claimed tasks and only its own done ones.
	e.post(pathAddTask, e.master, map[string]any{"title": "補水"}).wantResult(t, "ok")
	set(runA, "claimed").wantResult(t, "ok")
	set(runA, "done").wantResult(t, "ok")
	if n := len(list(t, e.snap(runA), "tasks")); n != 2 {
		t.Errorf("runner A sees %d tasks, want 2 (補水 open, own done)", n)
	}
	if n := len(list(t, e.snap(runB), "tasks")); n != 1 {
		t.Errorf("runner B sees %d tasks, want 1 (補水 open)", n)
	}
	// US-RN-1.5: the Master deletes; the log keeps the whole task.
	e.post(pathDeleteTask, e.master, map[string]any{"taskId": id}).wantResult(t, "ok")
	var payload string
	e.db().QueryRow(`SELECT payload FROM action_logs WHERE type = 'task.delete'`).Scan(&payload)
	if !strings.Contains(payload, "第 5 桌加兒童椅") || !strings.Contains(payload, `"claimedByRoleId":`+jsonInt(runAID)) {
		t.Errorf("delete log = %s", payload)
	}
	// Titles and roles that may not add.
	e.post(pathAddTask, e.master, map[string]any{"title": "  "}).wantError(t, http.StatusUnprocessableEntity, "invalid_task")
	e.post(pathAddTask, runA, map[string]any{"title": "x"}).wantError(t, http.StatusForbidden, "forbidden")
	e.post(pathSetTask, e.stylist, map[string]any{"taskId": id, "state": "claimed"}).wantError(t, http.StatusForbidden, "forbidden")
	if got := e.snap(e.stylist)["tasks"]; got != nil {
		t.Errorf("stylist tasks = %v, want null", got)
	}
}

func TestUS_RC_2_ReceptionCallsARunner(t *testing.T) {
	e := newEnv(t)
	rec, _ := e.jobRole(domain.Reception)
	runner, _ := e.jobRole(domain.Runner)
	e.post(pathAddTask, e.master, map[string]any{"title": "總召的任務"}).wantResult(t, "ok")
	r := e.post(pathAddTask, rec, map[string]any{"title": "門口需要支援"})
	r.wantResult(t, "ok")
	mine := list(t, r.snapshot(t), "tasks")
	if len(mine) != 1 || mine[0]["title"] != "門口需要支援" || mine[0]["createdBy"] != "角色Reception" {
		t.Errorf("reception sees %v, want only its own call", mine)
	}
	if n := len(list(t, e.snap(runner), "tasks")); n != 2 {
		t.Errorf("runner sees %d tasks, want 2", n)
	}
	e.post(pathSetTask, rec, map[string]any{"taskId": mine[0]["id"], "state": "claimed"}).wantError(t, http.StatusForbidden, "forbidden")
}

func TestUS_RC_1_GuestList(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	rec, _ := e.jobRole(domain.Reception)
	media := e.addRoleWithToken(domain.Media)
	text := "姓名\t桌號\t備註\n陳大文\t5\t素食\n\n陳小美\t5\n林阿姨\t主桌\t輪椅\n"
	r := e.post(pathImportGuests, e.master, map[string]any{"text": text})
	r.wantResult(t, "ok")
	guests := list(t, r.snapshot(t), "guests")
	if len(guests) != 3 || guests[0]["name"] != "陳大文" || guests[0]["table"] != "5" || guests[0]["note"] != "素食" ||
		guests[1]["note"] != nil || guests[2]["table"] != "主桌" {
		t.Fatalf("guests = %v", guests)
	}
	// Commas work too; the header is the first non-blank line.
	e.post(pathImportGuests, e.master, map[string]any{"text": "\n姓名,桌號\n陳大文,5,素食\n陳小美,5\n林阿姨,主桌,輪椅"}).wantResult(t, "ok")
	guests = list(t, e.snap(rec), "guests")
	if len(guests) != 3 {
		t.Fatalf("reception guests = %v", guests)
	}
	// US-RC-1.3: only the Master and the reception see the list.
	for _, tok := range []string{media, e.stylist} {
		if got := e.snap(tok)["guests"]; got != nil {
			t.Errorf("guests leaked: %v", got)
		}
	}
	if body := e.get(e.addRoleWithToken(domain.Host)).Body.String(); strings.Contains(body, "陳大文") {
		t.Error("the hosts' snapshot holds a guest")
	}
	// US-RC-1.5: one bad line refuses the whole list; a header only never empties it.
	for _, bad := range []string{"姓名\t桌號\t備註\n", "\n\n", "只有名字", "陳\t5\t備註\t多一欄", "\t5", "陳\t" + strings.Repeat("桌", 21), "陳​大文\t5"} {
		e.post(pathImportGuests, e.master, map[string]any{"text": bad}).wantError(t, http.StatusUnprocessableEntity, "invalid_import")
	}
	// US-RC-1.2: arrival by the reception.
	id := guests[0]["id"]
	r = e.post(pathArrive, rec, map[string]any{"guestId": id, "arrived": true})
	r.wantResult(t, "ok")
	if !list(t, r.snapshot(t), "guests")[0]["arrived"].(bool) {
		t.Error("guest not arrived")
	}
	e.post(pathArrive, media, map[string]any{"guestId": id, "arrived": false}).wantError(t, http.StatusForbidden, "forbidden")
	// US-RC-1.4: no replacing once someone arrived, or on the event day.
	e.post(pathImportGuests, e.master, map[string]any{"text": "新名單,1"}).wantError(t, http.StatusUnprocessableEntity, "guests_arrived")
	e.post(pathArrive, e.master, map[string]any{"guestId": id, "arrived": false}).wantResult(t, "ok")
	e.setClock(at(0, 0))
	e.post(pathImportGuests, e.master, map[string]any{"text": "新名單,1"}).wantError(t, http.StatusUnprocessableEntity, "locked_on_event_day")
	// The replaced list is logged in full.
	var payload string
	e.db().QueryRow(`SELECT payload FROM action_logs WHERE type = 'guests.import' ORDER BY id DESC LIMIT 1`).Scan(&payload)
	if !strings.Contains(payload, "陳大文") {
		t.Errorf("import log does not keep the replaced list: %s", payload)
	}
}

func TestUS_CA_1_Checklist(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	cash, _ := e.jobRole(domain.Cashier)
	r := e.post(pathChecklist, e.master, map[string]any{"text": "總金額確認\n\n簽名確認\n 鑰匙交給：伴郎 \n"})
	r.wantResult(t, "ok")
	items := list(t, r.snapshot(t), "checklist")
	if len(items) != 3 || items[2]["title"] != "鑰匙交給：伴郎" || items[0]["done"] != false {
		t.Fatalf("checklist = %v", items)
	}
	r = e.post(pathCheckItem, cash, map[string]any{"itemId": items[0]["id"], "done": true})
	r.wantResult(t, "ok")
	if got := list(t, r.snapshot(t), "checklist")[0]; got["done"] != true || got["doneBy"] != "角色Cashier" {
		t.Errorf("after check: %v", got)
	}
	e.post(pathChecklist, e.master, map[string]any{"text": "新的"}).wantError(t, http.StatusUnprocessableEntity, "checklist_started")
	e.post(pathCheckItem, cash, map[string]any{"itemId": items[0]["id"], "done": false}).wantResult(t, "ok")
	for _, bad := range []string{strings.Repeat("項\n", 31), "", " \n \n"} {
		e.post(pathChecklist, e.master, map[string]any{"text": bad}).wantError(t, http.StatusUnprocessableEntity, "invalid_checklist")
	}
	if n := len(list(t, e.snap(cash), "checklist")); n != 3 {
		t.Errorf("checklist has %d items after refused replacements, want 3", n)
	}
	e.post(pathCheckItem, e.stylist, map[string]any{"itemId": items[0]["id"], "done": true}).wantError(t, http.StatusForbidden, "forbidden")
	if got := e.snap(e.stylist)["checklist"]; got != nil {
		t.Errorf("stylist checklist = %v", got)
	}
	e.setClock(at(0, 0))
	e.post(pathChecklist, e.master, map[string]any{"text": "新的"}).wantError(t, http.StatusUnprocessableEntity, "locked_on_event_day")
}

// Every new endpoint for every role type (api.md section 4): only the
// listed cells are let in.
func TestJobEndpointPermissions(t *testing.T) {
	cells := map[string][]domain.RoleType{
		pathAck:          {domain.VenueLead},
		pathAddTask:      {domain.Master, domain.Reception},
		pathSetTask:      {domain.Master, domain.Runner},
		pathDeleteTask:   {domain.Master},
		pathImportGuests: {domain.Master},
		pathArrive:       {domain.Master, domain.Reception},
		pathChecklist:    {domain.Master},
		pathCheckItem:    {domain.Master, domain.Cashier},
	}
	bodies := map[string]map[string]any{
		pathAck: {"cueId": 1, "acked": true}, pathAddTask: {"title": "x"}, pathSetTask: {"taskId": 1, "state": "open"},
		pathDeleteTask: {"taskId": 1}, pathImportGuests: {"text": "a,1"}, pathArrive: {"guestId": 1, "arrived": true},
		pathChecklist: {"text": "a"}, pathCheckItem: {"itemId": 1, "done": true},
	}
	e := newEnv(t)
	tokens := map[domain.RoleType]string{domain.Master: e.master}
	for _, typ := range allRoleTypes {
		if typ != domain.Master {
			tokens[typ] = e.addRoleWithToken(typ)
		}
	}
	for path, ok := range cells {
		for typ, tok := range tokens {
			r := e.post(path, tok, bodies[path])
			allowedHere := false
			for _, a := range ok {
				allowedHere = allowedHere || a == typ
			}
			if allowedHere == (r.Code == http.StatusForbidden) {
				t.Errorf("%s as %s: status %d (allowed %v)", path, typ, r.Code, allowedHere)
			}
		}
	}
}

// Another event's task, guest and item ids are a 404 before any rule
// (api.md 9.8), whether that event is later or has ended.
func TestJobIDsOfAnotherEventAre404(t *testing.T) {
	for _, day := range []int{7, 1} {
		e := newEnv(t)
		f := e.otherEvent(day)
		var other int64
		e.db().QueryRow(`SELECT event_id FROM roles WHERE id = ?`, f.stylist).Scan(&other)
		e.exec(`INSERT INTO tasks (id, event_id, title, created_at, state, updated_at, claimed_by_role_id) VALUES (900, ?, '別場', 0, 'claimed', 0, ?)`, other, f.stylist)
		e.exec(`INSERT INTO guests (id, event_id, seq, name, table_label, arrived_at) VALUES (900, ?, 1, '別場賓客', '1', 1)`, other)
		e.exec(`INSERT INTO checklist_items (id, event_id, seq, title, done_at) VALUES (900, ?, 1, '別場', 1)`, other)
		runner := e.addRoleWithToken(domain.Runner)
		lead := e.addRoleWithToken(domain.VenueLead)
		for _, c := range []struct {
			path, tok string
			body      map[string]any
		}{
			{pathSetTask, runner, map[string]any{"taskId": 900, "state": "claimed"}},
			{pathSetTask, e.master, map[string]any{"taskId": 900, "state": "claimed"}},
			{pathDeleteTask, e.master, map[string]any{"taskId": 900}},
			{pathArrive, e.master, map[string]any{"guestId": 900, "arrived": true}},
			{pathCheckItem, e.master, map[string]any{"itemId": 900, "done": true}},
			{pathAck, lead, map[string]any{"cueId": f.doneCue, "acked": true}},
		} {
			e.post(c.path, c.tok, c.body).wantError(t, http.StatusNotFound, "not_found")
		}
	}
}

// api.md 9.4: deleting a role puts the task it had claimed back in the pool.
func TestUS_RN_1_DeletedClaimerReopensTheTask(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	runner, runnerID := e.jobRole(domain.Runner)
	other, _ := e.jobRole(domain.Runner)
	e.exec(`UPDATE roles SET name = '機動B' WHERE id = (SELECT MAX(id) FROM roles)`)
	r := e.post(pathAddTask, e.master, map[string]any{"title": "補水"})
	id := list(t, r.snapshot(t), "tasks")[0]["id"]
	e.post(pathSetTask, runner, map[string]any{"taskId": id, "state": "claimed"}).wantResult(t, "ok")
	e.post(pathDeleteRole, e.master, map[string]any{"version": e.version(), "roleId": runnerID}).wantResult(t, "ok")
	if got := list(t, e.snap(e.master), "tasks")[0]; got["state"] != "open" || got["claimedBy"] != nil {
		t.Errorf("after the claimer was deleted: %v", got)
	}
	e.post(pathSetTask, other, map[string]any{"taskId": id, "state": "claimed"}).wantResult(t, "ok")
}
