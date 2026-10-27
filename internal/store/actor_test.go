package store

// Who may write: actors bound to their event and credential, and the
// recovery code (access-control.md sections 2 and 5).

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// A write is bound to the event of whoever makes it (access-control.md
// section 5, rule 4).
func TestWritesAreBoundToTheActorsEvent(t *testing.T) {
	f := newFixture(t)
	if err := f.s.AssignRole(ctx, f.cueIDs[0], f.stylist, "髮型", f.masterBy, at(0, 0)); err != nil {
		t.Fatal(err)
	}
	other, otherBy := otherEvent(t, f.s)
	otherRole, err := f.s.AddRole(ctx, other, "攝影", domain.Media, otherBy, at(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.AssignRole(ctx, f.cueIDs[0], otherRole, "", f.masterBy, at(0, 0)); !errors.Is(err, ErrCrossEvent) {
		t.Errorf("cue and role of different events: err = %v, want ErrCrossEvent", err)
	}
	if _, err := f.s.AddRole(ctx, other, "攝影", domain.Media, f.masterBy, at(0, 0)); !errors.Is(err, ErrCrossEvent) {
		t.Errorf("AddRole on another event: err = %v, want ErrCrossEvent", err)
	}
	if _, err := f.s.AddCue(ctx, other, domain.Cue{Seq: 1, ScheduledStart: at(10, 0)}, f.masterBy, at(0, 0)); !errors.Is(err, ErrCrossEvent) {
		t.Errorf("AddCue on another event: err = %v, want ErrCrossEvent", err)
	}
	if err := f.s.IssueToken(ctx, otherRole, hash("x"), f.masterBy, at(0, 0)); !errors.Is(err, ErrCrossEvent) {
		t.Errorf("IssueToken on another event's role: err = %v, want ErrCrossEvent", err)
	}
	for name, a := range map[string]Actor{"zero": {}, "server": {kind: actorServer}} {
		if _, err := f.s.AddRole(ctx, f.eventID, "x", domain.Media, a, at(0, 0)); !errors.Is(err, ErrNoActor) {
			t.Errorf("%s actor: err = %v, want ErrNoActor", name, err)
		}
	}
	if _, err := f.s.AddRole(ctx, f.eventID, "x", domain.RoleType("master"), f.masterBy, at(0, 0)); !errors.Is(err, ErrRoleType) {
		t.Errorf("unknown role type: err = %v, want ErrRoleType", err)
	}
}

// Only a proven recovery code of the same event replaces the Master link
// (access-control.md section 2); the actor for it comes only from LookupRecoveryCode.
func TestMasterLinkChangesOnlyWithItsRecoveryCode(t *testing.T) {
	f := newFixture(t)
	otherEvent(t, f.s)
	if err := f.s.IssueToken(ctx, f.master, hash("new-master"), f.masterBy, at(1, 0)); !errors.Is(err, ErrMasterRole) {
		t.Errorf("Master reissuing itself: err = %v, want ErrMasterRole", err)
	}
	otherCode, _, err := f.s.LookupRecoveryCode(ctx, hash("recovery-2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.replaceMasterLink(ctx, otherCode, f.eventID, hash("new-master"), at(1, 0)); !errors.Is(err, ErrCrossEvent) {
		t.Errorf("another event's recovery code: err = %v, want ErrCrossEvent", err)
	}
	if _, _, err := f.s.LookupRecoveryCode(ctx, hash("wrong")); !errors.Is(err, ErrInvalidRecoveryCode) {
		t.Errorf("wrong code: err = %v, want ErrInvalidRecoveryCode", err)
	}
	code, eventID, err := f.s.LookupRecoveryCode(ctx, hash("recovery"))
	if err != nil || eventID != f.eventID {
		t.Fatalf("LookupRecoveryCode = %d (%v), want %d", eventID, err, f.eventID)
	}
	if err := f.s.RevokeToken(ctx, f.master, code, at(1, 0)); !errors.Is(err, ErrMasterRole) {
		t.Errorf("revoking the Master link alone: err = %v, want ErrMasterRole", err)
	}
	if err := f.s.IssueToken(ctx, f.master, hash("new-master"), code, at(1, 0)); !errors.Is(err, ErrMasterRole) {
		t.Errorf("IssueToken on the Master with the code: err = %v, want ErrMasterRole", err)
	}
	if err := f.s.ReplaceMasterLink(ctx, hash("recovery"), hash("new-master"), at(1, 0)); err != nil {
		t.Fatalf("recovery code reissue: %v", err)
	}
	if _, err := f.s.LookupToken(ctx, hash("master"), at(12, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("old Master link: err = %v, want ErrInvalidToken", err)
	}
}

// After the event ends the recovery code may still export, and that is
// logged (access-control.md section 2, use 3; section 5, rule 5).
func TestRecoveryExportIsLoggedAfterTheEnd(t *testing.T) {
	f := newFixture(t)
	e, _, _ := f.s.Event(ctx, f.eventID)
	if err := f.s.RecordRecoveryExport(ctx, f.masterBy, e.EndsAt()); !errors.Is(err, ErrNoActor) {
		t.Errorf("role actor: err = %v, want ErrNoActor", err)
	}
	code, _, err := f.s.LookupRecoveryCode(ctx, hash("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.RecordRecoveryExport(ctx, code, e.EndsAt().Add(-time.Second)); !errors.Is(err, ErrNotEnded) {
		t.Errorf("before the end: err = %v, want ErrNotEnded", err)
	}
	if err := f.s.RecordRecoveryExport(ctx, code, e.EndsAt()); err != nil {
		t.Fatal(err)
	}
	var actor, typ string
	if err := f.s.r.QueryRow(`SELECT actor, type FROM action_logs ORDER BY id DESC LIMIT 1`).Scan(&actor, &typ); err != nil ||
		actor != "recovery-code" || typ != "recovery-export" {
		t.Errorf("logged %q %q (%v)", actor, typ, err)
	}
}

// An actor acts only while its credential is valid: a revoked link or a
// purged event's ids cannot be replayed (round 4).
func TestActorNeedsAValidCredential(t *testing.T) {
	f := newFixture(t)
	if err := f.s.IssueToken(ctx, f.stylist, hash("stylist"), f.masterBy, at(1, 0)); err != nil {
		t.Fatal(err)
	}
	owner, err := f.s.LookupToken(ctx, hash("stylist"), at(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	stylistBy := owner.Actor
	if _, err := f.s.AddCue(ctx, f.eventID, domain.Cue{Seq: 7, ScheduledStart: at(12, 0)}, stylistBy, at(1, 0)); err != nil {
		t.Fatalf("valid link: %v", err)
	}
	if err := f.s.RevokeToken(ctx, f.stylist, f.masterBy, at(2, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddCue(ctx, f.eventID, domain.Cue{Seq: 8, ScheduledStart: at(12, 0)}, stylistBy, at(2, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("revoked link: err = %v, want ErrInvalidToken", err)
	}

	// Purge the event, create a new one, and replay the old actors.
	code, _, err := f.s.LookupRecoveryCode(ctx, hash("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	e, _, _ := f.s.Event(ctx, f.eventID)
	if ids, err := f.s.PurgeEnded(ctx, e.PurgeAt()); err != nil || len(ids) != 1 {
		t.Fatalf("purge: %v, %v", ids, err)
	}
	next, err := f.s.CreateEvent(ctx, NewEvent{
		Name: "下一場", Date: domain.Date{Year: 2027, Month: time.December, Day: 1}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: hash("master-next"), RecoveryCodeHash: hash("recovery-next"),
	}, e.PurgeAt())
	if err != nil {
		t.Fatal(err)
	}
	if next == f.eventID {
		t.Errorf("event id %d was given again after the purge", next)
	}
	nextMaster, err := f.s.LookupToken(ctx, hash("master-next"), e.PurgeAt())
	if err != nil {
		t.Fatal(err)
	}
	if nextMaster.RoleID == f.master {
		t.Errorf("role id %d was given again after the purge", nextMaster.RoleID)
	}
	if err := f.s.replaceMasterLink(ctx, code, next, hash("taken"), e.PurgeAt()); err == nil {
		t.Error("old recovery code took over the new event's Master link")
	}
	if _, err := f.s.AddRole(ctx, next, "x", domain.Media, f.masterBy, e.PurgeAt()); err == nil {
		t.Error("old Master actor wrote to the new event")
	}
}

// The recovery code does only its own three things (access-control.md
// section 2), and stops working once the developer resets it (round 4).
func TestRecoveryCodeScope(t *testing.T) {
	f := newFixture(t)
	code, _, err := f.s.LookupRecoveryCode(ctx, hash("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddCue(ctx, f.eventID, domain.Cue{Seq: 9, ScheduledStart: at(12, 0)}, code, at(1, 0)); !errors.Is(err, ErrNoActor) {
		t.Errorf("AddCue: err = %v, want ErrNoActor", err)
	}
	if _, err := f.s.StartEvent(ctx, f.eventID, f.version(t), code, at(9, 0)); !errors.Is(err, ErrNoActor) {
		t.Errorf("StartEvent: err = %v, want ErrNoActor", err)
	}
	if err := f.s.IssueToken(ctx, f.stylist, hash("s"), code, at(1, 0)); !errors.Is(err, ErrNoActor) {
		t.Errorf("non-Master link: err = %v, want ErrNoActor", err)
	}

	if _, err := f.s.w.Exec(`UPDATE events SET recovery_code_hash = ? WHERE id = ?`, hash("reset"), f.eventID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.replaceMasterLink(ctx, code, f.eventID, hash("new-master"), at(1, 0)); !errors.Is(err, ErrInvalidRecoveryCode) {
		t.Errorf("after reset: err = %v, want ErrInvalidRecoveryCode", err)
	}
}

// An actor printed with %#v must not reveal its credential hash (tech-stack.md 2.1「日誌」).
func TestActorDoesNotPrintItsCredential(t *testing.T) {
	f := newFixture(t)
	if got := fmt.Sprintf("%#v %+v %v", f.masterBy, f.masterBy, f.masterBy); strings.Contains(got, string(hash("master"))) || strings.Contains(got, "cred") {
		t.Errorf("printed %q", got)
	}
}

// Even inside another struct's unexported field, an actor must not print
// its credential hash (round 6).
func TestActorInStructDoesNotPrintItsCredential(t *testing.T) {
	f := newFixture(t)
	got := fmt.Sprintf("%v %+v %#v", struct{ a Actor }{f.masterBy}, struct{ a Actor }{f.masterBy}, struct{ a Actor }{f.masterBy})
	if strings.Contains(got, string(hash("master"))) {
		t.Errorf("printed the hash: %q", got)
	}
}
