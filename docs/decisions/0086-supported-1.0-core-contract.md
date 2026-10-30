# ADR 0086: Freeze the supported 1.0 core contract

- Status: Accepted
- Date: 2026-10-30
- Amends: [ADR 0046](0046-workspace-first-sequence.md) (M6 freeze content),
  [ADR 0085](0085-freeze-on-author-validated-journey.md) (the freeze now
  exists). Optional-track deferrals in ADR 0046 decision 4 stand except
  where a later accepted ADR already adopted the track.
- Resolves: [#141](https://github.com/Sannrox/rusui/issues/141).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (product contract;
  this ADR rewrites the 1.0 support claim),
  [docs/supported-topology.md](../supported-topology.md),
  [ROADMAP.md](../../ROADMAP.md), [CONTEXT.md](../../CONTEXT.md).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#141](https://github.com/Sannrox/rusui/issues/141) is the M6 freeze: name
the smallest supportable 1.0, each supported profile, explicit exclusions,
and compatibility rules, using the task, maintenance, and self-hosting
evaluations. [ADR 0085](0085-freeze-on-author-validated-journey.md) already
allows the freeze to start on the author-validated U8 journey and keeps
independent self-hosting as the 1.0 publish gate
([#146](https://github.com/Sannrox/rusui/issues/146)).

The demonstrations that exist are narrower than a broad 1.0 claim:

- Managed terminal workflow: [429 second results](../proofs/429-managed-rerun-2-results.md)
  **narrow** (native Linux Docker, reference Claude guest). Identity cells
  passed. Workspace diff in `rusui read` for container sessions and prompt
  release of a cancelled turn's guest and runner did not.
- Bounded issue-to-PR: [P8](../proofs/p8-pilot-results.md) **narrow**;
  [431 cohort](../proofs/431-cohort-results.md) **narrow** (single-package
  Go fixes, one pull request, follow-up on the same PR, stale-base refuse).
  Lost claim-response recovery and the 45-minute deadline were not shown.
- Self-hosting: [U8](../proofs/u8-operator-journey.md) **narrow**,
  author-validated, in-process. No independent maintainer.
- Maintenance: [T2](../proofs/t2-maintenance-eval.md) advisory/comment/repair
  gates **incomplete**; live comment, close, and merge remain unauthorized.
- Local interactive: [#186](../proofs/186-unified-workflow-results.md)
  **defer**; [ADR 0045](0045-local-unattended-promotion-deferred.md) keeps
  it out of the unattended 1.0 claim.

A freeze that copied every accepted ADR as a demonstrated guarantee would
overstate that evidence. A freeze that waited for independent self-hosting
or a thirty-day maintenance observation would ignore ADR 0085 and ADR 0046.

## Decision

The supported 1.0 core is the terminal-first remote workspace already in
[ARCHITECTURE.md](../../ARCHITECTURE.md), narrowed to the profiles and
limits below. This ADR is the scope and versioning record. The product
contract carries the same claim. Publishing a 1.0 release remains
[#146](https://github.com/Sannrox/rusui/issues/146) and still requires an
independent self-hosting journey with a go outcome.

### Support classes

| Class | Meaning |
| --- | --- |
| **supported** | In the 1.0 contract; demonstrated on the named profile, or a durable engine guarantee with crash-recovery tests |
| **author-validated** | In the contract for the author journey only; not an external self-hosting claim until #146 |
| **experimental** | Shipped or designed; may change without a 1.0 compatibility window |
| **excluded** | Not a 1.0 guarantee; a later ADR is required before it becomes one |

### Version tuple

One supported tuple. Mixing pieces across commits is unsupported.

| Piece | 1.0 value |
| --- | --- |
| `rusui` and `rusui-runner` | Same git commit. Version is the git tag from `scripts/lib/version.sh` |
| Host OS | Linux or macOS |
| Runner | One `rusui-runner` on the plane host ([ADR 0029](0029-single-host-runner.md)) |
| Isolation runtime | Docker or Podman CLI on `PATH` ([ADR 0028](0028-container-isolation-profile.md)) |
| Default guest | shikigami ACP from the reference guest image of that commit ([ADR 0073](0073-session-plane-guest-registry.md) D4) |
| Named guests | `shikigami` (ACP), `grok` (ACP), `claude` (Claude Code CLI 2.1.283), `codex` (app-server). Generic `protocol: acp` guests need a plane-signed conformance receipt ([ADR 0082](0082-signed-conformance-receipts.md)) |
| Guest image | Digest-pinned `$RUSUI_GUEST_IMAGE`. Toolchain is baked into that image ([ADR 0050](0050-guest-toolchain-in-image.md)); it is not fetched at an unpinned tag at session start |
| Listen | Loopback `127.0.0.1` by default |
| Store | One SQLite file, WAL, one writer |

Distribution: install `rusui` and `rusui-runner` together. The reference
guest image is a separate digest-pinned artifact, not a bundled floating
tag and not a per-session download. Operators supply model credentials
and, when used, GitHub App material. Integrity of the image is the digest
in diagnose/readyz; rollback is the previous binaries plus a copied
database (see Backup).

Docker Desktop and Podman Desktop remain unverified for the guest link
([ADR 0062](0062-guest-link-desktop-podman-deferred.md)). Process driver
(`-driver`) is test/dev, not this tuple.

### Identity and lifecycle

Nouns stay [CONTEXT.md](../../CONTEXT.md). 1.0 identities:

| Object | Identity | Lifecycle in 1.0 |
| --- | --- | --- |
| Project | Operator-named policy domain | Policy v2 keyed by project |
| Tenant | `tenant_id`; single-operator installs use `default` | Isolation on sessions and approvals ([ADR 0076](0076-tenant-isolation.md)). Shared-operator governance stays excluded ([ADR 0083](0083-retain-single-operator-access.md)) |
| Session | Stable id; review is `(project, bound repo, item)`; run/scheduled minted at create | Create, follow, attach, steer, approve, cancel, archive. Retention `short` (72h, prompt data removed) or `indefinite` ([ADR 0081](0081-session-retention.md)). Client disconnect does not cancel a turn |
| Turn | One leased attempt on an unattended session | queued / leased / completed / failed / cancelled. Heartbeat 60s, liveness 3 min, review/scheduled deadline 12 min, run deadline 45 min, retry limit 3 |
| Environment | One per session | Create / sleep / wake / archive. Kept until the operator archives it ([ADR 0063](0063-keep-environment-until-archive.md)). No operator-chosen location ([ADR 0034](0034-operator-host-location-deferred.md)) |
| Runner | Single `local` row | Outbound-only; no inbound port |
| Snapshot | `source_hash` of image digest, git pin, setup bytes | Shared prepared tree; never a live fork ([ADR 0035](0035-live-environment-fork-deferred.md)) |

Child sessions ([ADR 0071](0071-bounded-child-session.md),
[ADR 0072](0072-cross-project-child-session.md)) are in the contract with
focused tests. They are not part of the 429/431 demonstrated operator
journey.

### Operator and runner API

Primary path: `rusui` CLI and HTTP+SSE. Console and editor ACP are clients
of the same API; completeness of either is not a 1.0 gate
([ADR 0046](0046-workspace-first-sequence.md)).

Supported operator verbs on the version tuple, as shown in the 429 rerun
unless noted: `run`, `read` / `read -follow`, `attach`, `prompt`,
`approve`, `drain`, `resume`, `restore`, `diagnose`. `rusui read` does not
promise a workspace diff for container sessions.

Publication: turns propose; the operator applies GitHub writes as
`POST /sessions/{id}/write-actions` ([ADR 0080](0080-turns-propose-writes-go-through-actions.md)).
Guests never hold a GitHub or core credential. Humans merge. Comment,
close, merge, and land stay unauthorized.

Narrowed delivery claim, from P8 and 431: one pinned-path implement task
at a time, one pull request whose head is the turn candidate, a follow-up
that updates that pull request, admission that refuses a stale base. Lost
claim-response recovery is not a 1.0 guarantee.

Runner protocol: hello / claim / heartbeat / complete / fail. Claim mints
a runner turn token and a guest grant ([ADR 0075](0075-split-guest-grant-from-runner.md)).
Complete/fail are idempotent on `(turn_id, lease_generation, claimed_revision)`.

### Policy and budgets

`policy.yaml` v2 is the only executable baseline. Unknown fields fail
closed. Overlay may only narrow. Pause stops new claims and apply.
Halt cancels leased turns. Token and dollar budgets are not collected.
`max_concurrent_leases` (default 1) and `max_reviews_per_repo_per_utc_day`
are the 1.0 meters. Unmatched `session/request_permission` waits; inbox
history is not a grant.

### Evidence and receipts

SQLite is the source of truth. Optional `records/` JSON and Markdown
files are regenerated exports, not recovery inputs.

Portable fields on a turn receipt: session id, turn id, lease generation,
claimed revision, policy revision id, outcome, evidence class
(`server_verified` or `model_assertion`), action type, source git pin,
guest identity, and the guest ACP session id when present. Action rows
record `fs/*`, `terminal/*`, permission decisions, `write.propose` /
`write.apply`, and `core.draft`.

Signed ACP conformance receipts are **accepted** ([ADR 0082](0082-signed-conformance-receipts.md)):
HMAC-SHA256 under a plane-held key, over suite, entry digest, image
digest, and time. They qualify generic ACP guests on this plane only.

Signed portable session or evidence bundles are **deferred**. No 1.0 key
handling, expiry, or export format is defined for them. `rusui diagnostics`
is an operator bundle with secrets redacted, not a signed receipt.

### Export, retention, migrations, backup

- Retention: session class `short` or `indefinite` ([ADR 0081](0081-session-retention.md)).
  Environment until archive ([ADR 0063](0063-keep-environment-until-archive.md)).
- Schema: versioned `schema_migrations` only. Forward on start.
- Backup: copy `rusui.db` and `-wal`/`-shm` while drained or stopped.
- Restore: `rusui restore -db <copy>` inventories, clears live leases,
  drops grants, releases guests, rotates the plane id, and keeps receipt
  and approval records. Live authority does not survive
  ([ADR 0041](0041-ha-plane-deferred.md)).
- Upgrade: drain, stop, copy the database, replace same-commit binaries,
  start, or restore the copy ([docs/upgrade.md](../upgrade.md)).

### Compatibility and deprecation

- Dropping or shrinking a **supported** guarantee requires a later ADR.
  Experimental and excluded surfaces have no 1.0 compatibility window.
- Policy v1 files fail closed. Old clients that omit new optional request
  fields keep working; unknown policy fields fail closed.
- `rusui` and `rusui-runner` from different commits are unsupported.
- Independent self-hosting stays **author-validated** until #146. A
  guarantee that exists only on the U8 journey is marked that way and is
  not an external-readiness claim.
- Core definition drafts ([ADR 0084](0084-core-draft-actions.md)) stay
  **experimental**.

### Optional-track disposition

| Track | Class | Why |
| --- | --- | --- |
| Terminal CLI + HTTP+SSE workspace | supported | ADR 0046; 429 narrow |
| Container isolation, one host, SQLite | supported | ADR 0028, 0029, 0041 |
| shikigami default guest; grok/claude/codex; generic ACP by receipt | supported | ADR 0073, 0060, 0025, 0082 |
| Governed implement Actions, human merge | supported (narrow) | ADR 0080; P8/431 |
| OIDC CLI for one operator | supported | ADR 0074; console stays token-only |
| Tenant key `default` | supported | ADR 0076; not a second operator |
| Schedule bound to one session | supported | ADR 0069 |
| Session-owned webhook | supported | ADR 0070 |
| Bounded child sessions | supported (design); not in 429/431 journey | ADR 0071, 0072 |
| Author-validated install/restore | author-validated | U8; ADR 0085 |
| Local interactive / Sumika | excluded from 1.0 unattended | ADR 0016, 0045; #186 defer |
| MicroVM / Firecracker | excluded | ADR 0028 |
| Runner fleet | excluded | ADR 0029 |
| Postgres / HA plane | excluded | ADR 0041 |
| Shared-operator governance | excluded | ADR 0039, 0083 |
| Live environment fork | excluded | ADR 0035 |
| Environment desktop | excluded | ADR 0036 |
| Unauthenticated shared preview | excluded | ADR 0037 |
| Plane-minted third-party identity | excluded | ADR 0033 |
| Extension / plugin contract | excluded | ADR 0043 |
| Disconnected / offline execution | excluded | ADR 0042 |
| Docker Desktop / Podman Desktop guest link | excluded | ADR 0062 |
| Live comment, close, merge, land | excluded | ADR 0038; T2 incomplete |
| Thirty-day maintenance observation | excluded from this freeze | ADR 0046 M3; required for a maintenance release |
| Core drafts | experimental | ADR 0084 |
| Native mobile, training, workflow DSL, marketplace, cross-repo transactions, autonomous policy mutation | excluded | ROADMAP non-goals |

[#110](https://github.com/Sannrox/rusui/issues/110) is a maintenance-track
gate, not a dependency of this freeze. The #141 issue body no longer lists
it.

## Consequences

Easier: clients and operators can tell a stable core from experiments
without reading every ADR status. Later 1.0 implementation work
([#142](https://github.com/Sannrox/rusui/issues/142)–[#146](https://github.com/Sannrox/rusui/issues/146))
has an explicit inventory.

Harder: a demonstrated gap (container `rusui read` diff, cancel guest
release, lost claim response, independent self-hosting) cannot be sold as
supported. Widening a class needs evidence and a later ADR.

No schema, policy, runner, or credential migration. [ARCHITECTURE.md](../../ARCHITECTURE.md)
and [docs/supported-topology.md](../supported-topology.md) take this
claim. Conflicting earlier phase language in ADR 0046 about #141 waiting
on #110 is withdrawn.

## Rejected alternatives

- Freeze every accepted ADR as a demonstrated 1.0 guarantee: overstates
  P8, 429, 431, U8, T2, and #186.
- Wait for independent self-hosting or T2 promotion: contradicts ADR 0085
  and ADR 0046's split of M3 from M6.
- Include the local-interactive profile: #186 deferred it; ADR 0045
  forbids an unattended 1.0 claim.
- Signed portable session bundles in this freeze: no key-handling or
  export contract exists beyond conformance receipts and SQLite truth.
- Assign a release date or semver here: #146 publishes 1.0.

## Validation and reversal

Validate by: this ADR merged; the product-contract section matching it;
`scripts/adr-index --check` and `python3 scripts/check-docs.py` clean.
#146 still refuses to publish without an independent self-hosting go.
Reverse with a later ADR that widens, narrows, or supersedes a class; no
data migration.

## Sources

- [#141](https://github.com/Sannrox/rusui/issues/141),
  [#103](https://github.com/Sannrox/rusui/issues/103),
  [#118](https://github.com/Sannrox/rusui/issues/118),
  [#87](https://github.com/Sannrox/rusui/issues/87),
  [#653](https://github.com/Sannrox/rusui/issues/653)
- [ADR 0046](0046-workspace-first-sequence.md),
  [ADR 0085](0085-freeze-on-author-validated-journey.md)
- [P8 results](../proofs/p8-pilot-results.md),
  [431 results](../proofs/431-cohort-results.md),
  [429 second results](../proofs/429-managed-rerun-2-results.md),
  [U8 journey](../proofs/u8-operator-journey.md),
  [T2 eval](../proofs/t2-maintenance-eval.md),
  [#186 results](../proofs/186-unified-workflow-results.md)
