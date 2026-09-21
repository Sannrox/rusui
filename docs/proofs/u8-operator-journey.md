# U8 self-hosting operator journey

Investigation for [#118](https://github.com/Sannrox/rusui/issues/118).
The scenario list was predeclared before execution. No independent
self-hosting maintainer was available.

## Supported claim

**In:** one-operator loopback plane. The shipped HTTP/CLI/console/ACP
paths for install-check, dispatch, disconnect, inspect, approve,
follow-up, editor load, terminal observer page, preview grant mint,
drain, and SQLite restore. Same session identity across those actions.

**Out:** independent-maintainer external-user readiness, hosted SaaS,
team operators, live GitHub writes, Grok ACP inside a guest image.

Objects: session, turn, approval decision, console operator cookie,
ACP editor shim, terminal observer, preview grant, drain report,
restore report.
Evidence: `TestU8OperatorJourney` drives the shipped `Server.Handler`
and `store.Restore`. `GET /healthz` is not treated as topology ready.
Permitted action: **narrow** the self-hosting claim to this in-process
author exercise.
Policy: fail closed — missing independent participant is not external
readiness. A 200 on `/healthz` is not a successful diagnose.

## Recorded execution

| Field | Value |
| --- | --- |
| Baseline SHA | `861eacecfc2a70441c91b1e2d0b68861ce0d76c8` (`origin/main`) |
| Candidate | this commit |
| Commands | `go test ./internal/server -run ^TestU8OperatorJourney$ -count=1`; `make test`; `make validate` |
| Independent participant | none |
| Limitation | clean-machine author run only; not external-user evidence |

Credentials and private payloads are not retained.

## Scenario matrix

| ID | Scenario | Expected | Result |
| --- | --- | --- | --- |
| J01 | `GET /healthz` | process listening | pass |
| J02 | `GET /readyz` without full topology | not a secret dump; may be unready | pass |
| J03 | dispatch run session | `session_id` created | pass |
| J04 | inspect session | same prompt/id | pass |
| J05 | attach SSE then disconnect | follow-up still works | pass |
| J06 | follow-up | `turn_id` | pass |
| J07 | approve deny | 204 | pass |
| J08 | console inspect | same prompt | pass |
| J09 | `rusui acp` HTTP plane load | same session id | pass |
| J10 | terminal observer page | 200 | pass |
| J11 | mint preview grant | `?g=` token | pass |
| J12 | drain | 200 | pass |
| J13 | restore SQLite copy | same session id and kind survive | pass |

## Decision

**Narrow.** Documented operator APIs work on one loopback plane in this
repository. Self-hosting is **not** claimed ready for an independent
maintainer until that trial is run. Follow-up: recruit an independent
operator with a non-sensitive test repository; do not reopen U3–U6 for
this gap.
