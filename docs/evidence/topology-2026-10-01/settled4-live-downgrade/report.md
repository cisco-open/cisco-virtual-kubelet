# CVK gNOI lab round trip

Started: 2026-10-01T07:31:41.141027+00:00

Candidate revision: `665a7954a9def499cd3ee36e972e2b5fe15256f5`

Expected image: `cvk-tas-extentions:665a7954-settled4`

Context: `default`; namespace: `cvk-live`

Each device is verified before moving to the next. JSON files are the exact Kubernetes manifests and results. Automatic acceptance covers standalone Catalyst software commitment, secure gNOI, control-processor load/memory, Kubernetes readiness, and fleet workload readiness/ownership continuity. Unfamiliar or inconclusive output stops progression. Forwarding, routing adjacencies, and application traffic are not tested by this lab harness.

2026-10-01T07:31:44.337032+00:00 — workload baseline captured: 2 ready Pod(s), 2 controller group(s).

2026-10-01T07:31:44.337350+00:00 — Create `DeviceOperation/cycle-20261001073141-fdf61e-cat9k-live-certificates`; manifest `cycle-20261001073141-fdf61e-cat9k-live-certificates.manifest.json`.

2026-10-01T07:31:48.138049+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-certificates`: manager-owned network worker binding observed before execution.

2026-10-01T07:31:48.384180+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-certificates`: `Succeeded` — 5 certificate(s) installed

2026-10-01T07:31:48.384664+00:00 — Create `DeviceOperation/cycle-20261001073141-fdf61e-cat9k-live-baseline-verify`; manifest `cycle-20261001073141-fdf61e-cat9k-live-baseline-verify.manifest.json`.

2026-10-01T07:31:52.258515+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-baseline-verify`: manager-owned network worker binding observed before execution.

2026-10-01T07:31:52.543279+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-baseline-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T07:31:53.122216+00:00 — Create `DeviceOperation/cycle-20261001073141-fdf61e-cat9k-live-baseline-health`; manifest `cycle-20261001073141-fdf61e-cat9k-live-baseline-health.manifest.json`.

2026-10-01T07:32:20.061528+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-baseline-health`: manager-owned network worker binding observed before execution.

2026-10-01T07:32:20.394728+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-baseline-health`: `Succeeded` — 5 command(s) completed

2026-10-01T07:32:20.675010+00:00 — `cat9k-live` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T07:32:23.008559+00:00 — Create `DeviceOperation/cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-verify`; manifest `cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-verify.manifest.json`.

2026-10-01T07:32:43.174391+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-10-01T07:32:43.399829+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T07:32:43.905577+00:00 — Create `DeviceOperation/cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-health`; manifest `cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-health.manifest.json`.

2026-10-01T07:32:47.770186+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-health`: manager-owned network worker binding observed before execution.

2026-10-01T07:32:48.042376+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-health`: `Running` — operation is running

2026-10-01T07:32:58.312732+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-10-01T07:32:58.567981+00:00 — `cat9k-live` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T07:32:58.569045+00:00 — Create `IOSXESoftwareRollout/cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout`; manifest `cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout.manifest.json`.

2026-10-01T07:33:04.762478+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout`: approved frozen plan `sha256:6a10d2f6901c21ea1792304a2fc04b3143c99d1735325b96c10fe82b9ee93955` as `system:admin`.

2026-10-01T07:33:05.115360+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout`: `AwaitingApproval` — waiting for approval of sha256:6a10d2f6901c21ea1792304a2fc04b3143c99d1735325b96c10fe82b9ee93955

2026-10-01T07:33:15.416600+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout`: `Executing` — campaign is executing within frozen topology budgets

2026-10-01T07:33:26.266779+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-10-01T07:51:57.497982+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-10-01T08:02:00.657958+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-10-01T08:02:11.201420+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-10-01T08:02:11.439861+00:00 — Create `DeviceOperation/cycle-20261001073141-fdf61e-cat9k-live-downgrade-verify`; manifest `cycle-20261001073141-fdf61e-cat9k-live-downgrade-verify.manifest.json`.

2026-10-01T08:02:35.009500+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-verify`: manager-owned network worker binding observed before execution.

2026-10-01T08:02:35.339989+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-10-01T08:02:35.939439+00:00 — Create `DeviceOperation/cycle-20261001073141-fdf61e-cat9k-live-downgrade-health`; manifest `cycle-20261001073141-fdf61e-cat9k-live-downgrade-health.manifest.json`.

2026-10-01T08:03:12.670341+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-health`: manager-owned network worker binding observed before execution.

2026-10-01T08:03:12.989583+00:00 — `cycle-20261001073141-fdf61e-cat9k-live-downgrade-health`: `Succeeded` — 5 command(s) completed

2026-10-01T08:03:13.263864+00:00 — `cat9k-live` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T08:03:17.838407+00:00 — Required worker-log evidence incomplete: unsafe workload drain evidence: Force deleting pod in running state

2026-10-01T08:03:18.905315+00:00 — STOPPED: required evidence collection incomplete: manager/network/app worker logs. Preserve all operation objects; no automatic cleanup or retry was attempted.
