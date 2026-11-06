package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// before is a month before the fixture's wedding day.
var before = at(9, 0).AddDate(0, -1, 0)

// company is a company of the fixture's store with one member logged in.
type company struct {
	f     fixture
	orgID int64
}

// newCompany makes a company "甲" whose first member a@x.tw holds the login
// link "sa".
func newCompany(t *testing.T) company {
	t.Helper()
	f := newFixture(t)
	if err := f.s.IssueAdminLink(ctx, hash("admin")); err != nil {
		t.Fatal(err)
	}
	id, err := f.s.CreateOrganization(ctx, hash("admin"), "甲", "a@x.tw", before)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Login(ctx, "a@x.tw", hash("sa"), before); err != nil {
		t.Fatal(err)
	}
	return company{f: f, orgID: id}
}

func (c company) event(t *testing.T, session, name string, day int) int64 {
	t.Helper()
	id, err := c.f.s.CreateCompanyEvent(ctx, hash(session), NewEvent{
		Name: name, Date: domain.Date{Year: 2027, Month: time.June, Day: day}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: hash("m-" + name), RecoveryCodeHash: hash("r-" + name),
	}, before)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (c company) member(t *testing.T, email string) int64 {
	t.Helper()
	got, err := c.f.s.Company(ctx, hash("sa"), before)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Members {
		if m.Email == email {
			return m.ID
		}
	}
	t.Fatalf("no member %s", email)
	return 0
}

func TestUS_O_11_1_CreateOrganizationWithItsFirstMember(t *testing.T) {
	c := newCompany(t)
	got, err := c.f.s.Company(ctx, hash("sa"), before)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "甲" || len(got.Members) != 1 || got.Members[0].Email != "a@x.tw" || got.Me != got.Members[0].ID {
		t.Errorf("company = %+v", got)
	}
	if _, err := c.f.s.CreateOrganization(ctx, hash("nobody"), "乙", "b@x.tw", before); !errors.Is(err, ErrInvalidAdmin) {
		t.Errorf("without the admin link: err = %v, want ErrInvalidAdmin", err)
	}
	if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "乙", "a@x.tw", before); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("an email of another company: err = %v, want ErrEmailTaken", err)
	}
	if o, err := c.f.s.AdminOverview(ctx, hash("admin"), before); err != nil || len(o.Organizations) != 1 {
		t.Fatalf("overview = %+v, %v; the refused companies were written", o.Organizations, err)
	}
}

// At most OrganizationsPerDay in 24 hours, counted with a lower bound only
// (US-O-11.5).
func TestUS_O_11_5_OrganizationsPerDay(t *testing.T) {
	c := newCompany(t) // the first of the day
	for i := 1; i < OrganizationsPerDay; i++ {
		if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "公司", fmt.Sprintf("o%d@x.tw", i), before.Add(time.Hour)); err != nil {
			t.Fatalf("company %d: %v", i, err)
		}
	}
	if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "公司", "over@x.tw", before.Add(24*time.Hour-time.Second)); !errors.Is(err, ErrTooManyOrganizations) {
		t.Errorf("6th within 24 h: err = %v, want ErrTooManyOrganizations", err)
	}
	// A clock set back counts the "future" companies too.
	if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "公司", "back@x.tw", before.Add(-time.Hour)); !errors.Is(err, ErrTooManyOrganizations) {
		t.Errorf("clock set back: err = %v, want ErrTooManyOrganizations", err)
	}
	if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "公司", "next@x.tw", before.Add(24*time.Hour+time.Second)); err != nil {
		t.Errorf("a day after the first: %v", err)
	}
}

// The overview lists each company with its counts and no email; an event
// names its company (US-O-11.3).
func TestUS_O_11_3_OverviewListsCompanies(t *testing.T) {
	c := newCompany(t)
	c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	o, err := c.f.s.AdminOverview(ctx, hash("admin"), before)
	if err != nil {
		t.Fatal(err)
	}
	want := AdminOrganization{ID: c.orgID, Name: "甲", Members: 2, Events: 1, CreatedAt: before.UTC()}
	if len(o.Organizations) != 1 || o.Organizations[0] != want {
		t.Errorf("organizations = %+v, want [%+v]", o.Organizations, want)
	}
	byName := map[string]string{}
	for _, e := range o.Events {
		byName[e.Name] = e.Organization
	}
	if byName["甲婚宴"] != "甲" || byName["婚宴"] != "" {
		t.Errorf("event companies = %v", byName)
	}
	if s := fmt.Sprintf("%+v", o); strings.Contains(s, "@") {
		t.Errorf("the overview carries an email: %s", s)
	}
}

func TestUS_CO_1_2_LoginMakesALinkForAMember(t *testing.T) {
	c := newCompany(t)
	if err := c.f.s.Login(ctx, "nobody@x.tw", hash("sn"), before); !errors.Is(err, ErrNotMember) {
		t.Errorf("not a member: err = %v, want ErrNotMember", err)
	}
	if _, err := c.f.s.Company(ctx, hash("sn"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("a refused login made a link: err = %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.f.s.Login(cancelled, "nobody@x.tw", hash("sc"), before); err == nil || errors.Is(err, ErrNotMember) {
		t.Errorf("cancelled: err = %v", err)
	}
}

// A login link lasts SessionLife (US-CO-1.4); expired ones go with the
// daily purge.
func TestUS_CO_1_4_LoginLinkExpires(t *testing.T) {
	c := newCompany(t)
	if _, err := c.f.s.Company(ctx, hash("sa"), before.Add(SessionLife-time.Second)); err != nil {
		t.Errorf("just before the end: %v", err)
	}
	if _, err := c.f.s.Company(ctx, hash("sa"), before.Add(SessionLife)); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("at the end: err = %v, want ErrInvalidSession", err)
	}
	if _, err := c.f.s.PurgeEnded(ctx, before.Add(SessionLife)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM member_sessions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("sessions after the purge = %d, %v", n, err)
	}
}

// Logging out ends this link only (US-CO-1.4).
func TestUS_CO_1_4_LogoutEndsThisLinkOnly(t *testing.T) {
	c := newCompany(t)
	if err := c.f.s.Login(ctx, "a@x.tw", hash("sa2"), before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.Logout(ctx, hash("sa")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.Company(ctx, hash("sa"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("logged out: err = %v, want ErrInvalidSession", err)
	}
	if _, err := c.f.s.Company(ctx, hash("sa2"), before); err != nil {
		t.Errorf("the other device: %v", err)
	}
	if err := c.f.s.Logout(ctx, hash("sa")); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("twice: err = %v, want ErrInvalidSession", err)
	}
}

// A member keeps at most SessionsPerMember links; the oldest goes
// (US-CO-1.8).
func TestUS_CO_1_8_SessionsPerMember(t *testing.T) {
	c := newCompany(t) // "sa" is the first
	for i := 1; i <= SessionsPerMember; i++ {
		if err := c.f.s.Login(ctx, "a@x.tw", hash(fmt.Sprintf("s%d", i)), before); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.f.s.Company(ctx, hash("sa"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("the oldest after %d more: err = %v, want ErrInvalidSession", SessionsPerMember, err)
	}
	if _, err := c.f.s.Company(ctx, hash("s1"), before); err != nil {
		t.Errorf("the 20th newest: %v", err)
	}
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM member_sessions`).Scan(&n); err != nil || n != SessionsPerMember {
		t.Errorf("sessions = %d, %v, want %d", n, err, SessionsPerMember)
	}
}

func TestUS_CO_2_1_AddMember(t *testing.T) {
	c := newCompany(t)
	if err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.Login(ctx, "b@x.tw", hash("sb"), before); err != nil {
		t.Errorf("the new member logs in: %v", err)
	}
	if err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("twice: err = %v, want ErrEmailTaken", err)
	}
	if err := c.f.s.AddMember(ctx, hash("nobody"), "c@x.tw", before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("without a login link: err = %v, want ErrInvalidSession", err)
	}
}

// At most MembersPerOrganization (US-CO-2.5).
func TestUS_CO_2_5_MembersPerOrganization(t *testing.T) {
	c := newCompany(t)
	for i := 1; i < MembersPerOrganization; i++ {
		if err := c.f.s.AddMember(ctx, hash("sa"), fmt.Sprintf("m%d@x.tw", i), before); err != nil {
			t.Fatalf("member %d: %v", i, err)
		}
	}
	if err := c.f.s.AddMember(ctx, hash("sa"), "over@x.tw", before); !errors.Is(err, ErrTooManyMembers) {
		t.Errorf("51st: err = %v, want ErrTooManyMembers", err)
	}
	if err := c.f.s.AddMemberTo(ctx, c.orgID, "over@x.tw", before); !errors.Is(err, ErrTooManyMembers) {
		t.Errorf("51st by the command: err = %v, want ErrTooManyMembers", err)
	}
}

// Removing a member ends their login and console links (US-CO-2.3, 2.4).
func TestUS_CO_2_4_RemoveMemberEndsTheirLinks(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.Login(ctx, "b@x.tw", hash("sb"), before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.OpenConsole(ctx, hash("sb"), ev, hash("cb"), before); err != nil {
		t.Fatal(err)
	}
	b := c.member(t, "b@x.tw")
	if err := c.f.s.RemoveMember(ctx, hash("sa"), c.member(t, "a@x.tw"), before); !errors.Is(err, ErrCannotRemoveSelf) {
		t.Errorf("self: err = %v, want ErrCannotRemoveSelf", err)
	}
	if err := c.f.s.RemoveMember(ctx, hash("sa"), b, before); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.Company(ctx, hash("sb"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("their login link: err = %v, want ErrInvalidSession", err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("cb"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("their console link: err = %v, want ErrInvalidToken", err)
	}
	if err := c.f.s.RemoveMember(ctx, hash("sa"), b, before); !errors.Is(err, ErrNotFound) {
		t.Errorf("gone already: err = %v, want ErrNotFound", err)
	}
}

// A member of another company is not found (US-CO-3.5).
func TestUS_CO_2_3_RemoveOnlyOwnCompanysMembers(t *testing.T) {
	c := newCompany(t)
	if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "乙", "z@x.tw", before); err != nil {
		t.Fatal(err)
	}
	z, _, err := c.f.s.FindMember(ctx, "z@x.tw")
	if err != nil {
		t.Fatal(err)
	}
	var zID int64
	if err := c.f.s.r.QueryRow(`SELECT id FROM members WHERE email = 'z@x.tw'`).Scan(&zID); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.RemoveMember(ctx, hash("sa"), zID, before); !errors.Is(err, ErrNotFound) {
		t.Errorf("another company's member: err = %v, want ErrNotFound", err)
	}
	if id, _, err := c.f.s.FindMember(ctx, "z@x.tw"); err != nil || id != z {
		t.Errorf("the other member was removed: %d, %v", id, err)
	}
}

// Two members removing each other at once leave one (US-CO-2.3).
func TestUS_CO_2_3_ConcurrentMutualRemovalKeepsOne(t *testing.T) {
	c := newCompany(t)
	if err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.Login(ctx, "b@x.tw", hash("sb"), before); err != nil {
		t.Fatal(err)
	}
	a, b := c.member(t, "a@x.tw"), c.member(t, "b@x.tw")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, r := range []struct {
		session string
		target  int64
	}{{"sa", b}, {"sb", a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.f.s.RemoveMember(ctx, hash(r.session), r.target, before)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrInvalidSession):
			t.Errorf("the loser: err = %v, want ErrInvalidSession", err)
		}
	}
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM members`).Scan(&n); err != nil || ok != 1 || n != 1 {
		t.Errorf("succeeded = %d, members left = %d (%v), want 1 and 1", ok, n, err)
	}
}

// A removed member's concurrent console opening leaves no link behind
// (US-CO-2.4).
func TestUS_CO_2_4_RemovalRacingAConsoleLeavesNoLink(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.Login(ctx, "b@x.tw", hash("sb"), before); err != nil {
		t.Fatal(err)
	}
	b := c.member(t, "b@x.tw")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.f.s.OpenConsole(ctx, hash("sb"), ev, hash(fmt.Sprintf("race%d", i)), before)
		}()
	}
	if err := c.f.s.RemoveMember(ctx, hash("sa"), b, before); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM console_tokens`).Scan(&n); err != nil || n != 0 {
		t.Errorf("console links of a removed member = %d, %v", n, err)
	}
}

// The company page lists its events: the coming ones by date from the
// nearest, then the ended ones from the latest (US-CO-3.1).
func TestUS_CO_3_1_CompanyEventsInOrder(t *testing.T) {
	c := newCompany(t)
	c.event(t, "sa", "七日", 7)
	c.event(t, "sa", "三日", 3)
	c.event(t, "sa", "九日", 9)
	c.event(t, "sa", "一日", 1)
	june5 := at(10, 0).AddDate(0, 0, -1) // the 1st and 3rd ended
	if err := c.f.s.Login(ctx, "a@x.tw", hash("june5"), june5); err != nil {
		t.Fatal(err)
	}
	got, err := c.f.s.Company(ctx, hash("june5"), june5)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got.Events {
		names = append(names, e.Name+":"+string(e.Status))
	}
	want := "七日:Draft 九日:Draft 三日:Ended 一日:Ended"
	if strings.Join(names, " ") != want {
		t.Errorf("events = %v, want %s", names, want)
	}
}

// An event made on the company page belongs to that company only
// (US-CO-3.2, 3.5); the fixture's own event belongs to none.
func TestUS_CO_3_5_EventsBelongToTheirCompany(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "乙", "z@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.Login(ctx, "z@x.tw", hash("sz"), before); err != nil {
		t.Fatal(err)
	}
	other, err := c.f.s.Company(ctx, hash("sz"), before)
	if err != nil || len(other.Events) != 0 {
		t.Errorf("another company sees %+v, %v", other.Events, err)
	}
	for name, id := range map[string]int64{"another company's": ev, "no company's": c.f.eventID, "no such": 999} {
		if err := c.f.s.OpenConsole(ctx, hash("sz"), id, hash("cz-"+name), before); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s event: err = %v, want ErrNotFound", name, err)
		}
	}
	mine, err := c.f.s.Company(ctx, hash("sa"), before)
	if err != nil || len(mine.Events) != 1 || mine.Events[0].ID != ev {
		t.Errorf("own company = %+v, %v; the fixture's event belongs to none", mine.Events, err)
	}
	if o, err := c.f.s.LookupToken(ctx, hash("m-甲婚宴"), before); err != nil || o.RoleType != domain.Master {
		t.Errorf("the new event's Master link: %+v, %v", o, err)
	}
}

// At most EventsPerOrganizationDay in 24 hours, lower bound only (US-CO-3.4).
func TestUS_CO_3_4_EventsPerOrganizationDay(t *testing.T) {
	c := newCompany(t)
	mk := func(i int, now time.Time) error {
		_, err := c.f.s.CreateCompanyEvent(ctx, hash("sa"), NewEvent{
			Name: "x", Date: domain.Date{Year: 2027, Month: time.June, Day: 7}, TimeZone: "Asia/Taipei", MasterName: "總召",
			MasterTokenHash: hash(fmt.Sprintf("m%d", i)), RecoveryCodeHash: hash(fmt.Sprintf("r%d", i)),
		}, now)
		return err
	}
	for i := range EventsPerOrganizationDay {
		if err := mk(i, before); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
	if err := mk(100, before.Add(24*time.Hour-time.Second)); !errors.Is(err, ErrTooManyEvents) {
		t.Errorf("21st within 24 h: err = %v, want ErrTooManyEvents", err)
	}
	if err := mk(101, before.Add(-time.Hour)); !errors.Is(err, ErrTooManyEvents) {
		t.Errorf("clock set back: err = %v, want ErrTooManyEvents", err)
	}
	if err := mk(102, before.Add(24*time.Hour)); err != nil {
		t.Errorf("a day later: %v", err)
	}
}

// A console link is the event's Master, acting as the member (US-CO-4.1,
// 4.3): not a role's link, so no lastSeenAt key and no byTokenId.
func TestUS_CO_4_1_ConsoleLinkActsAsTheMaster(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.OpenConsole(ctx, hash("sa"), ev, hash("ca"), before); err != nil {
		t.Fatal(err)
	}
	o, err := c.f.s.LookupToken(ctx, hash("ca"), before)
	if err != nil {
		t.Fatal(err)
	}
	master, err := c.f.s.LookupToken(ctx, hash("m-甲婚宴"), before)
	if err != nil {
		t.Fatal(err)
	}
	if o.TokenID != 0 || o.RoleID != master.RoleID || o.EventID != ev || o.RoleType != domain.Master ||
		o.View != domain.Console || o.ChecksIn || o.RoleName != "總召" {
		t.Errorf("owner = %+v", o)
	}
	a := c.member(t, "a@x.tw")
	var consoleID int64
	if err := c.f.s.r.QueryRow(`SELECT id FROM console_tokens`).Scan(&consoleID); err != nil {
		t.Fatal(err)
	}
	if got, want := o.Actor.String(), fmt.Sprintf("member:%d/console:%d", a, consoleID); got != want {
		t.Errorf("actor = %q, want %q", got, want)
	}
	if _, err := c.f.s.AddRole(ctx, ev, "攝影", domain.Media, o.Actor, before); err != nil {
		t.Errorf("a Master write with the console link: %v", err)
	}
	d, err := c.f.s.Snapshot(ctx, hash("ca"), before)
	if err != nil || d.Owner.RoleType != domain.Master || len(d.Roles) != 2 {
		t.Errorf("snapshot = %+v, %v", d.Owner, err)
	}
	if _, err := c.f.s.LookupRoleToken(ctx, hash("ca"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("the commands' lookup: err = %v, want ErrInvalidToken", err)
	}
	if _, err := c.f.s.LookupRoleToken(ctx, hash("m-甲婚宴"), before); err != nil {
		t.Errorf("the commands' lookup of the Master link: %v", err)
	}
	// Never where only the recovery code may act, and never a recovery code reset.
	if err := c.f.s.ResetRecoveryCode(ctx, o.Actor, hash("r-new"), before); !errors.Is(err, ErrNoActor) {
		t.Errorf("reset the code with a console link: err = %v, want ErrNoActor", err)
	}
	if _, err := c.f.s.RescheduleEvent(ctx, o.Actor, domain.Date{Year: 2027, Month: time.June, Day: 8}, before); !errors.Is(err, ErrNoActor) {
		t.Errorf("reschedule with a console link: err = %v, want ErrNoActor", err)
	}
	var actor string
	if err := c.f.s.r.QueryRow(`SELECT actor FROM action_logs WHERE event_id = ? AND type = 'add-role'`, ev).Scan(&actor); err != nil ||
		actor != o.Actor.String() {
		t.Errorf("logged actor = %q, %v", actor, err)
	}
	if err := c.f.s.r.QueryRow(`SELECT actor FROM action_logs WHERE event_id = ? AND type = 'create-event'`, ev).Scan(&actor); err != nil ||
		actor != fmt.Sprintf("member:%d", a) {
		t.Errorf("creation's actor = %q, %v", actor, err)
	}
}

// A console link checked again inside a write: one removed in between
// cannot write (access-control.md section 5, rule 6).
func TestUS_CO_2_4_StaleConsoleActorCannotWrite(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.Login(ctx, "b@x.tw", hash("sb"), before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.OpenConsole(ctx, hash("sb"), ev, hash("cb"), before); err != nil {
		t.Fatal(err)
	}
	o, err := c.f.s.LookupToken(ctx, hash("cb"), before)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.RemoveMember(ctx, hash("sa"), c.member(t, "b@x.tw"), before); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.AddRole(ctx, ev, "攝影", domain.Media, o.Actor, before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("after removal: err = %v, want ErrInvalidToken", err)
	}
	if _, err := c.f.s.AddRole(ctx, c.f.eventID, "攝影", domain.Media, o.Actor, before); !errors.Is(err, ErrCrossEvent) {
		t.Errorf("another event: err = %v, want ErrCrossEvent", err)
	}
}

// Opening keeps ConsolesPerMemberEvent per member and event; the oldest
// goes (US-CO-4.3).
func TestUS_CO_4_3_ConsolesPerMemberEvent(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	other := c.event(t, "sa", "甲二", 8)
	if err := c.f.s.OpenConsole(ctx, hash("sa"), other, hash("other"), before); err != nil {
		t.Fatal(err)
	}
	for i := range ConsolesPerMemberEvent + 1 {
		if err := c.f.s.OpenConsole(ctx, hash("sa"), ev, hash(fmt.Sprintf("c%d", i)), before); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.f.s.LookupToken(ctx, hash("c0"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("the oldest: err = %v, want ErrInvalidToken", err)
	}
	for _, h := range []string{"c1", fmt.Sprintf("c%d", ConsolesPerMemberEvent), "other"} {
		if _, err := c.f.s.LookupToken(ctx, hash(h), before); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
}

// An ended event's console link answers ended (api.md 10.2).
func TestUS_CO_4_4_EndedEventsConsoleIsEnded(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	late := at(9, 0).AddDate(0, 0, 3)
	if err := c.f.s.Login(ctx, "a@x.tw", hash("late"), late); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.OpenConsole(ctx, hash("late"), ev, hash("ca"), late); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("ca"), late); !errors.Is(err, ErrEventEnded) {
		t.Errorf("err = %v, want ErrEventEnded", err)
	}
}

// Replacing the Master link with the recovery code ends the event's
// console links (US-CO-4.5).
func TestUS_CO_4_5_RecoveryEndsConsoleLinks(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	other := c.event(t, "sa", "甲二", 8)
	for h, id := range map[string]int64{"ca": ev, "co": other} {
		if err := c.f.s.OpenConsole(ctx, hash("sa"), id, hash(h), before); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.f.s.ReplaceMasterLink(ctx, hash("r-甲婚宴"), hash("m-new"), before); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("ca"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("this event's console: err = %v, want ErrInvalidToken", err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("co"), before); err != nil {
		t.Errorf("another event's console: %v", err)
	}
}

// While member links are closed every one is ErrMembersClosed, never
// ErrInvalidToken; role links work as ever (US-O-12.6).
func TestUS_O_12_6_ClosedMemberAccess(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.OpenConsole(ctx, hash("sa"), ev, hash("ca"), before); err != nil {
		t.Fatal(err)
	}
	c.f.s.CloseMemberAccess()
	checks := map[string]error{
		"login":   c.f.s.Login(ctx, "a@x.tw", hash("s2"), before),
		"logout":  c.f.s.Logout(ctx, hash("sa")),
		"add":     c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before),
		"open":    c.f.s.OpenConsole(ctx, hash("sa"), ev, hash("c2"), before),
		"remove":  c.f.s.RemoveMember(ctx, hash("sa"), 99, before),
		"console": func() error { _, err := c.f.s.LookupToken(ctx, hash("ca"), before); return err }(),
		"company": func() error { _, err := c.f.s.Company(ctx, hash("sa"), before); return err }(),
		"create": func() error {
			_, err := c.f.s.CreateCompanyEvent(ctx, hash("sa"), NewEvent{Name: "x",
				Date: domain.Date{Year: 2027, Month: time.June, Day: 7}, TimeZone: "Asia/Taipei", MasterName: "總召",
				MasterTokenHash: hash("mx"), RecoveryCodeHash: hash("rx")}, before)
			return err
		}(),
		"snapshot": func() error { _, err := c.f.s.Snapshot(ctx, hash("ca"), before); return err }(),
	}
	for name, err := range checks {
		if !errors.Is(err, ErrMembersClosed) {
			t.Errorf("%s: err = %v, want ErrMembersClosed", name, err)
		}
	}
	if _, err := c.f.s.LookupToken(ctx, hash("nobody"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("an unknown link stays invalid: err = %v", err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("m-甲婚宴"), before); err != nil {
		t.Errorf("the Master link: %v", err)
	}
}

// The reset after a restore deletes every login and console link
// (US-O-12.5).
func TestUS_O_12_5_ResetMemberAccess(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.OpenConsole(ctx, hash("sa"), ev, hash("ca"), before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.ResetMemberAccess(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.Company(ctx, hash("sa"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("login link: err = %v", err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("ca"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("console link: err = %v", err)
	}
	if err := c.f.s.Login(ctx, "a@x.tw", hash("again"), before); err != nil {
		t.Errorf("the member logs in again: %v", err)
	}
}

// The developer's commands (US-O-12.1–12.3).
func TestUS_O_12_MemberCommands(t *testing.T) {
	c := newCompany(t)
	if id, name, err := c.f.s.FindMember(ctx, "a@x.tw"); err != nil || id != c.orgID || name != "甲" {
		t.Errorf("find = %d %q %v", id, name, err)
	}
	if _, _, err := c.f.s.FindMember(ctx, "nobody@x.tw"); !errors.Is(err, ErrNotMember) {
		t.Errorf("find nobody: err = %v, want ErrNotMember", err)
	}
	if err := c.f.s.AddMemberTo(ctx, 999, "b@x.tw", before); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such company: err = %v, want ErrNotFound", err)
	}
	if err := c.f.s.AddMemberTo(ctx, c.orgID, "a@x.tw", before); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("taken: err = %v, want ErrEmailTaken", err)
	}
	// The command removes the last member too.
	if id, name, err := c.f.s.RemoveMemberByEmail(ctx, "a@x.tw"); err != nil || id != c.orgID || name != "甲" {
		t.Errorf("remove = %d %q %v", id, name, err)
	}
	if _, err := c.f.s.Company(ctx, hash("sa"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("removed member's link: err = %v", err)
	}
	if _, _, err := c.f.s.RemoveMemberByEmail(ctx, "a@x.tw"); !errors.Is(err, ErrNotMember) {
		t.Errorf("twice: err = %v, want ErrNotMember", err)
	}
	if err := c.f.s.AddMemberTo(ctx, c.orgID, "a@x.tw", before); err != nil {
		t.Errorf("add back: %v", err)
	}
}

// A console link is checked against the event's company on every lookup
// and write, not only when it is made: one planted for an event of no
// company or another company is no link (US-CO-3.5).
func TestUS_CO_3_5_PlantedConsoleLinkOfAnotherCompanyIsInvalid(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	a := c.member(t, "a@x.tw")
	if _, err := c.f.s.w.Exec(`INSERT INTO console_tokens (member_id, event_id, token_hash, created_at) VALUES (?, ?, ?, 0)`,
		a, c.f.eventID, hash("planted")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("planted"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("planted for the wedding of no company: err = %v, want ErrInvalidToken", err)
	}
	if err := c.f.s.OpenConsole(ctx, hash("sa"), ev, hash("ca"), before); err != nil {
		t.Fatal(err)
	}
	o, err := c.f.s.LookupToken(ctx, hash("ca"), before)
	if err != nil {
		t.Fatal(err)
	}
	// The event moved to another company behind the store's back.
	if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "乙", "z@x.tw", before); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.w.Exec(`UPDATE events SET organization_id = (SELECT id FROM organizations WHERE name = '乙') WHERE id = ?`, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.AddRole(ctx, ev, "攝影", domain.Media, o.Actor, before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("write after the move: err = %v, want ErrInvalidToken", err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("ca"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("lookup after the move: err = %v, want ErrInvalidToken", err)
	}
}

// The overview proves the admin link first and trusts no cancelled read
// (US-O-9.1, US-O-11.3).
func TestUS_O_11_3_OverviewNeedsTheAdminLink(t *testing.T) {
	c := newCompany(t)
	if _, err := c.f.s.AdminOverview(ctx, hash("nobody"), before); !errors.Is(err, ErrInvalidAdmin) {
		t.Errorf("not the admin link: err = %v, want ErrInvalidAdmin", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.f.s.AdminOverview(cancelled, hash("admin"), before); err == nil || errors.Is(err, ErrInvalidAdmin) {
		t.Errorf("cancelled: err = %v", err)
	}
	if _, err := c.f.s.CreateAdminInvite(ctx, hash("admin"), hash("code"), before); err != nil {
		t.Fatal(err)
	}
	o, err := c.f.s.AdminOverview(ctx, hash("admin"), before)
	if err != nil || len(o.Invites) != 1 || len(o.Events) != 1 || len(o.Organizations) != 1 {
		t.Errorf("overview = %+v, %v", o, err)
	}
	// An event whose zone no longer reads back fails the overview.
	if _, err := c.f.s.w.Exec(`UPDATE events SET timezone = 'Mars/Base' WHERE id = ?`, c.f.eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.AdminOverview(ctx, hash("admin"), before); err == nil {
		t.Error("an unreadable event: no error")
	}
}

// A console link whose event no longer reads back, or a lookup cancelled
// before the console query, is an error and never ErrInvalidToken
// (access-control.md 3.3).
func TestUS_CO_4_1_ConsoleLookupFailuresAreNotInvalid(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if err := c.f.s.OpenConsole(ctx, hash("sa"), ev, hash("ca"), before); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for name, h := range map[string]string{"console": "ca", "unknown": "nobody"} {
		if _, err := c.f.s.LookupToken(cancelled, hash(h), before); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("cancelled %s: err = %v", name, err)
		}
	}
	if _, err := c.f.s.w.Exec(`UPDATE events SET timezone = 'Mars/Base' WHERE id = ?`, ev); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"ca", "m-甲婚宴"} {
		if _, err := c.f.s.LookupToken(ctx, hash(h), before); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s of an unreadable event: err = %v", h, err)
		}
		if _, err := c.f.s.Snapshot(ctx, hash(h), before); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s snapshot of an unreadable event: err = %v", h, err)
		}
	}
}
