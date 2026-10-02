# P8 ten-task live pilot results

> **Historical evidence.** This record applies to its stated run and revision;
> it is not a current operator procedure.

Run 2026-09-26 on this operator host against `Sannrox/rusui`. Cohort:
[p8-pilot-cohort.md](p8-pilot-cohort.md), committed as `90bf40a` before
the first implement attempt. Humans still decide whether to merge task
PRs; this file does not merge them.

## Profile as run

| Piece | Value |
| --- | --- |
| Repository | `Sannrox/rusui` |
| Guest | `claude` (`claude-agent-acp` 0.81.1 in `rusui-guest:e516a1dbafa39c74`) |
| Model | loopback CLI proxy; Claude OAuth was in 66h quota cooldown, so the runner passed `ANTHROPIC_MODEL=grok-4.6` for this campaign only |
| Isolation | Docker container, diagnose `ready=true` |
| Policy | `implement: true`, `review: false` after catch-up flooded 40 review jobs |
| Publication | ADR 0015; PRs authored as `Sannrox` with `Rusui-Session` trailers |
| Merge | not performed for task PRs |

Time savings are not claimed.

## Attempts

| ID | Session/turn | Latency | Outcome | Evidence |
| --- | --- | --- | --- | --- |
| P8-01 | 33/33 | ~4.5 min | accepted | [PR 284](https://github.com/Sannrox/rusui/pull/284) `3c320e4`, one line in `docs/operator.md` |
| P8-02 | 42/42 | 12 min | failed | execution deadline; no PR; turn later marked failed so later tasks could claim |
| P8-03 | 43/43 | ~4 min | accepted | [PR 285](https://github.com/Sannrox/rusui/pull/285) `1a4fc0c`, `internal/env/live_container_test.go` |
| P8-04 | 44/44 | ~5 min | accepted | [PR 286](https://github.com/Sannrox/rusui/pull/286) `a56edbd`, `internal/ops/bundle.go` |
| P8-05 | 46/46 | ~2.5 min | blocked | completed; no PR; `policy.yaml` on `main` still `land: false` |
| P8-06 | 33/33 follow-up | ~5.5 min | failed | `rusui prompt` delivered `follow_up` pending_revision 2; PR 284 still one commit |
| P8-07 | 45/45 | ~3.5 min | blocked (publication) | same effort key created revision 2 because the prompt text differed (`spec_hash`); no second PR |
| P8-08 | 42 retry | n/a | blocked | after the P8-02 deadline the queued retry was not republished |
| P8-09 | 47/47 | ~20 s | blocked | stored result `blocked_reason` refused to write `GH_TOKEN` into git |
| P8-10 | admit | <1 s | blocked | `rusui run` returned `409 Conflict stale source` for an ancestor `-base-sha` |

First P8-01 runner claim hit a review-queue flood (GitHub catch-up with `review: true`) and Claude 429. Review was disabled and queued review turns failed closed before the successful 33/33 claim.

## Decision

**Narrow.** Eight of ten rows match their predeclared class if P8-07 is
counted on "no second PR" and P8-08 on blocked recovery. Two hoped-for
accepted publications missed: typed API extraction hit the 12-minute
deadline, and follow-up did not update the existing PR. No policy
bypass and no credential in a public artifact.

Admission should stay limited to small pinned-path docs and single-file
test/ops edits on this topology until follow-up republication and the
12-minute implement deadline are addressed. Effort-key uniqueness is
`(effort_key, spec_hash)` / revision, not effort key alone.
