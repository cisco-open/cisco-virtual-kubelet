# Opt-in metadata runtime composition

Catalyst Center metadata handoff is now wired through the existing manager,
isolated controller worker, admission binding and PnP journal. The new chart
value topology.controllerMetadataHandoff and manager flag
--enable-controller-metadata-handoff default to false. The feature requires the
existing managed-topology installation, leader election, isolated workers and
an explicit shared Lease namespace already matching the retained policy.
It does not require enabling read-write physical configuration reservations.
The physical admission binding and reservation protocol formats are unchanged.

Only Catalyst Center workers receive CISCO_VK_CONTROLLER_METADATA_PROTOCOL.
The setting participates in their existing worker contract hash. The adapter
descriptor advertises the protocol so an older connector binary cannot silently
accept the new worker contract. ND templates/factories are unchanged. Worker
setup supplies the existing concrete ConfigureMetadataHandoff hook only for the
explicit protocol; no new native client, transport or owner ledger was added.

## Live authority checks

Before new public cleanup preparation, and again after native read-only preflight,
the connector reobserves the current original worker chain and actual Pod-bound
caller plus installed execution authority. An old enabled process cannot rely
only on its startup setting after manager execution is disabled. Refusal before
preparation retains creators without creating an operation or native write.
The existing claim and native guards repeat runtime authorization later.

Manager authorization reuses the compiled managed-admission and worker-role
checks, actual SelfSubjectReview identity and administrator policy binding.
Complete namespace observations require one current ready manager Pod for the
exact manager account. Mixed/terminating manager rollouts, missing/duplicate
flags, incompatible policy/account/Lease inputs, unqualified containers or absent
runtime/image observations hold new work closed. The manager and connector must
resolve to the same image ID; this initial gate is not a heterogeneous image
compatibility assertion.

Connector authorization checks its current descriptor, explicit metadata mode,
unchanged physical installation binding and original Pod-bound credential.
The manager additionally observes recognizable active device-worker Pods across
the cluster, requiring known disjoint current device identities and qualified
managed worker envelopes. Missing/replaced sources, unresolved legacy actors,
late workers and source changes remain fenced. This complements the existing
device/Node/other-controller/fence census; it does not infer native exclusivity
from labels alone.

The shared observation-only network support role adds get namespaces, needed to
verify the fence namespace UID and active phase. No mutation or Secret access
was added. Matching chart/binary role contracts must roll out together, as with
other managed-profile updates; old/incomplete or broadened role shapes are
refused. The actual cross-namespace token test verifies this read and rejects a
Pod patch through the same role.

## Retention and settlement

Metadata custody preserves the original worker Deployment, bootstrap ConfigMap,
ServiceAccount and role bindings across in-flight/unknown phases and desired
image, pause, invalid-spec or deletion changes. Terminal custody requires
compatible manager settlement before normal reconciliation can replace those
objects. The existing group-worker retention behavior remains intact.

Manager terminal recovery is composed independently of enabling new handoffs.
It requires current bound manager authority and installed protection, but not a
live original worker. Turning off new execution is not a new native submission
permit. Generation/domain transitions, cancellation and deletion/source-lost
finalization remain unfinished; complete and settle existing work before changing
controller specs or manager/connector images. These guards do not manufacture
completion from an uncertain remote response.

## Qualification boundaries

Controlled tests exercise full composed callbacks with rendered native policy
contracts, typed storage, staged caller/readiness/compiler facts, worker image and
descriptor compatibility, role drift, disablement before journaling, authority
revocation during preflight and settlement after worker removal. These are not
installed-process proof.

A separate Kubernetes 1.35 API test creates real bound service-account tokens in
manager and tenant namespaces. Actual authenticated SelfSubjectReview claims,
cross-namespace manager observation and the fence namespace UID read pass. A
replacement Pod UID, Pod mutation and deleted-Pod credentials are denied. Pod
readiness/image status is staged because envtest runs no kubelet; this does not
claim that the manager and connector processes run in Kubernetes.

The final census/public-PnP real-API gate separately exercises native CEL/schema
and manager/Pod-bound worker phase enforcement with a controlled appliance.
[Machine-readable results and fingerprints](pnp-installed-runtime-wiring.json)
record final gates and the actual installed-run limitation.

## Installed native qualification remains pending

Docker's configured Unix socket accepted the local connection but returned no
response within bounded daemon-ping timeouts. The isolated
cvk-catc-qualification-20261017 cluster therefore could not be inspected or
upgraded. The hung read-only docker ps process was stopped; Docker, the stopped
main cluster, existing development cluster and backups were not restarted,
wiped or restored. A user availability question remains pending.

No installed new-mode mutation or fresh native PnP mutation is qualified by this
slice. Earlier private public-reconciler/native cycles remain separate evidence;
their simulated Kubernetes authority must not be attributed to these new runtime
hooks. Once Docker is available, the existing isolated cluster needs a matching
image/chart rollout, actual ready manager/connector proof, effective RBAC audit
and a full manifest-driven native cycle with exact generated-record cleanup.

Full pinned 93-resource execution/hardware parity, lifecycle transitions and
merge readiness remain incomplete. The new opt-in is experimental pending that
installed/native gate.
