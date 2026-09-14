# Repository Guidelines

`rusui` is a personal GitHub maintenance control plane. You write
`policy.yaml`. The server admits work, records immutable reviews, and
dry-runs apply. `ARCHITECTURE.md` is the product contract. Do not
implement v2/v3 behavior except as types or comments needed to keep
state machines honest. Direction beyond v1 is proposed in `VISION.md`
and `ROADMAP.md`; decisions live in `docs/decisions/`. A proposed ADR
does not change the contract until it is accepted.

GitHub Issues are the planning source of truth. Project-specific Skills
under `.agents/skills/` define the expected workflows for shaping,
delivering, verifying, assessing, and releasing work:

| Skill | Use when |
| --- | --- |
| `shape-work-item` | turning intent into a focused Issue |
| `advance-issue-frontier` | asking what is ready next |
| `assess-change-impact` | scoping risk across policy, admit, apply, Slack, or store |
| `deliver-ready-issue` | implementing, publishing, or landing one ready Issue |
| `verify-change` | collecting exact local evidence |
| `capture-project-decision` | promoting an accepted choice into an ADR or doc |
| `prepare-release` | assembling release readiness |

## Build, Test, and Development Commands

Run this before committing:

```bash
make all && make test && make validate
```

- `make all` builds host-platform binaries into `_output/`.
- `make test` runs unit tests. Narrow with
  `make test WHAT=./internal/engine TEST_ARGS='-run ^TestClaim$'`.
- `make validate` runs golangci-lint, govulncheck, go-fix, and shellcheck.
- `make release-images` builds Linux binaries and runtime images when
  Docker is available.

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
`bash .agents/skills/deliver-ready-issue/scripts/issue-lane.sh claim <issue>` before
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
`bash .agents/skills/deliver-ready-issue/scripts/issue-lane.sh claim <issue>`:

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

A lane publishes its first commit to the claim branch with
`git push -u origin HEAD` (or `git push` if upstream is already set) unless
the user asked otherwise, as a draft pull request that closes the issue and
carries the lane brief: agent, machine, base SHA, and authority ceiling. It
marks the pull request ready when verification and review are complete.
Immediately before publishing, the lane fetches and confirms that the default
branch is an ancestor of its head; it refreshes onto `main` only for a
conflict, a failing gate, an explicit request, or a sibling landing on a
shared surface, not merely because `main` advanced.

Landing is sequential. After each merge, fetch, recompute the frontier, and let
the remaining lanes recheck `mergeable` against the new `main`. A failed or
timed-out merge response may still have merged; reconcile the remote state
before retrying.

The lead of a parallel run keeps a ledger per lane: issue, branch, machine and
checkout, base SHA, owner, state, pull request, evidence, blockers, and
cleanup. Report verified outcomes, not launched work. The executable lead
procedure is `.agents/skills/deliver-ready-issue/references/parallel-delivery.md`.

## Security

Never commit secrets, GitHub or Slack tokens, webhook secrets, worker
secrets, local SQLite databases, `.version`, or `_output/`. Bind the
HTTP server to loopback. The GitHub client is read-only. Model CLIs
never receive GitHub write tokens.
