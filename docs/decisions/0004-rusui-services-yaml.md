# ADR 0004: services.yaml is `.rusui/services.yaml` only

- Status: Accepted
- Date: 2026-09-14
- Amends: [ADR 0001](0001-environment-plane.md) D10 (service
  declaration path). `.agents/setup`, `.agents/resume`, and `AGENTS.md`
  stand.
- Resolves: [#35](https://github.com/Sannrox/rusui/issues/35)
- Discussion: none. Merging with this status is the acceptance act.

## Context

#33 taught the environment supervisor to load `.rusui/services.yaml`
and, if that file was missing, `.amp/services.yaml`. The fallback copied
another product's workspace convention so existing repositories could
run without a rename. The maintainer rejected that path on 2026-09-14:
rusui must not read `.amp`.

## Decision

- The only services file is `.rusui/services.yaml`.
- A missing rusui file is a no-op. Any other path, including a valid
  `.amp/services.yaml`, is ignored.
- The mapping schema from #33 (`services.<name>.command`, optional
  `cwd` and `env`) does not change.

## Consequences

- Repositories that only declare services under another product's
  directory will not start those processes on wake until they add
  `.rusui/services.yaml`.
- The plane no longer couples its workspace contract to a foreign
  filename.

## Rejected alternatives

- **Keep the fallback.** Cheapest compatibility; the maintainer
  declined it.
- **Symlink or copy on setup.** Hidden coupling; still treats the
  foreign file as input.

## Validation and reversal

`internal/env` tests prove a missing rusui file starts nothing and that
a present rusui file is the only source. Reversal is a new ADR that
restores a named fallback.

## Sources

- [#35](https://github.com/Sannrox/rusui/issues/35)
- [#33](https://github.com/Sannrox/rusui/issues/33) / [PR #34](https://github.com/Sannrox/rusui/pull/34)
