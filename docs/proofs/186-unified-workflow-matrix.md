# #186 unified local and managed workflow: predeclared matrix

Predeclared 2026-09-29, before the first run, for
[#186](https://github.com/Sannrox/rusui/issues/186). The maintainer
confirmed the profile and thresholds on the issue the same day. Do not
change a cell's applicability or expected outcome after its result is
known; add findings to the results file instead.

## Fixed profile

| Piece | Local profile (L) | Managed profile (M) |
| --- | --- | --- |
| Candidate | `main` at `1003bf6` | same |
| Host | operator macOS host, same OS user | same host |
| Runtime | Sumika `3dadc7a`, v0.1.0, Rust 1.97.1, daemon on a private socket | Docker container driver, `rusui-trusted` network, `trusted` egress |
| Command / guest | `local_runtime` argv `/bin/zsh -f -i`, absolute scratch cwd | Claude Code guest image (`make guest-image` tag), model `claude-sonnet-5` through the operator's CLI proxy |
| Plane | dedicated eval plane on `127.0.0.1:8282` with its own database; no other plane is touched | same eval plane |
| GitHub | none (local sessions hold no GitHub credential) | read-only intake token; `implement: false`, so no GitHub writes |

Evidence labels: `synthetic` for fake inputs and injected faults, `live`
for real model calls. No live GitHub write happens in this campaign.

## Thresholds (confirmed)

- **pass**: every applicable cell passes in both profiles and every
  identity (Session, Environment, Process, Turn, Attach, receipt) stays
  attributable.
- **narrow**: one profile passes fully; the other fails only on
  non-identity transitions.
- **defer**: any identity, authorization, or ownership failure (PTY bytes
  outside Sumika, a duplicate policy or credential store).

## Matrix

`n/a` cells carry their reason. "Identity" means the Session, Environment,
Process (L), Turn (M), Attach, and receipt ids in the session detail and
receipts stay consistent with the transition.

| # | Transition | L expected | M expected |
| --- | --- | --- | --- |
| 1 | Start | `POST /projects/{p}/sessions` `{"kind":"local"}` returns 201; Sumika confirms a running Process generation 1 | `rusui run` creates a run session; an environment is created and the turn is claimed |
| 2 | First interaction | `rusui attach` shows the shell; typed input echoes | the guest completes the first turn; `rusui read` shows the transcript |
| 3 | Detach | `Ctrl+]` detaches; the Process keeps running | the attach CLI exits; session and environment continue |
| 4 | Reconnect | a second `rusui attach` reaches the same Process id and generation | a second attach reaches the same session and environment id |
| 5 | Attach steal | a new attach takes over; the old client is disconnected; Process unchanged | n/a: managed attach follows the write-lease rule; a second client is read-only (checked, not scored as steal) |
| 6 | Follow-up Turn | n/a: local sessions have no Turns by contract | `rusui prompt` adds a turn on the same session and environment |
| 7 | Cancellation | cancel sends SIGTERM; the session is cancelled only after Sumika reports the Process dead | cancelling a live turn ends it; the session reports cancelled |
| 8 | Process death | killing the Process outside Rusui is observed as dead; no automatic restart; an explicit restart creates generation 2 | n/a: managed sessions have no Process; container loss is row 15 |
| 9 | Runtime restart | restarting the Sumika daemon leaves state unknown until reconcile; death is never inferred | restarting the eval plane keeps the session and environment; the next turn runs on the same environment |
| 10 | Operator-visible recovery | after rows 8 and 9 the operator sees the true state and recovers explicitly | after row 9 `rusui sessions` and `rusui read` show a consistent state |
| 11 | Identity | attributable through rows 1 to 10 | attributable through rows 1 to 10 |
| 12 | Policy denial | a local session on a project without the `local` kind is refused | a run session on a project without `run` is refused |
| 13 | Credential refusal | create or attach with a wrong token is refused | a turn grant used after its turn ends is refused by the model proxy |
| 14 | Stale Process report | after restart to generation 2, generation 1 observations do not change generation 2 | n/a: no Process in the managed profile |
| 15 | Environment replacement | n/a: the local profile runs on the host, not a replaceable Environment | expiring the environment ends access; the next turn runs on a new environment id |
| 16 | Revoked operator access | after rotating the operator token, the old token is refused for read and attach | same |
| 17 | Ownership | PTY bytes stay in Sumika; the Rusui database holds no PTY bytes and no policy or credential copy exists in Sumika | one Rusui database is the only policy, credential-grant, and receipt store |
| 18 | Install and recovery | build Sumika from the baseline and start the daemon as documented; recover by the documented drain, stop, copy, restart path | `rusui setup plan` (writes nothing) and the same recovery path |

Record per cell: pass, fail, or n/a; the ids observed; latency where the
cell has a wait; operator steps; and whether the evidence is `live` or
`synthetic`.
