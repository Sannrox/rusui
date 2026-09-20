# ADR 0013: Exact-artifact verification and plane-owned publication

- Status: Proposed
- Date: 2026-09-20
- Resolves: [#96](https://github.com/Sannrox/rusui/issues/96)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (product contract;
  this ADR does not rewrite it and does not enable live GitHub writes),
  [ADR 0009](0009-credential-broker.md) (guest never holds GitHub REST),
  [ADR 0010](0010-hybrid-roadmap-sequence.md) (M2 verified delivery).
- Discussion: none. GitHub Discussions are disabled.
  Objects: source, task, candidate, proof, publication.
  Evidence: isolated verifier output over an exact candidate SHA;
  independent review of that SHA; never an agent self-declaration.
  Permitted action: open or update one draft pull request whose head is
  that candidate, on `refs/heads/rusui/<session>/*` of one bound repo.
  Policy: fail closed — guest and verifier have no write credentials;
  hostile repo text cannot verify, disable checks, mutate policy, or
  mint publication authority; unnamed GitHub operations are denied.

## Context

v1 dry-runs apply and keeps the GitHub client read-only. Issue-to-PR
delivery needs a smaller authority than “the agent may write to GitHub.”
ARCHITECTURE.md already sketches v3 implement (proof without publish
credentials, then a plane publish step). This ADR names the M2 profile
that later implementation must not exceed.

## Decision

### D1. Named identities

| Object | Identity | Owner |
| --- | --- | --- |
| **Source** | `(repo, item, snapshot_hash, main_sha)` | plane snapshot |
| **Task** | session of kind `run` plus prompt hash | plane session |
| **Candidate** | `(commit_sha, tree_sha, ref)` | git object on the session ref |
| **Proof** | `(command, exit_code, log_digest, head_sha, base_sha)` | isolated verifier |
| **Publication** | GitHub PR whose head is `commit_sha` | plane, using the installation token |

A proof is valid only for the source snapshot hash and candidate
`commit_sha` it names. Source drift invalidates it. Proofs expire;
expiry is not a pass.

### D2. Trusted verifier

The verifier runs in an isolated environment **without** GitHub write
credentials, Slack tokens, SQLite, or policy write. Its command is
selected by operator policy, not by repository text. Non-zero exit is
`denied`. Missing or timed-out required checks are
`checks_unavailable`, not proven.

Independent review is a **different session** than the author, judging
the same `commit_sha`. The author session cannot supply that review.

Maximum repair iterations after a failed proof: **3**. Further attempts
are `denied` until an operator retry.

### D3. Permitted publication actions

Only:

- `open_pr` — create a draft PR from `refs/heads/rusui/<session>/*`
  into the repository default branch
- `update_pr` — point that PR head at a newly proven `commit_sha`

Preconditions: bound repo is in policy; ref matches the session prefix;
default branch is the base; current GitHub head is empty or an ancestor
of the candidate, or the last proven head this plane published (lost
responses reconcile by GET, then open or update). Idempotent: if the PR
head already equals the proven SHA, the action is a no-op success.

Not permitted in this profile: merge, close, label, review submit,
protection, workflow, release, tag, dispatch, comment outside
`publishable` fields, or any unnamed GitHub write.

### D4. Credential owner

The GitHub App installation token is used only in the plane publish
step, after a `proven` outcome is recorded. Guests, verifier commands,
and model CLIs never receive it. The git proxy may push the candidate
to the session ref prefix under the per-turn grant; that grant cannot
open a PR.

### D5. Hostile repository

Repository setup, tests, and instructions cannot:

- declare the work verified
- disable required checks
- change `policy.yaml` or overlay
- mint publication authority or copy the installation token

Any of those observations is `denied`. Agent-authored receipts are not
proof.

### D6. Explicit outcomes

`denied`, `cancelled`, `source_drift`, `checks_unavailable`,
`ambiguous`, `proven`. `published` is a later implementation issue that
may consume only `proven`. Live close, merge, and arbitrary writes stay
outside this decision. `ARCHITECTURE.md` remains the product contract
until a later accepted rewrite names the live publish path.

## Consequences

- Implementation issues (P2–P6) can bind to these objects without
  inventing a wider GitHub write set.
- Accepting this ADR does not by itself authorize live mutation.
- Schema for proof rows can wait for the publisher issue.

## Rejected alternatives

- **Guest opens the PR.** Puts a write token in the environment.
- **CI green is proof.** Repository-controlled workflows are untrusted
  input, not verifier output.
- **Author session reviews itself.** No independent review.
- **Merge in this profile.** Out of scope; land remains a later track.
- **Unbounded repair.** Hides a broken candidate as “eventual pass.”

## Validation and reversal

`internal/publish.Evaluate` encodes the fail-closed gate. Reverse by
superseding this ADR before any publisher implementation. Proposed
status means dependents that require an **accepted** design stay
blocked until a later merge updates this header.

## Sources

- [#96](https://github.com/Sannrox/rusui/issues/96)
- [ARCHITECTURE.md](../../ARCHITECTURE.md) Implement (v3 only)
- [ADR 0009](0009-credential-broker.md)
