# ADR 0085: The stable-core freeze starts on the author-validated journey

- Status: Accepted; amended by 0086
- Date: 2026-10-28
- Amends: [ADR 0046](0046-workspace-first-sequence.md) decision 2 (the
  independent self-hosting proof as a freeze prerequisite). Every other
  part of ADR 0046 stands.
- Resolves: [#653](https://github.com/Sannrox/rusui/issues/653).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ROADMAP.md](../../ROADMAP.md),
  [ADR 0086](0086-supported-1.0-core-contract.md) (the freeze this
  decision authorized).
- Discussion: none. The owner decided this on 2026-10-28; merging with this
  status is the acceptance act.

## Context

ADR 0046 sequences the core as dependable managed sessions, bounded
issue-to-PR delivery, a usable daily terminal workflow with independent
self-hosting proof, then a stable-core freeze.
[#118](https://github.com/Sannrox/rusui/issues/118) closed with a narrow
outcome: the self-hosting journey was run by the author on a clean machine
([U8 operator journey](../proofs/u8-operator-journey.md)), and no
independent maintainer was available. Under ADR 0046 as written, the freeze
([#141](https://github.com/Sannrox/rusui/issues/141)) cannot start. The owner
has decided to start the 1.0 chain.

## Decision

1. The stable-core freeze ([#141](https://github.com/Sannrox/rusui/issues/141))
   may start on the author-validated self-hosting journey recorded for
   [#118](https://github.com/Sannrox/rusui/issues/118).
2. Independent self-hosting proof moves from a freeze prerequisite to a 1.0
   publish gate. Publishing 1.0
   ([#146](https://github.com/Sannrox/rusui/issues/146)) requires a recorded
   independent self-hosting journey with a go outcome.
3. Until that journey is recorded, the 1.0 contract inventory claims no
   external self-hosting readiness. A guarantee that rests only on the author
   journey is narrowed or marked author-validated.

Optional tracks, the maintenance gate, and the human-merge rule of ADR 0046
are unchanged.

## Consequences

The freeze can start now, on narrower evidence. The independent journey
still has to happen before 1.0 is published, so the published claim is
unchanged; only its place in the sequence moves. The contract inventory
gains an explicit author-validated marking. No schema, policy, API, runner,
or credential change follows.

## Rejected alternatives

- Keep the independent journey as a freeze prerequisite: the freeze waits on
  an operator who is not available, and nothing learned from freezing earlier
  is lost by moving the gate.
- Drop the independent journey: 1.0 would claim self-hosting readiness on the
  author's evidence alone.

## Validation and reversal

[#146](https://github.com/Sannrox/rusui/issues/146) refuses to publish
without a recorded independent journey with a go outcome. If that journey
fails, the published claim narrows as ADR 0046 already requires. Reverse
with a later ADR that restores the freeze prerequisite; no data migration is
required.

## Sources

- [ADR 0046](0046-workspace-first-sequence.md)
- [U8 operator journey](../proofs/u8-operator-journey.md)
- [#118](https://github.com/Sannrox/rusui/issues/118),
  [#141](https://github.com/Sannrox/rusui/issues/141),
  [#146](https://github.com/Sannrox/rusui/issues/146),
  [#653](https://github.com/Sannrox/rusui/issues/653)
