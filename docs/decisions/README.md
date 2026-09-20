# Architecture decision records

Decisions that must outlive a single pull request.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-environment-plane.md) | rusui becomes the environment plane for coding agents | Accepted |
| [0002](0002-grok-acp-agent-set.md) | P1 agent set is Grok CLI over ACP | Accepted |
| [0003](0003-operator-surface.md) | Operator surfaces are views of one object API | Accepted |
| [0004](0004-rusui-services-yaml.md) | services.yaml is `.rusui/services.yaml` only | Accepted |
| [0005](0005-policy-v2-project.md) | Policy v2 is keyed by project | Accepted |
| [0006](0006-session-start.md) | How review, run, and schedule sessions start | Accepted |
| [0007](0007-environment-snapshot.md) | P1 environment snapshot identity | Accepted |
| [0008](0008-p1-isolation-split.md) | P1 isolation split | Accepted |
| [0009](0009-credential-broker.md) | P1 credential broker | Accepted |
| [0010](0010-hybrid-roadmap-sequence.md) | Hybrid core-1.0 sequence and earlier gate mapping | Proposed |

## When to write an ADR

Add an ADR when a choice:

- changes a durable boundary (policy schema, worker or runner contract,
  persistence strategy, trust model, public API);
- chooses among durable alternatives;
- would be expensive to reverse without a migration story.

Small bug fixes and local refactors do not need ADRs. Capture them in code,
tests, and the pull request description instead.

## Lifecycle

- **Proposed**: under review in a pull request. `ARCHITECTURE.md` remains the
  product contract until acceptance.
- **Accepted**: merged with status updated; follow-up Issues may reference the
  ADR as a delivered dependency.
- **Superseded**: a newer ADR links here and this file links forward. Historical
  text is retained.

Template: [template.md](template.md). Title, status block, context, decision,
consequences, rejected alternatives, validation and reversal, sources. See
0001 for style. An accepted ADR does not change
[ARCHITECTURE.md](../../ARCHITECTURE.md) until that file is rewritten.
