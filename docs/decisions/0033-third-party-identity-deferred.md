# ADR 0033: Plane-minted third-party identity is deferred

- Status: Accepted
- Date: 2026-09-28
- Amends: [ADR 0009](0009-credential-broker.md) (the guest holds only
  the per-turn grant; that grant reaches only plane proxies).
- Resolves: [#322](https://github.com/Sannrox/rusui/issues/322)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0001](0001-environment-plane.md) D5,
  [ADR 0007](0007-environment-snapshot.md) (setup bytes are snapshot
  identity), [ADR 0020](0020-turn-scoped-github-publication.md)
  (turn-scoped GitHub access stays behind the plane),
  [ADR 0027](0027-guest-reachability-ask.md) (policy is the grant; an
  image ask cannot widen it),
  [ADR 0028](0028-container-isolation-profile.md) (container isolation).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[ADR 0009](0009-credential-broker.md) gives the guest one credential:
the per-turn grant (32 bytes, ten-minute TTL, one environment, hashed at
rest). The plane redeems it for git smart-HTTP and model egress. A guest
that must call a package registry, an object store, or a private
network has no supported identity. A long-lived token in the
environment, in `.agents/setup`, or in the image violates
[ADR 0001](0001-environment-plane.md) D5. A token in `services.yaml`
`env:` would persist into the snapshot.

[#322](https://github.com/Sannrox/rusui/issues/322) asked whether the
plane should mint a short-lived identity the guest redeems for one
measured audience. Its adoption gate requires the maintainer to name one
guest task that fails today because that identity is missing. None is
named.

Source evidence at the commit this decision was made against:

- The trusted-egress contract lets the container guest reach only
  `rusui.plane` ([ADR 0027](0027-guest-reachability-ask.md) D2).
  `GuestDialAllowed` (`internal/env/network.go`) states that rule, but
  only tests call it. `ApplyTrustedNetwork` attaches the guest to the
  `rusui-trusted` network, maps `rusui.plane`, and disables IPv6.
  `DockerCLI` creates that network with a plain `network create`, which
  filters no destination. Enforcement of the contract is a known gap.
  It is not closed by, and does not change, this decision.
- `policy.yaml` accepts egress classes `none`, `trusted`, `custom`, and
  `full` (`internal/policy/policy.go`). The container driver applies the
  trusted network to every environment; no code path reads `custom` or
  `full`. No policy grant names a third-party host.
- The dogfood repository has no `.agents/setup` or `.agents/resume`.
  No recorded setup, resume, or turn fails on a third-party call.

Under the grant, a third-party identity is unusable without a second
change that widens egress. The issue forbids that: an identity mint
cannot widen egress. That some hosts are reachable today through the
enforcement gap is not a grant, and a task that depends on it is not a
measured need.

## Decision

**D1. Defer.** rusui does not mint a third-party identity for the guest
in the 1.0 core. On the brokered path the guest keeps one credential:
the per-turn grant, redeemed only by plane proxies. The `implement`
exception stands unchanged: those sessions still receive the operator's
GitHub credential ([ADR 0015](0015-agent-publication.md)) until
[ADR 0020](0020-turn-scoped-github-publication.md) is implemented.
No audience is named, because no measured task needs one.

**D2. No long-lived third-party secret enters the environment.** A
registry, object-store, or cloud key in the image, in `.agents/setup`,
in `.agents/resume`, in `services.yaml` `env:`, or in the runner
environment of the guest remains a D5 violation. Operators who need
such a call today run it outside the managed session.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names one guest task and one audience (issuer and
   service) that fails today because the identity is missing; and
2. a superseding ADR selects the egress route to that audience under
   `policy.yaml` (the grant, [ADR 0027](0027-guest-reachability-ask.md)
   D1) in the same decision. An overlay may only narrow it.

**D4. Constraints any adopted design must meet.** These are recorded so
a later ADR starts from them, not so this one implements them:

- The plane is the issuer and the redemption point. The guest presents
  the per-turn grant. The preferred shape is the ADR 0009 pattern: a
  plane proxy route for that audience that swaps the grant for the
  third-party credential, so the guest never holds it. A token handed
  to the guest is acceptable only if the superseding ADR records why a
  plane route cannot work.
- One audience per credential. The subject names session and project,
  not the operator or the plane.
- TTL no longer than the per-turn grant. Minting stops when the grant
  expires, the lease is lost, the session is paused or cancelled, or
  the project is removed from policy.
- Nothing minted during setup. Snapshot prepare holds only the
  read-only prepare grant ([ADR 0009](0009-credential-broker.md)).
  Setup that needs a third-party call is refused, because its output
  becomes snapshot bytes.
- The issuer being unavailable fails the turn closed.
- A mint receipt records audience, subject, expiry, and turn, never the
  token.

No implementation follow-up is authorized. This ADR does not change
`policy.yaml`, the grant, or the proxies.

## Consequences

Easier: the brokered guest keeps one credential and the grant keeps one
reachable host. The credential-broker, snapshot, and egress contracts
stay unchanged.

Harder: a session cannot pull from a private registry or object store,
or reach a private network. Such work stays outside managed sessions
until D3 holds.

Irreversible: none. No schema, policy field, or proxy route is added.

## Threat examples

The deferral removes each of these by construction. An adopting ADR
must answer each one.

- **Token copied into the snapshot during setup.** Setup runs before
  the snapshot is cached under `source_hash`; anything it writes is
  reused by every later environment. D2 forbids a secret in setup, and
  D4 forbids a mint during setup.
- **Audience reuse across services.** A token valid for a registry and
  an object store lets a compromised dependency read both. D4 binds one
  audience per credential.
- **Subject too broad.** A subject that names the operator or the plane
  cannot distinguish a session or project in the third-party audit log.
  D4 requires session and project in the subject.
- **Mint after operator removal or pause.** A cached issuer credential
  outlives the decision to stop the session. D4 stops minting with the
  grant, lease, pause, cancel, or policy removal.
- **Identity used as an egress bypass.** A token that lets the guest
  dial a new host widens the grant. D3 requires the route to be chosen
  in policy in the same decision.

## Rejected alternatives

- **Name an audience now.** No task measures one, and the grant names
  no third-party host. Choosing the issuer, subject, and TTL without a
  task would fix a contract with no evidence.
- **Reject third-party identity outright.** A plane route that redeems
  the per-turn grant already fits ADR 0009. Rejecting it would forbid
  the shape this ADR prefers. Deferral keeps it open behind D3.
- **Secrets catalog of long-lived third-party keys in the guest.**
  Violates D5 and persists into snapshots. Minting was not shown to be
  impossible, so the issue's condition for this alternative is not met.
- **Enable `custom` egress for third-party hosts first.** Widens the
  grant without a measured task and without the identity boundary.

## Validation and reversal

Validation: this ADR is Accepted in the index; no plane code mints a
third-party credential; `GuestDialAllowed` still states the
`rusui.plane`-only rule; no code reads the `custom` or `full` egress
class. Reverse by a superseding ADR that meets D3, names
the audience, and amends ADR 0009 in the same change.

## Sources

- [#322](https://github.com/Sannrox/rusui/issues/322)
- [#44](https://github.com/Sannrox/rusui/issues/44) (git and model
  broker), [#91](https://github.com/Sannrox/rusui/issues/91) (container
  and credential boundary), [#303](https://github.com/Sannrox/rusui/issues/303)
  (image does not grant reachability)
- [ADR 0001](0001-environment-plane.md) D5,
  [ADR 0007](0007-environment-snapshot.md),
  [ADR 0009](0009-credential-broker.md),
  [ADR 0015](0015-agent-publication.md),
  [ADR 0020](0020-turn-scoped-github-publication.md),
  [ADR 0027](0027-guest-reachability-ask.md)
- `internal/env/network.go` `ApplyTrustedNetwork`, `GuestDialAllowed`;
  `internal/env/docker.go` `ensureNetwork`;
  `internal/policy/policy.go` egress classes
