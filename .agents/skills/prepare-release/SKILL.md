---
name: prepare-release
description: Prepare a rusui release by auditing version scope, compatibility, policy, validation, images, and release notes. Use when a maintainer asks for release readiness, a version bump plan, or a draft GitHub Release.
---

# Prepare Release

Assemble decision-ready release evidence. Do not tag, push, publish, or alter
GitHub state unless the maintainer explicitly authorizes that action.

## Procedure

1. Identify the target version, base tag, target commit, and milestone or merged
   PR range. Read `go.mod`, `.go-version`, `.github/workflows/build.yml`,
   `BUILD.md`, and open release-blocking Issues. Version is derived from git
   tags via `scripts/lib/version.sh`; do not treat generated `.version` as
   source. Complete when the exact release contents are bounded.
2. Classify changes as `Added`, `Changed`, `Fixed`, `Security`, or `Migration`.
   Check SemVer fit; before `1.0`, call out all public breaking changes even when
   they fit a minor bump. Complete when every user-visible merged change is
   represented once.
3. Audit release impact:
   - `go.mod` / `.go-version` consistency;
   - `policy.example.yaml` defaults and fail-closed unknown fields;
   - HTTP, Slack, and worker endpoint compatibility;
   - SQLite schema and immutable review-artifact compatibility;
   - environment variables, secrets, loopback bind, and operator tunnel docs;
   - security, backup, rollback, and operator actions;
   - `rusui` / `rusui-worker` binaries and runtime images.
   Complete when every applicable item is resolved or a named blocker.
4. Use `verify-change` for the full local gates
   (`make all && make test && make validate`). Confirm current GitHub CI when
   access is available. Do not run live GitHub or Slack tests without
   intentional prerequisites. Complete when evidence is current for the
   target commit.
5. Draft concise user-facing release notes. Put upgrade and operator actions
   before internal implementation detail. Credit contributors through GitHub's
   generated notes rather than maintaining a manual ledger.
6. Report go/no-go. A release is `go` only when required checks pass, no known
   blocker remains, and rollback/upgrade implications are explicit.

## Output

Return the target, commit range, readiness checklist, validation results,
compatibility/migration notes, release-note draft, and blockers. Separate
verified facts from recommendations.
