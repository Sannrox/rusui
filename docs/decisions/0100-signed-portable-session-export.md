# ADR 0100: Signed operator session export and import

- Status: Accepted
- Date: 2026-11-20
- Amends: [ADR 0086](0086-supported-1.0-core-contract.md) (the
  freeze-time deferral of signed portable session or evidence bundles).
  ADR 0086 remains the historical freeze record and stays superseded by
  [ADR 0095](0095-drop-1.0-freeze-as-product-goal.md).
- Resolves: [#788](https://github.com/Sannrox/rusui/issues/788)
- Related: [ARCHITECTURE.md](../../ARCHITECTURE.md) (product contract;
  this ADR does not rewrite it),
  [ADR 0082](0082-signed-conformance-receipts.md) (HMAC-SHA256 under a
  plane-held key; conformance receipts stay a different object),
  [ADR 0090](0090-receipt-evidence-and-policy-binding.md) (portable
  receipt fields),
  `rusui diagnostics` (redacted operator bundle, not a signed receipt),
  `rusui receipt` (unsigned JSON and Markdown, not a recovery input),
  `rusui restore` (SQLite copy that strips live authority),
  [#248](https://github.com/Sannrox/rusui/issues/248) (unsigned review
  receipts),
  [#833](https://github.com/Sannrox/rusui/issues/833) (implementation).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act. Until merge this is a proposal, not
  implementation authority. This ADR does not ship a bundle format.

## Context

[ADR 0086](0086-supported-1.0-core-contract.md) deferred signed portable
session or evidence bundles: no key handling, expiry, or export format
was defined for them. `rusui diagnostics` is an operator bundle with
secrets redacted, not a signed receipt another plane can import.
`rusui receipt` is an unsigned copy of one stored review. An operator
cannot export a session's transcript, receipts, and git pin as a
verifiable bundle, or import one.

[#788](https://github.com/Sannrox/rusui/issues/788) reopened the
question. Two options:

1. Adopt signed operator export. Export is operator-only, signed with a
   plane-held key, contains receipts and transcript metadata, and
   excludes live grants, tokens, and workspace bytes by default. Import
   is a new session on this plane only if the signature verifies and
   policy admits the source; it does not restore leases.
2. Retain the ADR 0086 deferral with a dated reason.

The objects this choice binds already exist:

| Object | Owner | What it already does |
| --- | --- | --- |
| Immutable receipts | SQLite | turn and review rows; evidence classes; git pin ([ADR 0090](0090-receipt-evidence-and-policy-binding.md)) |
| `rusui receipt` | operator CLI | unsigned JSON and Markdown; `signed: false`; not a recovery input |
| `rusui diagnostics` | operator CLI | redacted `diagnostics.json`; secrets stripped; not a signed receipt |
| Conformance receipt | plane | HMAC-SHA256 under `RUSUI_CONFORMANCE_KEY`; qualifies a generic ACP guest on this plane ([ADR 0082](0082-signed-conformance-receipts.md)) |
| `rusui restore` | operator CLI | copies the store; clears live leases; drops grants; rotates the plane id |
| Workspace export | environment | git bundle of workspace bytes; not evidence; not signed |

Source evidence at the commit this decision was made against
(`251e31e8a7acb896ddb54756d81d1c060199fd55`):

- `Export` sets `Signed: false` and `RecoveryInput: false`
  (`internal/receipt/receipt.go`). Markdown still says signed portable
  session bundles are not in the 1.0 contract.
- `WriteBundle` redacts secret values and records exclusions including
  guest workspace dirt and provider tokens (`internal/ops/bundle.go`).
- `signed-portable-session-bundle` is `ClassExcluded`
  (`internal/compat/compat.go`).
- Conformance HMAC is a dedicated plane-held key, never the operator
  token or worker secret (`internal/guest/guest.go` `SignReceipt` /
  `Verify`; [ADR 0082](0082-signed-conformance-receipts.md) D2).
- `Restore` clears leases, drops grants, expires managed guests, and
  rotates the plane id (`internal/store/restore.go`). Live authority
  does not survive a copy.

The named job is to move a session's evidence between operator-controlled
planes without copying live authority. Diagnostics is the wrong format
for that job: it is unsigned, plane-scoped, and not importable as a
session. Restore is the wrong import: it recovers one plane's store, not
one session's evidence.

## Decision

**D1. Adopt signed operator export.** rusui may export one session as a
signed bundle and import that bundle as a **new** session. This ADR
does not retain the ADR 0086 deferral. Dated reason, 2026-11-20: the
operator need is named (move receipts and transcript metadata without
live authority), and the signing, redaction, and restore-strip objects
already exist.

**D2. Operator-only.** Export and import are operator verbs. A guest,
runner, or unauthenticated caller is refused. Guests never receive a
core credential. Slack allowlisted users stay notify and approve, not
exporters.

**D3. What the bundle contains.** Default contents are receipts,
transcript metadata, and the source git pin. Transcript metadata is
session and turn ids, seq bounds, timestamps, and guest identity, not
prompt bodies. Live grants, tokens, workspace bytes, guest handles, and
terminal leases are absent by default. Secret values are redacted as
`rusui diagnostics` already redacts them.

**D4. Dedicated plane-held HMAC key.** The plane signs with HMAC-SHA256
under a dedicated key it holds, the same pattern as
[ADR 0082](0082-signed-conformance-receipts.md) D2, not the same secret.
The key is not `RUSUI_CONFORMANCE_KEY`, the operator token, the worker
secret, or a TLS key. It never travels in the bundle, `rusui read`, hook
logs, or transcripts. The bundle is operator-authenticated: the operator
requests export and import; the signature proves this plane produced the
bytes. A destination verifies with a key the operator already installed
on that plane. Copying the key is an operator secret install, not a
bundle field.

**D5. Expiry is a signed field.** A bundle names an expiry inside the
signed bytes. Missing, stripped, or past expiry is refused. Conformance
receipts do not expire because they stay on the plane (ADR 0082 D5);
this object leaves the plane, so expiry is required.

**D6. Import is a new session, not a restore.** Import succeeds only
when the signature verifies, the expiry holds, and policy admits the
source. It mints a new session on the importing plane. It does not keep
the source session id, restore leases, restore grants, restore terminal
leases, restore guest handles, or call `rusui restore`. Overlay may only
narrow which sources are admitted. Unknown fields fail closed. A plane
without the key refuses import and export.

**D7. Not plane recovery and not a marketplace.** SQLite backup and
`rusui restore` stay the path that recovers one plane. This object does
not replace them. There is no marketplace of session bundles, no unsigned
supported export, and no public catalog.

**D8. This ADR is the decision, not the format.** No CLI verb, env name,
schema, or byte layout ships here. [#833](https://github.com/Sannrox/rusui/issues/833)
implements D1–D7, including the format.

## Consequences

Easier: an operator can move a session's evidence between planes they
control without copying live grants, tokens, or workspace bytes. Signing
and redaction reuse HMAC receipts and diagnostics. Import cannot revive
a lease.

Harder: a dedicated HMAC key to generate, store, and (when two planes
must verify the same bundles) install; a signed expiry; a policy admit
of the source; a new session on import rather than a copied id. Schema,
CLI verbs, and the byte layout belong to the follow-up.

Irreversible: none until the follow-up writes rows or files. After
export, rollback stops new exports; outstanding bundles still expire.
After import, the new session is an ordinary session and follows
retention.

[ARCHITECTURE.md](../../ARCHITECTURE.md) still lists signed portable
session bundles among deferred tracks. This ADR is not that rewrite, and
it does not ship a bundle format.

## Threat examples

| Attempt | Result |
| --- | --- |
| Guest exports or imports a bundle | Refused. D2. Guests never receive a core credential. |
| Bundle includes live grants, tokens, or workspace bytes by default | Refused. D3. |
| Sign with the conformance key, operator token, or worker secret | Refused. D4. Dedicated key. |
| Key copied into the bundle | Refused. D4. |
| Import after stripping or omitting expiry | Refused. D5. Expiry is signed. |
| Import an expired bundle | Refused. D5. |
| Import without the matching key | Refused. D4, D6. |
| Import a source policy does not admit | Refused. D6. Overlay cannot add it. |
| Import restores the source session id, lease, grant, or terminal | Refused. D6. New session. |
| Treat diagnostics or `rusui receipt` as the signed bundle | Refused. Those stay unsigned copies. D1, D7. |
| Replace `rusui restore` with bundle import | Refused. D7. |
| Marketplace or unsigned public catalog of session bundles | Refused. D7. |
| Unsigned bundle as the supported path | Refused. D1, D4. |

## Rejected alternatives

- **Retain the ADR 0086 deferral.** Dated reason this ADR does not take
  it: 2026-11-20, the operator need is named, and receipts, redaction,
  and HMAC signing already exist. Deferral would keep an unsigned gap
  the operator cannot close without copying the database.
- **Treat `rusui diagnostics` as the portable format.** Unsigned, not a
  session, not importable. Redaction stays; the format does not.
- **Treat `rusui receipt` as the signed bundle.** One review, unsigned,
  `recovery_input: false`. The follow-up may reuse its fields; this ADR
  does not set `signed: true` on that export.
- **Import by `rusui restore`.** Restore recovers one plane and already
  strips live authority. It is not one session's evidence and it is not
  a new session mint.
- **Include workspace bytes by default.** Workspace export is a different
  object. Default evidence must not carry the tree.
- **Restore live grants or terminal leases.** Issue non-goal. Live
  authority does not travel.
- **Reuse `RUSUI_CONFORMANCE_KEY`.** Couples guest qualification to
  bundle validity. ADR 0082 D2 already forbids reusing an existing
  secret.
- **Sign on the operator's machine, or with the plane TLS key.** The
  signing key stays on the plane. TLS renewal must not invalidate
  bundles (ADR 0082 rejected that for receipts).
- **Asymmetric keys or a public catalog.** HMAC is the existing plane
  pattern. A marketplace is an issue non-goal.
- **Keep the source session id on import.** Collides with a live session
  and implies restored identity. Import mints a new session.

## Validation and reversal

Validation: this ADR is Accepted in the index; ADR 0086 lists this
amendment; `rusui receipt` remains unsigned; `signed-portable-session-bundle`
stays excluded until [#833](https://github.com/Sannrox/rusui/issues/833)
implements D1–D7. Reverse by a superseding ADR that restores the
deferral or names a different signer; do not delete this file.
Outstanding bundles expire; imported sessions remain ordinary sessions.

## Sources

- [#788](https://github.com/Sannrox/rusui/issues/788)
- [#833](https://github.com/Sannrox/rusui/issues/833)
- [#248](https://github.com/Sannrox/rusui/issues/248)
- [ADR 0082](0082-signed-conformance-receipts.md),
  [ADR 0086](0086-supported-1.0-core-contract.md),
  [ADR 0090](0090-receipt-evidence-and-policy-binding.md),
  [ADR 0095](0095-drop-1.0-freeze-as-product-goal.md)
- `internal/receipt/receipt.go` `Export`;
  `internal/ops/bundle.go` `WriteBundle`, `SecretValues`;
  `internal/guest/guest.go` `SignReceipt`, `Verify`;
  `internal/store/restore.go` `Restore`;
  `internal/compat/compat.go` `signed-portable-session-bundle`
- Commit `251e31e8a7acb896ddb54756d81d1c060199fd55`
