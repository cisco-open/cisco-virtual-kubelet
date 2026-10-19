# Owned tag CR deletion qualification

Subsequent qualification: [same-section owner handoff](tag-owner-handoff.md)
now closes the original-plus-two-successor native flow. The scope and source
hashes below describe this earlier phase.

The branch supports `deletionPolicy: Delete` for metadata-only inventory,
template and combined tag configurations. Source pruning remains independent:
the new `tags-owned-deletion-apply.yaml` policy revision deliberately uses
`prunePolicy: Retain` on the same named object as the source-pruning example,
preserving its UID and existing ownership.

The manager enrolls its finalizer before Apply. The isolated worker shares the
existing feature metadata cleanup handoff, durable journal, original-task
recovery, atomic receipts and status CAS. It receives no metadata/finalizer
writes. Inventory keeps its existing finalizer; templates and combined tags use
the tag finalizer. Existing feature cleanup supplies its original semantics to
the same worker, rather than duplicating the state machine.

Finalization operation identities bind the exact deletion event and full spec,
while canonical tag Delete verification preserves creator, latest-state and
absence receipts. Full native detail, original UUID/name identity and complete
empty device/interface/template consumer observations precede one-use dispatch.
A missing task, drift, unknown consumer or incomplete receipt retains the fence.
Ordinary Apply/prune cannot submit a finalization identity. Only a bound worker
completion proof can pass the registered pure manager callback; unrelated
finalizers remain unchanged.

Retained own operations/receipts take precedence over edited source when
selecting inventory cleanup. An ambiguous empty source cannot transfer a
completed discovery owner's custody to tags. Observation context is attached
only after selecting the cleanup protocol and loading its original input.

## Native results

All three scopes passed against Catalyst Center, using an isolated Kubernetes
API, fresh reconciler instances and a worker client that rejects metadata writes.
Each cycle created and updated one generated tag, then deleted the CR while the
source still declared that tag. The source prune policy stayed Retain.

| Scope | Native UUID | POST / PUT / DELETE | Original tasks recovered |
| --- | --- | --- | --- |
| inventory | `29ea7796-1d1e-439c-87a7-d15d14c6ca70` | 1 / 1 / 1 | 3 |
| combined | `653541bb-e0c8-4432-a8b0-cdd42894d513` | 1 / 1 / 1 | 3 |
| templates | `5770d29b-0fcd-43f3-a1d8-7b522e40ee5b` | 1 / 1 / 1 | 3 |

All three catalogs returned to their full pre-test baselines. The registered
manager callback accepted each bound proof; the harness then applied the same
optimistic metadata patch through its admin client and verified the unrelated
finalizer remained. This does not claim an installed manager/worker rollout.
Selected persisted states, including config conditions and finalizers, are in
[tag-config-deletion-native-records](tag-config-deletion-native-records/inventory/initial-pruned.json).
The exact source and gate records are in [qualification data](tag-config-deletion.json).

## Validation boundaries

Final gates passed: 3,615 fixture passing events, 265 real-API passing events,
five foundation/provider/controller packages, SWIM/Reload/XFSU/fallback checks
and the main binary build. The focused corrected routing/example gate also
passed. Counts include parent and package pass events. A broader initial
run exposed the discovery source-routing regression; it was intentionally
stopped after more than 2,000 passing cases and is not reported as a pass.

Native ConfigMap execution, installed actors, finalization following recreated
cohorts, ownership/section handoff and owned dynamic-rule native lifecycle remain
unqualified. Settled known tag receipts permit cleanup after ConfigMap loss in
controlled and real-API tests. Inventory source loss before any tag receipt
cannot establish family custody and retains its finalizer. An accepted original
operation with unavailable input also remains fenced. Imports gain no creator
authority; older writable-only receipts need an explicit owned Update before
cleanup. Shared receipt count/byte ceilings and operation deadlines still bound
scale. This closes one tag lifecycle gap, not full pinned NetAsCode parity.
