# ADR-0006 — Enforce resource-lifecycle principles via golangci-lint

- **Status:** Accepted
- **Relates to:** [principles.md](../principles.md)

## Context

[Principle 2](../principles.md) (explicit resource lifecycle) requires that resource
acquisition and release are paired and explicit at the call site: an unchecked `Close()`,
an unclosed HTTP response body, or an unclosed `sql.Rows`/`sql.Stmt` is a leak. The
principle states it is "a review blocker, not a suppression target" and names the linters
that catch it. Review alone does not scale — the guarantee needs machine checking on every
change so the obligation cannot regress silently between reviews.

The repository already runs `golangci-lint` through `just lint` with a v2-schema
`.golangci.yml`. Of the three lifecycle linters, `errcheck` is enabled in golangci-lint's
default set, while `bodyclose` and `sqlclosecheck` are default-off and therefore not active
unless requested.

[Principle 3](../principles.md) (illegal states unrepresentable) constrains the opposite
direction: it explicitly warns against "a blanket `exhaustive`-lint gate on every
interface" and directs that exhaustiveness checking be applied surgically to the few
genuinely closed variant sets, backed by a loud `default`. A global `exhaustive` gate would
contradict that guidance and pressure every interface switch toward foreign machinery.

The repository policy also rejects blanket suppressions (`//nolint`-style escapes) as a
workaround; the correct response to a lifecycle finding is to release the resource, not to
silence the linter.

## Decision

The framework will enforce principle 2 in CI by explicitly enabling `errcheck`,
`bodyclose`, and `sqlclosecheck` in `.golangci.yml`. All three are listed under
`linters.enable` so the lifecycle guarantee is intentional and visible rather than reliant
on golangci-lint's shifting default set.

The framework will deliberately **not** configure a global `exhaustive` gate. Exhaustiveness
remains a surgical, opt-in technique for the few closed variant sets, as principle 3
directs. Blanket suppressions are not introduced; lifecycle findings are fixed at the call
site.

## Consequences

- An unchecked `Close()`, an unclosed HTTP response body, or an unclosed sql resource now
  fails `just lint` and therefore CI, making the principle-2 obligation machine-enforced on
  every change.
- The change is additive to the existing config: the prior `staticcheck` ST1012 exclusion
  and the default linter set are retained unchanged.
- Enabling default-off linters can surface pre-existing violations elsewhere in the tree.
  At the time of this decision the three linters report **0 issues** across `./...`, so no
  cleanup backlog is incurred; future regressions will be caught at the point of
  introduction.
- A future ADR wishing to add exhaustiveness checking must scope it to specific packages or
  variant sets rather than enabling it globally, to stay consistent with principle 3.

## Alternatives considered

- **Enable a global `exhaustive` linter alongside the lifecycle linters.** Rejected:
  principle 3 explicitly warns against a blanket exhaustiveness gate; Go's compiler cannot
  prove exhaustiveness, and a global mandate would push every interface switch toward
  heavier machinery the principles reserve for genuinely closed sets.
- **Rely on review alone to catch unreleased resources.** Rejected: review does not scale
  to every change and regresses silently between passes; principle 2 names these linters
  precisely so the guarantee is mechanical.
- **Rely on golangci-lint's default set without listing the linters.** Rejected: `bodyclose`
  and `sqlclosecheck` are default-off and would not run, and default-set membership can
  change between tool versions; an explicit `enable` block makes the principle-2 contract
  durable and auditable.
- **Permit targeted `//nolint` suppressions for lifecycle findings.** Rejected: repository
  policy treats blanket suppressions as a workaround that masks the leak the linter exists
  to surface; the fix is to release the resource.
