package server

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BensonPigo/bethere/internal/api"
)

// maintenance restarts the handler with maintenance mode on, as a restart
// of serve would: the database stays, lastSeenAt starts empty.
func (e *env) maintenance() {
	e.h = New(Config{
		Static:      fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>BeThere</title>")}},
		Store:       e.fs,
		Now:         e.clock,
		Version:     "test-1",
		Logger:      slog.New(slog.NewJSONHandler(e.logs, nil)),
		Maintenance: true,
	})
}

// apiRoutes is every route api.Register adds, read from the registration
// itself so a new endpoint is covered without editing this test.
func apiRoutes(t *testing.T, e *env) [][2]string {
	t.Helper()
	a := api.New(api.Config{Store: e.fs, Now: e.clock, Logger: slog.New(slog.DiscardHandler)})
	a.Register(http.NewServeMux())
	var out [][2]string
	for _, p := range a.Routes() {
		method, path, ok := strings.Cut(p, " ")
		if !ok {
			t.Fatalf("pattern %q has no method", p)
		}
		out = append(out, [2]string{method, path})
	}
	if !slices.Contains(a.Routes(), "GET /api/snapshot") {
		t.Fatalf("routes not recorded: %v", a.Routes())
	}
	return out
}

func (r resp) wantMaintenance(t *testing.T, what string) {
	t.Helper()
	if r.Code != http.StatusServiceUnavailable || r.body["error"] != "maintenance" || len(r.body) != 1 {
		t.Errorf("%s: got %d %s, want 503 maintenance", what, r.Code, r.Body.String())
	}
}

// US-O-2.2: under maintenance every route refuses every caller but the
// Master, with an answer a device treats as offline: never "invalid" or
// "ended", which would clear it, and no event data.
func TestUS_O_2_2_OnlyTheMasterGetsThrough(t *testing.T) {
	e := newEnv(t)
	e.start()
	host := e.addRoleWithToken("Host")
	gone := newToken(t) // a link missing from a restored database
	e.maintenance()
	v := e.version()

	callers := map[string]func(*http.Request){
		"stylist":      func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+e.stylist) },
		"host":         func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+host) },
		"unknown link": func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+gone) },
		"no token":     func(*http.Request) {},
		"malformed":    func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") },
		"two headers": func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer "+e.master)
			r.Header.Add("Authorization", "Bearer "+e.master)
		},
		"basic master": func(r *http.Request) { r.Header.Set("Authorization", "Basic "+e.master) },
	}
	for _, rt := range apiRoutes(t, e) {
		if rt[1] == "/api/recover/master" {
			continue
		}
		for name, set := range callers {
			req := e.request(rt[0], rt[1], "", "{}")
			set(req)
			r := e.doReq(req)
			r.wantMaintenance(t, rt[0]+" "+rt[1]+" as "+name)
			if strings.Contains(r.Body.String(), "婚宴") {
				t.Errorf("%s %s as %s: event data in %s", rt[0], rt[1], name, r.Body.String())
			}
		}
	}
	if e.version() != v {
		t.Error("a refused request changed the event")
	}

	// Paths the router does not know, other methods and unclean spellings.
	for _, rq := range [][2]string{
		{"GET", "/api/ping"}, {"GET", "/api/nope"}, {"GET", "/api"}, {"HEAD", "/api/snapshot"},
		{"GET", "/api/recover/master"}, {"DELETE", "/api/snapshot"}, {"GET", "/x/../api/snapshot"},
		{"GET", "/api/./snapshot"}, {"GET", "/api//snapshot"},
	} {
		r := e.do(rq[0], rq[1], e.stylist, "")
		if rq[0] == "HEAD" {
			if r.Code != http.StatusServiceUnavailable {
				t.Errorf("HEAD: %d", r.Code)
			}
			continue
		}
		r.wantMaintenance(t, rq[0]+" "+rq[1])
	}

	// The Master works as usual and sees the flag (US-M-9.2, US-O-2.5).
	snap := e.get(e.master)
	snap.wantResult(t, "ok")
	if m := snap.snapshot(t); m["maintenance"] != true {
		t.Errorf("maintenance = %v, want true", m["maintenance"])
	}
	e.write(pathAdvance, e.master).wantResult(t, "ok")
	if e.version() == v {
		t.Error("the Master's advance did not apply")
	}
}

// US-O-2.2: an ended event, even the Master's, is refused rather than
// told "ended".
func TestUS_O_2_2_EndedIsRefusedNotEnded(t *testing.T) {
	e := newEnv(t)
	e.maintenance()
	ev, _, _ := e.st.Event(bg, e.eventID)
	e.setClock(ev.EndsAt())
	e.get(e.master).wantMaintenance(t, "ended Master")
	e.get(e.stylist).wantMaintenance(t, "ended stylist")
}

// US-O-2.2: a database error never lets anyone through.
func TestUS_O_2_2_LookupErrorRefuses(t *testing.T) {
	e := newEnv(t)
	e.maintenance()
	e.fs.lookupErr = errors.New("disk I/O error")
	e.get(e.master).wantMaintenance(t, "Master with a failing lookup")
}

// US-O-2.2: the recovery entry still replaces a lost Master link, and the
// new link gets in.
func TestUS_O_2_2_RecoveryStillWorks(t *testing.T) {
	e := newEnv(t)
	e.maintenance()
	r, tok := e.recover(e.recovery)
	r.wantResult(t, "ok")
	e.get(tok).wantResult(t, "ok")
	e.get(e.master).wantMaintenance(t, "replaced Master link")
}

// US-O-2.4: a refused link is never marked as seen.
func TestUS_O_2_4_RefusedLinksAreNotSeen(t *testing.T) {
	e := newEnv(t)
	e.maintenance()
	e.get(e.stylist).wantMaintenance(t, "stylist")
	for _, role := range e.get(e.master).snapshot(t)["roles"].([]any) {
		r := role.(map[string]any)
		switch r["type"] {
		case "Master":
			if r["lastSeenAt"] == nil {
				t.Error("the Master is not seen")
			}
		default:
			if r["lastSeenAt"] != nil {
				t.Errorf("%v seen while refused", r["name"])
			}
		}
	}
}

// US-O-2.3: the health check, the page and its files answer as usual.
func TestUS_O_2_3_HealthAndPagesStayUp(t *testing.T) {
	e := newEnv(t)
	e.maintenance()
	for _, p := range []string{"/healthz", "/support.json", "/", "/recover", "/print", "/new"} {
		if r := e.do(http.MethodGet, p, "", ""); r.Code != http.StatusOK {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
}
