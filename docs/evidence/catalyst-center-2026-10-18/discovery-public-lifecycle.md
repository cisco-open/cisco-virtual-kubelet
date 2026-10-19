# Public discovery config deletion lifecycle

Resolved pure-discovery inventory Apply can now opt into `deletionPolicy: Delete`.
The manager enrolls the independent discovery finalizer before mutation. The
worker uses the existing journal, retained ownership, original-task drain, exact
native delete, absence receipt and completion proof. The manager alone removes
its completed finalizer. See the [operator guide](../../controllers/catalyst-center-discovery-deletion.md).

## Common architecture and compatibility

The registry adds an optional list of additional configuration lifecycle policies.
The existing single policy/accessor and feature finalizer remain valid and unchanged.
Policies are copied, validated and checked for duplicate finalizer keys. The common
manager enumerates the policies, performs uncached reads and optimistic metadata
patches, and refuses conflicting enrolled families. It never constructs a product
client. No ND policy, transport, reservation, secret projection or worker RBAC is
expanded. The worker still cannot patch config metadata/finalizers.

Discovery enrollment is based on the resolved Apply/section/policy contract. The
worker validates the entire inline or ConfigMap model before any mutation; mixed
or unsupported sources do not fall back to general inventory execution. Deletion
routes retained pre-deletion work to the observation-only drain and finalization
work to the existing deletion-bound protocol. Empty/unowned completion preserves
unowned native jobs. Source absence, uncertainty, failed tasks, drift and ambiguous
families retain their finalizers and journal evidence. Accepted task expiry follows the common Apply timeout contract: Unknown retains
the original task for observation without resubmission. Completion leaves Ready false
while the manager finalizes. Ownership and deletion tombstones are retained.

## Validation

Fourteen public scenarios exercise owned cleanup, manager-enrolled creation,
accepted-create drain, ConfigMap creation, unsubmitted preparation retirement,
empty/unowned completion, lost acknowledgement, native drift, missing/unsupported
source, foreign operation, family conflict and configured task expiry. They enter the actual manager and
worker reconcilers, using a worker client that forbids metadata writes. Real API
qualification sends CRD status CAS and optimistic finalizer patches to Kubernetes;
only the manager's cache field indexes are emulated by a test List wrapper.

Registry/controller tests cover additive policy copying/validation, legacy behavior,
additional-family enrollment/release, stale proof and conflicting finalizers. Full
registry/controller, Catalyst Center (including SWIM), ND, software upgrade and
direct GNOI results are tracked in the validation JSON and logs.

The native run enrolled the manager finalizer **before creating discovery 188**
(create task `01a14ce7-61fb-7a9f-80ff-d7c52df17dc0`). Public worker deletion submitted
one exact delete (task `01a14ce7-d56d-7359-85cd-faf0b3b225e5`), recovered an injected
known-task Unknown, retained the absence receipt and published completion. The
manager removed only the discovery finalizer, leaving the qualification keeper,
owner and tombstone intact. Exactly one create and one delete occurred; the full
discovery baseline and device inventory identities were preserved. All Kubernetes
persistence and adapter reconstruction were simulated; native controller APIs were
real. The log's controller-runtime logger-initialization warning is a harness log
setup notice; the run passed and exact cleanup completed.

## Full-suite qualification status

The first full non-live suite found an outdated capability assertion (corrected)
and reached the ten-minute cumulative package timeout while the active test was
only seconds old. The timeout-contract follow-up passes all 14 focused/API public
scenarios and a new native run. The full public-stage audit completed in about 28 minutes with failures in
example compilation and site-bulk families. Those failures were repaired with a
focused passing gate. The newer retained-source/current-tree run is still active;
CI budgets and validation are recorded in the
[follow-up evidence](discovery-retained-source.md). No full-suite pass is claimed.

The existing isolated installed qualification cluster was inspected read-only:
controller Ready, idle journal, two InSync configs, old credential-recovery image,
no CiscoDevices. No deployed resources or Secrets were changed.

## Remaining scope

Installed manager/worker process and RBAC qualification remains pending. Automatic
ownership retirement/handoff to a replacement config UID is not implemented; a
successful config deletion does not release the retained section owner. Missing-
source recovery, paused/controller-change policy, mixed/grouped and legacy cleanup
remain pending. This does not establish whole-model/default/inline/null support or
full 93-resource execution parity.

Evidence: [source/native/gate record](discovery-public-lifecycle-validation.json),
[targeted tests](discovery-public-lifecycle-tests.log),
[public API qualification](discovery-public-lifecycle-api.log),
[full registry/controller](discovery-public-lifecycle-foundation.log),
[initial full-suite failure](discovery-public-lifecycle-adapter-initial.log),
[full-suite progress snapshot](discovery-public-lifecycle-adapter-final.jsonl),
[final public API](discovery-public-lifecycle-api-final.log),
[final focused cases](discovery-public-lifecycle-local-final.log),
[ND/upgrades/GNOI](discovery-public-lifecycle-nd-upgrades-gnoi.log),
[final native public lifecycle](discovery-public-lifecycle-native-final.log),
[final production build](discovery-public-lifecycle-build-final.log).
