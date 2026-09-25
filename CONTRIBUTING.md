# Contributing

Thanks for looking. rusui is a self-hosted environment plane published as source.
The product contract is [ARCHITECTURE.md](ARCHITECTURE.md). Agents should also
read [AGENTS.md](AGENTS.md).

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
Report security issues through [SECURITY.md](SECURITY.md), not a public issue.

## What to work on

GitHub Issues are the planning source of truth. Prefer issues labeled
`status:ready` whose `## Dependencies` are satisfied. Do not implement live
comment, close, merge, land, or unnamed GitHub writes.

Direction beyond the current contract lives in [VISION.md](VISION.md),
[ROADMAP.md](ROADMAP.md), and [docs/decisions/](docs/decisions/). An accepted
ADR does not change the contract until `ARCHITECTURE.md` is rewritten.

## Repository map

| Area | Start here | Contract or operator guide |
| --- | --- | --- |
| Policy | [`internal/policy`](internal/policy), [`policy.example.yaml`](policy.example.yaml) | [`docs/configuration.md`](docs/configuration.md) |
| Intake and admission | [`internal/gh`](internal/gh) reads GitHub; [`internal/server`](internal/server) accepts events; [`internal/engine`](internal/engine) refreshes and admits work | [`ARCHITECTURE.md`](ARCHITECTURE.md) |
| Runner and guest | [`cmd/rusui-runner`](cmd/rusui-runner), [`internal/runner`](internal/runner), [`internal/acp`](internal/acp), [`internal/env`](internal/env) | [`docs/operator.md`](docs/operator.md) |
| Receipts and persistence | [`internal/store`](internal/store), with review and turn flows in [`internal/engine`](internal/engine) | [`ARCHITECTURE.md`](ARCHITECTURE.md) |
| Operator surfaces | [`cmd/rusui`](cmd/rusui), [`internal/server`](internal/server), [`internal/slack`](internal/slack), [`internal/setup`](internal/setup) | [`docs/operator.md`](docs/operator.md), [`docs/configuration.md`](docs/configuration.md) |
| Terms and decisions | [`CONTEXT.md`](CONTEXT.md), [`docs/decisions/`](docs/decisions/) | [`ARCHITECTURE.md`](ARCHITECTURE.md) is the product contract; proposed direction stays proposed |

For a Go contributor, the fake ACP conformance case is
[`TestConformanceFakeAgent`](internal/acp/client_test.go); run it with
`go test ./internal/acp -run '^TestConformanceFakeAgent$'`. The review
evaluation set is [`eval/set.md`](eval/set.md), and its evidence work is
tracked in [issue #105](https://github.com/Sannrox/rusui/issues/105). Do not
present evaluation results as public evidence until #105 supplies a sanitized,
reproducible record.

## Development setup

You need:

- Go 1.26 on `PATH`. `make` pins the toolchain to [`.go-version`](.go-version)
  (currently `1.26.6`).
- `make`, `git`, and (for `make validate`) `shellcheck`.
- Docker only for `make release-images` or `./build/run.sh`.

```bash
git clone https://github.com/Sannrox/rusui.git
cd rusui
make all && make test && make validate
```

Binaries land at `_output/local/bin/$(go env GOOS)/$(go env GOARCH)/`.
Narrow a test with:

```bash
make test WHAT=./internal/engine TEST_ARGS='-run ^TestClaim'
```

Full target list: [docs/development.md](docs/development.md).

## Issues

Draft issues with:

- **Problem**
- **Observable outcome**
- **Acceptance evidence**
- **Non-goals**
- **`## Dependencies`** — parse dependencies only from this heading
- **Impact** — policy, admit/lease, GitHub, apply, Slack, store, docs

Use the [bug](.github/ISSUE_TEMPLATE/bug.yml) or
[feature](.github/ISSUE_TEMPLATE/feature.yml) form. Route exploitable behavior
through [SECURITY.md](SECURITY.md). Durable boundary changes need an
[ADR](docs/decisions/README.md).

A good first-issue candidate for someone comfortable with protocol research
is [issue #247](https://github.com/Sannrox/rusui/issues/247): decide one
GitLab intake profile and operator workflow. It is ready with no dependencies,
and its outcome is a reviewed adopt, defer, or reject decision, not an adapter
implementation.

## Pull requests

`main` accepts changes through pull requests. Do not push commits to `main`
directly.

Fork the repository and open a pull request against `main`. Maintainer
and agent delivery lanes (claim script, isolated worktrees, parallel
limits) are documented in [AGENTS.md](AGENTS.md); external contributors
do not need that protocol.

1. Keep the change to one issue and one coherent outcome.
2. Do not commit secrets, `*.db`, `.version`, or `_output/`.
3. Run `make all && make test && make validate` before you ask for review.
   Pull requests to `main` must pass the `build` and `images` GitHub
   Actions checks.
4. Summarize behavior, not file operations. Link the issue.
5. Call out impact on policy, the GitHub client, apply, Slack, persistence,
   configuration, compatibility, or security.
6. Fix actionable review findings before merge.

The intake GitHub client is read-only. Review sessions never receive a GitHub
write credential; only implement sessions do under
[ADR 0015](docs/decisions/0015-agent-publication.md).
Bind the HTTP server to loopback in examples unless the change is explicitly
about listen addresses.

## License

Contributions are accepted under the [MIT License](LICENSE).
