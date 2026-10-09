# ADR 0039: Shared-operator governance is deferred

- Status: Accepted; amended by 0059, 0074
- Date: 2026-09-29
- Amends: none. At acceptance, [ADR 0012](0012-operator-access.md) stood unchanged.
- Amended by: [ADR 0059](0059-unattributed-operator-retained.md),
  [ADR 0074](0074-single-operator-oidc-cli.md) (OIDC CLI credential form only).
  Operator calls stay unattributed. A member id is not identity.
- Resolves: [#127](https://github.com/Sannrox/rusui/issues/127)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0003](0003-operator-surface.md) (Slack is notify and approve;
  OIDC is later),
  [ADR 0005](0005-policy-v2-project.md) (policy is keyed by project),
  [ADR 0012](0012-operator-access.md) (one operator token, no team
  roles),
  [ADR 0033](0033-third-party-identity-deferred.md) (no second identity
  class in the guest),
  [ADR 0037](0037-shared-preview-deferred.md) (no viewer who is not the
  operator),
  [#128](https://github.com/Sannrox/rusui/issues/128) (implementation of
  a shared-operator profile stays unauthorized).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

- Amendment on acceptance: [ADR 0074](0074-single-operator-oidc-cli.md)
  permits optional OIDC CLI credentials for the same single operator.
  The token-only clauses named in ADR 0074 are superseded on acceptance;
  their text below remains historical rationale. Browser access,
  shared-operator deferral, and unattributed receipts remain unchanged.

## Context

[#127](https://github.com/Sannrox/rusui/issues/127) asked whether local
operator authentication and project policy should grow a
multi-operator governance boundary: identity mapping, revocation,
approvals, and behavior when an external authority is unavailable. Its
observable outcome is a reviewed design that selects one
shared-operation scenario, **or retains single-operator local policy as
sufficient**. Its adoption gate requires multiple operators or an
explicit governance requirement that creates an observed authorization
need. Neither is named.

The objects this choice binds:

| Object | Owner | Properties |
| --- | --- | --- |
| Operator | plane | one human; authenticates with `RUSUI_OPERATOR_TOKEN` |
| Policy | plane | keyed by project, not by operator |
| Session | plane | belongs to one project and one environment; not shared across operators |
| Approval | operator token or Slack allowlisted user | Slack is notify and approve, not a second plane operator |
| Receipt | plane store | attributable to session, turn, and action; not to an operator role |

Links: the plane has one Operator; a Session uses one Environment on
one project; an Approval records a decision against an intended action;
a Receipt does not mint authority.

Source evidence at the commit this decision was made against:

- `Server.OperatorTok` is one string
  (`internal/server/server.go`). Console sign-in compares the posted
  token to that string (`internal/server/console.go`
  `subtle.ConstantTimeCompare`). Cookie and CSRF MACs are keyed by the
  same secret. There is no operators table and no operator id on
  sessions, environments, or `approval_decisions`
  (`internal/store/migrate.go`).
- `ClassifyBearer` and `TestOperatorBrowserRejectsWorkerSecret`
  (`internal/server/access_test.go`) separate operator, worker, and
  none. Identical operator and worker secrets fail closed. A second
  operator secret is not a credential class.
- `rusui setup` generates one `RUSUI_OPERATOR_TOKEN`
  (`internal/setup/setup.go` `generatedSecrets`).
- Slack inbound auth is HMAC plus `RUSUI_SLACK_USERS`
  (`internal/server/server.go` `slackHook`). An unknown Slack user
  receives 403. Allowlisted users may `pause`, `resume`, `retry`, and
  the other slash commands. That is not console, terminal, preview, or
  policy-edit authority, and it is not an operator record in the store.
- [ADR 0012](0012-operator-access.md) D2 forbids OIDC and multi-user
  roles. D4 is a single operator with no team roles. [ADR 0003](0003-operator-surface.md)
  keeps Slack as notify and approve and names OIDC as later.

No second human holds plane authorization. No mandatory external
authority is named. Dogfood is one maintainer on one host.

## Decision

**D1. Defer. Retain single-operator local policy.** rusui does not add
a shared-operator, team-role, or external-authority object in the 1.0
core. Plane authorization remains one operator. On acceptance of
[ADR 0074](0074-single-operator-oidc-cli.md), operator API calls may
present the static token or a validated OIDC access token for the one
configured issuer/subject pair. The original static-token-only rule
is superseded for those calls.
Policy remains project-keyed ([ADR 0005](0005-policy-v2-project.md)).
A Session is not jointly owned.

**D2. Slack allowlist is not a second operator.** `RUSUI_SLACK_USERS`
gates inbound slash commands. It does not mint a console session, a
terminal write lease, a preview grant, or a policy overlay. Treating an
allowlisted Slack user as a plane operator is a refused action.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names the second actor (or the external authority)
   that must share control, the project scope they would hold, and the
   authorization need that the single operator token cannot meet; and
2. a superseding ADR amends ADR 0012 with that actor as a distinct
   credential class, including revocation and unavailable-authority
   behavior.

**D4. Constraints any adopted design must meet.** These are recorded so
a later ADR starts from them, not so this one implements them.
Identity, authorization, budget, approval, and audit stay separate
responsibilities:

- **Identity** is who presented a credential. It is not authorization.
- **Authorization** is which plane actions that identity may take, on
  which projects. An overlay may only narrow.
- **Budget** stays on the project ([ADR 0005](0005-policy-v2-project.md)).
  Sharing an operator does not create a second budget domain.
- **Approval** names the deciding identity on the receipt. Agent output
  cannot grant its own authority.
- **Audit** records identity, action, project, session, and time. It
  does not store the secret.
- **Session sharing** does not exist. A second operator on an adopted
  design receives their own authenticated view of the same session id,
  or is refused. They do not inherit the first operator's cookie,
  terminal write lease, or preview grants.
- **Revocation** ends that actor's outstanding cookies, leases, and
  grants the way ADR 0012 D2 ends them on operator-token rotation.
- **Unavailable authority** fails closed: when the required actor or
  external issuer cannot be reached, the plane does not fall back to a
  weaker class and does not auto-approve.
- **Local-only compatibility** remains: loopback HTTP, tailnet, or SSH
  local-forward as today. An adopted design must not require a hosted
  IdP for the single-operator path.

No implementation follow-up is authorized. This ADR does not change
`policy.yaml`, the store schema, Slack, or ADR 0012.

## Consequences

Easier: one operator secret, one policy key, one session owner. The
access, Slack, and persistence contracts stay unchanged.

Harder: a second human cannot sign in, hold a terminal lease, or act as
an external approver of plane authority. They can be on the Slack
allowlist for notify and approve, or they work as the same operator by
holding the same token, which is outside the supported contract.

Irreversible: none. No schema, policy field, or credential class is
added.

## Threat examples

The deferral removes each of these by construction. An adopting ADR
must answer each one.

- **Slack user treated as operator.** An allowlisted user pauses a
  project or retries a job and is then assumed to hold console and
  terminal rights. D2 forbids that promotion.
- **Shared operator token.** Two humans hold `RUSUI_OPERATOR_TOKEN`.
  Rotation revokes both; there is no per-actor revoke. D3 requires a
  distinct credential class before a second actor is supported.
- **External issuer outage becomes auto-approve.** A required IdP or
  second approver is unreachable, so the plane continues with the local
  token. D4 fails closed.
- **Session jointly owned.** A second actor inherits the first
  operator's cookie or write lease. D4 gives them their own view or
  refuses; leases and grants stay bound to the minting identity.
- **Agent output creates an operator.** Model text or a guest action
  adds a user to an allowlist or mints a token. D4: agent output cannot
  grant authority.
- **Audit row without identity.** A shared role string on a receipt
  cannot revoke one actor. D4 records the deciding identity.

## Rejected alternatives

- **Select a shared-operation scenario now.** No second actor or
  mandatory governance requirement is named. Choosing mapping,
  revocation, and unavailable-authority behavior without that need
  would fix a contract with no evidence.
- **Reject shared operators outright.** A second human on the same
  plane is a plausible later audience. Deferral keeps it open behind
  D3.
- **Treat `RUSUI_SLACK_USERS` as the operator set.** Mixes notify and
  approve with console, terminal, and policy authority. Slack is not
  reachable on loopback without operator infrastructure
  ([ARCHITECTURE.md](../../ARCHITECTURE.md) tunnel).
- **OIDC or team roles now.** Rejected by ADR 0012 D2 as out of #111
  non-goals. The gate on this issue has not appeared since.

## Validation and reversal

Validation: this ADR is Accepted in the index; `Server.OperatorTok`
remains one string; no operators table exists; Slack allowlist still
gates slash commands only. Reverse by a superseding ADR that meets D3
and D4 and amends ADR 0012 in the same change.

## Sources

- [#127](https://github.com/Sannrox/rusui/issues/127)
- [#111](https://github.com/Sannrox/rusui/issues/111),
  [#95](https://github.com/Sannrox/rusui/issues/95),
  [#128](https://github.com/Sannrox/rusui/issues/128)
- [ADR 0003](0003-operator-surface.md),
  [ADR 0005](0005-policy-v2-project.md),
  [ADR 0012](0012-operator-access.md),
  [ADR 0037](0037-shared-preview-deferred.md)
- `internal/server/server.go` `OperatorTok`, `slackHook`;
  `internal/server/console.go` `consoleEnabled`, `consoleSignIn`;
  `internal/server/access_test.go` `ClassifyBearer`;
  `internal/setup/setup.go` `generatedSecrets`;
  `internal/store/migrate.go` `approval_decisions`
