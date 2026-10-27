package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

var inviteNow = at(9, 0).AddDate(0, -1, 0)

func invitedEvent(code, token string) NewEvent {
	return NewEvent{
		Name: "受邀活動", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: hash(token), RecoveryCodeHash: hash("rc-" + token), InviteHash: hash(code),
	}
}

func tableRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.r.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func newInvite(t *testing.T, s *Store, code string, now time.Time) int64 {
	t.Helper()
	id, err := s.CreateInvite(ctx, hash(code), now)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// US-M-17.5: a code creates one event, is deleted with it, and only its
// id is logged.
func TestUS_M_17_5_InviteCreatesOneEvent(t *testing.T) {
	s, _ := newStore(t)
	inviteID := newInvite(t, s, "invite", inviteNow)
	eventID, err := s.CreateEvent(ctx, invitedEvent("invite", "t1"), inviteNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupToken(ctx, hash("t1"), inviteNow); err != nil {
		t.Errorf("the new Master link does not open: %v", err)
	}
	if n := tableRows(t, s, "invites"); n != 0 {
		t.Errorf("invites left = %d, want the used one deleted", n)
	}
	var payload string
	if err := s.r.QueryRow(`SELECT payload FROM action_logs WHERE event_id = ? AND type = 'create-event'`, eventID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(`{"inviteId":%d}`, inviteID); payload != want {
		t.Errorf("payload = %s, want %s", payload, want)
	}
	if _, err := s.CreateEvent(ctx, invitedEvent("invite", "t2"), inviteNow); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("second use: err = %v, want ErrInvalidInvite", err)
	}
	if n := tableRows(t, s, "events"); n != 1 {
		t.Errorf("events = %d, want 1", n)
	}
}

// US-M-17.6: a wrong or expired code writes nothing; a code is usable
// until the instant it expires.
func TestUS_M_17_6_WrongOrExpiredWritesNothing(t *testing.T) {
	s, _ := newStore(t)
	newInvite(t, s, "invite", inviteNow)
	expires := inviteNow.Add(30 * 24 * time.Hour) // ADR-0036
	for name, c := range map[string]struct {
		code string
		now  time.Time
	}{"wrong": {"other", inviteNow}, "expired": {"invite", expires}} {
		if _, err := s.CreateEvent(ctx, invitedEvent(c.code, "t-"+name), c.now); !errors.Is(err, ErrInvalidInvite) {
			t.Errorf("%s: err = %v, want ErrInvalidInvite", name, err)
		}
	}
	if tableRows(t, s, "events") != 0 || tableRows(t, s, "invites") != 1 {
		t.Fatal("a refused code wrote")
	}
	if _, err := s.CreateEvent(ctx, invitedEvent("invite", "t"), expires.Add(-time.Nanosecond)); err != nil {
		t.Errorf("just before expiry: %v", err)
	}
}

// US-M-17.5: a wrong code is answered without the writer, so guesses
// never queue the event day's writes.
func TestUS_M_17_5_WrongCodeNeedsNoWriter(t *testing.T) {
	s, _ := newStore(t)
	newInvite(t, s, "invite", inviteNow)
	tx, err := s.w.BeginTx(ctx, nil) // holds the only write connection
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := s.CreateEvent(c, invitedEvent("other", "t"), inviteNow); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("err = %v, want ErrInvalidInvite without waiting for the writer", err)
	}
}

// US-M-17.5: two requests with one code create one event.
func TestUS_M_17_5_OneCodeOneEventUnderRace(t *testing.T) {
	s, _ := newStore(t)
	newInvite(t, s, "invite", inviteNow)
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.CreateEvent(ctx, invitedEvent("invite", fmt.Sprint("t", i)), inviteNow)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrInvalidInvite):
			t.Errorf("err = %v", err)
		}
	}
	if ok != 1 || tableRows(t, s, "events") != 1 {
		t.Errorf("successes = %d, events = %d, want 1", ok, tableRows(t, s, "events"))
	}
}

// US-M-17.6: a cancelled request or a failing query is never "invalid".
func TestUS_M_17_6_OnlyAConfirmedMissIsInvalid(t *testing.T) {
	s, _ := newStore(t)
	newInvite(t, s, "invite", inviteNow)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, code := range []string{"invite", "other"} {
		if _, err := s.CreateEvent(cancelled, invitedEvent(code, "t"), inviteNow); err == nil || errors.Is(err, ErrInvalidInvite) {
			t.Errorf("cancelled (%s): err = %v", code, err)
		}
	}
	// A miss found just as the request was cancelled is the cancellation.
	if err := inviteMiss(cancelled, sql.ErrNoRows); !errors.Is(err, context.Canceled) {
		t.Errorf("miss after cancel: err = %v", err)
	}
	if _, err := s.w.Exec(`ALTER TABLE invites RENAME TO invites_moved`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEvent(ctx, invitedEvent("other", "t"), inviteNow); err == nil || errors.Is(err, ErrInvalidInvite) {
		t.Errorf("failing query: err = %v", err)
	}
}

// US-M-17.7: a failed creation leaves the code usable.
func TestUS_M_17_7_FailedCreationKeepsTheCode(t *testing.T) {
	s, _ := newStore(t)
	newInvite(t, s, "invite", inviteNow)
	newInvite(t, s, "invite2", inviteNow)
	if _, err := s.CreateEvent(ctx, invitedEvent("invite2", "t"), inviteNow); err != nil {
		t.Fatal(err)
	}
	// The same token hash again fails on UNIQUE after the code is deleted.
	if _, err := s.CreateEvent(ctx, invitedEvent("invite", "t"), inviteNow); err == nil {
		t.Fatal("a duplicate token hash was stored")
	}
	if _, err := s.CreateEvent(ctx, invitedEvent("invite", "t3"), inviteNow); err != nil {
		t.Errorf("the code was used up by a failed creation: %v", err)
	}
}

// US-M-17.11: the daily purge deletes expired codes only.
func TestUS_M_17_11_PurgeDeletesExpiredInvites(t *testing.T) {
	s, _ := newStore(t)
	newInvite(t, s, "old", inviteNow)
	newInvite(t, s, "new", inviteNow.Add(time.Hour))
	if _, err := s.PurgeEnded(ctx, inviteNow.Add(InviteLife)); err != nil {
		t.Fatal(err)
	}
	if tableRows(t, s, "invites") != 1 {
		t.Fatalf("invites = %d, want 1", tableRows(t, s, "invites"))
	}
	if _, err := s.CreateEvent(ctx, invitedEvent("new", "t"), inviteNow); err != nil {
		t.Errorf("the unexpired code was purged: %v", err)
	}
}
