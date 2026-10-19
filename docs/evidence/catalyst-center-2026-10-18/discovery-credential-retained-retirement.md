# Credential discovery: retained acknowledged retirement

Branch `pr/johalley/catc-support`; pinned NetAsCode module **0.5.0**.
This stage extends existing source attestation to acknowledged owned Delete/prune
operations. It does not authorize new deletion from a missing source.

## Execution and ownership

The existing worker attestation freezes config/controller UID and generation,
full spec identity, source UID/revision and model digest before submission.
Retirement additionally requires the exact original creator/credential pair,
queued version bindings for replacement, original target UUID and task. Terminal
prune requires explicit Delete policy and the original attested model digest.
Changed authority cannot be reconstructed from the current native inventory.

The retained observer performs no native mutation: it verifies the original task,
repeated complete native absence of the target and same-name replacements, all
retained ancestor absences, receipt capacity and an unchanged journal. Normal
and retained paths reuse one atomic retirement receipt publisher. A Delete never
publishes a credential application. Rejected/lost publication cannot replay it.

Recovered work remains Waiting. No queued successor or additional prune is
planned while the actual source/projection is missing. On restoration, ordinary
source attestation and full projection validation run before further planning.
A new ConfigMap UID does not rewrite the old operation's source identity.

## Qualification

Nine new scenarios each pass with controlled Kubernetes persistence and with a
real local Kubernetes API server: replacement projection loss, lost receipt
acknowledgement, rejected receipt publication, taskless outcome, remaining target,
foreign same-name job, failed task, partial-source pruning ConfigMap loss and
empty-source pruning ConfigMap loss. Positive cases hold the journal unchanged
for additional reconciles before restoring the source. They require retirement
without invented delivery or premature InSync and then complete the correctly
bound successor/remaining pruning without replay. Original audit records survive.
The targeted suite passes 94 test events, including 18 new source-loss cases.

This is controlled Catalyst Center behavior, not native credential mutation or
installed actor qualification. Read-only lab checks return three devices and two
SWIM images. Missing actual device CLI/SNMP credentials still prevent qualifying
credential-bearing discovery against hardware. API credentials alone do not prove
device authentication.

## Remaining boundaries

The config must remain present, active and unchanged. Config-deletion finalizers,
changed/deleting/absent-config recovery, mixed-family migration, installed actors,
native credential delivery/replacement/pruning and wider resource-model gaps
remain open. Prepared, Submitting and taskless uncertain requests are not replayed.
Full 93-resource parity is not claimed.

Recorded gate results and the exact source fingerprint are in the adjacent JSON.


## Recorded gates

The final relevant discovery/configuration regression passes **1,632 test events**
in **278.357 seconds**, with race detection and real API-server cases. The
94-event targeted replacement/prune suite passes in 128.463 seconds. Shared
controller contracts, Nexus Dashboard, software-upgrade coordination, maintenance,
SWIM/XFSU/reload/fallback tests, the full application build and strict documentation
build pass. Final-source read-only lab checks return three devices and two images.
No native credential mutation or installed-controller test is included.

The frozen package contains 1,590 direct Go files; fingerprint
`ac16920d1cef6243aea649f7607c91b81b626bcac3fb5305e13983f700e9796a`.
Adjacent JSON records commands, file digests and logs. Historical evidence retains
its original source snapshot and qualification boundaries.
