# Local interactive profile

The experimental local profile runs a policy-configured command through
Sumika on the Rusui host. Build Sumika from the source baseline recorded in
[issue #184](https://github.com/Sannrox/rusui/issues/184), start its daemon as
the same OS user as Rusui (`sumika daemon`), and explicitly add a `local` kind plus
`local_runtime` argv and absolute cwd to the Project in `policy.yaml`. The
initial supported hosts are macOS and Linux. The compatibility baseline is
Sumika commit [`3dadc7a`](https://github.com/Sannrox/sumika/commit/3dadc7a97fba2aaaa53aea767f269dfa6c1cfff0),
workspace version `0.1.0`, built with Rust `1.97.1`. Sumika's socket defaults to its
per-user location; set `SUMIKA_SOCK` on the Rusui process only when using an
alternate local socket.

Each Process generation stores an internal fingerprint of the argv and cwd
used at start. Policy edits affect future starts; existing Processes continue
to reconcile and can still be cancelled if the local profile is later disabled.
The fingerprint is not returned by the session API.

Create a local Session through the authenticated API:

```bash
curl -fsS -X POST http://127.0.0.1:8080/projects/local/sessions \
  -H "Authorization: Bearer $RUSUI_WORKER_SECRET" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: local-example-1' \
  -d '{"kind":"local"}'
```

HTTP `201` confirms the durable Session was created; it does not guarantee that
Sumika confirmed the Process start. A start error is returned as `start_error`
with the Process state `unknown`. Reconcile that generation before requesting
an explicit restart.

The request cannot supply argv or cwd. Rusui stores a Session without a Turn,
then records Sumika's Process observations separately. A client disconnect
does not kill the Process. Use Rusui cancellation to request a kill; the
Session is marked cancelled only after Sumika reports the Process dead. Rusui
first sends a graceful SIGTERM, then a reconciliation after the 30-second grace
period sends SIGKILL if the Process remains alive. Periodic reconciliation can
delay escalation until its next run. If the daemon is unavailable, state stays
unknown or cancellation remains unconfirmed until reconciliation. Never infer
death or restart automatically.
After death or a successful List confirms a Process is absent, an operator can
request its next generation explicitly. A lost generation with a pending cancel
remains unconfirmed; an explicit restart creates a new generation without
marking the old cancellation complete:

```bash
curl -fsS -X POST "http://127.0.0.1:8080/sessions/$SESSION_ID/restart" \
  -H "Authorization: Bearer $RUSUI_WORKER_SECRET"
```

Every local lifecycle transition is recorded as an append-only receipt in
the same transaction as the state change: Process start, each observed
state change, cancel request and cancellation, and Attach, steal, detach,
and exit. `GET /sessions/{id}?include=receipts` returns them as `process_receipts`
(paged by `process_receipt_after_id`), and the
console session page lists them under Local process activity.

Read a session without opening its terminal. The command prints the same
durable transcript the console shows, including recorded tool-call events,
and the current workspace diff. An empty diff and an unavailable workspace
are stated. A local session with no stored transcript is an error. Closing
the command leaves the session running.

```bash
rusui read [-url http://127.0.0.1:8080] [-token "$RUSUI_OPERATOR_TOKEN"] SESSION_ID
```

Follow a working session with `-follow`. The command prints the recorded
transcript, then each new transcript event and every change of the session
state (`queued`, `running`, `waiting` for an approval, `completed`, `failed`,
`cancelled`) as it is recorded. It stops when the session completes (exit 0),
fails, or is cancelled (exit 1); interrupt it to stop earlier. A waiting
state means an approval request of the running turn has no decision yet. A
local session has no turns: it stays `open` and is followed until it is
cancelled or you interrupt the command. It does not
print the workspace diff or terminal output. When the connection drops, or
nothing arrives from the plane for 45 seconds (the plane sends a heartbeat
every 15 seconds), it reconnects after the last event it printed, so events
recorded meanwhile are printed once. A rejected token, a missing session, or
a plane without this stream (`GET /sessions/{id}/read/follow?after=SEQ`)
ends it with an error instead of a retry.

To notify without reading transcript text, match the turn record. It is
one line after the state line, `turn: STATE session=ID turn=ID revision=N`,
with `approval=SEQ` added for a waiting turn; the stream carries it as an
`event: turn` with the JSON fields `session_id`, `turn_id`, `state`,
`revision`, and `approval_seq`. `waiting` is printed once for each approval
request when it becomes the oldest one without a decision; `ready` and
`blocked` are printed once when a turn completes, `blocked` when the
plane recorded its result or verdict as blocked. A failed or cancelled
session has no turn record. The record describes the session now: the
plane resends it after a reconnect and the command prints it once, but an
approval answered while the command was disconnected is not printed.

```bash
rusui read -follow [-url http://127.0.0.1:8080] [-token "$RUSUI_OPERATOR_TOKEN"] SESSION_ID
```

Read what `.agents/setup`, `.agents/resume`, and the `.rusui/services.yaml`
commands printed in a container session. `rusui logs` stays the list of
action receipts; `rusui envlog` is the hook and service output, so the two
never mix ([ADR 0031](decisions/0031-cli-command-tree.md)). Each capture is combined stdout and stderr of the last run of
that hook or service; a new run replaces it. A capture over 1 MiB keeps
its last 1 MiB and is marked truncated. A hook that is absent has no
capture, and a session with no output says so. The console session page
lists the same captures under Environment output without inlining bodies;
`rusui envlog` reads one kind or name when you pass `-kind` and `-name`.
Treat the output like the transcript: it may contain what setup printed,
and it is served only with the operator token.

```bash
rusui envlog [-url http://127.0.0.1:8080] [-token "$RUSUI_OPERATOR_TOKEN"] [-kind KIND] [-name NAME] [-omit-body] SESSION_ID
```

Fetch what a session published into a local checkout while its
environment keeps running. `rusui sync` asks the plane for the newest pull
request a turn of the session published (the plane observed it on GitHub
at the candidate SHA), then runs your own `git fetch` of that pull
request's head into `refs/rusui/sessions/SESSION_ID`. It uses your git
remote and your git credentials; Rusui passes no token to git, and the
implement-session credential never reaches the laptop. It does not take
the terminal write lease, start a turn, wake or touch the environment,
or change your branch, index, or working tree. It prints the pull
request, the ref update, and the commits that landed locally, which in a
stale checkout include the default-branch commits the session built on.

```bash
rusui sync [-url http://127.0.0.1:8080] [-token "$RUSUI_OPERATOR_TOKEN"] [-remote origin] [-dir .] SESSION_ID
git worktree add --detach ../session-SESSION_ID refs/rusui/sessions/SESSION_ID
```

It exits non-zero and changes nothing when the session is unknown
(`not found`), was cancelled, or never published a pull request (an
ordinary run or review session, or an implement session whose pull
request GitHub did not show at the candidate SHA). It also refuses when
`-dir` is not a git checkout or the `-remote` URL does not end in the
session's `owner/repo`. When the pull request head moved after the plane
observed it, sync fetches the current head and prints a `note:` line
with both SHAs. A second sync with nothing new says `already up to date`.

Copy a local file into a live session workspace. The path is relative to the
workspace. The cap is 32 MiB. The file is stored as bytes; the console does
not render it as HTML.

```bash
rusui put [-url http://127.0.0.1:8080] [-token "$RUSUI_OPERATOR_TOKEN"] -file ./shot.png SESSION_ID shot.png
```

A managed environment sleeps after `-env-idle-sleep`. Opening the console
terminal (observe or write), minting a preview, following a live preview
grant, or sending `rusui prompt` or a console prompt wakes it first. The
wake runs `.agents/resume` and the declared services once, then the
action proceeds on the same environment id and handle. The wake receipt
names its cause: `operator terminal`, `operator terminal write`,
`operator preview`, `preview grant`, or `operator prompt`. A failed wake
is an explicit error; no terminal lease or preview grant is created, and
the environment stays asleep. An expired or replaced environment is
never woken as a different handle.

An environment id names exactly one guest. When an environment expires,
or the pinned source changes, the session's next turn provisions a new
environment with a new id (`<name>-r<old id>` after expiry) instead of
refilling the old one. The old id stays `expired`, and the session's
environment receipts record `expire` and `replace` (naming the new id),
so every guest a session used stays attributable.

A preview grant survives sleep. Its identity is the environment id and
handle ([ADR 0012](decisions/0012-operator-access.md)), and sleep keeps
both, so the same grant URL works after wake without a new mint. In
practice a live grant keeps the environment awake until the grant ends.

Preview HTML and `POST /comment` live on that origin, not on `-addr`. Set
`RUSUI_PREVIEW_BASE` to a loopback URL with an explicit port that is not
the plane's, for example `http://127.0.0.1:8090`. The process then serves
the preview handler on that host:port beside the plane listener. Unset, mint
refuses. A PreviewBase that shares the plane origin, omits a port, or is
not loopback fails closed at start. Routes, grant query, and body limits:
[configuration.md](configuration.md#preview-origin).

Attach to either runtime with the same operator command:

```bash
export RUSUI_OPERATOR_TOKEN=...   # generated in rusui.env by rusui setup
rusui attach [-url http://127.0.0.1:8080] [-db rusui.db] SESSION_ID
```

The CLI reports the Session, Environment, runtime, current Process, and Turn
before connecting. For a local Session, run it on the Sumika host as the same
OS user and point `-db` at the server's SQLite database if it is not
`rusui.db`. Rusui checks that the local Session and Process match the
authenticated session detail before opening the PTY, and refuses a mismatch.
It attaches directly to Sumika; `Ctrl+]` detaches and terminal resizes follow
the local terminal. For a managed Session, input remains
line-oriented and the CLI acquires an audited write lease when available. If a
browser or another CLI already holds that lease, this CLI connects read-only.
It releases only the lease generation it acquired. Detach leaves the
environment shell running; the guest can read that same session's output.
Typing renews the lease; after an idle period longer than the lease TTL,
input fails with `no write lease` and the command must be run again.
Managed attachment replays the existing Session, Turn, and action transcript
events to stderr while terminal output stays on stdout. It reconnects by the
existing session id and environment; it does not create either object.

This profile runs with Sumika's OS identity and inherited environment. It may
use same-user files and CLI authentication; it has no managed-container
isolation, GitHub credential, turn grant, or model proxy credential. Local PTY
bytes remain with Sumika.

Do not pass `-addr 0.0.0.0:8080` unless secrets are set. Operator HTTP
(`-addr`) has no TLS unless `RUSUI_TLS_CERT` and `RUSUI_TLS_KEY` are set.
