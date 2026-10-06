# Reference rusui guest (ADR 0018 D4, ADR 0025, ADR 0050): git, gh, Node.js,
# pinned Go, make, a C toolchain for `go test -race`, headless Chromium, and
# the pinned Claude Code CLI. claude-agent-acp is not installed. The image
# does not declare a reachability ask (ADR 0027); toolchain is baked in, and
# the guest link still forwards only the plane and, in implement sessions,
# GitHub. The plane CA is mounted at run time at
# /usr/local/share/ca-certificates/rusui-plane.crt.
FROM node:22-bookworm-slim@sha256:48e4b67d85f87bd551df43704e24d252f56cc5f8e9718841aace50f19948f0f9

ARG GH_VERSION=2.101.0
ARG GH_SHA256_AMD64=9bca2d1c16825f109907a23307628a2f0698fbf99662b73a5cf0b020293072b8
ARG GH_SHA256_ARM64=b57e8063f18862647c9d22727c32e9da1b963f8bf9db648fe123a6975695640f
ARG GO_VERSION=1.26.6
ARG GO_SHA256_AMD64=708effb774be8237570d0add163225abbdfaf4fca28b2611df167beba4feef89
ARG GO_SHA256_ARM64=d0507e9e9d7fe012aae570108cbd76c15de879e17130ab8cb90d4d7445cb1f2e
ARG CLAUDE_CODE_VERSION=2.1.283

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates curl git procps \
        make build-essential \
        chromium fonts-liberation \
 && rm -rf /var/lib/apt/lists/*

RUN set -eu; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) sum="$GH_SHA256_AMD64" ;; \
      arm64) sum="$GH_SHA256_ARM64" ;; \
      *) echo "unsupported arch $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSLo /tmp/gh.tgz "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${arch}.tar.gz"; \
    echo "$sum  /tmp/gh.tgz" | sha256sum -c -; \
    tar -xzf /tmp/gh.tgz -C /tmp; \
    install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh; \
    rm -rf /tmp/gh.tgz "/tmp/gh_${GH_VERSION}_linux_${arch}"

RUN set -eu; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) sum="$GO_SHA256_AMD64" ;; \
      arm64) sum="$GO_SHA256_ARM64" ;; \
      *) echo "unsupported arch $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSLo /tmp/go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz"; \
    echo "$sum  /tmp/go.tgz" | sha256sum -c -; \
    tar -C /usr/local -xzf /tmp/go.tgz; \
    rm -f /tmp/go.tgz

ENV PATH=/usr/local/go/bin:${PATH}

RUN npm install -g --no-fund --no-audit "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && npm cache clean --force \
 && ! command -v claude-agent-acp

# Node does not read the system store; point it at the mounted plane CA.
ENV NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/rusui-plane.crt

WORKDIR /workspace
