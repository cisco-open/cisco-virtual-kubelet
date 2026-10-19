# Manager observation of unmanaged metadata scope

`CheckUnmanagedMetadataScope` adds a read-only manager prerequisite for a
source-bound Prepared metadata Delete. The adapter must independently supply the
complete validated creator serial cohort. This helper cannot establish creator
ownership or infer serials from omitted model content. It returns only an error,
not an attestation, grant, reservation, lease or native submission permit.

The existing bootstrap identity census now accepts a canonical serial set
internally. Its singular bootstrap caller retains the same behavior. Metadata
scope checking uses that shared census once per complete observation, rather
than listing all cluster resources once per serial. Two unfiltered observations
cover declared/retained CiscoDevice identities, Node machine/system identities
and other controllers' scalar/group physical work. The metadata prerequisite
also checks all retained configuration reservations, including settled or
deleting evidence, and every selected serial's existing bootstrap fence key.
Malformed fence evidence, missing list/Lease permission and invalid reservation
identity hold the observation closed.

The existing configuration source/owner binding brackets these observations;
current controller incarnation, revision, generation, spec and journal must
remain intact. Noncanonical/duplicate/empty serial sets, an unknown lease
namespace, physical operations, a task/submission or stale preparation are
rejected. The physical reservation digest, worker-grant and upgrade contracts
are unchanged. No CRD, runtime flag, status field or worker RBAC expansion is
introduced. Managers must supply their existing uncached reader and configured
lease namespace; namespace-scoped workers should not receive global access.

## Qualification

The 31 controlled cases cover both cohort members, foreign-tenant declarations,
retained identities, orphan Nodes, other scalar/group work, reservations,
malformed fences and late device/Node/reservation/fence/source changes. They also
exercise missing access and attempts to use this observer for physical or
already-submitted work. Input and journal snapshots remain unchanged.

The real isolated Kubernetes API test persists the controller and source, observes
two global censuses, then proves that an orphan Node alias, an orphan reservation,
a malformed bootstrap Lease and a newer source generation each refuse the
observation. Scope clearing after exact test-fixture removal is independently
checked. The bootstrap fence admission suite runs alongside it. No native
Catalyst Center request is part of this helper qualification.

The final gates pass 18 real-API/bootstrap admission test/package events,
3127 events across six foundation packages (including the 31 scope cases),
and 1032 selected PnP/journal/upgrade regression events. The main binary,
formatting, source fingerprints and 93-row ledger checks also pass.
[Results and source fingerprints](pnp-manager-scope.json) record the gates.
A separate read-only lab health check confirms token authentication and all three
switches Reachable at their previous versions; this is not qualification of a
new native admission path.

## Remaining integration

This helper is deliberately not wired as a production authorization result.
The manager still needs a request/actor binding to the exact validated PnP
creator cohort and selected preparation, plus serialization through the existing
lifecycle. Public new Delete dispatch stays disabled. The helper's observations
cannot lock cross-object state, exclude unknown hardware aliases or prevent
external appliance activity. Full consumer protection, CR deletion and missing
source context, manager finalization and installed native lifecycle qualification
remain pending. The full pinned 93-resource NetAsCode model is incomplete.
