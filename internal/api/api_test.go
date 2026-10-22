package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// lastSeenAt is updated in memory only after the token was proved (tech-stack.md 2.1「lastSeenAt」):
// not for a malformed header, an unknown token or an ended event.
func TestLastSeenOnlyAfterSuccessfulAuthentication(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "x.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	loc, _ := time.LoadLocation("Asia/Taipei")
	now := time.Date(2027, 6, 6, 9, 0, 0, 0, loc)
	master, _ := auth.NewToken()
	stylist, _ := auth.NewToken()
	if _, err := st.CreateEvent(ctx, store.NewEvent{
		Name: "x", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: auth.HashToken(master), RecoveryCodeHash: auth.HashToken("r"),
	}, now.AddDate(0, -1, 0)); err != nil {
		t.Fatal(err)
	}
	owner, _ := st.LookupToken(ctx, auth.HashToken(master), now)
	role, err := st.AddRole(ctx, owner.EventID, "新秘", domain.Stylist, owner.Actor, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.IssueToken(ctx, role, auth.HashToken(stylist), owner.Actor, now); err != nil {
		t.Fatal(err)
	}

	clock := now
	a := New(Config{Store: st, Now: func() time.Time { return clock }, Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))})
	mux := http.NewServeMux()
	a.Register(mux)
	send := func(method, path, token, body string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code
	}

	unknown, _ := auth.NewToken()
	send(http.MethodGet, "/api/snapshot", unknown, "")
	send(http.MethodGet, "/api/snapshot", master[:21], "")
	if _, ok := a.LastSeen(auth.HashToken(unknown)); ok {
		t.Error("unknown token recorded")
	}
	if _, ok := a.LastSeen(auth.HashToken(master)); ok {
		t.Error("recorded before any successful request")
	}

	send(http.MethodGet, "/api/snapshot", master, "")
	if at, ok := a.LastSeen(auth.HashToken(master)); !ok || !at.Equal(now) {
		t.Errorf("master last seen %v %v", at, ok)
	}
	// A refused write still proved the link.
	clock = now.Add(time.Minute)
	if code := send(http.MethodPost, "/api/cues/advance", stylist, `{"version":1}`); code != http.StatusForbidden {
		t.Fatalf("stylist advance: %d", code)
	}
	if at, ok := a.LastSeen(auth.HashToken(stylist)); !ok || !at.Equal(clock) {
		t.Errorf("stylist last seen %v %v", at, ok)
	}
	// After the end nothing is recorded.
	clock = now.AddDate(0, 0, 2)
	send(http.MethodGet, "/api/snapshot", master, "")
	if at, _ := a.LastSeen(auth.HashToken(master)); !at.Equal(now) {
		t.Errorf("ended event updated last seen to %v", at)
	}
}

func TestFormatTimeIsUTCWithMilliseconds(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Taipei")
	for in, want := range map[time.Time]string{
		time.Date(2027, 6, 6, 11, 15, 0, 0, loc):             "2027-06-06T03:15:00.000Z",
		time.Date(2027, 6, 6, 3, 15, 0, 123456789, time.UTC): "2027-06-06T03:15:00.123Z",
	} {
		if got := FormatTime(in); got != want {
			t.Errorf("FormatTime(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestCapabilityTableDeniesByDefault(t *testing.T) {
	for _, c := range []capability{capReadSnapshot, capAdvance, capUndo, capShift, capability(99)} {
		for _, typ := range []domain.RoleType{"", "master", "MASTER", "Admin", domain.Media, domain.StageAV,
			domain.VenueLead, domain.Reception, domain.Cashier, domain.Runner} {
			if allowed(c, typ) {
				t.Errorf("allowed(%d, %q)", c, typ)
			}
		}
	}
	// Host reads its dashboard and nothing else (api.md section 4).
	for _, c := range []capability{capAdvance, capUndo, capShift} {
		if allowed(c, domain.Host) || allowed(c, domain.Stylist) {
			t.Errorf("capability %d allowed for Host or Stylist", c)
		}
	}
	if !allowed(capReadSnapshot, domain.Host) {
		t.Error("Host cannot read its dashboard")
	}
	if allowed(capability(99), domain.Master) {
		t.Error("an unknown capability is allowed for the Master")
	}
}
