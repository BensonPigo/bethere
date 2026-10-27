package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

var taipei = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		panic(err)
	}
	return loc
}()

func at(hh, mm int) time.Time { return time.Date(2027, 6, 6, hh, mm, 0, 0, taipei) }

func hash(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

var ctx = context.Background()

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bethere.db")
	s, err := Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

// fixture is a draft event with a stylist and three cues.
type fixture struct {
	s        *Store
	path     string
	eventID  int64
	master   int64
	stylist  int64
	cueIDs   []int64
	masterBy Actor
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	s, path := newStore(t)
	f := fixture{s: s, path: path}
	var err error
	f.eventID, err = s.CreateEvent(ctx, NewEvent{
		Name: "婚宴", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: hash("master"), RecoveryCodeHash: hash("recovery"),
	}, at(0, 0).AddDate(0, -1, 0))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.LookupToken(ctx, hash("master"), at(12, 0))
	if err != nil {
		t.Fatal(err)
	}
	f.master, f.masterBy = owner.RoleID, owner.Actor
	if f.stylist, err = s.AddRole(ctx, f.eventID, "新秘", domain.Stylist, f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	for i, hm := range [][2]int{{10, 0}, {10, 40}, {11, 25}} {
		id, err := s.AddCue(ctx, f.eventID, domain.Cue{Seq: i + 1, Title: "cue", ScheduledStart: at(hm[0], hm[1]), DurationMin: 30}, f.masterBy, at(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		f.cueIDs = append(f.cueIDs, id)
	}
	return f
}

func (f fixture) version(t *testing.T) int64 {
	t.Helper()
	_, v, err := f.s.Event(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f fixture) start(t *testing.T) {
	t.Helper()
	if _, err := f.s.StartEvent(ctx, f.eventID, f.version(t), f.masterBy, at(9, 0)); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) statuses(t *testing.T) []domain.Status {
	t.Helper()
	cues, err := f.s.Cues(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]domain.Status, len(cues))
	for i, c := range cues {
		out[i] = c.Status
	}
	return out
}

func advance(now time.Time) func([]domain.Cue) (domain.Op, error) {
	return func(cues []domain.Cue) (domain.Op, error) { return domain.Advance(cues, now) }
}

func TestCreateEventAndTokens(t *testing.T) {
	f := newFixture(t)
	e, _, err := f.s.Event(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != domain.Draft || e.Date != (domain.Date{Year: 2027, Month: time.June, Day: 6}) || e.Location.String() != "Asia/Taipei" {
		t.Errorf("event = %+v", e)
	}
	owner, err := f.s.LookupToken(ctx, hash("master"), at(12, 0))
	if err != nil || owner.RoleType != domain.Master || owner.EventID != f.eventID {
		t.Errorf("master token owner = %+v (%v)", owner, err)
	}

	if _, err := f.s.AddRole(ctx, f.eventID, "第二位總召", domain.Master, f.masterBy, at(0, 0)); !errors.Is(err, ErrMasterRole) {
		t.Errorf("adding a Master role: err = %v, want ErrMasterRole", err)
	}

	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist-1"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist-2"), f.masterBy, at(2, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.LookupToken(ctx, hash("stylist-1"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("reissued token: err = %v, want ErrInvalidToken", err)
	}
	if owner, err := f.s.LookupToken(ctx, hash("stylist-2"), at(12, 0)); err != nil || owner.RoleID != f.stylist {
		t.Errorf("new token owner = %+v (%v)", owner, err)
	}
	if err := f.s.RevokeToken(ctx, f.stylist, f.masterBy, at(3, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.LookupToken(ctx, hash("stylist-2"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("revoked token: err = %v, want ErrInvalidToken", err)
	}
	if err := f.s.RevokeToken(ctx, f.stylist, f.masterBy, at(3, 0)); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking twice: err = %v, want ErrNotFound", err)
	}
}

// A database failure must never look like "token not found" (tech-stack.md 2.1「逾時與錯誤」).
func TestLookupTokenErrorIsNotNotFound(t *testing.T) {
	s, _ := newStore(t)
	s.r.Close()
	_, err := s.LookupToken(ctx, hash("anything"), at(12, 0))
	if err == nil || errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want an error other than ErrInvalidToken", err)
	}
}

func TestStartEvent(t *testing.T) {
	f := newFixture(t)
	v := f.version(t)
	if _, err := f.s.StartEvent(ctx, f.eventID, v-1, f.masterBy, at(9, 0)); !errors.Is(err, ErrVersionConflict) {
		t.Errorf("stale version: err = %v, want ErrVersionConflict", err)
	}
	if _, err := f.s.StartEvent(ctx, f.eventID, v, f.masterBy, at(0, 0).Add(-time.Minute)); !errors.Is(err, domain.ErrNotEventDay) {
		t.Errorf("day before: err = %v, want ErrNotEventDay", err)
	}
	nv, err := f.s.StartEvent(ctx, f.eventID, v, f.masterBy, at(9, 0))
	if err != nil {
		t.Fatal(err)
	}
	e, got, _ := f.s.Event(ctx, f.eventID)
	if nv != v+1 || got != nv || e.Status != domain.Live || !e.LiveStartedAt.Equal(at(9, 0)) {
		t.Errorf("after start: version %d/%d, event %+v", nv, got, e)
	}
}

func TestApplyCueOp(t *testing.T) {
	f := newFixture(t)
	v := f.version(t)
	if _, _, err := f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 0), advance(at(10, 0))); !errors.Is(err, domain.ErrNotLive) {
		t.Fatalf("draft event: err = %v, want ErrNotLive", err)
	}
	f.start(t)
	v = f.version(t)

	if _, _, err := f.s.ApplyCueOp(ctx, f.eventID, v-1, f.masterBy, at(10, 0), advance(at(10, 0))); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version: err = %v, want ErrVersionConflict", err)
	}
	failing := func([]domain.Cue) (domain.Op, error) { return domain.Op{}, domain.ErrNothingToAdvance }
	if _, _, err := f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 0), failing); !errors.Is(err, domain.ErrNothingToAdvance) {
		t.Fatalf("rule error: err = %v", err)
	}
	if got := f.version(t); got != v {
		t.Fatalf("version moved to %d after rejected writes, want %d", got, v)
	}

	nv, op, err := f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 2), advance(at(10, 2)))
	if err != nil {
		t.Fatal(err)
	}
	if nv != v+1 || f.version(t) != nv || len(op.Changes) != 1 {
		t.Errorf("version %d (stored %d), changes %d", nv, f.version(t), len(op.Changes))
	}
	cues, _ := f.s.Cues(ctx, f.eventID)
	if cues[0].Status != domain.InProgress || !cues[0].ActualStart.Equal(at(10, 2)) || !cues[0].ActualEnd.IsZero() {
		t.Errorf("cue 1 = %+v", cues[0])
	}
}

func TestShiftAndUndoThroughStore(t *testing.T) {
	f := newFixture(t)
	f.start(t)
	v := f.version(t)
	v, _, err := f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 0), advance(at(10, 0)))
	if err != nil {
		t.Fatal(err)
	}
	shift := func(cues []domain.Cue) (domain.Op, error) {
		plan, err := domain.PlanShift(cues, domain.ShiftRequest{FromCueID: f.cueIDs[1], DeltaMin: 10}, at(10, 5))
		return plan.Op, err
	}
	v, _, err = f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 5), shift)
	if err != nil {
		t.Fatal(err)
	}
	cues, _ := f.s.Cues(ctx, f.eventID)
	if cues[1].OffsetMin != 10 || cues[2].OffsetMin != 10 {
		t.Fatalf("offsets after shift = %d, %d, want 10, 10", cues[1].OffsetMin, cues[2].OffsetMin)
	}

	// Link management between steps does not count as a step to undo.
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(10, 6)); err != nil {
		t.Fatal(err)
	}
	v = f.version(t)

	v, undo, err := f.s.UndoLastCueOp(ctx, f.eventID, v, f.masterBy, at(10, 7))
	if err != nil {
		t.Fatal(err)
	}
	if undo.Kind != domain.OpShift {
		t.Errorf("undo = %+v, want a shift", undo)
	}
	cues, _ = f.s.Cues(ctx, f.eventID)
	if cues[1].OffsetMin != 0 || cues[2].OffsetMin != 0 {
		t.Errorf("offsets after undo = %d, %d, want 0, 0", cues[1].OffsetMin, cues[2].OffsetMin)
	}

	// Only one step back (data-model.md 4.1).
	if _, _, err := f.s.UndoLastCueOp(ctx, f.eventID, v, f.masterBy, at(10, 8)); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("second undo: err = %v, want ErrNothingToUndo", err)
	}
	if got := f.statuses(t); got[0] != domain.InProgress {
		t.Errorf("advance was undone too: %v", got)
	}
}

func TestUndoSkipAndAdvance(t *testing.T) {
	f := newFixture(t)
	f.start(t)
	v := f.version(t)
	if _, _, err := f.s.UndoLastCueOp(ctx, f.eventID, v, f.masterBy, at(10, 0)); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo with no history: err = %v, want ErrNothingToUndo", err)
	}

	skip := func(cues []domain.Cue) (domain.Op, error) { return domain.Skip(cues, f.cueIDs[1]) }
	v, _, err := f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 0), skip)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err = f.s.UndoLastCueOp(ctx, f.eventID, v, f.masterBy, at(10, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.statuses(t); got[1] != domain.Pending {
		t.Errorf("after undoing skip: %v, want cue 2 Pending", got)
	}

	v, _, err = f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 2), advance(at(10, 2)))
	if err != nil {
		t.Fatal(err)
	}
	v, _, err = f.s.ApplyCueOp(ctx, f.eventID, v, f.masterBy, at(10, 40), advance(at(10, 40)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.UndoLastCueOp(ctx, f.eventID, v, f.masterBy, at(10, 41)); err != nil {
		t.Fatal(err)
	}
	cues, _ := f.s.Cues(ctx, f.eventID)
	if cues[0].Status != domain.InProgress || !cues[0].ActualStart.Equal(at(10, 2)) || !cues[0].ActualEnd.IsZero() {
		t.Errorf("cue 1 after undo = %+v, want InProgress since 10:02", cues[0])
	}
	if cues[1].Status != domain.Pending || !cues[1].ActualStart.IsZero() {
		t.Errorf("cue 2 after undo = %+v, want Pending", cues[1])
	}
}

func TestPurgeEnded(t *testing.T) {
	f := newFixture(t)
	if err := f.s.AssignRole(ctx, f.cueIDs[0], f.stylist, "", f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	e, _, _ := f.s.Event(ctx, f.eventID)

	ids, err := f.s.PurgeEnded(ctx, e.PurgeAt().Add(-time.Second))
	if err != nil || len(ids) != 0 {
		t.Fatalf("before PurgeAt: deleted %v (%v), want none", ids, err)
	}
	ids, err = f.s.PurgeEnded(ctx, e.PurgeAt())
	if err != nil || !slices.Equal(ids, []int64{f.eventID}) {
		t.Fatalf("at PurgeAt: deleted %v (%v), want [%d]", ids, err, f.eventID)
	}
	if _, _, err := f.s.Event(ctx, f.eventID); !errors.Is(err, ErrNotFound) {
		t.Errorf("event still there: %v", err)
	}
	if _, err := f.s.LookupToken(ctx, hash("master"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("token still there: %v", err)
	}
	for _, table := range []string{"roles", "access_tokens", "cues", "cue_roles", "check_ins", "action_logs"} {
		var count int
		if err := f.s.r.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s has %d rows left (%v)", table, count, err)
		}
	}
}

func TestAddCueNeedsAScheduledStart(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.AddCue(ctx, f.eventID, domain.Cue{Seq: 9, Title: "x"}, f.masterBy, at(0, 0)); !errors.Is(err, ErrInvalidCue) {
		t.Errorf("err = %v, want ErrInvalidCue", err)
	}
}

func TestOneCueInProgressPerEvent(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.w.Exec(`UPDATE cues SET status = 'InProgress' WHERE event_id = ?`, f.eventID); err == nil {
		t.Error("database accepted two cues in progress")
	}
}

// Lookup results follow access-control.md 3.3: invalid, then ended.
func TestLookupTokenAfterTheEventEnded(t *testing.T) {
	f := newFixture(t)
	e, _, _ := f.s.Event(ctx, f.eventID)
	if _, err := f.s.LookupToken(ctx, hash("master"), e.EndsAt().Add(-time.Nanosecond)); err != nil {
		t.Errorf("just before end: %v", err)
	}
	if _, err := f.s.LookupToken(ctx, hash("master"), e.EndsAt()); !errors.Is(err, ErrEventEnded) {
		t.Errorf("at end: err = %v, want ErrEventEnded", err)
	}
	if _, err := f.s.LookupToken(ctx, hash("unknown"), e.EndsAt()); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("unknown token after end: err = %v, want ErrInvalidToken", err)
	}
	if _, err := f.s.AddRole(ctx, f.eventID, "攝影", domain.Media, f.masterBy, e.EndsAt()); !errors.Is(err, domain.ErrEnded) {
		t.Errorf("AddRole after end: err = %v, want ErrEnded", err)
	}
}

func TestHashesAreSHA256Sized(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueToken(ctx, f.stylist, []byte("short"), f.masterBy, at(1, 0)); err == nil {
		t.Error("short token hash accepted")
	}
	if _, err := f.s.CreateEvent(ctx, NewEvent{
		Name: "x", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: hash("m3"), RecoveryCodeHash: []byte("short"),
	}, at(0, 0)); err == nil {
		t.Error("short recovery code hash accepted")
	}
}

// The payload holds only operation-owned fields under stable keys, so a
// renamed Go field or an edited title never leaks into or breaks the log.
func TestCueOpPayloadFormat(t *testing.T) {
	f := newFixture(t)
	f.start(t)
	shift := func(cues []domain.Cue) (domain.Op, error) {
		plan, err := domain.PlanShift(cues, domain.ShiftRequest{FromCueID: f.cueIDs[1], DeltaMin: 10, Excluded: []int64{f.cueIDs[2]}}, at(10, 5))
		return plan.Op, err
	}
	if _, _, err := f.s.ApplyCueOp(ctx, f.eventID, f.version(t), f.masterBy, at(10, 5), shift); err != nil {
		t.Fatal(err)
	}
	var typ, payload string
	if err := f.s.r.QueryRow(`SELECT type, payload FROM action_logs ORDER BY id DESC LIMIT 1`).Scan(&typ, &payload); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`{"v":1,"shift":{"fromCueId":%d,"deltaMin":10,"excluded":[%d]},`+
		`"changes":[{"cueId":%d,"before":{"status":"Pending","offsetMin":0},"after":{"status":"Pending","offsetMin":10}}]}`,
		f.cueIDs[1], f.cueIDs[2], f.cueIDs[1])
	if typ != "cue.shift" || payload != want {
		t.Errorf("logged %s %s\nwant cue.shift %s", typ, payload, want)
	}
}

// otherEvent creates a second event and returns it with its Master actor.
func otherEvent(t *testing.T, s *Store) (int64, Actor) {
	t.Helper()
	id, err := s.CreateEvent(ctx, NewEvent{
		Name: "另一場", Date: domain.Date{Year: 2027, Month: time.July, Day: 1}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: hash("master-2"), RecoveryCodeHash: hash("recovery-2"),
	}, at(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.LookupToken(ctx, hash("master-2"), at(12, 0))
	if err != nil {
		t.Fatal(err)
	}
	return id, owner.Actor
}

// Only a supported zone and a real date are stored, so every event can be
// read and purged later on any host (rounds 3 and 4).
func TestCreateEventValidatesZoneAndDate(t *testing.T) {
	s, _ := newStore(t)
	create := func(zone string, d domain.Date) error {
		_, err := s.CreateEvent(ctx, NewEvent{
			Name: "x", Date: d, TimeZone: zone,
			MasterName: "總召", MasterTokenHash: hash(zone + d.Month.String()), RecoveryCodeHash: hash("r" + zone + d.Month.String()),
		}, at(0, 0))
		return err
	}
	day := domain.Date{Year: 2027, Month: time.June, Day: 6}
	for _, zone := range []string{"", "asia/taipei"} {
		if err := create(zone, day); !errors.Is(err, ErrTimeZone) {
			t.Errorf("zone %q: err = %v, want ErrTimeZone", zone, err)
		}
	}
	for _, d := range []domain.Date{{Year: 2027, Month: time.June, Day: 31}, {Year: 10000, Month: 1, Day: 1}, {Year: -1, Month: 1, Day: 1}, {}} {
		if err := create("Asia/Taipei", d); !errors.Is(err, domain.ErrInvalidDate) {
			t.Errorf("date %v: err = %v, want ErrInvalidDate", d, err)
		}
	}
	if err := create("Asia/Taipei", day); err != nil {
		t.Errorf("valid event: %v", err)
	}
}

// A row edited by hand into something unreadable is reported, and does
// not hold back deleting the others.
func TestPurgeSkipsUnreadableEvents(t *testing.T) {
	f := newFixture(t)
	e, _, _ := f.s.Event(ctx, f.eventID)
	if _, err := f.s.w.Exec(`INSERT INTO events (name, event_date, timezone, status, recovery_code_hash, created_at)
		VALUES ('bad', '2027-06-06', 'Mars/Base', 'Draft', ?, 0)`, hash("bad")); err != nil {
		t.Fatal(err)
	}
	ids, err := f.s.PurgeEnded(ctx, e.PurgeAt())
	if !slices.Equal(ids, []int64{f.eventID}) {
		t.Errorf("deleted %v, want [%d]", ids, f.eventID)
	}
	if err == nil || !strings.Contains(err.Error(), "Mars/Base") {
		t.Errorf("err = %v, want the unreadable event reported", err)
	}
}

// An older release must stop at a cue operation it cannot read instead of
// undoing an earlier step behind it (rounds 1–3).
func TestUndoStopsAtUnknownOperations(t *testing.T) {
	f := newFixture(t)
	f.start(t)
	if _, _, err := f.s.ApplyCueOp(ctx, f.eventID, f.version(t), f.masterBy, at(10, 0), advance(at(10, 0))); err != nil {
		t.Fatal(err)
	}
	ok := `"changes":[{"cueId":1,"before":{"status":"Pending","offsetMin":0},"after":{"status":"Skipped","offsetMin":0}}]`
	for _, row := range []struct {
		typ, payload string
		want         error
	}{
		{"cue.reorder", `{"v":1,` + ok + `}`, ErrUnknownOp},
		{"cue.skip", `{"v":2,` + ok + `}`, ErrUnknownOp},
		{"cue.skip", `{}`, ErrUnknownOp},
		{"cue.skip", `{"v":1,` + ok + `} trailing`, ErrUnknownOp},
		{"cue.skip", `{"v":1,"changes":[]}`, domain.ErrEmptyOp},
	} {
		if _, err := f.s.w.Exec(`INSERT INTO action_logs (event_id, at, actor, type, payload) VALUES (?, 0, 'server', ?, ?)`,
			f.eventID, row.typ, row.payload); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.s.UndoLastCueOp(ctx, f.eventID, f.version(t), f.masterBy, at(10, 1)); !errors.Is(err, row.want) {
			t.Errorf("%s %s: err = %v, want %v", row.typ, row.payload, err, row.want)
		}
	}
	if got := f.statuses(t); got[0] != domain.InProgress {
		t.Errorf("the earlier advance was undone: %v", got)
	}
}

// The recovery code's second use: change the date while the event is a
// draft, moving every cue with it in one transaction (access-control.md
// section 2, use 2; round 5).
func TestRescheduleEvent(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.s.LookupRecoveryCode(ctx, hash("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	now := at(0, 0).AddDate(0, 0, -7)
	to := domain.Date{Year: 2027, Month: time.June, Day: 13}

	if _, err := f.s.RescheduleEvent(ctx, f.masterBy, to, now); !errors.Is(err, ErrNoActor) {
		t.Errorf("Master link: err = %v, want ErrNoActor", err)
	}
	if _, err := f.s.RescheduleEvent(ctx, code, domain.Date{Year: 2027, Month: time.June, Day: 31}, now); !errors.Is(err, domain.ErrInvalidDate) {
		t.Errorf("invalid date: err = %v, want ErrInvalidDate", err)
	}
	if _, err := f.s.RescheduleEvent(ctx, code, domain.Date{Year: 2027, Month: time.May, Day: 1}, now); !errors.Is(err, domain.ErrEnded) {
		t.Errorf("date already past: err = %v, want ErrEnded", err)
	}

	v := f.version(t)
	nv, err := f.s.RescheduleEvent(ctx, code, to, now)
	if err != nil {
		t.Fatal(err)
	}
	e, stored, _ := f.s.Event(ctx, f.eventID)
	if nv != v+1 || stored != nv || e.Date != to {
		t.Errorf("version %d→%d (stored %d), date %v", v, nv, stored, e.Date)
	}
	cues, _ := f.s.Cues(ctx, f.eventID)
	if want := time.Date(2027, 6, 13, 10, 0, 0, 0, taipei); !cues[0].ScheduledStart.Equal(want) {
		t.Errorf("cue 1 = %v, want %v", cues[0].ScheduledStart, want)
	}
	var typ string
	if err := f.s.r.QueryRow(`SELECT type FROM action_logs ORDER BY id DESC LIMIT 1`).Scan(&typ); err != nil || typ != "reschedule" {
		t.Errorf("logged %q (%v), want reschedule", typ, err)
	}

	liveAt := time.Date(2027, 6, 13, 9, 0, 0, 0, taipei)
	if _, err := f.s.StartEvent(ctx, f.eventID, f.version(t), f.masterBy, liveAt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RescheduleEvent(ctx, code, domain.Date{Year: 2027, Month: time.June, Day: 20}, liveAt); !errors.Is(err, domain.ErrNotDraft) {
		t.Errorf("live event: err = %v, want ErrNotDraft", err)
	}
}

// Rescheduling far and back keeps cues and date together (round 6).
func TestRescheduleRoundTripKeepsCues(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.s.LookupRecoveryCode(ctx, hash("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	now := at(0, 0).AddDate(0, 0, -7)
	for _, y := range []int{2199, 2100, 2027} {
		if _, err := f.s.RescheduleEvent(ctx, code, domain.Date{Year: y, Month: time.June, Day: 6}, now); err != nil {
			t.Fatalf("to %d: %v", y, err)
		}
	}
	if _, err := f.s.RescheduleEvent(ctx, code, domain.Date{Year: 2262, Month: time.June, Day: 6}, now); !errors.Is(err, domain.ErrInvalidDate) {
		t.Errorf("year 2262: err = %v, want ErrInvalidDate", err)
	}
	cues, _ := f.s.Cues(ctx, f.eventID)
	if !cues[0].ScheduledStart.Equal(at(10, 0)) {
		t.Errorf("cue 1 = %v, want %v", cues[0].ScheduledStart, at(10, 0))
	}
	if _, err := f.s.AddCue(ctx, f.eventID, domain.Cue{Seq: 9, ScheduledStart: time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)}, f.masterBy, now); !errors.Is(err, ErrInvalidCue) {
		t.Errorf("cue in 2300: err = %v, want ErrInvalidCue", err)
	}
}
