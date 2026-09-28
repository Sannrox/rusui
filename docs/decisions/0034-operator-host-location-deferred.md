# ADR 0034: A registered operator host is not a session location yet

- Status: Accepted
- Date: 2026-09-28
- Amends: [ADR 0001](0001-environment-plane.md) D4 (driver kinds) and
  [ADR 0016](0016-local-interactive-runtime.md) (the local kind is not a
  managed location).
- Resolves: [#323](https://github.com/Sannrox/rusui/issues/323)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0009](0009-credential-broker.md) (per-turn grant),
  [ADR 0012](0012-operator-access.md) (one-writer terminal),
  [ADR 0028](0028-container-isolation-profile.md) (container isolation),
  [ADR 0029](0029-single-host-runner.md) (one runner host),
  [#124](https://github.com/Sannrox/rusui/issues/124) (fleet placement),
  [#127](https://github.com/Sannrox/rusui/issues/127) (shared operators),
  [#186](https://github.com/Sannrox/rusui/issues/186) (unified local and
  managed attach).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

Some work may need a particular machine: a GPU box, a signing Mac, or a
repository that cannot leave a private network. rusui has two ways to run
on "a machine" today, and neither is "start this managed session on that
host":

- `rusui-runner` claims turns for environments the plane created. The
  supported topology is one runner on the plane host
  ([ADR 0029](0029-single-host-runner.md)). Schema seeds one `runners`
  row named `local`; `POST /runners/hello` records presence of that name.
- The experimental `local` session kind runs a Sumika Process on the
  operator's host with no Turn, lease, or managed grant
  ([ADR 0016](0016-local-interactive-runtime.md)).

[#323](https://github.com/Sannrox/rusui/issues/323) asked whether a
named, registered host may be a location field on a managed session. Its
adoption gate requires the maintainer to name one workload that cannot
run in a plane-created container. None is named. Dogfood has no second
machine. No session, environment, or policy field names a location in
code or `policy.yaml`.

Three questions are easy to conflate:

| Question | Object | Owner |
| --- | --- | --- |
| Where does this environment run? | Location | this ADR |
| How do several runners share load, drain, and recover? | Fleet placement | [#123](https://github.com/Sannrox/rusui/issues/123) / ADR 0029, [#124](https://github.com/Sannrox/rusui/issues/124) |
| Is local PTY attach the same product as managed attach? | Sumika Process | [#186](https://github.com/Sannrox/rusui/issues/186) / ADR 0016 |

A location is an operator's choice for one session. Placement is the
plane's choice among equivalent hosts. A Sumika Process is human-driven
work that never becomes managed.

## Decision

**D1. Defer.** A managed session has no location field. Its only
location is the container runtime on the plane host
([ADR 0028](0028-container-isolation-profile.md),
[ADR 0029](0029-single-host-runner.md)). The CLI and console offer no
host picker.

**D2. The local kind stays unmanaged.** The experimental `local` session
kind remains the only supported "this machine" path. It keeps ADR 0016's
trust profile: the Sumika daemon's OS user, ambient host authority, no
Turn, lease, per-turn grant, model proxy token, or GitHub credential. It
does not become a managed location by registering the host.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names one workload and the hardware or network
   constraint that stops it from running in a plane-created container;
   and
2. a superseding ADR selects registration, isolation class, and
   credential model for that host, and amends ADR 0029 if the host runs
   a second runner.

**D4. Constraints any adopted design must meet:**

- **Isolation class.** The host runs a container or stronger boundary
  with the same machine-isolation guarantee as ADR 0028. A shared
  same-user login is not a managed location.
- **Credentials.** The guest on that host sees only plane grants
  ([ADR 0009](0009-credential-broker.md)). It never sees the operator's
  CLI logins, keychain, or home directory. A registered host holds no
  plane secret beyond its runner bootstrap.
- **Terminal.** One writer per environment, as
  [ADR 0012](0012-operator-access.md). Sumika attach on the same host does
  not become a second writer to a managed environment.
- **Offline at create.** Create refuses. The plane does not fall back to
  another host, because the operator chose this one.
- **Offline mid-turn.** Lease expiry and grant TTL end the turn
  ([ADR 0011](0011-unattended-session-contract.md)). The environment is
  lost, not migrated.
- **Operators.** One operator. Two operators on one host stay out of
  scope unless [#127](https://github.com/Sannrox/rusui/issues/127) is
  selected.

No implementation follow-up is authorized. This ADR adds no schema,
policy field, or runner protocol.

## Consequences

Easier: one managed location, one runner, one isolation profile. Sumika
local sessions keep a clear "unmanaged" label.

Harder: a workload that needs a GPU, a signing key on specific hardware,
or a private network stays outside managed sessions. The operator runs
it as a `local` session under ambient authority, or by hand.

Irreversible: none.

## Threat examples

The deferral removes each of these. An adopting ADR must answer each.

- **Treating a shared host as a container.** A registered laptop runs
  the guest as the operator's user; the guest reads other repositories,
  SSH keys, and CLI logins. D4 requires container-class isolation.
- **Injecting managed grants into a same-user login.** Handing a turn
  grant to a Sumika Process gives an unattended credential to a process
  with ambient authority. D2 keeps local sessions free of managed grants.
- **Two operators on one host.** A second operator's session on the same
  registered host shares its OS user and runtime. Out of scope without
  #127.
- **Silent fallback.** A create that moves to the plane host when the
  chosen host is offline runs work where the operator did not choose.
  D4 refuses instead.

## Rejected alternatives

- **Accept a location field now.** No workload is named and there is no
  second machine. The registration, isolation, and credential choices
  would have no evidence.
- **Register the Sumika host as a managed location.** Sumika runs under
  the operator's user with ambient authority. Managed grants there would
  break ADR 0009 and ADR 0016 D6.
- **Treat location as fleet placement.** Placement picks among
  equivalent hosts; a location is pinned by the operator. ADR 0029
  already refuses a fleet.

## Validation and reversal

Validation: this ADR is Accepted in the index; `runners` still seeds one
row named `local`; no session or policy field names a location. Reverse
by a superseding ADR that meets D3 and D4 and amends ADR 0001 D4, ADR
0016, and ADR 0029 in the same change.

## Sources

- [#323](https://github.com/Sannrox/rusui/issues/323)
- [#123](https://github.com/Sannrox/rusui/issues/123),
  [#124](https://github.com/Sannrox/rusui/issues/124),
  [#127](https://github.com/Sannrox/rusui/issues/127),
  [#186](https://github.com/Sannrox/rusui/issues/186)
- [ADR 0001](0001-environment-plane.md) D4,
  [ADR 0009](0009-credential-broker.md),
  [ADR 0011](0011-unattended-session-contract.md),
  [ADR 0012](0012-operator-access.md),
  [ADR 0016](0016-local-interactive-runtime.md),
  [ADR 0028](0028-container-isolation-profile.md),
  [ADR 0029](0029-single-host-runner.md)
- `internal/store/model.go` `LocalRunnerName`;
  `internal/server/server.go` runner hello
