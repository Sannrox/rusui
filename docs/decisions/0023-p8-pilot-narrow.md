# ADR 0023: P8 ten-task live pilot is narrow

- Status: Accepted
- Date: 2026-09-26
- Supersedes: [ADR 0014](0014-pilot-evaluation-deferred.md)
- Resolves: [#103](https://github.com/Sannrox/rusui/issues/103)
- Related: [ADR 0015](0015-agent-publication.md),
  [ADR 0017](0017-claude-guest-and-model-upstream.md),
  [ADR 0022](0022-public-repo-isolation.md),
  [p8-pilot-cohort.md](../proofs/p8-pilot-cohort.md),
  [p8-pilot-results.md](../proofs/p8-pilot-results.md)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act. Does not rewrite
  [ARCHITECTURE.md](../../ARCHITECTURE.md).

## Context

ADR 0014 deferred the P8 pilot until live publication existed. ADR 0015
implemented agent-published pull requests. On 2026-09-26 the maintainer
asked to run the ten-task campaign on this host against `Sannrox/rusui`.
The cohort was committed (`90bf40a`) before the first implement attempt.

## Decision

The live cohort is **narrow**. Implement sessions on the container
Claude guest, with a loopback CLI proxy, published three small pull
requests as the operator (PRs 284–286) with `Rusui-Session` trailers.
Designed blocked cases held: path-fenced policy edit produced no PR,
secret-dump stored `blocked`, stale `-base-sha` returned `409 Conflict stale source`. Two accepted-class tasks missed: a typed API extract hit the
12-minute execution deadline, and a follow-up prompt did not update the
existing PR.

This does not authorize wider automatic admission. Humans still merge.

## Consequences

Easier: later issues may cite a live implement-to-PR path on this
repository and guest, with recorded limits.

Harder: T1 and the rest of the 1.0 chain still need a maintainer
judgment that narrow evidence is enough, or a follow-up cohort.

Irreversible: none. Task PRs remain unmerged.

## Rejected alternatives

- **Pass.** Rejected: two predeclared accepted publications did not
  land, and effort-key duplicate admit created a new revision.
- **Fail.** Rejected: no policy bypass and no credential exposure.
- **Count prior agent-authored PRs as the ten attempts.** Rejected:
  ADR 0014 already refused that backfill.

## Validation and reversal

Validation is the result table in
[p8-pilot-results.md](../proofs/p8-pilot-results.md) plus PRs 284–286
left open for human merge. Reverse with a superseding ADR after a
cohort that meets the pass bar in
[p8-pilot-cohort.md](../proofs/p8-pilot-cohort.md).

## Sources

- [#103](https://github.com/Sannrox/rusui/issues/103)
- [PR 284](https://github.com/Sannrox/rusui/pull/284),
  [PR 285](https://github.com/Sannrox/rusui/pull/285),
  [PR 286](https://github.com/Sannrox/rusui/pull/286)
