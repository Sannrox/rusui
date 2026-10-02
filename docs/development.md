# Build, test, and images

The Makefile is a thin façade over `scripts/make-targets/`. Toolchain
versions, target behavior, repository layout, and CI checks are in the
[build reference](build-reference.md). Image internals are in
[build/README.md](../build/README.md).

<a id="toolchain"></a><a id="layout"></a><a id="ci"></a>

## Targets

Run this before committing:

```bash
make all && make test && make validate
```

Target definitions: [build reference](build-reference.md#make-targets).

```bash
make all WHAT=cmd/rusui
make test WHAT=./internal/engine GOFLAGS="-v" TEST_ARGS='-run ^TestClaim'
make test COVER=1
go test ./eval -v          # development corpus must match eval/results.md
go run ./cmd/rusui eval    # replay report; see eval/set.md
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
loopback inside the container. See [SECURITY.md](../SECURITY.md) before
exposing an HTTP listener.
