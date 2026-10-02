# On-demand pull request review

Ask the plane to review one bound pull request and wait for its immutable
result:

```bash
rusui review OWNER/REPO#42
```

The repository must have `review: true`, the project must not be paused, and
the daily review budget must have room. The command prints the review session,
then the stored artifact and its dry-run apply actions. Reviews never comment
or close on GitHub. Use `-url` and `-token` to select the plane and its
operator/worker credential; `-timeout` defaults to 30 minutes, and `0` waits
without a deadline.
