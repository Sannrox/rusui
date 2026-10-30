# Compatibility (1.0)

The supported 1.0 core is
[ADR 0086](decisions/0086-supported-1.0-core-contract.md). This page is
the operator map: versions, failure behavior, and how to reproduce the
checks. The product contract remains
[ARCHITECTURE.md](../ARCHITECTURE.md).

## Version tuple

`rusui` and `rusui-runner` from the same git commit, schema
`schema_migrations` version of that commit, policy v2. Mixing commits
is unsupported. Release binaries send the commit on `POST /runners/hello`
and the plane returns 409 when they differ. Dev builds that report
`unknown` skip that check.

| Surface | 1.0 version | Unsupported input |
| --- | --- | --- |
| SQLite schema | current `schema_migrations` version (forward from implicit v1) | schema newer than the binary; interrupted upgrade |
| Policy | v2; unknown fields fail closed | v1 files; unknown fields |
| Operator HTTP+SSE | current handlers; omitted optional request fields keep working | new required fields are not added without a later ADR |
| Runner protocol | hello / claim / heartbeat / complete / fail | runner commit != plane commit |
| Restore | `rusui restore -db <copy>` | missing or corrupt file |
| Diagnostics export | redacted `diagnostics.json` | using the bundle as recovery input |
| Signed portable session bundles | excluded | not defined |
| Postgres / HA plane | excluded | not a 1.0 store |

## Migration window

The supported window is any database this binary can open:

- implicit v1 (jobs table, no `schema_migrations` row);
- every stamped version through the binary's current schema.

Upgrade is forward on start. There is no downgrade. Compatibility with
every historical development build is not claimed.

Before replacing binaries: drain, stop, copy `rusui.db` and `-wal`/`-shm`.
If start fails, restore that copy. An interrupted upgrade leaves
`schema_upgrade_lock` and the next start fails closed until you restore
the copy. A schema newer than the binary also fails closed.

## Restore and export

`rusui restore -db <copy>` keeps session ids, prompts, receipts, policy
revisions, approval **records**, budgets, and retention class. It clears
live leases, drops grants, expires managed guests, and rotates the plane
id. Guest handles and live credentials are not portable.

`rusui diagnostics` is an operator bundle with secrets redacted. Optional
`records/` JSON and Markdown files are regenerated exports, not recovery
inputs.

## Reproduce

From a source checkout at the candidate commit:

```bash
scripts/compat-conformance.sh
```

That runs the mapped tests and `rusui compat-conformance`, which prints
JSON bound to this binary's commit and schema. Package `internal/compat`
is the catalog. Positive and incompatible-input checks live next to the
code they cover. The independent HTTP+SSE client is
[independent-client.md](independent-client.md).

Record the printed `commit` and `schema` with the candidate you tested.
