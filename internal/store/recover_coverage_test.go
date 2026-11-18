package store

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// US-O-3.3: two replacements at the same moment with the same code leave
// exactly one working Master link; the other new link and the old one
// stop working. Repeated, so a lost race shows.
func TestUS_O_3_3_ConcurrentReplacementsLeaveOneMasterLink(t *testing.T) {
	for round := range 20 {
		f := newFixture(t)
		const n = 6
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = f.s.ReplaceMasterLink(ctx, hash("recovery"), hash(fmt.Sprintf("new-%d", i)), at(9, 0))
			}()
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d request %d: %v", round, i, err)
			}
		}
		var active int
		if err := f.s.r.QueryRow(`SELECT count(*) FROM access_tokens WHERE role_id = ? AND revoked_at IS NULL`, f.master).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active != 1 {
			t.Fatalf("round %d: %d active Master links, want exactly 1", round, active)
		}
		working := 0
		for i := range n {
			if _, err := f.s.LookupToken(ctx, hash(fmt.Sprintf("new-%d", i)), at(12, 0)); err == nil {
				working++
			} else if !errors.Is(err, ErrInvalidToken) {
				t.Fatal(err)
			}
		}
		if working != 1 {
			t.Fatalf("round %d: %d of the new links work, want exactly 1", round, working)
		}
		if _, err := f.s.LookupToken(ctx, hash("master"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("round %d: the old Master link still works: %v", round, err)
		}
	}
}

// US-O-1.7: a database created by Open is readable and writable by its
// owner only (0600), as are the files SQLite makes next to it.
func TestUS_O_1_7_NewDatabaseFileIs0600(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.w.Exec(`UPDATE events SET name = 'x'`); err != nil { // makes the -wal file
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(f.path + suffix)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s%s mode = %o, want 600", "bethere.db", suffix, perm)
		}
	}
}

// US-O-10.3: the day's limit counts the codes made in the 24 hours before
// now, the 24th hour included: 20 codes made at t are still counted at
// t+24h-1ns and no longer at t+24h.
func TestUS_O_10_3_LimitBoundaryAtThe24thHour(t *testing.T) {
	for _, tc := range []struct {
		name  string
		later time.Duration
		want  error
	}{
		{"1ns inside 24h", 24*time.Hour - time.Nanosecond, ErrTooManyInvites},
		{"exactly 24h", 24 * time.Hour, nil},
		{"1ns past 24h", 24*time.Hour + time.Nanosecond, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if err := f.s.IssueAdminLink(ctx, hash("admin")); err != nil {
				t.Fatal(err)
			}
			now := at(9, 0).AddDate(0, -1, 0)
			for i := range AdminInvitesPerDay {
				if _, err := f.s.CreateAdminInvite(ctx, hash("admin"), hash(fmt.Sprintf("c%d", i)), now); err != nil {
					t.Fatalf("code %d: %v", i, err)
				}
			}
			_, err := f.s.CreateAdminInvite(ctx, hash("admin"), hash("next"), now.Add(tc.later))
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// scribble changes everything in d it can reach: elements, slices, maps and
// the values behind pointers.
func scribble(d *SnapshotData) {
	for i := range d.Cues {
		d.Cues[i].Title = "CHANGED"
		if d.Cues[i].NotePublic != nil {
			*d.Cues[i].NotePublic = "CHANGED"
		}
		for j := range d.Cues[i].Roles {
			d.Cues[i].Roles[j].RoleName = "CHANGED"
			if d.Cues[i].Roles[j].RoleNote != nil {
				*d.Cues[i].Roles[j].RoleNote = "CHANGED"
			}
		}
	}
	for i := range d.Roles {
		d.Roles[i].Name = "CHANGED"
	}
	if d.LastOp != nil {
		d.LastOp.Changes[0].After.Title = "CHANGED"
		if d.LastOp.Shift != nil {
			d.LastOp.Shift.DeltaMin = 99
			d.LastOp.Shift.Excluded = append(d.LastOp.Shift.Excluded[:0], 4242)
		}
	}
	for i := range d.Shifts {
		d.Shifts[i].Changes[0].After.Title = "CHANGED"
	}
	for i := range d.Assigns {
		d.Assigns[i].Added[0] = 4242
	}
	for cue, byRole := range d.Acks {
		for role := range byRole {
			byRole[role] = time.Time{}
		}
		delete(d.Acks, cue)
	}
	d.Acks[999] = map[int64]time.Time{1: time.Now()}
	for i := range d.Tasks {
		d.Tasks[i].Title = "CHANGED"
	}
	for i := range d.Guests {
		d.Guests[i].Name = "CHANGED"
		if d.Guests[i].Note != nil {
			*d.Guests[i].Note = "CHANGED"
		}
	}
	for i := range d.Checklist {
		d.Checklist[i].Title = "CHANGED"
	}
	d.Cues = d.Cues[:1]
	d.Roles = d.Roles[:0]
}

// US-O-14: what a caller does to the data a poll returned, or to what it
// gave the cache, never reaches the cache: the next poll still answers what
// the database holds, in every list the data has.
func TestUS_O_14_CallerChangesNeverReachTheCache(t *testing.T) {
	f := newFixture(t)
	f.start(t)
	note := "private"
	if err := f.s.AssignRole(ctx, f.cueIDs[0], f.stylist, note, f.masterBy, at(9, 30)); err != nil {
		t.Fatal(err)
	}
	cues, err := f.s.Cues(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := domain.PlanShift(cues, domain.ShiftRequest{FromCueID: f.cueIDs[1], DeltaMin: 10, Excluded: []int64{f.cueIDs[2]}}, at(9, 40))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.ApplyCueOp(ctx, f.eventID, f.version(t), f.masterBy, at(9, 40),
		func([]domain.Cue) (domain.Op, error) { return plan.Op, nil }); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO cue_acks (cue_id, role_id, at) VALUES (%[1]d, %[2]d, 1)`,
		`INSERT INTO tasks (event_id, title, created_at, updated_at) VALUES (%[3]d, 'task', 1, 1)`,
		`INSERT INTO guests (event_id, seq, name, table_label, note) VALUES (%[3]d, 1, 'g', 'A', 'n')`,
		`INSERT INTO checklist_items (event_id, seq, title) VALUES (%[3]d, 1, 'item')`,
		`INSERT INTO action_logs (event_id, at, actor, type, payload) VALUES (%[3]d, 5, 'role:1', 'rundown.assign', '{"cueId":%[1]d,"added":[%[2]d]}')`,
	} {
		if _, err := f.s.w.Exec(fmt.Sprintf(q, f.cueIDs[0], f.stylist, f.eventID)); err != nil {
			t.Fatal(err)
		}
	}

	want := samePoll(t, f.s, "master", at(12, 0)) // fills the cache; equals Snapshot
	if !f.s.cached(f.eventID) {
		t.Fatal("test setup: the poll did not fill the cache")
	}
	if d := want; d.LastOp == nil || d.LastOp.Shift == nil || len(d.Shifts) == 0 || len(d.Assigns) == 0 || len(d.Acks) == 0 ||
		len(d.Tasks) == 0 || len(d.Guests) == 0 || len(d.Checklist) == 0 || d.Cues[0].Roles == nil {
		t.Fatalf("test setup: a list is empty: %+v", d)
	}

	// The caller of the poll that filled the cache changes its data ...
	scribble(&want)
	samePoll(t, f.s, "master", at(12, 0))
	// ... and so does the caller of a poll answered from the cache.
	hit, err := f.s.PollSnapshot(ctx, hash("master"), at(12, 0))
	if err != nil {
		t.Fatal(err)
	}
	scribble(&hit)
	samePoll(t, f.s, "master", at(12, 0))
}
