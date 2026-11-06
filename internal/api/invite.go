package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

var createEventFields = fieldSet{"inviteCode": text, "name": text, "date": text}

// An invited event (US-M-17.3, 17.7): its zone and Master's name are fixed,
// the Master renames the role later; its date lies between today and
// newEventYears ahead, so no stranger's data outlives a lost link for long.
const (
	newEventZone      = "Asia/Taipei"
	newEventMaster    = "總召"
	maxEventNameRunes = 50
	newEventYears     = 2
	// createTimeout ends a creation well before the page gives up at 20 s,
	// so a commit is rarely left without anyone to answer (US-M-17.9).
	createTimeout = 10 * time.Second
)

var newEventLoc = func() *time.Location {
	loc, err := time.LoadLocation(newEventZone) // tzdata is embedded
	if err != nil {
		panic(err)
	}
	return loc
}()

// createEventResponse carries the new Master link's token and the recovery
// code, in this response only (US-M-17.8).
type createEventResponse struct {
	Result       string `json:"result"` // "ok"
	Token        string `json:"token"`
	RecoveryCode string `json:"recoveryCode"`
}

// createEvent is POST /api/events (US-M-17): the invite code in the body,
// never a token. A wrong, used, expired or malformed code answers the same;
// the name and date are checked first, so a typo never uses up the code.
func (a *API) createEvent(w http.ResponseWriter, r *http.Request) {
	// Gate lets a Master's request through; creating an event is never part
	// of checking restored data (US-M-17.10).
	if a.maintenance {
		writeError(w, http.StatusServiceUnavailable, "maintenance")
		return
	}
	b, ok := readBody(w, r, createEventFields)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	now := a.now()
	name, nameOK := domain.CleanLabel(b.name, maxEventNameRunes)
	date, dateOK := newEventDate(b.date, now)
	if !nameOK || !dateOK {
		writeError(w, http.StatusUnprocessableEntity, "invalid_event")
		return
	}
	inviteHash, err := auth.HashRecoveryCode(b.text)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "wrong_invite_code")
		return
	}
	token, code, codeHash, err := newEventSecrets()
	if err != nil {
		a.internal(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), createTimeout)
	defer cancel()
	_, err = a.store.CreateEvent(ctx, store.NewEvent{
		Name: name, Date: date, TimeZone: newEventZone, MasterName: newEventMaster,
		MasterTokenHash: auth.HashToken(token), RecoveryCodeHash: codeHash, InviteHash: inviteHash,
	}, now)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, createEventResponse{Result: "ok", Token: token, RecoveryCode: code})
	case errors.Is(err, store.ErrInvalidInvite):
		writeError(w, http.StatusUnprocessableEntity, "wrong_invite_code")
	default:
		a.internal(w, r, err)
	}
}

// newEventDate parses YYYY-MM-DD and accepts today in the event's zone
// through newEventYears ahead.
func newEventDate(s string, now time.Time) (domain.Date, bool) {
	day, err := time.Parse("2006-01-02", s)
	if err != nil {
		return domain.Date{}, false
	}
	y, m, d := now.In(newEventLoc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if day.Before(today) || day.After(today.AddDate(newEventYears, 0, 0)) {
		return domain.Date{}, false
	}
	return domain.Date{Year: day.Year(), Month: day.Month(), Day: day.Day()}, true
}
