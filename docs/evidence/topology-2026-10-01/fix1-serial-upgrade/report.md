# CVK gNOI lab round trip

Started: 2026-09-30T23:13:06.749955+00:00

Candidate revision: `665a7954a9def499cd3ee36e972e2b5fe15256f5`

Expected image: `cvk-tas-extentions:665a7954-fix1`

Context: `default`; namespace: `cvk-live`

Each device is verified before moving to the next. JSON files are the exact Kubernetes manifests and results. Automatic acceptance covers standalone Catalyst software commitment, secure gNOI, control-processor load/memory, Kubernetes readiness, and fleet workload readiness/ownership continuity. Unfamiliar or inconclusive output stops progression. Forwarding, routing adjacencies, and application traffic are not tested by this lab harness.

2026-09-30T23:13:10.043973+00:00 — workload baseline captured: 2 ready Pod(s), 2 controller group(s).

2026-09-30T23:13:10.044651+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-live-certificates`; manifest `cycle-20260930231306-1b8162-cat9k-live-certificates.manifest.json`.

2026-09-30T23:13:33.646557+00:00 — `cycle-20260930231306-1b8162-cat9k-live-certificates`: manager-owned network worker binding observed before execution.

2026-09-30T23:13:33.903240+00:00 — `cycle-20260930231306-1b8162-cat9k-live-certificates`: `Succeeded` — 5 certificate(s) installed

2026-09-30T23:13:33.904416+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-live-baseline-verify`; manifest `cycle-20260930231306-1b8162-cat9k-live-baseline-verify.manifest.json`.

2026-09-30T23:13:54.211460+00:00 — `cycle-20260930231306-1b8162-cat9k-live-baseline-verify`: manager-owned network worker binding observed before execution.

2026-09-30T23:13:54.467512+00:00 — `cycle-20260930231306-1b8162-cat9k-live-baseline-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-09-30T23:13:55.042362+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-live-baseline-health`; manifest `cycle-20260930231306-1b8162-cat9k-live-baseline-health.manifest.json`.

2026-09-30T23:14:15.276286+00:00 — `cycle-20260930231306-1b8162-cat9k-live-baseline-health`: manager-owned network worker binding observed before execution.

2026-09-30T23:14:15.567616+00:00 — `cycle-20260930231306-1b8162-cat9k-live-baseline-health`: `Succeeded` — 5 command(s) completed

2026-09-30T23:14:15.857126+00:00 — `cat9k-live` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-09-30T23:14:18.938499+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-101-certificates`; manifest `cycle-20260930231306-1b8162-cat9k-lab-101-certificates.manifest.json`.

2026-09-30T23:14:29.256538+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-certificates`: manager-owned network worker binding observed before execution.

2026-09-30T23:14:29.523873+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-certificates`: `Succeeded` — 5 certificate(s) installed

2026-09-30T23:14:29.524907+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-101-baseline-verify`; manifest `cycle-20260930231306-1b8162-cat9k-lab-101-baseline-verify.manifest.json`.

2026-09-30T23:14:33.325100+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-baseline-verify`: manager-owned network worker binding observed before execution.

2026-09-30T23:14:33.604079+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-baseline-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-09-30T23:14:34.123975+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-101-baseline-health`; manifest `cycle-20260930231306-1b8162-cat9k-lab-101-baseline-health.manifest.json`.

2026-09-30T23:14:47.774484+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-baseline-health`: manager-owned network worker binding observed before execution.

2026-09-30T23:14:47.999214+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-baseline-health`: `Succeeded` — 5 command(s) completed

2026-09-30T23:14:48.258245+00:00 — `cat9k-lab-101` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-09-30T23:14:52.292740+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-103-certificates`; manifest `cycle-20260930231306-1b8162-cat9k-lab-103-certificates.manifest.json`.

2026-09-30T23:14:59.388461+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-certificates`: manager-owned network worker binding observed before execution.

2026-09-30T23:14:59.625075+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-certificates`: `Succeeded` — 5 certificate(s) installed

2026-09-30T23:14:59.625438+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-103-baseline-verify`; manifest `cycle-20260930231306-1b8162-cat9k-lab-103-baseline-verify.manifest.json`.

2026-09-30T23:15:03.400821+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-baseline-verify`: manager-owned network worker binding observed before execution.

2026-09-30T23:15:03.689704+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-baseline-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-09-30T23:15:04.226849+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-103-baseline-health`; manifest `cycle-20260930231306-1b8162-cat9k-lab-103-baseline-health.manifest.json`.

2026-09-30T23:15:18.002380+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-baseline-health`: manager-owned network worker binding observed before execution.

2026-09-30T23:15:18.270559+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-baseline-health`: `Succeeded` — 5 command(s) completed

2026-09-30T23:15:18.536695+00:00 — `cat9k-lab-103` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-09-30T23:15:19.977535+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-live-upgrade-before-verify`; manifest `cycle-20260930231306-1b8162-cat9k-live-upgrade-before-verify.manifest.json`.

2026-09-30T23:15:33.688330+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-09-30T23:15:33.968221+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-before-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-09-30T23:15:34.540868+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-live-upgrade-before-health`; manifest `cycle-20260930231306-1b8162-cat9k-live-upgrade-before-health.manifest.json`.

2026-09-30T23:15:54.730295+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-before-health`: manager-owned network worker binding observed before execution.

2026-09-30T23:15:54.990953+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-09-30T23:15:55.265529+00:00 — `cat9k-live` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-09-30T23:15:55.266362+00:00 — Create `IOSXESoftwareRollout/cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout`; manifest `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout.manifest.json`.

2026-09-30T23:15:56.086113+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout`: approved frozen plan `sha256:5a771759b8467d53898ad3d9f391ebeeb43ba1291e83ae72f4df80781cdd000b` as `system:admin`.

2026-09-30T23:15:56.340484+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout`: `AwaitingApproval` — frozen plan created; approval of the exact plan hash is required

2026-09-30T23:16:06.627601+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout`: `Executing` — campaign is executing within frozen topology budgets

2026-09-30T23:16:28.050300+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-09-30T23:32:29.953486+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-09-30T23:42:32.740474+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-09-30T23:42:43.279363+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-09-30T23:42:43.574882+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-live-upgrade-verify`; manifest `cycle-20260930231306-1b8162-cat9k-live-upgrade-verify.manifest.json`.

2026-09-30T23:42:57.362941+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-verify`: manager-owned network worker binding observed before execution.

2026-09-30T23:42:57.585398+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-09-30T23:42:58.155723+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-live-upgrade-health`; manifest `cycle-20260930231306-1b8162-cat9k-live-upgrade-health.manifest.json`.

2026-09-30T23:43:18.348308+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-health`: manager-owned network worker binding observed before execution.

2026-09-30T23:43:18.593948+00:00 — `cycle-20260930231306-1b8162-cat9k-live-upgrade-health`: `Succeeded` — 5 command(s) completed

2026-09-30T23:43:18.852142+00:00 — `cat9k-live` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-09-30T23:43:26.506725+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-before-verify`; manifest `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-before-verify.manifest.json`.

2026-09-30T23:43:49.968665+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-09-30T23:43:50.187506+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-before-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-09-30T23:43:50.750343+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-before-health`; manifest `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-before-health.manifest.json`.

2026-09-30T23:43:54.599243+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-before-health`: manager-owned network worker binding observed before execution.

2026-09-30T23:43:54.851123+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-09-30T23:43:55.128095+00:00 — `cat9k-lab-101` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-09-30T23:43:55.129684+00:00 — Create `IOSXESoftwareRollout/cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout`; manifest `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout.manifest.json`.

2026-09-30T23:44:01.257200+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout`: approved frozen plan `sha256:45df897401ba15b45ff65294621cda603df2a9ac6ddd6f7c0062961800978e6a` as `system:admin`.

2026-09-30T23:44:01.512210+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout`: `AwaitingApproval` — waiting for approval of sha256:45df897401ba15b45ff65294621cda603df2a9ac6ddd6f7c0062961800978e6a

2026-09-30T23:44:11.839263+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout`: `Executing` — campaign is executing within frozen topology budgets

2026-09-30T23:44:22.606860+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-09-30T23:59:22.263586+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-10-01T00:09:36.170784+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-10-01T00:09:36.410997+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-verify`; manifest `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-verify.manifest.json`.

2026-10-01T00:09:53.310929+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-verify`: manager-owned network worker binding observed before execution.

2026-10-01T00:09:53.620095+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T00:09:54.124526+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-health`; manifest `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-health.manifest.json`.

2026-10-01T00:10:11.144898+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-health`: manager-owned network worker binding observed before execution.

2026-10-01T00:10:11.410878+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-health`: `Succeeded` — 5 command(s) completed

2026-10-01T00:10:11.668291+00:00 — `cat9k-lab-101` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T00:10:18.676940+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-before-verify`; manifest `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-before-verify.manifest.json`.

2026-10-01T00:10:22.486397+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-before-verify`: manager-owned network worker binding observed before execution.

2026-10-01T00:10:22.768291+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-before-verify`: `Succeeded` — running version: 17.18.02.0.4112.1766116039

2026-10-01T00:10:23.268306+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-before-health`; manifest `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-before-health.manifest.json`.

2026-10-01T00:10:40.257140+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-before-health`: manager-owned network worker binding observed before execution.

2026-10-01T00:10:40.494026+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-before-health`: `Succeeded` — 5 command(s) completed

2026-10-01T00:10:40.709778+00:00 — `cat9k-lab-103` accepted on `17.18.02`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T00:10:40.710327+00:00 — Create `IOSXESoftwareRollout/cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout`; manifest `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout.manifest.json`.

2026-10-01T00:10:46.853723+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout`: approved frozen plan `sha256:03fd35393dceb134d96a0357d90cfd436b8d401ec35b9a1280563790b832d611` as `system:admin`.

2026-10-01T00:10:47.122027+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout`: `AwaitingApproval` — waiting for approval of sha256:03fd35393dceb134d96a0357d90cfd436b8d401ec35b9a1280563790b832d611

2026-10-01T00:10:57.620623+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout`: `Executing` — campaign is executing within frozen topology budgets

2026-10-01T00:11:08.404373+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout`: observed CVK's owned NoSchedule maintenance taint during `Executing`.

2026-10-01T00:23:17.860463+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout`: `Soaking` — campaign is holding reservations during the continuous post-mutation health soak

2026-10-01T00:33:20.587773+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout`: `Executing` — campaign is waiting for a reservation, worker acknowledgement, or health gate

2026-10-01T00:33:31.076471+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout`: `Succeeded` — all targets completed and passed their post-mutation health gates

2026-10-01T00:33:31.384346+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-verify`; manifest `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-verify.manifest.json`.

2026-10-01T00:33:41.679458+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-verify`: manager-owned network worker binding observed before execution.

2026-10-01T00:33:41.907624+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-verify`: `Succeeded` — running version: 17.18.03.0.5496.1776157760

2026-10-01T00:33:42.371360+00:00 — Create `DeviceOperation/cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-health`; manifest `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-health.manifest.json`.

2026-10-01T00:33:46.136311+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-health`: manager-owned network worker binding observed before execution.

2026-10-01T00:33:46.391984+00:00 — `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-health`: `Succeeded` — 5 command(s) completed

2026-10-01T00:33:46.677009+00:00 — `cat9k-lab-103` accepted on `17.18.03`: committed software, Ready, secure/provisioned OS service, healthy processor load/memory, and CVK's maintenance taint cleared. Hosted application fleet readiness/owner continuity is recorded; a PDB-protected drain may intentionally move inventory away from the target. Forwarding/traffic not tested.

2026-10-01T00:33:51.998854+00:00 — PASS: every target completed upgrade with fresh gNOI, platform, workload, and worker evidence.
