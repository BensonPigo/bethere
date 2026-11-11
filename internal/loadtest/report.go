package loadtest

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"time"
)

// Op is one Master write: when it was sent, when its answer came and the
// eventVersion it made (0 when it failed).
type Op struct {
	Event    int
	Kind     string
	Sent     time.Time
	Answered time.Time
	Version  int64
}

// Sight is a device getting a snapshot with a newer eventVersion.
type Sight struct {
	At      time.Time
	Version int64
}

// Device is what one simulated device saw, in time order.
type Device struct {
	Event  int
	Sights []Sight
}

// Stat sums up some durations.
type Stat struct {
	N   int           `json:"n"`
	P95 time.Duration `json:"p95"`
	Max time.Duration `json:"max"`
}

// Row is one stretch of the run: how long the Masters waited for their
// answers, how long each device took to get each change, and how many
// changes a device never got.
type Row struct {
	Label   string `json:"label"`
	Writes  Stat   `json:"writes"`
	Updates Stat   `json:"updates"`
	Missed  int    `json:"missed"`
}

// Limits are US-O-13.8's pass marks.
type Limits struct {
	Update time.Duration
	Write  time.Duration
}

// Report is the outcome: one row per window, then the whole run.
type Report struct {
	Rows   []Row          `json:"rows"`
	Errors map[string]int `json:"errors"`
	Pass   bool           `json:"pass"`
	Fails  []string       `json:"fails"`
}

// P95 is the nearest-rank 95th percentile; 0 for none.
func P95(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[(len(s)*95+99)/100-1]
}

func stat(ds []time.Duration) Stat {
	if len(ds) == 0 {
		return Stat{}
	}
	return Stat{N: len(ds), P95: P95(ds), Max: slices.Max(ds)}
}

// window gathers one row's durations.
type window struct {
	writes, updates []time.Duration
	missed          int
}

// Analyze puts each successful write in the window its answer came in and
// measures, for every device of its event, the time from that answer to
// the device's first snapshot at least as new; a device that got it before
// the answer took no time. The last row is the whole run.
func Analyze(start time.Time, every time.Duration, ops []Op, devices []Device, errs map[string]int, lim Limits) Report {
	var windows []window
	var all window
	for _, op := range ops {
		if op.Version == 0 {
			continue
		}
		i := int(op.Answered.Sub(start) / every)
		for len(windows) <= i {
			windows = append(windows, window{})
		}
		w := &windows[i]
		w.writes = append(w.writes, op.Answered.Sub(op.Sent))
		for _, d := range devices {
			if d.Event == op.Event {
				w.see(op, d)
			}
		}
	}
	rows := make([]Row, 0, len(windows)+1)
	for i, w := range windows {
		rows = append(rows, w.row(fmt.Sprintf("%v–%v", every*time.Duration(i), every*time.Duration(i+1))))
		all.writes = append(all.writes, w.writes...)
		all.updates = append(all.updates, w.updates...)
		all.missed += w.missed
	}
	rows = append(rows, all.row("all"))
	return judge(rows, errs, lim)
}

func (w *window) see(op Op, d Device) {
	i := sort.Search(len(d.Sights), func(i int) bool { return d.Sights[i].Version >= op.Version })
	if i == len(d.Sights) {
		w.missed++
		return
	}
	w.updates = append(w.updates, max(d.Sights[i].At.Sub(op.Answered), 0))
}

func (w window) row(label string) Row {
	return Row{Label: label, Writes: stat(w.writes), Updates: stat(w.updates), Missed: w.missed}
}

// judge passes a run only when every row is within the limits, no device
// missed a change and nothing failed.
func judge(rows []Row, errs map[string]int, lim Limits) Report {
	r := Report{Rows: rows, Errors: errs}
	for _, row := range rows {
		if row.Updates.P95 > lim.Update {
			r.Fails = append(r.Fails, fmt.Sprintf("%s: updates p95 %v > %v", row.Label, row.Updates.P95, lim.Update))
		}
		if row.Writes.P95 > lim.Write {
			r.Fails = append(r.Fails, fmt.Sprintf("%s: writes p95 %v > %v", row.Label, row.Writes.P95, lim.Write))
		}
		if row.Missed > 0 {
			r.Fails = append(r.Fails, fmt.Sprintf("%s: %d changes never reached a device", row.Label, row.Missed))
		}
	}
	if rows[len(rows)-1].Writes.N == 0 {
		r.Fails = append(r.Fails, "no write succeeded")
	}
	for kind, n := range errs {
		r.Fails = append(r.Fails, fmt.Sprintf("%d errors: %s", n, kind))
	}
	slices.Sort(r.Fails)
	r.Pass = len(r.Fails) == 0
	return r
}

// Print writes the report for people.
func (r Report) Print(w io.Writer) {
	fmt.Fprintf(w, "%-12s %8s %10s %10s %8s %10s %10s %7s\n", "window", "writes", "p95", "max", "updates", "p95", "max", "missed")
	for _, row := range r.Rows {
		fmt.Fprintf(w, "%-12s %8d %10v %10v %8d %10v %10v %7d\n", row.Label,
			row.Writes.N, row.Writes.P95.Round(time.Millisecond), row.Writes.Max.Round(time.Millisecond),
			row.Updates.N, row.Updates.P95.Round(time.Millisecond), row.Updates.Max.Round(time.Millisecond), row.Missed)
	}
	if r.Pass {
		fmt.Fprintln(w, "PASS")
		return
	}
	fmt.Fprintln(w, "FAIL")
	for _, f := range r.Fails {
		fmt.Fprintln(w, "  "+f)
	}
}
