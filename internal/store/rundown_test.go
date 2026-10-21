package store

import (
	"errors"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// emptyEvent is a draft event on 2027-07-01 with no cues, its Master actor
// and two roles, 新秘 (Stylist) and 攝影A (Media).
type emptyEvent struct {
	id       int64
	masterBy Actor
	stylist  int64
	media    int64
}

func newEmptyEvent(t *testing.T, s *Store) emptyEvent {
	t.Helper()
	var e emptyEvent
	e.id, e.masterBy = otherEvent(t, s)
	var err error
	if e.stylist, err = s.AddRole(ctx, e.id, "新秘", domain.Stylist, e.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	if e.media, err = s.AddRole(ctx, e.id, "攝影A", domain.Media, e.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	return e
}

func july1(hh, mm int) time.Time { return time.Date(2027, 7, 1, hh, mm, 0, 0, taipei) }

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.r.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUS_M_0_2_ImportWritesEveryCueAndAssignmentInOneStep(t *testing.T) {
	s, _ := newStore(t)
	e := newEmptyEvent(t, s)
	_, v, _ := s.Event(ctx, e.id)
	logsBefore := countRows(t, s, `SELECT count(*) FROM action_logs WHERE event_id = ?`, e.id)
	rows := []ImportRow{
		{Line: 2, Seq: 1, Title: "新娘化妝", StartHour: 8, StartMinute: 0, DurationMin: 120, Roles: []string{"新秘"}, RoleNote: "新娘房集合"},
		{Line: 3, Seq: 3, Title: "一進預備", StartHour: 11, StartMinute: 15, DurationMin: 15, Fixed: true, Roles: []string{"新秘", "攝影A"}, RoleNote: "側門", Note: "公開備註"},
		{Line: 4, Seq: 2, Title: "迎賓", StartHour: 10, StartMinute: 30, DurationMin: 0},
	}
	if err := s.ImportRundown(ctx, e.id, rows, e.masterBy, july1(0, 0).AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	_, v2, _ := s.Event(ctx, e.id)
	if v2 != v+1 {
		t.Errorf("version %d → %d, want +1", v, v2)
	}
	if n := countRows(t, s, `SELECT count(*) FROM action_logs WHERE event_id = ?`, e.id) - logsBefore; n != 1 {
		t.Errorf("%d action log entries, want 1", n)
	}
	var typ, actor string
	if err := s.r.QueryRow(`SELECT type, actor FROM action_logs WHERE event_id = ? ORDER BY id DESC LIMIT 1`, e.id).Scan(&typ, &actor); err != nil ||
		typ != "import-rundown" || actor != e.masterBy.String() {
		t.Errorf("logged %q by %q (%v)", typ, actor, err)
	}

	cues, err := s.Cues(ctx, e.id)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 3 || cues[0].Title != "新娘化妝" || cues[1].Title != "迎賓" || cues[2].Title != "一進預備" {
		t.Fatalf("cues = %+v", cues)
	}
	if !cues[2].ScheduledStart.Equal(july1(11, 15)) || !cues[2].IsFixed || cues[2].DurationMin != 15 || cues[2].Status != domain.Pending {
		t.Errorf("一進預備 = %+v", cues[2])
	}
	if !cues[1].ScheduledStart.Equal(july1(10, 30)) || cues[1].IsFixed {
		t.Errorf("迎賓 = %+v", cues[1])
	}
	var note *string
	if err := s.r.QueryRow(`SELECT note_public FROM cues WHERE id = ?`, cues[2].ID).Scan(&note); err != nil || note == nil || *note != "公開備註" {
		t.Errorf("note_public = %v (%v)", note, err)
	}
	if err := s.r.QueryRow(`SELECT note_public FROM cues WHERE id = ?`, cues[1].ID).Scan(&note); err != nil || note != nil {
		t.Errorf("blank note stored as %v (%v), want NULL", note, err)
	}
	for _, a := range []struct {
		cue, role int64
		note      string
	}{{cues[0].ID, e.stylist, "新娘房集合"}, {cues[2].ID, e.stylist, "側門"}, {cues[2].ID, e.media, "側門"}} {
		var got string
		if err := s.r.QueryRow(`SELECT role_note FROM cue_roles WHERE cue_id = ? AND role_id = ?`, a.cue, a.role).Scan(&got); err != nil || got != a.note {
			t.Errorf("cue %d role %d: note %q (%v), want %q", a.cue, a.role, got, err, a.note)
		}
	}
	if n := countRows(t, s, `SELECT count(*) FROM cue_roles cr JOIN cues c ON c.id = cr.cue_id WHERE c.event_id = ?`, e.id); n != 3 {
		t.Errorf("%d assignments, want 3", n)
	}
}

func TestUS_M_0_2_AnyInvalidRowWritesNothingAndEveryBadRowIsListed(t *testing.T) {
	s, _ := newStore(t)
	e := newEmptyEvent(t, s)
	if _, err := s.AddRole(ctx, e.id, "雙胞胎", domain.Stylist, e.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRole(ctx, e.id, "雙胞胎", domain.Stylist, e.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	_, v, _ := s.Event(ctx, e.id)
	logs := countRows(t, s, `SELECT count(*) FROM action_logs`)
	rows := []ImportRow{
		{Line: 2, Seq: 1, Title: "ok", StartHour: 8, DurationMin: 10, Roles: []string{"新秘"}},
		{Line: 3, Seq: 1, Title: "seq repeats", StartHour: 9, DurationMin: 10},
		{Line: 4, Seq: 4, Title: "no such role", StartHour: 9, DurationMin: 10, Roles: []string{"攝影B"}},
		{Line: 5, Problems: []string{`start "9:5": must be HH:MM`}},
		{Line: 6, Seq: 6, Title: "twice", StartHour: 9, DurationMin: 10, Roles: []string{"新秘", "新秘"}},
		{Line: 7, Seq: 7, Title: "ambiguous", StartHour: 9, DurationMin: 10, Roles: []string{"雙胞胎"}},
		{Line: 8, Seq: 8, Title: "", StartHour: 24, DurationMin: -1},
		{Line: 9, Seq: 9, Title: "empty name", StartHour: 9, DurationMin: 10, Roles: []string{""}},
		{Line: 10, Seq: 10, Title: "too long", StartHour: 9, DurationMin: MaxCueDurationMin + 1},
	}
	err := s.ImportRundown(ctx, e.id, rows, e.masterBy, at(0, 0))
	var re *RundownError
	if !errors.As(err, &re) || !errors.Is(err, ErrInvalidRundown) {
		t.Fatalf("err = %v, want *RundownError", err)
	}
	lines := map[int]int{}
	for _, p := range re.Problems {
		lines[p.Line]++
	}
	for _, line := range []int{3, 4, 5, 6, 7, 8, 9, 10} {
		if lines[line] == 0 {
			t.Errorf("line %d not listed in %v", line, re.Problems)
		}
	}
	if lines[2] != 0 {
		t.Errorf("valid line 2 listed: %v", re.Problems)
	}
	if lines[8] != 3 {
		t.Errorf("line 8 has %d problems, want 3 (title, duration, start): %v", lines[8], re.Problems)
	}
	if lines[5] != 1 {
		t.Errorf("a row the caller already refused is listed %d times, want once: %v", lines[5], re.Problems)
	}
	if n := countRows(t, s, `SELECT count(*) FROM cues WHERE event_id = ?`, e.id); n != 0 {
		t.Errorf("%d cues written", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM action_logs`); n != logs {
		t.Errorf("action log grew from %d to %d", logs, n)
	}
	if _, v2, _ := s.Event(ctx, e.id); v2 != v {
		t.Errorf("version moved %d → %d", v, v2)
	}

	if err := s.ImportRundown(ctx, e.id, nil, e.masterBy, at(0, 0)); !errors.As(err, &re) {
		t.Errorf("no rows: err = %v, want *RundownError", err)
	}
}

func TestUS_M_0_2a_ImportOnlyIntoADraftWithoutCues(t *testing.T) {
	f := newFixture(t) // already has three cues
	row := []ImportRow{{Line: 2, Seq: 1, Title: "x", StartHour: 9, DurationMin: 5}}
	if err := f.s.ImportRundown(ctx, f.eventID, row, f.masterBy, at(0, 0)); !errors.Is(err, ErrRundownExists) {
		t.Errorf("event with cues: err = %v, want ErrRundownExists", err)
	}

	e := newEmptyEvent(t, f.s)
	// Live: only possible on the event day; set it directly.
	if _, err := f.s.w.Exec(`UPDATE events SET status = 'Live' WHERE id = ?`, e.id); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ImportRundown(ctx, e.id, row, e.masterBy, at(0, 0)); !errors.Is(err, domain.ErrNotDraft) {
		t.Errorf("live event: err = %v, want ErrNotDraft", err)
	}
	if _, err := f.s.w.Exec(`UPDATE events SET status = 'Draft' WHERE id = ?`, e.id); err != nil {
		t.Fatal(err)
	}
	ev, _, _ := f.s.Event(ctx, e.id)
	if err := f.s.ImportRundown(ctx, e.id, row, e.masterBy, ev.EndsAt()); !errors.Is(err, domain.ErrEnded) {
		t.Errorf("ended event: err = %v, want ErrEnded", err)
	}
	if err := f.s.ImportRundown(ctx, e.id, row, f.masterBy, at(0, 0)); !errors.Is(err, ErrCrossEvent) {
		t.Errorf("another event's Master: err = %v, want ErrCrossEvent", err)
	}
	if err := f.s.ImportRundown(ctx, e.id, row, e.masterBy, at(0, 0)); err != nil {
		t.Fatalf("valid import: %v", err)
	}
	if err := f.s.ImportRundown(ctx, e.id, row, e.masterBy, at(0, 0)); !errors.Is(err, ErrRundownExists) {
		t.Errorf("second import: err = %v, want ErrRundownExists", err)
	}
}
