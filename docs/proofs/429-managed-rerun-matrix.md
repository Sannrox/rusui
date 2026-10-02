# #429 managed remote-session rerun: predeclared matrix

> **Historical evidence.** This record applies to its stated run and revision;
> it is not a current operator procedure.

Predeclared 2026-10-01, before the first attempt, for
[#429](https://github.com/Sannrox/rusui/issues/429). The maintainer
chose the defaults and asked to run without a separate confirmation
commit; this file is the matrix exactly as fixed before the eval plane
started (19:45 UTC), with the open decisions resolved below. Do not
change a cell's applicability, expected outcome, or a threshold after its
result is known; findings go to the results file.

Rerun gate inherited from [#186](186-unified-workflow-results.md): resolve
#414 and #415 (closed), predeclare a new candidate, rerun the whole
managed matrix. #416 and #417 are closed. The local Sumika profile is out
of scope (non-goal); this campaign scores the managed profile only.

## Fixed profile

| Piece | Value | Confirm |
| --- | --- | --- |
| Candidate | `main` at `fb13ce1` (includes #427 environment replacement receipts, #428 CLI resume/restore, #434 `rusui read -follow`). Re-pin to the exact SHA in the commit that adds this file; no rebuild mid-campaign. | ☐ |
| Host | operator host, one OS user; OS and version recorded in results | ☐ |
| Runtime | Docker container driver, `rusui-trusted` network, `trusted` egress; Docker version recorded | ☐ |
| Guest | reference guest image from `make guest-image` at the candidate (`node:22-bookworm-slim@sha256:48e4b67d…`, Claude Code `2.1.283`, `claude-agent-acp`); image digest recorded before run 1 | ☐ |
| Model route | `claude-sonnet-5` through the operator's CLI proxy (same route as #186, for comparability) | ☐ |
| Plane | dedicated eval plane on `127.0.0.1:8282`, fresh database, `-env-idle-sleep 2m`; no other plane touched | ☐ |
| Policy profile | eval project with session kind `run`; `permissions` allow rules for `kind: read`, `kind: search`, `kind: edit`, and `kind: think`; no rule for `execute`, so every shell request is unmatched and waits for `rusui approve -allow\|-deny ID`. The result file write must therefore happen through an `edit` tool, not a shell (differs from #186, which allowed `execute`). Policy file hash recorded before run 1 | ☐ |
| GitHub | read-only intake token; `implement: false`; no GitHub writes | ☐ |
| Test repository | one non-sensitive public test repository, named in the confirmation comment | ☐ |
| Operator surface | shipped terminal commands only: `rusui run`, `sessions`, `read`, `read -follow`, `attach`, `prompt`, `approve`, `logs`, `envlog`, `drain`, `resume`, `restore`. Direct SQLite edits or HTTP calls are `synthetic` and named per cell. | ☐ |

Evidence labels: `live` = real container, real guest, real model call;
`synthetic` = injected fault, wrong credential, or a non-CLI trigger.
A cell whose prerequisite is unavailable is `not run`, never `pass`.

## Thresholds (to confirm)

- **pass**: every applicable cell passes `live` (or `synthetic` where the
  cell names it), every identity stays attributable, and all three core
  repetitions pass.
- **narrow**: every identity, authorization, and ownership cell passes;
  at most two non-identity transitions fail. Each failure gets a bounded
  defect issue, and the supported managed profile is stated without those
  transitions.
- **defer**: any identity, authorization, or ownership failure; any
  required cell `not run`; or more than two non-identity failures.

"Identity" = the session, turn (id and revision), environment (id and
handle), grant, and receipt ids shown by `rusui sessions`, `rusui read`,
and `GET /sessions/{id}?include=receipts` stay consistent with the
transition and attributable to it.

## Matrix

| # | Transition | Expected | Label |
| --- | --- | --- | --- |
| M1 | Start | `rusui run` creates a run session; an environment is provisioned; the turn is claimed and completes | live |
| M2 | Read | `rusui read` shows the prompt, transcript, and workspace diff of M1 | live |
| M3 | Follow | `rusui read -follow` on a fresh follow-up shows history, new events, and `completed` in order, exit 0 | live |
| M4 | Disconnect during follow | kill the follow client's connection mid-turn (drop the tunnel or kill the client); a new `read -follow` prints the gap once and the outcome | live |
| M5 | Attach | `rusui attach` acquires the write lease on the same session and environment | live |
| M6 | Detach and reconnect | `Ctrl+D` detaches; session and environment continue; a second attach reaches the same ids | live |
| M7 | Second client | a concurrent attach is read-only; its input does not run | live |
| M8 | Follow-up | `rusui prompt` adds revision N+1 on the same session and environment | live |
| M9 | Approval allow | the guest's `execute` request is unmatched; `read -follow` shows `waiting`; `rusui approve` allows; the turn continues and completes | live |
| M10 | Approval deny | a second `execute` request is denied by the operator; the guest is refused; the turn ends with a recorded reason | live |
| M11 | Cancellation | cancelling a live turn ends it at once; the session reports `cancelled`; a later grant use is refused | live |
| M12 | Sleep and wake | after `-env-idle-sleep`, the environment sleeps (sleep receipt); `rusui prompt` wakes it (wake receipt, cause `operator prompt`) on the same environment id and handle | live |
| M13 | Plane restart | stop and start the eval plane; the next turn runs on the same session and environment | live |
| M14 | Environment replacement | `rusui drain`, stop, copy, `rusui restore -db <copy>`, start: the managed environment is expired; the next turn provisions a new environment id with `expire` and `replace` receipts naming it | live (shipped restore path) |
| M15 | Operator recovery | after M13 and M14, `rusui sessions`, `read`, and `logs` show a consistent state; `rusui resume` reopens admission; work continues without Slack | live |
| M16 | Identity | attributable through M1 to M15 | — |
| M17 | Policy denial | a run session on a project without `run` is refused | synthetic |
| M18 | Credential refusal | a finished turn's grant is refused by the model proxy and git proxy | synthetic |
| M19 | Revoked operator access | after rotating the operator token with a plane restart, the old token gets 401 on read, read -follow, and attach | live |
| M20 | Ownership | one Rusui database holds policy revisions, grants, and receipts; the guest holds only its turn grant | live |

Core repetitions: M1, M2, M3, and M8 are run three times on three fresh
sessions before the full pass. Any repetition failure scores as that
cell's failure.

Record per cell: result; the ids observed; elapsed time where the cell
has a wait; operator steps and any intervention; real guest call versus
injected fault; failures verbatim (sanitized).

## Resolved decisions

1. Host: a Linux host with native Docker (not the #186 macOS host).
2. Model route: `claude-sonnet-5` through the operator's CLI proxy.
3. Test repository: `Sannrox/rusui` (public), read-only, `implement: false`.
4. M14 may use the shipped restore path as the expiry trigger.
5. Thresholds and three core repetitions as written above.
6. GitHub intake token: the operator's existing token, which is
   write-scoped, instead of a read-only token. Recorded as a profile
   deviation; the intake client is read-only and no guest receives it.
