# ADR 0097: Plane-minted package-registry identity for one guest audience

- Status: Accepted
- Date: 2026-11-20
- Amends: [ADR 0009](0009-credential-broker.md) (the brokered guest
  may hold one plane-minted package-registry identity in addition to
  the per-turn grant; git and model proxies are unchanged).
- Supersedes: [ADR 0033](0033-third-party-identity-deferred.md) (the
  deferral of plane-minted third-party identity). ADR 0033 D2 and D4
  still constrain this audience.
- Resolves: [#783](https://github.com/Sannrox/rusui/issues/783)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (product contract;
  this ADR does not rewrite it),
  [ADR 0001](0001-environment-plane.md) D5,
  [ADR 0027](0027-guest-reachability-ask.md) (policy is the grant; an
  identity cannot widen it),
  [ADR 0050](0050-guest-toolchain-in-image.md) (setup that needs
  packages stays an image rebuild),
  [ADR 0066](0066-inject-secret-at-wake.md) (stored secret ids are a
  different path),
  [ADR 0091](0091-egress-classes.md) and
  [ADR 0093](0093-egress-full-proxy.md) (the egress route already
  exists),
  [#322](https://github.com/Sannrox/rusui/issues/322) (the deferred
  research),
  [#822](https://github.com/Sannrox/rusui/issues/822) (implementation).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act. Until merge this is a proposal, not
  implementation authority. This ADR does not mint tokens.

## Context

[ADR 0033](0033-third-party-identity-deferred.md) deferred a
plane-minted third-party identity because no guest task and no
audience were named. Its D3 gate required the maintainer to name one
task and one audience (issuer and service), and a superseding ADR that
selects the egress route under `policy.yaml` in the same decision.
[#783](https://github.com/Sannrox/rusui/issues/783) names both: a turn
that must pull packages from one authenticated package registry.

Wake secrets ([ADR 0066](0066-inject-secret-at-wake.md)) inject
operator-held stored values at wake. That is not a minted identity.
A long-lived registry key in the image, `.agents/setup`,
`.agents/resume`, or `services.yaml` `env:` remains a D5 violation.

Source evidence at the commit this decision was made against
(`72d030f720a2165d7504283584140d57585ab726`):

- No plane path mints a third-party credential. `injectSecrets`
  (`internal/engine/secrets.go`) writes plane-held `RUSUI_SECRET_<ID>`
  values into `/run/rusui/secrets/<id>` and receipts kind `secret`
  with the id, not the value.
- `policy.yaml` already grants third-party hosts: `custom` plus
  `egress_allow`, or `full` public HTTPS (`internal/policy/policy.go`,
  [ADR 0091](0091-egress-classes.md), [ADR 0093](0093-egress-full-proxy.md)).
  `trusted` and `none` still forward only the plane.
- `GuestDialAllowed` (`internal/env/network.go`) still states the
  trusted-class plane-only rule. Custom and full reachability is the
  guest link, not that helper.
- Snapshot prepare still uses the read-only prepare grant
  ([ADR 0009](0009-credential-broker.md)). Setup bytes are snapshot
  identity ([ADR 0007](0007-environment-snapshot.md)).
- Git and model proxies still redeem the guest grant on the plane
  ([ADR 0009](0009-credential-broker.md), [ADR 0075](0075-split-guest-grant-from-runner.md)).
  They are not a package-registry route.

ADR 0033 D1 is the refused action this ADR replaces. D2 and D4 are
the constraints this ADR adopts. D3's gates hold: the audience is
named, and the egress route is selected below.

## Decision

**D1. Adopt one package-registry audience.** rusui may mint a
short-lived identity the guest presents to one operator-allowlisted
package-registry host. That is the only third-party audience. Issuer
is the plane. Service is that one host. Object stores, private
networks, cloud control planes, and a second registry are out of
scope.

The named task is a running turn that pulls packages. Snapshot
prepare, plane-stored pre-clone and pre-setup, and `.agents/setup`
do not receive this identity. Packages needed at create stay in the
guest image ([ADR 0050](0050-guest-toolchain-in-image.md)).

**D2. `policy.yaml` is the egress grant.** Minting does not add a
host. The project must already be able to reach that registry:

- `custom`: the host is in `egress_allow`;
- `full`: public HTTPS is already granted; the project still names
  the one host, so minting is not implicit for every public name;
- `trusted` and `none`: naming the audience is a load or claim error.

An overlay may only remove the audience or the host. Unknown fields
fail closed. Off by default: no mint unless the project names the
audience. Identity mint cannot widen egress
([ADR 0027](0027-guest-reachability-ask.md) D1).

**D3. ADR 0033 D2 and D4 hold.** The identity is:

- **Plane-issued.** The durable registry credential stays on the
  plane. The guest never holds it. The plane obtains a token whose
  TTL is at most the per-turn grant, by exchanging an audience-bound
  assertion or by calling the registry's token API with that
  plane-held credential. If the registry cannot issue a shorter-lived
  token, this path refuses; [ADR 0066](0066-inject-secret-at-wake.md)
  remains the stored-value injection and is not this identity.
- **One audience per credential.** The token is valid only for the
  named host. It is not git, model, GitHub REST, or a second registry.
- **Subject names session and project**, not the operator or the
  plane.
- **TTL no longer than the per-turn grant.** Mint at turn start,
  not at wake. Heartbeat renews it with the grant. Minting stops when
  the grant expires, the lease is lost, the session is paused or
  cancelled, the environment sleeps, or the project is removed from
  policy. Sleep strips the value from the guest.
- **Nothing minted during setup.** Snapshot prepare keeps the
  read-only prepare grant. A token in setup becomes snapshot bytes.
- **Issuer unavailable fails the turn closed.**
- **Receipt the id.** The receipt names audience, subject, expiry,
  and turn, never the token. The value is absent from `rusui read`,
  hook logs, and transcripts.

**D4. A guest-held minted token is acceptable for this audience.**
ADR 0033 D4 preferred a plane proxy that swaps the grant so the guest
never holds the third-party credential. That remains the shape for
git and model. It is not the first path here: package clients address
the registry by its public hostname; the guest link already forwards
that name without rewriting bytes ([ADR 0091](0091-egress-classes.md)
D2); a plane proxy would require TLS interception on that name or a
per-ecosystem rewrite of the project's registry URL onto
`rusui.plane`. Git remotes are plane-rewritten; package registry URLs
are not. The minted token is presented to the already-granted host.
A later ADR may add a plane registry proxy; this one does not.

**D5. Not a substitute for the git or model proxy.** Those routes
stay plane-redeemed. This identity does not authenticate GitHub REST,
publication, or model egress.

No implementation is authorized by this ADR beyond capturing the
decision. [#822](https://github.com/Sannrox/rusui/issues/822) mints
the class.

## Consequences

Easier: a turn can pull from one authenticated registry without a
durable key in the image, setup, resume, or `services.yaml`. The
egress grant stays the policy the operator already reviews.

Harder: a second guest-held credential class, a policy audience
binding that must not outrun egress, mint and strip at turn
boundaries, a receipt kind distinct from stored secrets, and a fail
closed when the issuer cannot mint. Schema and policy keys belong to
the follow-up.

Irreversible: none until the follow-up writes rows or injects values.
After mint, rollback stops new mints; outstanding tokens still expire
with the grant and strip on sleep.

[ARCHITECTURE.md](../../ARCHITECTURE.md) still lists third-party
identity minting as deferred until that file is rewritten. This ADR
is not that rewrite, and it does not mint.

## Threat examples

| Attempt | Result |
| --- | --- |
| Token copied into the snapshot during setup | Refused. D1 and D3 mint only at turn start. Setup keeps the prepare grant. |
| Audience reuse across a registry and an object store | Refused. D1 is one package-registry host. |
| Subject names the operator or the plane | Refused. D3 requires session and project. |
| Mint after pause, cancel, lease loss, or policy removal | Stops with the grant. Sleep strips the value. |
| Identity used as an egress bypass | Refused. D2 requires the host already in the egress grant. Mint adds no destination. |
| Inject the durable stored secret and call it minted | Refused. That is ADR 0066. D3 fails closed when the registry cannot issue a shorter-lived token. |
| `trusted` project names the audience | Load or claim error. No route. |
| Overlay adds the audience the baseline left off | Refused. Overlay may only narrow. |
| Guest uses the identity for git or model | Refused. D5. Proxies still redeem the guest grant. |
| Issuer down | Turn fails closed. |

## Rejected alternatives

- **Retain ADR 0033.** The audience and task are now named, and D3's
  gates hold. Wake secrets still put a durable value in the guest for
  the whole wake.
- **Inject the stored registry secret (ADR 0066) as this identity.**
  That is not minting. The guest would hold the operator's durable
  key. 0066 stays for stored ids; this ADR is a shorter-lived
  derivative.
- **Mint during setup so snapshot prepare can pull packages.** Setup
  output is snapshot identity. A token or a privately fetched tree
  would be reused by every later environment. ADR 0050 stands.
- **A plane registry proxy as the first path.** Preferred for git and
  model; for this audience it needs TLS interception or per-ecosystem
  URL rewrites the plane does not own (D4).
- **Every `egress_allow` host gets an identity.** One audience per
  credential. Reachability is not identity.
- **Enable a new host by minting.** Widens egress. Forbidden by D2
  and by the issue.
- **OIDC or a second identity for the operator.** Operator login is
  [ADR 0074](0074-single-operator-oidc-cli.md) /
  [ADR 0088](0088-issuer-vouched-subjects.md). This is a guest
  audience.
- **Long-lived keys in the image, setup, resume, or `services.yaml`.**
  Issue non-goal. Breaks [ADR 0001](0001-environment-plane.md) D5.

## Validation and reversal

Validation: this ADR is Accepted in the index; ADR 0033 is Superseded
by this record; ADR 0009 lists this amendment. No mint ships until
[#822](https://github.com/Sannrox/rusui/issues/822) implements D1–D5.
Reverse by a superseding ADR that restores the deferral or names a
different audience; do not delete this file.

## Sources

- [#783](https://github.com/Sannrox/rusui/issues/783)
- [#822](https://github.com/Sannrox/rusui/issues/822)
- [#322](https://github.com/Sannrox/rusui/issues/322)
- [ADR 0009](0009-credential-broker.md),
  [ADR 0027](0027-guest-reachability-ask.md),
  [ADR 0033](0033-third-party-identity-deferred.md),
  [ADR 0050](0050-guest-toolchain-in-image.md),
  [ADR 0066](0066-inject-secret-at-wake.md),
  [ADR 0091](0091-egress-classes.md),
  [ADR 0093](0093-egress-full-proxy.md)
- `internal/engine/secrets.go` `injectSecrets`;
  `internal/env/secrets.go` `SecretDir`;
  `internal/env/network.go` `GuestDialAllowed`;
  `internal/policy/policy.go` egress classes and `egress_allow`
- Commit `72d030f720a2165d7504283584140d57585ab726`
