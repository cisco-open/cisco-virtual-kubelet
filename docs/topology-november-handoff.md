# November topology roadmap handoff

Checkpoint date: **1 October 2026**. Continue on
**`pr/johalley/tas-extentions`**. This is a saved development checkpoint for
November release work, **not a merge recommendation or completed roadmap**.
No E00–E13 package has all its exit gates closed. Do not promote this branch
on the strength of the physical software-transition results alone.

Review update through `37db63c2`: the execution plan's
[**C0–C9 queue**](topology-roadmap-execution.md#concrete-completion-queue-c0c9)
is authoritative and supersedes the older N1–N5 queue and historical inventory
below. Restart-safe publication, interval/schema safety and a real native
bound-token suite have advanced. The manager currently runs `11ae6704`;
`.103` remains fenced after a lost NoReboot activation response, despite a
Ready Node reporting 17.18.2. Start with C0 evidence/recovery and C1 binding
ordering/full-path planning regressions, not another three-device cycle.
Install-only preparation is still unqualified; the NoReboot timeout does not
prove it unsupported. Manager acceptance, claim-time/soak network protection,
durable staging and independent activation remain substantive implementation.

Execution follow-up: `3212f777` subsequently completed the C0 `.103`
observation-only recovery and C1 binding/freeze increment on Helm revision 119.
The leaf, parent soak, Lease and maintenance taint settled normally with no
activation replay. C2 now has a physically qualified manager-acceptance trust
boundary at `90bc690c`; resume with its measured-load/rollback remainder before
C3. Treat C4 Install-only qualification as an independent guarded
investigation.

## Start here

1. Read the [evidence index](evidence/topology-2026-10-01/README.md), especially
   the distinction between device success and stopped evidence collection.
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
| Graph helper | Structured hash/field bounds, remote-port reverse identity and two-input conflict correction (`4711d3c7`) | Three-way permutations and duplicate-device cases still need qualification; no production consumer or physical drift acceptance. |
| Harness | Explicit source/target validation, direction-specific runs, binding observation, log-plane capture attempt, exact preceding cleanup-log correlation | Binding observation does not establish RPC order; offline marker correlation does not prove service health or complete drain safety |

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
| CVK | Helm `cisco-vk` in `cisco-vk-system`; historical revision 107, now revision 118 / manager `11ae6704` at this review | Capture current values with credentials redacted, chart/CRD/admission hashes and every manager/app/network image ID |
| Device inventory | `cvk-live`: `cat9k-live` = `198.51.100.100`; `cat9k-lab-101` = `.101`; `cat9k-lab-103` = `.103` | Match physical serial, CR/Node UID, worker identities and sole owner before any mutation |
| Workloads | `cvk-pr194-workloads`; two `cvk-topology-drain-{a,b}-0926` Deployments, PDB `cvk-topology-drain-0926` | Fresh baseline, portable package/signing/storage support, spare destination capacity and independent endpoint probes |
| Source | SFTP `10.0.2.2`, `/home/cisco/cvk-gnoi-images/`, Secret reference `pr194-iosxe-image-source` | Check image bytes, reachability, trust and Secret metadata/ownership; never print or archive Secret data |
| Exclusions | Historical duplicate `cat9k-node` and unqualified `nexus9300v-live` | Investigate ownership read-only; do not adopt, delete or clean retained objects to make tests pass |

Historical source checksums (recompute before use):

| Image | SHA-256 |
| --- | --- |
| `cat9k_iosxe.17.18.03.SPA.bin` | `df6055e4e1e88135b311998d721ff6d20a94a475113c1ff678fb65d14dc11049` |
| `cat9k_iosxe.17.18.02.SPA.bin` | `c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355` |

The latest saved `.101` result is 17.18.02. Two replacement Pods were Ready
on `.100` and PDB allowed disruptions was 1. Later read-only inspection
reported all three Nodes Ready/untainted; this is not an archived continuous
fleet stability test. No new lab mutation or cleanup was performed to create
this checkpoint. The lab remains provisioned; this document does not reserve
it indefinitely or assert its present state when read later.

A subsequent read-only Node listing reported all three C9Ks Ready with
`.100`/`.103` on 17.18.2 and `.101` on 17.18.3. The discrepancy with archived
`.101` downgrade evidence requires fresh CLI and secure OS.Verify before
choosing the next direction; a Node listing alone cannot resolve it.

## Exact next implementation increments

The following ordering is retained as the historical work-package mapping.
Use the current execution plan's C0–C9 deliverables, including the explicit
uncertainty-recovery and Install-only gates, for new execution. Its latest
checkpoint distinguishes completed subtests from remaining work.

Each increment should be a reviewable commit on this branch with its tests,
examples and evidence index updated. Do not wait until the end to test the
integration. Code locations, negative cases and acceptance details are in the
corresponding execution-plan section.

| Order | Deliverable / code boundary | Tests before the dependent next step |
| --- | --- | --- |
| 1 | E00 evidence/tooling and binding: repair initialization ordering in DeviceOperation/software-upgrade reconcilers; distinguish absent binding from permanently wrong identity; retain old-worker history read-only | E00-E/F/G/H: exact status-write/RPC spies, delayed/wrong/missing binding, API read error, worker replacement with settled and unresolved leaves; real API server; no denial hot loop or premature mutation; reproducible generation twice |
| 2 | E01 trustworthy observation + E02 measured inputs: provider/driver identities, manager-owned acceptance, admission and RO wiring as one security increment | E01-A–E/E02-A–C: authenticated bound-token peer/stale-Pod denial, same-Pod process restart/replay, VRF/process adjacency, timeout/incomplete publication, valid zero vs absent/overflow rate, old/new schema compatibility, isolated controlled traffic and supported supervisor evidence |
| 3 | E03 policy: overlapping risk groups, transfer pacing and continuous soak/recovery revalidation; bounded administrator critical-service/singleton-path protection, expiring evidence-bound grants and claim-time enforcement are implemented through `617b1cfc` | Complete remaining E03-A/B real-API/CAS negatives before E03-C–F physical redundant/single-path/congested/critical-service tests. Assert zero forbidden RPCs after stale/unhealthy evidence or grant expiry |
| Parallel to 3 | E04 device boundary qualification, before designing the supported staged contract | E04-A–D in both image directions: prove exactly what Install/NoReboot does, durable prepared identity, behavior across restart and explicit activation. An incapable C9K/release stays blocked; a combined reload is not a staged pass |
| 4 | E05 durable receipts/retained ownership, then E06 separate activation approval, windows, phase reservations and uncertainty recovery | E05-A–C/E06-A–E: API/admission/fault tests first, then E04-qualified hardware; stage across restarts/closed windows, independent approval, cancel/expiry and zero unapproved activation |
| 5 | E08-A fail-closed group recognition before E07 hard placement/broader drain eligibility | E07-A–D: automatic relocation with supported packages, PDB, required placement/capacity negatives, manager/worker restart and independent probes in both directions. Manual scale-down does not qualify automatic drain |
| 6 | E10 graph correctness and manager/CLI diagnostics; E09 transfer measurement and explicit cache decision | E10-A–D: true old-hash-collision regression, conflicting-peer order, local/remote port identity, bounds, accepted provenance, declared-link drift and isolated link restore. E09-A measures both transfer segments; only implement B–D durable cache if justified |
| 7 | E12 controlled ownership transfer before physical optional TAS move; E08 native lifecycle; E11 second platform and E12 scale | E12-B/C/D sole-authority and uncertain-operation tests, E08-B–D real scheduler/CVK lifecycle. E11-A–D needs a qualified second platform/image pair. E12-A measures 1/10/50/100 targets against explicit budgets; three C9Ks do not establish scale |
| 8 | E13 one pinned integrated candidate, migrations, security and end-user documentation | Core F01–F08/F13 plus applicable F09–F12; clean candidate CI, baseline/optional-version lanes, physical upgrade and downgrade, restoration and complete durable evidence. All unresolved exclusions stay visible |

Highest-priority unresolved safety/qualification issues are observation write
ownership/replay, claim-time network enforcement, initialization binding
ordering, and incomplete drain/service evidence. Also reproduce the transient
`NodeProjectionFailed` resource-version conflicts/uninitialized taints across
multiple reconcile periods (E00-D). Do not bypass these guards, disable
admission, widen accounts or force-delete Pods to obtain a green test.

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
