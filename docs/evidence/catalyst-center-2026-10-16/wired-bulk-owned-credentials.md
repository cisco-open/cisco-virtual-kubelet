# Owned credential definitions as private bulk prerequisites

Bulk source definitions now compose with the existing network-settings credential
owner. The inventory consumer does not create credentials, duplicate secret
projection, transfer ownership or treat masked API fields as applied values.
A separate network-settings Apply config must already own and reconcile the
exact definition. Missing ownership or application receipts remain fenced.

The adapter checks one current network-settings owner, its live config UID,
controller/namespace, Apply mode, current observed generation and InSync phase.
It compiles the original owner source using the existing credential splitter and
compares named definitions and semantic credential leaves. Versioned definitions
must have the existing task-backed UUID/version application receipt with matching
owner, resource, target, operation ID and complete canonical leaf set. Receipt
source aliases and Secret name/key provenance must still match the current owner
refs and controller allow-list. No Secret API object or projected secret file is
read. All six credential types are supported by this prerequisite contract.

Metadata-only NOAUTHNOPRIV uses the existing atomic action receipt and exact
metadata intent identity, not a fabricated write-only version. A valid older
receipt may continue to prove unchanged metadata after unrelated owner revisions;
future generations and changed metadata are refused. Creator/application receipt
semantics remain owned by the existing credential implementation.

The native UUID and desired public metadata must match in addition to the owned
receipt. The full typed public native metadata is retained in the private digest,
including undeclared fields, so an in-flight operation detects their changes.
Two owner/native observations bracket the existing allocation/external-assignment
dependency observer. The combined digest uses the existing private v2 bulk resource
identity. Original v1 and external/allocation-only v2 identities are unchanged.
Owner status heartbeat resourceVersion is deliberately excluded; owner UID,
generation, source UID/version, applied receipt and provenance remain bound.

The adapter factory is wired through original-source planning, dispatch,
revalidation, retained recovery, device evidence collection and final publication.
A rotated/replaced/pruned owner receipt invalidates the original operation at all
boundaries. This is prerequisite composition, not automatic credential rotation
from inventory or complete device authentication proof. Public bulk execution
remains gated and no common CRD, ND, direct-device or SWIM structure was changed.

## Local evidence

- [Bulk regression run](wired-bulk-owned-credentials-regression.log): all affected
  bulk tests passed, including original versioned owner compositions.
- [Ownership and boundary tests](wired-bulk-owned-credentials-fences.log): current,
  missing/duplicate/replaced owner, unreconciled definition, missing/changed
  version receipt, revoked or changed provenance, replaced native UUID/metadata,
  heartbeat stability, metadata-only receipts and older unchanged metadata all
  passed. Receipt replacement and owner loss after proof/final authorization
  preserve the original operation fence and prevent publication.
- [Twenty shared lifecycle compositions](wired-bulk-owned-credentials-cycles.log):
  versioned and metadata-only prerequisites both pass provision/reprovision,
  one-use dispatch, accepted-task recovery, separate proof, atomic publication,
  actor settlement, finish and restart no replay. Metadata-only owner receipts
  survive inventory receipt publication unchanged.
- [Shared foundations](wired-bulk-owned-credentials-foundations.log): adapter
  registration, ND, common configuration reservations, software lifecycle and
  upgrades passed.

Kubernetes ownership, manager/device actors and independent device proof in these
runs are simulated. They do not prove installed workers or physical provisioning.
Native qualification is recorded separately below. All parity flags remain partial.

Remaining work includes installed credential-owner/consumer recovery, accepted physical bulk provisioning and
complete device convergence, public execution routing and broader resource parity.
The installed cluster API-only recovery still awaits the separate pending approval.

## Native evidence and final regression

The [six-type native run](wired-bulk-owned-credentials-native.log) passed in
145.41 seconds. Each generated credential completed native create, rotation,
assignment/isolated slot cleanup, UUID/metadata composition with its persisted
version descriptor, and changed-receipt refusal. All six generated credentials
and the generated empty area were verified removed. Original credential baseline
identities were retained. Ownership and version status objects remain simulated
Kubernetes actors; producer task completion/native readback are real. No installed
worker, physical provisioning or device authentication proof is claimed.

A later digest refinement retains the full typed public native metadata as well
as desired-field equality. The [final bulk regression suite](wired-bulk-owned-credentials-final.log)
passes in 61.164 seconds, covering twenty lifecycle cases, nineteen ownership
prerequisite cases and twenty lifecycle-fence cases. Changes to unmanaged public
metadata alter the identity; a receipt change during collection is refused.
The [final native SNMPv3 run](wired-bulk-owned-credentials-native-final.log) against that exact refinement also passed in 29.81 seconds, including unchanged proof, changed-receipt refusal, and verified generated credential/area removal.

Bulk definitions are dependency assertions against an already applied,
independently owned network-settings manifest. Inventory cannot create or rotate
those credentials. Definitions without their existing applied-version/metadata
receipt remain refused; native metadata alone does not close this gate. No Secret
values are copied into the bulk source, operation, digest or evidence.

Subsequent [real API and native metadata qualification](wired-bulk-owned-credential-qualification.md) now closes the bounded metadata-only composition gap, including a combined real private API/native producer and consumer run. Installed workers and physical bulk execution remain unqualified.

Subsequent [combined versioned qualification](wired-bulk-owned-versioned-qualification.md) now exercises the public producer, real API receipts and native dependency consumer together for all six types. The earlier separate-fixture qualification limits remain historical; installed actors, physical device convergence and public bulk routing remain pending.
