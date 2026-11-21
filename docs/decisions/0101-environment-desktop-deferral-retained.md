# ADR 0101: Environment desktop deferral retained

- Status: Accepted
- Date: 2026-11-21
- Amends: [ADR 0036](0036-environment-desktop-deferred.md) (a graphical
  session in the environment stays deferred; D3 still unmet). ADR 0036
  remains Accepted.
- Resolves: [#789](https://github.com/Sannrox/rusui/issues/789)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (product contract;
  this ADR does not rewrite it),
  [ADR 0003](0003-operator-surface.md) (ten console views; no rusui
  desktop or mobile client),
  [ADR 0012](0012-operator-access.md) (terminal write lease, preview
  origin),
  [ADR 0019](0019-rusui-attach-client.md) (attach selects the
  runtime-owned transport),
  [ADR 0027](0027-guest-reachability-ask.md) (the image does not grant
  reachability),
  [ADR 0028](0028-container-isolation-profile.md) (container isolation),
  [ADR 0050](0050-guest-toolchain-in-image.md) (a headless browser in
  the image is not a desktop),
  [ADR 0065](0065-one-environment-terminal.md) (one terminal session,
  exclusive write lease),
  [#327](https://github.com/Sannrox/rusui/issues/327)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act. Until merge this is a proposal, not
  implementation authority. This ADR does not add a display server,
  guest-image package, schema, policy field, or attach surface.

## Context

[ADR 0036](0036-environment-desktop-deferred.md) deferred a graphical
desktop inside a managed environment. Terminal and authenticated HTTP
preview remain the operator's views. Its D3 gate requires both:

1. the maintainer names one guest task that fails with terminal, HTTP
   preview, and a headless browser in the guest image; and
2. a superseding ADR selects one desktop profile for container
   environments.

[#789](https://github.com/Sannrox/rusui/issues/789) reopened the
question. Two options:

1. Adopt one display protocol inside the existing environment. The
   operator would attach through the existing attach contract, with
   one writer as the terminal write lease, on the container isolation
   profile, with no extra guest reachability. Not a new session kind.
   Not a rusui desktop or mobile application.
2. Retain the deferral. ADR 0036 stands.

No named task meets D3. Authorizing this research did not name one.
[#327](https://github.com/Sannrox/rusui/issues/327) asked the same
question and closed as the original deferral; its unresolved case was
browser-driven verification of a web app, which ADR 0036 D2 already
refuses as an adoption reason. [VISION.md](../../VISION.md) and
[ARCHITECTURE.md](../../ARCHITECTURE.md) still name no guest task that
fails those three. Open and closed issues searched for emulator,
native viewer, graphical session, display protocol, and GUI name
classes of work, not a measured task. [#500](https://github.com/Sannrox/rusui/issues/500)
bakes a headless browser into the image and lists a graphical desktop
as a non-goal.

Source evidence at the commit this decision was made against
(`29ad69b5dda9b598a4b82098c3fda9f4a5f228dd`):

- The reference guest image (`build/guest-image/Dockerfile`) installs
  git, `gh`, Node.js, a C toolchain, and headless Chromium. It has no
  display server, window manager, or remote-display service.
- Attach is the runtime-owned terminal transport
  (`cmd/rusui/attach.go`). One write lease; a second writer observes.
  There is no pixel or display attach path.
- Preview remains a separate origin for HTTP services the guest
  starts. It does not carry a display protocol.
- The console has no desktop view. ADR 0003's ten views stand.

ADR 0036 D1, D2, and D4 stand. D3's first gate is still unmet.

## Decision

**D1. Retain the deferral.** A managed environment offers no graphical
session. There is no display service in the reference guest image, no
desktop view in the console, and no remote-display port through the
preview proxy or attach.

**D2. Browser verification still does not need a desktop.** A guest
that verifies a web app starts the app and drives the headless browser
already in the reference image over loopback, and the operator views
the app through the existing preview. That shape remains ADR 0036 D2.
It does not meet D3.

**D3. ADR 0036 D3 still holds.** Adoption stays a refused action until
both of its gates hold. This research did not name the task gate 1
requires. Dated reason, 2026-11-21: no guest task is named that fails
with terminal, HTTP preview, and a headless browser in the guest
image.

**D4. No implementation follow-up.** This ADR adds no display server,
guest-image package, schema, policy field, or attach surface. A later
ADR that meets D3 may adopt one display protocol under ADR 0036 D4,
as a further view on the existing session, joined through attach,
with one writer, on the container isolation profile, with no extra
guest reachability and no rusui desktop application. This one does
not authorize that work.

## Consequences

Easier: the guest image stays without a display stack; attach stays
one terminal; no pixel stream to authenticate or retain.

Harder: an emulator or native GUI tool still cannot run in a managed
session. The operator sees a web app through preview, not through a
guest-driven window.

Irreversible: none. Schema, policy, runner contract, trust model, and
public API are unchanged.

[ARCHITECTURE.md](../../ARCHITECTURE.md) still defers the environment
desktop. This ADR is not that rewrite.

## Threat examples

Retaining the deferral keeps each of these unintroduced. An adopting
ADR must still answer ADR 0036 D4.

| Attempt | Result |
| --- | --- |
| Display used to browse the plane origin | Unintroduced. No display. D4 of ADR 0036 would serve any later viewer off the plane origin with no operator cookie or plane credential. |
| Guest control without operator presence | Unintroduced. No display. D4 of ADR 0036 would keep one writer and limit guest control to a live turn. |
| Recording pixels into the transcript store | Unintroduced. No frames. D4 of ADR 0036 records receipts only. |
| Extra guest reachability for a display port | Refused. The image still does not grant reachability ([ADR 0027](0027-guest-reachability-ask.md)). Overlay may only narrow. |
| A rusui desktop or mobile client | Refused. ADR 0003 stands. |
| A new session kind for pixels | Refused. Display, if ever adopted, is another attach surface on the existing environment, not a second session. |

## Rejected alternatives

- **Adopt one display protocol on attach now.** D3 is unmet. Attach
  already joins the one environment terminal ([ADR 0065](0065-one-environment-terminal.md)).
  Adding a pixel stream would add a display process, an origin, a
  writer rule, and guest-image packages without a named task that
  terminal, preview, and a headless browser cannot serve.
- **A rusui desktop or mobile application.** Issue non-goal. ADR 0003
  and VISION already refuse it.
- **A new session kind for a graphical display.** The environment
  already exists. A second session would split the workspace and the
  write lease.
- **Unauthenticated desktop.** Issue non-goal. Preview already refuses
  an unauthenticated path ([ADR 0037](0037-shared-preview-deferred.md)).
- **Replace HTTP preview for web apps that already preview.** Issue
  non-goal. D2 already covers that work.
- **Reject a desktop in the core outright.** ADR 0036 already chose
  deferral so a design that keeps D4 can return if D3 holds. This
  retain keeps that door, dated.

## Validation and reversal

Validation: this ADR is Accepted in the index; ADR 0036 remains
Accepted and lists this amendment; the reference guest image has no
display service; attach has no display path; the console has no
desktop view. Reverse by a superseding ADR that meets ADR 0036 D3
and D4. Do not delete this file.

## Sources

- [#789](https://github.com/Sannrox/rusui/issues/789)
- [#327](https://github.com/Sannrox/rusui/issues/327)
- [#500](https://github.com/Sannrox/rusui/issues/500)
- [ADR 0003](0003-operator-surface.md),
  [ADR 0012](0012-operator-access.md),
  [ADR 0019](0019-rusui-attach-client.md),
  [ADR 0027](0027-guest-reachability-ask.md),
  [ADR 0028](0028-container-isolation-profile.md),
  [ADR 0036](0036-environment-desktop-deferred.md),
  [ADR 0050](0050-guest-toolchain-in-image.md),
  [ADR 0065](0065-one-environment-terminal.md)
- `build/guest-image/Dockerfile`;
  `cmd/rusui/attach.go`
- Commit `29ad69b5dda9b598a4b82098c3fda9f4a5f228dd`
