# Atomic site-retirement receipt publication

The staged publisher consumes the [v2 original-task and absence verifier](site-retirement-recovery.md)
and uses the existing single-CAS action-receipt persistence helper. It adds a
separate `site-retired:v1:<kind>:<path-hash>` row while advancing the same operation
to Verified in that status update. The complete creator/latest predecessor cohort
and section owner remain intact. There is no new transport, credential store,
CRD, RBAC or external-provider change.

## Publication and settlement

Publication requires the same controller/config/source frame, complete original
Delete task and independent absence/ancestry proof. It rechecks retained history,
source projection and operation identity before the status CAS. The bounded
record cannot replace an earlier retirement for the same path. Publication
refuses more than 256 action rows or more than 256 KiB of serialized action rows.
Pre-dispatch capacity reservation still belongs to the future public writer.

The existing persistence helper verifies that operation and new receipt survive
readback. The publisher additionally validates the full predecessor cohort, so
schema pruning cannot silently discard the creator while retaining the new row.
A conflict returns without retrying publication; a later attempt reobserves the
same acknowledged task. If the update committed but its response was lost, a
retry recognizes the exact retained receipt and preserves its first verification
time without more native reads or a repeated publication.

The shared journal now blocks generic sites-Delete verification and requires the
retirement receipt before Verified settlement. It also refuses cancellation if a
submitted retirement is relabeled Prepared. An actual unsubmitted Prepared
operation remains cancellable. Family/result markers and sites Delete metadata
provide independent schema-loss fences. Slot release preserves all producer and
retirement rows and the section owner; it is not custody transfer or authority
to recreate the missing site.

## Evidence

The [final race-enabled regression run](site-retirement-receipts-regression.log)
passes **57** families / **720** subcases in **53.176 s**, including existing
private-site, image, shared-journal, physical-receipt and no-dispatch tests. The
new receipt scenarios cover inline and ConfigMap sources, current/latest area
lineage, acknowledged Unknown, generic bypass attempts, pending/failed tasks,
conflicts, lost responses, dropped result/creator/operation fields, capacity,
malformed receipts and Prepared relabeling.

The [temporary real Kubernetes API run](site-retirement-receipts-api.log) passes
**54** scenarios across binding, recovery and publication in **31.812 s**. The
publication family contributes 21 scenarios. The byte-bound fixture stays within
the existing 128-byte sourceVersion field limit while explicitly exceeding the
256 KiB publication bound; no schema limit is widened. The
[source and terminal-gate audit](site-retirement-receipts-validation.json) fixes
the exact qualified source boundary.

These are component fixtures using native HTTP test servers and private API
servers; publication lifecycle fixtures use owned areas. No new lab mutation or
installed automatic Delete is claimed. Building/floor retirement, guarded public
submission and capacity admission, additional operational/settings consumer
checks, bounded lineage archival/recreation, source/config deletion and complete
installed qualification remain open. Full site lifecycle and 93-resource parity
remain incomplete.


The subsequent [staged dispatch writer](site-retirement-dispatch.md) now reserves
unknown receipt fields at their schema bounds before submission and connects the
existing journal to guarded native Delete. This does not promote these earlier
publication fixtures to public or installed retirement qualification.
