# ADR 0020: Turn-scoped GitHub publication stays behind the plane

- Status: Proposed
- Date: 2026-09-24
- Resolves: [Issue #243](https://github.com/Sannrox/rusui/issues/243)
- Amends (proposed): [ADR 0015](0015-agent-publication.md) D1 and D4;
  [ADR 0009](0009-credential-broker.md) for implement-session Git access.
- Related: [ADR 0001](0001-environment-plane.md) D3 and D5,
  [ADR 0017](0017-claude-guest-and-model-upstream.md) D3 and D4,
  [ARCHITECTURE.md](../../ARCHITECTURE.md), [VISION.md](../../VISION.md).
- Discussion: none. GitHub Discussions are disabled. This PR is the review
  venue. This proposed ADR does not change the product contract until
  accepted.

## Context

ADR 0001 D5 says the GitHub App credential stays on the plane and a managed
guest receives only a turn-scoped grant redeemed through the plane's proxies.
ADR 0009 defines that grant as a ten-minute credential bound to one
environment, renewed by heartbeat, and hashed at rest. ADR 0015 later made an
exception for `implement` sessions: the guest receives the operator's
fine-grained PAT and talks directly to GitHub. The PAT can remain usable after
the turn ends, and GitHub's permission model does not constrain it to the
session branch or to PR creation.

The security and identity questions are separate. The GitHub credential
determines who pushed and created a PR. The commit's configured author and
committer emails determine which account GitHub associates with the commit.
Changing the credential can therefore change the PR actor without requiring
the commit metadata to change.

## Decision

**Proposal: give eligible `implement` turns a renewable, turn-scoped grant to
use plane-owned GitHub publication proxies. Never give a managed guest a
GitHub write token.** The grant is a Rusui capability, not a GitHub token. It
authorizes only the current turn, its bound repository, and the publication
operations below.

- Git pushes go through the plane's git smart-HTTP proxy. The proxy checks the
  current lease generation and policy revision, restricts writes to
  `refs/heads/rusui/<session>/*`, and rejects other repositories and refs.
- The agent asks the plane to create or update its PR. The plane uses a
  GitHub App installation token scoped to that one repository with
  `Contents: write` and `Pull requests: write`. The publication endpoint
  accepts only create/update requests for the session's pushed branch. It is
  not a general GitHub REST proxy.
- The App private key and installation token stay on the plane. The guest
  receives neither `RUSUI_AGENT_GITHUB_TOKEN` nor a GitHub API credential or
  direct GitHub write egress. Review and ordinary run sessions receive no
  write grant.
- A grant expires after ten minutes and heartbeat may renew it only for the
  current live turn and generation. The plane refreshes the installation
  token server-side before its one-hour expiry. Ending, revoking, or losing
  the lease stops renewals; the proxy rejects later requests. Already
  completed GitHub writes are not undone.
- If the initial installation-token mint fails, the `implement` turn fails
  closed before the guest starts. If renewal fails during a turn, the plane
  refuses further GitHub writes, records the failure, and does not fall back
  to a PAT or direct GitHub access. The workspace remains available for
  operator recovery.
- The agent still decides when its work is ready to publish; the plane
  executes that bounded request. The GitHub pusher and PR author are the
  Rusui App. The operator's configured commit author/committer name and
  verified email may remain on commits, and the session trailers remain, but
  these are attribution metadata, not proof that the operator pushed or
  created the PR. The human remains responsible for review and merge.

If accepted, this amends ADR 0015 D1's direct `gh pr create` / `gh pr edit`
path and replaces D4's guest-held operator credential. D2's lack of a proof
gate, D3's session attribution, and D5's prohibition on agent merge, close,
land, label, and release actions remain. The existing solo profile in ADR
0017 D4 remains the minimum: PRs, required checks, conversation resolution,
and no force pushes or deletion on the default branch. An App-authored PR
lets the solo operator be a distinct human reviewer if the repository also
requires approval; this proposal does not change that profile or grant the App
a branch-protection bypass.

### Compared routes

| Route | Guest authority | Expiry and revocation | GitHub actor and branch-protection effect |
| --- | --- | --- | --- |
| Operator fine-grained PAT in the guest | Selected repositories and permissions, but no session-ref scope; the guest can call any GitHub operation allowed by the token. | Owner revocation is manual. GitHub's exposed-token API accepts a revocation request with `202 Accepted`; GitHub documents no completion-time guarantee. The configured expiry can be up to one year or absent, subject to owner policy. | Git push and PR creation are authenticated as the operator; the displayed PR creator follows from the authenticated user (**inference**). The operator is also the last pusher, so a solo repository cannot use a separate-human approval as a gate. |
| Per-turn GitHub App installation token in the guest | Can be narrowed at mint time to one installed repository and a subset of App permissions, but has no session-ref or endpoint restriction. The GitHub token itself reaches the guest. | Expires after one hour. The guest can call GitHub's revocation endpoint for that token; GitHub says a revoked token is invalidated and returns `204`, but publishes no timing SLA. Longer turns require minting and delivering a replacement. | Git operations and PR creation are authenticated as the App. GitHub identifies installation tokens as the App bot; the displayed PR creator follows from the PR API request (**inference**). Repository branch rules still apply, but the guest can call every GitHub endpoint allowed by the token until it expires or is revoked. |
| Turn grant to plane-owned git and PR proxies (proposed) | The guest can push only through Rusui's session-ref check and request only PR create/update. The App token stays on the plane. | The plane rejects subsequent proxy requests after grant expiry or revocation; heartbeat renewal is fenced by the current turn. The plane rotates its one-hour App token as needed. An in-flight or completed GitHub write cannot be recalled. | GitHub identifies the installation token as the App bot; the displayed pusher and PR creator follow from those authenticated requests (**inference**). The operator can be a separate human reviewer. The App receives no bypass role; GitHub's PR, check, and branch rules still apply. |

Fine-grained PATs are the simplest route and preserve operator-authored PRs,
but their lifetime is independent of a Rusui turn. An App token delivered to
the guest has a shorter maximum lifetime and can be downscoped, but it remains
a reusable GitHub credential during that hour and bypasses Rusui's ref and
operation checks. The proxy route adds server-side publication plumbing and
changes PR authorship to the App; it is the only compared route that keeps
GitHub authority out of the managed guest and lets Rusui enforce turn,
repository, ref, and operation boundaries together.

## Consequences

- Operators must install and configure the Rusui GitHub App for repositories
  that enable `implement`. The App permissions must remain limited to contents
  and pull requests; the plane narrows each installation token to the one
  repository used by the turn.
- Rusui must provide a bounded PR create/update operation alongside git proxy
  access. The guest cannot use `gh pr create` directly against GitHub.
- GitHub displays the App as pusher and PR author. A configured operator
  commit email may still associate commits with the operator, but does not
  authenticate that operator as the publisher.
- The solo operator can review an App-authored PR as a different actor from
  its last pusher. This supports, but does not require, a one-human approval
  rule. GitHub plan and repository settings still determine which branch
  restrictions are available; never add the App to a bypass list to make the
  path work.
- The plane becomes the security boundary for this narrow publication API.
  Its proxy must authorize every request against the live turn, policy,
  repository, ref, and operation, and must record a receipt. The App token
  still has GitHub-granted permissions while it is on the plane.
- This ADR records the target only. Before implementation, update the product
  contract, VISION identity wording, and operator docs; migrate the runner off
  direct `GH_TOKEN`; and prove the supported App identity and branch rules in
  the pilot profile. Until acceptance and that implementation lands, the
  current contract and code still use ADR 0015's direct operator credential.

## Rejected alternatives

- **Keep a fine-grained operator PAT in implement guests.** It preserves the
  operator as PR author and needs less plane plumbing, but a leaked token can
  outlive the turn and is not limited to a session ref or publication
  endpoints. It conflicts with the managed-guest credential boundary in ADR
  0001 D5 and the repository instruction that model CLIs never receive GitHub
  write tokens.
- **Give the guest a one-hour App installation token.** It improves expiry and
  repository/permission scoping over a PAT, but still puts a GitHub write
  credential in the managed environment and does not enforce Rusui's
  session-ref or operation restrictions.
- **Require a second GitHub account for the agent.** It changes the same
  authorship property without enforcing turn or ref scope, and leaves a
  reusable credential in the guest.

## Validation and reversal

Before enabling this route, a controlled pilot must demonstrate one
implement turn that pushes only a session ref and creates or updates one PR as
the App; prove a non-implement session, expired grant, stale lease generation,
other repository, other ref, and merge/close request are refused; and confirm
the GitHub installation token never appears in guest environment, logs, or
diagnostics. Record the PR actor, commit author/committer association, and
which required-review rules the solo operator can satisfy. Do not claim a
branch-protection guarantee based only on mocked tests.

Reverse by superseding this ADR, disabling the proxy publication grant, and
choosing a new credential and authorship contract. Do not silently restore a
guest-held PAT fallback.

## Sources

- [Issue #243](https://github.com/Sannrox/rusui/issues/243)
- [ADR 0001](0001-environment-plane.md) D3 and D5;
  [ADR 0009](0009-credential-broker.md);
  [ADR 0015](0015-agent-publication.md);
  [ADR 0017](0017-claude-guest-and-model-upstream.md) D3–D5
- GitHub Docs: [fine-grained PATs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens),
  [credential lifetimes and revocation](https://docs.github.com/en/organizations/managing-programmatic-access-to-your-organization/github-credential-types),
  [PAT revocation endpoint](https://docs.github.com/en/rest/credentials/revoke)
- GitHub Docs: [scoping installation tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app),
  [choosing GitHub App permissions](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app),
  [installation-token revocation](https://docs.github.com/en/rest/apps/installations) (the token is invalidated; the API returns `204`),
  [GitHub Apps and OAuth actor identity](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps)
- GitHub Docs: [create a pull request](https://docs.github.com/en/rest/pulls/pulls),
  [protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches),
  [review a pull request](https://docs.github.com/en/pull-requests/how-tos/review-pull-requests/reviewing-proposed-changes-in-a-pull-request),
  [commit email association](https://docs.github.com/en/account-and-profile/how-tos/email-preferences/setting-your-commit-email-address)
- Research checked against GitHub Docs on 2026-09-24. Authenticated actor
  labels for installation-token-created PRs are inferred from the App-token
  identity and PR API behavior; confirm the exact UI presentation in the
  required controlled pilot before implementation is enabled.
