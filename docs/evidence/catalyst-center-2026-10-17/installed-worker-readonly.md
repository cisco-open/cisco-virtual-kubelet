# Installed Catalyst Center worker: startup, persistence and source watches

A separate `cvk-catc-qualification-20261017` kind cluster runs Kubernetes 1.35.0
using an immutable ARM64 node-image digest and a private kubeconfig. The stopped
main cluster and the existing development cluster were not changed. The current
branch binary was built into a local nonroot image and installed with the normal
Helm chart, strict RBAC and topology disabled. No CiscoDevice, physical operation
or mutating controller configuration was submitted by this qualification.

## Reproduced startup failure and correction

The first real manager exited before worker creation: default-off startup checks
for retained configuration reservations, but the generated base manager role
lacked permission to list them. The runtime correctly refused to treat Forbidden
as evidence of absence.

The existing reservation RBAC marker now requests only `get;list` in the base
role. The normal pinned `make rbac-gen` regenerated the chart. Comparing against
the installed original role proves exactly those two grants were added, with no
other grant changed. Mutation/watch/status authority remains in the existing
separately installed or retained managed-topology manager role. Runtime startup,
retirement logic and other provider execution paths were not changed.

The [rendered-chart regression and related RBAC race tests](installed-worker-rbac-tests.log)
pass in **4.386 s**. The new regression covers default and strict profiles with
topology disabled, requires unscoped reservation inspection and rejects mutation
or status authority. [Generation log](installed-worker-rbac-generation.log) and
[exact permission delta](installed-worker-rbac-delta.json) retain the evidence.

## Real worker qualification

After the Helm correction, the manager and its isolated Catalyst Center worker
both reached Ready. The [sanitized connection proof](installed-worker-connection.json) records the endpoint reporting `Authenticated=True` and
`APICompatible=True` using the authorized credential Secret projected read-only
at the existing `/var/run/secrets/cisco-vk/controller/credentials` path. Worker
credential-loading and bootstrap contracts were not relaxed.

A ConfigMap-backed, pinned-model read-only site intent reported one deliberately
absent generated area as Add drift. Replacing the worker Pod produced a distinct
Pod UID, retained the same configuration UID/spec/drift, and performed a fresh
successful controller check. The original Pod identity is corroborated by
retained Kubernetes events; the [restart audit](installed-worker-restart-audit.json)
records that boundary. Changing the ConfigMap to declare a second absent area
then produced both expected drift entries through the real installed worker.
Neither area was created.

All ten [effective authorization checks](installed-worker-effective-rbac-audit.json)
pass. The default-off manager can inspect reservations but cannot create, update
or delete them. The worker can read ConfigMaps and patch configuration status,
but cannot read Secrets, patch intent, or update device mutation leases.

[Validation summary](installed-worker-validation.json) and [image/source/cluster metadata](installed-worker-qualification.json)
identify the result. The binary predates the subsequent marker/comment-only
source edit; its runtime code is unchanged, and RBAC was regenerated and upgraded
separately. The private cluster remains available for subsequent qualification.
Credential values, kubeconfig and raw Secret objects are not published in evidence.

## Remaining boundary

This closes the installed startup/read-only source-watch/restart gap. It does
not attest installed Apply ownership, asynchronous task recovery, private intent
key projection/rotation, physical reservation actors or device convergence.
Those flows and full NetAsCode resource parity remain unfinished. The parallel
wireless metadata lab harness still qualifies a separate native lifecycle; its
simulated Kubernetes actors must not be attributed to this installed result.

The subsequent private-key preparation confirmed demand-driven projection:
an allow-list entry alone creates no volume; a referencing configuration adds
the normal read-only intent-secret projection. The attempted Report-mode
prerequisite was refused with `UnsupportedConfiguration`, because the existing
report contract accepts no secret references. This diagnostic probe did not
submit an Apply or native mutation and does not qualify private ownership. Its
[retained preparation result](installed-private-key-report-refusal.json) separates
projection readiness from execution proof. The existing private-site Apply
contract is the next qualification step; no validation gate was relaxed.

Subsequent [installed private-site qualification](installed-private-sites.md)
now separately passes real area/building Create, building Update, settled Pod
replacement and original acknowledged-Update process recovery. The earlier
read-only/private-key-refusal snapshots above retain their original boundaries.
