# #785 guest-link host scores

Predeclared before the run. Pass bar is the #498 bar, fixed here.

| Piece | Value |
| --- | --- |
| Candidate | `392798f722fec4170f5038e503e5be87ca366866` (`origin/main` at claim) |
| Pass bar | On that host: `rusui diagnose` reports `guest_link` ready; one managed prompt completes; `follow` streams; preview opens through the guest link |
| Hosts | Docker Desktop; Podman |

A host that cannot meet the pass bar is **defer**. A defect during a
run that started is its own issue. Neither host is declared supported
until a later run meets the bar.

## Results

Recorded 2026-11-27 against the candidate above.

### Podman

**defer.** `podman --version` exits 127 (`command not found`). The host
cannot start a guest, so it cannot pass `guest_link`. A managed prompt,
follow, and preview were not started.

### Docker Desktop

**defer.** This run is not on Docker Desktop. Docker CLI 29.9.2, linux
client, linux/amd64 engine, context `default` on
`unix:///var/run/docker.sock`. Docker Desktop CLI and the Desktop engine
socket are absent. Native Linux Docker is already the verified host
([ADR 0047](../decisions/0047-guest-link.md)) and is not this score.

`guest_link` did not run on Docker Desktop. A managed prompt, follow, and
preview were not started.

Neither host is supported. Native Linux Docker remains the verified
guest-link host (ADR 0047). [ADR 0062](../decisions/0062-guest-link-desktop-podman-deferred.md)
stands.
