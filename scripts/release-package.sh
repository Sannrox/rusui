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
if [[ -n "$TAG" && "$SKIP_GITHUB" -eq 0 ]]; then
  command -v gh >/dev/null 2>&1 || fail "gh is required for gate flags; pass --skip-github for a dry run"
  # Gate flags cover exactly the pull requests merged into the release commit
  # since the nearest final tag before it: an rc tag never starts the range,
  # or a final release would drop gates first seen in its release candidates,
  # and a pull request merged after the tagged commit never counts, even when a
  # retry runs days later.
  prev="$(git describe --tags --abbrev=0 --match 'v*' --exclude '*-*' "${GIT_SHA}^" 2>/dev/null || true)"
  # The date only narrows the search and the commit range decides. There is
  # no upper date bound: GitHub can record a merge on a later day than its
  # commit date, and that pull request would silently lose its gates.
  range="$GIT_SHA"
  query=""
  if [[ -n "$prev" ]]; then
    range="${prev}..${GIT_SHA}"
    query="merged:>=$(TZ=UTC git log -1 --date=format-local:%Y-%m-%d --format=%cd "$prev")"
  fi
  # A lookup failure or a truncated search fails the release: an empty or
  # partial list would publish a manifest without its gates.
  prs="$(gh pr list --state merged --limit 1000 --search "$query" --json mergeCommit,labels)" \
    || fail "could not list merged pull requests for gate flags"
  [[ "$(jq length <<<"$prs")" -lt 1000 ]] || fail "1000 or more merged pull requests match \"${query}\"; gate flags would be incomplete"
  commits="$(git rev-list "$range")" || fail "could not list the commits in ${range}"
  labels="$(
    jq -r '.[] | select(.mergeCommit != null) | .mergeCommit.oid as $oid | .labels[] | "\($oid)\t\(.name)"' <<<"$prs" \
      | awk -F'\t' 'NR == FNR { in_range[$1] = 1; next } in_range[$1] { print $2 }' <(printf '%s\n' "$commits") - \
      | sort -u
  )" || fail "could not read gate flags from the merged pull requests"
  while IFS= read -r label; do
    [[ -n "$label" ]] && LABEL_ARGS+=(--label "$label")
  done <<<"$labels"
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
