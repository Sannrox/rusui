#!/usr/bin/env bash
set -euo pipefail

ROOT_PATH="$(cd "$(dirname "$0")/.." && pwd -P)"
PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_PATH/scripts/test-check-docs.py"
PYTHONDONTWRITEBYTECODE=1 python3 "$ROOT_PATH/scripts/check-docs.py" --root "$ROOT_PATH"
