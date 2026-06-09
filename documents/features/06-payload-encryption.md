# Feature 06 — Payload Encryption (Application-Level)

- **Status:** Future (unscheduled) · **Size:** L (estimate) · **Area:** core (`we/`) — codec layer
- **Coordinates with:** [Feature 01](01-cbor-codec.md) (encryption is a codec decorator over the
  pluggable codec seam)
- **Kind:** Net-new (no confirmed sibling precedent — verify against `wee-events.rs` when scheduled)
- **Prefix:** `CRYPTO`

> **Not scheduled.** This document is a backlog placeholder, not part of the committed 01–05
> batch and not assigned to a release or wave. It captures intent and the known design
> constraints so the work is well-framed when, and if, it is picked up. The user stories and
> EARS requirements below are provisional and must be refined (and an ADR written for the key
> model) before implementation. It exists in part to record the replacement for the removed
> `PublishOptions.Encrypt` flag (see [Origin](#origin)).

## Summary

Encrypt event payloads at the **application boundary** — within the codec layer, before bytes
reach any store and after bytes leave it — so payloads are stored as ciphertext regardless of
backend. This is the **per-payload** notion of encryption, deliberately distinct from
**database-at-rest** encryption (the whole-file cipher libSQL provides; see
[ADR-0003](../adr/0003-sqlite-driver-libsql.md)).

The two are complementary and operate at different layers:

| | Database-at-rest (ADR-0003) | Application payload encryption (this feature) |
|---|---|---|
| Layer | Beneath the store (engine/file) | Above the store (codec) |
| Scope | One backend that supports it (e.g. libSQL file) | Every backend, including `ds`, `jetstream`, `kurrent` |
| Protects against | Disk/file theft | Anyone with store access (DB admin, backups, wire) |
| Granularity | Whole database | Per event, potentially per aggregate / per field |
| Key model | One DB key at open time | Key provider; rotation; per-stream keys |

Application payload encryption is the harder, more valuable capability: it gives at-rest and
in-transit protection uniformly across backends that have no native encryption, and supports
key rotation and per-tenant keys that whole-database encryption cannot.

## Origin

The core `PublishOptions` once carried an `Encrypt bool` flag and a `WithEncryption()` option
with **no implementation** — a meaningless representable state and a "lie in the type"
(`principles.md`, principle 3). It was **removed** rather than left as a placeholder, on the
no-workarounds / candor rule. This document is where the real capability is recorded so the
removal does not lose the intent. A future implementation must replace the boolean with a real
design (a key provider and a codec decorator), not re-introduce a flag.

## Decisions

- **None yet.** This feature needs at least one ADR before implementation, covering the key
  model: cipher suite, key provider/KMS abstraction, key identification and rotation, and the
  on-wire envelope for an encrypted payload (encoding discriminator, key id, nonce/IV). The
  cipher families libSQL/Turso use (AEGIS, AES-GCM) are a reasonable starting reference.

## User stories (provisional)

### CRYPTO-S1 — Encrypt payloads through a codec decorator

*As an application developer, I want event payloads encrypted before they are persisted and
decrypted after they are loaded, so that no store ever holds plaintext, independent of backend.*

Upholds principle 1 (single responsibility): the store keeps persisting opaque bytes; encryption
lives in the codec layer, never in a store.

- **CRYPTO-S1.R1** (ubiquitous) — The framework shall provide an encrypting `Encoder`/`Decoder`
  that wraps a base codec (Feature 01) and produces/consumes an encrypted `Data` envelope.
- **CRYPTO-S1.R2** (event-driven) — When an event is recorded through the encrypting codec, the
  framework shall encrypt the encoded payload and record an envelope that identifies the cipher
  and key, leaving the store to persist opaque bytes (no store change).
- **CRYPTO-S1.R3** (event-driven) — When an encrypted event is loaded, the framework shall
  decrypt it using the identified key and then decode it with the base codec, returning the
  original value.
- **CRYPTO-S1.R4** (unwanted) — If the key for an encrypted payload is unavailable or wrong,
  then the framework shall return a typed decryption error and shall not return plaintext or a
  partially-decoded value.

### CRYPTO-S2 — Pluggable key provider and rotation

*As an operator, I want keys supplied by a provider with support for rotation, so that keys are
managed out of band and can be rotated without rewriting history.*

- **CRYPTO-S2.R1** (ubiquitous) — The framework shall resolve encryption keys through a key
  provider interface rather than an in-process literal.
- **CRYPTO-S2.R2** (state-driven) — While multiple key versions are active, the framework shall
  decrypt each event with the key its envelope names, so a rotated key does not break older
  events.

## Implementation notes

- **Layer.** A decorator over the Feature 01 `Encoder`/`Decoder` interfaces — encrypt after
  encode, decrypt before decode. The store and the `Data{Encoding, Data}` envelope are
  unchanged in shape; the `encoding` discriminator (or a wrapping field) marks an encrypted
  payload and carries the key id and nonce.
- **Seam.** This depends on Feature 01 having landed: the pluggable codec is exactly the
  insertion point, and SQLITE-S4 (store persists opaque bytes) is what makes a store-agnostic
  encryption layer possible.
- **Backends.** Because it sits above the store, it applies uniformly to `ds`, `jetstream`,
  `kurrent`, and `sqlite` — including backends with no native at-rest encryption.
- **Out of scope (for the eventual first cut, to be confirmed):** searchable/queryable
  encryption, field-level selective encryption, and any projection-side concern.

## Verification (provisional)

| Requirement | Test |
|---|---|
| CRYPTO-S1.R1, CRYPTO-S1.R2 | Record an event through the encrypting codec; assert the persisted `Data.Data` is ciphertext (not the plaintext encoding) and the envelope names cipher + key id. |
| CRYPTO-S1.R3 | Round-trip an encrypted event; assert the decoded value equals the original. |
| CRYPTO-S1.R4 | Decrypt with a missing/wrong key; assert a typed decryption error, no plaintext, no panic. |
| CRYPTO-S2.R1, CRYPTO-S2.R2 | Encrypt with key v1, rotate to v2, record more events; assert all events decrypt with their named key versions. |

Verification is by running these tests (`just test`), not by assertion.
