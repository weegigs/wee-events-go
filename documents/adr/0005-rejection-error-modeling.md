# ADR-0005 — Model domain rejections via `error` + `errors.As`

- **Status:** Accepted
- **Relates to:** [features/05-rejection-error-taxonomy.md](../features/05-rejection-error-taxonomy.md) · [principles.md](../principles.md)

## Context

The framework must distinguish a **domain refusal** — a well-formed command that is not
permitted in the aggregate's current state — from an **infrastructure failure** — the
store, network, or codec broke. Today both surface as a bare `error`, so neither callers
nor edge adapters can tell "the customer is already cancelled" apart from "DynamoDB is
down" (see feature 05).

The `wee-events.rs` sibling models this as a sum type, `ServiceError<E> = Rejection |
Store(E) | Codec(CodecError)`, switched on exhaustively at the boundary. Go has no sum
types — the native sum-type proposals remain unadopted as of Go 1.26 — and idiomatic Go
expresses fallible operations as `(T, error)` plus boundary classification via
`errors.As`, not via imported `Option`/`Result` wrappers. The repo's
[design principles](../principles.md) make this explicit: foreign machinery (`Option[T]`,
`Result[T]`, auto-releasing guards) is deliberately not imported, and sealed-interface
type-switching is reserved for genuinely closed sets that several call sites switch on.
The store layer already carries one typed sentinel, `RevisionConflict`, used as an
optimistic-concurrency retry signal.

## Decision

The framework will model a domain rejection as a `Rejection` value type that implements
`error`, carrying a `code`, a `message`, and structured `context` (raw JSON). Handlers
return it as their ordinary `error`; boundaries recover it via `errors.As(err,
&rejection)`. Anything not recoverable as a `Rejection` is treated as an infrastructure
failure. `RevisionConflict` remains a separate infrastructure sentinel and is never a
`Rejection`. No sealed union, `Option`, or `Result` type is introduced, and handler
signatures are unchanged.

## Consequences

- The change is additive: existing handlers and stores compile and behave unchanged, since
  a `Rejection` is just an `error`.
- The HTTP connector classifies at the edge — a recovered `Rejection` maps to a structured
  `4xx`, everything else (store, codec, unexpected, `RevisionConflict`) to a `5xx` — and
  the Restate connector (feature 03) uses the same `errors.As` branch for
  terminal-versus-retryable mapping, without a shared union type.
- Client-addressing faults classify as client errors in both connectors: an inbound decode
  failure (`*DecodeError`) and an unknown command name (`CommandNotFoundError`) map to
  `400` in the HTTP connector and to a terminal bad-request in the Restate connector.
  Recorded post-review: the HTTP connector originally let `CommandNotFoundError` fall to
  the `5xx` default, silently diverging from the Restate mapping; the connectors must
  never disagree on the 4xx-versus-5xx class for the same error.
- Classification is not compiler-checked. There is no exhaustive switch the compiler can
  verify; correctness rests on the `errors.As` recovery and on not wrapping a `Rejection`
  in a way that hides it (an obligation feature 05's tests enforce).
- The behaviour is consistent with principle 3 ("state is not an error"): a refusal is a
  value in the model, not a thrown infrastructure failure, and `RevisionConflict` stays in
  its own infrastructure lane.

## Alternatives considered

- **A sealed interface with an exhaustive type-switch** (the direct port of Rust's
  `ServiceError`). Rejected: principle 3 reserves sealed interfaces for genuinely closed
  sets switched on in several places; here only the boundary classifies, so the lighter
  `errors.As` form applies. A sealed result type would also force every existing handler to
  adopt a new return type, making the change invasive rather than additive, and the
  compiler still could not prove exhaustiveness without an extra linter.
- **A generic `Result[T]` (or `Option[T]`) carrying the rejection.** Rejected as foreign:
  the principles name imported `Option`/`Result` as the clearest "Rust in a trenchcoat"
  tell. Go already has `(T, error)` and `errors.As`; introducing a result wrapper adds a
  non-idiomatic type for no guarantee Go's error chain does not already provide.
- **Folding `RevisionConflict` into the rejection taxonomy.** Rejected: a revision conflict
  is an optimistic-concurrency signal a caller retries, whereas a domain rejection is a
  final "no" a caller does not retry. Conflating them would mislead both the HTTP status
  mapping (retryable conflict versus declined request) and the Restate
  terminal-versus-retryable branch.
