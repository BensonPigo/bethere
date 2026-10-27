//go:build !windows

// The serve test stops serve with SIGINT, which Windows cannot send to itself.

package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/store"
)

// syncBuffer is serve's stdout, read while serve writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// US-O-5.1, 5.3: serve deletes an event past its retention on start,
// keeps an open one, logs no name, and shuts down cleanly.
func TestUS_O_5_1_ServeDeletesExpiredEventsOnStart(t *testing.T) {
	db := newDB(t)
	_, openToken, _ := newEvent(t, db)
	loc, _ := time.LoadLocation("Asia/Taipei")
	old := time.Now().In(loc).AddDate(0, 0, -82).Format("2006-01-02")
	if r := runWith("", nil, "create-event", "-db", db, "-name", "舊婚宴", "-date", old, "-tz", "Asia/Taipei",
		"-master-name", "總召", "-base-url", base); r.code != 0 {
		t.Fatalf("create-event: %s", r.stderr)
	}

	var out syncBuffer
	code := make(chan int, 1)
	go func() {
		code <- run([]string{"serve", "-db", db, "-addr", "127.0.0.1:0"}, cli{stdout: &out, stderr: &out, lookupEnv: func(string) (string, bool) { return "", false }})
	}()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), `"msg":"purge"`); {
		if time.Now().After(deadline) {
			t.Fatalf("no purge log: %s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("serve exit %d: %s", c, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}

	var purged map[string]any
	for _, l := range logLines(t, out.String()) {
		if l["msg"] == "purge" {
			purged = l
		}
	}
	if d, _ := purged["deleted"].([]any); len(d) != 1 {
		t.Errorf("purge log = %v, want one deleted event", purged)
	}
	if strings.Contains(out.String(), "婚宴") || strings.Contains(out.String(), "總召") {
		t.Errorf("a name is in the log: %s", out.String())
	}

	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if ids, err := st.PurgeEnded(context.Background(), time.Now()); err != nil || len(ids) != 0 {
		t.Errorf("left to delete: %v %v", ids, err)
	}
	if body := getSnapshot(t, handler(t, db), openToken); body["result"] != "ok" {
		t.Errorf("open event: %v", body)
	}
}
