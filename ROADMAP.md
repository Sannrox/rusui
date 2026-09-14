# Roadmap

Sequencing context for [ADR 0001](docs/decisions/0001-environment-plane.md),
[ADR 0002](docs/decisions/0002-grok-acp-agent-set.md), and
[VISION.md](VISION.md). Phases, effort, and quarters are estimates for one
developer with agent assistance (about 45 productive weeks per year, plus or
minus 50 percent). They are not deadlines, priorities, or release commitments.
GitHub Issues remain the planning source of truth; this file links to them
and never overrides their `## Dependencies` sections.

Sources for competitive claims: Amp Orbs documentation (ampcode.com/docs,
read 2026-09-13), Claude Code cloud environment documentation (read
2026-09-13), OpenHands documentation, and the rusui source at
[b1ad9ba](https://github.com/Sannrox/rusui/tree/b1ad9ba000fab1a0454d58ecb29493a4274bf182).
Claims without a source are marked as assumptions.

## Shape

| Phase | Window | Effort | Goal |
| --- | --- | --- | --- |
| P0 Reset and wire | Q4 2026 | 6 weeks | Make the existing engine true in production, break the identity, prove the two bets (ACP, runner protocol) before building environments. |
| P1 Environments v1 | Q1–Q2 2027 | 18 weeks | Every session gets its own container environment prepared from a project snapshot; credentials are brokered; a human can watch and steer from CLI or browser. |
| P2 Orb parity on your metal | Q3–Q4 2027 | 24 weeks | MicroVM environments that sleep and wake from snapshots, portals, agent to agent, and a ship lane that publishes only proven work. |
| P3 Scale, governance, fleet | Q1–Q2 2028 | 26 weeks | Many runners, optional HA, governance through sekai-chisei, live fork, shared sessions. |
| P4 Ecosystem and 1.0 | Q3 2028 | 20 weeks | Extension points, offline operation, editor integration, a frozen 1.0 contract. |

Sequencing rationale: P0 and P1 close the table-stakes gaps that make rusui
unusable as an environment plane (a machine per session, credentials, a live
view) and ship a tracer of the wedge to one user, the maintainer. P2 matches
the rival edges worth matching and lays the moat (receipts on every action,
proofs before publish, governance adapter). P3 and P4 are cheap parity and
bets that need earlier evidence.

## P0 Reset and wire (Q4 2026, 6 weeks)

Gate: Grok ACP headless viability recorded in
[ADR 0002](docs/decisions/0002-grok-acp-agent-set.md); a KVM-capable host
identified.

| Item | Effort | Success measure | Kill signal | Depends on | Issues |
| --- | --- | --- | --- | --- | --- |
| P0.1 Production wiring and fail-closed fixes | 1 wk | Recovery paths run at startup and periodically; secrets unset means refuse to start; head-of-line claim fixed; toolchain aligned | None: mandatory | — | [#2](https://github.com/Sannrox/rusui/issues/2), [#3](https://github.com/Sannrox/rusui/issues/3), [#4](https://github.com/Sannrox/rusui/issues/4), [#5](https://github.com/Sannrox/rusui/issues/5) |
| P0.2 Domain model v2 with versioned migrations; port lease, receipt, and fencing code to session, turn, environment identities | 2 wks | All scenario tests pass against the new schema; N parallel turns per project | If porting exceeds 3 weeks, write the v2 store fresh and keep the old tests as the spec | P0.1, ADR 0001 accepted | [#8](https://github.com/Sannrox/rusui/issues/8) |
| P0.3 ACP viability decision, then `internal/acp` client with conformance harness | 2 wks | Decision names Grok CLI as the P1 guest; one prompt round-trips headless through `agent --permission-mode default agent stdio`; 24 h soak on that spawn | Grok drops stdio ACP or stops emitting `session/request_permission` for shell: re-plan P1 around shikigami `serve` plus API-key agents | — | [#6](https://github.com/Sannrox/rusui/issues/6), [#10](https://github.com/Sannrox/rusui/issues/10) |
| P0.4 Runner protocol v1 and `rusui-runner` with the process driver (heartbeat, deadline kill, env allowlist) | 1.5 wks | Plane on a VPS, runner on a laptop behind NAT; a session completes and survives a plane restart | None | P0.2 | [#9](https://github.com/Sannrox/rusui/issues/9), supersedes [#11](https://github.com/Sannrox/rusui/issues/11) |
| P0.5 Operator surface decision | 2 wks, parallel | Surfaces are views of one object API: HTTP+SSE, CLI first, ACP facade, ten-view Go/htmx/SSE console, Slack as notify/approve only; auth is a token plus identity-aware proxy ([ADR 0003](docs/decisions/0003-operator-surface.md)) | None | — | [#7](https://github.com/Sannrox/rusui/issues/7) |

## P1 Environments v1 (Q1–Q2 2027, 18 weeks)

Gate: rusui runs its own maintenance and one sibling repository's backlog lane
unattended for 30 days at 20 or more sessions per week with zero credential
leaks.

| Item | Effort | Success measure | Kill signal | Depends on | Issues |
| --- | --- | --- | --- | --- | --- |
| P1.1 Container driver (Docker/Podman API): base image, `.agents/setup` to project snapshot, sleep = stop, wake = start plus `.agents/resume`, 72 h TTL GC, CPU and memory sizes | 3 wks | Fresh environment from snapshot in under 10 s; setup runs once per source hash | None | P0.4 | [#23](https://github.com/Sannrox/rusui/issues/23), [#24](https://github.com/Sannrox/rusui/issues/24) |
| P1.2 `rusui-guest`: hooks, ACP host, PTY/tmux terminal server, `services.yaml` supervisor, event stream, artifact capture | 3 wks | Terminal shared with the agent; diff and files visible live; services restart on wake | None | P0.3, P1.1 | [#28](https://github.com/Sannrox/rusui/issues/28), [#31](https://github.com/Sannrox/rusui/issues/31), [#33](https://github.com/Sannrox/rusui/issues/33) |
| P1.3 Credential broker: GitHub App installations, per-turn grants, git smart-HTTP proxy with ref allowlist, API proxy, egress classes | 3 wks | No long-lived token found in any environment; push outside `refs/heads/rusui/<session>/*` rejected by the proxy | Receive-pack parsing unreliable: fall back to GitHub rulesets plus post-push verification and record the gap | P1.1 | — |
| P1.4 HTTP API with SSE, `rusui` CLI (run, sessions, attach, sync via shadow refs, logs), embedded console v1, ACP facade spike | 4 wks | `rusui run` from a laptop, watch in the browser, `rusui sync` mirrors the session branch into a local worktree; one editor attaches through ACP | None | P1.2, P0.5 | — |
| P1.5 Generalized event intake: GitHub App webhooks, generic signed endpoints, store-then-202, at-least-once with backoff, 24 h drop, fenced scheduler; review workflow ported onto triggers | 3 wks | A push and a cron both wake the right session exactly once under duplicate deliveries and a plane crash mid-handling | None | P0.2 | [#27](https://github.com/Sannrox/rusui/issues/27) |
| P1.6 Budgets and accounting: minutes, tokens, dollars per project per day; stop conditions to Slack, email, webhook adapters | 2 wks | A runaway session stops at the budget and the operator is notified once | None | P1.4 | — |

## P2 Orb parity on your metal (Q3–Q4 2027, 24 weeks)

Gate: wake under 2 s from snapshot; 20 idle environments cost about zero CPU;
a pull request opens only with a recorded proof.

| Item | Effort | Success measure | Kill signal | Depends on |
| --- | --- | --- | --- | --- |
| P2.1 Firecracker driver on KVM: rootfs from OCI image, vsock guest channel, tap plus nftables egress, memory and disk snapshots, lazy restore, warm pool; two-week spike evaluating open-source microVM runtimes first | 8 wks | Wake p95 under 2 s; restore across plane restart; egress default-deny verified | No KVM host: stay on containers plus gVisor, try CRIU, accept the parity gap | P1.1 |
| P2.2 Portals: `https://<session>-p<port>.<domain>` through the runner tunnel; plane-session auth; identity headers; time-boxed public links | 3 wks | A dev server declared in `services.yaml` opens from the console and a phone | None | P1.2, P1.4 |
| P2.3 Agent to agent: in-environment MCP server exposing spawn, message, wait, transfer, schedule, portal; lineage; fan-out | 3 wks | One session fans out four children and collects results; lineage visible | None | P1.2, P1.5 |
| P2.4 Ship lane: proofs run after the agent exits without publish credentials; publish consumes the exact artifact; PR open and update through the action executor; optional merge under policy | 3 wks | Zero pushes without a recorded proof digest matching `head_sha` | None | P1.3 |
| P2.5 Live actions for the review workflow (comment, then close) gated by the promotion table on the eval set | 2 wks | 0 false closes on the eval set per reason before any live close | A reason cannot reach 0 false closes: it stays advisory | P1.5 |
| P2.6 Console v2: projects, runners, policy, approvals inbox for `request_permission` and parked turns; terminal; diff; responsive | 3 wks | An approval answered from a phone resumes the turn within one heartbeat | None | P1.4 |
| P2.7 Hardening: jailer and seccomp, secret redaction in the event log, signed receipts, per-runner revocation, rate limits, SBOM and signing through tenkai | 2 wks | Threat model document; redaction corpus passes; revoked runner cannot claim | None | P2.1 |

## P3 Scale, governance, fleet (Q1–Q2 2028, 26 weeks)

Gate: 3 runners across 2 hosts; snapshots restore on a different host; the
plane fails over without losing a turn.

| Item | Effort | Success measure | Kill signal | Depends on |
| --- | --- | --- | --- | --- |
| P3.1 Multi-runner scheduling by size, labels, snapshot locality; S3-compatible snapshot store; cross-host restore; drain and upgrade | 6 wks | Environment created on host A wakes on host B after A drains | None | P2.1 |
| P3.2 Postgres backend and HA plane (leader lease for scheduler and reaper); OpenTelemetry; SLOs for wake and dispatch | 5 wks | Kill the leader mid-turn: the turn completes under the same lease generation on the new leader | Single-operator use never needs it: keep SQLite | P0.2 |
| P3.3 sekai-chisei governance adapter with fail-closed mode; minimal local RBAC | 5 wks | A denied action is refused by the plane with a receipt on both sides | None | P2.4 |
| P3.4 Live fork for best-of-N; Kubernetes agent-sandbox driver or conformance facade; provisioners for bare metal and cloud `.metal` | 6 wks | Fork into 4 siblings under 100 ms each; a cluster hosts runners without code changes | Fork adds no throughput in dogfood: keep snapshot-and-start | P2.1 |
| P3.5 Multiplayer-lite: share a session read or write, shared terminal, time-boxed | 2 wks | Two browsers type into one terminal; access expires | None | P3.3 |
| P3.6 Eval harness for workflows and agents: replayable sessions, regression gates before promotion | 2 wks | A regressing policy change cannot be promoted | None | P2.5 |

## P4 Ecosystem and 1.0 (Q3 2028, 20 weeks)

Gate: a contract document in the style of shikigami ADR 0004; external
security review closed; one external embedder.

| Item | Effort | Success measure | Kill signal | Depends on |
| --- | --- | --- | --- | --- |
| P4.1 Extension API for triggers, actions, notifiers, drivers (out-of-process gRPC plugins) | 6 wks | A third-party driver runs without a plane change | None | P3.4 |
| P4.2 Offline and air-gap bundles (signed, replay-safe, the tenkai pattern); local model gateway behind the egress proxy | 4 wks | A session runs with no internet using a local model | None | P2.7 |
| P4.3 Editor integration: rusui as a full ACP agent facade; onmyoji attaches as a client | 4 wks | An editor opens a session on the server and streams its tool calls | None | P2.3, P1.4 |
| P4.4 Security audit, threat model refresh, fuzzing of git proxy and webhook parsers, dependency policy; 1.0 contract freeze | 6 wks | No open high findings; frozen surfaces listed | None | P3.x |

## Assumptions

| # | Assumption | Verify | If false |
| --- | --- | --- | --- |
| A1 | Grok CLI keeps stdio ACP and `session/request_permission` under `--permission-mode default` with the operator's own login | [ADR 0002](docs/decisions/0002-grok-acp-agent-set.md); [#10](https://github.com/Sannrox/rusui/issues/10) soak | Drive shikigami through `serve`; P1 stands, positioning changes from "popular coding agents" to the family agent |
| A2 | A KVM-capable Linux host is available before P2 | `ls /dev/kvm` on the target | P2.1 collapses to containers plus gVisor; sleep and wake parity is not reached |
| A3 | GitHub App tokens plus a git smart-HTTP proxy can enforce repository and branch scope | One-week spike in P1.3 against a real push | Fall back to GitHub rulesets and post-push verification; record the residual risk |
| A4 | Amp keeps orbs closed to third-party agents and does not ship a self-hosted plane | Re-read the documentation quarterly | Compete on receipts, policy, and cost rather than self-hosting alone |
| A5 | shikigami accepts an ACP server through its ADR process | Open the ADR | Drive shikigami through its `serve` intake; weaker receipt capture |
| A6 | One developer with agents sustains about 45 productive weeks per year | Measure P0 against its estimate | Cut P3.5, P4.1, P4.3 first; gates still hold |
| A7 | Container stop and start is an acceptable sleep for P1 dogfood | 30-day dogfood in P1 | Pull P2.1 earlier |
| A8 | At least one open-source microVM runtime is reusable by mid-2027 | Two-week spike at the start of P2.1 | Build a minimal driver on firecracker-go-sdk; add 3 to 4 weeks |

## Counter-moves

| Move | Who | Likelihood | Response |
| --- | --- | --- | --- |
| Open the orb API to third-party agents or ship a self-hosted plane | Amp | Low: cannibalizes metered compute and their agent | Stay agent-agnostic and self-hosted; if Amp ships ACP, its agent becomes one more agent rusui runs |
| Add microVM backends, webhooks, and portals to an open-source canvas | OpenHands | Medium: open source, ACP, remote backends exist | Pull P2.1 ahead of P2.2 and P2.3; interoperate through ACP both ways |
| Restrict headless third-party use of Grok ACP | xAI | Medium | shikigami `serve`, later local model gateway (P4.2); [#10](https://github.com/Sannrox/rusui/issues/10) soak is the remaining gate |
| Kubernetes agent-sandbox becomes the standard sandbox API | k8s-sigs | Medium | Driver plus conformance facade in P3.4; the plane never depends on one driver |
| Self-hosted runners in the customer's VPC with the vendor's plane | Cursor, Claude Code | High: already shipping | The differentiator is the whole plane on the operator's side plus agent choice, receipts, and policy |
