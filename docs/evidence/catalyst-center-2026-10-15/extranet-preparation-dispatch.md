# Extranet durable preparation and shared-permit dispatch

Later stage: see [public lifecycle evidence](extranet-public-recovery.md) for the
current bounded routing and qualification. The text below records this earlier stage.

## Implementation

Private extranet preparation now validates the complete fabric-only source and
requires declared metadata prerequisites to converge before persisting a policy
operation. Every declared policy is checked before selecting a write; a later
invalid scope or unowned expansion cannot allow an earlier operation to prepare.
No prerequisite mutation is dispatched by this preflight.

Creation and owned additive expansion use the previous managed-planning bridges,
including parent/remote VN members and immutable CiscoDevice/Node identities.
Preparation persists the existing PhysicalGroup and Prepared operation together
through the shared journal CAS, detecting lost acknowledgement or schema pruning.
It creates no reservation, claim, cordon or appliance mutation itself.

Create has no native policy UUID before submission and invents no parent ID.
Only the validated extranet resource families receive that adapter-local journal
anchor exception. Update retains its native policy UUID. Other existing group
operations keep their required target/parent validation.

Prepared updates use `extranet-update-v2:<policy-ID>:<baseline-digest>:<result-digest>`.
The baseline digest pins the complete observed old policy across restart and
exact Prepared/Submitting revalidation. The separate result/dependency digest
remains verifiable after the policy changes. Native v1 update primitives and
retained receipts still verify through the existing result contract; new durable
preparations cannot silently use the older unbound update identity. Atomic action
receipts retain the original creation ownership plus the update operation.

Dispatch requires a real shared GroupSubmissionPermit. The bridge revalidates
the complete source, exact operation and physical group before the shared
Submitting transition and again under the dispatch permit. The native planner
then reobserves dependencies before its one-use POST/PUT. Task acknowledgement
uses the common durable receipt writer. A lost response retains Submitting; a
known task is returned even when task persistence fails after member revocation.
Neither outcome permits replay or automatic release.

All extranet resource families, including malformed/pruned create, legacy update
and v2 update evidence, remain reserved for group handling. The manager's retained
protocol check also recognizes those families when an old schema has dropped
PhysicalGroup. They cannot fall back to ordinary metadata/single-device dispatch.
No transport, reservation protocol, CRD field or separate receipt store is added.

## Verification

Preparation/dispatch race tests passed (14.806s), including:

- Create, owned additive update, remote subscriber-fabric footprint, inline and
  ConfigMap source binding, and already-converged no-op.
- Whole-source refusal for later invalid/unowned policies, unresolved metadata
  prerequisites, unknown collections/sections and unbounded omitted scope.
- Lost preparation acknowledgement, pruned group data, replaced Node/native
  identity, source changes and exact Prepared/Submitting operation matching.
- Old-policy changes that still lie within the desired subscriber set: the v2
  baseline changes, so restart revalidation refuses the stale preparation.
- Both native v1 and prepared v2 update verification through atomic ownership/
  action receipt recovery, with original creation ownership preserved.
- Shared all-member claims/permits, denied/missing permits, one accepted native
  request, stale native state, lost responses and post-request revocation.
- No replay or manufactured UUID/parent anchor; unsupported anchor exceptions
  cannot relax validation for other group operations.

A supplemental public-retention race test passed (3.849s) for complete and pruned
journals in Prepared, Submitting, Accepted, Unknown, Verifying, Verified and
Cancelling. Public reconciliation keeps them fenced, and legacy start/advance/
finish helpers cannot submit or erase them. This test was added after the broad
run started and was executed separately against the same production code.
Runtime manager/connector/protocol/RBAC checks passed (4.068s).

The fixtures use the real common reservation, lease and permit implementations
against simulated Kubernetes/native HTTP actors. They do not qualify installed
actors or a live device mutation. Full affected regression and documentation
results are recorded in companion logs.

## Native observation and remaining work

Current authenticated bounded read-only queries return HTTP 200: zero fabric-site
rows, two L3 VN rows and zero extranet-policy rows. No fabric fixture is available
or selected, and no live policy/device mutation was submitted. These reads are
prerequisite evidence only.

Public configuration routing, reservation publication, acknowledged-task recovery,
verified completion/receipt publication and terminal settlement still need to be
connected and qualified as one lifecycle. Whole-model prerequisite mutation
composition, receipt retirement/reapplication, removals/replacement, omitted-scope
semantics, installed actors and a native isolated fabric cycle remain pending.
Public Apply therefore remains gated. Full NetAsCode parity is not complete.

## Final regression and scope audit

The full affected race run passed: Catalyst Center 568.125s; NaC model, shared
configuration/reservation/controller protocols, ND, direct software upgrades,
maintenance and mutation guards passed (cached). The supplemental public fence
test passed separately after the full run had started, against unchanged
production code. Runtime integration passed in 4.068s. Strict documentation
built in 4.92s before this results append; the final build is recorded in the
companion documentation log. Whitespace validation passed.

The pinned upstream cc_*.tf resource inventory and the coverage ledger both
contain exactly 93 distinct resource types, with no missing or extra ledger rows.
The companion scope-audit JSON records that check. This verifies scope retention,
not implementation or live qualification of all 93 resources.
