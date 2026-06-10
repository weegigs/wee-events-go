# ADR-0009 — Use `pgregory.net/rapid` for property-based conformance testing

- **Status:** Accepted
- **Relates to:** [features/07-aggregate-identity.md](../features/07-aggregate-identity.md) · [features/04-storage-verification-tests.md](../features/04-storage-verification-tests.md)

## Context

The store conformance suite proves backend behaviour with example-based scenarios:
hand-picked identities, fixed payloads. Examples prove the examples. The identity
contract (ADR-0008) defines a *space* — a charset grammar for types and keys, arbitrary
unicode payload content behind the codec seam — and the owner's direction is that
conformance must cover that space's edge cases generatively, not anecdotally: stores
must adapt to the whole key space (including inputs nobody hand-picks, like `~~~~`,
single-character keys, pipe-runs, or unicode payloads with NUL and combining marks).

Go options for property-based testing:

- `testing/quick` (stdlib) — frozen since Go 1.x, no shrinking: a failure arrives as a
  large random input, not a minimal counterexample.
- `leanovate/gopter` — full-featured but heavyweight API, low maintenance activity.
- `pgregory.net/rapid` — actively maintained, zero transitive dependencies, integrates
  with `*testing.T` (`rapid.Check`), automatic shrinking to minimal counterexamples,
  deterministic failure replay via saved failure files, iteration budget tunable with
  `-rapid.checks`.

One structural fact: the validation suite is production-shaped code (`we/` package, not
`_test.go`), so its imports become module dependencies for consumers — exactly as
`testify` already is, by the same mechanism.

## Decision

Use `pgregory.net/rapid` for property-based tests: the pure identity codec properties
(constructor/encode/decode round-trips and rejection properties over the full charset
grammar) and the suite's generative conformance scenario (generated identities and
payloads round-tripped through every backend).

Iteration budget is the rapid default (100 checks) locally and in CI; `-rapid.checks`
raises it for soak runs. Failure cases shrink and are replayable deterministically.

## Consequences

- Edge cases are explored systematically; a regression reports the *minimal* failing
  identity or payload rather than a 60-character random string.
- `rapid` joins `testify` as a module dependency carried by the production-shaped
  suite — accepted on the existing precedent; both are test-domain libraries with no
  transitive baggage.
- Docker-backed backends run the generative scenario at 100 publish/load iterations
  per backend — single-digit seconds, acceptable in the existing integration budget.

## Alternatives considered

- **`testing/quick`.** Rejected: no shrinking, frozen API; failures are noise.
- **`gopter`.** Rejected: heavier API for no additional capability rapid lacks here.
- **Hand-enumerated edge-case tables.** Rejected as the *only* mechanism (tables stay
  for the closed reason-set assertions): enumerations encode the author's
  imagination; the point of this decision is coverage beyond it.
