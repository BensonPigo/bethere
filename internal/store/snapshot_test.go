package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// withHook sets afterTokenResolved for one test.
func withHook(t *testing.T, fn func()) {
	t.Helper()
	afterTokenResolved = fn
	t.Cleanup(func() { afterTokenResolved = nil })
}

// Snapshot resolves a token with LookupToken's rule and order:
// invalid, then ended (access-control.md 3.3).
func TestSnapshotResolvesLikeLookupToken(t *testing.T) {
	f := newFixture(t)
	e, _, _ := f.s.Event(ctx, f.eventID)
	if _, err := f.s.Snapshot(ctx, hash("unknown"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("unknown token: err = %v, want ErrInvalidToken", err)
	}
	if _, err := f.s.Snapshot(ctx, hash("master"), e.EndsAt().Add(-time.Nanosecond)); err != nil {
		t.Errorf("just before the end: %v", err)
	}
	if _, err := f.s.Snapshot(ctx, hash("master"), e.EndsAt()); !errors.Is(err, ErrEventEnded) {
		t.Errorf("at the end: err = %v, want ErrEventEnded", err)
	}
	if _, err := f.s.Snapshot(ctx, hash("unknown"), e.EndsAt()); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("unknown token after the end: err = %v, want ErrInvalidToken", err)
	}
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.RevokeToken(ctx, f.stylist, f.masterBy, at(2, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Snapshot(ctx, hash("stylist"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("revoked token: err = %v, want ErrInvalidToken", err)
	}
}

func TestSnapshotReadsTheWholeEvent(t *testing.T) {
	f := newFixture(t)
	if err := f.s.AssignRole(ctx, f.cueIDs[1], f.stylist, "新娘房", f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.AssignRole(ctx, f.cueIDs[1], f.master, "", f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.w.Exec(`UPDATE cues SET note_public = '公開' WHERE id = ?`, f.cueIDs[1]); err != nil {
		t.Fatal(err)
	}
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	d, err := f.s.Snapshot(ctx, hash("stylist"), at(12, 0))
	if err != nil {
		t.Fatal(err)
	}
	if d.Owner.RoleID != f.stylist || d.Owner.RoleType != domain.Stylist || d.Owner.RoleName != "新秘" || d.Owner.EventID != f.eventID {
		t.Errorf("owner = %+v", d.Owner)
	}
	if d.Version != f.version(t) || d.Event.ID != f.eventID || d.Event.Status != domain.Draft {
		t.Errorf("event %+v version %d", d.Event, d.Version)
	}
	if len(d.Cues) != 3 || d.Cues[0].ID != f.cueIDs[0] || d.Cues[2].ID != f.cueIDs[2] {
		t.Fatalf("cues = %+v", d.Cues)
	}
	c := d.Cues[1]
	if c.NotePublic == nil || *c.NotePublic != "公開" || d.Cues[0].NotePublic != nil {
		t.Errorf("notes: %v, %v", c.NotePublic, d.Cues[0].NotePublic)
	}
	// Roles of a cue by display order then id: the Master (created first)
	// before the stylist; an empty note reads back as "", NULL as nil.
	if len(c.Roles) != 2 || c.Roles[0].RoleID != f.master || c.Roles[1].RoleID != f.stylist ||
		c.Roles[1].RoleName != "新秘" || c.Roles[1].RoleNote == nil || *c.Roles[1].RoleNote != "新娘房" {
		t.Errorf("cue roles = %+v", c.Roles)
	}
	if len(d.Roles) != 2 || d.Roles[0].Type != domain.Master || d.Roles[1].ID != f.stylist {
		t.Errorf("roles = %+v", d.Roles)
	}
	if d.LastOp != nil {
		t.Errorf("draft event has an undoable op: %+v", d.LastOp)
	}
}

// LastOp is exactly what UndoLastCueOp would reverse (data-model.md 4.1).
func TestSnapshotLastOpFollowsUndo(t *testing.T) {
	f := newFixture(t)
	f.start(t)
	lastOp := func() *domain.Op {
		t.Helper()
		d, err := f.s.Snapshot(ctx, hash("master"), at(12, 0))
		if err != nil {
			t.Fatal(err)
		}
		return d.LastOp
	}
	if op := lastOp(); op != nil {
		t.Fatalf("before any operation: %+v", op)
	}
	if _, _, err := f.s.ApplyCueOp(ctx, f.eventID, f.version(t), f.masterBy, at(10, 0), advance(at(10, 0))); err != nil {
		t.Fatal(err)
	}
	op := lastOp()
	if op == nil || op.Kind != domain.OpAdvance || len(op.Changes) != 1 || op.Changes[0].After.ID != f.cueIDs[0] {
		t.Fatalf("after advance: %+v", op)
	}
	if _, _, err := f.s.UndoLastCueOp(ctx, f.eventID, f.version(t), f.masterBy, at(10, 1)); err != nil {
		t.Fatal(err)
	}
	if op := lastOp(); op != nil {
		t.Errorf("after undo: %+v, want nothing to undo", op)
	}
	// A format this release cannot read is not offered for undo.
	if _, err := f.s.w.Exec(`INSERT INTO action_logs (event_id, at, actor, type, payload) VALUES (?, 0, 'server', 'cue.reorder', '{"v":1}')`, f.eventID); err != nil {
		t.Fatal(err)
	}
	if op := lastOp(); op != nil {
		t.Errorf("unknown op offered for undo: %+v", op)
	}
}

// A revocation, a new write or a purge committed after the token was
// resolved cannot be paired with that lookup: the whole snapshot is read
// as of the lookup (api.md section 7). The next read sees the revocation.
func TestSnapshotIsOneReadTransaction(t *testing.T) {
	f := newFixture(t)
	if err := f.s.AssignRole(ctx, f.cueIDs[0], f.stylist, "", f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	v := f.version(t)

	var once sync.Once
	withHook(t, func() {
		once.Do(func() {
			if err := f.s.RevokeToken(ctx, f.stylist, f.masterBy, at(2, 0)); err != nil {
				t.Error(err)
			}
			if _, err := f.s.AddCue(ctx, f.eventID, domain.Cue{Seq: 9, Title: "after", ScheduledStart: at(13, 0)}, f.masterBy, at(2, 0)); err != nil {
				t.Error(err)
			}
			if err := f.s.AssignRole(ctx, f.cueIDs[1], f.stylist, "late", f.masterBy, at(2, 0)); err != nil {
				t.Error(err)
			}
		})
	})
	d, err := f.s.Snapshot(ctx, hash("stylist"), at(12, 0))
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != v {
		t.Errorf("version %d, want %d as of the lookup", d.Version, v)
	}
	if len(d.Cues) != 3 {
		t.Errorf("%d cues, want 3 as of the lookup (a cue added after it leaked)", len(d.Cues))
	}
	if len(d.Cues[1].Roles) != 0 {
		t.Errorf("assignment made after the lookup leaked: %+v", d.Cues[1].Roles)
	}
	if _, err := f.s.Snapshot(ctx, hash("stylist"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("next read: err = %v, want ErrInvalidToken", err)
	}
}

func TestSnapshotDuringPurgeStaysWhole(t *testing.T) {
	f := newFixture(t)
	e, _, _ := f.s.Event(ctx, f.eventID)
	var once sync.Once
	withHook(t, func() {
		once.Do(func() {
			if ids, err := f.s.PurgeEnded(ctx, e.PurgeAt()); err != nil || len(ids) != 1 {
				t.Errorf("purge: %v, %v", ids, err)
			}
		})
	})
	d, err := f.s.Snapshot(ctx, hash("master"), at(12, 0))
	if err != nil {
		t.Fatalf("snapshot during purge: %v", err)
	}
	if len(d.Cues) != 3 || len(d.Roles) != 2 {
		t.Errorf("partial snapshot: %d cues, %d roles", len(d.Cues), len(d.Roles))
	}
	if _, err := f.s.Snapshot(ctx, hash("master"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("after purge: err = %v, want ErrInvalidToken", err)
	}
}

// A cancelled or expired context is never reported as "no such token"
// (tech-stack.md 2.1「逾時與錯誤」), for a valid token or an unknown one, on lookups and
// on writes.
func TestContextErrorsAreNeverInvalidToken(t *testing.T) {
	f := newFixture(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	expired, cancel2 := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel2()
	for name, c := range map[string]context.Context{"cancelled": cancelled, "expired": expired} {
		for _, h := range []string{"master", "unknown"} {
			if _, err := f.s.LookupToken(c, hash(h), at(12, 0)); err == nil || errors.Is(err, ErrInvalidToken) {
				t.Errorf("%s LookupToken(%s): err = %v", name, h, err)
			}
			if _, err := f.s.Snapshot(c, hash(h), at(12, 0)); err == nil || errors.Is(err, ErrInvalidToken) {
				t.Errorf("%s Snapshot(%s): err = %v", name, h, err)
			}
		}
		v := f.version(t)
		if _, err := f.s.StartEvent(c, f.eventID, v, f.masterBy, at(9, 0)); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s StartEvent: err = %v", name, err)
		}
		if _, _, err := f.s.ApplyCueOp(c, f.eventID, v, f.masterBy, at(10, 0), advance(at(10, 0))); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s ApplyCueOp: err = %v", name, err)
		}
		if _, _, err := f.s.UndoLastCueOp(c, f.eventID, v, f.masterBy, at(10, 0)); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s UndoLastCueOp: err = %v", name, err)
		}
		if err := f.s.ImportRundown(c, f.eventID, nil, f.masterBy, at(0, 0)); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s ImportRundown: err = %v", name, err)
		}
	}

	// Cancelled while the snapshot is being read, after the lookup.
	c, cancel3 := context.WithCancel(ctx)
	withHook(t, cancel3)
	if _, err := f.s.Snapshot(c, hash("master"), at(12, 0)); err == nil || errors.Is(err, ErrInvalidToken) {
		t.Errorf("cancelled mid-read: err = %v", err)
	}
}

// The token query itself failing inside the transaction (here: its table
// is gone) is a database error, never "no such token" (tech-stack.md 2.1「逾時與錯誤」). The
// cancelled-context cases above fail earlier, at BEGIN, so they alone
// would not catch a lookup that maps query errors to ErrInvalidToken.
func TestFailingTokenQueryIsNeverInvalid(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.w.Exec(`ALTER TABLE access_tokens RENAME TO access_tokens_moved`); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"master", "unknown"} {
		if _, err := f.s.LookupToken(ctx, hash(h), at(12, 0)); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("LookupToken(%s): err = %v, want a database error", h, err)
		}
		if _, err := f.s.Snapshot(ctx, hash(h), at(12, 0)); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Errorf("Snapshot(%s): err = %v, want a database error", h, err)
		}
	}
}

// Cancellation racing a lookup of a valid token yields the owner or a
// context error, never ErrInvalidToken.
func TestCancellationRacingALookupIsNeverInvalid(t *testing.T) {
	f := newFixture(t)
	for i := range 300 {
		c, cancel := context.WithCancel(ctx)
		time.AfterFunc(time.Duration(i%30)*time.Microsecond*10, cancel)
		_, err := f.s.LookupToken(c, hash("master"), at(12, 0))
		_, err2 := f.s.Snapshot(c, hash("master"), at(12, 0))
		cancel()
		for _, e := range []error{err, err2} {
			if errors.Is(e, ErrInvalidToken) {
				t.Fatalf("iteration %d: a valid token was reported invalid: %v", i, e)
			}
		}
	}
}
