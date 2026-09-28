# ADR 0037: Sharing a preview without operator authentication is deferred

- Status: Accepted
- Date: 2026-09-28
- Amends: none. [ADR 0012](0012-operator-access.md) stands unchanged.
- Resolves: [#328](https://github.com/Sannrox/rusui/issues/328)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0012](0012-operator-access.md) D4–D6 (single operator, preview
  grant, origin isolation, no `allow-always`),
  [ADR 0032](0032-named-services.md) (a grant may name a declared
  service), [#115](https://github.com/Sannrox/rusui/issues/115)
  (authenticated preview mint),
  [#127](https://github.com/Sannrox/rusui/issues/127) (shared
  operators).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[ADR 0012](0012-operator-access.md) gives one operator preview grants: a
separate secret, a short TTL, a different origin, and no
`allow-always`. Showing a running app to someone without
`RUSUI_OPERATOR_TOKEN` (a reviewer, a demo audience) has no supported
path. [#328](https://github.com/Sannrox/rusui/issues/328) asked whether
rusui should mint a time-bounded, revocable preview grant for a viewer
who is not the operator.

Its adoption gate requires the maintainer to name who must open a
preview without the operator token. Nobody is named. Its own unresolved
question is whether a screenshot is enough for that audience.

Source evidence at the commit this decision was made against:

- The operator mints a grant from the console
  (`internal/server/preview.go` `consoleMintPreview`). The grant binds
  one session, one environment handle, and one port. Its TTL is the
  ten-minute `engine.GrantTTL`. The store keeps only the token hash.
- The preview proxy accepts the grant as the `g` query parameter or a
  bearer header, and refuses a request that carries an operator cookie.
  It refuses an expired or revoked grant, a replaced environment, and
  an environment that is not ready.
- The grant is therefore a bearer capability. Whoever holds the URL
  before expiry can open the preview, if they can reach the preview
  origin. That is a property of the operator's grant, not a sharing
  feature.
- `store.RevokePreviewGrant` has no caller; there is no revoke surface
  for the operator. The grant row carries no operator generation, so
  rotating `RUSUI_OPERATOR_TOKEN` does not end an outstanding grant
  before its TTL. ADR 0012 D4 names both as ends of a grant.
- An unexpired grant keeps its environment from idle sleep
  (`internal/store/environment.go`).

## Decision

**D1. Defer.** rusui mints no preview grant for a viewer who is not the
operator. There is no public, anonymous, or long-lived preview URL, and
no policy flag enables one. ADR 0012 is unchanged.

**D2. Forwarding is not sharing.** A preview grant belongs to the
operator who minted it. Sending its URL to someone else is outside the
supported contract. It gets no longer TTL, audience label, or audit
receipt.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names the audience that must open a live preview
   without the operator token, and why a screenshot or recording made
   by the operator does not serve it; and
2. a superseding ADR amends ADR 0012 with a grant class for that
   audience.

**D4. Constraints any adopted design must meet:**

- **A distinct class.** The share grant is neither the operator cookie,
  the operator preview grant, nor the turn grant. It is minted only by
  an operator session and never by the guest.
- **Bounded.** Expiry is chosen from fixed options with a hard ceiling,
  and never outlives the session. It ends on revoke, environment
  replace, cancel, and operator-token rotation.
- **Revocable.** The operator can list and revoke outstanding share
  grants. This needs the revoke surface and operator-generation binding
  that operator grants lack today.
- **Read only.** One port, the same origin isolation as ADR 0012 D5, no
  terminal, no write to the plane. Preview HTML still cannot call
  `/sessions/*`, `/jobs/*`, or `/approvals/*` with operator credentials.
- **Off by default.** A project enables it explicitly in `policy.yaml`;
  an overlay may only narrow it.
- **Not crawlable.** The response carries `noindex` and a strict
  referrer policy. A private repository's app never gets a public URL
  without an explicit per-project choice.
- **Visible.** Whether the guest may learn that the viewer is not the
  operator is stated, and a share grant does not keep the environment
  awake past its own expiry.

No implementation follow-up is authorized by this ADR.

## Consequences

Easier: one preview grant class, one audience, one origin rule.

Harder: showing a running app to a reviewer needs the operator present,
or a screenshot or recording the operator makes.

Irreversible: none.

The revoke and operator-generation gaps above are properties of today's
operator grant. This ADR records them. It does not change them.

## Threat examples

The deferral removes each of these for a share grant. An adopting ADR
must answer each. The first and third also apply to a forwarded operator
grant today, bounded by its ten-minute TTL.

- **Grant leaked in a transcript or pull request.** A URL with `g=` in a
  pasted log gives any reader the preview until expiry. D4 requires a
  hard ceiling, revoke, and a listing so the operator can end it.
- **Public grant used to reach a plane port.** A share grant that names
  an arbitrary port reaches a plane or host service. D4 limits it to one
  environment port through the preview proxy and forbids plane writes.
- **Grant that outlives the session.** A demo link still works after
  cancel or replace. D4 ends it with the session, the environment, and
  operator-token rotation.
- **Crawlable URL on a private repository's app.** A search engine or
  link previewer fetches and indexes the app. D4 requires `noindex`, a
  strict referrer policy, and an explicit per-project opt-in.

## Rejected alternatives

- **Accept share grants now.** No audience is named, and the operator
  grant has no revoke surface or generation binding to build on.
- **Reject sharing outright.** A reviewer who cannot hold the operator
  token is a plausible audience. Deferral keeps it open behind D3.
- **Lengthen the operator grant's TTL and forward it.** It widens the
  operator's own grant, has no audience or revoke, and makes forwarding
  the feature.
- **Mint an operator cookie for the viewer.** Gives a stranger the
  console. Rejected by ADR 0012.

## Validation and reversal

Validation: this ADR is Accepted in the index; `consoleMintPreview` is
the only preview grant mint; no policy field enables sharing. Reverse by
a superseding ADR that meets D3 and D4 and amends ADR 0012 in the same
change.

## Sources

- [#328](https://github.com/Sannrox/rusui/issues/328)
- [#115](https://github.com/Sannrox/rusui/issues/115),
  [#127](https://github.com/Sannrox/rusui/issues/127)
- [ADR 0012](0012-operator-access.md),
  [ADR 0032](0032-named-services.md)
- `internal/server/preview.go` `consoleMintPreview`, `previewProxy`;
  `internal/store/preview.go`; `internal/store/environment.go`;
  `internal/engine/engine.go` `GrantTTL`
