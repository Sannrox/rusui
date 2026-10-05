# Implement sessions

## First pinned implement task

For the `Sannrox/rusui` example, setup must leave `implement: true` on that
repository and `run` in project `rusui`'s `session_kinds`. Add a narrowly
scoped `RUSUI_AGENT_GITHUB_TOKEN`, one model credential or logged-in model
proxy, and `RUSUI_GUEST_MODEL` when the guest is Claude. Put them in
`rusui.env`, then apply setup and confirm its embedded `diagnose` report
shows `model_upstream: ready` and, for Claude, `model_guest: ready`. A
ready model list alone is not enough. Do not put a credential in the
shell command.

The task below pins the source commit before admission and allows only the
operator guide to change. Replace the prompt with one bounded task for your
own repository, and match `-project`, `-repo`, `-ref`, and `-paths` to its
policy and task. `run` prints the session and task IDs; the runner prints the
session and turn IDs it claims.

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
STATE="$HOME/Library/Application Support/rusui" # Linux: use setup's state directory
export RUSUI_WORKER_SECRET="$(sed -n 's/^RUSUI_WORKER_SECRET=//p' "$STATE/rusui.env")"
export RUSUI_PLANE_CA="$(sed -n 's/^RUSUI_PLANE_CA=//p' "$STATE/rusui.env")"
PLANE_URL="https://127.0.0.1:8080"
BASE_SHA="$(git ls-remote https://github.com/Sannrox/rusui.git refs/heads/main | cut -f1)"
EFFORT_KEY="first-run-$(date -u +%Y%m%dT%H%M%SZ)"
STARTED="$("$BIN/rusui" run -url "$PLANE_URL" \
  -project rusui -effort "$EFFORT_KEY" -repo Sannrox/rusui -ref main \
  -base-sha "$BASE_SHA" -paths docs/operator.md \
  'Add one concise operator troubleshooting entry for a documented setup failure.')"
SESSION_ID="$(printf '%s\n' "$STARTED" | jq -r '.session_id')"
TASK_ID="$(printf '%s\n' "$STARTED" | jq -r '.task_id')"
printf 'session_id=%s task_id=%s\n' "$SESSION_ID" "$TASK_ID"

"$BIN/rusui-runner" -url "$PLANE_URL" -repo Sannrox/rusui \
  -ca "$RUSUI_PLANE_CA" -acp -once
```

The runner logs a `claimed session=… turn=…` line after the turn ends. While
it runs, inspect the session and turn IDs with the operator token:

```bash
curl --cacert "$RUSUI_PLANE_CA" -fsS -H "Authorization: Bearer $RUSUI_WORKER_SECRET" \
  "$PLANE_URL/sessions/$SESSION_ID" | jq '{session: .session.ID, turns: [.turns[] | {id: .ID, state: .State}]}'
```

After the turn is complete, stop the setup-managed service before reading its
SQLite file. This removes the user-service entry but preserves the state;
rerun `setup apply` to install it again.

```bash
"$BIN/rusui" setup remove-service -state "$STATE"
```

Use the turn ID from the runner log to confirm the plane's observed outcome;
the guest's claim alone is not publication evidence:

```bash
TURN_ID=123 # replace with the runner's logged turn ID
sqlite3 -readonly "$STATE/rusui.db" \
  "SELECT json_extract(payload, '$.result.outcome') AS plane_outcome, json_extract(payload, '$.result.pull_request') AS pull_request, json_extract(payload, '$.result.candidate_sha') AS candidate_sha, json_extract(payload, '$.result.published_sha') AS published_sha FROM review_revisions WHERE job_id = $TURN_ID ORDER BY id DESC LIMIT 1;"
```

`published` means the plane saw the pull request at the candidate SHA: the
workspace HEAD, or the tip of another local branch in the session workspace,
such as one the agent checked out in a nested worktree. A pull request head
that is neither stays `unconfirmed`. A missing row, `unconfirmed`, or
`blocked` is not a published PR. Merging stays
with a human maintainer.

## Agent publication

In an implement session the agent pushes a branch and opens or updates its
own pull request as you ([ADR 0015](decisions/0015-agent-publication.md)).
It needs all of:

- `implement: true` on the repository in `policy.yaml`, and `run` in the
  project's `session_kinds`;
- `RUSUI_AGENT_GITHUB_TOKEN` on the plane;
- a pinned task: `rusui run -project SLUG -effort KEY -repo OWNER/NAME
  -ref main -base-sha SHA -paths docs,internal/x "the change"`;
- `gh` in the guest image (container guests) or on the runner `PATH`.

Ordinary `rusui run PROMPT` sessions, scheduled sessions, and review
sessions never receive the token.

A run turn may execute for 45 minutes from claim (`RunExecDeadline`).
Review and scheduled turns keep the 12-minute execution deadline.
Heartbeats renew the grant and the lease liveness window. They do not
move the execution deadline.

rusui cannot stop a token from doing what its scope allows. Before you
enable `implement`, set up:

- a fine-grained token limited to the bound repositories, with only
  **Contents** and **Pull requests** read and write; no administration,
  workflows, secrets, or environments;
- protection on the default branch that requires pull requests and status
  checks and blocks force pushes and deletion. With more than one
  maintainer, also require an approving review from someone other than the
  last pusher.

On a solo repository GitHub cannot tell the agent from you: merging needs
the same permission as pushing, and you cannot require your own approval.
rusui rejects `gh pr merge`, `gh pr close`, `gh release`, default-branch
and force pushes, and similar commands in implement sessions
([ADR 0017](decisions/0017-claude-guest-and-model-upstream.md) D3). Those
rules are workflow control, not a security boundary.

Merging stays yours. Commits carry `Co-authored-by: rusui` and
`Rusui-Session` trailers ([configuration](configuration.md)).

### Plane publication (opt-in)

`RUSUI_PUBLICATION=plane` moves publication behind the plane
([ADR 0044](decisions/0044-plane-publishes-from-turn-result.md)). It needs
GitHub App credentials (`RUSUI_GITHUB_APP_ID`, a private key, and the
installation) with **Contents** and **Pull requests** write on the bound
repositories; the plane refuses to start without them. The guest then
holds no GitHub credential: it pushes `rusui/<session>/<name>` through the
plane's git proxy and writes a `publish` request to its result, and the
plane creates or updates the pull request as the App on Complete, only
while the branch points at the result's `candidate_sha`; a result without
one, or a branch that moved, is blocked without a GitHub write.
`RUSUI_AGENT_GITHUB_TOKEN` is ignored. Pull requests show the App as
author. Keep it off until you have run the ADR 0020 pilot
([ADR 0020](decisions/0020-turn-scoped-github-publication.md) Validation).

A review-only App on a public test repository, including permissions,
tunnel verify/cleanup, a receipt check, and signature failures:
[github-app-pilot.md](github-app-pilot.md).
