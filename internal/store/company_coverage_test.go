package store

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// addB adds b@x.tw to the company and gives them the login link "sb".
func (c company) addB(t *testing.T) int64 {
	t.Helper()
	if _, _, err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.IssueLoginLink(ctx, "b@x.tw", hash("sb"), before); err != nil {
		t.Fatal(err)
	}
	return c.member(t, "b@x.tw")
}

// US-CO-1.4, 2.4 (token boundary): a member removed and added back with the
// same email holds nothing of the earlier membership: the login link, the
// console link and the login mail's link are all refused, and the new
// member logs in only with new ones.
func TestUS_CO_2_4_ReAddedMemberHoldsNoOldLink(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	b := c.addB(t)
	if err := c.f.s.OpenConsole(ctx, hash("sb"), ev, hash("cb"), before); err != nil {
		t.Fatal(err)
	}
	c.mailFor(t, b, "mb", before)
	if _, err := c.f.s.RemoveMember(ctx, hash("sa"), b, before); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatalf("add back: %v", err)
	}
	if c.member(t, "b@x.tw") == b {
		t.Error("the member number was given again")
	}
	if _, err := c.f.s.Company(ctx, hash("sb"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("old login link: err = %v, want ErrInvalidSession", err)
	}
	if err := c.f.s.Logout(ctx, hash("sb")); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("old login link's logout: err = %v, want ErrInvalidSession", err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("cb"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("old console link: err = %v, want ErrInvalidToken", err)
	}
	if err := c.f.s.ConfirmLoginMail(ctx, hash("mb"), hash("sb2"), before); !errors.Is(err, ErrInvalidLoginMail) {
		t.Errorf("old login mail link: err = %v, want ErrInvalidLoginMail", err)
	}
	if _, err := c.f.s.Company(ctx, hash("sb2"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("a refused confirm made a login link: err = %v", err)
	}
	// Only new links work for the new member.
	if err := c.f.s.IssueLoginLink(ctx, "b@x.tw", hash("sb3"), before); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.Company(ctx, hash("sb3"), before); err != nil {
		t.Errorf("new login link: %v", err)
	}
}

// US-CO-1.4: logging out, or a login link running out, leaves an already
// opened console link working.
func TestUS_CO_1_4_LogoutAndExpiryLeaveConsolesOpen(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.OpenConsole(ctx, hash("sa"), ev, hash("c1"), before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.Logout(ctx, hash("sa")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("c1"), before); err != nil {
		t.Errorf("console after logout: %v", err)
	}
	if err := c.f.s.IssueLoginLink(ctx, "a@x.tw", hash("sa2"), before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.OpenConsole(ctx, hash("sa2"), ev, hash("c2"), before); err != nil {
		t.Fatal(err)
	}
	late := before.Add(SessionLife + time.Hour)
	if _, err := c.f.s.Company(ctx, hash("sa2"), late); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("the login link did not expire: %v", err)
	}
	for _, h := range []string{"c1", "c2"} {
		if _, err := c.f.s.LookupToken(ctx, hash(h), late); err != nil {
			t.Errorf("console %s after its login link expired: %v", h, err)
		}
	}
	if _, err := c.f.s.PurgeEnded(ctx, late); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("c2"), late); err != nil {
		t.Errorf("console after the purge of its expired login link: %v", err)
	}
}

// removalRace runs attempt from many goroutines while the member b is
// removed, and returns how many attempts succeeded. Every attempt started
// after the removal had returned must have been refused with
// ErrInvalidSession.
func removalRace(t *testing.T, c company, b int64, attempt func(i int) error) (ok int) {
	t.Helper()
	var (
		wg      sync.WaitGroup
		removed atomic.Bool
		okN     atomic.Int32
	)
	start := make(chan struct{})
	for i := range 15 { // fewer than any limit of the company
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			after := removed.Load() // read before the call: the removal had returned
			err := attempt(i)
			switch {
			case err == nil && after:
				t.Errorf("attempt %d started after the removal and succeeded", i)
			case err == nil:
				okN.Add(1)
			case !errors.Is(err, ErrInvalidSession):
				t.Errorf("attempt %d: err = %v, want ErrInvalidSession", i, err)
			}
		}()
	}
	close(start)
	if _, err := c.f.s.RemoveMember(ctx, hash("sa"), b, before); err != nil {
		t.Fatal(err)
	}
	removed.Store(true)
	wg.Wait()
	if err := attempt(1000); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("after the removal: err = %v, want ErrInvalidSession", err)
	}
	return int(okN.Load())
}

// US-CO-2.4: a removed member's "add member" sent at the same moment
// leaves no member written after the removal.
func TestUS_CO_2_4_RemovalRacingAddMember(t *testing.T) {
	c := newCompany(t)
	b := c.addB(t)
	ok := removalRace(t, c, b, func(i int) error {
		_, _, err := c.f.s.AddMember(ctx, hash("sb"), fmt.Sprintf("race%d@x.tw", i), before)
		return err
	})
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM members`).Scan(&n); err != nil || n != 1+ok {
		t.Errorf("members = %d (%v), want a@x.tw and the %d added before the removal", n, err, ok)
	}
}

// US-CO-2.4: a removed member's "new event" sent at the same moment leaves
// no event written after the removal.
func TestUS_CO_2_4_RemovalRacingCreateEvent(t *testing.T) {
	c := newCompany(t)
	b := c.addB(t)
	ok := removalRace(t, c, b, func(i int) error {
		_, err := c.f.s.CreateCompanyEvent(ctx, hash("sb"), NewEvent{
			Name: "競態", Date: domain.Date{Year: 2027, Month: time.June, Day: 20}, TimeZone: "Asia/Taipei",
			MasterName: "總召", MasterTokenHash: hash(fmt.Sprintf("rm%d", i)), RecoveryCodeHash: hash(fmt.Sprintf("rr%d", i)),
		}, before)
		return err
	})
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM events WHERE organization_id = ?`, c.orgID).Scan(&n); err != nil || n != ok {
		t.Errorf("company events = %d (%v), want the %d made before the removal", n, err, ok)
	}
}

// onlyOne runs attempt from n goroutines at once and returns the errors.
func onlyOne(n int, attempt func(i int) error) []error {
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = attempt(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// wantOneWinner wants exactly one nil and every other error to be lose.
func wantOneWinner(t *testing.T, errs []error, lose error) {
	t.Helper()
	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, lose):
			t.Errorf("attempt %d: err = %v, want %v", i, err, lose)
		}
	}
	if won != 1 {
		t.Errorf("%d attempts succeeded, want exactly 1", won)
	}
}

// US-CO-2.1: the same email added at once is added once.
func TestUS_CO_2_1_ConcurrentAddOfOneEmail(t *testing.T) {
	c := newCompany(t)
	errs := onlyOne(8, func(int) error {
		_, _, err := c.f.s.AddMember(ctx, hash("sa"), "dup@x.tw", before)
		return err
	})
	wantOneWinner(t, errs, ErrEmailTaken)
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM members WHERE email = 'dup@x.tw'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d (%v), want 1", n, err)
	}
}

// US-CO-2.5: several members added at once for the 50th seat leave 50.
func TestUS_CO_2_5_ConcurrentAddOfTheLastSeat(t *testing.T) {
	c := newCompany(t)
	for i := 1; i < MembersPerOrganization-1; i++ { // 49 members
		if _, _, err := c.f.s.AddMember(ctx, hash("sa"), fmt.Sprintf("m%d@x.tw", i), before); err != nil {
			t.Fatal(err)
		}
	}
	errs := onlyOne(6, func(i int) error {
		_, _, err := c.f.s.AddMember(ctx, hash("sa"), fmt.Sprintf("last%d@x.tw", i), before)
		return err
	})
	wantOneWinner(t, errs, ErrTooManyMembers)
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM members`).Scan(&n); err != nil || n != MembersPerOrganization {
		t.Errorf("members = %d (%v), want %d", n, err, MembersPerOrganization)
	}
}

// US-CO-3.4: several events made at once as the 20th of the day leave 20.
func TestUS_CO_3_4_ConcurrentCreateOfTheLastEvent(t *testing.T) {
	c := newCompany(t)
	for i := 1; i < EventsPerOrganizationDay; i++ { // 19 events
		c.event(t, "sa", fmt.Sprintf("甲%d", i), 20)
	}
	errs := onlyOne(6, func(i int) error {
		_, err := c.f.s.CreateCompanyEvent(ctx, hash("sa"), NewEvent{
			Name: "最後", Date: domain.Date{Year: 2027, Month: time.June, Day: 20}, TimeZone: "Asia/Taipei",
			MasterName: "總召", MasterTokenHash: hash(fmt.Sprintf("lm%d", i)), RecoveryCodeHash: hash(fmt.Sprintf("lr%d", i)),
		}, before)
		return err
	})
	wantOneWinner(t, errs, ErrTooManyEvents)
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM events WHERE organization_id = ?`, c.orgID).Scan(&n); err != nil || n != EventsPerOrganizationDay {
		t.Errorf("events = %d (%v), want %d", n, err, EventsPerOrganizationDay)
	}
}

// US-O-11.2, 11.5: the same email given to two companies at once makes one
// company.
func TestUS_O_11_2_ConcurrentCompaniesOfOneEmail(t *testing.T) {
	c := newCompany(t)
	errs := onlyOne(4, func(i int) error {
		_, err := c.f.s.CreateOrganization(ctx, hash("admin"), fmt.Sprintf("公司%d", i), "same@x.tw", before)
		return err
	})
	wantOneWinner(t, errs, ErrEmailTaken)
	var orgs, members int
	if err := c.f.s.r.QueryRow(`SELECT (SELECT count(*) FROM organizations), (SELECT count(*) FROM members WHERE email = 'same@x.tw')`).
		Scan(&orgs, &members); err != nil || orgs != 2 || members != 1 {
		t.Errorf("organizations = %d, members = %d (%v), want 2 and 1", orgs, members, err)
	}
}
