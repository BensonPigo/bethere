package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// logLines decodes the JSON log written to buf.
func logLines(t *testing.T, buf string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line %q: %v", l, err)
		}
		lines = append(lines, m)
	}
	return lines
}

func purgeLog(t *testing.T, purge purgeFunc, now time.Time) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	purgeOnce(context.Background(), purge, now, slog.New(slog.NewJSONHandler(&buf, nil)))
	return logLines(t, buf.String())
}

var purgeNow = time.Date(2027, 9, 1, 0, 0, 0, 0, time.UTC)

// US-O-5.3: the log has the deleted ids, the count and the clock, nothing else.
func TestUS_O_5_3_PurgeLogsIdsAndCountOnly(t *testing.T) {
	lines := purgeLog(t, func(context.Context, time.Time) ([]int64, error) { return []int64{3, 5}, nil }, purgeNow)
	if len(lines) != 1 {
		t.Fatalf("log = %v, want one line", lines)
	}
	l := lines[0]
	if want := []string{"count", "deleted", "level", "msg", "now", "time"}; !slices.Equal(slices.Sorted(maps.Keys(l)), want) {
		t.Errorf("keys = %v, want %v", slices.Sorted(maps.Keys(l)), want)
	}
	if l["msg"] != "purge" || l["level"] != "INFO" || l["count"] != 2.0 || l["now"] != "2027-09-01T00:00:00Z" {
		t.Errorf("log = %v", l)
	}
	if d, _ := l["deleted"].([]any); !slices.Equal(d, []any{3.0, 5.0}) {
		t.Errorf("deleted = %v", l["deleted"])
	}

	// A run that deletes nothing says so, so a day without a line means it never ran.
	lines = purgeLog(t, func(context.Context, time.Time) ([]int64, error) { return nil, nil }, purgeNow)
	if len(lines) != 1 || lines[0]["count"] != 0.0 {
		t.Errorf("empty run log = %v", lines)
	}
}

// US-O-5.3: deletions committed beside a skipped event are logged, and so is the error.
func TestUS_O_5_3_PartialPurgeLogsBoth(t *testing.T) {
	lines := purgeLog(t, func(context.Context, time.Time) ([]int64, error) {
		return []int64{7}, errors.New("event 8: unknown time zone Mars/Base")
	}, purgeNow)
	if len(lines) != 2 || lines[0]["msg"] != "purge" || lines[0]["count"] != 1.0 || lines[1]["level"] != "ERROR" {
		t.Errorf("log = %v, want the deleted id then the error", lines)
	}
}

// US-O-5.3: a failure is only logged.
func TestUS_O_5_3_FailureIsOnlyLogged(t *testing.T) {
	lines := purgeLog(t, func(context.Context, time.Time) ([]int64, error) { return nil, errors.New("database is locked") }, purgeNow)
	if len(lines) != 1 || lines[0]["level"] != "ERROR" || !strings.Contains(lines[0]["err"].(string), "locked") {
		t.Errorf("log = %v, want one error", lines)
	}
}

// A clock far outside the event years never reaches the store.
func TestPurgeRefusesAClockOutOfRange(t *testing.T) {
	for _, now := range []time.Time{time.Unix(0, 0), time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)} {
		called := false
		lines := purgeLog(t, func(context.Context, time.Time) ([]int64, error) { called = true; return nil, nil }, now)
		if called || len(lines) != 1 || lines[0]["level"] != "ERROR" {
			t.Errorf("now %v: called %v, log %v", now, called, lines)
		}
	}
}

// Shutting down mid-run is not a failure.
func TestPurgeCancelledIsQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	purgeOnce(ctx, func(ctx context.Context, _ time.Time) ([]int64, error) { return nil, ctx.Err() }, purgeNow,
		slog.New(slog.NewJSONHandler(&buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("log = %s", buf.String())
	}
}

// US-O-5.1, 5.3: it runs again after a failure, and stops with the server.
func TestUS_O_5_1_PurgeRunsAtStartAndRepeats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		purgeDaily(ctx, func(context.Context, time.Time) ([]int64, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("database is locked")
			}
			return nil, nil
		}, func() time.Time { return purgeNow }, time.Millisecond, slog.New(slog.DiscardHandler))
	}()
	for deadline := time.Now().Add(5 * time.Second); calls.Load() < 3; {
		if time.Now().After(deadline) {
			t.Fatalf("ran %d times", calls.Load())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("still running after cancel")
	}
}
