# eval results

Archive snapshot. Current fixture list: [set.md](set.md).

Ran 2026-09-10 against dry-run apply (policy.fixture-style close/comments on).

| id | item | reason | operator | system | evidence | agree |
|---|---|---|---|---|---|---|
| E1 | issue/stale-old | stale_insufficient_info | close | close | server_verified | yes |
| E2 | issue/stale-recent | stale_insufficient_info | keep | keep | not eligible | yes |
| E3 | issue/dup | duplicate_or_superseded | close | close | server_verified | yes |
| E4 | issue/not-dup | duplicate_or_superseded | keep | close | server_verified | no |
| E5 | issue/fixed | implemented_on_main | close | close | server_verified | yes |
| E6 | pr/release-only | implemented_on_main | keep | keep | not eligible | yes |
| E7 | issue/vague | incoherent | advisory | advisory | model_assertion | yes |
| E8 | issue/flaky | not_reproducible_on_main | advisory | advisory | model_assertion | yes |
| E9 | issue/nudge | note | comment | comment | model_assertion | yes |
| E10 | issue/ok | keep | keep | keep | — | yes |

E4 is the designed false close: `duplicate_or_superseded` only checks that the canonical item exists.
