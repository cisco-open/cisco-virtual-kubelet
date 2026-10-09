# Catalyst Center SWIM live qualification — 2026-10-08

Status: **supervised end-to-end SWIM qualification passed**. Catalyst Center
upgraded `.101` from 17.18.03 to committed 17.18.04. The CVK rollout, upgrade
leaf and controller handoff all reached `Succeeded`; native verification and
the health soak passed, the canonical lease was released, and maintenance
settled with the node Ready, schedulable and untainted.

Cleanup and controller inventory synchronization were supervised lab actions.
Manifest-driven automatic cleanup is **not implemented**. The controller's
bounded readiness replacement and subsequent activation/verification/release
ran through the deployed CVK implementation.

The earlier, pre-deployment assessment is preserved in [preflight.md](preflight.md).
Its deployment-status statements describe that earlier snapshot.

## Environment

- Branch: `pr/johalley/catc-support`; uncommitted implementation under test.
- Cluster: Ubuntu16, Kubernetes v1.35.8+k3s1.
- Namespace: `cvk-live`; manager release `cisco-vk` in `cisco-vk-system`.
- Target: `cat9k-lab-101`, 198.51.100.101, serial FOC2520L6H1.
- Native baseline: 17.18.03.0.5496; intended target: 17.18.04.
- Catalyst Center instance: `catc`, UID e4506c10-8a8f-40b2-89b7-4187940627fb.
- Pinned appliance device: 70601ab8-5104-46bc-a061-1ffbaf108c16.
- Pinned image: 058d4e65-d4d7-41a4-9f6e-ee5ac63c9276, 17.18.04.0.759.
- Imported-image catalogue presence is verified; vendor integrity is not claimed.

## Live findings and fixes

1. The managed-namespace RBAC audit rejected the new SWIM worker's read access.
   Added an exact compiled role contract and checks for the canonical live
   controller binding, subject and service account. Namespaces containing inline
   CiscoDevice passwords remain rejected. Broadened roles, stale ownership and
   extra subjects remain rejected.
2. A pod selected but never evicted by a cancelled drain was incorrectly routed
   as an already device-clean completion when subsequently deleted normally.
   Corrected the narrow no-eviction case to ordinary deletion, retaining normal
   mutation-lease and maintenance checks. Partial eviction evidence remains closed.
3. An unowned CVK maintenance taint blocked cancellation recovery. Direct node
   repair was rejected by managed-node admission; admission was not disabled or
   impersonated. Added an explicit CiscoDevice recovery annotation bound to the
   exact cancelled drain operation and session. The manager requires idle,
   request-free canonical lease state, no software mutation authority, completed
   drain pod records and an unexpired recovery window before removing only the
   exact non-preexisting maintenance taint. Unrelated taints remain untouched.

## Attempts and safety gates

- Initial planning stopped while fresh network evidence was absent after worker
  replacement. No upgrade leaf was created.
- `r2`: BlockIfRunning correctly stopped when lab workloads returned to the target;
  cancelled before mutation.
- `r3`: PDB-aware drain evicted and natively cleaned the first workload. Replacement
  app activation initially failed on .100, preventing the second eviction. Normal
  cancellation and the tested recovery fixes settled the campaign and all its
  reservations. Native .101 inventory subsequently reported no hosted apps.
- `r4`: a fresh approved plan remained blocked by unhealthy lab workload evidence;
  cancelled before creating an upgrade leaf.
- The restored second lab fixture experienced the same uncertain app activation
  on .101. Its routine mutation lease was retained, rather than cleared manually.
  Last observed renewal: 13:38:24 UTC, duration 1860 seconds; earliest expiry
  14:09:24 UTC. The failed fixture was scaled down for native cleanup. The other
  fixture remains Running on .100. Restore both saved replica counts after testing.

The expired routine lease subsequently became idle. The upstream pod-deletion
queue retained its native cleanup request with a long retry backoff. A direct
worker-Pod restart request was rejected by managed-worker admission; no policy
was disabled or identity impersonated. The existing worker remains running to
preserve its queued cleanup. At 14:18 UTC, native CLI still showed the app
DEPLOYED and 17.18.03.0.5496 committed.

Native app cleanup completed through the normal worker retry at 14:22:44 UTC.
`show app-hosting list` then reported no apps. The `r5` attempt exposed a further
queue-recovery defect: the cancelled `r3` leaf had no initialized software phase,
but retained a settled drain and an older inventory of its never-evicted Pod.
The queue now recognizes this strictly empty software state with exact manager
and worker settlement bindings. Remaining inventory UIDs are accepted only for
explicitly released, complete Pods with no eviction or cleanup markers.
Ambiguous/started software state and partially evicted Pods remain blocking.
The software-upgrade, maintenance and controller race suites pass with this fix.
`r5` was cancelled and settled before deploying build `20261008-5`.

## Live SWIM handoff

`r6` planning was rejected before a fresh network observation was accepted.
`r7` acquired the canonical mutation lease and received manager maintenance
acknowledgement. Live validation exposed an uppercase declared serial versus
lowercase canonical manager identity mismatch; the adapter now uses the existing
`topologyidentity.CanonicalPhysicalIdentity` function. Adapter and full race
suites passed after that correction.

Fresh readiness task: `01a11c29-809f-7cbe-b520-65281e822493`. Six complete checks
qualified the manifest-pinned image, including the explicit HTTPS/SCP fallback.
Distribution was submitted at 15:38:39 UTC through the handoff, with task
`01a11c2b-9ff7-7a65-aa15-3d7734f696e1`. Its correlated child workflow is
`01a11c2b-adef-73d4-9b06-02d3ad2c1014`. The live API reports type `DISTRIBUTE`,
not `DISTRIBUTION`; the observer and a captured-wire-shape regression test now
match that contract. The distribution task tree subsequently reported successful HTTPS transfer,
image verification, install add and inventory sync. Native inventory showed
17.18.03.0.5496 committed and 17.18.04.0.759 inactive; the transferred archive
size was 1,249,489,705 bytes. No distribution resubmission was made.

Second readiness task: `01a11c36-b3e3-78f2-96a6-2c2548e6c1cd`. Its flash check
reported 1,938 MB free against 2,620 MB required, so activation remained blocked.
The rollout was explicitly paused (control revision 2) with this reason.
At that checkpoint the canonical lease and maintenance reservation remained held.
The recovery below subsequently completed the same operation without clearing
the lease manually or resubmitting distribution.

## Validation

`go test -race ./...` passed. Focused maintenance, provider, manager, controller,
SWIM and mutation-guard regression tests passed. Real Kubernetes API-server
tests passed for SWIM ownership enforcement and rollout/leaf schema round trips
(`envtest.log`).

Full deployment snapshots and execution timeline are retained at
`/tmp/cvk-swim-live-20261008` on the workstation and Ubuntu16. Credential files
and Secret objects are deliberately excluded from this evidence directory.

## Successful supervised recovery

1. Retrieved the obsolete `flash:gNOI_iosxe_17.18.02.0.4112.1766116039.bin`
   to Ubuntu16. Its 1,247,897,709 bytes and SHA-256 matched the invalidated
   preparation receipt. Fresh native SHA-512 matched the archived copy.
   Boot/install state, device serial, paused parent, current canonical lease
   and receipt invalidation were checked immediately before a one-shot removal.
   The archive was absent afterward; committed 17.18.03 and staged 17.18.04
   were unchanged. Free flash increased to 3,281,846,272 bytes.
2. Added bounded readiness replacement under a new device-worker grant. Each
   stage permits at most three submissions with a minimum one-minute interval;
   prior task/claim/stage/time receipts are retained. Missing responses,
   ambiguous dispatches, duplicate task IDs and revoked grants cannot replay.
   The first replacement task `01a11c62-78c5-713b-a7d0-558e2c1b336f` still
   saw the old cached 1,938 MB free-space value, so the rollout was paused again.
3. Submitted the documented device inventory sync API, task
   `01a11c64-4f18-72f4-9238-a4d115065496`. The device-correlated child reported
   successful synchronization. Resumed with control revision 5. Fresh readiness
   task `01a11c66-b50a-7c8b-9b63-a0325bc9a6d9` saw **3,129 MB** free against
   **2,620 MB** required and passed the six qualified checks. The narrowly
   configured positive HTTPS/SCP fallback remained the only warning exception.
4. CVK submitted activation task `01a11c68-4e89-7db5-91e6-7828fc8de4e5`.
   Its correlated `ACTIVATE` workflow `01a11c68-57ae-7b99-8837-10680698cc76`
   completed successfully. Native CLI first showed activated/uncommitted,
   then **committed 17.18.04.0.759**, with the auto-abort timer inactive.
   No manual commit or direct activation was performed.
5. The XE worker independently verified running version
   `17.18.04.0.759.1784396682` and committed native inventory. The handoff and
   leaf succeeded; the manager completed its health soak and settled maintenance.
   The canonical lease is idle, the node is Ready and schedulable without a
   maintenance taint, and both original demo workloads remain Running/Ready on
   `.100`. See `completed-state.json` and `device-committed.jsonl`.

`final-state.json` and `device-final.jsonl` preserve the earlier paused checkpoint;
`completed-state.json` is the subsequent successful outcome. Cleanup dispatch,
result, archive digest, sync task tree, readiness results and control patches are
included separately. The retained image backup is on Ubuntu16 at
`/tmp/cvk-swim-live-20261008/retired-17.18.02.bin`; image binaries and credentials
are excluded from this repository. `supervised-cleanup.py` is the exact one-shot
lab recovery script, not a general production remediation executor.

## Deployment and validation

Final deployment: Helm revision 176; manager and Catalyst Center worker image
`cvk-catc-handoff:20261008-9`, XE workers `cvk-catc-handoff:20261008-5`.
The final controller binary SHA-256 is
`1468d8c61b1b7a6abc7620382e3dcb826f01d051ccab6985981f757bee5fc0db`.
XE workers were retained through the operation. NX-OS remained on its original
image. No canonical lease was force-cleared and no managed admission policy
was disabled or impersonated.

The recovery changes passed `go test -race ./...`; after the final bounded
controller-backoff change, the adapter race suite passed again. Kubernetes
API-server tests verified ownership, schema round trips and durable readiness
history (`readiness-envtest.log`). Controller observation backoff is capped at
30 seconds so long pauses do not produce multi-minute recovery delays.

## Remaining work

- The cleanup and inventory-synchronization gaps identified by this historical
  supervised run are now implemented. See the separate
  [automatic remediation qualification](automatic-remediation/README.md) for
  manifest-driven deletion and successful synchronization evidence and its
  same-version readiness limitation. gNOI File.Get/Stat are unavailable on the
  tested XE release, so exact archive hashing/deletion reuses the existing
  constrained XE SSH transport; gNOI OS verification remains mandatory.
- Surface named readiness blockers in durable user-facing status.
- Qualify 26.02.01 and downgrade cycles; this run qualified `.101` to 17.18.04.
- Resolve the separate uncertain app-activation/long deletion-retry behavior and
  appliance/cluster clock skew without weakening evidence freshness checks.
