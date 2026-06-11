package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

type testEvent struct {
	Value string `json:"value"`
}

func newMemoryStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func newFileStore(t *testing.T, path string) *Store {
	t.Helper()
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(path, Global()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

// storeShardDB returns the *sql.DB backing the partition that id routes to,
// provisioning the shard. Test-only: lets a test inject malformed/duplicate
// rows behind the store, as the old tests did via the removed Store.db field.
func storeShardDB(t *testing.T, ctx context.Context, store *Store, id we.AggregateId) *sql.DB {
	t.Helper()
	p := store.strategy.PartitionFor(id)
	sh, err := store.ensureShard(ctx, p)
	require.NoError(t, err)
	return sh.db
}

func makeAggregateId() we.AggregateId {
	return we.AggregateId{Type: "sqlite-test", Key: we.NewRevisionGenerator().NewRevision(time.Now()).String()}
}

// SQLITE-S1.R1 — *Store satisfies we.EventStore. Compile-time assertion lives
// in store.go; this exercises it through the conformance suite against the
// in-memory and local-file targets (SQLITE-S1.R2, SQLITE-S1.R3, SQLITE-S1.R4,
// SQLITE-S2.R1, SQLITE-S2.R2, SQLITE-S2.R4, plus causation metadata).
func TestEventStoreValidationSuiteInMemory(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)
	we.NewEventStoreValidationSuite(ctx, store).Run(t)
}

func TestEventStoreValidationSuiteLocalFile(t *testing.T) {
	ctx := context.Background()
	store := newFileStore(t, filepath.Join(t.TempDir(), "events.db"))
	we.NewEventStoreValidationSuite(ctx, store).Run(t)
}

// SQLITE-S1.R4 — an aggregate with no rows loads as InitialRevision with no
// events.
func TestLoadInitial(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	aggregate, err := store.Load(ctx, id)
	require.NoError(t, err)

	assert.Empty(t, aggregate.Events)
	assert.Equal(t, we.InitialRevision, aggregate.Revision)
	assert.Equal(t, id, aggregate.Id)
}

// SQLITE-S2.R3 — a UNIQUE (aggregate_type, aggregate_key, revision) violation
// surfaces as we.RevisionConflict, not a raw driver error. Forced directly by
// inserting a duplicate revision row behind the store.
func TestUniqueViolationMapsToRevisionConflict(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "first"}))

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	revision := loaded.Events[0].Revision.String()

	// Insert a second row with the SAME revision for the same aggregate,
	// violating the unique index exactly as a lost concurrency race would.
	_, err = storeShardDB(t, ctx, store, id).ExecContext(ctx, `
INSERT INTO events
    (event_id, aggregate_type, aggregate_key, event_type, revision, causation_id, correlation_id, encoding, data)
VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, ?);`,
		"0000000000000000000000DUPE", id.Type, id.Key, "dupe", revision, "application/json", []byte("{}"))

	require.Error(t, err)
	assert.True(t, isUniqueViolation(err), "expected a unique-constraint violation, got: %v", err)
	assert.False(t, isBusy(err), "a unique violation must not be classified as transient busy")
}

// An event_id PRIMARY KEY collision is a different defect (a broken id source),
// not an optimistic-concurrency race; classifying it as we.RevisionConflict
// would tell the caller to reload and retry a write that can never succeed.
func TestEventIdCollisionIsNotARevisionConflict(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "first"}))

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	eventID := loaded.Events[0].EventID.String()

	// Re-insert the SAME event_id at a fresh revision: only the primary key is
	// violated, not the aggregate concurrency index.
	_, err = storeShardDB(t, ctx, store, id).ExecContext(ctx, `
INSERT INTO events
    (event_id, aggregate_type, aggregate_key, event_type, revision, causation_id, correlation_id, encoding, data)
VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, ?);`,
		eventID, id.Type, id.Key, "dupe", revisionForSequence(99).String(), "application/json", []byte("{}"))

	require.Error(t, err)
	assert.False(t, isUniqueViolation(err), "a primary-key collision must not classify as a revision conflict, got: %v", err)
	assert.False(t, isBusy(err), "a primary-key collision must not be classified as transient busy")
}

// SQLITE-S2.R4 — a stale ExpectedRevision returns we.RevisionConflict and
// persists none of the batch (transactional all-or-nothing).
func TestStaleExpectedRevisionPersistsNothing(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "seed"}))

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	stale := loaded.Revision

	// Advance the aggregate so the captured revision is now stale.
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "advance"}))

	batch := []we.DomainEvent{testEvent{Value: "a"}, testEvent{Value: "b"}, testEvent{Value: "c"}}
	err = store.Publish(ctx, id, we.Options(we.WithExpectedRevision(stale)), batch...)
	require.ErrorIs(t, err, we.RevisionConflict)

	after, err := store.Load(ctx, id)
	require.NoError(t, err)
	// Only the two committed events remain; none of the conflicting batch persisted.
	assert.Equal(t, 2, len(after.Events))
}

// SQLITE-S2.R1 — InitialRevision commits only against an empty aggregate.
func TestExpectedInitialRevisionConflictsOnNonEmpty(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "seed"}))

	err := store.Publish(ctx, id, we.Options(we.WithExpectedRevision(we.InitialRevision)), testEvent{Value: "next"})
	require.ErrorIs(t, err, we.RevisionConflict)
}

// SQLITE-S4.R1, SQLITE-S4.R2 — payload bytes and encoding round-trip verbatim
// for a recognised encoding.
func TestPayloadRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "hello"}))

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, len(loaded.Events))

	got := loaded.Events[0]
	assert.Equal(t, "application/json", got.Data.Encoding)

	var decoded testEvent
	require.NoError(t, we.UnmarshalFromData(got.Data, &decoded))
	assert.Equal(t, testEvent{Value: "hello"}, decoded)
}

// SQLITE-S4.R3 — a payload written with an unknown encoding is stored and
// returned unchanged, neither rejected nor rewritten. Written behind the
// store because the public Publish path always encodes via a registered
// encoder (constructor or explicit override); the store's
// codec-agnostic contract is about what it persists and returns, not what the
// caller chose to encode.
func TestUnknownEncodingRoundTripsUnchanged(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	rev := we.NewRevisionGenerator().NewRevision(time.Now()).String()
	payload := []byte{0x01, 0x02, 0x03, 0xff}
	encoding := "application/x-not-a-real-encoding"

	_, err := storeShardDB(t, ctx, store, id).ExecContext(ctx, `
INSERT INTO events
    (event_id, aggregate_type, aggregate_key, event_type, revision, causation_id, correlation_id, encoding, data)
VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, ?);`,
		rev, id.Type, id.Key, "raw", rev, encoding, payload)
	require.NoError(t, err)

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, len(loaded.Events))

	got := loaded.Events[0]
	assert.Equal(t, encoding, got.Data.Encoding)
	assert.Equal(t, payload, []byte(got.Data.Data))
}

// SQLITE-S3.R3 / CONFORMANCE-S5 — the SQLite store is the first adopter of the
// shared-backing conformance suite: two instances over one local file must each
// observe the other's commits (blind appends) and conflict on a stale
// cross-instance revision.
func TestSharedBackingConformance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared.db")

	first := newFileStore(t, path)
	second := newFileStore(t, path)

	we.NewSharedBackingSuite(ctx, first, second).Run(t)
}

// SQLITE-S3.R1, SQLITE-S3.R2 — the one option set selects in-memory and
// local-file targets with identical Load/Publish behaviour.
func TestTargetParity(t *testing.T) {
	ctx := context.Background()
	stores := map[string]*Store{
		"memory": newMemoryStore(t),
		"file":   newFileStore(t, filepath.Join(t.TempDir(), "parity.db")),
	}

	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			id := makeAggregateId()
			require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}, testEvent{Value: "y"}))
			loaded, err := store.Load(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, 2, len(loaded.Events))
		})
	}
}

// SQLITE-S3.R4 — the package doc comment records the chosen driver and target
// matrix. Verified by asserting the documented driver name is the one the
// store opens against.
func TestDocumentedDriver(t *testing.T) {
	// driverName is the value recorded in the package doc comment and passed
	// to sql.Open. A mismatch would mean the doc and the code disagree.
	assert.Equal(t, "libsql", driverName)
}

// Empty publish is a no-op returning no error.
func TestEmptyPublishIsNoOp(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	require.NoError(t, store.Publish(ctx, id, we.Options()))

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, loaded.Events)
	assert.Equal(t, we.InitialRevision, loaded.Revision)
}

// SQLITE-S3.R3 (concurrency) — two store instances over the same file
// hammering unconditioned appends to one aggregate. BEGIN IMMEDIATE plus
// busy_timeout serialize the writers, so every append commits at a fresh
// sequence: no lost writes, no duplicate revisions, no raw driver errors. (Any
// error that did surface must be a typed conflict, never a leaked SQLITE_BUSY.)
func TestConcurrentAppendsSerialize(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "race.db")
	first := newFileStore(t, path)
	second := newFileStore(t, path)

	id := makeAggregateId()

	const perWriter = 25
	var wg sync.WaitGroup
	writer := func(store *Store) {
		defer wg.Done()
		for range perWriter {
			// BEGIN IMMEDIATE plus busy_timeout serialize unconditioned appends,
			// so every publish must commit — the count assertion below depends
			// on all 2*perWriter events landing.
			assert.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "race"}))
		}
	}
	wg.Add(2)
	go writer(first)
	go writer(second)
	wg.Wait()

	loaded, err := first.Load(ctx, id)
	require.NoError(t, err)
	// Serialized appends commit every event at a distinct sequential revision.
	require.Equal(t, 2*perWriter, len(loaded.Events), "expected all serialized appends to commit")
	seen := map[we.Revision]bool{}
	for i, e := range loaded.Events {
		require.False(t, seen[e.Revision], "duplicate revision %s", e.Revision)
		seen[e.Revision] = true
		assert.Equal(t, revisionForSequence(uint64(i)+1), e.Revision, "revisions must be a dense 1-based sequence")
	}
}

// ENCODING-S2.R2 — a nil constructor encoder is an error, not a deferred panic.
func TestNilEncoderRejectedAtConstruction(t *testing.T) {
	store, err := NewStore(context.Background(), nil, InMemory(Global()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encoder is required")
	assert.Nil(t, store)
}

// ENCODING-S2.R3 / ENCODING-S3.R2 — a CBOR override on a JSON store produces a
// mixed-encoding stream that loads and decodes per event.
func TestPerPublishEncoderOverride(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t) // JSON constructor encoder
	id := makeAggregateId()

	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "json"}))
	require.NoError(t, store.Publish(ctx, id, we.Options(we.WithEncoder(we.MakeCBOREncoder())), testEvent{Value: "cbor"}))

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	require.Len(t, loaded.Events, 2)
	assert.Equal(t, we.JSONEncoding, loaded.Events[0].Data.Encoding)
	assert.Equal(t, we.CBOREncoding, loaded.Events[1].Data.Encoding)
	for i, want := range []string{"json", "cbor"} {
		var decoded testEvent
		require.NoError(t, we.UnmarshalFromData(loaded.Events[i].Data, &decoded))
		assert.Equal(t, want, decoded.Value)
	}
}

// ENCODING-S3.R2 — a store constructed with the CBOR encoder publishes
// envelopes carrying application/cbor with verbatim payload bytes.
func TestCBORConstructorEncoderRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeCBOREncoder(), InMemory(Global()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	id := makeAggregateId()
	event := testEvent{Value: "cbor-constructor"}
	require.NoError(t, store.Publish(ctx, id, we.Options(), event))

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	require.Len(t, loaded.Events, 1)

	got := loaded.Events[0].Data
	assert.Equal(t, we.CBOREncoding, got.Encoding)

	// Verbatim bytes: the store persists exactly what the constructor encoder
	// produced (fxamacker/cbor's default mode encodes this struct
	// deterministically, so byte equality is sound).
	want, err := we.MakeCBOREncoder().Encode(event)
	require.NoError(t, err)
	assert.Equal(t, []byte(want.Data), []byte(got.Data))

	var decoded testEvent
	require.NoError(t, we.UnmarshalFromData(got, &decoded))
	assert.Equal(t, event, decoded)
}

// SURFACE-S3.R1 — an undecodable event_id fails the load; it never yields a
// RecordedEvent carrying an empty fabricated timestamp.
func TestUndecodableEventIdFailsLoad(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)
	id := makeAggregateId()
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "good"}))

	// 26 chars, satisfies the CHECK, not a ULID (I, L, O, U are invalid base32).
	_, err := storeShardDB(t, ctx, store, id).ExecContext(ctx, `
INSERT INTO events
    (event_id, aggregate_type, aggregate_key, event_type, revision, causation_id, correlation_id, encoding, data)
VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, ?);`,
		"IIIIIIIIIIIIIIIIIIIIIIIIII", id.Type, id.Key, "bad", revisionForSequence(2).String(), "application/json", []byte("{}"))
	require.NoError(t, err)

	_, err = store.Load(ctx, id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid event id "IIIIIIIIIIIIIIIIIIIIIIIIII"`)
}

// ENCODING-S2.R5 — an explicit nil override errors and records nothing.
func TestNilEncoderOverrideRejectedAtPublish(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)
	id := makeAggregateId()

	err := store.Publish(ctx, id, we.Options(we.WithEncoder(nil)), testEvent{Value: "x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, we.NilEncoder)
	assert.Contains(t, err.Error(), "encoder must not be nil")

	loaded, err := store.Load(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, loaded.Events)
}

// Sanity: the conformance suite's RevisionConflict tests already cover the
// caller-level loop; this guards that the store never silently swallows a
// non-conflict driver error as success.
func TestLoadOnClosedStoreErrors(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	require.NoError(t, err)
	require.NoError(t, store.Close())

	_, err = store.Load(ctx, makeAggregateId())
	require.Error(t, err)
	assert.False(t, errors.Is(err, sql.ErrNoRows))
}
