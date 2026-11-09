package api

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// US-M-14: the Master's own console and a type not opened yet have no
// preview; every opened type does.
func TestUS_M_14_Previewable(t *testing.T) {
	for typ, want := range map[domain.RoleType]error{
		domain.Master:         store.ErrMasterRole,
		domain.RoleType("P1"): store.ErrRoleNotFound,
		domain.Host:           nil,
		domain.Stylist:        nil,
		domain.Media:          nil,
		domain.StageAV:        nil,
		domain.VenueLead:      nil,
		domain.Runner:         nil,
		domain.Reception:      nil,
		domain.Cashier:        nil,
		domain.RoleType(""):   store.ErrRoleNotFound,
	} {
		if got := previewable(typ); !errors.Is(got, want) || (want == nil) != (got == nil) {
			t.Errorf("%q: %v, want %v", typ, got, want)
		}
	}
}

// US-M-14, api.md 10.2: a preview whose caller is not resolved answers as
// the lookup failed; one whose caller is resolved, a console link's
// included (no TokenID), is checked for the capability before its own
// error is answered.
func TestUS_CO_4_8_PreviewAllowed(t *testing.T) {
	a := New(Config{Store: (*store.Store)(nil), Now: time.Now, Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))})
	master := store.TokenOwner{RoleID: 4, RoleType: domain.Master} // a console link: TokenID 0
	stylist := store.TokenOwner{TokenID: 2, RoleID: 5, RoleType: domain.Stylist}
	for _, c := range []struct {
		name   string
		caller store.TokenOwner
		err    error
		ok     bool
		status int
		body   string
	}{
		{"not resolved", store.TokenOwner{}, store.ErrInvalidToken, false, http.StatusOK, `{"result":"invalid"}`},
		{"not the Master", stylist, store.ErrRoleNotFound, false, http.StatusForbidden, `{"error":"forbidden"}`},
		{"no such role", master, store.ErrRoleNotFound, false, http.StatusNotFound, `{"error":"not_found"}`},
		{"members closed", master, store.ErrMembersClosed, false, http.StatusServiceUnavailable, `{"error":"maintenance"}`},
		{"fine", master, nil, true, http.StatusOK, ``},
		{"a stylist without an error", stylist, nil, false, http.StatusForbidden, `{"error":"forbidden"}`},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/roles/preview", nil)
		if got := a.previewAllowed(w, r, c.caller, c.err); got != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, got, c.ok)
		}
		if c.ok {
			if w.Body.Len() != 0 {
				t.Errorf("%s: answered %s", c.name, w.Body.String())
			}
			continue
		}
		if w.Code != c.status || !bytes.Equal(bytes.TrimSpace(w.Body.Bytes()), []byte(c.body)) {
			t.Errorf("%s: %d %s, want %d %s", c.name, w.Code, w.Body.String(), c.status, c.body)
		}
	}
}
