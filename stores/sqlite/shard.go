package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/weegigs/wee-events-go/we"
)

// errStoreClosed is defined here in Task 9 and is the canonical definition for
// the package; the store (Task 10) reuses it. Do not redefine elsewhere.
var errStoreClosed = errors.New("sqlite: store is closed")

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
	requests    chan shardRequest
	done        chan struct{}
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

func newShard(db *sql.DB, encoder we.Encoder, busyTimeout time.Duration) *shard {
	sh := &shard{
		db:          db,
		encoder:     encoder,
		busyTimeout: busyTimeout,
		requests:    make(chan shardRequest),
		done:        make(chan struct{}),
	}
	go sh.serve()
	return sh
}

func (s *shard) serve() {
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
	reply := make(chan shardResult, 1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, errStoreClosed
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
		return nil, publishRows(ctx, db, s.busyTimeout, id, options, rows)
	})
	return err
}

func (s *shard) stop() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	_ = s.db.Close()
}

// publishRows runs the publish transaction with bounded busy retries. Revision
// conflicts are terminal and never retried (SQLITE-S2.R5).
func publishRows(ctx context.Context, db *sql.DB, busyTimeout time.Duration, id we.AggregateId, options we.PublishOptions, rows []eventRow) error {
	var lastErr error
	for attempt := 0; attempt <= busyRetries; attempt++ {
		err := publishOnce(ctx, db, busyTimeout, id, options, rows)
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
