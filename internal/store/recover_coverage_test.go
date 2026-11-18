package store

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
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
