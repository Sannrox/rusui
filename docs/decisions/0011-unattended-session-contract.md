# ADR 0011: Recoverable unattended-session contract

- Status: Accepted
- Date: 2026-09-20
- Resolves: [#88](https://github.com/Sannrox/rusui/issues/88)
- Narrows: [#89](https://github.com/Sannrox/rusui/issues/89) (D2),
  [#90](https://github.com/Sannrox/rusui/issues/90) (D3),
  [#91](https://github.com/Sannrox/rusui/issues/91) (D4),
  [#92](https://github.com/Sannrox/rusui/issues/92) (D5)
- Amends: budget *sources* left open in
  [ADR 0005](0005-policy-v2-project.md); follow-up-as-turn in
  [ADR 0006](0006-session-start.md) (FIFO, not latest-wins).
  Does not amend pause, dry-run apply, [ADR 0002](0002-grok-acp-agent-set.md)
  spawn, [ADR 0007](0007-environment-snapshot.md) snapshot identity,
  [ADR 0008](0008-p1-isolation-split.md), or
  [ADR 0009](0009-credential-broker.md).
- Related: [ADR 0010](0010-hybrid-roadmap-sequence.md) (G0 / #87) is
  Accepted. That sequencing file is not a dependency of this
  lifecycle contract. This remains 0011 so the numbers do not collide.
- Discussion: none. GitHub Discussions are disabled. Accepted 2026-09-20.
  Objects: plane session, turn, guest ACP session, concurrent-lease
  meter. Evidence: policy parse fail-closed for unnamed meters; D2–D5
  package tests for FIFO, wait-on-permission, cancel, and claim
  reservation. Permitted action: wait on a live permission RPC, enqueue
  FIFO follow-ups, cancel a session, refuse unnamed budget keys. Policy:
  pause is not cancel; inbox allow is not a grant; token/dollar meters
  are unavailable.

## Context

[ADR 0006](0006-session-start.md) defines how review, run, and
scheduled sessions start. It does not say which execution may continue
after a disconnect, a plane restart, runner loss, a permission prompt,
or environment expiry. Those questions block D2, D3, and D5.

Recorded current behavior on baseline
[94aabd7](https://github.com/Sannrox/rusui/tree/94aabd7d292faa052ec8faba0f028f8e07bc9ade),
from isolated tests, not a live Grok conversation:

| Path | Observed now | Evidence |
| --- | --- | --- |
| Guest session | `HostACP` always `session/new`. `session/load` exists on the client and fake guest; the runner never calls it | `internal/runner/acp.go` `HostACP`; `TestConformanceFakeAgent` |
| Follow-up | Same job row: `pending_revision++`, snapshot body becomes the new prompt, lease kept if already leased. A second queued follow-up overwrites the first | `TestPromptFollowUpQueued`, `TestPromptFollowUpLeasedKeepsLease` |
| Permission | Gate answers the RPC immediately. Unmatched is `reject-once` plus `acp.approval`. Inbox `allow` stores a row and does not resume a turn | `answerPermission`; `TestLogsAndApprovalsInbox`; `TestPermissionMatchedAllowHasNoApproval` |
| Pause | Stops new claims and apply. Leased turns may heartbeat, complete, or fail. Does not kill the guest | [ARCHITECTURE.md](../../ARCHITECTURE.md) Pause |
| Cancel | No session-cancel operation. `session/cancel` is a protocol constant only | `internal/acp/protocol.go` |
| Budget | `max_reviews_per_repo_per_utc_day` is enforced at claim. Project `budgets:` is parsed and never consulted. Token/dollar meters do not exist | `Engine.Claim`; `policy.Project.Budgets` assigned only |
| Fencing | Turn lease generation, 3-minute liveness, 12-minute execution deadline, 10-minute grant TTL renewed on heartbeat. Steal of a live generation is denied | `Liveness`, `ExecDeadline`, `GrantTTL`; `TestTurnGrantExpiresWithoutHeartbeat` |
| Environment | Container sleep/wake; idle 72h expire destroys the container; session row remains; dirt is gone; rematerialize from snapshot | `TestEnvironmentCreateSleepWakeExpire`; ADR 0007 |
| Live Grok | Binary `grok 0.2.112` is present on this machine. `RUSUI_ACP_LIVE` was unset, so `TestLiveGrokConformance` did not run. This investigation does **not** claim that Grok preserves conversations or waits on a deferred permission | skipped live test |

A fixture that advertises `loadSession: true` cannot establish that
Grok does. Unavailable live credentials limit the result; they do not
count as passed evidence.

## Decision

The first unattended proof uses **one runner**, the **container**
driver (Docker or Podman), and **Grok ACP over container stdio** with
plane-owned proxies ([ADR 0009](0009-credential-broker.md)). The
process driver is not that topology.

### D1. Identities

- **Plane session** (`sessions.id`) is the durable conversation the
  operator attaches to. One session, one environment ([ADR 0006](0006-session-start.md)).
- **Turn** is one leased execution of the current prompt. Fencing
  identity is `(turn_id, lease_generation)`.
- **Guest ACP session** is ephemeral to the guest process. Persist the
  last guest `sessionId` on the plane session. On the next turn in the
  **same** environment, try `session/load`; if it fails, `session/new`
  and record that guest context was not restored.
- Do not promise Grok conversation preservation until a live
  `session/load` of a prior Grok id is demonstrated. D2 must treat
  restore-failed as a supported outcome, not a hidden retry loop.

### D2. Follow-up ordering

Each follow-up prompt is a **durable queued turn**, FIFO, on the same
session. Latest-wins overwrite is rejected: an operator instruction
must not disappear because a later prompt arrived while queued.

A follow-up during a live lease does not steal that lease. The current
turn runs to complete, fail, cancel, or expire on its
`claimed_revision`; the next claim on that session takes the oldest
unqueued follow-up. Pause still refuses new claims, including
follow-up enqueue when the project is paused.

### D3. Human approval: wait on the live RPC

Choose **waiting**, not checkpoint/retry, for an in-flight
`session/request_permission`:

1. Keep the JSON-RPC request open.
2. Heartbeat the turn so the lease and grant stay alive.
3. Bound the wait by the remaining execution deadline (default 12
   minutes). On timeout, answer `reject-once`, park `acp.approval`,
   and let the guest continue or stop as it will.
4. Immediately before `allow-once`, re-read **current** policy and
   overlay in the same way apply rechecks policy. If pause is set, the
   project is gone, the allow-rule no longer matches, or the turn is
   no longer leased at this generation: `reject-once`. Admit-time
   policy must not grant.
5. Never answer `allow-always` for unmatched requests.

After the RPC has been answered, or the turn has ended, an inbox
`allow` **must not** grant authority. That row is a record, not a
capability. D3 resumes by enqueueing a new follow-up turn; the guest
must request permission again under current policy. Stale approval
never authorizes a new tool call.

Checkpoint/retry remains the recovery path when there is no live RPC
to wait on (runner loss, process death, timeout already answered).

### D4. Cancel versus pause

Pause is unchanged: no new claims, no apply, in-flight lease may
finish. Pause is not cancellation.

**Cancel** is a new operator action on a session. It must:

- send `session/cancel` if a guest is live, then kill the guest
  process tree inside the container;
- fail the claimed turn as `cancelled` without consuming another
  automatic retry of that revision;
- release the budget reserve;
- leave the environment until idle expiry unless the operator also
  expires it;
- keep receipts and the session row.

Cancel does not reset `failed` history and does not clear pause.

### D5. First enforceable budget

Token and dollar accounting are **unavailable**. Policy must not
pretend they exist: `budgets` keys other than the meters below fail
closed at parse.

Supported meters, per project per UTC day except (1):

1. **`max_concurrent_leases`** (default 1). Reserve at claim, hold
   while `state=leased`, release on complete, fail, cancel, or lease
   expiry. This is the first runaway brake.
2. **Turn wall-clock** (`ExecDeadline`, 12 minutes). Heartbeat after
   the deadline is refused; the lease expires; the turn is requeued or
   failed per existing retry limits.
3. Existing **`max_reviews_per_repo_per_utc_day`** for review admits.

Maximum overshoot: one already-leased turn may continue until liveness
fails after the deadline is observed (up to `Liveness`, 3 minutes).
No new claim while at the concurrent cap. Pause does not release an
in-flight reserve.

Minutes-of-CPU and model-token charges may be recorded as telemetry
later; they are not admit meters until a source is demonstrated.

### D6. Retention across expiry

| Artifact | After sleep/wake | After 72h idle expire | After runner/guest death |
| --- | --- | --- | --- |
| Session row, turns, snapshots, receipts, approval decisions | kept | kept | kept (SQLite) |
| Container workspace dirt | kept | **gone** (destroy) | gone with the process; rematerialize from snapshot |
| Runner snapshot cache | hit skips setup | hit skips setup | local to that runner; plane stores the hash only |
| Guest ACP session | load may work; unproven for Grok | invalid; `session/new` | invalid |
| Agent checkpoint files | only if they sit in the workspace | gone | gone unless in SQLite receipts |

Do not promise preservation the container backend cannot show. D8
backup is a later issue; this ADR does not invent a retention GC.

### D7. Interruption table

| Interruption | Durable owner | Fencing | Recovery | Retained context | Budget |
| --- | --- | --- | --- | --- | --- |
| Client disconnect | session row | none on the client | `attach` resumes the event stream; the turn continues | plane receipts; guest unknown to the client | no change |
| Plane restart | SQLite | refresh-owner TTL; turn lease/grant TTL | expire owners; leased turns survive only if the runner still heartbeats | rows and receipts | in-flight reserve held until lease ends |
| Runner loss | turn | `lease_generation` + grant TTL | lease expires; retry or fail that revision; guest process is dead | receipts already written; guest id invalid | release after expiry |
| Grant expiry without heartbeat | turn credential | token hash + `expires_at` | complete/fail with that generation is rejected | receipts | release after lease expiry |
| Environment idle 72h | session row | `environments.expires_at` | destroy container; next turn rematerializes from snapshot | dirt gone; guest id invalid | no charge while expired |
| Overlay pause | overlay | stops new claims/apply | in-flight heartbeat/complete/fail allowed | unchanged | in-flight reserve held |
| Explicit cancel | session | new cancel generation | D4 | receipts; environment until TTL | release |
| Permission wait timeout | turn | RPC + execution deadline | `reject-once`, park, no stale allow | approval row | still charged until turn ends |
| Follow-up while leased | session | `claimed_revision` vs queued FIFO | current turn finishes; next claim runs the oldest follow-up | both prompts stored | current reserve unchanged; next turn reserves again |

## Consequences

D2 must persist guest session id, implement FIFO follow-ups, reconnect
without duplicating a live lease, and distinguish cancel from pause.
D3 must wait on the live permission RPC with policy recheck, and must
not treat inbox history as a grant. D4 uses the container topology
and treats grant loss as turn failure, not silent continue. D5
enforces concurrent leases and wall-clock, and refuses token/dollar
keys.

`ARCHITECTURE.md` names the accepted objects and meters. FIFO
follow-up, wait-on-permission, cancel, and claim-time reservation land
in D2–D5. Dry-run apply is unchanged. Unnamed budget keys fail closed
at parse.

## Rejected alternatives

- **Checkpoint/retry as the only approval path.** Throws away the
  synchronous ACP RPC that Grok already blocks on for
  `allow-once` / `reject-once` (ADR 0002 probe). Waiting is the live
  path; retry is only for a dead RPC.
- **Inbox allow resumes the old request.** Stale authority. Recheck
  would be skipped.
- **Keep latest-wins follow-up.** Drops a queued operator prompt.
- **Treat pause as cancel.** Changes the meaning of pause, which this
  issue forbids.
- **Token or dollar meters now.** No collection source exists;
  advertising them would fail closed in name only.
- **Process driver as the M1 topology.** Contradicts ADR 0008.
- **Promise Grok `session/load`.** Not measured in this investigation.

## Validation and reversal

Validation of this decision is maintainer review of this file.
Validation of the contract is D2–D5 plus a later live Grok
`session/load` and deferred-permission probe under
`RUSUI_ACP_LIVE=1`. Those live claims remain unproven here.

Reverse by superseding this ADR. No schema migration ships with this
proposal.

Unresolved, and not hidden:

- Whether Grok `0.2.112` honors `session/load` for a previous id.
- Whether Grok keeps `session/request_permission` open long enough
  for a human wait bounded by `ExecDeadline`.
- Exact cancel HTTP/CLI shape (D2/D3 surface work).
- Backup/GC of SQLite receipts (D8).

## Sources

- [#88](https://github.com/Sannrox/rusui/issues/88)
- Baseline
  [94aabd7](https://github.com/Sannrox/rusui/tree/94aabd7d292faa052ec8faba0f028f8e07bc9ade)
- `internal/runner/acp.go`, `internal/acp/client.go`,
  `internal/acp/client_test.go`, `internal/acp/live_test.go`,
  `internal/engine/followup.go`, `internal/server/approvals.go`,
  `internal/engine/engine.go` (`Claim`, `Heartbeat`, `expireLeaseTx`)
- ADRs [0002](0002-grok-acp-agent-set.md),
  [0005](0005-policy-v2-project.md),
  [0006](0006-session-start.md),
  [0007](0007-environment-snapshot.md),
  [0008](0008-p1-isolation-split.md),
  [0009](0009-credential-broker.md)
