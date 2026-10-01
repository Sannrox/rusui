# #429 managed remote-session rerun: results

Results for the matrix predeclared in
[429-managed-rerun-matrix.md](429-managed-rerun-matrix.md), scored
against the candidate `main` at `fb13ce19abc955dbd19c5db61c277fe327320190`.
The campaign ran on 2026-10-01 from 19:45 to 19:59 UTC. Machine and
account identifiers are omitted. Evidence labels as in the matrix.

## Decision: defer

The managed profile failed M1 (Start) live on the candidate, so no later
transition could be reached. The confirmed thresholds map any required
cell `not run` to **defer**. The same run found a separate defect
that [SECURITY.md](../../SECURITY.md) handles privately; it is withheld
here until it is reported and fixed. Under the thresholds it would be a
defer on its own.

No managed profile is supported by this campaign. The supported claim
stays where [#186](186-unified-workflow-results.md) left it. **Re-run
gate:** resolve #435 and the withheld finding, predeclare a new
candidate, and rerun the whole matrix.

## Recorded profile

| Field | Value |
| --- | --- |
| Candidate | `fb13ce19abc955dbd19c5db61c277fe327320190` |
| Host | Linux (kernel 7.2), native Docker 29.7.2 |
| Guest image | `make guest-image` at the candidate, image id `sha256:84c7c6f0a83bcb4840d9a49d3bb7dc1cd76a0bf4d52e4e76afc969c91490e1b1`, Claude Code 2.1.283 |
| Model route | `claude-sonnet-5` through the operator's CLI proxy; `rusui diagnose` reported `model_upstream: ready` and `model_guest: ready` |
| Plane | eval plane on `127.0.0.1:8282`, plane TLS from an eval-only CA, fresh database, `-env-idle-sleep 2m` |
| Policy | sha256 `831782f5d07caac5b117aa837073cced9a5892b989c088338e1e95bd8b049b6e` |
| Pre-run check | `rusui diagnose`: every check `ready` once the plane was up; `/readyz` ready |

Profile deviation: the GitHub intake token was write-scoped, not
read-only (matrix decision 6). It was used only for read-only intake and
was not given to any guest.

## Matrix

| # | Result | Evidence |
| --- | --- | --- |
| M1 Start | **fail**, live | `rusui run -project eval PROMPT` created session 1, environment 2, turn 1, and the runner claimed the turn at once. The Claude guest started (`claude --print … --permission-prompt-tool stdio`) but recorded no transcript event in 13 minutes. Its only TCP socket was `SYN_SENT` to the Docker bridge gateway on the plane port: `rusui.plane` maps to `host-gateway`, which on native Linux Docker is the `docker0` address, while the plane listens on loopback (#435). Nothing reported the failure; the turn would have waited for the execution deadline. |
| M2 – M16 | not run | blocked by M1 |
| M17 Policy denial | not run | blocked by M1 (the campaign was stopped before the synthetic rows) |
| M18 Credential refusal | not run | blocked by M1 |
| M19 Revoked access | not run | blocked by M1 |
| M20 Ownership | not run | blocked by M1 |
| Core repetitions | not run | blocked by M1 |

## Operator steps and interventions

- `rusui run` with `-repo -ref -base-sha` but no `-effort` and `-paths`
  answered `409 task blocked: missing pin` twice. That was an invocation
  error (a pinned task needs all five), not a product failure. The plain
  `rusui run -project eval PROMPT` form, as in #186 M1, was used.
- While M1 hung, `rusui read -follow 1` showed the session header and
  `state: running (turn 1 revision 1)` and nothing else, matching the
  durable state. After the session was cancelled (`POST
  /sessions/1/cancel`, synthetic), it printed `state: cancelled (turn 1
  revision 1)` and exited: the first live-guest observation of the
  follow stream (#430), outside the scored cells.
- A host-side listener on either bridge gateway was not reachable from
  the guest either; the host firewall drops guest-to-host traffic.
  Supplementary post-workaround rows would need a host firewall change,
  which was not made.
- The eval guest container was removed after the session was cancelled;
  the plane and runner were stopped.

## Findings

| Issue | Finding |
| --- | --- |
| #435 | Managed guests cannot reach a loopback plane on native Linux Docker; the turn waits silently until its deadline and `diagnose` stays ready. |
| withheld | Handled under SECURITY.md. |
