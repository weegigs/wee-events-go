package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/weegigs/wee-events-go/we"
)

// errStoreClosed is defined here in Task 9 and is the canonical definition for
// the package; the store (Task 10) reuses it. Do not redefine elsewhere.
var errStoreClosed = errors.New("sqlite: store is closed")

var errShardClosed = errors.New("sqlite: shard is closed")

// shard owns one partition's database exclusively. A single goroutine consumes
// the request channel and runs every load/publish/scan against the pinned
// connection, so concurrent callers are serialised without touching the
// database from more than one goroutine — the invariant that resolves the
// go-libsql SQLITE_MISUSE concurrency defect. SetMaxOpenConns(1) holds the
// invariant even if a future caller bypasses the channel.
type shard struct {
	db          *sql.DB
	encoder     we.Encoder
	busyTimeout time.Duration
	// local is true for file-backed targets, where busy_timeout governs write
	// contention. Remote (libsql/Hrana) targets set it false: the engine rejects
	// PRAGMA busy_timeout and serialises writers server-side.
	local    bool
	requests chan shardRequest
	done     chan struct{}
	closed   chan struct{}
	stopped  atomic.Bool
	gate     *operationGate
}

type operationGate struct {
	slots chan struct{}
}

func newOperationGate(limit int) *operationGate {
	if limit <= 0 {
		return nil
	}
	return &operationGate{slots: make(chan struct{}, limit)}
}

func (g *operationGate) runValue(ctx context.Context, operation func() (any, error)) (any, error) {
	if g == nil {
		return operation()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case g.slots <- struct{}{}:
	}
	defer func() { <-g.slots }()
	return operation()
}

type shardRequest struct {
	ctx   context.Context
	run   func(ctx context.Context, db *sql.DB) (any, error)
	reply chan shardResult
}

type shardResult struct {
	value any
	err   error
}

func newShard(db *sql.DB, encoder we.Encoder, busyTimeout time.Duration, local bool, gate *operationGate) *shard {
	sh := &shard{
		db:          db,
		encoder:     encoder,
		busyTimeout: busyTimeout,
		local:       local,
		requests:    make(chan shardRequest),
		done:        make(chan struct{}),
		closed:      make(chan struct{}),
		gate:        gate,
	}
	go sh.serve()
	return sh
}

func (s *shard) serve() {
	defer close(s.closed)
	defer func() { _ = s.db.Close() }()
	for {
		select {
		case <-s.done:
			return
		case req := <-s.requests:
			value, err := req.run(req.ctx, s.db)
			req.reply <- shardResult{value: value, err: err}
		}
	}
}

// dispatch hands an operation to the owner goroutine and waits for its result
// or the caller's cancellation. The reply channel is buffered so the owner is
// never blocked writing a result the caller has abandoned.
func (s *shard) dispatch(ctx context.Context, run func(ctx context.Context, db *sql.DB) (any, error)) (any, error) {
	return s.gate.runValue(ctx, func() (any, error) {
		return s.dispatchExclusive(ctx, run)
	})
}

func (s *shard) dispatchExclusive(ctx context.Context, run func(ctx context.Context, db *sql.DB) (any, error)) (any, error) {
	reply := make(chan shardResult, 1)
	if s.stopped.Load() {
		return nil, errShardClosed
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, errShardClosed
	case s.requests <- shardRequest{ctx: ctx, run: run, reply: reply}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-reply:
		return res.value, res.err
	}
}

func (s *shard) load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	value, err := s.dispatch(ctx, func(ctx context.Context, db *sql.DB) (any, error) {
		return loadEvents(ctx, db, id)
	})
	if err != nil {
		return we.Aggregate{}, err
	}
	return value.(we.Aggregate), nil
}

func (s *shard) publish(ctx context.Context, id we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	if len(events) == 0 {
		// Empty publish is a no-op state, not an error (matches the suite).
		return nil
	}
	encoder, err := options.EncoderFor(s.encoder)
	if err != nil {
		return fmt.Errorf("sqlite: %w", err)
	}
	rows, err := encodeEvents(encoder, options, events)
	if err != nil {
		return err
	}

	_, err = s.dispatch(ctx, func(ctx context.Context, db *sql.DB) (any, error) {
		return nil, publishRows(ctx, db, s.busyTimeout, s.local, id, options, rows)
	})
	return err
}

// scan runs an arbitrary read on the owner goroutine; enumeration uses it.
func (s *shard) scan(ctx context.Context, run func(ctx context.Context, db *sql.DB) ([]we.AggregateId, error)) ([]we.AggregateId, error) {
	value, err := s.dispatch(ctx, func(ctx context.Context, db *sql.DB) (any, error) {
		return run(ctx, db)
	})
	if err != nil {
		return nil, err
	}
	return value.([]we.AggregateId), nil
}

// stop asks the owner goroutine to shut down. It does not wait: in-flight
// work finishes on the owner before the deferred database close runs, so
// eviction never stalls behind a slow request. Callers that need the file
// handles released (Store.Close) follow up with awaitClosed.
func (s *shard) stop() {
	if s.stopped.CompareAndSwap(false, true) {
		close(s.done)
	}
}

// awaitClosed blocks until the owner goroutine has exited and its deferred
// database close has run.
func (s *shard) awaitClosed() {
	<-s.closed
}

// publishRows runs the publish transaction with bounded busy retries. Revision
// conflicts are terminal and never retried (SQLITE-S2.R5).
func publishRows(ctx context.Context, db *sql.DB, busyTimeout time.Duration, local bool, id we.AggregateId, options we.PublishOptions, rows []eventRow) error {
	var lastErr error
	for attempt := 0; attempt <= busyRetries; attempt++ {
		err := publishOnce(ctx, db, busyTimeout, local, id, options, rows)
		if err == nil {
			return nil
		}
		if errors.Is(err, we.RevisionConflict) {
			return err
		}
		if !isBusy(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}
