# SQLite Performance State: 2026-06-12

The important performance question is shard fan-out: how does writing to one
shard compare with writing to many independent shards in parallel?

The current answer is better, but not absolute. The store-wide remote mutex is
gone. Turso now runs remote shard operations without a store-level dispatch
gate. sqld uses a bounded dispatch cap of 4 because unbounded go-libsql
concurrency still reproduces hard hangs inside `libsql_prepare`. That cap keeps
more than one independent sqld shard active at once, while avoiding the driver
failure seen in the full benchmark matrix.

Sources:

- Final full-suite log: `/tmp/wee-events-go-bench-all-final.txt`
- Focused diagnostic benchmarks run after this report work:
  - `BenchmarkShardFanoutLocal`
  - `BenchmarkShardFanoutSqld`
  - `BenchmarkShardFanoutTurso`
- Post-fix regression check:
  - `mise exec -- just bench-integration` passed after serializing benchmark
    packages with `go test -p 1`.
  - `SQLITE_SHARD_FANOUT_WIDTHS=1,10 ... BenchmarkShardFanoutTurso` passed.

## What The Existing Suite Measures

The benchmark names are easy to misread:

- `creation/*` means "publish one event to a fresh aggregate." For lazy
  partitioned SQLite strategies, this may include opening/provisioning a shard,
  migrating schema, recording partition metadata, and writing the event.
- `steady_state/publish_batch/*` seeds the aggregate before timing. These rows
  are the best measure of event-write latency on an existing shard.
- `partition_write/*` also seeds aggregates before timing. These rows measure
  existing-shard concurrent writes.

For `Global` strategies, `spread` aggregate ids still route to one physical
shard. For named strategies such as `ByType`, `ByAggregate`, and
`PartitionBy(type)`, `spread` can route to many physical shards.

The full suite only benchmarked Turso as `TursoGlobal`, so those Turso
`spread/*` rows are not multi-database Turso fan-out. A focused diagnostic
benchmark was added for that.

## Steady Existing-Shard Writes

These figures come from the final `bench-all` log. Creation/provisioning rows
are intentionally excluded.

| Strategy | Write 1 | Write 10 total / event | Write 50 total / event | Append | Revision write | Read 500 |
|---|---:|---:|---:|---:|---:|---:|
| InMemory | 17.0 us | 49.0 us / 4.9 us | 187.4 us / 3.7 us | 16.9 us | 9.9 ms | 1.8 ms |
| LocalGlobal | 61.1 us | 111.7 us / 11.2 us | 272.2 us / 5.4 us | 54.8 us | 8.3 ms | 1.8 ms |
| LocalByType | 60.3 us | 106.4 us / 10.6 us | 265.1 us / 5.3 us | 51.3 us | 7.9 ms | 1.8 ms |
| LocalByAggregate | 60.5 us | 102.3 us / 10.2 us | 273.1 us / 5.5 us | 53.0 us | 8.3 ms | 1.9 ms |
| LocalHashed | 192.7 us | 122.2 us / 12.2 us | 288.4 us / 5.8 us | 54.3 us | 8.9 ms | 2.1 ms |
| LocalPartitionBy | 62.3 us | 110.8 us / 11.1 us | 267.5 us / 5.3 us | 57.0 us | 9.6 ms | 2.2 ms |
| SqldDefaultGlobal | 4.2 ms | 5.9 ms / 587.3 us | 12.2 ms / 243.2 us | 10.3 ms | 13.2 ms | 6.3 ms |
| SqldGlobal | 10.0 ms | 13.3 ms / 1.3 ms | 15.8 ms / 315.6 us | 8.4 ms | 16.2 ms | 7.2 ms |
| SqldByType | 10.7 ms | 11.7 ms / 1.2 ms | 15.7 ms / 313.9 us | 10.5 ms | 16.1 ms | 6.8 ms |
| SqldByAggregate | 9.5 ms | 12.6 ms / 1.3 ms | 14.0 ms / 279.0 us | 9.7 ms | 16.2 ms | 7.0 ms |
| SqldHashed | 9.6 ms | 12.3 ms / 1.2 ms | 15.8 ms / 316.3 us | 11.9 ms | 17.4 ms | 7.6 ms |
| SqldPartitionBy | 11.8 ms | 12.8 ms / 1.3 ms | 16.6 ms / 331.6 us | 13.0 ms | 18.1 ms | 7.0 ms |
| TursoGlobal | 1.1 s | 1.1 s / 106.2 ms | 1.1 s / 21.4 ms | 1.1 s | 2.2 s | 630.6 ms |

## Focused Shard Fan-Out

The new diagnostic benchmark pre-seeds shards outside the timed loop, then
measures one wave of parallel writes:

- `one_shard`: every worker writes a different aggregate routed to the same
  shard.
- `many_shards`: every worker writes a different aggregate routed to a distinct
  shard.

The test `TestSQLiteShardFanoutBenchmarkIDsMapToExpectedPartitions` verifies
that the width-100 inputs map to exactly 1 partition for `one_shard` and exactly
100 partitions for `many_shards`.

| Backend | Width | One shard | Many shards | Observation |
|---|---:|---:|---:|---|
| Local | 1 | 301.9 us | 173.5 us | Single-run noise dominates. |
| Local | 10 | 1.2 ms | 468.6 us | Spreading helps. |
| Local | 100 | 6.8 ms | 12.3 ms | This single run regressed under many files/shards; needs benchstat before reading too much into it. |
| sqld | 1 | 13.0 ms | 12.4 ms | Similar remote floor. |
| sqld | 10 | 90.9 ms | 43.2 ms | Many shards are faster with the sqld cap at 4. |
| sqld | 100 | 1.14 s | 171.3 ms | Many shards are much faster with the sqld cap at 4. |
| Turso | 1 | 1.12 s | 1.15 s | Same remote floor. |
| Turso | 10 | 12.37 s | 2.52 s | Many databases are much faster; clean command passed. |
| Turso | 50 | 67.34 s | 3.04 s | Quota-capped substitute for width 100; timing printed before manual cleanup interrupt. |

Earlier, attempting the live Turso width-100 diagnostic hit a Turso platform
`400` while creating shard database `fo-053`, which appears to be an account or
group database quota. A later interrupted width-50 run left 48 benchmark
databases under the configured prefix; after deleting those, a clean width-10
Turso diagnostic passed. The benchmark supports `SQLITE_SHARD_FANOUT_WIDTHS`,
so live Turso fan-out should be run at widths that fit the account quota.
The canonical scale ladder is still orders of magnitude: `1,10,100`. Turso
width 50 is only a live-account fallback when width 100 cannot be provisioned.

Future fan-out reporting should use the fixed-wave metrics suite instead of
single-run `ns/op` alone. The headline comparison should include `ops/s`,
`wave_p95_ms`, and `op_p95_ms` for `1,10,100` widths. Turso may use `1,10,50`
only when account quota blocks width 100.

## Interpretation

There is no storage-model reason a write wave across 50 independent Turso
databases should behave like 50 serial writes to one database. The updated
Turso path now matches that expectation: one-shard rows scale roughly with
worker count, while many-shards rows stay much closer to the live remote floor.

The key implementation change was replacing the store-wide remote operation
mutex with backend-specific behavior:

- every shard still serializes its own operations through its owner goroutine;
- Turso has no store-level dispatch gate, so independent Turso databases can
  execute concurrently;
- sqld uses a bounded dispatch gate of 4, because unbounded sqld operations
  reproduced a cgo hang in `go-libsql._Cfunc_libsql_prepare` during both write
  and read benchmark phases.

The sqld cap is a driver-stability compromise, not a storage-model requirement.
It preserves useful fan-out but does not claim unbounded sqld parallelism.

## Current Highs

Local SQLite writes remain strong. Existing-shard local writes cluster around
`60 us` for one event and about `5-6 us` per event in a 50-event batch.

sqld batching is effective. Single-event writes are milliseconds, but 50-event
batches amortize to roughly `243-332 us` per event.

Remote shard fan-out now works where it matters for the benchmark question.
sqld width-100 many-shards is `171.3 ms` versus `1.14 s` for one shard under the
cap of 4. Turso width-10 many-shards is `2.52 s` versus `12.37 s` for one shard;
the width-50 timing printed at `3.04 s` versus `67.34 s` before manual cleanup
interruption.

`bench-integration` now passes after changing the recipe to run benchmark
packages with `go test -p 1`; this avoids package-level Testcontainers port
races while leaving benchmark-level concurrency intact.

## Current Lows

Turso has a high live-operation floor: roughly `1.1 s` for a single event write
in this run.

The Turso fan-out benchmark cannot currently run at width 100 in this account
because the platform rejected database creation at shard 53.

Unbounded sqld remote operations are still unsafe with the current go-libsql
driver. Cap 8 passed the focused write diagnostic but the broader matrix later
hung in `go-libsql._Cfunc_libsql_prepare` during `SqldHashed/partition_read`.
Cap 4 passed both the focused sqld diagnostics and `bench-integration`.

## Next Investigation

The next performance question is whether this remains stable under repeated
runs and whether a driver upgrade or alternative driver can remove the sqld cap.
The target behavior is now visible:

- one shard: writes serialize through that shard owner;
- many shards: writes proceed independently across shard owners;
- no return to the go-libsql hangs that motivated the temporary global gate.

Until repeated runs are collected, read these as single-run diagnostic figures,
not benchstat-grade performance claims.

## Commands

```bash
mise exec -- go test ./stores/sqlite -run 'TestSQLiteShardFanoutBenchmarkIDsMapToExpectedPartitions' -count=1

mise exec -- go test -run '^$' -bench '^BenchmarkShardFanoutLocal$' -benchmem -benchtime=1x ./stores/sqlite

mise exec -- go test -run '^$' -bench '^BenchmarkShardFanoutSqld$' -benchmem -benchtime=1x -timeout 30m ./stores/sqlite

SQLITE_SHARD_FANOUT_WIDTHS=1,10,100 mise exec -- go test -run '^$' -bench '^BenchmarkShardFanoutTurso$' -benchmem -benchtime=1x -timeout 120m ./stores/sqlite

# If Turso quota rejects width 100, use an explicit quota-capped fallback:
SQLITE_SHARD_FANOUT_WIDTHS=1,10,50 mise exec -- go test -run '^$' -bench '^BenchmarkShardFanoutTurso$' -benchmem -benchtime=1x -timeout 120m ./stores/sqlite

mise exec -- just bench-integration

mise exec -- just bench-metrics

mise exec -- just bench-metrics-integration

mise exec -- just bench-metrics-turso 1,10,50
```
