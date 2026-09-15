# Contributing

Thanks for looking. rusui is a personal environment plane published as source.
The product contract is [ARCHITECTURE.md](ARCHITECTURE.md). Agents should also
read [AGENTS.md](AGENTS.md).

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
Report security issues through [SECURITY.md](SECURITY.md), not a public issue.

## What to work on

GitHub Issues are the planning source of truth. Prefer issues labeled
`status:ready` whose `## Dependencies` are satisfied. Do not implement live
GitHub mutation or implement-to-PR except as types or comments needed to keep
state machines honest.

Direction beyond the current contract lives in [VISION.md](VISION.md),
[ROADMAP.md](ROADMAP.md), and [docs/decisions/](docs/decisions/). An accepted
ADR does not change the contract until `ARCHITECTURE.md` is rewritten.

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

## Pull requests

Delivery is one issue, one branch, one checkout, one PR. See
[AGENTS.md](AGENTS.md) (parallel delivery lanes). Claim with
`bash .agents/skills/deliver-ready-issue/scripts/issue-lane.sh claim <issue>`
when you have Publish or Land authority.

1. Keep the change to one issue and one coherent outcome.
2. Do not commit secrets, `*.db`, `.version`, or `_output/`.
3. Run `make all && make test && make validate` before you ask for review.
4. Summarize behavior, not file operations. Link the issue.
5. Call out impact on policy, the GitHub client, apply, Slack, persistence,
   configuration, compatibility, or security.
6. Fix actionable review findings before merge.

The GitHub client is read-only. Model CLIs never receive GitHub write tokens.
Bind the HTTP server to loopback in examples unless the change is explicitly
about listen addresses.

## License

Contributions are accepted under the [MIT License](LICENSE).
