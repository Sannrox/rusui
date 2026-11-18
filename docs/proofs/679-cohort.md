# #679 benchmark cohort: 30 historical tasks, two runs

> **Historical evidence.** This record applies to its stated runs and
> revision; it is not a current operator procedure.

Predeclared before the first attempt for
[#679](https://github.com/Sannrox/rusui/issues/679). Do not replace a
task, change an expected outcome, or move a threshold after any result is
known. A failed task is never swapped for an easier one. Findings go to
the results record.

## Fixed profile

| Piece | Value |
| --- | --- |
| Candidate | `main` at the commit that merged this file. Plane, runner, and guest image are built from it once; no rebuild between or during runs |
| Repository | `Sannrox/rusui-bench`, a public mirror of this repository's `main` at the candidate. Public so the runner's checks fetch heads without a credential. Its default branch is never rewritten, Actions are off, task pull requests stay there, and nothing merges |
| Corpus | [`eval/corpus/679-tasks.json`](../../eval/corpus/679-tasks.json), sha256 `4a13d7bebec8c29793d6d267aca2376517558038c6276141e0115f48d95e2697` |
| Policy | [679-cohort-policy.yaml](679-cohort-policy.yaml), sha256 `02b770e8863f6b9f16836796a9bf7364a9af6d43981be124bdf489660a06671f`: only `run` sessions in project `bench`, `task_base: ancestor`, `implement: true`, `land: false`, egress class `full`; `read`, `search`, `edit`, `think`, `execute`, and `fetch` allowed, `switch_mode` and `other` rejected; the Claude guest sends no other kind, so no request waits for an operator |
| Runtime | native Linux Docker, container driver, guest link; `rusui diagnose` all ready, including `guest_link` and `model_guest`, before attempt 1 |
| Guest | `claude`, reference image built from the candidate; image digest recorded in the results record |
| Model | `RUSUI_GUEST_MODEL=claude-sonnet-5-5` through the operator's loopback model proxy |
| Publication | plane publication with a GitHub App installed on the mirror only, with contents and pull-request write; guests hold no GitHub credential |
| Plane | eval plane on loopback, fresh database per run, one runner, one task at a time |
| Runner | `eval/taskrun` at the candidate, `-guest claude`, task timeout 2 hours, check timeout 30 minutes |

Egress `full` is part of the profile, not a change to improve scores: a
guest under `trusted` cannot fetch Go modules, so it cannot build or test
its own change. Time savings and general code quality are not claimed.

## Corpus

Each task is an Issue of this repository and the squash commit that
closed it. The base is the commit before it. The test patch is the
commit's change to `_test.go` and `testdata` files, and the check runs
the test functions it added that compile and fail on the base and pass
on the fix twice in a row. Tests named `TestLive*` are excluded from the
checks. The prompt is the Issue's title and body without its acceptance
evidence, Reuse decision, dependency, and bookkeeping sections, and
without lines naming a test or the fixing pull request. Allowed paths
are the directories the fixing commit touched.

Selection rule, applied once to the last 500 first-parent commits on
`main` at the lane base: of the commits that close exactly one Issue,
add a Go test, and change non-test Go code, 53 qualified. Four were
excluded because their checks pass only with the fix's exact wording or
names, which the prompt does not give: #295 (an ADR file name and
sentences), #500 (exact Dockerfile lines), #343 (a method signature
read from source), and #290 (blocked-reason strings absent from the
base). The rest were split into three strata, and the 10 most recent
of each were taken:

- `docs`: the fix also changed a Markdown file;
- `single`: one non-test Go file, no docs;
- `multi`: non-test Go code in two or more packages, no docs.

Before this record merged, each task scored `verified` with its own
historical fix as the head, through the runner's scorer. In the first
pass, four tasks scored at once on a loaded host, and 8 of 30 failed
in under two minutes. A second pass verified all 8. That pass predates
the report's `failure` field, so the cause was not kept. The tasks that
replaced the four exclusions were verified in a third pass.

| ID | Issue | Base | Allowed paths | Held-back tests |
| --- | --- | --- | --- | --- |
| `docs-i750` | [#750](https://github.com/Sannrox/rusui/issues/750) | `24845831d0` | `docs`, `internal/provider`, `internal/runner` | 2 |
| `docs-i684` | [#684](https://github.com/Sannrox/rusui/issues/684) | `4cb1cf6470` | `docs`, `internal/provider`, `internal/runner` | 1 |
| `docs-i682` | [#682](https://github.com/Sannrox/rusui/issues/682) | `190ef1416b` | `docs`, `internal/engine`, `internal/server`, `internal/store` | 2 |
| `docs-i588` | [#588](https://github.com/Sannrox/rusui/issues/588) | `eb6361c188` | `docs`, `internal/engine`, `internal/runner`, `internal/server` | 2 |
| `docs-i442` | [#442](https://github.com/Sannrox/rusui/issues/442) | `9997588bc4` | `docs`, `internal/acp`, `internal/provider` | 1 |
| `docs-i415` | [#415](https://github.com/Sannrox/rusui/issues/415) | `7992b74206` | `docs`, `internal/engine`, `internal/store` | 1 |
| `docs-i401` | [#401](https://github.com/Sannrox/rusui/issues/401) | `686193052f` | `cmd/rusui`, `docs`, `internal/engine`, `internal/server` | 1 |
| `docs-i333` | [#333](https://github.com/Sannrox/rusui/issues/333) | `d64769ea96` | `docs/decisions`, `internal/acp`, `internal/provider` | 1 |
| `docs-i292` | [#292](https://github.com/Sannrox/rusui/issues/292) | `63081d0b97` | `ARCHITECTURE.md`, `docs`, `docs/decisions`, `internal/engine` | 1 |
| `docs-i264` | [#264](https://github.com/Sannrox/rusui/issues/264) | `3902a27bae` | `docs`, `internal/server`, `internal/store` | 1 |
| `single-i687` | [#687](https://github.com/Sannrox/rusui/issues/687) | `c17d8ed0de` | `internal/gh` | 1 |
| `single-i570` | [#570](https://github.com/Sannrox/rusui/issues/570) | `a1da22adc1` | `internal/engine` | 1 |
| `single-i446` | [#446](https://github.com/Sannrox/rusui/issues/446) | `ebb1acac22` | `internal/engine` | 1 |
| `single-i412` | [#412](https://github.com/Sannrox/rusui/issues/412) | `765c6f5d64` | `internal/engine` | 1 |
| `single-i416` | [#416](https://github.com/Sannrox/rusui/issues/416) | `8b310f427a` | `internal/store` | 1 |
| `single-i385` | [#385](https://github.com/Sannrox/rusui/issues/385) | `eee66d6b5a` | `internal/server` | 1 |
| `single-i344` | [#344](https://github.com/Sannrox/rusui/issues/344) | `fe6cda91fc` | `internal/provider` | 1 |
| `single-i345` | [#345](https://github.com/Sannrox/rusui/issues/345) | `c8771814fc` | `internal/server` | 1 |
| `single-i340` | [#340](https://github.com/Sannrox/rusui/issues/340) | `ef28fa575b` | `internal/env` | 1 |
| `single-i312` | [#312](https://github.com/Sannrox/rusui/issues/312) | `4331a4d996` | `internal/engine` | 2 |
| `multi-i569` | [#569](https://github.com/Sannrox/rusui/issues/569) | `bba7f81b0b` | `internal/engine`, `internal/store` | 1 |
| `multi-i557` | [#557](https://github.com/Sannrox/rusui/issues/557) | `87b2f7a77f` | `internal/engine`, `internal/store` | 3 |
| `multi-i448` | [#448](https://github.com/Sannrox/rusui/issues/448) | `2363f0d2dc` | `internal/engine`, `internal/env` | 1 |
| `multi-i437` | [#437](https://github.com/Sannrox/rusui/issues/437) | `2c6d88d88d` | `cmd/rusui`, `internal/engine`, `internal/provider`, `internal/server` | 1 |
| `multi-i441` | [#441](https://github.com/Sannrox/rusui/issues/441) | `fad85edb47` | `internal/engine`, `internal/env` | 1 |
| `multi-i400` | [#400](https://github.com/Sannrox/rusui/issues/400) | `6868c051c8` | `internal/engine`, `internal/gh`, `internal/store` | 1 |
| `multi-i398` | [#398](https://github.com/Sannrox/rusui/issues/398) | `0834a7705a` | `cmd/rusui`, `internal/ops` | 1 |
| `multi-i386` | [#386](https://github.com/Sannrox/rusui/issues/386) | `b24deaddba` | `cmd/rusui`, `internal/engine`, `internal/gh`, `internal/server` | 1 |
| `multi-i291` | [#291](https://github.com/Sannrox/rusui/issues/291) | `501d4d60d9` | `internal/engine`, `internal/store` | 3 |
| `multi-i265` | [#265](https://github.com/Sannrox/rusui/issues/265) | `bb50ecec17` | `cmd/rusui-runner`, `internal/runner`, `internal/server` | 1 |

The repository gate run on every head is
`go build ./... && go vet ./...`. The full test suite is not part of the
gate: on the scoring host it exceeds the per-package test timeout, and
some packages need a container runtime the scorer's empty environment
does not configure, so it fails on the historical fixes too.

## Measures

Per task and run, from the runner's report:

- outcome: `verified`, `failed`, `invalid`, or `not_verified`
  ([scoring](../../eval/set.md));
- interventions: operator permission decisions and turns after the
  first;
- regressions: heads that fail the repository gate;
- tokens and wall clock.

Per run: verified success (count of `verified`), interventions,
regressions, tokens per verified success, and wall clock per verified
success. Approval wait, from the plane's approval records: for each
approval, the time from the pending action to its decision, and the
approvals that timed out against the turn deadline. The policy answers
every kind the Claude guest sends, so any approval record is reported as a defect of
this profile.

## Runs and stop conditions

The first start of run 1, on the original policy, was stopped during
its second task. Under that policy a `fetch` request waited for an
operator approval, and no operator answers in this protocol, so the
first task's turn sat until the 45-minute run deadline. The policy now
answers every kind the Claude guest sends. Nothing from that start counts. Its stores
are kept, and the results record lists its tokens against the ceiling.

- Two full runs on the candidate, each with all 30 tasks in corpus
  order, one attempt per task per run.
- A task is rerun only for a named infrastructure failure: the plane,
  runner, or host stopped; the model proxy or GitHub answered 5xx or 429
  before the guest produced a result; the host ran out of disk. Each
  rerun is listed in the results record. A model failure is never
  rerun.
- A `failed` task is rescored once, without a new attempt, when its
  report `failure` shows the scorer failed before the head was tested:
  a module download, toolchain, or checkout error. The results record
  lists each rescore with both failures. The rescore's verdict stands.
- Token ceiling: 60M tokens across both runs, counting reported tokens.
  Reaching it stops the campaign; the result is at best **narrow**.
- The campaign stops immediately on a GitHub write outside the mirror,
  a credential in any public artifact, or two pull requests for one
  task in one run. Any of these is **fail**.

## Decision

Spread is the absolute difference between the two runs.

- **pass**: both runs complete all 30 tasks, verified success differs
  by at most 3 tasks, and tokens per verified success differ by at most
  25% of the lower run. The baseline is the mean of the two runs. Gate
  rule for later work: an improvement must exceed the observed spread
  on verified success or on tokens per verified success.
- **narrow**: no fail condition, both runs complete at least 20 tasks,
  and pass is not met. The baseline is accepted for the tasks both runs
  completed, with a band equal to the observed spread.
- **fail**: any fail condition, fewer than 20 tasks completed in either
  run, or verified success differing by more than 6 tasks. The results
  record names what would fix the noise.

## Known limits

- Guests can reach this repository's public history, so a task's
  upstream fix may be visible to the agent. The results record reports
  any session that fetched it, where the transcript shows it.
- Checks run the agent's code as the operator's user in fresh checkouts
  with an empty `HOME`; this is not a sandbox (see `eval/set.md`).
- The test patch replaces the agent's own edits to the files it touches.
  A correct fix whose tests the agent put elsewhere still scores on the
  held-back tests.
- The gate catches build and vet breakage only. A head that breaks an
  existing test outside the held-back checks still scores `verified`.
