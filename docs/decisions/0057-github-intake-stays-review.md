# ADR 0057: GitHub intake stays issues, pull requests, and comments

- Status: Superseded by [ADR 0070](0070-session-owned-webhook.md)
  for the session-owned wake webhook. GitHub review intake is restated
  in 0070 D4.
- Date: 2026-10-06
- Amends: none. [ARCHITECTURE.md](../../ARCHITECTURE.md) GitHub intake
  (issues, pull requests, comments) stands until that file is rewritten.
- Resolves: [#488](https://github.com/Sannrox/rusui/issues/488)
- Related: [docs/github-app-pilot.md](../github-app-pilot.md),
  [#507](https://github.com/Sannrox/rusui/issues/507) (CI-event intake
  remains blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

GitHub intake is issues, pull requests, and comments. A CI failure is
not an item.

[#488](https://github.com/Sannrox/rusui/issues/488) asked whether a
`check_run` or `workflow_run` failure should start a run session. Two
options:

1. A `check_run` or `workflow_run` failure on a bound repository
   starts a run session whose prompt is the failed job.
2. Keep review intake only.

No measured CI-failure case is recorded. A new event type is a new
admit path: parse, delivery id, prompt, and a session that is not an
issue. Catch-up already admits missed issue and pull-request
deliveries. A CI event would need the same miss path.

Source evidence at the commit this decision was made against:

- `ParseWebhook` returns an item only from `issue` or `pull_request`.
  A `check_run` body has no item.
- The GitHub App pilot guide lists `issues`, `pull_request`, and
  `issue_comment`. It does not list `check_run` or `workflow_run`.

## Decision

**D1. Keep review intake only.** GitHub webhooks that start work are
issues, pull requests, and comments. A CI failure is not an item.

**D2. CI-event intake stays refused** until a superseding ADR names
the failed-job prompt, the session kind, and the miss path. [#507](https://github.com/Sannrox/rusui/issues/507)
remains blocked on that later choice.

## Consequences

- Easier: one ingest. Catch-up stays issue and pull-request.
- Harder: a CI failure waits for a human issue or a later ADR.
- Schema, policy, runner contract, and public API are unchanged.

## Rejected alternatives

- **Start a run from `check_run` / `workflow_run` (option 1).** Adds
  a second ingest without a measured miss that review items cannot
  cover.

## Validation and reversal

Accept on merge. Validated when `ParseWebhook` still errors on a
check_run body, and the pilot guide still omits CI events.

Reverse with a superseding ADR that names the CI event, the prompt,
and the catch-up path.

## Sources

- [#488](https://github.com/Sannrox/rusui/issues/488)
- `ParseWebhook`, `docs/github-app-pilot.md`
- Decided against `main` at `f7c2a77`.
