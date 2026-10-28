package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// Only the newest admin link works (US-O-9.1): issuing one revokes the last.
func TestUS_O_9_1_IssueAdminLinkReplacesTheLast(t *testing.T) {
	f := newFixture(t)
	if err := f.s.CheckAdmin(ctx, hash("admin")); !errors.Is(err, ErrInvalidAdmin) {
		t.Errorf("before any link: err = %v, want ErrInvalidAdmin", err)
	}
	for _, h := range []string{"admin", "admin2"} {
		if err := f.s.IssueAdminLink(ctx, hash(h)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.CheckAdmin(ctx, hash("admin2")); err != nil {
		t.Errorf("new link: %v", err)
	}
	for _, h := range []string{"admin", "master"} { // the old link, and a role's link
		if err := f.s.CheckAdmin(ctx, hash(h)); !errors.Is(err, ErrInvalidAdmin) {
			t.Errorf("%s: err = %v, want ErrInvalidAdmin", h, err)
		}
	}
	if _, err := f.s.LookupToken(ctx, hash("admin2"), at(9, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("admin link as a role's link: err = %v, want ErrInvalidToken", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := f.s.CheckAdmin(cancelled, hash("nobody")); err == nil || errors.Is(err, ErrInvalidAdmin) {
		t.Errorf("cancelled: err = %v", err)
	}
}

// At most AdminInvitesPerDay codes in 24 hours (US-O-10.3), counted from a
// record of its own: a code used up or a new admin link frees nothing.
func TestUS_O_10_3_AdminInvitesPerDay(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueAdminLink(ctx, hash("admin")); err != nil {
		t.Fatal(err)
	}
	now := at(9, 0).AddDate(0, -1, 0)
	for i := range AdminInvitesPerDay {
		code := hash(string(rune('a' + i)))
		if _, err := f.s.CreateAdminInvite(ctx, hash("admin"), code, now); err != nil {
			t.Fatalf("invite %d: %v", i, err)
		}
		if i == 0 { // used at once: still counted
			if _, err := f.s.CreateEvent(ctx, NewEvent{Name: "x", Date: domain.Date{Year: 2027, Month: time.June, Day: 7},
				TimeZone: "Asia/Taipei", MasterName: "總召", MasterTokenHash: hash("m-x"), RecoveryCodeHash: hash("r-x"),
				InviteHash: code}, now); err != nil {
				t.Fatalf("the admin's code does not create an event: %v", err)
			}
		}
	}
	if err := f.s.IssueAdminLink(ctx, hash("admin2")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateAdminInvite(ctx, hash("admin2"), hash("over"), now.Add(23*time.Hour)); !errors.Is(err, ErrTooManyInvites) {
		t.Errorf("21st in 24 h: err = %v, want ErrTooManyInvites", err)
	}
	if _, err := f.s.CreateAdminInvite(ctx, hash("admin"), hash("old"), now.Add(25*time.Hour)); !errors.Is(err, ErrInvalidAdmin) {
		t.Errorf("revoked link: err = %v, want ErrInvalidAdmin", err)
	}
	if _, err := f.s.CreateAdminInvite(ctx, hash("admin2"), hash("next"), now.Add(25*time.Hour)); err != nil {
		t.Errorf("after 24 h: %v", err)
	}
}

func TestUS_O_10_3_ConcurrentInvitesStayUnderTheLimit(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueAdminLink(ctx, hash("admin")); err != nil {
		t.Fatal(err)
	}
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := range AdminInvitesPerDay + 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.s.CreateAdminInvite(ctx, hash("admin"), hash(string(rune('A'+i))), at(9, 0)); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != AdminInvitesPerDay {
		t.Errorf("%d invites made, want %d", ok, AdminInvitesPerDay)
	}
}

// The overview (US-O-9.2) lists the unused codes and every event, with
// Ended worked out from the date; only the admin link may read it.
func TestUS_O_9_2_AdminOverview(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueAdminLink(ctx, hash("admin")); err != nil {
		t.Fatal(err)
	}
	now := at(9, 0).AddDate(0, -1, 0)
	id, err := f.s.CreateAdminInvite(ctx, hash("admin"), hash("code"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AdminOverview(ctx, hash("master"), now); !errors.Is(err, ErrInvalidAdmin) {
		t.Errorf("role's link: err = %v, want ErrInvalidAdmin", err)
	}
	e, _, err := f.s.Event(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		now  time.Time
		want domain.EventStatus
	}{{now, domain.Draft}, {e.EndsAt(), EventEnded}} {
		o, err := f.s.AdminOverview(ctx, hash("admin"), c.now)
		if err != nil {
			t.Fatal(err)
		}
		if len(o.Events) != 1 || o.Events[0].ID != f.eventID || o.Events[0].Name != "婚宴" || o.Events[0].Status != c.want ||
			o.Events[0].Date != (domain.Date{Year: 2027, Month: time.June, Day: 6}) {
			t.Errorf("events = %+v, want %s", o.Events, c.want)
		}
		if c.now == now && (len(o.Invites) != 1 || o.Invites[0].ID != id || !o.Invites[0].ExpiresAt.Equal(now.Add(InviteLife))) {
			t.Errorf("invites = %+v", o.Invites)
		}
	}
	// An expired code is not listed, though the daily purge has not run.
	o, err := f.s.AdminOverview(ctx, hash("admin"), now.Add(InviteLife))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Invites) != 0 {
		t.Errorf("expired invites listed: %+v", o.Invites)
	}
}
