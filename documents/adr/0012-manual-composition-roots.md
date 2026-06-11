# ADR-0012 — Dependencies are wired by hand at per-binary composition roots

- **Status:** Accepted
- **Relates to:** [ADR-0004](0004-restate-go-sdk.md) (wire dispatch manually) · [ADR-0006](0006-lint-enforcement.md) (resource lifecycle) · [ADR-0007](0007-explicit-event-encoding.md) (encoding as an explicit constructor argument)

## Context

Google Wire generated the dependency injection for the sample binaries
(provider sets in `stores/ds`, injectors in `samples/counter`). Upstream
archived the project on 2025-08-22 after a final v0.7.0 release; its README
directs users to forks. Wire was removed from this repository (the provider
sets, generated injectors, `just wire` recipe, and go.mod tool directive),
leaving the question of what manages dependencies in its place.

The framework already answers it in miniature: capabilities are interface
values passed to constructors (`NewCounterService(store, randomizer)`), and
functions of a single capability are produced by partial application
(`counter.Loader(store)` returns an `EntityLoader`; `we.Publisher(store)`
returns an `EventPublisher`). This is the Reader pattern's architectural
payoff — dependencies visible in signatures, swappable at one root, test
doubles as interface fakes — expressed as ordinary Go closures.

The replacement candidates each move that payoff somewhere worse. Runtime
containers (uber fx/dig, samber/do) resolve graphs by reflection or
registration, so a missing dependency surfaces at process start instead of
compile time and disappears from function signatures. Monadic environments
(IBM fp-go's `ReaderIOResult`) founder on the type system: Go has no
higher-kinded types and methods cannot introduce type parameters, so Reader
composition degenerates into package-level pipeline calls. A maintained Wire
fork (goforj/wire) keeps a code-generation step whose output, for graphs of
this size, is the handful of constructor calls a maintainer would write
anyway.

## Decision

The framework and its binaries manage dependencies by manual constructor
wiring. Concretely:

1. **Capabilities are interface-typed constructor parameters.** A component
   names what it needs in its signature (`we.EventStore`, `we.Encoder`,
   `counter.Randomizer`); nothing reaches into a container or global.
2. **Partial application is the composition idiom.** Constructors return
   values or closures that capture their dependencies
   (`counter.Loader(store)`), so a dependency is applied once and the result
   is a plain function.
3. **Each binary owns one hand-written composition root.** A `local`/`live`
   function per binary is the only place concrete stores, clients, and
   encoders are named, errors from construction are propagated, and cleanup
   is ordered. The root is ordinary code: readable, debuggable, and checked
   by the compiler.
4. **Wide graphs get an explicit environment struct,** passed to
   constructors — not a container. The struct is the Go spelling of a Reader
   environment.
5. **`context.Context` never carries dependencies.** It is for cancellation
   and request-scoped values; a dependency smuggled through context is
   invisible in signatures and untyped at the boundary.
6. **No dependency-injection framework**, compile-time or runtime, in the
   framework or the samples.

## Consequences

Wiring mistakes are compile errors at the root, not start-time panics or
generator diagnostics. The dependency graph is greppable plain code, and
test composition uses the same constructors with fakes (the in-memory store,
the validation suite). The module sheds a tool dependency and a codegen
step.

The costs are accepted: adding a dependency threads it through the
constructor chain by hand (visibility is the point), and there is no
lifecycle container — shutdown ordering is the composition root's job,
under the mirrored-cleanup obligations ADR-0006 already lints for. If a
future binary's graph grows past what a hand-written root can carry, the
remedy is an environment struct (decision 4) before it is ever a container;
a container would take a new ADR superseding this one.

## Alternatives considered

- **goforj/wire (maintained fork).** Drop-in continuity, but preserves a
  generation step that buys nothing at this scale and re-couples the build
  to a community fork of an archived design.
- **uber fx / samber/do (runtime containers).** Lifecycle management comes
  bundled, but resolution failures move to process start and dependencies
  vanish from signatures — both regressions from compiler-checked roots.
- **IBM fp-go `ReaderIOResult` (environment monad).** The closest analogue
  to an Effect-style `R`, but without higher-kinded types or method-level
  type parameters the composition reads as nested pipeline functions that
  fight the language. The pattern's substance — capabilities as environment,
  partial application — survives in decisions 1–4 without the machinery.
- **Dependencies via `context.Context`.** Superficially Reader-like ambient
  environment; rejected as stringly-typed and invisible (decision 5).
