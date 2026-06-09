# Design Principles

These principles govern new and changed code in `wee-events-go`. They are enforced at
review time and, where a linter can check them, in `just lint`. They are distinct from
the mechanical [conventions](conventions.md) (naming, logging): conventions say how to
spell things; principles say how to shape them.

Two of the three principles below originate in languages with stronger guarantees than
Go (RAII from C++/Rust; "make illegal states unrepresentable" from sum-type languages).
They are stated here in **idiomatic Go terms, not ported wholesale** — the goal is code
that reads as native Go, not another language wearing a Go syntax. Go has no deterministic
destructors, no sum types (as of Go 1.26, the proposals remain unadopted), and a zero
value for every type. So the foreign machinery is deliberately *not* imported: no
`Guard`/`Owner` types that auto-release, no generic `Option[T]`/`Result[T]`. Where Go
gives the guarantee, lean on it; where it does not, validate at construction and at review
— and say so, rather than simulating a type system Go doesn't have.

## 1. Single Responsibility

A type, function, or package has one reason to change. Responsibilities are separated,
not bundled.

- **Do** keep the seams the framework already draws: stores persist bytes and never
  interpret payloads; codecs encode/decode; reducers fold events into state; handlers
  decide; publishers write. A change to the wire format must not touch a store.
- **Do** split a type the moment it grows a second axis of change (e.g. transport *and*
  business logic).
- **Don't** let a connector know domain rules, or a store know encodings — feature 01's
  codec layer exists precisely so the store stays single-responsibility.
- **Smell:** a function that both decides *and* persists *and* formats a response. Split
  it.

## 2. Explicit resource lifecycle (the Go form of RAII)

Go deliberately rejects RAII: no destructors, no scope-bound deterministic release. Its
substitute is not a weaker RAII but a different, explicit model — and Go treats *visible*
cleanup as a feature, not a deficiency. The principle: **a value is valid once it exists;
resource acquisition and release are paired and explicit at the call site.**

- **Prefer a useful zero value; require a constructor only when you can't.** Go has two
  legitimate idioms. Make the zero value work where achievable (`sync.Mutex`,
  `bytes.Buffer`, `strings.Builder` all do). Where a valid zero value is impossible, a
  constructor returns a usable value or an `error` — never a half-built object needing a
  later `.Init()`. `Make*` returns a value; `New*` returns `(*T, error)`. (See
  [conventions](conventions.md).) "Always use a constructor" is itself slightly un-Go.
- **Acquire, then `defer` release at the call site.** Resources expose `Close()` (or a
  returned `cleanup func()`, as the store test helpers already do —
  `store, cleanup, err := NewKurrentTestStore(...)`). The acquirer owns release; cleanup
  is explicit and local, not hidden in a destructor.
- **`context.Context`** carries operation lifetime and cancellation — use it, don't invent
  a lifetime type.
- **Lifecycle symmetry.** If construction acquires N resources, shutdown releases all N.
  Document who closes what; a resource with no owner is a leak waiting to happen.
- **Don't** use `runtime.AddCleanup` (or the deprecated `SetFinalizer`) for resource
  release — they are non-deterministic last-resort safety nets, not a substitute for
  `Close()`. **Don't** build a `Guard`/`Owner` wrapper to auto-release; that is RAII
  cosplay and reads as foreign.
- **Enforcement:** `errcheck`, `bodyclose`, and `sqlclosecheck` in `just lint` catch
  unreleased resources; an unchecked `Close()` is a review blocker, not a suppression
  target.

This is also the lever for principle 3: a constructor is the one gate where an invariant
is established, so "you hold the value" can mean "the invariant holds."

## 3. Illegal states unrepresentable (within language limits)

Push validity into types where Go supports it; where it doesn't, establish the invariant
at construction and at review. Use exactly the amount of type machinery Go gives you — no
more. This is Go's weakest axis, so aim at the high-leverage, native techniques and stop
there.

**Primary tools (idiomatic, high-leverage):**

- **Parse, don't validate.** Convert untrusted input into a typed value once, at the
  boundary, through a constructor that returns `error`. Downstream code receives a type
  that can only hold valid values. The existing typed identifiers (`AggregateId`,
  `Revision`, `EventType`, `CommandName`) are this pattern — extend it rather than passing
  bare `string`s. This is the single most Go-native, highest-return technique here.
- **Hold invariants in unexported fields.** Expose state through methods so an
  inconsistent value cannot be assembled by a caller (`time.Time` is the canonical std-lib
  example). *(Counter-example to fix over time: an aggregate whose `revision` and `events`
  are independently settable can be made inconsistent — prefer a constructed,
  method-accessed value.)*
- **No meaningless representable states.** A field that exists but does nothing is an
  illegal state made representable. *(Counter-example: `PublishOptions.Encrypt`, a flag
  with no implementation — either implement it or remove it; do not ship a lie in the
  type.)* This is the candor rule applied to data shapes.
- **State is not an error.** Encode "nothing to do" or "not allowed in this state" as a
  value in the model (a no-op, a typed `Rejection`), never as a thrown infrastructure
  error. See [conventions](conventions.md) and feature 05.

**Use sparingly (legitimate but heavier):**

- **Sealed interfaces for a genuinely closed variant set.** An unexported interface method
  plus a fixed set of implementors — used in the std lib (`go/ast.Node`,
  `database/sql/driver.Value`). Reserve it for sets you actually switch on in several
  places; the compiler cannot prove exhaustiveness, so back it with a loud `default` (and
  optionally the `exhaustive` linter). Feature 05's `Rejection | Store | Codec` taxonomy is
  a real closed set; its boundary classification via `errors.As` is the lighter, preferred
  form — full sealed-interface type-switching is only warranted if several call sites
  switch on all variants.

**Anti-patterns — these read as "another language in Go", don't:**

- **Generic `Option[T]` / `Result[T]`.** Go has `(T, error)` and the `, ok` comma-idiom.
  Importing Option/Result is the clearest "Rust in a trenchcoat" tell.
- **A blanket `exhaustive`-lint gate on every interface.** Apply it surgically to the few
  genuinely closed sets, not as a global mandate.

### Honest limits

- Go has nil and a zero value for every type; "this reference is never absent" is not
  type-enforceable. Mitigate with constructors and unexported fields, not with hope.
- Sum-type exhaustiveness needs a linter (`exhaustive`) or a loud `default`; the compiler
  will not catch a missing case. Go 1.26's proposals for native sum types remain
  unadopted — do not write docs or code that assume them.
- Go 1.26's `new(expr)` (e.g. `new(42)` → `*int`) covers optional pointer fields without a
  helper function — prefer it over a hand-rolled `ptr[T]`.
- These gaps are why principle 2 (construct-valid) carries the weight — the constructor is
  where the guarantee the type system can't make is actually made.

## Applying these in feature work

Each user story in a [feature epic](features/README.md) should note which principle its
acceptance criteria uphold (e.g. an unwanted-behaviour EARS requirement that rejects an
invalid construction is principle 3). Reviews check deliverables against these three
before merge.

Keeping code idiomatic is also mechanical: Go 1.26's revamped `go fix` is now the home of
the modernizers (dozens of fixers that move code to current language and library idioms).
Run them via the existing `just fix` recipe so new code stays native rather than drifting
toward older or foreign patterns.
