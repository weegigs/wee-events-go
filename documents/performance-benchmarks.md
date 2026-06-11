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

Collected: 2026-06-11 · Apple M4 Max (darwin/arm64, 16 hardware threads),
128 GiB, go1.26.4, Docker Engine 29.4.0 (Docker Desktop). Single run per leaf
(`-benchtime` default); values are ns/op converted to readable units. `✗`
marks a leaf that failed — every `✗` below is the local-file SQLite driver
defect described in Known Measurement Artifacts §5.

---

## Creation

Single-stream creation and spread/concentrated multi-stream creation across
increasing concurrency widths.

| Pattern × concurrency | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| single | 1.2 µs | 13.9 µs | 50.9 µs | 94.9 µs | 23.58 ms | 1.42 ms |
| spread × 2 | 5.5 µs | 43.0 µs | 1.65 ms | 113.1 µs | 6.08 ms | 1.25 ms |
| spread × 4 | 8.2 µs | 84.0 µs | 2.23 ms | 131.9 µs | 7.23 ms | 1.74 ms |
| spread × 8 | 13.7 µs | 160.5 µs | ✗ | 173.1 µs | 8.91 ms | 2.85 ms |
| spread × 16 | 26.8 µs | 314.2 µs | 50.39 ms | 244.0 µs | 8.24 ms | 5.21 ms |
| spread × 32 | 59.8 µs | 656.7 µs | 179.49 ms | 355.1 µs | 8.43 ms | 10.32 ms |
| concentrated × 2 | 5.4 µs | 43.6 µs | 1.59 ms | 114.2 µs | 2.45 ms | 970.4 µs |
| concentrated × 4 | 8.5 µs | 83.9 µs | ✗ | 134.8 µs | 2.84 ms | 1.67 ms |
| concentrated × 8 | 15.0 µs | 161.5 µs | ✗ | 174.9 µs | 2.90 ms | 2.89 ms |
| concentrated × 16 | 27.4 µs | 309.8 µs | ✗ | 244.2 µs | 3.44 ms | 6.12 ms |
| concentrated × 32 | 55.8 µs | 631.5 µs | ✗ | 355.9 µs | 9.57 ms | 12.27 ms |

## Steady-State Writes

Append operations under normal load: variable batch sizes, revision-checked
append, and unchecked append. Single-goroutine — unaffected by the local-file
driver defect.

| Operation | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| batch 1 | 579 ns | 14.7 µs | 57.0 µs | 94.8 µs | 6.39 ms | 512.0 µs |
| batch 10 | 4.3 µs | 71.5 µs | 135.9 µs | 109.2 µs | 7.09 ms | 640.3 µs |
| batch 50 | 19.3 µs | 305.1 µs | 380.1 µs | 145.9 µs | 4.08 ms | 827.3 µs |
| with_revision | 128.4 µs | 9.18 ms | 8.24 ms | 2.69 ms | 8.98 ms | 3.28 ms |
| append | 576 ns | 15.3 µs | 55.7 µs | 92.6 µs | 5.48 ms | 603.1 µs |

## Load Scaling

Read latency as stream depth grows from empty to 500 events. Single-goroutine.

| Stream depth (events) | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| 0 | 22 ns | 4.6 µs | 5.3 µs | 100.5 µs | 303.0 µs | 353.1 µs |
| 1 | 54 ns | 8.9 µs | 9.6 µs | 399.3 µs | 417.2 µs | 263.0 µs |
| 10 | 172 ns | 44.2 µs | 44.0 µs | 394.9 µs | 603.6 µs | 279.2 µs |
| 50 | 759 ns | 194.4 µs | 196.0 µs | 489.9 µs | 1.32 ms | 430.2 µs |
| 100 | 1.2 µs | 397.0 µs | 378.9 µs | 594.9 µs | 2.97 ms | 636.6 µs |
| 500 | 6.9 µs | 1.82 ms | 1.84 ms | 2.02 ms | 13.60 ms | 3.15 ms |

## Partition Writes

Concurrent writes partitioned across spread, concentrated, and contention
patterns.

| Pattern × concurrency | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| spread × 2 | 3.9 µs | 46.8 µs | 1.95 ms | 157.6 µs | 6.54 ms | 1.03 ms |
| spread × 4 | 7.1 µs | 89.7 µs | ✗ | 129.3 µs | 2.45 ms | 1.72 ms |
| spread × 8 | 11.5 µs | 176.8 µs | ✗ | 168.2 µs | 2.77 ms | 2.95 ms |
| spread × 16 | 21.9 µs | 354.7 µs | ✗ | 231.5 µs | 3.40 ms | 6.32 ms |
| spread × 32 | 42.9 µs | 772.3 µs | ✗ | 327.0 µs | 4.25 ms | 12.88 ms |
| concentrated × 2 | 4.0 µs | 47.0 µs | ✗ | 113.2 µs | 2.14 ms | 946.8 µs |
| concentrated × 4 | 6.0 µs | 91.7 µs | ✗ | 131.8 µs | 2.53 ms | 1.68 ms |
| concentrated × 8 | 11.9 µs | 180.1 µs | ✗ | 166.0 µs | 5.05 ms | 3.05 ms |
| concentrated × 16 | 23.0 µs | 425.2 µs | ✗ | 227.3 µs | 14.33 ms | 6.48 ms |
| concentrated × 32 | 44.4 µs | 776.9 µs | ✗ | 326.7 µs | 16.01 ms | 12.94 ms |
| contention × 2 | 3.5 µs | 46.7 µs | ✗ | 114.2 µs | 11.34 ms | 76.66 ms |
| contention × 4 | 5.9 µs | 89.1 µs | ✗ | 129.6 µs | 12.82 ms | 171.66 ms |
| contention × 8 | 10.9 µs | 173.4 µs | ✗ | 165.5 µs | 14.94 ms | 191.13 ms |
| contention × 16 | 21.1 µs | 335.9 µs | ✗ | 227.2 µs | 3.61 ms | 323.14 ms |
| contention × 32 | 42.7 µs | 673.2 µs | ✗ | 320.2 µs | 4.61 ms | 482.39 ms |

## Partition Reads

Concurrent reads across spread and concentrated access patterns.

| Pattern × concurrency | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| spread × 2 | 3.7 µs | 402.3 µs | ✗ | 660.5 µs | 1.97 ms | 839.2 µs |
| spread × 4 | 6.3 µs | 845.1 µs | ✗ | 900.0 µs | 2.86 ms | 818.2 µs |
| spread × 8 | 12.0 µs | 1.61 ms | ✗ | 1.48 ms | 3.32 ms | 1.03 ms |
| spread × 16 | 24.3 µs | 3.24 ms | ✗ | 2.50 ms | 4.99 ms | 2.45 ms |
| spread × 32 | 65.9 µs | 6.46 ms | ✗ | 4.18 ms | 7.38 ms | 5.28 ms |
| concentrated × 2 | 2.9 µs | 394.2 µs | ✗ | 669.2 µs | 1.71 ms | 586.7 µs |
| concentrated × 4 | 5.9 µs | 794.2 µs | ✗ | 994.1 µs | 2.49 ms | 806.2 µs |
| concentrated × 8 | 12.5 µs | 1.58 ms | ✗ | 1.51 ms | 3.49 ms | 1.01 ms |
| concentrated × 16 | 24.7 µs | 3.23 ms | ✗ | 2.66 ms | 5.54 ms | 3.07 ms |
| concentrated × 32 | 51.2 µs | 6.37 ms | ✗ | 3.94 ms | 9.53 ms | 8.50 ms |

## Mixed Read/Write

Interleaved read and write operations at increasing concurrency widths.

| Concurrency | Memory | SQLite mem | SQLite file | JetStream | Kurrent | DynamoDB-local |
|---|---|---|---|---|---|---|
| 2 | 6.8 µs | 456.8 µs | ✗ | 804.2 µs | 5.82 ms | 1.49 ms |
| 4 | 12.8 µs | 915.1 µs | ✗ | 965.9 µs | 4.95 ms | 2.37 ms |
| 8 | 23.7 µs | 1.96 ms | ✗ | 1.57 ms | 7.70 ms | 4.40 ms |
| 16 | 45.6 µs | 3.80 ms | ✗ | 2.88 ms | 10.86 ms | 11.41 ms |
| 32 | 87.9 µs | 7.35 ms | ✗ | 3.87 ms | 10.75 ms | 69.87 ms |

---

## Known Measurement Artifacts

**publish_with_revision stream growth.** The `publish_with_revision` scenario
appends to the same stream across benchmark iterations, so the stream grows
monotonically during a run. Read-side latency inside that scenario increases
as the stream deepens. This behaviour is intentional and mirrors the reference
suite in wee-events.rs; numbers from this scenario should be read as
representing a mix of stream depths, not a steady-state value. The effect is
clearly visible above: with_revision is one to two orders of magnitude above
append on the fast stores.

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
latency. DynamoDB-local's contention rows (77–482 ms) are dominated by
conditional-write conflict rejections inside the local emulator.

**Local-file SQLite fails under concurrency (`✗` cells).** Every concurrent
leaf against the local-file target either failed with
`SQLite failure: bad parameter or other API misuse` (SQLITE_MISUSE) from the
go-libsql driver — on `BEGIN IMMEDIATE`, `PRAGMA busy_timeout`, and statement
prepare alike, for reads as well as writes — or recorded pathological latency
(creation spread × 16/32 at 50 ms and 179 ms against 1.6 ms at width 2). All
single-goroutine leaves pass, which is why the conformance suite (also
single-goroutine) never observed it. The in-memory target is immune only
because `InMemory()` pins the pool to one connection, serialising all access.
The defect is in concurrent use of multiple pool connections against one local
file and is tracked outside this document; the affected cells stay `✗` until
the store or driver is fixed.

---

## Summary

JetStream is the standout networked store on this hardware: ~95 µs appends,
sub-µs-to-ms reads, and flat scaling across every concurrency width with no
contention collapse. DynamoDB-local sits at ~0.5–1 ms per operation but
degrades sharply under single-stream contention (482 ms at width 32). Kurrent
carries a large one-time stream-creation cost (~24 ms) and 3–16 ms operations
against the local container. SQLite in-memory is consistently 10–100× faster
than every networked store but serialises on one connection; the local-file
target is unusable under concurrent load until the go-libsql defect is
resolved. The in-memory reference store bounds the framework overhead itself
at roughly 0.5–90 µs across all scenarios.
