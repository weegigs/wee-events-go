# Declared Service Errors End-to-End

**Date:** 2026-07-14
**Issue:** wee-events-go-t3p
**Predecessor:** 2026-07-09 error-frame wire contract (wee-events-go-tu2)

## Problem

A service team must be able to define an error with a formal body — a stable
code and specified, typed fields — and have it returned to the caller across
the werestate boundary, where the caller may be written in a different
language. The wire contract for this exists (`we.ErrorFrame`: code, message,
closed scalar field set Text/I64/U64/Bool, byte-compatible with
wee-events.rs), and the interface for it exists (`we.ServiceErrorContract`),
but the plumbing is incomplete in two places:

1. **Server:** `mapError` (connectors/werestate/restate.go) frames only the
   concrete `we.Rejection` type. A custom typed error implementing
   `we.ServiceErrorContract` falls through to the retryable infrastructure
   lane and never reaches the client. The contract interface is declared but
   unwired.
2. **Client:** frame decode is package-private, so only the ingress `Client`
   recovers declared errors. A Go service calling another wee-events service
   inside a handler (Restate service-to-service), or any orchestrator holding
   a terminal error from an SDK call, receives the raw frame string as an
   opaque message with no exported decoder.

Plus three hardening items from the 2026-07-09 whole-branch review: the
ingress client sends no Accept header, reads response bodies unbounded, and
the transport-lane tests lack the negative assertion that transport errors
never classify as declared.

## Out of Scope

- **Field-schema description language.** How a team formally documents "code
  X carries fields Y" is a future concern (CUE is the likely direction).
  For now type reconciliation is per-team, exactly as it is for event and
  command payloads. The per-code schema seam belongs to the conformance
  repository (wee-events-go-2sl).
- **A typed in-handler client** (`Load`/`Execute` twin of the ingress
  `Client` over the SDK context). Deliberately deferred until the first real
  orchestrator exists to drive its ergonomics; the exported classification
  primitive below is sufficient and the client is additive later.

## Design

### 1. Server: frame any ServiceErrorContract

`mapError`'s rejection branch generalises from the concrete type to the
interface:

```go
var contract we.ServiceErrorContract
if errors.As(err, &contract) {
    frame := contract.ToErrorFrame()
    // encode + framedError wrapping exactly as today
}
```

`we.Rejection` already implements `we.ServiceErrorContract`, so the existing
branch is subsumed — no special case remains and observed behaviour for
rejections is unchanged (same frame bytes, same terminal classification, same
Unwrap chain). The rest of the taxonomy is untouched: `*we.DecodeError` and
`we.CommandNotFoundError` stay terminal, everything unclassified stays
retryable, and an error already marked terminal is returned unchanged.

The taxonomy ordering in `mapError` keeps the contract check first (where the
rejection check sits today). A type that implemented both the contract and,
say, wrapped a DecodeError is framed as a declared error — declaring the
contract is an explicit, stronger statement of intent than the built-in
classifications.

Usage a team writes (no registration, no server-side wiring):

```go
type InsufficientFunds struct {
    Balance  int64
    Required int64
}

func (e InsufficientFunds) Error() string {
    return fmt.Sprintf("insufficient funds: have %d, need %d", e.Balance, e.Required)
}

func (e InsufficientFunds) ToErrorFrame() we.ErrorFrame {
    return we.ErrorFrame{
        Code:    "account/insufficient-funds",
        Message: e.Error(),
        Fields: map[string]we.ErrorField{
            "balance":  we.MakeI64Field(e.Balance),
            "required": we.MakeI64Field(e.Required),
        },
    }
}
```

A handler returning this error produces the terminal message
`wee-events:error-frame+json:{"code":"account/insufficient-funds",...}` on
the wire — decodable by any wee-events implementation.

A frame that fails to encode (zero-value `ErrorField`) surfaces as an
internal-server terminal error, exactly as the rejection path handles it
today.

### 2. Client: one classification pipeline, two entry points

The pipeline that exists inside `Client.classifyFailure` — strip ingress
decoration, decode the frame, consult registered `FrameDecoder`s in order,
fall back to the generic `we.Rejection` — is extracted into one shared
function. Two entry points use it:

- **Ingress lane (existing):** `Client.classifyFailure` delegates to the
  shared pipeline. Behaviour is unchanged.
- **In-handler / orchestrator lane (new):** an exported function

  ```go
  // DeclaredError classifies an error returned by a Restate SDK call (or any
  // error whose message may carry a wee-events error frame). ok reports
  // whether the error was a declared service error; when true the returned
  // error is the decoded declared error — a decoder-mapped typed error, or
  // the generic we.Rejection fallback. ok=false means the transport /
  // infrastructure lane: the input is not a declared service outcome.
  func DeclaredError(err error, decoders ...FrameDecoder) (error, bool)
  ```

  A Restate-native orchestrator calls the target service with the SDK's own
  client and classifies the returned error with one call. The same function
  serves any holder of a terminal-error message (e.g. a Temporal activity
  that received a propagated failure string).

Decoration stripping (`stripIngressDecoration`) is applied defensively in
both lanes: the runtime provably decorates at the ingress edge, and the
service-to-service integration test (below) establishes empirically what it
does between services; stripping tolerates both presence and absence.

Decoder semantics are shared verbatim with the ingress client: consulted in
order, first claim wins, a claiming decoder must return a non-nil error, and
an unclaimed frame falls back to `we.Rejection` carrying the frame's code,
message, and fields. The generic fallback is the guaranteed floor of the
contract — typed decoding is opt-in refinement, reconciled per team.

`DeclaredError(nil, ...)` returns `(nil, false)`.

### 3. Ingress client hardening

- Send `Accept: application/json` on every ingress request.
- Bound response-body reads with `io.LimitReader`, using the same limit value
  as the server-side `maxBodyBytes`. A body at the cap boundary must still
  decode; a response exceeding the cap is a transport error, not a
  truncated-then-half-decoded value.
- Transport-lane client tests gain the symmetric negative assertion: a
  transport-lane error never satisfies `errors.As` for `we.Rejection` (and
  is not claimed by `DeclaredError`).

## Verification

Unit:

- `mapError` frames a custom `ServiceErrorContract` implementation (not
  `we.Rejection`): terminal, frame bytes carry code/message/fields, original
  error reachable through Unwrap.
- `mapError` regression: `we.Rejection` behaviour byte-identical to today.
- A non-contract error remains retryable.
- `DeclaredError`: decodes a framed terminal error (decorated and
  undecorated), respects decoder order and the non-nil-claim rule, falls back
  to generic `we.Rejection`, returns ok=false for non-frame errors and nil.
- Body-cap boundary conditions; Accept header presence.

Integration (containerised Restate runtime, the load-bearing proof):

- Service A calls service B inside a handler. B returns a typed declared
  error. A recovers it via `DeclaredError` with code and fields intact. This
  test also documents what decoration the runtime applies on the
  service-to-service path.
- Existing ingress round-trip tests continue to pass unchanged.

Gates: `just lint`, `just test`, `just test-integration`.

## Requirements Traceability

Extends RESTATE-S3 (boundary error mapping): R2/R3 generalise from "domain
rejection" to "declared service error" with `we.Rejection` as the base case.
The Declared-vs-Transport lane separation is preserved: a frame always
becomes a declared error, never a transport failure, and transport failures
never masquerade as declared outcomes.
