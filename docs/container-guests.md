# Container guests (trusted egress)

The first unattended topology is **one runner**, the **container** driver
(Docker or Podman CLI), and Grok ACP over container stdio with plane
proxies. Tests in this repository prove that path with `FakeRuntime` and a
stubbed Docker CLI. They do **not** claim live Docker/Podman, IPv6, or
cross-host combinations.

Container guests run with no network (`--network none`) and may reach
only `rusui.plane` (git and model HTTPS proxies), through a guest link: a
multiplexed stream over the same `docker exec` stdio that runs the
harness ([ADR 0047](decisions/0047-guest-link.md)). An implement session
under agent publication also reaches `github.com` and `api.github.com`
through its link. Direct addresses, IPv6, host loopback, DNS, and other
names are unreachable, so `.agents/setup` cannot download from the
internet; put dependencies in the image or the snapshot. Preview reaches
a guest port through the same kind of link. The guest image must include
Node, which runs the guest side of the link. `rusui diagnose` checks the
path with a throwaway guest (`guest_link`). The plane keeps listening on
loopback; no host firewall change is needed.
The guest image does not declare that ask. Policy and the guest link
are the grant
([ADR 0027](decisions/0027-guest-reachability-ask.md)). An image that
asked for more would be refused, not prompted.
The guest image must trust the plane CA at
`/usr/local/share/ca-certificates/rusui-plane.crt`. Provision that file as
part of the guest image identity (`source_hash`) or mount it with
`RUSUI_PLANE_CA`. An untrusted endpoint never receives the grant: TLS
handshake fails before the bearer is sent.

`.agents/setup` runs once per image and git pin. When it exits zero, the
plane copies `/workspace` (`.git` included) into the snapshot cache, and
every later environment for that image and pin, including one created
after earlier ones expired, starts from that tree without running setup.
Only `/workspace` is kept; files setup writes elsewhere in the guest are
not. A setup that exits non-zero stores nothing, so the next environment
runs it again
([ADR 0007](decisions/0007-environment-snapshot.md)).

Required for the container driver:

```bash
export RUSUI_TLS_CERT=/etc/rusui/plane.crt
export RUSUI_TLS_KEY=/etc/rusui/plane.key
export RUSUI_PLANE_CA=/etc/rusui/plane-ca.crt   # mounted into the guest
export RUSUI_GUEST_IMAGE=rusui-guest:local
```

The plane certificate must include Subject Alternative Names `rusui.plane`
(guest) and the listen address (host prepare, typically `127.0.0.1`). Host
git fetch and `rusui drain` use `https://127.0.0.1:<port>` with
`RUSUI_PLANE_CA`. Guests use `https://rusui.plane:<port>`.

If Docker or Podman is installed but the TLS pair is unset, the container
driver stays disabled and the process logs that fact. Process-driver review
on loopback HTTP remains available.

Snapshot prepare uses a **read-only prepare grant** through `/git-proxy/`,
not a GitHub token on the runner disk. Turn grants may push only
`refs/heads/rusui/<session>/*` on the bound repo. Grant loss (expiry, lease
loss, other session) is a failed turn, not a silent continue.

A guest started with any network other than `none` is not the
supported topology.

At startup the server expires hung refresh owners, reconciles missed hook
deliveries, catches up open and locally tracked items, and retries unpublished
apply attempts. The same paths repeat: refresh every 1s, reconcile every 5m,
catch-up every 15m, apply retry every 1m. Reconcile is skipped with a log when
`RUSUI_GITHUB_HOOK_IDS` is unset.

SQLite defaults to `rusui.db` in the current working directory (`*.db` is
gitignored). Stop the process, copy `rusui.db` (and `-wal`/`-shm` if
present). Restore with `rusui restore -db <copy>` while the plane is
stopped, then start the plane on that file. It prints an inventory and
counts sessions/turns/environments, clears live leases, drops turn grants,
expires managed environments without adopting their guests, gives the
copy a new plane id, and keeps approval rows as records only (they do not
authorize a new RPC).
A missing or corrupt file is an error, not a successful empty plane.
Container workspace dirt is not in the database; it rematerializes from
the snapshot after idle expiry. Credentials in env files are not in the
backup.
