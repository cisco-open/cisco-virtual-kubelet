# Credential config cleanup: owned deletion preflight

Branch `pr/johalley/catc-support`; pinned NetAsCode module **0.5.0**.
This stage implements a private, read-only preparation prerequisite. Public
credential config deletion, Delete dispatch and finalizer release remain disabled.

## Shared execution construct

Source pruning and config-cleanup observation now share one complete-cohort
selector. A set of desired names preserves ordinary source-removal semantics;
an empty set selects every non-retired owned creator for config cleanup. The
selector validates retained ancestor absence, exact UUID/name correspondence,
public field fingerprints and non-legacy ownership pairs, then sorts candidates.
Unrelated native names are never adopted. Active owned jobs remain eligible,
consistent with the existing exact-ID discovery stop/remove API.

The cleanup observer uses the existing `discoveryConfigCleanupObservation`
envelope. It binds the exact deleting config UID/generation/resourceVersion,
spec and deletion timestamp, controller identity, source UID/revision/model and
full journal digest. Only pure credential discovery sources with a valid current
projection, complete paired history, exclusive inventory ownership and no active
operation are accepted. Ordinary/credential mixed histories remain unsupported.

Projection validation uses an observation-only config copy without the deletion
timestamp. The spec and all Secret references stay unchanged; full mounted-file
and versioned-value validation still run. No payload builder or executable plan
is returned. Native inventory and all Kubernetes/source bindings are checked
again around two complete native reads. No status, owner, receipt or finalizer
is written by the observer.

Candidates retain the existing creator-pair and source-model prune identity.
They do not satisfy the existing ordinary cleanup preparation/submission shape.
This intentionally separates observed ownership from a destructive permit; no
new journal format, operation phases, transport, finalizer or CRD is introduced.

## Qualification

Eighteen scenarios pass with both controlled Kubernetes clients and real local
API persistence: inline/ConfigMap sources, active native job, absent native job,
public drift, foreign UUID, wrong name, source/config/journal races, missing
projection, revoked owner, legacy public receipt, missing pair, cleanup-family
conflict, future generation, foreign config name and unknown receipt family.
Positive cases require exactly two native inventory reads, exact bound candidate
identity, unchanged journal/finalizers and zero native mutations. A candidate is
explicitly rejected by the existing dispatch preparation gate.

The first controlled fixture attempted to set Kubernetes' immutable deletion
timestamp. It was corrected to use the ordinary Delete API in both persistence
modes. This was a fixture error, not a reason to relax production admission.
Existing public source-pruning tests also pass after extracting the shared selector.

## Remaining boundaries

This is not config-cleanup execution or reporting parity. The worker still needs
deletion-bound preparation/one-use dispatch, original-task recovery and completion
attestation. The manager must independently validate that completion before
finalizer release. Source-lost initial cleanup, in-flight deletion drain, mixed
histories, installed actors and native credential mutation remain unqualified.
The current source and real projected device credentials are required here;
API login credentials do not establish device authentication. The 93-resource
ledger remains explicit and full pinned-model parity is still incomplete.

The adjacent JSON records source fingerprints, gate results and logs. Lab reads
qualify connectivity and inventory only, not cleanup mutations.


## Recorded gates

The final relevant discovery/configuration suite passes **1,678 test events** in
**293.558 seconds** with race detection and real Kubernetes API persistence.
The 61-event targeted suite passes in 33.307 seconds, including 36 new observer
cases. Shared contracts, ND, upgrade coordination, maintenance, SWIM/XFSU,
application build, strict documentation and read-only lab gates pass.

The frozen package contains 1,594 direct Go files, SHA256
`3b759547471303d2489948c4b13b54217e42d51d48a5c4cb733475633aadceb5`.
Adjacent JSON records file fingerprints, binary digest, commands and log paths.
Read-only native checks returned three devices and two images; native credential
cleanup and installed actors remain unqualified.

## Follow-up

[Deletion-bound preparation](discovery-credential-cleanup-preparation.md) extends
this observation stage. The flags and gates above retain their original source
snapshot and do not claim that preparation was enabled at that time.
