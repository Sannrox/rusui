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

Export a pasteable receipt after the review completes:

```bash
rusui receipt SESSION_ID
rusui receipt -format json SESSION_ID
rusui receipt -out records SESSION_ID
```

The Markdown states the verdict, evidence class, policy revision, source git
pin, guest identity, reported usage when present, and whether any GitHub
action occurred. Dry-run intended-actions stay labeled dry-run. The files are
regenerated from SQLite; they are not recovery inputs and do not authorize a
write. They are unsigned.
