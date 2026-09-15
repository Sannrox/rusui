# ADR 0006: How review, run, and schedule sessions start

- Status: Accepted
- Date: 2026-09-15
- Amends: [ADR 0003](0003-operator-surface.md) (dogfood start API;
  `sync` is not required for P1 dogfood). [ADR 0005](0005-policy-v2-project.md)
  stands: session belongs to one project; review requires a bound repo.
- Resolves: [#45](https://github.com/Sannrox/rusui/issues/45)
- Discussion: none. Merging with this status is the acceptance act.

## Context

The plane already stores sessions and turns, but every review session
shares the immortal `local` environment, and the only start path is a
GitHub item. P1 dogfood needs `rusui run`, cron, and one container per
session, with duplicate deliveries still waking exactly one session.

[Find the way to P1 dogfood](https://github.com/Sannrox/rusui/issues/37)
locked three session kinds. This ADR is the start contract for those
kinds. Snapshot hash identity remains
[#42](https://github.com/Sannrox/rusui/issues/42). Credential brokering
remains [#44](https://github.com/Sannrox/rusui/issues/44).

## Decision

Object graph: `project → environment → session → turn`. **One session,
one environment.** Create session creates (or restores) one container.
Reviews do not share `local`. `local` is not a dogfood environment.

**Identity**

- **review:** one open session per `(project, bound repo, item)`.
  GitHub events that parse to a repo and item wake that session; a new
  item snapshot is a new **turn** (existing pending-revision rules).
  Duplicate `delivery_id` is a no-op. Payloads with no item (bare
  `push`) do not start a session.
- **run:** each unique create is a new session. HTTP create is
  idempotent on `Idempotency-Key`. Required inputs: project slug and
  prompt. No GitHub item. Workspace is the project's snapshot (#42).
- **scheduled:** each fire is a new session. `delivery_id` is
  schedule id plus the period bucket. A named **schedule** is an API
  object on the project (name, UTC cadence, prompt), not a
  `policy.yaml` field. Policy only allowlists the `scheduled` kind.
  The plane fenced scheduler fires it. Signed `POST /hooks/events`
  may fire one with project, schedule name, and `delivery_id` (no
  GitHub item). GitHub issue/PR events start **review**, not
  `scheduled`.

A scheduled session runs the schedule's stored prompt on a new
environment. It does not fan out review sessions over the backlog.

If a session from that schedule is still leased or running, **skip**
the fire. Duplicate `delivery_id` remains a no-op.

**Surfaces**

- Dogfood start API: `run`, `sessions`, `attach`, `logs`, `approve`.
  Create returns the session; `attach` streams it. A follow-up prompt
  is a new **turn** on the same session and environment, for every
  kind. For `review`, GitHub snapshot change and operator retry also
  open turns.
- `sync` / shadow refs are not required for P1 dogfood. ADR 0003's
  CLI list is not reopened; `sync` stays fog.
- Slack does not start sessions.
- No extra console view for schedules in P1 (ADR 0003's ten views
  stand). Schedules are CLI/API objects.

## Consequences

- Generic event ingest must accept project-scoped fires without
  `repo`/`item`.
- Claim looks up the session's **project** in current policy, not only
  a GitHub repo key.
- Two review items are two environments. Overlapping schedules do not
  stack containers.
- Catch-up remains the way to admit many GitHub items; a cron is not
  a backlog fan-out.

## Rejected alternatives

- **Immortal scheduled session, each fire a turn.** Fights 72h
  environment TTL.
- **New review session per GitHub delivery.** Breaks exactly-once and
  explodes environments.
- **Share `local` across reviews.** Contradicts one container per
  session.
- **Cron in `policy.yaml`.** Would amend ADR 0005's field list.
- **No plane scheduler** (external events only). Drops the fenced
  scheduler.
- **Always start overlapping fires.** Parallel containers blow the
  budget.
- **Block dogfood on `rusui sync`.** The gate is unattended sessions,
  not a local worktree.

## Validation and reversal

Accept on merge. Reopen if a review session must span two GitHub
items, or if a scheduled fire must open review sessions.

## Sources

- [#45](https://github.com/Sannrox/rusui/issues/45)
- [ADR 0003](0003-operator-surface.md), [ADR 0005](0005-policy-v2-project.md)
- `ensureReviewSessionTx` / `DefaultEnvironmentID` on `origin/main`
- `gh.ParseWebhook` (item required)
