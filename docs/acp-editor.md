# ACP editor attach

First supported editor-facing client: the in-tree stdio shim
`rusui acp`. It speaks **ACP protocolVersion 1** as an *agent*. A
desktop editor is the ACP *client* and spawns:

```
rusui acp -url http://127.0.0.1:8080 -token "$RUSUI_OPERATOR_TOKEN"
```

The shim is not a guest model loop. Durable state stays on the plane
HTTP/SSE API (`GET /sessions/{id}`, `GET /sessions/{id}/attach`,
`POST /sessions/{id}/turns`, `POST /sessions/{id}/cancel`,
`GET/POST /approvals`).

## Capability map

| ACP method | Shim |
| --- | --- |
| `initialize` | protocolVersion **1**, agent `rusui-acp` |
| `session/load` | attach existing rusui session id |
| `session/prompt` | follow-up turn |
| `session/cancel` | cancel session |
| `session/update` | SSE attach events |
| `session/request_permission` | pending `acp.approval` allow/deny |
| `session/new` | **unsupported** (will not start a different session) |
| `fs/*`, `terminal/*` | **unsupported** (guest-side; not the editor facade) |

Unsupported methods return JSON-RPC `-32601`. Authentication failure,
expired environments, and missing sessions fail closed.

## Auth

Prefer `RUSUI_OPERATOR_TOKEN`. The worker secret is accepted on the
same session/approval routes for CLI-shaped use. Worker/turn secrets
still cannot mint a console cookie.
