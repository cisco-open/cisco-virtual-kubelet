# Credential config cleanup: private execution and recovery

Branch `pr/johalley/catc-support`; pinned NetAsCode module **0.5.0**.
This stage implements a private Delete/task/retirement flow. It does not enable
public enrollment or manager completion/finalizer release, or claim full parity.

## Execution through the existing foundation

Prepared work is reconstructed from the deleting config, complete retained
history, exclusive owner, exact source/projection and durable envelope. The owned
cohort is checked before and after the shared Prepared-to-Submitting CAS. The
final common live-permit, source, owner, baseline and capacity checks precede
one exact-UUID Delete through the existing no-replay transport. No native reads
follow that final guard. A local one-use closure cannot submit twice.

Accepted/known Unknown work polls the original task; it never calls Delete again.
Verifying reuses the retained retirement observer for the successful correlated
task, repeated UUID/name absence and unchanged journal. Publication reuses the
single atomic retirement/Verified writer. Verified requires that receipt and
further native absence before shared slot release. No creator, owner or delivered
credential application is replaced. A Delete never claims credential delivery.

## Source commitment and acknowledged completion

New credential records use the existing versioned cleanup envelope with v2:
`discovery-finalize:v2:<deletion>:<spec>:<evidence>:<source-proof>:<resource>`.
The source proof hashes only UID, revision and public model digest. No projected
credential value or value hash is retained. The inner resource stays the existing
creator-pair/model-bound prune identity. Ordinary discovery retains v1; unknown
versions stay reserved. Old v1 credential records were preparation-only and are
refused by the executor rather than implicitly upgraded. Safely unsubmitted
Prepared work needs retirement and fresh v2 preparation.

After acknowledgement, completion validates the original spec/deletion,
controller/owner, history, source commitment and exact task using the shared
metadata gate. Missing or changed ConfigMaps/projections do not require a fake
source snapshot. The bridge returns no executable source and authorizes no new
request. Prepared work still needs real source/projection validation. Changed
source identity fields, config or authority are refused. Initial source-lost
cleanup is still unsupported.

## Qualification and boundaries

Twenty-eight execution scenarios run with controlled Kubernetes clients and a
real local API server: inline/ConfigMap/active jobs, timeout recovery, lost permit,
accepted and receipt acknowledgements, rejected/pruned receipts, ambiguous/503
outcomes, failed/pending/foreign tasks, remaining/foreign native jobs, late native
or source changes, acknowledged source/projection loss and restoration, config
or source-identity changes, three post-permit races, and legacy/unknown versions.
Positive cases require exactly one Delete, unchanged credential/creator/owner
audit, exact retirement proof, and no replay on later invocations. Late permit
refusals require zero Deletes and a taskless Unknown fence. Pure preparation
still carries no dispatchID; the private executor sets it after fresh preflight.
The earlier stage's disabled typed-submission assertion was updated accordingly.

These are controlled native responses with real or controlled Kubernetes
persistence, not an installed controller or actual Catalyst Center credential
cleanup. Read-only lab checks establish health/inventory only. Actual device
CLI/SNMP credentials remain unavailable for native credential-bearing discovery.
Public enrollment, worker completion proof and independent manager finalizer
release, in-flight Create drain, mixed histories, initial source-loss cleanup,
installed/native qualification and wider pinned model gaps remain pending.
The 93-row resource ledger is preserved; full pinned-model parity is incomplete.

Adjacent JSON records the exact source fingerprint and gate results.


## Recorded gates

The final relevant discovery/configuration suite passes **1,774 test events** in
**339.588 seconds**, with race detection and real Kubernetes API persistence.
It includes all 56 new execution/recovery cases on the final source. Shared
contracts, ND, upgrade coordination, maintenance, SWIM/XFSU, application build,
strict documentation and read-only lab gates pass.

The frozen package contains 1,600 direct Go files, SHA256
`00f673fe917d764b335c1c7b0021bf5a0c791a5b8df2dbb8b8b1b294a26f8173`.
Adjacent JSON records per-file fingerprints, binary digest, command and log paths.
Read-only lab checks returned three devices and two images. Neither installed
actors nor actual Catalyst Center credential cleanup are qualified by these gates.

## Follow-up

[Worker completion and manager validation](discovery-credential-cleanup-completion.md)
adds attestation and already-held finalizer release. The flags above describe
this original private-execution source snapshot.
