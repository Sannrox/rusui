# #186 unified local and managed workflow: results

> **Historical evidence.** This record applies to its stated run and revision;
> it is not a current operator procedure.

Results for the matrix predeclared in
[186-unified-workflow-matrix.md](186-unified-workflow-matrix.md)
(committed `7e507a8`, before the first run). Scored against the confirmed
candidate `main` at `1003bf6`. Managed rows marked **post-fix** ran on
`main` at `6fe0bfe` after defects found here were fixed. They are
supplementary evidence and are not scored against the candidate.

This record supersedes the first results file merged in #395, which was
written while the live campaign was still running and reported every live
cell as not run. The live campaign ran against the same candidate from
08:18 UTC. The decision (defer) is unchanged; its grounds are now two
measured identity failures instead of missing evidence.

Machine and account identifiers are omitted. Evidence labels: `live`
(real Sumika, real container, real model call) and `synthetic` (injected
fault or wrong credential).

## Decision: defer

Both profiles have an identity failure, and the confirmed thresholds map
any identity, authorization, or ownership failure to **defer**:

- **Local, L11.** The local lifecycle writes no receipt or event (#414).
- **Managed, M15 (post-fix).** An expired environment is re-provisioned
  under the same environment id with no receipt (#415).

At the candidate, the managed profile also failed M1 and M2 outright
(#396, #406), so it could not reach its other transitions there.

The experimental local profile stays experimental and outside the
supported 1.0 core ([ADR 0045](../decisions/0045-local-unattended-promotion-deferred.md)).
No product contract changes. For #141 this means the local
profile is not a 1.0 candidate. The managed transitions that passed
post-fix (M1 to M7, M9, M13, M16 to M18) are the evidence to cite.

**Re-run gate.** Resolve #414 and #415, predeclare a new candidate, and
rerun the whole matrix. #416 and #417 are robustness and operator gaps
that should land first but do not change a cell's identity outcome.

## Local profile (Sumika)

| # | Result | Evidence |
| --- | --- | --- |
| L1 Start | pass, live | 201 in about 27 ms; session 1, environment 1, Process 1 generation 1 running |
| L2 First interaction | pass, live | typed command ran in the shell and returned through attach |
| L3 Detach | pass, live | `Ctrl+]` exit 0; Process kept running; attach row `detached` |
| L4 Reconnect | pass, live | reached Process 1 generation 1, same shell PID |
| L5 Attach steal | pass, live | new attach took over; old client told "local PTY disconnected"; attach row `stolen`; same PID; see #416 |
| L6 Follow-up Turn | n/a | local sessions have no Turns |
| L7 Cancellation | pass, live | the interactive shell ignored SIGTERM; SIGKILL escalation about 105 s after cancel (30 s grace plus reconcile cadence); `cancelled` only after Sumika reported the Process dead |
| L8 Process death | pass, live | external SIGKILL seen as `dead` after about 25 s; no automatic restart; explicit restart created generation 2 |
| L9 Runtime restart | pass, live | with the daemon down: last-observed `running` until the next reconcile (about 30 s), then `unknown`; death never inferred; after return, `lost` |
| L10 Recovery | pass, live | operator saw the `dead`/`lost` history and restarted explicitly (generation 4) |
| L11 Identity | **fail** | Session, Environment, Process (per generation), and Attach (per Process and generation) attributable; **no receipt or event** for any local lifecycle transition (#414) |
| L12 Policy denial | pass, synthetic | `local` kind on a project without it: 409, no session |
| L13 Credential refusal | pass, synthetic | wrong token: 401 on create, read, and attach |
| L14 Stale Process report | pass, live | after restart and a full reconcile window, generation 1 stayed `dead` and generation 2 `running` |
| L15 Environment replacement | n/a | the local profile has no replaceable Environment |
| L16 Revoked operator access | pass, live | after rotating the operator token with a plane restart, the old token got 401 on read and attach |
| L17 Ownership | pass, live | typed markers absent from the Rusui database and WAL; Sumika config holds session presets only |
| L18 Install and recovery | pass, live | Sumika built from the baseline (`3dadc7a`, Rust 1.97.1); daemon on a private socket; drain, stop, copy, and restart kept every session. Restore and resume gaps: #417 |

## Managed profile (container, Claude guest)

| # | At `1003bf6` | Post-fix (`6fe0bfe`) |
| --- | --- | --- |
| M1 Start | **fail**: container name conflict with another plane (#406); then the guest produced no output until the 45 min deadline (#396) | pass: first turn completed in 16 s |
| M2 First interaction | **fail** (#396) | pass |
| M3 Detach | not reached | pass: write lease acquired, `Ctrl+D` detached |
| M4 Reconnect | not reached | pass |
| M5 Attach steal | n/a (write-lease rule) | second client read-only; its input did not run |
| M6 Follow-up Turn | not reached | pass: revision 2 on the same session and environment |
| M7 Cancellation | not reached | pass: live turn ended at once; session `cancelled` |
| M8 Process death | n/a | n/a |
| M9 Runtime restart | not reached | pass: after a plane restart the next turn ran on the same container |
| M10 Recovery | not reached | pass: `rusui sessions` and `rusui read` consistent after restart |
| M11 Identity | not reached | **fail** via M15 |
| M12 Policy denial | pass, synthetic: 409 | pass |
| M13 Credential refusal | not reached | pass: a finished turn's grant got 401 at the model proxy and git proxy |
| M14 Stale Process report | n/a | n/a |
| M15 Environment replacement | not reached | **fail**, synthetic expiry: access ended, but the next turn re-provisioned the same environment id with a new container and no receipt (#415) |
| M16 Revoked operator access | not reached | pass (same rotation as L16) |
| M17 Ownership | not reached | pass: one Rusui database holds policy revisions, grants, and receipts; the guest holds only its turn grant |
| M18 Install and recovery | not reached | pass: `rusui setup plan` created no files; the recovery path kept every session |

## Defects found and fixed during the campaign

| Issue | Defect | Fix |
| --- | --- | --- |
| #396 | Claude driver deadlock (init after first message), phantom init protocol field, no permission bridge | PR #397 |
| #398 | CLI streams over an `https` plane cut off after 3 s | PR #399 |
| #400 | advisory catch-up re-selected unchanged items | PR #402 |
| #401 | retry was Slack-only | PR #403 |
| #404 | `AskUserQuestion` parked headless turns on approval | PR #405 |
| #406 | container names collided across planes and replaced databases | PR #407 |

## Observations

- **Configuration.** The eval `rusui` project got permission kinds `read`,
  `search`, `edit`, and `execute` (the live policy's kinds) so run turns
  could write `$RUSUI_RESULT`. The predeclared profile did not fix
  permissions.
- **Placeholders.** Before #406, nine placeholder run sessions were created
  and cancelled in the eval database to step past the live plane's
  retained container names.
- **Unexplained.** On one plane process, a queued run turn was not
  claimed for at least 5 minutes while the runner polled. A goroutine dump
  showed no stuck environment operation, and a restart made the next
  claim succeed at once. Not reproduced.
- **Harness.** A zero-size pseudo-terminal makes local attach exit
  ("sumika resize requires … positive dimensions"). Real terminals have a
  size.
- **Latency.** Local cancel and runtime-restart state lag by up to one
  reconcile interval plus the 30 s grace.
