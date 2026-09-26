# ADR 0022: Public-repository unattended sessions default to the container driver

- Status: Accepted
- Date: 2026-09-26
- Amends: [ADR 0008](0008-p1-isolation-split.md) (names the public-repository
  default and the fail-closed startup when that topology is missing).
  [ADR 0009](0009-credential-broker.md) grants and trusted egress stand.
  [ADR 0016](0016-local-interactive-runtime.md) local sessions remain a
  separate experimental kind.
- Resolves: [#246](https://github.com/Sannrox/rusui/issues/246)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0001](0001-environment-plane.md) D4,
  [ADR 0007](0007-environment-snapshot.md),
  [#91](https://github.com/Sannrox/rusui/issues/91) (delivered container
  and credential boundary),
  [#125](https://github.com/Sannrox/rusui/issues/125) (optional stronger
  runtime; not required by this threat model),
  [#126](https://github.com/Sannrox/rusui/issues/126) (does not replace
  the container backend by default).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

ADR 0008 assigned machine isolation to a container and called the process
driver a test/dev stand-in. The operator topology already requires Docker
or Podman CLI, plane TLS, and a guest image. `rusui diagnose` / `GET /readyz`
treat a missing container CLI as an `unavailable` blocker.

[ARCHITECTURE.md](../../ARCHITECTURE.md) still described v1 as trusted
local execution: same OS user, ephemeral cwd, no filesystem jail. An
independent maintainer running unattended work on an untrusted public
repository could read that as permission to use the process driver as
the default, including silent fallback when Docker is missing.

The objects this choice binds:

| Object | Owner | Link |
| --- | --- | --- |
| Environment | plane | one session, one environment; driver is `container` or `process` |
| Session | plane | unattended kinds `review`, `run`, `scheduled`, `implement` |
| Turn | plane; runner claims | per-turn grant, lease, receipt |
| Runner | operator host | outbound-only; execs the guest inside the environment |
| Policy revision | operator file; overlay may only narrow | `visibility: public` is the untrusted-source signal |
| Receipt | plane store | permission and action rows; not a substitute for the container |

Protected resources: the operator home directory, SQLite, `policy.yaml`,
other local repositories, GitHub App and operator tokens, model secrets,
and the plane process.

## Decision

**D1. Default.** Unattended sessions whose bound repository is
`visibility: public` use the **container** driver as machine isolation.
The guest filesystem is the image plus the session workspace. Network
egress is the `trusted` class: plane git and model proxies only. The
guest holds the per-turn grant; GitHub REST, App keys, operator tokens,
and SQLite stay on the plane.

**D2. Opt-in.** The process driver (`rusui-runner -driver`) is explicit
test/dev. It is the same OS user. Ephemeral cwd and an env allowlist are
defense in depth; they do not hide the operator home, SQLite, or other
repos. Sumika local sessions ([ADR 0016](0016-local-interactive-runtime.md))
are a human-driven experimental kind. Neither is the public-repository
unattended default. Overlay may only narrow; it cannot widen a public
repository onto trusted-local execution.

**D3. Fail closed.** `rusui diagnose` and `GET /readyz` report `runtime`
`unavailable` when Docker or Podman is missing, and the report is not
ready. Unattended public-repository work must not start from that
report. Missing plane TLS or `RUSUI_GUEST_IMAGE` disables the container
driver; that is also not a process fallback for a public repository.
A stronger runtime (microVM / Firecracker) is evaluated only by
[#125](https://github.com/Sannrox/rusui/issues/125) after a named gap
the container path cannot meet.

## Consequences

Easier: an independent maintainer has one default for untrusted public
source, matching the already supported operator topology.

Harder: hosts without a container CLI cannot claim a public-repository
unattended session as supported. Process-driver fixtures remain valid
for tests and for operator-owned trusted work that opts in with `-driver`.

Irreversible: none. No schema or policy field is added. [#125](https://github.com/Sannrox/rusui/issues/125)
stays optional.

Limitations recorded inside the five-day investigation window:

- This ADR does not add a policy isolation field.
- `EnsureSessionEnvironment` still no-ops when neither container nor
  process driver is configured. Diagnose is the fail-closed startup
  operators must consult before dispatch; claim-time refusal of that
  no-op is a later enforcement, not a new jail.
- Wake latency and snapshot restore of a hypervisor remain #125.

## Rejected alternatives

- **Trusted-local as the public default.** Contradicts ADR 0008 and
  exposes operator home, SQLite, and credentials to untrusted source.
- **Silent process fallback when Docker is missing.** Turns an
  unavailable topology into a weaker one without an operator choice.
- **Require a microVM before public-repository sessions.** #125 is
  optional; #126's non-goal is replacing the supported backend by
  default. Container plus plane proxies meets the named threats.
- **Treat Sumika local as the unattended public profile.** Local
  sessions have no turns, grants, or machine isolation (ADR 0016).

## Validation and reversal

Validation: `rusui diagnose` with no container CLI reports `runtime`
unavailable and is not ready; ARCHITECTURE, CONTEXT, and the operator
guide name the container default. Reverse by a superseding ADR that
changes the default or the fail-closed startup, then rewrite the
contract in the same change.

## Sources

- [#246](https://github.com/Sannrox/rusui/issues/246)
- [#91](https://github.com/Sannrox/rusui/issues/91) (closed; container
  and credential boundary)
- [ADR 0008](0008-p1-isolation-split.md), [ADR 0009](0009-credential-broker.md),
  [ADR 0016](0016-local-interactive-runtime.md)
- `internal/ops/diagnose.go` `checkRuntime` (blocker, `unavailable`)
- [docs/operator.md](../operator.md) supported topology
