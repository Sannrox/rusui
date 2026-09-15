# Git smart-HTTP proxy: session repository and ref scope

- Status: research complete
- Date: 2026-09-15
- Resolves: [#38](https://github.com/Sannrox/rusui/issues/38)
- Product intent: ADR 0001 D5, ROADMAP P1.3 and assumption A3
- Verdict: **mixed**

## Verdict

A plane-owned smart-HTTP proxy can enforce D5 on the Git path: restrict
clone and fetch to a session’s repository set, and reject pushes whose
command list names any ref outside `refs/heads/rusui/<session>/*`.
GitHub App installation tokens can also pin the upstream credential to
that repository set, but they cannot pin it to a branch prefix. GitHub
does not publish a smart-HTTP wire spec; it documents HTTPS Git
authenticated with an installation token and, separately, protocol v2
support for fetch. The owning source for the bytes on the wire is Git’s
HTTP and pack protocols. Those protocols put every ref update in a
plaintext pkt-line command list (or push certificate) *before* the
`PACK` stream. That is reliable enough for P1 if the proxy fail-closes
on parse error and refuses the whole POST when any command is outside
the allowlist. The documented fallback (GitHub rulesets plus post-push
verification) is **not required from day one** to make D5 true on the
Git path, and it is **not a substitute** for per-session ref isolation.
Keep it as defense in depth against a leaked installation token and
against GitHub REST/Contents updates that never touch receive-pack.

## Question

Can a plane-owned git smart-HTTP proxy, exchanging a per-turn credential
for a GitHub App installation token, restrict clones and fetches to a
session’s repository set and reject pushes outside
`refs/heads/rusui/<session>/*`?

What does GitHub’s smart HTTP protocol actually send on
`git-receive-pack` / `git-upload-pack`? Is ref advertisement plus
receive-pack parsing reliable enough for P1, or is the documented
fallback (GitHub rulesets plus post-push verification) required from
day one?

This note answers feasibility against primary sources. It does not
design the credential broker.

## Product intent (not redesigned)

ADR 0001 D5: no secret is written into an environment; GitHub App
installation tokens replace PATs; a git smart-HTTP proxy inside the
plane exchanges a per-turn scoped credential for the real token,
restricts repositories to the session’s set, and rejects pushes to refs
outside `refs/heads/rusui/<session>/*`.

ROADMAP P1.3 success measure: no long-lived token in any environment;
push outside that prefix rejected by the proxy. Kill signal: receive-pack
parsing unreliable, then fall back to GitHub rulesets plus post-push
verification.

ROADMAP A3: GitHub App tokens plus a git smart-HTTP proxy can enforce
repository and branch scope; if false, fall back to rulesets and
post-push verification and record the residual risk.

P1 dogfood repositories named in the ticket: `Sannrox/rusui` (public)
and `Sannrox/shikigami` (private).

## What GitHub actually sends

GitHub Docs do not describe pkt-lines, `want`/`have` negotiation, or
the receive-pack command list. They document HTTPS clone URLs
(`git clone https://github.com/OWNER/REPOSITORY`) and that an App
installation token is the HTTP password for Git, with username
`x-access-token`, provided the App has the Contents repository
permission.

GitHub’s 2018 changelog states that GitHub.com supports Git wire
protocol v2, with the immediate benefit of server-side reference
filtering on fetch. That announcement is about fetch, not push.

The wire format therefore belongs to Git:

| Direction | HTTP | Body |
| --- | --- | --- |
| Discover refs for fetch (v0/v1) | `GET $GIT_URL/info/refs?service=git-upload-pack` | pkt-line advertisement: `# service=git-upload-pack`, then refs, capabilities behind a NUL on the first ref, flush-pkt |
| Discover refs for push (v0/v1) | `GET $GIT_URL/info/refs?service=git-receive-pack` | same shape; receive-pack capabilities (`report-status`, `delete-refs`, `ofs-delta`, `atomic`, `push-options`, `push-cert`, …) |
| Fetch objects (v0/v1) | `POST $GIT_URL/git-upload-pack` | `Content-Type: application/x-git-upload-pack-request`; pkt-line `want` / `have` / `done`; response is ACK/NAK then a pack |
| Push objects (v0/v1) | `POST $GIT_URL/git-receive-pack` | `Content-Type: application/x-git-receive-pack-request`; command list (or push-cert), optional push-options, then `PACK` |
| Protocol v2 probe | same `GET info/refs?service=…` plus `Git-Protocol: version=2` | `version 2` and a capability list; **no ref advertisement** unless the client later runs `ls-refs` |

Protocol v2 was designed so an HTTP helper can act as a proxy with
clear flush semantics. Its documented commands are `ls-refs`, `fetch`,
and `object-info`. It does not define a push command. Subsequent v2
requests go to `$GIT_URL/git-upload-pack` (and the spec says the same
pattern applies to `git-receive-pack`). Current Git still pushes with
the v0/v1 receive-pack command list even when `protocol.version` is 2.

`git-http-backend` is Git’s CGI for this: smart fetch, smart push, and
v2 when `GIT_PROTOCOL` is populated from the `Git-Protocol` header.
It is stateless: `--stateless-rpc` is one read/write cycle, which
matches one HTTP POST.

GitHub is an HTTPS Git host that authenticates with tokens and speaks
protocol v2 on fetch. A proxy in front of `https://github.com/owner/repo.git`
therefore sees the Git smart-HTTP requests above, not a GitHub-specific
RPC.

## Repository restriction: proxy-enforceable and token-enforceable

Smart HTTP appends `/info/refs`, `/git-upload-pack`, and
`/git-receive-pack` onto `$GIT_URL`. The session’s repository set is
visible in that URL (`https://github.com/<owner>/<repo>.git`). The
proxy can allow only those paths and return 403/404 for anything else,
which is what Git’s HTTP spec requires when a repository is missing or
access is denied.

Independently, `POST /app/installations/{installation_id}/access_tokens`
accepts `repositories` or `repository_ids` (up to 500) and optional
`permissions`. The token cannot be granted repositories or permissions
the installation does not have. Tokens expire in one hour. Used as the
HTTPS password, a token scoped to the session’s repos is refused by
GitHub for any other private repository even if the proxy were bypassed.

So repository scope for clone/fetch/push is enforceable twice: URL
allowlist on the proxy, and `repository_ids` on the minted token. D5’s
“restricts repositories to the session’s set” does not require parsing
pack data.

Public `Sannrox/rusui` can be cloned anonymously from github.com without
the proxy. That is not a credential leak. Push still needs a token.

## Ref restriction: proxy-enforceable, not token-enforceable

The access-token API’s body is `repositories`, `repository_ids`, and
`permissions`. There is no branch or ref-prefix parameter. Contents
write covers “repository contents, commits, branches, downloads,
releases, and merges” for every repository the token can access. An
installation token that can push can push any ref GitHub will accept
for that repo.

Ref scope therefore cannot be a property of the GitHub credential. It
has to be a property of the proxy (or of GitHub rulesets, which are a
different mechanism; see below).

### What receive-pack sends, and why parsing it is enough

After ref discovery, the client POSTs a command list, then the pack:

```
command-list = PKT-LINE(command NUL capability-list)
               *PKT-LINE(command)
               flush-pkt
command      = create / delete / update
create       = zero-id SP new-id SP name
delete       = old-id  SP zero-id SP name
update       = old-id  SP new-id  SP name
```

If `push-options` was advertised and requested, push-option pkt-lines
and another flush-pkt follow the commands and precede the pack. If
`push-cert` is used, the commands live inside the certificate instead
of a bare command-list; they are still pkt-lines of
`old-id SP new-id SP name`.

The packfile is the literal bytes `PACK` plus binary data. It is not
sent for delete-only pushes. It does not name refs. `git-receive-pack`
updates only the refs in the command list (or certificate), after
unpacking and running hooks.

A proxy that:

1. reads pkt-lines until flush-pkt (and the optional push-options
   block),
2. takes every `name` from `command` or from the push-cert command
   block,
3. allows the POST through only if every name is under
   `refs/heads/rusui/<session>/`,
4. fail-closes on unknown framing, truncated length prefixes, missing
   flush-pkt, or a body that starts at `PACK` with no commands,

knows the refs GitHub will update **before** it forwards the pack.
It does not need to parse the pack. It must reject the entire POST if
any command is out of scope: Git applies every command in the request
(unless `atomic`, in which case all succeed or all fail).

Pkt-lines are 4 hex digits of length including those four bytes,
payload up to 65516 bytes, 8-bit clean. Flush-pkt is `0000`. That is
enough to frame commands without interpreting pack objects.

`git-receive-pack`’s own pre-receive hook sees the same
`old SP new SP refname` lines, but github.com does not offer custom
pre-receive hooks. The proxy is the pre-receive equivalent for
github.com.

### Fetch does not need ref allowlisting for D5

D5 restricts clone/fetch by repository, not by ref. Upload-pack `want`
lines are object IDs. Protocol v2 `ls-refs` can take `ref-prefix`, but
the server MAY still show non-matching refs; clients must filter.
Filtering the advertisement is optional product behavior, not required
for the security claim.

## GitHub rulesets and post-push verification

Rulesets can restrict creations, updates, and deletions on branches
matching an `fnmatch` pattern (`*` does not cross `/`; `qa/**/*`
matches nested names). Bypass actors include GitHub Apps. Targeting
supports include and exclude patterns on the same ruleset.

A ruleset that targets all branches except `rusui/**/*`, with restrict
creations/updates/deletions, and with the App **not** a bypass actor,
stops the App pushing to `main` (and to any other non-`rusui/` branch)
even if the installation token is used directly against github.com.
Repository admins can be bypass actors so the maintainer can still
push to `main`.

That is class-level (`rusui/**`), not per-session
(`rusui/<session>/*`). Rulesets are static. They cannot isolate two
concurrent sessions on the same repository without rewriting the
ruleset every session.

Availability: public repositories on GitHub Free; public **and
private** repositories on GitHub Pro, Team, and Enterprise Cloud.
Push rulesets (file path/size/extension) are a Team/Enterprise feature
and do not target branches. For dogfood, rulesets apply to public
`Sannrox/rusui` on Free; private `Sannrox/shikigami` needs Pro or
above.

Post-push verification is possible: `push`, `create`, and `delete`
webhooks carry the git ref. They fire after GitHub has accepted the
update. Deleting a bad ref is remediation, not prevention, and leaves
objects in the repo until GC.

So the fallback is useful belt-and-suspenders for “do not push
`main`”. It does not implement D5’s per-session prefix, and it is not
required to make receive-pack parsing work.

## Paths that never hit receive-pack

These are outside the Git proxy. D5 already names an API proxy. They
are listed so a later grilling ticket does not treat receive-pack
parsing as the whole credential story.

| Path | Why it matters | Source |
| --- | --- | --- |
| `POST/PATCH/DELETE /repos/{owner}/{repo}/git/refs` | Creates, force-updates, or deletes any ref with Contents write | Git References REST |
| `PUT /repos/{owner}/{repo}/contents/{path}` | Commits a file on `branch` (default: default branch) | Contents REST |
| Git LFS batch API | Separate HTTPS protocol; rusui itself has no LFS attributes | GitHub LFS docs; this tree |
| SSH `git@github.com` | Bypasses the HTTP proxy; do not put deploy keys in the environment | GitHub clone docs (SSH URL) |
| Dumb HTTP (`GET $GIT_URL/info/refs` without `service=`) | Git clients fall back if Content-Type is wrong; GitHub is a smart server; the proxy must not serve dumb object URLs | gitprotocol-http |

Workflow files under `.github/workflows` also need the Workflows
permission, not only Contents.

## Reliability for P1

ROADMAP’s kill signal is “receive-pack parsing unreliable”. Against
the owning protocol documents, it is not. The ref names are specified
to appear as pkt-lines before `PACK`. Fail-closed parsing is a
correctness property of the proxy, not an unknown of GitHub’s protocol.

What this research did **not** do: capture a live github.com
receive-pack trace. GitHub does not publish one. A P1.3 spike can
confirm that github.com’s POST body matches the grammar (including
whether it advertises `push-options` / `push-cert` / protocol v2 on
receive-pack). That is confirmation, not a reason to start on the
fallback.

Assumption A3, split:

| Claim | Holds? |
| --- | --- |
| App tokens can enforce repository scope | Yes (`repositories` / `repository_ids`) |
| App tokens can enforce branch scope | No |
| A smart-HTTP proxy can enforce repository scope | Yes (URL path) |
| A smart-HTTP proxy can enforce `refs/heads/rusui/<session>/*` on push | Yes (command-list / push-cert names), fail-closed |
| Rulesets plus post-push can replace per-session proxy enforcement | No |

## Facts a later grilling ticket can take

1. GitHub has no smart-HTTP wire spec. Cite Git’s
   `gitprotocol-http` / `http-protocol`, `gitprotocol-pack`,
   `gitprotocol-v2`, `gitprotocol-common`, `git-http-backend`, and
   `git-receive-pack`. Cite GitHub only for tokens, Contents, HTTPS
   clone URLs, protocol v2 support, rulesets, and REST.
2. Clone/fetch/push over HTTPS to github.com are smart-HTTP
   `info/refs` + `git-upload-pack` / `git-receive-pack`. Authenticate
   upstream as `x-access-token` plus an installation token. Contents
   is required; Workflows is required to touch `.github/workflows`.
3. Mint installation tokens per turn (or refresh inside the hour)
   with `repository_ids` limited to the session set and `permissions`
   limited to what the turn needs. Never write that token into the
   environment; redeem it only inside the proxy.
4. Parse receive-pack POSTs as pkt-lines until flush-pkt, then
   optional push-options, then `PACK`. Allow only
   `refs/heads/rusui/<session>/*`. Reject the whole request on any
   other name or on parse failure. Do not inspect the pack to decide
   refs.
5. Handle protocol v2 on upload-pack (`ls-refs` / `fetch` after a
   capability advertisement). Do not expect a v2 push command; still
   parse v0/v1 command lists on receive-pack. Fail-closed if a
   receive-pack POST is not that grammar.
6. Do not serve dumb HTTP. Do not put SSH keys in the environment.
7. The Git proxy does not cover REST git refs or Contents-API
   commits. Those belong to the API proxy.
8. Rulesets can lock the App out of non-`rusui/**` branches as
   defense in depth. They cannot express per-session prefixes.
   Private-repo rulesets need GitHub Pro or above. Post-push
   webhooks are after the fact.
9. The P1.3 kill signal is not fired by these sources. A live
   github.com capture is still a reasonable implementation spike;
   it is not a reason to ship fallback-only.

## Sources

Git (owning the wire format):

- [gitprotocol-http / http-protocol](https://git-scm.com/docs/http-protocol) — URL layout, Basic auth, smart vs dumb, `info/refs?service=`, POST `git-upload-pack` / `git-receive-pack`, command-list grammar on HTTP
- [gitprotocol-pack](https://git-scm.com/docs/gitprotocol-pack) — pkt-line ref advertisement, upload-pack negotiation, receive-pack command-list / push-cert / push-options / PACK, report-status
- [gitprotocol-v2](https://git-scm.com/docs/protocol-v2) — `Git-Protocol: version=2`, capability advertisement without refs, commands `ls-refs` / `fetch` / `object-info`, HTTP subsequent POST to `git-upload-pack` (same for `git-receive-pack`), designed for HTTP proxies
- [gitprotocol-common](https://git-scm.com/docs/gitprotocol-common) — pkt-line length prefix, flush-pkt `0000`, refname rules
- [protocol-capabilities](https://git-scm.com/docs/protocol-capabilities) — receive-pack capabilities including `push-options` and `push-cert`
- [git-http-backend](https://git-scm.com/docs/git-http-backend) — smart fetch and push CGI, `GIT_PROTOCOL` / `Git-Protocol`, service enablement
- [git-upload-pack](https://git-scm.com/docs/git-upload-pack) — `--stateless-rpc`, `--http-backend-info-refs`
- [git-receive-pack](https://git-scm.com/docs/git-receive-pack) — pre-receive sees `old SP new SP refname`; github.com does not expose this hook
- [git-config `protocol.version`](https://git-scm.com/docs/git-config) — default 2; 0/1/2

GitHub (owning tokens, Git HTTPS auth, rulesets, REST, webhooks):

- [Authenticating as a GitHub App installation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation) — installation token as HTTPS Git password; `x-access-token:TOKEN@github.com`; 1-hour expiry; `repositories` / `repository_ids` / `permissions`
- [Generating an installation access token](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)
- [REST: Create an installation access token](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app) — body parameters; Contents permission enum
- [Choosing permissions for a GitHub App](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app) — Contents for HTTP Git; Workflows for `.github/workflows`
- [Cloning a repository](https://docs.github.com/en/repositories/creating-and-managing-repositories/cloning-a-repository) — HTTPS URL `git clone https://github.com/…`
- [Token authentication requirements for Git operations](https://github.blog/security/application-security/token-authentication-requirements-for-git-operations/) — GitHub App installation tokens are valid Git credentials on github.com
- [Git wire protocol v2 support](https://github.blog/changelog/2018-11-08-git-protocol-v2-support/) — GitHub.com speaks protocol v2 (fetch / ref filtering)
- [About rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets) — branch/tag targeting, App bypass, plan availability
- [Available rules for rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets) — restrict creations/updates/deletions
- [Creating rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository) — `fnmatch`, include/exclude, App bypass
- [REST Git references](https://docs.github.com/en/rest/git/refs) — create/update/delete any ref
- [REST repository contents](https://docs.github.com/en/rest/repos/contents) — create/update file on a chosen `branch`
- [Webhook events and payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads) — `push` / `create` / `delete` carry git refs after the fact

Product (intent only):

- [ADR 0001 D5](../decisions/0001-environment-plane.md)
- [ROADMAP P1.3 and A3](../../ROADMAP.md)

## Not claimed

- Exact github.com capability strings or a captured receive-pack POST.
  GitHub does not publish them; a P1.3 spike can dump one.
- That GitHub will never add a protocol-v2 push command. The current
  spec has none; fail-closed parsing covers a surprise.
- That rulesets on private `Sannrox/shikigami` work on GitHub Free.
  They need Pro or above.
- Design of the per-turn credential, token cache, or HTTP server.
- Git LFS policy. This tree has no LFS attributes; LFS is a different
  HTTPS API if a later dogfood repo uses it.
