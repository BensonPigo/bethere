package loginmail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/store"
)

// fakeStore answers PrepareLoginMail with outcome (or err) for every
// member and records who was asked.
type fakeStore struct {
	mu      sync.Mutex
	outcome store.MailOutcome
	err     error
	asked   []int64
}

func (f *fakeStore) PrepareLoginMail(_ context.Context, memberID int64, _ []byte, _ time.Time) (string, store.MailOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, memberID)
	if f.err != nil || f.outcome != store.MailWritten {
		return "", f.outcome, f.err
	}
	return "m@x.tw", store.MailWritten, nil
}

// fakeSender records the links sent, failing with err when set; block
// holds each send until the context ends.
type fakeSender struct {
	mu      sync.Mutex
	links   []string
	err     error
	block   bool
	started chan struct{}
}

func (f *fakeSender) Send(ctx context.Context, to, link string) error {
	if f.block {
		f.started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.links = append(f.links, to+" "+link)
	return nil
}

// logBuf is a log the worker and the test share.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }
func (l *logBuf) Len() int                    { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Len() }
func (l *logBuf) Reset()                      { l.mu.Lock(); defer l.mu.Unlock(); l.b.Reset() }

func newQueue(st *fakeStore, s *fakeSender, every time.Duration) (*Queue, *logBuf) {
	logs := &logBuf{}
	q := New(Config{Store: st, Sender: s, BaseURL: "https://bethere.tw", Now: time.Now,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Every: every})
	return q, logs
}

// api.md 10.2: a member waits at most once; taken out, they can wait again.
func TestUS_CO_1_11_QueueHoldsAMemberOnce(t *testing.T) {
	st, s := &fakeStore{}, &fakeSender{}
	q, _ := newQueue(st, s, time.Hour)
	q.Enqueue(1)
	q.Enqueue(1)
	q.Enqueue(2)
	q.SendWaiting(context.Background())
	if len(st.asked) != 2 || len(s.links) != 2 {
		t.Errorf("asked %v, sent %v; want members 1 and 2 once", st.asked, s.links)
	}
	if !strings.HasPrefix(s.links[0], "m@x.tw https://bethere.tw/login#t=") || len(s.links[0]) != len("m@x.tw https://bethere.tw/login#t=")+22 {
		t.Errorf("link = %q", s.links[0])
	}
	q.Enqueue(1)
	q.SendWaiting(context.Background())
	if len(st.asked) != 3 {
		t.Errorf("asked %v, want member 1 again", st.asked)
	}
}

// api.md 10.2: a hundred members wait at most; the rest are counted, and
// the count is logged once, without who.
func TestUS_CO_1_11_FullQueueIsCounted(t *testing.T) {
	st, s := &fakeStore{}, &fakeSender{}
	q, logs := newQueue(st, s, time.Hour)
	for id := int64(1); id <= 103; id++ { // api.md 10.2: 100
		q.Enqueue(id)
	}
	q.flush()
	if !strings.Contains(logs.String(), `"queue_full":3`) || strings.Count(logs.String(), "\n") != 1 {
		t.Errorf("log = %s", logs.String())
	}
	q.SendWaiting(context.Background())
	if len(st.asked) != 100 {
		t.Errorf("sent to %d, want 100", len(st.asked))
	}
}

// US-CO-1.11, 1.12: held-back and failed mails are counted with the last
// failure's kind; nothing is logged when nothing was held back.
func TestUS_CO_1_12_CountsAndTheirLine(t *testing.T) {
	st, s := &fakeStore{}, &fakeSender{err: errors.New("dial tcp: connection refused to a@x.tw")}
	q, logs := newQueue(st, s, time.Hour)
	q.flush()
	if logs.Len() != 0 {
		t.Errorf("a line with nothing to tell: %s", logs.String())
	}
	q.Enqueue(1)
	q.SendWaiting(context.Background())
	st.outcome = store.MailLimited
	q.Enqueue(2)
	q.Enqueue(3)
	q.SendWaiting(context.Background())
	st.outcome = store.MailNoMember
	q.Enqueue(4)
	q.SendWaiting(context.Background())
	q.flush()
	line := logs.String()
	for _, want := range []string{`"msg":"mail not sent"`, `"failed":1`, `"last_error":"connect"`, `"limited":2`, `"queue_full":0`} {
		if !strings.Contains(line, want) {
			t.Errorf("log lacks %s: %s", want, line)
		}
	}
	if strings.Contains(line, "a@x.tw") || strings.Contains(line, "#t=") {
		t.Errorf("the log carries an address or a link: %s", line)
	}
	logs.Reset()
	q.flush()
	if logs.Len() != 0 {
		t.Errorf("the counts were not reset: %s", logs.String())
	}
	st.outcome, st.err = store.MailWritten, errors.New("disk I/O error")
	q.Enqueue(5)
	q.SendWaiting(context.Background())
	q.flush()
	if !strings.Contains(logs.String(), `"last_error":"store"`) {
		t.Errorf("a store failure: %s", logs.String())
	}
}

// Run sends what is queued, logs every tick, and on its context's end
// cancels the send in progress, counts it and logs before returning.
func TestUS_CO_1_12_RunStopsInOrder(t *testing.T) {
	st, s := &fakeStore{}, &fakeSender{}
	q, logs := newQueue(st, s, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	q.Enqueue(1)
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.links) == 1 })
	st.mu.Lock()
	st.outcome = store.MailLimited
	st.mu.Unlock()
	q.Enqueue(2)
	waitFor(t, func() bool { return strings.Contains(logs.String(), `"limited":1`) })
	st.mu.Lock()
	st.outcome = store.MailWritten
	st.mu.Unlock()
	s.block, s.started = true, make(chan struct{})
	q.Enqueue(3)
	<-s.started
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if !strings.Contains(logs.String(), `"last_error":"canceled"`) {
		t.Errorf("the cancelled send was not logged: %s", logs.String())
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}

// api.md 10.2: once the service stops, members still waiting get no mail
// and are not counted, even when a member and the stop are ready at once.
func TestUS_CO_1_12_StoppedQueueTakesNoOne(t *testing.T) {
	for i := 0; i < 50; i++ { // the select picks at random
		st, s := &fakeStore{}, &fakeSender{}
		q, logs := newQueue(st, s, time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		q.Enqueue(1)
		q.Enqueue(2)
		q.Run(ctx)
		if len(st.asked) != 0 || logs.Len() != 0 {
			t.Fatalf("after the stop: asked %v, log %s", st.asked, logs.String())
		}
	}
}

func TestNewNeedsEverything(t *testing.T) {
	for name, cfg := range map[string]Config{
		"store":  {Sender: &fakeSender{}, Now: time.Now, Logger: slog.Default(), Every: time.Second},
		"sender": {Store: &fakeStore{}, Now: time.Now, Logger: slog.Default(), Every: time.Second},
		"now":    {Store: &fakeStore{}, Sender: &fakeSender{}, Logger: slog.Default(), Every: time.Second},
		"logger": {Store: &fakeStore{}, Sender: &fakeSender{}, Now: time.Now, Every: time.Second},
		"every":  {Store: &fakeStore{}, Sender: &fakeSender{}, Now: time.Now, Logger: slog.Default()},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("no %s: no panic", name)
				}
			}()
			New(cfg)
		}()
	}
}
