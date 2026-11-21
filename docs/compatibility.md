# Compatibility

This page is the operator map of current topology: versions, failure
behavior, and how to reproduce the checks. It is not a freeze class.
The product contract remains [ARCHITECTURE.md](../ARCHITECTURE.md)
([ADR 0095](decisions/0095-drop-1.0-freeze-as-product-goal.md)).

## Version tuple

`rusui` and `rusui-runner` from the same git commit, schema
`schema_migrations` version of that commit, policy v2. Mixing commits
is unsupported. Release binaries send the commit on `POST /runners/hello`
and the plane returns 409 when they differ. Dev builds that report
`unknown` skip that check.

| Surface | Current version | Unsupported input |
| --- | --- | --- |
| SQLite schema | current `schema_migrations` version (forward from implicit v1); a newer schema whose floor this binary meets | schema newer than the binary's floor allows; interrupted upgrade |
| Policy | v2; unknown fields fail closed | v1 files; unknown fields |
| Operator HTTP+SSE | current handlers; omitted optional request fields keep working | new required fields are not added without a later ADR |
| Runner protocol | hello / claim / heartbeat / complete / fail | runner commit != plane commit |
| Restore | `rusui restore -db <copy>` | missing or corrupt file |
| Diagnostics export | redacted `diagnostics.json` | using the bundle as recovery input |
| Review receipt export | JSON+Markdown schema 1 (`rusui receipt`) | using the files as recovery input; signed bundles |
| Signed portable session bundles | HMAC-SHA256 `hmac-v1` (`rusui export-session` / `rusui import-session`) | missing key; bad MAC; missing, stripped, or past expiry; source not admitted |
| Postgres | not the current store | Postgres DSN |

## Migration window

The supported window is any database this binary can open:

- implicit v1 (jobs table, no `schema_migrations` row);
- every stamped version through the binary's current schema.

Upgrade is forward on start. Compatibility with every historical
development build is not claimed.

Each store records a readable floor in `schema_floor`: the oldest schema
version whose binary can still read and write it. A binary opens a newer
schema, without changing it, when its own version is at least that floor
and no upgrade is in progress. Otherwise it fails closed. An expand-only
migration, which only adds what older releases ignore, keeps the floor;
a contract migration, which removes, renames or changes the meaning of
anything an older release reads, raises it to its own version. So the
previous release serves beside the next one during a rolling upgrade and
alone after a rollback, until a contract step ships
([ADR 0089](decisions/0089-redundant-plane.md) D8). There is no
downgrade across a contract step. Releases from before the floor existed
refuse every newer schema.

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
`records/` JSON and Markdown files from `rusui receipt SESSION_ID`
(`GET /sessions/{id}/receipt`) are regenerated exports, not recovery
inputs. They are unsigned.

`rusui export-session` / `rusui import-session` move receipts and
transcript metadata between operator-controlled planes under
`RUSUI_SESSION_EXPORT_KEY`. Import mints a new session. It does not
restore leases, grants, terminals, or guest handles. `rusui restore`
stays plane recovery.

## Reproduce

From a source checkout at the candidate commit:

```bash
scripts/compat-conformance.sh
```

That runs the mapped tests and `rusui compat-conformance`, which prints
JSON bound to this binary's commit and schema. Package `internal/compat`
is the catalog. Positive and incompatible-input checks live next to the
code they cover. The script selects them from the catalog
(`rusui compat-conformance -list-tests`), and a renamed or deleted
mapped test fails `TestMappedTestsExist`. The independent HTTP+SSE client is
[independent-client.md](independent-client.md).

Operator HTTP+SSE response fields are pinned in
`internal/server/testdata/operator-api.golden.json`: create, detail,
list, read, follow events, and follow-up. A renamed, removed, or retyped
field fails the mapped check. Regenerate the file with
`go test ./internal/server -run TestOperatorAPIResponseShapes -update`
only for a change this contract allows.

## Tenkai product

`deploy/tenkai.toml` installs the release binaries (checked against the
release `SHA256SUMS`) through `rusui setup apply`, with state in the
Tenkai workdir. Its profile v2 block provides `rusui.admission` (session
create) and `rusui.environment` (the rest of the operator HTTP+SSE
surface), both `1.0.0`. Each version is derived from that contract's
part of the operator-API golden file, so a shape change fails
`TestTenkaiProfileIsCurrent` until `contractVersions` in
`internal/compat/tenkai_test.go` names the new version. A `VERSION`
bump fails the same test until the product version, the install
version, and the pin move with it
(`go test ./internal/compat -run TestTenkaiProfileIsCurrent -update`).

Record the printed `commit` and `schema` with the candidate you tested.
The C3 independent-review brief freezes one candidate in
[independent-review.md](independent-review.md).
