# ADR 0014: Ten-task pilot evaluation is deferred pending live plane-owned publication

- Status: Proposed; amended by [ADR 0015](0015-agent-publication.md)
  (preconditions 1–2 and the identity-collapse failure criterion no
  longer apply; the pilot runs once ADR 0015 is implemented, against a
  named pilot repository and guest)
- Date: 2026-09-22
- Investigates: [#103](https://github.com/Sannrox/rusui/issues/103)
- Related: [ADR 0010](0010-hybrid-roadmap-sequence.md) (M2 verified
  delivery), [ADR 0013](0013-publication-authority.md) (candidate, proof,
  and publication objects; independent-session review). Does not amend
  [ARCHITECTURE.md](../../ARCHITECTURE.md).
- Discussion: none. GitHub Discussions are disabled. Merging with status
  Proposed records the investigation, per the route in #103, and leaves
  #103 open as the next pilot to run. Accepting this file is a separate
  maintainer act.

## Context

[#103](https://github.com/Sannrox/rusui/issues/103) (roadmap P8) asks for
a predeclared ten-task pilot: representative tasks, a manual baseline or
an explicit statement that time savings cannot yet be claimed, per-attempt
outcome and evidence records, fault-injection cases, and a predeclared
pass/narrow/defer/fail decision. Its dependencies — P5
([#100](https://github.com/Sannrox/rusui/issues/100)), P6
([#101](https://github.com/Sannrox/rusui/issues/101)), and P7
([#102](https://github.com/Sannrox/rusui/issues/102)) — are closed, which
makes #103 the only issue in the open backlog with every dependency
satisfied; every other open issue depends on #103 directly or
transitively.

ADR 0013 names the objects a verified delivery attempt must produce:
source, task, candidate `(commit_sha, tree_sha, ref)`, proof `(command,
exit_code, log_digest, head_sha, base_sha)`, and publication (a PR opened
or updated by the plane's `Publisher`, using the installation token, on
`refs/heads/rusui/<session>/*`). ADR 0013 D2 requires independent review
of that exact `commit_sha` from **a different session than the author**,
and states plainly: "The author session cannot supply that review" —
never an agent self-declaration.

The `Publisher` write surface (`open_pr` / `update_pr`,
`publication_attempts` persistence) merged in
[#177](https://github.com/Sannrox/rusui/pull/177) on 2026-09-21, the day
before this investigation. Its verification is unit and blackbox test
coverage (duplicate delivery, stale proof, changed policy, cancelled,
unnamed action, default-branch base) — no live invocation against a real
repository is recorded.

To check whether pilot-grade evidence already exists from recent
delivery, this investigation queried every pull request merged since
[#87](https://github.com/Sannrox/rusui/issues/87) accepted the M1–M6
sequence (2026-09-20 through 2026-09-21, 32 PRs, `#147`–`#178`), against
the GitHub API on 2026-09-22:

- 21 of 32 carry an "Agent Transcript" section describing an authoring
  session's own goal, decisions, and verification.
- **0 of 32** carry a native GitHub PR review (`reviews: []`).
- **32 of 32** were authored and merged by the same account
  (`Sannrox`), 2–15 minutes apart.
- None carry the candidate/proof/publication identifiers ADR 0013 D1
  names (`commit_sha` + `tree_sha` + `ref`, isolated proof log digest,
  `publication_attempts` row). Where a transcript reports review, it is
  prose written by the author session in its own PR description (for
  example #177: "Codex review unavailable (usage limit); Grok review
  produced no discrete findings") — the exact self-declaration pattern
  ADR 0013 D2 says independent review must never be substituted with.

This is evidence about the process that has been delivering rusui's own
issues, not about the pilot #103 asks for. No PR in this repository has
yet been opened by the plane's own `Publisher` against a live task; the
component merged on 2026-09-21 and, as of this review on 2026-09-22, its
only exercised paths are its own test suite.

## Decision

Defer #103. Zero of the required ten attempts currently exist through
the plane-owned candidate → proof → publication path ADR 0013 defines.
Running the pilot now, against the pre-`Publisher` delivery history,
would evaluate a different and less rigorous process than the one #103
is gating: one with no independent-session review and no separation
between the identity that writes code and the identity that merges it.
Backfilling that history as the pilot cohort would launder an
audit gap into a passing result.

A comparable manual baseline has not been collected; time savings cannot
be claimed from this investigation.

### Preconditions for running the actual pilot

1. `Publisher.open_pr` / `update_pr` is exercised end to end by a real
   session against a real target repository — not only its test suite.
2. Independent review runs as **a distinct session** from the author,
   evaluating the named `commit_sha`, with its verdict recorded as plane
   state (a proof or review record), not as prose the author session
   wrote about itself.
3. A named pilot repository (this repository, or another repository the
   maintainer explicitly approves) and a named guest profile are fixed
   before the first attempt, so the cohort is not selected after seeing
   results.

### Predeclared pilot methodology, to run once preconditions hold

Ten tasks, selected in advance and not replaced after a result is known,
spanning:

- one narrow configuration or documentation fix;
- one typed API addition with an accompanying test;
- one bug fix carrying its own regression test;
- one refactor bounded to a single package;
- one task predeclared to be denied by policy (a fault-injection case,
  not a real defect), to exercise the fail-closed path rather than only
  the success path.

Per attempt, record: candidate `commit_sha`/`tree_sha`/`ref`, proof
command/exit code/log digest, publication attempt id and PR link,
outcome (`accepted` / `failed` / `blocked` / `abandoned`), human
intervention minutes, count and category of independent-review
corrections, and wall-clock latency from task start to publication.
Duplicate delivery, stale proof, denied policy, and cancelled-task cases
already have blackbox/unit coverage from #101's `Publisher` tests; that
coverage is evidence for the mechanism, not a substitute for a live pilot
attempt, and the pilot should still exercise at least one of these paths
live.

Predeclared success criteria for the pilot itself (subject to maintainer
revision before the pilot starts, not after): **pass** requires at least
eight of ten attempts reaching `accepted` with independent review
confirming no critical defect and a human-intervention median under a
maintainer-set threshold; **narrow** admits a reduced task category if
only some categories clear that bar; **fail** is a pattern of policy
bypass, credential exposure, or author/reviewer identity collapse
occurring during the pilot. This ADR does not itself authorize wider
admission — that decision belongs to whichever pilot run actually
produces the ten attempts.

### Flag for whoever runs the pilot or its successor, T1 (#104)

The author/merger-identity and zero-native-review pattern observed above
in `#147`–`#178` is a related risk, not a #103 finding to fix here:
git/GitHub metadata alone cannot distinguish "a human reviewed and
merged quickly" from "the authoring session merged unattended." Whoever
designs T1's advisory-first rollout should decide how the pilot, and
ordinary delivery after it, will make that distinction observable —
for example by requiring the merging identity's review to be a recorded
GitHub PR review rather than prose in the PR the same identity wrote.

## Consequences

Easier: the next pilot attempt has a fixed cohort shape and success
criteria ready to execute the moment `Publisher` has a live attempt and
an independent-session reviewer, instead of re-deriving methodology
under pressure to show a result.

Harder: [#104](https://github.com/Sannrox/rusui/issues/104) (T1) and
every issue that transitively depends on P8 stay blocked. Per #103 and
#104's own dependency text, closing #103 as deferred does not satisfy a
downstream "accepted design or passing evidence outcome" dependency —
that is the intended effect, not a side effect to route around.

Irreversible: none. No schema, policy, or runtime change is implied.

## Rejected alternatives

- **Backfill `#147`–`#178` as the ten-task pilot cohort.** Rejected: none
  used the candidate/proof/publication path or the independent-session
  review ADR 0013 requires; treating them as pilot evidence would credit
  a different, unaudited process.
- **Declare pass from "32 PRs merged, all CI green, zero reported
  incidents."** Rejected: absence of a reported failure in a process
  with no independent review and no author/merger separation is an audit
  gap, not verified-delivery evidence.
- **Run the pilot immediately on `Publisher`, without an independent
  reviewer session.** Rejected: contradicts ADR 0013 D2 directly ("The
  author session cannot supply that review").
- **Leave #103 open and unresolved rather than recording a decision.**
  Rejected: the research route for this issue ends in a recorded pass,
  narrow, defer, or fail decision; an unresolved issue would keep
  re-litigating the same question without adding evidence.

## Validation and reversal

Validation is maintainer review of the audit above and the predeclared
methodology. Reverse by superseding this ADR once a live pilot with
independent-session review actually runs, recording its own pass,
narrow, or fail decision against the criteria predeclared here (or
revised criteria, recorded before that pilot's results are known).

## Sources

- [#100](https://github.com/Sannrox/rusui/issues/100),
  [#101](https://github.com/Sannrox/rusui/issues/101),
  [#102](https://github.com/Sannrox/rusui/issues/102) (P8 dependencies,
  closed completed)
- [#103](https://github.com/Sannrox/rusui/issues/103) (this decision)
- [ADR 0010](0010-hybrid-roadmap-sequence.md) D5 ("Humans merge core
  delivery"); [ADR 0013](0013-publication-authority.md) D1–D2 (object
  identities; independent-session review)
- [#177](https://github.com/Sannrox/rusui/pull/177) (`Publisher` merge,
  2026-09-21; its Agent Transcript is the cited self-declared-review
  example)
- GitHub API query against `Sannrox/rusui`, 2026-09-22: all 32 pull
  requests merged since #87 (`#147`–`#178`) — author, merging identity,
  native review count, and transcript presence for each
