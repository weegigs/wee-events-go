# Feature 05 — Structured Rejection / Error Taxonomy

- **Status:** Planned · **Size:** M · **Area:** core (`we/`) + `connectors/wehttp`
- **Coordinates with:** [Feature 01](01-cbor-codec.md) (shared `we/command.go`)
- **Prefix:** `REJECT`

## Summary

Give the framework a structured way to distinguish a **domain refusal** — the command
was well-formed but not permitted in the aggregate's current state — from an
**infrastructure failure** — the store, network, or codec broke. Today both surface as a
bare `error`, so callers and edge adapters cannot tell "the customer is already cancelled"
apart from "DynamoDB is down."

After this feature a handler can refuse a command with a typed `Rejection` carrying a
`code`, a `message`, and structured `context`; the boundary recovers it via `errors.As`;
the HTTP connector maps a rejection to a structured `4xx` and an infrastructure failure to
a `5xx`; and the same taxonomy is consumable by the Restate connector (Feature 03) for
terminal-versus-retryable mapping. A rejection is a **domain state**, not a thrown error —
this feature is the framework's primary expression of the repo's "state is not an error"
rule (principle 3).

## Decisions

- [ADR-0005](../adr/0005-rejection-error-modeling.md) — domain rejections are modelled as
  an `error` recovered at boundaries via `errors.As`, not a sealed union and not
  `Option`/`Result`; `RevisionConflict` remains a separate infrastructure sentinel.

## User stories

### REJECT-S1 — Refuse a command with a typed rejection

*As an aggregate author, I want to refuse a command with a typed `Rejection` carrying a
code, a message, and structured context, so that "not allowed in this state" is expressed
as a domain outcome rather than an infrastructure crash.* (Principle 3 — "state is not an
error": the refusal is a value in the model, not a thrown failure.)

- **REJECT-S1.R1** (ubiquitous) — The framework shall provide a `Rejection` value type
  carrying a `code`, a `message`, and structured `context`. *(See ADR-0005.)*
- **REJECT-S1.R2** (event-driven) — When a command handler refuses a command, the
  framework shall allow it to return a `Rejection` as its `error` without changing the
  handler signature.
- **REJECT-S1.R3** (state-driven) — While a `Rejection` propagates through the dispatcher
  and the entity service, the framework shall preserve it so that `errors.As` recovers the
  original `code`, `message`, and `context` unchanged.
- **REJECT-S1.R4** (unwanted) — If a handler returns a `Rejection`, then the framework
  shall not record any event for that command. *(A refusal is a no-op against the stream,
  not a state change.)*

### REJECT-S2 — Map domain rejections to a structured client error

*As an API client, I want a refused command to return a `4xx` with a machine-readable
body, so that a declined-but-well-formed request is distinguishable from a malformed one
and from a server fault.*

- **REJECT-S2.R1** (event-driven) — When a command path returns a value recoverable as a
  `Rejection` via `errors.As`, the HTTP connector shall respond with a `4xx` status.
- **REJECT-S2.R2** (event-driven) — When the HTTP connector responds to a rejection, it
  shall emit a JSON body carrying the rejection's `code`, `message`, and `context`.
- **REJECT-S2.R3** (unwanted) — If a command path returns a `Rejection`, then the HTTP
  connector shall not respond with a `5xx`.

### REJECT-S3 — Keep infrastructure failures server-side

*As an operator, I want a store or codec failure on the command path to return a `5xx` and
never be relabelled as a client error, so that genuine faults are visible and clients are
not told a broken backend was their fault.*

- **REJECT-S3.R1** (event-driven) — When a command path returns an infrastructure error
  (store, codec, or unexpected) that is not recoverable as a `Rejection`, the HTTP
  connector shall respond with a `5xx` status.
- **REJECT-S3.R2** (unwanted) — If the store returns an infrastructure error (including
  `RevisionConflict`), then the HTTP connector shall not respond with a `4xx` rejection
  body. *(`RevisionConflict` is infrastructure-adjacent — an optimistic-concurrency retry
  signal — not a domain refusal; see ADR-0005.)*
- **REJECT-S3.R3** (event-driven) — When the dispatcher or the entity service propagates an
  infrastructure error, the framework shall not wrap it in a way that causes `errors.As`
  to misclassify it as a `Rejection`.

### REJECT-S4 — Consumable terminal-versus-retryable taxonomy

*As the Restate connector (Feature 03), I want to classify a command result as a domain
rejection versus an infrastructure failure, so that a rejection becomes a Restate terminal
error and a store/codec failure remains retryable.*

- **REJECT-S4.R1** (ubiquitous) — The framework shall expose rejection-versus-infrastructure
  classification through `errors.As`, so a connector can branch without a sealed union.
- **REJECT-S4.R2** (event-driven) — When a connector classifies a result recoverable as a
  `Rejection`, the framework shall let it treat the result as terminal (not retried).
- **REJECT-S4.R3** (event-driven) — When a connector classifies an infrastructure error
  that is not a `Rejection`, the framework shall let it treat the result as retryable.

## Implementation notes

### Current Go state

Command handlers (`we/command.go`) return a plain `error`. `RoutedDispatcher[T]`
(`we/dispatcher.go`) and `EntityService[T]` (`we/service.go`) propagate it untyped.
`connectors/wehttp/http.go` collapses everything that is not a clean success into a `500`,
with no way to express "this was a well-formed request the domain declined" (which should
be a `4xx`). The store layer already has one typed sentinel — `we.RevisionConflict` — but
there is no general taxonomy.

### Rust reference (port origin)

`crates/wee-events/src/service.rs` defines the source taxonomy as a sum type:

```rust
pub struct Rejection {
    pub code: String,
    pub message: String,
    #[serde(default)]
    pub context: serde_json::Value,
}

pub enum ServiceError<E> {
    Rejection(Rejection),   // domain refusal
    Store(E),               // infrastructure failure
    Codec(CodecError),      // (de)serialization failure
}
```

`crates/wee-events/src/codec.rs` defines `CodecError` (the unified codec failure — see
[Feature 01](01-cbor-codec.md)). The Restate connector ([Feature 03](03-restate-integration.md))
maps `Rejection` to a Restate **terminal** error (not retried) and `Store`/`Codec` to
retryable errors.

### Go target

- New `we/rejection.go`: a `Rejection` value type with `Code string`, `Message string`,
  and `Context json.RawMessage` (raw JSON so callers get machine-readable detail, matching
  Rust's `serde_json::Value`). It implements `error` (`Error() string` → `"code: message"`)
  and is recovered at boundaries via `errors.As`. A constructor follows the repo's
  `New*`/`Make*` convention.
- The `Rejection | Store | Codec` distinction is **not** a sealed Go union: handlers keep
  returning `error`, and boundaries classify with `errors.As(err, &rejection)`. Anything
  not recoverable as a `Rejection` is treated as infrastructure (store/codec/unexpected).
  This keeps the handler signature unchanged and the change additive (ADR-0005).
- **`we/command.go`** — document and support that a handler returning a `Rejection` is a
  refusal, not an infrastructure error. **This file is also edited by Feature 01 —
  sequence 01 → 05, or co-own.**
- **`we/dispatcher.go` / `we/service.go`** — propagate the error unchanged; do not wrap a
  `Rejection` in a way that hides it from `errors.As` (satisfies REJECT-S1.R3, REJECT-S3.R3).
- **`connectors/wehttp/http.go`** — at the edge, classify: a recovered `Rejection` → `4xx`
  (e.g. `409`/`422`) with a JSON body carrying `code`/`message`/`context`; everything else
  (store, codec, unexpected, `RevisionConflict`) → `5xx`.

### Coordination with Feature 01

`we/command.go` is the only file shared with [Feature 01](01-cbor-codec.md) (which extends
`RemoteCommand` payload decoding). Land Feature 01 first, then Feature 05 — or assign both
to a single owner — to avoid file-level contention. `CodecError` (Feature 01) is the
infrastructure error this taxonomy classifies as `5xx`/retryable.

### Decisions recorded in ADR-0005

- `RevisionConflict` is infrastructure-adjacent, not a `Rejection`: it stays in the
  store/optimistic-concurrency lane (a caller retries it), distinct from a domain rejection
  (a caller does not retry — the answer is "no").
- Handler signatures are not changed to a sealed result type; classification is `error` +
  `errors.As`. This is the idiomatic Go shape and keeps the change additive.

## Verification

| Requirement | Test |
|---|---|
| REJECT-S1.R1, REJECT-S1.R2 | Construct a `Rejection` with `code`/`message`/`context`; assert it satisfies `error` and a handler can return it as its `error`. |
| REJECT-S1.R3 | A handler returns a `Rejection`; assert `errors.As` recovers it unchanged through the dispatcher and the entity service. |
| REJECT-S1.R4 | A handler refuses a command; assert no event is recorded to the stream for that command. |
| REJECT-S2.R1, REJECT-S2.R2 | `connectors/wehttp` handles a refused command; assert a `4xx` status and a JSON body carrying the rejection's `code`/`message`/`context`. |
| REJECT-S2.R3, REJECT-S3.R2 | Inject a store error (and separately `RevisionConflict`) on the same command path; assert the response is `5xx`, never a `4xx` rejection body; assert a rejection is never `5xx`. |
| REJECT-S3.R1, REJECT-S3.R3 | Inject a store/codec error; assert a `5xx`, and assert `errors.As` does not recover it as a `Rejection` through the dispatcher and service layers. |
| REJECT-S4.R1, REJECT-S4.R2, REJECT-S4.R3 | Classify a `Rejection` and an infrastructure error via `errors.As`; assert the rejection is treated as terminal and the infrastructure error as retryable (the branch the Restate connector consumes). |

Verification is by running these tests (`just test`), not by assertion.
