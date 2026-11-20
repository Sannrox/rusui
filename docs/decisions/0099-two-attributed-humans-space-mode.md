# ADR 0099: Two attributed humans on one space-mode session

- Status: Accepted
- Date: 2026-11-20
- Amends: [ADR 0039](0039-shared-operator-governance-deferred.md)
  (supersedes D1 "A Session is not jointly owned" and D4 "Session
  sharing does not exist" for space-mode attributed use; D1's
  shared-operator deferral, D2 Slack-is-not-an-operator, and D3's
  gate on a new credential class stand),
  [ADR 0083](0083-retain-single-operator-access.md) (supersedes D1
  as applied to space-mode read, prompt, and attach; D2 and D3 still
  gate a new plane-operator class),
  [ADR 0012](0012-operator-access.md) D4 (a granted attributed actor
  may read, prompt, and attach a space-mode session; the write lease
  stays exclusive),
  [ADR 0077](0077-session-access-grants.md) (a live space-mode grant
  includes prompt and attach, not only GET and reopen).
- Resolves: [#786](https://github.com/Sannrox/rusui/issues/786)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (product contract;
  this ADR does not rewrite it),
  [ADR 0065](0065-one-environment-terminal.md) (one terminal, one
  writer),
  [ADR 0078](0078-session-execution-modes.md) (user mode stays with
  `act`; space mode is the shared runtime),
  [ADR 0088](0088-issuer-vouched-subjects.md) (issuer-vouched subjects
  are the attributed identity),
  [#128](https://github.com/Sannrox/rusui/issues/128) (shared-operator
  implementation stays unauthorized),
  [#578](https://github.com/Sannrox/rusui/issues/578) (named the
  second actor and retained token sharing),
  [#830](https://github.com/Sannrox/rusui/issues/830) (implementation).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act. Until merge this is a proposal, not
  implementation authority. This ADR does not add a second operator
  token, mint grants, or change attach handlers.

## Context

[ADR 0039](0039-shared-operator-governance-deferred.md) deferred
shared-operator governance. [ADR 0083](0083-retain-single-operator-access.md)
retained that two people who want the same session share
`RUSUI_OPERATOR_TOKEN`. [#128](https://github.com/Sannrox/rusui/issues/128)
stayed closed. [#578](https://github.com/Sannrox/rusui/issues/578)
named the second actor and closed.

That retain is now the observed authorization need ADR 0039 D3 gate 1
asked for: two humans must use one environment as attributed actors
(read, prompt, attach) without copying the installing operator's
secret. Rotation of a shared token revokes both; receipts cannot name
who prompted or stole the terminal.

The objects this choice binds already exist:

| Object | Owner | What it already does |
| --- | --- | --- |
| Operator token | plane | one installing operator; policy and setup |
| Vouched subject | configured issuer | tenant-scoped attributed operator calls ([ADR 0088](0088-issuer-vouched-subjects.md)) |
| Tenant / actor token | plane | same operator class, one tenant ([ADR 0078](0078-session-execution-modes.md) D3) |
| Session grant | owning tenant | GET and reopen by id; not list, not follow-up ([ADR 0077](0077-session-access-grants.md)) |
| Execution mode | session, immutable | user: creator only; space: tenant runtime ([ADR 0078](0078-session-execution-modes.md)) |
| Terminal write lease | environment | one writer; second acquire refused while held ([ADR 0012](0012-operator-access.md) D4, [ADR 0065](0065-one-environment-terminal.md) D2) |

Source evidence at the commit this decision was made against
(`81c2ab461f884447057360f4ffe6001cf108b84d`):

- `sessionAllowed` lets the owning tenant, or a live grant on a
  space-mode session, GET the session. User-mode stays with `act`
  (`internal/server/access.go`). `visibleOwnedSession` is owning
  tenant only.
- Follow-up prompt (`followUpTurn`, console prompt) and console
  terminal (`termSessionEnv`) use `visibleOwnedSession`. A granted
  foreign tenant receives not-found on `POST /sessions/{id}/turns`
  (`internal/server/tenant_isolation_test.go`).
- Grant administration, cancel, archive, children, and preview mint
  stay owner-only (`session_grants.go`, `sessions.go`, `preview.go`).
- `ClassifyBearer` still maps one `RUSUI_OPERATOR_TOKEN`. A second
  operator secret is not a credential class
  (`internal/server/access.go`). Guests never receive it.
- Terminal write is one `terminal_leases` row per environment
  (`internal/store/terminal.go`). A second acquire while unexpired
  returns `write lease held` (`internal/server/console_term.go`).
  Observer attach is allowed. Local attach steal is a distinct
  generation with a `steal` receipt
  (`internal/store/runtime.go` `BeginSumikaAttach`).
- Identity for attributed calls is issuer-vouched or
  `RUSUI_TENANT_TOKENS`; tenant and actor come from configuration,
  not a request field (`lookupPrincipal`, ADR 0088 D2).

Same-tenant actors already share space-mode GET, prompt, and
terminal at tenant scope. The unsupported path is a second human
who must not hold `RUSUI_OPERATOR_TOKEN`, including a granted
foreign tenant, and whose prompt and attach must be attributed.

## Decision

**D1. Adopt attributed space-mode sharing. Do not share the
operator token.** Two humans may use one space-mode session as
attributed actors. Copying `RUSUI_OPERATOR_TOKEN` stays outside the
supported contract. This ADR does not add a second long-lived
operator secret, a user table, or a role object.

**D2. Who the second actor is.** The second actor is an
issuer-vouched subject ([ADR 0088](0088-issuer-vouched-subjects.md))
or an actor of a tenant that holds a live session grant
([ADR 0077](0077-session-access-grants.md)). Identity stays
issuer-vouched or plane-configured. No guest-minted invites. No
caller-supplied member header ([ADR 0059](0059-unattributed-operator-retained.md)).
Slack allowlisted users stay notify and approve, not plane operators
([ADR 0039](0039-shared-operator-governance-deferred.md) D2).

**D3. What they may do.** On a space-mode session they are granted
(owning tenant, or a live grant to their tenant) they may:

- read logs, transcript, and session GET;
- prompt (follow-up, including steer and queued);
- attach the environment terminal as observer, or take the write
  lease when it is free.

User-mode sessions stay with their creating `act`
([ADR 0078](0078-session-execution-modes.md)). A grant still does
not list the session in another tenant's catalog. Cancel, archive,
children, workspace writes, webhook mint, grant administration, and
approval decide stay with the owning tenant as ADR 0077 already
states, except follow-up, which this ADR moves onto the grant for
space mode only. Policy edit and `rusui setup` remain the
installing operator.

**D4. One writer on the terminal.** The environment still has one
write lease ([ADR 0012](0012-operator-access.md) D4,
[ADR 0065](0065-one-environment-terminal.md) D2). A second writer is
refused while the lease is held. Steal, explicit revoke, TTL, and
observer attach stay the existing paths. Two writers on one
terminal remain a refused action.

**D5. Attribution, not a new class.** Prompt, attach, steal, and
revoke receipts name the actor id already used on space-mode
approvals (ADR 0088 D3/D5). They do not name the raw subject or the
token. The actor does not inherit another actor's cookie, write
lease, or preview grants (ADR 0039 D4's per-identity binding still
holds). Browser console sign-in stays the static operator token
([ADR 0074](0074-single-operator-oidc-cli.md)); the second actor's
supported path is the operator-class API and CLI.

**D6. Full shared-operator governance stays deferred.** ADR 0039 D1
(no team-role or external-authority object), D2 (Slack is not an
operator), and D3 gate 2 (a new credential class amends ADR 0012)
stand. This ADR does not open hosted multi-tenant SaaS. Guests
never receive a core credential.

**D7. This ADR is the decision, not the mint.** No handler, schema,
or policy field changes here. The follow-up implements D3 for the
grant path (prompt and terminal attach today are owner-only) and
attributes those receipts.

## Consequences

Easier: two attributed humans share one space-mode environment
without copying the installing operator secret. Revoking one actor
is a subject-map edit or a grant revoke, not rotating the plane
token. Receipts can name who prompted or attached.

Harder: grant rights grow from GET/reopen to prompt and attach on
space mode. Follow-up tests that today expect not-found for a
grantee must flip. Terminal steal across two attributed actors
must keep one writer. Console remains the installing operator.

Irreversible: none until the follow-up writes grant checks and
receipt actors. Schema, policy, runner contract, and public routes
are unchanged by this record. Trust model: a granted attributed
actor is still the operator class, scoped to one tenant, not a
fourth credential class.

[ARCHITECTURE.md](../../ARCHITECTURE.md) still describes one
operator token. This ADR is not that rewrite.

## Threat examples

| Attempt | Result |
| --- | --- |
| Two humans hold `RUSUI_OPERATOR_TOKEN` | Unsupported. D1. |
| A second static operator secret | Refused. D1, D6. Not a credential class. |
| Guest mints an invite or receives a core credential | Refused. D2, D6. |
| Slack user treated as the second actor | Refused. D2; ADR 0039 D2. |
| Two writers on one terminal | Refused. D4. Observer attach is allowed. |
| Prompt or attach a user-mode session of another `act` | Refused. D3; ADR 0078. |
| Grant includes list, cancel, archive, or grant admin | Refused. D3. |
| Caller-supplied member header as identity | Refused. D2; ADR 0059. |
| Second actor inherits the first cookie or write lease | Refused. D5. Own credential, own lease acquire. |
| Policy edit or setup by the granted actor | Refused. D3. Installing operator only. |
| Hosted multi-tenant SaaS | Out of scope. D6. |

## Rejected alternatives

- **Retain ADR 0039 / ADR 0083 (keep sharing the operator token).**
  Dated reason this ADR does not take it: 2026-11-20, the second
  actor and the need the single token cannot meet are named (two
  attributed humans on one space-mode session; per-actor revoke and
  named receipts). Token sharing has no per-actor revoke.
- **A second `RUSUI_OPERATOR_TOKEN` or a rusui user table.** Makes
  the plane an account store. ADR 0083 D3 already fixed the shape:
  a verified assertion, not a local account.
- **Treat ADR 0096 share grants as the second human.** That grant
  is a non-operator viewer of one declared service. It is not
  prompt or terminal attach.
- **Two writers, or a shared write lease.** Issue non-goal. Breaks
  ADR 0012 D4 and ADR 0065 D2.
- **Guest-minted invites.** Agent output cannot grant authority
  (ADR 0039 D4).
- **Open console SSO for vouched subjects.** ADR 0074 keeps
  browser sign-in on the static token. Out of this research.
- **Move cancel, archive, policy, or setup onto the grant.** Those
  are owning-tenant / installing-operator rights. Sharing a session
  is not transferring the plane.

## Validation and reversal

Validation: this ADR is Accepted in the index; ADR 0039, ADR 0083,
ADR 0012, and ADR 0077 list this amendment; `ClassifyBearer` still
has one operator token; no second operator secret exists; user-mode
GET still denies another `act`. Prompt and terminal attach by a
granted foreign tenant stay not-found until the follow-up lands.

Reverse by a superseding ADR that restores token-only session use
or names a different actor; do not delete this file. Outstanding
grants keep their GET/reopen meaning if prompt/attach is later
removed.

## Sources

- [#786](https://github.com/Sannrox/rusui/issues/786)
- [#128](https://github.com/Sannrox/rusui/issues/128),
  [#578](https://github.com/Sannrox/rusui/issues/578)
- [ADR 0012](0012-operator-access.md),
  [ADR 0039](0039-shared-operator-governance-deferred.md),
  [ADR 0065](0065-one-environment-terminal.md),
  [ADR 0077](0077-session-access-grants.md),
  [ADR 0078](0078-session-execution-modes.md),
  [ADR 0083](0083-retain-single-operator-access.md),
  [ADR 0088](0088-issuer-vouched-subjects.md)
- `internal/server/access.go` `sessionAllowed`, `visibleOwnedSession`,
  `ClassifyBearer`, `lookupPrincipal`;
  `internal/server/sessions.go` `followUpTurn`;
  `internal/server/console_term.go` `termSessionEnv`,
  `consoleTermLease`;
  `internal/store/session_grant.go` `SessionGranted`;
  `internal/store/terminal.go` `PutTerminalLease`;
  `internal/store/runtime.go` `BeginSumikaAttach`
- Commit `81c2ab461f884447057360f4ffe6001cf108b84d`
