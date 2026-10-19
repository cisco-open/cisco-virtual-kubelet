# Complete producer history for wireless tag recreation

Follow-on [envelope and archive-chain reader work](wireless-tag-recreation-lineage.md)
now implements the next structural stage; execution remains fenced. This report
retains the earlier collector/absence qualification boundary.

## Implemented boundary

`wirelessTagRecreationCandidate` reconstructs one complete original tag producer
set from the existing action applications. It requires its original Create and
all member-specific v4 Delete tombstones, unchanged native/parent/owner identity,
one comparison-key identity, non-regressing controller/config generations and
new manifest/source input after deletion. A bulk policy producer cannot be split
into independently retired members. Imported updates without a creator, partial
producer sets and legacy deletion proofs cannot grant recreation eligibility.

The candidate retains independent copies of the exact original rows and binds
them with a deterministic history digest. Reordering the ledger or adding an
unrelated resource does not alter the digest; changing the original task history
does. This is structural validation, not cryptographic dispatch authority:
public row resealing alone does not establish a private MAC or current custody.

`recreationAbsent` separately reuses the native deletion scope and private
comparison protocol. It requires original UUID absence, an unchanged parent
RF/SSID graph and tombstone MAC, and brackets repeated observations with current
journal/key checks. It performs no native mutation or status publication.

## Qualification

The [local race-enabled gate](wireless-tag-recreation-local.log) checks real
fixture-produced Create/Update/Delete lifecycle receipts for singleton and bulk
policy producers: **84 history scenarios** across the two namespaces and two
producer shapes. The same 84 scenarios run against persisted receipts through an
[isolated temporary Kubernetes API](wireless-tag-recreation-api.log).

The local gate also runs 17 dependency readback scenarios. Its five post-delete
scenarios additionally exercise recreation absence with selected/unselected RF
and SSID UUID changes and stable ordering; each tests changed key, old UUID,
journal proof and tombstone MAC refusal. These native readers use HTTP fixtures,
not the lab. No new lab mutation, installed worker, physical device operation or
new recreation flow is claimed. Existing native v4 lifecycle qualification
remains separately recorded in [the bindings evidence](wireless-tag-bindings.md).

[Gate results and source fingerprints](wireless-tag-recreation-validation.json)
record the precise qualification boundary.

## Remaining execution work

Recreation remains fenced in the existing planner. The candidate/absence helpers
are preparation for integration, not a newly enabled manifest capability.

- Version the recreate operation to commit the exact predecessor cohort digest
  and complete producer membership within the existing resource-size bound.
- Validate archived predecessor chains without reinterpreting legacy receipts,
  losing siblings, accepting repeated UUIDs or shortening retained history.
- Reserve bounded ledger capacity and bind final preflight/submission/recovery to
  current source, owner, key and original task evidence.
- Atomically archive the old cohort and publish the new creator, preserving CAS
  and restart safety. Verify new native UUIDs differ from every retired UUID.
- Qualify repeated Create/Delete/Create cycles, partial/mixed cohorts, lost
  acknowledgments and corrupted/truncated chains locally, through Kubernetes
  APIs, and against generated lab metadata before enabling the path.

No resource-level parity flag or CRD/transport contract changed. ND, direct-device
upgrade and SWIM paths are untouched. Full pinned-model parity remains open.
