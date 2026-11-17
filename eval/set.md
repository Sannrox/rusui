# Maintenance evaluation corpus

A versioned corpus of recorded review results with maintainer judgments.
`rusui eval` replays it through admit, claim, complete, and dry-run apply
against a fake GitHub, then scores the plane's writes against the
judgments and the promotion gates of
[ADR 0038](../docs/decisions/0038-maintenance-eligibility-and-promotion.md).
No model runs and nothing is written to GitHub.

Run and update the corpus with the [evaluation how-to](../docs/evaluate-reviews.md).

## Splits

- **Development** ([corpus/development.json](corpus/development.json)) is
  public and synthetic. Reports list each case. [results.md](results.md)
  is its replay; `go test ./eval` fails when the replay no longer matches.
- **Held-out** is operator-owned and lives outside the repository. Its
  judgments stay human-owned. Reports carry its digest and totals, never
  its cases, titles, bodies, or notes. Promotion gates read only this
  split, so without it every promotable class is `incomplete`.

## Case format

Each file holds `version` (1), `split`, the scenario `clock`, the
`policy` text the replay runs under, and `cases`. A case has:

| field | meaning |
|---|---|
| `id`, `tags` | identity and situation: `useful_finding`, `incorrect_finding`, `stale_event`, `duplicate_event`, `protected_work`, `existing_implementation`, `adversarial_instruction`, `ambiguous_requirement` |
| `item`, `context` | GitHub item snapshot, and other items it references |
| `edited` | for `stale_event`: the item after it changes while the review runs |
| `result` | the recorded review result standing in for the guest; claim fields are filled at replay |
| `judgment` | `write` the maintainer wants (`none`, `comment`, `close`), other `allowed` writes, `disposition` of the finding (`useful`, `neutral`, `harmful`), `wrong_finding`, `correction_minutes`, `note` |

Unknown fields fail to load.

## Measures

Per split: cases, agreement, false actions (a write the judgment does
not allow), wrong actions (a false action on protected or stale work),
missed writes, duplicate work (extra writes or jobs from an
unchanged redelivery), finding dispositions, wrong findings, correction
minutes on disagreements, and recorded tokens with the count of cases
whose cost is unknown. The advisory rollback rule reads the last 20
held-out dispositions in case order. Model confidence is not a measure.

## Evidence identity

A report names its revision, replay version, and a SHA-256 digest of
each split, covering cases, judgments, clock, and policy. Evidence
applies only to identical inputs: change a judgment, the policy, the
scorer, or the revision and `-check` reports it stale. Without
`-revision`, the build's own revision is used. It is trusted only for a
`make` build whose linked commit matches a clean VCS stamp; otherwise it
gets `-dirty` (local changes) or `-unverified` (`go run`, a plain `go
build`, or a stamp from an enclosing checkout). Neither ever matches. `-revision` is
the operator's assertion and is trusted as given; a detected local change
still adds `-dirty`. Pass it only for a clean checkout of that revision. `-out` names the
file from those inputs and refuses to overwrite it, so prior results are
kept.

## Adding cases

For the steps to add cases, see the
[evaluation how-to](../docs/evaluate-reviews.md#add-a-development-case).

## Task runs

[`eval/taskrun`](taskrun/main.go) is a development tool, not part of the
shipped binary. It runs historical implementation tasks through a running
plane and scores each published head with checks the agent never sees.
The project must set `task_base: ancestor`
([configuration](../docs/configuration.md)) so each task is admitted at
its own base.

```sh
go run ./eval/taskrun -corpus FILE -policy FILE -revision REV \
  [-guest NAME] [-url URL] [-out DIR] [-check PRIOR]
```

It admits pinned runs with the worker secret (`RUSUI_WORKER_SECRET`) and
reads turns with the operator token (`RUSUI_OPERATOR_TOKEN`). Tasks run
one at a time. A rerun with the same inputs resumes the same sessions
instead of admitting tasks again.

A task corpus holds `version` (1), `project`, `repo`, `ref`, an optional
`gate` command, and `tasks`. A task has:

| field | meaning |
|---|---|
| `id` | lowercase identity, unique in the corpus |
| `base_sha` | full commit SHA the task starts from |
| `prompt`, `allowed_paths` | the only task fields sent to the plane |
| `test_patch` | held-back tests, applied outside the guest before checks |
| `checks` | commands (argv lists) that must fail on the base and pass on the head |

Unknown fields fail to load. Checks and the test patch never enter the
prompt, the guest workspace, or the guest's grant. They run in fresh
checkouts on the operator's machine with an empty `HOME` and `TMPDIR`
and only `PATH` and `LANG` from the caller's environment. That keeps
plane tokens and the caller's credential files out of reach, but it is
not a sandbox: checks run the agent's code as the calling user, so run
the tool where that user holds nothing else worth taking.

Each task scores `verified` (checks fail on the base and pass on the
head, and the gate passes on the unpatched head), `failed` (a check or
the gate fails on the head), `invalid` (a check already passes on the
base), or `not_verified` (no published head: blocked, unconfirmed, no
pull request, or timeout). A gate failure is also counted as a
regression. Per task the report records interventions (operator
permission decisions and turns after the first), tokens, and wall clock.
Totals add tokens per verified success. A task with unreported usage
counts in `unknown_tokens` instead of the token sum.

A report names the candidate revision, guest, and SHA-256 digests of the
policy file and the corpus file. As with replay evidence, a change to any
of them, or to the scorer, makes `-check` report it stale (exit 3) before any task is admitted, and
`-out` never replaces an existing report. Every report also states that
guests can reach the source repository's public history, so a task's
upstream fix may be visible to the agent.
