# Rusui

Self-hosted environment plane. Glossary for nouns in the current contract.

## Language

**Project**:
An operator-named policy and budget domain. A session belongs to exactly one project; an environment belongs to exactly one project; a GitHub repository binds to at most one project.
_Avoid_: Repository, environment, session, GitHub Project, workspace, tenant

**Bound repository**:
A GitHub `owner/name` listed under a project. Review requires at least one. GitHub is optional on the project itself. `visibility` (`public` or `private`) is a policy attribute of that bound repository, not of this source repo.
_Avoid_: Using the repository full name as the policy identity

**Session kind**:
One of `review`, `run`, or `scheduled`. `run` is operator-started (`rusui run`).
_Avoid_: operator, interactive, chat, implement (as a session kind)

**Session**:
A unit of work on one project and one environment. A review session is identified by `(project, bound repo, item)`. A `run` or `scheduled` session is minted at create.
_Avoid_: GitHub issue, turn, environment

**Turn**:
One leased attempt on a session.
_Avoid_: session, job (as the product noun)

**Environment**:
The machine a session runs on. One session, one environment.
_Avoid_: project, runner, snapshot (the prepared tree)

**Snapshot**:
The prepared, reusable tree identified by `source_hash` (base image digest, git pin, `.agents/setup` bytes). Two sessions may share a snapshot; they never share an environment.
_Avoid_: environment, GitHub item snapshot hash, image tag

**Schedule**:
A named trigger on a project with a UTC cadence and a prompt. Each fire may start one scheduled session.
_Avoid_: review fan-out, cron in policy.yaml

**Machine isolation**:
Rusui’s OS boundary for a session: a container in P1 dogfood. The process driver is a test/dev stand-in.
_Avoid_: tool fence, shikigami sandbox

**Tool fence**:
The guest's default permission mode plus policy-mapped `session/request_permission`. A live unmatched request waits on the RPC with a current-policy recheck; inbox history is not a grant.
_Avoid_: machine isolation, `--always-approve`, tool jail inside rusui

**Process** (proposed, [ADR 0016](docs/decisions/0016-local-interactive-runtime.md)):
A live local PTY child owned by Sumika, named `rusui-<project>-<session id>`. Sumika calls it a Session; rusui does not.
_Avoid_: session (for the PTY child), turn, environment

**Local session** (proposed, ADR 0016):
A session of kind `local`: a human drives one Process on their own machine. No turns, leases, grants, or GitHub credential.
_Avoid_: run session, implement session, attach

**Cancel**:
Operator action that stops a live guest and fails the claimed turn without automatic retry. Distinct from pause.
_Avoid_: pause, expire environment

**Concurrent-lease meter**:
Project `budgets.max_concurrent_leases` (default 1). Unnamed budget keys fail closed at parse.
_Avoid_: token or dollar limits (unavailable)

**Per-turn grant**:
The only credential in a P1 guest outside `implement` sessions (ADR 0015): D3’s 32-byte token, ten-minute TTL, hashed at rest, renewed on heartbeat. Git HTTP auth and `XAI_API_KEY` in the guest are this grant, not GitHub or xAI secrets.
_Avoid_: PAT, installation token, `auth.json`, xAI API key (in the guest)

**Plane proxy**:
Git smart-HTTP and model egress on the plane, which redeem a grant for the real token. GitHub REST stays plane-internal. The guest may reach only these endpoints under egress `trusted`. HTTPS to `rusui.plane`; plane CA at `/usr/local/share/ca-certificates/rusui-plane.crt`. Snapshot prepare uses a read-only grant on the same git proxy.
_Avoid_: runner-side proxy, `api.github.com` from the guest, PAT on the runner disk, HTTP to the proxies from a container guest

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
A `run` session started for an open pinned implementation task on a bound repository with `implement: true`. The only session that holds the operator's GitHub credential (ADR 0015).
_Avoid_: ordinary run session, scheduled session, review session as a publisher

**Publication**:
A pull request the agent opens or updates itself from an `implement` session, with the operator's GitHub credential via `git` and `gh` (ADR 0015). Commits carry `Co-authored-by: rusui` and `Rusui-Session` trailers. No proof gates it. Human merge is required.
_Avoid_: plane-owned publish step, agent merge or close, trailer as authorization
