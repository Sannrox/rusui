# ADR 0024: The CLI and the console open a session

- Status: Accepted
- Date: 2026-09-26
- Amends: [ADR 0003](0003-operator-surface.md) (which surface opens a session).
- Narrows: [ADR 0016](0016-local-interactive-runtime.md) D7 and
  [ADR 0019](0019-rusui-attach-client.md) (`rusui attach` is a terminal
  transport, not the session read).
- Narrows: [#186](https://github.com/Sannrox/rusui/issues/186) so that
  research no longer treats Sumika attach as the session UI.
- Resolves: [#281](https://github.com/Sannrox/rusui/issues/281)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) is unchanged.
  [#282](https://github.com/Sannrox/rusui/issues/282) is the follow-up
  that may rewrite it once the CLI can read a session without attaching
  its terminal.
- Discussion: none. Merging with this status is the acceptance act.

## Context

ADR 0003 makes the `rusui` CLI the primary operator tool and the embedded
console a view of the same objects. It treats an editor as the renderer of
the transcript. ADR 0016 and ADR 0019 then make `rusui attach` open a Sumika
PTY for a local session and a leased terminal for a managed session. An
operator can still believe there are two products: read a managed session in
the console, or attach a terminal.

The session record is the only durable work object. A client that
disconnects is not the end of that work. A terminal byte stream is not a
transcript. A local process supervisor is not the list of sessions.

## Decision

The `rusui` CLI and the embedded console are the surfaces that open a
session. Both show the same session: its transcript, its recorded tool-call
events, and its current diff. Where the session runs is a field on the
session. Closing the client leaves the session running. Opening the same
session id shows the durable history.

On a managed session, the terminal and the workspace files are further views
of that session. The terminal does not stand in for the transcript. An ACP
editor remains another client of the same session. Slack remains notify and
approve.

Sumika remains the supervisor of an experimental local process. Raw-PTY
attach stays that process's own attach. It is not the session list, and it
is not how a managed session is read.

This ADR does not rewrite [ARCHITECTURE.md](../../ARCHITECTURE.md). Issue
[#282](https://github.com/Sannrox/rusui/issues/282) is the follow-up that
may rewrite that contract after the CLI can read a session without opening
a terminal.

### What each client owns

| Client | Owns | Does not own |
| --- | --- | --- |
| `rusui` CLI | Opening a session by id: transcript, recorded tool-call events, current diff | The managed terminal and local PTY bytes |
| Embedded console | The same session read as the CLI | A private copy of the session |
| Managed terminal | A further view of a managed session | The transcript |
| Workspace files | A further view of a managed session | Session identity |
| ACP editor | Another client of the same session | A second session store |
| Slack | Notify and approve | Transcript, terminal, and opening a session |
| Sumika raw-PTY attach | The experimental local process's own attach | The session list, and reading a managed session |

The session id is the join key. A second open of that id reads the stored
history. It does not create a duplicate session, and it does not require the
previous client to stay connected.

Unspecified clients fail closed: they are not a session surface until a later
accepted ADR names them. Portals, a desktop client, and a phone client are
not session surfaces.

## Consequences

- Operators open work from the CLI or the console. Leaving either client
  does not stop the session.
- `rusui attach` stays the runtime-owned terminal transport from ADR 0019.
  It is not how a session is read.
- Local and managed sessions can differ in where they run. That difference
  is a field, not a second operator product.
- #186 may still measure the experimental local profile against a managed
  session. It cannot define the session UI, and it cannot require Sumika
  attach and the managed console to behave as one operator surface.
- No schema, credential, or runtime change.

## Rejected alternatives

- **One Sumika session and one managed session share one operator surface.**
  That makes a raw PTY the transcript and makes an experimental local
  process the session list.
- **The terminal is the session.** Tool-call events and the diff then exist
  only while a lease or a PTY is attached, so closing the client drops the
  history.
- **Rewrite ARCHITECTURE.md in this decision.** The product contract stays
  put until the CLI read exists and a follow-up amends it.
- **A new desktop or phone client.** The CLI and the console are enough to
  open a session.

## Validation and reversal

Accept on merge. The decision index links this ADR. ADR 0003, ADR 0016, and
ADR 0019 link forward. #186's body no longer requires the two profiles to
behave as the same operator surface. Reverse by superseding this ADR. No
data migration.

## Sources

- [#281](https://github.com/Sannrox/rusui/issues/281)
- [ADR 0003](0003-operator-surface.md)
- [ADR 0016](0016-local-interactive-runtime.md)
- [ADR 0019](0019-rusui-attach-client.md)
- [#186](https://github.com/Sannrox/rusui/issues/186)
- [#282](https://github.com/Sannrox/rusui/issues/282)
