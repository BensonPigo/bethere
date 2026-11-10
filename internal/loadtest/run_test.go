package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI is a server with one event per Master token: it answers
// snapshots with an ETag of the version, and advance and shift when the
// version matches, raising it.
type fakeAPI struct {
	mu       sync.Mutex
	versions map[string]int64 // by Master token
	eventOf  map[string]string
	writes   []string
	bodies   []map[string]any
	status   int             // when set, every answer
	checkIns []string        // "<token> <true|false>"
	noCheck  map[string]bool // tokens whose check-in is forbidden
	checkErr int             // when set, every check-in's answer
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	master, ok := f.eventOf[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		fmt.Fprint(w, `{"result":"invalid"}`)
		return
	}
	if r.URL.Path == "/api/checkin" {
		f.checkIn(w, r)
		return
	}
	if r.Method == http.MethodPost {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["version"] != float64(f.versions[master]) {
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":"version_conflict"}`)
			return
		}
		f.versions[master]++
		f.writes = append(f.writes, r.URL.Path)
		f.bodies = append(f.bodies, body)
	}
	tag := fmt.Sprintf(`"%d"`, f.versions[master])
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", tag)
	fmt.Fprintf(w, `{"result":"ok","snapshot":{"eventVersion":%d,"cues":[{"id":7,"status":"Done"},{"id":8,"status":"Pending"},{"id":9,"status":"Pending"}]}}`, f.versions[master])
}

func (f *fakeAPI) checkIn(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch {
	case f.checkErr != 0:
		w.WriteHeader(f.checkErr)
	case f.noCheck[tok]:
		w.WriteHeader(http.StatusForbidden)
	default:
		var body map[string]bool
		json.NewDecoder(r.Body).Decode(&body)
		f.checkIns = append(f.checkIns, fmt.Sprintf("%s %v", tok, body["checkedIn"]))
		fmt.Fprint(w, `{"result":"ok","snapshot":{}}`)
	}
}

func token(event, i int) string { return fmt.Sprintf("%02d%020d", event, i) }

// newFake serves events of n devices each, the first the Master.
func newFake(t *testing.T, events, n int) (*fakeAPI, *httptest.Server, []Link) {
	f := &fakeAPI{versions: map[string]int64{}, eventOf: map[string]string{}}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	var links []Link
	for e := 1; e <= events; e++ {
		f.versions[token(e, 0)] = 1
		for i := 0; i < n; i++ {
			f.eventOf[token(e, i)] = token(e, 0)
			links = append(links, Link{Event: e, Master: i == 0, Base: srv.URL, Token: token(e, i)})
		}
	}
	return f, srv, links
}

func quick(links []Link, srv *httptest.Server) Config {
	return Config{Links: links, Interval: 20 * ms, OpEvery: 60 * ms, Duration: 400 * ms, Drain: 100 * ms,
		NewClient: func() *http.Client { return srv.Client() }}
}

func TestUS_O_13_4_RunDevicesSeeEveryChange(t *testing.T) {
	f, srv, links := newFake(t, 3, 5)
	res := Run(context.Background(), quick(links, srv))
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	perEvent := map[int]int{}
	for i, op := range res.Ops {
		if op.Version == 0 || op.Answered.Before(op.Sent) {
			t.Errorf("op %d = %+v", i, op)
		}
		if i > 0 && op.Answered.Before(res.Ops[i-1].Answered) {
			t.Error("ops are not in the order answered")
		}
		perEvent[op.Event]++
	}
	// 400 ms of writes every 60 ms from 40 ms on (plus the stagger): about
	// 5 each; at least 3 each leaves room for a slow machine.
	for e := 1; e <= 3; e++ {
		if perEvent[e] < 3 {
			t.Errorf("event %d had %d writes", e, perEvent[e])
		}
	}
	for i, d := range res.Devices {
		last := d.Sights[len(d.Sights)-1].Version
		if want := f.versions[token(d.Event, 0)]; last != want {
			t.Errorf("device %d last saw %d, the event is at %d", i, last, want)
		}
	}
	r := Analyze(res.Start, time.Hour, res.Ops, res.Devices, res.Errors, Limits{Update: time.Second, Write: time.Second})
	if !r.Pass {
		t.Errorf("fails: %q", r.Fails)
	}
}

// ADR-0020: the first write is an advance, then shift by a minute from the
// first pending cue and advance in turn, each with the version it last got.
func TestUS_O_13_5_MasterWritesInTurn(t *testing.T) {
	f, srv, links := newFake(t, 1, 1)
	Run(context.Background(), quick(links, srv))
	if len(f.writes) < 3 {
		t.Fatalf("writes = %v", f.writes)
	}
	for i, path := range f.writes {
		want := map[int]string{0: "/api/cues/advance", 1: "/api/cues/shift"}[i%2]
		if path != want {
			t.Errorf("write %d = %s, want %s", i, path, want)
		}
		if f.bodies[i]["version"] != float64(i+1) {
			t.Errorf("write %d version %v", i, f.bodies[i]["version"])
		}
	}
	if s := f.bodies[1]; s["fromCueId"] != float64(8) || s["deltaMin"] != float64(1) || len(s) != 3 {
		t.Errorf("shift body = %v", s)
	}
	if len(f.bodies[0]) != 1 {
		t.Errorf("advance body = %v", f.bodies[0])
	}
}

func TestUS_O_13_8_RunCountsFailures(t *testing.T) {
	f, srv, links := newFake(t, 1, 2)
	f.status = http.StatusInternalServerError
	res := Run(context.Background(), quick(links, srv))
	if res.Errors["poll status 500 result \"\""] == 0 || len(res.Ops) != 0 {
		t.Errorf("errors %v, ops %v", res.Errors, res.Ops)
	}
}

// A write that loses to another device's write (409) is counted, and the
// Master starts over from the server's version.
func TestUS_O_13_8_MasterRecoversFromAConflict(t *testing.T) {
	f, srv, links := newFake(t, 1, 1)
	cfg := quick(links, srv)
	go func() {
		time.Sleep(50 * ms) // after the Master read the version, before its first write
		f.mu.Lock()
		f.versions[token(1, 0)] += 10
		f.mu.Unlock()
	}()
	res := Run(context.Background(), cfg)
	if res.Errors["shift status 409 result \"\""]+res.Errors["advance status 409 result \"\""] != 1 {
		t.Errorf("errors = %v", res.Errors)
	}
	var after int
	for _, op := range res.Ops {
		if op.Version > 11 {
			after++
		}
	}
	if after == 0 {
		t.Errorf("no write after the conflict: %+v", res.Ops)
	}
}

func TestUS_O_13_8_TransportFailures(t *testing.T) {
	_, srv, links := newFake(t, 1, 1)
	srv.Close()
	res := Run(context.Background(), quick(links, srv))
	if res.Errors["poll transport"] == 0 {
		t.Errorf("errors = %v", res.Errors)
	}
}

func TestUS_O_13_8_Timeouts(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() { close(block); srv.Close() })
	links := []Link{{Event: 1, Master: true, Base: srv.URL, Token: token(1, 0)}}
	cfg := quick(links, srv)
	cfg.NewClient = func() *http.Client { c := *srv.Client(); c.Timeout = 30 * ms; return &c }
	res := Run(context.Background(), cfg)
	if res.Errors["poll timeout"] == 0 {
		t.Errorf("errors = %v", res.Errors)
	}
}

func TestUS_O_13_5_NextWriteWithNoPendingCue(t *testing.T) {
	kind, body := nextWrite(1, &snapshot{EventVersion: 4})
	if kind != "shift" || body["fromCueId"] != int64(0) || body["version"] != int64(4) {
		t.Errorf("%s %v", kind, body)
	}
}

// US-O-13.5: each event's roles check in and out in turn; a role that may
// not check in is skipped from then on and not counted.
func TestUS_O_13_5_RolesCheckInInTurn(t *testing.T) {
	f, srv, links := newFake(t, 2, 3) // per event: Master and roles 1, 2
	f.noCheck = map[string]bool{token(1, 1): true}
	cfg := quick(links, srv)
	cfg.CheckInEvery = 25 * ms
	res := Run(context.Background(), cfg)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	var e1, e2 []string
	for _, c := range f.checkIns {
		if strings.HasPrefix(c, token(1, 0)[:2]) {
			e1 = append(e1, c)
		} else {
			e2 = append(e2, c)
		}
	}
	// Event 1: role 1 is refused once, then only role 2, in and out.
	want1 := []string{token(1, 2) + " true", token(1, 2) + " false", token(1, 2) + " true"}
	want2 := []string{token(2, 1) + " true", token(2, 2) + " true", token(2, 1) + " false", token(2, 2) + " false"}
	for i := range want1 {
		if i >= len(e1) || e1[i] != want1[i] {
			t.Fatalf("event 1 check-ins %v, want %v first", e1, want1)
		}
	}
	for i := range want2 {
		if i >= len(e2) || e2[i] != want2[i] {
			t.Fatalf("event 2 check-ins %v, want %v first", e2, want2)
		}
	}
	for _, c := range f.checkIns {
		if strings.HasPrefix(c, token(1, 0)) || strings.HasPrefix(c, token(2, 0)) {
			t.Errorf("a Master checked in: %s", c)
		}
	}
}

func TestUS_O_13_8_CheckInFailuresCount(t *testing.T) {
	f, srv, links := newFake(t, 1, 2)
	f.checkErr = http.StatusInternalServerError
	cfg := quick(links, srv)
	cfg.CheckInEvery = 25 * ms
	res := Run(context.Background(), cfg)
	if res.Errors["checkin status 500"] == 0 {
		t.Errorf("errors = %v", res.Errors)
	}
	srv.Close()
	res = Run(context.Background(), cfg)
	if res.Errors["checkin transport"] == 0 {
		t.Errorf("errors = %v", res.Errors)
	}
}

func TestUS_O_13_5_AllRolesRefuseCheckIn(t *testing.T) {
	f, srv, links := newFake(t, 1, 2)
	f.noCheck = map[string]bool{token(1, 1): true}
	cfg := quick(links, srv)
	cfg.CheckInEvery = 25 * ms
	if res := Run(context.Background(), cfg); len(res.Errors) != 0 || len(f.checkIns) != 0 {
		t.Errorf("errors %v, check-ins %v", res.Errors, f.checkIns)
	}
}
