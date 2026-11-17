# #700 Plane takeover drill

- Status: measured. Four drill runs on one candidate.
- Feeds: [#700](https://github.com/Sannrox/rusui/issues/700) and the
  objective proposed in [#698](698-recovery-objective.md).
- Candidate: `d8c196d`.
- Topology: the redundant plane of
  [ADR 0089](../decisions/0089-redundant-plane.md). Two plane instances
  share one PostgreSQL 17 store with `synchronous_commit=on`, on one
  machine, loopback only.
- Procedure: [`scripts/plane-takeover-drill.sh`](../../scripts/plane-takeover-drill.sh).

## Procedure

The script starts instance A, then instance B, on the same store. A
holds the scheduler lease. Through A it then:

1. creates a schedule that fires every minute;
2. creates a session, and a stand-in runner claims its turn through A
   and sends no heartbeat after that, so the runner is lost along with A;
3. creates one more session and gets its acknowledgement.

Five seconds later it kills A with `SIGKILL`, without draining it. It
then polls the store and asks B for work every second. It records when B
holds the scheduler lease, when the schedule fires on B, and when B
grants A's turn again. Finally it checks that the session A acknowledged
is still in the store.

GitHub calls go to a closed local port. No external system was touched.

## Results

Seconds after A was killed:

| Measure | Run 1 | Run 2 | Run 3 | Run 4 |
| --- | --- | --- | --- | --- |
| B holds the scheduler lease | 124.3 | 124.5 | 124.3 | 115.0 |
| First schedule fire, on B | 144.5 | 144.1 | 143.8 | 115.1 |
| A's turn granted again by B (generation 3) | 175.4 | 175.6 | 175.7 | 176.1 |
| Acknowledged writes lost | 0 | 0 | 0 | 0 |
| Schedule fires after the takeover | 1 | 1 | 1 | 1 |

Runs 1 to 3 used a working copy of the script that waited 30s longer
between starting the instances. Run 4 is the committed script. The
timers set these numbers, not the machine, and the difference between
runs is where A's last renewal fell before the kill:

- **Scheduler lease.** The lease is 2m and every instance tries to renew
  or claim it every 40s. A's last renewal fell up to 40s before the kill,
  and B took over at its first attempt after the lease ran out. The worst
  case is 2m40s after the kill.
- **Schedule fire.** The schedule ticks every minute on the lease
  holder, so the first fire came at B's first tick after the takeover.
  The worst case is the lease bound plus one tick: in run 4 the tick fell
  right after the takeover, in runs 1 to 3 about 20s after it.
- **Turn.** A turn lease lives 3m after its last heartbeat (`Liveness`).
  The stand-in runner claimed 5s before the kill and never sent a
  heartbeat, so the turn came back 180s after the claim, at generation 3
  (expired, then claimed). With a live runner that reaches B, the lease
  never lapses: heartbeats go to whichever instance answers.

B answered every claim request the drill sent it during the takeover,
so new work could keep arriving. Only work owned by A waited.

## Interruption window and lost work

For the supported topology (ADR 0089: several plane instances, one
store that commits synchronously to storage surviving the loss of any
one host):

- **Interruption window: at most 3m40s**, within the 5 minutes #698
  proposed. A surviving instance keeps serving requests. Jobs that tick
  every minute on the lease holder (schedules, apply retry) resume within
  the 2m40s lease bound plus one tick; the 1s refresh resumes with the
  lease. Reconcile and catch-up keep their own 5m and 15m cadence. A turn
  whose runner was lost along with the instance returns to the queue 3m
  after its last heartbeat, or at the lease takeover if that comes later,
  because the lease holder expires turns. The measured worst was 176s.
- **Lost acknowledged work: none.** Every write that returned success
  was in the store after the takeover. This holds only while the store
  meets [upgrade.md](../upgrade.md#postgresql-store): synchronous commit
  with a synchronous standby.
- **Not recovered, by design:**
  - Schedule buckets that pass entirely inside the window are skipped,
    not caught up. Each schedule fires at most once per bucket.
  - A turn whose runner was lost is redone from its start under a new
    generation. A late result from the old generation is refused.
  - Inbound deliveries sent to the lost instance stay the sender's to
    retry. Deliveries are idempotent, so retrying against any instance
    is safe.

## Acceptance tests

Each #700 clause has a test that CI runs against the real store, on
SQLite and on PostgreSQL:

| Clause | Test |
| --- | --- |
| Crash | `TestSurvivingInstanceReclaimsATurnAndCompletesItOnce` |
| Takeover of plane-wide jobs | `TestPausedInstanceIsFencedAfterTakeover`, and this drill |
| Stale instance | `TestPausedInstanceIsFencedAfterTakeover` |
| Webhook during loss | `TestWebhookDuringInstanceLossWakesTheSession` |
| Uncertain external action | `TestNewHolderReconcilesAnApplyLeftInFlight` |
| Rolling upgrade and rollback | `TestPreviousReleaseServesAnExpandedSchema`, `TestNewerSchemaIsRejected` |
| Readiness | `TestInstanceWithoutStoreYieldsTheLease`, `TestReadyzAndBoundedRunSession` |
| Restore | `TestRestoreClearsStaleAuthority`, `TestRestoreReleasesGuestHandlesAndPlaneIdentity`, `TestRestorePreservesIdentitiesRetentionAndReceipts` |
| Single host | the SQLite suite, unchanged, in the default CI job |

## Not measured

- **Store failover.** The drill kept the store up. The store's own
  failover time adds to the window, as #698 states.
- **Pauses and clock skew between hosts.** Both instances shared one
  clock. Lease expiry is judged on the store's clock (ADR 0089 D4), and
  `TestStoreClockAlignsSkewedInstances` covers skewed instances.
  `TestPausedInstanceIsFencedAfterTakeover` covers a paused instance. No
  pause on a real host class was observed.
- **Runner loss and placement.** This is #685.
