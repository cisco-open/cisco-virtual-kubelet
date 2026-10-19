# Wireless tag recreation envelope and archived lineage reader

The follow-on [private lineage proof stage](wireless-tag-lineage-private.md) adds
v5 update/member/delete modes and private incarnation binding. This report
retains the preceding Create-only envelope/archive-reader qualification boundary.

## Implemented

A reserved `tag-proof:v5:recreate` envelope binds the exact predecessor cohort
digest in addition to the canonical native selector, complete producer group,
comparison-key identity and journal predecessor. It is metadata-only, Create-only
and bounded by the existing 512-character operation resource limit. Malformed,
future, physical, wrong-group and wrong-key variants are refused. Existing
v1/v2/v3/v4 operations retain their interpretation.

The action reader now understands `wireless-tag-retired:v1` wrappers. Original
rows, operation IDs, task receipts, private effects and original seals stay
intact under a digest-labelled wrapper. Active records remain separate from
historical records. Every archive must be reachable from exactly one successor
creator; each predecessor is reconstructed as one complete original producer
set with all member tombstones. The reader validates exact history digests,
owner/parent/key continuity, source advancement, generations and chronology.
UUID reuse across incarnations and duplicate original operation IDs are refused.

This reuses the current action-application store and bounded resource fields;
there is no new CRD, Secret path, transport or auxiliary persistence store.

## Qualification boundary

The [local race gate](wireless-tag-recreation-lineage-local.log) and
[temporary Kubernetes API gate](wireless-tag-recreation-lineage-api.log) each
cover 84 original-history scenarios and 76 archive-chain scenarios across
singleton site tags and singleton/bulk policy producers. Twenty-four recreation
envelope cases, seventeen native dependency/readback scenarios and existing tag
custody/deletion and SWIM regression tests are included in the same gates.
[Exact results and source fingerprints](wireless-tag-recreation-lineage-validation.json)
record the terminal results.

The chain fixtures start with lifecycle-produced Create/Update/Delete receipts.
Synthetic successor histories exercise both one and two archive generations,
missing creators/tombstones, changed and truncated history, orphan groups,
reused UUIDs/nonces, invalid wrappers, ownership/key/parent changes and unchanged
input. Changed original-task history is publicly resealed before rejection, so
its retained cohort digest is independently checked. Row order is irrelevant to
the structural chain reader; active journal bookkeeping retains its existing
order-sensitive CAS semantics.

Complete valid one- and two-generation fixtures also make a status write/read
round trip and retain the active receipt bookkeeping, restoring the original
qualification state afterward. The API gate performs eight such round trips
through the temporary Kubernetes API. This proves status/schema/reader behavior,
not that a reconciler dispatched those synthetic successor operations. Synthetic
MACs are not presented as native custody qualification.

The initial [local](wireless-tag-recreation-lineage-local-initial-failed.log) and
[API](wireless-tag-recreation-lineage-api-initial-failed.log) status-round-trip
gates failed an overly strict Go representation equality assertion. The
[focused diagnostic](wireless-tag-recreation-lineage-status-diagnostic.log)
confirmed identical serialized status and a valid active receipt. The corrected
assertion requires both serialized equality and active receipt bookkeeping;
cleanup restores the original fixture even on failure. Initial failed commands
are retained as failures, not counted as passing qualification.

## Still required before activation

The planner still fences recreation. It does not emit v5 operations, archive
rows or recreate lab tags. Native v4 lifecycle qualification remains separately
recorded in [the bindings evidence](wireless-tag-bindings.md).

- Bind private effects to incarnation lineage through subsequent updates and
  deletes; structural/public hashes alone do not establish native custody.
- Integrate complete-group candidate/absence checks and capacity reservation
  into planning and repeated final preflight. Partial/mixed cohorts must not
  borrow a bulk producer's authority.
- Atomically archive the predecessor cohort and publish the new creator through
  the existing CAS writer after original task/tree and independent readback.
- Recover acknowledged work without replay across restart, lost response,
  source/key/owner changes and uncertain native outcomes.
- Qualify repeated manifest-driven tag recreation lifecycles in the lab,
  then installed workers and broader cohort/migration/handoff combinations.

This completes the envelope and structural archive reader stage, not recreation
execution or full NetAsCode parity. ND and direct-device transport/upgrade
implementations were not modified.
