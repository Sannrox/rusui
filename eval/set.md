# Maintenance evaluation corpus

A versioned corpus of recorded review results with maintainer judgments.
`rusui eval` replays it through admit, claim, complete, and dry-run apply
against a fake GitHub, then scores the plane's writes against the
judgments and the promotion gates of
[ADR 0038](../docs/decisions/0038-maintenance-eligibility-and-promotion.md).
No model runs and nothing is written to GitHub.

```bash
rusui eval                                   # development split, Markdown
rusui eval -heldout ~/rusui-heldout.json     # add the operator-owned split
rusui eval -heldout FILE -gate comment       # exit 4 fail, 5 incomplete
rusui eval -heldout FILE -out eval-reports   # keep a JSON report; never overwrites
rusui eval -heldout FILE -check PRIOR.json   # exit 3 if PRIOR came from other inputs
go test ./eval -update                       # refresh results.md after reviewing the diff
```

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

Add development cases for new situations and regenerate `results.md`.
Move a case to held-out only by copying it into the operator's file;
held-out cases are never committed.
