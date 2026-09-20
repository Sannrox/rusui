# Build, test, and images

The Makefile is a thin façade over `scripts/make-targets/`. Image internals:
[build/README.md](../build/README.md).

## Toolchain

| Source | Version |
| --- | --- |
| `go.mod` | language `go 1.26` |
| `.go-version` | `1.26.6` — `make` sets `GOTOOLCHAIN` unless `FORCE_HOST_GO` is set |
| CI | `.github/workflows/build.yml` uses `go-version-file: .go-version` |

Docker is optional for host `make all`.

## Targets

Run this before committing:

```bash
make all && make test && make validate
```

| Target | What it does |
| --- | --- |
| `make all` | Host-platform binaries into `_output/local/bin/$(go env GOOS)/$(go env GOARCH)/` |
| `make test` | Unit tests (`COVER=1` writes coverage HTML) |
| `make validate` | golangci-lint, govulncheck, go-fix, shellcheck |
| `make update` | Apply go-fix modernizations |
| `make release-images` | Docker build image, linux binaries, `rusui` / `rusui-runner` runtime images |
| `make docker-clean` | Remove docker build containers/tags and `_output` |
| `make clean` | Remove `_output` |

```bash
make all WHAT=cmd/rusui
make test WHAT=./internal/engine GOFLAGS="-v" TEST_ARGS='-run ^TestClaim'
make test COVER=1
go test ./eval -v
```

Dockerized Makefile:

```bash
./build/run.sh make all
./build/run.sh make test
./build/run.sh make validate
make release-images
```

Bump `build/build-image/VERSION` after changing the toolchain Dockerfile.

## Runtime images

`make release-images` compiles `cmd/rusui` and `cmd/rusui-runner` for
`BUILD_PLATFORMS` and wraps each in `build/server-image/Dockerfile`.

Tags look like `rusui-arm64:v0.0.0-dev_<sha>` until a `v*` git tag exists.

```bash
make release-images
BUILD_PLATFORMS='linux/amd64 linux/arm64' make release-images
DOCKER_REGISTRY=ghcr.io/sannrox/rusui ./build/release.sh
```

`release.sh` pushes only when `DOCKER_REGISTRY` is set. The server still binds
loopback inside the container. Publish a port only after `-addr 0.0.0.0:8080`
and with secrets set.

## Layout

| Path | Role |
| --- | --- |
| `cmd/rusui` | HTTP server |
| `cmd/rusui-runner` | Outbound runner (process driver or ACP) |
| `internal/engine` | admit, lease, refresh, complete, dry-run apply |
| `internal/server` | HTTP + Slack routing |
| `internal/policy` | YAML parse, fail-closed unknown fields |
| `internal/store` | SQLite |
| `internal/gh` | read-only GitHub client + webhook verify |
| `internal/slack` | HMAC, allowlist, outbound exceptions |
| `eval/` | review-quality fixtures |

The historical agent paste-prompt is [v1-build-prompt.md](v1-build-prompt.md).
Do not treat it as the contract.

## CI

On push to `main` and on pull requests, GitHub Actions runs `make all`,
`make test`, `make validate`, and `make release-images`. Branch protection
requires a pull request to `main` and the `build` and `images` checks.
Do not push commits to `main` directly.
