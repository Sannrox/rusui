# Operator guide

Run a self-hosted rusui installation on your own machine. Apply is dry-run:
nothing is commented, closed, or merged on GitHub.

First loopback session: [tutorial.md](tutorial.md).
Flags, env, HTTP, and policy: [configuration.md](configuration.md).
Upgrade, drain, diagnostics: [upgrade.md](upgrade.md).
Nouns: [CONTEXT.md](../CONTEXT.md).

## Supported topology

The unattended proof is one tuple (D1/D4). Other OS/runtime/image
combinations are not claimed:

| Piece | Supported value |
| --- | --- |
| Host OS | Linux or macOS |
| Runner | one `rusui-runner` on the same host as the plane |
| Runtime | Docker or Podman CLI (`docker`/`podman` on `PATH`) |
| Guest | `$RUSUI_GUEST_IMAGE` (Grok, or Claude Code via `claude-agent-acp`, over container stdio) |
| Egress | `trusted`: HTTPS to `rusui.plane` only |
| Listen | loopback `127.0.0.1` |

Check it before dispatch:

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui" diagnose -policy policy.yaml -addr 127.0.0.1:8080
```

`diagnose` (and `GET /readyz`) report each check as `ready`,
`misconfigured`, or `unavailable`. They do not print secret values.
Exit status 0 means the topology is ready; do not start unattended work
otherwise. `GET /healthz` only proves the process is listening.

Process-driver review (`-driver`) is test/dev. It is not this topology.

## Prerequisites

- Go 1.26 on `PATH`. `make` uses `GOTOOLCHAIN=go$(cat .go-version)`
  (currently 1.26.6).
- A **read-only** GitHub token for intake of repositories listed in policy.
  `implement` sessions additionally need your GitHub credential in
  `RUSUI_AGENT_GITHUB_TOKEN`: a fine-grained token with contents and
  pull-request write on the bound repositories only (ADR 0015). The guest
  image must provide `gh`.
- A tunnel or webhook relay if GitHub or Slack must reach the process.
- For review turns: `-acp` (the plane's guest: Grok by default; for
  `RUSUI_GUEST=claude` the image or `PATH` needs
  `@agentclientprotocol/claude-agent-acp@0.81.1`, which requires Node.js) or a process driver
  command (`-driver`) that prints review JSON on stdout. There is no
  in-tree `review-driver` binary.

## Set up with `rusui setup`

`rusui setup` prepares this machine ([ADR 0018](decisions/0018-rusui-setup.md)).
Build rusui first (step 1), then:

```bash
rusui setup plan    # what would change; writes nothing
rusui setup apply   # idempotent; ends with rusui diagnose
```

`apply` creates the state directory (`0700`; `-state DIR` overrides the
platform default), plane TLS (a local CA and a certificate for
`127.0.0.1`, `localhost`, and `rusui.plane`), `rusui.env` (`0600`) with
generated worker, webhook, Slack, and operator secrets, a policy skeleton
when none exists, and, when Docker or Podman is installed, the
`rusui-trusted` network and the reference guest image. The image
(`build/guest-image`: git, `gh`, Node.js 22, `claude-agent-acp` 0.81.1) is
tagged by its image ID, so the tag names the bits the guest runs (the
Dockerfile hash is only the build cache key); `-rebuild-image` rebuilds it
with `--pull --no-cache`. Setup records the tag as `RUSUI_GUEST_IMAGE`
and sets `RUSUI_GUEST=claude` unless you chose another image or guest.
`make guest-image` builds the same tag from a checkout. It never overwrites a policy or existing TLS
(`-rotate-tls` replaces TLS only) and never prints secret values.

Lines marked `needs-you` are inputs only you can supply: the GitHub token
and model access (a key, or `RUSUI_MODEL_UPSTREAM` for a CLI proxy you
logged in to), plus a guest image when no container runtime is installed. Put them in `rusui.env`, then start the plane
with the command `apply` prints. The sections below describe the same
steps by hand.

## 1. Build and policy

```bash
git clone https://github.com/Sannrox/rusui.git
cd rusui
cp policy.example.yaml policy.yaml
# edit projects / bound repos
make all
```

Policy is version 2, keyed by **project**. `comments: true` / `close: true`
authorize **simulated** intended-action rows, not GitHub writes.

## 2. Secrets

Required or the server exits (unless `-allow-insecure`):

```bash
export RUSUI_GITHUB_TOKEN=ghp_...
export RUSUI_WEBHOOK_SECRET="$(openssl rand -hex 16)"
export RUSUI_WORKER_SECRET="$(openssl rand -hex 16)"
export RUSUI_SLACK_SECRET="$(openssl rand -hex 16)"   # placeholder if Slack is unused
```

Set the webhook secret to the same value GitHub stores on the webhook. Set
the worker secret on **both** the server and `rusui-runner`. A random
`RUSUI_SLACK_SECRET` is only enough to *start* the process; inbound Slack
HMAC will fail until you replace it with the Slack app signing secret and
restart. See [SECURITY.md](../SECURITY.md).

## 3. Run (loopback)

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui" -addr 127.0.0.1:8080 -policy policy.yaml -db rusui.db
```

In another terminal:

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui-runner" \
  -url http://127.0.0.1:8080 \
  -repo OWNER/REPO \
  -token "$RUSUI_WORKER_SECRET" \
  -acp
```

`GET http://127.0.0.1:8080/healthz` must return `ok`. Then:

```bash
"$BIN/rusui" diagnose -policy policy.yaml -addr 127.0.0.1:8080
curl -fsS http://127.0.0.1:8080/readyz
"$BIN/rusui" run -project rusui -token "$RUSUI_WORKER_SECRET" "bounded session"
```

`run` creates a `run` session through the public API. Container turns still
need the guest image, plane TLS, and a runner.

## Local interactive profile

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

The request cannot supply argv or cwd. Rusui stores a Session without a Turn,
then records Sumika's Process observations separately. A client disconnect
does not kill the Process. Use Rusui cancellation to request a kill; the
Session is marked cancelled only after Sumika reports the Process dead. Rusui
first sends a graceful SIGTERM, then escalates to SIGKILL after 30 seconds if
the Process remains alive. If
the daemon is unavailable, state stays unknown or cancellation remains
unconfirmed until reconciliation. Never infer death or restart automatically.
After death or a successful List confirms a Process is absent, an operator can
request its next generation explicitly, as long as cancellation is not pending:

```bash
curl -fsS -X POST "http://127.0.0.1:8080/sessions/$SESSION_ID/restart" \
  -H "Authorization: Bearer $RUSUI_WORKER_SECRET"
```

This profile runs with Sumika's OS identity and inherited environment. It may
use same-user files and CLI authentication; it has no managed-container
isolation, GitHub credential, turn grant, or model proxy credential. Local PTY
bytes remain with Sumika. The unified Rusui client path is being completed in
issue #185.

Do not pass `-addr 0.0.0.0:8080` unless secrets are set. Operator HTTP
(`-addr`) has no TLS unless `RUSUI_TLS_CERT` and `RUSUI_TLS_KEY` are set.

## Container guests (trusted egress)

The first unattended topology is **one runner**, the **container** driver
(Docker or Podman CLI), and Grok ACP over container stdio with plane
proxies. Tests in this repository prove that path with `FakeRuntime` and a
stubbed Docker CLI. They do **not** claim live Docker/Podman, IPv6, or
cross-host combinations.

Container guests may reach only `rusui.plane` (git and model HTTPS proxies).
Direct addresses, IPv6, host loopback, and other proxy names are denied.
The guest image must trust the plane CA at
`/usr/local/share/ca-certificates/rusui-plane.crt`. Provision that file as
part of the guest image identity (`source_hash`) or mount it with
`RUSUI_PLANE_CA`. An untrusted endpoint never receives the grant: TLS
handshake fails before the bearer is sent.

Required for the container driver:

```bash
export RUSUI_TLS_CERT=/etc/rusui/plane.crt
export RUSUI_TLS_KEY=/etc/rusui/plane.key
export RUSUI_PLANE_CA=/etc/rusui/plane-ca.crt   # mounted into the guest
export RUSUI_GUEST_IMAGE=rusui-guest:local
```

The plane certificate must include Subject Alternative Names `rusui.plane`
(guest) and the listen address (host prepare, typically `127.0.0.1`). Host
git fetch and `rusui drain` use `https://127.0.0.1:<port>` with
`RUSUI_PLANE_CA`. Guests use `https://rusui.plane:<port>`.

If Docker or Podman is installed but the TLS pair is unset, the container
driver stays disabled and the process logs that fact. Process-driver review
on loopback HTTP remains available.

Snapshot prepare uses a **read-only prepare grant** through `/git-proxy/`,
not a GitHub token on the runner disk. Turn grants may push only
`refs/heads/rusui/<session>/*` on the bound repo. Grant loss (expiry, lease
loss, other session) is a failed turn, not a silent continue.

Permissive `docker run` without `--network rusui-trusted` is not the
supported topology.

At startup the server expires hung refresh owners, reconciles missed hook
deliveries, catches up open and locally tracked items, and retries unpublished
apply attempts. The same paths repeat: refresh every 1s, reconcile every 5m,
catch-up every 15m, apply retry every 1m. Reconcile is skipped with a log when
`RUSUI_GITHUB_HOOK_IDS` is unset.

SQLite defaults to `rusui.db` in the current working directory (`*.db` is
gitignored). Stop the process, copy `rusui.db` (and `-wal`/`-shm` if
present). Restore with the shipped `store.Restore` path: it inventories
sessions/turns/environments, clears live leases, drops turn grants, and
keeps approval rows as records only (they do not authorize a new RPC).
A missing or corrupt file is an error, not a successful empty plane.
Container workspace dirt is not in the database; it rematerializes from
the snapshot after idle expiry. Credentials in env files are not in the
backup.

## Implement sessions (agent publication)

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

## 4. GitHub webhook

Create a **repository** webhook:

- Payload URL: `https://<your-tunnel>/hooks/github`
- Content type: `application/json`
- Secret: `$RUSUI_WEBHOOK_SECRET`
- Events: `issues`, `pull_request`, `issue_comment`

The handler ACKs 2xx after committing `delivery_id` and a refresh request. It
does not fetch GitHub on that path.

Generic signed intake: `POST /hooks/events` with `X-Rusui-Signature-256`
(same secret), store, then 202.

`RUSUI_GITHUB_HOOK_IDS=owner/repo=123` is required for delivery reconcile.

## 5. Tunnel

GitHub and Slack cannot reach `127.0.0.1`. Point a tunnel at the process.

cloudflared (example):

```bash
cloudflared tunnel --url http://127.0.0.1:8080
# webhook:  https://<id>.trycloudflare.com/hooks/github
# Slack:    https://<id>.trycloudflare.com/hooks/slack
```

smee (example):

```bash
smee -u https://smee.io/<id> -t http://127.0.0.1:8080/hooks/github
```

## 6. Slack (optional for GitHub intake)

The Slack **signing secret** is still required at process start. GitHub
webhooks do not call Slack. If you want slash commands:

1. Slash command `/rusui` posting to `https://<tunnel>/hooks/slack`.
2. Set `RUSUI_SLACK_SECRET` to the Slack app **signing secret** (not the
   random placeholder from step 2) and restart `rusui`.
3. `export RUSUI_SLACK_USERS=U0123ABCD,U0456EFGH` — Slack **user ids**.
   An empty allowlist 403s everyone.
4. Optional exceptions: `RUSUI_SLACK_BOT_TOKEN` and `RUSUI_SLACK_CHANNEL`.

Commands: `status`, `pause [project]`, `resume [project]`, `sweep [repo]`,
`retry repo` or `retry repo#item`, `reload`. `implement` is rejected.
`pause rusui` is the project slug, not `Sannrox/rusui`.

`retry` requeues `state=failed` on unchanged content. `sweep` only enqueues
refreshes.

## 7. Images

Runtime images still bind loopback **inside the container**. Publish a host
port only after `-addr 0.0.0.0:8080` and with secrets set.
See [development.md](development.md).

## Restart, uninstall, data retention

Restart is the same command line. On start the process runs `Recover`
(expire refresh owners, strip nothing from a live DB except hung owners).
SQLite is the session truth; container dirt is not in the file.

Stop the process (SIGINT). Data that remains until you delete it:

- `rusui.db` plus `-wal`/`-shm` if present
- snapshot cache (`snapshots/` next to the database by default)

Secrets live in the operator environment, not in those files. Do not
embed token values in examples. Restore a copied database with
`store.Restore` (inventory, clear live leases, drop grants). Guest
containers named `rusui-*` are not in the backup; destroy them with the
container CLI if any are left.

Uninstall: stop the process, delete the database and snapshot directory,
unset the env vars listed in [configuration.md](configuration.md).

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Server exits immediately | Missing GitHub token or webhook/worker/Slack secret; or use `-allow-insecure` locally |
| `GET /healthz` fails | Process not listening; wrong `-addr` |
| `GET /readyz` 503 / `diagnose` exit 1 | Read the JSON checks: missing runtime is `unavailable`; bad policy/TLS/CA is `misconfigured` |
| GitHub webhook 401 | Secret mismatch, or tunnel not hitting `/hooks/github` |
| Runner `auth` / 401 | Secret mismatch between processes |
| Slack 403 `user` | `RUSUI_SLACK_USERS` missing that user id, or empty |
| Slack 401 | Signing secret / timestamp skew > 5 minutes |
| Claim never returns work | Repo not bound in policy, paused, or daily budget exhausted |
| Reconcile skipped | `RUSUI_GITHUB_HOOK_IDS` unset |
| No GitHub comment appeared | Apply is dry-run; look at intended-actions in SQLite |
| Policy change ignored | Slack `reload` or restart |
