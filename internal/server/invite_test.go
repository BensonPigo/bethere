package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/store"
)

const pathEvents = "/api/events"

func (f *faultStore) CreateEvent(ctx context.Context, ne store.NewEvent, now time.Time) (int64, error) {
	f.recordDeadline(ctx)
	var id int64
	err := f.around(ctx, now, func(ctx context.Context, now time.Time) (err error) {
		id, err = f.Store.CreateEvent(ctx, ne, now)
		return err
	})
	return id, err
}

// invite stores a new invite code as create-invite does.
func (e *env) invite() string {
	e.t.Helper()
	code, err := auth.NewRecoveryCode()
	if err != nil {
		e.t.Fatal(err)
	}
	h, _ := auth.HashRecoveryCode(code)
	if _, err := e.st.CreateInvite(bg, h, e.clock()); err != nil {
		e.t.Fatal(err)
	}
	return code
}

func (e *env) createEvent(token string, fields map[string]any) resp {
	e.t.Helper()
	return e.post(pathEvents, token, fields)
}

func eventFields(code, name, date string) map[string]any {
	return map[string]any{"inviteCode": code, "name": name, "date": date}
}

// US-M-17.3–17.5, 17.8: the code, typed loosely, creates a draft event whose
// Master link opens the console as 總召 and whose recovery code works;
// nothing secret reaches the log.
func TestUS_M_17_5_InviteCreatesAWorkingEvent(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	typed := " " + strings.ToLower(strings.ReplaceAll(code, "-", "")) + " "
	r := e.createEvent("", eventFields(typed, "  小明與小華的婚禮 ", "2027-06-06"))
	r.wantResult(t, "ok")
	wantKeys(t, "create", r.body, "result", "token", "recoveryCode")
	tok, _ := r.body["token"].(string)
	rc, _ := r.body["recoveryCode"].(string)
	if auth.CheckToken(tok) != nil {
		t.Fatalf("token %q", tok)
	}
	snap := e.snap(tok)
	ev, _ := snap["event"].(map[string]any)
	if ev["name"] != "小明與小華的婚禮" || ev["date"] != "2027-06-06" || ev["timezone"] != "Asia/Taipei" || snap["eventStatus"] != "Draft" {
		t.Errorf("event = %v, status %v", ev, snap["eventStatus"])
	}
	roles, _ := snap["roles"].([]any)
	if len(roles) != 1 || roles[0].(map[string]any)["name"] != "總召" || roles[0].(map[string]any)["type"] != "Master" {
		t.Errorf("roles = %v", roles)
	}
	rec, newTok := e.recover(rc)
	rec.wantResult(t, "ok")
	e.get(newTok).wantResult(t, "ok")

	e.createEvent("", eventFields(code, "再一場", "2027-06-06")).wantError(t, http.StatusUnprocessableEntity, "wrong_invite_code")
	for _, secret := range []string{code, tok, rc, newTok} {
		if strings.Contains(e.logs.String(), secret) {
			t.Errorf("log holds a secret: %s", e.logs.String())
		}
	}
}

// US-M-17.6: wrong, malformed and expired codes answer alike.
func TestUS_M_17_6_BadCodesAnswerAlike(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	other, _ := auth.NewRecoveryCode()
	for _, c := range []string{other, "", "x", code + "0"} {
		e.createEvent("", eventFields(c, "婚禮", "2027-06-06")).wantError(t, http.StatusUnprocessableEntity, "wrong_invite_code")
	}
	e.setClock(e.clock().Add(store.InviteLife))
	e.createEvent("", eventFields(code, "婚禮", "2027-08-01")).wantError(t, http.StatusUnprocessableEntity, "wrong_invite_code")
}

// US-M-17.7: a bad name or date is refused before the code is touched, so
// the code still works afterwards.
func TestUS_M_17_7_BadNameOrDateKeepsTheCode(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	// The clock is 2027-06-06 09:00 in Taipei.
	for _, f := range [][2]string{
		{"", "2027-06-06"}, {"   ", "2027-06-06"}, {strings.Repeat("婚", 51), "2027-06-06"},
		{"婚‮禮", "2027-06-06"}, {"婚\n禮", "2027-06-06"},
		{"婚禮", "2027-06-05"}, {"婚禮", "2029-06-07"}, {"婚禮", "2027-02-30"},
		{"婚禮", "2027-6-6"}, {"婚禮", ""}, {"婚禮", "2027-06-06T00:00:00Z"},
	} {
		r := e.createEvent("", eventFields(code, f[0], f[1]))
		if r.Code != http.StatusUnprocessableEntity || r.body["error"] != "invalid_event" {
			t.Errorf("%q %q: %d %s", f[0], f[1], r.Code, r.Body.String())
		}
	}
	e.createEvent("", eventFields(code, strings.Repeat("婚", 50), "2029-06-06")).wantResult(t, "ok")
}

// US-M-17.7: "today" is Taipei's: at 00:30 there, yesterday is refused
// though the event of yesterday has not ended yet.
func TestUS_M_17_7_TodayIsTaipeis(t *testing.T) {
	e := newEnv(t)
	e.setClock(at(0, 30))
	code := e.invite()
	e.createEvent("", eventFields(code, "婚禮", "2027-06-05")).wantError(t, http.StatusUnprocessableEntity, "invalid_event")
	e.createEvent("", eventFields(code, "婚禮", "2027-06-06")).wantResult(t, "ok")
}

// The body takes exactly the three fields (api.md section 1).
func TestUS_M_17_4_BodyIsExact(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	for _, b := range []string{
		`{"inviteCode":"` + code + `","name":"婚禮"}`,
		`{"inviteCode":"` + code + `","name":"婚禮","date":"2027-06-06","masterName":"我"}`,
		`{"recoveryCode":"` + code + `","name":"婚禮","date":"2027-06-06"}`,
		`{"inviteCode":1,"name":"婚禮","date":"2027-06-06"}`,
	} {
		e.do(http.MethodPost, pathEvents, "", b).wantError(t, http.StatusBadRequest, "bad_request")
	}
	e.createEvent("", eventFields(code, "婚禮", "2027-06-06")).wantResult(t, "ok")
}

// US-M-17.10: in maintenance even a Master's link cannot create an event.
func TestUS_M_17_10_MaintenanceRefusesEvenTheMaster(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	open := e.h
	e.maintenance()
	e.createEvent(e.master, eventFields(code, "婚禮", "2027-06-06")).wantMaintenance(t, "Master creating an event")
	e.createEvent("", eventFields(code, "婚禮", "2027-06-06")).wantMaintenance(t, "creating an event")
	// The code was not used.
	e.h = open
	e.createEvent("", eventFields(code, "婚禮", "2027-06-06")).wantResult(t, "ok")
}

// US-M-17.6: a database failure is a server error, never "wrong code".
func TestUS_M_17_6_StoreErrorIsNotWrongCode(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	e.fs.writeErr = errors.New("disk I/O error")
	r := e.createEvent("", eventFields(code, "婚禮", "2027-06-06"))
	r.wantError(t, http.StatusInternalServerError, "internal")
	e.fs.writeErr = nil
	e.createEvent("", eventFields(code, "婚禮", "2027-06-06")).wantResult(t, "ok")
}

// US-M-17.9: the creation ends well before the page gives up at 20 s.
func TestUS_M_17_9_CreationIsBounded(t *testing.T) {
	e := newEnv(t)
	code := e.invite()
	e.createEvent("", eventFields(code, "婚禮", "2027-06-06")).wantResult(t, "ok")
	if len(e.fs.deadlines) != 1 || e.fs.deadlines[0] <= 0 || e.fs.deadlines[0] > 10*time.Second {
		t.Errorf("deadlines = %v, want one of at most 10 s", e.fs.deadlines)
	}
}
