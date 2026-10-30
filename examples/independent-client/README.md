# Independent operator client

A bounded HTTP+SSE client for the supported 1.0 operator API. It is the
C4 example: dispatch a `run` session, follow (and reconnect) the
transcript stream, approve when a pending approval exists, and retrieve
the session and its action receipts.

It uses only the Go standard library and the published operator
endpoints. It does not import `github.com/sannrox/rusui/internal/...`,
open SQLite, or add an in-process extension.

Scenario, versions, reproduction, and exclusions:
[docs/independent-client.md](../../docs/independent-client.md).
