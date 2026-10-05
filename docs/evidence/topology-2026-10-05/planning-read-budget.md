# Complete planning reconcile: bounded API reads

Follow-up to `8fa9b785`, 5 October 2026. This closes another measured request
amplification defect, not the whole R8 sustained-controller acceptance gate.

## Defect and implementation

With 1,000 inventoried devices, the complete planning path made 7, 61, 301 and
601 reads for 1, 10, 50 and 100 selected targets. Repeated worker Deployment,
Pod and ReplicaSet ancestry checks alone exceeded the predeclared 250-request
reconcile budget at 50 targets. The earlier two-read fleet-assessment repair
did not cover this separate path.

Planning now creates a namespace-scoped worker snapshot and a Node snapshot
using four fresh API lists. The original device inventory makes five planning
reads, independent of selected target count. Existing UID/owner/revision,
duplicate-worker and health checks still validate the joins. Lists are not a
cross-kind transaction. A plan is an approval proposal, not authority to mutate.
The snapshot exists only in a local reconciler copy during `buildFrozenPlan`;
it is never installed on the shared reconciler or reused by grant, drain,
activation or mutation-claim checks. No RBAC, API fields or admission policy
changes are part of this repair.

Negative controls reject ambiguous workers, missing/foreign ReplicaSet ancestry,
failure of any of the four lists and cancelled collection. A deletion test
deliberately retains the old planning snapshot and proves that both the normal
admission reader and a subsequent plan reject the deleted worker.

## Real API-server result

Pinned Kubernetes 1.35 envtest creates 1,000 actual device/Node API identities,
100 defaulted worker Deployment/ReplicaSet/Pod fixtures, and 80 separate
unapproved campaigns. It executes the **complete planning Reconcile**, including
policy/ledger reads and frozen-status publication. HTTP transport counting
includes writes, not just Reader method calls. No device client, credential or
running worker exists in the test; no upgrade leaf is created.

| Targets | Samples | Maximum API requests/reconcile | p50 | p95 | p99 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 20 | 9 | 98.223 ms | 103.329 ms | 107.860 ms |
| 10 | 20 | 9 | 96.909 ms | 106.135 ms | 112.957 ms |
| 50 | 20 | 9 | 91.957 ms | 107.652 ms | 111.525 ms |
| 100 | 20 | 9 | 90.503 ms | 109.048 ms | 144.243 ms |

All samples satisfy the existing 250-request and 0.5/2/5-second percentile
budgets. The fixture client uses QPS/Burst 1,000 to isolate API/reconcile costs;
these local results are not a WAN or managed-control-plane latency guarantee.
The fixtures deliberately do not run a kubelet or simulate device progress.

```sh
go test -race ./internal/controller \
  -run 'TestRolloutPlanningReadBudget|TestPlanning' -count=1 -v
KUBEBUILDER_ASSETS="$(setup-envtest use -i 1.35.0 -p path)" \
  go test -tags envtest ./internal/controller \
  -run '^TestEnvtest_PlanningReconcileReadBudget$' -count=1 -v
```

Local captures: `/tmp/cvk-20261005-planning-budget-before.log`,
`/tmp/cvk-20261005-planning-snapshot-final.log`, and
`/tmp/cvk-20261005-planning-api-final.log`. The sanitized table above is the
durable result; raw captures are not release artifacts.

Still required: approved execution/recovery state-machine load, simultaneous
campaigns and overlapping groups, watch churn/cardinality, injected API
latency/conflicts and manager RSS measurement. No R8 or full-roadmap completion
claim follows from this planning-only qualification.
