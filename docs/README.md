# Documentation

rusui is a self-hosted environment plane. The current product contract is
[ARCHITECTURE.md](../ARCHITECTURE.md); [CONTEXT.md](../CONTEXT.md) defines its
nouns. GitHub comment and close remain dry runs. In `implement` sessions an
agent may push and open its own pull request under
[ADR 0015](decisions/0015-agent-publication.md).

Choose what you need now. Pages keep their established paths so existing
links continue to work. This index covers current guidance and historical
records; historical material is labeled below and is not a runbook.

## Learn: tutorials

Follow an experience from start to finish, with expected observations.

- [Your first rusui session](tutorial.md) runs a disposable sample without
  GitHub, Slack, or model credentials; it also has an optional loopback exercise.

## Do: how-to guides

Use these when you already have a task to complete.

- [Run rusui against a repository](operator.md) — setup, webhook, runner,
  restart, removal, and troubleshooting.
- [Use the local interactive profile](local-interactive.md).
- [Configure container guests](container-guests.md).
- [Request a pull request review](pull-request-review.md).
- [Use implement sessions](implement-sessions.md).
- [Run a review-only GitHub App pilot](github-app-pilot.md).
- [Install a user service](user-service.md).
- [Drain, upgrade, diagnose, or remove](upgrade.md).
- [Build, test, and release images](development.md).
- [Evaluate recorded reviews](evaluate-reviews.md).
- [Contribute code or issues](../CONTRIBUTING.md); agents follow the canonical
  [agent instructions](../AGENTS.md). The [AGENT.md](../AGENT.md) and
  [CLAUDE.md](../CLAUDE.md) files are pointers to those instructions.
- [Report a vulnerability](../SECURITY.md).

## Look up: reference

Use these for precise commands, fields, interfaces, and source layout.

- [Configuration, CLI flags, environment, HTTP, policy, and Slack](configuration.md).
- [Build targets, toolchain, layout, and CI](build-reference.md).
- [ACP editor shim](acp-editor.md).
- [Build and runtime image internals](../build/README.md).
- [Maintenance evaluation corpus and measures](../eval/set.md).
- [Turn measurements and review dispositions](review-results.md).
- [MIT license](../LICENSE).

## Understand: explanations

Read these for rationale, boundaries, direction, and the evidence behind claims.

- [Product contract](../ARCHITECTURE.md) and [noun glossary](../CONTEXT.md).
- [Vision](../VISION.md) and [roadmap](../ROADMAP.md) describe proposed or
  sequenced work; they do not change the current contract.
- [Architecture decisions](decisions/README.md), including their status and
  superseded records.
- [Supported unattended topology](supported-topology.md) explains what the
  current evidence supports and its limits.
- [Proof and research records](proofs/README.md) explain what was observed
  at a recorded revision; they are historical evidence, not current setup steps.
- [Timed macOS walkthrough record](proofs/operator-timed-macos-walkthrough.md)
  is historical, unverified project evidence.
- [Community standards](../CODE_OF_CONDUCT.md).

## Historical and pointer pages

- [Archived v1 agent build prompt](v1-build-prompt.md) is provenance only;
  do not implement from it.
- [BUILD.md](../BUILD.md) points to the current build guide.
- [Development evaluation replay](../eval/results.md) records historical
  results for the development corpus, not a public benchmark.
- [Project README](../README.md) is the short repository entry point.

The documentation gate checks this page's reachability graph, local links,
and Markdown heading fragments. Run `python3 scripts/check-docs.py` or
`make validate` from the repository root.
