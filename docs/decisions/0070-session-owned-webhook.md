# ADR 0070: A session may own a signed webhook

- Status: Accepted
- Date: 2026-10-06
- Amends: none. GitHub review intake (issues, pull requests, comments)
  stands.
- Supersedes: [ADR 0057](0057-github-intake-stays-review.md) for the
  refusal of a session-owned wake webhook. GitHub review intake is
  restated here as D4.
- Resolves: [#488](https://github.com/Sannrox/rusui/issues/488)
  option 1
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until
  rewritten), [#510](https://github.com/Sannrox/rusui/issues/510)
  (implementation), [#499](https://github.com/Sannrox/rusui/issues/499)
  (archive stops delivery)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#488](https://github.com/Sannrox/rusui/issues/488) asked whether a
session may own a signed webhook whose delivery stores the event,
wakes the environment, and adds a prompt.
[ADR 0057](0057-github-intake-stays-review.md) closed that question by
keeping GitHub review intake only.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product the plane hosts a signed HTTP
endpoint for a thread. A valid delivery wakes that machine. Archive
makes the URL miss. The guest does not bind a public port.

That product does not totally conflict with rusui. The guest link
already forbids the guest listening on a public port. The signing
secret stays on the plane. GitHub issues, pull requests, and comments
remain the review intake. A CI failure is still not a GitHub intake
item.

[#510](https://github.com/Sannrox/rusui/issues/510) is the
implementation. This ADR does not add routes.

## Decision

**D1. Session-owned webhook.** A session may own a webhook URL. A
delivery with a valid signature is stored, wakes that environment, and
adds a prompt containing the event id.

**D2. Invalid signature does not wake.** An invalid signature is
stored as a refusal and does not wake the session.

**D3. The guest does not listen on a public port.** The plane owns
the URL. The signing secret stays on the plane.

**D4. GitHub review intake stands.** GitHub webhooks that start review
work remain issues, pull requests, and comments. A CI failure is not
an intake item. This ADR does not replace that path.

**D5. Archive stops delivery.** An archived session does not accept
delivery ([ADR 0063](0063-keep-environment-until-archive.md),
[#499](https://github.com/Sannrox/rusui/issues/499)).

## Consequences

- Easier: an outside service can continue one session without opening
  a new review item.
- Harder: the plane exposes a public POST URL per session. Signature
  verification and archive-404 are load-bearing.
- Intake and admit change in #510. Guest link destinations are
  unchanged. No plugin runtime inside the guest.
- `ARCHITECTURE.md` changes only when that file is next rewritten.

## Rejected alternatives

- **Review intake only (option 2 / ADR 0057).** Conflicts with the
  directed product shape. Not a total rusui-contract conflict that
  would keep it. GitHub review intake itself is kept (D4).

- **Guest binds a public port.** Issue non-goal. Breaks the guest
  link.

- **CI failure as a GitHub intake item.** Not this issue. D4 keeps
  that refusal.

## Validation and reversal

Accept on merge. Validated when #510 lands: one valid delivery
produces one new turn on the same environment; one bad signature
produces no turn; GitHub review intake is unchanged; an archived
session refuses the delivery.

Reverse with a superseding ADR that drops session-owned webhooks.

## Sources

- [#488](https://github.com/Sannrox/rusui/issues/488)
- [ADR 0057](0057-github-intake-stays-review.md)
- Decided against `main` at `59c0452`.
