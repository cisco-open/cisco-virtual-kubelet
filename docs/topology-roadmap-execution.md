# Topology roadmap: execution and acceptance plan

Status: **planned; execution remains outstanding**. Audit baseline:
`bf3f4764`, branch `pr/johalley/tas-extentions`, 30 September 2026.

This document converts the [topology roadmap](topology-roadmap.md) into work
packages with implementation scope, test procedures and completion gates.
The roadmap retains the architecture and exclusions. This plan defines what
must be executed to satisfy it. Nothing below is a claim that new APIs or
tests already exist. New API concepts are requirements, not apply-ready YAML.

## 1. Scope, ordering and completion accounting

The main extension requires E00–E07 and E13's core acceptance: reliable
network evidence and policy, durable preparation, separately authorized
activation, and supported workload relocation through upgrade and downgrade.
The wider roadmap additionally requires E08–E12 and E13's applicable wider
acceptance. Observation publication, ordinary combined upgrades, an ephemeral
image cache and synthetic TAS tests are baselines, not substitutes for these
deliverables.

| Package | Roadmap | Work to execute | Prerequisites | Audit status |
| --- | --- | --- | --- | --- |
| E00 | T0 | Lab ownership, capability inventory and evidence baseline | None | Partial historical evidence; open |
| E01 | T1 | Observation correctness, provenance and meaningful regression tests | E00 inventory | Partial implementation; open |
| E02 | T1–T2 | Measured traffic/headroom and supervisor/stack health | E01 | Open |
| E03 | T2 | Administrator network policy, overlapping risk groups, expiring grants | E01–E02 | Partial campaign gate; open |
| E04 | T3 | Physical qualification of the preparation/activation boundary | E00; read-only investigation may start immediately | Open |
| E05 | T3 | Durable staged receipts and staged ownership | E04 positive capability evidence | Open |
| E06 | T4 | Separate activation approval, windows and phase reservations | E03, E05 | Open |
| E07 | T5 | Physical drain qualification and hard placement | E00, E03; full lifecycle tests need E06 | Partial drain evidence; open |
| E08 | T6 | Group recognition and actual CVK native TAS lifecycle | E00; group drain needs E07; physical owner transfer needs E12-B–D | Synthetic baseline only; open |
| E09 | T7 | Transfer measurement and conditional durable prefetch/cache | Measurement: E00; cache: E06 plus measured need | Ephemeral baseline only; open |
| E10 | T8 | Observed graph diagnostics and declared-policy drift | E01, E03 | Open |
| E11 | T9 | Second-platform lifecycle and generic API decision | E05–E06; capability discovery may start earlier | Open; suitable hardware required |
| E12 | T10 | Scale envelope and controlled ownership transfer | Stable E03–E06 contracts | Open |
| E13 | All | Integrated acceptance, migration, documentation and evidence closure | Completed dependencies for claimed scope | Open |

Recommended implementation/merge order is E00, E01, E02, E03, E04, E05,
E06, E07, E08, E09, E10, E11, E12, E13. E04 discovery, E09 measurement and
E11 capability discovery can proceed alongside earlier coding; E10 can
start after E03. Each package should be an independently reviewable change
or short dependent stack. E04 may deliver evidence and fixtures without
runtime changes. Do not enable E05/E06 on a cohort until E04 passes.

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
- Forced drain, automatic authority from discovery, graph-cost scheduling,
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

# Real API-server/CEL tests: install the pinned setup-envtest from Makefile.
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

Close E00 only with the matrix, exact deployed revisions, baseline results,
test topology and evidence storage location recorded. Prior `/tmp` reports
are historical inputs, not proof that current prerequisites hold.

## 4. E01 — repair observation correctness and trust provenance

Code: `internal/provider/topology_observation.go`, its tests,
`internal/drivers/iosxe/topology.go`, `internal/drivers/common`,
`internal/topologyhealth/gate.go`, `api/v1alpha1/types.go`, manager health
acceptance and native status-write admission.

### Required updates

1. Set completeness from actual source coverage. Truncation, invalid records,
   duplicates that cannot be disambiguated and unsupported source paths must
   yield incomplete/unknown evidence. Bound deterministic output and record
   why data is incomplete; never silently lose evidence and keep `Complete`.
2. Preserve neighbor source, interface and routing context. Do not merge CDP
   and OSPF identities based solely on equal strings, or multiple adjacencies
   solely by neighbor ID. Define and test a canonical composite identity.
3. Implement the supported newer OSPF adjacency path or return explicit
   unsupported coverage. Distinguish a proven empty source from an unread or
   partially decoded source. Preserve compatibility for telemetry consumers.
4. Add collection start/end, oldest source time, sample sequence, exact worker
   Pod incarnation/config revision, and manager acceptance binding. Define
   sequence reset on worker replacement; replay and old-incarnation samples
   cannot refresh acceptance. Bound polling duration and persisted sizes.
5. Validate identity, producer and time before using evidence. Separate
   worker-owned samples from manager-owned acceptance; protect both in
   admission. A new Node heartbeat must not refresh an old network sample.
6. Fix `TestEvaluateRequiresCompleteFreshEvidence`: give each case a valid
   identity and assert its specific stale/incomplete/skew reason. Add
   regressions that fail on the current truncation and duplicate behavior.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E01-A | Unit fixtures at 0, 1, 64 and 65 records; reordered inputs; conflicting duplicates; repeated peer IDs on different interfaces/VRFs; valid empty and unsupported sources | Deterministic bounded output; no false completeness or accidental identity merging |
| E01-B | Fake-clock tests for stale, future, replayed, slow collection and reordered samples; wrong serial/UID/worker revision; worker restart | Exact expected reasons; acceptance never extends oldest evidence freshness |
| E01-C | Real API updates using manager, app worker, network RO/RW worker and unrelated identity | Only designated fields writable; old worker/incarnation and forged manager acceptance rejected |
| E01-D | Compare physical summaries with device interface/CDP/OSPF output; interrupt a test source or use an unsupported source fixture | Physical coverage documented; missing data blocks opted-in checks, with no upgrade leaf mutation |

Close E01 only when the new negative tests fail against the old behavior,
pass against the correction, and physical observations match their declared
coverage. A successful transport call alone cannot pass E01-D.

## 5. E02 — supply measured path and platform-health inputs

Code: IOS-XE readers/optional driver interfaces, worker observation publisher,
`api/v1alpha1`, `internal/topologyhealth`, telemetry export and fixtures.

### Required updates

1. Publish directional counter samples, sampling interval, interface capacity
   and provenance. Document the unit conversion and headroom formula for each
   supported model. Keep incoming and outgoing capacity distinct where needed.
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
| E02-A | Counter fixtures for reset, wrap, huge values, capacity change, missing direction, threshold boundaries and empty scope | Correct conservative headroom or explicit Unknown; no false idle capacity |
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

1. Add bounded administrator-required network checks and explicit disruptive
   activation prohibition for critical/single-homed services. Campaign input
   may only tighten restrictions; define monotonic policy transitions.
2. Add overlapping administrator-declared risk groups with exact physical
   membership. Freeze relevant membership/policy into plans and include
   non-target unhealthy members and all campaigns in one CAS admission.
3. Evaluate alternate-path and surviving-path headroom using declared scope
   and E02 evidence. Add hold-time continuity and bounded transfer-rate policy;
   account for aggregate reservations where a shared path is known. Enforce
   actual byte pacing in the worker on each policy-covered transfer segment.
   If a transport cannot enforce the bound, reject that opted-in operation.
4. Bind grants to accepted evidence hash, producer identity, policy epoch,
   control revision and absolute expiry. Workers validate this authority
   before every new mutation claim, including activation after a long install.
   Publish renewal/fencing through durable CAS; no admission/claim race may
   preserve expired authority. Bound evidence changes while a grant is in use.
5. Recheck during recovery/soak. A late failure stops new admissions while
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
| E03-F | Expire evidence between install and activation; fail health during soak | No activation on expired authority; soak continuity resets and accepted work remains accounted for |

Close E03 after actual service probes qualify the declared path scenario.
Label-only redundancy tests qualify accounting, not forwarding resilience.

## 7. E04 — prove the device preparation boundary

Code/evidence: `internal/softwarelifecycle`, IOS-XE lifecycle adapter,
gNOI OS inventory readers, capability fixtures and physical qualification report.

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

### Required updates

1. Build a bounded diagnostic graph retaining local/remote interface, protocol,
   VRF context, sample time and provenance. Resolve managed peers only from
   sufficient identity evidence; preserve unknown peers and asymmetric edges.
2. Compare against administrator-declared peers/risk groups. Report missing
   peers, changed uplinks, stale coverage and potential single points of
   failure in the declared model. Never assert end-to-end redundancy solely
   from CDP links.
3. Add read-only explain output with the evidence behind each finding. A
   policy can block on qualified evidence; suggestions require a new operator
   plan and do not rewrite labels, membership or approvals.

### Tests and exit gate

| ID | Execute | Required result |
| --- | --- | --- |
| E10-A | Fixtures: spoofed/equal names, unknown neighbors, asymmetric links, multiple VRFs, LAGs, stale/truncated input and graph size limit | Honest unresolved identity/coverage; bounded deterministic diagnostics |
| E10-B | Physical capture, controlled change to an isolated test uplink, restore and recapture | Drift shown with provenance and cleared on fresh recovery; declared authority unchanged |
| E10-C | Compare protected labels, policy, approval hashes and RBAC before/after discovery changes | Diagnostics cannot grant mutation authority or silently edit the plan |

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
| F13 | Complete upgrade and separate downgrade for all qualified C9Ks | Exact final OS and secure gNOI health; app/network probes pass; expected scheduling restored; maintenance settled and no unexplained reservations/Leases |

Run F01–F08/F13 for core acceptance, plus applicable F09–F12 for wider roadmap
claims. F06 requires E08-A's rejection guard, not positive group support;
F07's core crash tests do not require the wider ownership-transfer feature.
Explain cohort limitations for every row. An unsupported artifact,
manual scale-down workaround or simulated path must not be recorded as a
positive full-service scenario. Manual recovery is retained as a failure case;
after repair, rerun the intended positive path to qualify it.

### Evidence and final checklist

Use a unique `/tmp/cvk-topology-<run-id>/` directory, then copy sanitized
evidence to the project's approved durable artifact location. Resolve that
location in E00; `/tmp` alone is not a release archive. A versioned index must
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
