package loadtest

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const ms = time.Millisecond

func TestUS_O_13_9_P95(t *testing.T) {
	ds := func(n int) []time.Duration {
		var out []time.Duration
		for i := n; i >= 1; i-- { // reversed: P95 sorts
			out = append(out, time.Duration(i)*ms)
		}
		return out
	}
	for _, c := range []struct {
		n    int
		want time.Duration
	}{{0, 0}, {1, 1 * ms}, {19, 19 * ms}, {20, 19 * ms}, {21, 20 * ms}, {100, 95 * ms}, {101, 96 * ms}} {
		if got := P95(ds(c.n)); got != c.want {
			t.Errorf("P95 of 1..%d ms = %v, want %v", c.n, got, c.want)
		}
	}
	in := []time.Duration{3 * ms, 1 * ms, 2 * ms}
	P95(in)
	if in[0] != 3*ms {
		t.Error("P95 sorted its input")
	}
}

var t0 = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

var lim = Limits{Update: 2400 * ms, Write: 400 * ms}

func TestUS_O_13_6_AnalyzeMeasuresEachDevice(t *testing.T) {
	ops := []Op{
		{Event: 1, Sent: at(1000 * ms), Answered: at(1100 * ms), Version: 2},
		{Event: 2, Sent: at(2000 * ms), Answered: at(2300 * ms), Version: 5},
		{Event: 1, Sent: at(11 * time.Second), Answered: at(11*time.Second + 200*ms), Version: 3},
		{Event: 1, Sent: at(12 * time.Second), Answered: at(12*time.Second + 900*ms), Version: 0}, // failed
	}
	devices := []Device{
		// Saw version 2 at 1.6 s, then 3 (skipping nothing) at 13 s.
		{Event: 1, Sights: []Sight{{at(0), 1}, {at(1600 * ms), 2}, {at(13 * time.Second), 3}}},
		// Saw version 2 before the Master's answer came, then jumped to 4.
		{Event: 1, Sights: []Sight{{at(0), 1}, {at(1050 * ms), 2}, {at(12 * time.Second), 4}}},
		// Never saw event 2's version 5.
		{Event: 2, Sights: []Sight{{at(0), 4}}},
	}
	r := Analyze(t0, 10*time.Second, ops, devices, map[string]int{}, lim)
	want := []Row{
		{Label: "0s–10s", Writes: Stat{2, 300 * ms, 300 * ms}, Updates: Stat{2, 500 * ms, 500 * ms}, Missed: 1},
		{Label: "10s–20s", Writes: Stat{1, 200 * ms, 200 * ms}, Updates: Stat{2, 1800 * ms, 1800 * ms}},
		{Label: "all", Writes: Stat{3, 300 * ms, 300 * ms}, Updates: Stat{4, 1800 * ms, 1800 * ms}, Missed: 1},
	}
	if len(r.Rows) != len(want) {
		t.Fatalf("rows = %+v", r.Rows)
	}
	for i := range want {
		if r.Rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, r.Rows[i], want[i])
		}
	}
	if r.Pass {
		t.Error("a missed change passed")
	}
}

func TestUS_O_13_8_PassMarks(t *testing.T) {
	op := func(write, update time.Duration) ([]Op, []Device) {
		return []Op{{Event: 1, Sent: at(0), Answered: at(write), Version: 2}},
			[]Device{{Event: 1, Sights: []Sight{{at(write + update), 2}}}}
	}
	for _, c := range []struct {
		name          string
		write, update time.Duration
		errs          map[string]int
		pass          bool
		fail          string
	}{
		{"at the limits", 400 * ms, 2400 * ms, nil, true, ""},
		{"slow update", 400 * ms, 2401 * ms, nil, false, "all: updates p95 2.401s > 2.4s"},
		{"slow write", 401 * ms, 2400 * ms, nil, false, "all: writes p95 401ms > 400ms"},
		{"an error", 100 * ms, 100 * ms, map[string]int{"poll timeout": 2}, false, "2 errors: poll timeout"},
	} {
		ops, devices := op(c.write, c.update)
		r := Analyze(t0, time.Hour, ops, devices, c.errs, lim)
		if r.Pass != c.pass || (c.fail != "" && !contains(r.Fails, c.fail)) {
			t.Errorf("%s: pass %v, fails %q", c.name, r.Pass, r.Fails)
		}
	}
}

func TestUS_O_13_8_EveryWindowCounts(t *testing.T) {
	ops := []Op{
		{Event: 1, Sent: at(0), Answered: at(100 * ms), Version: 2},
		{Event: 1, Sent: at(10 * time.Second), Answered: at(10*time.Second + 100*ms), Version: 3},
	}
	// The second window's only update is slow.
	sights := []Sight{{at(200 * ms), 2}, {at(13 * time.Second), 3}}
	r := Analyze(t0, 10*time.Second, ops, []Device{{Event: 1, Sights: sights}}, nil, lim)
	if r.Pass || !contains(r.Fails, "10s–20s: updates p95 2.9s > 2.4s") {
		t.Errorf("fails = %q", r.Fails)
	}
}

func TestUS_O_13_8_NoWriteNoPass(t *testing.T) {
	r := Analyze(t0, time.Hour, []Op{{Event: 1, Version: 0}}, nil, nil, lim)
	if r.Pass || !contains(r.Fails, "no write succeeded") {
		t.Errorf("fails = %q", r.Fails)
	}
}

func TestUS_O_13_9_Print(t *testing.T) {
	var b bytes.Buffer
	Report{Rows: []Row{{Label: "all", Writes: Stat{3, 120 * ms, 130 * ms}, Updates: Stat{9, 1500 * ms, 2 * time.Second}}}, Pass: true}.Print(&b)
	for _, want := range []string{"window", "all", "120ms", "1.5s", "PASS"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("lacks %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	Report{Rows: []Row{{Label: "all"}}, Fails: []string{"no write succeeded"}}.Print(&b)
	if !strings.Contains(b.String(), "FAIL\n  no write succeeded") {
		t.Errorf("%s", b.String())
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
