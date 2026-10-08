# Reference rusui guest (ADR 0018 D4, ADR 0025, ADR 0050): git, gh, Node.js,
# pinned Go, make, a C toolchain for `go test -race`, headless Chromium, and
# the pinned Claude Code CLI and shikigami ACP guest. claude-agent-acp is not installed. The image
# does not declare a reachability ask (ADR 0027); toolchain is baked in, and
# the guest link still forwards only the plane and, in implement sessions,
# GitHub. The plane CA is mounted at run time at
# /usr/local/share/ca-certificates/rusui-plane.crt.
# The release client has only public WebPKI roots. Build the pinned source
# with explicit extra-CA trust so the mounted plane CA is honored.
FROM rust:1.97.1-bookworm@sha256:0e2bcaef56d041a486784e54104a81aebe0da44bd03019bd70bc0401e42e4a97 AS shikigami-build
ARG SHIKIGAMI_SOURCE_REF=9e53d723018c4f89f9f2d0dd4373ff725962b330
ARG SHIKIGAMI_SOURCE_SHA256=387f8522fbf701be571de081ea4cb94a2f0f03bbc94c4e6351d459a76750ee80
RUN apt-get update && apt-get install -y --no-install-recommends python3 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN curl -fsSLo /tmp/source.tgz "https://codeload.github.com/Sannrox/shikigami/tar.gz/${SHIKIGAMI_SOURCE_REF}" \
 && echo "$SHIKIGAMI_SOURCE_SHA256  /tmp/source.tgz" | sha256sum -c - \
 && tar -xzf /tmp/source.tgz --strip-components=1 -C /src
RUN python3 - <<'PYCODE'
from pathlib import Path
p = Path("crates/shikigami-http/src/lib.rs")
s = p.read_text()
needle = "_ => Arc::new(reqwest::Client::new()),"
assert s.count(needle) == 1
s = s.replace(needle, r"""_ => {
                let mut builder = reqwest::Client::builder();
                if let Ok(path) = std::env::var("SHIKIGAMI_EXTRA_CA_CERTS") {
                    let pem = std::fs::read(path)
                        .map_err(|_| ModelError::Message("cannot read model CA".into()))?;
                    let cert = reqwest::Certificate::from_pem(&pem)
                        .map_err(|_| ModelError::Message("invalid model CA".into()))?;
                    builder = builder.add_root_certificate(cert);
                }
                Arc::new(builder.build()
                    .map_err(|_| ModelError::Message("cannot build model HTTP client".into()))?)
            },""")
p.write_text(s)
PYCODE
RUN cargo build --locked --release --bin shikigami --no-default-features --features model-http

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

COPY --from=shikigami-build /src/target/release/shikigami /usr/local/bin/shikigami
RUN shikigami --version
ENV SHIKIGAMI_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/rusui-plane.crt

# Node does not read the system store; point it at the mounted plane CA.
ENV NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/rusui-plane.crt

WORKDIR /workspace
