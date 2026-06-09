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
	store, err := NewStore(ctx, InMemory())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func newFileStore(t *testing.T, path string) *Store {
	t.Helper()
	ctx := context.Background()
	store, err := NewStore(ctx, LocalFile(path))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
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

// SQLITE-S1.R5 — NewStore returns an error and no store value when the target
// cannot be opened or migrated.
func TestNewStoreRequiresTarget(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx)
	require.Error(t, err)
	assert.Nil(t, store)
}

func TestNewStoreInvalidLocalFile(t *testing.T) {
	ctx := context.Background()
	// A path under a non-existent directory cannot be opened or migrated.
	store, err := NewStore(ctx, LocalFile(filepath.Join(t.TempDir(), "missing-dir", "events.db")))
	require.Error(t, err)
	assert.Nil(t, store)
}

func TestNewStoreEmptyLocalFile(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, LocalFile("  "))
	require.Error(t, err)
	assert.Nil(t, store)
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
	_, err = store.db.ExecContext(ctx, `
INSERT INTO events
    (event_id, aggregate_type, aggregate_key, event_type, revision, causation_id, correlation_id, encoding, data)
VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, ?);`,
		"0000000000000000000000DUPE", id.Type, id.Key, "dupe", revision, "application/json", []byte("{}"))

	require.Error(t, err)
	assert.True(t, isUniqueViolation(err), "expected a unique-constraint violation, got: %v", err)
	assert.False(t, isBusy(err), "a unique violation must not be classified as transient busy")
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
// store because the public Publish path always encodes JSON; the store's
// codec-agnostic contract is about what it persists and returns, not what the
// caller chose to encode.
func TestUnknownEncodingRoundTripsUnchanged(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(t)

	id := makeAggregateId()
	rev := we.NewRevisionGenerator().NewRevision(time.Now()).String()
	payload := []byte{0x01, 0x02, 0x03, 0xff}
	encoding := "application/x-not-a-real-encoding"

	_, err := store.db.ExecContext(ctx, `
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

// busy_timeout is a per-connection SQLite setting, but database/sql is a pool:
// a publish that lands on a freshly created pool connection must still wait
// out a held write lock rather than failing fast with SQLITE_BUSY. The test
// pins the store's only idle (pragma-bearing) connection so Publish is forced
// onto a fresh one, while a second store instance holds the file's write lock
// for longer than the in-store immediate-retry loop can absorb.
func TestBusyTimeoutAppliesToPoolConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "busy.db")

	blocker := newFileStore(t, path)
	publisher := newFileStore(t, path)

	// Pin the connection prepare() configured; Publish must open a fresh one.
	pinned, err := publisher.db.Conn(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, pinned.Close()) }()

	// Take the write lock from the other instance and hold it briefly.
	lock, err := blocker.db.Conn(ctx)
	require.NoError(t, err)
	_, err = lock.ExecContext(ctx, "BEGIN IMMEDIATE")
	require.NoError(t, err)

	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(250 * time.Millisecond)
		_, rbErr := lock.ExecContext(ctx, "ROLLBACK")
		assert.NoError(t, rbErr)
		assert.NoError(t, lock.Close())
	}()

	// With busy_timeout active on the fresh connection the engine waits for the
	// lock; without it BEGIN IMMEDIATE fails fast and the bounded retry loop is
	// exhausted while the lock is still held.
	err = publisher.Publish(ctx, makeAggregateId(), we.Options(), testEvent{Value: "waited"})
	<-released
	require.NoError(t, err)
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
			err := store.Publish(ctx, id, we.Options(), testEvent{Value: "race"})
			if err != nil {
				// Unconditioned appends never set an expected revision, so the
				// only acceptable failure is a typed conflict — never a raw busy.
				assert.ErrorIs(t, err, we.RevisionConflict)
			}
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

// Sanity: the conformance suite's RevisionConflict tests already cover the
// caller-level loop; this guards that the store never silently swallows a
// non-conflict driver error as success.
func TestLoadOnClosedStoreErrors(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, InMemory())
	require.NoError(t, err)
	require.NoError(t, store.Close())

	_, err = store.Load(ctx, makeAggregateId())
	require.Error(t, err)
	assert.False(t, errors.Is(err, sql.ErrNoRows))
}
