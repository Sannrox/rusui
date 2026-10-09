# Your first rusui session

This tutorial runs a complete, disposable sample session. You will see a
stored receipt without connecting GitHub, Slack, or a model. The sample is
labeled `SAMPLE` throughout and cannot publish a pull request or change a
real repository.

For a real repository, follow the [operator guide](operator.md). For a
review-only App trial, follow the [GitHub App guide](github-app-pilot.md).
Look up flags and environment variables in [configuration.md](configuration.md).

## Sample demo (no GitHub, no model)

### Prerequisites

Use a `rusui` release binary on your `PATH`, or build one from a checkout
with Go 1.26 and `make`:

```bash
make all
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
```

The commands below use the checkout binary. If you installed a release
binary, replace `"$BIN/rusui"` with `rusui` and skip the build.

### Run and observe

```bash
"$BIN/rusui" demo
```

Expect a line beginning `SAMPLE rusui demo`, followed by `Project: sample`,
a session and turn ID, `sample/demo#1`, and a receipt containing a `keep`
verdict. These are fixture records, not a review of your repository. The
demo admits the sample issue, runs a deterministic driver, prints its stored
receipt, and exits.

### Cleanup

The default demo creates a unique temporary state directory and removes it
before exit. No cleanup command is needed. If you intentionally used
`demo -keep`, remove only the owned `rusui-sample-demo-*` directory printed
by the command after inspecting it. Never remove its parent directory.

<a id="first-loopback-session"></a>

## Continue with a loopback server

The next exercise starts a local process and checks `/healthz`. It requires
a read-only GitHub token and generated local secrets, but does not call
GitHub until you follow the operator guide. Stop the process with Ctrl-C;
remove only the `policy.yaml` and `rusui.db*` files you created for this
exercise if you no longer need them.

### What you need

- Go 1.26 on `PATH`. `make` pins [`.go-version`](../.go-version) (currently 1.26.9).
- `make`, `git`, and `curl`.
- A **non-empty** GitHub token in the environment. The server refuses to
  start if `RUSUI_GITHUB_TOKEN` and `GITHUB_TOKEN` are both empty. This
  walkthrough does not call GitHub until you follow [operator.md](operator.md).

### 1. Clone and build

```bash
git clone https://github.com/Sannrox/rusui.git
cd rusui
make all
```

Binaries land in `_output/local/bin/$(go env GOOS)/$(go env GOARCH)/`.

### 2. Copy policy

```bash
cp policy.example.yaml policy.yaml
```

The example binds project `rusui` to `Sannrox/rusui` with `visibility:
public`. Leave it for this walkthrough. Edit bound repos before you point
the process at your own GitHub.

### 3. Set throwaway secrets

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

### 4. Start the server

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui" -addr 127.0.0.1:8080 -policy policy.yaml -db rusui.db
```

Leave this terminal running. `rusui.db` is created in the current
directory and is gitignored.

### 5. Prove it is up

In another terminal:

```bash
curl -fsS http://127.0.0.1:8080/healthz
```

You should see `ok`. If the process exited immediately, a required secret
or the GitHub token was missing.

Optional: `"$BIN/rusui" diagnose -policy policy.yaml -addr 127.0.0.1:8080 -url http://127.0.0.1:8080`
reports the supported topology. Without model access it also reports
`model_upstream: misconfigured`; add a provider key or configured
`RUSUI_MODEL_UPSTREAM` before unattended work. A loopback tutorial without
Docker is allowed to print `runtime: unavailable`; that is not a failure of
this walkthrough. Unattended container work needs the checks in
[operator.md](operator.md).

### 6. Optional: start a runner

The guest is selected by the plane (Grok ACP by default). Only do this if
`grok` is on your `PATH` and you want a turn claim. Use the **same**
`RUSUI_WORKER_SECRET` as the server.

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

### What you just did

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
