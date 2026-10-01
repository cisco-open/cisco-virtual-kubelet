# CVK gNOI lab round trip

Started: 2026-10-01T08:04:50.440122+00:00

Candidate revision: `665a7954a9def499cd3ee36e972e2b5fe15256f5`

Expected image: `cvk-tas-extentions:665a7954-settled5`

Context: `default`; namespace: `cvk-live`

Each device is verified before moving to the next. JSON files are the exact Kubernetes manifests and results. Automatic acceptance covers standalone Catalyst software commitment, secure gNOI, control-processor load/memory, Kubernetes readiness, and fleet workload readiness/ownership continuity. Unfamiliar or inconclusive output stops progression. Forwarding, routing adjacencies, and application traffic are not tested by this lab harness.

2026-10-01T08:04:54.046976+00:00 — workload baseline captured: 2 ready Pod(s), 2 controller group(s).

2026-10-01T08:04:54.047362+00:00 — Create `DeviceOperation/cycle-20261001080450-64cad6-cat9k-lab-101-certificates`; manifest `cycle-20261001080450-64cad6-cat9k-lab-101-certificates.manifest.json`.

2026-10-01T08:05:07.779364+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-certificates`: manager-owned network worker binding observed before execution.

2026-10-01T08:05:38.848760+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-certificates`: `Succeeded` — 5 certificate(s) installed

2026-10-01T08:05:38.849393+00:00 — Create `DeviceOperation/cycle-20261001080450-64cad6-cat9k-lab-101-baseline-verify`; manifest `cycle-20261001080450-64cad6-cat9k-lab-101-baseline-verify.manifest.json`.

2026-10-01T08:05:42.597311+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-baseline-verify`: manager-owned network worker binding observed before execution.

2026-10-01T08:06:54.902228+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-baseline-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T08:06:55.495394+00:00 — Create `DeviceOperation/cycle-20261001080450-64cad6-cat9k-lab-101-baseline-health`; manifest `cycle-20261001080450-64cad6-cat9k-lab-101-baseline-health.manifest.json`.

2026-10-01T08:07:22.362201+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-baseline-health`: manager-owned network worker binding observed before execution.

2026-10-01T08:07:22.723764+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-baseline-health`: `Succeeded` — 5 command(s) completed

2026-10-01T08:07:23.007501+00:00 — `cat9k-lab-101` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T08:07:24.688208+00:00 — Create `DeviceOperation/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-before-verify`; manifest `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-before-verify.manifest.json`.

2026-10-01T08:07:51.495973+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-10-01T08:07:51.807594+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-before-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T08:07:52.346480+00:00 — Create `DeviceOperation/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-before-health`; manifest `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-before-health.manifest.json`.

2026-10-01T08:07:56.211308+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-before-health`: manager-owned network worker binding observed before execution.

2026-10-01T08:07:56.520202+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-10-01T08:07:56.816916+00:00 — `cat9k-lab-101` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T08:07:56.817782+00:00 — Create `IOSXESoftwareRollout/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`; manifest `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout.manifest.json`.

2026-10-01T08:08:03.190910+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`: approved frozen plan `sha256:3a3d70168e146cb47e3d52690879285d81c8e9047f2b1b62283defa2aa79f138` as `system:admin`.

2026-10-01T08:08:03.479853+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`: `AwaitingApproval` — waiting for approval of sha256:3a3d70168e146cb47e3d52690879285d81c8e9047f2b1b62283defa2aa79f138

2026-10-01T08:08:13.761993+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-10-01T08:08:24.684573+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-10-01T08:23:24.902726+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-10-01T08:33:38.058659+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-10-01T08:33:59.110079+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-10-01T08:33:59.390296+00:00 — Create `DeviceOperation/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-verify`; manifest `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-verify.manifest.json`.

2026-10-01T08:34:09.782555+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-verify`: manager-owned network worker binding observed before execution.

2026-10-01T08:34:10.080049+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-10-01T08:34:10.621136+00:00 — Create `DeviceOperation/cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-health`; manifest `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-health.manifest.json`.

2026-10-01T08:34:30.947708+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-health`: manager-owned network worker binding observed before execution.

2026-10-01T08:34:31.204103+00:00 — `cycle-20261001080450-64cad6-cat9k-lab-101-downgrade-health`: `Succeeded` — 5 command(s) completed

2026-10-01T08:34:31.439432+00:00 — `cat9k-lab-101` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T08:34:36.039165+00:00 — Required worker-log evidence incomplete: unsafe workload drain evidence: Force deleting pod in running state

2026-10-01T08:34:36.882438+00:00 — STOPPED: required evidence collection incomplete: manager/network/app worker logs. Preserve all operation objects; no automatic cleanup or retry was attempted.
