# Durable metadata fence references

The existing NetworkController metadata handoff now has an optional fences map
list. Each entry retains the canonical physical identity, bootstrap Lease name
and original UID in the sealed lease namespace. It may grow incrementally within the sealed cohort while Prepared; each
recorded entry is immutable and cannot disappear while the handoff is retained.
Worker submission requires the complete cohort.
Deep copies isolate these references. Legacy status encoding remains unchanged
when no handoff or references are present.

Manager actor admission requires separate seal and fence-publication steps.
Only the manager may append the references, while the exact original operation
is still Prepared and there is no refusal or release evidence. The worker cannot
attach references during progression or refusal, and cannot enter Submitting
without the complete reference set. Publication cannot accompany submission.
The compiled live metadata policy fingerprint was updated, so an older policy
cannot authorize a new runtime through the protection reader.

ReadMetadataFenceCohort checks authoritative current controller/configuration
state, canonical serial/name correspondence, unique original UIDs and each
Lease's retained metadata marker, original binding, source/config/controller
identity and worker identity. It reconstructs the existing bootstrap record
shape and rejects released, deleting, malformed, missing or replaced objects.
It repeats Lease and controller observations before returning independent copies
in canonical order. Cross-object observation is not a transaction or a dispatch
grant. Live installation, source, actor and consumer checks remain required.

## Qualification and limits

Actual isolated Kubernetes API admission tests cover a two-serial cohort,
manager-only incremental publication, submission without references or with a
partial cohort, duplicate or foreign identities, malformed names/UIDs, immutable
original references even during partial acquisition, original
refusal, uncertain outcomes and acknowledged absence. Reference names and UIDs
in this actor test are staged; it does not create or verify corresponding Leases.
A separate controlled reader suite creates typed retained Lease fixtures and
covers replacement during reread, missing evidence, altered binding/worker,
release markers, changed annotations, stale controller state, duplicate UIDs and
copy isolation. This is fixture qualification, not an installed worker or native
PnP lifecycle. Existing bootstrap and manager-seal real API tests also pass.

The final API gate passes 74 test/package events. Seven shared packages pass
3333 events. The shared ND and direct-device upgrade regression suites, Catalyst Center
compatibility runs, main binary build and Helm render contract are recorded in
[results and source fingerprints](pnp-metadata-fence-references.json). The first
restricted foundation attempt failed because local test sockets were forbidden;
the final run used test listener access. The full Catalyst Center run also
exposed an obsolete parser rejection for explicit empty inventory tags. That
selection is intentionally supported by owned tag pruning. The obsolete case
was removed. A second stale test counted four history-bearing operation/receipt
embeddings, omitting the retained metadata operation. It now checks five and
compares the handoff and journal structural schemas, ignoring field-description
text only. The final inventory/tag-pruning/old-schema regressions pass 68
test/package events. The initial broad run passed 15112 events and retains both
failures in the evidence rather than being relabelled green.
No lab configuration or devices changed.

## Remaining execution work

The acquisition writer must create canonical marked fences, recover uncertain
creates without adopting replacement incarnations, and append each verified
original UID through the existing manager status CAS. An incomplete acquisition
must retain its recorded originals and cannot authorize native work. Runtime worker
claim/dispatch must consume the shared corroboration and complete source/actor/
consumer protections. Original native outcome verification, exact UID release,
lost-acknowledgement/restart recovery and manager release acknowledgement remain
pending. The compatibility callback stays unset and automatic activation remains
disabled. Installed/native lifecycle qualification, complete consumer protection,
configuration deletion/missing-source finalization and the wider pinned
93-resource execution/hardware gaps are not complete.
