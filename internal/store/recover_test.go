package store

import (
	"context"
	"errors"
	"testing"
	"time"
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
