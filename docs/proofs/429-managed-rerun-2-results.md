# #429 managed remote-session rerun: second results

Second run of the matrix predeclared in
[429-managed-rerun-matrix.md](429-managed-rerun-matrix.md), unchanged:
same cells, thresholds, policy (sha256 `831782f5…`), guest image, model
route, and host. The re-run gate in the
[first results](429-managed-rerun-results.md) asked for a new candidate
that resolves #435 and the withheld finding.

| Field | Value |
| --- | --- |
| Candidate | `32494cdaa0d017b3bc9293bd8746821c756f6834` (guest link, [ADR 0047](../decisions/0047-guest-link.md)) |
| Host | Linux (kernel 7.2), native Docker 29.7.2 |
| Guest image | `sha256:84c7c6f0a83bcb4840d9a49d3bb7dc1cd76a0bf4d52e4e76afc969c91490e1b1`, Claude Code 2.1.283 |
| Model route | `claude-sonnet-5` through the operator's CLI proxy |
| Plane | eval plane on `127.0.0.1:8284`, eval-only CA, fresh database, `-env-idle-sleep 2m` |
| Pre-run check | `rusui diagnose`: every check ready, including `guest_link` |
| Date | 2026-10-02 |

Profile deviation as before: a write-scoped intake token, used only for
read-only intake and given to no guest.

## Decision: narrow

Every identity, authorization, and ownership cell passed. Two
non-identity transitions failed (M2, M11), within the narrow bound of
two. Each has a bounded defect issue.

Supported managed profile after this run: native Linux Docker, the
reference Claude guest, terminal operation through `rusui run`,
`read -follow`, `attach`, `prompt`, `approve`, `drain`, `resume`, and
`restore`, **excluding** a workspace diff in `rusui read` for container
sessions (M2) and prompt release of a cancelled turn's guest and runner
(M11). Docker Desktop on macOS is not covered by this run.

## Matrix

| # | Result | Evidence |
| --- | --- | --- |
| M1 Start | pass, live | three fresh sessions; each created in 1 s and completed its first turn in 14–20 s |
| M2 Read | **fail**, live | prompt and transcript shown; the diff is always `file inspection is process-workspace only` for a container session, so the workspace diff is never shown (#440) |
| M3 Follow | pass, live | `read -follow` showed history, `waiting` on each approval, and `completed`, exit 0, in all three repetitions |
| M4 Disconnect during follow | pass, live | follow client killed with SIGKILL mid-turn; the turn completed in the gap; a new `read -follow` printed every entry once and the outcome, matching plain `read` |
| M5 Attach | pass, live | write lease acquired on session 4, environment 5; typed command ran in the guest |
| M6 Detach and reconnect | pass, live | Ctrl+D detached with exit 0; a second attach reached session 4, environment 5, a new lease generation |
| M7 Second client | pass, live | a concurrent attach was told the write lease is held and connected read-only; its input did not run; the first client's did |
| M8 Follow-up | pass, live | revision 2 on the same session and environment in all three repetitions |
| M9 Approval allow | pass, live | an unmatched shell request showed `waiting`; `rusui approve -allow` let the turn continue and complete |
| M10 Approval deny | pass, live | `rusui approve -deny` returned "denied by rusui policy" to the guest; the turn ended with a recorded `blocked` verdict. See the observation on auto-allowed commands below |
| M11 Cancellation | **fail**, synthetic trigger | `POST /sessions/{id}/cancel` (no CLI verb) ended the turn in 240 ms and the session reported `cancelled`, and the guest's model calls were refused afterwards. The guest harness kept running and the runner stayed on the cancelled turn for about 2 min 45 s, until idle sleep stopped the container; this delayed the next session's turn by about 173 s (#441) |
| M12 Sleep and wake | pass, live | `sleep` receipt after idle; `rusui prompt` woke environment 2 with a `wake` receipt, cause `operator prompt`, same id |
| M13 Plane restart | pass, live | plane process stopped and restarted (new pid); the next turn ran on the same session and environment 4 |
| M14 Environment replacement | pass, live | `drain`, stop, copy, `restore`, start: environment 3 got `expire` ("restored copy does not adopt the source guest"); the next turn ran on new environment 10 with a `replace` receipt naming it |
| M15 Operator recovery | pass, live | after the restore, `sessions` (8), `read`, and `logs` were consistent; a prompt was refused while paused (`409 paused`); `rusui resume` reopened admission without Slack |
| M16 Identity | pass | session, turn, environment, and receipt ids stayed attributable through M1 to M15 |
| M17 Policy denial | pass, synthetic | a run on a project without `run`: `409 policy` |
| M18 Credential refusal | pass, synthetic | a turn grant captured from the guest returned 200 at the model and git proxies during the turn and 401 at both after it |
| M19 Revoked operator access | pass, live | after rotating the operator token with a plane restart, the old token got 401 on `read`, `read -follow`, and `attach` |
| M20 Ownership | pass, live | the guest environment held only per-turn grant variables; neither the provider key nor the GitHub token appeared in it |
| Core repetitions | pass | M1, M2 (transcript part), M3, M8 three times each |

## Operator steps and interventions

- 16 approvals were granted with `rusui approve -allow`; most were the
  guest writing its result file with a shell command, which the eval
  policy holds for approval.
- The first M13 and M14 attempts were invalid: the harness failed to
  stop the old plane, so the "restart" never happened and the restore
  moved the database under a live plane (SQLite error 1032). Both cells
  were rerun after every plane and runner process was confirmed stopped;
  only the reruns are scored.
- M4 timing: the turn finished before the reconnect, so the gap held the
  outcome rather than new entries; in-connection resume after a cursor is
  covered by the #430 integration tests.

## Observations

- **Auto-allowed shell commands.** Claude Code ran `ls`, `cat`, and
  `uname -a` without a permission request, although the policy has no
  rule for `execute`. Its own read-only command list bypasses the plane's
  approval for those commands. Recorded as #442.
- **Transcript content.** For the Claude guest the durable transcript
  holds permission and approval entries, not tool calls or messages, so
  `read` and `read -follow` show little of what the agent did.

## Findings

| Issue | Finding |
| --- | --- |
| #440 | `rusui read` never shows a workspace diff for container sessions |
| #441 | Cancelling a managed turn does not stop the guest or release the runner |
| #442 | The Claude guest runs read-only shell commands without a plane permission request |
