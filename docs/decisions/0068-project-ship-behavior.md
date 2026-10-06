# ADR 0068: Project ship behavior is pull-request or push-base

- Status: Accepted
- Date: 2026-10-06
- Amends: [ADR 0015](0015-agent-publication.md). Implement publication
  is still a pull request when the field is omitted or `pull-request`.
- Supersedes: [ADR 0055](0055-implement-publication-is-pull-request.md)
- Resolves: [#486](https://github.com/Sannrox/rusui/issues/486)
  option 1
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged until
  rewritten; live merge, comment, and close stay unauthorized),
  [#505](https://github.com/Sannrox/rusui/issues/505) (implementation)
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

[#486](https://github.com/Sannrox/rusui/issues/486) asked whether a
project policy field chooses `pull-request` (default) or `push-base`.
[ADR 0055](0055-implement-publication-is-pull-request.md) chose option
2: pull-request only.

The maintainer directed that rusui match a researched
operator-visible remote-environment product unless a rusui contract is
a total conflict. In that product the project chooses how finished
work enters the repository: push the base branch, push a branch that
opens a pull request, or a custom prompt.

Pushing the default branch without a pull request is a land of the
candidate onto the base branch. rusui's dogfood repository stays on
pull-request, and live merge, comment, and close stay unauthorized.
The conflict is the default, not the existence of a project field.
Option 1 keeps omitted and `pull-request` as today's implement
publication, names `push-base` as an explicit project choice, and
keeps dogfood on `pull-request`. That is the rusui shape of the
researched product. Enabling `push-base` in the dogfood policy stays
a non-goal.

[#505](https://github.com/Sannrox/rusui/issues/505) is the
implementation. This ADR does not add the field to `policy.yaml`.

## Decision

**D1. A project `ship` field.** Omitted or `pull-request` keeps
today's implement publication: open or update a pull request.
`push-base` pushes the default branch and does not open a pull
request. Any other value is a policy load error.

**D2. Dogfood stays `pull-request`.** This repository does not set
`push-base`.

**D3. Review and ordinary run stay read-only on GitHub.** Only
implement sessions receive a GitHub write credential ([ADR 0015](0015-agent-publication.md),
[ADR 0020](0020-turn-scoped-github-publication.md)).

**D4. Live merge, comment, and close stay unauthorized.** `push-base`
is a push of the session branch onto the default branch. It is not
GitHub merge of a pull request, not a comment, and not an issue close.

## Consequences

- Easier: a project that must write the default branch can say so in
  policy instead of hoping the agent does it.
- Harder: `push-base` is a land. Operators who set it accept that
  implement sessions write the default branch. Dogfood does not.
- Policy and publication change in #505. Apply stays dry-run for
  comment, close, and merge.
- `ARCHITECTURE.md` changes only when that file is next rewritten.

## Rejected alternatives

- **Pull-request only (option 2 / ADR 0055).** Conflicts with the
  directed product shape. The total conflict is dogfood land and live
  GitHub merge/comment/close, which this ADR still refuses.

- **Default `push-base`.** Would land dogfood without a pull request.
  Refused.

## Validation and reversal

Accept on merge. Validated when #505 lands: omitted still opens one
pull request and does not push the default branch; `push-base` pushes
only that branch; review and ordinary run still have no GitHub write
credential.

Reverse with a superseding ADR that drops the field.

## Sources

- [#486](https://github.com/Sannrox/rusui/issues/486)
- [ADR 0055](0055-implement-publication-is-pull-request.md)
- [ADR 0015](0015-agent-publication.md)
- Decided against `main` at 86cca93.
