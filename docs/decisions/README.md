# Architecture decision records

Decisions that must outlive a single pull request.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-environment-plane.md) | rusui becomes the environment plane for coding agents | Accepted |
| [0002](0002-grok-acp-agent-set.md) | P1 agent set is Grok CLI over ACP | Accepted |

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

Template: title, status block, context, decision, consequences, rejected
alternatives, validation and reversal, sources. See 0001 for style.
