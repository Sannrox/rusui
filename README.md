# Rusui (留守居)

Self-hosted environment plane for coding agents. You write `policy.yaml`.
The server admits work onto sessions and turns, records immutable reviews,
and **dry-runs** apply for comment and close. It does not comment, close,
or merge on GitHub. In `implement` sessions the agent pushes and opens
its own pull requests with the operator's credential
([ADR 0015](docs/decisions/0015-agent-publication.md)).

留守居 is the steward who keeps house while you are away.

**Status:** self-hosted source, loopback by default, dry-run apply for
comment and close. Not a hosted product.

[Docs](docs/README.md) · [Architecture](ARCHITECTURE.md) ·
[Contributing](CONTRIBUTING.md) · [Security](SECURITY.md) ·
[License](LICENSE)

## What it is

GitHub is intake. The rusui server is the source of truth. Slack is the
optional human socket. A runner claims turns and hosts a guest CLI (process
driver or ACP). Model processes receive a GitHub write credential only in `implement`
sessions.

The experimental local-interactive profile is default-off and runs a
policy-configured process through Sumika on the operator's host ([ADR 0016](docs/decisions/0016-local-interactive-runtime.md), [operator profile](docs/local-interactive.md)).

Nouns (project, session, turn, environment, runner): [CONTEXT.md](CONTEXT.md).
The v1 contract: [ARCHITECTURE.md](ARCHITECTURE.md).

## What it is not

Live comment, close, merge, or land; unnamed GitHub writes; a dashboard;
or a multi-tenant SaaS. Later milestones and competitive sequencing live
in [VISION.md](VISION.md) and [ROADMAP.md](ROADMAP.md); they are not the
runbook.

## Quick start

Run the [first-session tutorial](docs/tutorial.md) to see a disposable
session and receipt without credentials. To connect a real repository, use
the [operator guide](docs/operator.md); look up flags and policy fields in
the [configuration reference](docs/configuration.md).

Contributor gate before a PR: `make all && make test && make validate`.

## Next

| I want to… | Read |
| --- | --- |
| Walk through a first loopback session | [docs/tutorial.md](docs/tutorial.md) |
| See a sample session and receipt without GitHub | [docs/tutorial.md](docs/tutorial.md#sample-demo-no-github-no-model) |
| Run it against a real repository | [docs/operator.md](docs/operator.md) |
| Review-only GitHub App behind a tunnel | [docs/github-app-pilot.md](docs/github-app-pilot.md) |
| Drain, upgrade, diagnostics | [docs/upgrade.md](docs/upgrade.md) |
| Look up flags, env, HTTP, policy | [docs/configuration.md](docs/configuration.md) |
| Learn the nouns | [CONTEXT.md](CONTEXT.md) |
| Build, test, images | [docs/development.md](docs/development.md) |
| Read the contract | [ARCHITECTURE.md](ARCHITECTURE.md) |
| See accepted direction | [VISION.md](VISION.md), [ROADMAP.md](ROADMAP.md), [docs/decisions/](docs/decisions/) |
| Contribute | [CONTRIBUTING.md](CONTRIBUTING.md), [AGENTS.md](AGENTS.md) |
