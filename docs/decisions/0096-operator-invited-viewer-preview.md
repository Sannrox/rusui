# ADR 0096: Operator-invited viewer preview grants

- Status: Accepted
- Date: 2026-11-20
- Amends: [ADR 0012](0012-operator-access.md) D4 (a second preview
  grant class for an invited viewer).
- Supersedes: [ADR 0037](0037-shared-preview-deferred.md) (the deferral
  of a viewer who is not the operator). ADR 0037 D2 still describes
  the operator grant: forwarding that URL is not sharing.
- Resolves: [#782](https://github.com/Sannrox/rusui/issues/782)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (product contract;
  this ADR does not rewrite it),
  [ADR 0032](0032-named-services.md) (a grant names a declared service),
  [ADR 0039](0039-shared-operator-governance-deferred.md) (a viewer is
  not a second operator),
  [ADR 0077](0077-session-access-grants.md) (tenant session read/reopen
  is a different job),
  [#328](https://github.com/Sannrox/rusui/issues/328) (the deferred
  research),
  [#818](https://github.com/Sannrox/rusui/issues/818) (implementation).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act. Until merge this is a proposal, not
  implementation authority. This ADR does not mint grants.

## Context

[ADR 0037](0037-shared-preview-deferred.md) deferred a preview grant
for anyone who is not the operator. Its D3 gate required the maintainer
to name that audience and a superseding ADR that meets D4 and amends
ADR 0012. [#782](https://github.com/Sannrox/rusui/issues/782) names the
audience: a human the operator invites to view one service. A
screenshot or recording the operator makes does not serve a reviewer
who must click through a running app.

Source evidence at the commit this decision was made against
(`fefcfeae1f7f8c5eae013517ed5133f321bada18`):

- Operator mint is console `POST /console/sessions/{id}/preview` and
  CLI `POST /sessions/{id}/preview` (`internal/server/preview.go`
  `consoleMintPreview`, `mintPreview`). Both call `createPreviewGrant`.
  The guest cannot mint.
- The grant binds session, environment id, handle, port, optional
  service name, and a ten-minute `engine.GrantTTL`. The store keeps
  the token hash (`internal/store/preview.go`). There is no grant
  class column and no operator generation on the row.
- The preview proxy accepts `g` or a bearer header and refuses an
  operator cookie (`previewGrant`). It refuses an expired or revoked
  grant, a replaced environment, and an environment that is not ready.
- `store.RevokePreviewGrant` is used from tests and from environment
  archive (`RevokePreviewGrantsForEnvironment`). There is no operator
  list or revoke surface. Rotating `RUSUI_OPERATOR_TOKEN` does not end
  an outstanding grant before its TTL.
- An unexpired, unrevoked grant keeps its environment from idle sleep
  (`internal/store/environment.go`). A grant can also wake a sleeping
  environment (`previewProxy`).
- `POST /comment` on the preview origin turns into a session follow-up
  (`previewComment`). That is a plane write.
- Preview responses do not set `noindex` or a strict referrer policy.
- `policy.yaml` has no share field. Overlay still may only narrow.

ADR 0037 D1–D3 are the refused action this ADR replaces. D4 is the
constraint list this ADR adopts.

## Decision

**D1. Adopt a share grant for one invited viewer.** rusui may mint a
preview grant for a human the operator invites to view one declared
service. That is the only audience. The grant is a distinct class from
the operator cookie, the operator preview grant, the turn grant, and
the guest grant. Only an operator session mints it. The guest, worker,
and turn never do.

**D2. ADR 0037 D4 holds.** The share grant is:

- **Bounded.** Expiry is chosen from fixed options with a hard ceiling
  of 24 hours, and never outlives the session. It ends on revoke,
  environment replace, session cancel, and operator-token rotation.
- **Revocable.** The operator can list and revoke outstanding share
  grants. The row binds the operator generation that minted it, so
  rotation ends it before TTL.
- **Read only.** One declared service, hence one port. Same origin
  isolation as ADR 0012 D5. No terminal. No write to the plane,
  including `POST /comment`. Preview HTML still cannot call
  `/sessions/*`, `/jobs/*`, or `/approvals/*` with operator credentials.
- **Off by default.** A project enables it explicitly in `policy.yaml`.
  An overlay may only narrow it. Unknown fields fail closed.
- **Not crawlable.** The response carries `noindex` and a strict
  referrer policy. This is not a public, anonymous, or long-lived URL.
  Listen addresses are unchanged: the preview origin stays isolated
  and loopback-first (ADR 0012 D3). How the invitee reaches that
  origin stays operator infrastructure.
- **Visible.** The guest is not told that the viewer is not the
  operator. A live share grant may prevent idle sleep only while it is
  unexpired and unrevoked; it does not keep the environment awake past
  its own expiry.

**D3. The share grant names a declared service.** The operator picks
the service. Raw-port mint stays operator-grant-only (ADR 0032 D5).

**D4. Forwarding the operator grant is still not sharing.** The
operator preview grant stays the operator's. Sending its URL to
someone else remains outside the supported contract.

**D5. A viewer is not an operator.** This ADR does not add a second
operator, a user table, or shared-operator governance. ADR 0039 and
ADR 0083 stand. Tenant session grants (ADR 0077) stay session
read/reopen; they are not this preview class.

No implementation is authorized by this ADR beyond capturing the
decision. [#818](https://github.com/Sannrox/rusui/issues/818) mints
the class.

## Consequences

Easier: a reviewer can open one running service without the operator
token, on the existing preview origin, with a revoke path the operator
grant still lacks.

Harder: a second grant class, a policy flag, list/revoke, operator
generation on the row, crawl headers, and a sleep rule that must not
outlive the grant. Schema change is required before mint. Overlay
cannot turn sharing on.

Irreversible: none until the follow-up writes rows. After mint, rollback
keeps the class column and refuses new share mints; outstanding share
grants still expire, revoke, and die on generation bump.

[ARCHITECTURE.md](../../ARCHITECTURE.md) still lists unauthenticated
preview as deferred until that file is rewritten. This ADR is not that
public path.

## Threat examples

| Attempt | Result |
| --- | --- |
| Grant leaked in a transcript or pull request | Bearer capability until expiry. Hard ceiling, list, and revoke end it. Operator generation bump ends every outstanding share grant. |
| Share grant used to reach a plane or host port | Named declared service only. Proxy dials the stored port. Plane routes stay on the other origin. |
| Grant that outlives the session | Ends on session cancel, environment replace, grant TTL, revoke, and operator-token rotation. |
| Crawlable URL on a private repository's app | `noindex`, strict referrer policy, policy opt-in, no public listen. |
| Viewer posts `/comment` or types in the terminal | Refused. Share grant is read-only; no plane write, no terminal. |
| Guest mints a share grant | Refused. Operator session only. |
| Overlay enables sharing the baseline left off | Refused. Overlay may only narrow. |
| Invitee presents the operator cookie on the preview origin | Refused (ADR 0012 D5). |
| Lengthen the operator grant and forward it | Rejected (D4). |

## Rejected alternatives

- **Retain ADR 0037.** The audience is now named, and a screenshot does
  not let that reviewer click the running app. D3's first gate holds.
- **Forward the operator grant with a longer TTL.** Widens the
  operator's own grant, has no audience label, no list/revoke, and
  makes forwarding the feature.
- **Mint an operator cookie for the viewer.** Gives a stranger the
  console. Rejected by ADR 0012.
- **Unauthenticated crawlable URL.** #782 non-goal. No public,
  anonymous, or long-lived preview URL.
- **Reuse tenant session grants (ADR 0077).** Those are by-id session
  read/reopen across tenants, not an HTTP port on the preview origin.
- **A portal object or user table.** New concept. The job is a preview
  grant class beside the operator grant.

## Validation and reversal

Validation: this ADR is Accepted in the index; ADR 0037 is Superseded
by this record; ADR 0012 lists this amendment. No mint ships until a
follow-up issue implements D1–D5. Reverse by a superseding ADR that
restores the deferral or names a different audience; do not delete
this file.

## Sources

- [#782](https://github.com/Sannrox/rusui/issues/782)
- [#818](https://github.com/Sannrox/rusui/issues/818)
- [#328](https://github.com/Sannrox/rusui/issues/328)
- [ADR 0012](0012-operator-access.md),
  [ADR 0032](0032-named-services.md),
  [ADR 0037](0037-shared-preview-deferred.md),
  [ADR 0039](0039-shared-operator-governance-deferred.md),
  [ADR 0077](0077-session-access-grants.md)
- `internal/server/preview.go` `consoleMintPreview`, `mintPreview`,
  `createPreviewGrant`, `previewGrant`, `previewProxy`,
  `previewComment`; `internal/store/preview.go`;
  `internal/store/environment.go`; `internal/engine/engine.go`
  `GrantTTL`
- Commit `fefcfeae1f7f8c5eae013517ed5133f321bada18`
