package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// samePoll checks that PollSnapshot answers exactly what Snapshot does
// for the token at now (US-O-14.3).
func samePoll(t *testing.T, s *Store, token string, now time.Time) SnapshotData {
	t.Helper()
	want, wantErr := s.Snapshot(ctx, hash(token), now)
	got, err := s.PollSnapshot(ctx, hash(token), now)
	if !reflect.DeepEqual(err, wantErr) && !errors.Is(err, wantErr) {
		t.Fatalf("%s: poll err = %v, Snapshot err = %v", token, err, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: poll\n%+v\nSnapshot\n%+v", token, got, want)
	}
	return got
}

func (s *Store) cached(id int64) bool {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	_, ok := s.cache.events[id]
	return ok
}

func (s *Store) cachedN() (int64, bool) {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	return s.cache.gen.n, s.cache.events != nil
}

func generationOf(t *testing.T, s *Store) int64 {
	t.Helper()
	var n int64
	if err := s.r.QueryRow(`SELECT n FROM data_generation`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// US-O-14.1: every table but SQLite's, Litestream's and the generation's
// own has the three triggers, exactly.
func TestUS_O_14_1_EveryTableHasItsTriggers(t *testing.T) {
	s, _ := newStore(t)
	rows, err := s.r.Query(`SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT GLOB 'sqlite_*' AND name NOT GLOB '_litestream_*' AND name != 'data_generation' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) != 19 { // the tables of migrations 0001–0009
		t.Fatalf("%d tables: %v", len(tables), tables)
	}
	for _, table := range tables {
		for op, suffix := range map[string]string{"INSERT": "ins", "UPDATE": "upd", "DELETE": "del"} {
			name := table + "_gen_" + suffix
			want := fmt.Sprintf("CREATE TRIGGER %s AFTER %s ON %s BEGIN UPDATE data_generation SET n = n + 1; END", name, op, table)
			var sql string
			err := s.r.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ? AND tbl_name = ?`, name, table).Scan(&sql)
			if err != nil || sql != want {
				t.Errorf("%s: %q (%v), want %q", name, sql, err, want)
			}
		}
	}
	var n int
	if err := s.r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger'`).Scan(&n); err != nil || n != 19*3 {
		t.Errorf("%d triggers (%v), want only these", n, err)
	}
}

// US-O-14.1: a write from anywhere raises the generation, a command on the
// host included, and the next poll sees it.
func TestUS_O_14_1_EveryWriterRaisesTheGeneration(t *testing.T) {
	f := newFixture(t)
	samePoll(t, f.s, "master", at(12, 0))
	n0 := generationOf(t, f.s)

	other, err := Open(ctx, f.path, false) // a command on the host
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.AddRole(ctx, f.eventID, "攝影", domain.Media, f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	other.Close()
	if generationOf(t, f.s) <= n0 {
		t.Fatal("another process's write did not raise the generation")
	}
	if d := samePoll(t, f.s, "master", at(12, 0)); len(d.Roles) != 3 {
		t.Errorf("roles %+v, want the new one", d.Roles)
	}

	n1 := generationOf(t, f.s)
	if _, err := f.s.w.Exec(`UPDATE cues SET title = '改了' WHERE id = ?`, f.cueIDs[0]); err != nil {
		t.Fatal(err)
	}
	if generationOf(t, f.s) != n1+1 {
		t.Error("an UPDATE of one row did not add 1")
	}
	if d := samePoll(t, f.s, "master", at(12, 0)); d.Cues[0].Title != "改了" {
		t.Errorf("title %q", d.Cues[0].Title)
	}
	if _, err := f.s.w.Exec(`DELETE FROM cues WHERE id = ?`, f.cueIDs[2]); err != nil {
		t.Fatal(err)
	}
	if d := samePoll(t, f.s, "master", at(12, 0)); len(d.Cues) != 2 {
		t.Errorf("%d cues after a DELETE", len(d.Cues))
	}
}

// US-O-14.2: while nothing changes, polls of three events all use the
// cache; a change of the generation reads again.
func TestUS_O_14_2_PollsUseTheCacheWhileNothingChanges(t *testing.T) {
	f := newFixture(t)
	ids := []int64{f.eventID}
	for _, name := range []string{"二", "三"} {
		id, err := f.s.CreateEvent(ctx, NewEvent{Name: name, Date: domain.Date{Year: 2027, Month: time.June, Day: 6},
			TimeZone: "Asia/Taipei", MasterName: "總召", MasterTokenHash: hash("m" + name), RecoveryCodeHash: hash("r" + name)}, before)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, token := range []string{"master", "m二", "m三"} {
		samePoll(t, f.s, token, at(12, 0))
	}
	for _, id := range ids {
		if !f.s.cached(id) {
			t.Errorf("event %d is not in the cache", id)
		}
	}
	// What the cache holds is what is answered: planted, it shows.
	f.s.cache.mu.Lock()
	d := f.s.cache.events[f.eventID]
	d.Cues = nil
	f.s.cache.events[f.eventID] = d
	f.s.cache.mu.Unlock()
	if d, _ := f.s.PollSnapshot(ctx, hash("master"), at(12, 0)); len(d.Cues) != 0 {
		t.Fatal("the poll did not use the cache")
	}
	if err := f.s.AssignRole(ctx, f.cueIDs[0], f.stylist, "", f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	if d := samePoll(t, f.s, "master", at(12, 0)); len(d.Cues) != 3 {
		t.Errorf("after a write: %d cues", len(d.Cues))
	}
	if f.s.cached(ids[1]) {
		t.Error("a newer generation kept an older one's event")
	}
}

// US-O-14.3: every change shows on the next poll, and the answer is
// Snapshot's, whoever filled the cache.
func TestUS_O_14_3_PollAnswersLikeSnapshot(t *testing.T) {
	f := newFixture(t)
	e, _, _ := f.s.Event(ctx, f.eventID)
	jobsRoles := map[string]domain.RoleType{"runner": domain.Runner, "reception": domain.Reception, "cashier": domain.Cashier}
	for token, typ := range jobsRoles {
		id, err := f.s.AddRole(ctx, f.eventID, token, typ, f.masterBy, at(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.s.IssueToken(ctx, id, hash(token), f.masterBy, at(1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.AddTask(ctx, f.eventID, "搬椅子", f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ReplaceGuests(ctx, f.eventID, []GuestRow{{Name: "王", Table: "1"}}, f.masterBy, before); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ReplaceChecklist(ctx, f.eventID, []string{"紅包"}, f.masterBy, before); err != nil {
		t.Fatal(err)
	}
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	all := []string{"runner", "stylist", "reception", "cashier", "master", "unknown"}
	check := func() {
		for _, token := range all {
			samePoll(t, f.s, token, at(12, 0))
		}
	}
	check() // the Runner fills the cache first; the Master still gets every list
	if d := samePoll(t, f.s, "runner", at(12, 0)); d.Guests != nil || d.Checklist != nil || len(d.Tasks) != 1 {
		t.Errorf("runner: guests %v, checklist %v, tasks %v", d.Guests, d.Checklist, d.Tasks)
	}
	if d := samePoll(t, f.s, "master", at(12, 0)); len(d.Guests) != 1 || len(d.Checklist) != 1 || len(d.Tasks) != 1 {
		t.Errorf("master: guests %v, checklist %v, tasks %v", d.Guests, d.Checklist, d.Tasks)
	}

	if err := f.s.SetCheckIn(ctx, f.eventID, f.stylist, true, f.masterBy, at(2, 0)); err != nil {
		t.Fatal(err)
	}
	check()
	if err := f.s.RevokeLink(ctx, f.eventID, f.stylist, f.masterBy, at(2, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PollSnapshot(ctx, hash("stylist"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("revoked: err = %v, want ErrInvalidToken", err)
	}
	if err := f.s.ReissueLink(ctx, f.eventID, f.stylist, hash("stylist2"), f.masterBy, at(2, 0)); err != nil {
		t.Fatal(err)
	}
	all = append(all, "stylist2")
	check()
	if _, err := f.s.PollSnapshot(ctx, hash("master"), e.EndsAt()); !errors.Is(err, ErrEventEnded) {
		t.Errorf("at the end, from the cache: err = %v, want ErrEventEnded", err)
	}
	samePoll(t, f.s, "master", e.EndsAt().Add(-time.Nanosecond))
	if err := f.s.ReplaceMasterLink(ctx, hash("recovery"), hash("master2"), at(3, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PollSnapshot(ctx, hash("master"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("replaced Master link: err = %v, want ErrInvalidToken", err)
	}
	samePoll(t, f.s, "master2", at(12, 0))
}

// US-O-14.3: a member's console link follows removal and closed member
// links on the next poll.
func TestUS_O_14_3_ConsoleLinksOnTheNextPoll(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if _, _, err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.IssueLoginLink(ctx, "b@x.tw", hash("sb"), before); err != nil {
		t.Fatal(err)
	}
	for session, console := range map[string]string{"sa": "ca", "sb": "cb"} {
		if err := c.f.s.OpenConsole(ctx, hash(session), ev, hash(console), before); err != nil {
			t.Fatal(err)
		}
	}
	samePoll(t, c.f.s, "cb", before)
	samePoll(t, c.f.s, "m-甲婚宴", before)
	if _, err := c.f.s.RemoveMember(ctx, hash("sa"), c.member(t, "b@x.tw"), before); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.PollSnapshot(ctx, hash("cb"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("removed member's console: err = %v, want ErrInvalidToken", err)
	}
	samePoll(t, c.f.s, "ca", before)
	c.f.s.CloseMemberAccess()
	if _, err := c.f.s.PollSnapshot(ctx, hash("ca"), before); !errors.Is(err, ErrMembersClosed) {
		t.Errorf("closed: err = %v, want ErrMembersClosed", err)
	}
	samePoll(t, c.f.s, "m-甲婚宴", before)
}

// US-O-14.3: roles polling one event at once each get Snapshot's answer
// (run with -race: the shared data is only read).
func TestUS_O_14_3_ConcurrentPollsOfOneEvent(t *testing.T) {
	f := newFixture(t)
	tokens := []string{"master", "stylist", "runner", "reception"}
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []domain.RoleType{domain.Runner, domain.Reception} {
		id, _ := f.s.AddRole(ctx, f.eventID, string(typ), typ, f.masterBy, at(1, 0))
		if err := f.s.IssueToken(ctx, id, hash(map[domain.RoleType]string{domain.Runner: "runner", domain.Reception: "reception"}[typ]), f.masterBy, at(1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.ReplaceGuests(ctx, f.eventID, []GuestRow{{Name: "王", Table: "1"}}, f.masterBy, before); err != nil {
		t.Fatal(err)
	}
	want := map[string]SnapshotData{}
	for _, token := range tokens {
		d, err := f.s.Snapshot(ctx, hash(token), at(12, 0))
		if err != nil {
			t.Fatal(err)
		}
		want[token] = d
	}
	var wg sync.WaitGroup
	for i := range 40 {
		token := tokens[i%len(tokens)]
		wg.Go(func() {
			for range 20 {
				got, err := f.s.PollSnapshot(ctx, hash(token), at(12, 0))
				if err != nil || !reflect.DeepEqual(got, want[token]) {
					t.Errorf("%s: %v", token, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

// US-O-14.4: without a readable generation the poll reads everything and
// keeps nothing; a cancelled read is never a missing link and is not kept.
func TestUS_O_14_4_NoGenerationReadsDirectly(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.w.Exec(`DELETE FROM data_generation`); err != nil {
		t.Fatal(err)
	}
	samePoll(t, f.s, "master", at(12, 0))
	if _, set := f.s.cachedN(); set {
		t.Error("kept data without a generation")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.s.PollSnapshot(cancelled, hash("master"), at(12, 0)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v, want context.Canceled", err)
	}
}

// A cancellation that comes after the transaction began fails the
// generation's read and is still the context's error, never a missing link.
func TestUS_O_14_4_CancelledAfterBeginIsNotInvalid(t *testing.T) {
	f := newFixture(t)
	read, cancel := context.WithCancel(ctx)
	afterPollBegin = cancel
	t.Cleanup(func() { afterPollBegin = nil })
	if _, err := f.s.PollSnapshot(read, hash("master"), at(12, 0)); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// A read that fails partway is an error, never a missing link, and is not
// kept.
func TestUS_O_14_4_FailedReadIsNotKept(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.w.Exec(`DROP TABLE cue_acks`); err != nil { // loadAcks fails
		t.Fatal(err)
	}
	_, err := f.s.PollSnapshot(ctx, hash("master"), at(12, 0))
	if err == nil || errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrEventEnded) {
		t.Errorf("err = %v, want the database's error", err)
	}
	if _, set := f.s.cachedN(); set {
		t.Error("a failed read was kept")
	}
}

func TestUS_O_14_4_CancelledReadIsNotKept(t *testing.T) {
	f := newFixture(t)
	read, cancel := context.WithCancel(ctx)
	beforeCacheStore = cancel
	t.Cleanup(func() { beforeCacheStore = nil })
	f.s.PollSnapshot(read, hash("master"), at(12, 0))
	if _, set := f.s.cachedN(); set {
		t.Error("a cancelled read was kept")
	}
}

// US-O-14.5: a read that got the generation before a write and stores
// after it overwrites nothing newer, and every later poll sees the write.
func TestUS_O_14_5_StaleReadDoesNotHideAWrite(t *testing.T) {
	for _, newerStored := range []bool{true, false} {
		t.Run(fmt.Sprint("newer stored ", newerStored), func(t *testing.T) {
			f := newFixture(t)
			if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
				t.Fatal(err)
			}
			paused, release := make(chan struct{}), make(chan struct{})
			var first atomic.Bool
			beforeCacheStore = func() {
				if first.CompareAndSwap(false, true) {
					close(paused)
					<-release
				}
			}
			t.Cleanup(func() { beforeCacheStore = nil })
			done := make(chan struct{})
			go func() { f.s.PollSnapshot(ctx, hash("master"), at(12, 0)); close(done) }()
			<-paused
			n0 := generationOf(t, f.s)
			if err := f.s.SetCheckIn(ctx, f.eventID, f.stylist, true, f.masterBy, at(2, 0)); err != nil {
				t.Fatal(err)
			}
			if newerStored {
				samePoll(t, f.s, "master", at(12, 0))
			}
			close(release)
			<-done
			if n, _ := f.s.cachedN(); newerStored && n == n0 {
				t.Error("the stale read replaced the newer data")
			}
			d := samePoll(t, f.s, "master", at(12, 0))
			if !d.Roles[1].CheckedIn {
				t.Errorf("the check-in does not show: %+v", d.Roles)
			}
		})
	}
}

// US-O-14.6: an ended event is neither read nor kept, and the purge
// empties the cache.
func TestUS_O_14_6_EndedEventsAndThePurge(t *testing.T) {
	f := newFixture(t)
	e, _, _ := f.s.Event(ctx, f.eventID)
	if _, err := f.s.PollSnapshot(ctx, hash("master"), e.EndsAt()); !errors.Is(err, ErrEventEnded) {
		t.Fatalf("err = %v", err)
	}
	if f.s.cached(f.eventID) {
		t.Error("an ended event was kept")
	}
	samePoll(t, f.s, "master", at(12, 0))
	if !f.s.cached(f.eventID) {
		t.Fatal("not kept while Live")
	}
	if _, err := f.s.PurgeEnded(ctx, at(12, 0)); err != nil { // deletes nothing
		t.Fatal(err)
	}
	if _, set := f.s.cachedN(); set {
		t.Error("the purge did not empty the cache")
	}
}

// US-O-14.7: Snapshot, used by writes and previews, reads the database
// and leaves the cache alone: it neither fills it nor reads from it.
func TestUS_O_14_7_SnapshotDoesNotUseTheCache(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.Snapshot(ctx, hash("master"), at(12, 0)); err != nil {
		t.Fatal(err)
	}
	if _, set := f.s.cachedN(); set {
		t.Error("Snapshot filled the cache")
	}
	samePoll(t, f.s, "master", at(12, 0))
	f.s.cache.mu.Lock()
	d := f.s.cache.events[f.eventID]
	d.Cues = nil
	f.s.cache.events[f.eventID] = d
	f.s.cache.mu.Unlock()
	if d, err := f.s.Snapshot(ctx, hash("master"), at(12, 0)); err != nil || len(d.Cues) != 3 {
		t.Errorf("Snapshot read the cache: %d cues (%v)", len(d.Cues), err)
	}
}

// US-O-14.8: the change notices' index.
func TestUS_O_14_8_ActionLogIndex(t *testing.T) {
	s, _ := newStore(t)
	var sql string
	err := s.r.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'action_logs_event_type'`).Scan(&sql)
	if err != nil || sql != "CREATE INDEX action_logs_event_type ON action_logs (event_id, type, id)" {
		t.Errorf("%q (%v)", sql, err)
	}
}

// The cache's own rules: merge one generation, replace with a newer one,
// drop an older one.
func TestUS_O_14_2_CacheRules(t *testing.T) {
	var c snapshotCache
	g := func(schema, n int64) generation { return generation{schema, n} }
	c.put(g(1, 5), 1, SnapshotData{Version: 1})
	c.put(g(1, 5), 2, SnapshotData{Version: 2})
	if _, ok := c.get(g(1, 5), 2); !ok || len(c.events) != 2 {
		t.Error("one generation did not merge")
	}
	c.put(g(1, 4), 3, SnapshotData{})
	if _, ok := c.get(g(1, 4), 3); ok || len(c.events) != 2 {
		t.Error("an older generation was stored")
	}
	c.put(g(1, 6), 3, SnapshotData{})
	if _, ok := c.get(g(1, 5), 1); ok || len(c.events) != 1 {
		t.Error("a newer generation kept the older events")
	}
	c.put(g(2, 1), 4, SnapshotData{})
	if _, ok := c.get(g(2, 1), 4); !ok || len(c.events) != 1 {
		t.Error("another schema did not replace")
	}
	if _, ok := c.get(g(2, 2), 4); ok {
		t.Error("a different generation was used")
	}
	if _, ok := c.get(g(1, 1), 4); ok {
		t.Error("another schema with the same n was used")
	}
	c.clear()
	if _, ok := c.get(g(2, 1), 4); ok || c.events != nil {
		t.Error("clear kept something")
	}
}
