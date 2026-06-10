# Feature 09 — Error Surfacing: No Fabricated Values

- **Status:** Ready · **Size:** M · **Area:** core (`we/`) + `stores/ds` + `stores/sqlite` + `stores/jetstream` + both connectors + samples
- **Coordinates with:** [Feature 07](07-aggregate-identity.md) (shared
  `connectors/werestate/restate.go`, `connectors/wehttp/http.go` — sequence 07 first or
  co-own)
- **Prefix:** `SURFACE`

## Summary

Remediate every finding from the error-surfacing audit (2026-06-10): sites where an
error is detected and then replaced with a value the caller cannot distinguish from real
data. The owner's rule, which this feature encodes as policy: **a detected error is
surfaced or the process panics — it is never converted into a plausible value.**
Misleading data is strictly worse than failure.

The audit found three classes in production code: fabricated-plausible values (a
reference sample whose domain refusals read as infrastructure faults, a conflict
classifier that can misclassify or panic, a duplicate-revision truncation), zero-value
substitutions (an empty `Timestamp` manufactured on a read path, a journal decode that
zero-fills identity fields), and silent telemetry/startup degradation. Each requirement
below names the exact surfaced error; every mutation scenario carries its failure
companion.

## Decisions

- [ADR-0005](../adr/0005-rejection-error-modeling.md) (amended) — a domain refusal
  **must** be expressed as `we.Rejection`; any other error type is classified as
  infrastructure by every connector. The reference samples must model this correctly.
- Owner ruling recorded here: `we.Revision` is **incremental and comparable only** —
  scheme-opaque, not convertible to a timestamp. Methods implying otherwise are removed.

## User stories

### SURFACE-S1 — Reference samples model domain refusals as rejections

*As a developer learning the framework from the samples, I want the account sample's
domain guards to use the rejection taxonomy, so that copying the sample produces correct
4xx/terminal behaviour instead of 500s and infinite Restate retries.*

- **SURFACE-S1.R1** (ubiquitous) — The account sample's domain guards shall return
  `we.Rejection` values with exactly these codes: `account.already-open`,
  `account.not-open`, `account.insufficient-funds`.
- **SURFACE-S1.R2** (ubiquitous) — The `account.insufficient-funds` rejection's
  `context` shall carry `{"balance": <current balance>, "requested": <withdrawal
  amount>}` from the actual command and state — no hardcoded values.
- **SURFACE-S1.R3** (event-driven) — When a refused account command flows through
  `wehttp`, the response shall be `422` with the rejection body (REJECT-S2 contract);
  when it flows through `werestate`, classification shall be terminal — never retried.
  *(Failure companion: the pre-fix behaviour — 500 / infinite retry — is the defect.)*
- **SURFACE-S1.R4** (ubiquitous) — ADR-0005 shall state the contract explicitly: a
  domain refusal not expressed as `we.Rejection` is treated as infrastructure by every
  connector.

### SURFACE-S2 — Conflict classification is chain-robust and panic-free

*As an operator of the DynamoDB store, I want revision-conflict classification to
survive SDK wrapping changes and malformed cancellation reasons, so that a conflict is
never silently relabelled as a generic failure and a transaction abort never crashes the
process.*

- **SURFACE-S2.R1** (ubiquitous) — `maybeRevisionConflict` shall locate
  `*types.TransactionCanceledException` with a single whole-chain
  `errors.As(err, &tc)` — no manual `Unwrap()` steps pinning SDK nesting.
- **SURFACE-S2.R2** (unwanted) — If a cancellation reason's `Code` is `nil`, then the
  classifier shall skip it without dereferencing — never panic.
- **SURFACE-S2.R3** (unwanted) — If no reason is `ConditionalCheckFailed`, then the
  original error shall be returned unchanged (no false `RevisionConflict`).
- **SURFACE-S2.R4** (ubiquitous) — `isRevisionConflict` shall use
  `errors.Is(err, we.RevisionConflict)`, not `==`, so a wrapped sentinel still
  classifies.

### SURFACE-S3 — Read paths never fabricate values

*As a consumer of loaded events, I want every value on a `RecordedEvent` to be real or
the load to fail, so that data I act on is never a silent substitute.*

- **SURFACE-S3.R1** (unwanted) — If a sqlite row's `event_id` does not parse as a ULID,
  then `Load` shall return an error wrapping the parse failure with the form
  `sqlite: invalid event id "<id>": …` — never a `RecordedEvent` carrying
  `Timestamp("")`. (`timestampFromEventID` becomes `(we.Timestamp, error)`.)
- **SURFACE-S3.R2** (unwanted) — If a Restate journal entry's `$id`, `$type`, or
  `$revision` is missing or not a string, then `EntityResponse.UnmarshalJSON` shall
  return an error naming the field (form: `werestate: entity response missing $id`) —
  never a zero-valued response treated as replay success.
- **SURFACE-S3.R3** (ubiquitous) — `Revision.Timestamp()` shall be removed, with its
  test. The `Revision` doc comment shall state the contract: an opaque, per-store,
  incremental and lexicographically comparable token — **not** convertible to a
  timestamp. *(A hex revision parses as valid base32 and yields a confident garbage
  date; the method is unsound by contract, not by implementation.)*

### SURFACE-S4 — Transport edges fail loudly

*As an operator, I want transport-boundary failures to be visible at the point they
occur, so that limits, disconnects, and misconfiguration surface as errors instead of
degraded-but-plausible behaviour.*

- **SURFACE-S4.R1** (unwanted) — If a JetStream publish carries more than 65 536 events
  in one changeset, then `Publish` shall return an error of the form
  `jetstream: changeset exceeds maximum batch size 65536` — never wrap the `uint16`
  index into duplicate revisions.
- **SURFACE-S4.R2** (unwanted) — If a stored changeset decodes to more than 65 536
  events (foreign writer), then `decodeChangeSet` shall return an error — never
  truncate. *(Companion to R1 on the read side.)*
- **SURFACE-S4.R3** (ubiquitous) — Resource encoding in `we` shall be pure (marshal to
  bytes, no `http.ResponseWriter` access); the HTTP connector shall write the response,
  responding `500` with the static body `"failed to encode resource"` only when
  marshalling failed **before** any byte was written.
- **SURFACE-S4.R4** (unwanted) — If the body write fails after the status is committed,
  then the HTTP connector shall log and return — never attempt a second status write
  that appends error text to a `200` stream.
- **SURFACE-S4.R5** (unwanted) — If the OpenTelemetry resource merge fails in the
  counter sample, then startup shall fail with that error — never serve traffic with a
  silently mislabelled trace identity. (`traceResource` returns
  `(*resource.Resource, error)`; `configureTracing` propagates.)
- **SURFACE-S4.R6** (unwanted) — If the serverless sample's injector fails at startup,
  then the process shall log the error before exiting non-zero — never exit silently.

## Out of scope

- Validating identity at the HTTP boundary — that is IDENTITY-S3 (Feature 07).
- The deliberate, documented discards inventoried as acceptable by the audit: deferred
  `Close` errors, `tp.Shutdown` log-only, init-time `panic` on CBOR `DecMode`,
  `ulid.MustNew` entropy panics (panic is the owner-preferred failure mode), and
  `redactToken`'s documented chain-severing.
- `AggregateId.Encode`/`Decode` injectivity — Feature 07.

## Verification

| Requirement | Test |
|---|---|
| SURFACE-S1.R1–R3 | Account sample handler tests assert `we.Rejection` with exact codes and context fields via `errors.As`; wehttp test drives a refused account command → 422 body; werestate `mapError` test asserts terminal classification for each code |
| SURFACE-S1.R4 | ADR-0005 text review (doc change, no test) |
| SURFACE-S2.R1–R3 | Unit tests feed: deeply wrapped `TransactionCanceledException` (extra wrap layers) → `RevisionConflict`; reasons `[nil-Code, ConditionalCheckFailed]` → `RevisionConflict` without panic; reasons `[nil-Code]` only → original error |
| SURFACE-S2.R4 | Unit: wrapped `fmt.Errorf("…%w", we.RevisionConflict)` still classifies |
| SURFACE-S3.R1 | sqlite test inserts a 26-char non-ULID `event_id` behind the store; `Load` errors with `invalid event id`, no event slice returned |
| SURFACE-S3.R2 | Unit: journal JSON with missing `$id` / numeric `$revision` → error naming the field; valid round-trip still green (`TestEntityResponseRoundTrips`) |
| SURFACE-S3.R3 | Compile-level removal; `Revision` doc comment review; `revision_test.go` timestamp cases deleted |
| SURFACE-S4.R1, R2 | jetstream unit tests: 65 537 events → publish error; synthetic oversized changeset → decode error |
| SURFACE-S4.R3, R4 | wehttp test: marshal-failure entity → single `500` static body (exists — `encodeFailureMapsToStatic5xx`); write-failure path asserts no second `WriteHeader` (hijacked/failing writer test double) |
| SURFACE-S4.R5 | telemetry unit test: conflicting schema URLs → startup error |
| SURFACE-S4.R6 | Code review of serverless `main.go` (log line precedes exit; no test harness for `os.Exit` paths) |
