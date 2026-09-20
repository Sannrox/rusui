# v1 evaluation set

Operator judgments for dry-run comparison. These fixtures are public
review-quality cases, not live GitHub items. Slack is in-tree; live apply is
still off.

```bash
go test ./eval -v
```

Latest run: [results.md](results.md).

| id | item | reason | operator | notes |
|---|---|---|---|---|
| E1 | issue/stale-old | stale_insufficient_info | close | 90 days, no non-bot comments |
| E2 | issue/stale-recent | stale_insufficient_info | keep | comment 10 days ago |
| E3 | issue/dup | duplicate_or_superseded | close | same report as #1 |
| E4 | issue/not-dup | duplicate_or_superseded | keep | canonical exists but different bug |
| E5 | issue/fixed | implemented_on_main | close | merge commit on default branch |
| E6 | pr/release-only | implemented_on_main | keep | merged only into release-* |
| E7 | issue/vague | incoherent | advisory | no server-verified close |
| E8 | issue/flaky | not_reproducible_on_main | advisory | never live-close in v2 |
| E9 | issue/nudge | propose_comment / note | comment | useful comment |
| E10 | issue/ok | keep | keep | abstain |
