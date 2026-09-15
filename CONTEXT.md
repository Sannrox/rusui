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
