# Performance Benchmarks

Latency tables for the shared event-store benchmark suite, covering all scenario
groups implemented in `/we/event-store-benchmark-suite.go`. Scenario semantics
are matched to the wee-events.rs reference suite so results are comparable across
implementations.

**How to run**

```bash
# Local stores only (no Docker)
just bench

# Docker-backed stores (testcontainers required)
just bench-integration

# Compare two result files
just bench-compare old.txt new.txt

# Benchstat-grade statistics (6 samples recommended)
go test -run '^$' -bench '.' -benchmem -count 6 -timeout 60m ./we ./stores/sqlite
```

Collected: _pending first run_ / environment: _pending_

---

## Creation

Single-stream creation and spread/concentrated multi-stream creation across
increasing concurrency widths.

| Pattern × concurrency | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| single | — | — | — | — | — | — |
| spread × 2 | — | — | — | — | — | — |
| spread × 4 | — | — | — | — | — | — |
| spread × 8 | — | — | — | — | — | — |
| spread × 16 | — | — | — | — | — | — |
| spread × 32 | — | — | — | — | — | — |
| concentrated × 2 | — | — | — | — | — | — |
| concentrated × 4 | — | — | — | — | — | — |
| concentrated × 8 | — | — | — | — | — | — |
| concentrated × 16 | — | — | — | — | — | — |
| concentrated × 32 | — | — | — | — | — | — |

## Steady-State Writes

Append operations under normal load: variable batch sizes, revision-checked
append, and unchecked append.

| Operation | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| batch 1 | — | — | — | — | — | — |
| batch 10 | — | — | — | — | — | — |
| batch 50 | — | — | — | — | — | — |
| with_revision | — | — | — | — | — | — |
| append | — | — | — | — | — | — |

## Load Scaling

Read latency as stream depth grows from empty to 500 events.

| Stream depth (events) | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| 0 | — | — | — | — | — | — |
| 1 | — | — | — | — | — | — |
| 10 | — | — | — | — | — | — |
| 50 | — | — | — | — | — | — |
| 100 | — | — | — | — | — | — |
| 500 | — | — | — | — | — | — |

## Partition Writes

Concurrent writes partitioned across spread, concentrated, and contention
patterns.

| Pattern × concurrency | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| spread × 2 | — | — | — | — | — | — |
| spread × 4 | — | — | — | — | — | — |
| spread × 8 | — | — | — | — | — | — |
| spread × 16 | — | — | — | — | — | — |
| spread × 32 | — | — | — | — | — | — |
| concentrated × 2 | — | — | — | — | — | — |
| concentrated × 4 | — | — | — | — | — | — |
| concentrated × 8 | — | — | — | — | — | — |
| concentrated × 16 | — | — | — | — | — | — |
| concentrated × 32 | — | — | — | — | — | — |
| contention × 2 | — | — | — | — | — | — |
| contention × 4 | — | — | — | — | — | — |
| contention × 8 | — | — | — | — | — | — |
| contention × 16 | — | — | — | — | — | — |
| contention × 32 | — | — | — | — | — | — |

## Partition Reads

Concurrent reads across spread and concentrated access patterns.

| Pattern × concurrency | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| spread × 2 | — | — | — | — | — | — |
| spread × 4 | — | — | — | — | — | — |
| spread × 8 | — | — | — | — | — | — |
| spread × 16 | — | — | — | — | — | — |
| spread × 32 | — | — | — | — | — | — |
| concentrated × 2 | — | — | — | — | — | — |
| concentrated × 4 | — | — | — | — | — | — |
| concentrated × 8 | — | — | — | — | — | — |
| concentrated × 16 | — | — | — | — | — | — |
| concentrated × 32 | — | — | — | — | — | — |

## Mixed Read/Write

Interleaved read and write operations at increasing concurrency widths.

| Concurrency | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| 2 | — | — | — | — | — | — |
| 4 | — | — | — | — | — | — |
| 8 | — | — | — | — | — | — |
| 16 | — | — | — | — | — | — |
| 32 | — | — | — | — | — | — |

---

## Known Measurement Artifacts

**publish_with_revision stream growth.** The `publish_with_revision` scenario
appends to the same stream across benchmark iterations, so the stream grows
monotonically during a run. Read-side latency inside that scenario increases
as the stream deepens. This behaviour is intentional and mirrors the reference
suite in wee-events.rs; numbers from this scenario should be read as
representing a mix of stream depths, not a steady-state value.

**In-memory store floor: wave fan-out and ULID generation.** The goroutine
spawn and WaitGroup rendezvous for concurrent waves costs approximately 1–2 µs
at width 32. Each `ulid.Make()` call adds approximately 150 ns. Both costs are
visible only in the in-memory store, where they form a non-trivial fraction of
total latency. Networked stores dominate on I/O and these costs are
insignificant by comparison.

**Docker Desktop VM networking on macOS.** Testcontainer-backed store numbers
(JetStream, Kurrent, DynamoDB-local) include the Docker Desktop VM networking
layer present on macOS hosts. These figures are machine-relative and should not
be compared directly to Linux CI or production numbers. Run on Linux for
cross-environment comparisons.

**Kurrent page size and contention error discarding.** Kurrent benchmarks use
the client default page size of 97 events; conformance tests use `PageSize(5)`.
Read latency for long streams will differ between the two. Contention scenario
benchmarks deliberately discard errors — `ns/op` for those rows includes time
spent on attempts that were rejected by backends enforcing optimistic
concurrency. The figure reflects throughput under contention, not per-success
latency.

---

## Summary

_Populated after the first full collection._
