# Reference rusui guest (ADR 0018 D4): git, gh, Node.js, and the Claude
# Code ACP adapter at the ADR 0017 version. The plane CA is mounted at run
# time at /usr/local/share/ca-certificates/rusui-plane.crt, never baked in.
FROM node:22-bookworm-slim@sha256:48e4b67d85f87bd551df43704e24d252f56cc5f8e9718841aace50f19948f0f9

ARG GH_VERSION=2.101.0
ARG GH_SHA256_AMD64=9bca2d1c16825f109907a23307628a2f0698fbf99662b73a5cf0b020293072b8
ARG GH_SHA256_ARM64=b57e8063f18862647c9d22727c32e9da1b963f8bf9db648fe123a6975695640f
ARG CLAUDE_ACP_VERSION=0.81.1

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git procps \
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

RUN npm install -g --no-fund --no-audit "@agentclientprotocol/claude-agent-acp@${CLAUDE_ACP_VERSION}" \
 && npm cache clean --force

# Node does not read the system store; point it at the mounted plane CA.
ENV NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/rusui-plane.crt

WORKDIR /workspace
