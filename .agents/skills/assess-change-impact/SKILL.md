---
name: assess-change-impact
description: Assess a proposed or implemented rusui change across policy, admit/lease, GitHub intake, apply, Slack, persistence, and operations boundaries. Use when scoping an Issue, planning tests, reviewing a diff, or identifying migration, documentation, compatibility, and security obligations.
---

# Assess Change Impact

Build an evidence-backed impact map before implementation or review.

## Procedure

1. Read the linked Issue or request, `README.md`, `ARCHITECTURE.md`,
   `docs/README.md`, and the relevant code. For a diff, inspect every changed
   file and its direct callers or implementors. Complete when the claimed
   outcome and actual change surface are both known.
2. Trace applicable boundaries:
   - hard defaults versus `policy.yaml` baseline versus SQLite overlay
     (overlay may only narrow);
   - webhook ACK (no live fetch) versus refresh coordinator versus admit;
   - GitHub as intake versus the server as source of truth versus Slack as
     the human socket;
   - read-only GitHub client versus apply (dry-run in v1; live later);
   - model CLI review artifacts versus credentials (only `implement`
     sessions receive a GitHub write credential, ADR 0015);
   - `review` / `apply` / `implement` lanes and lease generations;
   - loopback bind versus operator tunnel;
   - SQLite recovery truth versus optional JSON/MD exports;
   - v1 dry-run versus v2 live apply versus v3 implement-to-PR.
   Complete when each applicable boundary has an owner and expected invariant.
3. Identify persistence and compatibility obligations. Include fresh and
   upgraded SQLite state, immutable review revisions, policy reload, old
   clients/configuration, error semantics, and rollback or recovery impact
   where relevant. Complete when data-loss and partial-failure paths are
   accounted for.
4. Map evidence to risk: unit tests for pure logic; package tests for engine,
   policy, store, GitHub client, Slack, and server; eval set for review-quality
   claims; ignored live tests only when a real service is essential. Complete
   when every material risk has a proposed check or an explicit residual
   uncertainty.
5. Determine durable artifacts that must change: docs, `policy.example.yaml`,
   `policy.fixture.yaml`, an ADR, or a repository Skill. Complete when no
   artifact is proposed merely to record temporary planning.

## Output

Return a compact matrix with columns:

| Surface | Evidence found | Required change/check | Risk if missed |
| --- | --- | --- | --- |

Then list scope boundaries, blocking questions, and the smallest safe PR split.
Do not approve an architecture, perform a full security audit, or claim a later
milestone without inspecting the implementations.
