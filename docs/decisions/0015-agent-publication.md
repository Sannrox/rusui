# ADR 0015: The agent publishes its own pull requests

- Status: Accepted
- Date: 2026-09-23
- Supersedes: [ADR 0013](0013-publication-authority.md) (exact-artifact
  verification and plane-owned publication).
- Amends: [ADR 0009](0009-credential-broker.md) ("No GitHub REST token in
  the guest" and session-ref enforcement for publishing sessions);
  [ADR 0014](0014-pilot-evaluation-deferred.md) (pilot preconditions 1–2
  and the identity-collapse failure criterion);
  [ARCHITECTURE.md](../../ARCHITECTURE.md) current contract (publication).
- Related: [ADR 0010](0010-hybrid-roadmap-sequence.md) D5 (humans merge
  core delivery).
- Discussion: none. GitHub Discussions are disabled. The maintainer chose
  this model directly; merging with this status is the acceptance act.
- Accepted target amendment: [ADR 0020](0020-turn-scoped-github-publication.md)
  sets a plane-owned, turn-scoped publication route for future implementation
  of D1 and D4. This ADR and [ARCHITECTURE.md](../../ARCHITECTURE.md) continue
  to describe current behavior until the implementation follow-up lands.

## Context

ADR 0013 made the plane the only publisher. A guest proposed a candidate
commit, an isolated verifier proved it, a different session reviewed it,
and only then did the plane's `Publisher` open or update a draft pull
request with a GitHub App installation token. The guest never held a
GitHub write credential.

That model has not produced a single live publication. The `Publisher`
merged in [#177](https://github.com/Sannrox/rusui/pull/177) but needs a
GitHub App installation, an isolated verifier run, and a second review
session before its first pull request. [ADR 0014](0014-pilot-evaluation-deferred.md)
deferred the ten-task pilot ([#103](https://github.com/Sannrox/rusui/issues/103))
on exactly those prerequisites, and every later roadmap item depends on
that pilot.

Widely used coding agents take a simpler route. The agent runs `git` and
the GitHub CLI (`gh`) with the operator's own credentials, pushes a
branch, and opens the pull request itself. The pull request is authored
by the operator. Commit trailers mark the agent's involvement: a
`Co-authored-by` line naming the agent and a trailer linking the agent
thread that produced the change. The human reviews the pull request on
GitHub and decides whether to merge. The operator already trusts the
agent with their working tree; giving it the same GitHub identity they
use at the keyboard matches that trust instead of building a second
authority for it.

## Decision

### D1. The agent publishes

A session in a project whose policy enables `implement` receives a
GitHub credential for the operator's configured identity. Inside the
session the agent runs `git push` and `gh pr create` / `gh pr edit`
directly against GitHub. The plane no longer opens or updates pull
requests on the agent's behalf.

The plane decides only **whether** a session gets the credential: it
provides it to `implement` sessions of bound repositories and to nothing
else. Review sessions, snapshot prepare, and projects without
`implement` keep the read-only grants of ADR 0009.

### D2. No publication gate

Opening or updating a pull request requires no `proven` outcome, no
isolated verifier run, and no independent review session. The agent
decides when its work is ready to publish, in the same way it would at
an operator's keyboard. The human review of the pull request on GitHub
is the review.

Verifier runs, proof records, and review sessions remain available as
evidence that an operator or a later workflow may request. They no
longer block publication.

### D3. Attribution through trailers

Every commit the agent makes in a session carries two trailers, both on
by default:

```text
Co-authored-by: rusui <noreply@rusui.invalid>
Rusui-Session: <session id or console URL>
```

The operator can turn either trailer off with
`RUSUI_DISABLE_COAUTHOR_TRAILER=1` or `RUSUI_DISABLE_SESSION_TRAILER=1`.
The co-author address uses the reserved `.invalid` domain so it can
never resolve to a real GitHub account. Attribution is a label, not a
control: repository content or the agent can omit or alter it, and the
plane does not rely on it to authorize anything.

### D4. Credential owner and scope

The credential belongs to the operator. The pull request author is the
operator's account, not a bot. Scope is set where the credential is
issued: operators should use a fine-grained token limited to the bound
repositories with contents and pull-request write, and no
administration, workflow, or secret scopes. The plane does not narrow a
credential it was given.

The model provider key stays behind the plane's model proxy (ADR 0009).
Only the GitHub credential moves into the guest.

### D5. What stays unauthorized

Humans merge ([ADR 0010](0010-hybrid-roadmap-sequence.md) D5). The
product contract still does not authorize merge, close, land, label, or
release actions by the agent. Unlike ADR 0013, this is no longer
technically enforced: a token that can push can usually do more. Holding
this line is the job of the token's scope (D4) and of repository branch
protection, not of the plane.

## Consequences

Easier:

- The first live publication needs only an operator token, not a GitHub
  App installation, a verifier environment, and a second review session.
- #103 can run once this decision is implemented, against a named pilot
  repository and guest ([ADR 0014](0014-pilot-evaluation-deferred.md)
  precondition 3).
- The agent can respond to review feedback on its own pull request
  directly, without a plane round trip.

Harder:

- A GitHub write credential now lives in the guest environment. A
  hostile repository or a misbehaving model can use it for anything the
  token permits. Token scope and branch protection become the security
  boundary.
- The pull request author and merger are the same account. GitHub
  metadata alone can no longer show whether a human reviewed before a
  pull request was opened; the session trailer and the session record
  are the evidence.
- `internal/publish.Evaluate`, the plane `Publisher`, and the
  `publication_attempts` persistence stop being the publication path.
  Removing or repurposing them, adding the credential to `implement`
  sessions, allowing GitHub egress for those sessions, and adding the
  trailers is follow-up implementation work. Until that lands, the code
  still implements ADR 0013.

Irreversible: none. No schema is dropped by this decision.

## Rejected alternatives

- **Keep plane-owned publication (ADR 0013).** Rejected by the
  maintainer: it has produced no live publication and blocks the pilot
  on infrastructure that ordinary coding-agent use does not need.
- **Plane runs `gh` with the operator's login.** Keeps the credential out
  of the guest but still makes the plane the publisher and keeps the
  agent from managing its own pull request. Rejected in favour of the
  simpler agent-driven model.
- **Keep the proof and review gate, change only the identity.** Rejected:
  the gate, not the identity, is what makes publication depend on the
  verifier and a second session.
- **A dedicated bot account for the agent.** Separates identities but
  needs a second GitHub account and token per operator. Can be revisited
  if author/merger separation becomes a requirement.

## Validation and reversal

This works when an `implement` session pushes a branch and opens a pull
request with the two trailers, as the operator, with no plane-side
publication step, and a session without `implement` cannot push. Reverse
by superseding this ADR, withdrawing the guest credential, and restoring
a plane-owned publication gate.

## Sources

- [ADR 0013](0013-publication-authority.md) (superseded)
- [ADR 0009](0009-credential-broker.md), [ADR 0010](0010-hybrid-roadmap-sequence.md),
  [ADR 0014](0014-pilot-evaluation-deferred.md)
- [#103](https://github.com/Sannrox/rusui/issues/103),
  [#177](https://github.com/Sannrox/rusui/pull/177)
- Decision made against `main` at
  [`69072c7`](https://github.com/Sannrox/rusui/commit/69072c7f4669c4bf0fc9ca256ee714bf92938f42)
