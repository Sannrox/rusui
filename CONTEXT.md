# Rusui

Self-hosted environment plane. Glossary for nouns in the current contract.

## Language

**Project**:
An operator-named policy and budget domain. A session belongs to exactly one project; an environment belongs to exactly one project; a GitHub repository binds to at most one project.
_Avoid_: Repository, environment, session, GitHub Project, workspace, tenant

**Bound repository**:
A GitHub `owner/name` listed under a project. Review requires at least one. GitHub is optional on the project itself.
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
Grok `--permission-mode default` plus policy-mapped `session/request_permission`. Unmatched requests are denied and parked.
_Avoid_: machine isolation, `--always-approve`, tool jail inside rusui
