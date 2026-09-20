# First loopback session

This is a learning path. At the end you have a rusui process on loopback
that answers `/healthz`. You have not connected GitHub or Slack yet.

How-to for a real repository: [operator.md](operator.md).
Flags and env: [configuration.md](configuration.md).

## What you need

- Go 1.26 on `PATH`. `make` pins [`.go-version`](../.go-version) (currently 1.26.6).
- `make`, `git`, and `curl`.
- A **non-empty** GitHub token in the environment. The server refuses to
  start if `RUSUI_GITHUB_TOKEN` and `GITHUB_TOKEN` are both empty. This
  walkthrough does not call GitHub until you follow [operator.md](operator.md).

## 1. Clone and build

```bash
git clone https://github.com/Sannrox/rusui.git
cd rusui
make all
```

Binaries land in `_output/local/bin/$(go env GOOS)/$(go env GOARCH)/`.

## 2. Copy policy

```bash
cp policy.example.yaml policy.yaml
```

The example binds project `rusui` to `Sannrox/rusui` with `visibility:
public`. Leave it for this walkthrough. Edit bound repos before you point
the process at your own GitHub.

## 3. Set throwaway secrets

These values are for a local process only. Do not reuse them on a tunnel.

```bash
export RUSUI_GITHUB_TOKEN="${RUSUI_GITHUB_TOKEN:-$GITHUB_TOKEN}"
export RUSUI_WEBHOOK_SECRET="$(openssl rand -hex 16)"
export RUSUI_WORKER_SECRET="$(openssl rand -hex 16)"
export RUSUI_SLACK_SECRET="$(openssl rand -hex 16)"
```

If `RUSUI_GITHUB_TOKEN` is still empty, export a read-only token first.
`-allow-insecure` skips webhook, worker, and Slack secrets; it does not
skip the GitHub token. See [SECURITY.md](../SECURITY.md).

## 4. Start the server

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui" -addr 127.0.0.1:8080 -policy policy.yaml -db rusui.db
```

Leave this terminal running. `rusui.db` is created in the current
directory and is gitignored.

## 5. Prove it is up

In another terminal:

```bash
curl -fsS http://127.0.0.1:8080/healthz
```

You should see `ok`. If the process exited immediately, a required secret
or the GitHub token was missing.

Optional: `"$BIN/rusui" diagnose -policy policy.yaml -addr 127.0.0.1:8080 -url http://127.0.0.1:8080`
reports the supported topology. A loopback tutorial without Docker is
allowed to print `runtime: unavailable`; that is not a failure of this
walkthrough. Unattended container work needs the checks in
[operator.md](operator.md).

## 6. Optional: start a runner

The P1 guest is Grok ACP. Only do this if `grok` is on your `PATH` and you
want a turn claim. Use the **same** `RUSUI_WORKER_SECRET` as the server.

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui-runner" \
  -url http://127.0.0.1:8080 \
  -repo Sannrox/rusui \
  -token "$RUSUI_WORKER_SECRET" \
  -acp \
  -once
```

`-once` claims at most one turn and exits. There is no in-tree
`review-driver`. A process driver is any command that prints review JSON
on stdout; pass `-driver` instead of `-acp`.

Without a prepared session this command may wait or exit with no work.
That is enough to prove the runner can authenticate. Wiring a webhook and
a real item is [operator.md](operator.md).

## What you just did

- Built host binaries with `make all`.
- Loaded policy v2 from a file.
- Started a loopback server that fail-closes without secrets.
- Confirmed `/healthz`.

You did **not** open a port, comment on GitHub, or run live apply. Those
are later milestones. Dry-run apply is the current contract:
[ARCHITECTURE.md](../ARCHITECTURE.md).

## Next

| I want to… | Read |
| --- | --- |
| Reach GitHub and Slack | [operator.md](operator.md) |
| Look up flags and HTTP | [configuration.md](configuration.md) |
| Learn the nouns | [../CONTEXT.md](../CONTEXT.md) |
| Change the code | [../CONTRIBUTING.md](../CONTRIBUTING.md) |
