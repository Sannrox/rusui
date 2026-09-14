# ADR 0001: rusui becomes the environment plane for coding agents

- Status: Proposed
- Date: 2026-09-14
- Resolves: nothing yet. Tracks
  [#2](https://github.com/Sannrox/rusui/issues/2) to
  [#11](https://github.com/Sannrox/rusui/issues/11).
- Discussion: none. GitHub Discussions are disabled on this repository; the
  pull request that adds this file is the review venue, and merging it with
  status `Accepted` is the acceptance act.
- Related: [VISION.md](../../VISION.md), [ROADMAP.md](../../ROADMAP.md),
  [ARCHITECTURE.md](../../ARCHITECTURE.md) (the v1 contract this ADR
  eventually supersedes in part)

## Context

rusui is a GitHub maintenance bot keyed on `(repo, item, lane)`. Its core is
unusually rigorous for its size: webhook deliveries are acknowledged only
after a durable commit; per-item refreshes are fenced single-owner with
generations; review jobs are leased with heartbeats, receipts, and per-revision
retry budgets; policy is an immutable revision re-checked at apply time; every
proposed action carries an evidence class; more than fifty crash-recovery
scenarios are tested.

It has no concept of an environment, session, runner, sandbox, stream, or user
interface. The worker runs one fixed workflow through a bespoke Codex CLI file
contract on the operator's own machine, and the production binary does not
schedule the recovery paths the tests prove (#2).

The maintainer's intent is a self-hosted cloud development environment in the
class of Amp Orbs, Cursor cloud agents, Codex cloud, and OpenHands: give an
agent a whole machine, let it work for hours unattended, prove its work, hand
back a reviewable result, and never let code or secrets leave the operator's
perimeter. Sibling projects already cover the agent harness (shikigami), the
governance plane (sekai-chisei), delivery (tenkai), and a desktop shell
(onmyoji). None of them provides machines to agents.

The hard part of an unattended agent plane is exactly what the current engine
does well. The mistake would be to discard it because its identity is a GitHub
issue number.

## Decision

rusui becomes the **environment plane**: the layer that gives an agent a
machine, keeps it alive across sleep and wake, brokers credentials so no
secret enters it, wires events and schedules into sessions, and records every
external action. The engine's semantics are kept; its nouns change.

- **D1 Identity.** Environment, session, turn, runner, event, and action
  replace `(repo, item, lane)`. The v1 schema migrates in place through
  versioned migrations, not `CREATE IF NOT EXISTS` drift. Leases, receipts,
  generations, and policy binding keep their current semantics on the new
  identities. A lease belongs to a turn, so one live lease can no longer block
  a whole repository (#4, #8).
- **D2 Agent interface.** The Agent Client Protocol (ACP; JSON-RPC 2.0 over
  stdio) is the only agent interface. The plane never embeds a model loop.
  Because `fs/*` and `terminal/*` are client-side ACP methods, every tool call
  passes through code the plane owns and becomes a receipt;
  `session/request_permission` maps to policy decisions and human approvals.
  shikigami gains an ACP server; until then its `serve` intake is the interim
  path. Viability across Codex, Claude Code, and Gemini CLI under the
  operator's own accounts is a gate, not an assumption (#6).
- **D3 Runner protocol.** An outbound-only runner replaces `cmd/rusui-worker`
  and the `input.v1.json`/`output.v1.json` contract (#9, #11). A persistent
  control channel carries presence and dispatch; per-turn work channels carry
  a 32-byte credential with a ten-minute TTL, bound to one environment,
  hashed at rest. Runners open no inbound port.
- **D4 Environment drivers.** One driver interface with VM semantics from the
  first version (`Create`, `Exec`, `Snapshot`, `Restore`, `Pause`, `Resume`,
  `Fork`, `Destroy`, `PortForward`). Order of implementation: process,
  container (Docker/Podman), Firecracker on KVM. Sleep is a stopped container
  in the first version and a memory-plus-disk snapshot on microVMs later.
- **D5 Credentials.** No secret is ever written into an environment. GitHub
  App installation tokens replace personal access tokens. A git smart-HTTP
  proxy inside the plane exchanges a per-turn scoped credential for the real
  token, restricts repositories to the session's set, and rejects pushes to
  refs outside `refs/heads/rusui/<session>/*`. Model API keys traverse an
  egress proxy with a host allowlist. Egress classes `none`, `trusted`,
  `custom`, `full` are enforced by the driver.
- **D6 Operator surface.** API first (HTTP with SSE; gRPC for the family). A
  `rusui` CLI is the primary developer surface. rusui also exposes each
  session as an ACP agent so ACP-capable editors and onmyoji render the
  transcript, tool calls, diffs, and permission prompts natively. An embedded
  web console covers what editors cannot show: environments, runners,
  budgets, receipts, sessions, an approvals inbox, terminal, and portals;
  responsive so approvals work from a phone. Chat adapters (Slack first) carry
  notifications and approvals only (#7).
- **D7 Reachability.** Loopback-only stops being a design requirement and
  becomes an authentication model: local token, identity-aware proxy or
  tailnet, OIDC later. Secrets are required, never optional (#3).
- **D8 Persistence.** SQLite with WAL and one writer through the first year,
  behind a repository interface so Postgres and a leader-lease HA plane can be
  added later without touching the engine. Snapshots are content-addressed on
  local disk first, S3-compatible later.
- **D9 Governance.** Local policy v2 by default. When `governance.required` is
  set, decisions, budgets, approvals, and receipts go through sekai-chisei and
  the plane fails closed if it is unreachable, mirroring shikigami ADR 0001.
- **D10 Conventions.** Adopt `.agents/setup`, `.agents/resume`, `AGENTS.md`,
  and the `services.yaml` schema (`.rusui/services.yaml`, falling back to
  `.amp/services.yaml`) so repositories prepared for other planes work without
  changes.

The review bot survives as the first built-in workflow: issue and pull request
events trigger a review session, which emits actions with evidence classes,
applied dry-run or live per policy and the promotion table.

## Consequences

- Breaking changes to the schema, policy schema, worker contract, and Slack
  vocabulary. No users are affected; the only migration is the maintainer's
  own database.
- `ARCHITECTURE.md` is rewritten for the new nouns after acceptance; until
  then it remains the product contract and `AGENTS.md` keeps the v1/v2/v3
  language.
- Two languages in the family (Go here, Rust in shikigami and sekai-chisei);
  integration stays on gRPC and protobuf, and the same ADR discipline applies.
- The OS sandbox boundary must be assigned between rusui (environment
  isolation) and shikigami
  ([#282](https://github.com/Sannrox/shikigami/issues/282), tool-level
  sandbox) before Phase 1. The working assumption is that rusui isolates the
  machine and shikigami isolates tools inside it; both may hold.
- The maintainer needs a KVM-capable Linux host before Phase 2; without one,
  sleep and wake parity is not reached.

## Rejected alternatives

- **Status quo.** Finish v1 as a GitHub maintenance bot. Cheapest; does not
  reach the goal.
- **Fold the capability into shikigami or sekai-chisei.** Avoids a fourth
  service but puts machine lifecycle inside an agent harness or a policy
  plane, which both repositories explicitly exclude.
- **Adopt an existing open-source plane (OpenHands, Kubernetes
  agent-sandbox) and contribute.** Fastest parity; forfeits the durability
  core, the self-hosted-on-metal wedge, and the family integration. A
  Kubernetes agent-sandbox driver remains on the roadmap as a driver, not as
  the plane.
- **Rewrite in Rust to match the family.** The sandbox ecosystem
  (firecracker-go-sdk, containerd, gVisor, CNI, the open-source microVM
  runtimes) is Go; the plane is I/O bound; language is not the coupling.
- **Keep a bespoke per-agent adapter contract.** Every new agent would cost an
  adapter and tool calls would not pass through plane-owned code.

## Validation and reversal

Gates are measured by dogfood on the maintainer's own repositories because no
other users exist. Phase 0 ends when ACP headless viability is confirmed for
the chosen agent set and a KVM host is identified. Phase 1 ends after thirty
unattended days on rusui plus one sibling repository at twenty or more
sessions per week with zero credential leaks. Phase 2 ends when wake from
snapshot is under two seconds and no pull request opens without a recorded
proof.

Reversal: the engine core is untouched by D1 to D10, migrations are versioned,
and ACP is an open standard, so the agent side is not lock-in. If D2 fails its
gate, the plane still ships for shikigami and API-key agents and only the
positioning changes.

## Sources

- Repository state at
  [b1ad9ba](https://github.com/Sannrox/rusui/tree/b1ad9ba000fab1a0454d58ecb29493a4274bf182)
  and the Issues it produced: #2 to #11.
- Amp Orbs documentation (ampcode.com/docs/orbs and related pages, read
  2026-09-13); Claude Code cloud environment documentation (read 2026-09-13);
  OpenHands documentation. Competitive claims in ROADMAP.md cite these.
- Agent Client Protocol specification (agentclientprotocol.com).
