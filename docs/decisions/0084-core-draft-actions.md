# ADR 0084: Definition drafts are operator-applied core Actions

- Status: Accepted
- Date: 2026-10-24
- Amends: [ADR 0080](0080-turns-propose-writes-go-through-actions.md) (GitHub-only
  proposals and the ban on further Action types),
  [ADR 0009](0009-credential-broker.md) (plane-held secrets; the guest
  still holds only the per-turn grant).
- Resolves: [#635](https://github.com/Sannrox/rusui/issues/635).
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md),
  [ADR 0044](0044-plane-publishes-from-turn-result.md).
- Discussion: none. Merging with this status is the acceptance act.
  Until merge this is a proposal, not implementation authority.

## Context

A domain interview agent drafts ontology members through scoped draft
authoring. ADR 0080 D1–D4 make GitHub the only write target (`write.propose`
/ `write.apply`) and forbid further Action types. Rusui has no core
client. A core PermissionDenied has no session-visible mapping. Putting
core MCP tools in the guest would hand a write credential to the guest
(ADR 0009) and let a turn write without a governed Action (ADR 0080).

## Decision

1. A turn that asks to draft records `core.draft` and outcome `proposed`.
   Complete does not call the core. The ask travels in the turn result
   (`draft`: namespace, branch id, parent revision digest, members).
   Every member's `definition_json` carries `source_turn`. Agent members
   are refused. The guest never receives a core credential.
2. Applying the proposal is `POST /sessions/{id}/draft-actions` (operator).
   The plane-held core client creates the branch, applies the edits, lints
   the branch (`GetOntologyLintReport` with `branch_id`), and opens a
   definition proposal. It does not approve, merge, or publish. The
   receipt is `core.draft.apply`. Apply pins the expected branch head in a
   durable apply record before the edit so a retried apply cannot adopt
   another writer's head. A retried apply of the same proposal returns the
   stored receipt.
3. One new Action pair on the existing `actions` table: `core.draft` and
   `core.draft.apply`. No schema migration. Guest ingest of both types is
   refused. Review apply stays dry-run and does not auto-apply drafts.
4. A core PermissionDenied is recorded on the session as the apply
   receipt (`refused`, `code: permission_denied`) and is visible on
   `GET /sessions/{id}`. It is not a GitHub `blocked_reason` and not an
   exception log alone.

On acceptance, this decision amends ADR 0080 D1, D2, and D4: turns may
propose a core definition draft in addition to a GitHub write, and
`core.draft` / `core.draft.apply` are admitted Action types. ADR 0080's
GitHub path, guest-credential ban, and "turns only propose" rule stand.
ADR 0009 stands: the core URL and bearer stay on the plane.

## Consequences

Easier: interview turns reuse the propose/apply split; tests can fake the
core; a missing draft grant is a session record.

Harder: the plane now holds a core credential and must not inject it into
the guest or the guest link. Draft apply is a second operator write
surface beside `write-actions`.

Irreversible: none. Unset `RUSUI_CORE_URL` to refuse apply. Historical
`core.draft` rows stay.

## Rejected alternatives

- **Core MCP server in the guest (option 2).** Conflicts with ADR 0009
  (guest-held write credential) and ADR 0080 (a turn that writes).
- **Plane writes on Complete.** Skips the governed Action; ADR 0080
  already rejected that for GitHub.
- **Reuse `write.propose` for definition members.** The GitHub body is a
  branch and SHA; a draft is a namespace and members. Mixing them hides
  the target.
- **Vendored gRPC proto in rusui.** The HTTP/JSON projection is the
  supported JSON path; the engine talks to a small client interface.

## Validation and reversal

Tests: a scripted interview turn records `core.draft` with types, links,
and `source_turn`; apply calls create, edit, lint, and proposal and never
merge or publish; a PermissionDenied is stored on the session; guest
ingest of `core.draft` is refused. Reverse by ignoring `draft` in the
result and leaving `RUSUI_CORE_URL` unset.

## Sources

- [#635](https://github.com/Sannrox/rusui/issues/635)
- [ADR 0080](0080-turns-propose-writes-go-through-actions.md)
- [ADR 0009](0009-credential-broker.md)
- Source baseline: `181295318dd7f99c117e13518c2f01c6eb708f1c`
