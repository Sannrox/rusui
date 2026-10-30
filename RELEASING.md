# Releasing rusui

Family versioning, tagging, and release policy:
[tenkai ADR 0040](https://github.com/Sannrox/tenkai/blob/main/docs/decisions/0040-family-versioning-and-release-policy.md).

This page is rusui-local: how a maintainer prepares and tags **this**
repository.

## Who cuts a tag

Agents never push `v*` tags. After the version-bump pull request merges, the
maintainer creates an annotated, signed `vX.Y.Z` tag with the registered
release-signing key. The tag must equal `v` plus the `VERSION` file. The
workflow refuses unsigned or lightweight tags.

## Prepare

1. Choose the target version. Do not invent one in a feature pull request.
   Write it to `VERSION`.
2. Draft the Keep a Changelog section with `scripts/changelog-draft.sh`.
   Pull requests never edit `CHANGELOG.md`; the version-bump step writes the
   section, and the tag workflow publishes GitHub Release notes from it.
3. Land the version-bump pull request. Binary ldflags still come from the git
   tag via `scripts/lib/version.sh`.

## Tag

Create an annotated, signed tag on the merged release commit:

```text
vX.Y.Z
```

Pre-releases are `vX.Y.Z-rc.N` or `vX.Y.Z-beta.N`. Hotfixes are tagged on
`release/vX.Y`, never on `main` ahead of a minor. A tag is never moved,
deleted, or reused.

## After the tag

The `v*` workflow verifies the tag, builds, tests, writes an SBOM, attests
provenance, records `rusui` / `rusui-runner` binary and image digests, drafts
then publishes a GitHub Release from the matching CHANGELOG section, and
attaches a family release manifest (component, version, git SHA, binary and
image digests, attestation URLs, gate flags). Images are tagged with the
release version. There is no `latest` image tag.

Pull requests and `workflow_dispatch` dry-run the same packaging path and
never mint attestations or a GitHub Release. Image push to a registry stays
the existing `DOCKER_REGISTRY` path in `build/release.sh`.

Allowed tag-signing keys live in `security/release-tag-fingerprints` with the
public key in `security/release-tag-key.asc`.
