# Build and repository reference

Use the [development guide](development.md) for commands to run. This page
lists versions, targets, paths, and CI behavior for lookup.

## Toolchain

| Source | Version |
| --- | --- |
| `go.mod` | language `go 1.26` |
| `.go-version` | `1.26.6` — `make` sets `GOTOOLCHAIN` unless `FORCE_HOST_GO` is set |
| CI | `.github/workflows/build.yml` uses `go-version-file: .go-version` |

Docker is optional for host `make all`. Unattended public-repository
sessions require the container topology
([ADR 0022](decisions/0022-public-repo-isolation.md)).

## Make targets

| Target | What it does |
| --- | --- |
| `make all` | Host-platform binaries into `_output/local/bin/$(go env GOOS)/$(go env GOARCH)/` |
| `make test` | Unit tests (`COVER=1` writes coverage HTML) |
| `make validate` | documentation links and reachability, golangci-lint, govulncheck, go-fix, shellcheck, workflow skills |
| `make update` | Apply go-fix modernizations |
| `make release-images` | Docker build image, linux binaries, `rusui` / `rusui-runner` runtime images |
| `make docker-clean` | Remove docker build containers/tags and `_output` |
| `make clean` | Remove `_output` |

## Layout

| Path | Role |
| --- | --- |
| `cmd/rusui` | HTTP server |
| `cmd/rusui-runner` | Outbound runner (process driver or ACP) |
| `internal/engine` | admit, lease, refresh, complete, dry-run apply |
| `internal/server` | HTTP + Slack routing |
| `internal/policy` | YAML parse, fail-closed unknown fields |
| `internal/store` | SQLite |
| `internal/gh` | intake GitHub client (read-only) + webhook verify |
| `internal/publish` | proof and independent-review outcome evaluation; optional evidence since [ADR 0015](decisions/0015-agent-publication.md), never a publication gate |
| `internal/slack` | HMAC, allowlist, outbound exceptions |
| `eval/` | review-quality fixtures |

The historical agent paste-prompt is [v1-build-prompt.md](v1-build-prompt.md).
Do not treat it as the contract.

## CI

On push to `main` and on pull requests, GitHub Actions runs `make all`,
`make test`, `make validate`, and `make release-images`. Branch protection
requires a pull request to `main` and the `build` and `images` checks.
Do not push commits to `main` directly.
