package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Config is one run. Devices poll every Interval from a staggered start,
// like the real client (sync-and-offline.md section 1); each event's
// Master writes every OpEvery, advance and shift in turn, the events
// staggered; every CheckInEvery one of each event's roles checks in or
// out, in turn (0: never). Masters and check-ins stop after Duration;
// devices keep polling for Drain more so the last change can reach them.
type Config struct {
	Links        []Link
	Interval     time.Duration
	OpEvery      time.Duration
	CheckInEvery time.Duration
	Duration     time.Duration
	Drain        time.Duration
	NewClient    func() *http.Client
}

// Result is what a run saw.
type Result struct {
	Start   time.Time
	Ops     []Op
	Devices []Device
	Errors  map[string]int
}

type runner struct {
	cfg  Config
	mu   sync.Mutex
	errs map[string]int
}

func (r *runner) fail(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs[kind]++
}

// Run simulates every link as a device and every Master link also as the
// event's Master.
func Run(ctx context.Context, cfg Config) Result {
	r := &runner{cfg: cfg, errs: map[string]int{}}
	start := time.Now()
	devCtx, stopDevices := context.WithDeadline(ctx, start.Add(cfg.Duration+cfg.Drain))
	defer stopDevices()
	opCtx, stopOps := context.WithDeadline(ctx, start.Add(cfg.Duration))
	defer stopOps()

	devices := make([]Device, len(cfg.Links))
	ops := make([][]Op, len(cfg.Links)) // a Master's writes, by its link
	var wg sync.WaitGroup
	for i := range cfg.Links {
		r.start(devCtx, opCtx, &wg, i, &devices[i], &ops[i])
	}
	r.startCheckIns(opCtx, &wg)
	wg.Wait()
	return Result{Start: start, Devices: devices, Errors: r.errs, Ops: inOrder(ops)}
}

// start runs link i as a device and, for a Master link, as the Master.
func (r *runner) start(devCtx, opCtx context.Context, wg *sync.WaitGroup, i int, d *Device, ops *[]Op) {
	l, cfg := r.cfg.Links[i], r.cfg
	d.Event = l.Event
	offset := cfg.Interval * time.Duration(i) / time.Duration(len(cfg.Links))
	wg.Go(func() { r.device(devCtx, l, offset, d) })
	if l.Master {
		first := 2*cfg.Interval + cfg.OpEvery*time.Duration(l.Event)/time.Duration(eventCount(cfg.Links))
		wg.Go(func() { *ops = r.master(opCtx, l, first) })
	}
}

// inOrder is every Master's writes in the order they were answered.
func inOrder(ops [][]Op) []Op {
	var all []Op
	for _, o := range ops {
		all = append(all, o...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Answered.Before(all[j].Answered) })
	return all
}

func eventCount(links []Link) int {
	events := map[int]bool{}
	for _, l := range links {
		events[l.Event] = true
	}
	return len(events)
}

// every calls f after offset and then every period until ctx ends.
func every(ctx context.Context, offset, period time.Duration, f func()) {
	t := time.NewTimer(offset)
	defer t.Stop()
	for fired(ctx, t) {
		f()
		t.Reset(period)
	}
}

// fired waits for t; false when ctx ends first.
func fired(ctx context.Context, t *time.Timer) bool {
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (r *runner) device(ctx context.Context, l Link, offset time.Duration, d *Device) {
	client := r.cfg.NewClient()
	etag := ""
	var seen int64
	every(ctx, offset, r.cfg.Interval, func() {
		snap, tag, err := r.get(ctx, client, l, etag)
		if err == nil {
			etag = tag
			seen = d.see(snap, seen)
		}
	})
}

// see notes a snapshot newer than seen and answers the newest version.
func (d *Device) see(snap *snapshot, seen int64) int64 {
	if snap == nil || snap.EventVersion <= seen {
		return seen
	}
	d.Sights = append(d.Sights, Sight{At: time.Now(), Version: snap.EventVersion})
	return snap.EventVersion
}

// snapshot is the part of a snapshot the run reads.
type snapshot struct {
	EventVersion int64 `json:"eventVersion"`
	Cues         []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	} `json:"cues"`
}

type answer struct {
	Result   string    `json:"result"`
	Snapshot *snapshot `json:"snapshot"`
}

// get asks for the snapshot; it is nil when unchanged (304). Failures are
// counted, except those of a run that is ending.
func (r *runner) get(ctx context.Context, client *http.Client, l Link, etag string) (*snapshot, string, error) {
	snap, resp, err := r.send(ctx, client, poll(ctx, l, etag), l, "poll")
	if err != nil || snap == nil {
		return nil, etag, err
	}
	return snap, resp.Header.Get("ETag"), nil
}

// poll is the snapshot request, conditional on etag when there is one.
func poll(ctx context.Context, l Link, etag string) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, l.Base+"/api/snapshot", nil)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	return req
}

// send does one request with the link's token. 304 gives no snapshot; any
// answer other than 200 "ok" with a snapshot is counted as an error.
func (r *runner) send(ctx context.Context, client *http.Client, req *http.Request, l Link, what string) (*snapshot, *http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+l.Token)
	resp, err := client.Do(req)
	if err != nil {
		r.failUnlessEnding(ctx, what+" "+transportKind(err))
		return nil, nil, err
	}
	defer resp.Body.Close()
	snap, err := r.answerOf(ctx, resp, what)
	return snap, resp, err
}

// answerOf is the snapshot of an answer, nil for 304; any other answer
// than 200 "ok" is counted.
func (r *runner) answerOf(ctx context.Context, resp *http.Response, what string) (*snapshot, error) {
	if resp.StatusCode == http.StatusNotModified {
		return nil, nil
	}
	snap, err := okSnapshot(resp, what)
	if err != nil {
		r.failUnlessEnding(ctx, err.Error())
	}
	return snap, err
}

// okSnapshot is the snapshot of a 200 "ok" answer; anything else is an
// error naming the status and result.
func okSnapshot(resp *http.Response, what string) (*snapshot, error) {
	var a answer
	err := json.NewDecoder(resp.Body).Decode(&a)
	if err != nil || !a.ok(resp.StatusCode) {
		return nil, fmt.Errorf("%s status %d result %q", what, resp.StatusCode, a.Result)
	}
	return a.Snapshot, nil
}

// ok: a 200 "ok" answer with a snapshot.
func (a answer) ok(status int) bool {
	return status == http.StatusOK && a.Result == "ok" && a.Snapshot != nil
}

func (r *runner) failUnlessEnding(ctx context.Context, kind string) {
	if ctx.Err() == nil {
		r.fail(kind)
	}
}

func transportKind(err error) string {
	var t interface{ Timeout() bool }
	if errors.As(err, &t) && t.Timeout() {
		return "timeout"
	}
	return "transport"
}

// master reads the event's version, then writes after first and every
// OpEvery until ctx ends: advance first, since the first advance on the
// event day starts the event (ADR-0020), then shift and advance in turn.
func (r *runner) master(ctx context.Context, l Link, first time.Duration) []Op {
	client := r.cfg.NewClient()
	snap, _, err := r.get(ctx, client, l, "")
	if err != nil {
		return nil
	}
	var ops []Op
	every(ctx, first, r.cfg.OpEvery, func() {
		kind, body := nextWrite(len(ops), snap)
		op := Op{Event: l.Event, Kind: kind, Sent: time.Now()}
		snap = r.writeOrReread(ctx, client, l, kind, body, &op, snap)
		op.Answered = time.Now()
		if ctx.Err() == nil { // a write cut off by the run's end is no write
			ops = append(ops, op)
		}
	})
	return ops
}

// writeOrReread writes and answers the new snapshot, noting its version
// in op; when the write fails it answers what the server has now, so the
// next write starts from it, or else snap.
func (r *runner) writeOrReread(ctx context.Context, client *http.Client, l Link, kind string, body map[string]any, op *Op, snap *snapshot) *snapshot {
	if s := r.write(ctx, client, l, kind, body); s != nil {
		op.Version = s.EventVersion
		return s
	}
	if s, _, err := r.get(ctx, client, l, ""); err == nil {
		return s
	}
	return snap
}

// nextWrite is the n-th write's endpoint and body: advance, then shift by
// a minute from the first pending cue, in turn.
func nextWrite(n int, snap *snapshot) (string, map[string]any) {
	if n%2 == 0 {
		return "advance", map[string]any{"version": snap.EventVersion}
	}
	return "shift", map[string]any{"version": snap.EventVersion, "fromCueId": firstPending(snap), "deltaMin": 1}
}

// firstPending is the first pending cue's id, 0 when none.
func firstPending(snap *snapshot) int64 {
	for _, c := range snap.Cues {
		if c.Status == "Pending" {
			return c.ID
		}
	}
	return 0
}

func (r *runner) write(ctx context.Context, client *http.Client, l Link, kind string, body map[string]any) *snapshot {
	snap, _, err := r.send(ctx, client, post(ctx, l, "/api/cues/"+kind, body), l, kind)
	if err != nil {
		return nil
	}
	return snap
}

// post is a JSON POST of body to the link's server.
func post(ctx context.Context, l Link, path string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.Base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// startCheckIns starts each event's check-ins, staggered, unless they
// are off.
func (r *runner) startCheckIns(ctx context.Context, wg *sync.WaitGroup) {
	if r.cfg.CheckInEvery <= 0 {
		return
	}
	for event, roles := range rolesByEvent(r.cfg.Links) {
		first := r.cfg.CheckInEvery * time.Duration(event) / time.Duration(eventCount(r.cfg.Links))
		wg.Go(func() { r.checkIns(ctx, roles, first) })
	}
}

func rolesByEvent(links []Link) map[int][]Link {
	roles := map[int][]Link{}
	for _, l := range links {
		if !l.Master {
			roles[l.Event] = append(roles[l.Event], l)
		}
	}
	return roles
}

// checkIns has the event's roles check in and out in turn (US-O-13.5).
// A role whose type does not check in (403) is skipped from then on, not
// counted.
func (r *runner) checkIns(ctx context.Context, roles []Link, first time.Duration) {
	client := r.cfg.NewClient()
	checked := make([]bool, len(roles))
	skip := make([]bool, len(roles))
	n := 0
	every(ctx, first, r.cfg.CheckInEvery, func() {
		for tries := 0; tries < len(roles); tries++ {
			i := n % len(roles)
			n++
			if skip[i] {
				continue
			}
			status := r.checkIn(ctx, client, roles[i], !checked[i])
			skip[i] = status == http.StatusForbidden
			checked[i] = checked[i] != (status == http.StatusOK)
			return
		}
	})
}

// checkIn sets one role's check-in and answers the HTTP status, 0 when
// none came; anything but 200 or 403 is counted.
func (r *runner) checkIn(ctx context.Context, client *http.Client, l Link, in bool) int {
	req := post(ctx, l, "/api/checkin", map[string]bool{"checkedIn": in})
	req.Header.Set("Authorization", "Bearer "+l.Token)
	resp, err := client.Do(req)
	if err != nil {
		r.failUnlessEnding(ctx, "checkin "+transportKind(err))
		return 0
	}
	resp.Body.Close()
	r.countCheckIn(ctx, resp.StatusCode)
	return resp.StatusCode
}

// countCheckIn counts any answer but 200 or 403.
func (r *runner) countCheckIn(ctx context.Context, status int) {
	if status != http.StatusOK && status != http.StatusForbidden {
		r.failUnlessEnding(ctx, fmt.Sprintf("checkin status %d", status))
	}
}
