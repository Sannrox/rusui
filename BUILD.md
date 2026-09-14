# Build prompt — rusui

Paste this whole file to a coding agent in a fresh checkout of `rusui`.
Read `ARCHITECTURE.md` first. It is the authority. Do not implement live
apply or implement-to-PR except as types/comments needed to keep the
state machines honest. The domain nouns are environment, session, turn,
runner, event, and action. A review job is a turn on a session.

First working slice: fake GitHub (webhook POST plus list/detail delivery
shape), webhook → refresh → claim → Codex `input.v1.json`/`output.v1.json`
→ dry-run apply, plus the state-transition tests below. Evaluate
`eval/set.md` on that path. Add Slack and live delivery reconciliation
after the slice is green.

```
Goal: Ship milestone v1 of rusui: one configured repository, one
model CLI adapter, signed GitHub webhook intake that ACKs without live
fetch, serialized per-item refresh coordinator, authenticated
claim/lease, immutable review JSON in SQLite, deterministic dry-run
apply, pause/status/retry, daily review budget. No live GitHub
mutations. No implement. No land.

Context:
- New private repo: rusui. Personal tool, not a product in
  aldunis-platform, sekai-chisei, shikigami, or tenkai.
- Inspired by ClawSweeper's read → write → act split and Tatara's
  admit/claim split. Do not fork either. Do not import ai-task-queue.
- ARCHITECTURE.md is the contract. Do not invent weaker ones.
- Operator default: policy.example.yaml. Eligible dry-run tests load
  policy.fixture.yaml. Capability flags authorize simulation in v1.
- Canonical review JSON is a SQLite blob. Filesystem JSON/MD are
  optional exports, not recovery inputs.

Build this v1:

1. HTTP server (Go 1.22+ net/http, no web framework)
   - Bind 127.0.0.1 only. Document the operator tunnel.
   - POST /hooks/github — verify signature; one SQLite txn: unique
     delivery_id + coalesced refresh_request for (repo, item) from
     payload identity; 2xx only after commit. No live GitHub fetch.
     No model spawn. Delivery during an owner fetch sets
     needs_another_refresh; it does not start a second owner.
   - Refresh coordinator is the only pending-snapshot writer.
     One owner fetch-and-admit per (repo, item). Taking ownership
     increments refresh_generation and sets owner_expires_at (2 min).
     Fetch timeout 30s. Admit only if still owner; otherwise discard
     (expired ownership). Reaper and process startup expire hung or
     crashed owners, backoff, max 5 fetch retries. After finish, if
     needs_another_refresh, enqueue a new generation.
   - POST /hooks/slack — HMAC over raw body; reject timestamp skew
     > 5 minutes; allowlist user_id; commands: status, pause, resume,
     sweep, retry, reload. Reject implement.
   - POST /jobs/claim, heartbeat, complete, fail — worker secret;
     complete/fail require lease_generation and claimed_revision.
     All acceptance checks run inside the finalize transaction.
   - GET /healthz
   - Fake GitHub implements repository webhook POST and
     GET .../hooks/{id}/deliveries plus GET .../deliveries/{id}.
     Reconcile: list then detail; identity from detail payload;
     checkpoint advances only after detail + refresh_request commit;
     failed detail does not skip ahead until retry_limit, then skip
     only that delivery_id.
   - Startup + periodic: that reconcile; open-item catch-up; refresh
     of locally tracked non-terminal items even if they are no longer
     open (closed during outage). Slack waits until the slice is green.
     Cadence: refresh tick 1s, reconcile 5m, catch-up 15m, apply retry
     1m. Unset `RUSUI_GITHUB_HOOK_IDS` skips reconcile with a log.

2. Store (SQLite)
   - schema_migrations (versioned; no CREATE IF NOT EXISTS drift)
   - environments, sessions, turns, runners, events, actions
   - deliveries, refresh_requests, policy_revisions, overlay,
     review_revisions (canonical JSON blob), apply_attempts,
     reconcile_checkpoints, receipts, retry_audit, daily_review_counts,
     evidence_invalidations
   - `jobs` is a read view of turns joined to sessions; writes go to
     `turns` / `sessions`. A lease belongs to a turn.
   - Lease fields as ARCHITECTURE.md
   - completion_receipts keyed by (job_id, lease_generation,
     claimed_revision)
   - expire_lease shared by claim and reaper
   - retry_count per pending_revision; admit of a new snapshot or
     operator_retry resets it; older-revision fail/expire does not
     spend the new budget
   - pause: claim rejects; apply insert rejects; admit of a new
     snapshot while leased keeps state=leased; in-flight complete/fail
     still allowed
   - evidence_invalidations unique on (review_revision_id, action_id);
     consumed once by force admit

3. Policy — schema, precedence, pause, daily budget exactly as
   ARCHITECTURE.md. Claim transaction also rejects if the repo is
   missing or review is false. In-flight leases may finish after
   revocation. Apply rechecks current policy in the intended-action
   insert transaction. Stale_insufficient_info thresholds: age ≥ 60
   days and no non-bot comment in 60 days.

4. Review lane
   - Spawn only after claim. Trusted local execution as specified.
   - One adapter: Codex CLI; server-built input.v1.json pinned to
     claimed snapshot SHAs; output.v1.json. Size limits as
     ARCHITECTURE.md. Model must not fetch live GitHub.
   - main_sha on the review row is the admitted snapshot SHA, not a
     claim-time fetch.
   - Kill CLI at execution_deadline_at even if heartbeats succeed.
   - Public-repo jobs receive only PublicContext.
   - /complete and /fail: one SQLite transaction; receipt lookup then
     require live unexpired matching lease; else reject with no row.
     Unreceipted stale completions are not history.
   - /complete: validate identity against the claim; overlay server
     main_sha; classify evidence; enforce triples. Canonical JSON in
     SQLite with receipt. claimed < pending → queued, no apply.
     not_reproducible_on_main and incoherent never enqueue apply.
     implemented_on_main requires default-branch reachability of the
     merge commit (release-branch merge does not qualify). duplicate /
     stale require server_verified checks. confidence is recorded, not
     a gate. Dry-run/intended-action text labels evidence class and
     the server-limit sentence. Record token/spend if the CLI reports
     them.
   - /fail: finalize_failure as ARCHITECTURE.md (failure receipt;
     claimed < pending queues without spending budget; claimed ==
     pending increments retry_count).

5. Apply lane (deterministic Go, no model)
   - Dry-run: one intended-action per action_id; no mutate APIs.
   - Must not call admit. Enqueue refresh_request on item-hash mismatch.
     On main-evidence failure: same txn cancel + evidence_invalidation
     + force refresh_request. Recovery must not force-admit twice.
   - Final txn: policy/overlay, pending_revision match, item-hash
     match, then insert intended-action.

6. Slack
   - retry requeues state=failed on an unchanged revision only
     (audit row, reset retry_count).
   - sweep enqueues refresh_requests only.
   - pause as specified.

Constraints:
- Do not add Kafka, RabbitMQ, Redis, a dashboard, Cloudflare workers,
  live comment/close/land, implement-to-PR, OS filesystem jail, or
  OS network jail
- Do not spawn CLIs or fetch GitHub inside webhook handlers
- Do not let apply write pending snapshots
- Do not give the model process a GitHub write token
- Do not put private facts in PublicContext
- Do not couple this repo to aldunis-platform or sekai-chisei code
- Do not treat model confidence as eligibility
- Do not use filesystem JSON as canonical recovery
- Modules: cmd/rusui, cmd/rusui-worker, internal/store, internal/github,
  internal/slack, internal/policy, internal/review, internal/apply,
  internal/lease, internal/exec, internal/reconcile, internal/refresh
- No production credentials in the repo

Verification — `make all && make test && make validate`. `go test ./...` must include:

1. Expired worker heartbeats, then completes → heartbeat rejected,
   complete rejected; pending review still queued
2. Successful complete, lost HTTP response, retry complete with the
   same generation → one review revision, one action_id
3. Policy pause or file reload denies action after review, before
   dry-run apply → attempt cancelled, no intended-action row
4. Apply commits in_flight then crashes before intended-action write
   → startup reconcile retries once under the same action_id
5. Webhook never reaches the server (or crashes before commit) →
   checkpointed reconcile or catch-up admits current live state
6. Issue body unchanged, main_sha advanced → pending_revision
   unchanged; implemented_on_main re-verifies evidence
7. Crash before SQLite finalize → retry complete writes one review
   row and one receipt
8. CLI hangs while heartbeats succeed → execution deadline kills the
   child and expires the lease
9. Catch-up runs several times during one review of an unchanged item
   → pending_revision unchanged; completion is current; one eligible
   intended-action
10. Admit revision B while A runs, complete A, lose the response,
    claim B, retry A's complete → A's receipt returned; no extra
    revision; B's lease untouched
11. Reclaim expired leases only via /jobs/claim (no reaper) → job
    still reaches retry_limit and then failed
12. Review B is running; older apply reads a snapshot that already
    matches pending B → A cancelled, pending_revision unchanged, B
    undisturbed; apply did not call admit
13. CLI exits with no JSON; /fail records a failure receipt immediately;
    retry /fail returns the same receipt; no review revision; no
    intended-action
14. Block the GitHub evidence fetch, commit a pause, release the fetch
    → apply cancelled, no intended-action row
15. Fail revision A (retry_count > 0), admit B → B starts at
    retry_count 0 with a full retry_limit; replay A's failure receipt
    does not increment B's counter or change B's lease
16. Webhook handler does not call the GitHub item API; 2xx after
    delivery+refresh_request commit
17. Owner fetch A (old) loses ownership; B admits new state; A
    completes → A discarded as expired ownership; pending is B;
    never two owners
18. Apply final txn: review no longer matches pending → cancelled,
    no intended-action
19. CLI JSON with wrong snapshot_hash or item id → failure receipt;
    no review revision; no apply
20. propose_close not_reproducible_on_main with confidence=high →
    advisory; no intended-action
21. Item closed on GitHub while local job still queued; catch-up of
    locally tracked items admits closed state
22. state=failed, unchanged snapshot, sweep only → still failed;
    Slack retry → queued, retry_count 0, receipts preserved
23. pause: new claim rejected; in-flight complete still accepted;
    apply insert cancelled
24. claim → pause → changed snapshot admitted → in-flight complete
    accepted (state was still leased) → resume → new claim of the
    new pending_revision
25. Delivery arrives during owner fetch → needs_another_refresh;
    after owner finishes, a second refresh runs; pending reflects
    the later live state
26. Policy reload removes the repo or sets review=false → new claim
    rejected; already-leased complete still accepted
27. Complete after expire with no receipt → rejected; no review row
28. Evidence failure: crash after the atomic cancel+invalidation+
    refresh_request commit; recovery consumes the invalidation once;
    pending_revision increments at most once
29. Intended-action text includes evidence class and the server-limit
    sentence (on-branch ≠ solves the issue; item exists ≠ duplicate)
30. stale_insufficient_info: age 59 days or recent non-bot comment
    → not eligible; age 60 days and 60 days without non-bot comment
    → eligible when close is on
31. Evidence failure: cancel + invalidation + force refresh_request
    in one txn; crash immediately after commit → recovery does not
    insert a second invalidation; pending_revision increments at most
    once (replaces the looser test 28 if duplicated)
32. Refresh owner process crash → startup expires ownership and
    another fetch admits; item is not stuck
33. Refresh fetch hangs past 30s / owner_expires_at → reaper expires
    ownership, backoff retry, item unblocked
34. PR merged only into release-* → implemented_on_main not eligible
35. Fake list/detail: detail fetch fails → checkpoint does not
    advance; after retry_limit that delivery_id is skipped and later
    deliveries still process
36. input.v1.json contains only pinned snapshot SHAs; a live newer
    main in the fake is not present in the model input

Also include eval/set.md with one case per reason_code, keep/abstain,
and the release-branch misleading PR. Running the set is part of v1
slice sign-off, before Slack.

Also: operator can start server+worker against a fake GitHub with
policy.fixture.yaml, post a signed fixture webhook, see 2xx before
the fake fetch runs, refresh admits, claim, store one SQLite review
revision, and see one intended-action dry-run without any model write
access to GitHub.
```
