# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Environment

This project uses [mise](https://mise.jdx.dev) for toolchain and task management
(Go 1.26, golangci-lint, gopls, natscli). Provision it once:

```bash
mise install
```

## Common Development Commands

Tasks are defined in `.mise.toml` and run with `mise run <task>`:

```bash
# Run all unit tests
mise run test

# Build the sample server
mise run build

# Generate Wire dependency injection code (wire is a go.mod tool directive)
mise run wire

# Lint
mise run lint

# Apply Go 1.26 modernisers + gofmt simplification
mise run fix

# Update all dependencies to latest and tidy
mise run update-deps
```

Ad-hoc Go commands run through mise to pick up the pinned toolchain, e.g.
`mise exec -- go test -v ./we/...`.

### Running Integration Tests
Integration tests require Docker containers for EventStore and NATS:

```bash
mise run test:integration
```

This starts the services in `local/docker-compose.yml` and runs `go test -v ./stores/...`.

## Architecture Overview

This is an event sourcing framework implementing CQRS (Command Query Responsibility Segregation) with multiple event store backends.

### Core Concepts

1. **Aggregates** (`/we/aggregate.go`): Domain entities that process commands and emit events
2. **Commands** (`/we/command.go`): Intent to change state, processed by aggregates
3. **Events** (`/we/event.go`): Immutable records of state changes
4. **Reducers** (`/we/reducer.go`): Functions that apply events to rebuild aggregate state
5. **Entity Loaders** (`/we/entity-loader.go`): Reconstruct entities from event streams

### Package Structure

- `/we` - Core event sourcing framework (uses Go generics extensively)
- `/stores` - Event store implementations:
  - `/ds` - AWS DynamoDB backend
  - `/esbs` - EventStore DB backend
  - `/jetstream` - NATS JetStream backend
- `/samples/counter` - Complete example showing framework usage
- `/connectors/wehttp` - HTTP adapter for REST APIs

### Key Patterns

1. **Generics Usage**: The framework heavily uses Go 1.18+ generics for type-safe aggregate handling
2. **Dependency Injection**: Uses Google Wire for compile-time DI (see `wire.go` files)
3. **Functional Style**: Reducers and handlers follow functional patterns
4. **Event Metadata**: Events include revision numbers, timestamps, and correlation IDs

### Testing Approach

- Unit tests mock event stores using test implementations
- Integration tests use Docker containers via testcontainers-go
- The `/we/event-store-validation-suite.go` provides a standard test suite for all store implementations

### Conventions

Per `documents/conventions.md`:
- Logging: Only `info` (user-facing) and `debug` (developer) levels
- Constructors: `New*` returns pointer, `Make*` returns value
- Comments may include author initials (e.g., "KAO")