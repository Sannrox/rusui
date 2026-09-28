# ADR 0038: Maintenance eligibility and separate action-promotion gates

- Status: Accepted
- Date: 2026-09-28
- Resolves: [#104](https://github.com/Sannrox/rusui/issues/104)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (Executable policy,
  Webhook intake and recovery, Apply, Review quality),
  [ADR 0005](0005-policy-v2-project.md) (policy v2),
  [ADR 0006](0006-session-start.md) (how review sessions start),
  [ADR 0010](0010-hybrid-roadmap-sequence.md) (M3 continuous
  maintenance), [ADR 0015](0015-agent-publication.md) (agent
  publication), [ADR 0023](0023-p8-pilot-narrow.md) (P8 narrow),
  [ADR 0030](0030-schedule-new-session.md) (schedules).
  Consumers: [#105](https://github.com/Sannrox/rusui/issues/105)
  (replay corpus), [#106](https://github.com/Sannrox/rusui/issues/106)
  (advisory review), [#107](https://github.com/Sannrox/rusui/issues/107)
  (comments), [#108](https://github.com/Sannrox/rusui/issues/108)
  (repair), [#109](https://github.com/Sannrox/rusui/issues/109)
  (notifications), [#110](https://github.com/Sannrox/rusui/issues/110)
  (thirty-day gate).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act. Does not rewrite
  [ARCHITECTURE.md](../../ARCHITECTURE.md); D8 lists the amendments.

## Context

Webhooks, catch-up, schedules, and `rusui run` start work. Nothing
decides which maintenance is useful, already in progress, protected, or
authorized for a live action. Today:

- `review: true` admits a review for every changed bound item. Admission
  coalesces per `(repo, item)` on the snapshot hash, so unchanged
  content creates no job.
- `comments` and `close` authorize simulation only. Apply writes an
  intended-action record after rechecking policy, pause, and freshness in
  one transaction. Land and merge are unauthorized.
- `implement: true` lets a pinned task publish its own pull request
  ([ADR 0015](0015-agent-publication.md)). Nothing selects tasks
  automatically.
- Capacity is `max_reviews_per_repo_per_utc_day` and the project's
  `max_concurrent_leases`. In the P8 run, catch-up with `review: true`
  queued about 40 review jobs ahead of the pilot and hit a model rate
  limit ([p8-pilot-results.md](../proofs/p8-pilot-results.md)).
- The fetched item snapshot carries state, labels, draft, head, base and
  default-branch SHAs, comment counts, and linked same-repo items. It
  does not carry the author's association or assignees.
- P8 was **narrow** ([ADR 0023](0023-p8-pilot-narrow.md)): three small
  accepted publications (docs and single-file test/ops edits), a missed
  deadline, and a follow-up that did not update its PR. The maintainer
  directed that this accepted narrow outcome satisfies the P8 dependency
  for this design. It does not satisfy any promotion below.

## Decision

### D1. Action classes are separate

| Class | Effect | This profile |
| --- | --- | --- |
| Advisory | A durable review result and inbox entry. No GitHub write. | On for eligible items |
| Comment | One live comment per result identity (#107) | Off until D6 gate |
| Repair | One pinned task, published by the plane (D4, #108) | Off until D6 gate |
| Close | Live issue close | Unauthorized; not promotable here |
| Merge / land | Merge a pull request | Unauthorized; not promotable here |

Each class has its own policy switch and its own gate. Enabling one
grants nothing to another: an accepted comment gate does not permit
repair, and neither permits close or merge. Close promotion keeps the
per-reason table in ARCHITECTURE "Review quality" and its own research
([#137](https://github.com/Sannrox/rusui/issues/137)); merge keeps
[#139](https://github.com/Sannrox/rusui/issues/139).

### D2. The first maintenance profile

One project, its bound repositories with `review: true`, one runner.

- **Events.** `pull_request` (opened, synchronize, reopened,
  ready_for_review) and `issues` (opened, edited, reopened) through the
  refresh coordinator. `issue_comment` refreshes the item; it never starts
  an action by itself. Named schedules and catch-up use the same path.
- **Eligible for advisory.** Open items on a bound repository. Pull
  requests must target the default branch and not be drafts.
- **Eligible for repair (when promoted).** Open issues in the one
  selected category (D7) that a trusted requester opted in (D3), are not
  protected, and have no existing effort (D4).
- **Out of scope.** Closed or merged items, other base branches, items
  on unbound repositories, and any item whose private context would reach
  a public output (`never_leak_private_to_public` stands).

### D3. Trust and protection come from GitHub permissions, not prose

- **Trusted requester.** Applying a label requires triage access, so the
  opt-in label `rusui:repair` is the trusted request for repair. The
  operator's own `rusui run`, schedule, and Slack commands are trusted
  through the existing admit path. Issue text, repository files, and
  comments are untrusted inputs. They may inform reasoning, never
  eligibility or authorization.
- **Protected work.** An item is protected when it is a draft, carries a
  protected label (default `rusui:hold` and `security`), or is assigned
  to a human. Protected items get advisory at most. A protected label on
  an item with a running action cancels the action at the next recheck.
- Assignees and author association are not in the snapshot today; D8
  adds them. Until then, "assigned to a human" is unknown and repair
  stays off.

### D4. Existing effort and freshness

- **Existing effort** blocks a new repair when any of these exist for
  the item: an open rusui task with its effort key; an open pull request
  that links the issue (`linked_same_repo_items` or a closing keyword);
  a branch named `*/<issue>` or `*/<issue>-*`. Advisory is not blocked;
  admission already coalesces it per item.
- **A repair's own effort is not competition.** When the plane starts a
  repair it records the effort key, task revision, claim branch, and,
  once opened, the pull request number. Later rechecks exclude exactly
  those identities. Any other task, branch, or pull request for the
  item is competing effort and refuses the push.
- **Freshness.** Admission uses only refresh-coordinator snapshots.
  A result is current only while the item hash equals the hash it was
  made from. A stale result stays in the inbox marked superseded; it is
  never published. Comment rechecks item hash, `main_sha`, policy
  revision, overlay, and protection in the transaction before the GitHub
  write, as apply does today.
- **Repair recheck needs plane-owned publication.** Under ADR 0015 the
  guest holds the operator's credential and publishes itself, so the
  plane cannot recheck between the agent's decision and the push. Repair
  therefore stays off until
  [ADR 0020](0020-turn-scoped-github-publication.md)'s plane-owned
  publication is implemented. The plane then runs the same recheck
  (hash, `main_sha`, policy, overlay, protection, existing effort) before
  it pushes or opens the pull request, and refuses on any change.

### D5. Capacity, cooldowns, and rechecks

- **Fair order.** Claim order per project: fresh webhook work before
  catch-up backlog, round-robin across repositories, oldest first within
  a repository. Catch-up may queue at most `max_concurrent_leases` × 2
  review jobs per repository per pass; the rest waits for the next pass.
- **Budgets.** The daily review cap and concurrent-lease meter stand.
  Repair counts against both and adds `max_repairs_per_repo_per_utc_day`
  (default 1). Exhaustion stays visible in the inbox; nothing is dropped
  silently.
- **Cooldowns.** An unchanged item gets no new job (existing rule). A
  declined repair candidate is not reconsidered for seven days unless its
  hash changes. A class stopped by a rollback trigger stays off until the
  operator edits `policy.yaml`.
- **Rechecks.** Policy and overlay are reread at admit, at claim, and in
  the transaction before any write. Overlay may only narrow.

### D6. Measured gates and rollback thresholds

Measurements come from the versioned replay corpus (#105) with a
held-out split, plus live advisory shadow results with maintainer
disposition. Model confidence is never a measure.

| Class | Promote only if | Roll back immediately when |
| --- | --- | --- |
| Advisory | on by default for the profile | harmful dispositions exceed useful over the trailing 20 |
| Comment | held-out ≥ 30 cases and ≥ 20 live shadow results; useful ≥ 2× harmful; false recommendations ≤ 10%; 0 private-context leaks; 0 duplicate comments in replay | any leak; any wrong action; any duplicate comment on one identity; false recommendations > 10% or harmful > half of useful over the trailing 20 published |
| Repair | D4 plane-owned publication exists; held-out eligibility: 0 protected or existing-effort selections and ≥ 9/10 judged suitable; then a predeclared 10-attempt cohort with ≥ 7 accepted, 0 SHA mismatches, 0 policy bypass | any wrong action; any duplicate effort; accepted < 5 of the trailing 10 |

**Notification burden** is measured across all classes: more than 10
actionable inbox items per repository per UTC day. Above it, routine
advisory completions move to a digest (#109). If the burden stays above
the limit with the digest on for three consecutive days, comment and
repair stop for that repository; advisory keeps running.

Definitions:

- **Useful / harmful**: the maintainer's recorded disposition of a
  result, with neutral as the third value.
- **False recommendation**: a finding the maintainer marks wrong.
- **Wrong action**: a live write on an ineligible, protected, stale, or
  out-of-scope item, or one outside the enabled class.
- **Duplicate work**: a second comment on one result identity, or a
  repair on an item that already had an effort under D4.

A gate that cannot be measured leaves the class advisory.

### D7. First repair category

Documentation corrections: an issue opted in with `rusui:repair` whose
accepted fix touches only declared documentation paths, run as a pinned
task with those paths. This is the class P8 accepted. Single-file
test or ops edits come next only through a separate gate. Enabling the
category still needs the D6 repair gate.

### D8. Configuration, feedback, and amendments

- **Versioned configuration.** The profile lives in `policy.yaml`, so
  each change is a new `policy_revision`. Proposed repository fields:
  `advisory` (default on when `review` is on), `comment` (live),
  `repair` (category name or off), `protected_labels`,
  `max_repairs_per_repo_per_utc_day`. `comments` and `close` keep their
  current simulation meaning.
- **Feedback.** The maintainer records useful, neutral, or harmful with
  a note on each result from the CLI or inbox. Feedback adds cases to
  the corpus development split. It never edits policy. Promotion is a
  maintainer edit to `policy.yaml` that cites the evaluation report
  revision.
- **Amendments required before any live class:**
  1. ARCHITECTURE gains a maintenance-profile section built from D1–D7.
  2. The policy schema gains the D8 fields; unknown fields still fail
     closed.
  3. The item snapshot gains author association and assignees.
  4. The inbox gains dispositions (#109).
  5. The claim order and catch-up cap in D5 replace plain queue order.
  6. Repair only: ADR 0020 plane-owned publication is implemented, with
     the D4 recheck before the push.

Implementation belongs to #105–#109. This ADR authorizes no live GitHub
write. Every class except advisory stays off until its gate is recorded.

## Consequences

Easier: each live action has one switch, one gate, and one rollback
rule; a catch-up flood cannot starve fresh work; the first repair class
matches what P8 showed works.

Harder: advisory must run long enough to collect 20 dispositions before
any comment goes live. Repair waits on snapshot fields, the corpus,
plane-owned publication (ADR 0020), and a new cohort.

Irreversible: none. Every new field defaults off except advisory.

## Rejected alternatives

- **A single `maintenance: safe` switch.** One flag would promote every
  class together, the opposite of D1.
- **Trust the issue author's text or a repository file for
  eligibility.** Anyone can write text; only triage access can label.
- **Promote on model confidence.** Uncalibrated; ARCHITECTURE already
  forbids it.
- **Start repair with Go fixes.** P8 missed a typed extraction on the
  12-minute deadline. Docs are the evidenced class.
- **Include close in this profile.** Close has its own per-reason gate
  and a revival contract (#137).

## Validation and reversal

Validation: #105 replays the corpus against D6 and shows a known
regression failing its gate; #106 runs advisory for the D2 profile with
no GitHub write; #110 measures the thirty-day run against these
thresholds. Reverse a class by setting its field off; revise the
thresholds with a superseding ADR.

## Sources

- [#104](https://github.com/Sannrox/rusui/issues/104), downstream
  [#105](https://github.com/Sannrox/rusui/issues/105)–[#110](https://github.com/Sannrox/rusui/issues/110)
- [ADR 0005](0005-policy-v2-project.md),
  [ADR 0015](0015-agent-publication.md),
  [ADR 0020](0020-turn-scoped-github-publication.md),
  [ADR 0023](0023-p8-pilot-narrow.md),
  [p8-pilot-results.md](../proofs/p8-pilot-results.md)
- `internal/policy/policy.go` (repository capabilities, budgets),
  `internal/snapshot` `Item`, ARCHITECTURE "Admission", "Apply",
  "Review quality"
