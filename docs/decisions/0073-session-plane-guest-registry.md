# ADR 0073: The session is the product; the guest is chosen per session

- Status: Accepted
- Date: 2026-10-07
- Amends: [ADR 0025](0025-provider-boundary.md) (the provider set becomes
  policy data and admits generic ACP guests),
  [ADR 0060](0060-shikigami-acp-guest-pin.md) (shikigami becomes the
  default guest), [ADR 0071](0071-bounded-child-session.md) (a child may
  name its own guest).
- Resolves: [#572](https://github.com/Sannrox/rusui/issues/572) (tracer).
- Related: [VISION.md](../../VISION.md) principle 1,
  [ADR 0026](0026-harness-model-upstream.md),
  [ADR 0061](0061-one-model-upstream-retained.md),
  [ADR 0012](0012-operator-access.md),
  [ADR 0039](0039-shared-operator-governance-deferred.md).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

Operators want long-lived, sandboxed sessions they can leave and pick up
from another client, with whichever agent harness they prefer inside.
Rusui already owns most of that shape. What it does not own is harness
choice.

Source evidence at `bcfe10f`:

- The session outlives the provider process. Resume starts a new process
  against the stored cursor ([ADR 0025](0025-provider-boundary.md)).
- `attachSession` (`internal/server/sessions.go`) streams session, turn,
  and action events over SSE. Any client can follow a session it did not
  start.
- The guest set is a closed `switch` in `internal/provider/provider.go`:
  `grok`, `claude`, `codex`, `shikigami`. Each kind has a hard-coded argv,
  probe, and pin. Grok and shikigami speak ACP; Claude speaks Claude Code
  stream-json; Codex speaks app-server JSON-RPC.
- The guest is plane-wide. `ModelConfigFromEnv` reads one `RUSUI_GUEST`
  (`internal/server/modelproxy.go`); `rusui setup` defaults it to
  `claude`. A project cannot choose a different guest from another
  project, and a session cannot choose at all.
- The model proxy forwards to one origin (ADR 0061). ADR 0026 already
  allows that origin to be a gateway rather than a provider API.

Adding a harness today means Go code in `internal/provider`, a new
conformance case, and a new release, even when the harness speaks ACP.
[VISION.md](../../VISION.md) already names ACP as the agent interface and
rules out per-vendor adapters as a goal.

## Decision

**D1. The session is the product.** A session, its environment, and its
transcript outlive every client and every guest process. A client
disconnect never cancels a turn. Any authenticated client may attach to a
session through the existing SSE stream and continue it.

**D2. Guests are policy data.** `policy.yaml` gains a guest registry. Each
entry names: `protocol` (`acp`, or one of the pinned native protocols from
ADR 0025), `argv`, `probe`, `pin`, and `image`. A project lists the guests
it allows and names one default. A session request may name any allowed
guest; an unlisted guest is refused before the environment starts.
`RUSUI_GUEST` remains only as the fallback default when policy names none.

**D3. Generic ACP guests are admitted by conformance, not by code.** A
`protocol: acp` entry is supported when the guest passes the ACP subset in
`internal/provider` conformance: `initialize`, `session/new`,
`session/prompt`, `session/update`, `session/request_permission`, and
`session/cancel`. `session/load` is optional; without it, resume follows
ADR 0025 (new process, plane transcript). The native Claude and Codex
adapters stay as pinned protocols. ADR 0025's refusal of
`claude-agent-acp` as the Claude guest stands.

**D4. Shikigami is the default guest.** A fresh `rusui setup` names
`shikigami` as the project default. Operators choose another guest in
policy.

**D5. The plane owns the transcript.** Every protocol's events are
normalized into plane session events. Search, attach, resume, and
handoff read the plane record, never a provider's home directory.

**D6. Model egress is unchanged.** One upstream (ADR 0061). Guests that
speak different provider dialects share it when the upstream is a gateway
that serves both. No per-guest connection is added.

**D7. A child session may name a guest.** The ADR 0071 bounds apply
unchanged; the child's guest must be allowed by the child's project.

**Not decided here:** multi-user identity and session sharing (ADR 0012
and ADR 0039 stand), microVM isolation, and any first-party web client.
Clients consume the existing HTTP and SSE API.

## Consequences

Easier: a new ACP harness ships as a policy edit and a conformance run.
Projects on one plane can use different guests. Sessions are portable
across clients because the transcript is the plane's.

Harder: the container is now the only isolation boundary for guests the
maintainer did not write. Guest images grow in number. Conformance must
run against each registered ACP guest, not only the pinned four.

Schema: policy gains the guest registry and per-project `guests`.
Unknown fields are still refused. Store: sessions record the guest name
and pin they ran with. Runner contract: spawn reads the registry entry
instead of the `switch`. Public API: session create accepts an optional
guest name. Trust model: unchanged; the guest still holds only the
per-turn grant ([ADR 0009](0009-credential-broker.md)).

## Rejected alternatives

- **Rusui runs its own model loop.** Violates VISION principle 1 and
  duplicates the harness work guests already do.
- **ACP only; drop native Claude and Codex.** ADR 0025 rejected the
  Claude ACP adapter on conformance grounds; nothing has changed that.
- **Keep the plane-wide guest.** Blocks per-project choice and forces a
  release for every new ACP harness.
- **Named model connections per guest.** ADR 0061 already refused them;
  a dual-dialect gateway removes the need.

## Validation and reversal

Validated when the tracer passes: one shikigami session started from the
CLI, detached mid-turn, reattached from a second client over SSE, and
completed, with model calls through a gateway upstream and the guest
named on the session record. Then a second ACP guest is admitted by a
policy edit and a conformance run, with no change to `internal/provider`
beyond its test fixture.

Reverse with a superseding ADR that restores the closed provider set.
Registry entries for the four pinned guests map one-to-one back to the
`switch`.

## Sources

- [ADR 0025](0025-provider-boundary.md),
  [ADR 0026](0026-harness-model-upstream.md),
  [ADR 0060](0060-shikigami-acp-guest-pin.md),
  [ADR 0061](0061-one-model-upstream-retained.md),
  [ADR 0071](0071-bounded-child-session.md),
  [#507](https://github.com/Sannrox/rusui/issues/507)
- `internal/provider/provider.go` `Argv`, `ProbeArgv`, `PinnedVersion`;
  `internal/server/modelproxy.go` `ModelConfigFromEnv`;
  `internal/server/sessions.go` `attachSession`;
  `internal/setup/setup.go` `RUSUI_GUEST` default
- Decided against `main` at `bcfe10f`.
