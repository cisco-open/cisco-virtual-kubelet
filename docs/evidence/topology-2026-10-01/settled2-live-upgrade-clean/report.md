# CVK gNOI lab round trip

Started: 2026-10-01T06:20:24.912015+00:00

Candidate revision: `665a7954a9def499cd3ee36e972e2b5fe15256f5`

Expected image: `cvk-tas-extentions:665a7954-settled2`

Context: `default`; namespace: `cvk-live`

Each device is verified before moving to the next. JSON files are the exact Kubernetes manifests and results. Automatic acceptance covers standalone Catalyst software commitment, secure gNOI, control-processor load/memory, Kubernetes readiness, and fleet workload readiness/ownership continuity. Unfamiliar or inconclusive output stops progression. Forwarding, routing adjacencies, and application traffic are not tested by this lab harness.

2026-10-01T06:20:28.417934+00:00 — workload baseline captured: 2 ready Pod(s), 2 controller group(s).

2026-10-01T06:20:28.418324+00:00 — Create `DeviceOperation/cycle-20261001062024-d04c5c-cat9k-live-certificates`; manifest `cycle-20261001062024-d04c5c-cat9k-live-certificates.manifest.json`.

2026-10-01T06:20:32.426674+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-certificates`: manager-owned network worker binding observed before execution.

2026-10-01T06:21:34.536328+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-certificates`: `Succeeded` — 5 certificate(s) installed

2026-10-01T06:21:34.537065+00:00 — Create `DeviceOperation/cycle-20261001062024-d04c5c-cat9k-live-baseline-verify`; manifest `cycle-20261001062024-d04c5c-cat9k-live-baseline-verify.manifest.json`.

2026-10-01T06:21:41.605328+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-baseline-verify`: manager-owned network worker binding observed before execution.

2026-10-01T06:22:12.688981+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-baseline-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-10-01T06:22:13.252047+00:00 — Create `DeviceOperation/cycle-20261001062024-d04c5c-cat9k-live-baseline-health`; manifest `cycle-20261001062024-d04c5c-cat9k-live-baseline-health.manifest.json`.

2026-10-01T06:22:20.382235+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-baseline-health`: manager-owned network worker binding observed before execution.

2026-10-01T06:22:20.686367+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-baseline-health`: `Succeeded` — 5 command(s) completed

2026-10-01T06:22:20.951335+00:00 — `cat9k-live` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T06:22:23.672770+00:00 — Create `DeviceOperation/cycle-20261001062024-d04c5c-cat9k-live-upgrade-before-verify`; manifest `cycle-20261001062024-d04c5c-cat9k-live-upgrade-before-verify.manifest.json`.

2026-10-01T06:22:47.161356+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-10-01T06:22:47.416343+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-before-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-10-01T06:22:47.986683+00:00 — Create `DeviceOperation/cycle-20261001062024-d04c5c-cat9k-live-upgrade-before-health`; manifest `cycle-20261001062024-d04c5c-cat9k-live-upgrade-before-health.manifest.json`.

2026-10-01T06:23:21.549794+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-before-health`: manager-owned network worker binding observed before execution.

2026-10-01T06:23:21.795043+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-10-01T06:23:22.083713+00:00 — `cat9k-live` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T06:23:22.084793+00:00 — Create `IOSXESoftwareRollout/cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout`; manifest `cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout.manifest.json`.

2026-10-01T06:23:28.404943+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout`: approved frozen plan `sha256:5f6320db0036fa210ccf9844bd24642458a3e013fdec82a611d2deaafaaad4ed` as `system:admin`.

2026-10-01T06:23:28.697999+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout`: `AwaitingApproval` — waiting for approval of sha256:5f6320db0036fa210ccf9844bd24642458a3e013fdec82a611d2deaafaaad4ed

2026-10-01T06:23:38.940641+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout`: `Executing` — campaign is executing within frozen topology budgets

2026-10-01T06:23:49.769820+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-10-01T06:41:19.635428+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-10-01T06:51:23.531859+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-10-01T06:51:34.070925+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-10-01T06:51:34.335978+00:00 — Create `DeviceOperation/cycle-20261001062024-d04c5c-cat9k-live-upgrade-verify`; manifest `cycle-20261001062024-d04c5c-cat9k-live-upgrade-verify.manifest.json`.

2026-10-01T06:51:54.519743+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-verify`: manager-owned network worker binding observed before execution.

2026-10-01T06:51:54.798033+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T06:51:55.359815+00:00 — Create `DeviceOperation/cycle-20261001062024-d04c5c-cat9k-live-upgrade-health`; manifest `cycle-20261001062024-d04c5c-cat9k-live-upgrade-health.manifest.json`.

2026-10-01T06:51:59.204775+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-health`: manager-owned network worker binding observed before execution.

2026-10-01T06:51:59.474334+00:00 — `cycle-20261001062024-d04c5c-cat9k-live-upgrade-health`: `Succeeded` — 5 command(s) completed

2026-10-01T06:51:59.746426+00:00 — `cat9k-live` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T06:52:04.631223+00:00 — STOPPED: cat9k-live: ready worker image identity is not proven. Preserve all operation objects; no automatic cleanup or retry was attempted.
