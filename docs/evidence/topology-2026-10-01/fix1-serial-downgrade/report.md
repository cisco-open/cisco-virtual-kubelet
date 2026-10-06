# CVK gNOI lab round trip

Started: 2026-10-01T00:34:05.216711+00:00

Candidate revision: `665a7954a9def499cd3ee36e972e2b5fe15256f5`

Expected image: `cvk-tas-extentions:665a7954-fix1`

Context: `default`; namespace: `cvk-live`

Each device is verified before moving to the next. JSON files are the exact Kubernetes manifests and results. Automatic acceptance covers standalone Catalyst software commitment, secure gNOI, control-processor load/memory, Kubernetes readiness, and fleet workload readiness/ownership continuity. Unfamiliar or inconclusive output stops progression. Forwarding, routing adjacencies, and application traffic are not tested by this lab harness.

2026-10-01T00:34:08.754660+00:00 — workload baseline captured: 2 ready Pod(s), 2 controller group(s).

2026-10-01T00:34:08.755524+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-live-certificates`; manifest `cycle-20261001003405-6721c9-cat9k-live-certificates.manifest.json`.

2026-10-01T00:34:19.221819+00:00 — `cycle-20261001003405-6721c9-cat9k-live-certificates`: manager-owned network worker binding observed before execution.

2026-10-01T00:34:19.522483+00:00 — `cycle-20261001003405-6721c9-cat9k-live-certificates`: `Succeeded` — 5 certificate(s) installed

2026-10-01T00:34:19.523675+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-live-baseline-verify`; manifest `cycle-20261001003405-6721c9-cat9k-live-baseline-verify.manifest.json`.

2026-10-01T00:34:23.384392+00:00 — `cycle-20261001003405-6721c9-cat9k-live-baseline-verify`: manager-owned network worker binding observed before execution.

2026-10-01T00:34:23.688665+00:00 — `cycle-20261001003405-6721c9-cat9k-live-baseline-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T00:34:24.204102+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-live-baseline-health`; manifest `cycle-20261001003405-6721c9-cat9k-live-baseline-health.manifest.json`.

2026-10-01T00:34:47.715996+00:00 — `cycle-20261001003405-6721c9-cat9k-live-baseline-health`: manager-owned network worker binding observed before execution.

2026-10-01T00:34:47.994328+00:00 — `cycle-20261001003405-6721c9-cat9k-live-baseline-health`: `Succeeded` — 5 command(s) completed

2026-10-01T00:34:48.282593+00:00 — `cat9k-live` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T00:34:52.111723+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-101-certificates`; manifest `cycle-20261001003405-6721c9-cat9k-lab-101-certificates.manifest.json`.

2026-10-01T00:35:02.654832+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-certificates`: manager-owned network worker binding observed before execution.

2026-10-01T00:35:02.894546+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-certificates`: `Succeeded` — 5 certificate(s) installed

2026-10-01T00:35:02.895073+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-101-baseline-verify`; manifest `cycle-20261001003405-6721c9-cat9k-lab-101-baseline-verify.manifest.json`.

2026-10-01T00:35:06.790551+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-baseline-verify`: manager-owned network worker binding observed before execution.

2026-10-01T00:35:07.038133+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-baseline-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T00:35:07.623793+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-101-baseline-health`; manifest `cycle-20261001003405-6721c9-cat9k-lab-101-baseline-health.manifest.json`.

2026-10-01T00:35:21.223270+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-baseline-health`: manager-owned network worker binding observed before execution.

2026-10-01T00:35:21.502650+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-baseline-health`: `Succeeded` — 5 command(s) completed

2026-10-01T00:35:21.736297+00:00 — `cat9k-lab-101` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T00:35:25.020402+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-103-certificates`; manifest `cycle-20261001003405-6721c9-cat9k-lab-103-certificates.manifest.json`.

2026-10-01T00:35:35.286732+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-certificates`: manager-owned network worker binding observed before execution.

2026-10-01T00:35:35.550697+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-certificates`: `Succeeded` — 5 certificate(s) installed

2026-10-01T00:35:35.551619+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-103-baseline-verify`; manifest `cycle-20261001003405-6721c9-cat9k-lab-103-baseline-verify.manifest.json`.

2026-10-01T00:35:39.352634+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-baseline-verify`: manager-owned network worker binding observed before execution.

2026-10-01T00:35:39.571247+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-baseline-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T00:35:40.116503+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-103-baseline-health`; manifest `cycle-20261001003405-6721c9-cat9k-lab-103-baseline-health.manifest.json`.

2026-10-01T00:35:56.971630+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-baseline-health`: manager-owned network worker binding observed before execution.

2026-10-01T00:35:57.278075+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-baseline-health`: `Succeeded` — 5 command(s) completed

2026-10-01T00:35:57.568936+00:00 — `cat9k-lab-103` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T00:35:59.679104+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-live-downgrade-before-verify`; manifest `cycle-20261001003405-6721c9-cat9k-live-downgrade-before-verify.manifest.json`.

2026-10-01T00:36:10.143744+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-10-01T00:36:10.403062+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-before-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T00:36:10.952546+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-live-downgrade-before-health`; manifest `cycle-20261001003405-6721c9-cat9k-live-downgrade-before-health.manifest.json`.

2026-10-01T00:36:14.765654+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-before-health`: manager-owned network worker binding observed before execution.

2026-10-01T00:36:15.049744+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-10-01T00:36:15.333561+00:00 — `cat9k-live` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T00:36:15.334249+00:00 — Create `IOSXESoftwareRollout/cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout`; manifest `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout.manifest.json`.

2026-10-01T00:36:21.513994+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout`: approved frozen plan `sha256:03a81b37435416efeec7d8fb9a26bccd70557464a1d1b1e3e3997db2b1057197` as `system:admin`.

2026-10-01T00:36:21.759033+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout`: `AwaitingApproval` — waiting for approval of sha256:03a81b37435416efeec7d8fb9a26bccd70557464a1d1b1e3e3997db2b1057197

2026-10-01T00:36:32.069415+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-10-01T00:36:42.861916+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-10-01T00:53:06.807807+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-10-01T01:03:09.945282+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-10-01T01:03:30.997154+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-10-01T01:03:31.242047+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-live-downgrade-verify`; manifest `cycle-20261001003405-6721c9-cat9k-live-downgrade-verify.manifest.json`.

2026-10-01T01:03:48.086521+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-verify`: manager-owned network worker binding observed before execution.

2026-10-01T01:03:48.335844+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-10-01T01:03:48.839235+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-live-downgrade-health`; manifest `cycle-20261001003405-6721c9-cat9k-live-downgrade-health.manifest.json`.

2026-10-01T01:03:52.629293+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-health`: manager-owned network worker binding observed before execution.

2026-10-01T01:03:52.931271+00:00 — `cycle-20261001003405-6721c9-cat9k-live-downgrade-health`: `Succeeded` — 5 command(s) completed

2026-10-01T01:03:53.259944+00:00 — `cat9k-live` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T01:03:59.967068+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-before-verify`; manifest `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-before-verify.manifest.json`.

2026-10-01T01:04:07.056221+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-10-01T01:04:07.342348+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-before-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T01:04:07.899258+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-before-health`; manifest `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-before-health.manifest.json`.

2026-10-01T01:04:31.563345+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-before-health`: manager-owned network worker binding observed before execution.

2026-10-01T01:04:31.847886+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-10-01T01:04:32.161408+00:00 — `cat9k-lab-101` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T01:04:32.162115+00:00 — Create `IOSXESoftwareRollout/cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout`; manifest `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout.manifest.json`.

2026-10-01T01:04:33.031315+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout`: approved frozen plan `sha256:d4d960d828b22dc6e43f951346dc1551630e1f851c6da42a8ba0ebc69b3529a3` as `system:admin`.

2026-10-01T01:04:33.298261+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout`: `AwaitingApproval` — frozen plan created; approval of the exact plan hash is required

2026-10-01T01:04:43.645840+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout`: `Executing` — campaign is executing within frozen topology budgets

2026-10-01T01:04:54.444052+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-10-01T01:20:04.059899+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-10-01T01:30:06.550780+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-10-01T01:30:27.705591+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-10-01T01:30:28.074635+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-verify`; manifest `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-verify.manifest.json`.

2026-10-01T01:30:38.526678+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-verify`: manager-owned network worker binding observed before execution.

2026-10-01T01:30:38.761761+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-10-01T01:30:39.289431+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-health`; manifest `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-health.manifest.json`.

2026-10-01T01:30:43.033964+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-health`: manager-owned network worker binding observed before execution.

2026-10-01T01:30:43.303742+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-health`: `Succeeded` — 5 command(s) completed

2026-10-01T01:30:43.616356+00:00 — `cat9k-lab-101` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T01:30:51.105374+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-before-verify`; manifest `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-before-verify.manifest.json`.

2026-10-01T01:30:54.960208+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-10-01T01:30:55.196563+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-before-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T01:30:55.734626+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-before-health`; manifest `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-before-health.manifest.json`.

2026-10-01T01:30:59.620440+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-before-health`: manager-owned network worker binding observed before execution.

2026-10-01T01:30:59.888584+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-10-01T01:31:00.141302+00:00 — `cat9k-lab-103` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T01:31:00.141890+00:00 — Create `IOSXESoftwareRollout/cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout`; manifest `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout.manifest.json`.

2026-10-01T01:31:06.280010+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout`: approved frozen plan `sha256:40aaa1856faeddca77721f72d69aff81d6b88e5384de5e8cb54e31d06a6752ac` as `system:admin`.

2026-10-01T01:31:06.551750+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout`: `AwaitingApproval` — waiting for approval of sha256:40aaa1856faeddca77721f72d69aff81d6b88e5384de5e8cb54e31d06a6752ac

2026-10-01T01:31:16.862703+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout`: `Executing` — campaign is executing within frozen topology budgets

2026-10-01T01:31:27.715555+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-10-01T01:43:28.098738+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-10-01T01:53:30.434404+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-10-01T01:53:30.751613+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-verify`; manifest `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-verify.manifest.json`.

2026-10-01T01:53:44.411379+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-verify`: manager-owned network worker binding observed before execution.

2026-10-01T01:53:44.689629+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-10-01T01:53:45.205473+00:00 — Create `DeviceOperation/cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-health`; manifest `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-health.manifest.json`.

2026-10-01T01:54:05.425718+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-health`: manager-owned network worker binding observed before execution.

2026-10-01T01:54:05.673732+00:00 — `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-health`: `Succeeded` — 5 command(s) completed

2026-10-01T01:54:05.931404+00:00 — `cat9k-lab-103` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T01:54:11.340097+00:00 — PASS: every target completed downgrade with fresh gNOI, platform, workload, and worker evidence.
