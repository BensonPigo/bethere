package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// The jobs' edges (api.md section 9): the Master's view of a
// claim, concurrent claims, limits, who sees what.

// addTask has the Master add a task and returns its id.
func (e *env) addTask(title string) any {
	e.t.Helper()
	r := e.post(pathAddTask, e.master, map[string]any{"title": title})
	r.wantResult(e.t, "ok")
	return list(e.t, r.snapshot(e.t), "tasks")[0]["id"]
}

func (e *env) setTask(tok string, id any, state string) resp {
	e.t.Helper()
	return e.post(pathSetTask, tok, map[string]any{"taskId": id, "state": state})
}

// renameLast renames the role added last, so that two roles of one type
// are told apart in a snapshot.
func (e *env) renameLast(name string) {
	e.t.Helper()
	e.exec(`UPDATE roles SET name = ? WHERE id = (SELECT MAX(id) FROM roles)`, name)
}

// US-RN-1.1: the Master's snapshot names who claimed a task.
func TestUS_RN_1_1_MasterSeesTheClaimer(t *testing.T) {
	e := newEnv(t)
	runner, _ := e.jobRole(domain.Runner)
	e.renameLast("機動甲")
	id := e.addTask("補水")
	if got := list(t, e.snap(e.master), "tasks")[0]; got["state"] != "open" || got["claimedBy"] != nil {
		t.Errorf("open task in the Master's snapshot = %v", got)
	}
	e.setTask(runner, id, "claimed").wantResult(t, "ok")
	got := list(t, e.snap(e.master), "tasks")[0]
	if got["state"] != "claimed" || got["claimedBy"] != "機動甲" || got["createdBy"] != "總召" {
		t.Errorf("claimed task in the Master's snapshot = %v", got)
	}
}

// US-RN-1.3, US-M-16.1: the Master marks a task done, open or claimed; a
// claimed one keeps its claimer; the task then shows in the claimer's own
// finished list and a done task is no longer claimable.
func TestUS_RN_1_3_MasterMarksDone(t *testing.T) {
	e := newEnv(t)
	runner, _ := e.jobRole(domain.Runner)
	e.renameLast("機動甲")
	other, _ := e.jobRole(domain.Runner)
	open, claimed := e.addTask("開著的"), e.addTask("認領的")
	e.setTask(runner, claimed, "claimed").wantResult(t, "ok")
	v := e.version()
	r := e.setTask(e.master, open, "done")
	r.wantResult(t, "ok")
	e.setTask(e.master, claimed, "done").wantResult(t, "ok")
	byID := map[any]map[string]any{}
	for _, tk := range list(t, e.snap(e.master), "tasks") {
		byID[tk["id"]] = tk
	}
	if got := byID[open]; got["state"] != "done" || got["claimedBy"] != nil {
		t.Errorf("open task marked done = %v", got)
	}
	if got := byID[claimed]; got["state"] != "done" || got["claimedBy"] != "機動甲" {
		t.Errorf("claimed task marked done = %v", got)
	}
	if e.version() != v {
		t.Error("a task changed the event version")
	}
	// The claimer sees the one it had, no runner sees the unclaimed done one.
	if n := len(list(t, e.snap(runner), "tasks")); n != 1 {
		t.Errorf("claimer sees %d tasks, want its own done one", n)
	}
	if n := len(list(t, e.snap(other), "tasks")); n != 0 {
		t.Errorf("the other runner sees %d tasks, want 0", n)
	}
	e.setTask(other, open, "claimed").wantError(t, http.StatusUnprocessableEntity, "invalid_task_state")
	// The Master reopens a done task; its claimer is cleared.
	r = e.setTask(e.master, claimed, "open")
	r.wantResult(t, "ok")
	if got := list(t, r.snapshot(t), "tasks")[0]; got["state"] != "open" || got["claimedBy"] != nil {
		t.Errorf("reopened task = %v", got)
	}
}

// US-RN-1.2: two runners pressing "I'll take it" at once leave one claimer.
func TestUS_RN_1_2_TwoRunnersClaimAtOnce(t *testing.T) {
	e := newEnv(t)
	a, _ := e.jobRole(domain.Runner)
	b, _ := e.jobRole(domain.Runner)
	id := e.addTask("補水")
	rs := e.parallel(2, func(i int) resp {
		tok := a
		if i == 1 {
			tok = b
		}
		return e.post(pathSetTask, tok, map[string]any{"taskId": id, "state": "claimed"})
	})
	wantOneOK(t, rs, http.StatusUnprocessableEntity, "task_taken")
	if got := list(t, e.snap(e.master), "tasks")[0]; got["state"] != "claimed" {
		t.Errorf("task = %v", got)
	}
}

// US-RN-1.2: claiming a task of one's own again is no error and no change;
// a done task can neither be claimed nor reopened by a runner.
func TestUS_RN_1_2_ClaimAgainAndDone(t *testing.T) {
	e := newEnv(t)
	a, _ := e.jobRole(domain.Runner)
	b, _ := e.jobRole(domain.Runner)
	id := e.addTask("補水")
	e.setTask(a, id, "claimed").wantResult(t, "ok")
	var logsBefore = e.count(`SELECT count(*) FROM action_logs WHERE type = 'task.set'`)
	r := e.setTask(a, id, "claimed")
	r.wantResult(t, "ok")
	if got := list(t, r.snapshot(t), "tasks")[0]; got["state"] != "claimed" || got["claimedBy"] != "角色Runner" {
		t.Errorf("after claiming again: %v", got)
	}
	if n := e.count(`SELECT count(*) FROM action_logs WHERE type = 'task.set'`); n != logsBefore {
		t.Errorf("a repeated claim wrote %d log entries", n-logsBefore)
	}
	e.setTask(a, id, "done").wantResult(t, "ok")
	e.setTask(a, id, "claimed").wantError(t, http.StatusUnprocessableEntity, "invalid_task_state")
	e.setTask(a, id, "open").wantError(t, http.StatusUnprocessableEntity, "invalid_task_state")
	e.setTask(b, id, "claimed").wantError(t, http.StatusUnprocessableEntity, "invalid_task_state")
	e.setTask(a, id, "done").wantResult(t, "ok") // done again: nothing to change
}

// US-M-16.1 (api.md 9.4): a title of 100 characters, at most 200 tasks.
func TestUS_M_16_1_TaskLimits(t *testing.T) {
	e := newEnv(t)
	e.post(pathAddTask, e.master, map[string]any{"title": strings.Repeat("任", 101)}).wantError(t, http.StatusUnprocessableEntity, "invalid_task")
	e.post(pathAddTask, e.master, map[string]any{"title": strings.Repeat("任", 100)}).wantResult(t, "ok")
	for i := 1; i < 200; i++ {
		e.post(pathAddTask, e.master, map[string]any{"title": fmt.Sprintf("任務%d", i)}).wantResult(t, "ok")
	}
	e.post(pathAddTask, e.master, map[string]any{"title": "第201個"}).wantError(t, http.StatusUnprocessableEntity, "too_many_tasks")
	if n := e.count(`SELECT count(*) FROM tasks`); n != 200 {
		t.Errorf("tasks = %d, want 200", n)
	}
	// The limit counts every state: a deleted task frees a place.
	id := list(t, e.snap(e.master), "tasks")[0]["id"]
	e.post(pathDeleteTask, e.master, map[string]any{"taskId": id}).wantResult(t, "ok")
	e.post(pathAddTask, e.master, map[string]any{"title": "補上"}).wantResult(t, "ok")
}

// US-RC-2.1: two receptions each see only the calls they made.
func TestUS_RC_2_1_ReceptionsDoNotSeeEachOthersCalls(t *testing.T) {
	e := newEnv(t)
	a, _ := e.jobRole(domain.Reception)
	e.renameLast("迎賓甲")
	b, _ := e.jobRole(domain.Reception)
	e.renameLast("迎賓乙")
	e.post(pathAddTask, a, map[string]any{"title": "甲的呼叫"}).wantResult(t, "ok")
	e.post(pathAddTask, b, map[string]any{"title": "乙的呼叫"}).wantResult(t, "ok")
	for tok, want := range map[string]string{a: "甲的呼叫", b: "乙的呼叫"} {
		mine := list(t, e.snap(tok), "tasks")
		if len(mine) != 1 || mine[0]["title"] != want {
			t.Errorf("a reception sees %v, want only %s", mine, want)
		}
	}
	if n := len(list(t, e.snap(e.master), "tasks")); n != 2 {
		t.Errorf("the Master sees %d calls, want 2", n)
	}
}

// guestText is n valid guests, one per line.
func guestText(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "賓客%d,%d\n", i, i%20+1)
	}
	return b.String()
}

// US-RC-1.5: one bad line among good ones refuses the whole list and keeps
// the old; the limits are 1000 guests and a table of 20 characters.
func TestUS_RC_1_5_MixedAndLimits(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	e.post(pathImportGuests, e.master, map[string]any{"text": "陳大文,5\n陳小美,5"}).wantResult(t, "ok")
	for _, bad := range []string{
		"好一,1\n好二,2\n壞,\n好三,3",
		"好一,1\n壞," + strings.Repeat("桌", 21),
		"好一,1\n" + strings.Repeat("名", 51) + ",1",
		"好一,1,備註\n好二,2," + strings.Repeat("註", 101),
		"好一,1\n只有名字",
		guestText(1001),
	} {
		e.post(pathImportGuests, e.master, map[string]any{"text": bad}).wantError(t, http.StatusUnprocessableEntity, "invalid_import")
		if n := e.count(`SELECT count(*) FROM guests`); n != 2 {
			t.Fatalf("a refused list left %d guests, want the 2 before", n)
		}
	}
	e.post(pathImportGuests, e.master, map[string]any{"text": "好一," + strings.Repeat("桌", 20)}).wantResult(t, "ok")
	e.post(pathImportGuests, e.master, map[string]any{"text": "好一,1,\n好二,2," + strings.Repeat("註", 100) + "\n" + strings.Repeat("名", 50) + ",3"}).wantResult(t, "ok")
	r := e.post(pathImportGuests, e.master, map[string]any{"text": guestText(1000)})
	r.wantResult(t, "ok")
	if n := len(list(t, r.snapshot(t), "guests")); n != 1000 {
		t.Errorf("guests = %d, want 1000", n)
	}
}

// US-RC-1.2: the reception cancels an arrival of its own, and an arrival
// of a guest of the same name is told apart by its id.
func TestUS_RC_1_2_ReceptionCancelsAnArrival(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	rec, _ := e.jobRole(domain.Reception)
	e.post(pathImportGuests, e.master, map[string]any{"text": "陳大文,5\n陳大文,5"}).wantResult(t, "ok")
	guests := list(t, e.snap(rec), "guests")
	first, second := guests[0]["id"], guests[1]["id"]
	e.post(pathArrive, rec, map[string]any{"guestId": second, "arrived": true}).wantResult(t, "ok")
	r := e.post(pathArrive, rec, map[string]any{"guestId": first, "arrived": true})
	r.wantResult(t, "ok")
	e.post(pathArrive, rec, map[string]any{"guestId": first, "arrived": false}).wantResult(t, "ok")
	got := list(t, e.snap(rec), "guests")
	if got[0]["arrived"] != false || got[1]["arrived"] != true {
		t.Errorf("guests = %v, want only the second arrived", got)
	}
}

// US-RC-1.3: only the Master and the reception hold the guest list; the
// venue lead and the cashier hold none of it.
func TestUS_RC_1_3_VenueLeadAndCashierSeeNoGuests(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	e.post(pathImportGuests, e.master, map[string]any{"text": "陳大文,5,素食"}).wantResult(t, "ok")
	for _, typ := range []domain.RoleType{domain.VenueLead, domain.Cashier} {
		tok, _ := e.jobRole(typ)
		r := e.get(tok)
		r.wantResult(t, "ok")
		if got := r.snapshot(t)["guests"]; got != nil {
			t.Errorf("%s: guests = %v, want null", typ, got)
		}
		for _, s := range []string{"陳大文", "素食"} {
			if strings.Contains(r.Body.String(), s) {
				t.Errorf("%s response contains %q", typ, s)
			}
		}
	}
}

// US-CA-1.1: 30 items are taken; a list none of which is done is replaced.
func TestUS_CA_1_1_ThirtyItemsAndReplace(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("項目%d", i))
	}
	r := e.post(pathChecklist, e.master, map[string]any{"text": strings.Join(lines, "\n")})
	r.wantResult(t, "ok")
	if n := len(list(t, r.snapshot(t), "checklist")); n != 30 {
		t.Fatalf("checklist has %d items, want 30", n)
	}
	e.post(pathChecklist, e.master, map[string]any{"text": strings.Join(lines, "\n") + "\n項目31"}).
		wantError(t, http.StatusUnprocessableEntity, "invalid_checklist")
	e.post(pathChecklist, e.master, map[string]any{"text": strings.Repeat("項", 101)}).
		wantError(t, http.StatusUnprocessableEntity, "invalid_checklist")
	r = e.post(pathChecklist, e.master, map[string]any{"text": strings.Repeat("項", 100) + "\n第二項"})
	r.wantResult(t, "ok")
	if items := list(t, r.snapshot(t), "checklist"); len(items) != 2 || items[1]["title"] != "第二項" || items[1]["done"] != false {
		t.Errorf("replaced checklist = %v", items)
	}
}

// US-CA-1.2: two cashiers ticking one item in turn keep the first one's
// name and time; ticking again changes nothing; once every tick is taken
// back the list can be replaced.
func TestUS_CA_1_2_FirstTickIsKept(t *testing.T) {
	e := newEnv(t)
	e.dayBefore()
	a, _ := e.jobRole(domain.Cashier)
	e.renameLast("禮金甲")
	b, _ := e.jobRole(domain.Cashier)
	e.renameLast("禮金乙")
	r := e.post(pathChecklist, e.master, map[string]any{"text": "總金額確認\n簽名"})
	r.wantResult(t, "ok")
	id := list(t, r.snapshot(t), "checklist")[0]["id"]
	tick := func(tok string, done bool) resp {
		return e.post(pathCheckItem, tok, map[string]any{"itemId": id, "done": done})
	}
	doneAt := func() int {
		return e.count(`SELECT coalesce(done_at, 0) FROM checklist_items WHERE id = ?`, id)
	}
	e.setClock(e.clock().Add(time.Minute))
	tick(a, true).wantResult(t, "ok")
	first := doneAt()
	e.setClock(e.clock().Add(time.Minute))
	r = tick(b, true)
	r.wantResult(t, "ok")
	if got := list(t, r.snapshot(t), "checklist")[0]; got["done"] != true || got["doneBy"] != "禮金甲" {
		t.Errorf("after the second tick: %v, want done by 禮金甲", got)
	}
	tick(a, true).wantResult(t, "ok")
	if doneAt() != first || first == 0 {
		t.Errorf("done_at moved from %d to %d on a repeated tick", first, doneAt())
	}
	if n := e.count(`SELECT count(*) FROM action_logs WHERE type = 'checklist.check'`); n != 1 {
		t.Errorf("checklist.check logs = %d, want 1", n)
	}
	e.post(pathChecklist, e.master, map[string]any{"text": "新的"}).wantError(t, http.StatusUnprocessableEntity, "checklist_started")
	tick(b, false).wantResult(t, "ok") // taking back needs no one's own tick
	if got := list(t, e.snap(a), "checklist")[0]; got["done"] != false || got["doneBy"] != nil {
		t.Errorf("after taking back: %v", got)
	}
	e.post(pathChecklist, e.master, map[string]any{"text": "新的"}).wantResult(t, "ok")
}

// US-H-1.2: with every cue done a host's snapshot holds no delay and no
// current title.
func TestUS_H_1_2_HostWithNoPendingCue(t *testing.T) {
	e := newEnv(t)
	host := e.addRoleWithToken(domain.Host)
	e.live()
	for {
		r := e.write(pathAdvance, e.master)
		if r.Code != http.StatusOK {
			r.wantError(t, http.StatusUnprocessableEntity, "nothing_to_advance")
			break
		}
		e.setClock(e.clock().Add(30 * time.Minute))
	}
	for _, c := range cues(t, e.snap(e.master)) {
		if c["status"] != "Completed" {
			t.Fatalf("cue %v is %v, want every cue done", c["title"], c["status"])
		}
	}
	d := e.snap(host)
	if d["delayMin"] != 0.0 || d["currentTitle"] != nil {
		t.Errorf("host dashboard with nothing pending = delay %v, title %v; want 0, null", d["delayMin"], d["currentTitle"])
	}
}
