# #498 guest-link host scores

Predeclared before the run. Pass bar is fixed here.

| Piece | Value |
| --- | --- |
| Candidate | `aaa74c1dca533ccd11c20e1406093bdc5ec57af7` (`origin/main` at claim) |
| Pass bar | On that host: `rusui diagnose` reports `guest_link` ready; one managed prompt completes; `follow` streams; preview opens through the guest link |
| Hosts | Docker Desktop; Podman |

A host that cannot meet the pass bar is **defer**. A defect during a
run that started is its own issue. Neither host is declared supported
until a later run meets the bar.

## Results

Recorded 2026-10-06 against the candidate above. Binary: `rusui dev commit=aaa74c1dca533ccd11c20e1406093bdc5ec57af7`.

### Podman

**defer.** `podman --version` exits 127 (`command not found`). The host cannot start a guest, so it cannot pass `guest_link`.

### Docker Desktop

**defer.** Docker CLI 29.1.3, darwin client, linux/arm64 engine. `rusui diagnose -policy policy.example.yaml -addr 127.0.0.1:8080` on the candidate:

- `runtime`: ready (`container CLI present`)
- `guest_image`: misconfigured (`RUSUI_GUEST_IMAGE unset`)
- `plane_tls`: misconfigured (`RUSUI_TLS_CERT and RUSUI_TLS_KEY required for container guests`)
- `plane_ca`: misconfigured (`RUSUI_PLANE_CA unset`)
- `model_upstream`: misconfigured (`xAI upstream needs a model key or RUSUI_MODEL_UPSTREAM`)
- `guest_link` did not run
- process exit 1, `"ready": false`

A managed prompt, follow, and preview were not started. The pass bar requires those after `guest_link` is ready.

Neither host is supported. Native Linux Docker remains the verified guest-link host (ADR 0047).
