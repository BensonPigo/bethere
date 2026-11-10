package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mailFor writes a login mail for the member with link mail and wants it
// written.
func (c company) mailFor(t *testing.T, memberID int64, mail string, now time.Time) {
	t.Helper()
	to, outcome, err := c.f.s.PrepareLoginMail(ctx, memberID, hash(mail), now)
	if err != nil || outcome != MailWritten || to == "" {
		t.Fatalf("mail %s: %q %v %v", mail, to, outcome, err)
	}
}

func (c company) countMails(t *testing.T) int {
	t.Helper()
	var n int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FROM login_mails`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// US-CO-1.3: the login reads the member only.
func TestUS_CO_1_3_MemberByEmail(t *testing.T) {
	c := newCompany(t)
	if id, err := c.f.s.MemberByEmail(ctx, "a@x.tw"); err != nil || id != c.member(t, "a@x.tw") {
		t.Errorf("member = %d %v", id, err)
	}
	if _, err := c.f.s.MemberByEmail(ctx, "nobody@x.tw"); !errors.Is(err, ErrNotMember) {
		t.Errorf("stranger: err = %v, want ErrNotMember", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.f.s.MemberByEmail(cancelled, "nobody@x.tw"); err == nil || errors.Is(err, ErrNotMember) {
		t.Errorf("cancelled: err = %v", err)
	}
	c.f.s.CloseMemberAccess()
	if _, err := c.f.s.MemberByEmail(ctx, "a@x.tw"); !errors.Is(err, ErrMembersClosed) {
		t.Errorf("closed: err = %v, want ErrMembersClosed", err)
	}
}

// US-CO-1.9, 1.10: a login mail goes to the member's address and its link
// is traded once for a login link, within 15 minutes.
func TestUS_CO_1_10_ConfirmLoginMail(t *testing.T) {
	c := newCompany(t)
	a := c.member(t, "a@x.tw")
	to, outcome, err := c.f.s.PrepareLoginMail(ctx, a, hash("m1"), before)
	if err != nil || outcome != MailWritten || to != "a@x.tw" {
		t.Fatalf("prepare = %q %v %v", to, outcome, err)
	}
	if err := c.f.s.ConfirmLoginMail(ctx, hash("m1"), hash("s1"), before.Add(15*time.Minute-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.Company(ctx, hash("s1"), before); err != nil {
		t.Errorf("the new login link: %v", err)
	}
	if err := c.f.s.ConfirmLoginMail(ctx, hash("m1"), hash("s2"), before); !errors.Is(err, ErrInvalidLoginMail) {
		t.Errorf("twice: err = %v, want ErrInvalidLoginMail", err)
	}
	c.mailFor(t, a, "m2", before)
	if err := c.f.s.ConfirmLoginMail(ctx, hash("m2"), hash("s3"), before.Add(15*time.Minute)); !errors.Is(err, ErrInvalidLoginMail) {
		t.Errorf("at 15 minutes: err = %v, want ErrInvalidLoginMail", err)
	}
	for _, h := range [][]byte{hash("nothing"), hash("sa")} { // unknown; a login link
		if err := c.f.s.ConfirmLoginMail(ctx, h, hash("s4"), before); !errors.Is(err, ErrInvalidLoginMail) {
			t.Errorf("not a mail's link: err = %v", err)
		}
	}
	if _, err := c.f.s.Company(ctx, hash("s3"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("a refused confirm made a link: err = %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.f.s.ConfirmLoginMail(cancelled, hash("nothing"), hash("s5"), before); err == nil || errors.Is(err, ErrInvalidLoginMail) {
		t.Errorf("cancelled: err = %v", err)
	}
	c.f.s.CloseMemberAccess()
	if err := c.f.s.ConfirmLoginMail(ctx, hash("m2"), hash("s6"), before); !errors.Is(err, ErrMembersClosed) {
		t.Errorf("closed: err = %v, want ErrMembersClosed", err)
	}
}

// US-CO-1.10: two confirms of one link leave one login link.
func TestUS_CO_1_10_ConcurrentConfirmsLeaveOne(t *testing.T) {
	c := newCompany(t)
	c.mailFor(t, c.member(t, "a@x.tw"), "m", before)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.f.s.ConfirmLoginMail(ctx, hash("m"), hash(fmt.Sprintf("s%d", i)), before)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrInvalidLoginMail) {
			t.Errorf("err = %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("confirms that worked = %d, want 1", ok)
	}
}

// US-CO-1.10, 1.11: a removed member's mail no longer works, and their
// mails still count for the company.
func TestUS_CO_1_11_RemovedMembersMailsStillCount(t *testing.T) {
	c := newCompany(t)
	if _, _, err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	b := c.member(t, "b@x.tw")
	c.mailFor(t, b, "mb", before)
	if _, err := c.f.s.RemoveMember(ctx, hash("sa"), b, before); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.ConfirmLoginMail(ctx, hash("mb"), hash("sb"), before); !errors.Is(err, ErrInvalidLoginMail) {
		t.Errorf("removed member's mail: err = %v, want ErrInvalidLoginMail", err)
	}
	if _, outcome, err := c.f.s.PrepareLoginMail(ctx, b, hash("mb2"), before); err != nil || outcome != MailNoMember {
		t.Errorf("mail to a removed member: %v %v, want MailNoMember", outcome, err)
	}
	var org, all int
	if err := c.f.s.r.QueryRow(`SELECT count(*) FILTER (WHERE organization_id = ?), count(*) FROM login_mails`, c.orgID).
		Scan(&org, &all); err != nil || org != 1 || all != 1 {
		t.Errorf("mails of the company = %d, all = %d (%v), want 1 and 1", org, all, err)
	}
}

// US-CO-1.11: ten mails a member in any 24 hours.
func TestUS_CO_1_11_MailsPerMemberDay(t *testing.T) {
	c := newCompany(t)
	a := c.member(t, "a@x.tw")
	for i := 1; i <= 10; i++ { // US-CO-1.11: 10
		c.mailFor(t, a, fmt.Sprintf("m%d", i), before.Add(time.Duration(i)*time.Minute))
	}
	if _, outcome, err := c.f.s.PrepareLoginMail(ctx, a, hash("m11"), before.Add(24*time.Hour+time.Minute-time.Nanosecond)); err != nil || outcome != MailLimited {
		t.Errorf("the 11th within 24 hours of the 1st: %v %v, want MailLimited", outcome, err)
	}
	// 24 hours after the first, it leaves the count.
	c.mailFor(t, a, "m12", before.Add(24*time.Hour+time.Minute))
	if n := c.countMails(t); n != 11 {
		t.Errorf("mails = %d, want 11", n)
	}
}

// US-CO-1.11: a hundred mails a company in any 24 hours, whoever of it
// they go to.
func TestUS_CO_1_11_MailsPerOrganizationDay(t *testing.T) {
	c := newCompany(t)
	ids := []int64{c.member(t, "a@x.tw")}
	for i := 1; i < 10; i++ {
		email := fmt.Sprintf("m%d@x.tw", i)
		if _, _, err := c.f.s.AddMember(ctx, hash("sa"), email, before); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.member(t, email))
	}
	for i := 0; i < 100; i++ { // US-CO-1.11: 100
		c.mailFor(t, ids[i%10], fmt.Sprintf("m%d", i), before)
	}
	if _, err := c.f.s.CreateOrganization(ctx, hash("admin"), "乙", "z@x.tw", before); err != nil {
		t.Fatal(err)
	}
	z, err := c.f.s.MemberByEmail(ctx, "z@x.tw")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.f.s.AddMember(ctx, hash("sa"), "new@x.tw", before); err != nil {
		t.Fatal(err)
	}
	fresh := c.member(t, "new@x.tw")
	if _, outcome, err := c.f.s.PrepareLoginMail(ctx, fresh, hash("over"), before); err != nil || outcome != MailLimited {
		t.Errorf("the company's 101st: %v %v, want MailLimited", outcome, err)
	}
	c.mailFor(t, z, "other company", before)
}

// US-CO-1.11: four hundred mails in all in any 24 hours.
func TestUS_CO_1_11_MailsPerDay(t *testing.T) {
	c := newCompany(t)
	a := c.member(t, "a@x.tw")
	// 400 rows of other companies' members, as the worker would write them.
	if _, err := c.f.s.w.Exec(`INSERT INTO organizations (name, created_at) VALUES ('丙', 0)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ { // US-CO-1.11: 400
		if _, err := c.f.s.w.Exec(`INSERT INTO login_mails (member_id, organization_id, token_hash, created_at, expires_at)
			VALUES (NULL, (SELECT max(id) FROM organizations), NULL, ?, ?)`, toNS(before), toNS(before)); err != nil {
			t.Fatal(err)
		}
	}
	if _, outcome, err := c.f.s.PrepareLoginMail(ctx, a, hash("m"), before); err != nil || outcome != MailLimited {
		t.Errorf("the 401st: %v %v, want MailLimited", outcome, err)
	}
	c.mailFor(t, a, "m2", before.Add(MailWindow))
}

// api.md 10.2: a member removed between the read and the write makes the
// insert fail on its foreign key; no login mail is left.
func TestUS_CO_1_11_MemberRemovedBeforeTheWrite(t *testing.T) {
	c := newCompany(t)
	if _, _, err := c.f.s.AddMember(ctx, hash("sa"), "b@x.tw", before); err != nil {
		t.Fatal(err)
	}
	b := c.member(t, "b@x.tw")
	if _, err := c.f.s.w.Exec(`CREATE TRIGGER gone BEFORE INSERT ON login_mails BEGIN DELETE FROM members WHERE id = NEW.member_id; END`); err != nil {
		t.Fatal(err)
	}
	if to, _, err := c.f.s.PrepareLoginMail(ctx, b, hash("m"), before); err == nil || to != "" {
		t.Errorf("prepare = %q %v, want the foreign key's error", to, err)
	}
	if n := c.countMails(t); n != 0 {
		t.Errorf("mails = %d, want none", n)
	}
}

// PrepareLoginMail's failures are errors, never a mail.
func TestUS_CO_1_11_PrepareFailures(t *testing.T) {
	c := newCompany(t)
	a := c.member(t, "a@x.tw")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := c.f.s.PrepareLoginMail(cancelled, a, hash("m"), before); err == nil {
		t.Error("cancelled: no error")
	}
	if _, err := c.f.s.w.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON login_mails BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if to, _, err := c.f.s.PrepareLoginMail(ctx, a, hash("m"), before); err == nil || to != "" {
		t.Errorf("refused insert: %q %v", to, err)
	}
	if _, err := c.f.s.w.Exec(`DROP TRIGGER refuse; ALTER TABLE login_mails RENAME COLUMN created_at TO c`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.f.s.PrepareLoginMail(ctx, a, hash("m"), before); err == nil {
		t.Error("no counts: no error")
	}
}

// US-CO-1.13: links an email-only release made, with no verified, are no
// links, as the purge deletes them.
func TestUS_CO_1_13_UnverifiedLinksAreRefused(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	a := c.member(t, "a@x.tw")
	// The INSERTs of the email-only release (M4).
	if _, err := c.f.s.w.Exec(`INSERT INTO member_sessions (member_id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		a, hash("old session"), toNS(before), toNS(before.Add(SessionLife))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.w.Exec(`INSERT INTO console_tokens (member_id, event_id, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		a, ev, hash("old console"), toNS(before)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.Company(ctx, hash("old session"), before); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("old login link: err = %v, want ErrInvalidSession", err)
	}
	if err := c.f.s.Logout(ctx, hash("old session")); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("old login link's logout: err = %v, want ErrInvalidSession", err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("old console"), before); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("old console link: err = %v, want ErrInvalidToken", err)
	}
	// The ones this release makes work.
	if err := c.f.s.OpenConsole(ctx, hash("sa"), ev, hash("new console"), before); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("new console"), before); err != nil {
		t.Errorf("new console link: %v", err)
	}
	if _, err := c.f.s.PurgeEnded(ctx, before); err != nil {
		t.Fatal(err)
	}
	var sessions, consoles int
	if err := c.f.s.r.QueryRow(`SELECT (SELECT count(*) FROM member_sessions WHERE verified IS NULL),
		(SELECT count(*) FROM console_tokens WHERE verified IS NULL)`).Scan(&sessions, &consoles); err != nil || sessions+consoles != 0 {
		t.Errorf("unverified after the purge: %d sessions, %d consoles (%v)", sessions, consoles, err)
	}
	if _, err := c.f.s.Company(ctx, hash("sa"), before); err != nil {
		t.Errorf("the purge took a verified login link: %v", err)
	}
	if _, err := c.f.s.LookupToken(ctx, hash("new console"), before); err != nil {
		t.Errorf("the purge took a verified console link: %v", err)
	}
}

// US-O-12.6, US-CO-1.13: while member links are closed an old console link
// is closed too, never "invalid".
func TestUS_O_12_6_ClosedUnverifiedConsoleIsClosed(t *testing.T) {
	c := newCompany(t)
	ev := c.event(t, "sa", "甲婚宴", 7)
	if _, err := c.f.s.w.Exec(`INSERT INTO console_tokens (member_id, event_id, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		c.member(t, "a@x.tw"), ev, hash("old console"), toNS(before)); err != nil {
		t.Fatal(err)
	}
	c.f.s.CloseMemberAccess()
	if _, err := c.f.s.LookupToken(ctx, hash("old console"), before); !errors.Is(err, ErrMembersClosed) {
		t.Errorf("err = %v, want ErrMembersClosed", err)
	}
}

// The purge deletes login mails 24 hours old (data-model.md LoginMail).
func TestUS_CO_1_11_PurgeDayOldMails(t *testing.T) {
	c := newCompany(t)
	a := c.member(t, "a@x.tw")
	c.mailFor(t, a, "old", before)
	c.mailFor(t, a, "new", before.Add(time.Nanosecond))
	if _, err := c.f.s.PurgeEnded(ctx, before.Add(MailWindow)); err != nil {
		t.Fatal(err)
	}
	if n := c.countMails(t); n != 1 {
		t.Errorf("mails after the purge = %d, want the newer one", n)
	}
}

// US-O-12.5: the reset clears every login mail's link and keeps the rows.
func TestUS_O_12_5_ResetClearsLoginMails(t *testing.T) {
	c := newCompany(t)
	c.mailFor(t, c.member(t, "a@x.tw"), "m", before)
	if err := c.f.s.ResetMemberAccess(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.ConfirmLoginMail(ctx, hash("m"), hash("s"), before); !errors.Is(err, ErrInvalidLoginMail) {
		t.Errorf("err = %v, want ErrInvalidLoginMail", err)
	}
	if n := c.countMails(t); n != 1 {
		t.Errorf("mails after the reset = %d, want 1", n)
	}
}

// US-O-12.5: each step of the reset fails as a whole.
func TestUS_O_12_5_ResetFailsWhole(t *testing.T) {
	c := newCompany(t)
	if _, err := c.f.s.w.Exec(`ALTER TABLE login_mails RENAME COLUMN token_hash TO t`); err != nil {
		t.Fatal(err)
	}
	if err := c.f.s.ResetMemberAccess(ctx); err == nil {
		t.Fatal("no error")
	}
	if _, err := c.f.s.Company(ctx, hash("sa"), before); err != nil {
		t.Errorf("a failed reset deleted the login links: %v", err)
	}
}

// US-O-12.8: the developer's login link for a member.
func TestUS_O_12_8_IssueLoginLink(t *testing.T) {
	c := newCompany(t)
	if err := c.f.s.IssueLoginLink(ctx, "a@x.tw", hash("issued"), before); err != nil {
		t.Fatal(err)
	}
	if _, err := c.f.s.Company(ctx, hash("issued"), before); err != nil {
		t.Errorf("issued link: %v", err)
	}
	c.f.s.CloseMemberAccess()
	if err := c.f.s.IssueLoginLink(ctx, "a@x.tw", hash("closed"), before); !errors.Is(err, ErrMembersClosed) {
		t.Errorf("closed: err = %v, want ErrMembersClosed", err)
	}
}
