# Manager metadata fence acquisition and recovery

AcquireMetadataFenceCohort now creates retained canonical bootstrap Leases and
appends each observed UID to the existing Prepared metadata handoff through a
status CAS. It requires current exact metadata and bootstrap protection contracts,
the sealed source/owner/cohort and worker incarnation, and a non-nil compatibility
callback. It preserves the operation and effect history and never invokes a
native API, submits an operation, settles a fence or grants dispatch.

The private acquisition path reuses the global identity census, source/owner
validation and worker observer. Its fence exception accepts only recorded exact
UIDs and, before the first UID publication, the exact next pending binding.
Other unrecorded keys, malformed/foreign/unmarked/released evidence and changed
worker/protection/source bindings hold acquisition closed. Public observation
continues to require absence and cannot adopt even this operation's own fences.

Each acknowledged or recovered fence is independently read and its UID retained
before progressing to another serial. Post-create denial or source/device drift
leaves that reference intact. A failed or uncertain status write is not retried
with a stale controller snapshot. A fresh read can recover a committed reference
or the exact next retained binding; recorded UID replacements are rejected.
An unrecorded original is identified by its full retained operation/source/
worker/protection binding before the first UID publication; this is not recovery
from arbitrary external deletion/recreation or admission bypass.

The optional manager route now seals, takes a fresh authoritative controller
snapshot, acquires the cohort and resumes it on subsequent reconciles. A matching
complete cohort is a no-op. Product projection remains pure; the manager creates
no concrete adapter or product client. The composition root supplies no
compatibility callback, so these writes remain disabled in normal deployment.

## Qualification

Controlled storage cases cover normal/idempotent acquisition, copied callback
arguments, missing/denied compatibility, changed policy/worker/source during
observation, foreign keys, denied or uncertain Create, same/foreign creation
races, malformed/missing original identity, post-create source/device drift,
status conflict/pruning/lost response and published UID replacement. The first
UID remains durable after a later prerequisite fails. Separate manager-route
fixtures cover first acquisition, lost Create response recovery and completed
cohort reconciliation without duplicate creation. Those route fixtures use typed
fake storage and rendered policies, not actual admission.

The real isolated Kubernetes API run uses server-assigned Lease UIDs, real
manager admission and status CAS. A client wrapper loses the first Create
response, then loses its UID-publication status response. Fresh snapshots recover
the same retained original, complete a two-serial cohort with two total Creates
and two total reference updates, and observe completion without new writes.
Enrollment and public absence checks remain held by retained custody. The exact
original operation remains Prepared with no submittedAt, native task, release or
fabricated effect receipt. This API test impersonates the manager with broad
RBAC to isolate admission. Worker readiness, background compiler status and
remaining installed-runtime compatibility are explicitly staged. No running
worker, native PnP removal or installed lab mutation is qualified.

An initial API test also completed acquisition but then encountered an older
malformed-Lease fixture after the newly installed strict policy. That fixture
now runs before admission installation, preserving both its negative observation
check and actual acquisition enforcement. Final gates pass 24 controlled acquisition, 74 real API, 22 manager-route and
1193 PnP/SWIM compatibility test/package events. Seven shared API, controller, ND
and upgrade packages pass 3358 events. Main build and source/format checks pass.
Exact source fingerprints are recorded in [qualification results](pnp-metadata-fence-acquisition.json).

## Remaining execution and parity gaps

Worker claim/dispatch still needs the complete live source/actor/consumer and
custody checks; a complete reference set is not a grant. Original native outcome
verification, certified-refusal/verified-absence settlement, exact UID deletion,
manager release acknowledgement and restart/uncertain-outcome recovery remain
pending. The composition-root callback must stay unset until the full deployed
manager/worker/enrollment/settlement contract is qualified. Public new pruning,
complete consumer protection, deletion/missing-source finalization,
installed/native full lifecycle and wider pinned 93-resource execution/hardware
gaps remain incomplete.
