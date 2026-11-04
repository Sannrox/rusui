# ADR 0087: Resume ACP guests with session/load

- Status: Accepted
- Date: 2026-11-04
- Amends: [ADR 0060](0060-shikigami-acp-guest-pin.md) D3 (the
  `session/load` rule).
- Resolves: [#674](https://github.com/Sannrox/rusui/issues/674)
- Related: [ADR 0025](0025-provider-boundary.md) (resume without a guest
  load), [ADR 0073](0073-session-plane-guest-registry.md) D3 and D5
  (`session/load` is optional; the plane owns the transcript),
  [#673](https://github.com/Sannrox/rusui/issues/673) (shikigami `v3.0.0`
  pin).

## Context

ADR 0060 D3 lists "fail-closed `session/load`" among the shikigami session
methods. That phrase described the guest: shikigami refused unknown ids. It
did not say whether rusui calls the method.

Shikigami `v3.0.0` advertises `loadSession: true` in `initialize`. Its
`session/load` restores a known session, replays the stored conversation as
`session/update` notifications, then answers. Unknown ids still fail
closed.

Rusui's generic ACP host (`internal/runner` `hostACP`) already calls
`session/load` whenever the plane holds a guest session id from an earlier
turn, whatever the guest advertises. Any error falls back to `session/new`,
and the turn records a `resume` receipt (`succeeded` or `refused`). Before
this ADR, the client also recorded the replayed updates as new transcript
rows, so a resumed shikigami turn repeated turns the plane already holds.

## Decision

**D1. Rusui resumes a generic ACP guest with `session/load`.** It calls it
only with a guest session id the plane recorded for this session. A
successful answer resumes that session and records `resume` `succeeded`.

**D2. Any load failure starts a new guest session.** An error, including an
unknown id or a guest without the method, falls back to `session/new` and
records `resume` `refused`. The turn continues from the plane transcript
(ADR 0025, ADR 0073 D3). Rusui never reuses an id the guest refused.

**D3. The load answer decides, not the advertisement.** `loadSession` in
`initialize` neither enables nor skips the call.

**D4. Load replay is not recorded.** `session/update` notifications that
arrive while `session/load` is in flight are replay of turns the plane
already holds (ADR 0073 D5). The client drops them. Updates during
`session/prompt` are recorded as before.

## Consequences

- Easier: a shikigami turn in the same environment keeps the guest's own
  context without duplicating the plane transcript.
- Harder: a guest that sends fresh, non-replay updates during
  `session/load` loses them. ACP defines load updates as replay only.
- Schema, policy, public API, and the spawn argv are unchanged. The runner
  contract changes only in that load replay produces no transcript rows.

## Rejected alternatives

- **Refuse `session/load` for every guest.** Every turn would lose the
  guest's context. ADR 0073 already makes the method optional, and D2
  already makes a failed load safe.
- **Gate the call on `loadSession`.** A guest without the method answers
  with an error and takes the D2 path. The gate adds a branch with the same
  outcome.
- **Deduplicate replayed rows in the plane.** That needs content matching.
  The client already knows which messages are replay.

## Validation and reversal

`TestHostACPReusesGuestSession` loads a known id from a guest that replays
an earlier turn. The test asserts `resume` `succeeded` and that no replayed
update is recorded. `TestHostACPLoadsOrCreatesGuestSession` loads an
unknown id and asserts `session/new` and `resume` `refused`.

Reverse with a superseding ADR that stops calling `session/load`.

## Sources

- [#674](https://github.com/Sannrox/rusui/issues/674),
  [#673](https://github.com/Sannrox/rusui/issues/673)
- Shikigami `v3.0.0` `docs/acp.md` (`session/load` row and `initialize`
  reply)
- Decided against `main` at `74bef74`.
