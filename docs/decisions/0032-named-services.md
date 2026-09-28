# ADR 0032: Named services declare a port and a health path

- Status: Accepted
- Date: 2026-09-28
- Amends: [ADR 0004](0004-rusui-services-yaml.md) (the services.yaml
  schema) and [ADR 0012](0012-operator-access.md) D4 (a preview grant
  names a service).
- Resolves: [#329](https://github.com/Sannrox/rusui/issues/329)
- Related: [#320](https://github.com/Sannrox/rusui/issues/320) (the
  preview proxy reaches the guest), [#334](https://github.com/Sannrox/rusui/issues/334)
  (service output is stored by service name),
  [#330](https://github.com/Sannrox/rusui/issues/330),
  [#331](https://github.com/Sannrox/rusui/issues/331).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

`.rusui/services.yaml` names a service and gives it a `command`, `cwd`,
and `env` (ADR 0004). The plane starts those commands on provision and
wake. Nothing says which port a service listens on or whether it is up.
The operator mints a preview by typing a raw port (ADR 0012 D4). The
session shows no list of services and no health.

The parser ignores unknown keys today. A misspelled field is silently
dropped.

Since #320, a preview grant reaches the port inside the session's
container. All grants are served from one preview origin. The services
of one session run in one container.

The first dogfood app is one HTTP server. No known workload needs two
services to address each other through a public URL.

## Decision

**D1. Two new fields, and unknown fields fail closed.** A service may
declare `port` and `health`. Nothing else is added.

```yaml
services:
  web:
    command: pnpm dev --port "$PORT"
    cwd: app
    port: 3000
    health: /healthz
    env:
      NODE_ENV: development
  worker:
    command: pnpm worker
```

- `port` is an integer from 1024 to 65535. It is the port the service
  listens on inside the environment. Two services may not declare the
  same port. A service without `port` is not previewable.
- `health` is an absolute HTTP path. It requires `port`. The plane
  sends `GET` to `127.0.0.1:<port><health>` inside the environment. A
  2xx or 3xx answer is healthy. A service with `port` and no `health`
  is healthy when the port accepts a TCP connection.
- Any other key, at the file or service level, is a parse error. A
  parse error fails the start, as an invalid file does today.

**D2. The plane injects the port and identity, not a public origin.**
Each service command gets `PORT` (its declared port, when set),
`RUSUI_SESSION_ID`, and `RUSUI_SERVICE`. The plane does not allocate
ports. The file is the only source of the port.

The plane does not inject a public origin. The preview origin is the
plane's deployment setting, not the app's configuration. The app uses
relative URLs. Services in one environment reach each other on
`127.0.0.1:<port>`. A later ADR may inject an origin when a workload
needs one.

**D3. Lifecycle.**

| Event | Services |
| --- | --- |
| Provision | setup (once per source hash), then start each service in name order |
| Wake | resume, then start each service in name order |
| Sleep | stop all services, then stop the environment |
| Replace or expire | stop all services, then destroy |
| Preview mint | ensure: probe health once |

- Start launches the command under the plane's supervisor and does not
  wait for it to exit. A command that cannot be launched fails the
  provision or wake, as today.
- After launch, the plane probes health until the service is healthy
  or a fixed plane deadline passes. A service that misses the deadline
  is `unhealthy`. It does not fail the wake. The environment is still
  usable, and its output is in `rusui envlog`.
- Ensure does not restart a service. A restart policy is out of scope.
- `.agents/setup` prepares the tree. It must not start a server. A
  process that setup leaves running is not supervised, not stopped on
  sleep, and not previewable by name.

**D4. The session lists its services.** Session detail, `rusui read`,
and the console show each declared service with its name, port, and
state: `starting`, `healthy`, `unhealthy`, or `exited`. A missing file
is an empty list. An invalid file shows the parse error, and no service
starts. The last output is the service capture from #334.

**D5. A preview grant names a service.** The operator mints a preview
for a service name. The plane reads the port from the declared file at
mint and stores the service name and the port in the grant. It refuses
a name that is not declared, has no `port`, or is not healthy at mint.
The proxy keeps dialing only the port the grant stores (ADR 0012).

The raw-port mint stays, for the operator only. The grant records no
service name, and the console labels it undeclared. The grant class,
TTL, secret, origin, and revocation of ADR 0012 do not change. The
guest cannot mint any grant.

## Threat examples

| Attempt | Result |
| --- | --- |
| A service binds a different port than it declares | Health on the declared port fails. The service is `unhealthy`, and a named mint is refused. The undeclared port is not reachable by name. |
| Preview of an undeclared port | A named mint cannot express it. A raw-port mint is operator-only and labeled undeclared. The guest cannot mint. |
| The public origin is stored as if it were a secret | The origin is not injected and carries no authority. Access needs a grant. A grant is short-lived, revocable, and invalid after replace or an operator generation bump. A grant leaked in a transcript or PR expires. |
| A long-running server is started from `.agents/setup` | Setup runs once per source hash, not on wake. The process is not supervised, not stopped on sleep, and not previewable by name. Its output is the setup capture in `rusui envlog`. |
| A misspelled field, such as `prot: 3000` | Parse error. Nothing starts. The session shows the error. |
| `health: http://other-host/` | Parse error. `health` is a path on the declared port, never a URL. |
| Preview HTML calls `/sessions/*` with operator credentials | Unchanged from ADR 0012 D5: a different origin, and the cookie is not sent. |

## Consequences

- Easier: the operator picks a service by name. The session shows
  whether it is up. #330 can wake and ensure named services. #331 can
  address a comment to a named preview.
- Harder: `services.yaml` parsing becomes strict. A file that relied on
  an ignored key now fails to start until the key is removed.
- Store: a preview grant gains a nullable service name. Service state is
  per environment and is replaced on each start.
- Policy, GitHub, Slack, and egress: no change. Overlay still cannot add
  egress.
- The services of one session share the preview origin. They are one
  trust domain, because they share one container.

This ADR authorizes implementation of D1 to D5 as later work. It does
not implement them.

## Rejected alternatives

- **The plane allocates ports and injects them.** This adds a second
  source of truth, and preview links would change on every wake. One
  container has no port conflict that the file cannot avoid.
- **Inject a public origin now.** No first-slice workload needs it. An
  injected origin would be baked into builds and stored config.
- **Remove the raw-port mint.** The operator still debugs undeclared
  ports. ADR 0012 keeps them operator-only.
- **An unhealthy service fails the wake.** This would hide the
  environment the operator needs in order to fix it.
- **A second services file or a plugin runtime.** Non-goals of #329.
  ADR 0004 keeps one file.

## Validation and reversal

Validation: this ADR is Accepted in the index. ADR 0004 and ADR 0012
link forward. The example above uses only D1 fields. Implementation
proves that an unknown key fails parsing, that a named mint of an
unhealthy or undeclared service is refused, and that the session lists
service states. Reverse by a superseding ADR. A reversal that removes
the fields restores the ADR 0004 schema.

## Sources

- [#329](https://github.com/Sannrox/rusui/issues/329)
- [ADR 0004](0004-rusui-services-yaml.md), [ADR 0012](0012-operator-access.md)
- `internal/env/services.go` (`ParseServices`), `internal/server/preview.go`
  (`consoleMintPreview`, `previewProxy`)
- Decided against `main` at `ef0aec4`
