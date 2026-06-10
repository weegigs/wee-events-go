# ADR-0007 — Event encoding is an explicit constructor argument

- **Status:** Accepted
- **Supersedes:** [ADR-0001](0001-default-event-encoding-json.md)
- **Relates to:** [features/08-explicit-event-encoding.md](../features/08-explicit-event-encoding.md) · [ADR-0002](0002-cbor-library-fxamacker.md)

## Context

ADR-0001 made `application/json` the default event encoder: `we.MarshalToData` silently
selects the JSON encoder, and every store writes JSON without any call site naming that
choice. The interop reasoning was sound — JSON is the cross-family wire format shared
with `wee-events.rs` — but the mechanism is an implicit package-level default.

The owner's direction (2026-06-10): **all defaults must be explicit.** A reader of a
store's construction site must see what bytes it writes; a hidden fallback is the kind
of implicitness that later reads as a surprise. The read path is already explicit — the
envelope's `encoding` discriminator selects the decoder — so the asymmetry is confined
to the write path.

## Decision

There is no default event encoding. `MarshalToData` requires an `Encoder` argument, and
every store constructor takes an explicit `we.Encoder` it uses for all writes. A `nil`
encoder is a constructor error. Encoding is a per-store-instance decision; `Publish`
offers no per-call override (a stream's encoding must not vary call-by-call).

JSON remains the **recommended** encoding wherever interop with the Rust/TypeScript
families or existing JSON streams matters — recommended here, in the samples, and in the
suite (which all pass `we.MakeJSONEncoder()` explicitly), but never assumed by code.

## Consequences

- Every construction site names its encoding; an unnamed encoding is a compile error,
  not a runtime fallback.
- With JSON supplied, output is byte-identical to the ADR-0001 behaviour — interop and
  parity guarantees carry over unchanged (CBOR-S3.R2 remains the oracle).
- The constructor surface of every store changes (breaking, pre-release): one added
  required parameter.
- ADR-0001's status becomes Superseded; its interop rationale survives as this ADR's
  recommendation rather than as a hidden default.

## Alternatives considered

- **Keep the implicit JSON default (status quo).** Rejected: a hidden package-level
  global the owner has explicitly ruled against; the cost of explicitness is one
  argument per construction site.
- **Per-publish encoder selection.** Rejected: noisier at every call site for no added
  safety, and it invites a single stream carrying mixed encodings chosen accidentally —
  the stream-level property is exactly what should be fixed at construction.
- **A configurable global default.** Rejected for the same reason ADR-0001 rejected it:
  two deployments could silently disagree about the bytes for the same stream; a global
  knob is implicitness with extra steps.
