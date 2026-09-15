# Grok CLI model egress (ACP stdio)

Research for [Which hosts and credentials does Grok CLI need for model
egress?](https://github.com/Sannrox/rusui/issues/39). Date: 2026-09-15.
Confirms and updates the off-repo probe recorded in
[ADR 0002](../decisions/0002-grok-acp-agent-set.md); that probe is not
treated as sufficient by itself.

## Verdict

Spawn `agent --permission-mode default agent stdio` (Grok `0.2.112`, and
`1.0.30` which still speaks ACP) reaches the model over **HTTPS port 443**.
With a cached OAuth session it calls `cli-chat-proxy.grok.com` and refreshes
at `auth.x.ai`. With an API key it calls `api.x.ai` instead of (or in
addition to) that proxy.

The process always attaches a Bearer credential from **inside the
environment**: `~/.grok/auth.json` (OAuth cache), or `XAI_API_KEY` /
`GROK_CODE_XAI_API_KEY` (env), or a per-model `api_key` / `env_key` in
`config.toml`. There is no first-party mode where the CLI sends
unauthenticated requests and a plane proxy injects the xAI secret. A plane
egress proxy can **hold** the xAI key and swap `Authorization` if the CLI is
pointed at it, but the CLI still requires some local credential to emit.

Minimum rusui egress class that can still complete a turn: **`trusted`**,
if that class is the vendor-API host allowlist on 443. **`none` cannot.**
**`full` is not required** for model completion. Use **`custom`** only if
`trusted` is implemented as something narrower than the hosts below.

## Binaries probed

| Path | Version | Role |
| --- | --- | --- |
| `~/.local/bin/agent` → `~/.grok/downloads/grok-macos-aarch64` | `0.2.112` (`9bbd559437aa`) | ADR 0002 P1 spawn |
| `~/.grok/bin/grok` → `grok-1.0.30` (also `/opt/homebrew/bin/grok`) | `1.0.30` (`04b7ffed98c6`) | later CLI that still has `agent stdio` |

`--help` on both: `login --oauth` is “Grok OAuth via `auth.x.ai`”;
`login --device-auth` is device-code; `agent` accepts
`--cli-chat-proxy-base-url` and `--xai-api-base-url`; stdio is
`agent stdio`. `--permission-mode default` is a top-level flag on the
binary, matching the ADR 0002 spawn.

ACP `initialize` against `agent --permission-mode default agent stdio`
(JSON-RPC 2.0, newline-delimited) on 2026-09-15:

| Condition | `authMethods` |
| --- | --- |
| `0.2.112`, real `~/.grok/auth.json` present | `cached_token` (“Cached token from `~/.grok/auth.json`”), `grok.com` (“Sign in with Grok”) |
| `0.2.112`, isolated `$GROK_HOME`, no key | `grok.com` only |
| `0.2.112`, isolated `$GROK_HOME`, dummy `XAI_API_KEY` or `GROK_CODE_XAI_API_KEY` | `xai.api_key` (“`XAI_API_KEY` or `api_key`/`env_key` in `config.toml`”), `grok.com` |
| `1.0.30`, real `auth.json` present | `cached_token`, `grok.com` |
| `1.0.30`, isolated `$GROK_HOME`, with or without dummy `XAI_API_KEY` / `GROK_CODE_XAI_API_KEY` | `grok.com` only |

`1.0.30` still documents API-key auth and still has the spawn; it did not
advertise `xai.api_key` on this host’s `initialize`. That is an observed
initialize gap, not proof that API-key inference was removed.

Current first-party ACP example authenticates with `xai.api_key` when
`XAI_API_KEY` is set, else `cached_token`, and errors with “Run `grok login`
first, or set `XAI_API_KEY`.”
([Headless & Scripting](https://docs.x.ai/build/cli/headless-scripting))

## Hosts and ports

xAI’s enterprise deployment page is the first-party network table. All
listed connections are **HTTPS, port 443**, TLS 1.2 or 1.3 via `rustls`,
OS trust store, TLS not optional.
([Enterprise Deployments](https://docs.x.ai/build/enterprise))

### Required to complete a turn

| Host | Port | When |
| --- | --- | --- |
| `cli-chat-proxy.grok.com` | 443 | Cached OAuth/OIDC session. Default base `https://cli-chat-proxy.grok.com/v1` (`GROK_CLI_CHAT_PROXY_BASE_URL`, `--cli-chat-proxy-base-url`). Inference, settings, model catalog. `0.2.112` strings include `/v1/chat/completions`, `/models-v2`, `/settings`, `/login-config`. |
| `auth.x.ai` | 443 | OAuth2/OIDC login and silent refresh of the session in `auth.json`. `agent login --oauth` help text: “Use Grok OAuth via `auth.x.ai`.” |
| `api.x.ai` | 443 | API-key path. Default base `https://api.x.ai/v1` (`GROK_XAI_API_BASE_URL`, `--xai-api-base-url`). xAI REST inference is `https://api.x.ai` with `Authorization: Bearer <xAI API key>`. ([REST API overview](https://docs.x.ai/developers/rest-api-reference/inference)) |

Enterprise docs: `cli-chat-proxy.grok.com` and `auth.x.ai` are required for
core functionality; `api.x.ai` “only needed when using `api_key` auth
instead of the inference proxy.”

### Not required for a stdio model turn

These can be blocked without stopping inference, per the same enterprise
table, plus CLI flags:

| Host | Why it is not the P1 turn |
| --- | --- |
| `code.grok.com` | Remote session sync / `agent headless` WebSocket relay (`wss://code.grok.com/ws/code-agent` in `0.2.112` strings). P1 spawn is stdio. |
| `assets.grok.com` | UI assets |
| `x.ai` (`/cli/install.sh`, `/cli/changelogs`) | Installer and auto-update. Suppress with `GROK_DISABLE_AUTOUPDATER=1` / `--no-auto-update`. |
| `storage.googleapis.com` | Installer CDN fallback |
| `console.x.ai` | Where humans mint API keys; not called at turn time |
| `grok.com` | Browser login. Needed only if ACP authenticates with method `grok.com`, not with `cached_token` / `xai.api_key` |
| `management-api.x.ai` | Management keys / ACLs; not the CLI inference path |
| `127.0.0.1` (random port) `/callback` | OAuth loopback at **login**, not at turn |

Tool traffic (`web_search`, `web_fetch`, MCP, marketplace git) is outside
model egress. Isolation of that is Grok’s `--permission-mode default`
fence, already locked.

The CLI honors `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY`. Enterprise docs
ask that proxy idle timeouts be at least 10 minutes because inference is
SSE. A TLS-inspecting proxy needs its CA in the **OS trust store** of the
environment.

## Where auth lives

Not secret values; names and locations only.

### OAuth cache file (default interactive path)

- Path: `$GROK_HOME/auth.json`, default `~/.grok/auth.json`. Mode `0600` on
  this host.
- First-party: “Grok stores credentials in `~/.grok/auth.json`”; hot-reload
  on the next API call; `grok logout` clears it.
  (shipped user guide `~/.grok/docs/user-guide/02-authentication.md`)
- Live file on this host is a map keyed by
  `https://auth.x.ai::<oauth-client-id>`. Nested field **names**:
  `auth_mode`, `coding_data_retention_opt_out`, `create_time`, `email`,
  `expires_at`, `first_name`, `key`, `oidc_client_id`, `oidc_issuer`,
  `principal_id`, `principal_type`, `profile_image_asset_id`,
  `refresh_token`, `team_id`, `user_id`.
- `key` is the access token the CLI sends as Bearer; `refresh_token` is
  the refresh grant. This is an OAuth cache, not an API-key file.
- ACP method `cached_token` is advertised only when this file is present.
- Relocate the whole tree with `GROK_HOME`.

### Environment (API-key path; no `auth.json` required)

| Name | Role |
| --- | --- |
| `XAI_API_KEY` | Documented CI/headless key. Fallback when no session token is active. ([docs overview](https://docs.x.ai/build/overview), [settings reference](https://docs.x.ai/build/settings/reference), enterprise “API key” section) |
| `GROK_CODE_XAI_API_KEY` | Still present in the `0.2.112` binary; that version’s ACP `initialize` treats it like `XAI_API_KEY` for advertising `xai.api_key`. Current public ACP sample uses `XAI_API_KEY`. |

`0.2.112` with an isolated empty `$GROK_HOME` and only a dummy
`XAI_API_KEY` advertised `xai.api_key` and did **not** require
`auth.json`.

### Config file (per-model)

`~/.grok/config.toml` `[model.<id>]` `api_key` (inline secret) or `env_key`
(name of an env var). Credential resolution, highest first:
`model.api_key` > `model.env_key` > active session token in `auth.json` >
`XAI_API_KEY`.
(Enterprise “Authentication”; shipped `02-authentication.md` “Auth
Precedence”.)

### Other mint paths (still land in `auth.json`)

- `grok login` / `grok login --oauth` / `--device-auth`
- Enterprise OIDC (`GROK_OIDC_ISSUER`, `GROK_OIDC_CLIENT_ID`)
- External auth provider (`GROK_AUTH_PROVIDER_COMMAND`); stdout becomes
  the token **stored in `auth.json`**

Login is a one-time mint. A turn with a still-valid `auth.json` does not
need a browser. Refresh of that session still needs `auth.x.ai:443`.

## Can the xAI secret live only on a plane egress proxy?

**No, not with first-party CLI behavior as documented and probed.**

ADR 0001 D5 wants no secret written into the environment and “model API
keys traverse an egress proxy with a host allowlist.” Two proxy shapes
exist in first-party docs:

1. **Forward proxy** (`HTTPS_PROXY`). The CLI still dials
   `cli-chat-proxy.grok.com` or `api.x.ai` and still sends its local
   Bearer. The proxy can allowlist those hosts. The secret remains in
   the guest (`auth.json` or env). TLS inspection additionally plants a
   CA in the guest trust store.
2. **Reverse proxy** (`GROK_CLI_CHAT_PROXY_BASE_URL` /
   `GROK_XAI_API_BASE_URL` / the matching `agent` flags, or per-model
   `base_url`). Shipped auth guide shows
   `export GROK_CLI_CHAT_PROXY_BASE_URL="https://grok-proxy.acme.com/v1"`
   for enterprise OIDC: the CLI still stores tokens in `auth.json` and
   sends that Bearer to the proxy.

The `0.2.112` binary, given no credentials, logs that it has “No auth
credentials for cli-chat-proxy” and skips that catalog. There is no
documented empty-auth “proxy will attach the key” mode.

What a rusui plane **can** do without putting the **xAI API key** in the
guest:

- Point the CLI at the plane reverse proxy and put a **different**
  in-guest credential there (OIDC access token, external-provider token,
  dummy `XAI_API_KEY` the proxy accepts and replaces). That still puts
  *a* secret or stand-in in the environment.
- Use `HTTPS_PROXY` as a host allowlist only, and still inject
  `XAI_API_KEY` or mount `auth.json` — that satisfies connectivity, not
  D5.

D5 as written (“no secret enters an environment”) is **not** satisfied by
Grok CLI today unless the plane treats a short-lived turn token as
non-secret relative to the xAI key and swaps it on the proxy. The CLI
will not talk to the model with zero in-process credential.

## Minimum egress class

ADR 0001 names classes `none` / `trusted` / `custom` / `full` and says
they are enforced by the driver. ROADMAP P1.3 (credential broker, API
proxy, egress classes) is not implemented; this repo does not yet define
which host set `trusted` contains.

Mapping the first-party network table onto those names:

| Class | Can a Grok ACP turn complete? |
| --- | --- |
| `none` | No. No path to `cli-chat-proxy.grok.com` / `api.x.ai`. |
| `trusted` | **Yes, if** `trusted` is the vendor model-API allowlist and includes the hosts in “Required to complete a turn” on 443 (OAuth session: proxy + `auth.x.ai`; API key: `api.x.ai`). This is the class D5 describes. |
| `custom` | Yes, with an operator allowlist of those same hosts, if `trusted` is implemented as something narrower. |
| `full` | Yes, but more than model egress needs. |

Operational minimum: **`trusted`**. Not `none`. Not `full` for the model
call itself.

A DNS resolver able to resolve those names is implied unless the driver
pins IPs (TLS SNI still needs the hostname).

## Updates to ADR 0002’s auth row

ADR 0002 (2026-09-14) recorded: `cached_token` (`~/.grok/auth.json`),
`grok.com`; `agent login --oauth` / `--device-auth`; API-key auth is not
disabled.

Confirmed on 2026-09-15 against `0.2.112` help, ACP `initialize`,
`auth.json` **key names**, shipped user guide, and docs.x.ai:

- `cached_token` and `grok.com` still appear when `auth.json` exists.
- `xai.api_key` is a real ACP method on `0.2.112` when `XAI_API_KEY` or
  `GROK_CODE_XAI_API_KEY` is set and no session file is present. ADR 0002
  said API-key auth is not disabled; the method name is now observed.
- Default inference host for the session-token path is
  `cli-chat-proxy.grok.com`, not `grok.com`. `grok.com` is the interactive
  login method, not the chat endpoint.
- Token issuer in the live cache is `auth.x.ai`, not the older
  `accounts.x.ai/sign-in` slot quoted in some in-binary curl samples.
- `1.0.30` still exposes `agent stdio` and the same login flags. Treat it
  as “later that still speaks ACP,” with the `xai.api_key`-on-initialize
  gap noted above.

## Sources

- Local `0.2.112` (`9bbd559437aa`) and `1.0.30` (`04b7ffed98c6`) binaries:
  `--help`, `login --help`, `agent --help`, `agent stdio --help`; ACP
  `initialize` under `--permission-mode default agent stdio`; `strings`
  for default URLs and env names. Dummy env values only; `auth.json`
  values not printed.
- Shipped user guide extracted under `~/.grok/docs/user-guide/`
  (`02-authentication.md`, `05-configuration.md`, `14-headless-mode.md`,
  `15-agent-mode.md`, `26-config-reference.md`).
- [Grok Build overview](https://docs.x.ai/build/overview),
  [CLI reference](https://docs.x.ai/build/cli/reference),
  [Headless & Scripting (ACP)](https://docs.x.ai/build/cli/headless-scripting),
  [Settings reference](https://docs.x.ai/build/settings/reference),
  [Enterprise Deployments](https://docs.x.ai/build/enterprise)
  (read 2026-09-15).
- [xAI REST API overview](https://docs.x.ai/developers/rest-api-reference/inference)
  (base URL `https://api.x.ai`, Bearer API key).
- [ADR 0001 D5](../decisions/0001-environment-plane.md),
  [ADR 0002](../decisions/0002-grok-acp-agent-set.md),
  [ROADMAP P1.3](../../ROADMAP.md).
