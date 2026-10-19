# Credential discovery cleanup: shared metadata contract

Branch `pr/johalley/catc-support`; pinned NetAsCode module **0.5.0**.
This stage supplies the metadata-validation prerequisite for credential config
cleanup. It does **not** enable credential config deletion or claim full parity.

## Shared contract

`ValidateConfigContract` remains the mandatory pre-request gate. It first checks
shared metadata, then requires a clean absolute root and a regular projected file
for every authorized Secret reference. Adapter-specific projected-value/version
validation remains required. Missing, stale and nonregular projections do not
pass dispatch admission.

`ValidateConfigMetadataContract` reuses exactly the same internal metadata
validator and safe relative-path derivation, with no filesystem reads. It checks
controller/config validation, pause, namespace/controller binding, descriptor,
model version/format, managed sections, supported Secret uses, authorized aliases
and safe projection identities. It grants no new mutation authority.

Manager discovery enrollment and release now use the metadata entry point;
existing no-credential discovery shapes stay unchanged. Retained credential
completion also uses this gate on the **original** references, replacing its
previous copy with SecretRefs cleared. It then independently matches each
original operation version to its path, alias and authorized Secret name/key.
Valid current metadata cannot relabel an old operation after alias remapping.

## Qualification and boundaries

Shared contract tests exercise both entry points for each invalid metadata case.
Additional cases distinguish missing/nonregular projections and unsafe roots
from valid metadata. Credential recovery tests verify missing projection,
unauthorized alias, unsupported use, unmanaged section, invalid pointer,
controller mismatch, pause, model mismatch and original binding remapping. An
explicit enrollment refusal test keeps credential Delete policy unsupported.

The broader discovery/configuration suite includes public ordinary deletion and
source-lost acknowledged Create/Delete/prune recovery. Separate ND, shared
contracts, upgrade coordination, maintenance and SWIM/XFSU gates guard the
existing architecture. Adjacent JSON records results and exact source digests.
Native lab checks are read-only and do not qualify credential mutation.

Credential config-deletion execution, deletion-bound source authority, manager
completion/finalizer release, installed qualification and native credential
mutation remain pending. The 93-row pinned resource ledger is preserved; its
reporting and controlled tests are not claims of full deployment parity.


## Recorded gates

The relevant discovery/configuration regression passes **1,640 test events** in
**277.536 seconds**, with race detection and real Kubernetes API cases. Shared
contract, ND, upgrade-coordination, maintenance and SWIM/XFSU gates pass. The full
application build, strict documentation build and read-only native inventory
checks pass. No credential cleanup mutation or finalizer release was enabled.

The package snapshot contains 1,591 direct Go files, SHA256
`41a4255bd1b32e5439a148a92acae29aaba538963d78a7f28d7959b472085606`.
The adjacent JSON also fingerprints the shared contract source/tests, worker and
manager call sites, and complete application binary.
