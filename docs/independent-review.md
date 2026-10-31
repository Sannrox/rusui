# Independent security review brief (1.0)

This is the written C3 scope for
[#143](https://github.com/Sannrox/rusui/issues/143). A reviewer can
execute it without further design. It does not commission a reviewer,
perform the review, or remediate findings. Publishing 1.0 remains
[#146](https://github.com/Sannrox/rusui/issues/146).

The support profile is
[ADR 0086](decisions/0086-supported-1.0-core-contract.md). This brief
does not claim external self-hosting readiness. Author-validated
install, disconnect/reconnect, upgrade, and restore stay U8 until #146.

Timebox: five reviewer-days. Finite evidence. Do not promise absence of
vulnerabilities.

## Candidate identities

Review this immutable tuple. Mixing commits is unsupported.

| Piece | Frozen identity |
| --- | --- |
| Git commit | `384b08cb3d45630068574f4ab605cfc932011b59` (`main` at `#669`) |
| Workspace version | `0.1.0` (`VERSION`; no `v*` tag yet) |
| Binaries | `rusui` and `rusui-runner` built from that commit |
| SQLite schema | `schema_migrations` version **54** |
| Policy | v2; project `guests.default: shikigami` and `allowed: [grok, claude, codex, shikigami]`; other capability defaults from `policy.example.yaml` |
| Topology | [supported-topology.md](supported-topology.md): one runner on the plane host, Docker or Podman CLI, loopback HTTPS `127.0.0.1` (`RUSUI_TLS_CERT` / `RUSUI_TLS_KEY` / `RUSUI_PLANE_CA` from `rusui setup`), one SQLite WAL file |
| Guest image | `$RUSUI_GUEST_IMAGE` as written by `rusui setup apply` or `make guest-image` at the candidate (`rusui-guest:` plus the first 16 hex of the image id). Record that tag and the full image `Id` (`sha256:` + 64 hex) from `docker image inspect` or `podman image inspect` before day 1. diagnose/readyz only confirm the env is set. Do not retag to a floating name. |
| Default guest | shikigami ACP from that image |
| Named guests in scope | `shikigami`, `grok`, `claude` (Claude Code CLI 2.1.283), `codex` |
| Conformance | `scripts/compat-conformance.sh` at the candidate (`internal/compat`) |

C1 freeze: [ADR 0086](decisions/0086-supported-1.0-core-contract.md)
([#141](https://github.com/Sannrox/rusui/issues/141)). C2 checks:
[compatibility.md](compatibility.md)
([#142](https://github.com/Sannrox/rusui/issues/142)). C4 public-API
example: [independent-client.md](independent-client.md)
([#144](https://github.com/Sannrox/rusui/issues/144)).

Remediation that changes a boundary produces a new candidate SHA and
re-runs the affected conformance checks before #143 can clear.

## Independence

The reviewer cannot be:

- GitHub login `Sannrox`, which authored and merged the candidate.
- Any login with commits on the path that produced ADR 0086, the C2
  conformance package, the C4 example, or commit `384b08c`.
- A coding agent that implemented those surfaces.
- Anyone who holds production operator, worker, webhook, or publication
  credentials for a rusui install under review.

The reviewer may be an external assessor or a second maintainer who
fails every line above. They get a throwaway loopback topology, not
write access to `Sannrox/rusui` beyond a private advisory.

## Configuration under review

Default 1.0 profile only:

- Listen `127.0.0.1`. Do not pass `-addr 0.0.0.0` or `-allow-insecure`.
- Policy from `rusui setup apply` (starter project `default`,
  `guests.default: shikigami`) or copied from `policy.example.yaml`.
  Set the project `guests` block to `default: shikigami` and
  `allowed: [grok, claude, codex, shikigami]` (the commented block
  in the example). Keep `comment: false`, `close: false`,
  `implement: false`, `land: false`, `repair: ""`,
  `comments: false`, `never_release: true`,
  `never_leak_private_to_public: true`, `egress: trusted`.
  Overlay may only narrow. Do not omit `guests`: setup then writes
  `RUSUI_GUEST=claude`.
- One `rusui-runner` on the plane host. Public-repo unattended sessions
  use the container topology; they must not fall back to the process
  driver.
- Intake GitHub client read-only. Implement publication, if enabled for
  an abuse case, is `POST /sessions/{id}/write-actions` (ADR 0080).
  Guests never hold a GitHub or core credential.
- Secrets required to start: `RUSUI_WEBHOOK_SECRET`,
  `RUSUI_WORKER_SECRET`, `RUSUI_SLACK_SECRET`, and a read-only
  `RUSUI_GITHUB_TOKEN` / `GITHUB_TOKEN`. Optional
  `RUSUI_SECRET_<ID>` values inject at wake and drop on sleep.
- Loopback plane TLS from `rusui setup apply`: `RUSUI_TLS_CERT`,
  `RUSUI_TLS_KEY`, and `RUSUI_PLANE_CA`. Unset cert/key disables
  the container driver. The plane listens HTTPS on `127.0.0.1`.
  Guests trust the CA as `rusui.plane`.

Out of configuration: operator tunnel, Slack as a required human
socket (signing and allowlist still in credential scope), OIDC as a
completeness gate, non-loopback listen, `-allow-insecure`.

## Boundaries in scope

Examine these six surfaces on the candidate. Skip unselected optional
backends (ADR 0086 exclusions).

### 1. Selected credential

Plane-held secrets versus guest grant versus runner turn token
(ADR 0009, ADR 0075, ADR 0066, ADR 0084). Guests hold the grant only.
Core credentials stay on the plane. Model proxy credentials do not
become GitHub write tokens.

### 2. Isolation

Container is the unattended public-repo profile (ADR 0008, ADR 0022,
ADR 0028). Trusted egress is HTTPS to `rusui.plane` only. Process
driver is test/dev. Image identity is the recorded digest. Cross-session
workspace and host filesystem must stay unreachable from a guest.

### 3. Operator / preview

Operator HTTP+SSE with bearer `RUSUI_OPERATOR_TOKEN`. Worker bearer
must not act as operator. Unauthenticated shared preview is excluded
(ADR 0037). Loopback default is not a network jail if rebound.
Session follow, attach, and receipts must not leak webhook, worker, or
publication secrets.

### 4. Policy / approval

Policy v2; unknown fields fail closed; overlay cannot widen or enable
`repair`. Pause stops new claims and apply. Halt cancels leased turns.
Unmatched `session/request_permission` waits; inbox history is not a
grant. `max_concurrent_leases` default 1.
`max_reviews_per_repo_per_utc_day` is a 1.0 meter.

### 5. Action publication

Turns propose; the operator applies named GitHub writes (ADR 0080,
which amends ADR 0015). Humans merge. Comment, close, merge, and land
stay unauthorized on this profile. Default `comment: false` and empty
`repair`. The guest may push only `refs/heads/rusui/<session>/*`
through the git proxy under the turn grant. Default-branch push,
`gh pr create` / `edit`, merge, and a guest-held GitHub or core
token are fails.

### 6. Recovery

Copy `rusui.db` plus `-wal`/`-shm` while drained or stopped.
`rusui restore -db <copy>` keeps session ids, receipts, policy
revisions, approval records, budgets, and retention class. It clears
live leases, drops grants, expires managed guests, and rotates the
plane id. Interrupted upgrade leaves `schema_upgrade_lock` and the
next start fails closed. Diagnostics and `rusui receipt` exports are
not recovery inputs and are unsigned.

## Methods

Five reviewer-days, in this order:

1. Read this brief, [ARCHITECTURE.md](../ARCHITECTURE.md),
   [SECURITY.md](../SECURITY.md), ADR 0086, and the ADRs cited per
   boundary.
2. Build the candidate (`make all`) and run
   `scripts/compat-conformance.sh`. Record commit and schema from the
   printed report.
3. Record `$RUSUI_GUEST_IMAGE` and the image `Id` from
   `docker image inspect` or `podman image inspect`. Record the
   diagnose/readyz class separately.
4. Code-inspect the packages that own the six boundaries:
   `internal/policy`, `internal/engine`, `internal/gh`,
   `internal/runner`, `internal/server`, `internal/store`,
   `internal/publish`, `internal/env`.
5. Exercise the abuse cases below on a disposable loopback topology.
   Bind examples to `127.0.0.1`. Do not use a reachable non-loopback
   listen.
6. Write the report: in-scope findings, omissions, severity, residual
   risk. Finite evidence only.

Public tracking contains safe summaries only. Exploitable findings go
to a [GitHub security advisory](https://github.com/Sannrox/rusui/security/advisories/new).
Do not open a public issue for an exploitable defect. Do not name
private repositories.

## Tested abuse cases

Each case is a question the reviewer must answer with a pass, fail, or
not-applicable plus evidence.

| ID | Boundary | Case |
| --- | --- | --- |
| A1 | Credential | Guest environment listing does not contain webhook, worker, Slack, operator, or GitHub write secrets. |
| A2 | Credential | Wake-injected `RUSUI_SECRET_<ID>` is absent after sleep. |
| A3 | Credential | Runner turn token cannot call operator HTTP+SSE. Operator token cannot claim runner jobs. |
| A4 | Credential | Guest grant cannot mint a GitHub write or a core credential. |
| A5 | Isolation | Public-repo unattended session refuses the process driver when Docker/Podman is missing. |
| A6 | Isolation | Guest with `egress: trusted` cannot reach an address other than the plane proxy. |
| A7 | Isolation | Two concurrent sessions (if the budget is raised only for this case) cannot read each other's workspace. |
| A8 | Isolation | Empty `$RUSUI_GUEST_IMAGE` fails closed (diagnose/readyz `misconfigured`). |
| A9 | Operator | Worker bearer on `GET /sessions/{id}/read/follow` is 401. |
| A10 | Operator | Unauthenticated shared preview endpoints are absent. |
| A11 | Operator | Receipts and diagnostics omit secret values. |
| A12 | Policy | Unknown policy field refuses start. Overlay cannot set `repair: docs` or `comment: true` when the file is off. |
| A13 | Policy | Pause stops new claims and apply; halt cancels a leased turn. |
| A14 | Policy | Unmatched `session/request_permission` does not become a grant. |
| A15 | Publication | With default policy, the plane does not comment, close, merge, or land. |
| A16 | Publication | An implement turn's proposed write is not applied until `POST /sessions/{id}/write-actions`. |
| A17 | Publication | Guest git may push only `refs/heads/rusui/<session>/*` via the turn grant. Default-branch push, `gh pr create` / `edit`, merge, and a guest-held GitHub or core token fail. |
| A18 | Recovery | Restore drops grants and live leases; a restored guest cannot use a pre-restore grant. |
| A19 | Recovery | Interrupted schema upgrade fails closed until the pre-upgrade copy is restored. |
| A20 | Recovery | `rusui diagnostics` / `rusui receipt` cannot restore live authority. |

Raising `max_concurrent_leases` for A7 is a temporary edit of the
review `policy.yaml` project `budgets` (default 1). Overlay cannot
widen it. Revert the file before recording the candidate
configuration.

## Omissions (out of scope)

Do not spend the five days here. Name them as omissions in the report:

- MicroVM / Firecracker, runner fleet, Postgres / HA, shared-operator
  governance, live environment fork, environment desktop,
  unauthenticated preview, plane-minted third-party identity, plugins,
  disconnected execution, Docker Desktop / Podman Desktop guest link.
- Live close, merge, land. Live comments and docs repair when enabled
  in a non-default profile.
- Local-interactive Sumika unattended claim, process-driver production
  use, core definition drafts, signed portable session bundles.
- Independent self-hosting (#146), thirty-day maintenance observation
  (#110), commissioning or remediating (#145 / #146).
- GitHub, Slack, guest CLI, and model-provider internals.
- Operator tunnel, non-loopback listen, `-allow-insecure`.
- Exhaustive source coverage outside the packages listed under Methods.

## Severity

Use this rubric in the report. It is the #143 gate, not a CVSS product
claim.

| Severity | Meaning | 1.0 gate |
| --- | --- | --- |
| Critical | Unauthenticated write, or theft of a plane secret from the default topology | Must fix or remove the capability; independently recheck |
| High | Guest-to-host, guest obtains a GitHub write or core credential, overlay widens policy, restore rehydrates live grants | Same as critical |
| Medium | Non-secret session data leak, drain/pause bypass, bounded denial | Record owner and residual; does not by itself block |
| Low | Docs mismatch, excluded-track residual, defense-in-depth | Record; does not block |

No unresolved critical or high finding clears #143. Lower findings stay
in the reviewed report with ownership. Public text is a safe summary.

## Report shape

The #143 report must include:

- Candidate SHA, schema 54, `$RUSUI_GUEST_IMAGE` tag, image `Id`,
  diagnose/readyz output class (`ready` / `misconfigured` /
  `unavailable`).
- Which abuse cases passed, failed, or were not applicable.
- Findings with severity, boundary, and a private reproduction.
- Omissions actually skipped.
- Residual risk after any in-review mitigations.
- A release disposition: proceed to remediation (#145) / narrow the
  supported candidate / fail the gate.

This brief is complete for #143 to start.
