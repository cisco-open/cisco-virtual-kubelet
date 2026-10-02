# Topology awareness: remaining implementation roadmap

Status: **incomplete; implemented and reviewed through `6fab26f5` plus the
read-only graph diagnostic increment**, 2 October 2026.
Start with the [November handoff](topology-november-handoff.md) and
[versioned evidence](evidence/topology-2026-10-01/README.md). The checkpoint
preserves runtime/harness changes after `665a7954`; it is not a release candidate.
Working branch:
`pr/johalley/tas-extentions`. Baseline: `15c14d7d`, after PRs
#190, #191, #192, #193, and #194 merged. Implementation and qualification
gaps remain in T0–T10, including correctness issues in the bounded observation
slice. The existing combined lifecycle does not meet the independent
preparation/activation completion criterion.

The [execution plan](topology-roadmap-execution.md) is the actionable companion
to this design: it identifies ordered work packages, code ownership, tests,
physical prerequisites, evidence and completion gates for every outstanding
area. Its work-package IDs must be used when reporting progress. Updating a
status description does not implement or qualify the corresponding feature.

The trustworthy-network-evidence, claim-time-authority, administrator
disruption-protection, continuous recovery/soak, overlapping-risk-group and
worker byte-pacing increments are now implemented. Physical upgrade and
downgrade on `.103` exercised the 25 MB/s policy and exact native recovery of
lost IOS XE Install responses without replay. The remaining E03 checkpoint is
the independent forwarding/service-path matrix, while preparation must be
qualified independently before a separately approved activation step on a
capable platform. Operators should be able to prepare
an image in advance, understand which devices are safe to interrupt, and activate only
within an approved window while validating recovery. Broader workload
relocation, cache optimization, and additional platforms follow their own
qualification gates.

Start with the execution plan's [**C0–C9 queue**](topology-roadmap-execution.md#concrete-completion-queue-c0c9).
Restart-safe sequence allocation, bounded diagnostics, collection-interval
validation, manager-owned acceptance, rate provenance and a genuine
bound-token admission suite have advanced. Do not reimplement those fixes.
Controlled loaded-path and reverse mixed-version qualification remain open.
The evidence-bound worker claim and continuous recovery/soak checks are
complete; broader execution-time policy and physical service-path acceptance
remain open.
First preserve and reconcile `.103`'s uncertain NoReboot activation, fix new
operation binding ordering and add full-path planning regressions. The
existing nil-status test checks only a helper, not the failing freeze path.
Envtest CRD passes remain distinct from Helm admission-policy tests.

At the 1 October 17:36 UTC read-only lab check, the manager ran `11ae6704`,
all three C9K Nodes were Ready and reported `17.18.2`, but `.103` still held
its maintenance taint and mutation Lease after `ActivationOutcomeUnknown`.
Do not use Node readiness to clear that fence. The successful Install followed
by a timed-out Activate(NoReboot) did **not** test an independent Install-only
hold and does not establish whether the cohort supports durable preparation.
See C0/C4 for recovery and the correct qualification sequence.

Follow-up: clean candidate `3212f777` completed C0's exact-identity,
observation-only recovery and C1's binding/freeze increment. Helm revision 119
converged the C9K workers; `.103` passed its full health soak and released the
maintenance taint and Lease through normal reconciliation. Continue with C2;
do not interpret this closure as an Install-only, service-continuity or full
roadmap pass.

The first C2 trust-boundary increment is complete at `90bc690c`: workers write
only raw bounded network samples, the manager owns a separately admission-
protected accepted copy, and rollout freshness begins at original collection
start. Kubernetes 1.35 bound-token tests and read-only CLI comparison on all
three physical C9Ks passed, including manager restart. C2 remains open for
controlled loaded directional-rate accuracy, redundant-supervisor capability
and reverse mixed-version/rollback cases. Candidate `95077ba7` adds raw rate
provenance, independent manager recomputation and publisher concurrency/lost-
response coverage, and corrects a CRD integer-bound issue found by physical
execution. Candidate `9c48a98c` adds an evidence-bound, expiring manager grant,
monotonic grant renewal and an uncached worker recheck immediately before each
new mutation claim; see the
[C3 qualification record](evidence/topology-2026-10-01/c3-claim-time-network-authority.md).
Candidate `617b1cfc` adds bounded administrator-owned critical-service and
singleton-path prohibitions at planning and pre-execution revalidation. Its
[qualification record](evidence/topology-2026-10-01/c3-administrator-disruption-protections.md)
also records the retained-value Helm compatibility defect found and repaired
before physical convergence. Candidate `ae4f3a7b` extends the opted-in network
gate through post-operation recovery and every continuous-soak pass, requiring
a manager-accepted sample collected after the device operation; see the
[recovery/soak qualification](evidence/topology-2026-10-01/c3-continuous-network-recovery-soak.md).
Candidate `7417b09b` adds administrator-declared overlapping risk groups,
freezes exact physical membership and charges unhealthy non-target peers plus
all campaigns in the existing single-ledger CAS; see the
[risk-group qualification](evidence/topology-2026-10-01/c3-overlapping-risk-groups.md).
Candidate `c8ec293c` derives a bounded aggregate byte rate from every matching
risk group and paces the worker-to-device gNOI stream. Candidates `0f4a013a`
and `2f27f302` add fail-closed IOS XE native observation of an interrupted
Install and forward that optional capability through the shared lifecycle
adapter. The [physical pacing record](evidence/topology-2026-10-01/c3-physical-pacing/README.md)
captures the exact-candidate 17.18.03 → 17.18.02 → 17.18.03 run, including two
lost terminal Install responses resolved without replay.

The original `/tmp/cvk-topology-awareness-options.md` was unavailable during
this review. This plan reconstructs the remaining scope from the checked-in
[topology guide](topology-awareness.md), the
[October release scope](releases/v2026.10.0.md), and the implementation. It
supersedes neither the current runtime contract nor release qualification.

### Execution status for this branch

The foundation is implemented. The Ubuntu16 physical regression used image
`cvk-tas-extentions:665a7954-fix1` for six single-target campaigns, followed by
a settled `.101` downgrade from 17.18.03 to 17.18.02 using
`665a7954-settled5`. The follow-up completed transfer, activation, gNOI Verify,
health/recovery soak, PDB-aware replacement and settled maintenance. Its
original evidence collector stopped on Virtual Kubelet's generic delayed API
cleanup marker. Offline classification now requires preceding exact
namespace/name/UID-correlated CVK device-clean and `ProviderDeleteSuccess`
records. The stopped process was not rerun with this classifier.

No E00–E13 package yet satisfies every exit gate. Separate staging/activation,
application/forwarding continuity and full drain qualification remain open.
The corrected harness now validates explicit source URLs/digests, waits for
manager-owned operation binding, resolves rollout targets for in-flight taint
observation, captures manager/network/app logs and reports only the executed
direction. The settled follow-up still records `Force deleting pod in running
state` during delayed API cleanup, but each marker is correlated to the same
Pod UID's clean-delete acknowledgement and provider success; this is not
evidence of forced device teardown. E07 service/traffic, hard-placement and
restart/fault gates remain open. Transient new-object admission denials also
require the binding-order regression already added to the next test gate.

The historical six-campaign physical image embeds `665a7954`, but was built with uncommitted changes;
the embedded base SHA does not identify all tested source. This checkpoint
also contains later harness/documentation edits, not deployed runtime proof.
The 1 October review again found no branch PR/CI runs. Clean runtime
follow-ups through `11ae6704` exist, but have not passed full acceptance.
Build a clean, reviewable final candidate and verify its CI before
final acceptance. The [execution plan](topology-roadmap-execution.md) records
the evidence corrections, immediate repairs and test-by-test exit gates and
remains the completion ledger.

| Slice | Reviewed status through the current checkpoint (physical evidence retains its original revision) | Required execution |
| --- | --- | --- |
| T0 | Six-campaign regression plus settled `.101` follow-up; exact evidence archived and harness correlation repaired; clean `9e578131` candidate deployed to Ubuntu16 with all three physical target observations checked | E00: finish direct device CLI/secure OS.Verify, complete log-plane/service/path baselines and durable capability indexing. |
| T1 | Manager-accepted Pod-bound evidence, restart-safe publisher, interval/schema safety, directional-rate provenance/recomputation, concurrency/lost-response tests and real bound-token admission suite passed; physical k3s schema compatibility corrected | C2 / E01–E02: controlled loaded-path accuracy with an independent source, redundant-hardware capability, reverse mixed-version/rollback and candidate CI. |
| T2 | Network checks at plan freeze and manager admission; evidence-bound expiring grant and monotonic renewal; uncached exact-sample worker recheck before each new mutation claim; bounded administrator critical-service/singleton-path prohibitions; overlapping risk groups with exact physical membership and cross-campaign CAS accounting; administrator aggregate-rate policy with worker pacing; post-operation accepted-evidence recovery and continuous-soak enforcement; real-API negative coverage; physical 25 MB/s upgrade/downgrade pacing and secure-gNOI validation | C3 / E03: independent loaded/headroom measurement and redundant/singleton/critical/congested service-path tests. |
| T3 | Combined lifecycle exists; `3212f777` reconciled `.103`'s lost NoReboot outcome without replay; `2f27f302` proves exact IOS XE native Install completion after a lost gNOI response without replay; independent prepare contract remains absent | C4/C5 / E04–E05: true Install-only qualification, then durable receipts/ownership. |
| T4 | Separate activation authorization/reservations absent | E06: implement append-only activation approval, phase windows, atomic budget transitions and recovery. |
| T5 | `.101` leaf records ordered device-clean completion and settled maintenance; replacement Pods ready; delayed API cleanup markers correlated offline | E07: close missing log-plane evidence, prove service/traffic continuity, hard-placement blocking, restart/cancel recovery and broader workload eligibility; this is partial evidence, not full drain qualification. |
| T6 | Synthetic co-location/conflict evidence exists; full scheduler and physical group lifecycle absent | E08: raw group-field recognition before expanding drain, remaining scheduler scenarios, native controller recreation, physical service and group-aware drain tests. |
| T7 | Existing ephemeral cache only | E09: measure both transfer segments; implement durable prefetch/PVC cache only when its decision gate passes, then qualify failures. |
| T8 | Bounded helper plus a read-only `kubectl ciscovk topology graph` consumer; accepted manager evidence, authenticated collection freshness, duplicate identities and three-way conflicts fail closed. CDP remote-port identity now survives collection, accepted status and CLI graph construction. A physical read-only run rendered all four devices and returned incomplete because CDP names could not be authenticated as bound serial identities and the NX-OS device was unbound; the remote-port schema increment still needs exact-candidate deployment evidence | E10: add administrator-declared link input and trusted peer-identity mapping/provenance, finish diagnostic-boundary fixtures, then qualify a controlled physical link change/restore. Do not infer path health or grant disruption authority from the graph. |
| T9 | Second-platform qualification absent | E11: probe a suitable platform, qualify its lifecycle and record the public-API decision. Unsupported hardware leaves this gate open. |
| T10 | Legacy handoff/convergence hardened; broader ownership and scale remain | E12: measure the supported envelope and test controlled single-cluster/offline handoff including staged/uncertain operations. Three switches do not prove fleet scale. |

Previous status text overstated completion by equating baseline functionality
with later roadmap deliverables. Existing test passes and physical upgrade
results remain useful evidence for the scenarios actually exercised; they do
not establish the missing contracts or the full physical acceptance matrix.
E13 supplies the final integrated acceptance gate after implementation.

Before another disruptive qualification, follow C0/C1 and E00-F/G/H: retain
the uncertain leaf/fences, truthful direction-specific reports, exact
target/source identities, both worker planes and manager logs, service/claim
timelines and reproducible tooling. Test binding ordering and the full
network-enabled freeze path, then complete accepted-observation and
claim-time enforcement. These trust-boundary increments are now complete;
continue with the remaining E03 independent headroom and failed-path/service
scenarios. Qualify
the actual Install-only boundary alongside E03 policy, then implement E05/E06. Broader drain testing still requires
device-clean, replacement and service proof. Another successful combined
reload alone cannot close these contracts.

Full completion also requires lab inputs beyond three C9Ks: measured forwarding
paths/load, portable supported applications and independent probes, capable
staging hardware/releases, an appropriate second platform/image pair, and
durable evidence storage. Supervisor claims need relevant redundant hardware.
Resolve these during E00/E04/E11, not at final acceptance. Missing inputs keep
their positive tests blocked; they do not prevent independent implementation
and fixture work or justify silently narrowing the roadmap.

## 1. Baseline and remaining scope

| Area | Implemented at this baseline | Remaining work |
| --- | --- | --- |
| Identity and scheduling, Phases 0–1 | Protected declared topology, physical identity, Node binding, capacity, affinity/spread, maintenance fencing | Operational diagnostics, broader scale evidence, optional TAS lifecycle qualification |
| Campaigns, Phase 2 | Immutable plans and approval hashes, canaries, soak, windows, policy epochs, shared domain reservations, worker mutation claims; opt-in bounded network evidence gate | Critical-service policy; phase-specific authorization and durable stage/activation reservations |
| Distribution, Phase 3A | Deterministic selection of existing endpoints; frozen source, digest and Secret UID | Measured path locality, durable prefetch, optional cache and cache-loss recovery |
| Drain, initial Phase 4 | Opt-in bounded ReplicaSet/Deployment drain, Eviction/PDB checks, device teardown and replacement evidence | Physical qualification, safe hard-placement support, individually justified workload types |
| Lifecycle, remaining Phase 4 | Combined install/activate with durable request markers and limited rollback; rollout `NoReboot` preparation mapping | Durable staged receipt, later activation approval, restart-safe reservation handoff |
| Observed topology | Bounded IOS-XE CDP/OSPF/interface status, source/interface identities, diagnostic collection metadata and an unintegrated graph helper | Manager acceptance/replay checks, complete routing context, trustworthy rate coverage, graph correctness and operator-visible drift diagnostics |
| Native TAS | Separate Kubernetes 1.37 scheduler conformance with fixture Nodes | Real app-hosting and group lifecycle tests; version-specific opt-in examples |
| Other drivers, Phase 5 | Reusable coordination and optional lifecycle interfaces | Second qualified software-lifecycle driver before a public generic API |
| Larger deployments | Bounded single-ledger, single-control-plane model | Scale measurements and explicit ownership transfer; active multi-cluster control remains conditional |

Maintenance windows already exist and gate new mutation claims. Existing
claims also distinguish install, staging, activation and rollback. Neither
feature yet supplies an independently durable stage/approve/activate flow.
The current ledger conservatively charges both transfer and unavailable
budgets throughout a leaf.

The current drain guide explicitly limits its qualification claim to unit and
disposable API-server coverage. Earlier lab activity must be tied to a commit,
feature configuration and retained evidence before it can qualify this exact
drain implementation. PR CI success alone is insufficient.

## 2. Architecture decisions to preserve

1. The default Kubernetes scheduler places application Pods. CVK projects
   approved device attributes onto virtual Nodes. CVK's manager admits
   software operations and owns fleet policy; per-device workers execute
   device operations. The manager continues to open no device sessions.
2. Use Kubernetes APIs and the existing CVK components. No alternate
   scheduler, scheduler plugin, Kueue, Volcano, external topology controller,
   workflow engine, or required metrics database. Test tooling can remain
   external to the deployed product. Optional existing artifact storage is an
   operator prerequisite, not a newly required platform component.
3. Preserve the two functional worker ServiceAccounts per managed namespace:
   app hosting and network management, each configured with its existing
   disabled/read-only/read-write mode. Do not introduce per-node accounts.
   New observations use the network plane's read capability; device mutations
   still require its write capability. Keep manager permissions distinct.
4. Preserve physical identity, exact Node/CR/Secret UID binding, immutable
   approved intent, policy epochs, append-only claims, CAS reservations and
   quarantine after uncertain outcomes. Expired Leases are not evidence that
   an accepted device operation stopped.
5. Administrator declarations remain policy authority. Device observations
   can block execution or report drift; they cannot grant permission, change
   protected topology, or increase a budget.
6. New behavior is disabled by default. Omitted fields preserve old behavior
   and existing canonical hashes. Adding a feature does not implicitly
   change a previously approved campaign.
7. Keep the Kubernetes 1.35 managed-topology baseline. Native Workload/PodGroup
   support remains separately discovered, version-pinned and qualified.
   Do not enable cluster feature gates automatically.

Upstream Virtual Kubelet provides the provider integration boundary; it does
not supply CVK's network-upgrade policy. Reuse that boundary instead of
introducing campaign decisions into upstream Pod lifecycle callbacks. See
the [Virtual Kubelet provider documentation](https://virtual-kubelet.io/docs/providers/).

### How scheduling relates to device upgrades

| Native Kubernetes mechanism | Container placement | CVK software-upgrade responsibility |
| --- | --- | --- |
| Node labels, affinity and topology spread | Select eligible locations and distribute replicas | Use the same protected inventory plus operational risk domains when admitting targets |
| Node capacity and allocatable | Limit application resource placement | Do not interpret CPU/memory headroom as spare forwarding bandwidth |
| Maintenance taint and cordon | Prevent new placement on a draining device | Fence before disruption and retain guards through recovery |
| Eviction and PDB | Bound voluntary application disruption | Wait for actual teardown and qualified replacement readiness; also check network health |
| Native PodGroup/TAS | Group placement within selected topology domains | Account for group replacement constraints; no direct authorization of OS activation |
| CRD status, Leases and CAS updates | Durable API state and coordination primitives | Record plan, phase authority, domain reservations and uncertain device outcomes |

TAS is primarily a placement feature, including group co-location. Use topology
spread or anti-affinity for application replicas that need failure separation.
Neither placement choice proves that a router can reboot safely. Kubernetes
documents TAS as an opt-in feature; qualify the API and feature gates on each
supported version rather than inferring stability from `v1beta1` object names.
See [native TAS](https://kubernetes.io/docs/concepts/workloads/workload-api/topology-aware-scheduling/).

## 3. Delivery sequence and dependencies

These are proposed PR slices, not existing PR numbers or a promise to ship all
items in one release. Keep each change independently reviewable and usable.

| Slice | Deliverable | Dependency and completion gate |
| --- | --- | --- |
| T0 | Capability and qualification baseline; reproducible operator evidence | First; inventory the lab and prove current behavior at a pinned commit |
| T1 | Typed, bounded network observations and rollout diagnostics | T0; stale, partial and unsupported data demonstrably block opted-in checks |
| T2 | Critical-service policy, network health gates and richer risk groups | T1; redundant-path, singleton and congested-path scenarios pass |
| T3 | Durable stage/activation contract and conservative execution | T0; evidence must prove independently observable staging and activation |
| T4 | Phase-specific reservations and later activation approval | T2 + T3; restart, race and window-boundary tests pass |
| T5 | Workload drain qualification and incremental placement support | T0 + T2; physical relocation and failure-recovery evidence |
| T6 | Real native TAS integration and optional hierarchy qualification | T0; group-aware drain additionally depends on T5 |
| T7 | Path measurement and optional durable prefetch/cache | Measurement can start after T0; implementation needs T4 and demonstrated benefit |
| T8 | Observed graph diagnostics and policy drift reporting | T1 + T2; discovery never rewrites authority |
| T9 | Second-platform software lifecycle and public API decision | T3 + T4 contracts; platform-specific hardware evidence |
| T10 | Scale qualification and controlled ownership handoff | Core contracts stable; active multi-cluster design needs a separate fencing proof |

The initial T1 slice has landed on the branch. Next, complete E00 evidence
and correct E01/E02 trust, identity and missing-rate handling. Qualify E04's
independent device boundary alongside E03 policy implementation, then build
E05/E06 durable staging and separate activation. Add the E08-A group guard
before E07's broader drain eligibility. E10 helper repairs and E09 measurement
can advance independently; physical TAS ownership transfer depends on E12.
The execution plan gives the ordered deliverables and exact tests. Keep cache
selection conditional on measurement and the second-platform API decision
dependent on successful hardware qualification.

## 4. T0 — establish capability and qualification evidence

Record platform, exact releases, image identities, supervisor/stack layout,
gNOI OS and certificate behavior, storage, application packaging, and current
CVK/Kubernetes/chart versions. Cover upgrade and downgrade independently.

Use the previously assigned physical lab targets `198.51.100.100`,
`198.51.100.101`, and `198.51.100.103`. Before execution, confirm current
ownership, Ubuntu host and kube-context, CI exclusions, connectivity and
available images; historic assignments are not live inventory. This planning
change performs no device operations. Use signed/supported application images
on C9Ks without the required SSD-120/USB-flash storage; failure to launch an
unsupported unsigned package is an expected negative case.

Produce a capability matrix that separately records observation, transfer,
durable stage, activation, rollback, inventory completeness, and workload
drain support. A configured feature flag is not a capability probe. Initial
evidence may remain in versioned test fixtures and documentation; introduce a
public capability API only when a runtime consumer needs it.

Complete the required release checks for the merged baseline separately from
this roadmap. Do not use these new features to widen October release claims.

## 5. T1–T2 — network-aware admission

### Observation contract

Reuse `internal/drivers/iosxe/topology.go` and narrow optional driver interfaces.
The newer OSPF path now traverses adjacencies, but full coverage and VRF/process
identity still need qualification. The current normalized neighbor carries
area, not VRF. A successful call or empty list must not be interpreted as
proof of complete source coverage or healthy required paths.

Add bounded typed observations for selected interfaces, adjacency state,
traffic samples and qualified supervisor/stack health. Worker observations
carry device identity, worker revision, sample sequence, collection interval,
completeness and errors. Keep a small summary under `CiscoDevice.status` and
reuse the existing status/admission ownership rules. Add a separate evidence
resource only if measured size or ownership constraints require it.

The target contract requires the manager to record acceptance and apply
freshness bounds, including the oldest contributing sample and clock-skew
checks. Worker-supplied timestamps cannot extend evidence indefinitely.
Authenticated request identity can establish the producer, not the truth of
a compromised device or worker. Preserve the current shared-account trust
boundary in the threat model.

Observe through the network worker using the existing transports. OTEL remains
an optional export of evidence, never a required authorization dependency.
Counter reset, interface-speed changes, missing samples and unsupported YANG
models produce `Unknown`, not zero utilization or a healthy empty topology.
The observation bridge now carries direction-specific rate presence/validity;
missing or overflowing IOS-XE rate leaves remain Unknown while measured zero
is valid. E02 still needs YANG representation fixtures and an independent
idle/load qualification before capacity policy relies on the percentage.
Collection timestamps, a process-local sequence and worker Pod UID now exist.
The manager owns an accepted copy, validates exact device/revision/Pod identity
and monotonic sequence, and preserves original collection-start freshness.
An evidence digest, producer identity, sequence and absolute expiry are bound
to the manager grant; the worker performs an uncached exact-tuple check before
each new mutation claim. A granted target may renew only to a complete newer
tuple with a strictly later expiry. Recovery/soak policy evaluation, reverse
mixed-version qualification and controlled loaded-rate validation remain open.
Pod UID does not distinguish a container restart inside that Pod, so sequence
and collection provenance remain part of the accepted identity. Empty expected
identity does not bypass opted-in checks.

### Policy and admission

Extend the administrator policy and immutable campaign with typed, bounded
checks. Initial checks should cover expected interface/adjacency sets, alternate
path health, utilization/headroom thresholds, observation age and hold time.
No arbitrary scripts, URLs, expressions with device access, or CLI snippets.
Campaigns may tighten administrator restrictions only.

Add an explicit administrator prohibition on disruptive activation for
critical services or a singleton path. Permit preparation separately only
where T3 proves it non-disruptive. An operator requiring literally no downtime
must either retain this prohibition or separately qualify an actual hitless
platform procedure; `ISSU`, `NoReboot`, PDBs and redundant labels are not proof.

The existing domain model assigns one value per label key. It can represent a
site and a redundancy pair, but cannot conveniently express one device
belonging to multiple groups under the same risk dimension. Add bounded,
administrator-declared overlapping groups to the existing policy, resolving
membership to exact physical identities. Freeze relevant group definitions and
membership in the approved plan. Count unhealthy non-target members and other
campaigns; all applicable ceilings must pass in one CAS admission decision.
Do not replace the existing label budgets for simple fleets.

Use the existing maintenance window initially. Add separate preparation and
activation windows with T4. Start with explicit UTC intervals and a bounded
latest-start allowance for disruption/recovery; recurring calendars and
timezone handling require demonstrated demand. Closing a window prevents new
claims; it cannot revoke a device RPC already accepted.

For bandwidth-sensitive operations, evaluate the actual distribution and
surviving forwarding paths. Thresholds and concurrency alone do not guarantee
available bandwidth. Require a declared minimum headroom model, current sample
coverage and a bounded aggregate transfer rate where the path is known. Rate
limits constrain CVK traffic only. If path or capacity is unknown, prohibit
the opted-in activity or require an explicit maintenance period. Do not claim
arbitrary traffic-engineering simulation.

The exact accepted-evidence check is now performed immediately before each new
claim. Extend the broader administrator/group policy through recovery and soak.
If a gate
fails after an irreversible operation starts, stop new admissions and observe
the accepted operation to settlement; a policy change does not justify replay
or automatic fleet rollback. Failure-domain budgets must cover simultaneous
campaigns, not just the current target list.

The manager grant now binds the accepted evidence digest, producer
revision/Pod UID, sequence and absolute expiry; the worker rejects expired or
mismatched authority before claiming a mutation. The existing policy epoch and
control revision remain independently bound by admission. A network failure can still occur after
the final check; reserve enough redundancy for the declared failure model and
describe that residual risk rather than promising continuous availability.

Suggested operator reasons include `EvidenceStale`, `AlternatePathUnavailable`,
`CriticalServiceProtected`, `TransferHeadroomInsufficient` and
`ActivationWindowClosed`. These are proposed names, not current API fields.
Report the blocking check, relevant domain, observation time and recovery
action in status and Events. Keep metric labels bounded.

Acceptance requires negative cases for an unavailable peer outside the target
set, incomplete routing observations, clock skew, overlapping risk groups,
policy changes during admission, and counter resets. A live forwarding probe
is required to qualify an end-to-end service claim; control-plane adjacency
alone supports only an adjacency-health claim.

## 6. T3–T4 — durable preparation and controlled activation

### Qualify the boundary before designing around it

Distinguish three operations: fetching image bytes to the worker/cache,
installing or registering them on the device, and activating the device image.
Only the last is necessarily a reboot, but either device mutation may affect
service on a particular platform. Follow the
[gNOI OS contract](https://raw.githubusercontent.com/openconfig/gnoi/main/os/os.proto)
and verify IOS-XE implementation behavior on every supported cohort.

Prove that staged state can be identified after worker restart, that the exact
activation identity is observable, and that external image removal/change is
detectable. Include primary/standby combinations. If reliable stage identity
or non-disruption evidence is unavailable, retain the combined conservative
path for that cohort.

### API and state machine

Prefer extending the existing IOS-XE leaf and campaign with a versioned managed
protocol, rather than adding another executor. Keep existing combined mode as
the default. New field names and enum additions must be reviewed against CEL
transition and compatibility rules before implementation.

The proposed logical flow is:

```text
Plan -> approve preparation -> prepare -> Staged -> approve activation
     -> revalidate -> drain/fence -> activate -> verify -> network soak -> settle
```

Persist a staged receipt bound to the device/Node identities, image digest,
exact platform activation version, supervisor inventory, source and trust
identities, policy/protocol versions and recorded operation claims. Re-observe
device inventory before activation. A receipt for downloaded bytes is not an
installed-image receipt, and a version string alone does not prove a digest.
Document any platform limitation in verifying installed content identity.

Use a separate append-only activation approval, bound to the frozen plan,
staged receipt hash and activation window. Native admission binds the approver
to the authenticated user and a distinct permission. Preparing an image must
not authorize reboot. Missing, expired or stale authorization blocks activation;
approval is not silently rewritten after drift.

Keep the hardware mutation Lease during any device mutation and uncertain
outcome. A conclusively staged, idle device may release execution reservations;
retain its staged ownership record so another campaign cannot replace the same
intent unnoticed. Reacquire authority and revalidate before activation.
Conflicting device activity while idle invalidates or requires revalidation
of the receipt according to explicitly tested rules.

### Reservation semantics

| Operation | Transfer budget | Disruption budget | Required behavior |
| --- | --- | --- | --- |
| Source prefetch to worker/cache | Charge the known transfer domain | None if no device mutation occurs | Verify bytes and bound bandwidth/storage |
| Qualified non-disruptive device stage | Charge while transferring | None only with cohort evidence | Serialize device mutation; persist its outcome |
| Unqualified device preparation | Charge conservatively | Charge conservatively | Preserve current restrictions |
| Idle, conclusively staged | Release active transfer use | Release only on conclusive healthy state | Retain immutable staged receipt and ownership |
| Drain/activation/recovery | Charge only if transfer is still active | Hold through device and application recovery/soak | Atomic accounting before the first side effect |
| Uncertain operation | Retain applicable reservations | Never release on timeout alone | Observe or require audited reconciliation |

Use one CAS transition to change budget classes. Do not release the preparation
record and then separately acquire activation capacity, leaving an unaccounted
interval. Bound staged record retention and define safe garbage collection:
deleting metadata must not delete images or abandon an uncertain mutation.

Pause/cancel stops new claims and may leave a confirmed staged image installed.
Window expiry does not trigger image deletion. Recovery resumes observation,
not replay of `OS.Install` or `OS.Activate`. Preserve explicit downgrade campaigns
and the narrow existing rollback behavior; no fleet-wide rollback transaction.

Acceptance: restart at every claim/response/status boundary; two competing
campaigns; manager failover; stage eviction; external software change; worker
upgrade; trust rotation; approval drift; time-window edge; activation timeout;
standby mismatch; and partial rollback. Packet captures or device operation
history must show no duplicate mutation after an ambiguous response.

## 7. T5 — qualify and extend workload relocation

First qualify today's supported ReplicaSet/Deployment subset on physical
hardware. Record actual application reachability, not just Pod `Ready`, both
before relocation and on the replacement. Respect the current volume,
controller, namespace, placement and packaging restrictions.

Next consider hard node affinity/selectors and topology spread for that same
controller subset. Preserve the original placement intent. A manager-side
feasibility check is advisory because the Kubernetes scheduler can race with
other placements; do not claim it reserves destination capacity or implement a
second scheduler. PDB approval and observed qualified replacement readiness
remain execution gates. A replacement stuck Pending must block activation.

Do not automatically increase Deployment replicas or create duplicate apps:
that changes owner intent and can have application side effects. Operators can
provision spare replicas/capacity in advance. Support topology-safe surge only
through a separately specified owner-aware protocol if the workload requires it.

Broaden support by explicit capability, not by removing the current exclusions:

| Workload | Decision |
| --- | --- |
| ReplicaSet/Deployment, portable volumes | Qualify current behavior first, then hard placement |
| Native PodGroup members | Block group-aware drain until T6 proves recreation and placement semantics |
| StatefulSet/PVC | Conditional: device volume/runtime support, attachment and identity recovery must be proven first |
| Job/CronJob | Conditional: application retry/idempotence and owning controller semantics must be qualified |
| DaemonSet | A replacement normally follows the same Node; require explicit interruption semantics, not a relocation claim |
| Bare Pods/custom controllers | Continue blocking unless a specific owner/recovery contract is implemented |

Native [PDB and disruption semantics](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/)
bound voluntary Pod evictions; they do not guarantee network forwarding or
storage availability. Keep direct deletion, force deletion and PDB bypass out
of the supported flow. Preserve session/finalizer protection, exact device-clean
inventory and restart-safe cleanup.

## 8. T6 — optional native TAS with real CVK workloads

Retain the current separate 1.37 test lane, with pinned Kubernetes image,
feature gates and served schema discovery. Its current fixture Nodes verify
scheduler binding; extend evidence to supported applications on actual virtual
Nodes, checking startup, service behavior, recreation and group constraints.

The baseline Go dependencies use Kubernetes `v0.35.0`. Audit whether typed Pod
reads preserve the newer scheduling-group fields before using them for drain
eligibility or hashing. Use a narrowly scoped dynamic/unstructured reader when
needed, or qualify a client-library update against the baseline server. Do not
assume an unknown field is absent merely because an older Go type omitted it.
Until recognition is proven, reject unsupported group-managed drain through
version-aware native admission or a verified raw-object check. Include this
negative test before any physical TAS-plus-drain run.

Use native workload owners for recreation where qualified. The current example
contains standalone Pods, so it does not by itself demonstrate a durable
controller-backed lifecycle. CVK must not start owning Workload/PodGroup objects
as a side effect of Node publication.

Test a maintenance-tainted member, insufficient capacity in the selected site,
partial group failure, controller restart and recovery. Co-location can keep a
replacement in a domain without spare capacity; that must remain an explained
blocker. Gang scheduling does not provide transactional application startup or
permission to disrupt every member simultaneously.

Evaluate native hierarchical grouping only for a concrete application scenario,
such as keeping the application in one site with its workers grouped by rack.
Keep it a separate optional conformance lane until API discovery, controller
lifecycle and CVK workload support are all proven. It provides no automatic
router-path cost model and should not raise the baseline version for ordinary
rollouts.

## 9. T7 — distribution measurement, then optional caching

Measure origin-to-worker and worker-to-device bytes, throughput, elapsed time,
repeat downloads and WAN crossings. The worker streams gNOI data, so a source
near the switch is not automatically near the worker or on the useful path.
Record worker placement and both path segments; never label locality from
source DNS or device site alone.

Proceed with caching only when the measurements justify its storage and
security costs. Prefer per-worker durable prefetch to an explicitly supplied
PVC, using existing CVK binaries and accounts, before a shared cache service.
Worker placement can use native affinity; storage can use an existing PV/PVC.
Do not require CVK to install a CSI provisioner or third-party cache product.
If suitable storage is absent, preserve the current streaming behavior.

A PVC mount is not a multi-worker cache protocol. Respect RWO/RWOP placement,
owner changes, crash recovery and concurrent readers/writers. Shared cache
serving remains conditional on a separate bounded design and a demonstrated
benefit over operator-provided mirrors. It must not silently require another
privileged worker identity.

Use digest-addressed files, size limits, atomic publication after hash
verification, per-artifact synchronization, bounded capacity, explicit TTL/GC,
read-use protection, restart revalidation and byte-rate limits. Partial files,
corrupt content, full/lost volumes and permission changes invalidate readiness.
Cache loss may refetch the frozen origin under current authority; it cannot
change the endpoint or bypass a stage/activation approval. Do not put multi-GB
images in ConfigMaps or CRD status.

Retain verified source TLS/SSH identity, source purpose and Secret UID checks,
SSRF protection and credential isolation. Cross-namespace reuse requires
explicit authorization even when the digest matches. Current managed sources
reject private HTTPS; a cache must not work around that restriction. Any new
private endpoint/trust mode needs a separate policy and security review.

Acceptance: controlled WAN-path measurement, failed transfers, digest mismatch,
volume loss, namespace isolation, concurrent cache use, restart during publish,
GC during an active read, and no additional activation after cache recovery.

## 10. T8 — observed topology and drift diagnostics

Build a bounded observed graph from qualified neighbor/interface observations.
Resolve edges to declared physical identities with provenance, sample time and
confidence. Preserve unknown/unmanaged neighbors and asymmetric observations;
CDP names alone are not authenticated chassis identity. Model interface/VRF
context where needed rather than merging unrelated paths.

The bounded helper has freshness checks, source-aware structured JSON keys,
duplicate detection, input/output limits, reverse-port matching and
order-independent three-way conflict canonicalization. The read-only kubectl
plugin consumes only the manager-owned accepted sample, retains unbound
devices, and can fail CI/automation closed with `--require-complete`. It does
not yet accept declared-link policy or a manager-approved mapping from protocol
peer names to physical identities. The physical diagnostic therefore correctly
reports the current CDP-name/serial mismatch as unresolved instead of guessing.

Compare that graph with declared risk groups and expected adjacency sets. Show
missing peers, changed uplinks, new single points of failure and stale data.
Use the evidence to block opted-in policy or suggest a new plan for operator
review. Never rewrite protected labels or approve a new path automatically.

Do not infer end-to-end redundancy from neighbor links alone: overlays, routing
policy, LAGs, external dependencies and shared physical risks can invalidate
that inference. Graph-cost workload scheduling and an authoritative discovered
graph remain outside this design. They would require an independently justified
architecture beyond the native scheduler/declared-policy boundary.

## 11. T9–T10 — portability, scale and ownership

For the next platform, prefer NX-OS evaluation because CVK already has an NX-OS
driver, but first verify the actual hardware/release gNOI OS service and image
lifecycle. IOS XR follows its own runtime/transport qualification. An absent
service remains unsupported; do not silently substitute CLI execution.

Reuse `internal/devicecoordination`, `internal/topologyrollout`, source security,
maintenance and drain evidence only where their guarantees hold. Inventory,
version comparison, image compatibility, supervisor order, certificate workflows,
activation, rollback and full app inventory stay platform-owned. The existing
`ValidateTargetVersion` syntax is an IOS-XE-shaped constraint despite its shared
package; portability must audit it and related assumptions explicitly.

Only after a second driver passes the same failure suite should a separate ADR
decide whether to extract a public `SoftwareRollout` API. Preserve IOS-XE clients
and provide a tested migration; do not add empty NX-OS/IOS XR API fields today.

Benchmark bounded fleet snapshots and simultaneous campaigns at the current
configured limits. Measure reconcile time, API QPS, reservation contention,
status/ledger bytes and watch cardinality. The current default ledger limits
are 256 records and 256 KiB; saturation must remain explicit and fail closed.
Ship a measured supported envelope, then expand limits only with evidence.

Do not shard the ledger merely by site: global limits and overlapping risk
groups need atomic admission across shards. A scale redesign must retain that
property before replacing the single CAS authority.

Multiple CVK installations controlling one physical device remain prohibited.
For single-cluster handoff, drain active authority, prove device state, retain
identity/history, and explicitly transfer ownership. Cross-cluster Leases
cannot fence one another. Active multi-cluster failover is a separate research
gate requiring a common authority or device-enforced fencing across partitions;
without that proof, document controlled offline ownership transfer only.

## 12. API evolution, security and recovery requirements

- Make additive fields optional and preserve old default behavior and hashes.
  Test stored baseline objects against new schemas and controllers. If new
  phases/enums cannot be safely understood by old workers, bump the managed
  protocol and refuse new admissions during mixed-version operation.
- Keep changes in the current API version only if lossless semantics permit.
  Otherwise propose a separate version with a conversion/migration strategy
  compatible with the native-only constraint. Avoid requiring a conversion
  webhook by default; use explicit quiesced export/recreate migration if
  conversion cannot be represented safely.
- Define controller/worker/chart/CRD upgrade order, interrupted Helm recovery,
  retained cleanup RBAC, feature disablement, and downgrade restrictions for
  every slice. Never downgrade over active new-protocol claims.
- Keep new policy knobs bounded and typed, admission fail closed, approver
  authority distinct from planning, and Secret content out of plan hashes,
  status, Events and recordings. Test both functional read-only profiles.
- Do not broaden credentials for one capability into unrelated device flows.
  Preserve secure gNOI and certificate lifecycle behavior.
- Define crash behavior at every durable-state transition. Cleanup must remain
  possible after feature disablement without restoring mutation authority.
  Garbage collection must preserve unresolved operation evidence.

## 13. Code ownership and review boundaries

| Concern | Extend these existing locations |
| --- | --- |
| Campaign/leaf API and CEL | `api/ops/v1alpha1/iosxesoftwarerollout_types.go`, `iosxesoftwareupgrade_types.go`; generated CRDs and chart copies |
| Observation summaries | `api/v1alpha1`, `internal/drivers/registry.go`, `internal/drivers/iosxe/topology.go`, network-worker wiring |
| Policy, risk groups and budgets | `internal/topologyrollout/policy.go`, `ledger.go`, `store.go` |
| Manager admission and recovery | `internal/controller/iosxesoftwarerollout_controller.go`, `_execution.go`, `_drain.go`, managed topology/maintenance controllers |
| Device execution and sources | `internal/provider/softwareupgrade`, `internal/softwarelifecycle`, `internal/drivers/iosxe/softwarelifecycle` |
| Drain and cleanup evidence | `internal/workloaddrain`, `internal/provider/maintenance`, manager drain controllers |
| Native admission and RBAC | `charts/cisco-virtual-kubelet/templates/topology-*.yaml`, values and render/integration tests |
| Scheduling qualification | `charts/cisco-virtual-kubelet/tests/topology-kind-test.sh`, `native-tas-kind-test.sh`, examples and separate CI lanes |
| Operator experience | Existing `kubectl ciscovk` commands, CRD printer columns, status/Events, topology and lifecycle runbooks |

Create new packages/resources only when the feature has a distinct ownership,
persistence or protocol boundary. Keep the pure decision code unit-testable;
do not build a generic policy framework before multiple concrete consumers exist.

## 14. End-to-end acceptance and evidence

Every PR supplies focused unit and API-server/admission tests appropriate to
its change, generated-artifact parity, chart checks, security checks and strict
documentation validation. Run race and fault-injection tests for coordination
changes. Retain the Kubernetes 1.35 baseline alongside optional TAS lanes.

Before promoting the combined functionality, execute this physical-lab matrix:

| Scenario | Required result |
| --- | --- |
| Two redundant devices, peer healthy | Prepare as qualified; activate one at a time, including all shared risk groups |
| Peer down, stale or outside campaign | Block the target; continue only after current evidence satisfies policy |
| Single-homed critical service | Refuse disruptive activation; state why there is no safe outage path |
| Busy/oversubscribed uplink | Delay or rate-bound preparation under policy; block disruption without surviving-path headroom |
| Window closed after staging | Retain confirmed staged evidence; start no activation |
| Eligible replicated app and PDB | Relocate within supported semantics, prove device cleanup and replacement service, then activate |
| Hard placement with no spare capacity | Report a blocked replacement; never weaken constraints or PDB |
| Worker/manager restart or API outage | Recover recorded intent without duplicate device mutation |
| Image/source/Secret/topology drift | Require current authority and, for changed frozen identity, a newly approved plan |
| Cache missing/corrupt | Refetch only under policy or block; no false staged success |
| Upgrade then separate downgrade | Prove final version, app availability, network health and settled reservations in both directions |

The three switches can prove per-device and declared-domain behavior. They
cannot by themselves establish WAN capacity, physical path redundancy or
high-priority service protection unless the lab actually contains those paths,
loads and services. Label-only simulations must be identified as simulations.

Save sanitized evidence under a unique `/tmp` run directory and preserve the
release evidence in the project's approved durable location. Include manifests
before application, exact hashes/approvals, `kubectl get/describe/events/logs`,
manager and worker transitions, device terminal output and relevant `show`
commands, forwarding/application probe results, byte/path measurements, and
final Node/Pod/PDB/Lease/reservation/device state. Exclude credentials and keys.

Each delivered slice must include valid example manifests, read-only commands
to explain blockers, remediation and rollback instructions, and an explicit
qualification statement. Future YAML fields are intentionally not presented as
apply-ready manifests in this plan.

## 15. Completion criteria

The main extension is complete when an operator can prepare a qualified image,
review the staged evidence, approve activation separately, and complete a
topology-constrained upgrade and downgrade with typed network-health gates and
auditable recovery. Applications remain governed by native scheduling and the
qualified drain subset.

The wider roadmap is complete only for individually qualified capabilities.
Caching, extra workload types, native group lifecycle, a second driver and
larger-fleet support require their specific gates. Forced drain, automatic
trust in discovery, mandatory experimental TAS, unsupported zero-downtime
claims and unfenced multi-cluster ownership are explicit exclusions, not
unfinished code to enable later.
