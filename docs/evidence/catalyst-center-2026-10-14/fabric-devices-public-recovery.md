# Acknowledged bulk fabric-device recovery

The public configuration reconciler now recovers retained bulk fabric-device
Create operations. It uses the existing group journal, acknowledged reservation
reader, installed actor authorization and shared verification CAS. It does not
enable new public bulk submission or treat a completed Catalyst Center task as
proof of device convergence.

## Original incoming set

New resource identities use `fabric-devices-create:v2`, retaining the original
sorted incoming native device IDs separately from the complete physical group.
That group still includes existing peers and fabric-zone parent members. A peer
may itself have a declaration in the source; recovery must not mistake it for
an incoming member. The existing native desired/dependency digest, selected mask
and pre-existing membership UUIDs remain bound to the original operation.

The action validator accepts both identity versions. Legacy v1 readback remains
supported, but source reconstruction refuses a legacy group when declarations
for existing peers make the incoming selection ambiguous. It never guesses a
subset, rewrites an acknowledged identity or resubmits a native request.
Canonical ID order, count, uniqueness, syntax and the existing 512-character
resource limit are enforced. Oversized new identities are refused before
submission; a matching no-op needs no journal identity.

## Recovery boundary

Accepted and Unknown operations with a retained task require the exact source,
section owner, complete group identity, live claims/leases and authorization for
every member. The whole rooted task tree must finish successfully, all original
incoming memberships must match, and repeated native/managed observations must
preserve every physical target. Only then does the shared CAS move the retained
operation to Verifying.

Further reconciliation publishes `GroupConvergenceVerificationUnavailable`
and keeps the journal and every reservation. This deliberately records the
remaining implementation gap: an independent source-bound device convergence
verifier, final application receipt, member completion, settlement and retirement
are still required. Native task completion and membership readback do not release
claims or manufacture an application receipt.

## Tests and lab

Public recovery tests cover Accepted, Unknown, a separately declared peer,
running/failed task descendants, partial membership, revoked authority, changed
source and pruned group identity. Three simulated adapter restarts preserve one
native submission and all three claims, without application/completion/settlement
or release. These are HTTPS fixtures and a fake Kubernetes API with simulated
installed worker/session/lease evidence, not installed actor qualification.

Additional tests cover unambiguous and ambiguous legacy incoming reconstruction,
legacy native readback, malformed explicit IDs, duplicate/order/count errors and
an oversized identity. Existing preparation, dispatch and physical/managed
footprint cases remain covered.

Full race suites passed: Catalyst Center 275.814s and shared configuration
31.046s. See [full regression](fabric-devices-public-recovery-full-regression.log).
The final focused legacy/identity tests passed in 2.885s, including the isolated
otherwise-canonical oversized identity case. See
[identity checks](fabric-devices-public-recovery-identity-tests.log).
Strict documentation build and `git diff --check` passed.

The authenticated read-only lab check succeeded using the supplied credential
file. Fabric-site, multicast and multicast-VN inventories each returned HTTP 200
with zero rows. The prerequisite test explicitly skipped accepted execution
(0.31s, package 1.119s); this is not a successful live bulk mutation. See
[sanitized access](fabric-devices-public-recovery-live-access.log) and
[prerequisite result](fabric-devices-public-recovery-live-prerequisites.log).

## Remaining scope

Public source grouping/chunks/options and whole-model routing, independent device
proof and final lifecycle, installed manager/device admission, real API-server
schema persistence, mixed border-role cases and accepted live execution remain
pending. No full resource or section completion flag changes.
