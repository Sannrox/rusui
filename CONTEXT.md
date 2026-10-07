# Rusui

Self-hosted environment plane. Glossary for nouns in the current contract.

## Language

**Project**:
An operator-named policy and budget domain. A session belongs to exactly one project; an environment belongs to exactly one project; a GitHub repository binds to at most one project.
_Avoid_: Repository, environment, session, GitHub Project, workspace, tenant

**Operator**:
The single human who holds `RUSUI_OPERATOR_TOKEN`. Plane authorization is that token, not a role table or external authority ([ADR 0012](docs/decisions/0012-operator-access.md), [ADR 0039](docs/decisions/0039-shared-operator-governance-deferred.md)). Slack allowlisted users may notify and approve; they are not plane operators.
_Avoid_: team, org, IdP user, second operator, Slack user as operator

**Bound repository**:
A GitHub `owner/name` listed under a project. Review requires at least one. GitHub is optional on the project itself. `visibility` (`public` or `private`) is a policy attribute of that bound repository, not of this source repo.
_Avoid_: Using the repository full name as the policy identity

**Session kind**:
The runtime accepts `review`, `run`, and `scheduled`. The experimental
`local` kind from [ADR 0016](docs/decisions/0016-local-interactive-runtime.md)
is default-off and requires a project `local_runtime` policy profile.
_Avoid_: operator, interactive, chat, implement (as a session kind)

**Session**:
A unit of work on one project and one environment. A review session is
identified by `(project, bound repo, item)`. A `run` or `scheduled` session
is minted at create. An experimental `local` session is human-driven and has
no Turn. An optional session mode (`low`, `medium`, `high`, `ultra`) is
forwarded on ACP `session/new` when set; omit keeps today's spawn. A parent
may start bounded children in the same project; each child is a new session
and a new environment from a snapshot
([ADR 0006](docs/decisions/0006-session-start.md), [ADR 0071](docs/decisions/0071-bounded-child-session.md)).
_Avoid_: GitHub issue, turn, environment

**Child session**:
A run session started by a parent in the same project. It has its own
environment and turn. It cannot add a project, repository, kind, or egress
class the parent lacks. Parent cancel cancels children. Child failure is a
recorded result on the parent ([ADR 0071](docs/decisions/0071-bounded-child-session.md)).
_Avoid_: live fork, shared writable workspace, cross-project child

**Turn**:
One leased attempt on an unattended session. A local session has no Turn.
_Avoid_: session, job (as the product noun)

**Prompt attachment**:
A file the operator attaches to a turn prompt. Bytes live on disk next to the plane store; SQLite keeps digest and path. The guest receives it as an ACP prompt part (flat `image`, nested `resource` for PDF). The plane admits only kinds the pinned HTTP guest advertises, at 8 MiB per part and 16 MiB aggregate. `rusui read` records the name and digest, not the bytes.
_Avoid_: workspace upload, git blob, image generation

**Environment**:
The machine a session runs on. One session, one Rusui Environment record. For
the experimental local profile, this identifies the operator's host and does
not imply isolation; multiple local sessions may share that physical host.
A managed session has no operator-chosen location; it runs on the plane host
([ADR 0034](docs/decisions/0034-operator-host-location-deferred.md)). It offers
no graphical desktop; the operator uses the terminal and HTTP preview
([ADR 0036](docs/decisions/0036-environment-desktop-deferred.md)).
_Avoid_: project, runner, snapshot (the prepared tree)

**Runner**:
One outbound-only `rusui-runner` on the same host as the plane. Schema seeds a
single `runners` row named `local`. Snapshot bytes stay on this host
([ADR 0029](docs/decisions/0029-single-host-runner.md)).
_Avoid_: fleet, Kubernetes, additional placement hosts

**Plane store**:
One SQLite file on the plane host, one writer. Recovery is copy and
`store.Restore`; live leases and grants do not survive. A redundant plane is
not selected ([ADR 0001](docs/decisions/0001-environment-plane.md) D8, [ADR 0041](docs/decisions/0041-ha-plane-deferred.md)).
_Avoid_: Postgres, leader lease, active-active, second writer

**Snapshot**:
The prepared, reusable tree identified by `source_hash` (base image digest, git pin, `.agents/setup` bytes). Two sessions may share a snapshot; they never share an environment. A second session starts from a snapshot, never from a live fork ([ADR 0035](docs/decisions/0035-live-environment-fork-deferred.md)). A snapshot hit skips plane pre-clone, pre-setup, and `.agents/setup`.
_Avoid_: environment, GitHub item snapshot hash, image tag

**Schedule**:
A named trigger on a project with a UTC cadence and a prompt. It may bind to one session; a fire then appends that prompt on the same session and environment, waiting if a turn is live ([ADR 0069](docs/decisions/0069-schedule-binds-a-session.md)). An unbound schedule still mints a new session per fire and skips while live ([ADR 0030](docs/decisions/0030-schedule-new-session.md)). Deleting the schedule stops later fires and does not cancel a running turn. The guest may request set, replace, or clear of its own session's bound schedule; the plane receipts the request. The guest still cannot write policy.yaml.
_Avoid_: review fan-out, cron in policy.yaml, steer of a live turn

**Session webhook**:
A plane-hosted signed POST URL owned by one session. A valid delivery is stored, wakes that environment, and queues a prompt containing the delivery id. An invalid signature is stored as a refusal and does not wake. Archive makes the URL miss. The guest does not listen on a public port ([ADR 0070](docs/decisions/0070-session-owned-webhook.md)).
_Avoid_: GitHub review webhook, guest bind, plugin runtime

**Machine isolation**:
Rusui’s OS boundary for an unattended session. The default for public-repository sessions is a container ([ADR 0022](docs/decisions/0022-public-repo-isolation.md)). The process driver is explicit test/dev opt-in; a missing runtime fails closed (`rusui diagnose` / `GET /readyz`). The supported isolation profile remains that container; a stronger runtime is not selected ([ADR 0028](docs/decisions/0028-container-isolation-profile.md)). Snapshot is the prepared tree, not a hypervisor memory image.
_Avoid_: tool fence, shikigami sandbox, Sumika local session, microVM

**Tool fence**:
The guest's default permission mode plus policy-mapped `session/request_permission`. A live unmatched request waits on the RPC with a current-policy recheck; inbox history is not a grant.
_Avoid_: machine isolation, `--always-approve`, tool jail inside rusui

**Process** ([ADR 0016](docs/decisions/0016-local-interactive-runtime.md)):
A live local PTY child owned by Sumika, named `rusui-<session id>`. Sumika calls
it a Session; Rusui does not. Rusui keeps one durable Process record per local
session generation, with the last observed state and revision. That record is
an observation, not authority that the child is currently live. Its internal
identity fingerprint pins the argv and cwd used for that generation across
policy reloads. Rusui never stores local PTY bytes.
_Avoid_: session (for the PTY child), turn, environment

**Local session** (experimental, ADR 0016):
A human-driven session of kind `local` associated with one Sumika Process on the
operator's host. It has no Turns, leases, managed grants, model proxy
credentials, or GitHub credential, and it is not an OS isolation boundary.
It is not a supported unattended product claim ([ADR 0045](docs/decisions/0045-local-unattended-promotion-deferred.md)).
_Avoid_: run session, implement session, attach

**Attach**:
A live client connection to a Process or managed terminal. Sumika owns local
Attach and enforces one writer by stealing the previous Attach; detach leaves
the Process running. Rusui keeps separate Attach generation observations with
stale-disconnect fencing. They are not client connections or Turn state.
A managed environment has one terminal session. Attach joins it. The guest
can read its output. A write still takes the exclusive lease
([ADR 0065](docs/decisions/0065-one-environment-terminal.md)).
_Avoid_: session, turn, process ownership

**Extension**:
Not a product object. Integrators use the object API, CLI, ACP, or Slack.
A plugin boundary is not selected ([ADR 0043](docs/decisions/0043-extension-contract-deferred.md)).
_Avoid_: plugin host, in-process loader, marketplace

**Cancel**:
Operator action that stops a live guest and fails the claimed turn without automatic retry. Distinct from pause.
_Avoid_: pause, expire environment

**Concurrent-lease meter**:
Project `budgets.max_concurrent_leases` (default 1). Unnamed budget keys fail closed at parse. A session past the cap waits (`wait: lease`); it does not 409.
_Avoid_: token or dollar limits (unavailable)

**Session size**:
Named container CPU and memory: `small` (1 CPU, 2 GiB), `medium` (2 CPU, 4 GiB), `large` (4 CPU, 8 GiB). Project `size` is the default; a run may override it.
_Avoid_: hypervisor, second host

**Per-turn grant**:
The only credential in a P1 guest outside `implement` sessions (ADR 0015): D3’s 32-byte token, ten-minute TTL, hashed at rest, renewed on heartbeat. Git HTTP auth and `XAI_API_KEY` in the guest are this grant, not GitHub or xAI secrets. The plane mints no third-party identity beside it ([ADR 0033](docs/decisions/0033-third-party-identity-deferred.md)).
_Avoid_: PAT, installation token, `auth.json`, xAI API key, registry or cloud key (in the guest)

**Wake secret**:
A project `secrets` allowlist of plane ids. At wake the plane injects values from `RUSUI_SECRET_<ID>` at `/run/rusui/secrets/<id>` and as uppercase env. On sleep they are gone. The receipt names the id, session, and turn, not the value ([ADR 0066](docs/decisions/0066-inject-secret-at-wake.md)).
_Avoid_: long-lived token in the image, OIDC (ADR 0033)

**Plane hook**:
A project script stored next to `policy.yaml` at `hooks/<slug>/pre-clone` and `hooks/<slug>/pre-setup`. Pre-clone runs before the git pin is placed. Pre-setup runs immediately before `.agents/setup`. A non-zero exit leaves no environment. Output is in `rusui envlog`. The hook environment does not receive the operator token or a provider key. Wake still runs `.agents/resume` only ([ADR 0067](docs/decisions/0067-plane-stored-pre-hooks.md)).
_Avoid_: services file from another product, operator token in hook env

**Plane proxy**:
Git smart-HTTP and model egress on the plane, which redeem a grant for the real token. GitHub REST stays plane-internal. The guest may reach only these endpoints under egress `trusted`. HTTPS to `rusui.plane`; plane CA at `/usr/local/share/ca-certificates/rusui-plane.crt`. Snapshot prepare uses a read-only grant on the same git proxy. A disconnected or local-inference session profile is not selected ([ADR 0042](docs/decisions/0042-disconnected-execution-deferred.md)).
_Avoid_: runner-side proxy, `api.github.com` from the guest, PAT on the runner disk, HTTP to the proxies from a container guest, offline session kind

**Source**:
The pinned intake identity `(repo, item, snapshot_hash, main_sha)` a candidate is proven against. Source drift invalidates proof.
_Avoid_: live GitHub fetch at publish time as the identity

**Task**:
A `run` session plus prompt hash that produced the candidate.
_Avoid_: review session, GitHub issue as the implementation identity

**Candidate**:
The git object `(commit_sha, tree_sha, ref)` on `refs/heads/rusui/<session>/*`.
_Avoid_: working tree, unverified branch tip, default branch

**Proof**:
Isolated verifier output `(command, exit_code, log_digest, head_sha, base_sha)` for one candidate SHA. Independent review is a different session judging that SHA. Agent self-declaration is not proof.
_Avoid_: CI green, author-session review, expired or drifted proof

**Implement session**:
A `run` session started for an open pinned implementation task on a bound repository with `implement: true`. The only session that holds the operator's GitHub credential (ADR 0015), or, with plane publication, the only session the plane publishes for (ADR 0044).
_Avoid_: ordinary run session, scheduled session, review session as a publisher

**Publication**:
A pull request for an `implement` session, or a fast-forward of the default branch when the project `ship` is `push-base`. Omitted or `pull-request` keeps today's path: the agent opens or updates a pull request with the operator's GitHub credential via `git` and `gh` (ADR 0015), or with `RUSUI_PUBLICATION=plane` the guest holds no credential and the plane opens or updates it as the GitHub App from the turn result's `publish` request (ADR 0044). `push-base` pushes only that default branch and does not open a pull request. Commits carry `Co-authored-by: rusui` and `Rusui-Session` trailers. No proof gates it. Human merge is required for `pull-request`. Live merge, comment, and close stay unauthorized.
_Avoid_: guest-callable publish endpoint, agent merge or close, trailer as authorization, defaulting dogfood to `push-base`
