package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weegigs/wee-events-go/we"
)

// benchLocal runs the shared event-store benchmark suite against a local-file
// backend rooted at root with the given strategy.
func benchLocal(b *testing.B, root string, strategy LocalStrategy) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(root, strategy))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

// BenchmarkSqliteInMemory measures the in-memory single-database store.
func BenchmarkSqliteInMemory(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

// BenchmarkSqliteLocalGlobal uses a single file as the store root: Global routes
// every aggregate to one database, so its root must be a file path, not the
// directory the named strategies use.
func BenchmarkSqliteLocalGlobal(b *testing.B) {
	benchLocal(b, filepath.Join(b.TempDir(), "events.db"), Global())
}

func BenchmarkSqliteLocalByType(b *testing.B)      { benchLocal(b, b.TempDir(), ByType()) }
func BenchmarkSqliteLocalByAggregate(b *testing.B) { benchLocal(b, b.TempDir(), ByAggregate()) }
func BenchmarkSqliteLocalHashed(b *testing.B)      { benchLocal(b, b.TempDir(), Hashed(8)) }
func BenchmarkSqliteLocalPartitionBy(b *testing.B) {
	benchLocal(b, b.TempDir(), PartitionBy(func(id we.AggregateId) string { return id.Type }))
}

type sharedSqldBenchmarkInstance struct {
	container testcontainers.Container
	dataURL   string
	adminURL  string
}

var (
	sqldBenchmarkMu     sync.Mutex
	sqldBenchmarkShared *sharedSqldBenchmarkInstance
)

func TestMain(m *testing.M) {
	code := m.Run()
	if sqldBenchmarkShared != nil {
		_ = sqldBenchmarkShared.container.Terminate(context.Background())
	}
	os.Exit(code)
}

func ensureSqldBenchmarkInstance(ctx context.Context) (*sharedSqldBenchmarkInstance, error) {
	sqldBenchmarkMu.Lock()
	defer sqldBenchmarkMu.Unlock()

	if sqldBenchmarkShared != nil {
		return sqldBenchmarkShared, nil
	}

	ctr, err := testcontainers.Run(
		ctx,
		"ghcr.io/tursodatabase/libsql-server:latest",
		testcontainers.WithEnv(map[string]string{
			"SQLD_NODE": "primary",
		}),
		testcontainers.WithExposedPorts("8080/tcp", "9090/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("8080/tcp").WithStartupTimeout(2*time.Minute),
		),
		testcontainers.WithCmd(
			"/bin/sqld",
			"--admin-listen-addr",
			"0.0.0.0:9090",
			"--enable-namespaces",
		),
	)
	if err != nil {
		if ctr != nil {
			_ = ctr.Terminate(ctx)
		}
		return nil, err
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, err
	}

	dataPort, err := ctr.MappedPort(ctx, "8080/tcp")
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, err
	}

	adminPort, err := ctr.MappedPort(ctx, "9090/tcp")
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, err
	}

	sqldBenchmarkShared = &sharedSqldBenchmarkInstance{
		container: ctr,
		dataURL:   fmt.Sprintf("http://%s:%s", host, dataPort.Port()),
		adminURL:  fmt.Sprintf("http://%s:%s", host, adminPort.Port()),
	}
	return sqldBenchmarkShared, nil
}

func benchSqldDefault(b *testing.B, strategy SingleTargetStrategy) {
	ctx := context.Background()
	instance, err := ensureSqldBenchmarkInstance(ctx)
	if err != nil {
		b.Fatal(err)
	}

	store, err := NewStore(ctx, we.MakeJSONEncoder(), SqldDefault(instance.dataURL, "", strategy))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

func benchSqldNamespaced(b *testing.B, strategy NamingStrategy, options ...we.BenchmarkSuiteOption) {
	ctx := context.Background()
	instance, err := ensureSqldBenchmarkInstance(ctx)
	if err != nil {
		b.Fatal(err)
	}

	store, err := NewStore(ctx, we.MakeJSONEncoder(), SqldNamespaced(instance.adminURL, instance.dataURL, "", strategy))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store, options...).Run(b)
}

func runSqldMetrics(b *testing.B, strategy NamingStrategy) {
	ctx := context.Background()
	instance, err := ensureSqldBenchmarkInstance(ctx)
	if err != nil {
		b.Fatal(err)
	}

	store, err := NewStore(ctx, we.MakeJSONEncoder(), SqldNamespaced(instance.adminURL, instance.dataURL, "", strategy))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}

func BenchmarkSqliteSqldDefaultGlobal(b *testing.B) { benchSqldDefault(b, Global()) }
func BenchmarkSqliteSqldGlobal(b *testing.B)        { benchSqldNamespaced(b, Global()) }
func BenchmarkSqliteSqldByType(b *testing.B)        { benchSqldNamespaced(b, ByType()) }

func BenchmarkSqliteSqldByAggregate(b *testing.B) {
	benchSqldNamespaced(b, ByAggregate(), we.WithBenchmarkConcurrencyLevels(2, 4, 8, 16))
}

func BenchmarkSqliteSqldHashed(b *testing.B) { benchSqldNamespaced(b, Hashed(8)) }
func BenchmarkSqliteSqldPartitionBy(b *testing.B) {
	benchSqldNamespaced(
		b,
		PartitionBy(func(id we.AggregateId) string { return id.Type }),
		we.WithBenchmarkConcurrencyLevels(2, 4, 8, 16),
	)
}

func BenchmarkMetricsSqliteSqldGlobal(b *testing.B) { runSqldMetrics(b, Global()) }
func BenchmarkMetricsSqliteSqldByType(b *testing.B) { runSqldMetrics(b, ByType()) }

func BenchmarkSqliteTursoGlobal(b *testing.B) {
	cfg := tursoConfigFromEnv(b)
	cfg.Prefix = cfg.Prefix + "-bench-" + shortBenchmarkSuffix()
	b.Cleanup(func() {
		cleanupTursoBenchmarkPrefix(b, cfg)
	})

	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Turso(cfg, Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

func runTursoMetrics(b *testing.B, strategy NamingStrategy) {
	cfg := tursoConfigFromEnv(b)
	cfg.Prefix = cfg.Prefix + "-metrics-" + shortBenchmarkSuffix()
	b.Cleanup(func() {
		cleanupTursoBenchmarkPrefix(b, cfg)
	})

	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Turso(cfg, strategy))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}

func BenchmarkMetricsSqliteTursoGlobal(b *testing.B) { runTursoMetrics(b, Global()) }
func BenchmarkMetricsSqliteTursoByType(b *testing.B) { runTursoMetrics(b, ByType()) }

// BenchmarkShardFanoutTurso isolates the question the full suite does not
// answer: after shards already exist, how does live Turso behave when a wave of
// writes targets one shard versus many independent shards?
//
// It is intentionally outside the default just bench-turso filter
// (^BenchmarkSqliteTurso): a 100-shard live run can hit Turso account/database
// quotas and is a diagnostic benchmark, not a normal all-suite gate.
func BenchmarkShardFanoutTurso(b *testing.B) {
	cfg := tursoConfigFromEnv(b)
	cfg.Prefix = cfg.Prefix + "-fo-" + shortBenchmarkSuffix()
	b.Cleanup(func() {
		cleanupTursoBenchmarkPrefix(b, cfg)
	})

	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Turso(cfg, fanoutByTypeStrategy()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteShardFanoutBenchmarks(ctx, b, store, sqliteShardFanoutWidths(b))
}

func BenchmarkShardFanoutSqld(b *testing.B) {
	ctx := context.Background()
	instance, err := ensureSqldBenchmarkInstance(ctx)
	if err != nil {
		b.Fatal(err)
	}

	store, err := NewStore(ctx, we.MakeJSONEncoder(), SqldNamespaced(instance.adminURL, instance.dataURL, "", fanoutByTypeStrategy()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteShardFanoutBenchmarks(ctx, b, store, sqliteShardFanoutWidths(b))
}

func BenchmarkShardFanoutLocal(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(b.TempDir(), fanoutByTypeStrategy()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteShardFanoutBenchmarks(ctx, b, store, sqliteShardFanoutWidths(b))
}

func cleanupTursoBenchmarkPrefix(b *testing.B, cfg TursoConfig) {
	b.Helper()
	client := newHTTPTursoClient(cfg.APIToken)
	ctx := context.Background()
	databases, err := client.ListDatabases(ctx, cfg.Org)
	if err != nil {
		b.Logf("failed to list turso databases for cleanup: %v", err)
		return
	}
	for _, db := range databases {
		if db.Name == cfg.Prefix || strings.HasPrefix(db.Name, cfg.Prefix+"-") {
			if err := client.DeleteDatabase(ctx, cfg.Org, db.Name); err != nil {
				b.Logf("failed to delete turso benchmark database %q: %v", db.Name, err)
			}
		}
	}
}

func fanoutByTypeStrategy() *partitionBy {
	return PartitionBy(func(id we.AggregateId) string { return id.Type })
}

func sqliteShardFanoutWidths(b *testing.B) []int {
	b.Helper()
	raw := strings.TrimSpace(os.Getenv("SQLITE_SHARD_FANOUT_WIDTHS"))
	if raw == "" {
		return []int{1, 10, 100}
	}
	widths, err := we.ParsePositiveIntList(raw)
	if err != nil {
		b.Fatalf("invalid SQLITE_SHARD_FANOUT_WIDTHS value %q", raw)
	}
	return widths
}

func runSQLiteShardFanoutBenchmarks(ctx context.Context, b *testing.B, store we.EventStore, widths []int) {
	for _, width := range widths {
		b.Run(fmt.Sprintf("write/one_shard/%d", width), func(b *testing.B) {
			benchFanoutWrite(ctx, b, store, fanoutOneShardIDs(width))
		})
		b.Run(fmt.Sprintf("write/many_shards/%d", width), func(b *testing.B) {
			benchFanoutWrite(ctx, b, store, fanoutManyShardIDs(width))
		})
	}
}

func benchFanoutWrite(ctx context.Context, b *testing.B, store we.EventStore, ids []we.AggregateId) {
	b.ReportAllocs()
	for _, id := range ids {
		if err := store.Publish(ctx, id, we.Options(), makeFanoutEvents(10)...); err != nil {
			b.Fatal(err)
		}
	}
	events := makeFanoutEvents(1)
	for b.Loop() {
		if err := we.RunMeasuredWave(len(ids), func(worker int) error {
			return store.Publish(ctx, ids[worker], we.Options(), events...)
		}).Err; err != nil {
			b.Fatal(err)
		}
	}
}

func fanoutOneShardIDs(width int) []we.AggregateId {
	ids := make([]we.AggregateId, width)
	for i := range ids {
		ids[i] = we.AggregateId{Type: "fo-one", Key: fmt.Sprintf("%03d", i)}
	}
	return ids
}

func fanoutManyShardIDs(width int) []we.AggregateId {
	ids := make([]we.AggregateId, width)
	for i := range ids {
		ids[i] = we.AggregateId{Type: fmt.Sprintf("fo-%03d", i), Key: "aggregate"}
	}
	return ids
}

func shortBenchmarkSuffix() string {
	id := strings.ToLower(ulid.Make().String())
	return id[len(id)-8:]
}

func makeFanoutEvents(count int) []we.DomainEvent {
	events := make([]we.DomainEvent, count)
	for i := range events {
		events[i] = testEvent{Value: fmt.Sprintf("fanout-%d", i)}
	}
	return events
}

func runSQLiteMetrics(b *testing.B, store we.EventStore) {
	b.Helper()
	we.RunMetricsBenchmark(b, context.Background(), store)
}

func BenchmarkMetricsSqliteInMemory(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}

func BenchmarkMetricsSqliteLocalGlobal(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(filepath.Join(b.TempDir(), "events.db"), Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}

func BenchmarkMetricsSqliteLocalByType(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(b.TempDir(), ByType()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}
