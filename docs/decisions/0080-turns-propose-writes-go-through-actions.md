# ADR 0080: Agent turns only propose; writes go through governed Actions

- Status: Accepted; amended by [ADR 0084](0084-core-draft-actions.md)
- Status notes: definition drafts are a second propose/apply target.
- Date: 2026-10-14
- Amends: [ADR 0015](0015-agent-publication.md) (guest GitHub credential),
  [ADR 0044](0044-plane-publishes-from-turn-result.md) (plane writes on
  Complete).
- Resolves: [#607](https://github.com/Sannrox/rusui/issues/607).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0020](0020-turn-scoped-github-publication.md).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

Implement turns could write GitHub two ways: the guest held the operator
PAT and ran `gh pr create`, or Complete called the plane publisher from
the turn result. Both skip a governed Action. Review apply stays dry-run.
The session already records Actions; no new Action type family is needed.

## Decision

1. A turn that asks to write records `write.propose` and outcome
   `proposed`. Complete does not call GitHub.
2. Applying the proposal is `POST /sessions/{id}/write-actions` (operator)
   which performs the former Complete-time GitHub write and records
   `write.apply` as the receipt. Review JSON stays immutable; session
   publication reads `write.apply`.
3. Guests never receive `github_token`. Implement claims always set
   `publication=plane`. The git proxy allows only session refs. `gh pr
   create` / `edit` and default-branch pushes are fenced.
4. No new Action types beyond `write.propose` and `write.apply` on the
   existing `actions` table. No schema migration.

## Consequences

A turn cannot land a GitHub change by skipping Actions. Plane publication
must be on for apply to succeed. `RUSUI_AGENT_GITHUB_TOKEN` is ignored.

## Rejected alternatives

- **Keep guest PAT for agent publication.** That is a write from the turn.
- **New Action type family / schema.** The issue forbids designing new
  Action types; the existing table is enough.
- **Live Chisei client.** Rusui has no Chisei client; a local Action plus
  one SQLite transaction is the analogue.

## Validation and reversal

Tests: Complete does not call `PublishPull`; apply writes once and stores
a session-referenced receipt; guest ingest of `write.propose` is refused;
git proxy and fence refuse default-branch and `gh pr create`. Revert by
restoring Complete-time publication.

## Sources

- [#607](https://github.com/Sannrox/rusui/issues/607)
