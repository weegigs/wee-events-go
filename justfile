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
bench filter='.':
    go test -run '^$' -bench '{{filter}}' -benchmem -timeout 30m ./we ./stores/sqlite

# Run event-store benchmarks for Docker-backed stores (testcontainers, requires Docker)
bench-integration filter='.':
    go test -run '^$' -bench '{{filter}}' -benchmem -timeout 120m ./stores/jetstream ./stores/kurrent ./stores/ds

# Compare two benchmark result files (benchstat is a go.mod tool directive)
bench-compare old new:
    go tool benchstat {{old}} {{new}}
