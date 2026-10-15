# #603 governed transform authoring on the session model

Question: can a write, test on sample, publish loop for sekai-chisei
governed transforms run on the existing session model without new ADRs?

| Piece | Value |
| --- | --- |
| rusui | `073dc5a9755b7a46d4f39aaeb75e9f890902a60d` (`origin/main` at claim) |
| sekai-chisei | `2bb985f6337345b9be85d7b74ce5f1e2957b2f9e` (`origin/main`, after Sannrox/sekai-chisei#1386) |
| Pass bar | Each step runs on accepted contracts: a session writes the transform, tests it on a bounded sample with the engine the plane will run, and the plane submits it to sekai-chisei as one recorded Action whose receipt names the definition digest and, when the apply also runs it, the run id |

A step that needs a change to an accepted ADR fails the bar and is listed
below. A step that could not run here is reported as not run.

## Walkthrough

### 1. Write

A `run` session with an ACP guest writes `transform.sql` in the
workspace. Nothing new is needed: this is an ordinary turn.

Not run here: no container runtime or model upstream was available on
the measuring host, so no guest turn was started.

### 2. Test on a sample

**Toolchain.** The reference guest image's final stage is a slim Node
base plus `git`, `make`, `build-essential`, and a headless browser. It
has no Python, no SQLite CLI, and no dataframe or analytical engine.
Guests run with `--network none` and the guest link forwards only the
plane and, for implement sessions, GitHub
([ADR 0047](../decisions/0047-guest-link.md)), so `.agents/setup` cannot
install one. [ADR 0050](../decisions/0050-guest-toolchain-in-image.md)
already answers this: bake the toolchain into the image. That needs an
image, not an ADR. The policy `guests:` registry
([ADR 0073](../decisions/0073-session-plane-guest-registry.md)) lets a
guest entry name its own `image`; only an empty `image` falls back to
`RUSUI_GUEST_IMAGE`. A transform-authoring guest entry can therefore
carry the engine without changing the image other guests use. That is
the existing "project preset" path.

**Engine parity.** The engine in the guest must be the engine the plane
runs. The analytical profile runs SQL in an in-memory SQLite connection
over string columns (sekai-chisei `docs/governed-transforms.md`). The
same join and aggregate, on the same three-order sample, measured with
SQLite 3.53.4 and the `duckdb` engine 1.5.6:

| Engine | `SUM(o.amount)` over string columns | With `CAST(o.amount AS BIGINT)` |
| --- | --- | --- |
| SQLite (analytical profile) | `east 2 17`, `west 1 4` | `east 2 17`, `west 1 4` |
| `duckdb` | binder error: no `sum(VARCHAR)` | `east 2 17`, `west 1 4` |

A transform that passes in a `duckdb` guest can fail or change meaning
on the analytical profile, and the reverse. Test with the target
profile's engine: SQLite for `analytical`, the runner's declared
`engine` and `runtime_image` for `isolated_process`.

**Sample data.** A bounded synthetic fixture checked into the repository
fits the session model unchanged. An exported sample of tenant data
does not: the guest has no object-store or plane-data identity
([ADR 0033](../decisions/0033-third-party-identity-deferred.md)), and
the plane would have to hand dataset rows into a guest, which no
accepted ADR covers.

### 3. Publish

Turns only propose; writes go through governed Actions
([ADR 0080](../decisions/0080-turns-propose-writes-go-through-actions.md)).
Today that path is GitHub only:

- `write.propose` carries a branch, title, body, and candidate SHA
  (`internal/engine/write.go`). There is no field for a transform
  definition.
- `ApplySessionWrite` performs a GitHub write, for implement sessions
  only.
- ADR 0080 D4: no new Action types beyond `write.propose` and
  `write.apply`.

Publishing to sekai-chisei therefore needs: a proposal target for a
transform definition, a plane-side apply that calls
`PutGovernedTransform` and, when the proposal asks for a first run,
`RunGovernedTransform`, with a plane-held sekai-chisei credential
(`namespace:write`, `dataset:write`; the transform RPCs are experimental
and gated by `SEKAI_EXPERIMENTAL_RPCS=1`), and a per-project policy
capability to allow it (policy v2 fails closed on unknown fields). The
guest never needs to reach sekai-chisei, so the guest link is unchanged.

Not run here: rusui has no sekai-chisei client.

### 4. Plane receipt

Measured directly on sekai-chisei with `cargo run --example
analytical_transform` at the revision above: a two-input join is put
through `put_governed_transform` and run once.

```json
{
  "definition_digest": "sha256:893dc91767f8a36ab747f22bc7f9b7be0c0d05213dcc4db80f68323161456a41",
  "input_digest": "sha256:ebf909787594314d1739864c4838d07cbdfaf294e49e2cb4ed195079367b479c",
  "lineage_parent": "",
  "quarantined": false,
  "rows_in": 5,
  "rows_out": 2,
  "run_id": "88717a34-9852-4416-aa29-2f641076288b"
}
```

The plane stays the authority: the definition digest and run receipt
come from sekai-chisei. A rusui apply receipt would record references
to them, the `definition_digest` from the put and the `run_id` when the
apply also ran the transform, and never copy the rows or the receipt
body. A definition can have many runs, so the digest alone does not
identify one.

## ADRs touched

| ADR | Touched? | Why |
| --- | --- | --- |
| [0080](../decisions/0080-turns-propose-writes-go-through-actions.md) | **Yes, amend** | D1, D2 and D4 define GitHub-only proposals and forbid new Action types; publishing a transform is a new proposal target and apply path |
| [0050](../decisions/0050-guest-toolchain-in-image.md), [0047](../decisions/0047-guest-link.md), [0073](../decisions/0073-session-plane-guest-registry.md) | No | Baking the engine into an image is what ADR 0050 chose, and a registered guest may name that image; no new destination |
| [0033](../decisions/0033-third-party-identity-deferred.md) | No, with a synthetic sample | An exported tenant sample would need a plane-to-guest data path that ADR 0033 and the guest model do not provide |
| [0009](../decisions/0009-credential-broker.md) | No | A sekai-chisei credential is a durable plane secret, which ADR 0009 already allows; it is never granted to the guest |

## Decision

**Fails the bar; file feature issues.** Write and test fit the session
model once the image carries the target engine and the sample is a
repository fixture. Publish does not: it needs an ADR amending 0080.
Proceeding as a preset or a document alone would leave publish outside
governed Actions.

Recommended follow-ups, for the maintainer to file:

1. An ADR amending 0080: a sekai-chisei transform proposal target, its
   plane apply through `PutGovernedTransform` and an optional first
   `RunGovernedTransform`, an apply receipt that records the
   `definition_digest` and, when run, the `run_id`, and the per-project
   policy capability that allows it.
2. A guest image carrying the target profile's engine (SQLite for
   `analytical`; the runner's engine for `isolated_process`), registered
   as its own `guests:` entry, with engine parity checked against the
   plane.
3. A plane sekai-chisei client and credential, implementing the ADR.
