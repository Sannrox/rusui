# Review results reference

This page describes recorded Turn measurements and review-result
dispositions. For operator procedures, see the [operator guide](operator.md).

## Turn measurements

`GET /projects/{slug}/measurements` with the operator token returns Turn
counts, terminal states, the median duration, permission denies, and how
many Turns omitted token counts. The row names the session id. It does
not include the prompt, tool arguments, diffs, terminal bytes, file
contents, or credentials. Export stays off unless `RUSUI_OTEL_ENDPOINT`
points at a collector you run.

## Result dispositions

Record your judgment of an advisory review result so the comment gate
([ADR 0038](decisions/0038-maintenance-eligibility-and-promotion.md) D6)
can be measured. The result id is `review_result.revision_id` from
`GET /sessions/{id}`.

```sh
rusui disposition -value useful 42
rusui disposition -value harmful -wrong -note "flagged a correct lock" 43
rusui disposition            # comment gate report
```

A disposition is a record only. It changes no policy, job, or action and
writes nothing to GitHub. Re-recording keeps history; the latest counts.
The report reads the 20 most recent disposed results that produced a
dry-run comment. Below 20 it says `incomplete`; otherwise `fail` or
`shadow_pass`, which covers the shadow thresholds only. Held-out cases, replay
duplicates, and private-context leaks are listed as `not_measured`;
`rusui eval` covers the first two. Promotion stays a `policy.yaml` edit.
