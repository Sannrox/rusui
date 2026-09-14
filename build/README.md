# Build and release images

Two Docker layers, same shape as the Makefile flow.

## Build image

`build/build-image/Dockerfile` is a `golang:<.go-version>-trixie` toolchain.
`build/run.sh` builds that image (if needed) and runs a command inside it
with the repo bind-mounted.

```bash
./build/run.sh make all
./build/run.sh make test
./build/run.sh make validate
```

Bump `build/build-image/VERSION` after changing the Dockerfile so local
data containers and tags rebuild.

## Runtime images

`make release-images` (or `./build/release-images.sh`):

1. Build the toolchain image
2. Compile `cmd/rusui` and `cmd/rusui-runner` for `BUILD_PLATFORMS`
   (defaults to the Docker engine's linux arch)
3. Wrap each binary in `build/server-image/Dockerfile` (`debian:trixie-slim`
   + ca-certificates)

Tags look like `rusui-arm64:v0.0.0-dev_<sha>` until a `v*` git tag exists.
`+` in git describe is replaced with `_`.

```bash
make release-images
BUILD_PLATFORMS='linux/amd64 linux/arm64' make release-images
DOCKER_REGISTRY=ghcr.io/sannrox/rusui ./build/release.sh
```

`release.sh` pushes only when `DOCKER_REGISTRY` is set.

The server still binds loopback by default. Publish a port only after
passing `-addr 0.0.0.0:8080`.
