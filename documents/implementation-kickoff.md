# Implementation Kickoff — Coordinating Agent Brief

This document briefs a **coordinating agent** that manages the implementation of the
missing features (01–05) by delegating each to a worker, verifying deliverables, and
integrating them in dependency order. Hand this file to a fresh agent — or load it as the
opening context of a coordinator session — and it has everything needed to run the work.

A ready-to-paste bootstrap prompt is at the end ([Spin up the coordinator](#spin-up-the-coordinator)).

## Mission

Drive every feature in [`features/`](features/README.md) from **Planned** to **Done**:
implemented, tested, lint-clean, and merged — without two workers ever touching the same
file at the same time.

## Read before doing anything

The coordinator must read these first; they are the source of truth and override any
assumption:

1. [`roadmap.md`](roadmap.md) — status table, sequencing, the ADR log.
2. [`features/README.md`](features/README.md) — the epic/story/EARS model, the **file-ownership
   matrix**, and the dependency graph. This is the contract that keeps work parallel-safe.
3. The five feature epics, [`features/01-cbor-codec.md`](features/01-cbor-codec.md) …
   [`features/05-rejection-error-taxonomy.md`](features/05-rejection-error-taxonomy.md) — each
   is a series of user stories with EARS requirements (IDs like `CBOR-S2.R1`) and a
   verification table. The requirements **are** the acceptance criteria.
4. [`principles.md`](principles.md) — single responsibility; explicit resource lifecycle;
   illegal-states-unrepresentable within Go's limits. Workers are held to these.
5. [`adr/README.md`](adr/README.md) — accepted and **proposed** decisions. Proposed ADRs
   (0003, 0004) must be locked before the dependent code is wired (see
   [Decisions to lock early](#decisions-to-lock-early)).
6. Repo root `CLAUDE.md` and [`conventions.md`](conventions.md) — house rules:
   `New*`/`Make*`, `info`/`debug` logging only, past-tense commits, no AI co-author notes,
   no lint suppressions, T-shirt sizing not time estimates, jj split workflow.

## Definition of done (per feature)

A feature is Done only when **all** hold:

- Every EARS requirement in the epic is implemented and covered by a test; the test or its
  commit cites the requirement ID it satisfies.
- `just test` is green and `just lint` is clean. The lint gate now enforces `errcheck`,
  `bodyclose`, and `sqlclosecheck` (ADR-0006) — an unchecked `Close()` fails CI. Suppressions
  are not an option.
- New event-store backends pass the conformance suite (feature 04), **including the
  shared-backing pair tests**, against in-memory and local-file targets.
- Any **Proposed** ADR the feature depends on is resolved to **Accepted** (or superseded)
  once implementation locks the decision.
- The feature's `Status:` header and the `roadmap.md` row are flipped to Done.

## Sequencing — waves

From the dependency graph in `features/README.md`:

- **01 (codec)** and **05 (rejection)** both edit `we/command.go` → serialize: 01 then 05.
- **02 (sqlite)**, **03 (restate)**, **04 (conformance)** are independent.
- **04** is foundational for **02**'s acceptance gate (02 must pass the enhanced suite), so
  land 04's suite early.

```
Wave A (parallel):   01 codec   |   04 conformance suite   |   02 sqlite   |   03 restate
                        │                  │                    │
                        ▼ (command.go free) ▼ (suite available) ▼ (validate against 04)
Wave B:              05 rejection           02 runs the new conformance scenarios
```

Practical ordering: start **01** and **04** immediately (both unblock others); start **02**
and **03** in parallel (new isolated packages); begin **05** once 01 has merged; gate **02**'s
"done" on **04** having merged.

## Worker isolation and merge protocol

Follow the repo's parallel-worker standards (`CLAUDE.md` → "Sub-Agent and Parallel Worker
Standards") and the superpowers workflow skills.

- **One workspace per worker.** Use a jj workspace per in-flight feature
  (`jj:jj-workspace` / `jj:spawn-worker`) so working copies never collide. Workers editing
  only new packages (02 `stores/sqlite/`, 03 `connectors/werestate/`) can share the main
  workspace safely *because their files are disjoint*; core-touching features (01, 05) and
  anything risky get their own workspace.
- **File-level ownership is absolute.** A worker writes only the files its row in the
  ownership matrix lists. The single overlap (`we/command.go`, 01 & 05) is serialized, never
  concurrent.
- **TDD.** Workers use `superpowers:test-driven-development` — a failing test per EARS
  requirement first, then implementation. `superpowers:verification-before-completion`
  before any "done" claim: run the commands, paste the output, no assertions without evidence.
- **Merge as you go.** Review each deliverable the moment it lands (architecture review +
  field-by-field audit per the house standards), run `just test` and `just lint`, then
  integrate. Do not batch merges.
- **Commits.** jj split workflow, past tense, one logical change per commit (e.g. split the
  implementation from any ADR status flip).

## Coordinator loop

```
repeat until all features Done:
  1. SELECT features whose dependencies are merged and whose owned files are free.
  2. For each, SPAWN a worker with a self-contained brief (see dispatch card):
       epic path · requirement IDs to satisfy · owned files · principles · relevant ADRs · DoD.
  3. MONITOR — herdr panes for live terminal work; Agent subagents for context-isolated tasks.
  4. On worker completion: REVIEW → VERIFY (just test + just lint + conformance for stores)
       → MERGE → flip Status → resolve dependent ADR if the decision is now locked.
  5. UPDATE roadmap.md status; record blockers.
```

Spawning mechanisms available in this environment:
- **Agent subagents** — isolated context, results returned to the coordinator; best for
  finite, well-scoped tasks (the feature-doc conversion used these).
- **jj workspaces + `jj:spawn-worker`** — isolated working copies for genuinely parallel
  code edits that must not collide.
- **herdr** (`HERDR_ENV=1`, CLI on PATH) — real terminal panes: run a worker in one pane,
  `just test`/`just lint` in another, `herdr wait output` on results, watch live. Use
  `herdr pane split <id> --direction down|right` and `herdr pane run <id> "<cmd>"`.

## Dispatch cards

| Feature | Owns (writes only) | Depends on | Key ADR | Acceptance gate |
|---|---|---|---|---|
| 01 CBOR codec | `we/codec.go` (new), `we/data-marshaller.go`, `we/command.go` | — | 0001, 0002 | `CBOR-S*` reqs; JSON regression green |
| 02 SQLite/Turso | `stores/sqlite/**` (new) | 04 (for acceptance) | 0003 *(Proposed)* | conformance suite incl. shared-backing |
| 03 Restate | `connectors/werestate/**` (new) | — | 0004 *(Proposed)* | durable at-most-once; replay no double-apply |
| 04 Conformance | `we/event-store-validation-suite.go`; each store's `*_test.go` | — | — | existing 3 stores pass expanded suite |
| 05 Rejection | `we/rejection.go` (new), `we/command.go`, `we/service.go`, `we/dispatcher.go`, `connectors/wehttp/http.go` | 01 (shared `command.go`) | 0005 | 4xx/5xx never conflated |

Each worker brief should restate that file's EARS requirement IDs (from the epic) as its
explicit checklist.

## Decisions to lock early

These are **Proposed** ADRs; the coordinator must get a human decision (or confirm the
recommendation) before the dependent code is wired, then flip the ADR to Accepted:

- **ADR-0003** — SQLite driver: `go-libsql` (cgo) vs the pure-Go split. Blocks 02's driver
  wiring. Needs the build-target call (is cgo acceptable for distribution?).
- **ADR-0004** — Restate Go SDK version pin. Confirm `restatedev/sdk-go` builds under Go
  1.26 before 03 commits to an API surface.

## Verification commands (the gates)

From the `justfile` (run in a mise-activated shell):

- `just test` — unit tests (the primary gate).
- `just lint` — golangci-lint, now enforcing the lifecycle linters (ADR-0006).
- `just test-integration` — Docker-backed store tests (KurrentDB, NATS); relevant once 02
  adds a containerised SQLite/Turso target.
- `just build` — builds the sample server.
- `just fix` — Go 1.26 modernizers + gofmt; keep new code idiomatic (principles.md).

## Reporting

Keep `roadmap.md`'s feature status table current — one line per feature, Planned → In
progress → Done, with any blocker named. The ADR log table tracks decision status.

## Spin up the coordinator

Paste this to a fresh agent in this repo to start coordination:

> You are the coordinating agent for the wee-events-go feature implementation. Read
> `documents/implementation-kickoff.md` and everything it lists under "Read before doing
> anything", then drive features 01–05 to Done following the coordinator loop, wave
> sequencing, and file-ownership rules in that brief. Use one jj workspace per
> core-touching worker; verify every deliverable with `just test` and `just lint` (and the
> conformance suite for stores) before merging; merge as you go, never batch. Before wiring
> feature 02 or 03, surface ADR-0003 and ADR-0004 to me for a decision. Start by reading the
> brief and proposing your Wave A worker assignments; do not spawn workers until I approve
> the wave plan.
