# D6 session-workflow proof

Investigation for [#95](https://github.com/Sannrox/rusui/issues/95).
The twenty-run matrix was predeclared before execution. Review-kind runs
prove lifecycle only, not review quality. Live GitHub mutation is out of
scope.

## Supported claim

**In:** one-runner container stop/start (Docker or Podman CLI), plane
session/turn/grant/lease/follow-up/cancel/restore, three session kinds
(review, run, scheduled), HTTP approval deny, workspace marker identity
across sleep/wake.

**Out:** Grok ACP inside a guest image (no in-tree linux guest with Grok),
thirty-day soak, live GitHub writes, uninterrupted SSE delivery, semantic
review quality.

Objects: session, turn, environment handle, per-turn grant, lease
generation, follow-up queue, approval decision, restore report.
Evidence: `TestD6SessionWorkflowMatrix` (shipped engine, store.Restore,
HTTP `/approvals/{id}`) and `TestLiveContainerProcessIdentity` (shipped
`env.Container` + live runtime when Docker/Podman is present).
Permitted action: declare M1 lifecycle proven under that claim.
Policy: fail closed if the container runtime is missing (live test skips;
it does not pass). Unavailable model credentials are not a live guest
proof. A missing restore artifact is not a successful restore.

## Recorded execution

| Field | Value |
| --- | --- |
| Baseline SHA | `0e222f1b13d9b4fc253e335a2cda653f1cda2f61` (`origin/main`) |
| Candidate | this commit (file introduced with the matrix and live test) |
| Commands | `go test ./internal/engine -run ^TestD6SessionWorkflowMatrix$ -count=1`; `go test ./internal/env -run ^TestLiveContainerProcessIdentity$ -count=1`; `make test`; `make validate` |
| Guest | none (no in-tree linux guest with Grok) |
| Image | `alpine:3.20` digest `sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc` |
| Runtime | Docker 29.5.2, Ubuntu 24.04.4 LTS (Colima VM) |
| Live duration | `TestLiveContainerProcessIdentity` ~11s (10.63s then 10.88s on this host) |
| Artifact SHA-256 | `docs/proofs/d6-session-workflow.md` hashed after this table; tests `7c58f0e8d77f8b5079690dcf3a5fb7b083632e60ff585e83fda1bc28893bde6c` (`session_matrix_test.go`), `7872b12ca2dfc9c7e8a6731d9a16b801888ae3a013ac339a2d65d2a6fd21a789` (`live_container_test.go`) |
| Limitations | live proof is process identity + `/workspace` marker persist, not a Grok ACP guest; reconnect accounts for later claims, not gapless SSE; D7 clean-install diagnostics remain those already delivered on main |

Credentials and private payloads are not retained.

## Twenty-run matrix

| Run | Kind | Fault / steer | Expected | Result |
| --- | --- | --- | --- | --- |
| R01 | review | claim + complete | receipt; leased then complete | pass |
| R02 | run | StartRun + claim | lane `run` | pass |
| R03 | scheduled | CreateSchedule + StepSchedules | a scheduled turn appears | pass |
| R04–R13 | run | ten FIFO follow-ups | claims in enqueue order | pass |
| R14 | run | attach SSE then disconnect | later claim still works | pass |
| R15 | run | VACUUM copy + `store.Restore` + Recover | session survives; leases/grants stripped; turn queued | pass |
| R16 | review | HTTP deny then lease expire without heartbeat | GET `/approvals/{id}` shows deny; complete rejected | pass |
| R17 | review | duplicate delivery id | one `deliveries` row | pass |
| R18 | scheduled | StepSchedules while live | skip; one scheduled session | pass |
| R19 | run | cancel leased turn | complete rejected | pass |
| R20 | review | concurrent-lease cap | second claim `ErrBudget` | pass |

R04–R13 are ten follow-up claims on one run session (combined scenario).
R15 is the backup/restore interruption path. R16 combines approval denial
with grant/lease expiry (runner loss without heartbeat).

## Remaining gaps (narrowed, not deferred-as-pass)

- Grok ACP inside a guest image: out. No linux guest image in-tree.
- Plane process restart of `cmd/rusui` as a live binary, as opposed to
  `store.Restore` of a consistent SQLite copy: not claimed.
- Uninterrupted network delivery of SSE: not claimed; reconnect may
  continue the session.
- D7 packaged clean-install walk-through is already on main; this
  investigation does not re-package images.
