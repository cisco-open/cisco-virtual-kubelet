# Independent discovery cohort progress with retained active jobs

Date: 2026-10-16. Branch: `pr/johalley/catc-support`.
Pinned NetAsCode model: 0.5.0.

## Execution and readiness

The pinned module declares a collection of discovery resources. CVK's owned
executor previously returned Busy immediately upon finding an unchanged active
job. That prevented independent creates and source pruning elsewhere in the same
manifest, and could hide a later member's drift behind the first pending result.

The executor now validates every desired member before selecting a write. Known
native identity/field mismatches remain errors; active-but-matching retained jobs
mark readiness pending. A selected independent write can proceed. If no create or
replacement remains, Busy means the fully validated cohort still has pending
runtime completion. The existing pruning planner may proceed in that specific
case only when it has separately verified obsolete owned candidates. It repeats
whole-cohort validation and exact candidate matching immediately before dispatch.

The same journal serializes submissions. No scheduler, retry loop, provider,
transport, public API field or ownership store was introduced. Native job/task
identity, frozen source/credential selection, section ownership, single-use
submission, receipt/capacity fencing and Unknown task recovery remain shared.
A no-write cohort with any retained pending job remains out of InSync. Complete/
Inactive is still required for every unchanged desired job before readiness.
Imported matching jobs are not adopted; legacy and foreign ownership protections
remain intact. Invalid/changed members cannot use a pending peer to bypass preflight.

Example: `examples/configs/catalyst-center/discovery-cohort-apply.yaml`. Revise the
same config UID to remove a job under Delete; Retain does not remove omitted jobs.
Mixed discovery/tag/template/device pruning and unverified-create cancellation
are separate unsupported flows.

## Fixture and Kubernetes evidence

The discovery race suite passes (7.030s). Public reconciliation creates a second
job while the first stays Active, prunes the second while retaining the active
first job, remains out of InSync until that first job completes, and finally
prunes its exact owned ID. All three new acknowledged operations are JSON-restored
as Unknown and recovered without replay. A foreign native job remains untouched.
Initial and late cohort drift/identity changes refuse dispatch and consume the
single-use closure.

The same public cohort flow passes with real Config/Controller objects on an
isolated Kubernetes 1.35 API server (6.30s), including API-generated metadata and
persistent status. The existing single-job active flow also passes (7.28s; package
15.727s). Native HTTP/tasks are fixtures in those tests; this is not installed
worker or native-plus-Kubernetes qualification.

## Native qualification and regression status

`TestLiveDiscoveryCohortProgress` passes (88.76s; package 90.893s) against the
authorized .101 target. Generated jobs:
`CVK_COHORT_753a14cd52517df8_keep` and
`CVK_COHORT_753a14cd52517df8_remove`. Their native IDs are recorded below.

The public worker created the first job, then a bounded exact-owned-ID test-only
restart provided the Active/In Progress precondition. The source added a second
job, and its POST was observed while the retained first job was Active/In Progress.
After both jobs converged, another separately recorded restart made the retained
job active again. A source revision removed the second job under Delete; its DELETE
was observed while the first stayed Active/In Progress. The retained job was still
Active/In Progress immediately after the independent prune retired, and the config
was not InSync. It subsequently converged without another write when that retained
job completed. Final empty-source pruning removed the first job.

All four acknowledged public operations were restored as Unknown and recovered
without replay. Both original job baseline and target device ID/serial/count were
preserved. The proxy allowed only the two nonce jobs, their published native IDs,
two POSTs, two DELETEs and two bounded test-only restart PUTs. Each private restart
was claimed exclusively before submission and retained its acknowledged task ID;
these preconditions are not a production restart feature. No upgrade, template or
site provisioning was requested.

Private evidence: `/private/tmp/cvk-discovery-cohort-2668774368/`;
native log: `/tmp/cvk-discovery-cohort-native.log`.
`complete.json`: cleaned/qualified true; creates=2, deletes=2, testOnlyStarts=2,
knownTaskRecoveries=4, createWhileActive=1, pruneWhileActive=1. The before-create,
before-prune and after-prune metadata files independently record Active/In Progress.
The final source and journal retain two verified deletion tombstones and no pending
operation. Kubernetes persistence in this native run is simulated; the separate
real API-server tests above cover their stated integration axis.

| Revision/effect | Native task |
| --- | --- |
| 1: Public first create | `01a14255-ee55-7c16-927a-e90f8c1a732c` |
| Private retained-job restart | `01a14256-3631-7972-9198-8b21dc04af90` |
| 2: Public second create | `01a14256-70f3-7207-911d-54a6d1719ea3` |
| Private retained-job restart | `01a14256-d12c-7c50-948d-0bc245ec6a3b` |
| 3: Public independent prune | `01a14257-0e3d-7615-a24a-46a597f760b5` |
| 4: Public final prune | `01a14257-31a4-7e12-bbbd-9fb0f37b1660` |

Native IDs: `95`, `100`. No generated job remains.

The new two-job repository example passes compilation (2.217s). ND, model catalog,
direct-device gNOI, SWIM/software lifecycle and migration-tool race checks pass
(migration tool 1.550s; other packages cached). The full Catalyst Center package
passes with race detection (620.551s; `/tmp/cvk-discovery-cohort-package.log`).
All production changes were present in that run; later live/manifest checks are
recorded separately. Strict documentation validation passes (4.89s before this
result annotation), focused Go formatting checks pass, and `git diff --check` is clean.

## Remaining scope

This implements independent progress inside discovery-only cohorts. It does not
claim a general mixed-resource dependency planner, unverified initial-create
cancellation/handoff, per-site discovery, inline credentials, legacy adoption,
whole-CR cleanup, owner handoff, installed actors or complete resource parity.
Broader scale, throttling and runtime failure/outcome classification remain pending.
