package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// A recovery code is "wrong" only when the query succeeded and found no
// event (US-O-3.4): never for a cancelled request or a failing query.
func TestUS_O_3_4_OnlyAConfirmedMissIsAWrongCode(t *testing.T) {
	f := newFixture(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	expired, cancel2 := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel2()
	for name, c := range map[string]context.Context{"cancelled": cancelled, "expired": expired} {
		for _, h := range []string{"recovery", "unknown"} {
			if err := f.s.ReplaceMasterLink(c, hash(h), hash("new"), at(9, 0)); err == nil || errors.Is(err, ErrInvalidRecoveryCode) {
				t.Errorf("%s (%s): err = %v", name, h, err)
			}
		}
	}
	if _, err := f.s.w.Exec(`ALTER TABLE events RENAME TO events_moved`); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"recovery", "unknown"} {
		if err := f.s.ReplaceMasterLink(ctx, hash(h), hash("new2"), at(9, 0)); err == nil || errors.Is(err, ErrInvalidRecoveryCode) {
			t.Errorf("failing query (%s): err = %v", h, err)
		}
	}
}

// The old Master link is revoked only together with the new one being
// stored and logged (US-O-3.3): a failing insert leaves the old link.
func TestUS_O_3_3_ReplacementIsOneTransaction(t *testing.T) {
	f := newFixture(t)
	// A token hash already in use makes the insert fail on UNIQUE.
	err := f.s.ReplaceMasterLink(ctx, hash("recovery"), hash("taken"), at(9, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.ReplaceMasterLink(ctx, hash("recovery"), hash("taken"), at(9, 0)); err == nil {
		t.Fatal("a duplicate token hash was stored")
	}
	if _, err := f.s.LookupToken(ctx, hash("taken"), at(9, 0)); err != nil {
		t.Errorf("the failed replacement revoked the current link: %v", err)
	}
	var n int
	if err := f.s.r.QueryRow(`SELECT count(*) FROM action_logs WHERE type = 'token.recover'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("token.recover rows = %d (%v), want 1", n, err)
	}
}

// The developer resets a recovery code with the Master link or with the
// current code itself (US-O-4.1, 4.2): the old code stops working, the
// new one works, the Master link is left alone, and the log names who
// acted but no code or hash.
func TestUS_O_4_ResetRecoveryCode(t *testing.T) {
	f := newFixture(t)
	v := f.version(t)
	if err := f.s.ResetRecoveryCode(ctx, f.masterBy, hash("second"), at(9, 0)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.LookupRecoveryCode(ctx, hash("recovery")); !errors.Is(err, ErrInvalidRecoveryCode) {
		t.Errorf("old code: err = %v, want ErrInvalidRecoveryCode", err)
	}
	byCode, _, err := f.s.LookupRecoveryCode(ctx, hash("second"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.ResetRecoveryCode(ctx, byCode, hash("third"), at(9, 0)); err != nil {
		t.Fatalf("reset with the current code: %v", err)
	}
	if err := f.s.ReplaceMasterLink(ctx, hash("third"), hash("new-master"), at(9, 0)); err != nil {
		t.Errorf("new code does not replace the Master link: %v", err)
	}
	if got := f.version(t); got != v {
		t.Errorf("version = %d, want %d", got, v)
	}

	rows, err := f.s.r.Query(`SELECT actor, payload FROM action_logs WHERE type = 'recovery-code.reset' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var actor, payload string
		if err := rows.Scan(&actor, &payload); err != nil {
			t.Fatal(err)
		}
		got = append(got, actor+" "+payload)
	}
	want := []string{fmt.Sprintf(`role:%d {"byTokenId":%d}`, f.master, f.masterBy.tokenID), "recovery-code {}"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("log = %q, want %q", got, want)
	}
}

// Only the Master link or the current code may reset, never once the
// event has ended (US-O-4.3); a refused reset leaves the code as it was.
func TestUS_O_4_3_ResetRefused(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	stylist, err := f.s.LookupToken(ctx, hash("stylist"), at(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	e, _, err := f.s.Event(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		actor Actor
		now   time.Time
		want  error
	}{
		"non-Master link": {stylist.Actor, at(1, 0), ErrNoActor},
		"no actor":        {Actor{}, at(1, 0), ErrNoActor},
		"ended":           {f.masterBy, e.EndsAt(), domain.ErrEnded},
	} {
		if err := f.s.ResetRecoveryCode(ctx, c.actor, hash("taken-over"), c.now); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	if _, _, err := f.s.LookupRecoveryCode(ctx, hash("recovery")); err != nil {
		t.Errorf("a refused reset changed the code: %v", err)
	}
}
