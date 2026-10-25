package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

const pathRecover = "/api/recover/master"

func (f *faultStore) ReplaceMasterLink(ctx context.Context, codeHash, tokenHash []byte, now time.Time) error {
	return f.around(ctx, now, func(ctx context.Context, now time.Time) error {
		return f.Store.ReplaceMasterLink(ctx, codeHash, tokenHash, now)
	})
}

func recoverBody(code string) string {
	return `{"recoveryCode":` + jsonString(code) + `}`
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// recover sends the code with no token and returns the new link's token
// on success.
func (e *env) recover(code string) (resp, string) {
	e.t.Helper()
	r := e.do(http.MethodPost, pathRecover, "", recoverBody(code))
	tok, _ := r.body["token"].(string)
	return r, tok
}

// US-O-3.3, 3.6: a new Master link; the old one is invalid on its next
// request, the new one opens the console.
func TestUS_O_3_3_RecoveryCodeReplacesTheMasterLink(t *testing.T) {
	e := newEnv(t)
	v := e.version()
	r, tok := e.recover(e.recovery)
	r.wantResult(t, "ok")
	// A link is not a cue: the version stays (api.md section 3).
	if e.version() != v {
		t.Errorf("version %d → %d", v, e.version())
	}
	wantKeys(t, "recover", r.body, "result", "token")
	if auth.CheckToken(tok) != nil {
		t.Fatalf("token %q is not a token", tok)
	}
	e.get(e.master).wantResult(t, "invalid")
	snap := e.get(tok)
	snap.wantResult(t, "ok")
	if role := snap.snapshot(t)["role"].(map[string]any); role["type"] != "Master" {
		t.Errorf("new link opens %v, want the Master", role)
	}
	// Other links are untouched.
	e.get(e.stylist).wantResult(t, "ok")

	// The log names the recovery code, and nothing secret.
	var actor, typ, payload string
	if err := e.db().QueryRow(`SELECT actor, type, payload FROM action_logs ORDER BY id DESC LIMIT 1`).Scan(&actor, &typ, &payload); err != nil {
		t.Fatal(err)
	}
	if actor != "recovery-code" || typ != "token.recover" || payload != fmt.Sprintf(`{"roleId":%d}`, e.masterRole) {
		t.Errorf("logged %s %s %s", actor, typ, payload)
	}
	norm, _ := auth.NormalizeRecoveryCode(e.recovery)
	for _, secret := range []string{tok, e.master, e.recovery, norm} {
		if strings.Contains(payload, secret) || strings.Contains(e.logs.String(), secret) {
			t.Errorf("a secret is in the log: %s", payload)
		}
	}

	// The code still works for the next time (it changes only when reset).
	r2, tok2 := e.recover(e.recovery)
	r2.wantResult(t, "ok")
	e.get(tok).wantResult(t, "invalid")
	e.get(tok2).wantResult(t, "ok")
}

// US-O-3.2: typed from paper, spaces, hyphens and case do not matter.
func TestUS_O_3_2_CodeAsTypedFromPaper(t *testing.T) {
	e := newEnv(t)
	typed := " " + strings.ToLower(strings.ReplaceAll(e.recovery, "-", " - ")) + "\n"
	r, _ := e.recover(typed)
	r.wantResult(t, "ok")
}

// US-O-3.4: a wrong code, a malformed one and another event's are told
// apart by nothing; a wrong code changes nothing.
func TestUS_O_3_4_WrongCodeTellsNothing(t *testing.T) {
	e := newEnv(t)
	other := newCode(t)
	v := e.version()
	var bodies []string
	for _, code := range []string{other, "", "not a code", e.recovery + "0", strings.Repeat("Z", 26)} {
		r, _ := e.recover(code)
		r.wantError(t, http.StatusUnprocessableEntity, "wrong_recovery_code")
		bodies = append(bodies, r.Body.String())
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Errorf("answers differ: %q vs %q", b, bodies[0])
		}
	}
	if e.version() != v {
		t.Error("a wrong code changed the event")
	}
	e.get(e.master).wantResult(t, "ok")

	// A malformed request is not a wrong code.
	for _, b := range []string{``, `{}`, `{"recoveryCode":1}`, `{"recoveryCode":"x","extra":1}`, `{"code":"x"}`} {
		e.do(http.MethodPost, pathRecover, "", b).wantError(t, http.StatusBadRequest, "bad_request")
	}
}

// US-O-3.4: a database error is a server error, never "wrong code".
func TestUS_O_3_4_ServerErrorIsNeverWrongCode(t *testing.T) {
	e := newEnv(t)
	e.fs.writeErr = errors.New("disk I/O error")
	r, _ := e.recover(e.recovery)
	r.wantError(t, http.StatusInternalServerError, "internal")
	e.fs.writeErr = nil

	e.st.Close()
	r, _ = e.recover(e.recovery)
	r.wantError(t, http.StatusInternalServerError, "internal")
}

// US-O-3.5: after the event ends there is no new link.
func TestUS_O_3_5_NoNewLinkAfterTheEnd(t *testing.T) {
	e := newEnv(t)
	ev, _, _ := e.st.Event(bg, e.eventID)
	e.setClock(ev.EndsAt())
	r, tok := e.recover(e.recovery)
	r.wantError(t, http.StatusUnprocessableEntity, "event_ended")
	if tok != "" {
		t.Error("ended answer carries a token")
	}
	var n int
	if err := e.db().QueryRow(`SELECT count(*) FROM access_tokens t JOIN roles r ON r.id = t.role_id
		WHERE r.type = ? AND t.revoked_at IS NULL`, domain.Master).Scan(&n); err != nil || n != 1 {
		t.Errorf("active Master links = %d (%v), want 1", n, err)
	}
	e.setClock(ev.EndsAt().Add(-time.Nanosecond))
	r, _ = e.recover(e.recovery)
	r.wantResult(t, "ok")
}

// US-O-3.3: a code replaces only its own event's Master link.
func TestUS_O_3_3_OnlyTheCodesOwnEvent(t *testing.T) {
	e := newEnv(t)
	otherMaster := newToken(t)
	otherCode := newCode(t)
	h, _ := auth.HashRecoveryCode(otherCode)
	if _, err := e.st.CreateEvent(bg, store.NewEvent{
		Name: "另一場", Date: domain.Date{Year: 2027, Month: time.June, Day: 6}, TimeZone: "Asia/Taipei",
		MasterName: "總召", MasterTokenHash: auth.HashToken(otherMaster), RecoveryCodeHash: h,
	}, e.clock()); err != nil {
		t.Fatal(err)
	}
	r, tok := e.recover(otherCode)
	r.wantResult(t, "ok")
	e.get(e.master).wantResult(t, "ok")
	e.get(otherMaster).wantResult(t, "invalid")
	if ev := e.get(tok).snapshot(t)["event"].(map[string]any); ev["name"] != "另一場" {
		t.Errorf("new link opens %v", ev)
	}
}

// US-O-3.1: the entry needs no token; one sent along, valid or not, is
// never read.
func TestUS_O_3_1_NoTokenNeeded(t *testing.T) {
	e := newEnv(t)
	r := e.do(http.MethodPost, pathRecover, e.stylist, recoverBody(newCode(t)))
	r.wantError(t, http.StatusUnprocessableEntity, "wrong_recovery_code")
	e.get(e.master).wantResult(t, "ok")
	e.get(e.stylist).wantResult(t, "ok")

	r = e.do(http.MethodPost, pathRecover, e.stylist, recoverBody(e.recovery))
	r.wantResult(t, "ok")
	req := e.request(http.MethodPost, pathRecover, "", recoverBody(e.recovery))
	req.Header.Add("Authorization", "Bearer x")
	req.Header.Add("Authorization", "junk")
	e.doReq(req).wantResult(t, "ok")
	e.get(e.stylist).wantResult(t, "ok")
}

func newCode(t *testing.T) string {
	t.Helper()
	c, err := auth.NewRecoveryCode()
	if err != nil {
		t.Fatal(err)
	}
	return c
}
