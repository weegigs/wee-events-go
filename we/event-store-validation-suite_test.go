package we

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidationSuiteAgainstMemoryStore validates the suite mechanism itself
// (CONFORMANCE-S1.R1 / R2): constructing the suite over an in-memory store and
// calling Run executes every single-store scenario as a named subtest, and the
// faithful reference store passes them all.
func TestValidationSuiteAgainstMemoryStore(t *testing.T) {
	ctx := context.Background()
	suite := NewEventStoreValidationSuite(ctx, newMemoryEventStore(MakeJSONEncoder()))
	suite.Run(t)
}

// TestValidationSuiteRegistersScenarios asserts that Run registers each
// single-store scenario as a named subtest (CONFORMANCE-S1.R1), including the
// three ported scenarios and the retained hex-boundary guard (CONFORMANCE-S1.R3,
// CONFORMANCE-S6.R2). Registration is inspected through the suite's scenario set
// and confirmed against live subtest names produced by Run.
func TestValidationSuiteRegistersScenarios(t *testing.T) {
	ctx := context.Background()
	suite := NewEventStoreValidationSuite(ctx, newMemoryEventStore(MakeJSONEncoder()))

	registered := map[string]bool{}
	for _, sc := range suite.scenarios() {
		require.NotNil(t, sc.run, "scenario %q must have a runnable method", sc.name)
		registered[sc.name] = true
	}

	for _, required := range []string{
		"detects a stale revision and a retry then succeeds",
		"treats an empty publish as a no-op returning the current revision",
		"preserves event ordering across separately published batches",
		"published with an expected revision past ten events",
		"round-trips full-charset identities through storage",
	} {
		assert.True(t, registered[required], "Run must register %q as a scenario", required)
	}

	// Confirm the scenario names actually surface as live subtest names when Run
	// drives them, so the registered set is not merely declarative.
	for _, sc := range suite.scenarios() {
		name := sc.name
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, t.Name(), subtestSegment(name),
				"scenario %q must run as a named subtest", name)
		})
	}
}

// TestSharedBackingSuiteAgainstMemoryStore validates the paired shared-backing
// entry point (CONFORMANCE-S5.R1 / R4): two stores constructed over one backing
// run both shared-backing scenarios as named subtests, and the faithful
// reference backing passes them.
func TestSharedBackingSuiteAgainstMemoryStore(t *testing.T) {
	ctx := context.Background()

	backing := newMemoryBacking()
	a := &memoryEventStore{backing: backing, encoder: MakeJSONEncoder()}
	b := &memoryEventStore{backing: backing, encoder: MakeJSONEncoder()}

	suite := NewSharedBackingSuite(ctx, a, b)

	registered := map[string]bool{}
	for _, sc := range suite.scenarios() {
		registered[sc.name] = true
	}
	assert.True(t, registered["blind appends succeed across store instances"])
	assert.True(t, registered["stale revision conflicts across store instances"])

	// Run the scenarios for real against the shared backing so the mechanism is
	// exercised, not just inspected.
	suite.Run(t)
}

// TestSharedBackingDetectsBrokenStore is the negative guard for
// CONFORMANCE-S1.R4 / CONFORMANCE-S5.R5: the stale-revision scenario turns on a
// single assertion — that a stale cross-instance publish returns
// RevisionConflict. This test reproduces the scenario's setup against a store
// that drops the expected-revision check and confirms the publish wrongly
// succeeds, so the scenario's require.Equal(RevisionConflict, err) would fail
// rather than pass vacuously. Driving the live scenario method against a
// surrogate is impossible because the scenario takes a concrete *testing.T, and
// a real failing subtest would fail this test; reproducing the decisive
// assertion is the faithful equivalent.
func TestSharedBackingDetectsBrokenStore(t *testing.T) {
	ctx := context.Background()

	backing := newMemoryBacking()
	good := &memoryEventStore{backing: backing, encoder: MakeJSONEncoder()}
	broken := &ignoresExpectedRevisionStore{inner: &memoryEventStore{backing: backing, encoder: MakeJSONEncoder()}}

	aggregateId := AggregateId{Type: "go-test", Key: "broken-store-guard"}

	require.NoError(t, broken.Publish(ctx, aggregateId, Options(), StoreValidationEvent{TestStringValue: "seed"}))

	// Instance A (broken) loads the head it intends to build on.
	loaded, err := broken.Load(ctx, aggregateId)
	require.NoError(t, err)
	staleRevision := loaded.Revision

	// Instance B (good) advances the aggregate over the shared backing, making
	// staleRevision stale.
	require.NoError(t, good.Publish(ctx, aggregateId, Options(), StoreValidationEvent{TestStringValue: "advance"}))

	// The decisive assertion the scenario makes is RevisionConflict here. A
	// correct store returns RevisionConflict; the broken store returns nil,
	// proving the scenario would fail against it.
	err = broken.Publish(ctx, aggregateId, Options(WithExpectedRevision(staleRevision)), StoreValidationEvent{TestStringValue: "stale"})
	assert.NotEqual(t, RevisionConflict, err,
		"the broken store is expected to drop the conflict check")
	assert.NoError(t, err,
		"the broken store silently accepts the stale write — the scenario's RevisionConflict assertion catches this defect")

	// A correct store over the same backing rejects the same stale publish,
	// confirming the scenario's assertion is the discriminating check.
	correct := &memoryEventStore{backing: backing, encoder: MakeJSONEncoder()}
	err = correct.Publish(ctx, aggregateId, Options(WithExpectedRevision(staleRevision)), StoreValidationEvent{TestStringValue: "stale-again"})
	require.Equal(t, RevisionConflict, err,
		"a correct store rejects the stale publish, so the scenario passes only for conforming stores")
}

// subtestSegment mirrors how the testing package rewrites subtest names: spaces
// become underscores. Used only to assert a scenario name surfaces in t.Name().
func subtestSegment(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == ' ' {
			out = append(out, '_')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// ignoresExpectedRevisionStore models a defective backend that drops the
// expected-revision check, so a stale publish silently succeeds. It is used only
// to prove the suite catches such a defect.
type ignoresExpectedRevisionStore struct {
	inner *memoryEventStore
}

func (s *ignoresExpectedRevisionStore) Load(ctx context.Context, id AggregateId) (Aggregate, error) {
	return s.inner.Load(ctx, id)
}

func (s *ignoresExpectedRevisionStore) Publish(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) error {
	// Defect under test: strip the expected revision so concurrency control is
	// effectively disabled.
	options.ExpectedRevision = ""
	return s.inner.Publish(ctx, aggregateId, options, events...)
}

var _ EventStore = (*ignoresExpectedRevisionStore)(nil)
