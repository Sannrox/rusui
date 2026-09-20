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
rusui drain -url http://127.0.0.1:8080 -token "$RUSUI_WORKER_SECRET"
```

The JSON `live` list is the blocked/running work. Resume with Slack
`resume` after the new binary is up, or leave pause set until you
explicitly resume.

## Replace and recover

1. Drain.
2. Stop the process (SIGINT).
3. Copy `rusui.db` (and `-wal`/`-shm`) as the rollback artifact.
4. Replace the `rusui` / `rusui-runner` binaries or image tag.
5. Start with the same `-db` and `-policy`.
6. If start fails, restore the copied database with the shipped
   `store.Restore` path (inventory, clear live leases, drop grants,
   keep receipts and approval **records**).

Durable session, approval, budget, and receipt rows stay in SQLite across
a successful upgrade. Live grants do not.

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
