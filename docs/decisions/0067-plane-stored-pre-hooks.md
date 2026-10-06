# ADR 0067: Plane-stored pre-clone and pre-setup hooks

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0007](0007-environment-snapshot.md) Prepare. Repository
  `.agents/setup` and `.agents/resume` still run. The plane may also
  store a pre-clone hook and a pre-setup hook.
- Supersedes: [ADR 0054](0054-repository-hooks-only.md)
- Resolves: [#485](https://github.com/Sannrox/rusui/issues/485)
  option 1
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until
  rewritten), [#504](https://github.com/Sannrox/rusui/issues/504)
  (implementation), [#334](https://github.com/Sannrox/rusui/issues/334)
  (hook logs)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#485](https://github.com/Sannrox/rusui/issues/485) asked whether a
project may store a pre-clone hook and a pre-setup hook on the plane,
with failure stopping create, and output in the session log.
[ADR 0054](0054-repository-hooks-only.md) chose option 2: only
repository `.agents/setup` and `.agents/resume`.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product the project holds scripts that run
before clone and before repository setup, stored outside the
repository, so access that the clone itself needs can exist first.
Repository setup and resume still run from the tree.

That product does not totally conflict with rusui. The hooks receive
no operator token and no provider key, matching the issue. Wake still
runs `.agents/resume` only.

[#504](https://github.com/Sannrox/rusui/issues/504) is the
implementation. This ADR does not add schema.

## Decision

**D1. Plane-stored pre-clone and pre-setup.** A project may store a
pre-clone hook and a pre-setup hook on the plane. Pre-clone runs
before clone. Pre-setup runs immediately before `.agents/setup`.

**D2. Failure stops create.** A non-zero exit leaves no environment.
Output is in the session log.

**D3. No operator token, no provider key.** The hook environment does
not receive `RUSUI_OPERATOR_TOKEN` or a model-provider key.

**D4. Repository hooks stand.** `.agents/setup` from the pin still
runs on prepare. Wake still runs `.agents/resume` only. This ADR does
not replace resume.

## Consequences

- Easier: a project can prepare clone credentials or a private index
  before the tree exists.
- Harder: the plane now stores executable text. Operators must treat
  those hooks as trusted code. A hook that copies a secret into the
  snapshot is a #504 hazard to test.
- Environment create changes in #504. Guest link destinations are
  unchanged.
- `ARCHITECTURE.md` changes only when that file is next rewritten.

## Rejected alternatives

- **Repository hooks only (option 2 / ADR 0054).** Conflicts with the
  directed product shape. Not a total rusui-contract conflict that
  would keep it.

- **Hooks that receive the operator token.** Issue non-goal.

- **Replacing `.agents/resume` on wake.** Issue non-goal.

## Validation and reversal

Accept on merge. Validated when #504 lands: a failing pre-clone leaves
no environment; a successful pair runs in order once per create; a
repository without plane hooks behaves as today.

Reverse with a superseding ADR that drops plane-stored hooks.

## Sources

- [#485](https://github.com/Sannrox/rusui/issues/485)
- [ADR 0054](0054-repository-hooks-only.md)
- [ADR 0007](0007-environment-snapshot.md)
- Decided against `main` at `d885287`.
