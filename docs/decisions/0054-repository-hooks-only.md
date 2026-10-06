# ADR 0054: Setup and resume stay repository hooks

- Status: Superseded by [ADR 0067](0067-plane-stored-pre-hooks.md)
- Date: 2026-10-06
- Amends: [ADR 0007](0007-environment-snapshot.md) (prepare runs
  `.agents/setup` from the pin; wake runs `.agents/resume` only).
- Resolves: [#485](https://github.com/Sannrox/rusui/issues/485)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until
  rewritten), [#504](https://github.com/Sannrox/rusui/issues/504)
  (plane-stored hooks remain blocked).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

Only `.agents/setup` and `.agents/resume` from the repository run.
Nothing on the plane can prepare access before the clone.

[#485](https://github.com/Sannrox/rusui/issues/485) asked whether a
project may store a pre-clone hook and a pre-setup hook on the plane,
with failure stopping create, and output in the session log. Two
options:

1. Plane-stored pre-clone and pre-setup hooks. Non-zero exit stops
   create. The hook receives no operator token and no provider key.
2. Retain repository hooks only.

[ADR 0007](0007-environment-snapshot.md) already places setup in the
prepared tree: clone the pin, run `.agents/setup` if present, record
the snapshot. Wake runs `.agents/resume` only. A plane-stored hook
would run before the pin exists, on the host or in an empty guest,
with a new object to store, fail, and log. No named create needs
access the git pin and the per-turn grant cannot provide.

Source evidence at the commit this decision was made against:

- `prepareWorkspace` fetches the pin, then `runSetup`. There is no
  pre-clone or pre-setup step.
- Setup is `.agents/setup` inside the guest, from the repository tree.

## Decision

**D1. Retain repository hooks only.** Create runs `.agents/setup` from
the pin when present. Wake runs `.agents/resume` only. The plane
stores no pre-clone or pre-setup script.

**D2. Plane-stored hooks stay refused** until a superseding ADR names
the access that must exist before clone and how a hook without the
operator token and without a provider key obtains it. [#504](https://github.com/Sannrox/rusui/issues/504)
remains blocked on that later choice.

## Consequences

- Easier: one setup story. The snapshot hash still covers setup bytes.
- Harder: a private clone that needs a plane-side credential dance
  before git fetch cannot run until a later ADR.
- Schema, policy, runner contract, and public API are unchanged.

## Rejected alternatives

- **Plane-stored pre-clone and pre-setup hooks (option 1).** Adds two
  scripts, a fail-closed create path, and a log, before the pin that
  already names setup. The hook still cannot receive the operator
  token or a provider key, so it cannot do the access work the
  problem statement wants without a later secret decision.

## Validation and reversal

Accept on merge. Validated when prepare still runs only
`.agents/setup` from the repository tree, and execs contain no
pre-clone or pre-setup path.

Reverse with a superseding ADR that names the hook store, the guest
or host it runs on, and the access it is allowed.

## Sources

- [#485](https://github.com/Sannrox/rusui/issues/485)
- [ADR 0007](0007-environment-snapshot.md) Prepare
- Decided against `main` at `4061937`.
