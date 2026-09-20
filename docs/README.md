# Documentation

rusui is a self-hosted environment plane. You write `policy.yaml`. The server
admits work onto sessions and turns, records immutable reviews, and dry-runs
apply.

[ARCHITECTURE.md](../ARCHITECTURE.md) is the product contract.
[CONTEXT.md](../CONTEXT.md) is the noun glossary.
[VISION.md](../VISION.md) and [ROADMAP.md](../ROADMAP.md) are sequencing, not
the runbook.

## Start here

| Role | Start |
| --- | --- |
| First look | [README](../README.md) |
| First session | [tutorial.md](tutorial.md) |
| Operator | [operator.md](operator.md) |
| Flags, env, HTTP, policy | [configuration.md](configuration.md) |
| Contributor | [CONTRIBUTING.md](../CONTRIBUTING.md), [development.md](development.md) |
| Agent | [AGENTS.md](../AGENTS.md) |
| Security report | [SECURITY.md](../SECURITY.md) |

## Map

| File | Type | Authority |
| --- | --- | --- |
| [../README.md](../README.md) | tutorial | what it is, quick start, next steps |
| [tutorial.md](tutorial.md) | tutorial | first loopback session |
| [../ARCHITECTURE.md](../ARCHITECTURE.md) | explanation | product contract |
| [../CONTEXT.md](../CONTEXT.md) | explanation | product nouns |
| [../VISION.md](../VISION.md) | explanation | accepted direction; not the runbook |
| [../ROADMAP.md](../ROADMAP.md) | explanation | published M1–M6 sequence ([ADR 0010](decisions/0010-hybrid-roadmap-sequence.md) Accepted) |
| [operator.md](operator.md) | how-to | run, webhook, Slack, troubleshoot |
| [upgrade.md](upgrade.md) | how-to | drain, packaged upgrade, redacted diagnostics |
| [proofs/d6-session-workflow.md](proofs/d6-session-workflow.md) | explanation | D6 twenty-run matrix and supported claim |
| [configuration.md](configuration.md) | reference | flags, env, HTTP, policy, Slack verbs |
| [development.md](development.md) | how-to | `make`, Docker images, CI |
| [../build/README.md](../build/README.md) | reference | image internals |
| [../BUILD.md](../BUILD.md) | archive | pointer to development.md |
| [decisions/](decisions/) | explanation | ADRs |
| [v1-build-prompt.md](v1-build-prompt.md) | archive | historical agent prompt; not the contract |
| [../eval/set.md](../eval/set.md) | reference | review-quality fixtures |
| [../eval/results.md](../eval/results.md) | reference | last TestEvalSet snapshot |
| [../SECURITY.md](../SECURITY.md) | how-to | vulnerability reports |
| [../CONTRIBUTING.md](../CONTRIBUTING.md) | how-to | issues and pull requests |
| [../AGENTS.md](../AGENTS.md) | how-to | agent instructions |
| [../CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md) | explanation | community standards |
| [../LICENSE](../LICENSE) | reference | MIT |
