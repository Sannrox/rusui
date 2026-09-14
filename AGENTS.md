# Repository Guidelines

`rusui` is a personal GitHub maintenance control plane. You write
`policy.yaml`. The server admits work, records immutable reviews, and
dry-runs apply. `ARCHITECTURE.md` is the product contract. Do not
implement v2/v3 behavior except as types or comments needed to keep
state machines honest.

GitHub Issues are the planning source of truth. Project-specific Skills
under `.agents/skills/` define the expected workflows for shaping,
delivering, verifying, assessing, and releasing work:

| Skill | Use when |
| --- | --- |
| `shape-work-item` | turning intent into a focused Issue |
| `advance-issue-frontier` | asking what is ready next |
| `assess-change-impact` | scoping risk across policy, admit, apply, Slack, or store |
| `deliver-ready-issue` | implementing, publishing, or landing one ready Issue |
| `verify-change` | collecting exact local evidence |
| `capture-project-decision` | promoting an accepted choice into an ADR or doc |
| `prepare-release` | assembling release readiness |

## Build, Test, and Development Commands

Run this before committing:

```bash
make all && make test && make validate
```

- `make all` builds host-platform binaries into `_output/`.
- `make test` runs unit tests. Narrow with
  `make test WHAT=./internal/engine TEST_ARGS='-run ^TestClaim$'`.
- `make validate` runs golangci-lint, govulncheck, go-fix, and shellcheck.
- `make release-images` builds Linux binaries and runtime images when
  Docker is available.

The Makefile is a thin façade over `scripts/make-targets/`. Verification
should be proportional to the change. Before delivery, use `verify-change`.
Before commit or PR, fix actionable review findings.

## Issue conventions

Draft Issues with problem, observable outcome, acceptance evidence,
non-goals, `## Dependencies`, and impact. Parse dependencies only from
`## Dependencies`. Prefer `status:ready` and `status:blocked` labels
when mutation is authorized.

## Security

Never commit secrets, GitHub or Slack tokens, webhook secrets, worker
secrets, local SQLite databases, `.version`, or `_output/`. Bind the
HTTP server to loopback. The GitHub client is read-only. Model CLIs
never receive GitHub write tokens.
