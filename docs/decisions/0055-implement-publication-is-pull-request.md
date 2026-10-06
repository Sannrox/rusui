# ADR 0055: Implement publication stays a pull request

- Status: Superseded by [ADR 0068](0068-project-ship-behavior.md)
- Date: 2026-10-06
- Amends: [ADR 0015](0015-agent-publication.md) (the agent publishes
  its own pull requests; a push to the default branch is not a
  publication).
- Resolves: [#486](https://github.com/Sannrox/rusui/issues/486)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until
  rewritten), [#505](https://github.com/Sannrox/rusui/issues/505)
  (`push-base` remains blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

Implement sessions publish a pull request. A project cannot choose a
different publication, and a wider write must not become the default
by accident.

[#486](https://github.com/Sannrox/rusui/issues/486) asked whether
policy should name that publication. Two options:

1. A `ship` field. Omitted or `pull-request` keeps today's implement
   publication. `push-base` pushes the default branch and does not
   open a PR. Dogfood stays `pull-request`.
2. Keep pull-request publication only.

[ADR 0015](0015-agent-publication.md) made the agent open its own
pull request. Humans merge core delivery ([ADR 0010](0010-hybrid-roadmap-sequence.md)
D5). `push-base` is a write to the default branch without a review
surface. No named project needs that write. A policy field that
exists but dogfood must never set is complexity that waits for a
project that needs it.

Source evidence at the commit this decision was made against:

- Policy parse uses known fields. `ship` is not a field.
- An implement turn records a published pull request number. There is
  no push-to-default publication path.

## Decision

**D1. Keep pull-request publication only.** Implement still opens or
updates a pull request. Policy has no `ship` field.

**D2. `push-base` stays refused** until a superseding ADR names one
project that must write the default branch without a pull request,
and keeps dogfood on pull-request. [#505](https://github.com/Sannrox/rusui/issues/505)
remains blocked on that later choice.

## Consequences

- Easier: one publication. The default branch is still a human merge.
- Harder: a project that wants agent pushes to default waits for a
  later ADR.
- Schema, policy, runner contract, and public API are unchanged.

## Rejected alternatives

- **A `ship` field with `push-base` (option 1).** Adds a wider write
  and a policy knob dogfood must never set, without a named project
  that needs it.

## Validation and reversal

Accept on merge. Validated when policy still refuses `ship`, and an
implement turn still records a pull request.

Reverse with a superseding ADR that names `push-base`, the default
(`pull-request`), and the project that needs the wider write.

## Sources

- [#486](https://github.com/Sannrox/rusui/issues/486)
- [ADR 0015](0015-agent-publication.md)
- Decided against `main` at `d0f9501`.
