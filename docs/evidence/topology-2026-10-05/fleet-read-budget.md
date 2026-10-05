# Fleet assessment request budget

R8 review found a concrete scale defect: `currentFleetMembers` listed devices
once and then fetched every Node individually. The 1,000-device regression
failed with **1,001 API reads**, exceeding the entire reconcile budget of 250
before target-specific admission work.

The correction uses two fresh, uncached API lists and indexes Nodes by name.
All existing UID, physical identity, topology projection, health freshness and
non-target risk checks remain. This is not a cached authorization result;
subsequent mutation admission still revalidates live authority. Missing Nodes,
rebound identities and API outages fail closed. Unknown health stays Unknown.
No new RBAC, account, watch, API field or external dependency is introduced.

`TestFleetAssessmentUsesBoundedFreshReads` fails before the change and passes
after it. The complete controller race suite passes. The pinned Kubernetes
1.35.0 `TestEnvtest_FleetAssessmentFreshSnapshot` stores 1,000 actual
CiscoDevice/Node objects with API-assigned identities, measures 80 invocations,
and proves deletion is observed by the next assessment. These fixtures are
deliberately unhealthy and cannot authorize a software operation.

Initial local results on the working tree subsequently committed with this
record (20 samples each, two requests per sample):

| Campaign target slots | Fleet-read p50 | p95 | p99 |
| --- | --- | --- | --- |
| 10 | 41.77 ms | 56.01 ms | 93.86 ms |
| 50 | 41.98 ms | 55.17 ms | 62.12 ms |
| 100 | 43.08 ms | 50.00 ms | 55.54 ms |

The one-target case also passed; the raw log contains its measurements.
Raw local logs: `/tmp/cvk-20261005-fleet-reads-before.log`,
`/tmp/cvk-20261005-fleet-reads-after.log`,
`/tmp/cvk-20261005-fleet-api.log`.

**Scope limit:** the helper assesses the full fleet independently of campaign
size. These calls do not execute complete reconciles, native watches, parallel
campaigns, worker readiness/grants, injected latency, RSS or sustained churn.
The full R8 real-controller scale gate remains open; do not interpret this
substep measurement as proof that the whole reconcile meets its latency or
250-request budget. Physical validation also requires the committed candidate.
