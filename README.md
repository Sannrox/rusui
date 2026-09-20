# Rusui (留守居)

Self-hosted environment plane for coding agents. You write `policy.yaml`.
The server admits work onto sessions and turns, records immutable reviews,
and **dry-runs** apply. It does not comment, close, or merge on GitHub.

留守居 is the steward who keeps house while you are away.

**Status:** self-hosted source, loopback by default, dry-run apply.
Not a hosted product.

[Docs](docs/README.md) · [Architecture](ARCHITECTURE.md) ·
[Contributing](CONTRIBUTING.md) · [Security](SECURITY.md) ·
[License](LICENSE)

## What it is

GitHub is intake. The rusui server is the source of truth. Slack is the
optional human socket. A runner claims turns and hosts a guest CLI (process
driver or ACP). Model processes never receive GitHub write tokens.

Nouns (project, session, turn, environment, runner): [CONTEXT.md](CONTEXT.md).
The v1 contract: [ARCHITECTURE.md](ARCHITECTURE.md).

## What it is not

Live GitHub mutation, implement-to-PR, land, a dashboard, or a multi-tenant
SaaS. Later milestones and competitive sequencing live in
[VISION.md](VISION.md) and [ROADMAP.md](ROADMAP.md); they are not the
runbook.

## Quick start

Prerequisites: Go 1.26 (`make` pins [`.go-version`](.go-version), currently
1.26.6) and a **read-only** GitHub token.

```bash
git clone https://github.com/Sannrox/rusui.git
cd rusui
cp policy.example.yaml policy.yaml    # set your project and owner/repo
make all

export RUSUI_GITHUB_TOKEN=...                    # required
export RUSUI_WEBHOOK_SECRET="$(openssl rand -hex 16)"
export RUSUI_WORKER_SECRET="$(openssl rand -hex 16)"
export RUSUI_SLACK_SECRET="$(openssl rand -hex 16)"   # replace with the Slack signing secret if you use Slack

BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui" -addr 127.0.0.1:8080 -policy policy.yaml -db rusui.db
```

In another terminal, export the same `RUSUI_WORKER_SECRET`:

```bash
BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)"
"$BIN/rusui-runner" -url http://127.0.0.1:8080 -repo OWNER/REPO -acp
```

`GET http://127.0.0.1:8080/healthz` returns `ok`. `-acp` hosts one Grok ACP
turn (the P1 guest). A process driver is a command that prints review JSON
on stdout; pass `-driver` instead of `-acp`. There is no in-tree
`review-driver` binary.

The process refuses to start if webhook, worker, or Slack secrets are unset.
Pass `-allow-insecure` only for local experiments. GitHub and Slack cannot
reach loopback: [docs/operator.md](docs/operator.md).

Contributor gate before a PR: `make all && make test && make validate`.

## Next

| I want to… | Read |
| --- | --- |
| Run it against a real repository | [docs/operator.md](docs/operator.md) |
| Look up flags, env, HTTP, policy | [docs/configuration.md](docs/configuration.md) |
| Learn the nouns | [CONTEXT.md](CONTEXT.md) |
| Build, test, images | [docs/development.md](docs/development.md) |
| Read the contract | [ARCHITECTURE.md](ARCHITECTURE.md) |
| See accepted direction | [VISION.md](VISION.md), [ROADMAP.md](ROADMAP.md), [docs/decisions/](docs/decisions/) |
| Contribute | [CONTRIBUTING.md](CONTRIBUTING.md), [AGENTS.md](AGENTS.md) |
