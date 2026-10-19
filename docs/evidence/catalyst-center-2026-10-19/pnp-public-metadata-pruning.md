# Public owned PnP metadata pruning

The normal NetworkControllerConfig Apply reconciler now connects omitted owned
PnP registrations to the existing metadata lifecycle. An explicitly configured
metadata runtime is required for new preparation. Default production composition
still supplies no authorization hooks, so this change does not activate installed
mutation execution.

The complete pinned inventory-only source, explicit Prune Delete, retained
config-deletion policy, section owner, canonical creator receipts and receipt
capacity must validate. The existing deterministic full-cohort planner repeats
native eligibility and managed-serial absence checks. Public preparation writes
only the original Prepared operation. Repeated reconciles cannot submit it until
the manager seals the original identity cohort and records its complete ready
fences. The worker then resumes that exact operation through the existing
single-use claim and guarded transport; it cannot use an unsealed legacy path.

A synchronous acknowledgement advances to independent absence verification and
the exact creator-bound tombstone. Worker finish stays blocked while manager
custody exists. The shared manager settlement and retirement retain all creator
and absence evidence, then a later public reconcile finishes the operation and
plans the next pending UUID. Unknown and lost responses never become successful
from absence and never replay. Certified pre-HTTP refusal waits for manager
settlement before any fresh plan. A retained PnP handoff also keeps source edits
from falling through to a different execution family.

After the pending owned cohort is exhausted the stage reports PnPRemovalComplete
while configuration readiness remains false. This is metadata cleanup only;
it does not attest physical site claiming or whole-model convergence. Existing
acknowledged recovery remains available without enabling new dispatch.

## Qualification

The public Reconcile entry point is tested with two owned records through
preparation, unsealed waiting, manager handoff, one submission per operation,
verification, blocked premature finish, manager settlement/retirement, next UUID,
and repeated completed reconciles without replay. Separate scenarios exercise a
lost native response and a late unsafe sibling: Unknown retains its journal and
fences; the original pre-HTTP refusal retains durable certified custody with zero
DELETEs. Disabled runtime does not even prepare new work.

The same three scenarios pass against a real isolated Kubernetes 1.35 API server
with source CRDs, server-assigned UIDs, active manager/Pod-bound worker admission,
status CAS and real Lease deletion. Appliance responses are controlled. Broad
impersonation RBAC isolates admission testing; worker readiness, compiler status
and deployment compatibility are staged. These tests do not prove installed RBAC
or a production runtime rollout.

The Catalyst Center lab run passed the public Reconcile path for two newly
generated uncontacted records:

- 6ad5bf700bc0186771cb1616 / CVK7BA44A70
- 6ad5bf730bc0186771cb1617 / CVK7BA44A71

It used two registration POSTs and exactly two creator-UUID DELETEs, with public
preparation, repeated unsealed waiting, original guarded submission, independent
absence, bound tombstones, manager settlement/retirement and public finish.
Final repeated reconciles did not replay. Two final reads verified an empty PnP
catalog and unchanged native network inventory. No device claim/reset/upgrade
or other device mutation was allowed by the qualification transport.

Private recovery evidence is retained at
/private/tmp/cvk-pnp-cohort-1713719313. Its records are now deleted; this completed
journal must not be replayed. Native-run Kubernetes storage and runtime authority
were simulated with JSON-persisted snapshots; real API actor/schema tests are the
separate qualification above. This is public reconciler/native transport proof,
not installed manager/worker end-to-end proof.

[Machine-readable results and source fingerprints](pnp-public-metadata-pruning.json)
record the final verification gates.

Final regression passes 18699 test/package events across 12 packages, including the complete Catalyst Center suite, ND, shared APIs/controller foundations, maintenance, direct upgrades and software lifecycle. The final three real-API scenarios pass; the main build, strict documentation build, formatting, scoped whitespace and frozen-source checks pass. The resource table retains 93 unique rows.

## Remaining full-model scope

Installed manager/worker/enrollment/settlement compatibility and composition,
complete consumer census and protection, controller generation/domain changes,
config/controller deletion and missing-source finalization remain pending.
Physical PnP claim completion and the wider pinned 93-resource hardware/execution
gaps remain open. This milestone establishes neither full-model completion nor
merge readiness.
