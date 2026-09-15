---
name: deliver-ready-issue
description: Deliver a dependency-ready rusui GitHub issue through a bounded implementation workflow in an isolated worktree lane. Use when asked to implement, publish, or land a specific ready issue, to take the next explicitly approved frontier item through verification and review, or to run several ready issues as parallel lanes.
---

# Deliver Ready Issue

Take one approved issue from readiness check to the highest delivery stage the
user authorized. Keep the issue as planning truth and the pull request as
implementation truth. A pasted issue reference is context, not authority to
publish or to widen the task.

One run of this Skill is one delivery lane: one issue, one claim branch
`<type>/<issue>` on GitHub, one worktree, one pull request, one owner. Claims
live on GitHub because lanes may run on different machines; worktrees only
isolate lanes that share a machine. To deliver several ready issues at once,
the lead follows [references/parallel-delivery.md](references/parallel-delivery.md)
and runs this Skill once per lane. [scripts/issue-lane.sh](scripts/issue-lane.sh)
inspects, claims, and releases a lane deterministically.

## Establish the authority ceiling

Infer the ceiling from the user's explicit request. When it is unclear, choose
the lower ceiling and state what remains:

- **Implement**: change the local working tree and verify it.
- **Publish**: implement, commit, push, and open a ready pull request.
- **Land**: publish, resolve review and CI, then merge and clean up.

Permission for a higher stage includes its preceding stages. It never includes
unrelated issue creation, prioritization, assignment, release publication,
force-pushing protected branches, or weakening repository protections.

## Deliver the issue

### 1. Prove readiness

1. Resolve the exact repository and issue. If the user asks for the "next"
   issue, require an explicit selection or a recommendation produced by
   `advance-issue-frontier` before starting.
2. Read `README.md`, `ARCHITECTURE.md`, `docs/README.md`, the issue, linked
   decisions, and the live pull request and issue state. `ARCHITECTURE.md` is
   the product contract; do not implement v2/v3 behavior except as types or
   comments needed to keep state machines honest.
3. Read `## Dependencies` literally. Treat an open predecessor or unresolved
   non-issue dependency as blocking; accept explicit completed-prerequisite
   evidence as delivered.
4. Check the claim state on GitHub, the only state other machines share:

   ```sh
   bash .agents/skills/deliver-ready-issue/scripts/issue-lane.sh check <issue>
   ```

   It reports open pull requests referencing the issue, claim branches
   (`<type>/<issue>`, `*/<issue>-*`), assignees with the assignment age, and
   a verdict; exit code 3 is `claimed`. A claim means another lane owns the
   issue: continue only when the user directs the takeover, and preserve the
   existing branch, pull request, and evidence first. An assignment older than
   six hours without branch or pull request is reported as a hint; ask the
   maintainer before claiming. Then inspect this machine with
   `git worktree list --porcelain` and `git branch --list '*/<issue>*'`; a
   local lane for the issue is somebody's isolation, not yours.
5. Confirm that the issue is open, unblocked, focused enough for one pull
   request, and has testable acceptance evidence.

Stop without creating a branch when readiness, ownership, or dependencies are
ambiguous. Report the smallest action that would unblock delivery.

### 2. Isolate the work

1. Inspect `git status -sb` and `git worktree list`. Preserve every unrelated
   change, branch, worktree, and running process. Never switch, reset, stash,
   or clean the primary checkout or another lane's worktree.
2. Claim the issue on GitHub before implementing, under Publish or Land
   authority or an explicit claim authorization:

   ```sh
   bash .agents/skills/deliver-ready-issue/scripts/issue-lane.sh claim <issue>
   ```

   The script derives `<type>` from the issue, creates `<type>/<issue>` on
   GitHub from the default branch with an atomic ref creation, assigns you, and
   prints the branch and base SHA. Exit code 3 means another machine claimed
   first: stop and report; do not create a differently named branch. Under an
   Implement-only ceiling, do not claim; state in the report that the issue
   stays unclaimed and invisible to other machines, or ask for claim
   authority first.
3. Check the claim branch out in the lane worktree and confirm the base SHA:

   ```sh
   git fetch --prune origin
   git worktree add --track -b <type>/<issue> .worktrees/issue-<issue> origin/<type>/<issue>
   git -C .worktrees/issue-<issue> rev-parse HEAD
   ```

   On a machine without an existing checkout, a fresh clone on the claim branch
   is the lane worktree. Do all further work inside it. If the worktree already
   exists, another lane on this machine owns it; return to step 1.4.
4. When other lanes are active on the machine, serialize shared Git mutations:
   `fetch --prune`, worktree creation or removal, local branch deletion, and
   merges happen one at a time and never while a sibling lane runs them.
   Committing and publishing your own branch from your own worktree is not a
   shared mutation.

Never discard, overwrite, stash, or commit unrelated work merely to obtain a
clean tree, and never reuse a worktree for a different issue. Beyond the
branch and the assignment the script creates, do not comment on or otherwise
mark the issue unless documented maintainer policy requires it.

### 3. Bound the implementation

1. Run `assess-change-impact` against the issue and affected paths.
2. Translate the issue acceptance evidence into code, tests, documentation,
   policy, configuration, compatibility, and security obligations.
3. Implement one coherent outcome. Avoid opportunistic cleanup.
4. If the issue cannot produce one reviewable pull request, stop and recommend
   a split. Do not create follow-up issues without authorization.

### 4. Verify and review

1. Add focused deterministic tests while implementing.
2. Run `verify-change` for intermediate local evidence and retain exact
   commands, results, skips, and remaining uncertainty. Dirty-worktree
   results are not the final PR proof.
3. Run `autoreview` before committing when that skill is available. Fix
   actionable findings and rerun the relevant checks until no material
   finding remains or a documented blocker requires maintainer judgment.
4. Inspect the final diff for scope, generated artifacts, secrets, SQLite
   databases, `.version`, `_output/`, and accidental runtime state.

### 5. Publish when authorized

1. Stage only intended paths and create a narrow imperative commit.
   Never use `--no-gpg-sign`. If signing fails, stop and fix GPG.
   Immediately before publishing, run `git fetch --prune origin` and
   `git merge-base --is-ancestor origin/main HEAD`. If the check fails, refresh
   the branch onto `main` and rerun the affected checks. Otherwise refresh only
   for a failing gate, an explicit request, or a sibling lane that landed on a
   shared surface (`policy.yaml`, admit/lease, GitHub-client, apply, Slack,
   persistence). Do not rebase merely because `main` advanced.
2. Run `verify-change` again against the committed candidate before publishing.
   If this final verification or any review fix changes content, repeat review,
   commit, and final verification before continuing.
3. Publish the claim branch with `git push -u origin HEAD` (or `git push` if
   upstream is already set) unless the user asked for a different publish path.
   Do not invent a verified-push script.
4. Open the pull request as a draft with the first published commit, then mark
   it ready once verification and review are complete and `mergeable` is no
   longer `UNKNOWN` (`gh pr ready <pr>`). The pull request:
   - closes the issue with a visible `Closes #<issue>` line;
   - carries the lane brief: agent, machine, base SHA, authority ceiling;
   - summarizes behavior rather than file operations;
   - lists verification evidence and skipped checks;
   - calls out policy, GitHub-client, apply, Slack, persistence, configuration,
     compatibility, and security impact;
   - includes an agent transcript when the repository workflow requires it.
5. Return the pull request URL. Do not publish when the ceiling is Implement.

### 6. Land when authorized

1. Wait for required CI and review. Resolve actionable feedback in the same
   branch and rerun affected checks. Re-publish review fixes on the same
   branch with `git push` unless the user asked for a different publish path.
2. Recheck that dependencies and repository protections still permit landing.
   Land one lane at a time; wait for a sibling lane's merge to finish and
   recheck `mergeable` before starting yours.
3. Prefer squash merge for linear history on `main`:
   `gh pr merge --squash --delete-branch`.
   Use `--match-head-commit` with the published tip when available.
   A failed or timed-out merge response may still have merged; reconcile the
   remote pull request state and `main` ancestry before retrying.
4. Confirm the issue closed and the default branch contains the merge. The
   merge deleted the claim branch and the closed issue ends the claim; the
   assignment stays as history. Then remove the local lane, serialized with
   other shared Git mutations:

   ```sh
   git worktree remove .worktrees/issue-<issue>
   git branch -D <type>/<issue>
   git worktree prune
   ```

   Squash merges are invisible to `git branch --merged`; confirm the landing
   with `gh pr view <pr> --json state,mergeCommit` before deleting. Leave the
   primary checkout untouched unless the user asks to synchronize it. A lane
   that is abandoned before publishing releases its claim with
   `bash .agents/skills/deliver-ready-issue/scripts/issue-lane.sh release <issue>`,
   which refuses to delete a branch with
   commits or an open pull request unless told to.
5. Invoke `advance-issue-frontier` in report-only mode unless the user also
   authorized frontier status updates.

Never enable auto-merge, bypass checks, relax protection, or merge beyond the
authority ceiling.

## Report completion

Return:

- issue and authority ceiling;
- claim state (claimed, unclaimed under Implement, or taken over), machine and
  worktree, branch, base SHA, final commit, and pull request when created;
- implemented outcome;
- verification and review evidence;
- merge state and newly available follow-up work when applicable;
- blockers, skipped checks, and remaining uncertainty.

## Boundaries

- Deliver only the selected issue; do not choose project priority.
- Do not implement blocked work or infer that silence grants ownership.
- Work only inside your lane worktree. Never switch, reset, stash, or clean the
  primary checkout or another lane's worktree, and never delete a worktree or
  branch you do not own.
- Never work around a lost claim race with a differently named branch, a
  forced ref update, or by removing another lane's assignment.
- Keep secrets, credentials, logs, databases, and runtime state out of Git.
- Do not substitute a successful build for issue acceptance evidence.
- Do not close an issue manually when the implementation has not landed.
- Do not give a model process a GitHub write token, or enable live comment,
  close, implement, or land behavior unless the authorized issue and
  `ARCHITECTURE.md` milestone explicitly require it.
