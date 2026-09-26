# ADR 0021: First GitLab intake is GitLab.com project issues only

- Status: Accepted
- Date: 2026-09-25
- Accepted by: Sannrox, repository maintainer (2026-09-25)
- Resolves: [Issue #247](https://github.com/Sannrox/rusui/issues/247)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (current product contract)
- Base: `85a487b6bf71b246775fa34de145d81238e1ff21`

## Context

Rusui's first-party intake is GitHub-specific. `POST /hooks/github` verifies
GitHub's raw-body HMAC, parses `repository.full_name` plus issue or pull
request number, then queues an authoritative refresh. Delivery recovery polls
GitHub's hook-delivery API when `RUSUI_GITHUB_HOOK_IDS` is configured, and
periodic catch-up lists open items plus locally tracked non-terminal items
([`internal/server/server.go`](../../internal/server/server.go),
[`internal/gh/github.go`](../../internal/gh/github.go),
[`internal/engine/recover.go`](../../internal/engine/recover.go)). The signed
generic event route accepts a rusui-shaped event; it does not define a GitLab
adapter or provider-specific policy identity ([`internal/server/server.go`](../../internal/server/server.go),
[`internal/engine/engine.go`](../../internal/engine/engine.go)).

Policy binds `owner/repo` strings and review budgets by repo. Snapshots and
jobs likewise use `(repo, item)`. Event rows have a `source`, but
`deliveries.delivery_id` and `events.delivery_id` are globally unique strings;
delivery rows do not retain a provider. Worker receipts are keyed by job,
lease generation, and revision, with an opaque kind and payload. Those
identities do not yet preserve which forge and instance supplied an item
([`internal/policy/policy.go`](../../internal/policy/policy.go),
[`internal/store/migrate.go`](../../internal/store/migrate.go),
[`internal/store/store.go`](../../internal/store/store.go)).

GitLab project webhooks can emit issue events on GitLab.com and Self-Managed.
The current webhook reference documents a signing token with Standard
Webhooks-style `webhook-id`, `webhook-timestamp`, and `webhook-signature`
headers; signing tokens became generally available in GitLab 19.1. GitLab
also documents the older `X-Gitlab-Token` as a plain-text header and recommends
signing tokens for new hooks. [GitLab Webhooks (accessed 2026-09-25)](https://docs.gitlab.com/user/project/integrations/webhooks/)

Project issue hooks expose `open`, `update`, `close`, and `reopen` actions.
The payload includes the project ID and path, issue IID, action, and current
state. The Issues API can list and retrieve project issues and filter by
`state`, `updated_after`, and confidentiality. GitLab's role matrix gives
Guest permission to view ordinary issues; a Guest cannot read confidential
issues unless they authored or are assigned to them. [GitLab Webhook Events
(accessed 2026-09-25)](https://docs.gitlab.com/user/project/integrations/webhook_events/),
[GitLab Issues API (accessed 2026-09-25)](https://docs.gitlab.com/api/issues/),
[GitLab Roles and Permissions (accessed 2026-09-25)](https://docs.gitlab.com/user/permissions/),
[GitLab Confidential Issues (accessed 2026-09-25)](https://docs.gitlab.com/user/project/issues/confidential_issues/)

GitLab's `read_api` access-token scope grants read-only API access within the
token's reach. A project access token is project-scoped, but GitLab.com
requires Premium or Ultimate for project access tokens; on GitLab Self-Managed
they are available on any license. A personal token can reach all projects
available to its user, so a dedicated account with access only to bound
projects is the narrow fallback for GitLab.com Free. [GitLab Access Token Scopes (accessed 2026-09-25)](https://docs.gitlab.com/security/tokens/access_token_scopes/),
[GitLab Project Access Tokens (accessed 2026-09-25)](https://docs.gitlab.com/user/project/settings/project_access_tokens/)

GitLab keeps recent webhook requests for two days in the UI. Its project-webhook
events API lists events over a seven-day window and provides a separate POST
resend operation. Resending is operator-initiated; the delivery docs say to
handle duplicates and identify retries with the same `webhook-id` (equal to
`Idempotency-Key`). GitLab may temporarily disable a hook after consecutive
failures. [GitLab Webhooks (accessed 2026-09-25)](https://docs.gitlab.com/user/project/integrations/webhooks/),
[GitLab Project Webhooks API (accessed 2026-09-25)](https://docs.gitlab.com/api/project_webhooks/)

Slack is optional for intake but is the current human command surface.
`status`, `pause`, `resume`, `sweep`, `retry`, and `reload` are implemented as
Slack commands. The `rusui` CLI has session, diagnostics, review, run, and
other commands, but no equivalents for that maintenance set
([`docs/operator.md`](../operator.md), [`cmd/rusui/main.go`](../../cmd/rusui/main.go)).

## Decision

**Decision: NARROW.** The first GitLab intake profile is issues from a bound
GitLab.com project. `ARCHITECTURE.md` remains the product contract until a
follow-up changes it.

The first workflow is: a maintainer configures one project webhook for ordinary
issue events and leaves confidential-issue events disabled; an open, edit,
close, or reopen delivery schedules a read-only refresh; Rusui fetches the
issue's current canonical state and applies the existing policy, snapshot,
revision, and claim rules. Project webhook setup requires a Maintainer or
Owner; GitLab documents those four issue actions and the issue payload fields.
The intake identity needs only Guest issue-read permission for non-confidential
issues, while token creation and webhook setup remain maintainer operations.
[GitLab Webhooks (accessed 2026-09-25)](https://docs.gitlab.com/user/project/integrations/webhooks/),
[GitLab Webhook Events (accessed 2026-09-25)](https://docs.gitlab.com/user/project/integrations/webhook_events/),
[GitLab Roles and Permissions (accessed 2026-09-25)](https://docs.gitlab.com/user/permissions/)

The event body is a wake-up signal, not the review snapshot. No merge requests,
group or instance hooks, other GitLab hosts, comments, confidential-issue
events, or live GitLab writes are part of this profile.

Any implementation of that profile should meet these boundaries:

1. **Verify and acknowledge safely.** Require GitLab's signing token and
   verify the HMAC over the exact `{webhook-id}.{webhook-timestamp}.{raw body}`
   bytes with constant-time comparison. Reject stale timestamps, as GitLab
   recommends for replay protection. Require the
   configured `https://gitlab.com` origin and an expected project binding;
   do not trust payload paths or `X-Gitlab-Instance` as configuration. Require
   the signed headers documented as generally available in GitLab 19.1, with
   no plaintext-token fallback. Persist a recognized delivery before returning
   2xx, and return 2xx for an already-persisted delivery. GitLab documents the
   signing construction and recommends quick successful responses.
   [GitLab Webhooks (accessed 2026-09-25)](https://docs.gitlab.com/user/project/integrations/webhooks/)

2. **Use a read-only item credential.** Fetch and reconcile issues through the
   GitLab Issues API with `read_api`; do not grant `api` or send this token to
   the guest. Set the identity to Guest for ordinary, non-confidential issues
   and request `confidential=false`; the webhook must leave confidential-issue
   events disabled. Prefer a project access token where the GitLab.com
   subscription permits one, with token role Guest. Otherwise use a dedicated
   service account with Guest membership restricted to the bound project(s).
   The maintainer creates the token and configures the webhook; Rusui does not
   need GitLab write authority to receive or refresh issues.
   [GitLab Access Token Scopes (accessed 2026-09-25)](https://docs.gitlab.com/security/tokens/access_token_scopes/),
   [GitLab Project Access Tokens (accessed 2026-09-25)](https://docs.gitlab.com/user/project/settings/project_access_tokens/),
   [GitLab Project Access Tokens API (accessed 2026-09-25)](https://docs.gitlab.com/api/project_access_tokens/)

3. **Make source identity explicit.** Resolve a configured source as a typed
   provider plus origin and GitLab project identity, with the path as its
   human-readable locator; pair that with item kind `issue` and project IID.
   A raw `group/project` string must not alias a GitHub repository with the
   same path. Namespace delivery identity by provider, origin, and
   `webhook-id`; do not deduplicate on `X-Gitlab-Event-UUID`, which GitLab says
   recursive webhook deliveries may share. Preserve the provider-qualified
   source reference, external item identity, policy revision, and source
   snapshot hash in the immutable review input/result provenance. The event
   record should also retain the delivery ID and action. Model output alone is
   not source evidence. [GitLab Webhooks (accessed 2026-09-25)](https://docs.gitlab.com/user/project/integrations/webhooks/)

   The policy decision should record the provider-qualified bound source and
   policy revision used to admit or refuse it. The immutable receipt envelope
   should carry `provider`, `origin`, `project_id`, `project_path`,
   `item_kind`, `external_item_id`, `delivery_id`, `policy_revision_id`, and
   `snapshot_hash`; it must contain no GitLab credential. This evidence belongs
   to the plane's source record, not the guest's claimed result.

4. **Treat lifecycle events as refreshes.** Accept only the ordinary Issue
   Hook and supported `Issue` item type. Map `open`, `update`, `close`, and
   `reopen` to a refresh of the same provider-qualified issue; use the API
   response, not the webhook payload, for current state and content. In
   addition to open-item and locally tracked-item catch-up, periodically
   reconcile project issues with `state=all`, `confidential=false`, and
   `updated_after=<checkpoint>`. Persist the checkpoint only after every page
   succeeds, with an overlap window and idempotent refreshes to tolerate
   pagination and timestamp ties. This can discover the current state of an
   issue opened and closed while Rusui was down, even if Rusui never tracked
   it. GitLab's UI shows request history for two days, and its API lists
   webhook events within a seven-day window and exposes a manual resend
   endpoint; replay is not automatic. Keep the intake token read-only: an
   operator can use the UI or an appropriately privileged GitLab identity for
   exceptional event resend, but ordinary recovery relies on issue
   reconciliation and does not need webhook-management permissions. The
   project-webhooks API itself requires Maintainer or Owner access.
   [GitLab Webhooks (accessed 2026-09-25)](https://docs.gitlab.com/user/project/integrations/webhooks/),
   [GitLab Project Webhooks API (accessed 2026-09-25)](https://docs.gitlab.com/api/project_webhooks/),
   [GitLab Issues API (accessed 2026-09-25)](https://docs.gitlab.com/api/issues/)

5. **Do not claim CLI parity yet.** GitLab delivery and refresh should continue
   without Slack, but operators cannot perform the full existing maintenance
   loop through the CLI today. Add CLI equivalents for `status`, `pause`,
   `resume`, `sweep`, `retry`, and `reload` before presenting the GitLab
   profile as operable without Slack. Slack remains an optional command and
   notification surface after that parity work; it must not become a GitLab
   credential or intake dependency.

The bounded implementation follow-up is two slices: first provide CLI
equivalents for the existing maintenance commands; then support only one
GitLab.com project issue source with signed issue events, read-only `read_api`,
the typed identity and receipt envelope above, deduplicated durable intake,
and checkpointed current-state reconciliation. Manual resend is an
operator-only exception. The second slice does not introduce a
generic provider framework or support other GitLab hosts or item kinds.

## Consequences

The profile limits endpoint trust to GitLab.com and one project-issue API;
it avoids accepting arbitrary self-managed origins and their independently
configured versions. It also means a deployment on GitLab.com Free cannot use
a project access token, and a personal `read_api` token is broader than one
project unless its account's project membership is constrained. Guest is the
documented minimum role for ordinary issue visibility, and the project access
token API supports that role; implementation must still prove that a
Guest-scoped token can list and retrieve the selected project's
non-confidential issues through the API.

Provider-qualified identity requires a deliberate policy and persistence
change. Policy v2 rejects unknown fields, so this proposal does not suggest
silently overloading `repos:` or changing its meaning. The delivery dedupe key
must become provider-aware, and immutable source provenance must span policy,
snapshot, review result, and operator-visible receipt. These are schema and
API design changes, not just an alternate webhook parser.

The GitHub path and its recovery remain intact. Self-managed GitLab can be
revisited only with an explicit supported version floor, configured origin
allowlist, per-instance credentials, and tests for the same security and
recovery properties. Live comments, closes, and merges remain unauthorized.

## Rejected alternatives

- **GitLab Self-Managed as the first profile.** Its project-token availability
  is broader, but each operator chooses the host and version. Signing-token
  support and behavior need an explicit version floor and origin allowlist;
  adding those now broadens the first adapter's trust and operations surface.
- **All GitLab work items or merge requests.** The issue event shape already
  covers several work-item types, but rusui's current review identity is
  GitHub issue or pull request. Supporting additional kinds before a typed
  source boundary would conflate provider identity and existing item policy.
- **Use the GitLab webhook payload as the source snapshot.** It is useful for
  routing and event identity, but rusui's review revision must be built from
  a canonical read under current policy, with duplicates and missed delivery
  handled independently.
- **Reuse `owner/repo`, the GitHub webhook secret, and current global
  `delivery_id`.** Same-path repositories on two providers would collide;
  GitLab signs a different message and header format; and the current delivery
  row does not store its source provider.
- **Treat current CLI commands as enough.** Session inspection and diagnostics
  do not replace Slack's pause, sweep, retry, and policy-reload controls.

## Validation and reversal

Before accepting implementation, prove one GitLab.com project end to end with
captured official Issue Hook fixtures for each supported action; valid, altered,
stale, and wrongly bound signatures; replayed and colliding delivery IDs;
Guest-scoped `read_api` access to non-confidential issues and exclusion of
confidential items; project path changes; API rate-limit/error retry; and
downtime catch-up for open and already tracked closed/reopened issues. Also
prove that an untracked issue opened and closed during downtime is discovered
by the `updated_after` scan, and that a partial or failed paginated scan does
not advance its checkpoint. Verify that the immutable review input and receipt
identify the GitLab origin, project, IID, policy revision, and snapshot hash,
while the guest receives no GitLab token. Exercise status, pause, resume,
sweep, retry, and reload with Slack unavailable. Keep a live black-box
receiver/API test for the adapter; unit fixtures alone do not prove the route.

This proposal is reversible before implementation without changing the
contract. After implementation, disabling GitLab intake must leave stored
provider provenance and receipts readable; no migration may rewrite GitLab
identities as GitHub `owner/repo` values.

## Sources

- [Issue #247](https://github.com/Sannrox/rusui/issues/247)
- [GitLab Webhooks](https://docs.gitlab.com/user/project/integrations/webhooks/) — signature format, delivery headers, request history, retries, disabling, and duplicates (accessed 2026-09-25).
- [GitLab Webhook Events](https://docs.gitlab.com/user/project/integrations/webhook_events/) — issue actions and payload fields (accessed 2026-09-25).
- [GitLab Project Webhooks API](https://docs.gitlab.com/api/project_webhooks/) — event-history window, list, and resend API (accessed 2026-09-25).
- [GitLab Issues API](https://docs.gitlab.com/api/issues/) — project issue read and filter operations (accessed 2026-09-25).
- [GitLab Access Token Scopes](https://docs.gitlab.com/security/tokens/access_token_scopes/) — token reach and `read_api` scope (accessed 2026-09-25).
- [GitLab Project Access Tokens](https://docs.gitlab.com/user/project/settings/project_access_tokens/) — project scope and GitLab.com license availability (accessed 2026-09-25).
- [GitLab Project Access Tokens API](https://docs.gitlab.com/api/project_access_tokens/) — token role and scope fields (accessed 2026-09-25).
- [GitLab Roles and Permissions](https://docs.gitlab.com/user/permissions/) — Guest issue-read permissions (accessed 2026-09-25).
- [GitLab Confidential Issues](https://docs.gitlab.com/user/project/issues/confidential_issues/) — Guest access limits for confidential issues (accessed 2026-09-25).
- Local contract inspected at base `85a487b6bf71b246775fa34de145d81238e1ff21`: `ARCHITECTURE.md`, `internal/server/server.go`, `internal/gh/github.go`, `internal/engine/engine.go`, `internal/engine/recover.go`, `internal/policy/policy.go`, `internal/store/migrate.go`, `internal/store/store.go`, `docs/operator.md`, and `cmd/rusui/main.go`.
