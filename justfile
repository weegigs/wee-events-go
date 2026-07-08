# Project tasks for wee-events-go.
# Toolchain (go, golangci-lint, gopls, just) is provided by mise; run these
# from a mise-activated shell so the pinned tools are on PATH.

# List available recipes
default:
    @just --list

# Run all unit tests
test:
    go test -v ./...

# Build the counter sample server
build:
    go build -o counter-server ./samples/counter/server

# Lint
lint:
    golangci-lint run

# Apply Go 1.26 modernisers + gofmt simplification
fix:
    go fix ./...
    gofmt -s -w .

# Tidy go.mod / go.sum
tidy:
    go mod tidy

# Update all dependencies to latest and tidy
update-deps:
    go get -u ./...
    go mod tidy

# Run integration tests (requires Docker)
test-integration:
    docker compose -f local/docker-compose.yml up -d
    go test -v ./stores/...

# Run event-store benchmarks for local stores (no Docker)
bench filter='^(BenchmarkMemory|BenchmarkSqlite(InMemory|Local))':
    go test -run '^$' -bench '{{filter}}' -benchmem -timeout 30m ./we ./stores/sqlite

# Run event-store benchmarks for Docker-backed stores (testcontainers, requires Docker).
bench-integration filter='^(BenchmarkSqliteSqld|BenchmarkJetStream|BenchmarkKurrent|BenchmarkDynamo)':
    go test -p 1 -run '^$' -bench '{{filter}}' -benchmem -timeout 120m ./stores/sqlite ./stores/jetstream ./stores/kurrent ./stores/ds

# Run live Turso benchmarks (requires TURSO_* credentials; provisions temporary databases).
bench-turso filter='^BenchmarkSqliteTurso': check-turso-env
    go test -run '^$' -bench '{{filter}}' -benchmem -timeout 120m ./stores/sqlite

# Fail fast unless the live Turso benchmark environment is configured.
check-turso-env:
    @missing=0; \
    for name in TURSO_ORG TURSO_GROUP TURSO_DB_PREFIX TURSO_API_TOKEN TURSO_GROUP_TOKEN; do \
      eval "value=\${$name:-}"; \
      if [ -z "$value" ]; then \
        echo "missing required Turso benchmark environment variable: $name" >&2; \
        missing=1; \
      fi; \
    done; \
    exit "$missing"

# Run every benchmark tier, including live Turso.
bench-all:
    just check-turso-env
    just bench
    just bench-integration
    just bench-turso

# Run fixed-wave metrics benchmarks for local stores.
bench-metrics widths='1,10,100' waves='30' filter='^(BenchmarkMemoryMetrics|BenchmarkMetricsSqlite(InMemory|Local))':
    WE_METRICS_WIDTHS='{{widths}}' WE_METRICS_WAVES='{{waves}}' go test -run '^$' -bench '{{filter}}' -benchmem -benchtime=1x -timeout 60m ./we ./stores/sqlite

# Run fixed-wave metrics benchmarks for Docker-backed stores.
bench-metrics-integration widths='1,10,100' waves='30' filter='^(BenchmarkMetricsSqliteSqld|BenchmarkMetricsJetStream|BenchmarkMetricsKurrent|BenchmarkMetricsDynamo)':
    WE_METRICS_WIDTHS='{{widths}}' WE_METRICS_WAVES='{{waves}}' go test -p 1 -run '^$' -bench '{{filter}}' -benchmem -benchtime=1x -timeout 120m ./stores/sqlite ./stores/jetstream ./stores/kurrent ./stores/ds

# Run fixed-wave metrics benchmarks for live Turso.
bench-metrics-turso widths='1,10,100' waves='30' filter='^BenchmarkMetricsSqliteTurso': check-turso-env
    WE_METRICS_WIDTHS='{{widths}}' WE_METRICS_WAVES='{{waves}}' go test -run '^$' -bench '{{filter}}' -benchmem -benchtime=1x -timeout 120m ./stores/sqlite

# Run every fixed-wave metrics tier, including live Turso.
bench-metrics-all widths='1,10,100' waves='30':
    just check-turso-env
    just bench-metrics '{{widths}}' '{{waves}}'
    just bench-metrics-integration '{{widths}}' '{{waves}}'
    just bench-metrics-turso '{{widths}}' '{{waves}}'

# Compare two benchmark result files (benchstat is a go.mod tool directive)
bench-compare old new:
    go tool benchstat {{old}} {{new}}
