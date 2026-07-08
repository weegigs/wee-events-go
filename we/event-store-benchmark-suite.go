package we

import (
	"context"
	"fmt"
	"testing"

	"github.com/oklog/ulid/v2"
)

// BenchmarkConcurrencyLevels is the default set of wave widths the concurrent
// benchmark groups sweep.
var BenchmarkConcurrencyLevels = []int{2, 4, 8, 16, 32}

// benchmarkLoadEventCounts is the aggregate-size sweep for load_scaling.
var benchmarkLoadEventCounts = []int{0, 1, 10, 50, 100, 500}

// benchmarkBatchSizes is the per-publish event-count sweep for
// steady_state/publish_batch.
var benchmarkBatchSizes = []int{1, 10, 50}

// benchmarkPayloadValue pads StoreBenchmarkEvent so its serialized JSON form is
// approximately 50 bytes — a fixed payload, unlike the validation suite's
// faker-generated events, so payload construction cost is constant and never
// distorts timed regions.
const benchmarkPayloadValue = "store-benchmark-payload-pad0"

// NewEventStoreBenchmarkSuite builds the shared benchmark suite over a store.
// The same suite runs against every backend; only the store construction (and
// any Docker plumbing around it) differs per package.
func NewEventStoreBenchmarkSuite(ctx context.Context, store EventStore, options ...BenchmarkSuiteOption) *EventStoreBenchmarkSuite {
	suite := &EventStoreBenchmarkSuite{
		store:  store,
		ctx:    ctx,
		levels: BenchmarkConcurrencyLevels,
	}
	for _, option := range options {
		option(suite)
	}

	return suite
}

type EventStoreBenchmarkSuite struct {
	store  EventStore
	ctx    context.Context
	levels []int
}

type BenchmarkSuiteOption func(*EventStoreBenchmarkSuite)

// WithBenchmarkConcurrencyLevels overrides the wave widths swept by the
// concurrent benchmark groups.
func WithBenchmarkConcurrencyLevels(levels ...int) BenchmarkSuiteOption {
	return func(suite *EventStoreBenchmarkSuite) {
		suite.levels = levels
	}
}

// StoreBenchmarkEvent is the fixed benchmark payload.
type StoreBenchmarkEvent struct {
	Value string `json:"value"`
	Index int    `json:"index"`
}

// makeBenchmarkEvents builds n fixed-payload events; Index carries the element
// index so batch members are distinguishable.
func makeBenchmarkEvents(count int) []DomainEvent {
	events := make([]DomainEvent, count)
	for i := range count {
		events[i] = StoreBenchmarkEvent{Value: benchmarkPayloadValue, Index: i}
	}

	return events
}

// Identity helpers generate keys with ulid.Make(), which is goroutine-safe.
// The validation suite's package-level ulid.Monotonic entropy is not, and
// these helpers run inside worker goroutines.

// makeBenchId identifies a single-partition, single-use aggregate.
func makeBenchId() AggregateId {
	return AggregateId{Type: "bench", Key: ulid.Make().String()}
}

// makeSpreadId places each worker on its own partition: the worker index is
// baked into the type, so writes and reads fan out across partitions.
func makeSpreadId(worker int) AggregateId {
	return AggregateId{Type: fmt.Sprintf("spread-%d", worker), Key: ulid.Make().String()}
}

// makeConcentratedId places every worker on one partition: the type is shared
// and only the key is fresh, so all traffic lands on a single partition.
func makeConcentratedId() AggregateId {
	return AggregateId{Type: "concentrated", Key: ulid.Make().String()}
}

// concentratedId adapts makeConcentratedId to the per-worker id signature the
// wave benchmarks take.
func concentratedId(_ int) AggregateId {
	return makeConcentratedId()
}

// runWave runs one wave of exactly n goroutines, each performing one
// operation, and joins them. One timed iteration is one wave, so ns/op is the
// latency of an n-wide wave. It is a thin wrapper over RunMeasuredWave, which
// holds the join logic shared with the metrics suite.
func runWave(n int, op func(worker int) error) error {
	return RunMeasuredWave(n, op).Err
}

// benchmark pairs a sub-benchmark path with the function that implements it.
type benchmark struct {
	name string
	run  func(b *testing.B)
}

// benchmarks returns the ordered set of sub-benchmarks Run registers. Slashes
// in the names produce nested b.Run groups (e.g. creation/spread/8).
func (s *EventStoreBenchmarkSuite) benchmarks() []benchmark {
	benchmarks := []benchmark{
		{"creation/single", s.creationSingle},
	}
	for _, level := range s.levels {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("creation/spread/%d", level), s.creationWave(level, makeSpreadId)})
	}
	for _, level := range s.levels {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("creation/concentrated/%d", level), s.creationWave(level, concentratedId)})
	}
	for _, size := range benchmarkBatchSizes {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("steady_state/publish_batch/%d", size), s.steadyStatePublish(size)})
	}
	benchmarks = append(benchmarks,
		benchmark{"steady_state/publish_with_revision", s.steadyStatePublishWithRevision},
		benchmark{"steady_state/append", s.steadyStatePublish(1)},
	)
	for _, count := range benchmarkLoadEventCounts {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("load_scaling/%d", count), s.loadScaling(count)})
	}
	for _, level := range s.levels {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("partition_write/spread/%d", level), s.partitionWrite(level, makeSpreadId)})
	}
	for _, level := range s.levels {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("partition_write/concentrated/%d", level), s.partitionWrite(level, concentratedId)})
	}
	for _, level := range s.levels {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("partition_write/contention/%d", level), s.partitionWriteContention(level)})
	}
	for _, level := range s.levels {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("partition_read/spread/%d", level), s.partitionRead(level, makeSpreadId)})
	}
	for _, level := range s.levels {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("partition_read/concentrated/%d", level), s.partitionRead(level, concentratedId)})
	}
	for _, level := range s.levels {
		benchmarks = append(benchmarks, benchmark{fmt.Sprintf("mixed/read_write/%d", level), s.mixedReadWrite(level)})
	}

	return benchmarks
}

func (s *EventStoreBenchmarkSuite) Run(b *testing.B) {
	for _, benchmark := range s.benchmarks() {
		b.Run(benchmark.name, benchmark.run)
	}
}

// seed pre-populates an aggregate outside any timed region. It must only be
// called from the benchmark goroutine: it fails via b.Fatal.
func (s *EventStoreBenchmarkSuite) seed(b *testing.B, id AggregateId, count int) {
	b.Helper()
	seedAggregate(b, s.ctx, s.store, id, count)
}

// creationSingle measures publishing one event to a fresh aggregate. The id is
// generated inside the timed region to match the reference suite; ulid.Make()
// costs roughly 150 ns, an acknowledged measurement floor.
func (s *EventStoreBenchmarkSuite) creationSingle(b *testing.B) {
	b.ReportAllocs()
	events := makeBenchmarkEvents(1)
	for b.Loop() {
		if err := s.store.Publish(s.ctx, makeBenchId(), Options(), events...); err != nil {
			b.Fatal(err)
		}
	}
}

// creationWave measures a wave of level workers, each creating a fresh
// aggregate (id generated inside the timed region) with one event.
func (s *EventStoreBenchmarkSuite) creationWave(level int, id func(worker int) AggregateId) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		events := makeBenchmarkEvents(1)
		for b.Loop() {
			err := runWave(level, func(worker int) error {
				return s.store.Publish(s.ctx, id(worker), Options(), events...)
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// steadyStatePublish measures publishing a batch of size events to one
// established aggregate. Seeding sits before b.Loop, whose timer starts at the
// first Loop call, so seeding is untimed and runs exactly once. The aggregate
// grows across iterations by design, matching the reference suite.
func (s *EventStoreBenchmarkSuite) steadyStatePublish(size int) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		id := makeBenchId()
		s.seed(b, id, 1)
		events := makeBenchmarkEvents(size)
		for b.Loop() {
			if err := s.store.Publish(s.ctx, id, Options(), events...); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// steadyStatePublishWithRevision measures the optimistic-concurrency
// round-trip: load the current revision, then publish conditioned on it.
func (s *EventStoreBenchmarkSuite) steadyStatePublishWithRevision(b *testing.B) {
	b.ReportAllocs()
	id := makeBenchId()
	s.seed(b, id, 1)
	events := makeBenchmarkEvents(1)
	for b.Loop() {
		aggregate, err := s.store.Load(s.ctx, id)
		if err != nil {
			b.Fatal(err)
		}
		if err := s.store.Publish(s.ctx, id, Options(WithExpectedRevision(aggregate.Revision)), events...); err != nil {
			b.Fatal(err)
		}
	}
}

// loadScaling measures Load against an aggregate of count events. A count of
// zero loads a fresh, never-published identity.
func (s *EventStoreBenchmarkSuite) loadScaling(count int) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		id := makeBenchId()
		s.seed(b, id, count)
		for b.Loop() {
			if _, err := s.store.Load(s.ctx, id); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// partitionWrite measures a wave of level workers each appending one event to
// its own pre-seeded aggregate; the id function decides whether the aggregates
// spread across partitions or concentrate on one.
func (s *EventStoreBenchmarkSuite) partitionWrite(level int, id func(worker int) AggregateId) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		ids := make([]AggregateId, level)
		for worker := range level {
			ids[worker] = id(worker)
			s.seed(b, ids[worker], 10)
		}
		events := makeBenchmarkEvents(1)
		for b.Loop() {
			err := runWave(level, func(worker int) error {
				return s.store.Publish(s.ctx, ids[worker], Options(), events...)
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// partitionWriteContention measures a wave of level workers all appending to
// one aggregate.
func (s *EventStoreBenchmarkSuite) partitionWriteContention(level int) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		id := makeBenchId()
		s.seed(b, id, 1)
		events := makeBenchmarkEvents(1)
		for b.Loop() {
			// Errors are deliberately discarded: concurrent appends to one
			// aggregate may legitimately conflict on some backends, and the
			// wave measures contention behaviour, not success. Mirrors the
			// reference suite.
			_ = runWave(level, func(worker int) error {
				return s.store.Publish(s.ctx, id, Options(), events...)
			})
		}
	}
}

// partitionRead measures a wave of level workers each loading its own
// 50-event aggregate; the id function decides the partition placement.
func (s *EventStoreBenchmarkSuite) partitionRead(level int, id func(worker int) AggregateId) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		ids := make([]AggregateId, level)
		for worker := range level {
			ids[worker] = id(worker)
			s.seed(b, ids[worker], 50)
		}
		for b.Loop() {
			err := runWave(level, func(worker int) error {
				_, err := s.store.Load(s.ctx, ids[worker])
				return err
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// mixedReadWrite measures a wave of 2×level workers over 2×level spread
// aggregates: even-indexed workers load, odd-indexed workers append. Readers
// own the even-indexed aggregates, so the aggregates being loaded stay at
// their seeded 50 events while the writers' aggregates grow.
func (s *EventStoreBenchmarkSuite) mixedReadWrite(level int) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		workers := 2 * level
		ids := make([]AggregateId, workers)
		for worker := range workers {
			ids[worker] = makeSpreadId(worker)
			s.seed(b, ids[worker], 50)
		}
		events := makeBenchmarkEvents(1)
		for b.Loop() {
			err := runWave(workers, func(worker int) error {
				if worker%2 == 0 {
					_, err := s.store.Load(s.ctx, ids[worker])
					return err
				}
				return s.store.Publish(s.ctx, ids[worker], Options(), events...)
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
