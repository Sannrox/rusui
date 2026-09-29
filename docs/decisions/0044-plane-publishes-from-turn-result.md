# ADR 0044: The plane publishes from the turn result

- Status: Accepted
- Date: 2026-09-29
- Resolves: [#382](https://github.com/Sannrox/rusui/issues/382)
- Amends: [ADR 0020](0020-turn-scoped-github-publication.md) (how the agent
  asks the plane to publish)
- Related: [ADR 0015](0015-agent-publication.md),
  [ADR 0038](0038-maintenance-eligibility-and-promotion.md) D4,
  [ARCHITECTURE.md](../../ARCHITECTURE.md) (Publication).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

ADR 0020 moves implement-session publication behind the plane: the guest
pushes its session ref through the git proxy and "asks the plane to create
or update its PR" through a publication endpoint. The git proxy already
restricts pushes to `refs/heads/rusui/<session>/*` under a turn grant. The
guest already reports its outcome in one JSON result file that the runner
returns on Complete, and Complete is where the plane checks the lease and
observes what GitHub shows.

A guest-callable publication endpoint would be a second authenticated
write surface a compromised guest can probe mid-turn.

## Decision

- **The request travels in the turn result.** The agent writes
  `{"publish": {"branch": "rusui/<session>/<name>", "title": "...",
  "body": "..."}}` to `$RUSUI_RESULT`. There is no guest-callable
  publication endpoint.
- **The plane writes once, on Complete.** Before the GitHub write it
  checks, outside the result transaction, that the turn still holds the
  lease generation and claimed revision it reports, that the session is an
  implement session (open task, bound repository with `implement: true`,
  project admits `run`), and that the branch is under the session prefix.
  It then updates the session's prior pull request, or finds an open pull
  request for the branch, or creates one against the task ref. Lookup by
  branch makes a retried Complete after a lost response update, not
  duplicate.
- **One repository, two permissions.** The write uses an installation
  token minted for the task repository with `contents: write` and
  `pull_requests: write`. The git proxy uses the same scoped token for that
  repository's pushes. Only create and update of a pull request are
  implemented; there is no merge, close, label, or release call.
- **The result is the receipt.** The plane sets `publisher: "plane"` and
  the pull request number on the stored result; published, unconfirmed,
  and blocked keep their ADR 0015 meaning. A refused or failed write
  becomes `blocked` with the reason. There is no fallback to a personal
  token.
- **Off by default.** `RUSUI_PUBLICATION=plane` enables it and requires
  GitHub App credentials; the plane refuses to start without them. In this
  mode implement guests receive no GitHub credential and
  `RUSUI_AGENT_GITHUB_TOKEN` is ignored. The default stays ADR 0015.
- **Only the plane names pull requests.** In this mode a guest's
  `pull_request` claim is refused. A follow-up updates the session's prior
  pull request only when the plane published it from the same branch, and
  the plane confirms that pull request is still open with that branch as
  head before editing it; otherwise the turn is blocked.
- **The fence follows the credential.** The implement-session permission
  fence (ADR 0017 D3) applies when the guest holds the agent credential or
  publishes through the plane, on the runner and on approval re-checks
  alike.
- **A failed mint refuses writes, not the turn.** ADR 0020 fails the turn
  before the guest starts when the first installation token cannot be
  minted. Here the guest holds no credential either way, so a failed mint
  refuses the proxy push and blocks publication with the reason; the
  workspace stays available for recovery.

Everything else in ADR 0020 stands, including its pilot before the route
becomes the default.

## Consequences

Easier: no new guest-facing API; the lease check, eligibility, and receipt
reuse the Complete path; repair (#108) gains its plane-owned recheck point.

Harder: publication happens only at the end of a turn, so an agent cannot
open a draft pull request mid-turn and keep working on it within the same
turn. A follow-up turn updates it.

Irreversible: none. Unset `RUSUI_PUBLICATION` to return to ADR 0015.

## Rejected alternatives

- **Guest-callable publication endpoint (ADR 0020 as written).** A second
  write surface for the same outcome, reachable while the turn runs.
- **Installation-wide App token.** Grants every repository the App is
  installed on; ADR 0020 asks for one repository.

## Validation and reversal

Tests cover eligibility refusals, stale lease, foreign branches, lost
responses, and follow-up updates against a fake GitHub. The ADR 0020
controlled pilot on the maintainer's App remains the gate for making
`plane` the default. Reverse by unsetting `RUSUI_PUBLICATION`.

## Sources

- [ADR 0020](0020-turn-scoped-github-publication.md) Decision and Validation.
- `internal/server/gitproxy.go`, `internal/engine/result.go` at `f1b8abe`.
