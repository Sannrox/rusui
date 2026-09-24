# ADR 0009: P1 credential broker

- Status: Accepted; amended by [ADR 0015](0015-agent-publication.md)
  (`implement` sessions receive the operator GitHub credential and
  push directly; the model proxy and read-only grants stand)
- Date: 2026-09-15
- Amends: [ADR 0001](0001-environment-plane.md) D5 (how the plane
  redeems secrets). D3’s per-turn credential (32 bytes, ten-minute
  TTL, one environment, hashed at rest) stands.
- Resolves: [#44](https://github.com/Sannrox/rusui/issues/44)
- Proposed amendment: [ADR 0020](0020-turn-scoped-github-publication.md)
  evaluates implement-session Git access. This accepted decision remains in
  force until that proposal is accepted.
- Discussion: none. Merging with this status is the acceptance act.

## Context

D5 forbids writing a secret into an environment. Grok still emits a
local credential on every model call
([#39](https://github.com/Sannrox/rusui/issues/39)). A git smart-HTTP
proxy can enforce repo set and `refs/heads/rusui/<session>/*`
([#38](https://github.com/Sannrox/rusui/issues/38)). Installation
tokens cover today’s PAT on the dogfood repos and cannot pin a branch
prefix ([#40](https://github.com/Sannrox/rusui/issues/40)).

## Decision

**Proxies live on the plane**, not the runner: git smart-HTTP
(guest-facing), GitHub REST (plane-internal only), model egress
(guest-facing). The GitHub App private key and the xAI API key never
leave the plane host. Missing either at session start is fail closed.

The guest holds **only the per-turn grant** (D3). Isolated `GROK_HOME`
(no operator `auth.json`). `XAI_API_KEY` and git HTTP auth **are that
grant**, not the xAI or GitHub secret. Grok uses the API-key path
pointed at the plane (`GROK_XAI_API_BASE_URL`); the model proxy swaps
`Authorization`. Heartbeat **renews** the grant. One grant scopes git
(bound repos + session ref prefix), and model egress. It is not an
installation token.

**No GitHub REST token in the guest.** `api.github.com` is not on the
guest allowlist. Intake and dry-run apply use installation tokens on
the plane.

Git remotes in prepare and session containers are rewritten to the
plane smart-HTTP proxy. Auth is the current grant as HTTP extra header
/ `GIT_ASKPASS`. The proxy fail-closes on parse error and refuses any
receive-pack command outside the session ref prefix. Snapshot prepare
uses a **read-only prepare grant** (clone/fetch of the pin’s repo,
no push), same proxy, not a PAT on the runner disk.

Dogfood **egress class `trusted`:** the guest may reach only the plane
proxy endpoints. The plane may call `api.x.ai` and GitHub. `none`
cannot complete a model turn. `full` is not required.

Guest trust: **HTTPS to the plane**, plane CA in the guest image (part
of base-image identity / `source_hash`). No HTTP to the proxies.

The runner is the ACP host and **execs Grok inside the container** so
guest egress and the dummy grant apply. Bind-mount plus Grok on the
runner host is rejected: it bypasses container egress.

## Consequences

- `ARCHITECTURE.md` non-goal “GitHub App fleet” is stale: one App for
  the operator’s dogfood repos is in scope; a multi-tenant App
  marketplace is not.
- Provisioning that App is ordinary ops, not a wayfinder ticket.
- Process-driver “mint a read-only GitHub token into the job” is not
  the P1 container path.

## Rejected alternatives

- **Proxies on the runner.** Runner would hold the App key and xAI key.
- **Copy `~/.grok/auth.json` into the container.** Long-lived OAuth in
  the environment.
- **Real xAI key in the guest.** Fails D5 on the model path.
- **Fine-grained PAT instead of an App.** Cannot mint 1-hour
  downscoped tokens; cannot implement D5.
- **GitHub REST in the guest.** Extra secret surface; apply stays on
  the plane.
- **Grok on the runner with a bind-mount.** Bypasses machine isolation
  egress.

## Validation and reversal

Accept on merge. Reopen if Grok cannot be pointed at
`GROK_XAI_API_BASE_URL` with a dummy `XAI_API_KEY`, or if a P1 turn
must call GitHub REST from inside the guest.

## Sources

- [#44](https://github.com/Sannrox/rusui/issues/44)
- [#38](https://github.com/Sannrox/rusui/issues/38),
  [#39](https://github.com/Sannrox/rusui/issues/39),
  [#40](https://github.com/Sannrox/rusui/issues/40)
- [ADR 0001](0001-environment-plane.md) D3, D5
- [ADR 0007](0007-environment-snapshot.md) runner-local snapshot prepare
- [ADR 0008](0008-p1-isolation-split.md)
