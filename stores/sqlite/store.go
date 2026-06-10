package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// schema is migrated one statement at a time: the go-libsql driver's
// ExecContext runs only the first statement of a multi-statement string, so
// the index MUST be a separate Exec or it is silently skipped — which would
// disable the optimistic-concurrency guard (SQLITE-S2.R3).
var schema = []string{
	`CREATE TABLE IF NOT EXISTS events (
    event_id        TEXT NOT NULL CHECK(length(event_id) = 26),
    aggregate_type  TEXT NOT NULL,
    aggregate_key   TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    revision        TEXT NOT NULL CHECK(length(revision) = 26),
    causation_id    TEXT,
    correlation_id  TEXT,
    encoding        TEXT NOT NULL,
    data            BLOB NOT NULL,
    PRIMARY KEY (event_id)
);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_events_aggregate
    ON events (aggregate_type, aggregate_key, revision);`,
}

// Store is a SQLite/libSQL-backed we.EventStore. It owns the connection pool
// and must be released through Close.
type Store struct {
	db          *sql.DB
	busyTimeout time.Duration
	encoder     we.Encoder
}

// compile-time assertion that *Store satisfies the EventStore contract
// (SQLITE-S1.R1).
var _ we.EventStore = (*Store)(nil)

type config struct {
	dsn         string
	maxOpen     int
	busyTimeout time.Duration
	options     []libsqlOption
}

// Option configures a Store target and connection behaviour. The same option
// set selects in-memory, local-file, or remote targets (SQLITE-S3.R1).
type Option func(*config) error

// InMemory selects a private in-memory database. The pool is pinned to a
// single connection so every operation shares the one in-memory database.
func InMemory() Option {
	return func(c *config) error {
		c.dsn = ":memory:"
		c.maxOpen = 1
		return nil
	}
}

// LocalFile selects a local-file database at path.
func LocalFile(path string) Option {
	return func(c *config) error {
		if strings.TrimSpace(path) == "" {
			return errors.New("sqlite: local file path must not be empty")
		}
		c.dsn = "file:" + path
		return nil
	}
}

// Remote selects a remote sqld/Turso database. url must use the libsql://,
// https://, or http:// scheme. An empty authToken is permitted for unsecured
// sqld endpoints.
func Remote(url string, authToken string) Option {
	return func(c *config) error {
		if strings.TrimSpace(url) == "" {
			return errors.New("sqlite: remote url must not be empty")
		}
		c.dsn = url
		if authToken != "" {
			c.options = append(c.options, withAuthToken(authToken))
		}
		return nil
	}
}

// BusyTimeout overrides the SQLite busy_timeout applied to every connection
// used for publishing (busy_timeout is per-connection, so each write
// connection sets it before taking the write lock).
func BusyTimeout(timeout time.Duration) Option {
	return func(c *config) error {
		if timeout < 0 {
			return errors.New("sqlite: busy timeout must not be negative")
		}
		c.busyTimeout = timeout
		return nil
	}
}

// NewStore opens the configured target, applies the connection PRAGMAs, and
// migrates the events table. The encoder is the store's explicit write
// encoding (ENCODING-S2.R1); nil is a construction error, never a deferred
// panic (ENCODING-S2.R2). It returns a usable *Store or an error and never
// a half-built store (SQLITE-S1.R5, SQLITE-S3.R2).
func NewStore(ctx context.Context, encoder we.Encoder, options ...Option) (*Store, error) {
	if encoder == nil {
		return nil, errors.New("sqlite: encoder is required")
	}

	cfg := config{busyTimeout: defaultBusyTimeout}
	for _, option := range options {
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}

	if cfg.dsn == "" {
		return nil, errors.New("sqlite: a target option is required (InMemory, LocalFile, or Remote)")
	}

	dsn, err := applyLibsqlOptions(cfg.dsn, cfg.options)
	if err != nil {
		return nil, err
	}

	// Capture the remote auth token (if any) so a driver error that echoes the
	// connection string cannot leak the credential through a wrapped error.
	var authToken string
	for _, option := range cfg.options {
		if option.key == "authToken" {
			authToken = option.value
		}
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, redactToken(fmt.Errorf("sqlite: failed to open database: %w", err), authToken)
	}

	if cfg.maxOpen > 0 {
		db.SetMaxOpenConns(cfg.maxOpen)
	}

	store := &Store{
		db:          db,
		busyTimeout: cfg.busyTimeout,
		encoder:     encoder,
	}

	if err := store.prepare(ctx); err != nil {
		// A half-built store is never returned: release the pool we opened.
		_ = db.Close()
		return nil, redactToken(err, authToken)
	}

	return store, nil
}

// Close releases the connection pool. Pairs with NewStore (principle 2).
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) prepare(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: failed to acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := applyPragmas(ctx, conn, s.busyTimeout); err != nil {
		return err
	}

	for _, statement := range schema {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("sqlite: failed to migrate schema: %w", err)
		}
	}

	return nil
}

func (s *Store) Load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	const query = `
SELECT event_id, event_type, revision, causation_id, correlation_id, encoding, data
FROM events
WHERE aggregate_type = ? AND aggregate_key = ?
ORDER BY revision ASC;`

	rows, err := s.db.QueryContext(ctx, query, id.Type, id.Key)
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

func (s *Store) Publish(ctx context.Context, id we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	if len(events) == 0 {
		// Empty publish is a no-op (matches the suite's EmptyPublish semantics).
		// The no-op deliberately short-circuits BEFORE override validation: with
		// zero events the nil encoder is never used and nothing can be
		// mis-recorded, so an empty publish is a state, not an error.
		return nil
	}

	// Resolve the publish encoder before encoding: an explicit per-publish
	// override wins, and an explicit nil override is an error that records
	// nothing (ENCODING-S2.R3, ENCODING-S2.R5).
	encoder, err := options.EncoderFor(s.encoder)
	if err != nil {
		return fmt.Errorf("sqlite: %w", err)
	}

	rows, err := encodeEvents(encoder, options, events)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= busyRetries; attempt++ {
		err := s.publishOnce(ctx, id, options, rows)
		if err == nil {
			return nil
		}
		// A revision conflict is a terminal state, never retried (SQLITE-S2.R5).
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

func (s *Store) publishOnce(ctx context.Context, id we.AggregateId, options we.PublishOptions, rows []eventRow) (err error) {
	conn, connErr := s.db.Conn(ctx)
	if connErr != nil {
		return fmt.Errorf("sqlite: failed to acquire connection: %w", connErr)
	}
	defer func() { _ = conn.Close() }()

	// busy_timeout is per-connection and the pool may hand out a connection
	// that never had it set; without it BEGIN IMMEDIATE fails fast with
	// SQLITE_BUSY instead of waiting out a concurrent writer.
	if err := applyBusyTimeout(ctx, conn, s.busyTimeout); err != nil {
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
