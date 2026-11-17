# ADR 0090: Review receipts count evidence by class and cite the claim-time policy

- Status: Accepted
- Date: 2026-11-17
- Amends: [ADR 0086](0086-supported-1.0-core-contract.md) (portable
  turn-receipt fields).
- Resolves: [#661](https://github.com/Sannrox/rusui/issues/661).
- Related: [docs/pull-request-review.md](../pull-request-review.md),
  [docs/compatibility.md](../compatibility.md).
- Discussion: none. GitHub Discussions are disabled. Merging with this
  status is the acceptance act.

## Context

ADR 0086 lists "policy revision id" and "evidence class (`server_verified`
or `model_assertion`)" as portable turn-receipt fields. The receipt export
had two problems:

- It cited the plane's newest policy revision, not the one the review ran
  under. A receipt exported again after a policy reload changed for the
  same immutable review.
- It reported one top-level class, the strongest across the review. One
  server-verified action labelled a review that rested mostly on model
  assertions.

The plane also records a third class, `plane_observed`, for guest requests
it saw over ACP. ADR 0086 does not define it.

## Decision

1. Three evidence classes. `server_verified`: the plane checked the claim
   against GitHub before recording it. `plane_observed`: the plane saw and
   recorded a guest request; this proves the request happened, not what it
   claims. `model_assertion`: the guest claimed it; it is stored and never
   treated as proof.
2. A receipt carries each bound action's class and `evidence_summary`. The
   summary counts the review's bound action rows by the class the plane
   recorded: `server_verified`, `plane_observed` and `model_assertion`.
   Evidence labels inside the guest's artifact are not counted; a guest
   cannot mark its own claim `server_verified`. There is no top-level
   `evidence_class`.
3. `policy_revision_id` and `policy_revision` name the policy the turn was
   claimed under. The claim records it on the turn, and the stored review
   copies it. Reviews stored before this binding omit both fields.
4. Receipt JSON v1 fields: `schema_version`, `recovery_input`, `signed`,
   `session_id`, `turn_id`, `review_revision_id`, `lease_generation`,
   `claimed_revision`, `policy_revision_id` and `policy_revision` (omitted
   when unknown), `repo`, `item`, `item_kind`, `source_git_pin`,
   `guest_name`, `guest_pin` and `guest_session_id` (omitted when empty),
   `verdict`, `evidence_summary`, `usage` (omitted when not reported),
   `github_action`, and `actions` (`action_id`, `type`, `reason_code`,
   `evidence_class`, `limit_sentence`, `body`).

## Consequences

- Schema 57 adds nullable `policy_revision_id` columns to `turns` and
  `review_revisions`. It is forward-only like every migration; roll back by
  restoring the pre-upgrade copy.
- Receipt JSON v1 drops the top-level `evidence_class`. 1.0 has not been
  released, so the v1 field list is fixed here, not versioned again.
- A reviewer reads the evidence mix from the counts instead of one label.

## Rejected alternatives

- Fold `plane_observed` into `model_assertion` to keep two classes. That
  hides that the plane, not the guest, recorded the row.
- Report the weakest class at the top. One label still hides the mix.
- Count the guest's proposed evidence labels too. The old strongest-class
  label did, which let a guest raise its own label.
- Cite the newest policy revision recorded before the review was stored.
  A reload during a turn would still mislabel it.

## Validation and reversal

- `TestReceiptCitesPolicyTheReviewWasClaimedUnder` claims a review, reloads
  policy, completes the review, and expects the earlier revision.
- `TestExportReceiptCountsEvidenceInsteadOfTakingTheStrongest` and
  `TestExportReceiptOmitsUnknownPolicyRevision` cover the summary and the
  omitted binding.
- Reversal is a new ADR. The columns stay.

## Sources

- [#661](https://github.com/Sannrox/rusui/issues/661).
