# Feature 04 — Storage Verification Conformance Parity

- **Status:** Planned · **Size:** M · **Area:** core + tests (`we/event-store-validation-suite.go` + per-store `*_test.go`)
- **Coordinates with:** [Feature 02](02-sqlite-turso-store.md) (the new SQLite store is the natural first adopter of the shared-backing suite)
- **Prefix:** `CONFORMANCE`

## Summary

Bring the Go event-store conformance suite up to parity with the `wee-events.rs` sibling.
The Rust suite has 14 single-store scenarios plus 2 shared-backing scenarios; the Go
`EventStoreValidationSuite` has 11 single-store scenarios. This feature ports the 5
missing scenarios — three single-store and two shared-backing — and retains the existing
Go-specific hex-boundary scenario, so every backend (the existing `ds`, `jetstream`, and
`kurrent` stores, plus the new SQLite store from Feature 02) is validated against one
contract.

The suite is the executable check that a store upholds the framework's persistence
invariants — single-responsibility persistence that preserves revision ordering, optimistic
concurrency, and the no-op semantics of an empty publish. Where a backend fails an added
scenario, that is a real defect surfaced by the new coverage; the backend is fixed, not the
test weakened.

## Decisions

- No ADRs. This feature is additive test coverage: it extends an existing suite and wires
  new scenarios into existing per-store test files. It introduces no new architectural
  decision and changes no production contract — in particular, `EventStore.Publish` keeps
  its `error`-only signature (see Implementation notes).

## User stories

### CONFORMANCE-S1 — Validate a backend against the single-store suite

*As a backend author, I want to validate a store implementation against one shared
conformance suite, so that every backend is held to the same persistence contract without
each author re-deriving the scenarios.*

Upholds principle 1 (single responsibility): the suite asserts that a store persists and
replays bytes faithfully and never interprets payloads.

- **CONFORMANCE-S1.R1** (ubiquitous) — The framework shall expose an
  `EventStoreValidationSuite` whose `Run(t *testing.T)` registers each single-store
  scenario as a named subtest.
- **CONFORMANCE-S1.R2** (event-driven) — When a backend constructs the suite over its store
  and calls `Run(t)`, the framework shall execute every registered single-store scenario
  against that store.
- **CONFORMANCE-S1.R3** (event-driven) — When the three ported single-store scenarios are
  added to the suite, the framework shall register them in `Run(t)` so existing backends
  pick them up without any per-backend wiring change beyond re-running.
- **CONFORMANCE-S1.R4** (unwanted) — If a backend fails any registered scenario, then the
  framework shall report the failing subtest by name and shall not pass the suite.

### CONFORMANCE-S2 — Cover the optimistic-retry path

*As a backend author, I want the suite to exercise the real-world optimistic-retry path, so
that a store's concurrency control is proven end to end, not just in isolated conflict
checks.*

Ports Rust `stale_revision_detected_and_retry_succeeds`. Upholds principle 3 (illegal states
unrepresentable): a stale expected revision must be rejected, never silently accepted.

- **CONFORMANCE-S2.R1** (event-driven) — When an aggregate is loaded, a concurrent write
  advances its revision, and a publish is then attempted with the now-stale expected
  revision, the framework shall fail that publish with a revision conflict.
- **CONFORMANCE-S2.R2** (event-driven) — When the caller reloads the aggregate after a stale
  conflict and retries the publish with the fresh expected revision, the framework shall
  accept the retry.
- **CONFORMANCE-S2.R3** (state-driven) — While the retry path completes, the suite shall
  observe revisions through `Load` between steps and assert the aggregate holds all three
  events in order.
- **CONFORMANCE-S2.R4** (unwanted) — If the publish carrying the stale expected revision were
  to succeed, then the scenario shall fail, since accepting a stale write would silently lose
  the concurrent commit.

### CONFORMANCE-S3 — Treat an empty publish as a no-op state

*As a backend author, I want publishing an empty event list to be a no-op that returns the
current revision, so that "nothing to record" is a normal state outcome rather than an
error.*

Ports Rust `empty_publish_returns_current_revision`. Upholds principle 3, "State is not an
error": an empty publish reports state, it does not fail.

- **CONFORMANCE-S3.R1** (event-driven) — When a publish is issued with an empty event list,
  the framework shall treat it as a no-op and shall not return an error.
- **CONFORMANCE-S3.R2** (event-driven) — When an empty publish completes, a follow-up `Load`
  shall report the aggregate's revision and event set unchanged from before the publish.
- **CONFORMANCE-S3.R3** (unwanted) — If an empty publish returns an error or mutates the
  aggregate's revision or events, then the scenario shall fail.

### CONFORMANCE-S4 — Load events in strictly ascending revision order

*As a backend author, I want events from separately published batches to load back in
strictly ascending revision order, so that reducers fold a deterministic history regardless
of how writes were batched.*

Ports Rust `event_ordering_preserved`. Upholds principle 1: ordering is the store's
responsibility, independent of payload content.

- **CONFORMANCE-S4.R1** (event-driven) — When events recorded across separately published
  batches are loaded, the framework shall return them in strictly ascending revision order.
- **CONFORMANCE-S4.R2** (unwanted) — If any loaded event appears out of revision order or a
  revision is duplicated, then the scenario shall fail.

### CONFORMANCE-S5 — Two store instances over one backing observe and conflict correctly

*As a backend author whose store can open two instances over one backing, I want a paired
shared-backing suite, so that cross-instance visibility and cross-instance concurrency
control are both proven.*

Ports Rust `blind_appends_succeed_across_store_instances` and
`stale_revision_conflicts_across_store_instances`, wired in Rust through
`shared_store_test_suite!`. Upholds principle 3: a stale revision must conflict even when the
advancing write came from a different instance over the same backing.

- **CONFORMANCE-S5.R1** (ubiquitous) — The framework shall expose a paired entry point
  `NewSharedBackingSuite(ctx, a, b EventStore)` and `(*SharedBackingSuite).Run(t)` for stores
  that can produce two instances over one backing.
- **CONFORMANCE-S5.R2** (event-driven) — When two independent instances over the same backing
  each blind-append to the same aggregate, the framework shall persist both writes and each
  instance shall observe the other's commits on a subsequent `Load`.
- **CONFORMANCE-S5.R3** (event-driven) — When a revision is loaded from instance A and
  instance B then advances the aggregate, a publish from A carrying that now-stale expected
  revision shall fail with a revision conflict.
- **CONFORMANCE-S5.R4** (event-driven) — When a backend's `*_test.go` constructs two instances
  over one backing and calls the paired `Run(t)`, the framework shall execute both
  shared-backing scenarios against that pair.
- **CONFORMANCE-S5.R5** (unwanted) — If the stale cross-instance publish were to succeed, or
  if either instance failed to observe the other's commits, then the scenario shall fail.

### CONFORMANCE-S6 — Retain the hex-boundary base-encoding guard

*As a maintainer, I want to keep the Go-specific revision base-encoding boundary test, so
that the suite guards a boundary the Rust suite's smaller batch would miss.*

Retains the existing Go `PublishesWithAnExpectedRevisionPastTenEvents` scenario, which has no
Rust equivalent.

- **CONFORMANCE-S6.R1** (event-driven) — When 12 events are seeded so that revision 10 encodes
  as `0x0a`, a publish with an expected revision past the ten-event boundary shall succeed.
- **CONFORMANCE-S6.R2** (ubiquitous) — The framework shall retain this scenario in the suite,
  since Rust's `publishes_with_expected_revision` uses a smaller batch and would miss the
  base-encoding boundary. It is worth porting *to* Rust later.

## Implementation notes

### Current Go state

`we/event-store-validation-suite.go` defines `EventStoreValidationSuite` and a `Run(t)` that
registers each scenario as a subtest:

```go
func (s *EventStoreValidationSuite) Run(t *testing.T) {
	t.Run("loads an initial revision", s.LoadInitial)
	t.Run("loads a revision with events", s.LoadsRevisionWithEvents)
	t.Run("publishes single event", s.PublishesSingleEvent)
	t.Run("publishes multiple events in a single transaction", s.PublishesMultipleEvents)
	t.Run("preserves the event content when recording", s.ValidateEventContent)
	t.Run("published with an expected initial revision", s.PublishesWithAnExpectedInitialRevision)
	t.Run("published with an expected revision", s.PublishesWithAnExpectedRevision)
	t.Run("published with an expected revision past ten events", s.PublishesWithAnExpectedRevisionPastTenEvents)
	t.Run("returns a revision conflict with an initial revision", s.RevisionConflictOnInitialRevision)
	t.Run("returns a revision conflict on subsequent revision", s.RevisionConflictOnSubsequentRevision)
	t.Run("supports causation id", s.Causation)
}
```

Backends opt in from their own test file, e.g. `stores/kurrent/event-store_test.go:25`:

```go
t.Run("kurrentdb event store validation", func(t *testing.T) {
	suite := we.NewEventStoreValidationSuite(ctx, store)
	suite.Run(t)
})
```

### Rust reference (port origin)

`crates/wee-events/src/test_suite.rs`. The five scenarios present in Rust but missing from
Go:

1. **`stale_revision_detected_and_retry_succeeds`** — the optimistic-retry path
   (CONFORMANCE-S2): load, concurrent write advances the aggregate, publish with the now-stale
   expected revision conflicts, reload, retry with the fresh revision, retry succeeds (3
   events total).
2. **`empty_publish_returns_current_revision`** — an empty event list is a no-op returning the
   current revision unchanged (CONFORMANCE-S3); a state, not an error.
3. **`event_ordering_preserved`** — events from separately published batches load back in
   strictly ascending revision order (CONFORMANCE-S4).
4. **`blind_appends_succeed_across_store_instances`** *(shared-backing)* — two independent
   instances over one backing both write and each observes the other's commits
   (CONFORMANCE-S5.R2).
5. **`stale_revision_conflicts_across_store_instances`** *(shared-backing)* — a stale revision
   loaded from instance A conflicts after instance B advances the aggregate
   (CONFORMANCE-S5.R3).

In Rust the single-store scenarios are wired through `store_test_suite!` and the
shared-backing pair through `shared_store_test_suite!`; backends opt into the shared-backing
variant by supplying a store *pair* over one backing.

### Go target

Add three single-store methods to `EventStoreValidationSuite` and register them in `Run(t)`
(satisfies CONFORMANCE-S1.R3):

- `StaleRevisionDetectedAndRetrySucceeds(t *testing.T)` (CONFORMANCE-S2)
- `EmptyPublishReturnsCurrentRevision(t *testing.T)` (CONFORMANCE-S3)
- `EventOrderingPreserved(t *testing.T)` (CONFORMANCE-S4)

Add a paired shared-backing entry point for the two-instance scenarios. The current
`EventStoreValidationSuite` holds a single `store`; introduce a paired constructor and a
paired `Run` (satisfies CONFORMANCE-S5.R1):

```go
func NewSharedBackingSuite(ctx context.Context, a, b EventStore) *SharedBackingSuite
func (s *SharedBackingSuite) Run(t *testing.T) // BlindAppends…, StaleRevisionConflicts…
```

A backend that can produce two instances over one backing (a SQLite file, the same DynamoDB
table, the same Kurrent/NATS server) opts in the same way it opts into the single-store suite.

### Signature note

The Go `EventStore.Publish` returns only `error` — there is no returned `ChangeSet` (unlike
Rust's `publish`, which returns the new revision). Therefore:

- `EmptyPublishReturnsCurrentRevision` verifies via a follow-up `Load` that the revision and
  event set are unchanged, rather than inspecting a return value (CONFORMANCE-S3.R2).
- The retry scenario observes revisions through `Load` between steps (CONFORMANCE-S2.R3).

The `EventStore` signature is **not** changed to carry a returned revision — verifying through
`Load` is sufficient and keeps the contract stable for all existing backends. This is why the
feature carries no ADR.

### Retained Go-specific scenario

`PublishesWithAnExpectedRevisionPastTenEvents` (the hex-boundary base-encoding guard, seeding
12 events so revision 10 = `0x0a`) has no Rust equivalent and is retained as CONFORMANCE-S6.
It is worth porting *to* Rust later, since Rust's `publishes_with_expected_revision` uses a
smaller batch and would miss that boundary.

### Wiring

- The three new single-store scenarios are picked up automatically by every backend through
  the existing `suite.Run(t)` call — no per-backend change beyond re-running.
- The two shared-backing scenarios require each backend's `*_test.go` to construct two
  instances over one backing and call the paired `Run`. Each store's worker owns that edit in
  their own test file — the only per-store change — which avoids cross-team contention on the
  suite file itself.

## Verification

| Requirement | Test |
|---|---|
| CONFORMANCE-S1.R1, CONFORMANCE-S1.R2 | Construct `EventStoreValidationSuite` over an in-memory test store and call `Run(t)`; assert every single-store scenario runs as a named subtest. |
| CONFORMANCE-S1.R3 | Assert `Run(t)` registers `StaleRevisionDetectedAndRetrySucceeds`, `EmptyPublishReturnsCurrentRevision`, and `EventOrderingPreserved`; existing `ds`/`jetstream`/`kurrent` backends pick them up with no per-backend wiring change. |
| CONFORMANCE-S1.R4 | A backend that violates a scenario fails the named subtest and the suite does not pass. |
| CONFORMANCE-S2.R1, CONFORMANCE-S2.R2, CONFORMANCE-S2.R3 | `StaleRevisionDetectedAndRetrySucceeds`: load, concurrent write, publish with stale revision conflicts, reload, retry succeeds; assert 3 events in order via `Load`. |
| CONFORMANCE-S2.R4 | Assert the stale-revision publish returns a conflict, not success. |
| CONFORMANCE-S3.R1, CONFORMANCE-S3.R2 | `EmptyPublishReturnsCurrentRevision`: empty publish returns no error; a follow-up `Load` shows revision and event set unchanged. |
| CONFORMANCE-S3.R3 | Assert no error and no mutation of revision or events after an empty publish. |
| CONFORMANCE-S4.R1, CONFORMANCE-S4.R2 | `EventOrderingPreserved`: publish separate batches; `Load` returns events in strictly ascending revision order with no duplicates. |
| CONFORMANCE-S5.R1, CONFORMANCE-S5.R4 | A backend constructs two instances over one backing via `NewSharedBackingSuite` and calls `Run(t)`; assert both shared-backing scenarios execute. |
| CONFORMANCE-S5.R2 | `BlindAppendsSucceedAcrossStoreInstances`: both instances write; each observes the other's commits on `Load`. |
| CONFORMANCE-S5.R3, CONFORMANCE-S5.R5 | `StaleRevisionConflictsAcrossStoreInstances`: a revision loaded from A conflicts after B advances; assert the publish fails with a conflict. |
| CONFORMANCE-S6.R1, CONFORMANCE-S6.R2 | `PublishesWithAnExpectedRevisionPastTenEvents`: seed 12 events so revision 10 = `0x0a`; publish past the boundary succeeds; scenario retained in the suite. |

Verification is by running these tests (`just test` for the `we` package and existing
backends; `just test-integration` for backends that need Docker; the SQLite backend from
Feature 02 runs both the single-store and shared-backing suites), not by assertion. Each
backend that can produce a shared backing is wired to the paired suite — the SQLite store is
the natural first adopter (two stores over one file).
