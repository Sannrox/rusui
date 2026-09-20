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
| Operator | [operator.md](operator.md) |
| Flags, env, HTTP, policy | [configuration.md](configuration.md) |
| Contributor | [CONTRIBUTING.md](../CONTRIBUTING.md), [development.md](development.md) |
| Agent | [AGENTS.md](../AGENTS.md) |
| Security report | [SECURITY.md](../SECURITY.md) |

## Map

| File | Type | Authority |
| --- | --- | --- |
| [../CONTEXT.md](../CONTEXT.md) | explanation | product nouns |
| [operator.md](operator.md) | how-to | run, webhook, Slack, troubleshoot |
| [configuration.md](configuration.md) | reference | flags, env, HTTP, policy, Slack verbs |
| [development.md](development.md) | how-to | `make`, Docker images, CI |
| [decisions/](decisions/) | explanation | ADRs |
| [v1-build-prompt.md](v1-build-prompt.md) | archive | historical agent prompt; not the contract |
| [../eval/set.md](../eval/set.md) | reference | review-quality fixtures |
