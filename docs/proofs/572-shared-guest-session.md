# Per-session guest tracer (#572)

The isolated live tracer passed on 2026-10-08. Its candidate is the #572
implementation based on `aef8c0b51fa761c67f1a91f0965c78dea6f5090a`.
The opt-in executable proof is `TestLiveGuestDisconnectAndResume` in
`internal/server/guest_live_test.go`.

## Boundary and inputs

- A loopback HTTP plane with a temporary SQLite store and process workspace.
- CLI-created project-only session, without GitHub credentials or repository.
- Public [Shikigami source](https://github.com/Sannrox/shikigami) at
  `6b80da068c50dcb5001687163ea4651b7412c1d9`, CLI version `1.1.1`, built with
  `--locked -p shikigami-cli --no-default-features --features model-http`.
- CLI Proxy API at `http://127.0.0.1:8317`, using the existing gateway access
  configuration only inside the plane. The guest received a per-turn grant.
- Model `claude-haiku-4-5-20251001`, through OpenAI Chat Completions. A separate
  bounded Anthropic Messages request used the same leased turn and gateway.
- Explicit guest config: local governance, no event adapter, `workspace_exec`
  tools, eight model turns maximum, loopback network allowlist. State and
  config lived in temporary directories outside the managed workspace.

This guest pin reads `model.base_url` from its config rather than
`OPENAI_BASE_URL`. The fixture explicitly points it at the plane's
`/model-proxy/v1`, and uses `OPENAI_API_KEY` as the grant variable.
Its inplace ACP workspace requires the state directory outside that workspace.
These are guest configuration requirements, not a new plane connection.
`grok-4.6` was listed by the gateway but failed because its backing CLI version
was outdated; no daily-driver installation was changed.

## Observed result

1. `rusui run` created the session using the project's Shikigami default.
2. `rusui read -follow` attached. The proof held the first model request at the
   plane boundary, killed this reader, and confirmed the session was not
   cancelled.
3. A second HTTP client attached through `/sessions/{id}/attach` over SSE.
4. The Anthropic dialect request succeeded through the same origin. Releasing
   the first request let the real guest write its structured result and finish.
5. The second client submitted another prompt. A new guest process loaded the
   original cursor and completed that turn in the same durable session.
6. The session retained `guest_name=shikigami`, `guest_pin=1.1.1`, and the same
   nonempty guest cursor. The turn row completed with lease generation two.
7. The `model.proxy` receipts recorded the gateway origin and HTTP 200. No
   request bodies, gateway keys, or turn grants were included in these receipts.

The live command also passed with the optional native Codex fixture enabled:

```bash
go test ./internal/server -run '^TestLiveGuestDisconnectAndResume$' \
  -v -count=1 -timeout 4m
```

It requires explicitly supplied `RUSUI_LIVE_GATEWAY_URL`,
`RUSUI_LIVE_GATEWAY_KEY`, `RUSUI_LIVE_MODEL`, `RUSUI_LIVE_GUEST`, and
`RUSUI_LIVE_CLI`. Keep the key in process environment; do not put it in policy
or a command transcript. Optional `RUSUI_LIVE_CODEX` points at an installed
native binary and enables the Responses proof. Ordinary tests skip this live proof.

## Deterministic coverage and limits

Policy parsing refuses unsupported Claude pins, unknown guests, mismatched native protocols, missing
spawn/probe/pin data, and defaults outside the project allowlist. API refusal
creates no session or environment. A frozen image and argv survive registry
edits, while removing a project allowance refuses execution. The starter preserves the environment guest fallback. Database upgrade
preserves legacy sessions for first-claim binding. Fake ACP integration covers
mid-turn disconnect, reattach, and the next turn independently of a model.
Native Codex routing retains its thread cursor and rejects attachments before
admission until native attachment input is supported. Review exposed the previous
synthetic app-server handshake; a contract-faithful fixture fails against that
adapter and passes after correction. The host now sends `clientInfo` followed
by `initialized`, reads `thread.id`, supplies text `input`, and waits past the
turn-start acknowledgement for completion. Native command/file approvals map
to plane policy and return one-shot accept/decline; additional permission
profiles and managed network approvals are denied. Read-only sandbox settings override account defaults.
An isolated `codex-cli 0.161.0` process also accepted initialize and thread/start
without a model call, account state, or operator workspace. The local generated
schema and [official app-server contract](https://learn.chatgpt.com/docs/app-server)
were checked. A live native Codex turn then authenticated with the per-turn
API-key grant, selected the gateway model, wrote its result, and completed.
Its `/v1/responses` receipt records the same gateway origin and HTTP 101
for the successful WebSocket upgrade.
Codex 0.161.0 requires `openai_base_url` in thread configuration;
`OPENAI_BASE_URL` alone is insufficient. The adapter supplies that configuration
with the `/v1` suffix. Missing provider thread state falls back to a new thread
with the same workspace, read-only sandbox, policy, and grant.

Revoking the oldest queued guest does not starve later allowed sessions; a
before/after test covers that queue behavior. Existing Claude tests retain
its stream-json behavior. A dual-dialect proxy test checks both credential
headers and receipt redaction.

The live proof establishes the process-driver path and real gateway calls.
It does not establish container packaging or an OS sandbox. Generic ACP
registration, child guest selection, fresh setup defaults, transcript
normalization, and multi-user identity remain separate dependent issues.
