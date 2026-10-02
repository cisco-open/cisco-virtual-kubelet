# Topology roadmap: execution and acceptance plan

Status: **implemented and reviewed through `34050731`; roadmap not complete**,
2 October 2026.
Start with the current checkpoint and C0–C9 execution queue below. The
[November handoff](topology-november-handoff.md) preserves historical evidence
and release boundaries. Reviewed branch: `pr/johalley/tas-extentions`.
Historical physical evidence below is
from `665a7954` plus runtime changes through `0884666e`. The initial physical deployment
used the rebuilt image `cvk-tas-extentions:665a7954-fix1` (full binary revision
`665a7954a9def499cd3ee36e972e2b5fe15256f5`) on Ubuntu16. That binary embeds
the base commit while the build includes uncommitted runtime changes; it is
not a reproducible clean-commit candidate. The latest follow-up used
`665a7954-settled5`; its stopped report and offline analysis are preserved in
the [evidence index](evidence/topology-2026-10-01/README.md). The wider E03–E13
roadmap remains open; this execution added a fresh disruptive six-rollout
qualification of the currently implemented combined path on all three target
C9Ks and recorded its limitations below.

This document converts the [topology roadmap](topology-roadmap.md) into work
packages with implementation scope, test procedures and completion gates.
The roadmap retains the architecture and exclusions. This plan distinguishes
implemented evidence from required work. Items under "Required updates" and
the acceptance matrices are requirements until an explicit result closes
them; proposed API concepts are not apply-ready YAML.

### Current checkpoint (1 October 2026, lab read at 17:36 UTC)

This checkpoint supersedes the historical N1–N5 queue below, not the detailed
E00–E13 acceptance requirements. This review used source inspection, saved
test output, read-only lab API queries and GitHub PR/run listings. It did not
deploy code, execute a new upgrade or run a fresh full test suite.

| Area | Verified progress | Still required |
| --- | --- | --- |
| Source / CI | Clean branch at `37db63c2`, matching local remote-tracking ref; GitHub branch PR and workflow-run queries both returned empty | Publish a reviewable candidate and obtain candidate-specific CI; a local remote-tracking ref is not a fresh remote-head attestation |
| Observation/schema safety | `bc155820`: nonblank headroom interface scope; collection-start freshness and interval validation in publisher, consumer and admission. `c7023691`: runtime admission digest synchronized with rendered policy | Manager-owned acceptance, original contributing-source age, concurrency/lost-ack, cross-device and mixed-version qualification; do not reimplement the completed sequence/schema fixes |
| Native authorization | Saved `/tmp/cvk-managed-shared-worker-kind.log` ends with a full integration pass on Kubernetes `v1.35.0`, including retained Lease, split-plane/stale-token, native Node guards and foreground deletion cases | Archive sanitized log and exact tested inputs; unbound peer-Pod denial is not proof of the complete distinct-device/namespace matrix. Retain two functional accounts, not per-node accounts |
| Planning/admission | `11ae6704` fixes network-enabled plan freeze reading nil `status.effectivePolicy`; manager rechecks current network evidence at admission | The new test exercises only the freshness helper. Add an actual freeze/Reconcile regression with nil status; worker claim-time and recovery/soak network enforcement remain absent |
| Current lab | Ubuntu16 server `v1.35.8+k3s1`; Helm revision 118 deployed; manager Deployment Ready `1/1`, image tag `cvk-tas-extentions:11ae6704`; all three C9K Nodes Ready and reporting `17.18.2` | Capture resolved manager/app/network image IDs, fresh CLI and secure Verify, paths/services and current ownership. Node status is not device inventory or service continuity |
| `.103` safety state | Leaf `roadmap-noreboot-20261001-103-cat9k-lab-103-8a61c53c` remains `Failed/ActivationOutcomeUnknown`; maintenance `NoSchedule` taint and disruptive Lease remain held by that leaf UID | Read-only reconciliation and audited recovery before another mutation on `.103`; never delete the fence to resume testing |
| Preparation boundary | Recorded `OS.Install` validated `17.18.03.0.5496.1776157760`; subsequent `Activate(NoReboot=true)` lost its response. Historical secure Verify returned running `17.18.02.0.4112.1766116039` | An install-only hold was **not tested**. This is neither a successful staged receipt nor proof that this C9K cohort cannot support one |

The [OpenConfig OS contract](https://github.com/openconfig/gnoi/blob/master/os/os.proto)
distinguishes Install from Activate: Activate changes the next-boot selection
even when reboot is suppressed. Verify reports running state. Consequently,
old-version Verify cannot settle a lost NoReboot response. Pin the actual gNOI
dependency/proto revision in E04 evidence; the upstream contract is not proof
of a particular IOS-XE implementation.

The NoReboot narrative is [saved](evidence/topology-2026-10-01/11ae6704-noreboot-boundary.md),
but its complete raw object/log/console bundle is not yet archived in the
branch. The tested runtime remains `11ae6704`; `37db63c2` is a documentation
checkpoint, not another deployed image. Historical six-rollout success must
not be relabelled as acceptance of this candidate.

### C0/C1 execution follow-up (`3212f777`, 18:25 UTC)

C0's retained `.103` incident and C1's implementation increment are complete
for this candidate. Exact IOS-XE CLI and secure gNOI evidence proved the
validated 17.18.03 target committed and running. The new worker recovered the
terminal outcome using read-only Verify, never replayed activation, released
the disruptive Lease only after durable exact-target settlement, completed
the parent's continuous health soak, and removed the maintenance taint through
normal manager reconciliation. The full record is the
[C0 outcome audit](evidence/topology-2026-10-01/c0-103-activation-outcome-audit.md).

C1 added the managed DeviceOperation pre-binding wait with zero status/device
dispatch, exact device/Pod binding enforcement, network-only worker Pod UID
wiring, and a real network-enabled freeze regression with unpublished
`status.effectivePolicy`. Repository-wide race, pinned envtest, Helm/render,
strict docs/license, shared-worker admission and native topology integration
lanes passed. Helm revision 119 converged all three C9K app and network workers
to the exact clean image. Candidate remote CI, the delayed live-binding/API
error submatrix and two-pass generator parity remain E00/C9 acceptance work;
they do not reopen the resolved `.103` mutation outcome.

The next implementation package is C2. C4 may proceed in parallel only on an
isolated, unquarantined target with a structurally Install-only harness; the
completed NoReboot recovery is not that qualification.

### C2 acceptance-boundary follow-up (`90bc690c`, 18:56 UTC)

The first C2 increment is implemented and physically qualified. Worker-owned
`healthObservation.network` is now raw input; the manager separately owns
`acceptedNetwork`, validates exact device/revision/Pod/sequence/hash/interval
provenance and preserves collection-start freshness. Rollout consumers use
only the accepted field. Native Kubernetes 1.35 admission proved that a real
bound worker token cannot forge acceptance, and the full race/envtest/render
matrix passed. Helm revision 120 converged the manager and all six C9K workers
to `90bc690c`. All three devices produced complete accepted samples matching
fresh IOS XE interface/CDP CLI, survived a manager restart and remained Ready,
untainted and mutation-settled. See the
[C2 qualification record](evidence/topology-2026-10-01/c2-manager-accepted-network-evidence.md).

C2 is not closed: controlled loaded ingress/egress comparison has no declared
traffic source or tolerance, redundant-supervisor hardware is unavailable,
and the reverse old-manager/new-worker plus explicit rollback matrix remains
open. C3 must not treat headroom or supervisor state as qualified disruption
authority until those gates pass. Publisher concurrency and lost-response
coverage are completed by the follow-up below.

### C2 rate-provenance follow-up (`95077ba7`, 19:48 UTC)

Directional capacity/rates and their IOS-XE model source are now retained in
raw and accepted observations; the manager independently recomputes every
derived headroom value. Optimistic-lock concurrency, lost-response replay,
same-Pod monotonicity and producer replacement have explicit tests. Physical
execution found an OpenAPI signed-bound rounding defect that local Kubernetes
1.35 did not expose; `95077ba7` uses the JSON-safe `2^53-1` ceiling and locks it
with a generated-schema regression test.

Helm revision 122 converged the manager and all six workers on the exact clean
candidate. All three C9Ks published and received manager acceptance for
44–46 rate-qualified interfaces, survived a manager-only restart and remained
Ready and untainted. The manager-new/old-worker transition failed closed. See
the [C2 rate-provenance record](evidence/topology-2026-10-01/c2-rate-provenance-followup.md).

C2 remains open for controlled non-management-path load with a predeclared
tolerance, appropriate redundant hardware, reverse old-manager/new-worker and
explicit rollback qualification. These prerequisites keep headroom out of C3
disruption authority; they do not reopen the completed publisher concurrency
and lost-response tests.

### C3 claim-time authority follow-up (`9c48a98c`, 20:30 UTC)

The first C3 trust-boundary increment is implemented and qualified. The
manager performs an uncached final read, binds its grant to the exact accepted
network-evidence digest/producer/Pod/sequence and original-sample expiry, and
may renew only to a complete newer tuple with a strictly later expiry. The
worker performs an uncached exact-tuple check before every new mutation claim.
Expired or replaced evidence yields zero new durable claims and zero activation
markers; an already accepted device RPC remains observable and is not replayed.

The repository-wide race suite, pinned Kubernetes 1.35 envtest suite, native
tuple-transition tests, deterministic generation and Helm/render checks passed.
Helm revision 123 converged the manager and all six C9K workers to the exact
candidate. Fresh samples matched each replacement network-worker Pod UID,
secure gNOI `OS.Verify` succeeded on all three switches, all Nodes remained
Ready and untainted, and the settled log window was clean. No image mutation
was performed solely to exercise a negative fence. See the
[C3 qualification record](evidence/topology-2026-10-01/c3-claim-time-network-authority.md).

C3/E03 remains open for byte pacing, continuous
recovery/soak policy and the physical redundant/singleton/critical/congested
service-path matrix. C2's controlled loaded-rate and compatibility
prerequisites also remain open.

### C3 administrator-protection follow-up (`617b1cfc`, 21:00 UTC)

Bounded administrator-owned `CriticalService` and `SingletonPath` selector
rules now fail closed during target freeze and current-policy revalidation
before execution. Rules use only protected required topology keys, are included
in both policy hashes and preserve the previous hash/wire document when
omitted. Unit/race, pinned envtest, chart and documentation suites passed.
Physical retained-value deployment found and repaired a nil-list Helm upgrade
defect before any resource was applied. Helm revision 124 then converged the
manager and all six C9K workers to exact candidate `617b1cfc`; current accepted
observations and three secure read-only gNOI Verify operations passed. No live
protection rule or software mutation was used. E03 remains open for pacing,
recovery/soak enforcement and qualified physical
redundant/singleton/critical/congested service-path scenarios.

### C3 continuous recovery/soak follow-up (`ae4f3a7b`, 21:20 UTC)

An enabled network gate now remains authoritative after the device operation
and throughout every continuous-soak reconciliation. Settlement requires a
manager-accepted sample whose collection started after operation completion,
and re-evaluates the complete current interface/neighbor/headroom/completeness,
freshness and worker-binding policy. A failed or replaced sample retains the
reservation and resets soak continuity; accepted work is observed, not
replayed. Omitted/disabled network policy preserves prior behavior.

The full race, pinned Kubernetes 1.35 envtest, render and strict documentation
suites passed. Helm revision 125 converged the manager and all six C9K workers
to exact candidate `ae4f3a7b`; accepted samples matched current worker Pod UIDs,
all Nodes remained Ready/schedulable/untainted, and three secure read-only
gNOI Verify operations succeeded. Expected fail-closed replacement conflicts
ended at convergence and the explicit settled log window was clean. See the
[C3 recovery/soak record](evidence/topology-2026-10-01/c3-continuous-network-recovery-soak.md).

E03 remains open for byte pacing and an isolated
physical redundant/singleton/critical/congested failed-path matrix. This
read-only physical qualification does not substitute for those fixtures.

### C3 overlapping-risk-group follow-up (`7417b09b`, 21:50 UTC)

The administrator policy now supports bounded, overlapping protected-label
selectors with independent transfer and unavailable ceilings. Planning freezes
each target's memberships and a hash of the complete relevant physical
membership. Admission recomputes that hash, counts unhealthy non-target peers
and all active campaigns, and admits only when every matching group passes in
the same ledger compare-and-swap. Policy or membership drift requires a new
approval.

The full race, pinned Kubernetes 1.35 envtest, generated-CRD validation,
render and strict documentation suites passed. The additive CRD was reviewed
and applied, then Helm revision 127 converged the manager and all six C9K
workers to exact candidate `7417b09b`. Accepted samples matched current worker
Pod UIDs, all Nodes were Ready and untainted, and three secure read-only gNOI
Verify operations succeeded. See the
[C3 risk-group record](evidence/topology-2026-10-01/c3-overlapping-risk-groups.md).

This closes E03-A's code/API overlapping-accounting portion. Byte pacing and
the isolated physical redundant/singleton/critical/congested service-path
matrix remain open; label accounting is not forwarding-path qualification.

### C3 physical pacing and interrupted-Install recovery (`2f27f302`, 23:45 UTC)

Risk-group aggregate byte limits now derive a per-leaf ceiling and the worker
paces the actual gNOI content reader. A guarded IOS XE observer can settle a
lost terminal Install response only when native inventory proves the exact
target, every package is added, activity is quiescent, the exact source package
is verified with the pinned size, and exactly one recent matching `install-add`
operation succeeded. The neutral driver interface is optional; unsupported
drivers remain unchanged. Managed replacement workers also require the exact
plane-specific Pod name and UID before any status write.

Candidate `2f27f302` and Helm revision 131 completed a physical `.103`
17.18.03 → 17.18.02 → 17.18.03 cycle with the frozen 25,000,000 bytes/second
ceiling. Both IOS XE Install streams lost their terminal response after the
full image arrived. CVK retained ownership, accepted only the exact native
proof, issued no duplicate Install, activated the validated target, survived
both reloads and held the disruption reservation through current accepted-
network-evidence soak. Secure gNOI Verify and IOS XE CLI proved the exact final
versions in each direction; the final ledger contained no reservation.

Repository-wide race, pinned Kubernetes 1.35 envtest, two-pass generation,
Helm/render and strict documentation gates passed. See the
[physical pacing record](evidence/topology-2026-10-01/c3-physical-pacing/README.md).
This closes C3's worker-to-device physical pacing and interrupted-Install
recovery portions. E02-B/E03-C–F remain open because there was no independent
traffic source, predeclared measurement tolerance, controllable redundant or
singleton path, critical-service probe, or congested forwarding fixture.

### Concrete completion queue — C0–C9

Each row is a separately reviewable delivery increment, not a claim of
completion. Implement, test and archive its result before advancing through
its dependency. E/F IDs retain the detailed gates later in this document.

Preserve the architecture: native Kubernetes APIs/scheduler/admission/RBAC
and CVK controllers/workers only; no third-party scheduling, workflow or
policy component. Keep two functional worker ServiceAccounts per managed
namespace (app-hosting and network-management) with RO/RW profiles, not
per-device accounts. Human/planner/activation-approver permissions remain
separately bound roles; they must not be granted implicitly to those workers.

| Order / packages | Implementation or deployment deliverable | Verification and exit condition |
| --- | --- | --- |
| **C0 — preserve and recover** / E00, E04, E13 | Archive `.103` rollout/leaf/Lease, manifests, approval, current/previous logs and available device history before rotation. Add a read-only uncertainty diagnosis and an audited resolution path tied to the exact leaf/device/claim identities. Inspect installed/active/committed/next-boot state and in-progress sessions. Retain quarantine unless a qualified procedure proves settlement. | First run recovery unit/API tests for lost response, conflicting inventory, restart, stale actor and operation UID mismatch. On `.103`, correlate secure Verify with `show version`, `show boot`, `show install summary`, supported install detail/log commands and console history. Release fences only through the tested resolution path after conclusive settlement and health proof. If still unknown, leave `.103` blocked; no automatic replay, lease expiry takeover or forced cleanup. |
| **C1 — stable base deployment** / E00, E01 | Fix new-leaf/DeviceOperation reconciliation ordering: absent manager binding waits boundedly without forbidden status writes or transport calls; wrong binding remains denied. Add a real freeze/Reconcile test for the `11ae6704` nil-status case. Validate generator, schema, Helm policy and embedded contract digest together. | Run E00-E–H with exact write/RPC spies, wrong/new/missing binding, API read error, retained uncertain predecessors and worker replacement. Fresh network-enabled plan reaches AwaitingApproval, emits no leaf before approval and blocks stale evidence after approval. Two generation passes produce no additional drift. Deploy a clean pinned image only after local/native admission gates; capture all worker identities and zero unexplained denial loops. |
| **C2 — accepted measured evidence** / E01–E02 | Add manager-owned acceptance of worker samples, protected separately from worker publication, and make consumers use accepted evidence. Bind Pod/revision, sequence/hash and original sample age without moving device reads into the manager. Finish adjacency identity/migration and directional-rate qualification. | E01-A–E/E02-A–C: concurrent producers, stale reads, lost acknowledgements, same-Pod and manager restart, oldest-source expiry, wrong-device bound tokens, RO/RW, old/new schema/chart/binary and rollback. Compare all three devices' CLI to observations; measure idle/loaded ingress and egress against an independent source with tolerance declared before testing. Unsupported supervisor health stays Unknown. |
| **C3 — execution-time network safety** / E03 | Evidence-bound expiring grants, monotonic renewal, uncached worker enforcement, bounded administrator critical-service/singleton-path prohibitions, overlapping risk groups including non-target peers and all campaigns, post-operation continuous network recovery/soak evaluation, and administrator-derived worker byte pacing are implemented. Retain reservations for work already accepted. | Claim-time expiry/replacement/rotation, native tuple transitions, planning/pre-execution protection, risk-group CAS/drift, pacing/fail-closed resolver tests and soak-reset tests pass. Complete tightened-policy/API-lag/restart cases and E03-C–F physical tests with measured pacing, redundant, single-path, critical-service and congested-path scenarios. Labels alone do not qualify redundancy. |
| **C4 — independent Install qualification** / E04 | Build a narrowly scoped, guarded qualification mode/harness that ends after successful Install/Validated and has **no reachable Activate or reboot call**. Keep mutation ownership while investigating. Do not use the existing `strategy: NoReboot` as prepare-only. Independently inspect installed identity and durability after observer/worker restart. | E04-A–D on an isolated, unquarantined target: record baseline/console/service probes, exact digest/version and inventory before/after, hold across restart, detect external removal/replacement and qualify both image directions. C0 settlement is required before using `.103`. Capability discovery/harness fixtures may precede C2/C3; any physical mutation still needs E00 safety, ownership and impact scope. Lack of durable identity is an explicit cohort blocker, not a fabricated pass. |
| **C5 — durable prepare and separate activation** / E05–E06 | After C4 defines platform semantics, implement versioned prepare-only protocol, immutable staged receipt and retained staged ownership; then distinct append-only activation authorization, phase windows and atomic transfer/staged/disruption reservations. Bind receipt to device/Node/leaf identities, source digest/exact installed version, trust/policy and supervisor scope. | E05-A–C/E06-A–E: native admission rejects receipt/approval forgery and RO/app mutation; stage survives manager/worker restart and a closed activation window; changed image/trust/policy invalidates authority. Test cancellation, expiry, crash-before/after dispatch, lost response and restart without replay. Upgrade and downgrade both require the separate authorized activation and measured recovery before budget release. A running old version alone never creates a valid receipt. |
| **C6 — workload continuity and eligibility** / E07, E08-A | Add raw group-field recognition/fail-closed guard before widening drain. Implement supported Deployment/ReplicaSet hard node affinity/selectors/topology spread without relaxing original constraints. Preserve PDB, device-clean acknowledgement and native replacement readiness before disruptive activation. | E07-A–D: capacity/placement failures and PDB 429 block; restart/cancel preserve exact ownership; no forced eviction. Run portable supported applications and continuous endpoint probes through both image directions. Unsigned apps without the required C9K SSD/USB storage are expected unsupported, not an upgrade failure. Full independent-activation integration follows C5. |
| **C7 — graph and distribution** / E09–E10 | Wire bounded graph diagnostics to accepted evidence; prove three-way conflicts/duplicates/order invariance, freshness and declared-link drift before exposing results. Measure source→worker and worker→device traffic, then record the durable-cache decision. | E10-A–D with controlled physical link change/restore; diagnostics never independently grant disruption authority. E09-A measures bytes/time/concurrency/storage for both segments. If measurements justify persistent cache, implement E09-B–D with immutable digest/trust/Secret identity, PVC reservation/GC and disk-full/restart tests; otherwise record an evidence-backed deferral, not a cache implementation pass. |
| **C8 — broader platform, ownership and TAS** / E08, E11–E12 | Implement/test sole-authority staged/uncertain ownership transfer, measure bounded scale, qualify a second lifecycle driver with real images, and then real CVK native TAS group lifecycle. Keep these as independent reviewable changes. | E12-B–D before physical group movement; prove no simultaneous owners and no reuse of uncertain claims. E12-A synthetic API load at 1/10/50/100 targets plus measured real-worker limits. E11-A–D requires a qualified second platform/image pair, not the existing virtual NX-OS Node alone. Before E08-B–D, verify available upstream Kubernetes binaries/APIs/feature gates; the current 1.35 lab and a filename mentioning 1.37 are not TAS qualification. |
| **C9 — one-candidate acceptance** / E13 | Freeze a clean source/chart/CRD/admission/container digest set, publish PR/CI, deploy it with explicit migration order and run the complete applicable acceptance matrix. Update operator examples, supported capabilities and recovery instructions from actual evidence. | F01–F08/F13 core plus F09–F12 wider gates; all three physical targets upgrade and downgrade serially first, then only evidenced safe concurrency. Capture least-privilege approvals, both terminal views, service/path probes, recovery soak, final inventory and fence/budget settlement. Stop on unexplained discrepancy; fixes require a new candidate and affected reruns. Missing prerequisites remain open. |

**Critical path:** C0/C1 → C2 → C3 → C5 → C6 → C9, with C4 gating
C5 and C7/C8 required for the full wider roadmap. C4 read-only discovery,
E09 measurements, graph unit work and platform/TAS inventory can advance
without waiting for every preceding feature; they cannot bypass physical
mutation safety or substitute for their integration gates.

### Deployment and validation recipe for each increment

1. Record clean commit, toolchain, test command/exit code and all image/chart
   digests. Before schema changes, export affected objects and verify that
   the previous reader can consume them; stored duplicate adjacency IDs may
   make the old map schema unsafe to restore.
2. Run focused tests first, then the existing branch gates below in a dedicated
   local/disposable environment. Add the new cases from the C-row; today's
   passing suites do not exercise APIs that have not been implemented.

   ```sh
   make test
   make test-envtest
   bash charts/cisco-virtual-kubelet/tests/topology-render-test.sh
   bash charts/cisco-virtual-kubelet/tests/managed-shared-worker-kind-test.sh --cluster-name cvk-roadmap-acceptance
   python3 -m unittest discover -s scripts/tests -p 'test_iosxe_gnoi_lab_cycle*.py'
   git diff --check
   ```

   Also regenerate CRDs/DeepCopy/RBAC/chart copies using pinned tools twice,
   compare their outputs, run Helm lint, and run the native topology scheduler
   lane plus the optional TAS lane only on its verified API/version. Archive
   server version and admission type-check/readiness results. Envtest does
   not replace the Helm policy/bound-token lane.
3. Establish migration order in the disposable cluster first. Pause new
   admissions and settle or explicitly retain in-flight/staged ownership.
   Use compatible schema/policy/manager/worker transitions with matching
   contract digests; fail closed through mismatches. Do not blindly deploy a
   stricter policy against old publishers. Test rollback compatibility before
   relying on Helm rollback (which does not restore CRD storage semantics).
4. On Ubuntu16, explicitly select `/etc/rancher/k3s/k3s.yaml` for Helm/kubectl;
   never use the workstation's default context. Reconfirm ownership and CI
   exclusions, target serial/UIDs, supported images and signing/storage,
   secrets/trust metadata (not secret values), free capacity and probes.
   Deploy clean immutable images, wait for policy/manager/worker readiness,
   then run read-only CLI/Verify and observation checks before mutation.
5. Canary on one qualified, unquarantined target; verify the new safety gate's
   positive and negative paths, then expand to `.100`, `.101`, `.103` only
   as their gates permit. Preserve uncertain operation/lease evidence; lack
   of a usable canary is a stop condition for mutation, not permission to
   take an unrelated CI node. Restore intended lab state after each test.
6. Save artifacts as the run proceeds under a new run-ID directory in
   `docs/evidence/`: pre-apply manifest, approval actor/hash, API/event timeline,
   worker/manager identities and current/previous logs, device console/show
   output, RPC claim correlation, traffic/service samples, final state,
   exact commands/exit codes and checksums. Redact credentials/tokens/keys.
   Missing console or probes is a missing gate, not implicit success.

### Prerequisites to resolve before promising full completion

| Input / responsible execution lane | Required resolution | If unavailable |
| --- | --- | --- |
| `.103` uncertain state / C0 lifecycle recovery | **Resolved at `3212f777`:** conclusive installed/running/committed evidence and exact-identity audited settlement | Reopen quarantine on any conflicting later inventory; C4 still needs an independent Install-only test |
| Real service paths and traffic / E00, C2–C3 | Record wiring, redundant vs singleton paths, load/probe endpoints, affected services and predeclared loss/rate tolerances | Accounting/fixture tests only; no forwarding or headroom qualification |
| Portable applications / C6 | Supported signed image or qualifying SSD/USB storage, destination capacity, native owner and PDB, reachable probe | No cross-device app-continuity claim; do not relax signing policy |
| Prepare capability / C4 | Install-only durable identity, trust/content verification and restart/removal behavior in both directions | E05/E06 physical acceptance blocked for that cohort; combined lifecycle remains distinct |
| Redundant supervisor / E02, C4 | Actual supported redundant hardware if supervisor behavior is claimed | Keep that capability explicitly unqualified |
| Second platform / C8 | Eligible device, credentials, lifecycle API and two compatible images | E11 stays blocked; NX-OS virtual Node readiness is not lifecycle evidence |
| Native group scheduling / C8 | Available supported Kubernetes release, actual APIs/feature gates and matching CVK ownership lifecycle | E08 optional physical qualification remains pending; do not upgrade based on a planned version number |

The next executable coding increment is **C1 binding-order + real freeze-path
regressions**, alongside C0 read-only preservation/diagnosis. Resolve C0
before reusing `.103`; do not start another blanket three-device cycle first.
No E00–E13 package is closed by this review.

### Historical N1–N5 queue (superseded by C0–C9)

| Commit | Implemented | Qualification boundary |
| --- | --- | --- |
| `4711d3c7` | Separate 20-second collection and 5-second publication contexts; remote-port graph identity and two-input conflict diagnostic correction | Timeout fallback still needs end-to-end publisher testing. Graph helper has no runtime consumer; three-way conflict permutations remain untested. |
| `f9b76322` plus current follow-up | Publisher checks manager-recorded revision/Pod, physical hash and provenance, allocates the next sequence from persisted status, and patches with an optimistic status precondition | These are still client-side checks. Manager-owned acceptance, server-enforced ordering/freshness and real worker-token qualification remain. |
| `c5a2deeb` plus current follow-up | Native admission compares authenticated token Pod UID with manager proof and submitted Pod UID, requires a ready manager-recorded worker revision, requires positive, monotonic collection sequence/end-time provenance, and safely preserves omitted optional condition observations | Render checks establish contract consistency. The disposable API-server suite now proves genuine bound-token acceptance plus forged revision/replay/not-ready denial, old-token denial after a live replacement binding, and fail-closed denial while manager Pod proof is absent; peer-device and mixed-version qualification remain. |

The process-local counter defect is now repaired in the publisher: a sample
with no sequence reads the persisted high-water value from the live
`CiscoDevice`, advances it under an optimistic resource-version patch, and
retries bounded conflicts. A same-Pod restart after sequence 10,000 therefore
resumes at 10,001; a replacement worker Pod starts at one. Overflow is
rejected. `TestPublishNetworkObservationRestartSequenceRecovery` covers both
paths. This closes sequence allocation, but does not yet close manager-owned
acceptance, server-enforced ordering, concurrent-producer qualification, or
real bound-token admission.

The previously captured `/tmp/cvk-envtest.log` now contains successful provider
and controller package results (176.478s and 13.318s). This is historical local
suite evidence without per-test counts. The existing `startEnvtest` installs
CRDs, uses an administrative client, and does **not** install the Helm
ValidatingAdmissionPolicies or issue worker tokens. The prior statement that
the Kubernetes 1.35 admission suite passed must therefore be read as CRD/schema
coverage only; E01-C/E authorization remains unqualified. New tests below
exercise persisted observations separately from authorization.

Execute these reviewable increments in order; retain the full E00–E13 scope:

| Next | Implementation deliverable | Required tests and exit condition |
| --- | --- | --- |
| N1 / E01-B | **Publisher ordering hardened:** the publisher now uses a direct API client for its high-water read/write, rejects a collection that is not newer than the accepted collection, and deliberately discards a status-patch conflict rather than assigning an older collection a newer sequence. Manager-owned acceptance of exact Pod/revision/sequence/hash and original sample time is still pending. | Race-tested publisher tests cover restart, replacement, overflow and old-collection rejection; the Kubernetes 1.35 envtest covers API persistence. Remaining N1 work is concurrent-producer/lost-ack/manager-restart coverage plus manager acceptance and stale-evidence exclusion. |
| N2 / E01-C/E | Extend native admission to validate submitted provenance and ordering against the old object, and protect manager acceptance. The current policy binds the exact manager-recorded ready worker revision and prevents same-worker sequence/time replay; live replacement-token and missing-manager-proof cases now pass. It still needs peer-device and mixed-version token cases. Audit metadata and the entire status delta, including optional fields. Preserve exactly two functional ServiceAccounts and RO/RW profiles. Define chart/binary/schema rollout and rollback order with matching preflight digests. | Install rendered policies on a disposable API server; wait for policy readiness/type-check results. Exercise actual bound tokens for correct, peer-device, replaced and missing-Pod callers; direct status patches must not bypass publisher checks. Test app/unrelated callers, RO mutation denials, absent optional health fields, forged readiness/revision/sequence/time, metadata changes, manager writes and old/new chart/binary combinations. Zero unauthorized writes/RPCs. |
| N3 / E00/E01-A/B, E02-A | **Diagnostic bounding and collection safety advanced:** normalization errors are reduced to fixed reason classes and defensively capped at the CRD's 256-byte limit; status-bound fields and 64-item collections are enforced; IOS-XE preserves OSPF VRF/process context when its operational model provides it; delimiter-bearing identity fields are collision-safe; a non-cooperative source yields incomplete evidence without creating overlapping collectors; and IOS-XE rate fixtures preserve measured zero, absent leaves and conversion overflow. Oldest-source preservation, remote-port identity and retained-leaf regressions remain pending. | The 1.35 envtest proves malformed duplicate interface/CDP input is accepted as bounded incomplete evidence without changing manager fields. Race-tested unit coverage proves timeout-to-incomplete, bounded hung-source collection/recovery, overlength-field rejection and 64/65 collection limits. The IOS-XE fixture proves rate-presence semantics; physical rate accuracy and prior-sample expiry remain pending. |
| N4 / E00, E01-D/E, E02-B | **Clean candidate deployed for read-only qualification:** source `9e578131`, image digest recorded, Helm revision 112, native admission preflight passed, and all three physical target workers/observations were checked. | Evidence is saved in [`9e578131-observation-validation.md`](evidence/topology-2026-10-01/9e578131-observation-validation.md). Fresh direct device CLI/secure OS.Verify, independent service probes, all log planes and the E04 preparation/activation boundary remain required before disruption. |
| N5 / E03 and E04 | **Manager-side current-evidence revalidation added:** every reservation revalidates the enabled network gate with the live target and exact current network worker binding. Later candidates add administrator protection, evidence-bound expiring grants, claim-time enforcement and continuous recovery/soak checks; overlapping budgets remain pending. Qualify the device preparation/activation boundary on the isolated cohort. | Complete overlapping-group and pacing tests. E04-A–D prove transfer, durable staging identity, restart and explicit activation in both directions before enabling E05/E06. |

N1–N3 can proceed locally without physical traffic hardware. N4 gates the next
disruptive run; E04 discovery and E11 capability inventory may proceed read-only
earlier. E05/E06 fixtures can be developed while hardware qualification is
pending, but their physical acceptance requires E04. Then execute orders 4–8
below for placement/TAS, graph/distribution, ownership/scale/second platform,
and E13. No package is closed by this review.

## 1. Scope, ordering and completion accounting

The main extension requires E00–E07 and E13's core acceptance: reliable
network evidence and policy, durable preparation, separately authorized
activation, and supported workload relocation through upgrade and downgrade.
The wider roadmap additionally requires E08–E12 and E13's applicable wider
acceptance. Observation publication, ordinary combined upgrades, an ephemeral
image cache and synthetic TAS tests are baselines, not substitutes for these
deliverables.

| Package | Roadmap | Work to execute | Prerequisites | Package status and remaining gate |
| --- | --- | --- | --- | --- |
| E00 | T0 | Lab ownership, capability inventory and evidence baseline | None | In progress: clean candidates through `11ae6704` deployed; C0 must preserve/diagnose `.103` uncertainty; C1 must finish binding-order and freeze regressions. Complete source/image, CLI/Verify and service evidence still required. |
| E01 | T1 | Observation correctness, provenance and meaningful regression tests | E00 inventory | In progress: restart-safe ordering, bounded publication, interval checks and native bound-token suite advanced through `11ae6704`; C2 still needs manager acceptance, complete identity/migration matrix, original-source age and physical CLI qualification. |
| E02 | T1–T2 | Measured traffic/headroom and supervisor/stack health | E01 | In progress: directional rate presence/validity and conservative headroom are implemented; controlled load, sampling provenance and supervisor evidence remain. |
| E03 | T2 | Administrator network policy, overlapping risk groups, expiring grants | E01–E02 | In progress: evidence-bound expiring grants, monotonic renewal, uncached pre-claim enforcement, bounded administrator critical-service/singleton-path prohibitions, exact overlapping membership with cross-campaign ledger accounting, continuous accepted-evidence recovery/soak enforcement, worker byte pacing and physical `.103` pacing in both image directions are implemented; independent loaded/headroom measurement and service-path acceptance remain. |
| E04 | T3 | Physical qualification of the preparation/activation boundary | E00; read-only investigation may start immediately | Investigated but unqualified: `3212f777` reconciled the earlier NoReboot timeout, but C4 must test a true Install-only hold; E04-A–D remain open. |
| E05 | T3 | Durable staged receipts and staged ownership | E04 positive capability evidence | Not started: receipt/protocol/retained ownership absent; physical qualification depends on E04. |
| E06 | T4 | Separate activation approval, windows and phase reservations | E03, E05 | Not started: independent authorization and phase accounting absent; physical qualification depends on E04–E05. |
| E07 | T5 | Physical drain qualification and hard placement | E00, E03; full lifecycle tests need E06 | In progress: fresh combined rollouts used `Drain`, with final Running workloads; per-target eviction/replacement/service timelines, hard placement and independent-activation integration remain. |
| E08 | T6 | Group recognition and actual CVK native TAS lifecycle | E00; group drain needs E07; physical owner transfer needs E12-B–D | In progress: synthetic co-location/conflict evidence only; E08-A guard, remaining scheduler scenarios and physical native owner lifecycle remain. |
| E09 | T7 | Transfer measurement and conditional durable prefetch/cache | Measurement: E00; cache: E06 plus measured need | Not started for roadmap measurement/decision: ephemeral cache exists; E09-A and explicit cache selection decision remain mandatory. |
| E10 | T8 | Observed graph diagnostics and declared-policy drift | E01, E03 | In progress: bounded helper and read-only kubectl consumer use only manager-accepted evidence; missing/stale evidence, duplicate physical identities and three-way conflicts fail closed. Candidate `e5660db4` passed exact physical remote-port deployment. Candidate `34050731` passed physical peer mapping and nine-link declaration comparison, protected ConfigMap provenance, unchanged rollout-policy hashing, replacement-Pod fail-closed behavior and reversible declaration mismatch/restoration on all three C9Ks. A controlled isolated physical link change plus remaining protocol/VRF/LAG and live authorization/restart fixtures remain. |
| E11 | T9 | Second-platform lifecycle and generic API decision | E05–E06; capability discovery may start earlier | Blocked — prerequisite for positive qualification: no qualified second-platform image pair/service evidence; discovery and fixtures can proceed. |
| E12 | T10 | Scale envelope and controlled ownership transfer | Stable E03–E06 contracts | In progress: legacy handoff/convergence exists; measured scale envelope and full staged/uncertain-operation ownership transfer remain. |
| E13 | All | Integrated acceptance, migration, documentation and evidence closure | Completed dependencies for claimed scope | Not started for final acceptance: no single candidate has passed the applicable F01–F13 matrix. |

**Completion accounting:** none of the 14 work packages has all of its exit
gates closed. This does not discard the implemented baseline or passing
subtests. Substantial implementation remains in E03–E06, E07–E08 and E10–E12;
the remaining work is not solely lab testing. A percentage based on commit
counts or passing unit tests would obscure those dependencies.

### Historical baseline review (before the disruptive run)

At approximately 19:21 UTC on 30 September, read-only Kubernetes/Helm queries
on Ubuntu16 confirmed the following. These are a point-in-time baseline, not
new lifecycle qualification:

| Check | Result and qualification boundary |
| --- | --- |
| Source and remote | Local HEAD and remote branch both resolve to `665a7954`; `gh pr list --head pr/johalley/tas-extentions --state all` and branch-specific `gh run list` returned no PRs/runs. No candidate CI result is established. |
| Cluster/deployment | Ubuntu16 reports Kubernetes `v1.35.8+k3s1`; Helm release `cisco-vk` is revision 98. Manager plus all six C9K app/network Deployments report one ready replica and image tag `cvk-tas-extentions:0884666e`. Capture resolved image digests in E00; a commit-shaped tag alone is not immutable evidence. |
| Three intended C9Ks | `.100`, `.101`, `.103` report Ready and OS image `17.18.2`. Published network samples are complete, have non-zero sequences and match their respective manager-recorded network Pod UIDs. This verifies publication/binding, not replay resistance or forwarding health. |
| gNOI condition | `GNOIConfigurationReady=True` explicitly reports that Secret material and exact worker readiness are valid but **device connectivity has not been checked**. Secure device `OS.Verify` is still required by E00-B/F13. |
| Inventory exceptions | The duplicate `cat9k-node` for `.101` remains NotReady. `nexus9300v-live` uses the older `pr194-ee3b18f` worker, has no network sample/worker proof, and has inactive gNOI Secret validation. Neither is a newly qualified target. |
| Applications | Two historical drain Deployments report ready replicas using `flash:/nginx.tar`. No independent service probe or cross-device package portability was checked; do not count these as E07 evidence. |

Do not erase the duplicate Node, retained historical leaves or their evidence
to obtain a clean snapshot. Resolve exact ownership and retained operation
state first. Repeated historical-leaf status-write denials seen during the
preceding deployment require the regression described below; a quiet manager
log does not establish healthy network-worker reconciliation.

### Review findings that change the next work

This earlier findings table is retained for traceability; the C0–C9 queue
and current checkpoint above supersede its status and ordering. Historical
lab findings remain qualified by their recorded image.

| Priority/package | Current code evidence | Required change and proof |
| --- | --- | --- |
| First: E01 producer and freshness acceptance | Publisher reads/writes the live API, rejects an older collection and never retries it with a later sequence. The rollout adapter rejects an empty expected worker identity. No independent manager acceptance or oldest-source time exists. | N1: manager-owned acceptance and strict required identity at server admission. E01-B/C must reject replay without delaying legitimate restart recovery. |
| First: E01 authenticated write ownership | Native policy now binds authenticated Pod UID to manager proof and sample, requires the exact manager-recorded ready worker revision, and rejects non-monotonic same-worker sequence/end-time provenance. The current bound-token cases, including replacement fencing, are server-qualified; peer-device and mixed-version paths remain. | N2: complete the real bound-token matrix, protected acceptance and full-field/metadata delta audit. Preserve the two functional accounts and test chart/binary migration. |
| First: E03 claim-time and recovery checks | `9c48a98c` adds an exact evidence-bound expiring manager grant, monotonic renewal and uncached worker revalidation before every new mutation claim. `617b1cfc` adds bounded administrator critical-service/singleton-path prohibitions. `ae4f3a7b` enforces a post-operation accepted sample and the complete current network policy throughout recovery/soak. `7417b09b` freezes exact overlapping-group membership and atomically counts unhealthy non-target peers plus all campaigns. The current candidate derives the strictest conservative per-transfer share from overlapping aggregate group ceilings and paces both source materialization and gNOI upload, failing closed for an incapable resolver. Already accepted work remains recoverable. | Physically measure pacing and headroom. E03-F must still cover plan-to-approval delay, install-to-activation delay, API/cache lag and post-operation network failure; assert zero new forbidden RPCs without abandoning accepted work. Do not advertise code-level accounting or soak checks as qualified forwarding continuity. |
| First: E01 read-only mode and collection bounds | RO publisher/status RBAC and separate 20-second collection/5-second write contexts are implemented. Normalization errors include input strings without bounding the combined reason to 256 characters. | N3: timeout-to-incomplete publisher regression, schema-valid error normalization and prior-sample expiry. N2/N4: integrated RO startup, status authorization and denial of every device mutation path. |
| First: E00/E13 retained-leaf reconciliation | Uncommitted `bindingDenied` handling skips a status write on failed binding; one unit test checks the replacement-Pod case. This is separate from new-object binding delays still seen by DeviceOperation and leaf reconcilers. | Test settled and unresolved predecessors with a status-write spy and zero RPC assertions, including missing binding/API-read errors and manager recovery. For new objects, wait boundedly for the exact manager binding before status/transport work; preserve real wrong-Pod denial. Require real-API and lab regression without deleting retained history. |
| First: E01 adjacency identity and compatibility | New identities include source, peer and local interface; delimiter-bearing fields use length-prefixed hashed identities and CRD-bound fields are length-checked. IOS-XE now carries OSPF VRF/process/area context when exposed by the operational YANG model; `RequiredNeighbors` still selects only a peer string and blocks when it resolves to multiple adjacencies. | Add remote-interface context and specify an unambiguous selector/migration for legitimate multi-adjacency peers. Test old/new workers and the map-to-atomic CRD transition. Do not describe the list topology change as purely additive. |
| First: E02 absent rates | `InterfaceStats` now carries direction-specific presence/validity; IOS-XE marks missing leaves and Kbps conversion overflow invalid, while measured zero remains valid. `interfaceHeadroom` returns Unknown unless both directions are present and valid. | Add driver/API fixtures for every supported YANG representation and independently measure idle/load behavior (E02-A/B). Keep supervisor/stack health separate until it has a qualified source. |
| Next: E03–E06 missing runtime contracts | `AdminPolicyConfig` now has protected disruption rules, overlapping groups and optional aggregate-rate ceilings; the campaign has a network gate and plan approval. The lifecycle backend exposes inventory and registration, but no durable separately approved staged-receipt workflow. | Complete E03 physical pacing/path qualification; qualify E04 before finalizing E05/E06 platform semantics. Existing `NoReboot`, claims and approval hashes cannot satisfy these new contracts by renaming states. |
| Next: E07/E08 placement and group eligibility | `validateDrainPodSpec` explicitly rejects node selectors, required affinity and hard topology spread. The native TAS script uses fixture Nodes and installs no CVK runtime. | Add only qualified placement eligibility and fail-closed raw group recognition before expanding drain; pass E07-A–D and E08-A–D with portable applications and independent probes. |
| Next: E10 graph correctness and integration | Structured hash, local/remote-port matching, duplicate identity handling and order-independent three-way conflicting-peer canonicalization are implemented. `kubectl ciscovk topology graph` reads only manager-owned `acceptedNetwork`, uses authenticated collection start, preserves unbound objects, and consumes strict mappings/declarations from an admission-protected ConfigMap with Kubernetes/content provenance. Candidate `34050731` physically passed the mapped nine-link model and declaration-only drift/restoration. | Finish protocol/VRF/LAG and live authorization/restart fixtures, then perform an explicitly isolated physical link change/restore. Graph completeness must never imply path health. |
| Evidence: E10 test claims | Tests cover reordered two- and three-way conflicts, matching declarations, canonical mapping hashes, ambiguous/unavailable/external mappings, strict JSON, stale/missing time, known-peer asymmetry, state-sensitive hashing, accepted-vs-raw provenance, unbound devices, kubectl transport and bounds. Physical evidence proves remote ports, protected graph-policy provenance, replacement-Pod fail-closed behavior, exact mappings/declarations and reversible declared drift with an unchanged rollout-policy hash. | Add the controlled physical link change/restore and remaining real-API authorization/restart cases before claiming E10-A–D coverage. |

### Next execution sequence and required tests

Each row delivers code, examples and its evidence before advancing to the
dependent physical scenario. Independent read-only discovery and measurements
can proceed alongside implementation.

| Order | Concrete next deliverable | Testing required before closing the increment |
| --- | --- | --- |
| 1 | Reconcile E00 evidence, repair the harness, binding retries and reproducible generation, then finish E01/E02 correctness: RO publication, exact producer acceptance, identity/VRF handling, rate validity. | E00-A–H; E01-A–E; E02-A first, followed by isolated E02-B/C. Compare device terminal output with published samples; reproduce old failures and assert exact blockers/zero dispatched mutations. |
| 2 | Qualify E04 independently, while adding E03 administrator policy, risk groups and expiring grants. | E04-A–D in both image directions; E03-A/B CAS and real-API negative tests before E03-C–F physical path/load tests. Record incapable cohorts as blocked. |
| 3 | Implement E05 staged receipts/ownership and E06 independent activation approval, windows and atomic phase reservations. | E05-A/B and E06-A/B/D fault/admission suites; then E05-C and E06-C/E on E04-qualified hardware. Hold across worker/manager restarts and a closed activation window; prove no activation before the separate grant. |
| 4 | Add E08-A group guard, then E07 hard-placement support and automatic service-preserving drain. | E08-A before broader drain eligibility; E07-A–D upgrade and downgrade with portable signed/supported artifacts, PDB, spare capacity and independent service probes. Manual workload scale-down does not pass this increment. |
| 5 | Complete E10 graph corrections and manager/CLI integration; perform E09 transfer measurements and select or reject durable cache explicitly. | E10-A–D including isolated physical link change/restoration; E09-A warm/cold measurement of both segments. If cache is selected, implement and pass E09-B–D. |
| 6 | Complete E12 ownership transfer before moving physical devices to the optional TAS cluster; finish E08 physical group lifecycle. | E12-B/C and E12-D when moving across clusters; complete remaining E08-B scenarios and E08-C/D. Remove old authority before destination enrollment. |
| 7 | Qualify E11 second platform and E12 scale once shared contracts are stable. | E11-A capability discovery can start earlier; E11-B–D require qualified hardware/images. E12-A benchmarks at 1/10/50/100 targets and boundaries need predefined latency/resource budgets. |
| 8 | Run E13 migration/security and the final integrated physical matrix on one pinned candidate; archive evidence and align release claims. | Core F01–F08/F13 plus applicable F09–F12; exact candidate CI, baseline and optional-version lanes, recovery and final ownership/maintenance checks. |

**Next implementation checkpoint:** complete the following repairs within
order 1, then qualify E04 alongside E03. The last run repeated the combined
path while these prerequisites remained open; it supplies baseline regression
evidence but does not advance independent staging/activation acceptance.

| Sequence / owner | Concrete work | Required evidence before advancing |
| --- | --- | --- |
| 1 / E00, E13 | Reconcile all six rollout/leaf UIDs and both-direction device results; retain the stopped harness runs and their resumes. Record source patch checksum, harness revision, Helm values/revision, CRD/admission digests and resolved image IDs. Archive sanitized artifacts with checksums. | E00-F below; distinguish device success from an aborted test or incomplete evidence. Commit reviewed changes before building the next candidate; version output alone cannot attest to a dirty build. |
| 2 / E00, E13 | Repair `scripts/iosxe-gnoi-lab-cycle.py`: use explicit source URL/Secret inputs without rewriting HTTP arguments to hard-coded SFTP; verify exact frozen target/CR/Node identities and source before approval; capture manager and both worker planes. Resolve rollout targets through frozen-plan identities for maintenance observation. | Fixture tests for wrong target/digest/source, single-direction resume, partial failure and missing evidence. Emit success only for directions actually executed. `canarySoakSeconds=0` selects today's default; use explicit tested durations and include them in the deadline. |
| 3 / E00, E07, E13 | Replace skipped app-inventory equality with fleet reconciliation: baseline owner/Pod/application mapping, actual eviction, device-clean proof, replacement readiness, endpoint probe and final owner counts. Preserve the original baseline across resumed directions. Capture Node guard, claim, RPC and device timestamps. | E00-C/F and E07-B timeline assertions; a vacuous RUNNING check on an empty target inventory cannot prove application survival. Missing pre-dispatch fencing evidence is an explicit unqualified gate. |
| 4 / E00, E01 | Reproduce new-object manager-binding delay separately from retained predecessor fencing. Fix legitimate initialization waiting without weakening native admission, broad forbidden-error suppression or deleting history. | E00-E/H and E01-C/E: real bound-token API tests, bounded retries, zero out-of-authority writes/RPCs, and successful progress after binding arrives. |
| 5 / E00, E01, E02, E10 | Restore reproducible generation/envtest tooling; complete authenticated RO observation publication, timeout fallback, durable sample acceptance and claim-time design; finish graph regression correctness. | E00-G, E01-A–E, E02-A and focused E10-A. Run real API tests before physical RO checks; preserve existing two functional accounts and baseline compatibility. |

Then execute orders 2–8 above. E04 experiments require exclusive ownership,
console/recovery access, independent probes and the safety regressions before
any install/stage side effect. Define the exact hold point and recovery path;
an inconclusive staged identity prevents proceeding to separate activation.
Fake-device implementation of shared E05/E06 contracts can proceed while
hardware qualification is blocked, but enabling a cohort still requires E04.

Inputs still needed for physical closure are a portable supported application
and independent service probe, isolated traffic sources/test links, suitable
redundant supervisor hardware for any failover claim, a qualified second-platform
image pair, and durable evidence storage. These are prerequisites to resolve
in E00/E11, not reasons to postpone the independent code and fixture work.
The latest run referenced `pr194-iosxe-image-source` and the SFTP image path;
revalidate Secret UID, source authorization and host trust before reuse.
Historical cleanup notes do not establish the current credential state.

### Prerequisite decisions that must not be left until the final run

The implementing maintainer records these decisions in the versioned E00
qualification record, with the lab/platform owner supplying hardware inputs.
Each record needs a responsible owner, evidence link and next action; an
unavailable input stays `Blocked — prerequisite`, not an implied code task.

| Resolve before | Required decision/output | If unavailable or unsupported |
| --- | --- | --- |
| E00 closure | Durable sanitized artifact destination, exclusive device ownership, image checksums/trust, baseline manifests and recovery access | Retain read-only/fixture work; no disruptive campaign until authority and recovery are established. |
| E02-B / E03-C–F | Diagram actual forwarding/test paths, probe host/service, isolated traffic source, acceptable outage and rate-measurement tolerance | Label accounting simulations accurately; physical path/capacity/service acceptance remains open. Never load the management path. |
| E04 → E05/E06 | Per-release result: transfer-only, stable device staging, activation identity, disruption class and supported recovery | Keep combined lifecycle for incapable cohorts. Obtain a capable cohort; do not replace the required staged-receipt test with prefetch or `NoReboot`. Implement shared contracts against fixtures meanwhile. |
| E07-B / E08-C | Signed/supported portable app, independent service probe, spare eligible capacity and qualified native workload owner | Device-only upgrade tests can continue when safe; positive drain/TAS lifecycle remains open. Do not weaken package, PDB or placement requirements. |
| E02 supervisor claims / E11-C | Appropriate redundant hardware; second-platform gNOI service, compatible images and exclusive ownership | Scope supervisor claims to tested cohorts; E11 remains blocked until a second driver passes. A virtual NX-OS Node or negative probe is not positive qualification. |
| E09 / E12-A | Written cache decision after measurement; predeclared scale latency, resource and storage budgets | Cache may be `Not selected — conditional` with evidence; scale remains unqualified without measured budgets/results. |

The full roadmap cannot be guaranteed on the present three C9Ks
alone. Do not silently redefine completion if a capability or lab prerequisite
fails; report the achieved core subset and the named wider blocker. Calendar
or release pressure does not close an acceptance gate.

The execution sequence above supersedes a purely numeric E00–E13 order.
E04 discovery, E09 measurement and E11 capability discovery can proceed
alongside earlier coding; E10 pure-helper repairs can start immediately,
while policy integration depends on E01/E03. Each package should be an
independently reviewable change or short dependent stack. E04 may deliver
evidence and fixtures without runtime changes. Do not enable E05/E06 on a
cohort until E04 passes.

Split E12 when the optional TAS cluster needs ownership of lab devices:
execute its handoff implementation and E12-B–D after E06 and before E08-C/D.
Its scale benchmark can remain later. E08-A's fail-closed group recognition
must accompany E07's broader drain eligibility, even if positive native TAS
qualification is deferred; otherwise a grouped Pod could enter ordinary drain.

Use these status values for every package and test: `Not started`,
`In progress`, `Implemented / unqualified`, `Passed`, `Failed`, or
`Blocked — prerequisite`. Record commit, deployed image digest, test ID,
evidence path and residual limitation alongside a status. Missing hardware
or an unsupported device response is not `Passed` for a positive capability.
Use `Not selected — conditional` only for an explicitly conditional feature
with the decision evidence described below, never for a required test.

### Execution evidence for revision `938a488f`

These results are evidence for this revision only; they do not close packages
whose remaining gates are listed above.

| Package/test | Result | Evidence and limitation |
| --- | --- | --- |
| E01/E02 unit and race coverage | Passed | `go test -race -count=1 ./...`; duplicate/over-limit records, exact freshness reasons and headroom cases are covered. |
| E01/E02 physical observation | Historical partial result | Earlier publication and the `EvidenceIncomplete` negative below belong to `938a488f`. Latest Pod-bound publication belongs to `0884666e` and is recorded separately below; do not backdate those fields to this revision. |
| E01 network gate negative | Passed | A temporary rollout targeting `.100` stopped at `PlanningFailed / EvidenceIncomplete`; no software-upgrade leaf or device mutation was created. |
| E08-B native TAS subset | Passed for co-location/conflict assertions | Fresh kind v0.33.0 / `kindest/node:v1.37.0`, exact checked-in feature-gate config; co-location and unschedulable conflict assertions passed; cluster deleted afterward. Remaining E08-B fault/restart/maintenance cases and physical CVK lifecycle remain unqualified. |
| Repository gates | Passed | Focused tests, full race suite, Helm lint, topology render test, strict MkDocs build and `git diff --check`. The Makefile generator target remains incompatible with its pinned controller-tools package; CRD parity was checked with controller-gen v0.19.0 and the reviewed validation was applied to both CRD copies. |
| Physical deployment health | Passed after managed rollout convergence | Manager, three IOS-XE app workers and three IOS-XE network workers converged to the immutable image; all three managed IOS-XE Nodes were Ready. Protected admission rejected direct worker mutation as expected; the upgrade was completed through Helm. |
| E04/E07 physical combined upgrade on `cat9k-lab-103` | Passed as a combined Reload regression; E04/E07 gates remain open | `cvk-roadmap-938a-upgrade-103` / leaf `...-8e943463` transferred the pinned `17.18.03` image, submitted gNOI `OS.Activate`, survived the IOS-XE reload, and reached `Succeeded` with running version `17.18.03.0.5496.1776157760`. The Node returned `Ready=True`, workers returned healthy, and network evidence returned complete. This proves the current combined path only; it does not prove a separately durable staged receipt, activation approval, critical-service drain, service probe, or hard placement contract. |
| E04/E07 physical combined downgrade on `cat9k-lab-103` | Passed as a combined Reload regression; E04/E07 gates remain open | `cvk-roadmap-938a-downgrade-103` / leaf `...-53aa8191` transferred the pinned `17.18.02` image and reached `Succeeded` with running version `17.18.02.0.4112.1766116039`. The Node returned `Ready=True`, network evidence was complete, and both worker Deployments remained healthy. The first apply was rejected while the post-reload topology initialization guard was still settling; retry after `maintenanceSession=Settled` was clean and no duplicate mutation was issued. |

### Follow-up integration evidence for the handoff-race correction

The following bounded fixes were added after the `938a488f` evidence capture:

| Change/test | Result | Evidence and limitation |
| --- | --- | --- |
| Rollout planning during topology handoff | Passed | `f079f5c5` converts the transient `topology initialization guard` planning error into a five-second deferred reconcile. The frozen plan is still built before any leaf/device mutation; non-handoff planning errors remain terminal. Unit coverage is in `internal/controller/iosxesoftwarerollout_planning_test.go`. |
| Concurrent managed status writers | Passed | Managed Device status, topology status, pre-rollout fences and retained Lease binding repairs now re-read and retry on API conflicts while preserving fields owned by other writers. This removes the physical `Operation cannot be fulfilled ... object has been modified` hot loop observed during worker rollout. |
| Worker startup admission ordering | Passed | App workers keep the native admission check fail-closed but retry for the bounded Pod-binding window, preventing a legitimate freshly-created worker from CrashLooping before the manager stamps its Pod name/UID. Genuine policy/RBAC denial still fails startup after the timeout. |
| Disposable Kubernetes integration | Passed | `CVK_TOPOLOGY_TEST_ALLOW_DISPOSABLE_CONTEXT=true bash charts/cisco-virtual-kubelet/tests/topology-kind-test.sh` and `bash charts/cisco-virtual-kubelet/tests/managed-shared-worker-kind-test.sh --cluster-name cvk-roadmap-shared-it` both passed on `kindest/node:v1.35.0`; both disposable clusters were deleted by their test cleanup. |
| Physical deployment of the correction | Historical convergence; latest deployment recorded separately | The follow-up corrections precede the current `0884666e` publication check. Keep each run tied to its actual revision; latest Ready/configuration conditions do not prove historical or current secure gNOI device connectivity. |

Historical combined 17.18.02↔17.18.03 upgrade/downgrade evidence remains
valid for the previously tested revisions. The single-device positive
regression above belongs to `938a488f`, not the current runtime; neither closes E04–E06 or
the new E07 service-probe gate for `938a488f`. No separate staged receipt,
activation approval, durable cache, physical TAS lifecycle, second-driver
qualification, scale envelope or ownership-transfer evidence exists yet.

Conditional work must have a written, evidence-backed decision:

- E09 measurements are mandatory. Durable per-worker PVC caching is required
  only if measurements demonstrate benefit and suitable storage is supplied;
  otherwise record `Not selected — conditional`, with results and rationale.
  Shared cache serving needs a separate design and is not automatically added.
- Extra workload types and hierarchical TAS require their explicit platform
  and owner contracts. Record supported and unsupported types individually;
  do not remove exclusions to make the checklist appear complete.
- E11 must qualify a second driver before claiming second-platform support.
  An unsupported NX-OS probe leaves this objective blocked; it does not justify
  an empty generic API. Public API extraction requires the subsequent ADR.

### Follow-up topology evidence and graph-safety changes

The current revision also tightens the observation contract without changing
the existing rollout opt-in defaults:

| Change/test | Result | Evidence and limitation |
| --- | --- | --- |
| Source-qualified neighbor identity | Implemented / unqualified for full E01 | CDP and OSPF retain source, local interface and OSPF area. Equal peer names are no longer merged solely by string equality; ambiguous required peers fail closed. Legacy observations fall back to peer ID. VRF/process context and explicit multi-adjacency selection remain pending. |
| Collection provenance | Implemented / partially enforced | Start/end timestamps, process-local sequence and network-worker Pod UID are published. The planning gate compares expected revision/Pod when populated and checks sequence/interval presence. Empty-identity migration, authenticated write ownership, persistent ordering and claim-time use remain pending. |
| Observed graph diagnostics | In progress | `internal/topology/graph.go` is consumed read-only by `kubectl ciscovk topology graph`. Tests cover declarations, canonical/ambiguous/unavailable/external mappings, strict protected-ConfigMap input, reordered conflicts, hashing, stale/missing sources, accepted provenance, asymmetry, unbound devices, kubectl transport and bounds. Candidate `34050731` passed exact physical mapped/declaration output plus declaration-only fault/restoration. Controlled isolated physical-link drift/restore and remaining live boundary cases remain open. |
| Compatibility | In progress | Added scalar fields are optional and the network gate remains opt-in. The neighbor list changes from map-by-ID to atomic, and required-peer ambiguity now blocks. Persisted objects, server-side-apply ownership and mixed-version publication/rollback need explicit E01-C/E13 tests. |
| Physical observation publication | Passed for a read-only check of the latest IOS-XE deployment | Ubuntu16 Helm revision 98 ran `cvk-tas-extentions:0884666e`; all three app workers and three network workers converged, all three managed C9K Nodes/CiscoDevices were Ready, and all three published complete samples with collection start/end times, producer revisions, non-zero sequences, and Pod-UID equality with the manager's worker proof. Final logs reported no manager errors in the final three-minute window. This is publication/deployment evidence, not qualification of the network RO profile, full staging/activation, service reachability, controlled-load, or a new software upgrade/downgrade run. |
| Repository gates at the prior implementation turn | Passed as recorded | Full race suite, strict MkDocs, Helm lint, topology render contract and `git diff --check` passed locally. The stale managed-device variable count assertion was corrected from 7 to 9; no chart template behavior changed. These local results do not establish passing CI or complete real-API coverage at this revision. |

### Evidence reconciliation at `665a7954` (runtime `0884666e`)

The focused observation/graph/gate tests were rerun successfully during this
review using `go test -count=1 ./internal/topology ./internal/topologyhealth
./internal/provider -run 'Test(BuildGraph|BuildNetworkObservation|Evaluate)'`.
Their pass confirms the current assertions; it does not fill the missing
scenarios listed above. `gh run list --branch pr/johalley/tas-extentions`
returned no runs at review time, so candidate CI remains unverified.

Historical sources inspected include
`/tmp/cvk-tas-extentions-physical-evidence-20260930.md` and
`/tmp/cvk-roadmap-qualification-20260930/{physical-validation,final-state}.txt`.
They require a sanitized, checksummed durable index before release closure:

- The retained `cvk-lab-upgrade-20260930-r7` object has one target (`.101`)
  and `succeeded=1`. The report describes separate earlier runs for `.100`
  and `.103`; recover their exact campaign/leaf evidence before presenting
  this as one three-target upgrade run.
- `cvk-lab-downgrade-20260930-r11` records three successes, but used
  `BlockIfRunning` after manual workload scale-down/cleanup. This supports
  combined device downgrade, not E07's automatic drain/service criterion.
- The report states that admission bindings were restored after cleanup.
  Record when protection was changed and which operations occurred in that
  interval; replay relevant E13 negatives with admission continuously enforced.
- `.103` has later combined 17.18.02↔17.18.03 regression results at
  `938a488f`. Revision 96 records provenance publication for `6bd7d471`;
  revision 98 records Pod-bound publication for `0884666e`. Neither
  publication run is a new image upgrade/downgrade or independent
  preparation/activation qualification. These were read-only checks of
  configured workers, not qualification of the network read-only profile.
- Earlier `.100` incompleteness was tied to duplicate CDP peer names at
  `938a488f`. The later interface-qualified publisher reports complete
  `.100` samples. Preserve both revision-specific results; requiring an
  ambiguous peer name still blocks by design until selection is refined.

Forced drain, automatic authority from discovery, graph-cost scheduling,
mandatory experimental TAS, zero-downtime promises and active unfenced
multi-cluster control remain excluded. They are not completion tasks.

## 2. Common implementation and test contract

Preserve native Kubernetes scheduling, manager-owned policy, per-device
execution and exactly two functional worker ServiceAccounts per managed
namespace, each with disabled/read-only/read-write modes. The manager opens
no device sessions. New mutation capabilities require network write authority;
read-only network observations must also work with the network read-only mode.

Every package that changes APIs, policy or coordination must deliver:

1. Bounded typed fields, ownership rules and explicit defaults; unchanged
   behavior and canonical hashes when optional features are omitted.
2. A versioned protocol where older managers/workers cannot safely interpret
   the new behavior. Mixed-version workers receive no new-protocol grants.
3. Generated DeepCopy/CRD/chart parity, admission/CEL and RBAC changes,
   compatibility fixtures, positive and negative real-API tests. Update
   admission contract digests and their Helm/runtime tests together.
4. Focused unit tests, fake-device fault injection, race tests for coordination,
   Kubernetes 1.35 baseline tests, and physical tests named in the package.
   Default `go test` does not run the build-tagged envtest suite.
5. Operator examples after the schema exists, readable blocker/status/Event
   output, remediation, cancellation, feature-disable cleanup, upgrade order
   and downgrade restrictions. No credentials in hashes, logs or recordings.

Use existing locations: `api/v1alpha1`, `api/ops/v1alpha1`,
`internal/topologyhealth`, `internal/topologyrollout`, `internal/controller`,
`internal/provider/softwareupgrade`, `internal/provider/maintenance`,
`internal/workloaddrain`, `internal/softwarelifecycle`, platform drivers,
`charts/cisco-virtual-kubelet`, and `tools/kubectl-ciscovk`. Add a resource or
package only when persistence or ownership requires a separate boundary.

### Verification commands

Run the relevant focused commands after each implementation package, then
the full gates on its final revision. These are existing repository commands:

```bash
# Focused network/policy/drain logic; add package-specific test selections.
go test -count=1 ./internal/topologyhealth ./internal/topologyrollout ./internal/workloaddrain
go test -count=1 ./internal/controller ./internal/provider ./internal/provider/softwareupgrade ./internal/provider/maintenance

# After API changes; review generated changes, then rerun for idempotence.
make deepcopy-gen manifests
git diff --check
helm lint charts/cisco-virtual-kubelet
bash charts/cisco-virtual-kubelet/tests/topology-render-test.sh

# Real API-server CRD/schema tests; rendered policy/token tests are a separate lane.
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20260305142021-f9589b9f2b9d
# Ensure the go install binary directory is in PATH, then run:
make test-envtest

# Final revision and coordination regression gate.
go test -race -count=1 ./...

# Documentation validation with the repository's locked dependencies.
make mkdocs-build
```

Also run the pinned vulnerability scan and relevant smoke/security jobs in
`.github/workflows/smoke.yml`. Add new tests to those jobs; a local test that CI
never invokes is insufficient recurring coverage. Save commands, exit codes,
test counts and skips. Assert exact rejection reasons and zero forbidden RPCs,
not just an error or terminal phase.

Publish independently reviewable increments through a PR so the candidate has
actual CI evidence. Verify the final HEAD and each required check rather than
inferring success from a branch push or an empty run list. Add regression
coverage for the new acceptance, RO wiring, retained history and claim-time
boundaries to the appropriate baseline/API lanes; keep optional TAS tests in
their separate lane. This planning review does not create a PR or dispatch CI.

Before the next API change, resolve the recorded Makefile/controller-tools
generation incompatibility: pin a compatible generator, retain its actual
version metadata, regenerate both CRD copies and DeepCopy code, and rerun for
idempotence in CI. The prior v0.19.0 parity comparison is interim evidence;
it does not close reproducible generation or real-server schema migration.
Include the `workerPodUID` field in generator parity and a real-server
create/update/read round trip: a field present in handwritten CRD copies is
not proof that regeneration retains it or the API server stores it.
Missing `setup-envtest` is a remediable tooling task, not a hardware blocker:
install the repository pin, provision its 1.35.0 assets, run the build-tagged
suite and record the exit code and actual executed tests. If download or
execution fails, retain the precise failure and resolve it before acceptance.

`topology-kind-test.sh`, `managed-shared-worker-kind-test.sh` and
`native-tas-kind-test.sh` are disposable-cluster tests. Use dedicated
kubeconfig files and explicit contexts. Do not run their cleanup/finalizer
operations on the physical lab. For native TAS use the version, node-image
digest, kind version, feature gates and matching kubectl from the repository's
separate CI lane; verify served schemas before executing examples.

## 3. E00 — establish a reproducible physical baseline

### Required updates

1. Create a versioned qualification record and a reusable, read-only capture
   procedure. Inventory `198.51.100.100` (`cat9k-live`), `.101`
   (`cat9k-lab-101`) and `.103` (`cat9k-lab-103`), their serials, Node/CR UIDs,
   IOS-XE releases, stack/supervisor layout, storage and app packaging support.
2. Reconfirm Ubuntu host, kube-context, active CVK owner, CI exclusions,
   credentials/trust references, manager/worker image digests and chart/CRD
   revisions. Investigate the historical duplicate `cat9k-node` object before
   deciding whether it can be retired; do not adopt or delete it by name alone.
3. Locate both qualified images and verify checksums at the source. Historical
   candidates are 17.18.02 and 17.18.03 in Ubuntu16's
   `/home/cisco/cvk-gnoi-images/`; existence and compatibility require fresh
   verification. Capture manifests and source trust configuration without keys.
4. Build a per-device/release capability matrix for observation, transfer,
   durable stage identity, activation, rollback, full app inventory and drain.
   A configured feature flag must not count as a successful probe.
5. Prepare portable application artifacts and service probes for eligible
   destinations. Verify storage and signing requirements on every target;
   unsigned packages on C9Ks without supported SSD/USB-flash storage remain
   expected negative cases. A `flash:` path is not proof of portability.
6. Record actual links, routing/VRF context, service endpoints and management
   access before designing redundant-path/congestion tests. Define isolated
   test ports and traffic sources; never overload a shared management uplink.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E00-A | Capture Node/CR identities, healthy worker Pods, maintenance/ledger/Lease state and device `show version`, install, redundancy, interface and app-hosting inventory | One known owner; exact identities; no unexplained active mutation or scheduling guard |
| E00-B | Secure gNOI OS.Verify and read-only source/image verification on each device | Actual capability and image identity recorded, with unsupported cases explicit |
| E00-C | Probe each application's service from an independent host; collect Node Ready and Pod Ready separately | Observable application response baseline, not readiness alone |
| E00-D | Sample topology taints/conditions across several heartbeat and reconcile periods; inspect manager decisions | Explain the previously observed transient `uninitialized` taints; repeated unexplained flapping becomes a defect, not a passing final snapshot |
| E00-E | Reproduce worker replacement with retained settled and unresolved old-Pod-bound leaves in the disposable API lane; then observe equivalent retained lab history | Settled out-of-authority history is not rewritten; unresolved claims remain fenced and recoverable. No recurring status-write denial loop over at least three reconciliation periods; manager and every network worker inspected, without deleting history to hide the symptom |
| E00-F | Test the repaired harness against fixtures before a physical run: single-direction resume, mismatched frozen target/source, changed worker, empty app inventory, absent guard/log evidence and interrupted collection | Each report names only executed directions and verified checks; incomplete evidence cannot receive full acceptance. Capture manager/app/network image IDs and logs, original baseline, exact approvals, guard/claim/RPC ordering and final ledger/Lease settlement; no hard-coded source substitution |
| E00-G | Install pinned tooling; regenerate both CRD copies and DeepCopy artifacts twice; run envtest, chart admission tests and candidate CI | Reproducible second generation has no changes; real-server test counts/skips and exact candidate results recorded; missing tooling is resolved |
| E00-H | Create DeviceOperation/leaf before its manager binding, delay that binding, then supply it; repeat with wrong Pod/UID, stale binding and a permanent authorization failure | Initialization waits boundedly without a denial hot loop or premature RPC; exact binding enables progress. Wrong identity never gains authority. Correlate logs/object revisions and count transport dispatches; do not classify all forbidden responses as transient |

Close E00 only with the matrix, exact deployed revisions, baseline results,
test topology and evidence storage location recorded. Prior `/tmp` reports
are historical inputs, not proof that current prerequisites hold.

## 4. E01 — repair observation correctness and trust provenance

Code: `internal/provider/topology_observation.go`, its tests,
`internal/drivers/iosxe/topology.go`, `internal/drivers/common`,
`internal/topologyhealth/gate.go`, `api/v1alpha1/types.go`, manager health
acceptance and native status-write admission.

Current increment: `938a488f` added newer-path OSPF adjacency traversal and
source/limit corrections; `9523720c` added source/interface identities and
collection metadata; `6bd7d471` adds producer, sequence, collection-window,
and directional-rate validity checks; `0884666e` binds observations to the
network-worker Pod UID. `4711d3c7` separates collection/write deadlines;
`f9b76322` adds publisher binding/sequence guards; `c5a2deeb` adds native
authenticated Pod-UID comparison. These increments do not close the full manager
acceptance contract. Keep the positive publication evidence, then
execute the remaining work below.

### Required updates

1. Set completeness from actual source coverage. Truncation, invalid records,
   duplicates that cannot be disambiguated and unsupported source paths must
   yield incomplete/unknown evidence. Bound deterministic output and record
   why data is incomplete; never silently lose evidence and keep `Complete`.
2. Preserve neighbor source, interface and routing context. Do not merge CDP
   and OSPF identities based solely on equal strings, or multiple adjacencies
   solely by neighbor ID. Retain OSPF VRF/process separately from area and
   preserve the remote interface when available. Define collision-safe
   composite identity encoding and qualified peer selectors; test legitimate
   parallel adjacencies, same-name protocols and delimiter-bearing values.
3. Qualify the implemented newer OSPF adjacency traversal. Distinguish a
   proven empty source from unread, unsupported or partially decoded data;
   test both model paths and overlapping records. Preserve compatibility for
   telemetry consumers.
4. Add collection start/end, oldest source time, sample sequence, exact worker
   Pod incarnation/config revision, and manager acceptance binding. Define
   sequence reset on worker replacement **and container/process restart within
   the same Pod**; the current process-local counter resets in both cases.
   Choose a persisted counter or a manager-accepted producer epoch, not just
   Pod UID. Replay and old-incarnation samples cannot refresh acceptance.
   Bind `observedAt` and collection times to the actual oldest contributing
   source time; a fresh envelope cannot rejuvenate old source data. Bound the
   collection with a per-cycle deadline and test context cancellation. Use a
   separate bounded publication context so timeout/incomplete evidence can be
   persisted; cancellation of the parent shuts down both operations. If an
   API outage prevents publication, consumers expire the previous sample and
   deny new opted-in claims. Prove that slow sources honor cancellation.
5. Validate identity, producer and time before using evidence. Separate
   worker-owned samples from manager-owned acceptance; protect both in
   admission. Bind request Pod identity to the manager-owned target proof in
   native admission, not merely the sample's claimed Pod UID. A new Node
   heartbeat must not refresh an old network sample.
6. Retain the corrected specific stale/incomplete/skew assertions in
   `TestEvaluateRequiresCompleteFreshEvidence`; extend the suite for the
   still-missing replay/incarnation/collection cases. Enforce field lengths
   and bounded error text before publication so a rejected status write cannot
   silently leave an older complete sample as the only available evidence.
7. Qualify stored-object/schema migration from map-by-ID to atomic neighbors,
   including multiple status field managers, legacy missing-identity samples,
   mixed publishers and attempted CRD/runtime downgrade. Document the required
   upgrade order and block rollback when duplicate peer IDs are incompatible
   with the old schema.
8. Replace optional/variadic runtime producer identity with an explicit
   validated managed observation input. Missing expected Pod identity must
   block opted-in provenance-dependent grants, including legacy-worker
   migration. Correct comments that call worker-supplied fields manager-owned
   or authenticated before acceptance exists. Preserve the shared-account
   trust model: a matching caller-supplied string alone is not authentication.
9. Start the publisher for network read-only workers using existing read
   permissions/status ownership, without enabling write reconcilers. Verify
   manager provisioning, RBAC, native admission and startup together; simply
   dropping `!opts.ReadOnly` is not sufficient proof of safe integration.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E01-A | Unit fixtures at 0, 1, 64 and 65 records for both interfaces and neighbors; reordered/conflicting duplicates; same peer across protocols/interfaces/VRFs/processes; delimiter/overlength values; valid empty and unsupported sources | Deterministic bounded output; no false completeness, identity collision or accidental merging; error status remains publishable |
| E01-B | Fake-clock tests for stale, future, replayed, slow/hung collection and reordered samples; fresh envelope with old source times; wrong/missing identity; Pod replacement and same-Pod process restart; manager restart | Exact reasons; bounded cancellation; durable ordering; acceptance never extends oldest evidence freshness or trusts an empty expected identity |
| E01-C | Real API updates using manager, app worker, network RO/RW worker and unrelated identity; same shared account on wrong device/stale Pod; persisted map-to-atomic schema transition and mixed-version publication/downgrade | Only designated fields writable; old/wrong worker and forged manager acceptance rejected; migration preserves evidence and blocks incompatible rollback |
| E01-D | Compare physical summaries with device interface/CDP/OSPF output; interrupt a test source or use an unsupported source fixture | Physical coverage documented; missing data blocks opted-in checks, with no upgrade leaf mutation |
| E01-E | Render/start both network RO and RW profiles; exercise real admission with bound tokens and retained wrong-device/stale-Pod identities; perform a physical RO publication check | Both profiles publish permitted observations; RO cannot install, activate, configure or authorize mutations; app identity cannot write network evidence. Do not use admin impersonation without bound-token identity as the only security test |

Close E01 only when the new negative tests fail against the old behavior,
pass against the correction, and physical observations match their declared
coverage. A successful transport call alone cannot pass E01-D.

### Review regression coverage and next test additions

These test names are the handoff for the next implementer; a passing
characterization must not be counted as a passed future acceptance gate.

| Test / lane | Current assertion | Remaining acceptance |
| --- | --- | --- |
| `TestPublishNetworkObservationRequiresExactManagedWorkerBinding` | Exact publication preserves manager status; 11 negative cases assert the specific rejection and unchanged persisted status; repeated sequence rejected and next sequence accepted | Add write spies, stale-reader/conflict/lost-response cases, collection time vs Pod start/readiness, and live-manager acceptance to N1. |
| `TestPublishNetworkObservationRestartSequenceRecovery` | Same Pod resumes at 10,001 after persisted 10,000; a replacement manager-bound Pod starts at one; overflow is rejected | Add concurrent-producer, stale-reader/lost-ack, manager restart, time regression and manager-acceptance coverage in N1. |
| `TestEnvtest_NetworkObservationStatusRoundTrip` | Kubernetes 1.35 CRD accepts complete then incomplete samples and preserves manager fields. Duplicate interface/CDP inputs are reduced to bounded reason classes and accepted as incomplete evidence. | Admin-client schema/persistence coverage is passing. It is not token/policy coverage; add actual admission and partially initialized manager-health tests in N2/N3. |
| Helm render/preflight lane | Policy structure and compiled digest agree | N2 must install the exact rendered policies, inspect their type-check results, and test authenticated tokens against them. |
| Required new N2 admission suite | Not implemented by this review | Correct/peer/stale/missing-Pod tokens; direct API replay and forged provenance; optional fields; manager acceptance immutability; app and RO mutation denial; chart/binary migration. Require a positive write for the authorized worker so universal denial cannot pass the suite. |
| Required new N3 publisher timeout suite | Not implemented by this review | Actual collection cancellation followed by a successful incomplete status write under its independent deadline, parent cancellation and API-outage expiry; use clock/deadline injection where practical. |

Review validation commands (repository root):

```sh
GOCACHE=/tmp/cvk-gocache go test -race -count=1 ./internal/provider \
  -run '^Test(BuildNetworkObservation|PublishNetworkObservation)'
# Use the pinned setup-envtest version from Makefile and its 1.35.0 assets.
KUBEBUILDER_ASSETS="$(setup-envtest use 1.35.0 -p path)" \
  GOCACHE=/tmp/cvk-gocache go test -tags envtest -count=1 -v \
  ./internal/provider -run '^TestEnvtest_NetworkObservationStatusRoundTrip$'
git diff --check
```

The focused race lane and the full `./internal/provider` package passed,
including publisher negative cases, restart recovery and overflow. The new
Kubernetes 1.35 round-trip test passed with exit code zero and no skips.
`mkdocs build --strict` and `git diff --check` passed. Runtime publisher and
normalization code changed in this review; no chart, lab deployment or device
software was changed. Full E00-G still requires generation parity and the
complete candidate CI/envtest matrix; the recorded local subset does not close
it.

## 5. E02 — supply measured path and platform-health inputs

Code: IOS-XE readers/optional driver interfaces, worker observation publisher,
`api/v1alpha1`, `internal/topologyhealth`, telemetry export and fixtures.

Current percentage publication uses reported RX/TX rates and interface speed,
with direction-specific presence/validity flags added in `6bd7d471`. Missing
or overflowing rates are Unknown and measured zero is valid. Oldest-source/
sample-interval acceptance, independently measured accuracy and supervisor
readiness remain unqualified; rate flags alone do not close capacity policy.

### Required updates

1. Publish directional counter samples, sampling interval, interface capacity
   and provenance. Document the unit conversion and headroom formula for each
   supported model. Where a qualified device-reported rate supplies this
   information, reuse it with its documented sampling/age semantics; do not
   add a parallel counter-delta collector without need. Keep incoming and
   outgoing capacity distinct where needed.
2. Derive headroom only from trustworthy covered samples. Counter reset/wrap,
   speed changes, first sample, missing fields, zero/unknown capacity or invalid
   intervals produce Unknown. Validate ranges and avoid arithmetic overflow.
3. Require explicit interfaces/path scope for a headroom policy. Reject a
   threshold with an empty effective scope, rather than passing an empty loop.
4. Add bounded supervisor/stack readiness observations for supported cohorts;
   missing inventory remains Unknown. Advertise capability only after a
   successful probe, and retain unsupported platform behavior explicitly.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E02-A | Counter fixtures for reset, wrap, huge values/conversion overflow, capacity change, speed with absent statistics, either rate direction absent, measured zero, threshold boundaries and empty scope | Correct conservative headroom or explicit Unknown; absent rates must not become 100% headroom |
| E02-B | Sample idle then controlled loaded lab test links, recording interface counters and independent traffic rate | Published utilization agrees within a documented tolerance chosen before the run |
| E02-C | Remove/stale one contributing sample and recover it; simulate standby/stack mismatch before attempting any hardware fault | Unknown/unhealthy blocks; fresh complete recovery clears the specific blocker |

Close only the cohorts actually tested. Supervisor failover claims require
appropriate physical redundancy; a single standalone switch cannot qualify
them. Controlled traffic over an emulated bottleneck must be labelled as such.

## 6. E03 — implement enforceable network admission policy

Code: `internal/topologyrollout/{policy,ledger,store}.go`, rollout API/snapshot,
manager planning/execution, worker managed mutation checks, chart policy,
admission/RBAC and operator diagnostics.

### Required updates

1. Implemented through `617b1cfc`: bounded administrator-required
   critical-service and singleton-path prohibitions apply at planning and
   immediately before execution. The current combined lifecycle is blocked as
   a whole. Future independently qualified preparation may bypass disruption
   protection only through the separate E04–E06 contract; campaign input must
   never relax administrator restrictions.
2. Implemented through `7417b09b`: add overlapping administrator-declared risk groups with exact physical
   membership. Freeze relevant membership/policy into plans and include
   non-target unhealthy members and all campaigns in one CAS admission.
3. Partially implemented in the current candidate: matching risk groups may
   declare an aggregate transfer-rate ceiling. The manager freezes the lowest
   conservative per-transfer share, and the worker paces remote materialization
   and gNOI upload; incapable resolvers fail closed. Still evaluate
   alternate-path and surviving-path headroom using declared scope and E02
   evidence, add qualified hold-time continuity, and physically measure the
   aggregate bound on a known shared path.
4. Implemented through `ae4f3a7b`: bind grants to accepted evidence hash,
   producer identity, policy epoch,
   control revision and absolute expiry. Workers validate this authority
   before every new mutation claim, including activation after a long install.
   Publish renewal/fencing through durable CAS; no admission/claim race may
   preserve expired authority. Bound evidence changes while a grant is in use.
   Planning success must not substitute for claim-time acceptance: wire the
   network gate into execution and post-mutation health/soak, with accepted
   evidence and expiry persisted in the authority workers validate. Retain
   API-outage fail-closed behavior without moving device reads to the manager.
5. Implemented through `ae4f3a7b`: recheck during recovery/soak. A late
   failure stops new admissions while
   already accepted operations remain observed and reserved through settlement.
6. Expose the failing check/domain/sample time/remediation in status and Events.
   Preserve bounded metric labels and existing omitted-feature hashes.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E03-A | Unit/CAS race tests: overlapping groups, unhealthy peer outside target set, competing campaigns, policy tightening, membership drift | Every applicable ceiling enforced atomically; changed frozen membership requires new approval |
| E03-B | Real API tests: planner relaxes admin checks, edits protection, spoofs approval or acceptance; worker claims after expiry/rotation | Unauthorized changes and stale authority rejected before RPC dispatch |
| E03-C | Physical declared redundant pair: healthy peer, then isolated test peer/path unavailable or stale | Healthy case admits one disruption; unhealthy case dispatches zero new mutations |
| E03-D | Declare a critical singleton; attempt activation and separately qualified preparation | Activation prohibited; preparation allowed only if its own E04 capability and policy permit it |
| E03-E | Load the declared surviving/distribution test path; run two campaigns and measure bytes/time | Headroom blocks as specified; aggregate CVK transfer rate stays within the configured bound plus a predeclared burst tolerance |
| E03-F | Pass planning then expire/change evidence before approval, grant, drain and claim; expire it between install/activation; introduce API/cache lag or worker replacement; fail network health during soak | New claims and forbidden RPCs remain zero after invalidation, including with fresh generic Node/device health. Soak continuity resets; accepted work stays observed/reserved. Omitted network policy preserves the baseline contract |

Close E03 after actual service probes qualify the declared path scenario.
Label-only redundancy tests qualify accounting, not forwarding resilience.

## 7. E04 — prove the device preparation boundary

Code/evidence: `internal/softwarelifecycle`, IOS-XE lifecycle adapter,
gNOI OS inventory readers, capability fixtures and physical qualification report.

Current evidence: `11ae6704` proved Install/Validated followed by an uncertain
NoReboot Activate, not an Install-only hold. C0 must reconcile `.103` before
reuse. C4's qualification path must structurally prevent Activate/reboot after
Install; stopping a process at a timing-sensitive point in the existing
combined reconciler is not an acceptable boundary test. E04 defines device
semantics before E05's durable receipt implementation; requiring E05 to pass
before investigating E04 would create a circular dependency.

### Required execution

1. Capture the installed/active image inventory, exact activation identity and
   supervisor state before an operation on each supported release/cohort.
2. Exercise image transfer/install independently from activation, with service
   probes and console capture. Determine whether device preparation changes
   forwarding, boot configuration or application behavior. Do not equate
   `OS.Activate(NoReboot=true)` with an install-only or non-disruptive contract.
3. Restart the worker after preparation; independently re-observe staged
   identity. Explain the connection between verified source digest and device
   installed identity, including limits of the platform's verification API.
4. In controlled test state, remove/change staged material and verify detection.
   Repeat with trust rotation, worker replacement and supported supervisors.
   Retain device operation history to distinguish observation from replay.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E04-A | Transfer/install, hold before activation, restart observer | A stable, independently observable installed identity; no accidental activation |
| E04-B | Probe services throughout preparation; examine boot/install state | Measured disruption class, not an inference from the strategy name |
| E04-C | External image removal/change and supervisor mismatch | Stage cannot be falsely reused; exact limitation recorded |
| E04-D | Repeat for upgrade and downgrade image/release combinations | Capability table names both directions and exact inventory semantics |

E04 passes only for an evidenced cohort. If stable identity cannot be proven,
keep that cohort on the combined lifecycle and mark independent staging
blocked. This blocks E05/E06 physical completion for that cohort; it does not
complete T3/T4. Locate a capable release/platform before claiming full support.

## 8. E05 — implement durable staging and retained ownership

Code: existing leaf/campaign API, `internal/softwarelifecycle`, IOS-XE adapter,
`internal/provider/softwareupgrade`, ledger/coordination and native admission.

### Required updates

1. Define an opt-in managed protocol for prepare-only intent. Preserve current
   combined behavior by default. Persist operation claims before every device
   side effect; store a receipt only after conclusive qualified observation.
2. Make the receipt immutable and bind device/Node UIDs, physical identity,
   source digest, exact activation version, source/trust identities,
   supervisor inventory, protocol/policy versions and relevant operation
   claims. Canonically hash it. Mark any installed-digest limitation explicitly.
3. Persist retained staged ownership after active work settles. A competing
   campaign cannot replace the staged intent unnoticed. Record invalidation
   after conflicting mutation or drift; do not manufacture a new receipt
   under an old approval. Re-observe inventory before reuse.
4. Define restart, cancel, delete, feature-disable, retention and garbage-
   collection rules. An uncertain operation retains its Lease/reservations;
   deleting metadata never deletes the image or abandons unresolved evidence.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E05-A | API/CEL tests: receipt edits/removal, UID recreation, hash ordering, old objects/new workers and new objects/old workers | Immutable exact receipt, preserved baseline hashes and safe mixed-version rejection |
| E05-B | Inject crashes before claim, after claim/before RPC, after device acceptance/before status and after receipt persistence | No duplicate mutation; ambiguity stays reserved until observed or audited reconciliation |
| E05-C | Physical prepare-only run; restart manager/worker; start competing preparation; change source/trust/inventory | Receipt survives restart; conflicts/drift block reuse; no unapproved activation |

Close when a staged device can remain idle across restarts with recoverable
ownership and trustworthy receipt evidence. E06 must still implement later
activation; terminal `StagedForNextBoot` alone does not close this package.

## 9. E06 — implement separate activation approval and reservations

Code: rollout/leaf APIs, manager/worker state machines, ledger/store,
maintenance/drain integration, chart RBAC/CEL, CLI and examples.

### Required updates

1. Add append-only activation authorization bound to frozen plan, staged
   receipt hash and an explicit UTC activation window. Define per-target or
   bounded cohort scope before schema generation, including whether a cohort
   waits for all its receipts; never approve unspecified future receipts.
2. Bind approver identity through native admission and a distinct activation
   permission. Preparation approval and existing plan approval alone cannot
   authorize activation. Expired/drifted approval requires new valid intent.
3. Add separate preparation/activation windows and a latest-start/recovery
   allowance. Define exact boundary comparisons with a fake clock. Closing
   a window prevents new claims without cancelling accepted device work.
4. Implement atomic phase-budget transitions: prefetch charges transfer;
   qualified staging charges transfer and its measured disruption class;
   conclusively idle staging retains ownership without active transfer;
   drain/activation/recovery holds disruption through application/network soak.
   Uncertain work retains every applicable reservation. Charge ongoing
   transfer too if it overlaps activation; never release then reacquire in
   separate unaccounted operations.
5. Revalidate receipt/inventory, current network grant, trust, identity and
   budgets before drain/activation. Preserve canary/wave sequencing and narrow
   rollback semantics. Recovery observes accepted operations without replay.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E06-A | Real API tests: preparation-only identity, activation approver, forged actor, changed receipt hash, mutated/removed approval, window expiry | Only exact independently authorized activation can claim a mutation |
| E06-B | Concurrent campaigns and failover at every ledger/claim/status transition; pause/cancel; approval/source/Secret drift | No budget gap/double admission; no approval inheritance or duplicate RPC |
| E06-C | Physical prepare, hold through a closed activation window, restart, then submit valid activation approval | Image remains staged while closed; only approved activation causes device transition |
| E06-D | Lost activation response, timeout, standby mismatch and partial rollback through fake-device faults plus feasible physical cases | Observe without replay; unresolved state quarantined; no fleet-wide rollback |
| E06-E | Separate upgrade and downgrade with independently approved activation | Exact final versions, healthy services, restored scheduling and settled reservations in both directions |

E06 closes the main new lifecycle contract only after E06-C/E pass on qualified
physical hardware. A combined Reload campaign is a regression test, not a
substitute. Packet capture of TLS traffic alone cannot prove RPC counts;
correlate instrumented dispatch claims with device operation/install history.

## 10. E07 — finish supported workload relocation

Code: `iosxesoftwarerollout_drain*.go`, `internal/workloaddrain`,
`internal/provider/maintenance`, IOS-XE app inventory and scheduling examples.

### Required updates

1. Add eligibility for qualified node selectors, required node affinity and
   topology spread within the existing ReplicaSet/Deployment subset. Hash and
   retain owner/placement intent. A feasibility check reports likely blockers;
   the native scheduler remains the placement authority. A feasibility check
   does not reserve capacity or guarantee that replacement will succeed.
2. Preserve PDB-aware Eviction, exact UID teardown, replacement checks,
   finalizers and maintenance fencing. No forced deletion, implicit replica
   increase, weakened selectors or automatic application duplication.
3. Reproduce the prior stale app/lease and non-portable `flash:/nginx.tar`
   failure. Determine whether each is configuration, expected quarantine or a
   cleanup defect; add a regression and repair defects in the owning layer.
4. Supply test workloads deployable on all eligible destinations. Capture real
   service availability through drain, replacement, reboot and recovery.
   Keep unsupported workload owners/storage blocked with explicit reasons.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E07-A | Unit/API tests: placement preserved, PDB 429, owner/UID drift, disallowed volume, foreign device app, Pending replacement | Activation remains blocked; no direct workload DELETE or constraint relaxation |
| E07-B | Physical two-replica service with PDB, portable image and spare eligible capacity; drain and upgrade, then drain and downgrade | Device-clean proof and reachable replacement precede activation in both directions |
| E07-C | Hard placement with no spare capacity, absent package and exhausted PDB; then restore prerequisites | Blocked status explains the cause; controlled recovery succeeds without manual scale-down as the success path |
| E07-D | Restart during eviction/cleanup/replacement; cancel and disable execution gate during cleanup | Exact cleanup resumes; no leaked app or silently abandoned maintenance state |

Close only after successful application-level probes and supported automated
drain in both directions. Devices that cannot run the supplied package may
remain device-only upgrade targets, but cannot count as positive app-hosting
qualification. Record the tested subset and unsupported owners explicitly.

## 11. E08 — qualify native TAS with physical CVK workloads

Code: manager drain eligibility/raw Pod reader, admission, optional TAS
examples, `native-tas-kind-test.sh`, CI lane and operator guide.

### Required updates

1. Test whether pinned typed clients preserve served scheduling-group fields.
   Use a narrow raw/unstructured reader if necessary, with exact Pod UID and
   current-object binding. Missing typed fields cannot mean “not grouped.”
2. Reject unqualified group-managed drain before eviction or mutation; bind
   group membership into eligibility checks so a change cannot evade fencing.
3. Supply a native controller-backed workload with proven recreation and group
   semantics. Qualify actual CVK startup and services, then add group-aware
   drain only for that tested contract. Do not make CVK an implicit group owner.
4. Retain baseline Kubernetes 1.35 behavior. Use a dedicated qualified cluster
   for the repository's optional TAS version/gates. Physical devices move
   between owners only through E12's controlled handoff; never run two clusters
   against one switch. Evaluate hierarchy separately for a concrete use case.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E08-A | Served raw scheduling-group object read with old typed client; membership change/race; API absent | Group is recognized or rejected explicitly; no silent eligibility bypass |
| E08-B | Pinned disposable native-TAS lane: co-location, maintenance-tainted member, selected-site capacity shortage, partial group failure and controller restart | Schema and scheduler behavior match examples; Pending/blocking reasons are visible |
| E08-C | Same qualified native owner on physical CVK Nodes; restart/recreate members; probe services | Real applications start and recover within group constraints |
| E08-D | Group drain with/without spare domain capacity after E07 | Supported recreation succeeds or blocks before activation; no partial-group availability claim from binding alone |

Synthetic Nodes close E08-B only. Physical group lifecycle and supported group
drain remain open if native owner/runtime prerequisites are missing. Optional
hierarchy can be documented as unqualified without raising the baseline.

## 12. E09 — measure distribution and gate durable caching

Code: image resolver/source transports, transfer status/metrics, worker storage
configuration and chart, ledger phase accounting and lifecycle revalidation.

### Required updates and decision

1. Measure origin-to-worker and worker-to-device bytes, elapsed time,
   throughput, repeat fetches and WAN crossings separately. Record actual
   worker placement, cache hit/miss, CPU/storage cost and network route.
2. Compare the current ephemeral cache, origin streaming and an available
   mirror across repeated campaigns. Record the workload, capacity and
   bandwidth goals before testing; justify any durable-storage addition.
3. If justified, implement opt-in per-worker prefetch to an explicitly
   supplied PV/PVC using existing binaries/accounts. Bind artifact readiness
   to namespace, frozen source/trust identity, digest and size. A cache hit
   still requires current source authorization. Preserve existing fallback
   when storage is absent and policy permits streaming.
4. Add durable ownership/locking for restart or writer overlap, RWO/RWOP
   placement, atomic verified publication, size/capacity bounds, TTL/GC and
   active-reader protection. Apply rate limits and E06 transfer accounting.
5. Cache loss or corruption invalidates readiness. Refetch only the approved
   source under current authority; preserve stage and activation approvals.
   Keep HTTPS/SSH verification, SSRF restrictions and credential isolation.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E09-A | Physical image transfer with both path segments measured; repeated runs with warm/cold cache | Reproducible benefit/cost report; no locality inference from labels alone |
| E09-B | Conditional cache tests: corrupt/partial/symlink entries, digest mismatch, disk full, permissions, namespace/source change | Reject unsafe bytes and unauthorized reuse; no false readiness |
| E09-C | Kill during publication; recreate Pod; lose/reattach volume; concurrent writer/read/GC | Safe ownership, revalidation and bounded storage; no deletion of active data |
| E09-D | Physical approved campaign after cache loss under open and closed authority/windows | Allowed refetch uses the same frozen source; no extra activation or approval bypass |

E09-A and the design decision are mandatory. If caching is selected, E09-B–D
must pass before closing its implementation. Ephemeral `emptyDir` reuse does
not demonstrate persistence across Pod replacement or volume loss.

## 13. E10 — implement graph diagnostics and policy drift

Code: observation schema/normalization, pure comparison logic,
manager read-only diagnostics, status/Events and `kubectl ciscovk` output.

Current implementation is a bounded helper in `internal/topology/graph.go`
with a read-only kubectl-plugin caller over manager-accepted status. E10-A is
partial. The exact `34050731` physical run passed E10-C's policy-hash and
read-only-authority separation and part of E10-D's real API/CLI path. E10-B's
physical isolated-link change and full runtime qualification remain open.
`GraphObservation.ObservedAt` is checked only when `MaxObservationAge` is
positive. CLI JSON separates a topology-content hash from a deterministic
provenance hash over manager-accepted device/worker/sequence/collection
identity and, when selected, the administrator graph ConfigMap UID,
resourceVersion and canonical content hash. Structured JSON hashing, state
bounds, three-way conflicting-peer diagnostics, remote-port reverse matching,
strict peer mappings, external endpoints and declared-link comparison are
implemented. `graph.json` is admission protected but separate from
`policy.json`, so it cannot alter rollout approvals. Broader protocol/VRF/LAG,
maximum-bound and physical-drift coverage still require the tests below.

### Required updates

1. Build a bounded diagnostic graph retaining local/remote interface, protocol,
   VRF context, sample time and provenance. Resolve managed peers only from
   sufficient identity evidence; preserve unknown peers and asymmetric edges.
   Do not compare a CDP hostname or OSPF router ID directly with a managed
   device serial as if the namespaces were interchangeable.
2. Compare against administrator-declared peers/risk groups. Report missing
   peers, changed uplinks, stale coverage and potential single points of
   failure in the declared model. Never assert end-to-end redundancy solely
   from CDP links.
3. Add read-only explain output with the evidence behind each finding. A
   policy can block on qualified evidence; suggestions require a new operator
   plan and do not rewrite labels, membership or approvals.
4. Repair helper determinism and resource bounds before integrating it:
   conflicting duplicates must not select a different edge when reordered;
   their diagnostics must also be independent of the incoming peer order.
   Canonical encoding must distinguish delimiter/newline-bearing fields.
   Bound input adjacency count, declared links, field lengths, diagnostics and
   user-supplied limits in addition to unique output nodes/edges. Duplicate
   floods must not bypass the edge cap and allocate unbounded diagnostics.
5. Separate graph coverage from operational health. Define stale/empty
   coverage and DOWN adjacencies explicitly; match reverse observations in
   protocol/VRF/interface context. Different local port names at opposite ends
   are normal; require qualified remote-port correspondence or report unknown,
   not a false asymmetric-link result. Include accepted sample identity/time in
   the evidence token, or provide a separate documented topology-content hash
   and provenance token. Never use a topology-content hash as fresh authority.
6. Wire the helper into a bounded manager-owned diagnostic snapshot and
   read-only CLI/status/Events. Specify reconcile triggers and persistence;
   protect any new manager fields from workers via CEL and RBAC, with schema,
   compatibility and contract-digest tests. The manager opens no device session.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E10-A | Fixtures: actual reordered conflicting duplicates, unknown/spoofed names, known-peer asymmetric links, mismatched VRFs/protocols, LAGs, stale/empty/truncated input, missing/unexpected declarations, ambiguous hash inputs, edge/input/diagnostic/declaration bounds | No first-wins identity ambiguity or hash collision from serialization; explicit coverage/health semantics and bounded deterministic diagnostics |
| E10-B | Physical capture, controlled change to an isolated test uplink, restore and recapture | Drift shown with provenance and cleared on fresh recovery; declared authority unchanged |
| E10-C | Compare protected labels, policy, approval hashes and RBAC before/after discovery changes | Diagnostics cannot grant mutation authority or silently edit the plan |
| E10-D | Real API/CLI integration: publish/reconcile, watch observation/policy changes, restart manager, stale worker input, unauthorized diagnostic status writes | Reachable operator output updates and expires predictably; only manager owns acceptance/diagnostics; no per-node accounts or device sessions introduced |

Close after usable diagnostic examples and physical drift evidence. An
authoritative discovered topology or graph-cost scheduler remains excluded.

## 14. E11 — qualify another platform and decide API portability

Code: optional driver/lifecycle interfaces, NX-OS or IOS-XR adapter, shared
coordination/source security, platform fixtures and an architecture decision.

### Required updates

1. Inventory candidate hardware, exact release, gNOI OS/certificate service,
   compatible image pair and management ownership. Begin with read-only probes;
   the existing NX-OS lab Node alone establishes no software-lifecycle support.
2. Implement the qualified platform's inventory, version comparison, image
   compatibility, supervisor order, trust, stage/activate/verify/rollback and
   complete app inventory. Audit shared IOS-XE-shaped version assumptions.
3. Reuse coordination only where semantics match. An absent gNOI service stays
   unsupported; do not substitute an implicit CLI executor. Keep platform
   credentials and capability flags isolated.
4. After the second driver passes, write an ADR accepting or rejecting a
   generic public SoftwareRollout API. If accepted, implement and test an
   IOS-XE-compatible migration; if rejected, document platform-specific APIs.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E11-A | Read-only hardware/service probes; unsupported service/driver fixture | Capabilities evidenced; unsupported target creates no mutating leaf |
| E11-B | Reuse E03/E05/E06 failure suite with platform-specific inventory/version fixtures | Equivalent authorization, reservation and no-replay guarantees |
| E11-C | Physical second-platform prepare/approve/upgrade/downgrade and recovery suite | Qualified lifecycle and final healthy device, with application tests for any claimed drain capability |
| E11-D | Public API ADR and, if selected, stored-object/client migration and downgrade tests | Existing IOS-XE clients remain supported; no speculative empty platform fields |

Without a qualified image pair and hardware, mark E11 blocked and list those
inputs. A negative capability result is useful evidence but cannot close the
second-driver objective.

## 15. E12 — measure scale and qualify ownership transfer

Code: benchmarks/load fixtures in `internal/topologyrollout` and manager tests,
metrics, managed handoff/retirement controllers and operator runbook.

### Required updates

1. Benchmark snapshots/campaigns at 1, 10, 50 and 100 targets, then test ledger
   record/byte limits and one-over-limit cases. Exercise simultaneous campaigns,
   overlapping groups, unhealthy non-target members and slow API responses.
2. Measure reconcile p50/p95/p99, API request rate, conflict retries, watch
   cardinality, memory and serialized status/ledger size. Set acceptance budgets
   before runs in a versioned test profile; derive a supported envelope, not
   a bigger default limit. Keep atomic global/group admission intact.
3. Implement or close gaps in controlled single-cluster ownership handoff:
   quiesce claims, settle accepted operations, verify physical state, retain
   identity/history, fence old writers, then transfer ownership. Define how
   idle staged receipts are revalidated or invalidated; never copy approval
   blindly to a new owner.
4. Document controlled offline cross-cluster transfer with old authority
   conclusively removed, then explicit destination enrollment. If the old
   controller may still mutate, block transfer. Cross-cluster Lease expiry is
   insufficient fencing; active multi-cluster failover stays excluded.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E12-A | Repeatable benchmark/load runs at recorded limits with API latency and contention | Measured supported envelope; over-limit admission explicitly rejects without dropping reservations |
| E12-B | Two prospective owners; restart/partition at each handoff boundary; active mutation, uncertain state and staged ownership | At most one authorized writer; unresolved operations prevent transfer |
| E12-C | Physical single-device handoff, positive destination observation and negative old-writer mutation attempt | New exact owner established; old writer cannot mutate; no history/identity loss |
| E12-D | Offline transfer rehearsal if cross-cluster migration is claimed | Evidence of old-authority shutdown/fencing before destination credentials/authority become usable |

Synthetic fleets qualify controller scale only. Physical tests qualify device
semantics at the tested cohort size. Report both separately; three switches
cannot substantiate large-fleet throughput.

## 16. E13 — integrated acceptance and release closure

### Deployment and compatibility sequence

Each implementation package must make this sequence executable for its actual
schema/protocol, including exact Helm values and commands in the final runbook:

1. Capture current CRs, ledger identity, admission/RBAC and image digests;
   quiesce new admissions and retain recovery for accepted operations.
2. Apply compatible additive CRDs, admission and retained cleanup permissions
   with new execution gates disabled. Test interruption at this boundary.
3. Upgrade manager, then workers; verify exact bound revisions and protocol
   acknowledgement before granting new behavior. Test interruption and mixed
   versions, feature disablement and resumed cleanup.
4. Enable the new capability only on its qualified cohorts; server-dry-run
   the actual manifests, review/freeze and approve them through their real
   identities. Never remove admission bindings to make a passing test possible.
5. Before rollback to old software, settle new-protocol activity and prove
   stored objects/claims can be read safely. If not, block downgrade and use
   the documented quiesced migration; never erase claims/finalizers to proceed.

### Final physical acceptance matrix

Run on one pinned candidate revision after package-level tests. Each row
must cite its setup manifest, commands, observations and result.

| ID | Setup/action | Pass condition and owning packages |
| --- | --- | --- |
| F01 | Healthy declared redundant devices, overlapping risk groups, two campaigns | One permitted disruption at a time across all applicable domains; forwarding probes confirm the tested service contract (E03/E06) |
| F02 | Required peer down/stale/outside campaign; critical singleton | No new mutation; exact blocker and current evidence visible (E01–E03) |
| F03 | Controlled loaded/oversubscribed path | Preparation is blocked or measured rate-bound; unsafe disruption blocked (E02/E03/E09) |
| F04 | Prepare image, close activation window, restart workers/manager | Staged receipt/ownership retained; no activation until valid separate approval and current window (E04–E06) |
| F05 | Replicated portable app, PDB and spare eligible capacity | Device-clean proof plus reachable replacement before activation during both upgrade and downgrade (E07) |
| F06 | Hard placement without capacity; unqualified group or unsupported package | Explained block, no weakened placement/PDB and no activation (E07/E08) |
| F07 | API outage or restart at durable mutation boundaries | No duplicate operation; uncertainty retains authority/accounting and recovery is auditable (E05/E06/E12) |
| F08 | Image, source Secret UID, trust, device identity, topology or receipt drift | Changed frozen intent cannot reuse old approval; permitted credential rotation follows explicit tested rules (E03/E05/E06/E09) |
| F09 | Cache corrupt/missing, when durable caching is selected | Correct frozen-source refetch or block; no extra activation and no false staged receipt (E09) |
| F10 | Actual native TAS application group and controller recreation | Qualified placement/startup/service/recovery; group drain only within tested semantics (E08) |
| F11 | Physical diagnostic topology change, then restoration | Accurate drift and recovery with no change to administrator authority (E10) |
| F12 | Second platform lifecycle and controlled owner handoff | Platform-specific positive qualification and single-writer evidence (E11/E12) |
| F13 | Complete upgrade and separate downgrade on `.100`, `.101`, `.103`, recording each device and direction independently | Exact final OS and secure gNOI health; applicable app/network probes pass; expected scheduling restored; maintenance settled and no unexplained reservations/Leases. Run independent prepare/approve/activate on each E04-qualified cohort; a target lacking that capability stays explicitly blocked for that contract, not silently omitted |

Run F01–F08/F13 for core acceptance, plus applicable F09–F12 for wider roadmap
claims. F06 requires E08-A's rejection guard, not positive group support;
F07's core crash tests do not require the wider ownership-transfer feature.
Explain cohort limitations for every row. An unsupported artifact,
manual scale-down workaround or simulated path must not be recorded as a
positive full-service scenario. Manual recovery is retained as a failure case;
after repair, rerun the intended positive path to qualify it.

Before F01 starts, require passing focused/unit, real-API/admission, generated
parity, baseline integration, security and coordination gates on the candidate;
freeze manifests, image digests, measured tolerances and recovery steps. Keep
admission enforced for the entire physical run. Stop new disruptive claims on
unexpected version, service loss beyond the declared tolerance, missing
identity/evidence or unresolved device outcome; preserve consoles, claims and
reservations for recovery. Never continue to the next switch just because a
timeout elapsed. After a code fix, publish a new candidate and rerun its affected
negative/positive gates plus the integrated lifecycle regression; do not combine
passes from different binaries into one candidate acceptance result.

### Disruptive physical qualification: image `665a7954-fix1` (30 Sep–1 Oct 2026)

This historical run exercised the currently implemented *combined* rollout path, not the
unimplemented E04/E05/E06 independent staged-receipt and activation contracts.
Ubuntu16 (`192.0.2.43`) ran native `IOSXESoftwareRollout` manifests with exact
frozen-plan hashes and an SFTP source. Read-only API inspection on 1 October
reconfirmed all six campaign/target successes; their approvals identify
`system:admin`. This does not qualify least-privilege approval roles. The run
reported admission enabled; retain policy/binding snapshots and audit coverage
before treating continuous enforcement as independently evidenced. Workload
policy was `Drain` and the namespace retained its PDB (`minAvailable: 1`).
The final workload snapshot was healthy, but the worker logs recorded repeated
maintenance-session retry errors and `Force deleting pod in running state`
events while the two selected workloads were being relocated. The device
campaigns therefore prove the combined IOS-XE upgrade/downgrade path only;
they do **not** qualify E07's PDB-safe eviction/replacement contract. The
updated harness now fails evidence acceptance on that marker instead of
calling the run green. A separate earlier run also exposed an approval/source
argument defect; the corrected run below was restarted with fresh immutable
rollouts.

The observed sequence is material for the next fix: the app worker received a
terminating Pod, retried `DeletePod` while the managed maintenance session was
unresolved or had already changed incarnation, and the upstream virtual-kubelet
delete queue later emitted the force-delete warning. This is not evidence that
the device application was safely torn down. The owning drain/provider path
must preserve the exact session through the delayed callback (or prove a
durable device-clean completion before allowing the session to change), while
continuing to fail closed on an actually stale or foreign Pod.

| Target | Upgrade rollout | Downgrade rollout | Result |
| --- | --- | --- | --- |
| `cat9k-live` (`198.51.100.100`) | `cycle-20260930195719-82acb4-cat9k-live-upgrade-rollout` → 17.18.03 | `cycle-20260930203117-91cb6a-cat9k-live-downgrade-rollout` → 17.18.02 | Both `Succeeded`; fresh gNOI Verify/health evidence and maintenance taint cleared. |
| `cat9k-lab-101` (`198.51.100.101`) | `cycle-20260930205904-3fff1e-cat9k-lab-101-upgrade-rollout` → 17.18.03 | `cycle-20260930215119-b263a8-cat9k-lab-101-downgrade-rollout` → 17.18.02 | Both `Succeeded`; target returned Ready and healthy. |
| `cat9k-lab-103` (`198.51.100.103`) | `cycle-20260930205904-3fff1e-cat9k-lab-103-upgrade-rollout` → 17.18.03 | `cycle-20260930221718-79a4e5-cat9k-lab-103-downgrade-rollout` → 17.18.02 | Both `Succeeded`; the post-reload health/recovery soak completed at 22:40Z. |

The corrected serial run produced the following additional campaigns:

| Target | Upgrade rollout | Downgrade rollout | Result |
| --- | --- | --- | --- |
| `cat9k-live` | `cycle-20260930231306-1b8162-cat9k-live-upgrade-rollout` → 17.18.03 | `cycle-20261001003405-6721c9-cat9k-live-downgrade-rollout` → 17.18.02 | Device and platform gates passed; drain evidence is rejected by the unsafe-force-delete marker. |
| `cat9k-lab-101` | `cycle-20260930231306-1b8162-cat9k-lab-101-upgrade-rollout` → 17.18.03 | `cycle-20261001003405-6721c9-cat9k-lab-101-downgrade-rollout` → 17.18.02 | Device and platform gates passed; maintenance acknowledgement briefly lagged before transfer, then settled. Drain evidence is rejected. |
| `cat9k-lab-103` | `cycle-20260930231306-1b8162-cat9k-lab-103-upgrade-rollout` → 17.18.03 | `cycle-20261001003405-6721c9-cat9k-lab-103-downgrade-rollout` → 17.18.02 | Device and platform gates passed; drain evidence is rejected. |

Evidence directories on Ubuntu16 are:
`/home/cisco/cvk-roadmap-exec-20260930-665a7954-fix1-rollout`,
`/home/cisco/cvk-roadmap-exec-20260930-665a7954-fix1-cat9k-live-downgrade`,
`/home/cisco/cvk-roadmap-exec-20260930-665a7954-fix1-lab101-103`,
`/home/cisco/cvk-roadmap-exec-20260930-665a7954-fix1-lab101-downgrade`, and
`/home/cisco/cvk-roadmap-exec-20260930-665a7954-fix1-lab103-downgrade`.
The historical corrected-harness evidence is retained at
`/tmp/cvk-roadmap-next-upgrade2` and `/tmp/cvk-roadmap-next-downgrade` on
Ubuntu16. Sanitized snapshots and filtered log excerpts are now in the
[branch evidence index](evidence/topology-2026-10-01/README.md); omitted full
polling/unfiltered logs still depend on those temporary sources.
The final state had all three Nodes `Ready`, no maintenance/unreachable taints,
complete network observations, `GNOIConfigurationReady=True`, both workload
Pods `Running`, and PDB `Allowed disruptions=1`. All manager and three network
worker Deployments used `cvk-tas-extentions:665a7954-fix1`.
The retained `.103` app-worker baseline records image ID
`sha256:1ca830c68b3f3b6e69aaa6764e77785f945d509bbefe01eea46262a0bdc6c2df`.
That proves the sampled container identity, not the complete source/build
provenance or every executing manager/network worker; E00-F must capture them.

The run also exposed admission retries on newly created objects, separate
from retained predecessor history. For `.103`'s final health DeviceOperation,
creation was at `22:40:44Z`, worker status writes were denied at
`22:40:44Z–22:40:55Z`, and manager-binding metadata plus successful execution
were recorded at `22:40:59Z`. The final inspected 20-minute window **did
contain these errors**; the earlier error-free statement was incorrect.
The ordering and current reconciler code suggest execution racing manager
object binding, rather than proving token propagation delay. Reproduce this
as E00-H, record binding transitions, and establish the cause before fixing.
No duplicate OS mutation was observed; complete dispatch/device-history
correlation is still required for F07's no-replay assertion. Forwarding and
application traffic were not independently probed, so F01, E02-B and E07's
service acceptance remain open.

The harness now records manifests, frozen plan, source digest, binding
transitions, manager/app/network worker logs, and gNOI/CLI results. It rejects
an unsafe force-delete drain marker and reports only the direction actually
executed. Its remaining limitations require E00-F/E07-B before reuse as
acceptance tooling:

- The workload check currently proves owner/replica continuity and readiness;
  it does not replace a service endpoint probe or prove a PDB-safe eviction.
- Final logs are collected after the campaign, so a future implementation
  should make the drain controller itself refuse force deletion and emit a
  durable device-clean/replacement proof before activation.
- Final taint absence does not prove fencing before an RPC or ledger/Lease
  release; the harness records an observed in-flight taint but does not itself
  establish E03/E06 reservation semantics.
- Worker identity and logs are collected from `<device>-vk` (app hosting), the
  topology-selected network worker and the configured manager deployment.
  The corrected run captured all three planes; the remaining gap is a
  durable, per-event drain/eviction proof rather than log-plane coverage.
- Image source selection is now explicit: HTTP(S) sources do not require a
  Secret, SFTP sources require the named Secret, and approval verifies the
  frozen target URL/digest/Secret against the requested transition. A source
  that does not match is rejected before mutation.
- Resumed invocations establish a new baseline. Preserve the original app
  inventory and join reports by target/rollout/leaf UIDs to detect lost apps.

Preserve the initial malformed direct-leaf attempt and the stopped reports as
failed test attempts. The recorded deletion was of the exact statusless leaf
after inspection; do not infer its lack of side effects solely from empty
status or make deleting historical leaves part of normal recovery.

Repository verification for this execution passed `go test -race -count=1
./internal/topology ./internal/provider/softwareupgrade ./cmd/cisco-vk`,
`python3 -m py_compile scripts/iosxe-gnoi-lab-cycle.py`, and `git diff
--check`. The earlier full race suite, Helm lint, render and MkDocs results
remain evidence for their tested source states. Harness edits after those
checks and the dirty build must not inherit full candidate qualification.
At the historical physical checkpoint, `make test-envtest` stopped because
`setup-envtest` was absent. The later local run downloaded the pinned tool and
1.35.0 assets and completed both packages; see the current review above.
Re-run the full E00-G matrix for the eventual deployment candidate.

### Follow-up drain qualification: settled worker images (1 Oct 2026)

The next physical runs were deliberately kept separate from the earlier
`fix1` result so that a provider fix could not inherit an unrelated pass. They
exposed and then narrowed the delayed Virtual Kubelet callback boundary:

| Candidate/run | Physical activity | Evidence and interpretation |
| --- | --- | --- |
| `665a7954-settled3` | `.101` upgrade with two PDB-protected workloads | Platform and gNOI gates passed. A second callback arrived after the manager session purpose had moved to `SoftwareMutation`; the old worker rejected it as a different session incarnation. This was the trigger for accepting the exact software-mutation completion purpose. |
| `665a7954-settled4` | `.100` downgrade from 17.18.03 to 17.18.02 | Transfer, reload, OS.Verify, health and recovery passed. The run was intentionally rejected by the original harness because the base Virtual Kubelet logged its delayed API cleanup marker before the exact released-completion callback. The device remained healthy; this was an evidence-classification gap, not a device mutation failure. Evidence is retained in `/tmp/cvk-settled4-live-downgrade` and `/tmp/cvk-settled4-live-downgrade.tar.gz` on the lab host. |
| `665a7954-settled5` (`sha256:3970030086b07fb6db2f582ec83358cae2548555ae39f25cca68f31eb2635efd`) | `.101` downgrade from 17.18.03 to 17.18.02, with both workloads relocated to `.100` | Exact SFTP source/digest/Secret, approval hash, 1,247,897,709-byte transfer, reload activation, OS.Verify `17.18.02.0.4112.1766116039`, secure/provisioned gNOI health, Ready node, PDB reopening and settled maintenance all passed. The original process stopped only while collecting logs because it treated every `Force deleting pod in running state` line as unsafe. Offline revalidation with the corrected harness accepted each marker only when the same namespace/name/UID also had CVK's device-clean acknowledgement and `ProviderDeleteSuccess`. |

The marker is emitted by Virtual Kubelet's delayed Kubernetes API cleanup queue
after the provider delete has already returned; it does not identify which
side performed the device cleanup. The harness now rejects a marker that
lacks preceding exact correlated records, and accepts its log classification when
both records precede the marker in the single-worker log with exact namespace,
name and UID matches. This is log correlation, not an independent durable
device or service proof. The real `.101` log contains both records for each
drained UID (for example `e717875d-14f8-4355-bbfd-c4fded105336` and
`560bc776-9e4b-47f9-b908-fce5e1018594`). The focused harness tests cover the
missing-event, foreign-namespace/UID, prefix-collision and late-proof cases.
The original stopped report remains unchanged; the patched classifier has
only been run offline, not through a fresh physical cycle.

The `.101` report and all JSON manifests/logs are archived at
`/tmp/cvk-settled5-lab101-downgrade.tar.gz` locally and on Ubuntu16 under
`/tmp/cvk-settled5-lab101-downgrade.tar.gz`. Sanitized copies and original/source
hashes now live in the [versioned evidence bundle](evidence/topology-2026-10-01/README.md).
The stopped collector saved the app-worker log but not the manager/network
logs for this run; older-run logs cannot fill that gap. Final observations were:

- all three physical CVK workers on `cvk-tas-extentions:665a7954-settled5`;
- `.101` running 17.18.02 and accepted by gNOI Verify/health;
- both workload replicas Ready on `cat9k-live` (`.100`), with PDB
  `allowedDisruptions=1`;
- `.101` maintenance session `Settled` and no CVK maintenance taint; complete
  ledger/Lease settlement was not independently archived for this follow-up;
- a transient `NodeProjectionFailed` resource-version conflict was observed on
  `cat9k-live` during final read-only inspection; the normal reconciler retried
  and converged all three Nodes to `Ready`, no taints, `TopologyReady=True`,
  `TopologyConflict=False` (reported follow-up inspection, not a continuously
  archived all-node stability test);
- no forwarding, routing-adjacency or application endpoint probe. Those remain
  explicit E02-B/E07 service-acceptance gaps.

This follow-up supplies partial drain/device-clean/session settlement
evidence for the tested Catalyst cohort; it does not close E07 or qualify the
whole roadmap. The app worker still fail-closes routine update callbacks
while a disruptive session is active, and the remaining service/traffic,
independent staged activation, fault-injection, real-admission and envtest
gates must still be executed.

### Evidence and final checklist

Use a unique `/tmp/cvk-topology-<run-id>/` directory, then copy sanitized
evidence to the project's approved durable artifact location. This checkpoint
uses `docs/evidence/topology-2026-10-01/` for sanitized text; select an approved
external archive for future large recordings. `/tmp` alone is not a release archive. A versioned index must
link each test ID to its artifacts and their checksums. Large images, private
keys, credentials and raw Secret dumps are excluded.

Capture source/deployment commits and digests; tool/server versions; hardware
and image hashes; manifests before apply; identities/approval hashes;
`kubectl get`, `describe`, Events and manager/worker logs; relevant device
terminal output and available `terminal monitor` messages; install history;
service/forwarding probes with timestamps and declared outage tolerance;
transfer measurements; injected fault boundaries; and final Nodes, Pods,
PDBs, device inventory, receipts, Leases and ledger state. Logs alone do not
prove a mutation did not occur; correlate device and Kubernetes evidence.

To close a package, fill in:

| Field | Required content |
| --- | --- |
| Package/status | E00–E13 ID and explicit status |
| Implementation | Commit/PR, generated artifacts and deployed image digest |
| Automated tests | Test IDs/commands, exact outcomes, skips and CI run |
| Physical tests | Device/release/cohort, actual topology, test IDs and result |
| Evidence | Durable sanitized artifact link and checksums |
| Limitations | Unsupported cases, unresolved defects and conditional decisions |
| Recovery | Final healthy state or retained unresolved-operation guards |

- [ ] All required implementation packages are delivered; no missing feature
  has been relabelled as completed baseline functionality.
- [ ] Each acceptance row has positive/negative results appropriate to its
  claim; unavailable hardware remains a named blocker.
- [ ] API compatibility, real admission/RBAC, race/fault, security, generated
  parity, baseline Kubernetes and optional-version lanes pass as applicable.
- [ ] Core independent preparation/approval/activation succeeds on physical
  hardware through upgrade and downgrade with service probes.
- [ ] Examples, CLI explanations, architecture/security guides, lifecycle
  runbook, chart documentation and release qualification match the final code.
- [ ] The roadmap status table references the actual tested commits/evidence;
  conditional exclusions and blocked wider capabilities are explicit.

Until these gates are satisfied, report the achieved subset and remaining
work. Do not describe the whole roadmap as complete or gap-free.
