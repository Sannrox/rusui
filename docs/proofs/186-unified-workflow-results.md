# #186 unified workflow: results

Candidate: `main` `1003bf6` plus this results record.
Campaign date: 2026-09-29.
Live predeclared profile: Sumika binary `3dadc7a` was **not present** on
the host (`sumika` not in `PATH`). Docker was available. No eval plane
on `127.0.0.1:8282` was started. No live GitHub write.

Decision: **defer**. See
[ADR 0045](../decisions/0045-local-unattended-promotion-deferred.md).

Evidence labels: `synthetic` = package tests at this revision.
`not-run` = predeclared live cell not executed.

## Matrix outcomes

| # | Transition | L | M | Evidence |
| --- | --- | --- | --- | --- |
| 1 | Start | synthetic pass | not-run | `TestLocalRuntimeHTTPAndSumikaLifecycle` POST `/projects/test/sessions` `{"kind":"local"}` returns 201 and a running Process named `rusui-<id>`. Managed live `rusui run` not started. |
| 2 | First interaction | synthetic pass | not-run | Fake Sumika attach yields raw `pty-ready` bytes; Rusui does not store them. Managed `rusui read` not started. |
| 3 | Detach | synthetic pass | not-run | Attach row becomes `detached`; Sumika status stays blocked. |
| 4 | Reconnect | synthetic pass | not-run | A later attach reaches the same Process id. |
| 5 | Attach steal | synthetic pass | n/a | Second attach generation 2; first recorded `stolen`. |
| 6 | Follow-up Turn | n/a | not-run | Local has no Turns. Managed prompt not started live. |
| 7 | Cancellation | not-run | not-run | Missing-session cancel is 404 in tests. Live SIGTERM and managed turn cancel were not run. |
| 8 | Process death | synthetic pass | n/a | Daemon reports dead; reconcile records it. |
| 9 | Runtime restart | not-run | not-run | No Sumika daemon or eval-plane restart in this campaign. |
| 10 | Operator-visible recovery | not-run | not-run | Depends on rows 8–9 live. |
| 11 | Identity | synthetic pass | not-run | Session id, Process name, attach generations stay consistent in the test. Live dual-profile identity not measured. |
| 12 | Policy denial | not-run | not-run | Create with request-supplied argv is 400 in tests. Live create on a project that does not allow `local` / `run` was not run. |
| 13 | Credential refusal | not-run | not-run | Operator bearer cannot restart a local Process (401) in tests. Wrong-token create/attach and expired turn-grant refusal were not run as predeclared. |
| 14 | Stale Process report | not-run | n/a | Policy reload leaves Process identity unchanged in tests. Generation-1 vs generation-2 after an explicit restart was not run. |
| 15 | Environment replacement | n/a | not-run | Local has no replaceable Environment. |
| 16 | Revoked operator access | not-run | not-run | Token rotation not exercised live. |
| 17 | Ownership | synthetic pass | synthetic pass | Session detail leaks no argv/cwd/identity_hash. Rusui stores no PTY bytes. One store in tests. |
| 18 | Install and recovery | fail | not-run | Predeclared Sumika build was not installed. Drain/copy/restart of an eval plane was not run. |

## Operator effort

No live operator minutes on the predeclared eval plane. Synthetic tests
ran in `make test WHAT=./internal/server TEST_ARGS='-run TestLocalRuntimeHTTPAndSumikaLifecycle'`
at this candidate.

## Why not pass or narrow

The predeclared **pass** bar needs every applicable live cell in both
profiles. The local Sumika binary named in the matrix was missing, so
the live local profile did not run. Incomplete live evidence is not a
narrow pass.
