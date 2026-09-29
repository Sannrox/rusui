# ADR 0042: Disconnected execution is deferred

- Status: Accepted
- Date: 2026-09-29
- Amends: none. [ADR 0009](0009-credential-broker.md) and
  [ADR 0026](0026-harness-model-upstream.md) stand: the guest reaches
  the plane model proxy; the proxy forwards to an upstream.
- Resolves: [#131](https://github.com/Sannrox/rusui/issues/131)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0027](0027-guest-reachability-ask.md) (guest dials `rusui.plane`
  only),
  [ADR 0015](0015-agent-publication.md) (publication is a GitHub write),
  [#132](https://github.com/Sannrox/rusui/issues/132) (implementation of
  a disconnected workflow stays unauthorized).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#131](https://github.com/Sannrox/rusui/issues/131) asked whether rusui
should define one local-inference and disconnected execution profile.
Self-hosting the plane does not keep model requests inside the
operator's network unless the upstream is local, and disconnected
operation also affects source prepare, dependencies, evidence, and
later reconciliation. Its observable outcome is one useful task that
can complete without external connectivity, plus reconciliation limits,
or a deferral. Its adoption gate requires a stated connectivity or
data-boundary requirement the current profile cannot meet. None is
named.

The objects this choice binds:

| Object | Owner | Properties |
| --- | --- | --- |
| Guest | environment | dials `rusui.plane` only under egress `trusted` |
| Model proxy | plane | authenticates the per-turn grant; forwards the body upstream |
| Upstream | operator | `RUSUI_MODEL_UPSTREAM` or the provider API |
| PerTurnGrant | plane broker | the only credential in the guest |
| Intake / publication | GitHub | require reaching GitHub |

Links: the guest presents the grant to the plane proxy; the proxy
swaps it for the upstream key; GitHub intake and implement publication
are separate plane-side calls.

Source evidence at the commit this decision was made against:

- [ADR 0026](0026-harness-model-upstream.md): harness, upstream, and
  model id are independent. The guest does not hold the provider key.
- [ADR 0027](0027-guest-reachability-ask.md): the guest image does not
  declare reachability. Trusted egress is hostname `rusui.plane`.
- `store`/server model proxy fails closed with neither a key nor
  `RUSUI_MODEL_UPSTREAM` (`internal/setup/setup.go` diagnose; docs in
  [docs/configuration.md](../configuration.md)).
- Implement sessions publish to GitHub ([ADR 0015](0015-agent-publication.md)).
  Review intake is GitHub webhooks and catch-up. Those paths are not a
  disconnected workflow.
- No guest/model pairing is recorded that completes a named task with
  the upstream unreachable.

A CLI proxy on loopback (`RUSUI_MODEL_UPSTREAM=http://127.0.0.1:…`) is
already an operator choice for where the *model* lives. It is not a
disconnected plane: GitHub, container image pulls, and toolchain
downloads still leave the host when those features run.

## Decision

**D1. Defer.** rusui does not add a disconnected or local-inference
session profile in the 1.0 core. Supported unattended work still uses
the plane model proxy and the current intake/publication paths. An
operator who points `RUSUI_MODEL_UPSTREAM` at a loopback proxy is using
the existing upstream setting, not this profile.

**D2. No silent offline mode.** If the upstream, GitHub, or a required
toolchain is unreachable, the turn fails closed. The plane does not
queue external actions for later replay as if they had succeeded.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names the connectivity or data-boundary requirement,
   the eligible task, and the guest/model pairing that cannot complete
   on the current profile; and
2. a superseding ADR defines offline artifact identity, staleness, and
   reconciliation without replaying external actions.

**D4. Constraints any adopted design must meet:**

- **Named task and pairing.** One workflow, one guest, one model id,
  with baseline quality/cost. Not every guest.
- **Source, runtime, toolchain, clock, credentials** enumerated as
  present on the host or refused.
- **Receipts stay plane-local** until a reachable reconciliation
  window. External GitHub or Slack actions are not taken offline and
  not replayed later as new identities.
- **Revocation still ends grants.** Disconnect does not extend TTL.
- **No training.** A non-goal of #131.

No implementation follow-up is authorized.

## Consequences

Easier: one proxy path, one grant, fail closed on missing upstream.

Harder: a session cannot complete when GitHub or the model upstream is
unreachable. Air-gapped work stays outside managed sessions.

Irreversible: none.

## Threat examples

The deferral removes each of these by construction. An adopting ADR
must answer each one.

- **Offline publication later posted twice.** A queued GitHub write
  replays after reconnect. D4 forbids replaying external actions as new
  identities.
- **Stale grant used after disconnect.** TTL is ignored because the
  issuer is unreachable. D4: revocation and expiry still end grants.
- **Local model treated as a new guest.** Swapping only the upstream
  URL is ADR 0026, not a new profile. D1 keeps that distinction.
- **Assume every guest supports local inference.** A non-goal. D4
  names one pairing.

## Rejected alternatives

- **Define a disconnected profile now.** No requirement or task is
  named. Choosing offline identity and reconciliation without that
  would fix a contract with no evidence.
- **Reject disconnected execution outright.** A named air-gap task is
  a plausible later audience. Deferral keeps it open behind D3.
- **Treat loopback `RUSUI_MODEL_UPSTREAM` as the profile.** It does not
  cover GitHub, images, or toolchains, and it already works.
- **Train or host a model in rusui.** A non-goal of #131.

## Validation and reversal

Validation: this ADR is Accepted in the index; the guest still dials
the plane proxy; diagnose still requires a key or
`RUSUI_MODEL_UPSTREAM`. Reverse by a superseding ADR that meets D3 and
D4.

## Sources

- [#131](https://github.com/Sannrox/rusui/issues/131)
- [#95](https://github.com/Sannrox/rusui/issues/95),
  [#103](https://github.com/Sannrox/rusui/issues/103),
  [#132](https://github.com/Sannrox/rusui/issues/132)
- [ADR 0009](0009-credential-broker.md),
  [ADR 0026](0026-harness-model-upstream.md),
  [ADR 0027](0027-guest-reachability-ask.md)
- `internal/setup/setup.go` model-access diagnose;
  [docs/configuration.md](../configuration.md)
