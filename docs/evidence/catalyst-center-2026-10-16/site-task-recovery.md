# Singular site task trees and acknowledged-task recovery

## Implementation

The public site compiler now attaches a product-scoped completion observer and
recovery selector to the existing configuration apply plan. Singular area,
building and floor Accepted tasks require the shared complete rooted task tree.
A pending child stays pending; a failed, foreign, incomplete or unavailable tree
cannot fall back to a successful root task.

Acknowledged Unknown operations can observe their original task when the resource
is still declared and the retained operation is a valid singular metadata
Create/Update with its parent identity, task ID and submission time. The existing
reconciler checks config/source/controller generations and section ownership
before task observation. Its existing Unknown-to-Verifying transition, native
readback and journal settlement finish the operation. No new submission permit,
transport, CRD field or recovery journal is introduced.

Taskless outcomes, undeclared resources, root/bulk/design operations, physical
reservations, prepared/cancelled operations, credentials and creator-receipt
shapes cannot acquire this singular recovery permission. Existing bulk full-tree
completion and floor-image/planned-AP protocols remain separate. Unknown bulk
or design operations have not gained recovery through this change.

This closes the production task-tree and acknowledged-recovery gaps identified
in [the preceding singular dispatch evidence](site-singular-dispatch.md).
It does not implement creator ownership. Recovered Retain-only creates use the
existing desired-state/parent verification contract without inventing a created
UUID receipt or adopting a same-name object for later deletion. The earlier
native create-result UUID observation remains qualification evidence only.

## Verification

- [152 focused race subcases](site-task-focused-final.log) pass in 19.432 seconds:
  70 whole-tree cases across five create/update stages and Accepted/Unknown
  phases, 17 recovery-authority boundaries, 35 public recovery cases and the
  existing 30 public no-dispatch cases. Whole-tree tests reject successful-root
  fallback and observer mutation of the retained operation.
- Restarted public recovery handles a completed original task with exactly one
  initial write. Pending/failed/foreign/unavailable child evidence retains the
  original Unknown task; changed source or owner prevents even task observation.
  Lost acknowledgements remain taskless and never replay. No creator receipt is
  fabricated when the operation finishes.
- [35 real private Kubernetes API cases](site-task-api.log) pass in 42.361 seconds
  using Kubernetes 1.35.0 and real CRDs, with simulated Catalyst Center HTTP.
  They repeat the five-stage recovery and source/owner/child fencing matrix.
  These are private API persistence checks, not installed actor qualification.
- [Native lab cycle](site-task-native.log) passes in 35.77 seconds including exact
  cleanup. One complete source creates an isolated area/building/floor hierarchy
  and then updates floor width. After each acknowledgement the harness marks
  that original durable test operation Unknown; restarted public reconciliation
  observes the real native task tree and converges without resubmission. All
  four tasks recover, full native attributes/no-op are checked and all generated
  sites are removed child-first. Original create result UUIDs also bind exact
  native kind/path/parent before cleanup.
- The same native cycle injects four local pre-submit read failures and checks
  four certified retirements followed by exactly four real create/update writes.
  No physical device or SWIM operation is performed. Native tasks/HTTP are real;
  persistence, worker restarts and uncertainty injection are simulated.
- [Site/floor/no-dispatch regressions](site-task-regression-final.log) pass in
  32.228 seconds. Two old HTTP fixtures initially lacked tree responses and were
  corrected to model the stronger completion contract; production does not fall
  back to root-only responses for fixture compatibility.
- [ND, controller and software-upgrade foundation packages](site-task-foundations.log)
  pass (some cached). The broader configuration/template/site/floor regression
  passes in 191.064 seconds ([terminal log](site-task-broad.log)).
  [Catalyst Center SWIM/readiness/inventory-sync/upgrade regression fixtures](site-task-swim.log)
  also pass in 3.746 seconds. These are fixtures, not another live upgrade.

Reproduce the native cycle with privately loaded live credentials and the
following explicit opt-ins:

```text
CATC_SITE_EXECUTION=isolated-hierarchy
CATC_SITE_SINGULAR_MANIFEST=isolated-hierarchy
CATC_SITE_TASK_RECOVERY=isolated-acknowledged-tasks
CATC_SITE_NO_DISPATCH=isolated-preflight-reads
CATC_LIVE_REQUESTS_PER_SECOND=2
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenterSiteSingularManifest$' -count=1 -v
```

The existing live runner also needs CATC endpoint/username/password environment
variables, which must not be printed. The test deliberately uses generated empty
sites, not device upgrades or provisioning claims.

[Source hashes](site-task-sources.json), [exact 93-resource scope audit](site-task-ledger.log)
and [strict docs](site-task-docs.log) accompany the qualification.

The additional [five public native-readback cases](site-task-readback.log) and
[five private-API readback cases](site-task-readback-api.log) check the next
boundary after a successful original task tree: missing created objects and
replaced update UUIDs retain Verifying with no receipt or resubmission. Together
with the initial matrix, this gives 157 focused cases and 40 private-API cases.
The five added cases pass in 4.288 seconds (public fixtures) and 13.843 seconds
(private API including server startup/teardown). Only these added cases were rerun
after adding them; production/live code did
not change after native qualification.

## Remaining work

Durable site creator lineage, private latest-applied baselines, dependency-safe
prune/delete and atomic owned lifecycle settlement remain missing. Installed
workers, real actor/reservation coordination and applicable hardware behavior
still require qualification. Bulk/design recovery and the remaining per-resource
ledger gaps are separate work. Full pinned-model parity remains incomplete.
