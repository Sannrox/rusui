# Repository Guidelines

`rusui` is a self-hosted environment plane. You write `policy.yaml`. The
server admits work onto environments, sessions, turns, runners, events,
and actions; records immutable reviews; dry-runs apply for close; and
posts live review comments only when policy `comment` is true. Automatic
docs repair stays off until policy `repair: docs` (and `implement: true`).
In `implement` sessions the agent proposes a GitHub write; applying it
is a governed Action ([ADR 0080](docs/decisions/0080-turns-propose-writes-go-through-actions.md)).
A run session may propose a definition draft; applying it is `core.draft`
([ADR 0084](docs/decisions/0084-core-draft-actions.md)). Guests never receive a
core credential.
`ARCHITECTURE.md` is the product contract. Do not implement live close,
merge, land, or unnamed GitHub writes. Live comments are the `comment`
policy path only. Direction beyond the current
contract is proposed in `VISION.md` and `ROADMAP.md`; decisions live in
`docs/decisions/`. A proposed ADR does not change the contract until it
is accepted.

Human contributor path: [CONTRIBUTING.md](CONTRIBUTING.md). Docs map:
[docs/README.md](docs/README.md).

## Always

- Treat `ARCHITECTURE.md` as the contract and `CONTEXT.md` as the noun glossary.
- Run `make all && make test && make validate` before claiming a change is
  done. Narrow with
  `make test WHAT=./internal/engine TEST_ARGS='-run ^TestClaim'`.
- Parse issue dependencies only from `## Dependencies`.
- Bind examples to loopback. `-addr` may bind elsewhere; do not document
  `0.0.0.0` unless the change is about listen addresses. The intake GitHub
  client is read-only. Only `implement` sessions receive a GitHub write
  credential (ADR 0015); review sessions never do.
- Use `verify-change` before delivery. Fix actionable review findings.
- Preserve other delivery lanes; never switch, reset, or stash the primary
  checkout.

## Ask first

- Enabling `comment: true` on a bound repository (live GitHub comments).
- Enabling `repair: docs` on a bound repository (automatic docs repair).
- Enabling `close` as a live GitHub write (it is dry-run).
- Passing `-addr 0.0.0.0` or `-allow-insecure` in a runbook.
- Rewriting `ARCHITECTURE.md`, or implementing VISION/ROADMAP as if shipped.
- Opening, labeling, or merging GitHub issues and pull requests.
- Adding a new ADR.
- Pushing commits to `main` (branch protection requires a pull request).

## Never

- Implement live close, merge, land, or unnamed GitHub writes.
- Give a model process a GitHub write token outside an `implement`
  session, or commit secrets, webhook
  secrets, worker secrets, Slack tokens, `*.db`, `.version`, or `_output/`.
- Link private repositories or put private names in public issues, pull
  requests, or documentation.
- Implement from `docs/v1-build-prompt.md` (historical `BUILD.md`).
- Invent live apply from model `confidence = high`.

GitHub Issues are the planning source of truth. The shared agent-kit Skills
define the expected workflows for shaping, delivering, verifying, assessing,
and releasing work (see [Shared agent skills](#shared-agent-skills)):

| Skill | Use when |
| --- | --- |
| `shape-work-item` | turning intent into a focused Issue |
| `advance-issue-frontier` | asking what is ready next |
| `assess-change-impact` | scoping risk across policy, admit, apply, Slack, or store |
| `deliver-ready-issue` | implementing, publishing, or landing one ready Issue |
| `verify-change` | collecting exact local evidence |
| `capture-project-decision` | promoting an accepted choice into an ADR or doc |
| `prepare-release` | assembling release readiness |
| `technical-documentation` | writing or reviewing docs and agent instructions |
| `refactor-docs` | restructuring documentation |

## Build, Test, and Development Commands

Run this before committing:

```bash
make all && make test && make validate
```

- `make all` builds host-platform binaries into `_output/`.
- `make test` runs unit tests. Narrow with
  `make test WHAT=./internal/engine TEST_ARGS='-run ^TestClaim'`.
- `make validate` runs golangci-lint, govulncheck, go-fix, shellcheck, workflow skills, and release-script checks.
- `make release-images` builds Linux binaries and runtime images when
  Docker is available.
- `make guest-image` builds the reference guest image
  (`build/guest-image`, ADR 0018).

The Makefile is a thin façade over `scripts/make-targets/`. Verification
should be proportional to the change. Before delivery, use `verify-change`.
Before commit or PR, fix actionable review findings.

## Issue conventions

Draft Issues with problem, observable outcome, acceptance evidence,
non-goals, `## Dependencies`, and impact. Parse dependencies only from
`## Dependencies`. Prefer `status:ready` and `status:blocked` labels
when mutation is authorized.

Each delivered issue is one lane: one claim branch `<type>/<issue>` on GitHub,
one worktree at `.worktrees/issue-<issue>` (or a dedicated clone on another
machine), one pull request, one owner. Claims live on GitHub because lanes run
on several machines; claim with
`bash "${AGENT_KIT_HOME:-$HOME/.agents/agent-kit}"/skills/deliver-ready-issue/scripts/issue-lane.sh claim <issue>` before
implementing. Agents never switch, reset, or stash the primary checkout;
inspect `git status -sb` and `git worktree list` before Git or GitHub work and
preserve other lanes. See [Parallel delivery lanes](#parallel-delivery-lanes).

## Parallel delivery lanes

A delivery lane is one issue, one branch, one isolated checkout, one pull
request, and one owner. Several agents may deliver several issues at the same
time, on one machine or on many, only as separate lanes. One issue is never
split across lanes, and one lane never carries a second issue.

### Claims live on GitHub

Machines cannot see each other's worktrees, so a claim is only what GitHub
shows. An issue is claimed, and therefore active, when any of the following
exists:

1. an open pull request, draft or ready, that references the issue;
2. a branch `<type>/<issue>`, or any branch matching `*/<issue>-*`;
3. the authenticated login assigned to the issue less than six hours ago.

An assignment older than six hours with neither branch nor pull request is an
ownership hint, not a veto: report it and ask the maintainer before claiming.
A claim is stale once its issue is closed or its pull request has merged; its
owner or the maintainer removes it. Any other takeover first preserves the
existing branch, pull request, and evidence and requires the maintainer's
confirmation, unless the lead of the same parallel run owns the stalled lane.

A lane claims before it implements, under Publish or Land authority or with an
explicit claim authorization under Implement, using
`bash "${AGENT_KIT_HOME:-$HOME/.agents/agent-kit}"/skills/deliver-ready-issue/scripts/issue-lane.sh claim <issue>`:

- the branch `<type>/<issue>` is created on GitHub from the default branch by
  an atomic ref creation, so when two machines race exactly one claim
  succeeds and the other sees `claimed`;
- the authenticated login is assigned to the issue, which shows the claim in
  the issue list and timestamps it in the issue timeline.

`<type>` is derived deterministically so that every machine computes the same
branch: the Conventional Commit type in the issue title, with `bug` mapped to
`fix`; otherwise the type label (`bug` → `fix`, `enhancement` → `feat`,
`documentation` → `docs`); otherwise `chore`. Lane branches carry no slug
because the name is the claim; the pull request title carries the description.
Human topic branches may keep `<type>/<issue>-<slug>` and are detected as
claims by the same rule, as are legacy `codex/<issue>-<slug>` branches.

An Implement-only lane cannot claim and is invisible to other machines. Say so
in the report, or ask for claim authority before starting.

### Isolation on each machine

Local isolation protects lanes that share a machine; it is not a claim.

- Each lane checks out its claim branch in `.worktrees/issue-<issue>` inside
  the repository, ignored by Git. A fresh clone dedicated to the lane, as on a
  cloud agent, is equivalent. The base SHA is recorded in the lane report.
- The primary checkout belongs to the maintainer. Agents never switch its
  branch, reset it, stash it, or run long jobs in it while another lane is
  active.
- A worktree serves exactly one issue. Finished lanes are removed; a worktree is
  never reused for a different issue or renamed to hide its origin.
- Shared Git state is mutated in short, serialized slots: `git fetch --prune`,
  worktree creation and removal, local branch deletion, and merging belong to
  the lead of a parallel run or happen one lane at a time on a shared machine.
  A lane commits and publishes its own branch from its own worktree; that is
  not a shared mutation. Nobody holds a slot across implementation, test runs,
  or a remote wait.

### Limits, collisions, and landing

The three-lane limit counts claims visible on GitHub per repository: open
implementation pull requests plus claim branches without one. Assigned or
planned work with neither is not a running lane.

Parallel lanes must not collide. Collision surfaces in this repository are
`policy.yaml`, admit/lease, GitHub-client, apply, Slack, and persistence.
Lanes that would both change one of these surfaces run in sequence, not in
parallel.

A lane publishes from its worktree with
`bash "${AGENT_KIT_HOME:-$HOME/.agents/agent-kit}"/skills/deliver-ready-issue/scripts/issue-lane.sh push <issue>`,
which creates GitHub-verified commits on the claim branch and resets the
worktree to the published commit, so local `HEAD` is the pull request head.
Open the pull request as a draft that closes the issue and includes a scrubbed
public lane brief: agent role or opaque agent ID, claim branch, base SHA,
authority ceiling, relative worktree path (or `fresh clone`), and verification
evidence. Do not include hostnames, home paths, or absolute worktree paths in
GitHub content. Keep machine and checkout identity in the private lead ledger;
never copy it into a public issue, pull request, or comment. Mark the pull
request ready when verification and review are complete.
Immediately before the first publish, the lane fetches and confirms that the
default branch is an ancestor of its head; it refreshes onto `main` only for a
conflict, a failing gate, an explicit request, or a sibling landing on a
shared surface, not merely because `main` advanced.

Land with
`bash "${AGENT_KIT_HOME:-$HOME/.agents/agent-kit}"/skills/deliver-ready-issue/scripts/issue-lane.sh land <issue> --pr <pr>`.
It squash-merges only the reviewed head and confirms the merge and the closed
issue. Landing is sequential. After each merge, fetch, recompute the frontier,
and let the remaining lanes recheck `mergeable` against the new `main`.

The lead of a parallel run keeps a private ledger per lane: issue, branch,
machine and checkout, base SHA, owner, state, pull request, evidence, blockers,
and cleanup. Keep that ledger private; GitHub content uses only the scrubbed
public lane brief above. Report verified outcomes, not launched work. The
executable lead procedure is `references/parallel-delivery.md` in the shared
`deliver-ready-issue` Skill.

## Security

Never commit secrets, GitHub or Slack tokens, webhook secrets, worker
secrets, local SQLite databases, `.version`, or `_output/`. Bind examples
to loopback. The intake GitHub client is read-only. Model CLIs receive
a GitHub write credential only in `implement` sessions (ADR 0015). This repository is public; do not link private
repos. Canonical agent policy is this file; `AGENT.md` and `CLAUDE.md`
are pointers. Skill stubs live in `.agents/skills/`.

## Shared agent skills

The workflow Skills (`advance-issue-frontier`, `assess-change-impact`,
`capture-project-decision`, `deliver-ready-issue`, `prepare-release`,
`refactor-docs`, `shape-work-item`, `technical-documentation`, `verify-change`)
are canonical in [Sannrox/agent-kit](https://github.com/Sannrox/agent-kit).
There is no pin: this repository always uses the latest `main`.
`.agents/skills/<name>/` holds only a generated stub that points to the shared
skill; read the installed skill under `~/.agents/agent-kit/` (or
`$AGENT_KIT_HOME`). Run `bash scripts/validate-workflow-skills.sh` to clone or
update it; `make validate` runs the same check.

- Repository-specific facts the shared Skills need come from this file. Edit
  this file, not the stubs.
- Never edit the stubs or the vendored `scripts/validate-workflow-skills.sh`
  here; change them upstream in agent-kit, then run
  `bash scripts/validate-workflow-skills.sh --render` here when the check
  reports drift.
- Repository-only Skills live next to the stubs as full folders and stay local.

## Agent skill profile

Facts the shared agent-kit skills read that the sections above do not already
state.

Feature: a new observable operator or integration outcome.

- Read `ARCHITECTURE.md` (the product contract), `CONTEXT.md`, and
  `docs/README.md` alongside `README.md`. Do not implement behavior beyond the
  current contract except as types or comments needed to keep state machines
  honest.
- Issue dependencies live under `## Dependencies` (not
  `## GitHub dependencies`). An explicit statement that prerequisites are
  completed counts as delivered; `None` means no dependencies.
- GitHub Discussions are disabled. Route cross-boundary, trust-model,
  policy-capability, or difficult-to-reverse choices to an ADR pull request.
- ADRs: allocate the next number in `docs/decisions/`, follow
  `docs/decisions/template.md`, and regenerate the index with
  `scripts/adr-index`; never edit its table by hand. An accepted ADR does not
  change `ARCHITECTURE.md` until that file is rewritten.
- CI is `.github/workflows/build.yml`; its gates are the commands in
  [Build, Test, and Development Commands](#build-test-and-development-commands).
- Before publishing, inspect the diff for SQLite databases, `.version`,
  `_output/`, secrets, and runtime state.

### Compatibility and failure obligations

Fresh and upgraded SQLite state, immutable review revisions, policy reload
(the SQLite overlay may only narrow `policy.yaml`), old clients and
configuration, error semantics, and rollback or recovery impact.

### Additional impact boundaries

- hard defaults versus `policy.yaml` baseline versus SQLite overlay;
- webhook ACK (no live fetch) versus refresh coordinator versus admit;
- GitHub as intake versus the server as source of truth versus Slack as the
  human socket;
- read-only GitHub client versus apply (dry-run);
- model CLI review artifacts versus credentials (only `implement` sessions
  receive a GitHub write credential, ADR 0015);
- `review` / `apply` / `implement` lanes and lease generations;
- loopback bind versus operator tunnel;
- SQLite recovery truth versus optional JSON/MD exports;
- durable artifacts: docs, `policy.example.yaml`, `policy.fixture.yaml`, ADRs.

### Verification matrix

Rows beyond [Build, Test, and Development Commands](#build-test-and-development-commands):

| Surface | Required evidence |
| --- | --- |
| Go source | focused `make test WHAT=./internal/<pkg>`, then the relevant build |
| admit, lease, refresh, or apply | state-transition tests for success, expiry, steal-deny, and recovery |
| GitHub client | read-only client tests; never require a write token |
| Slack commands | HMAC, skew, allowlist, and command-routing tests |
| policy | parse/default/fail-closed tests; `policy.example.yaml` vs `policy.fixture.yaml` |
| SQLite persistence | store tests for revisions, leases, and immutable review blobs |
| review eval | `eval/set.md` path when review-quality claims change |
| packaging or runtime images | `make release-images` when Docker is intentionally available |
| live GitHub, Slack, tunnel, or image behavior | the real boundary with isolated state; unit or mocked tests do not establish it |

### Release audit

- workspace version is the `VERSION` file; a `v*` tag must equal `v` plus
  that value. Binary ldflags still come from git tags via
  `scripts/lib/version.sh`; generated `.version` is not a source;
- `scripts/validate-release.sh` checks the tag-triggered release path: signed
  annotated tags, CHANGELOG notes, SBOM, and the family release manifest.
  Pull requests and dry runs must not publish or attest;
- `go.mod` / `.go-version` consistency;
- `policy.example.yaml` defaults and fail-closed unknown fields;
- HTTP, Slack, and worker endpoint compatibility;
- SQLite schema and immutable review-artifact compatibility;
- environment variables, secrets, loopback bind, and operator tunnel docs;
- security, backup, rollback, and operator actions;
- `rusui` / `rusui-runner` binaries and runtime images;
- GitHub Release notes come from the matching CHANGELOG section.
