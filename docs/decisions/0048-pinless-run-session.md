# ADR 0048: A project without a repository runs pinless sessions claimed by project key

- Status: Proposed
- Date: 2026-10-06
- Amends: [ADR 0007](0007-environment-snapshot.md) (P1 snapshots require a
  git pin).
- Resolves: [#493](https://github.com/Sannrox/rusui/issues/493)
- Related: [ADR 0005](0005-policy-v2-project.md) (a project with no repos
  may run `run` and `scheduled`), [ARCHITECTURE.md](../../ARCHITECTURE.md)
  (claim and snapshot contract; unchanged until that file is rewritten).
- Discussion: none. Merging with this status changed to Accepted is the
  acceptance act.

## Context

[ADR 0005](0005-policy-v2-project.md) lets a project bind no repository
and still allow `run` and `scheduled` sessions, and policy v2 parses
`repos: {}`. [ADR 0007](0007-environment-snapshot.md) then refuses the
environment: a P1 snapshot needs a git pin, and a project with no bound
repository "fails at environment create". Its reversal clause names this
case: reopen if a P1 session must start without a git pin.

The rest of the session path is keyed by repository as well:

- `rusui run` fails with "project has no bound repo";
- a runner claims with `-repo`, and the plane selects turns by
  `sessions.repo`;
- `sessions.repo`, snapshot rows, and operator item numbers are keyed by a
  non-empty repository string;
- the source hash takes its pin from the bound repository's default branch.

#493 asks for a `run` session whose workspace starts empty, whose snapshot
is the base image only, and that never needs a pin, while review on the
same project stays refused.

## Decision

**D1. Pinless snapshot.** A session of a project with no bound repository
has no git pin. Its `source_hash` is the digest of the base image identity
with an empty pin and no setup bytes. The workspace starts empty, with no
`.git`. Setup and resume are skipped: there is no repository to hold
`.agents/setup` or `.agents/resume`.

**D2. Project key.** Such a session stores the reserved key
`project:<slug>` where a repository name would go (`sessions.repo`,
snapshot rows, item numbering). A policy repository name is `owner/name`
and never contains `:`, so the key cannot collide with one. Only a project
with zero bound repositories gets a project key; a project with any bound
repository keeps today's repository keys and pins.

**D3. Claim.** A runner started with `-project <slug>` claims work for that
project's key; `-repo` and `-project` are exclusive. The claim gate checks
that the project exists in policy, has no bound repository, and allows the
session kind. Everything else in the claim contract (lease, generation,
deadline, retry, claim token) is unchanged.

**D4. No GitHub.** A project-key session never reaches GitHub: no intake,
no review, no implement (`-effort`, `-base-sha`, and `-paths` are refused),
no git proxy remote, and no publication. Code that reads a repository
name refuses a project key rather than calling GitHub with it.

## Consequences

- Easier: an operator can run a guest in an empty workspace without
  binding a scratch repository.
- No schema migration: the key fits the existing repository columns.
- Runner contract: a new `-project` flag. Existing runners are unaffected.
- Trust model: unchanged. A project-key session holds no GitHub credential,
  and the guest keeps its usual per-turn model grant.
- Every path that treats `sessions.repo` as a GitHub repository must guard
  the key; the implementation lists and tests those guards.
- `ARCHITECTURE.md` still describes a repository-keyed claim and a required
  pin. It changes only when that file is next rewritten.

## Rejected alternatives

- **Project-scoped claim for every session** (claim by `{project}` or
  `{repo}`, empty `sessions.repo`, snapshots and item numbers per project).
  The cleaner model, but it needs a migration and rewrites the claim
  contract for sessions that already work.
- **Keep ADR 0007 and bind a scratch repository.** No code, but #493's
  outcome (no pin, base image only) is not delivered and the operator
  manages a repository they do not want.

## Validation and reversal

Accept on merge. Validated when, on a project with `repos: {}` and
`session_kinds: [run]`:

- `rusui run -project SLUG PROMPT` creates a session whose turn a
  `-project SLUG` runner claims and runs, with no git pin;
- its `source_hash` depends only on the image identity;
- setup and resume do not run;
- a review on that project is still refused, and `-effort` is refused.

Reverse by removing the `-project` claim path. Sessions already stored
with a project key stay readable; new ones are refused again at create.

## Sources

- [#493](https://github.com/Sannrox/rusui/issues/493)
- [ADR 0005](0005-policy-v2-project.md), [ADR 0007](0007-environment-snapshot.md)
- Decided against `main` at `3a20923`.
