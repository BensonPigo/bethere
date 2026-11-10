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
// staggered. Masters stop after Duration; devices keep polling for Drain
// more so the last change can reach them.
type Config struct {
	Links     []Link
	Interval  time.Duration
	OpEvery   time.Duration
	Duration  time.Duration
	Drain     time.Duration
	NewClient func() *http.Client
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
	for i, l := range cfg.Links {
		devices[i].Event = l.Event
		offset := cfg.Interval * time.Duration(i) / time.Duration(len(cfg.Links))
		wg.Go(func() { r.device(devCtx, l, offset, &devices[i]) })
		if l.Master {
			first := 2*cfg.Interval + cfg.OpEvery*time.Duration(l.Event)/time.Duration(eventCount(cfg.Links))
			wg.Go(func() { ops[i] = r.master(opCtx, l, first) })
		}
	}
	wg.Wait()
	res := Result{Start: start, Devices: devices, Errors: r.errs}
	for _, o := range ops {
		res.Ops = append(res.Ops, o...)
	}
	sort.Slice(res.Ops, func(i, j int) bool { return res.Ops[i].Answered.Before(res.Ops[j].Answered) })
	return res
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f()
			t.Reset(period)
		}
	}
}

func (r *runner) device(ctx context.Context, l Link, offset time.Duration, d *Device) {
	client := r.cfg.NewClient()
	etag := ""
	var seen int64
	every(ctx, offset, r.cfg.Interval, func() {
		snap, tag, err := r.get(ctx, client, l, etag)
		if err != nil {
			return
		}
		etag = tag
		if snap != nil && snap.EventVersion > seen {
			seen = snap.EventVersion
			d.Sights = append(d.Sights, Sight{At: time.Now(), Version: seen})
		}
	})
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.Base+"/api/snapshot", nil)
	if err != nil {
		return nil, "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	snap, resp, err := r.send(ctx, client, req, l, "poll")
	if err != nil {
		return nil, "", err
	}
	if snap == nil {
		return nil, etag, nil
	}
	return snap, resp.Header.Get("ETag"), nil
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
	if resp.StatusCode == http.StatusNotModified {
		return nil, resp, nil
	}
	var a answer
	err = json.NewDecoder(resp.Body).Decode(&a)
	if resp.StatusCode != http.StatusOK || err != nil || a.Result != "ok" || a.Snapshot == nil {
		kind := fmt.Sprintf("%s status %d result %q", what, resp.StatusCode, a.Result)
		r.failUnlessEnding(ctx, kind)
		return nil, nil, errors.New(kind)
	}
	return a.Snapshot, resp, nil
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
		if s := r.write(ctx, client, l, kind, body); s != nil {
			snap, op.Version = s, s.EventVersion
		} else if s, _, err := r.get(ctx, client, l, ""); err == nil {
			snap = s // the next write starts from what the server has
		}
		op.Answered = time.Now()
		ops = append(ops, op)
	})
	return ops
}

// nextWrite is the n-th write's endpoint and body: advance, then shift by
// a minute from the first pending cue, in turn.
func nextWrite(n int, snap *snapshot) (string, map[string]any) {
	if n%2 == 0 {
		return "advance", map[string]any{"version": snap.EventVersion}
	}
	var from int64
	for _, c := range snap.Cues {
		if c.Status == "Pending" {
			from = c.ID
			break
		}
	}
	return "shift", map[string]any{"version": snap.EventVersion, "fromCueId": from, "deltaMin": 1}
}

func (r *runner) write(ctx context.Context, client *http.Client, l Link, kind string, body map[string]any) *snapshot {
	b, err := json.Marshal(body)
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.Base+"/api/cues/"+kind, bytes.NewReader(b))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	snap, _, err := r.send(ctx, client, req, l, kind)
	if err != nil {
		return nil
	}
	return snap
}
