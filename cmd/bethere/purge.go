package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// purgeFunc is store.PurgeEnded: it deletes the events whose retention ran
// out and returns their ids, even alongside an error for skipped events.
type purgeFunc func(ctx context.Context, now time.Time) ([]int64, error)

// purgeDaily runs the daily cleanup (US-O-5): once at start, so a VM that
// was stopped catches up on boot, then every interval until ctx is done.
// A failure is only logged; the next run tries again.
func purgeDaily(ctx context.Context, purge purgeFunc, now func() time.Time, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		purgeOnce(ctx, purge, now(), logger)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// purgeOnce logs only event ids, the count and the clock it used, never
// names or contents (US-O-5.3); the clock is there so a deletion can be
// traced back to a wrong one.
func purgeOnce(ctx context.Context, purge purgeFunc, now time.Time, logger *slog.Logger) {
	// A clock this far off would delete every event; refuse instead.
	if !domain.InRange(now) {
		logger.Error("purge skipped: clock out of range", "now", now.UTC())
		return
	}
	ids, err := purge(ctx, now)
	// Deletions commit even when some events were skipped, so they are
	// logged whatever the error.
	if err == nil || len(ids) > 0 {
		if ids == nil {
			ids = []int64{}
		}
		logger.Info("purge", "now", now.UTC(), "count", len(ids), "deleted", ids)
	}
	// While shutting down the error is the cancellation; the next start runs
	// the cleanup again.
	if err != nil && ctx.Err() == nil {
		logger.Error("purge failed; the next run tries again (an unreadable event stays until fixed by hand; expired invites are only retried)", "err", err)
	}
}
