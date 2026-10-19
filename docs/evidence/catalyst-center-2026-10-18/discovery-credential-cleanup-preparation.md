# Credential config cleanup: deletion-bound preparation

Branch `pr/johalley/catc-support`; pinned NetAsCode module **0.5.0**.
This stage reserves private Prepared cleanup work. It does not enable public
credential deletion, native Delete dispatch or manager finalizer release.

## Shared construct

The preparer repeats the existing complete native cleanup observation, loads the
current exact config/source/controller, and binds their identities and retained
status digest before journal reservation. Ordinary and credential preparers share
one final status-CAS/readback routine. No second journal, transport, finalizer,
Secret store, CRD field or operation-phase machine is added.

The operation reuses `discovery-finalize:v1:<deletion>:<spec>:<evidence>:<resource>`.
The inner resource is the existing creator-pair/model-bound credential prune
identity. The final wrapper stays below the existing 512-character resource bound.
Source UID/revision, ConfigUID/name/generation, controller generation and target
UUID remain in the shared operation fields. A private preparation marker is set
only by the typed preparer, not by returning an observation.

Typed cleanup envelopes reuse credential retirement parsing, receipt capacity and
finish-proof validation. Ordinary discovery selectors reserve malformed envelopes
but yield valid typed cleanup to the credential checks. Ordinary execution and
active-config retained recovery explicitly refuse typed finalization. Common
generic verification cannot mark it Verified, and journal finish requires the
typed retirement receipt. Native submission remains rejected, even with a
hand-constructed dispatchID, because this stage does not add typed Delete permits.

## Qualification

Eighteen new preparation scenarios run with controlled Kubernetes clients and
real local API persistence: inline/ConfigMap sources, active job, missing native
job, public drift, foreign UUID, wrong name, source/config/journal races, missing
projection, revoked owner, legacy receipt, missing pair, cleanup-family conflict,
future generation, foreign config name and unknown receipt family. Positive
cases verify exact deletion/spec/evidence/creator/source binding, unchanged audit
and owners, and zero native mutations. Repeated preparation cannot overwrite an
occupied slot or perform native reads. Ordinary cleanup, active recovery, generic
verification and proofless finish cannot consume or release the typed record.

The source-race fixture was rearmed for the preparer's new preflight. A stable,
valid source after an earlier rejected observation is eligible for a fresh
preflight; the test must change it during that new observation to test a race.
Production source-change guards were not relaxed.

## Remaining boundaries

Prepared work is retained; this is not cleanup execution or full configuration
parity. Next steps are reconstruction of deletion-bound work, one-use native
submission, original-task recovery and atomic retirement publication, followed
by worker completion and independent manager release. Public enrollment,
source-lost initial cleanup, mixed histories, installed actors and native device
credential mutation remain unqualified. Read-only lab checks do not qualify
native cleanup. The 93-resource coverage ledger and full pinned-model goal remain
open.

Adjacent JSON records exact source digests, gate results and logs.


## Recorded gates

The final relevant discovery/configuration suite passes **1,716 test events** in
**307.236 seconds**, with race detection and real Kubernetes API persistence.
This includes all 36 new preparation cases after the final shared-writer
consolidation. Shared contracts, ND, upgrade coordination, maintenance, SWIM/XFSU,
application build, strict documentation and read-only lab gates pass.

The frozen package contains 1,597 direct Go files, SHA256
`3f66fee04ff993745bac3895c099f67fe0b9870d83d4bc535cf1406920d0a2fb`.
Adjacent JSON records per-file fingerprints, the binary digest, exact regression
command and log paths. Read-only lab checks returned three devices and two images.
No native credential cleanup or installed-controller flow is qualified here.

## Follow-up

[Private cleanup execution](discovery-credential-cleanup-execution.md) adds the
source commitment and execution/recovery stages. This preparation evidence
retains its original snapshot, where native dispatch was disabled.
