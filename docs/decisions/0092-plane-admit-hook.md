# ADR 0092: Plane-stored admit hook

- Status: Accepted
- Date: 2026-11-17
- Amends: [ADR 0067](0067-plane-stored-pre-hooks.md). The plane may store a
  third hook per project, which the plane itself executes before a session
  starts.
- Resolves: [#693](https://github.com/Sannrox/rusui/issues/693)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (unchanged),
  `VISION.md` (optional governance plane)

## Context

A deployment that runs a governance service cannot require its decision
before agent work starts: a session is admitted by `policy.yaml` alone.
A built-in client would bind rusui to one governance protocol. Agent tools
commonly delegate such a decision to an operator-owned executable that
reads the request on stdin and answers with its exit code.
ADR 0067 already stores operator scripts per project next to
`policy.yaml`; those run inside the environment, not on the plane.

## Decision

1. A project may store `hooks/<project-slug>/admit`, an executable file.
   The plane reads which projects have one at start.
2. Every operator-started session (run, task, child, scheduled, local)
   runs it on the plane host, as the plane user, before the session row is
   written. It runs outside any store transaction.
3. Stdin is one JSON object: `project`, `kind`, `repo`, `base_sha`,
   `actor`, and `prompt_sha256`. The prompt text is never sent. The
   environment is `PATH` only (ADR 0067 D3).
4. Exit 0 admits; the first stdout line (at most 512 bytes) is recorded
   as the reference in `admit.allow` on the new session. Exit 2 refuses;
   the first stdout line is the reason, recorded in `admit.refuse` with no
   session, and returned to the caller. Any other exit, a crash, a file
   that cannot run, or a run past 10 seconds refuses.
5. A start that returns an existing session (same idempotency key, task
   spec, or schedule bucket) does not run the hook. A refused schedule
   bucket replays its refusal instead of asking every tick; an operator
   retry asks again.
6. Without the file, admission is unchanged and nothing is recorded. The
   hook can only narrow admission; `policy.yaml` still decides first.

## Consequences

- Trust: the plane executes operator-stored code on its own host. It is
  as trusted as `policy.yaml`; whoever can write the hooks directory can
  already change policy.
- A session start can take up to 10 seconds longer. A scheduler tick runs
  each due schedule's hook in turn.
- No schema change; two plane-observed action types.
- HA: each instance reads its own hooks directory. Instances must ship the
  same file, like `policy.yaml`.
- Review sessions are not gated. They are created by refresh, inside its
  transaction, for every eligible item on every tick, not by a start
  request; gating them needs a separate decision about when to ask.

## Rejected alternatives

- A built-in client for one governance protocol: binds rusui to it.
- Running the hook inside the start transaction: holds a store
  transaction for up to 10 seconds and would rerun the hook on every
  PostgreSQL serialization retry.
- Running the hook in the environment like ADR 0067 hooks: no environment
  exists before admission, and creating one is what admission guards.

## Validation and reversal

Engine tests start sessions against a temp hook: refusal leaves no
session and records the reason, allow records the reference once across
a retry, faults and timeouts refuse, the hook sees neither plane secrets
nor the prompt, and a refused schedule bucket asks once. Reversal: delete
the file; recorded actions stay as history.

## Sources

[#693](https://github.com/Sannrox/rusui/issues/693),
[ADR 0067](0067-plane-stored-pre-hooks.md).
