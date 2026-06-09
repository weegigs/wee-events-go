# ADR-0004 — Use the Restate Go SDK and wire dispatch manually

- **Status:** Accepted
- **Relates to:** [features/03-restate-integration.md](../features/03-restate-integration.md)

## Context

Feature 03 adds a connector that serves a `we.EntityService[T]` through Restate's
durable runtime. Two facts about the Restate Go ecosystem bear on the integration.

- `github.com/restatedev/sdk-go` is the official Restate SDK for Go and the only
  maintained option for registering Restate services and virtual objects from Go. There
  is no standard-library or in-house alternative.
- It is a pre-1.0 SDK (latest is `v0.24.0`), so its service / virtual-object registration
  API may still change between releases. The connector therefore needs a way to *detect* a
  breaking change on upgrade — rather than avoiding upgrades by holding the dependency back.

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

The framework will depend on `github.com/restatedev/sdk-go` and **track its latest release
like every other dependency** (via `just update-deps`) — no version pin or hold-back.
Dispatch from Restate handlers into the domain is wired **manually** through the existing
`RoutedDispatcher[T]` rather than through any code-generation step.

The durability-critical risk — that an SDK upgrade could silently change handler
registration or replay semantics — is guarded by the **Restate integration test**
(registration + replay / idempotency), not by freezing a version. An upgrade that breaks the
registration API fails that test loudly and is then adapted deliberately.

`restatedev/sdk-go v0.24.0` builds cleanly under Go 1.26.4 (its `go.mod` requires `go 1.24`),
verified against the `restate.NewService` / `server.NewRestate()` surface the connector uses.
That is the development baseline, not a ceiling.

## Consequences

- A single Restate `execute` handler funnels every command through the connector's
  `EntityService[T]`, which owns its `RoutedDispatcher[T]`; no per-command registration
  or generated glue is required, and adding a command needs no connector change.
- The connector stays decoupled from concrete command types, matching the runtime nature
  of the Go dispatcher (`go.mod` notes the same trade-off for `Handles<C>` — Go cannot
  reproduce Rust's compile-time dispatch, so the manual route is the honest equivalent).
- The SDK rides `just update-deps` to the latest release with every other dependency. The
  **integration test is the upgrade gate**: it re-proves handler registration and replay on
  each bump, so a breaking change surfaces as a failing test rather than silent drift. There
  is no routine version-holding.
- **A version is held back only on a known compatibility problem** — for example a release
  that fails to build under the project's Go toolchain, or that breaks the integration test
  until the connector is adapted. Such a hold is the documented exception, recorded when it
  occurs, not the default.

## Alternatives considered

- **Write a Go code generator that emits handler registration from command definitions.**
  Rejected: it reproduces the Rust macro's output without the macro's compiler support,
  adds a build step and a generator to maintain, and buys nothing over routing through the
  existing runtime dispatcher, which already maps command names to handlers.
- **Pin / hold the SDK behind its latest release by default.** Rejected: it forgoes fixes
  and improvements and contradicts the project's track-latest dependency policy. The
  integration test already guards the durability-critical semantics, so a standing pin adds
  upgrade debt without adding safety. Version-holding is reserved for a known compatibility
  problem (see Consequences).
- **Implement a bespoke Restate ingress client instead of using the SDK.** Rejected: it
  re-derives the official SDK's protocol handling — invocation, replay, and journaling —
  by hand, a large surface to keep correct against a runtime the project does not control.
