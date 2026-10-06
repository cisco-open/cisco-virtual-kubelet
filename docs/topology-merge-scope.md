# PR #197: bounded topology rollout scope

Scope decision: **5 October 2026**. The project owner explicitly deferred the
remaining broader-roadmap work so this branch can be reviewed and merged as
an incremental change. This document defines the merge boundary for
[`pr/johalley/tas-extentions`, PR #197](https://github.com/cisco-open/cisco-virtual-kubelet/pull/197).
It does not declare the complete roadmap finished, publish a release, or
qualify unrestricted production deployment.

## What this PR adds

These extensions build on the existing managed-topology foundation. They use
native Kubernetes APIs and CVK's manager/workers, without a third-party
scheduler or workflow controller.

| Included capability | Operator benefit | Qualification boundary |
| --- | --- | --- |
| IOS-XE `PrepareOnly`, immutable prepared receipts and separately authorized activation | Prepare an image without activating it; approve the exact retained receipt for a later activation window | Physical evidence is for the tested single-supervisor C9300 / IOS XE 17.18.02–17.18.03 cohort, not every Catalyst model, supervisor layout or release |
| Manager-accepted network observations, expiring claim-time authority and recovery/soak checks | Reject missing, stale or mismatched evidence before admitting new device mutations | Observations are bounded consistency signals; they do not independently prove forwarding safety or absence of service disruption |
| Administrator-declared critical/singleton protections, overlapping risk budgets and worker byte pacing | Express explicit disruption exclusions and shared upgrade/distribution limits | Operators supply and validate policy; CVK does not discover safe alternate paths or infer available headroom from labels |
| Observation-only retirement of eligible cancelled, settled preparation | Release retained preparation ownership while preserving receipts, claims and settled drain audit | Only never-activation-approved, exactly bound and natively corroborated preparation; not package removal, force-clear or generic uncertain-operation recovery |
| Placement checks and settled-drain recovery for the existing native Eviction/PDB path | Preserve eligible workload placement and device-clean/replacement evidence during a voluntary drain | Development preview, disabled by default; bounded ReplicaSet/Deployment subset only, not general evacuation or service-through-reload qualification |
| Read-only accepted graph diagnostics | Compare bounded observations with administrator declarations and inspect topology drift | Diagnostic only; graph completeness does not grant upgrade authority |
| Transfer measurements, digest-addressed worker-local caching and bounded planning reads | Observe distribution costs and avoid repeated per-target fleet reads | No shared persistent cache or sustained production fleet-scale qualification |
| Protocol/admission, TLS and compatibility hardening; optional native TAS conformance | Fail closed on incompatible workers, lossy old clients or incomplete authority; exercise native scheduling separately | Current matching manager/chart/CRDs required; the Kubernetes 1.37 test lane does not enable production grouped drain |

The [operator guide](topology-awareness.md) describes the APIs, permissions and
procedures. The [prepare manifest](https://github.com/cisco-open/cisco-virtual-kubelet/blob/f23e17e4cd7cb41ab0bbdf17ace34ffe93717fd7/examples/topology/iosxe-software-prepare.yaml)
and [activation patch](https://github.com/cisco-open/cisco-virtual-kubelet/blob/f23e17e4cd7cb41ab0bbdf17ace34ffe93717fd7/examples/topology/iosxe-software-activate-patch.yaml)
are examples to adapt, not reusable approval grants. Obtain a fresh exact
plan/receipt hash for each intended operation.

## Defaults and non-negotiable safeguards

- Managed topology remains disabled by default (`topology.enabled=false`).
  Network-management defaults to `readOnly`; mutation requires explicit
  read-write authority and the applicable gNOI gates.
- Workload drain remains separately disabled by default
  (`topology.policy.workloadDrain.enabled=false`). When enabled it requires
  the administrator namespace allowlist and normal Eviction/PDB enforcement.
  Native `schedulingGroup` membership is rejected before eviction. No PDB
  bypass, forced deletion, or grouped-drain fallback is introduced.
- The optional network-health gate must be selected explicitly. When selected,
  missing/stale/unaccepted observations fail closed. Independently validate
  critical services, path redundancy and maintenance windows operationally;
  neither Kubernetes scheduling nor a Ready Node proves network safety.
- Keep the two namespace-shared functional worker identities and the separate
  planning, activation and recovery grants. Do not disable admission, remove
  receipts/finalizers, or clear mutation Leases to make progress.
- IOS-XE rollout planning rejects unsupported drivers. Existing NX-OS app
  hosting/configuration support is not NX-OS software-rollout qualification.
- Apply the matching CRDs, chart/admission contract and runtime using the
  operator guide. Quiesce new campaigns and inspect outstanding claims before
  migration. Older managers deliberately refuse incompatible contracts; that
  denial is not a working rollback. Unrestricted downgrade, feature-disable
  with unresolved work, and cross-cluster transfer remain unqualified.
- New staged, recovery and drain features are for controlled evaluation within
  the documented cohort until their wider qualification is complete. Merging
  code does not enable them on an existing fleet or certify zero downtime.

## Evidence supporting this increment

Do not attribute every historical run to the newest commit:

| Evidence | Exact scope |
| --- | --- |
| [Three-device physical matrix](evidence/topology-2026-10-02/e13-final-candidate-physical-matrix.md) | Runtime `6f3686e9`: coherent downgrade and return upgrade on `.100`, `.101`, `.103`, topology budgets, secure Verify, health and settlement; not the later runtime or the full independent service-path matrix |
| [Separate activation](evidence/topology-2026-10-02/e06-separate-activation.md) | Receipt-bound authorization, closed-window behavior and physical activation in both image directions for the stated cohort |
| [Graph qualification](evidence/topology-2026-10-02/e10-isolated-link-change.md) | Controlled isolated-link change/restoration, manager replacement and worker status-forgery denial |
| [Strict HTTPS and preparation](evidence/topology-2026-10-05/bed27185-physical-validation.md) | Exact `bed27185` on `.103`, public-CA faults/restoration and preparation/retirement; not all-three-device strict HTTPS qualification |
| [Voluntary drain and retirement](evidence/topology-2026-10-05/settled-drain-retirement.md) | Drain/prepare on `bed27185`, corrected retirement on `079f6b4f`, manager restart, unchanged native installer history and healthy final Nodes/workers/apps. Two separate 30-minute management-Service captures each passed 1,799 requests; no activation/reload occurred in this follow-up |
| [Planning read budget](evidence/topology-2026-10-05/planning-read-budget.md) | Bounded full planning reads, real API and 1,000-member synthetic inventory; not sustained controller/watch/resource qualification |
| Automated regression recorded with the [latest recovery evidence](evidence/topology-2026-10-05/settled-drain-retirement.md#clean-startup-and-native-tas-regression-follow-up) | Full race/vet, 48 real-API tests, native shared-account/startup admission, pinned optional TAS, 40 Python safety tests, generation/Helm/docs and reproducible four-target packaging; preserve each run's stated source provenance |

The latest deployed physical runtime is `079f6b4f`; subsequent commits through
`f23e17e4` change test harnesses and documentation, not deployed runtime code.
The scope decision itself makes no runtime, RBAC, default or device change.
Check the final PR head's CI separately; previous green runs do not certify
later commits.

### Scope-update validation (5 October)

This documentation-only change passed strict MkDocs/license checks, all 40
`scripts/tests` safety tests, all 122 release-helper tests, the saved acceptance
checkpoint's schema validation, `git diff --check`, an uncached
`go test -race -count=1 ./...`, and `go vet ./...`.
The historical checkpoint still fails `--require-complete`, as intended.
No physical-device mutation or new qualification run was performed for this
scope decision.

The initial parallel build/test pass hit the one-second timeout in
`TestProjectedMaterialWatcherSignalsAtomicRotation`. That watcher and its test
are unchanged from `origin/main`; 100 race-enabled repetitions of the watcher
tests and the subsequent uncached full suite passed without changing them.
The timeout was not reproduced or established as a runtime defect; it is
recorded here rather than hidden by the successful rerun. Final-head CI must
still pass independently.

## Future roadmap, with testing preserved

The detailed [R0–R9 execution plan](topology-roadmap-execution.md#remaining-completion-plan)
remains the authoritative implementation and test backlog. Its unfinished
items are **not prerequisites for this bounded PR merge**. They are required
before making their broader support or full-roadmap completion claims:

| Package | Future delivery / required validation before expanding support |
| --- | --- |
| R0 | Freeze a new candidate-specific fixture/profile/result inventory mapped to every E/F gate; keep historical checkpoints unchanged |
| R1 | Interrupted and mixed/reverse-version deployment, rollback and feature-disable with settled and uncertain objects; prove no lost audit or duplicate device mutation |
| R2 | Calibrated directional rates/headroom and independent forwarding probes on isolated alternate, singleton, critical and congested paths; demonstrate correct activation denial and recovery |
| R3 | Physical inactive-image removal/replacement and source/trust/identity drift; exact invalidation and fresh-plan recovery in both directions |
| R4 | Complete negative/restart matrix and signed/portable application upgrade/downgrade with device-clean, replacement readiness and independent service-through-reload observations |
| R5 | Design/implement opt-in native grouped drain; test membership races, capacity/domain budgets, native-owner physical startup and continuity before removing the current rejection guard |
| R6 | Repeat both images across cold/warm/restarted workers and two controlled origins; measure CPU/RSS/disk and declared budgets before deciding durable caching |
| R7 | Authorize a capable second platform and two images; qualify its adapter and physical lifecycle before widening the public rollout API |
| R8 | Sustained whole-controller watches/churn/conflicts/resources, plus independent-cluster device-side-fenced transfer and return |
| R9 | After dependencies close, freeze one final candidate and execute six separate prepare/hold/approve/activate device-direction runs with independent probes and full CI/review |

Resume in the plan's dependency order: R0 inventory, R1 compatibility, then
the relevant R2–R8 implementation/fixtures and tests, and finally R9 integrated
acceptance. Continue only on explicitly authorized lab targets; do not borrow
CI nodes, shared links or another cluster's credentials. New failures in an
included safety contract are merge blockers, not automatically deferred work.

The saved [71-row checkpoint](evidence/topology-2026-10-05/acceptance.json)
remains historical and incomplete. Its validator's `--require-complete` mode
must still reject that snapshot. Deferral is not a PASS or NOT_APPLICABLE
result, and this scope decision does not weaken that validator.

## Bounded merge checklist

1. Review the included behavior and these limitations, not a claim of complete
   R0–R9 acceptance. Keep the opt-in/default-deny boundaries unchanged.
2. Pass affected local regression and strict documentation checks; preserve
   physical results with their exact revisions rather than relabelling them.
3. Push the final candidate and require all six PR checks to pass:
   `build-and-smoke`, `native-tas-conformance`, `govulncheck`, `helm4-compat`,
   `terraform-provider`, and `ygot-validate`. Re-evaluate on any later push.
4. Resolve review findings and obtain the required human approval. Confirm no
   merge conflict or branch-protection blocker remains before merging.

This scope approval is not a substitute for code-review approval. GitHub's
live check and review state is the source of truth for merge authorization.
