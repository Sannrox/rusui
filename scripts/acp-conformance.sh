#!/usr/bin/env bash
# Local Grok ACP conformance. CI does not run this; attach output to the PR
# when credentials and `agent` are available.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
if ! command -v agent >/dev/null 2>&1; then
  if [[ -x "${HOME}/.local/bin/agent" ]]; then
    PATH="${HOME}/.local/bin:${PATH}"
    export PATH
  fi
fi
if ! command -v agent >/dev/null 2>&1; then
  echo "skip: agent not on PATH" >&2
  exit 0
fi
echo "spawn: agent --permission-mode default agent stdio"
RUSUI_ACP_LIVE=1 go test ./internal/acp -run '^TestLiveGrokConformance$' -count=1 -timeout 2m -v
