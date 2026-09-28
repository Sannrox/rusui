# ADR 0036: A graphical desktop in the environment is deferred

- Status: Accepted
- Date: 2026-09-28
- Amends: none. [ADR 0003](0003-operator-surface.md) stands: ten console
  views, no rusui desktop or mobile client.
- Resolves: [#327](https://github.com/Sannrox/rusui/issues/327)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [VISION.md](../../VISION.md) (no desktop application in rusui),
  [ADR 0012](0012-operator-access.md) (terminal write lease, preview
  origin), [ADR 0027](0027-guest-reachability-ask.md) (the image does
  not grant reachability), [ADR 0028](0028-container-isolation-profile.md)
  (container isolation), [#114](https://github.com/Sannrox/rusui/issues/114)
  (terminal), [#115](https://github.com/Sannrox/rusui/issues/115)
  (preview).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

Some work needs a graphical application inside the environment: a
browser the guest drives, an emulator, or a native viewer for a test
artifact. rusui offers a console terminal and an authenticated HTTP
preview on a separate origin ([ADR 0012](0012-operator-access.md)). It
offers no graphical session inside the environment.

This is not the client question.
[ADR 0003](0003-operator-surface.md) rejects a rusui desktop or mobile
client; onmyoji is the desktop client outside this repository. The
question is whether the *environment* may run a display the operator
and the guest can use.

[#327](https://github.com/Sannrox/rusui/issues/327) requires the
maintainer to name one guest task that fails without a graphical
session. None is named. Its own unresolved question is whether the
first case is browser-driven verification of a web app, which may not
need a desktop.

Source evidence at the commit this decision was made against:

- The reference guest image (`build/guest-image/Dockerfile`) carries
  git, `gh`, Node.js, and the Claude Code CLI. It has no display server,
  window manager, browser, or remote-display service.
- The operator sees an environment through the transcript, the terminal
  write lease, and the HTTP preview. None carries pixels.
- The guest can already reach a server it starts inside its own
  container over loopback. Loopback inside the container is not egress.

## Decision

**D1. Defer.** A managed environment offers no graphical desktop in the
1.0 core. There is no display service in the reference guest image, no
desktop view in the console, and no remote-display port through the
preview proxy.

**D2. Browser verification does not need a desktop.** A guest that
verifies a web app can start the app and drive a headless browser
inside its own container over loopback. The reference image has no
browser; an operator who needs one uses a guest image that installs it,
whose identity is part of `source_hash`. This ADR does not change the
reference image. The operator views the app through the existing
preview ([ADR 0012](0012-operator-access.md)). A task in this shape does
not meet the adoption gate.

**D3. Adoption is a refused action until both gates hold:**

1. the maintainer names one guest task that fails with terminal, HTTP
   preview, and a headless browser in the guest image; and
2. a superseding ADR selects one desktop profile for container
   environments.

**D4. Constraints any adopted design must meet:**

- **No new console screen.** The desktop is a further view on the
  existing session page, like the terminal. ADR 0003's ten views stand;
  no rusui desktop application.
- **Lifecycle.** The display starts after setup, never during it, so
  nothing from it enters the snapshot. It stops on sleep and starts
  again on wake after `.agents/resume`. Replace and expiry end it.
- **Writer.** One writer, as the terminal write lease in ADR 0012.
  Others observe. The guest may drive the display only while its turn is
  live; that is not operator presence.
- **Origin.** The viewer is served on the preview origin or its own,
  never the plane origin. It carries no operator cookie and no plane
  credential.
- **Reachability.** The display adds no egress. A browser inside it
  holds only the guest's grant, which cannot mint an operator session
  (ADR 0012), so reaching the plane host does not open the console.
- **Receipts, not pixels.** rusui records start, stop, attach, and
  writer changes. It never stores frames, screenshots, or recordings in
  the transcript store.

No implementation follow-up is authorized. This ADR changes no image,
console view, or proxy.

## Consequences

Easier: the guest image stays small; no remote-display surface to
authenticate; no pixel data to retain.

Harder: an emulator or native GUI tool cannot run in a managed session.
Browser verification works headless only with a guest image that
installs a browser; the reference image does not. The operator sees the
app through preview rather than watching the guest's browser.

Irreversible: none.

## Threat examples

The deferral removes each of these. An adopting ADR must answer each.

- **Desktop used to browse the plane origin.** A browser in the
  environment opens the console with a cookie or token it finds, and
  approves its own actions. The guest can reach the plane host, so the
  protection is not the network: D4 serves the viewer off the plane
  origin and gives the desktop no operator cookie or plane credential,
  and the guest's grant cannot mint an operator session.
- **Guest control without operator presence.** The guest drives the
  display between turns, or while the operator believes they hold the
  writer. D4 limits guest control to a live turn and keeps one writer.
- **Recording pixels into the transcript store.** Frames of a private
  repository's UI, or of secrets shown on screen, persist in SQLite and
  exports. D4 records receipts only.

## Rejected alternatives

- **Accept a desktop profile now.** No task is named, and the likely
  first case, browser verification, fits D2 without a display.
- **Reject a desktop in the 1.0 core.** An emulator or native viewer may
  have no headless path. Deferral keeps that open behind D3.
- **Add a desktop console view.** Breaks ADR 0003's ten views for a
  feature with no measured user.

## Validation and reversal

Validation: this ADR is Accepted in the index; the reference guest image
has no display service; the console has no desktop view. Reverse by a
superseding ADR that meets D3 and D4.

## Sources

- [#327](https://github.com/Sannrox/rusui/issues/327)
- [#114](https://github.com/Sannrox/rusui/issues/114),
  [#115](https://github.com/Sannrox/rusui/issues/115)
- [ADR 0003](0003-operator-surface.md),
  [ADR 0012](0012-operator-access.md),
  [ADR 0027](0027-guest-reachability-ask.md),
  [ADR 0028](0028-container-isolation-profile.md)
- `build/guest-image/Dockerfile`
