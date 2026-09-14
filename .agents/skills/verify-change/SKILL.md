---
name: verify-change
description: Verify a rusui Go, policy, documentation, configuration, or workflow change with proportionate deterministic checks. Use after implementation, before review, or when a contributor needs an exact evidence report without overstating unrun tests.
---

# Verify Change

Run the narrowest useful checks first, then expand according to change risk.
Verification produces an evidence record, not only a list of commands. Every
claim must identify the source revision, the behavior covered, and any limits
that remain.

## Procedure

1. Inspect `git status`, the diff, and the stated outcome. Preserve unrelated
   worktree changes. Record the full `git rev-parse HEAD`, the checkout path,
   and `git status --porcelain=v1 --untracked-files=all --ignore-submodules=none`
   before checking behavior. Use `assess-change-impact` when risk is unclear.
   A dirty implementation worktree is allowed, but record which paths are
   intentional; identify an uncommitted candidate as `HEAD` plus that
   intentional diff rather than as an immutable revision. Complete when every
   changed path is classified.
2. Record three independent statuses rather than collapsing proof and
   authority into one state:

   **Evidence state**

   - `hypothesis` — a report or reviewer observation without an independent
     reproduction.
   - `reproduced` — the current baseline fails on the affected user path or
     a focused regression establishes the defect.
   - `validated` — the responsible boundary is repaired, relevant sibling
     paths were checked, and proof ran on exact candidate content.

   **Review disposition**

   - `not-required` — the change is within the authorized low-risk path.
   - `review-required` — the change is sensitive, compatibility-affecting,
     high-impact, or still depends on maintainer judgment.
   - `review-complete` — the required maintainer review or decision is
     complete. This does not by itself authorize a merge.

   **Delivery status**

   - `unmerged` — no verified canonical merge has occurred.
   - `merged` — hosted checks passed and the canonical branch is verified to
     contain the recorded merge commit. Do not infer this from an open PR.
   - `rejected/duplicate` — record the reason and the governing finding.

   Evidence may be `validated` while review disposition is
   `review-required`. Do not treat local tests as maintainer review or an
   open PR as a canonical merge.

   For documentation, configuration, and Skill changes, use `validated` only
   after the applicable syntax, links, commands, and cross-file references are
   checked. Do not force a defect state where no runtime behavior is claimed.
3. For a defect or behavior change, reproduce the before state on the recorded
   baseline when practical. Identify the canonical owner and root cause, then
   inspect affected callers, callees, sibling implementations, and lifecycle
   cleanup. Fix the smallest responsible boundary.
4. Run focused tests for the affected package first. Add checks based on the
   surface:

   | Surface | Required evidence |
   | --- | --- |
   | defect or refactor | before/after reproduction, canonical owner and root cause, affected sibling paths, characterization or regression proof |
   | Go source | focused `make test WHAT=./internal/<pkg>`, then relevant build |
   | admit, lease, refresh, or apply | state-transition tests for success, expiry, steal-deny, and recovery |
   | GitHub client | read-only client tests; never require a write token |
   | Slack commands | HMAC, skew, allowlist, and command-routing tests |
   | policy | parse/default/fail-closed tests; `policy.example.yaml` vs `policy.fixture.yaml` |
   | SQLite persistence | store tests for revisions, leases, and immutable review blobs |
   | review eval | `eval/set.md` path when review-quality claims change |
   | docs/templates/Skills | syntax, links or commands where practical |

   Unit or mocked tests do not establish live GitHub, Slack, tunnel, or image
   behavior. For those claims, use the real supported boundary with isolated
   state; record unavailable prerequisites as skipped checks with residual risk.
5. Immediately before accepting results, repeat the revision and worktree
   guards from step 1. Without an immutable candidate or an explicit
   fingerprint of candidate paths, keep the result at `hypothesis` or
   `reproduced`.
6. Before ship-level handoff, run the normal repository gates unless the user
   explicitly requested a narrower check:

   ```bash
   make all && make test && make validate
   ```

   Run `make release-images` only when packaging or runtime images changed and
   Docker is intentionally available. Complete when every applicable local
   gate has a result.
7. Keep service-dependent tests ignored unless prerequisites and credentials
   are intentionally available. Never print secrets or persist live GitHub or
   Slack payloads. Complete when skipped checks name both the reason and
   residual risk.
8. Review failures against the changed scope. Report pre-existing failures with
   evidence; do not relabel a failure as pre-existing without comparison.

## Output

Report at least:

- evidence state, review disposition, and delivery status;
- baseline and candidate identity;
- checkout status and any intentional dirty paths;
- stated outcome and affected user path or documentation surface;
- commands run and pass/fail result;
- focused behavior covered;
- checks skipped with reasons;
- failures and whether they block the stated outcome; and
- remaining uncertainty.

Never use “all tests pass” unless all stated tests actually ran and passed.
