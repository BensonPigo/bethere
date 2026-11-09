//go:build !windows

// The serve test stops serve with SIGINT, which Windows cannot send to itself.

package main

import (
	"context"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/store"
)

// runServe runs serve with env until its log shows want, then stops it
// with SIGINT as systemd would and returns the log.
func runServe(t *testing.T, db string, env map[string]string, want string) string {
	t.Helper()
	var out syncBuffer
	code := make(chan int, 1)
	go func() {
		code <- run([]string{"serve", "-db", db, "-addr", "127.0.0.1:0"}, cli{stdout: &out, stderr: &out,
			lookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }})
	}()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), want); {
		if time.Now().After(deadline) {
			t.Fatalf("no %s in the log: %s", want, out.String())
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
	return out.String()
}

// oldEvent adds an event whose retention ran out.
func oldEvent(t *testing.T, db string) {
	t.Helper()
	loc, _ := time.LoadLocation("Asia/Taipei")
	old := time.Now().In(loc).AddDate(0, 0, -82).Format("2006-01-02")
	if r := runWith("", nil, "create-event", "-db", db, "-name", "舊婚宴", "-date", old, "-tz", "Asia/Taipei",
		"-master-name", "總召", "-base-url", base); r.code != 0 {
		t.Fatalf("create-event: %s", r.stderr)
	}
}

// US-O-5.1, 5.3: serve deletes an event past its retention on start,
// keeps an open one, logs no name, and shuts down cleanly.
func TestUS_O_5_1_ServeDeletesExpiredEventsOnStart(t *testing.T) {
	db := newDB(t)
	_, openToken, _ := newEvent(t, db)
	oldEvent(t, db)

	out := runServe(t, db, nil, `"msg":"purge"`)

	var purged map[string]any
	for _, l := range logLines(t, out) {
		if l["msg"] == "purge" {
			purged = l
		}
	}
	if d, _ := purged["deleted"].([]any); len(d) != 1 {
		t.Errorf("purge log = %v, want one deleted event", purged)
	}
	if strings.Contains(out, "婚宴") || strings.Contains(out, "總召") {
		t.Errorf("a name is in the log: %s", out)
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

// US-O-2.1: serve starts in maintenance mode when told to, and deletes
// nothing while the Master checks a restored database.
func TestUS_O_2_1_ServeInMaintenanceDeletesNothing(t *testing.T) {
	db := newDB(t)
	oldEvent(t, db)
	out := runServe(t, db, map[string]string{"BETHERE_MAINTENANCE": "1"}, `"msg":"purge paused: maintenance mode"`)
	if !strings.Contains(out, `"maintenance":true`) {
		t.Errorf("listening log does not say maintenance: %s", out)
	}
	if strings.Contains(out, `"msg":"purge"`) {
		t.Errorf("purged in maintenance mode: %s", out)
	}
	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if ids, err := st.PurgeEnded(context.Background(), time.Now()); err != nil || len(ids) != 1 {
		t.Errorf("the expired event was not kept: %v %v", ids, err)
	}
}
