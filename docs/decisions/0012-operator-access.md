# ADR 0012: Single-operator access for terminal and preview

- Status: Accepted
- Date: 2026-09-20
- Amends: [ADR 0003](0003-operator-surface.md) reachability (credential
  classes, browser session, preview origin). The API-first, CLI-first,
  Go-template/htmx console, and Slack-as-notify/approve decisions stand.
- Resolves: [#111](https://github.com/Sannrox/rusui/issues/111)
- Related: [ADR 0009](0009-credential-broker.md) (guest grants),
  [ADR 0010](0010-hybrid-roadmap-sequence.md) (M4 surfaces),
  [ADR 0011](0011-unattended-session-contract.md) (cancel vs pause).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.
  Objects: operator credential, worker credential, turn grant, browser
  session, terminal write lease, preview origin.
  Evidence: worker bearer is not an operator browser session; preview
  hosts fail closed when they share the plane origin; credential
  rotation revokes terminal write and preview grants.
  Permitted action: one operator may read transcripts, hold one terminal
  write lease, and open a preview on a **different origin**.
  Policy: fail closed — worker/turn secrets cannot mint browser
  sessions; public bind still requires operator TLS; no team roles.

## Context

ADR 0003 chose HTTP+SSE, a bearer token, an embedded console, and
"identity-aware proxy or tailnet in front of the same token check."
Existing session and approval routes authenticate the **worker** secret
or a **turn grant**. Enabling a browser console, terminal, or preview
on that check would mix three jobs:

- the runner bootstrap secret (`RUSUI_WORKER_SECRET`);
- the per-turn guest grant;
- the human operator who signs in, watches, types, and opens a port.

A single maintainer does not need OIDC or RBAC. They do need revocation
when a token leaks, a distinct origin for untrusted application HTML,
and a rule for who types in the terminal after a second tab opens.

## Decision

### D1. Three credential classes

| Class | Secret | Audience | May |
| --- | --- | --- | --- |
| **Operator** | `RUSUI_OPERATOR_TOKEN` (separate from worker) | Human CLI and console | List/attach sessions, approve, drain, hold terminal write, mint preview grants |
| **Worker** | `RUSUI_WORKER_SECRET` | `rusui-runner` and scripts | Hello, claim, heartbeat, complete/fail, drain |
| **Turn** | per-turn grant | Guest / ACP host | Proxies, turn actions, approval poll for that turn |

Presenting a worker or turn secret as a browser session **fails closed**.
CLI may keep using the worker secret for today's runner-shaped commands
(`run`, `sessions`, `drain`). Console sign-in and future `rusui login`
use the operator token. Implementation must not accept the worker secret
on cookie-authenticated HTML POST.

Until `RUSUI_OPERATOR_TOKEN` is set, console HTML routes stay disabled.
The current worker-authenticated JSON API is unchanged.

### D2. Browser session

- Sign-in: operator token over HTTPS (loopback HTTP is allowed only on
  `127.0.0.1` / `::1`).
- Session: `HttpOnly`, `SameSite=Strict`, `Secure` when TLS is on,
  path scoped to the plane origin. Alternative: `Authorization: Bearer`
  for CLI/API; not both on a preview origin.
- Logout and operator-token rotation delete the server-side session row
  (or bump a generation). Active terminal write leases and preview
  grants for that operator generation become invalid.
- Cookie-authenticated mutations require a CSRF token bound to the
  session. Bearer API calls do not use cookies and do not need CSRF.
- No OIDC, no multi-user roles, no refresh-token family.

### D3. Transport and deployment

Supported first topology:

1. Plane binds loopback (default).
2. Operator reaches it through a **tailnet or identity-aware proxy**
   that terminates TLS and forwards to loopback, **or** through SSH
   local-forward. Direct `-addr 0.0.0.0` without plane TLS remains
   fail-closed for operator HTML.

Webhook tunnels (smee, cloudflared) stay operator infrastructure for
GitHub/Slack **hooks only**. They are not a substitute for operator
authentication on console, terminal, or preview.

### D4. Who can read, type, and preview

Single operator. No team roles.

| Action | Who | Concurrent | Ends |
| --- | --- | --- | --- |
| Read transcript, files, receipts | Operator session | Many readers | Session cancel, operator generation bump, idle environment expiry |
| Terminal write | At most **one** write lease per environment | Second writer is refused until the lease is released or stolen by the same operator after explicit revoke | Cancel, environment destroy/replace, operator generation bump, lease TTL |
| Terminal observe | Operator session without the write lease | Many | Same as read |
| Open preview | Operator session mints a **preview grant** (separate secret, short TTL) | Many grants | Grant TTL, revoke, environment replace, operator generation bump |

The guest ACP host may use `terminal/*` inside the container as today;
that is not a browser terminal. Browser terminal control is this lease.

Environment replace (new git pin) invalidates the previous handle's
terminal lease and preview grants. Dirt is gone with the container
([ADR 0011](0011-unattended-session-contract.md)).

### D5. Preview origin isolation

Untrusted application content (a `services.yaml` port, a guest HTTP
server) **must not** share the plane origin.

- Preview URL host is not the plane host. First supported shape:
  `http(s)://127.0.0.1:<guest-port>` via the tailnet/SSH forward, or a
  dedicated preview hostname that is not a parent/child of the plane
  hostname.
- Plane cookies have `Domain` of the plane host only; they are not
  sent to the preview host.
- Preview grant is presented to a **preview proxy** as a query or
  header, never as the operator cookie.
- Preview HTML cannot call `/jobs/*`, `/sessions/*`, or `/approvals/*`
  using operator credentials. CORS default deny.
- No `allow-always` for preview grants.

### D6. Threat examples

| Attempt | Result |
| --- | --- |
| Guest or leaked turn grant presented to console sign-in | Rejected (wrong class) |
| Worker secret used as cookie session | Rejected |
| CSRF POST to `/approvals/{id}` with operator cookie, no CSRF token | Rejected |
| Second browser tab takes terminal write while first holds the lease | Rejected until revoke |
| Preview page on plane origin | Rejected (not isolated) |
| Preview page `fetch()` to plane with operator cookie | Cookie not sent (SameSite + different origin) |
| Operator token rotated | Outstanding cookies, terminal leases, preview grants invalid |
| Public bind without TLS for HTML | Fail closed |
| Inbox `allow` treated as terminal/preview grant | Rejected ([ADR 0011](0011-unattended-session-contract.md) allow is a record) |

## Consequences

Easier: M4 console, terminal, and portals have one access model before
any HTML is served. Worker and guest secrets stay out of the browser.

Harder: a new operator secret must be provisioned before console HTML
ships. Preview needs a second origin, not a path under `/sessions/{id}`.

This ADR does not implement the console, terminal, or preview proxy.
Dependent issues implement against these objects.

## Rejected alternatives

- **Worker secret as the browser token.** Mixes runner bootstrap with
  human session; rotation would kill the runner.
- **Turn grant as console auth.** Grants expire in ten minutes and are
  scoped to one turn.
- **Same-origin preview under `/preview/`.** Untrusted JS inherits the
  plane origin and cookies.
- **OIDC / multi-user RBAC now.** Out of #111 non-goals; later M5.
- **Public bind with loopback-as-security leftover.** ARCHITECTURE
  already says loopback is not a jail.

## Validation and reversal

Accept on merge. Reverse by superseding this ADR before console HTML
ships. No schema change is required to accept; implementation may add
an operator-session table later.

## Sources

- [#111](https://github.com/Sannrox/rusui/issues/111)
- [ADR 0003](0003-operator-surface.md), [ADR 0009](0009-credential-broker.md),
  [ADR 0011](0011-unattended-session-contract.md)
- Current worker/turn checks in `internal/server/server.go`
