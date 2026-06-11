package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "github.com/tursodatabase/go-libsql"

	"github.com/weegigs/wee-events-go/we"
)

const driverName = "libsql"

const defaultBusyTimeout = 5 * time.Second

// busyRetries bounds the number of transient SQLITE_BUSY retries on the
// publish transaction. busy_timeout already blocks within the engine; this is
// a small additional guard for the rare case the timeout is exhausted. A
// revision conflict is NEVER retried here (SQLITE-S2.R5).
const busyRetries = 3

// Backend pairs a strategy with the catalog that serves it. (Deferred here from
// the catalog task because the constructors below are the first code to read
// its fields.)
type Backend struct {
	strategy PartitionStrategy
	catalog  PartitionCatalog
}

// Store is a partitioned SQLite/libSQL event store. A PartitionStrategy routes
// each aggregate to a Partition; a PartitionCatalog maps the partition to a
// database target; each partition is owned by one shard goroutine. The store
// satisfies we.EventStore (SQLITE-S1.R1).
type Store struct {
	strategy PartitionStrategy
	catalog  PartitionCatalog
	encoder  we.Encoder

	mu     sync.Mutex
	shards map[Partition]*shard
	known  map[Partition]struct{}
	closed bool
}

var _ we.EventStore = (*Store)(nil)

// NewStore opens a partitioned store over the given backend. The encoder is the
// store's explicit write encoding (ENCODING-S2.R1); nil is a construction error.
func NewStore(_ context.Context, encoder we.Encoder, backend Backend) (*Store, error) {
	if encoder == nil {
		return nil, errors.New("sqlite: encoder is required")
	}
	return &Store{
		strategy: backend.strategy,
		catalog:  backend.catalog,
		encoder:  encoder,
		shards:   map[Partition]*shard{},
		known:    map[Partition]struct{}{},
	}, nil
}

// Close stops every shard and releases every database. Pairs with NewStore.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for _, sh := range s.shards {
		sh.stop()
	}
	s.shards = map[Partition]*shard{}
	return nil
}

func (s *Store) Load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	partition := s.strategy.PartitionFor(id)
	sh, ok, err := s.openExisting(ctx, partition)
	if err != nil {
		return we.Aggregate{}, err
	}
	if !ok {
		// A partition that was never provisioned holds no events; this is a
		// state, not an error.
		return we.Aggregate{Id: id, Revision: we.InitialRevision}, nil
	}
	return sh.load(ctx, id)
}

func (s *Store) Publish(ctx context.Context, id we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	partition := s.strategy.PartitionFor(id)
	sh, err := s.ensureShard(ctx, partition)
	if err != nil {
		return err
	}
	return sh.publish(ctx, id, options, events...)
}

// ensureShard returns the partition's shard, provisioning and opening it on
// first use. The shard map grows monotonically; unbounded strategies
// (ByAggregate) trade memory for isolation, as documented.
func (s *Store) ensureShard(ctx context.Context, p Partition) (*shard, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errStoreClosed
	}
	if sh, ok := s.shards[p]; ok {
		s.mu.Unlock()
		return sh, nil
	}
	s.mu.Unlock()

	target, err := s.catalog.EnsureTarget(ctx, p)
	if err != nil {
		return nil, err
	}
	sh, err := s.openShard(ctx, p, target)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		sh.stop()
		return nil, errStoreClosed
	}
	if existing, ok := s.shards[p]; ok {
		// Another goroutine opened it first; discard the duplicate.
		sh.stop()
		return existing, nil
	}
	s.shards[p] = sh
	s.known[p] = struct{}{}
	return sh, nil
}

// openExisting returns the partition's shard if its target already exists,
// without provisioning. Used by Load.
func (s *Store) openExisting(ctx context.Context, p Partition) (*shard, bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, false, errStoreClosed
	}
	if sh, ok := s.shards[p]; ok {
		s.mu.Unlock()
		return sh, true, nil
	}
	s.mu.Unlock()

	target, ok, err := s.catalog.ExistingTarget(ctx, p)
	if err != nil || !ok {
		return nil, false, err
	}
	sh, err := s.openShard(ctx, p, target)
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		sh.stop()
		return nil, false, errStoreClosed
	}
	if existing, ok := s.shards[p]; ok {
		sh.stop()
		return existing, true, nil
	}
	s.shards[p] = sh
	s.known[p] = struct{}{}
	return sh, true, nil
}

// openShard opens and migrates a target, applies the WAL pragma, records its
// partition name, and starts its owner goroutine.
func (s *Store) openShard(ctx context.Context, p Partition, target Target) (*shard, error) {
	db, err := sql.Open(driverName, target.dsn)
	if err != nil {
		return nil, redactToken(fmt.Errorf("sqlite: failed to open database: %w", err), target.authToken)
	}
	db.SetMaxOpenConns(1)

	// Pragmas before migration: WAL is a file-level property and busy_timeout a
	// per-connection one, and the CREATE TABLE statements in migrate are
	// themselves writes. Without WAL + busy_timeout in force first, two
	// instances opening one shared file race their migrations into a raw
	// "database is locked". SetMaxOpenConns(1) means the single pooled
	// connection applyShardPragmas configures is the same one migrate reuses.
	if err := applyShardPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, redactToken(err, target.authToken)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, redactToken(err, target.authToken)
	}
	if err := s.catalog.PrepareShard(ctx, p, db); err != nil {
		_ = db.Close()
		return nil, redactToken(err, target.authToken)
	}
	return newShard(db, s.encoder, defaultBusyTimeout), nil
}

// applyShardPragmas applies the persistent WAL journal mode (and a baseline
// busy_timeout) once on the shard's single connection. WAL is a file-level
// property: setting it once lets cross-instance readers (Load) not block on a
// writer holding the file's write lock. busy_timeout is additionally re-applied
// per write transaction in publishOnce.
//
// busy_timeout is set FIRST so it is in force for everything that follows. The
// WAL conversion is then retried explicitly: busy_timeout does NOT cover it
// (see setWALJournalMode).
func applyShardPragmas(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: failed to acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := applyBusyTimeout(ctx, conn, defaultBusyTimeout); err != nil {
		return err
	}
	return setWALJournalMode(ctx, conn)
}

// setWALJournalMode converts the database to WAL, retrying on the transient
// "database is locked" that a concurrent first-time conversion by another
// connection to the same file produces. Shards open lazily on first use, so two
// instances over one fresh file race the conversion; the journal-mode change
// needs a brief exclusive lock and returns SQLITE_BUSY WITHOUT invoking the
// busy handler, so busy_timeout cannot absorb it. The conversion converges as
// soon as one connection wins — thereafter the file is already WAL and the
// pragma is an instant no-op for everyone else.
func setWALJournalMode(ctx context.Context, conn *sql.Conn) error {
	deadline := time.Now().Add(defaultBusyTimeout)
	backoff := time.Millisecond
	for {
		// The returned journal-mode value is intentionally NOT validated: an
		// :memory: target reports "memory" (WAL is inapplicable there, but the
		// database is private so there is no cross-instance contention to guard
		// against), so requiring "wal" would wrongly fail every in-memory shard.
		var mode string
		err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode)
		if err == nil {
			return nil
		}
		if !isBusy(err) || time.Now().After(deadline) {
			return fmt.Errorf("sqlite: failed to set journal_mode: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 100*time.Millisecond {
			backoff *= 2
		}
	}
}

// Local builds a backend backed by local SQLite files under root. Global uses
// root as a single file; named strategies use it as a directory.
func Local(root string, strategy LocalStrategy) Backend {
	return Backend{strategy: strategy, catalog: newLocalCatalog(root, strategy)}
}

// InMemory builds a single private in-memory database. Only single-target
// strategies are legal.
func InMemory(strategy SingleTargetStrategy) Backend {
	return Backend{strategy: strategy, catalog: newSingleTargetCatalog(Target{dsn: ":memory:"})}
}

// SqldDefault builds a backend over one shared sqld endpoint. Only single-target
// strategies are legal.
func SqldDefault(url, authToken string, strategy SingleTargetStrategy) Backend {
	return Backend{strategy: strategy, catalog: newSingleTargetCatalog(Target{dsn: url, authToken: authToken})}
}

// SqldNamespaced builds a backend that provisions one sqld namespace per
// partition. adminURL is the namespace admin endpoint; dataURL is the data
// endpoint partitions are addressed under.
func SqldNamespaced(adminURL, dataURL, authToken string, strategy NamingStrategy) Backend {
	provisioner := newSqldProvisioner(adminURL, dataURL, authToken)
	return Backend{strategy: strategy, catalog: newNamedTargetCatalog(strategy, provisioner)}
}

// Turso builds a backend that provisions one Turso platform database per
// partition. Only naming strategies are legal.
func Turso(config TursoConfig, strategy NamingStrategy) Backend {
	provisioner := newTursoProvisioner(newHTTPTursoClient(config.APIToken), config)
	return Backend{strategy: strategy, catalog: newNamedTargetCatalog(strategy, provisioner)}
}

func loadEvents(ctx context.Context, db *sql.DB, id we.AggregateId) (we.Aggregate, error) {
	const query = `
SELECT event_id, event_type, revision, causation_id, correlation_id, encoding, data
FROM events
WHERE aggregate_type = ? AND aggregate_key = ?
ORDER BY revision ASC;`

	rows, err := db.QueryContext(ctx, query, id.Type, id.Key)
	if err != nil {
		return we.Aggregate{}, fmt.Errorf("sqlite: failed to load events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []we.RecordedEvent
	for rows.Next() {
		var (
			eventID       string
			eventType     string
			revision      string
			causationID   sql.NullString
			correlationID sql.NullString
			encoding      string
			data          []byte
		)
		if err := rows.Scan(&eventID, &eventType, &revision, &causationID, &correlationID, &encoding, &data); err != nil {
			return we.Aggregate{}, fmt.Errorf("sqlite: failed to scan event: %w", err)
		}

		// The revision is a sequence, not a ULID; the event_id ULID carries
		// the creation time, so the timestamp is derived from it.
		ts, err := timestampFromEventID(eventID)
		if err != nil {
			return we.Aggregate{}, err
		}

		events = append(events, we.RecordedEvent{
			AggregateId: id,
			EventID:     we.EventID(eventID),
			Revision:    we.Revision(revision),
			Timestamp:   ts,
			EventType:   we.EventType(eventType),
			Metadata: we.RecordedEventMetadata{
				CausationId:   we.EventID(causationID.String),
				CorrelationId: we.CorrelationID(correlationID.String),
			},
			Data: we.Data{
				Encoding: encoding,
				Data:     data,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return we.Aggregate{}, fmt.Errorf("sqlite: failed to read events: %w", err)
	}

	revision := we.InitialRevision
	if len(events) > 0 {
		revision = events[len(events)-1].Revision
	}

	return we.Aggregate{
		Id:       id,
		Events:   events,
		Revision: revision,
	}, nil
}

func publishOnce(ctx context.Context, db *sql.DB, busyTimeout time.Duration, id we.AggregateId, options we.PublishOptions, rows []eventRow) (err error) {
	conn, connErr := db.Conn(ctx)
	if connErr != nil {
		return fmt.Errorf("sqlite: failed to acquire connection: %w", connErr)
	}
	defer func() { _ = conn.Close() }()

	// busy_timeout is per-connection. A shard pins one connection that already
	// has it set, but two SEPARATE Store instances over the same file have
	// independent pools and one cannot see the other's pragma; ensuring it per
	// write transaction is what lets BEGIN IMMEDIATE wait out a concurrent
	// cross-instance writer instead of failing fast with SQLITE_BUSY.
	if err := applyBusyTimeout(ctx, conn, busyTimeout); err != nil {
		return err
	}

	// BEGIN IMMEDIATE takes the write lock up front, avoiding the read->write
	// upgrade SQLITE_BUSY_SNAPSHOT race. The read that establishes the current
	// sequence therefore runs against the latest committed state, so revisions
	// are monotonic per aggregate even across separate store instances sharing
	// a file.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("sqlite: failed to begin transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			if _, rbErr := conn.ExecContext(ctx, "ROLLBACK"); rbErr != nil && err == nil {
				err = fmt.Errorf("sqlite: failed to roll back transaction: %w", rbErr)
			}
		}
	}()

	current, err := currentSequence(ctx, conn, id)
	if err != nil {
		return err
	}

	// Honour the caller's expected revision against the current sequence
	// (SQLITE-S2.R1, SQLITE-S2.R2, SQLITE-S2.R4). A mismatch returns the conflict
	// before any row is written, so none of the batch persists.
	if conflict := checkExpectedRevision(options.ExpectedRevision, current); conflict != nil {
		return conflict
	}

	const insert = `
INSERT INTO events
    (event_id, aggregate_type, aggregate_key, event_type, revision, causation_id, correlation_id, encoding, data)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`

	for i, row := range rows {
		// Revisions are the 1-based per-aggregate sequence formatted as 26-char
		// hex. Sequence-derived revisions (not wall-clock ULIDs) are what make
		// ordering and the expected-revision check correct across instances.
		revision := revisionForSequence(current + uint64(i) + 1)
		if _, err := conn.ExecContext(ctx, insert,
			row.eventID, id.Type, id.Key, row.eventType, revision.String(),
			nullable(row.causationID), nullable(row.correlationID), row.encoding, row.data,
		); err != nil {
			// The UNIQUE (aggregate_type, aggregate_key, revision) index is the
			// authoritative optimistic-concurrency guard: a concurrent appender
			// that committed the same sequence first makes this insert violate
			// the index, and the violation maps to a revision conflict
			// (SQLITE-S2.R3), not a raw driver error.
			if isUniqueViolation(err) {
				return we.RevisionConflict
			}
			return fmt.Errorf("sqlite: failed to insert event: %w", err)
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("sqlite: failed to commit transaction: %w", err)
	}
	committed = true

	return nil
}

// currentSequence returns the aggregate's current last sequence number — 0 for
// an aggregate with no rows (which loads as InitialRevision).
func currentSequence(ctx context.Context, conn *sql.Conn, id we.AggregateId) (uint64, error) {
	const query = `
SELECT revision FROM events
WHERE aggregate_type = ? AND aggregate_key = ?
ORDER BY revision DESC LIMIT 1;`

	var revision string
	err := conn.QueryRowContext(ctx, query, id.Type, id.Key).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite: failed to read current revision: %w", err)
	}

	sequence, err := sequenceForRevision(we.Revision(revision))
	if err != nil {
		return 0, err
	}

	return sequence, nil
}

// checkExpectedRevision compares the caller's expected revision against the
// aggregate's current sequence. An empty expectation appends unconditionally;
// InitialRevision matches only an empty aggregate (current == 0); a specific
// revision matches only the current last revision.
func checkExpectedRevision(expected we.Revision, current uint64) error {
	if expected == "" {
		return nil
	}

	if expected == we.InitialRevision {
		if current == 0 {
			return nil
		}
		return we.RevisionConflict
	}

	expectedSequence, err := sequenceForRevision(expected)
	if err != nil {
		// An unparseable expected revision cannot match any real revision; treat
		// it as a conflict rather than a raw error so the caller's reload/retry
		// loop drives recovery.
		return we.RevisionConflict
	}

	if expectedSequence != current {
		return we.RevisionConflict
	}

	return nil
}

type eventRow struct {
	eventID       string
	eventType     string
	causationID   string
	correlationID string
	encoding      string
	data          []byte
}

func encodeEvents(encoder we.Encoder, options we.PublishOptions, events []we.DomainEvent) ([]eventRow, error) {
	rows := make([]eventRow, len(events))
	for i, event := range events {
		data, err := we.MarshalToData(encoder, event)
		if err != nil {
			return nil, fmt.Errorf("sqlite: failed to encode event: %w", err)
		}

		rows[i] = eventRow{
			eventID:       newEventID(),
			eventType:     we.EventTypeOf(event).String(),
			causationID:   options.CausationId.String(),
			correlationID: options.CorrelationId.String(),
			encoding:      data.Encoding,
			data:          data.Data,
		}
	}

	return rows, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
