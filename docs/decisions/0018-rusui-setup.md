# ADR 0018: `rusui setup` provisions a host; `rusui diagnose` verifies it

- Status: Accepted
- Date: 2026-09-23
- Related: [ADR 0009](0009-credential-broker.md) (plane TLS and CA),
  [ADR 0012](0012-operator-access.md) (operator token),
  [ADR 0017](0017-claude-guest-and-model-upstream.md) (guests and model
  upstream), [#93](https://github.com/Sannrox/rusui/issues/93)
  (`rusui diagnose`), [#118](https://github.com/Sannrox/rusui/issues/118)
  (operator journey).
- Discussion: none. GitHub Discussions are disabled. The maintainer chose
  this direction directly; merging with this status is the acceptance act.

## Context

Running rusui on a machine takes a manual runbook
([docs/operator.md](../operator.md)): a state directory, a policy file,
five generated secrets, plane TLS (a CA plus a certificate for `127.0.0.1`
and `rusui.plane`), a guest image, a container network, and a service that
keeps the plane running. `rusui diagnose` reports what is missing but
creates nothing. There is no in-tree guest image; the D6 proof ran with
`alpine` and no agent inside.

Preparing the self-test on rusui itself showed the cost: every step is
manual, and none is repeatable on another machine. The maintainer asked
whether an Ansible playbook, or something like it, should set up machines
so anyone can run rusui.

Mature platforms keep one source of truth for what a correct host looks
like, show a dry-run diff before changing anything, apply idempotently,
and verify with the same readiness check the product already uses.

## Decision

### D1. Setup belongs to rusui

`rusui setup plan` prints what would change on this machine and changes
nothing. `rusui setup apply` makes those changes, is safe to repeat, and
ends by running `rusui diagnose`; it exits non-zero while any blocker
remains. The knowledge of a correct host lives in the binary, next to
`diagnose`, and is tested by `make test`.

### D2. What setup manages

Under one state directory (default `$XDG_DATA_HOME/rusui`, or
`~/Library/Application Support/rusui` on macOS; `0700`):

| Item | Setup does | Never |
| --- | --- | --- |
| `rusui.db`, `snapshots/` | creates the paths | deletes data |
| plane TLS | creates a local CA and a server certificate with SANs `127.0.0.1`, `localhost`, and `rusui.plane` | overwrites existing files without `--rotate-tls` |
| `rusui.env` (`0600`) | writes non-secret settings; generates local random secrets (worker, webhook, operator token); lists external secrets as empty keys | writes GitHub tokens, model keys, or Slack tokens |
| `policy.yaml` | writes a skeleton when absent | overwrites an existing policy |
| guest image | builds the reference image from `build/guest-image` and records its tag | pulls unpinned images |
| container network | creates `rusui-trusted` | changes other networks |
| service | installs a launchd user agent (macOS) or systemd `--user` unit (Linux) that runs the plane with `rusui.env` on loopback | binds anything but loopback |

### D3. Secrets and interactive steps

Setup never prints secret values, not even in `plan`. External secrets
(GitHub token, model key, Slack token) and interactive logins (a CLI proxy
such as CLIProxyAPI, a tunnel account) are reported as **needs you**, with
the exact command to run. Setup does not perform OAuth logins itself.

### D4. Reference guest image

`build/guest-image` is the supported guest: a pinned Debian slim base with
`git`, `gh`, Node.js, and `@agentclientprotocol/claude-agent-acp` at the
ADR 0017 version. The plane CA is mounted at run time, not baked in, so
one image serves every installation. The recorded tag is derived from the
built image's ID, so it names the image's bits; the Dockerfile hash is
only a build cache key. The image is the environment snapshot's base
identity (ADR 0007).

### D5. Other machines and other tools

For a remote machine, run `rusui setup apply` on that machine (for
example over SSH). Configuration-management tools such as Ansible or a Nix
module may wrap `rusui setup apply`; they must not re-implement its
steps. Such wrappers are optional and deferred until more than one host
is in use.

### D6. Delivery slices

1. Setup core: `plan` / `apply`, state directory, TLS, `rusui.env`,
   policy skeleton, container network, missing-secret report, final
   `diagnose`.
2. Reference guest image under `build/guest-image`, built by setup.
3. Service unit (launchd and systemd `--user`).

The rusui self-test (ADR 0017 D5) runs on a host prepared by slices 1
and 2.

## Consequences

Easier: one command prepares a machine, and the same command on a second
machine gives the same result. The self-test also proves the setup path.

Harder: rusui now writes to the host (certificates, units, images) and
must stay idempotent across versions. Rotating TLS or upgrading the guest
image becomes an explicit setup action.

Security: generated secrets and keys are `0600` in a `0700` directory. The
CA key stays on the plane host and is never mounted into a guest; only
the CA certificate is.

## Rejected alternatives

- **Ansible playbook as the primary path.** Duplicates the definition of a
  correct host in a second language that drifts from the binary, cannot
  be tested by `make test`, and adds a toolchain every operator must
  learn.
- **Nix module only.** Fits some operators, excludes the rest.
- **A shell script in `scripts/`.** Untested and drifts the same way.
- **Documentation only.** The status quo: manual and not repeatable.

## Validation and reversal

Validation: on a clean machine, `rusui setup apply` followed by
`rusui diagnose` reports ready apart from the "needs you" secrets; a second
`apply` changes nothing. Reverse by superseding this ADR; the files setup
wrote remain valid manual configuration.

## Sources

- [docs/operator.md](../operator.md), `internal/ops/diagnose.go`
- [docs/proofs/d6-session-workflow.md](../proofs/d6-session-workflow.md)
- [ADR 0007](0007-environment-snapshot.md), [ADR 0009](0009-credential-broker.md),
  [ADR 0017](0017-claude-guest-and-model-upstream.md)
