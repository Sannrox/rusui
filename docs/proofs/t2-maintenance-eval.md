# T2 maintenance evaluation replay

Project evidence for [#105](https://github.com/Sannrox/rusui/issues/105).
It describes this repository's development corpus and the plane's dry-run
behavior on it. It is not a public benchmark and makes no claim about
other repositories, models, or future results.

## Method

`rusui eval` loads the development split
([eval/corpus/development.json](../../eval/corpus/development.json)),
replays each recorded review result through admit, claim, complete, and
dry-run apply in a throwaway store against a fake GitHub, and compares
the plane's writes with the maintainer's judgment. The policy, scenario
clock, and judgments are part of the corpus and its digest. Gates follow
[ADR 0038](../decisions/0038-maintenance-eligibility-and-promotion.md) D6.

Reproduce from a clean checkout of the revision named in the delivering
pull request (`-revision` is trusted as given):

```bash
make all WHAT=cmd/rusui
_output/local/go/bin/rusui eval -revision "$(git rev-parse HEAD)"
```

Replay version 1. Development corpus digest
`a9d704006dba3b80cc50f924c61e4b53fcedbb261d499c411d6dcc1b8396ff9f` at
the time of writing; [eval/results.md](../../eval/results.md) holds the
current per-case table.

## Development results

| measure | value |
|---|---|
| cases | 16 |
| agree with judgment | 13 / 16 |
| false actions (writes the judgment does not allow) | 3 / 16 |
| wrong actions (writes on protected or stale work) | 1 / 16 |
| missed writes | 0 / 16 |
| duplicate work | 0 |
| findings useful / neutral / harmful | 6 / 3 / 5 of 14 |
| wrong findings | 5 / 14 |
| correction minutes | 13 |
| tokens in / out | 14520 / 2910 (2 cases unknown) |

## Failures

- **E4, false close.** `duplicate_or_superseded` checks that the canonical
  item exists, not that the reports match. Known since v1.
- **E12, close on held work (a wrong action).** A `rusui:hold` item past the stale
  thresholds is closed in dry-run. Protected labels are not enforced yet
  (ADR 0038 D3; [#106](https://github.com/Sannrox/rusui/issues/106)).
- **E14, wrong comment.** The plane does not judge comment content; a
  comment the maintainer marks wrong is still written in dry-run. It is a
  false recommendation, which the comment gate bounds, not a wrong action.

Held: an unchanged redelivery (E13) adds no job or comment; a result that
finishes after its item changed (E15) writes nothing; repository text
asking for a close (E11) does not pass the stale check.

## Gates

| class | status | why |
|---|---|---|
| advisory | incomplete | no held-out split in the repository; the rollback rule reads the last 20 held-out dispositions |
| comment | incomplete | no held-out split in the repository; live shadow results and leak checks are not measured by replay |
| repair | incomplete | plane-owned publication (ADR 0038 D4) does not exist |
| close, merge | unauthorized | not promotable under ADR 0038 |

## Limitations

- The development split is 16 synthetic cases written by the maintainer's
  agent. It checks plane behavior and scoring, not model quality.
- Recorded results stand in for the guest. A model change needs new
  recorded results and new judgments, which change the digest.
- Held-out results exist only where an operator supplies a held-out file;
  they are reported in aggregate and are not part of this document.
- Cost covers recorded tokens only; operator and compute time are not
  measured.
