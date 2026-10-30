# Independent operator client (1.0)

A contributor can integrate against the frozen public API without the
in-tree CLI, console, or any `internal/` Go package. This page is the C4
scenario, the support profile, and the sanitized reproduction.

The reusable program is
[`examples/independent-client`](../examples/independent-client/).

## Scenario

Dispatch one `run` session, observe it on the operator HTTP+SSE surface,
approve when a pending approval exists, and retrieve attributable
results (session JSON and action receipts).

Supported pair: `rusui` / `rusui-runner` from the same git commit as this
checkout, schema of that commit, policy v2, operator HTTP+SSE. Mixing
commits is unsupported ([compatibility.md](compatibility.md),
[ADR 0086](decisions/0086-supported-1.0-core-contract.md)).

Credential: one operator bearer (`RUSUI_OPERATOR_TOKEN`, or `-token`).
The worker secret, guest grant, and database path are out of scope.

## Reproduce

From a source checkout that already has a loopback plane (see
[operator.md](operator.md) or the in-process tests):

```bash
go test ./examples/independent-client ./internal/server \
  -count=1 -timeout 120s \
  -run 'TestIndependentClient|TestNoInternalImports|TestIndependentClientExampleBinary|TestIndependentClientJourney|TestIndependentClientAuthRefused|TestIndependentClientExplicitIncompatibility'

go run ./examples/independent-client \
  -url http://127.0.0.1:8080 \
  -token "$RUSUI_OPERATOR_TOKEN" \
  -project rusui \
  -prompt "bounded independent session" \
  -idempotency-key demo-1 \
  -follow 2s
```

The program prints JSON with `session_id`, follow counts, any pending
approvals, the session body, and `/logs` receipts. Repeating the same
`-idempotency-key` returns the same session. After the first follow it
reconnects with `after=<last entry seq>`.

`scripts/compat-conformance.sh` includes the mapped independent-client
checks.

## Assumptions recorded while implementing

Written from [ARCHITECTURE.md](../ARCHITECTURE.md),
[compatibility.md](compatibility.md), the HTTP table in
[configuration.md](configuration.md), and [operator.md](operator.md).
No private assistance and no in-tree CLI source was required for the
wire format after the HTTP table listed the endpoints below.

- Follow (`GET /sessions/{id}/read/follow`) is operator-token only. A
  worker bearer is refused (401). The example therefore uses the operator
  token for every call, including dispatch.
- `POST /projects/{slug}/sessions` with `kind` other than `run` or
  `local` is 400 `kind`. That is the explicit incompatibility check for
  this client.
- Approval list JSON uses the Go default exported field names (`ID`,
  `SessionID`, `Type`, …). There is no separate JSON schema document.
- Dispatch of a `run` session does not need a runner. Observation of
  queued state, reconnect, and empty receipts is enough for this
  scenario. Completing a turn still needs `rusui-runner` and a guest.
- Bind the plane to loopback. This example does not implement
  `RUSUI_PLANE_CA` / HTTPS.

## Exclusions

Not claimed by this example, and not required for C4:

- Runner hello/claim/complete, guest ACP, GitHub writes, console HTML,
  OIDC login, HTTPS/CA, database restore, signed portable bundles.
- A plugin, workflow language, or in-process extension (ADR 0043 remains
  excluded).
- Changes inside a private dependent repository.

## Evidence

`TestIndependentClient*` in `examples/independent-client` and
`internal/server` cover authentication, explicit incompatibility,
interrupted follow/reconnect, idempotent dispatch, approval, receipts,
and `go run` of the example against an in-process plane. The example
package imports only the Go standard library
(`TestIndependentClientImportsOnlyStdlib`).
