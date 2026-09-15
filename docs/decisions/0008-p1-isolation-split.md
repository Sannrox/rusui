# ADR 0008: P1 isolation split

- Status: Accepted
- Date: 2026-09-15
- Amends: [ADR 0001](0001-environment-plane.md) (OS sandbox assignment
  that was a working assumption before Phase 1). [ADR 0002](0002-grok-acp-agent-set.md)
  spawn and `--always-approve` rejection stand.
- Resolves: [#43](https://github.com/Sannrox/rusui/issues/43)
- Discussion: none. Merging with this status is the acceptance act.

## Context

ADR 0001 said the OS sandbox boundary must be assigned between rusui
(environment isolation) and shikigami (tool-level sandbox,
[shikigami#282](https://github.com/Sannrox/shikigami/issues/282))
before Phase 1, and assumed rusui isolates the machine while
shikigami isolates tools. P1’s guest is Grok, not shikigami.
[Find the way to P1 dogfood](https://github.com/Sannrox/rusui/issues/37)
locked the split in grilling; this ADR records it. The issue title
said ADR 0004; that number is
[services.yaml](0004-rusui-services-yaml.md).

## Decision

P1 has two fences, and they are not the same noun.

- **Machine isolation** is rusui’s. The dogfood environment is a
  **container**. The process driver is a test/dev stand-in and does
  not count toward the gate.
- **Tool fence** is Grok’s `--permission-mode default`. Unmatched
  `session/request_permission` is denied and parked (policy allow-rules
  in [ADR 0005](0005-policy-v2-project.md)). `--always-approve` is not
  the unattended spawn ([ADR 0002](0002-grok-acp-agent-set.md)).
- shikigami’s tool-level sandbox is out of P1 and out of this map.

Rusui does not claim to jail tools inside the guest process. Grok
does not claim to isolate the machine.

## Consequences

- Dogfood sessions run in containers. Process-directory sessions are
  not evidence for the 30-day gate.
- Receipts on `session/update` and `session/request_permission` are
  the plane’s view of tools; they are not a substitute for the
  container.
- shikigami#282 can proceed on its own timeline without blocking P1.

## Rejected alternatives

- **Wait for shikigami ACP + tool sandbox before P1.** P1’s guest is
  Grok.
- **Treat `--always-approve` as the tool fence.** Skips permission
  mapping.
- **Process driver as dogfood isolation.** Contradicts the map’s
  container default.
- **Rusui implements a tool jail inside the guest.** Wrong plane.

## Validation and reversal

Accept on merge. Reopen if P1 adds a guest that does not emit
`session/request_permission` for shell, or if dogfood must run
without a container runtime.

## Sources

- [#43](https://github.com/Sannrox/rusui/issues/43)
- [ADR 0001](0001-environment-plane.md) consequences (working assumption)
- [ADR 0002](0002-grok-acp-agent-set.md) spawn and receipts
- [Find the way to P1 dogfood](https://github.com/Sannrox/rusui/issues/37) Notes
