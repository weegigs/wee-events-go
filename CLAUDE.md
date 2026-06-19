# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Environment

This project uses [mise](https://mise.jdx.dev) for the toolchain (Go 1.26,
golangci-lint, gopls, natscli, just) and [just](https://just.systems) for project
tasks. Provision the tools once:

```bash
mise install
```

## Common Development Commands

Tasks are defined in the `justfile` and run with `just <recipe>` from a
mise-activated shell (`just --list` shows them all):

```bash
# Run all unit tests
just test

# Build the sample server
just build

# Lint
just lint

# Apply Go 1.26 modernisers + gofmt simplification
just fix

# Update all dependencies to latest and tidy
just update-deps
```

Ad-hoc Go commands pick up the pinned toolchain via mise, e.g.
`mise exec -- go test -v ./we/...`.

### Running Integration Tests
Integration tests require Docker containers for KurrentDB and NATS:

```bash
just test-integration
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
  - `/kurrent` - KurrentDB backend (formerly EventStore DB)
  - `/jetstream` - NATS JetStream backend
- `/samples/counter` - Complete example showing framework usage
- `/connectors/wehttp` - HTTP adapter for REST APIs

### Key Patterns

1. **Generics Usage**: The framework heavily uses Go 1.18+ generics for type-safe aggregate handling
2. **Dependency Injection**: Manual constructor wiring at each binary's composition root (e.g. `local`/`live` in the samples) — no DI framework (ADR-0012)
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

## Encoding Layers

Four layers, one owner each (`documents/adr/0011-encoding-boundary.md`;
design: `docs/superpowers/specs/2026-06-10-encoding-boundary-design.md`):

1. **Application format** — typed events per language (e.g. a Go struct)
2. **Wire format** — owned by the API edge, negotiated via Content-Type
   (wehttp JSON/CBOR intake; werestate payloads)
3. **Presentation contract** — `we.Data{encoding, []byte}`: what Publish
   hands a store and what Load hands back; verbatim at the store boundary
   (original bytes, original tag — never transcoded)
4. **Storage format** — store-chosen and optimal for the medium (jetstream
   CBOR envelope, ds binary attribute, sqlite BLOB, …)

Before changing anything encoding-touching: which layer is being changed,
and does the change make one layer's property leak into another?

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:6cd5cc61 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->
