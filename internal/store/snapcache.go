package store

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// The snapshot cache (ADR-0041, US-O-14): a poll reads the data generation
// first and, while it and the schema version are what they were when an
// event's data was read, uses that data instead of reading the event
// again. Links are still looked up in the database every time.

// generation is the data generation counter with the schema version.
type generation struct{ schema, n int64 }

// snapshotCache holds one generation's events: each event's data as the
// store read it, every list included, Owner left empty, nothing of the
// present time and nothing kept only in memory. It is shared by every poll
// and only ever read. No events (nil) means no generation.
type snapshotCache struct {
	mu     sync.Mutex
	gen    generation
	events map[int64]SnapshotData
}

// same reports whether the cache holds generation g; mu is held.
func (c *snapshotCache) same(g generation) bool { return c.events != nil && c.gen == g }

// newer reports whether g replaces what the cache holds; mu is held.
func (c *snapshotCache) newer(g generation) bool {
	return c.events == nil || g.schema != c.gen.schema || g.n > c.gen.n
}

func (c *snapshotCache) get(g generation, id int64) (SnapshotData, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.same(g) {
		return SnapshotData{}, false
	}
	d, ok := c.events[id]
	return d, ok
}

// put adds d to generation g's events, replaces the cache with g when g
// is newer, and drops d when g is older.
func (c *snapshotCache) put(g generation, id int64, d SnapshotData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.same(g) {
		c.events[id] = d
	} else if c.newer(g) {
		c.gen, c.events = g, map[int64]SnapshotData{id: d}
	}
}

// clear empties the cache and keeps no generation (US-O-14.6).
func (c *snapshotCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen, c.events = generation{}, nil
}

// beforeCacheStore, when set by a test, runs before a poll stores what it
// read.
var beforeCacheStore func()

// PollSnapshot is Snapshot for GET /api/snapshot (api.md section 7 item
// 2): one read transaction that reads the data generation first, looks the
// token up in the database and takes the event's data from the snapshot
// cache when the generation is unchanged. Without a readable generation
// it reads everything, exactly as Snapshot does, and keeps nothing.
func (s *Store) PollSnapshot(ctx context.Context, tokenHash []byte, now time.Time) (SnapshotData, error) {
	tx, err := s.r.BeginTx(ctx, nil)
	if err != nil {
		return SnapshotData{}, err
	}
	defer tx.Rollback()
	g, err := readGeneration(ctx, tx)
	if err != nil {
		return s.uncached(ctx, tx, tokenHash, now)
	}
	return s.pollAt(ctx, tx, g, tokenHash, now)
}

func readGeneration(ctx context.Context, tx *sql.Tx) (generation, error) {
	var g generation
	err := tx.QueryRowContext(ctx, `SELECT (SELECT schema_version FROM pragma_schema_version), n
		FROM data_generation WHERE id = 1`).Scan(&g.schema, &g.n)
	return g, err
}

// uncached is Snapshot inside tx; a cancelled context is its own error,
// never a missing link.
func (s *Store) uncached(ctx context.Context, tx *sql.Tx, tokenHash []byte, now time.Time) (SnapshotData, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotData{}, err
	}
	owner, e, v, err := resolveToken(ctx, tx, tokenHash, now, s.consoles())
	if err != nil {
		return SnapshotData{}, err
	}
	return loadSnapshotData(ctx, tx, owner, e, v)
}

// pollAt resolves the link like resolveToken (ErrInvalidToken, then the
// event, then ErrEventEnded) with the event taken from the cache when it
// holds generation g.
func (s *Store) pollAt(ctx context.Context, tx *sql.Tx, g generation, tokenHash []byte, now time.Time) (SnapshotData, error) {
	owner, err := lookupOwner(ctx, tx, tokenHash, s.consoles())
	if err != nil {
		return SnapshotData{}, err
	}
	d, err := s.eventAt(ctx, tx, g, owner.EventID, now)
	if err != nil {
		return SnapshotData{}, err
	}
	d.Owner = owner
	return jobsFor(owner.RoleType).strip(d), nil
}

// eventAt is the event from the cache, else read and stored; an ended
// event's data is neither read nor stored.
func (s *Store) eventAt(ctx context.Context, tx *sql.Tx, g generation, id int64, now time.Time) (SnapshotData, error) {
	if d, ok := s.cache.get(g, id); ok {
		return d, endedErr(d.Event, now)
	}
	d, err := readEvent(ctx, tx, id, now)
	if err != nil {
		return SnapshotData{}, err
	}
	s.remember(ctx, g, id, d)
	return d, nil
}

func endedErr(e domain.Event, now time.Time) error {
	if e.Ended(now) {
		return ErrEventEnded
	}
	return nil
}

// readEvent reads the event's row and, unless it has ended, all its data.
func readEvent(ctx context.Context, tx *sql.Tx, id int64, now time.Time) (SnapshotData, error) {
	e, v, err := loadEvent(ctx, tx, id)
	if err != nil {
		return SnapshotData{}, err
	}
	if err := endedErr(e, now); err != nil {
		return SnapshotData{}, err
	}
	return loadEventData(ctx, tx, e, v, allJobs)
}

// remember stores what a read that was not cancelled got.
func (s *Store) remember(ctx context.Context, g generation, id int64, d SnapshotData) {
	if beforeCacheStore != nil {
		beforeCacheStore()
	}
	if ctx.Err() == nil {
		s.cache.put(g, id, d)
	}
}
