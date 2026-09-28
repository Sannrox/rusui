# ADR 0031: The rusui CLI stays a flat verb list

- Status: Accepted
- Date: 2026-09-28
- Amends: [ADR 0003](0003-operator-surface.md) (CLI verb list).
- Resolves: [#336](https://github.com/Sannrox/rusui/issues/336)
- Related: [ADR 0006](0006-session-start.md) (`sync` stays a later
  verb, still flat), [ADR 0024](0024-session-surface.md) (no rusui TUI),
  [#83](https://github.com/Sannrox/rusui/issues/83) (`logs` are action
  receipts), [#334](https://github.com/Sannrox/rusui/issues/334)
  (hook output is a different object).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

The `rusui` binary is a switch plus a per-verb `flag.NewFlagSet`. No
subcommand starts the HTTP server. Each client verb redeclares `-url`
and `-token`. `rusui logs` is action receipts. #334 needs a name for
setup/resume/service output that cannot steal `logs`.

Objects this choice binds:

| Object | Owner | Properties |
| --- | --- | --- |
| Command | `rusui` argv[1] | flat verb; stdlib `flag` |
| PlaneURL | flag `-url` | default `http://127.0.0.1:8080` |
| Token | flag `-token` | operator class for session verbs |
| ReceiptLog | `rusui logs` | turn action receipts |
| EnvLog | `rusui envlog` | bounded setup/resume/service output (#334) |

Actions: StartPlane (no subcommand, or later `serve` alias); InvokeVerb;
ListReceipts; ReadEnvLog. A nested session object is a refused action
until a superseding ADR.

## Decision

**D1. No-arg still starts the plane.** `rusui` with no subcommand binds
loopback HTTP as today. `rusui serve` is an authorized alias of that
start; this ADR does not implement the alias. Unknown verbs print usage
to stderr and exit 2. `rusui-runner` remains a separate binary.

**D2. Verbs stay flat.** Session client verbs do not group under a
session object. Stdlib `flag` remains. A command framework is refused
until help/completions are a measured pain on a nested tree.

**D3. Shared `-url` / `-token`.** Session client verbs accept one
operator token. Worker secrets are not CLI flags. Extracting a shared
FlagSet is compatible with this ADR and is not required in this change.

**D4. Two log nouns.** `rusui logs SESSION_ID` stays action receipts.
Setup/resume/service output (#334) is `rusui envlog SESSION_ID`. They
are different objects. #334 must not ship a CLI name that collides with
`logs`.

**D5. Compat.** Keep today's `rusui run`, `rusui logs SESSION_ID`, and
no-arg server start. Do not break them in this ADR.

No implementation follow-up is authorized except the `envlog` name when
#334 ships storage.

## Usage synopsis

```
rusui                         # start the plane (loopback HTTP)
rusui serve                   # authorized alias; not implemented here
rusui run ...
rusui review ...
rusui acp ...
rusui sessions ...
rusui attach ...
rusui prompt ...
rusui logs SESSION_ID         # action receipts
rusui envlog SESSION_ID       # #334 hook output (when stored)
rusui read SESSION_ID
rusui put ...
rusui approve ...
rusui diagnose ...
rusui policy ...
rusui setup ...
rusui demo ...
rusui drain ...
rusui diagnostics ...
```

## Consequences

Easier: #334 has a verb. Receipts keep `logs`. Scripts keep no-arg
server start.

Harder: the switch grows until a later ADR groups it.

Irreversible: none.

## Rejected alternatives

- **Nest session verbs now.** No measured help/completion pain.
- **Steal `rusui logs` for hook output.** Collides with receipts.
- **Require `rusui serve` and break no-arg start.** Breaks setup and
  user-service invocations without a migration need.

## Validation and reversal

Validation: this ADR is Accepted in the index; 0003 links forward;
`envlog` is the #334 CLI name. Reverse by a superseding ADR that
rewrites the synopsis.

## Sources

- [#336](https://github.com/Sannrox/rusui/issues/336)
- `cmd/rusui/main.go` switch
- [ADR 0003](0003-operator-surface.md), [ADR 0024](0024-session-surface.md)
