# ADR 0082: Signed ACP conformance receipts

- Status: Accepted
- Date: 2026-10-14
- Resolves: #616
- Related: [ADR 0073](0073-session-plane-guest-registry.md) (generic ACP
  guests are admitted by conformance), #600 (conformance runs inside the
  pinned guest image), [ARCHITECTURE.md](../../ARCHITECTURE.md)

## Context

ADR 0073 admits a generic ACP guest by conformance rather than by code. Until
now policy proved conformance with an unkeyed SHA-256 over the entry's own
fields. Anyone can compute that value for an edited entry without running the
suite, so it shows only that the entry is unchanged, not that the suite ran.
#600 moved the run into the pinned guest image; the evidence that it ran
still needed a key that the policy author alone does not hold.

## Decision

**D1. The plane runs conformance.** `rusui acp-conformance` reads the entry
from the policy file and posts it to the plane (`POST /guests/conformance`,
worker-authenticated, like drain and resume). The plane starts a throwaway
container from the entry's digest-pinned image on its own container runtime,
runs the version probe and the ACP subset there, and only then signs.

**D2. A dedicated plane-held HMAC key signs receipts.** `rusui setup`
generates `RUSUI_CONFORMANCE_KEY` (32 random bytes, hex) into the plane's env
file with the other generated secrets. Only the plane process reads it; it is
never sent to runners, guests, or clients. No existing secret is reused, so
rotating the worker secret or operator token does not touch receipts and a
receipt key leak does not grant API access.

**D3. A receipt names what ran.** The receipt records the time, the entry
digest (protocol, argv, probe, pin, image digest, suite), the image digest,
and the suite version, with HMAC-SHA256 over those fields. The operator
pastes it into the entry as `receipt:`.

**D4. Load and admission verify; failure is closed.** Policy parsing checks,
without the key, that the receipt is present and still names the entry, its
image digest, and the current suite. The plane verifies the signature with its
key at startup and on reload, and again when it admits a session for the
guest. A missing, stale, or mis-signed receipt, or a plane without the key,
refuses the guest.

**D5. No rotation and no expiry.** Replacing the key invalidates every
receipt; the operator reruns conformance for each generic guest. A receipt
does not expire; requalification remains an operator action.

## Consequences

Easier: a receipt is evidence that this plane ran the suite against this exact
image content, and a policy edit cannot forge one.

Harder: qualifying a guest needs a running plane with a container driver. The
key is one more secret in the env file to keep out of Git and backups that
leave the host. Key loss or replacement requires requalifying every generic
guest.

Schema: a generic ACP entry carries `receipt` (`suite`, `entry`, `image`,
`time`, `mac`) instead of the earlier `conformance` digest, which is now an
unknown field and refused. Public API: `POST /guests/conformance`. Trust model:
the worker secret can request a qualification, which only runs the entry's
own argv in its own image, as a turn would. Runner contract: unchanged.

## Rejected alternatives

- **Reuse the operator token or worker secret as the key.** Couples receipt
  validity to API credential rotation, and runners hold the worker secret.
- **Sign with the plane TLS key.** Certificate renewal would invalidate
  receipts, and TLS is optional on loopback planes.
- **Sign on the operator's machine.** The key would have to leave the plane.
- **Receipt expiry or key rotation with a key ring.** No present need; a
  rerun covers both.

## Validation and reversal

Validated by tests: a receipt signed without the plane key is refused at
reload and admission, an edited entry with a stale receipt is refused at
parse, a plane without the key admits no generic guest, and a receipt from the
plane's own run is admitted. Reverse with a superseding ADR; removing the key
check returns to entry-bound checks only.

## Sources

- #616, #600, ADR 0073.
