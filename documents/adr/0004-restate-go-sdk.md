# ADR-0004 — Pin the Restate Go SDK and wire dispatch manually

- **Status:** Proposed
- **Relates to:** [features/03-restate-integration.md](../features/03-restate-integration.md)

## Context

Feature 03 adds a connector that serves a `we.EntityService[T]` through Restate's
durable runtime. Two facts about the Restate Go ecosystem bear on the integration.

- `github.com/restatedev/sdk-go` is the official Restate SDK for Go and the only
  maintained option for registering Restate services and virtual objects from Go. There
  is no standard-library or in-house alternative.
- The SDK's service / virtual-object registration API has shifted between releases. An
  unpinned dependency would let a minor upgrade silently change handler registration
  semantics under the connector.

The Rust sibling (`wee-events-restate`) generates its dispatch glue from the `service!`
and `#[handler]` macros — the macro expands to the per-command registration and routing
code. Go has no macro system, so there is no equivalent code generator. The connector
must produce the dispatch wiring some other way: either a Go code generator written for
this purpose, or explicit hand-written registration.

The existing service layer already owns command routing. `we.EntityService[T]`
(`we/service.go`) executes a command by delegating to `RoutedDispatcher[T]`
(`we/dispatcher.go`), which maps a command name to its handler at runtime. The connector
is handed an already-constructed `EntityService[T]`; it does not need to know individual
command types.

## Decision

The framework will depend on `github.com/restatedev/sdk-go` at a pinned version,
confirmed to build under Go 1.26, and will record that version in `go.mod`. Dispatch from
Restate handlers into the domain will be wired manually through the existing
`RoutedDispatcher[T]` rather than through any code-generation step.

## Consequences

- A single Restate `execute` handler funnels every command through the connector's
  `EntityService[T]`, which owns its `RoutedDispatcher[T]`; no per-command registration
  or generated glue is required, and adding a command needs no connector change.
- The connector stays decoupled from concrete command types, matching the runtime nature
  of the Go dispatcher (`go.mod` notes the same trade-off for `Handles<C>` — Go cannot
  reproduce Rust's compile-time dispatch, so the manual route is the honest equivalent).
- The pinned version is an explicit upgrade gate: bumping the SDK is a deliberate change,
  verified against the connector's handler registration and the integration tests, rather
  than an implicit drift. The version is kept current through `just update-deps`.
- The SDK's API surface must be re-confirmed at each bump, because registration semantics
  have moved between releases; the integration test (Feature 03) is the proof that a given
  pin still works.

## Alternatives considered

- **Write a Go code generator that emits handler registration from command definitions.**
  Rejected: it reproduces the Rust macro's output without the macro's compiler support,
  adds a build step and a generator to maintain, and buys nothing over routing through the
  existing runtime dispatcher, which already maps command names to handlers.
- **Track the SDK at a floating / latest version.** Rejected: the registration API has
  changed between releases, so a floating dependency risks silent behavioural change in
  the durability-critical path; a pin makes upgrades explicit and testable.
- **Implement a bespoke Restate ingress client instead of using the SDK.** Rejected: it
  re-derives the official SDK's protocol handling — invocation, replay, and journaling —
  by hand, a large surface to keep correct against a runtime the project does not control.
