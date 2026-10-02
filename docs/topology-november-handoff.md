# November topology roadmap handoff

Checkpoint date: **2 October 2026**. Continue on
**`pr/johalley/tas-extentions`**. This is a saved development checkpoint for
November release work, **not a merge recommendation or completed roadmap**.
Some packages are closed for an explicitly bounded scope, but the complete
E00–E13 roadmap still has the external gates listed below. Do not promote this
branch as a gap-free multi-platform/service-continuity solution on the strength
of the physical software-transition results alone.

Latest review through `25da0918`: exact runtime `6f3686e9` completed a
topology-budgeted three-device 17.18.03 to 17.18.02 downgrade and return to
17.18.03. All six leaves settled, exact secure Verify and post-mutation health
passed, all Nodes returned Ready and the ledger emptied. `c1680a7b` restores
the standard-library-only `kubectl-ciscovk` release boundary and removes the
uncached-runner heartbeat race from Kubernetes 1.37 native TAS conformance.
The exact physical record is the
[E13 matrix](evidence/topology-2026-10-02/e13-final-candidate-physical-matrix.md).
Remote CI/review of the final published head remains mandatory.

Historical review update through `34050731`:
the execution plan's
[**C0–C9 queue**](topology-roadmap-execution.md#concrete-completion-queue-c0c9)
is authoritative and supersedes the older N1–N5 queue and historical inventory
below. Manager-accepted evidence, claim-time authority, administrator
protections, continuous post-operation soak, overlapping risk groups and
worker byte pacing are implemented. Helm revision 131 ran exact candidate
`2f27f302` on Ubuntu16. Physical `.103` downgrade and reverse upgrade both
completed, including exact no-replay recovery from lost terminal IOS XE
Install responses. At that checkpoint, install-only preparation was still
unqualified; neither the earlier NoReboot timeout nor the new combined Reload
runs proved a durable staging boundary. Durable staging and separate
activation were implemented and qualified later. Independent loaded/service-
path qualification remains substantive work.

Execution history: `3212f777` completed the C0 `.103`
observation-only recovery and C1 binding/freeze increment on Helm revision 119.
The leaf, parent soak, Lease and maintenance taint settled normally with no
activation replay. C2 now has a physically qualified manager-acceptance trust
boundary at `90bc690c`; resume with its measured-load/rollback remainder before
C3. C3 then advanced through `2f27f302`; see the
[physical pacing and recovery record](evidence/topology-2026-10-01/c3-physical-pacing/README.md).
Treat C4 Install-only qualification as an independent guarded investigation.

## Start here

1. Read the [current evidence index](evidence/topology-2026-10-02/README.md)
   first, then the [historical index](evidence/topology-2026-10-01/README.md),
   especially the distinction between device success and stopped evidence
   collection.
2. Use the [execution plan](topology-roadmap-execution.md) as the authoritative
   implementation/test ledger. Its E00–E13 and F01–F13 IDs define completion.
3. Use the [design roadmap](topology-roadmap.md) for architecture, exclusions
   and the wider T0–T10 scope. This handoff supplies the restart order, not a
   narrower replacement roadmap.

The commit containing this document saves the runtime changes, regression
tests, harness corrections, sanitized evidence and remaining-work ledger.
Find its immutable identity with:

```sh
git log -1 --format=fuller -- docs/topology-november-handoff.md
```

## What is saved and what it proves

| Area | Evidence or saved implementation | Boundary |
| --- | --- | --- |
| Combined software lifecycle | Serial campaigns on `.100`, `.101`, `.103` reached 17.18.03 and returned to 17.18.02; manifests, frozen plans, leaves, Verify and CLI health results retained | Earlier dirty `665a7954-fix1` builds; not independent preparation/activation, traffic continuity or final candidate acceptance |
| Latest `.101` downgrade | `665a7954-settled5`, 08:04–08:34 UTC, 1 October; `Succeeded`, gNOI `17.18.02.0.4112.1766116039`, settled drain/session, replacement Pods Ready on `.100` | Original collector **STOPPED** during app-log classification, before saving manager/network logs; no full end-to-end harness pass |
| Drain callback repair | Exact released device-clean completion recognized after promotion to `SoftwareMutation`, settlement and Pod disappearance; regression tests retained | Released completion is acknowledgement-only, not authority for another device teardown. Broader lifecycle/fault/service qualification remains E07 |
| Retained worker history | Wrong-bound worker skips forbidden leaf status updates | Missing/new binding, read failures, unresolved predecessors and real-API zero-dispatch tests still required |
| Observation work | Restart-safe allocation, bounded diagnostics and native Pod-UID checks, followed by manager-owned acceptance at `90bc690c`, native bound-token denial, three-device CLI comparison and manager-restart qualification | Loaded directional-rate accuracy, remaining concurrency/lost-response and mixed-version rollback cases, plus redundant-supervisor qualification remain C2. |
| Clean candidate qualification | `9e578131` image and Helm revision 112 deployed on Ubuntu16; all three physical targets reported Ready, complete UID-bound observations and healthy topology/gNOI conditions; `.103` replacement worker advanced from sequence one to three | Read-only evidence only. Direct CLI/secure OS.Verify, complete log planes, claim-time enforcement and E04 preparation/activation remain open. See [`9e578131-observation-validation.md`](evidence/topology-2026-10-01/9e578131-observation-validation.md). |
| Graph diagnostics | Structured hash/field bounds, deterministic conflicts and a read-only CLI over manager-accepted evidence. Candidate `e5660db4` passed physical remote-port tests. Candidate `34050731` passed exact physical peer mappings/declarations, protected ConfigMap provenance, unchanged rollout-policy hashing, replacement-Pod fail-closed behavior and declaration-only drift/restoration. | Finish remaining protocol/VRF/LAG and live authorization/restart fixtures, then perform controlled isolated physical-link drift/restore. The graph never grants disruption authority. |
| Harness | Explicit source/target validation, direction-specific runs, binding observation, log-plane capture attempt, exact preceding cleanup-log correlation | Binding observation does not establish RPC order; offline marker correlation does not prove service health or complete drain safety |
| Physical pacing / lost Install response | Candidate `2f27f302`, Helm revision 131, completed `.103` 17.18.03 → 17.18.02 → 17.18.03 with a frozen 25 MB/s ceiling, exact native Install corroboration, no replay, secure Verify, CLI health, network soak and empty final ledger | Worker/device acknowledgement measurement only; no independent forwarding load, service-continuity, alternate/singleton/critical/congested path qualification or independent staging |

The saved full `.101` app log has preceding clean acknowledgements and
`ProviderDeleteSuccess` for both deleted Pod UIDs. The revised classifier
rejects later-only proof, another namespace/UID and prefix matches. The
original STOPPED report is preserved, not rewritten as a pass. Leaf timestamps
independently record the manager's ordered eviction/device-clean decisions;
these remain control-plane evidence, not an external forwarding probe.

### Historical source and image provenance

- Historical base SHA: `665a7954a9def499cd3ee36e972e2b5fe15256f5`.
- Historical settled-follow-up tag: `cvk-tas-extentions:665a7954-settled5`.
- Reported OCI index digest:
  `sha256:3970030086b07fb6db2f582ec83358cae2548555ae39f25cca68f31eb2635efd`.
- Archived `.101` app-container image ID:
  `sha256:5e5bb79240d2e8806b296e63d7c98ca88e6d32561186c476fd28e047c78abb93`.
  An OCI index and a resolved platform/container image ID are not interchangeable.
- The physical binary embeds the base SHA, but was built from a dirty tree.
  This checkpoint also contains later harness/doc changes. Neither the base
  SHA nor this new checkpoint is proof of the complete historical image input.
  `checkpoint-source.json` fingerprints the saved source, **not** the dirty
  build. A clean rebuild/redeployment is mandatory for final acceptance.

The [verification record](evidence/topology-2026-10-01/verification.md) records
local checkpoint tests separately from physical tests and outstanding CI,
envtest, admission and generation gates.

## Lab inventory to revalidate before resuming

| Scope | Historical value | Required fresh check |
| --- | --- | --- |
| Lab control host | SSH alias `ubuntu16`, account `cisco`, address `192.0.2.43` | Reconfirm host ownership and CI exclusions; do not use Ubuntu17 or other CI nodes implicitly |
| Cluster | On Ubuntu16: context `default`, k3s `v1.35.8+k3s1` | Verify server identity/version; never assume the workstation's `default` is this cluster |
| CVK | Latest qualified run: Helm revision 157 / manager and all six C9K app/network workers `cvk-tas-extentions:6f3686e9`; older Nexus worker was outside the cohort | Capture current values with credentials redacted, chart/CRD/admission hashes and every manager/app/network image ID before later work |
| Device inventory | `cvk-live`: `cat9k-live` = `198.51.100.100`; `cat9k-lab-101` = `.101`; `cat9k-lab-103` = `.103` | Match physical serial, CR/Node UID, worker identities and sole owner before any mutation |
| Workloads | `cvk-pr194-workloads`; two `cvk-topology-drain-{a,b}-0926` Deployments, PDB `cvk-topology-drain-0926` | Fresh baseline, portable package/signing/storage support, spare destination capacity and independent endpoint probes |
| Source | SFTP `10.0.2.2`, `/home/cisco/cvk-gnoi-images/`, Secret reference `pr194-iosxe-image-source` | Check image bytes, reachability, trust and Secret metadata/ownership; never print or archive Secret data |
| Exclusions | Historical duplicate `cat9k-node` and unqualified `nexus9300v-live` | Investigate ownership read-only; do not adopt, delete or clean retained objects to make tests pass |

Historical source checksums (recompute before use):

| Image | SHA-256 |
| --- | --- |
| `cat9k_iosxe.17.18.03.SPA.bin` | `df6055e4e1e88135b311998d721ff6d20a94a475113c1ff678fb65d14dc11049` |
| `cat9k_iosxe.17.18.02.SPA.bin` | `c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355` |

The latest qualified matrix leaves `.100`, `.101` and `.103` conclusively
committed and running exact `17.18.03.0.5496.1776157760`. All three Nodes were
Ready, every post-mutation health gate and secure gNOI Verify passed, and the
topology ledger was empty. This is not an archived continuous fleet-stability
or service-continuity test. The lab remains provisioned; this document does
not reserve it indefinitely or assert its present state when read later.

A historical read-only Node listing before the final matrix reported all three
C9Ks Ready with `.100`/`.103` on 17.18.2 and `.101` on 17.18.3. That earlier
Node-only discrepancy was resolved by direct CLI and secure OS.Verify before
the final campaigns. As a standing rule, do not infer device software state
from a Node listing alone; repeat both checks before choosing a future
direction.

## Exact next implementation increments

The core code work for manager-accepted evidence, preparation receipts,
separate activation, graph diagnostics, pacing, no-replay recovery and
same-cluster handoff is complete for its stated scope. Resume only with the
following still-open gates; do not rebuild completed mechanisms under new
names.

| Order | Remaining gate | Required execution and acceptance |
| --- | --- | --- |
| 1 | E02/E03 independent network-path qualification | Provide an independent traffic generator/probe, documented redundant and singleton paths, critical-service labels, a controllable congested link and predeclared rate/loss/outage tolerances. Exercise positive and zero-RPC negative cases while correlating accepted evidence, grants, claims and device telemetry. |
| 2 | E07 physical workload continuity | Supply a portable signed app package supported by every target and an independent endpoint probe. Run automatic drain/replacement through both image directions, PDB and hard-placement negatives, no-spare-capacity, manager/worker restart and cancel. The unsigned `.101` fixture without `sdd-120` remains an expected unsupported case. |
| 3 | E08-C/D native TAS lifecycle | Select a Kubernetes-supported native Workload controller for the pinned optional-version lane, then prove grouped creation/replacement, physical owner lifecycle and group-aware drain on CVK Nodes. Keep the feature opt-in and native; add no third-party scheduler. |
| 4 | E04/E05 lifecycle extension | Define and qualify device-side image removal/replacement plus an explicit receipt-invalidation contract that cannot orphan or silently transfer prepared ownership. Existing immutable receipts must not be cleared manually. |
| 5 | E11 second platform | Obtain a qualified platform/image pair and service evidence for NX-OS, IOS XR or another driver. Run capability discovery and the same preparation/activation/security matrix before deciding whether the public API can become generic. |
| 6 | E12-D and broader scale | Provide a second cluster and offline transfer procedure; prove one owner, uncertain-operation fencing and rollback. Measure production throughput/resources beyond the synthetic 100-target envelope. |
| 7 | Final wider E13 | On the candidate containing the applicable increments, rerun complete CI, migrations, RBAC/admission, physical upgrade/downgrade, service/path probes and durable sanitized evidence. Unavailable inputs remain named exclusions. |

Do not bypass guards, disable admission, widen functional accounts or
force-delete Pods to obtain a green test. A successful IOS XE image transition
cannot substitute for forwarding, application, second-platform or
cross-cluster evidence.

The current review adds publisher negative/preservation tests, same-Pod restart
recovery/overflow coverage, bounded malformed-source publication, and
`TestEnvtest_NetworkObservationStatusRoundTrip`.
The latter runs CRD validation with an administrative client, without Helm
admission policies. Do not count it as E01-C/E bound-token authorization.
Keep local test exit codes and pass/skip counts: `go test` without `-tags
envtest` may report success with no matching tests.

This preserves the native-Kubernetes constraint and the two functional
ServiceAccount model with read-only/read-write role options. Do not introduce
a third-party scheduler or return to one account per device.

## Resume procedure and test sequence

### 1. Restore the branch and verify the archive

Start with a clean worktree; preserve any later work instead of resetting it.

```sh
git fetch origin
git switch pr/johalley/tas-extentions
git status --short
git log -5 --oneline
# Only with no overlapping local work:
git pull --ff-only origin pr/johalley/tas-extentions
cd docs/evidence/topology-2026-10-01
shasum -a 256 -c SHA256SUMS
```

The archive contains historical Kubernetes objects, not templates to replay.
Never apply saved result/leaf objects, reuse historical UIDs/session tokens or
replay approval hashes. Generate a new campaign and inspect its frozen plan.

### 2. Re-establish E00 without changing devices

On the verified Ubuntu16 host, use an explicit context. Capture outputs in a
new restricted run directory with UTC timestamps; sanitize before committing.

```sh
kubectl --context default cluster-info
kubectl --context default get nodes -o wide
kubectl --context default -n cvk-live get ciscodevices
kubectl --context default -n cvk-live get iosxesoftwarerollouts,iosxesoftwareupgrades
kubectl --context default -n cvk-pr194-workloads get deploy,pods,pdb -o wide
kubectl --context default -n cvk-live get leases
helm list -n cisco-vk-system
```

Also inspect exact maintenance/ledger/ownership records and bound worker UIDs
using the E00 procedure. Correlate secure OS.Verify with device `show version`,
`show install summary`, `show gnxi state detail`, processor, app-hosting,
storage and actual routing/interface inventory. `GNOIConfigurationReady`
alone explicitly does not prove device connectivity. Retain device-console
and Kubernetes timestamps, not just final screenshots. Verify a management
recovery path before any later disruptive step.

### 3. Implement/test the next increment locally and on a disposable API server

From the repository root:

```sh
go test -race ./...
python3 -m unittest discover -s scripts/tests -p 'test_iosxe_gnoi_lab_cycle*.py'
helm lint charts/cisco-virtual-kubelet
mkdocs build --strict --site-dir /tmp/cvk-november-docs
git diff --check
```

For E00-G use the pinned `setup-envtest` install command in `Makefile`, then
`make test-envtest`; record test counts and skips. Follow the execution plan's
generation/chart/admission matrix. Run disposable kind/TAS tests with a
dedicated kubeconfig, never the physical lab's context. Pin optional TAS
versions/images to the repository lane and check actual served schemas.

### 4. Commit, build and deploy a clean candidate

Record a clean Git SHA, toolchain, chart/CRD/admission hashes and both OCI
index and resolved platform digests. Deploy that exact candidate to manager
and both worker planes, then verify every running container's identity.
Do not reuse `665a7954-settled5` or treat the embedded dirty-build SHA as
reproducibility. Before mutation, pass the relevant admission/RO negatives
and re-establish ownership, no unresolved claims and independent service
baseline. Capture image-source trust references without credential bytes.

### 5. Perform the physical integration test for that increment

Use `python3 scripts/iosxe-gnoi-lab-cycle.py --help` to construct explicit
arguments: context, namespace, evidence directory, full revision, deployed
image, exact device-name/address pairs, both version/URL/SHA tuples and the
SFTP Secret **reference**. Without `--execute`, the harness creates read-only
DeviceOperation probes in Kubernetes but does not request Install/Activate.
It is not an entirely API-read-only command.

Only after the relevant gates pass, add `--execute` for the scoped combined
regression. Run one verified target at a time. A resumed single-direction run
needs `--only-direction` and a freshly verified `--start-version`; it must not
inherit the old baseline or claim the other direction. This harness does not
implement the future staged/activation or service-path tests; add those
scenario-specific tests as E03–E08 are implemented.

Show/save the manifest before applying it; record target/source selection and
new approval identity/hash. Observe rollout, leaf, Pod/PDB, drain/session,
claim/Lease/ledger and actual device behavior throughout. Run independent
traffic probes with declared outage tolerance. Reconcile any stopped report
before advancing to the next device. Unknown activation outcome must retain
guards and enter the documented recovery path, not trigger a blind replay.

### 6. Close only the tested scope

Record successful and failed test IDs, exact commits/digests, skipped cases,
device releases, logs and final health/ownership. Capture every log plane
even if one validator fails; do not let the first error discard later logs.
Verify final device software, app inventory and service probes, scheduling
guards, settled sessions, Leases and reservations independently. Sanitize,
checksum and commit the new evidence next to an updated completion ledger.
Only E13 acceptance can establish release readiness; missing hardware or
service probes must remain explicit blockers, not be relabelled as complete.
