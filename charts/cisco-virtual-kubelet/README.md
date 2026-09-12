# Cisco Virtual Kubelet Helm chart

The chart installs the CVK manager, its CRDs, and the RBAC used by legacy and
per-device workers. `topology.enabled` adds an opt-in, manager-owned topology
and IOS-XE rollout safety boundary. It is disabled by default and does not
change standalone or existing secure-gNOI behavior until explicitly enabled.

## Managed topology prerequisites

Managed topology currently requires all of the following:

- Kubernetes 1.35 or newer. There is no `v1beta1` or webhook fallback.
- `aggregator.enabled=false`; every selected CiscoDevice needs its own
  identity-bound worker.
- `controller.leaderElect=true`; only the elected manager reconciles topology
  and rollout authority. Election reduces overlap, while the ledger/CAS and
  admission protocols remain the durable safety boundary across failover.
- `serviceAccount.vkName=cisco-virtual-kubelet`; managed mode intentionally
  combines fixed managed or legacy worker roles with a namespaced binding to
  `cisco-virtual-kubelet-device`. Topology-generated ServiceAccount names are
  UID-derived; custom names for the fixed role contract are outside this
  roadmap.
- Every resolved virtual Node name is a DNS subdomain of at most 63 bytes in
  managed mode. Dotted names remain valid; legacy CiscoDevice names longer
  than 63 bytes need an explicit short `spec.nodeName` before enrollment.
- `rbac.profile=strict`; disabled write-class controllers must not leave their
  namespaced API permissions on managed workers.
- `gnoi.enableWriteClass=false`; the Phase 2 managed maintenance protocol
  supports campaign-owned `IOSXESoftwareUpgrade` only, not generic
  `IOSXEOperationalAction` mutations.
- Exactly one authoritative managed-topology CVK release in the cluster.
- The CRDs from this chart applied before the manager is upgraded.
- The chart's `ValidatingAdmissionPolicy` objects and bindings installed,
  structurally verified at contract version `v1`, free of reported expression
  warnings, and proven by live API-server requests to deny worker mutations.
  Kubernetes does not type-check these expressions against matched CRD schemas,
  so an empty warning list is not sufficient qualification.
- Stable, non-empty required topology labels on every selected
  `CiscoDevice.metadata.labels`.
- An explicit `spec.maxPods` from 1 through 110 on every selected device.

The managed bound makes scheduler capacity intentional. Topology-disabled
standalone devices retain the compatibility behavior in which non-positive
values fall back to 16 and other existing `int32` values remain accepted.

Helm rejects an enabled render for an older Kubernetes version or aggregator
mode. The manager performs discovery and admission preflight again at startup;
the version gate alone is not considered proof that enforcement is active.

## Configuration

This conservative example opts in only CiscoDevices bearing
`topology.cisco.vk/managed=true`, projects region and zone to Nodes, and admits
only one transfer and one unavailable device in the configured domains:

```yaml
aggregator:
  enabled: false

controller:
  leaderElect: true

rbac:
  profile: strict

topology:
  enabled: true
  policy:
    namespace: "" # release namespace
    name: ""      # <fullname>-topology-policy
    fleetSelector:
      matchLabels:
        topology.cisco.vk/managed: "true"
      matchExpressions: []
    requiredTopologyKeys:
      - topology.kubernetes.io/region
      - topology.kubernetes.io/zone
    projectedTopologyKeys:
      - topology.kubernetes.io/region
      - topology.kubernetes.io/zone
    globalMaxConcurrentTransfers: 1
    globalMaxUnavailable: 1
    domainMaxConcurrentTransfers:
      topology.kubernetes.io/region: 1
    domainMaxUnavailable:
      topology.kubernetes.io/zone: 1
    healthFreshnessSeconds: 300
    maxCampaignTargets: 100
    maxActiveReservations: 256
    maxLedgerBytes: 262144
  ledger:
    name: ""      # <fullname>-topology-ledger
```

Every domain-budget key must also appear in `requiredTopologyKeys`, and every
projected key must be required. `operations.cisco.vk/*` keys may define
administrator risk domains, but cannot be projected as scheduler-visible Node
topology; projection accepts only region, zone, and `topology.cisco.vk/*`.
Campaign limits may tighten the administrator ceilings above; they cannot
loosen them. The policy selector must be non-empty and every selector key must
live under `topology.cisco.vk/*`; those are the enrollment labels protected by
native admission from ordinary CiscoDevice editors.

Every software-rollout target must also carry the protected,
low-cardinality `operations.cisco.vk/qualification-cohort` label describing
its lab-qualified hardware/capability class. The rollout must provide a
same-named canary cohort with at least one explicit target for every distinct
value. Cohort membership is frozen and revalidated; one hardware class never
implicitly qualifies another, and incompatible image sets require separate
qualified campaigns.

Every selected CiscoDevice must declare a verified, fleet-unique
`spec.physicalIdentity` such as a chassis serial or hardware UUID. The field is
write-once and is the case-insensitive rollout ledger/deduplication authority;
an older object's absent value may be populated before enrollment, after which
admission rejects removal or replacement. Node names, UIDs,
or worker-reported inventory cannot substitute for it. The per-device worker
does provide live device and Node health evidence, which a compromised worker
could falsify; that evidence is a freshness/consistency signal and cannot
rewrite declared identity, protected topology, policy, or ledger authority.
The initialization scheduling guard remains until the spec declaration,
canonical manager status binding, and live authenticated worker observation
all agree.

The production scheduling baseline is ordinary Node affinity and topology
spread. The example includes a `policy/v1` PodDisruptionBudget for availability
intent, but Phase 2 uses `BlockIfRunning` and neither evicts Pods nor consumes
the PDB; eviction-aware drain is deferred to Phase 4. A separate Kubernetes
1.37-only experimental Workload/PodGroup/TAS example lives under
`examples/topology/`; it requires disabled-by-default in-tree feature gates and
is not installed or enabled by this chart.

The chart renders the complete policy as one coherent `policy.json` value. It
renders `ledger.json` empty and never invents a UID. On startup, the manager:

1. reads the live policy ConfigMap and validates its complete schema and
   non-empty fleet selector before any ledger write;
2. uses its protected `topology.cisco.vk/admission-policy-prefix` annotation to
   verify the chart's exact eight admission policies and bindings;
3. reads and initializes the ledger with the ledger ConfigMap's real
   Kubernetes UID; and
4. adds that UID to the policy's protected
   `topology.cisco.vk/ledger-uid` annotation.

Once bound, a missing, empty, or recreated ledger is an identity failure. CVK
does not silently initialize new admission authority over in-flight work.
The policy, ledger, eight admission policy/binding pairs, and fixed managed
worker role carry `helm.sh/resource-policy: keep`. A values rollback,
`topology.enabled=false`, or `helm uninstall` therefore cannot silently erase
that safety state; retained objects require a separate audited retirement.

Topology mode never uses the release-wide worker identity as its steady state.
Each selected device gets `cisco-vk-managed-<node>-<device-uid-hash>` and the narrow
managed role. Each unselected device gets
`cisco-vk-legacy-<node>-<device-uid-hash>` and retains the legacy runtime/role;
native admission still confines its Node and Pod-status writes to the embedded
unmarked Node. A fresh topology-enabled install omits the shared worker
ClusterRoleBinding and RoleBinding. During an upgrade, Helm `lookup` retains
only exact preexisting shared bindings, marks them
`topology.cisco.vk/retire-shared-worker-access=rollout-v1`, and protects them
from a premature Helm delete. The manager removes that bridge only after no
Deployment, ReplicaSet, or Pod uses the shared ServiceAccount and all
per-device replacement bindings have passed their audit. It globally defers
first managed admission until retirement; replacement workers remain isolated
per-device legacy identities during the handoff. ReplicaSet read/delete is
granted only by the retained managed-topology manager role, solely for exact,
owner-verified, zero-replica retirement; the default controller role does not
carry it.

The manager writes the immutable
`topology.cisco.vk/isolated-legacy-worker=<CiscoDevice UID>` marker only after
auditing the exact generated ServiceAccount and bindings. Admission denies a
user-supplied marker even before Node identity exists.

For managed workers, the manager hashes the desired PodTemplate after removing
only the hash's own carrier annotation/environment variable. Secret
resourceVersions are included through the existing rollout-trigger
annotations, while Secret bytes are not. It injects the digest as
`CISCO_VK_WORKER_CONFIG_REVISION`; the worker echoes it through its status-only
Node writer. `CiscoDevice.status.workerRevision` is ready only when the exact
owned Deployment has completed, one non-terminating owned Pod is Ready, and a
matching Node heartbeat occurred after that Pod started. Credential or gNOI
trust rotation therefore gates new gNOI work until the `Recreate` replacement
proves it loaded the desired inputs.

Phase 2 health does not claim end-to-end forwarding validation. It gates on a
fresh bound Node heartbeat, manager-owned identity/topology/gNOI configuration
evidence, and the leaf lifecycle result; packet forwarding, expected
interfaces/routing adjacencies, and complete stack/supervisor role health still
need qualified manual evidence until typed IOS-XE producers exist.

Topology projection can be enabled without activating device software
mutation. To execute rollout-created gNOI leaves, retain the existing explicit
worker gate as well:

```yaml
gnoi:
  disabled: false
  enableSoftwareUpgrade: true
```

`topology.enabled` deliberately does not imply this mutation permission.
The rollout controller and its manager-side campaign/leaf mutation RBAC remain
inactive until both gates above are enabled. Read-only leaf observation stays
active so disabling new upgrades cannot erase an unsettled-operation fence.
The chart rejects `gnoi.enableWriteClass=true` in managed mode: generic
`IOSXEOperationalAction` has not yet been integrated with the managed campaign
reservation, claim, and maintenance-session protocol.
On a selected managed device, only manager-created upgrade leaves bearing the
complete managed identity and reservation bindings are supported. Direct or
unmanaged `IOSXESoftwareUpgrade` objects are not executed or modified by its
managed worker; submit an `IOSXESoftwareRollout` instead.

## Install or upgrade

Helm does not upgrade CRDs from a chart's `crds/` directory. Apply the new CRDs
before rolling the manager. For an existing installation, back up the live
definitions, review the exact diff, and explicitly take ownership of only the
checked-in CVK CRD files before installing with a values file:

```bash
kubectl get customresourcedefinitions.apiextensions.k8s.io -o yaml \
  > cvk-crds-before-upgrade.yaml
kubectl diff --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade \
  -f charts/cisco-virtual-kubelet/crds/
kubectl apply --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade \
  -f charts/cisco-virtual-kubelet/crds/

helm upgrade --install cvk charts/cisco-virtual-kubelet \
  --namespace cisco-vk-system \
  --create-namespace \
  --values managed-topology-values.yaml
```

`kubectl diff` returns status 1 when changes are present. Helm owns fields from
the initial CRD install, so plain server-side apply can report a managed-fields
conflict. The reviewed force flag is scoped to the exact `-f` objects; never
point it at an unrelated manifest directory.

Enabling topology on an existing release must use a live `helm upgrade` so
`lookup` can retain the exact shared worker bindings until their users are
quiescent. Offline `helm template` output and GitOps pruning cannot enumerate
that live, cross-namespace authority and are unsupported for the migration
unless existing CVK RBAC is excluded from prune through controller-reported
retirement completion.

Do not use `helm upgrade --force` for a managed-topology release. Replacing the
policy or ledger changes Kubernetes object identity and intentionally freezes
new admission. A normal three-way Helm upgrade leaves the manager-populated
ledger data and live UID annotation alone because the chart's desired bootstrap
fields remain empty/absent.

Before enabling workers, inspect admission type checking and bindings:

```bash
kubectl get validatingadmissionpolicies.admissionregistration.k8s.io \
  -l app.kubernetes.io/instance=cvk

kubectl get validatingadmissionpolicybindings.admissionregistration.k8s.io \
  -l app.kubernetes.io/instance=cvk
```

Then print the type-check warnings. Any output must block enablement:

```bash
kubectl get validatingadmissionpolicies.admissionregistration.k8s.io \
  -l app.kubernetes.io/instance=cvk \
  -o jsonpath='{range .items[*].status.typeChecking.expressionWarnings[*]}{.fieldRef}{": "}{.warning}{"\n"}{end}'
```

No output is necessary but not sufficient: Kubernetes does not type-check VAP
expressions against CRD schemas. Before production enrollment, exercise the
installed policies through the real API server and prove at least that the
bound worker cannot change Node labels/spec/binding annotations through
`nodes/status`, cannot change manager-owned leaf status, and can only add or
remove the exact IOS-XE upgrade cleanup finalizer. Prove that an older worker
cannot persist a durable device-mutation marker without the matching
reservation/control-bound claim. Also prove that an unbound worker cannot
update or delete any retained managed Lease, that the bound worker cannot
change its device/Node/worker bindings, and that an approver without the
planner role cannot change campaign control. Prove Pod-status writes succeed
only for Pods bound to the virtual Node encoded in either generated worker
identity. Finally, prove a managed worker cannot write an unmarked Node or
unmanaged/peer leaf, a generated legacy worker cannot write a peer or managed
Node, and the retired shared ServiceAccount has no binding.

Verify the live UID binding and the initialized ledger without rewriting them:

```bash
kubectl get configmap cvk-cisco-virtual-kubelet-topology-policy \
  --namespace cisco-vk-system \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/ledger-uid}{"\n"}'

kubectl get configmap cvk-cisco-virtual-kubelet-topology-ledger \
  --namespace cisco-vk-system \
  -o jsonpath='{.metadata.uid}{"\n"}{.data.ledger\.json}{"\n"}'
```

The two printed UIDs must match. Names change when the Helm release name,
`fullnameOverride`, or explicit topology names change.

## Ownership and delegated roles

The manager pre-creates and binds managed Nodes. Each selected CiscoDevice gets
a device-incarnation-bound worker ServiceAccount whose name embeds the exact
virtual Node, plus a dynamic binding to the fixed
`cisco-virtual-kubelet-managed-worker` ClusterRole. That role retains the
cluster-wide runtime reads, Pod status, Events, and Lease read/update operations
a virtual kubelet needs, but its Node authority is only:

- `get` on `nodes`; and
- `get`, `update`, and `patch` on `nodes/status`.

Upgrade-leaf permissions are not cluster-wide. The existing namespaced
`cisco-virtual-kubelet-device` RoleBinding grants them only when
the software-upgrade gate is active. The managed-leaf admission policy is the
field-level boundary: main-resource writes may only add or remove
`ops.cisco.vk/iosxesoftwareupgrade-cleanup`, and status writes must preserve
manager admission/control while pairing every durable mutation marker with
its exact reservation and bounded control-revision claim. Leaf annotations and
manager admission freeze the CiscoDevice UID and generation, so any executable
device-spec change requires a new approved plan; same-UID Secret data rotation
is revalidated rather than pinned to one Secret resourceVersion.

An admitted reservation also creates an immutable
`CiscoDevice.status.topologyLock` containing the campaign/plan, reservation,
policy epoch, unique acquisition, device generation, Node UID, and projection
hash. The lock transaction can only advance from `Active` to `Releasing`;
native admission freezes the complete CiscoDevice spec, protected
topology/risk labels, and the Node-adoption/reclassification annotations in
both states, including against the manager. Deletion is denied while the lock
exists, a legacy writer handoff is in progress, or a maintenance session is
not `Settled`. Only the manager's exact release transaction or completed
handoff restores the normal mutation and deletion paths.

Policy authority is carried as a monotonic epoch, not inferred from
`resourceVersion` alone. Canonically equivalent rewrites only refresh the
recorded audit version. Compatible numeric changes fence zero-claim leaves and
publish a new epoch whose limits are the strictest of the frozen, previously
effective, and current values. Structural edits (selector, required/projected
keys, domain-budget shape, policy/ledger identity, or an incompatible target
cap) require a new approved rollout. Manager admission, worker control,
reservations, and mutation claims must agree on the exact epoch. Pending and
granted manager admission is also bound to the reservation's exact
topology-lock acquisition ID, preventing stale authority from crossing a later
acquisition.

The CRD also protects the optional frozen-plan parent: after publication, even
the manager cannot remove/re-add or replace `status.frozenPlan` to evade its
nested immutability contract.

Rollout health uses the manager-owned
`CiscoDevice.status.healthObservation`, which binds an observation time to the
live Node `Ready` heartbeat and a hash of the current device phase/conditions.
The controller rejects a changed heartbeat or condition hash and calculates
freshness from the older source time. Post-operation health must be a newly
authenticated observation strictly after leaf completion. Initial Node binding
does not synthesize health: the observation remains absent until the manager
verifies a real heartbeat, and each fixed identity/topology/gNOI readiness
condition needs an explicit producer observation rather than a
`lastTransitionTime` fallback.

The manager pre-creates every Lease a generated worker may write: the exact
`kube-node-lease/<virtual-node>` heartbeat, every driver-declared config-family
arbitration object, and the shared `device-disruptive-mutation` fence. Each has
`topology.cisco.vk/retain-lease=true`, a purpose, and immutable device, Node,
and worker bindings. A worker has no Lease create/delete verb and may update
only a pre-bound object using its purpose-specific monotonic protocol. The
manager may safely adopt a compatible legacy object and, after revoking worker
RBAC and checking UID/resourceVersion plus every binding, delete exact objects
during device retirement. Mutation holder transitions clear prior requests
atomically; requests bind the current software-upgrade UID and have an
immutable identity with a nondecreasing control revision.

Admission then restricts a status write to the exact username stored on the
old Node. Labels, spec, identity/binding annotations, Node taints,
owner references, and finalizers must remain unchanged. Only the two upstream
VK bookkeeping annotation values may change on a worker status write.

An unselected CiscoDevice keeps the legacy runtime under its own
`cisco-vk-legacy-<node>-<device-uid-hash>` ServiceAccount and the historical
roles. Node admission permits that identity to create or mutate only its exact
unmarked Node and prevents it from entering a manager-bound Node. This retains
the compatibility path without retaining a fleet-wide shared credential.

The fixed role can read Pods, ConfigMaps, Secrets, and Services cluster-wide
because Pods assigned to a virtual Node may originate in any namespace. Its
only Pod mutation is the `pods/status` subresource; native admission permits it
only when `Pod.spec.nodeName` equals the virtual Node embedded in the generated
managed or legacy worker identity and preserves Pod metadata/spec. The managed
role has no Pod main-resource,
log, or exec permission. Diagnostic and DeviceOperation ConfigMap writes come
from the existing `-device` ClusterRole through a namespaced RoleBinding and
are therefore confined to the CiscoDevice namespace. Events remain
cluster-wide because assigned Pods may be in any namespace.

This remains a virtual-kubelet runtime role, not a complete worker sandbox.
Cluster-wide Secret reads are a residual trust boundary required by the current
cross-namespace Pod volume resolver: a ServiceAccount grant cannot be limited
to Secrets referenced by Pods on one dynamic Node. The Pod policy confines
status writes to Pods on one virtual Node, but the ServiceAccount still has
cluster-wide visibility and can set any schema-valid status for those Pods; a
conforming provider filter is not an authorization boundary. A future native
hardening step should authenticate a worker as `system:node:<virtual-node>` and
qualify Kubernetes Node Authorizer/NodeRestriction behavior (or mediate status
through the manager), which can couple Pod and referenced-Secret authority.
Until then, treat workers as cluster-trusted and audit every additive binding.

The chart creates, but deliberately does not bind, these roles:

| Role suffix | Purpose |
| --- | --- |
| `-topology-author` | Change protected topology/risk labels and explicit Node adoption or reclassification approvals |
| `-rollout-planner` | Create campaigns and pass the separate `control` authorization check for pause/resume/cancel |
| `-rollout-approver` | Patch a campaign and pass the separate `approve` authorization check |
| `-topology-policy-admin` | Edit the coherent administrator policy, but never its ledger UID binding |
| `-topology-ledger-breakglass` | Audited disaster-recovery mutation of the CAS ledger |

Bind only the role needed, in the resource's namespace. For example:

```bash
kubectl create rolebinding cvk-topology-authors \
  --namespace network-devices \
  --clusterrole cvk-cisco-virtual-kubelet-topology-author \
  --group network-topology-admins

kubectl create rolebinding cvk-rollout-planners \
  --namespace network-devices \
  --clusterrole cvk-cisco-virtual-kubelet-rollout-planner \
  --group network-rollout-planners

kubectl create rolebinding cvk-rollout-approvers \
  --namespace network-devices \
  --clusterrole cvk-cisco-virtual-kubelet-rollout-approver \
  --group network-rollout-approvers
```

Keep planner and approver groups separate when separation of duties is a
requirement. Approval and control updates each bind their asserted username to
the authenticated caller and require their distinct custom verb. Bind
`-topology-policy-admin` only in the policy namespace. Leave
the ledger break-glass role unbound during normal operation.

Useful RBAC checks for a generated managed-worker identity are:

```bash
kubectl auth can-i get nodes \
  --as=system:serviceaccount:network-devices:WORKER_SERVICE_ACCOUNT
kubectl auth can-i patch nodes \
  --as=system:serviceaccount:network-devices:WORKER_SERVICE_ACCOUNT
kubectl auth can-i patch nodes --subresource=status \
  --as=system:serviceaccount:network-devices:WORKER_SERVICE_ACCOUNT
```

The expected answers are `yes`, `no`, and `yes`. RBAC alone is insufficient;
also execute a server-side dry-run that attempts to change a managed Node label
through `/status` as that worker and confirm the admission policy denies it.

## Admission coverage

When enabled, eight `admissionregistration.k8s.io/v1` policy/binding pairs deny:

- managed Node metadata/spec writes by anyone except the exact manager, and
  Node-status writes by anyone except the bound worker; a generated legacy
  identity may mutate only its exact unmarked Node;
- Pod-status writes unless either generated worker identity encodes the exact
  immutable `Pod.spec.nodeName`, plus any Pod metadata/spec change via status;
- protected CiscoDevice topology/risk/adoption/reclassification changes
  without the delegated `topology` authorization, including legacy
  spec.labels/spec.taints/spec.region/spec.zone projection inputs once the
  managed Node identity is established; while a topology lock is active or
  releasing, the complete spec is immutable and deletion also requires no
  unsettled maintenance session;
- user creation, mutation, or removal of the manager-created, exact-UID
  `topology.cisco.vk/isolated-legacy-worker` authority marker;
- change or removal of a consumed `request-legacy-handoff` annotation after
  the manager has published a non-Complete handoff phase;
- forgery of manager-owned CiscoDevice identity, projection, authenticated
  health, worker-revision, handoff, topology-lock, or maintenance status even
  before enrollment;
  after Node identity or handoff state exists, all status plus lifecycle
  finalizers and owner references are manager-owned;
- forged campaign requester, control requester, approver, approval permission,
  or frozen-plan hash, a non-neutral control at creation, plus non-manager
  campaign status writes;
- managed leaf rebinding and worker changes to manager admission/control
  status (the worker may only add/remove its exact cleanup finalizer on the
  main resource), including an older worker's unclaimed durable mutation
  marker;
- create/delete by generated workers, arbitrary/unbound Lease writes, or a
  heartbeat, config-family, or mutation update outside its bounded protocol;
  mutation requests additionally bind the current `software-upgrade/<leaf UID>`
  holder while every Lease purpose/device/Node/worker binding stays immutable;
- policy ledger-UID replacement or unauthorized policy/ledger mutations.

All bindings use `validationActions: [Deny]` and all policies use
`failurePolicy: Fail`.

## Recovery and removal

Disabling managed topology or uninstalling its release is blocked as an
operational retirement while any reservation, claimed mutation, maintenance
session, or managed writer handoff is active. First pause new campaigns,
resolve or quarantine every claimed mutation, wait for reservations and
sessions to settle, export campaign/leaf/ledger evidence, and complete the
controller's reverse writer handoff for every managed device.

Helm keep protection deliberately leaves the policy, ledger, admission
policies/bindings, managed-worker role, and supplemental manager role/binding
behind. Complete the UID-bound reverse handoff documented in
`docs/topology-awareness.md` before a live Helm upgrade disables the feature.
The chart rejects that live downgrade while any Node identity or incomplete
handoff remains, or when retained policy/ledger identity and manager authority
are incomplete. Keep `controller.leaderElect=true` and
`aggregator.enabled=false` through retirement.

Offline `helm template`/GitOps pruning cannot discover existing shared or
cross-namespace worker authority and is unsupported for this transition unless
prune excludes existing CVK RBAC and retained topology objects through
controller-reported completion. Completed isolated workers still rely on
Node/Pod admission; delete retained enforcement only after every generated
worker, binding, Node marker, and durable device state is gone. The policy-admin
and ledger break-glass permissions remain separate.

Never delete and recreate only the ledger to clear a failure. If the ledger or
policy identity is damaged, keep admission paused and use the separately
authorized break-glass procedure to reconcile physical device state and
durable claims. Recreating empty authority over unresolved work is not a
supported recovery path. The keep annotation is a deletion safeguard, not
proof that retirement preconditions have been met.

A deleted and recreated CiscoDevice with the same namespace/name has a new UID
and intentionally cannot inherit the old retained heartbeat, config-family, or
mutation Leases. Settle and export the old device's reservations/session first,
remove its worker binding, then let the manager perform exact UID/resourceVersion
cleanup before enrolling the new incarnation. It never rotates an active Lease
identity into a new device incarnation.

Run the chart regression directly with:

```bash
charts/cisco-virtual-kubelet/tests/topology-render-test.sh
```

That test covers Helm rendering, qualification gates, policy shape, retained
resources, and required CEL contract fragments. It does not replace CEL
compilation and denial probes through a Kubernetes 1.35 API server, which are
required before declaring a cluster qualified.
