# Feature 03 — Restate Durable Execution Connector

- **Status:** Planned · **Size:** L · **Area:** new package (`connectors/werestate/`)
- **Coordinates with:** [Feature 05](05-rejection-error-taxonomy.md) (consumes the
  rejection taxonomy at the edge); **Independent** of Features 02 and 04
- **Prefix:** `RESTATE`

## Summary

Add a connector that exposes a `we.EntityService[T]` through [Restate](https://restate.dev)
so commands execute durably: each command is applied at most once per idempotency key,
survives process restarts, and replays deterministically. The connector mirrors the HTTP
adapter (`connectors/wehttp`) — `load` reads current state, `execute` applies a
`{command, payload}` envelope — but targets Restate's durable runtime rather than plain
REST. The service layer is already transport-agnostic; this feature adds a second edge
without modifying core `we/` types. Effect routing (the side-effect fan-out the Rust
sibling carries) is deliberately phase 2, so phase 1 delivers durable execute plus load.

## Decisions

- [ADR-0004](../adr/0004-restate-go-sdk.md) — depend on `github.com/restatedev/sdk-go` at
  a pinned version (confirmed under Go 1.26) and wire dispatch manually through the
  existing `RoutedDispatcher[T]` rather than generating glue, since Go has no equivalent
  to Rust's `service!` macro.

## User stories

### RESTATE-S1 — Serve an entity service through Restate

*As an application developer, I want to expose an existing `we.EntityService[T]` as a
Restate service, so that a deployed aggregate gains durable execution without rewriting
its domain logic.*

Upholds principle 2 (explicit resource lifecycle): the connector is constructed from an
already-built `EntityService[T]` and registered explicitly; it owns no domain wiring of
its own.

- **RESTATE-S1.R1** (ubiquitous) — The framework shall register a Restate service, keyed
  by aggregate id (`type:key`), that exposes a `load` handler and an `execute` handler for
  a supplied `we.EntityService[T]`.
- **RESTATE-S1.R2** (event-driven) — When the `load` handler is invoked, the framework
  shall return the current entity state together with its `$id`, `$type`, and `$revision`.
- **RESTATE-S1.R3** (event-driven) — When the `execute` handler receives a
  `{command, payload}` envelope, the framework shall dispatch it through
  `EntityService.Execute` and return the resulting entity state with its `$id`, `$type`,
  and `$revision`.
- **RESTATE-S1.R4** (ubiquitous) — The framework shall reuse the `wehttp` request/response
  envelope (`{command, payload:{encoding, data}}`) so the Restate and HTTP connectors stay
  consistent and the codec layer (Feature 01) applies uniformly.
- **RESTATE-S1.R5** (ubiquitous) — The framework shall route commands through the
  `EntityService[T]`'s existing `RoutedDispatcher[T]` and shall not modify core `we/`
  types to do so. *(See ADR-0004.)*

### RESTATE-S2 — Durable, at-most-once command execution

*As an application developer, I want each command applied at most once per idempotency
key and the work to survive process restarts, so that retries and crashes never
double-apply events.*

Upholds principle 3 (illegal states unrepresentable): "this command already ran" is
modelled as a returned prior result, not re-executed work.

- **RESTATE-S2.R1** (event-driven) — When the `execute` handler receives a command
  carrying an idempotency key, the framework shall apply the command at most once for that
  key.
- **RESTATE-S2.R2** (event-driven) — When a process restarts mid-invocation, the framework
  shall allow the Restate runtime to replay the invocation to completion so the command
  completes exactly once.
- **RESTATE-S2.R3** (unwanted) — If an `execute` request repeats an idempotency key that
  already completed, then the framework shall return the original result and shall **not**
  re-apply the command or append new events.
- **RESTATE-S2.R4** (unwanted) — If a replay re-enters a handler whose side effects already
  ran, then the framework shall yield the journaled outcome rather than re-running those
  side effects.

### RESTATE-S3 — Boundary error mapping

*As an operator, I want infrastructure failures retried and business refusals not retried,
so that transient faults self-heal while a rejected command fails fast with its reason.*

Upholds principle 3 (state is not an error): a domain rejection is a modelled outcome that
surfaces as a terminal error, distinct from an infrastructure fault.

- **RESTATE-S3.R1** (event-driven) — When a handler fails with an infrastructure error
  (store or transport), the framework shall surface it as a retryable Restate error so the
  runtime may retry.
- **RESTATE-S3.R2** (event-driven) — When a handler fails with a domain rejection (the
  Feature 05 taxonomy), the framework shall surface it as a Restate **terminal** error
  carrying the rejection code, message, and context.
- **RESTATE-S3.R3** (unwanted) — If a command is rejected on business grounds, then the
  framework shall **not** present it as a retryable error, so the runtime does not retry a
  refusal.
- **RESTATE-S3.R4** (state-driven) — While the Feature 05 rejection taxonomy is not yet
  available, the framework shall map handler errors conservatively (documented interim
  behaviour) and shall not silently classify a refusal as retryable.

### RESTATE-S4 — Effect routing (phase 2, future scope)

*As an application developer, I want execution notifications to trigger side-effect
workflows by name or predicate, so that downstream effects run durably off committed
events — recorded here as planned, not delivered in phase 1.*

- **RESTATE-S4.R1** (optional feature) — Where effect routing is included (phase 2), the
  framework shall route execution notifications to side-effect handlers selected by an
  effect filter (all / name / names / predicate), mirroring the Rust `EffectRouter`.
- **RESTATE-S4.R2** (ubiquitous) — The framework shall scope phase 1 to durable `execute`,
  idempotency, and `load`; effect routing and notification fan-out are out of the first
  cut.

## Implementation notes

### Current Go state

The service layer is already transport-agnostic:

- `we.EntityService[T]` (`we/service.go`) — `Load` and `Execute`. `Execute` loads the
  entity, dispatches the command (possibly publishing events), reloads, and returns the
  new state.
- `RoutedDispatcher[T]` (`we/dispatcher.go`) — routes a command name to its handler and
  reports whether events were published.
- `connectors/wehttp/http.go` — the existing edge adapter (`GET /{type}/{key}` loads,
  `POST /{type}/{key}` executes a `{command, payload}` body); the structural template for
  this connector. There is no durable-execution integration today.

### Rust reference (port origin)

`crates/wee-events-restate/src/`:

- `client.rs` — `RestateClient<D: ServiceDefinition>` implements `TypedService` over HTTP;
  `execute_idempotent(name, target, command, idempotency_key)` posts to the Restate
  ingress (`/{executor}/{idempotency_key}/run`).
- `effects.rs` — `EffectRouter`, `SideEffectFilter` (`All | Name | Names | Predicate`),
  `EffectTrigger`; routes execution notifications to side-effect workflows (phase 2 here).
- `types.rs` — ingress payloads: `Metadata` (correlation id, optional causation id,
  optional idempotency key), `CommandRequest`, `ExecuteRequest`, `EntityResponse`,
  `ExecuteNotification`.
- `names.rs` / `generated.rs` — naming convention `{service}:executor` (and
  `{service}:runner` for effect routing) plus command/load routing helpers.
- `correlation.rs` — correlation-id derivation.
- `error.rs` / `lib.rs` — `IntoHandlerError` mapping `wee_events` / `ServiceError` to
  `restate_sdk::errors::TerminalError`; `Rejection` serialized to JSON.

Rust generates the dispatch glue from the `service!` / `#[handler]` macros. Go has no
macro system, so the equivalent wiring is explicit (see ADR-0004).

### Go target

New package `connectors/werestate` (mirrors `connectors/wehttp`):

- A handler builder, e.g. `NewService[T](svc we.EntityService[T], ...Option[T])`,
  registering a Restate virtual-object/service whose key is the aggregate id (`type:key`)
  with `execute` and `load` handlers (RESTATE-S1).
- **Idempotency:** use Restate's idempotency key for at-most-once application; a replayed
  request with the same key returns the original result rather than re-applying
  (RESTATE-S2 — the primary acceptance test).
- **Dispatch wiring:** route through the `EntityService[T]`'s existing
  `RoutedDispatcher[T]`; command registration is implicit in the supplied service, so no
  per-command codegen is required (RESTATE-S1.R5; ADR-0004).
- **Error mapping:** infrastructure errors become retryable Restate errors; domain
  rejections (Feature 05) map to terminal errors carrying code/message/context
  (RESTATE-S3). Until Feature 05 lands, map conservatively and document the interim
  behaviour.
- **Effect routing:** out of the first cut; phase 2 adds an `EffectRouter` equivalent with
  name/predicate filters that triggers side-effect handlers on execution notifications
  (RESTATE-S4).
- Library: `github.com/restatedev/sdk-go`, pinned and confirmed under Go 1.26 (ADR-0004).

## Verification

| Requirement | Test |
|---|---|
| RESTATE-S1.R1, RESTATE-S1.R2, RESTATE-S1.R3 | Wire the counter sample (`samples/counter`) through the connector; `load` returns state with `$id`/`$type`/`$revision`; `execute` of `increment` returns the advanced state. |
| RESTATE-S1.R4 | Assert the request/response envelope shape matches `wehttp` (`{command, payload:{encoding, data}}`). |
| RESTATE-S1.R5 | Confirm dispatch goes through the supplied `EntityService[T]`/`RoutedDispatcher[T]` with no change to core `we/` types. |
| RESTATE-S2.R1, RESTATE-S2.R3 | Execute `increment` twice with the same idempotency key against a Restate runtime; assert the state increments once and the second call returns the original result. |
| RESTATE-S2.R2, RESTATE-S2.R4 | With a containerized Restate runtime, kill and restart the service mid-invocation; assert the command completes exactly once and journaled side effects do not re-run. |
| RESTATE-S3.R1, RESTATE-S3.R3 | Force a store/transport failure; assert the error is presented as retryable. |
| RESTATE-S3.R2 (post Feature 05) | Reject a command on business grounds; assert a Restate terminal, non-retried error carrying the rejection code/message/context. |
| RESTATE-S3.R4 | Until Feature 05 lands, assert the documented conservative mapping and that a refusal is never classified retryable. |
| RESTATE-S4.R2 | Confirm phase 1 ships `execute`/`load`/idempotency only; no effect-routing surface is present. |

Verification is by running these tests (`just test`, plus the Restate integration tests
against a containerized runtime), not by assertion.
