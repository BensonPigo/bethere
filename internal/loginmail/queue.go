// Package loginmail is the login mail queue (api.md 10.2): the login
// endpoint puts a member's number in it and answers at once; one worker
// writes each mail within the limits of US-CO-1.11 and sends it. What is
// held back or fails is only counted, and logged at most once a minute,
// so no request can flood the log.
package loginmail

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/mail"
	"github.com/BensonPigo/bethere/internal/store"
)

// Size is the most members waiting at once.
const Size = 100

// Store is what the worker needs of the store.
type Store interface {
	PrepareLoginMail(ctx context.Context, memberID int64, mailHash []byte, now time.Time) (string, store.MailOutcome, error)
}

// Config sets up a Queue; every field is required.
type Config struct {
	Store   Store
	Sender  mail.Sender
	BaseURL string
	Now     func() time.Time
	Logger  *slog.Logger
	// Every is how often the counts are logged: a minute in serve.
	Every time.Duration
}

// Queue holds the members waiting for a login mail, each at most once.
type Queue struct {
	cfg     Config
	ch      chan int64
	mu      sync.Mutex
	waiting map[int64]bool
	counts  counts
}

// counts are the mails not sent since the last log line.
type counts struct {
	failed, limited, full int
	lastErr               string
}

func New(cfg Config) *Queue {
	if cfg.Store == nil || cfg.Sender == nil || cfg.Now == nil || cfg.Logger == nil || cfg.Every <= 0 {
		panic("loginmail: Store, Sender, Now, Logger and Every are required")
	}
	return &Queue{cfg: cfg, ch: make(chan int64, Size), waiting: map[int64]bool{}}
}

// Enqueue asks for a login mail to the member. A member already waiting
// is not added again; a full queue counts the request and drops it.
func (q *Queue) Enqueue(memberID int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.waiting[memberID] {
		return
	}
	select {
	case q.ch <- memberID:
		q.waiting[memberID] = true
	default:
		q.counts.full++
	}
}

// Run is the worker; it returns once ctx ends, after the mail in progress
// (cancelled, counted as failed) and the last log line. Members still
// waiting get no mail and are not counted.
func (q *Queue) Run(ctx context.Context) {
	tick := time.NewTicker(q.cfg.Every)
	defer tick.Stop()
	defer q.flush()
	for q.step(ctx, tick.C) {
	}
}

// step waits for the next thing to do and does it; false once ctx ended.
func (q *Queue) step(ctx context.Context, tick <-chan time.Time) bool {
	select {
	case <-ctx.Done():
	case <-tick:
		q.flush()
	case id := <-q.ch:
		q.takeUnlessStopped(ctx, id)
	}
	return ctx.Err() == nil
}

// takeUnlessStopped takes the member unless ctx ended: when the stop and a
// member were ready at once, the member gets no mail and is not counted.
func (q *Queue) takeUnlessStopped(ctx context.Context, memberID int64) {
	if ctx.Err() == nil {
		q.take(ctx, memberID)
	}
}

// SendWaiting sends to every member waiting now, then returns; tests use
// it instead of Run to see the mails at once.
func (q *Queue) SendWaiting(ctx context.Context) {
	for {
		select {
		case id := <-q.ch:
			q.take(ctx, id)
		default:
			return
		}
	}
}

// take sends to a member taken from the queue, who may then be added
// again (api.md 10.2).
func (q *Queue) take(ctx context.Context, memberID int64) {
	q.mu.Lock()
	delete(q.waiting, memberID)
	q.mu.Unlock()
	q.send(ctx, memberID)
}

// send writes and sends one login mail; a failed send keeps its row, so
// it counts towards the limits (US-CO-1.11).
func (q *Queue) send(ctx context.Context, memberID int64) {
	to, token, ok := q.prepare(ctx, memberID)
	if !ok {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, mail.Timeout)
	defer cancel()
	if err := q.cfg.Sender.Send(sendCtx, to, mail.Link(q.cfg.BaseURL, token)); err != nil {
		q.failedAs(mail.Kind(err))
	}
}

// prepare writes the member's login mail and returns where it goes and its
// link's token; ok is false when nothing is to be sent, counted unless the
// member was removed.
func (q *Queue) prepare(ctx context.Context, memberID int64) (to, token string, ok bool) {
	token, err := auth.NewToken()
	if err != nil {
		q.failedAs("store")
		return "", "", false
	}
	to, outcome, err := q.cfg.Store.PrepareLoginMail(ctx, memberID, auth.HashToken(token), q.cfg.Now())
	q.note(outcome, err)
	return to, token, err == nil && outcome == store.MailWritten
}

// note counts a mail the store held back or could not write.
func (q *Queue) note(outcome store.MailOutcome, err error) {
	switch {
	case err != nil:
		q.failedAs("store")
	case outcome == store.MailLimited:
		q.mu.Lock()
		q.counts.limited++
		q.mu.Unlock()
	}
}

// failedAs counts a mail not sent for kind: a send's failure, or "store"
// when the row could not be written.
func (q *Queue) failedAs(kind string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.counts.failed++
	q.counts.lastErr = kind
}

// flush logs the counts since the last line, if any, and starts again.
func (q *Queue) flush() {
	q.mu.Lock()
	c := q.counts
	q.counts = counts{}
	q.mu.Unlock()
	if c.failed == 0 && c.limited == 0 && c.full == 0 {
		return
	}
	q.cfg.Logger.Warn("mail not sent", "failed", c.failed, "last_error", c.lastErr, "limited", c.limited, "queue_full", c.full)
}
