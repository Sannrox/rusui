# GitHub App installation tokens vs rusui's PAT

Resolves [#40](https://github.com/Sannrox/rusui/issues/40). Primary sources:
GitHub Docs (Apps, installation tokens, permissions, fine-grained PATs,
git over HTTPS) and rusui on `origin/main`. This ticket does not
provision an App.

## Answer

An installation access token (IAT) can cover everything the current
read-only PAT does on `Sannrox/rusui` and `Sannrox/shikigami` except
**repository-webhook delivery reconcile**, which an IAT can do only with
repository **Webhooks: read**, and which the App-native path does with a
**JWT as the App**, not an IAT. Webhook **intake** never used the PAT;
it used HMAC. For P1 dogfood, register an App, install it on those two
repos, and grant **Metadata: read** (mandatory), **Contents: write**,
**Issues: read**, **Pull requests: read**. Subscribe the App webhook to
`issues`, `issue_comment`, `pull_request`, and `push`. Mint IATs for
REST and git smart HTTP. Keep HMAC on the App webhook secret. Do **not**
grant Issues/PR write, Administration, or Contents-only-read. A
fine-grained PAT can stand in for REST reads and git HTTPS on those two
user-owned repos; it cannot mint 1-hour downscoped tokens and therefore
cannot implement ADR 0001 D5.

## What the PAT does today

`cmd/rusui/main.go` loads `RUSUI_GITHUB_TOKEN` or `GITHUB_TOKEN` and
fails closed if both are empty (`"set RUSUI_GITHUB_TOKEN or GITHUB_TOKEN
(read-only)"`). `RUSUI_GITHUB_HOOK_IDS` is `owner/repo=hookid` pairs.
`RUSUI_WEBHOOK_SECRET` is the HMAC key for `POST /hooks/github`; it is
not the PAT.

`internal/gh/client.go` is GET-only. `README.md` states the client
"never comments, closes, or merges." Apply is dry-run (`ARCHITECTURE.md`).
The PAT never clones or pushes.

| Call in `internal/gh/client.go` | Path | Why |
| --- | --- | --- |
| `GetItem` | `GET /repos/{owner}/{repo}` | default branch |
| `GetItem` | `GET /repos/{owner}/{repo}/git/ref/heads/{branch}` | `MainSHA` |
| `GetItem` / `GetItemExists` | `GET /repos/{owner}/{repo}/issues/{n}` | issue or PR-as-issue |
| `GetItem` | `GET /repos/{owner}/{repo}/pulls/{n}` | PR fields |
| `GetItem` | `GET /repos/{owner}/{repo}/issues/{n}/comments` | non-bot comment count |
| `IsAncestor` | `GET /repos/{owner}/{repo}/compare/{commit}...{defaultTip}` | evidence: `ahead` or `identical` |
| `ListOpenItems` | `GET /repos/{owner}/{repo}/issues?state=open` | catch-up |
| `ListDeliveries` | `GET /repos/{owner}/{repo}/hooks/{id}/deliveries` | missed-delivery reconcile |
| `GetDelivery` | `GET /repos/{owner}/{repo}/hooks/{id}/deliveries/{id}` | replay payload |

Webhook intake (`internal/server/server.go`, `gh.Verify`) checks
`X-Hub-Signature-256` against `RUSUI_WEBHOOK_SECRET` and does not call
GitHub. Reconcile is skipped when `RUSUI_GITHUB_HOOK_IDS` is unset.

## Installation tokens

To act as an installation, mint an IAT: JWT from the App private key,
then `POST /app/installations/{id}/access_tokens`. The IAT expires in
**one hour**. The mint body may further restrict `repositories` /
`repository_ids` (up to 500, subset of the installation) and
`permissions` (subset of the App grant). Prefix `ghs_`. Use it as
`Authorization: Bearer` on REST/GraphQL, or as the HTTPS git password:

```
git clone https://x-access-token:TOKEN@github.com/owner/repo.git
```

HTTP git requires the **Contents** repository permission. Write Contents
to push. Pushing `.github/workflows/**` also needs **Workflows**. GitHub
does not offer a ref-prefix permission; `refs/heads/rusui/<session>/*`
is a plane-proxy rule (ADR 0001 D5), not an App permission.

IAT API success depends only on the App's permissions, not a user's.
Activity is attributed to the App (`app[bot]`), not the installer.
IATs do not need SSO authorization. They can be revoked with
`DELETE /installation/token` or by uninstalling the App.

## Permission map for dogfood

Install on the user account that owns `Sannrox/rusui` and
`Sannrox/shikigami`, **only those repositories**. Metadata is
read-only and is forced on whenever any repository permission is
granted.

| Need | App permission | Events (App webhook) |
| --- | --- | --- |
| `GET /repos/{owner}/{repo}` | Metadata: read | — |
| git clone; `git/ref`; `compare` | Contents: read (included in write) | `push` (Contents: read) |
| git push to session refs | Contents: write | — |
| issues, comments, open-item list | Issues: read | `issues`, `issue_comment` |
| `GET /pulls/{n}` | Pull requests: read | `pull_request` |
| HMAC intake | none (App webhook secret) | — |
| repo-hook delivery list/detail (today's reconcile) | Webhooks: read (`repository_hooks`) | — |
| App-hook delivery list/detail (P1.5) | JWT as the App, **not** an IAT | — |
| push of `.github/workflows/**` | Workflows: write | — |

Recommended **P1 App permission set**:

- Metadata: read (mandatory)
- Contents: write
- Issues: read
- Pull requests: read

Subscribe the App webhook to `issues`, `issue_comment`, `pull_request`,
and `push`. Set a webhook secret and keep validating
`X-Hub-Signature-256`. `installation` and `installation_repositories`
are delivered to Apps by default.

**Do not grant** for P1 dogfood:

- Issues write / Pull requests write — agent writes go through
  plane-owned proxies and apply; apply is still dry-run; live comment
  and PR open are P2.4 / P2.5.
- Administration — not required if reconcile moves to
  `GET /app/hook/deliveries` with a JWT.
- Contents: read only — P1.3 must push session refs.
- Workflows: write — only if a session must push workflow files.
  rusui has `.github/workflows/build.yml`. Without this permission,
  Contents: write cannot update that path.

Keep repository **Webhooks: read** on the App only if v1 reconcile
(`RUSUI_GITHUB_HOOK_IDS` + `ListDeliveries` / `GetDelivery`) stays on
per-repo hooks. P1.5 should drop that grant and use the App webhook
plus JWT.

Mint IATs with `permissions` and `repository_ids` narrowed per turn:
Contents: write (and Workflows: write if needed) for the git proxy;
Issues: read + Pull requests: read + Metadata: read + Contents: read
for the read client. Never put the App private key or a long-lived
token in an environment.

## What a PAT can do that an IAT cannot

Capabilities rusui's current PAT (or a PAT in general) has that an IAT
does not:

1. **User identity.** A PAT acts as the user. An IAT acts as the App
   bot. rusui does not write today, so attribution is unused; live
   comment/close/PR later would show `app[bot]` unless a user-to-server
   token is added.
2. **Any repository the user can access.** A classic PAT with `repo`
   sees every accessible repo. An IAT sees only the installation.
3. **Multiple resource owners in one secret.** A classic PAT can. An
   IAT is one installation. A fine-grained PAT is one owner.
4. **User-only REST.** `GET /issues` (assigned-to-me), notifications,
   gists, user-owned Projects, `GET /user`. rusui does not call these.
5. **Long-lived without minting.** PAT until expiry or revocation. IAT
   is one hour and needs the App private key to mint.
6. **App webhook deliveries.** `GET /app/hook/deliveries` and
   `GET /app/hook/deliveries/{id}` require a JWT. They do not work with
   an IAT, a user-to-server token, or a fine-grained PAT. If P1.5 moves
   intake to the App webhook, reconcile authenticates **as the App**,
   not as an installation.

What an IAT can do that a PAT cannot (why D5 wants Apps): mint 1-hour
tokens downscoped to repos and permissions; bot identity independent of
the user; one App webhook for every installed repo; installation
lifecycle events; rate limits that scale with repos and users; no SSO
step; survives the installer leaving an organization.

## Fine-grained PAT as a substitute

Fine-grained PATs (`github_pat_`) use the same permission names as Apps,
are limited to one user or organization owner, can be limited to
selected repositories, and last up to a year (or never). GitHub
recommends them over classic PATs for scripts, and Apps for long-lived
integrations.

**Substitute** for:

- Local REST reads on `Sannrox/rusui` and `Sannrox/shikigami` with
  Metadata, Contents, Issues, Pull requests, and (if keeping repo-hook
  reconcile) Webhooks: read.
- Git clone and push over HTTPS with Contents: write (and Workflows:
  write if pushing workflow files).
- HMAC intake of **repository** webhooks. That path does not need an
  App.

**Not a substitute** for:

- ADR 0001 D5 / P1.3: minting 1-hour, repo-and-permission-downscoped
  tokens from a private key so no long-lived secret enters an
  environment. A fine-grained PAT is itself the long-lived secret.
- App-native webhooks (`GET /app/hook/deliveries` is JWT-only;
  `installation` events).
- Identity that outlives the user (Apps do not consume a seat; a PAT
  dies if the user loses access).
- More than one resource owner per token; more than 50 fine-grained
  PATs per user.
- Checks API, Packages, contributing to public repos the user does not
  belong to, outside-collaborator org repos, user-owned Projects.

Classic PAT leftover: write to public repos the user does not own, and
a few REST endpoints that still reject fine-grained tokens. rusui does
not need those.

## Residual gaps after the P1 App

| Gap | Why | Mitigation |
| --- | --- | --- |
| No ref-prefix permission | Contents: write can push any ref | Plane git proxy (ADR 0001 D5) |
| IAT cannot list App webhook deliveries | JWT-only endpoints | Plane holds the private key; mint JWT for reconcile |
| IAT cannot list **repo** hook deliveries without Webhooks: read | Today's `RUSUI_GITHUB_HOOK_IDS` path | Grant Webhooks: read until P1.5, then delete repo hooks |
| 1-hour expiry | Documented IAT TTL | Mint on demand in the broker; never cache in the guest |
| Workflow files | Contents: write is not enough for `.github/workflows/**` | Grant Workflows: write only if dogfood must edit Actions |
| User-attributed writes | IAT is the bot | Out of P1; user-to-server token if ever required |
| Repos the App is not installed on | Installation scope | Install only the dogfood pair |

No PAT-only capability that rusui **uses today** is missing from an
IAT plus an App JWT, except the choice of which webhook object
reconcile reads (repo hook vs App hook).

## Sources

- rusui `origin/main`: `cmd/rusui/main.go`, `internal/gh/client.go`,
  `internal/gh/github.go`, `internal/server/server.go`, `README.md`
  (GitHub credentials), `ARCHITECTURE.md` (webhook intake, dry-run
  apply), `docs/decisions/0001-environment-plane.md` D5, `ROADMAP.md`
  P1.3 / P1.5.
- [Authenticating as a GitHub App installation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation)
- [Generating an installation access token](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)
- [Choosing permissions for a GitHub App](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app)
- [Using webhooks with GitHub Apps](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/using-webhooks-with-github-apps)
- [Types of webhooks](https://docs.github.com/en/webhooks/types-of-webhooks) (App webhook is one, undeletable)
- [Webhook events and payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads) (`issues` / `issue_comment` need Issues; `pull_request` needs Pull requests; `push` needs Contents)
- [REST API: GitHub App webhooks](https://docs.github.com/en/rest/apps/webhooks) (`GET /app/hook/deliveries` is JWT-only)
- [REST API: repository webhooks](https://docs.github.com/en/rest/repos/webhooks) (deliveries need repository Webhooks: read)
- [Permissions required for GitHub Apps](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps)
- [Deciding when to build a GitHub App](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/deciding-when-to-build-a-github-app)
- [Managing personal access tokens](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)
- [GitHub credential types](https://docs.github.com/en/organizations/managing-programmatic-access-to-your-organization/github-credential-types)
- [Differences between GitHub Apps and OAuth apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps) (git HTTPS via Contents + IAT)
