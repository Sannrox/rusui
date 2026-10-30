#!/usr/bin/env bash
# Build release artifacts into --out without publishing or attesting.
#
# Usage:
#   bash scripts/release-package.sh --out dist [--tag vX.Y.Z] [--git-sha SHA]
#        [--skip-build] [--skip-test] [--skip-github] [--with-images]
set -euo pipefail

ROOT_PATH="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_PATH"

OUT=""
TAG=""
GIT_SHA=""
SKIP_BUILD=0
SKIP_TEST=0
SKIP_GITHUB=0
WITH_IMAGES=0

fail() {
  printf 'release-package: %s\n' "$1" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) OUT="${2:-}"; shift 2 ;;
    --tag) TAG="${2:-}"; shift 2 ;;
    --git-sha) GIT_SHA="${2:-}"; shift 2 ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    --skip-test) SKIP_TEST=1; shift ;;
    --skip-github) SKIP_GITHUB=1; shift ;;
    --with-images) WITH_IMAGES=1; shift ;;
    *) fail "unknown argument: $1" ;;
  esac
done

[[ -n "$OUT" ]] || fail "usage: $0 --out dist [--tag vX.Y.Z] [--git-sha SHA]"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

workspace_version() {
  local text
  [[ -f "$ROOT_PATH/VERSION" ]] || fail "missing VERSION file"
  text="$(tr -d '[:space:]' <"$ROOT_PATH/VERSION")"
  [[ -n "$text" ]] || fail "VERSION is empty"
  printf '%s\n' "$text"
}

VERSION="$(workspace_version)"
if [[ -n "$TAG" ]]; then
  [[ "${TAG#v}" == "$VERSION" ]] || fail "tag ${TAG} does not match workspace version ${VERSION}"
  python3 "$ROOT_PATH/scripts/changelog-notes.py" "$VERSION" >"${OUT}/RELEASE_NOTES.md"
fi
if [[ -z "$GIT_SHA" ]]; then
  GIT_SHA="$(git rev-parse HEAD)"
fi
[[ "$GIT_SHA" =~ ^[0-9a-f]{40}$ ]] || fail "git sha must be a full lowercase SHA"

if [[ "$SKIP_TEST" -eq 0 ]]; then
  make test
fi

host_os="$(go env GOOS 2>/dev/null || true)"
host_arch="$(go env GOARCH 2>/dev/null || true)"
if [[ "$SKIP_BUILD" -eq 0 ]]; then
  [[ -n "$host_os" && -n "$host_arch" ]] || fail "go is required to package binaries"
  make all
  for bin in rusui rusui-runner; do
    src="${ROOT_PATH}/_output/local/bin/${host_os}/${host_arch}/${bin}"
    [[ -f "$src" ]] || fail "missing binary ${src}"
    dest="${OUT}/${bin}-${VERSION}-${host_os}-${host_arch}"
    cp "$src" "$dest"
  done
fi

IMAGE_ARGS=()
if [[ "$WITH_IMAGES" -eq 1 ]]; then
  command -v docker >/dev/null 2>&1 || fail "docker is required for --with-images"
  make release-images
  if docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^(rusui|rusui-runner)-[^:]+:latest$' >/dev/null; then
    fail "must not produce a latest image tag"
  fi
  source "${ROOT_PATH}/scripts/lib/init.sh"
  version::get_version
  docker_tag="${GIT_VERSION/+/_}"
  [[ -n "$docker_tag" ]] || fail "GIT_VERSION is empty after image build"
  for bin in rusui rusui-runner; do
    for arch in amd64 arm64; do
      name="${bin}-${arch}:${docker_tag}"
      if ! docker image inspect "$name" >/dev/null 2>&1; then
        continue
      fi
      digest="$(docker image inspect --format '{{.Id}}' "$name")"
      [[ "$digest" == sha256:* ]] || fail "image ${name} has no sha256 id"
      docker save -o "${OUT}/${bin}-${arch}-${VERSION}-image.tar" "$name"
      IMAGE_ARGS+=(--image "${name}=${digest}")
      printf 'image %s digest %s\n' "$name" "$digest"
    done
  done
  [[ ${#IMAGE_ARGS[@]} -gt 0 ]] || fail "--with-images did not find rusui runtime images"
fi

python3 "$ROOT_PATH/scripts/release-sbom.py" "${OUT}/rusui.cdx.json"

python3 -c '
import hashlib, pathlib, sys
out = pathlib.Path(sys.argv[1])
lines = []
for path in sorted(p for p in out.iterdir() if p.is_file() and p.name != "SHA256SUMS"):
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    lines.append(f"{digest}  {path.name}")
(out / "SHA256SUMS").write_text("\n".join(lines) + ("\n" if lines else ""))
' "$OUT"

LABEL_ARGS=()
if [[ -n "$TAG" && "$SKIP_GITHUB" -eq 0 ]] && command -v gh >/dev/null 2>&1; then
  prev="$(git tag --list 'v*' --sort=-version:refname)"
  prev="$(awk -v cur="$TAG" '$0 != cur { print; exit }' <<<"$prev")"
  search=()
  if [[ -n "$prev" ]]; then
    since="$(git log -1 --format=%cs "$prev" 2>/dev/null || true)"
    if [[ -n "$since" ]]; then
      search+=(--search "merged:>=${since}")
    fi
  fi
  while IFS= read -r label; do
    [[ -n "$label" ]] && LABEL_ARGS+=(--label "$label")
  done < <(gh pr list --state merged --limit 100 "${search[@]}" --json labels --jq '.[].labels[].name' 2>/dev/null || true)
fi

python3 "$ROOT_PATH/scripts/release-manifest.py" \
  --component rusui \
  --version "$VERSION" \
  --git-sha "$GIT_SHA" \
  ${TAG:+--tag "$TAG"} \
  --dist "$OUT" \
  --out "${OUT}/rusui-release.json" \
  "${IMAGE_ARGS[@]}" \
  "${LABEL_ARGS[@]}"

echo "version=${VERSION}"
echo "artifacts in ${OUT}:"
cat "${OUT}/SHA256SUMS"
if [[ ${#IMAGE_ARGS[@]} -eq 0 ]]; then
  echo "images: none in this package"
else
  echo "images: ${#IMAGE_ARGS[@]} (no latest tag)"
fi
