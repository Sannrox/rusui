# Upgrade, drain, and diagnostics

Use the `rusui` binary from a release or runtime image. A source checkout is
not required.

Supported profile: `linux-or-macos/one-runner/docker-or-podman-cli/rusui-guest/grok-acp/loopback`
([operator.md](operator.md)). Other combinations fail closed in
`rusui diagnose` / `GET /readyz`.

## Artifact identity

```bash
rusui -version
rusui diagnose -policy policy.yaml -addr 127.0.0.1:8080
```

Record binary version, commit, SQLite schema, guest image name, and
topology. Compare them to the candidate package before replacing files.

## Drain

Stop new claims, then wait for live leases to complete. Drain does **not**
cancel in-flight turns or delete receipts, approvals, or budgets.

```bash
rusui drain -token "$RUSUI_WORKER_SECRET"
# defaults to https://127.0.0.1:8080 when RUSUI_TLS_CERT and RUSUI_TLS_KEY are set
```

The JSON `live` list is the blocked/running work. Resume after the new
binary is up, or leave pause set until you explicitly resume:

```bash
rusui resume -token "$RUSUI_WORKER_SECRET"           # global
rusui resume -token "$RUSUI_WORKER_SECRET" -project rusui
```

Slack `resume [project]` does the same.

## Replace and recover

1. Drain.
2. Stop the process (SIGINT).
3. Copy `rusui.db` (and `-wal`/`-shm`) as the rollback artifact.
4. Replace the `rusui` / `rusui-runner` binaries or image tag.
5. Start with the same `-db` and `-policy`.
6. If start fails, stop it and restore the copied database with
   `rusui restore -db <copy>` (inventory, clear live leases, drop grants,
   release guests and rotate the plane id, keep receipts and approval
   **records**), then start on that file and `rusui resume`.

A schema newer than this binary, or an interrupted upgrade
(`schema_upgrade_lock`), fails closed. Restore the copy; do not retry
start on the half-upgraded file. Versions and checks:
[compatibility.md](compatibility.md).

On the PostgreSQL store, step 3 is a backup taken with the server's own
tooling, and step 6 restores that backup with the same tooling, then runs
`rusui restore` with all instances stopped and `RUSUI_STORE_URL` naming the
restored database. It applies the same steps there; start the instances on
that URL afterwards.

Durable session, approval, budget, and receipt rows stay in SQLite across
a successful upgrade. Live grants do not. Schema v50 defaults existing
sessions to short-lived retention (72h from `created_at`); expiry
removes prompt data and records `retention.expire`.

## PostgreSQL store

SQLite remains the single-host default. PostgreSQL backs the redundant
topology only ([ADR 0089](decisions/0089-redundant-plane.md)), which stays
outside the 1.0 core until
[#700](https://github.com/Sannrox/rusui/issues/700).

Set `RUSUI_STORE_URL=postgres://...` to use it. An empty database gets the
current schema on first start; a database at another schema version fails
closed. Prompt attachment bytes live in the store, so any instance can
serve an attachment another stored. The files beside `-db` are each
instance's cache: a missing file is rebuilt from the store, and files whose
rows retention removed are deleted once they are an hour old. On upgrade, each
instance copies the files it holds into rows stored without bytes; serve
the old files from the instance that stored them until then.

The server must commit each write synchronously to storage that survives
the loss of any one host before it acknowledges. Run with
`synchronous_commit=on` and at least one synchronous standby
(`synchronous_standby_names`). An asynchronous replica can lose an
acknowledged write or a lease generation and is not supported.

## Diagnostics

```bash
rusui diagnostics -db rusui.db -policy policy.yaml -out rusui-diagnostics
```

`-db` is opened **read-only** (no migrations). Prefer a stopped plane or a
copied file so the live process keeps the write lock. The bundle records
the on-disk schema and whether `pause:global` is already set.

Writes `diagnostics.json` and `exclusions.txt`. Secret values from the
environment are redacted. Guest workspace dirt and provider tokens are
excluded.

## Removal

Stop the process. Delete `rusui.db*` and the snapshot directory to drop
session truth. Unset operator env vars. Container leftovers (`rusui-*`)
are not in the database.
