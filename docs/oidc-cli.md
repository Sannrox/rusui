# Sign in to the CLI with OIDC

OIDC is optional. It authenticates the existing single operator for API
commands; it does not introduce team roles or browser-console SSO
([ADR 0074](decisions/0074-single-operator-oidc-cli.md)).

## Configure the plane and provider

Create a public client supporting Authorization Code with PKCE S256. Do
not provision a client secret. Register the redirect
`http://127.0.0.1:9876/callback`, or another port used with
`-callback-port`. Configure the provider to issue a signed JWT access token
with the API audience `rusui`. The client ID and API audience may differ.
Use short access-token lifetimes; enable refresh tokens for the client if
you want automatic renewal. Some providers require the `offline_access`
scope to issue refresh tokens.

Set the plane's environment before starting it:

```sh
RUSUI_OIDC_ISSUER=https://idp.example/realms/example
RUSUI_OIDC_AUDIENCE=rusui
RUSUI_OIDC_CLIENT_ID=rusui-cli
RUSUI_OIDC_SUBJECT=exact-operator-subject
RUSUI_OIDC_SCOPES='openid offline_access'
```

All four identity fields are required when any OIDC setting is present.
Scopes default to `openid`. The exact issuer and subject authorize the
single operator; email, groups, and caller-supplied member IDs do not.
Issuer and endpoint URLs require HTTPS, except literal loopback HTTP for
development. Discovery must return exactly the configured issuer.

The verifier supports RS256 with RSA keys of at least 2048 bits and ES256
with P-256 keys. Startup fails if discovery or usable keys cannot load.
Keys refresh every ten minutes, and an unknown key ID schedules a refresh
at most once per thirty seconds. The request that encountered an unknown
key is refused; subsequent requests can use the refreshed key set. Request
verification performs no network I/O. Refresh failures retain the last
usable keys; unexpired signed tokens may still authenticate during an
issuer outage. Expired credentials cannot.

Keep `RUSUI_OPERATOR_TOKEN` for console access and explicit break-glass
API access. It remains separate from `RUSUI_WORKER_SECRET`.

## Sign in

```sh
rusui login -url https://plane.example
rusui read -url https://plane.example 42
rusui logout -url https://plane.example
```

`login` opens the default browser and waits up to five minutes on loopback.
Use `-no-browser` to print the URL without opening it, or `-callback-port`
with a different provider-registered port. The browser must run on the
machine where `rusui login` is listening. This is not a device-code flow
for a remote terminal. A failed browser launch prints instructions to open
the displayed URL manually.

HTTPS CLI calls use the existing `RUSUI_PLANE_CA` configuration for the
plane. Issuer discovery and token exchange use system trust separately.
No provider or plane endpoint redirect is followed by login or refresh.

Successful login checks that the plane accepts the access token, then
stores it with its refresh token, issuer, client ID, token endpoint, and
expiry. Files are written atomically with mode `0600` under
`$XDG_CONFIG_HOME/rusui/credentials/` when set, otherwise the OS user config
directory. Each normalized server URL has a separate file. Login refuses
to silently change a saved issuer, client, or token endpoint; remove that
local login before accepting changed configuration.

## Credential precedence and supported commands

Operator API commands use:

1. Explicit `-token`, including an explicitly empty value.
2. `RUSUI_OPERATOR_TOKEN` when nonempty.
3. The saved OIDC login for that `-url`.
4. An existing worker-token default for commands that already accept workers,
   when no saved login exists.

The saved login works with `read`, `envlog`, `sync`, `put`, `retry`,
`disposition`, `sessions`, `prompt`, `logs`, `approve`, `archive`,
`unarchive`, `review`, and the API-facing ACP editor adapter. Tokens near
expiry refresh automatically before a command starts, only against the
endpoint pinned at login. Refresh failure is reported; the CLI does not
silently select another credential. If a long-running stream or operation
later loses access after expiry, rerun the command to refresh credentials.

`attach` uses console and terminal authority and still requires the static
operator token. Runner/worker-only commands, including `run`, `drain`, and
`resume`, keep their existing worker credentials. Saved OIDC credentials
never authorize worker claim/completion routes, turn actions, console
cookies, terminal write leases, or preview grants.

## Logout, revocation, and rollback

`logout` removes only the selected server's local credential. It does not
revoke refresh tokens or already-issued access tokens at the provider.
Provider logout likewise does not instantly invalidate a JWT at the plane;
its expiration bounds the revocation delay. Use provider-side revocation
and short access-token lifetimes when that delay matters.

Changing the configured issuer or subject and restarting the plane rejects
old OIDC identities. Remove all OIDC environment settings and restart to
return to static-token authentication. No plane database migration is
involved. Static-token rotation keeps its existing console revocation
behavior; it is not OIDC revocation.
