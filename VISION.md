# Vision

> **Not the runbook.** Accepted product direction
> ([ADR 0001](docs/decisions/0001-environment-plane.md)).
> The current contract is [ARCHITECTURE.md](ARCHITECTURE.md). Do not treat
> this file as shipped operator behavior.

## Purpose

`rusui` is a public, self-hosted **environment plane** for coding agents: the layer
that gives an agent a machine, keeps that machine alive across sleep and wake,
brokers credentials so no secret enters it, wires events and schedules into
sessions, and records every external action.

留守居 is the steward who keeps house while you are away. The steward now runs
the workshop too.

It is agent-agnostic through the Agent Client Protocol. The P1 supported
guest is Grok CLI ([ADR 0002](docs/decisions/0002-grok-acp-agent-set.md)).
[shikigami](https://github.com/Sannrox/shikigami) is the first-party agent,
[sekai-chisei](https://github.com/Sannrox/sekai-chisei) the optional
governance plane, and [tenkai](https://github.com/Sannrox/tenkai) the
delivery path. onmyoji is a desktop client outside this repository.

## Problem

Cloud coding agents decide on two questions: where the work runs and how it is
handed back. Today's answers are metered machines in a vendor's cloud running
the vendor's agent, with the control plane, credentials, and receipts on the
vendor's side. An operator who wants a different agent, their own hardware, an
air gap, or an auditable record of every push and comment has no product that
combines those. Meanwhile the hard parts of unattended agent work, idempotent
intake, fenced ownership, leases with receipts, policy bound at admission and
re-checked at execution, are exactly what rusui's engine already does for one
workflow.

## Product promise

An operator starts a session from the CLI, an editor, a webhook, or a
schedule. rusui creates or wakes an environment prepared from a project
snapshot, drives any ACP agent inside it, streams the transcript, diff, and
terminal, brokers scoped short-lived credentials through its own proxies, runs
the project's proofs after the agent stops, and publishes only proven work
through actions that carry a policy revision, an evidence class, and a receipt.
Idle environments cost nothing; waking one is fast. Nothing leaves the
operator's perimeter and there is no per-minute meter.

## Principles

1. **Machines are the product; the agent is a guest.** rusui never embeds a
   model loop. ACP is the only agent interface.
2. **Keep the semantics, change the nouns.** Intake, fenced ownership, leases,
   receipts, immutable policy revisions, and evidence classes carry over from
   v1 unchanged in meaning.
3. **No secret enters an environment.** Credentials are brokered per turn and
   redeemed only through plane-owned proxies.
4. **Every external action has a receipt.** Policy is re-read inside the
   transaction that commits the action.
5. **Fail closed.** Missing secrets, unknown policy fields, unreachable
   required governance, and unmatched permission requests all refuse.
6. **API first.** Every surface is a view of the same object types. CLI,
   editors via ACP, the embedded console, and chat adapters are clients of
   one HTTP+SSE API ([ADR 0003](docs/decisions/0003-operator-surface.md)).
7. **Self-hosted, single binary, your metal.** SQLite by default; Postgres and
   high availability are options, never requirements.
8. **Dogfood gates.** Every phase ends in a measurement on the maintainer's own
   repositories.

## Boundaries in the family

| Concern | Owner |
| --- | --- |
| Machines, snapshots, sleep and wake, runners | rusui |
| Credential brokering, git and API proxies, egress classes | rusui |
| Events, schedules, sessions, actions, receipts | rusui |
| Agent turn loop and tool execution | shikigami or any ACP agent |
| Policy decisions, budgets, approvals across projects (when required) | sekai-chisei |
| Release and deployment of the binaries | tenkai |
| Desktop conversation and dispatch UI | onmyoji |

## Non-goals

- A hosted, multi-tenant SaaS.
- A chat-first agent interface; editors and onmyoji own that.
- A desktop or native mobile application.
- Embedding a model loop or writing agent adapters per vendor.
- Replacing the governance plane or the delivery plane.

## Path

Sequencing, gates, effort, and the assumptions the plan rests on live in
[ROADMAP.md](ROADMAP.md). Planning truth is the GitHub Issue tracker.
