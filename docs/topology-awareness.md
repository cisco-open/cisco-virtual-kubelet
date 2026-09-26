# Managed topology and topology-aware IOS-XE rollouts

Managed topology is an opt-in CVK operating mode that gives the default
Kubernetes scheduler reliable device topology and gives the CVK manager a
bounded, topology-aware admission path for IOS-XE gNOI software campaigns. It
uses only Kubernetes-native API machinery: Nodes, labels, taints, RBAC,
ValidatingAdmissionPolicy, ConfigMaps, Leases, the Eviction/PDB APIs, status
conditions, Events, and the default kube-scheduler. It installs no alternate
scheduler, scheduling plugin, webhook, or third-party topology controller.

The implementation covers roadmap Phases 0–2, the first evidence-backed part
of Phase 3, and one deliberately narrow Phase 4 slice:

- correct Node identity, ownership, topology projection, capacity, and
  maintenance fencing;
- scheduling with ordinary affinity and topology-spread constraints; and
- a manager-side `IOSXESoftwareRollout` campaign above the existing,
  per-device `IOSXESoftwareUpgrade` executor; and
- deterministic topology-local selection among existing operator-provided
  artifact endpoints, with a concrete endpoint and Secret UID frozen per
  target; and
- a disabled-by-default, native Eviction/PDB-aware drain for an explicitly
  marked subset of ReplicaSet-backed workloads.

Later roadmap work is not hidden behind incomplete API fields. Durable
prefetch/cache optimization, broader workload drain support, independently
durable stage/activate, an observed graph API, mandatory native TAS, and a
public multi-driver rollout API remain separate, evidence-gated work. See
[Deferred roadmap](#deferred-roadmap-and-limitations).

!!! warning

    Managed topology changes a cluster security and Node-writer boundary. It
    is disabled by default. Do not enable it on a production fleet until the
    admission probes, migration checks, secure gNOI path, and rollback
    procedure in this guide have passed on that cluster. Workload drain is an
    additional pre-release capability and has not been qualified on physical
    IOS-XE hardware by this change.

## Architecture and ownership

```mermaid
flowchart LR
    I[Administrator inventory labels] --> D[CiscoDevice]
    P[Administrator policy ConfigMap] --> M[CVK manager]
    D --> M
    M --> N[Manager-bound Kubernetes Node]
    N --> S[Default kube-scheduler]
    S --> W[Device-hosted workload]
    W --> A[Shared app-hosting identity<br/>per managed-device namespace]
    R[IOSXESoftwareRollout] --> M
    M --> L[CAS reservation ledger]
    M --> U[Immutable IOSXESoftwareUpgrade leaf]
    U --> K[Shared network-management identity<br/>per managed-device namespace]
    K --> G[Secure IOS-XE gNOI service]
```

The ownership split is strict:

| Surface | Authority |
| --- | --- |
| Desired stable topology and operational risk domains | protected `CiscoDevice.metadata.labels` |
| Stable physical chassis identity used for deduplication | write-once operator-declared `CiscoDevice.spec.physicalIdentity` |
| Node name, UID binding, projected labels, static/maintenance taints | manager |
| Node and Pod status, app execution | app-hosting read-write identity; native admission binds each write to a manager-owned Node/Pod relationship |
| Pod placement | default kube-scheduler |
| Fleet plan, policy evaluation, reservations, child admission | manager |
| Configuration, image transfer/install/activate/verify, limited rollback | network-management identity selected as read-write |

The manager never opens a device session. The worker never chooses fleet
policy. A rollout is not represented as a Pod or Job because scheduler retries
cannot provide at-most-once device-mutation claims or domain reservations.

The physical chassis identity is deliberately operator-declared, write-once,
and manager-deduplicated case-insensitively. Its lowercase canonical value is
recorded in the immutable Node-identity status. A per-device worker supplies live device inventory,
Node health, and execution observations, so compromise of that worker can
falsify those observations. It cannot rewrite the declared physical identity,
protected topology, policy, or ledger authority. Treat worker evidence as a
freshness and consistency signal, not as the root identity assertion.

Managed topology defines exactly two reusable functional ServiceAccount
identities in each namespace that contains managed CiscoDevices, regardless of
device count: one for app hosting and one for network management. These are not
cluster-global accounts because Kubernetes ServiceAccounts are namespaced, and
they are never multiplied per device. Each identity selects exactly one of
`disabled`, `readOnly`, or `readWrite`; `disabled` removes that account, its
bindings, and its worker plane, so a namespace with a disabled plane
materializes fewer than two accounts. App-hosting read-only is a
delegated Kubernetes observation identity only—the manager does not create an
app worker, the Node remains guarded from scheduling, and app hosting is
unavailable. App-hosting read-write runs the virtual-kubelet path. Network
read-only runs telemetry, diagnostics, and read-only operations; network
read-write additionally permits configuration and explicitly enabled gNOI
mutation reconcilers. Runtime gates mirror RBAC so read-only never means
"mutate the device and then fail to report status."

Topology-disabled mode retains the historical combined
`cisco-virtual-kubelet` ServiceAccount. During upgrade, an existing combined
or per-device identity is a keep-protected migration bridge only. A fresh
managed install does not create that third account, and the manager retires
old identities after their workloads are quiesced and both replacement planes
have passed their profile audit.

For a selected managed device, the manager also content-addresses the complete
desired worker PodTemplate, excluding only the revision's own annotation and
environment carrier. Credential, gNOI TLS, and provisioning Secret
resourceVersions are PodTemplate inputs, but Secret bytes never enter the
hash. The digest is injected as
`CISCO_VK_WORKER_CONFIG_REVISION`; the running worker returns it through its
status-only Node writer. `CiscoDevice.status.workerRevision` becomes ready only
after the exact owned Deployment completes, exactly one non-terminating owned
Pod is Ready, and that Pod publishes a matching heartbeat after its start
time. A Secret rotation therefore blocks new gNOI campaign admission until the
`Recreate` worker rollout proves that the new process loaded the new inputs.

## Prerequisites

Managed topology currently requires:

- Kubernetes 1.35 or newer;
- API-server kubelet authentication using `system:node:<nodeName>`, the Node
  and RBAC authorizers, and the `NodeRestriction` admission plugin; the
  projected-token policy relies on a genuine kubelet identity;
- one authoritative managed-topology CVK release per cluster;
- split per-device worker processes (`aggregator.enabled=false`) using two
  namespace-shared functional identities;
- manager leader election (`controller.leaderElect=true`), which reduces
  overlapping reconciliation while ledger/CAS and admission remain the
  durable safety fence across failover;
- the fixed default VK role name (`serviceAccount.vkName` remains
  `cisco-virtual-kubelet`) and `rbac.profile=strict`;
- distinct app-hosting and network-management account names, each also
  distinct from the controller and legacy VK identity; the admission-policy
  prefix, manager username, policy namespace/name, ledger name, and both
  functional account names must all resolve to pairwise distinct strings;
- `gnoi.enableWriteClass=false`; Phase 2 admits only campaign-owned
  `IOSXESoftwareUpgrade`, not generic `IOSXEOperationalAction` mutations;
- CRDs applied before the manager Deployment is upgraded;
- the complete versioned set of native admission policies and bindings installed with
  `failurePolicy: Fail` and `validationActions: [Deny]`;
- an administrator-owned, non-empty managed-fleet selector;
- complete, valid values for every required topology key on each enrolled
  `CiscoDevice`; and
- an explicit `spec.maxPods` from 1 through 110 on every selected device.

Every namespace containing a managed CiscoDevice must be a dedicated,
administrator-controlled namespace. Do not give tenant principals `edit`,
`admin`, RBAC `bind`/`escalate`, ServiceAccount impersonation, access to the shared accounts, worker
Pod `exec`/`attach`/`portforward`/`proxy` or logs, workload `scale`, or delete
authority there. The same boundary rejects `create` on both `pods/binding` and
the legacy `bindings` resource, which could otherwise race the scheduler and
redirect a reserved worker Pod, and update/patch on reserved Deployment or
ReplicaSet status. These permissions can reuse a shared identity, extract its
bound token or device credentials, bypass a safe drain, or remove an
enforcement object. The manager resolves every namespaced RoleBinding through
its Role or ClusterRole using direct API reads and rejects these capabilities,
including wildcard rules and ServiceAccount collection deletion. `bind` and
`escalate` are rejected even when resource-named because Kubernetes permits
those special verbs to delegate authority the subject does not otherwise hold;
ordinary RBAC mutation remains subject to Kubernetes escalation prevention.
The audit also rejects Kubernetes 1.36 constrained-ServiceAccount
`impersonate:serviceaccount` and `impersonate-on:serviceaccount:*` authority, so
enabling that beta capability does not weaken the v1.35 deployment boundary.
It audits both the CiscoDevice namespace and a distinct
`CONFIG_LEASE_NAMESPACE`. In the latter, the only accepted reusable-account
grant is the exact manager-created network Lease binding; an arbitrary binding
to either reserved account is rejected even while its referenced Role is
absent. Role, RoleBinding, and ClusterRole watch mapping follows the reserved
ServiceAccount subject back to its CiscoDevice namespace, so changes in the
coordination namespace requeue the affected devices.
The manager fails closed, but
cluster-admin and principals that can create or change ClusterRoleBindings
remain part of the cluster trust boundary and must be controlled separately.

The `maxPods` bound is a managed-topology scheduling invariant. Standalone
mode deliberately keeps its historical compatibility behavior: values at or
below zero fall back to 16, and otherwise the existing `int32` value is used.

The default VK role name remains a migration invariant for retiring the
topology-disabled combined identity. Steady-state managed workers instead use
the account names under `topology.workerAccounts`; the manager may bind only
the four fixed functional profiles and the fixed, non-selectable app-read,
network-global-read, and Lease-only support roles. A distinct
`CONFIG_LEASE_NAMESPACE` receives only the matching Lease support role, never
the tenant network profile.

The manager checks API discovery, policy generation, binding shape, contract
version, built-in-resource expression warnings, and the exact live rule sets
of the fixed functional worker and support ClusterRoles before enabling managed workers. It
rejects role aggregation or any additional resource or verb. Each worker then
performs positive and negative server-side dry runs with its own credentials: a
harmless Node-status write must succeed, while a label write smuggled through
`/status` must be denied. A version check or an empty warning list alone is not
proof that admission works.

Helm may apply the manager's narrowly scoped `get`/`bind` grant before admission
because those are independent Kubernetes objects. No worker completion binding
is rendered by Helm, and controller startup remains blocked until both policy
and role attestations pass. The Helm actor and controller ServiceAccount are
therefore trusted computing-base identities during this reconciliation window;
protect them from compromise and concurrent out-of-band RBAC mutation.

Node authorization is necessary but does not turn the functional worker token
into a `system:node:*` credential. App read-write still has the documented
cluster-wide workload-input reads required by upstream virtual-kubelet. Treat
the native Node authorizer and NodeRestriction as prerequisites for the real
kubelet that requests projected tokens, not as a substitute for the shared
worker admission policies or the locked namespace boundary.

## Configure and enable

Start from
[`examples/topology/managed-topology-values.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/topology/managed-topology-values.yaml).
The conservative defaults enroll only devices explicitly labeled
`topology.cisco.vk/managed=true`, require region and zone, and admit one
transfer and one unavailable member.

Choose worker authority explicitly. This network-observer baseline supports
scheduling and app hosting but cannot apply device configuration or run an
upgrade:

```yaml
topology:
  enabled: true
  workerAccounts:
    appHosting:
      serviceAccountName: "" # <release-fullname>-app-hosting
      accessMode: readWrite
    networkManagement:
      serviceAccountName: "" # <release-fullname>-network-management
      accessMode: readOnly
```

Set network management to `readWrite` before enabling configuration apply or
software lifecycle. Helm rejects `gnoi.enableSoftwareUpgrade=true` unless that
profile is selected. Set either access mode to `disabled` to omit that worker
plane. `appHosting: readOnly` is not a reduced-function Virtual Kubelet: it is
a non-running observation identity, no app worker is launched, and the device
cannot accept scheduled Pods.

The reusable accounts remove per-device RBAC-object growth, not per-device
runtime isolation. With both planes active, each device has one app worker and
one network worker, so host-cluster CPU, memory, and Pod capacity must still be
sized for two worker Pods per device. Disabled planes and app read-only mode do
not launch their corresponding worker.

Leave account names empty unless an external naming convention requires an
override. Admission treats resolved worker names as cluster-reserved
identities, even though ServiceAccounts are namespaced. An explicit override
must therefore be unique across every CVK release in the cluster; reuse fails
closed rather than sharing authority. Bootstrap persists both resolved names
in retained `policy.json` and protected policy annotations. Helm and manager
preflight reject a later rename; retire managed topology and re-enroll to
change either identity. The owning release, retained policy coordinates, and
admission-policy prefix are locked with them. Changing a policy name/namespace,
`fullnameOverride`, or `nameOverride` likewise requires retirement followed by
clean re-enrollment.

`CONFIG_LEASE_NAMESPACE` is also an identity-bound bootstrap choice. Its
resolved value (empty means each CiscoDevice namespace) is stored in the
retained policy and protected annotation. Helm and manager preflight reject an
in-place change because the former namespace could otherwise retain writable
Lease authority. Complete the documented retirement, change the value, and
re-enroll the fleet instead.

Access-profile changes are fail-closed transitions, not live RBAC swaps. Before
removing read-write authority, the manager verifies that topology locks,
maintenance, reservations, and device mutation are settled, and that app
workloads do not depend on the app plane. It then drains the applicable worker
Deployments, ReplicaSets, and Pods before replacing or removing the binding.
Escalation likewise waits for prior worker incarnations to disappear. Do not
edit the generated bindings or ServiceAccounts to bypass this transition.

Before selecting an existing CiscoDevice, populate its previously absent
`spec.physicalIdentity` from authoritative inventory and confirm the value is
fleet-unique without regard to case. This is the supported migration path for
older objects: the field may be set once, but admission rejects later removal
or replacement. Do this before adding the managed-selector label so the
manager never has to infer chassis identity from worker-reported data.

Apply CRDs first because Helm does not upgrade resources in a chart's `crds/`
directory. On an existing installation, back up the live definitions and
review the server-side result before the explicit ownership handoff:

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
  --values examples/topology/managed-topology-values.yaml
```

`kubectl diff` returns status 1 when it finds differences. Plain server-side
apply can conflict with Helm's initial CRD field ownership; the explicit force
is limited to the exact reviewed CVK CRD files passed through `-f`.

The first combined-to-split worker migration must be a live `helm upgrade`.
The chart uses Kubernetes `lookup` to preserve only the exact existing shared
RoleBinding and ClusterRoleBinding while the manager replaces their users.
Offline `helm template` output and GitOps pruning cannot discover those live,
cross-namespace identities. They are not a supported source of truth for this
one-time handoff unless pruning explicitly excludes existing CVK RBAC until
the manager reports retirement complete. Normal offline review of rendered
manifests remains useful; applying that output as a pruning migration is the
unsafe operation.
The live Helm upgrade identity needs cluster-wide ConfigMap `list` permission
to locate the one retained managed-policy object carrying this release's Helm
ownership annotations. The chart fails closed if that lookup is denied, if
more than one owned policy is found, or if its coordinates or admission prefix
do not match the current release.

Live upgrades also preserve the bound policy and ledger UIDs. The chart does not resubmit initialized ledger data; restoring retained authority is required if either object is missing.

Treat controller/chart changes to fixed RBAC or admission contracts as a
maintenance window: settle all campaigns, maintenance sessions and mutation
Leases first. An older manager cannot recognize a newly changed fixed-role
contract and intentionally quarantines mismatched authority. Worker accounts
and Pods can therefore rotate, with temporary virtual-Node `NotReady` status;
this is not a zero-downtime control-plane upgrade guarantee. Keep admission
enabled and wait for native garbage collection and exact worker rebinding.
Previously denied garbage-collection requests may take several minutes to
retry; do not remove finalizers or replace retained Leases to accelerate them.

Do not use `helm upgrade --force`. The policy and ledger are identity-bound by
their Kubernetes UIDs. Replacement is intentionally treated as a safety
failure, not as an empty new fleet.

On the first managed-topology bootstrap the chart creates the administrator
policy and an empty kept ledger. The manager validates the whole `policy.json`,
initializes `ledger.json` with the live ledger UID, and adds that UID to the
protected policy annotation. Subsequent live upgrades preserve that annotation
but do not submit a ledger update, avoiding a lookup/apply race with live
reservations. They fail on a partial pair, a bound empty ledger, or a UID
mismatch. Policy/ledger coordinates and `fullnameOverride` cannot move while
release-owned managed-topology admission authority remains; retire that
authority explicitly before establishing new coordinates. Confirm that the
UIDs match:

```bash
kubectl get configmap cvk-cisco-virtual-kubelet-topology-policy \
  -n cisco-vk-system \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/ledger-uid}{"\n"}'

kubectl get configmap cvk-cisco-virtual-kubelet-topology-ledger \
  -n cisco-vk-system \
  -o jsonpath='{.metadata.uid}{"\n"}{.data.ledger\.json}{"\n"}'
```

### Label vocabulary

Use low-cardinality, administrator-declared labels. The policy accepts at most
16 required and 16 projected keys.

| Key family | Purpose | Project to Node? |
| --- | --- | --- |
| `topology.kubernetes.io/region` | broad placement/failure region | yes |
| `topology.kubernetes.io/zone` | scheduler availability domain | yes |
| `topology.cisco.vk/*` | site, building, rack, fabric, flattened domains, and explicit managed-fleet enrollment | only if allowlisted |
| `operations.cisco.vk/*` | rollout-only risk and qualification facts, including redundancy domain, image family, and `qualification-cohort` | no |
| `distribution.cisco.vk/*` | artifact endpoint/cache locality used only by rollout planning and transfer budgets | no |

Every budget-domain key must also be required. Every projected key must be
required. The managed-fleet selector itself may use only
`topology.cisco.vk/*`, so ordinary device editors cannot silently enter or
leave managed writer ownership.

For hierarchical domains, make values globally unique or publish a flattened
key, for example:

```yaml
metadata:
  labels:
    topology.kubernetes.io/region: eu-central
    topology.kubernetes.io/zone: berlin-1
    topology.cisco.vk/site: berlin-campus
    topology.cisco.vk/rack-domain: eu-central.berlin-1.berlin-campus.rack-7
    operations.cisco.vk/redundancy-domain: stack-a
    operations.cisco.vk/image-family: cat9k
    operations.cisco.vk/qualification-cohort: c9300
```

Managed mode treats metadata labels as authority. The legacy `spec.region`,
`spec.zone`, and topology values under `spec.labels` may temporarily agree
with them, but a conflict blocks projection. Managed mode never invents an
unknown region or zone from the platform family. Non-topology legacy
`spec.labels` remain compatible for the migration period.

### Enroll devices safely

Before enrollment:

1. Record the existing Node name, UID, labels, taints, bound workloads, and
   owning `CiscoDevice` UID.
2. Resolve every Node-name collision. `spec.nodeName` is immutable; when it is
   omitted, the compatibility name is `metadata.name`. Managed topology
   accepts the existing DNS-subdomain grammar, including dotted names, but the
   resolved name must be at most 63 bytes for the managed Node and admission
   binding contract. Set an explicit short `spec.nodeName` before enrollment
   when a legacy object name is longer.
3. Add the required metadata labels, the
   `operations.cisco.vk/image-family` needed by campaigns, and a qualified,
   low-cardinality `operations.cisco.vk/qualification-cohort` representing the
   target's tested hardware/capability class. Set `spec.physicalIdentity` to a
   verified stable chassis serial, UUID, or equivalent; it is immutable and
   must be unique across the managed fleet.
4. Verify no other controller owns the intended topology labels or managed
   taints.
5. Add the fleet-enrollment label only after admission policies are active.

On an upgrade from shared worker credentials, Helm retains the exact old
shared account and bindings as a marked migration bridge while the controller
replaces each old Deployment using `Recreate`. The controller removes the
bridge only after no Deployment, ReplicaSet, or Pod still uses it and the app
and network replacement profiles have passed their binding audit. New managed
admission remains globally deferred while the legacy bridge exists. Do not
manually delete it during this handoff. Fresh topology-enabled installs never
create the legacy account. ReplicaSet read/delete permission exists only in the retained
managed-topology manager role and is used solely to remove an exact,
owner-verified, zero-replica legacy ReplicaSet during that retirement; it is
absent from the default controller role.

The manager pre-creates or explicitly adopts the Node, records the Node UID in
`status.nodeIdentity`, reconciles the two namespace-shared profile bindings, and holds
`topology.cisco.vk/uninitialized=true:NoSchedule` through the writer handoff.
It removes the guard only after the complete projection is present, the worker
protocol is bound, and the live authenticated worker observation of physical
identity agrees with both the write-once spec declaration and its canonical
status binding. The same three-way agreement is rechecked before
reclassification can restore `TopologyReady=True`; disagreement reapplies the
`NoSchedule` initialization guard. Watch the relevant conditions:

```bash
kubectl get ciscodevice -A \
  -o custom-columns='NAMESPACE:.metadata.namespace,DEVICE:.metadata.name,NODE:.status.nodeIdentity.nodeName,IDENTITY:.status.conditions[?(@.type=="NodeIdentityReady")].status,TOPOLOGY:.status.conditions[?(@.type=="TopologyReady")].status'

kubectl get nodes -L topology.kubernetes.io/region \
  -L topology.kubernetes.io/zone -L topology.cisco.vk/site
```

`TopologyReady=False`, `TopologyIncomplete=True`, or
`TopologyConflict=True` is a scheduling block, not an informational warning.
Changing an established projection requires delegated `topology` permission
and an explicit reclassification acknowledgement. Existing Pods are not
relocated when a Node label changes. A campaign reservation also writes
`CiscoDevice.status.topologyLock`, binding the campaign, plan, reservation,
policy epoch, unique acquisition, device generation, Node UID, and projection
hash. The manager advances the immutable lock transaction only from `Active`
to `Releasing` before ledger cleanup. While either state exists, native
admission freezes the complete CiscoDevice spec, protected topology/risk
labels, and the protected Node-adoption/reclassification annotations,
including against the manager or a delegated topology author. This prevents a
live campaign from inheriting a
changed address, driver, credential/trust reference, runtime limit, or taint.
Deletion is denied while any topology lock exists, a legacy writer handoff is
in progress, or a maintenance session is not `Settled`. There is no implicit
admission break-glass: only the manager's normal exact release transaction or
handoff completion restores mutation and deletion.

## Workload scheduling

Use the default scheduler. Do not set `schedulerName`, and avoid direct
`spec.nodeName` assignments. The
[`devices-and-workload.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/topology/devices-and-workload.yaml)
example shows one fleet member plus drain-qualified soft site preference and
spread. It also includes a matching `policy/v1`
`PodDisruptionBudget` and the opt-in `operations.cisco.vk/drain-safe=true`
template label. The label alone grants no eviction authority. With the
default `BlockIfRunning` campaign policy, CVK does not evict Pods or consume
the PDB and fails closed while either Kubernetes or the device reports a live
workload.

Hard placement uses node affinity:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: topology.cisco.vk/site
              operator: In
              values: [berlin-campus]
```

Use weighted preferences when one site or rack is desirable but a qualified
fallback is acceptable. Missing preferred labels do not make a Pod
unschedulable:

```yaml
affinity:
  nodeAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        preference:
          matchExpressions:
            - key: topology.cisco.vk/site
              operator: In
              values: [berlin-campus]
      - weight: 50
        preference:
          matchExpressions:
            - key: topology.cisco.vk/rack
              operator: In
              values: [berlin-rack-7]
```

Availability spreading uses the standard topology-spread API:

```yaml
topologySpreadConstraints:
  - maxSkew: 1
    minDomains: 2
    topologyKey: topology.kubernetes.io/zone
    whenUnsatisfiable: DoNotSchedule
    nodeTaintsPolicy: Honor
    labelSelector:
      matchLabels:
        app: edge-agent
```

Use `DoNotSchedule` for a hard failure-domain requirement and
`ScheduleAnyway` for a preference. Add a PodDisruptionBudget for replicated
applications. It affects CVK only when both administrator policy and an exact
campaign opt into the drain described below.

Hard affinity, selectors, and spread remain valid with `BlockIfRunning`, but
the preview `Drain` contract rejects them because CVK cannot prove spare
scheduler capacity outside the maintained Node. A drain-qualified template
must use preferences and `ScheduleAnyway`; its PDB and the bounded recovery
timeout remain the fail-safe if no replacement can become Ready. Before opting
in, prove that the default scheduler has qualified alternative capacity and
that the application can actually run there; CVK neither reserves replacement
capacity nor treats a soft preference as a placement guarantee.

Important native scheduler behavior:

- incomplete Nodes do not receive the required projected labels and retain the
  `topology.cisco.vk/uninitialized=true:NoSchedule` guard;
- an active device mutation adds
  `cisco.vk/device-maintenance=gnoi:NoSchedule`; both guards block new
  scheduler placements but do not evict an existing Pod;
- broad tolerations or direct `nodeName` can bypass a scheduler taint; and
- changing labels does not move already-bound Pods.

The workload example deliberately contains no toleration for either CVK-owned
guard. Application manifests should not add one: tolerating a maintenance
guard makes scheduler placement possible while the device is fenced. Inspect
the effective state with `kubectl get node <name> -o jsonpath='{.spec.taints}'`
when diagnosing a Pending Pod.

The rollout leaf therefore repeats its workload check after it obtains the
device fence. A drain campaign additionally rechecks for newly bound Pods and
in-flight writes before promoting to gNOI mutation. These post-fence checks are
required even when scheduling policy is correct.

### Experimental in-tree Workload/PodGroup/TAS

Native Workload/PodGroup/TAS remains feature-gated upstream. Kubernetes 1.36
introduced [topology-aware workload scheduling](https://kubernetes.io/docs/concepts/workloads/workload-api/topology-aware-scheduling/)
as an alpha, disabled-by-default feature. Kubernetes 1.37 exposes the
[Workload and PodGroup resources](https://kubernetes.io/docs/concepts/workloads/podgroup-api/)
through `scheduling.k8s.io/v1beta1`, still disabled by default, and requires
the `GenericWorkload` and `TopologyAwareWorkloadScheduling` feature gates on
the relevant control-plane components. Its single topology constraint
co-locates the group in one label domain; this is different from topology
spread, which distributes replicas across domains.

The separately gated
[`experimental-native-tas-v1.37.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/topology/experimental-native-tas-v1.37.yaml)
shows a Workload template, standalone PodGroup, and Pods using only the default
scheduler and CVK-projected Node labels. It is deliberately excluded from the
Kubernetes 1.35 qualification test and must not be applied until discovery and
`kubectl explain` confirm the v1.37 fields on the target cluster. CVK does not
create or watch these APIs, so its current client-library line is not an
adapter; Node labels are the compatibility layer. The earlier v1.36 alpha
versioned schema is not promised by this example.

This remains experimental rather than a production dependency because the APIs
are disabled by default, the built-in integration is still limited, and
multi-level `CompositePodGroup` topology is a separate v1.37 alpha feature.
CVK ships no TAS controller, CRD, scheduler profile, or third-party component.

## Topology-aware IOS-XE software campaigns

Enable the existing device-side upgrade executor separately:

```yaml
gnoi:
  disabled: false
  enableSoftwareUpgrade: true
```

`topology.enabled` never grants software-mutation authority by implication.
The rollout controller and its manager/worker mutation RBAC are inactive until
both gNOI gates above are enabled. Read-only observation of retained leaves
continues after disablement so an unsettled operation remains fenced.
Keep `gnoi.enableWriteClass=false`: the managed Phase 2 coordinator rejects
generic `IOSXEOperationalAction` because those operations do not yet implement
the campaign reservation, claim, and maintenance-session contract.
On an enrolled managed device, direct or unmarked
`IOSXESoftwareUpgrade` objects are ignored. Submit an
`IOSXESoftwareRollout`; the manager creates retained, immutable,
UID/reservation-bound leaves only after admission succeeds.

### Secure gNOI and image-source prerequisites

Complete the IOS-XE secure gNXI configuration, password-metadata or mTLS
trust, and gNOI OS provisioning described in
[IOS-XE upgrade and downgrade runbook](gnoi-iosxe-upgrade-runbook.md) and
[gNOI software lifecycle design](gnoi-software-lifecycle.md). Managed topology
does not weaken or replace those controls.

For a dedicated verified gNOI trust bundle, the Kubernetes object uses a
same-namespace Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: c9k-a-gnoi-tls
  namespace: network-devices
type: Opaque
data:
  ca.crt: BASE64_CA_PEM
  tls.crt: BASE64_OPTIONAL_CLIENT_CERT_PEM
  tls.key: BASE64_OPTIONAL_CLIENT_PRIVATE_KEY_PEM
---
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: c9k-a
  namespace: network-devices
spec:
  # ...normal device fields...
  gnoi:
    port: 9339
    transportSecurity: tls
    tls:
      secretRef:
        name: c9k-a-gnoi-tls
```

`ca.crt` is required; `tls.crt` and `tls.key` must be supplied together when
mutual TLS is used. Keep private keys in Secrets, never ConfigMaps or rollout
specs.

The managed campaign API pins one image digest and family and accepts up to 16
named existing artifact endpoints. Each endpoint is either:

- anonymous HTTPS with verified TLS and no URL credentials; or
- SFTP with an endpoint-bound Secret and verified `knownHosts` data.

Scoped endpoints select protected `CiscoDevice.metadata.labels`; every
selector key must be listed in administrator `requiredTopologyKeys`. A
`distribution.cisco.vk/cache-domain` label is the recommended source-locality
key. It is protected by admission and may define a transfer-budget domain, but
cannot be projected to a Node. The lowest numeric priority among matching
scoped endpoints wins. Equal-priority matches fail planning. Exactly one unscoped catch-all
is required and is considered only when no scoped endpoint matches. Every
endpoint serves the single campaign-wide SHA-256 identity; CVK does not
silently fail over after approval.

`IOSXESoftwareRollout` is still an alpha, unreleased API. Phase 3A replaces
the earlier development-preview `plan.source` shape with `plan.image.sources`
and moves the frozen endpoint snapshot onto each target. There is no conversion
webhook between those shapes. Do not deploy the Phase 2 controller/CRD by
itself and later upgrade a cluster containing rollout objects: land the stacked
changes before the first release. Any cluster that ran the preview must first
settle and export its rollout evidence, remove those preview objects, and then
upgrade the CRD and controller together. An older controller cannot safely be
rolled back over objects written with the Phase 3A schema.

Managed source resolution rejects redirects, environment proxies, URL
userinfo, queries/fragments, unsafe or mixed DNS answers, loopback,
link-local, multicast, unspecified, metadata, and other special-use
destinations. Private-address HTTPS is rejected. Private SFTP is allowed only
with an endpoint-bound Secret. This stricter policy applies to manager-created
managed leaves; standalone legacy leaf behavior remains compatible.

An SFTP Secret has this shape:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: site-a-image-source
  namespace: network-devices
  labels:
    cisco.vk/purpose: software-image-source
type: Opaque
stringData:
  allowedScheme: sftp
  allowedHost: images.site-a.example.net
  allowedPort: "22"
  username: image-reader
  privateKey: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    REPLACE_ME
    -----END OPENSSH PRIVATE KEY-----
  knownHosts: |
    images.site-a.example.net ssh-ed25519 REPLACE_ME
```

The manager freezes each target's selected source, priority, URL, digest, and
Secret UID into the plan and managed leaf. In-place credential rotation under
that UID is allowed, but every use revalidates source purpose, endpoint, and
trust policy. Delete/recreate changes the UID, fences new claims, and requires
a newly frozen and approved rollout. Transient resolution failures retry only
that frozen endpoint within the install deadline. A permanent failure or
expired deadline fails the leaf and fences further campaign admission; it
never falls through to a different mirror under the approved hash.

### Opt-in PDB-aware drain (development preview)

The Phase 4 drain slice is intentionally narrower than a general-purpose
`kubectl drain`. It exists only for manager-created IOS-XE software-rollout
leaves, is disabled at both administrator and campaign layers, and fails
closed when workload portability cannot be proven. Its repository
qualification scope is unit and disposable API-server coverage; it does not
claim physical-switch workload, upgrade, or downgrade qualification. Keep
`BlockIfRunning` in production until the complete combination has passed the
site's IOS-XE and application tests.

First give the administrator policy an explicit namespace allowlist and
ceilings. With the gNOI gates shown below, this also creates the split
namespace-scoped cleanup/read and Eviction Role/RoleBinding pairs in each
listed namespace:

```yaml
topology:
  enabled: true
  policy:
    # ...the normal managed-topology policy...
    workloadDrain:
      enabled: true
      allowedNamespaces: [edge-workloads]
      maxTimeoutSeconds: 900
      maxPods: 8
      maxTerminationGraceSeconds: 120

gnoi:
  disabled: false
  enableSoftwareUpgrade: true
```

When `workloadDrain.enabled` is false, the drain policy is omitted from the
canonical administrator policy. Keeping it false across an upgrade therefore
preserves existing `BlockIfRunning` policy semantics and hashes. Enabling the
administrator gate does not convert a campaign automatically: that campaign
must also select `Drain` and may only tighten the allowlist and bounds:

```yaml
spec:
  plan:
    # ...targets, image, strategy, budgets, health, and canaries...
    workloads:
      policy: Drain
      drain:
        namespaces: [edge-workloads]
        timeoutSeconds: 900
        maxPods: 8
        maxTerminationGraceSeconds: 120
```

Each allowlist contains 1–16 unique namespace DNS labels. `timeoutSeconds` must
be 300–7200 and at least 120 seconds greater than
`maxTerminationGraceSeconds`; `maxPods` is 1–32; and termination grace is
30–600 seconds. Each value must also be no greater than its administrator cap.
Start from
[`examples/topology/iosxe-software-rollout-drain.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/topology/iosxe-software-rollout-drain.yaml)
rather than converting an already planned campaign. Executable intent is
immutable, so changing workload policy requires a new frozen plan and
approval.

Enabling or disabling drain, or changing its allowlist or caps, is still a
semantic administrator-policy edit. Active campaigns follow the normal
policy-epoch fencing protocol, but their immutable `workloads.policy` never
changes from `BlockIfRunning` to `Drain`. Any leaf whose drain session has
already started enters `Recovering` rather than inheriting changed authority.
Plan such policy changes outside an active drain; create and approve a new
campaign when executable drain intent must change.

Disabling the administrator drain gate or removing a namespace makes the
manager start no new Eviction. Applying the same change with a live Helm
upgrade also removes that namespace's separate, non-retained Eviction grant.
A narrow Pod-cleanup/read Role and RoleBinding
carry Helm keep protection and remain available for exact marker/finalizer
cleanup plus controller/PDB reads during workload recovery. They never grant
Eviction, direct Pod deletion, or device access. Keep `topology.enabled=true`,
`gnoi.disabled=false`, and `gnoi.enableSoftwareUpgrade=true` until every drain
is `Settled`; those top-level gates run the manager and worker controllers that
may continue already accepted teardown/inventory and perform cleanup. Never
remove a protected finalizer manually as a substitute for device-clean evidence.
Helm keep and live lookup protect Helm transitions; a GitOps pruner must
explicitly exclude the cleanup pair until settlement.

Every active Pod bound to the target Node must meet all of these conditions
when the manager freezes drain evidence and again before eviction:

- its namespace is in the campaign and administrator allowlists, the total
  candidate count does not exceed `maxPods`, and it is not already
  terminating;
- both the Pod and its controlling Pod template carry
  `operations.cisco.vk/drain-safe=true`;
- the bound Pod names the default scheduler, every controlling template leaves
  `nodeName` unset, and neither Pod nor template tolerates
  `cisco.vk/device-maintenance=gnoi:NoSchedule` or all `NoSchedule` taints;
- it has no `nodeSelector`, required node/Pod affinity or anti-affinity, or
  `DoNotSchedule` topology spread. Preferred affinity and `ScheduleAnyway`
  spread are supported; hard placement remains supported only with
  `BlockIfRunning` in this preview;
- the Pod is controlled by an exact `apps/v1` ReplicaSet incarnation, either
  directly or through an exact Deployment incarnation; bare Pods, Jobs,
  CronJobs, DaemonSets, StatefulSets, and custom controllers are unsupported;
- every volume is from the portable Secret, ConfigMap, Projected, or
  DownwardAPI subset; PVCs, host paths, `emptyDir`, and all other volume
  sources block drain; and
- exactly one `policy/v1` PodDisruptionBudget selects the Pod. It must have
  observed its current generation, report at least one allowed disruption,
  and report a consistent healthy replica set. The Kubernetes 1.35
  [Eviction API rejects overlapping PDBs for one Pod](https://github.com/kubernetes/kubernetes/blob/release-1.35/pkg/registry/core/pod/storage/eviction.go#L1580-L1593),
  so CVK rejects that shape before drain.

The `drain-safe` label is an explicit portability assertion, not a safety
override. It must appear on the ReplicaSet template and, for a
Deployment-owned ReplicaSet, on the Deployment template as well. The current
[`devices-and-workload.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/topology/devices-and-workload.yaml)
shows the label, soft default-scheduler affinity/spread, and a matching PDB. CVK
still submits every termination through `policy/v1` Eviction. The API server's
live PDB decision is authoritative even when the frozen status showed
`disruptionsAllowed: 1`.

The durable flow is deliberately serial and revalidates identity at every
boundary:

1. The manager freezes exact Pod, ReplicaSet/Deployment, PDB, Node, policy,
   reservation, and worker identities under a new UUIDv4 drain session.
2. The manager snapshots pre-existing scheduling state, applies a
   session-owned cordon plus
   `cisco.vk/device-maintenance=gnoi:NoSchedule`, activates the existing
   maintenance session, and blocks ordinary device writes.
3. The manager atomically adds its drain-session marker and one protected
   finalizer to one exact Pod UID, revalidates its evidence, and asks the
   Eviction subresource to terminate it. No second selected Pod is evicted
   until the first controller has observed its current generation and returned
   to exactly its desired total replicas. A Deployment must report every desired
   replica updated, Ready, and available with zero unavailable; a direct
   ReplicaSet must report every desired replica Ready and available.
4. Only the selected Pod's provider teardown may acquire the ordinary
   device-mutation Lease as `software-drain/<leaf UID>`. After teardown, the
   worker performs a fresh, complete device workload inventory. On IOS-XE,
   both the app-hosting configuration and operational inventories must be
   readable and every app must have a consistent CVK workload identity; a
   partial, malformed, or unattributed result fails closed. During active drain
   the manager requires no foreign or unknown device workload. During terminal
   recovery, an attributable replacement may already have returned to the
   restored Node; a complete scan may therefore contain non-selected UIDs but
   must still prove the exact selected UID absent and report zero unknowns.
   A cache-lagged provider callback is resolved against the uncached live Pod;
   an exact-UID mismatch fails closed. The worker reauthorizes the session
   immediately before device dispatch and before every Lease renewal. Once the
   manager has durably accepted `DeviceClean`, verified the canonical Lease is
   idle, and removed its exact marker/finalizer, a later provider callback is a
   completion acknowledgement only: it performs no driver call, inventory,
   Lease operation, or Kubernetes write. Upstream Virtual Kubelet then performs
   its existing exact-UID, zero-grace API deletion. The provider accepts this
   narrow path from `DeviceClean` (the crash boundary before the manager records
   `Released`) or `Released`, only with ordered accepted-Eviction, termination,
   and positive newer inventory evidence plus a still-idle, request-free exact
   Lease. A recovery-only `Protected` to `Released` transition without device-
   clean evidence never qualifies. The worker logs
   `managed drain device-clean completion acknowledged without device mutation`
   at this boundary before upstream Virtual Kubelet completes the API deletion.
5. After every selected Pod is complete, the manager proves that no new Pod is
   bound to the guarded Node, the mutation Lease is idle and request-free, and
   all frozen campaign/source/target/control facts still match. Only then does
   it promote the reservation and maintenance session to disruptive gNOI
   software mutation.
6. Once the device outcome is conclusive, recovery restores only the cordon
   and taint owned by this session, permits normal workload writes, and waits
   for the exact controller and PDB UIDs, their current drain-safe templates,
   selectors and healthy observed generations, post-mutation device/Node health,
   and continuous soak. The topology reservation remains held through this
   recovery and is settled last.

Replacement health uses the provider's Pod status and native controller/PDB
observations. On IOS-XE, a running application can become Pod Ready before
DHCP or an application endpoint is usable; this is not an HTTP readiness
guarantee. Validate the service independently during qualification. A PDB
shared by separate single-replica Deployments protects their aggregate
availability, not the availability of each application.
Deployment `minReadySeconds` can require sustained provider readiness before
the controller becomes available and CVK permits the next eviction. It is a
useful settling delay, not a substitute for application-level health checks.

Include failed-write quarantine and native Virtual Kubelet queue backoff in
the maintenance window. An uncertain app lifecycle operation can retain the
canonical mutation Lease for 31 minutes after its last renewal; expiry does
not immediately schedule another attempt. A replacement that cannot become
healthy prevents the next eviction and gNOI promotion. Do not clear the Lease
or change Pod metadata merely to force a retry. The IOS-XE lifecycle wait
defaults to 180 seconds per state; a shorter
`cisco.io/apphost-package-timeout` override can turn a slow transition into a
failed write and a much longer recovery wait.

The leaf's durable manager states are `Preparing`, `Guarded`, `Evicting`,
`Drained`, `Promoting`, `Promoted`, `Recovering`, and `Settled`. Each selected
Pod advances through `Selected`, `Protected`, `EvictionRequested`,
`TerminationObserved`, `DeviceClean`, `Released`, and `Complete`. Treat these
as current evidence, not commands to patch manually. Manager and worker status
ownership, immutable identity fields, Pod marker/finalizer pairing, Lease
purpose, and monotonic transitions are protected by CRD validation and native
admission.

A PDB denial, changed controller/PDB incarnation, unsafe current controller or
PDB semantics, new or foreign Pod during active drain, partial
device inventory, busy mutation Lease, elapsed drain deadline, policy drift,
pause, or cancellation prevents promotion. Recovery starts no new eviction;
it completes teardown already accepted by Kubernetes, removes protection from
Pods not evicted, and restores only restrictions whose session ownership is
still exact. A pre-existing cordon or taint is preserved. If an operator
cordons the Node during the session or ownership is ambiguous, CVK leaves the
cordon in place. Expiry is not evidence that device mutation or teardown is
safe, and unresolved cleanup/recovery retains the reservation for operator
investigation. A routine Deployment, ReplicaSet, HPA, or PDB generation change
does not by itself strand recovery: CVK revalidates the exact object UID against
its current template, selector, observed generation, and replica health without
granting new eviction authority.

`status.managerDrain.recoveryDeadline` bounds accepted-teardown and inventory
authority; passing it never means cleanup succeeded. If recovery is still
legitimately required after that deadline, increment `spec.control.revision`
to a value strictly greater than the campaign revision and every retained
leaf's manager-control and drain revisions,
preserving existing `pause` and terminal `cancel` flags. The manager records
the new revision in `status.managerControl` and
`status.managerDrain.controlRevision`, advances `updatedAt`, and issues one new
window equal to the immutable drain timeout plus 120 seconds. The session
token, start time, selected UIDs, and state remain unchanged. State stays
`Recovering`: renewal may resume only device teardown already accepted through
Kubernetes Eviction and the fresh inventory needed to prove that teardown; it
cannot start another Eviction or a disruptive gNOI software mutation. A
deleting campaign cannot be edited reliably, so its safety finalizer performs
the same bounded, synthetic revision advance when an exact recovery window
expires. Repeated renewal is an explicit audited indication of an unhealthy
worker/device path and should trigger operator investigation.

For example, after confirming that `8` is greater than the campaign and every
leaf's `status.managerControl.revision` and
`status.managerDrain.controlRevision`, the same or another authorized operator
can renew a cancelled campaign without clearing cancellation while recording
the authenticated requester and time:

```bash
REQUESTER="$(kubectl auth whoami -o jsonpath='{.status.userInfo.username}')"
REQUESTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

kubectl patch iosxesoftwarerollout CAMPAIGN -n network-devices --type=merge \
  -p "{\"spec\":{\"control\":{\"revision\":8,\"cancel\":true,\"requestedBy\":\"${REQUESTER}\",\"requestedAt\":\"${REQUESTED_AT}\",\"reason\":\"renew bounded drain recovery\"}}}"
```

Do not copy that revision blindly or omit an already true `pause` or `cancel`
flag. The caller needs the delegated `control` verb. Export the pre/post rollout
and leaf status for the change record.

A control renewal also cannot silently carry an old Lease request into its new
window. If the canonical Lease is still held under the preceding recovery
revision, the provider returns a mutation-incomplete result, neither renews the
old revision nor dispatches another device call, and leaves the hold quarantined
through the exact `renewTime + leaseDurationSeconds` boundary. Only then may it
retire the exact same Lease UID, session, operation, and holder when the request
metadata is either complete and bound to that strictly older revision or wholly
absent from a crash before publication. Partial, malformed, foreign, or
unexpired state remains blocked for investigation. Retirement preserves the
transition counter and performs no device call. On a later reconcile, cleanup
must first obtain a fresh current-revision CiscoDevice maintenance-session
acknowledgement before the provider can reacquire the Lease and resume
already-accepted teardown. The 31-minute Lease TTL can outlast a recovery
window, so an operator may need another explicit bounded renewal while waiting.
Neither Lease expiry nor safe retirement is device-clean evidence.

Worker replacement never makes old device-clean evidence current by itself.
The provider binds drain authorization to the immutable configuration revision
of its own running process, `status.workerControl`, the protected Node's
`topology.cisco.vk/worker-observed-revision`, and the manager-authenticated
Deployment/revision/exact-Pod readiness proof in
`CiscoDevice.status.workerRevision`. If device-clean evidence is still needed
after a credential, trust, or PodTemplate rotation,
the replacement worker must perform and publish a strictly newer device
inventory. The worker-drain inventory
revision and both observation timestamps advance together with
`observedWorkerConfigRevision`. Admission permits that rollover but rejects a
stale process, a rewritten same-revision observation, and any manager attempt
to consume inventory whose worker revision does not match the current control
acknowledgement.

After the manager has already persisted `DeviceClean`, that manager-owned
record is the durable acceptance fence. Finishing deletion of the same
terminating Pod UID does not require the worker that produced the inventory to
remain alive, the drain deadline to remain open, or mutation-only scheduling
guards to remain applied. This exception grants no device access: it requires
the exact session/leaf/Pod identities, absent manager protection, ordered
device-clean evidence, and a wholly idle request-free canonical Lease. It is
what makes the final Kubernetes deletion restart-safe during worker rollout and
drain recovery.

The manager's drain RBAC starts only in administrator-allowlisted namespaces.
A retained cleanup/read Role permits Pod read/update/patch and read-only
PDB/ReplicaSet/Deployment access. A separate non-retained execution Role grants
only `pods/eviction` create while every feature gate remains active. Neither
contains a Pod `delete` verb. A fail-closed admission policy confines Pod
updates to the exact drain-session annotation/finalizer pair and globally
rejects direct workload Pod DELETE by the manager identity; it is not permission to
alter Pod spec, labels, owners, or unrelated metadata. CVK has no force-delete
path for application workloads and never bypasses a PDB. The separate topology
manager role permits exact-UID deletion of reserved worker Pods for security
quarantine only; native admission denies ordinary application Pod deletion.

After applying the live Helm change that removes a namespace from drain policy
(or disables drain), and after every drain that used it is `Settled`, verify
that no Pod there retains
`ops.cisco.vk/drain-session` or
`ops.cisco.vk/iosxe-rollout-drain`, then explicitly remove the retained cleanup
pair if drain will not be re-enabled. Replace `RELEASE_FULLNAME` with the Helm
generated full name (for example, `cvk-cisco-virtual-kubelet`). Review the
objects first and confirm their `operations.cisco.vk/drain-authority=cleanup`
label, Role rules, RoleBinding subject, and release ownership:

```bash
kubectl get role,rolebinding \
  RELEASE_FULLNAME-workload-drain -n edge-workloads -o yaml
kubectl delete role,rolebinding \
  RELEASE_FULLNAME-workload-drain -n edge-workloads
```

PDBs govern voluntary Kubernetes eviction; they do not prove network
redundancy, forwarding health, storage portability, or that a switch outage is
safe. Qualify the application's behavior and every target IOS-XE
release/platform combination independently. This phase continues to use the
existing combined IOS-XE install/activate leaf. A separately durable
stage/approve/activate protocol is not implemented or implied.

The manager-side Eviction/PDB, identity, reservation, and recovery mechanics
are deliberately above the IOS-XE driver. A future NX-OS or IOS XR adapter can
reuse them only after it implements exact session-authorized device teardown,
complete post-teardown device workload inventory, and a lifecycle with equally
strong mutation/outcome guarantees. No current non-IOS-XE driver gains drain
or software-rollout support from this gate, and the public API remains IOS-XE
specific until a second qualified driver proves the common contract.

### Submit, review, and approve

Start from
[`examples/topology/iosxe-software-rollout.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/topology/iosxe-software-rollout.yaml).
Create a new object for every upgrade or downgrade; executable intent is
immutable. A downgrade is the same workflow with a qualified lower
`targetVersion` and its exact compatible image/digest.

The creator must have the deliberately unbound rollout-planner role, and
`spec.plan.requestedBy` must exactly equal the authenticated Kubernetes
username. A campaign must start with neutral `spec.control.revision: 0`.

After creation, wait for `AwaitingApproval` and review the complete frozen
target/source/policy snapshot:

```bash
kubectl get iosxesoftwarerollout cat9k-17-18-4 \
  -n network-devices -o yaml

PLAN_HASH="$(kubectl get iosxesoftwarerollout cat9k-17-18-4 \
  -n network-devices -o jsonpath='{.status.frozenPlan.hash}')"
printf '%s\n' "$PLAN_HASH"
```

Review the qualification cohorts as part of that approval. Every selected
device must carry a protected
`operations.cisco.vk/qualification-cohort=<hardware-capability-class>` label.
`spec.plan.canaries` must contain one entry whose `name` exactly equals each
distinct cohort value, with at least one explicit target device in that entry.
The manager rejects missing cohorts, mismatched membership, duplicate canary
assignments, and a cohort without a canary. It freezes and revalidates the
cohort on every target, so later device-label or generation drift requires a
new plan. This prevents one successful hardware class from implicitly
qualifying a different class; incompatible hardware/image combinations need
separate qualified images or separate campaigns. The label is an
administrator assertion backed by lab evidence, not hardware auto-discovery.

Approval is append-only and authorizes only that exact hash. The approver must
have the separately delegated rollout-approver role, and `approvedBy` must
match that request's authenticated identity:

```bash
APPROVER="$(kubectl auth whoami -o jsonpath='{.status.userInfo.username}')"
APPROVED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

kubectl patch iosxesoftwarerollout cat9k-17-18-4 \
  -n network-devices --type=merge \
  -p "{\"spec\":{\"approval\":{\"planHash\":\"${PLAN_HASH}\",\"approvedBy\":\"${APPROVER}\",\"approvedAt\":\"${APPROVED_AT}\"}}}"
```

Do not copy this command under a different identity; admission will reject the
asserted username. Once published, the optional `status.frozenPlan` parent
cannot be removed and re-added or replaced; API-server transition validation
enforces this even for manager-authenticated status writes. Policy updates are
classified rather than treating every
ConfigMap `resourceVersion` as executable drift:

- metadata-only or canonically equivalent rewrites update the recorded audit
  resourceVersion without changing the active safety epoch;
- compatible numeric changes start a durable next epoch. The effective target
  cap, fleet/domain transfer and unavailable ceilings, health-freshness limit,
  active-reservation cap, and ledger-size cap are the minimum of the frozen,
  previously effective, and current values, so an existing rollout can never
  be loosened by a policy edit; and
- changing policy/ledger identity, policy version, selector, required or
  projected topology-key sets, domain-key or optional-budget shape, or setting
  `maxCampaignTargets` below the frozen target count sets
  `PolicyChanged=True` with `Reason=ReplanRequired` and requires a new rollout.

During an epoch transition the campaign pauses and publishes a durable
`status.policyTransition` before revoking any unclaimed authority. Zero-claim
leaves release their old reservations and may be rearmed only after the new
`status.effectivePolicy.epoch` is published. The manager admission, worker
acknowledgement, reservation, and every mutation claim all carry that exact
epoch. Pending and granted manager admission also carries the exact
32-character topology-lock acquisition ID shared with the reservation and
device lock. A stale cleanup or worker grant therefore cannot target a later
acquisition that happens to reuse a reservation name. A claimed mutation is
never rearmed or abandoned by a policy change.

The frozen target also binds the CiscoDevice UID and `metadata.generation`;
any CiscoDevice spec change, including its driver, management address/port, or
trust-reference selection, requires re-planning and re-approval. In-place data
rotation of an already bound Secret keeps the same Secret UID and is allowed
only after purpose, endpoint, and trust checks pass again. Delete/recreate
Secret replacement is not allowed under the old plan. Granted work that the
worker has not claimed is revoked and its reservation is released.
Already-claimed work keeps its reservation and is observed to a conclusive
outcome; policy or target drift never silently expands or abandons existing
mutation authority.

### Admission and execution behavior

For each target, the manager checks:

- same namespace and frozen CiscoDevice UID and generation;
- driver `XE`, image-family match, unique declared physical identity, exact
  Node UID, projection hash, and worker protocol;
- deterministic source selection still resolves to the exact frozen endpoint
  and Secret incarnation;
- fresh device and Node health, including current producer observations for
  `NodeIdentityReady`, `TopologyReady`, and `GNOIConfigurationReady`;
- the selected workload policy: `BlockIfRunning` requires no live Kubernetes
  or device workload, while `Drain` requires the exact bounded eligibility,
  PDB, session, teardown, replacement-readiness, and inventory proofs above;
- global and every independent domain transfer/unavailability ceiling;
- existing unhealthy or maintained fleet members, including non-targets; and
- no conflicting device mutation.

Health freshness is manager-authenticated rather than inferred from whichever
status field changed most recently. The manager writes
`CiscoDevice.status.healthObservation` with its observation time, the exact
bound Node `Ready` heartbeat time, and a SHA-256 hash of the current device
phase and complete condition set. Planning, admission, and post-operation
settlement re-read both live objects. The device-condition hash must still
match. The live Node must remain `Ready=True`, its heartbeat cannot regress,
and its nonzero Ready transition time cannot be later than the snapshotted
heartbeat. A later live heartbeat with that unchanged transition proof is
treated only as informer/API read skew: the authenticated snapshot heartbeat,
not the newer live value, remains the freshness bound. The effective health
time is the oldest of the manager observation, snapshotted Node heartbeat, and
required condition-producer observations (and is capped at current time), so
refreshing one source cannot make older evidence fresh and a future-dated
heartbeat cannot extend authority. A device phase/condition hash change,
non-True Node Ready state, heartbeat regression, or Ready transition after the
snapshot invalidates the proof until the manager records a new snapshot; the
post-operation gate additionally requires that authenticated time to be
strictly later than leaf completion.

The first Node binding deliberately leaves `healthObservation` absent; identity
creation is not health evidence. Each fixed readiness condition also requires
an explicit, current manager producer observation. For those CiscoDevice
conditions, the controller does not fall back to `lastTransitionTime`, because
an old transition can remain unchanged while its external evidence becomes
stale or unavailable. Node Ready `lastTransitionTime` is used only to prove
state continuity across heartbeat skew, never as producer or freshness time.
`GNOIConfigurationReady=True` additionally requires the manager-owned worker
revision proof above. Deployment availability alone is insufficient: during a
Secret-driven `Recreate`, an old Pod or a heartbeat that predates the new Pod
cannot authorize gNOI work.

Campaign health is intentionally narrower than end-to-end network assurance.
Automated campaign gates prove a fresh bound Node `Ready` heartbeat,
manager-owned identity/topology/gNOI configuration observations, and the
upgrade leaf's lifecycle result. They do not yet prove production packet
forwarding, expected interface or routing-adjacency state, or complete
stack/supervisor role health. Those checks require lab prequalification and
explicit manual operational evidence until typed, freshness-bound IOS-XE
producers are implemented; absent evidence never counts as an automatic pass.

The single ConfigMap ledger is updated with resourceVersion compare-and-swap.
It is bounded to 256 records and 256 KiB by default. Phase 3A still
conservatively holds both transfer and disruption reservations for the whole
leaf. Source locality reduces WAN path cost but does not claim an independent
prefetch boundary. A Lease expiry never settles an accepted device mutation.

The final worker-side workload check is fail closed. Under `BlockIfRunning`,
terminating Kubernetes Pods still block; retained `Succeeded` or `Failed` Pods
alone do not. Any live app or orphan returned by the device driver blocks, and
a missing callback or inventory error blocks rather than assuming the device
is empty. Under `Drain`, only an exact selected teardown in the active session
may run, and its complete post-delete device inventory becomes durable worker
evidence before promotion. The provider coordination contract is
driver-neutral for future platforms, although campaign registration and the
software lifecycle remain explicitly IOS XE only.

Both combined and network-only workers select strict inventory through the
driver's explicit `DrainPodInventoryProvider` capability. The ordinary
`ListPods` reader is never substituted for this complete scan; a missing
capability or a partial-inventory error blocks a new device mutation.

Campaign phases are `AwaitingApproval`, `Paused`, `Executing`, `Soaking`,
`Cancelling`, `Succeeded`, `Failed`, and `Cancelled`. The explicitly frozen
canary from every qualification cohort is wave zero; non-canaries are wave one
and remain ineligible until every cohort's canary has settled its
post-operation checks. Budgets still serialize admission within a wave. A
completed leaf is not settled immediately: its effective manager-authenticated
health time, including the snapshotted Node heartbeat and required producer
observations, must be strictly later than leaf completion. A newer live
heartbeat alone cannot satisfy this gate. The device and Node must then remain
continuously healthy for the configured canary or wave soak interval. A stale
or unhealthy observation resets progress rather than consuming an elapsed
timer. `status.counts.cancelled` distinguishes targets fenced by cancellation
from failed targets.

Before a new device RPC, the worker commits an append-only, stage-specific
mutation claim bound to the current reservation, policy epoch, and control
revision. It must first publish a worker-control acknowledgement of that same
epoch and grant. An ambiguous install, activation, or rollback result keeps the
reservation and pauses safe progression until an operator reconciles physical
state.

`rollbackOnFailure: true` is the existing limited leaf attempt to reactivate
the previously observed version after post-verify failure. It is not a
fleet-wide transaction and is not a substitute for a separately planned
downgrade campaign.

### Pause, resume, and cancel

Only the rollout-planner role carries the custom `control` permission. Every
change increments `revision` and binds `requestedBy` to the authenticated
caller. Example pause:

```bash
REQUESTER="$(kubectl auth whoami -o jsonpath='{.status.userInfo.username}')"
REQUESTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

kubectl patch iosxesoftwarerollout cat9k-17-18-4 \
  -n network-devices --type=merge \
  -p "{\"spec\":{\"control\":{\"revision\":1,\"pause\":true,\"requestedBy\":\"${REQUESTER}\",\"requestedAt\":\"${REQUESTED_AT}\",\"reason\":\"change freeze\"}}}"
```

Resume with a larger revision and `pause:false`. Cancel with a larger revision
and `cancel:true`; cancellation is terminal. Pause/cancel prevents future
claims but cannot undo an RPC already accepted by the switch. Inspect
`status.control.requestedRevision`, `effectiveRevision`, `pausePending`, and
`cancellationPending` rather than assuming a patch is immediately effective.

## Observe and troubleshoot

Campaign status is intentionally bounded. Use status for current state,
retained leaf objects for per-device evidence, Events for transitions, and
logs/traces for detailed correlation.

```bash
kubectl get iosxesoftwarerollouts -A
kubectl get iosxesoftwareupgrades -n network-devices -w

kubectl get events -n network-devices \
  --field-selector involvedObject.kind=IOSXESoftwareRollout \
  --sort-by=.lastTimestamp

kubectl logs -n cisco-vk-system \
  deploy/cvk-cisco-virtual-kubelet-controller -f

kubectl get pods -n network-devices \
  -l app.kubernetes.io/name=cisco-virtual-kubelet
kubectl logs -n network-devices POD_NAME -f
```

Useful campaign fields are:

- `status.phase`, `status.message`, and `status.conditions`;
- `status.frozenPlan.hash`, policy identity, source identity, target UIDs,
  topology snapshots, cohort, and deterministic leaf names;
- `status.counts` and each bounded `status.targets[]` reason; and
- requested versus effective control revisions.

For a drain target, the retained leaf is the durable, detailed record:

```bash
kubectl get iosxesoftwareupgrade LEAF_NAME -n network-devices \
  -o jsonpath='{.status.managerDrain.state}{"\n"}{range .status.managerDrain.pods[*]}{.namespace}{"/"}{.name}{"\t"}{.uid}{"\t"}{.phase}{"\t"}{.deviceCleanInventoryRevision}{"\n"}{end}'

kubectl get iosxesoftwareupgrade LEAF_NAME -n network-devices \
  -o jsonpath='{.status.workerDrain}{"\n"}'

kubectl get ciscodevice DEVICE_NAME -n network-devices \
  -o jsonpath='{.status.maintenanceSession}{"\n"}'

kubectl get node NODE_NAME \
  -o jsonpath='{.spec.unschedulable}{"\n"}{.spec.taints}{"\n"}'
```

`status.managerDrain` records the immutable session and per-Pod progress;
`status.workerDrain` records the fresh device inventory revision, completeness,
remaining selected UIDs, and bounded foreign/unknown counts. The manager
Deployment log explains campaign/reconciliation failures, while the exact
per-device worker log contains provider teardown, Lease-renewal, inventory,
and gNOI execution failures. Status is the durable source of truth: a missing
log line, process restart, or expired log retention is not proof that a step
did or did not occur. Export leaf status, Events, both log intervals, the Node,
the CiscoDevice, the mutation Lease, and relevant PDBs for a change record.

The manager exports bounded Prometheus series without campaign, device,
domain, image, or policy values as labels:

- `cisco_vk_topology_projection_reconciliations_total{result}` with the bounded
  `projected`, `skipped`, `error`, or `other` result;
- `cisco_vk_topology_rollout_reconciliations_total`;
- `cisco_vk_topology_rollout_target_transitions_total`;
- `cisco_vk_topology_rollout_admission_waits_total`;
- `cisco_vk_topology_rollout_ledger_active_reservations`; and
- `cisco_vk_topology_rollout_ledger_serialized_bytes`.

Full identities belong in structured logs and trace correlation, never metric
labels. Secrets and credential material must not appear in any signal.

Common fail-closed states include:

| Symptom/reason | Meaning and next check |
| --- | --- |
| `TopologyIncomplete` | required metadata label absent/invalid; repair inventory before re-enrollment |
| `TopologyConflict` | legacy and authoritative values differ, reclassification is unapproved, or Node identity is foreign |
| `WorkerProtocolPending` | split worker planes have not completed profile/admission handoff |
| `WorkerRolloutPending` | desired PodTemplate/Secret revision has not been reported by the exact new Ready worker Pod |
| `AdmissionBlocked` | workload, health, mutation, global, or domain budget gate is closed |
| `DrainRecovering` | normal drain progress stopped or device work concluded; no new eviction starts while exact cleanup and owned-guard restoration continue |
| `DrainCleanupBlocked` / `DrainGuardRecovery` | accepted teardown lacks device-clean proof, or exact scheduling/session state has not been safely restored |
| `WorkloadRecovery` | the exact ReplicaSet/Deployment or PDB UID, its current drain-safe template/selector, or current replica health is not recovery-safe |
| `HealthyPostMutationSoak` | device and replacement workload health currently pass; the reservation remains held for the configured continuous soak |
| `PolicyChanged` / policy identity failure | structural policy/ledger identity or incompatible target-cap change; stop new claims and create a new plan |
| `PolicyTransition` | a compatible numeric policy edit is fencing zero-claim leaves before publishing a stricter monotonic safety epoch |
| target identity/generation change | CiscoDevice was replaced or its executable spec changed; stop new claims and create a new approved plan |
| `SourceIdentityChanged` | source Secret UID or frozen endpoint changed |
| `MutationOutcomeUnresolved` | switch may have accepted a mutation; do not retry or release from a timer |
| ledger UID/decoding error | retain all guards; reconcile ledger and physical state through audited break-glass; admission rejects empty updates, the manager validates replacement format/UID, and delete/recreate is unsupported |

## Delegated authorization

The chart creates these ClusterRoles but binds none of them:

| Role suffix | Delegated action |
| --- | --- |
| `-topology-author` | change protected topology/risk labels and adoption/reclassification approvals |
| `-rollout-planner` | create campaigns and issue monotonic pause/resume/cancel controls |
| `-rollout-approver` | approve one exact frozen-plan hash |
| `-topology-policy-admin` | change coherent policy content, not its ledger UID binding |
| `-topology-ledger-breakglass` | exceptional ledger recovery; normally unbound |

Use namespace RoleBindings and keep planner, approver, policy administrator,
and ledger recovery identities separate where separation of duties is
required. Kubernetes RBAC cannot constrain an arbitrary label selector's
result; the manager therefore applies the administrator fleet selector,
same-namespace rule, explicit `allowAll`, target cap, frozen target UIDs, and
exact-hash approval as additional authority boundaries.

Managed worker RBAC exposes four selectable roles. The manager binds the app
account to exactly one app role and the network account to exactly one network
role; it never stacks read-only and read-write. The app read-write role has no
Node metadata/spec, generic Pod update/create, Pod log/exec, Lease create/delete,
RBAC, token, or Secret write authority. Its CiscoDevice and operation-risk
reads come from an implementation-only namespaced support role, never its
cluster-bound profile. Both running planes may create a
`SelfSubjectReview` to verify their Pod-bound token identity. The network roles
are namespaced; only the non-selectable global-read support role supplies
cluster-scoped reads. Network read-write has no Node/Pod mutation, RBAC, token,
or cluster-wide Secret grant.
Its bounded upgrade/action main/status verbs are stable so two Helm releases
cannot race to redefine a cluster-shared profile. The explicit runtime gate
still decides whether each mutation controller is registered, and native
admission independently enforces operation ownership and protocol.

Admission also reserves the two functional ServiceAccount objects to the
manager, permits their TokenRequest subresource only to a kubelet for an exact
Pod-bound token, and denies legacy ServiceAccount-token Secrets. This closes
the otherwise-valid path where another token-issuing principal could reuse the
shared username without the manager-bound worker Pod incarnation.

Network worker names use the full immutable CiscoDevice UID before the
generated suffix (`u<device-uid>-network-...`), independently of the device
name length. This keeps the identity intact when Kubernetes truncates
generated Pod names. The UID-first naming transition rotates prior worker
accounts through the existing policy-epoch settlement and cleanup sequence;
it does not run old and replacement network workers concurrently.

At startup, the manager derives one worker-account policy epoch from every
verified admission contract that protects reusable worker credentials: shared
and generated ServiceAccount ownership, bound TokenRequest issuance, legacy
ServiceAccount-token Secret prevention, and reserved Deployment, ReplicaSet,
Pod, and Pod-update behavior. The epoch hashes each policy and binding UID,
generation, and compiled Spec digest; `resourceVersion`, labels, and annotations
are intentionally excluded. Every shared and generated worker ServiceAccount
is stamped with that epoch. An account created before this boundary—or under an
older contract—is never adopted: the manager revokes its exact bindings first,
deletes the old ServiceAccount to invalidate every token tied to its UID,
foreground-drains workloads using it, and only then creates the replacement.
This one-time guarded migration also invalidates a legacy JWT whose
immutable-type ServiceAccount-token Secret had its mutable ServiceAccount name
and UID annotations changed to a different existing, non-reserved account.
Legacy authentication uses the signed claims and token bytes rather than those
mutable annotations; UID rotation invalidates the claimed old ServiceAccount
directly.
For a normal chart-policy epoch change, the network-management account is not
rotated until every device in the namespace has settled its mutation Lease,
maintenance session, admitted software-upgrade leaf, and rollout reservation.
The independent app-hosting account completes UID rotation, RBAC regrant, and
Deployment recreation without waiting on an unrelated network mutation; the
blocked network account and workload remain unchanged. Generated per-device identities use the same per-device
authority-settlement proof before an epoch-only rotation. A phase-zero legacy
worker has no durable managed Node identity for the complete proof, so the
manager preserves its old UID and running workload and reports a blocker; after
the operator verifies device operations are idle and removes that workload, the
manager performs the UID rotation. The transition reports the blocking object
and retries, so an upgrade does not silently interrupt an active gNOI or
configuration mutation. A concrete compromise signal—unsafe namespace RBAC,
malformed generated workload/access, or an explicitly attributable legacy
token Secret—takes the fail-closed quarantine path immediately instead of
waiting.

Shared-account quarantine is authority-first and does not trust deterministic
RoleBinding names. It removes every namespaced RoleBinding whose exact subject
is the reserved account in both the device and configured Lease namespaces,
removes the known cluster-wide grants, deletes each exact-proven shared
ServiceAccount with a UID precondition to invalidate minted tokens, and then
terminates every workload using that account. Foreign objects that merely
collide with a reserved name are retained and reported. Direct API reads
repeat the binding audit after access is installed; a grant racing the initial
audit triggers the same synchronous quarantine before reconcile returns.
The manager can delete a reserved worker Pod only with its exact UID as a
precondition. Admission denies its direct deletion of ordinary application
Pods, including when a UID is supplied; quarantine does not grant a workload
deletion or PodDisruptionBudget bypass.
Native garbage collection can remove only its `foregroundDeletion` finalizer
from already-deleting reserved worker objects, preserving spec, status,
bindings and unrelated finalizers. This permits a foreground worker-identity
rotation to finish without administrator finalizer repair.
Generated-account compromise uses the same authority-first rule inside the
device namespace: every RoleBinding with the exact UID-derived account subject
is removed regardless of its name, role, or mutable metadata before the owned
ServiceAccount UID is deleted. Foreign or arbitrary cluster-wide bindings stay
inside the explicit cluster-admin trust boundary and keep reconciliation
failed closed for operator review.

Native Kubernetes node lifecycle reconciliation remains enabled: the exact
node-controller identity can mark silent Nodes `Unknown`, reconcile built-in
health taints, and synchronize the deprecated OS/architecture label aliases.
It cannot publish healthy readiness, rewrite projected topology or worker
bindings, remove CVK maintenance guards, change capacity, or clear a cordon.
This keeps stale virtual Nodes out of ordinary scheduling without assigning
CVK identity authority to a native controller.

The manager pre-creates purpose-bound heartbeat, config-family, and mutation
Leases. App read-write and network read-write may update only the Lease scopes
they need; read-only network management can observe but not mutate them.
Admission checks the manager-owned device/Node/Pod bindings on each write,
rather than deriving authority from a per-device username.

Shared identities deliberately move the residual blast radius from one device
to one tenant namespace. The app read-write account still needs cluster-wide
Pod and workload-input reads because a virtual Node can receive Pods from any
namespace; Kubernetes RBAC cannot limit Secret reads to dynamically referenced
Pod inputs. A compromised worker can also attempt peer writes under the same
account, so fail-closed admission—not process filtering—must enforce object
binding. This is not equivalent to Node Authorizer/NodeRestriction. Use
separate namespaces for mutually untrusted fleets, audit every profile binding,
and treat app-hosting workers as cluster-trusted workloads until node-scoped
credentials or manager-mediated status are qualified. Because upstream
`nodeutil` constructs cluster-wide Pod, Secret, ConfigMap, and Service
informers, functional app hosting should run only in a dedicated trusted
workload cluster, or where every workload namespace accepts the same
Secret-read trust boundary. A compromised app read-write worker exposes those
cluster-wide reads; namespace separation cannot contain them.

## Rollback and retirement

There are two distinct rollback procedures.

For a failed or undesired IOS-XE version, settle any ambiguous operation first,
then create and approve a new `IOSXESoftwareRollout` whose target version and
digest describe the qualified downgrade. Do not mutate or replay an old
campaign.

For managed-topology feature retirement:

An installation that was retired with an older chart must first be upgraded
once with the current chart and `topology.enabled=true`. Older instructions
allowed the completed `topology.cisco.vk/request-legacy-handoff` annotation to
be removed, and admission intentionally does not allow that authorization to
be recreated after `status.nodeIdentity` is gone. If every selected retired
device still has its completed request, leave those annotations in place. If
any selected retired device does not, use a temporary non-empty fleet selector
that matches none of the retired devices for this topology-enabled upgrade
(for example, require an absent
`topology.cisco.vk/retirement-migration-hold=selected` label), and verify the
selector result is empty before upgrading. This hold prevents an unintended
forward enrollment; without it, the device will be deliberately re-enrolled
and must complete another legacy handoff before topology can be disabled.

Wait for the controller rollout and its managed-admission preflight to
succeed; this installs and verifies the retained
`<release>-cisco-virtual-kubelet-legacy-node-marker` policy and binding that
protect released Nodes after the controller is disabled. Only then perform a
second Helm upgrade with `topology.enabled=false`, retaining the same policy
identity and temporary selector values. A direct transition from an older
retired release to the current disabled chart is rejected when retained
topology state exists but that guard is absent or stale. An installation that
has never created retained topology state does not require this migration.
The guard's digest annotation is an installation/version stamp used by Helm;
Helm does not recompute the live policy Spec. The exact server-stored Spec was
already hashed and checked by the topology-enabled manager preflight.

1. stop creating campaigns and request an effective pause;
2. resolve every claimed mutation and wait for all reservations, leaves, and
   maintenance sessions to settle;
3. export campaign, leaf, policy, ledger, Event, and redacted log evidence;
4. while `topology.enabled=true`, set
   `topology.cisco.vk/request-legacy-handoff` on each managed CiscoDevice to
   that object's current `status.nodeIdentity.nodeUID`, using an identity with
   the delegated `topology` verb;
5. wait for `status.legacyHandoff.phase=Complete` on every requested device and
   verify `status.nodeIdentity` is absent, the Node carries the matching
   `topology.cisco.vk/legacy-handoff=<node UID>` audit marker, the configured
   namespace-shared legacy worker is Ready with a heartbeat later than
   `isolatedReadyAt`, and the temporary UID-scoped ServiceAccount and bindings
   are absent;
6. run a live Helm upgrade with the same release, policy namespace/name, and
   ledger name, keeping `controller.leaderElect=true` and
   `aggregator.enabled=false`, then set `topology.enabled=false`; and
7. verify the two functional accounts and all managed profile bindings are
   removed. The controller has already restored the configured legacy account
   for topology-disabled compatibility; it is not a managed-topology identity.

If workload drain was ever enabled, include each retained cleanup Role/Binding
in the evidence export. After step 2, verify that its namespace has no reserved
drain marker/finalizer, then remove the exact pair with the command above before
disabling managed topology.

For example, repeat this for each managed device and do not remove the request
while topology remains enabled—the selected device would otherwise begin a new
forward enrollment:

```bash
DEVICE_NAMESPACE=network-devices
DEVICE_NAME=edge-01
NODE_UID="$(kubectl get ciscodevice "$DEVICE_NAME" \
  --namespace "$DEVICE_NAMESPACE" \
  -o jsonpath='{.status.nodeIdentity.nodeUID}')"
kubectl annotate ciscodevice "$DEVICE_NAME" \
  --namespace "$DEVICE_NAMESPACE" \
  topology.cisco.vk/request-legacy-handoff="$NODE_UID" --overwrite
kubectl wait ciscodevice "$DEVICE_NAME" \
  --namespace "$DEVICE_NAMESPACE" \
  --for='jsonpath={.status.legacyHandoff.phase}=Complete' --timeout=30m
```

The reverse handoff is durable and fail closed: `Preparing` records the
authorized immutable identity, `LegacyWriterPending` records that managed API
authority is revoked and Node release is durably authorized,
`SharedWriterPending` records that the temporary UID-scoped worker proved a
post-release heartbeat and is being replaced under `Recreate`, and `Complete`
is published only after the configured namespace-shared compatibility worker
proves a newer Ready heartbeat and the temporary ServiceAccount and bindings
are deleted. The per-device identity is therefore transition-only; in managed
steady state the app-hosting and network-management accounts remain the only
worker identities. The legacy shared account exists only for devices returned
to topology-disabled compatibility mode.
If a crash occurs while creating the temporary identity before its
cluster-wide binding and marker exist, a topology-disabled restart validates
the partial ServiceAccount/RoleBinding as exact and device-owned, deletes it
with UID preconditions, proves absence, and continues through the ordinary
compatibility path. Drift or an additive binding remains fail closed for
operator review; partial namespaced metadata is deliberately not used to
force global retirement preflight because it is not cluster-admin evidence.
Once `Preparing` exists, admission freezes the consumed
`topology.cisco.vk/request-legacy-handoff` value against change or removal
until the handoff reaches `Complete`; the controller does not re-read mutable
authorization midway through the protocol. Device deletion is likewise denied
until `Complete`, preventing lifecycle cleanup from interrupting the
single-writer transfer.
The manager clears its Node identity/projection/health status after isolated
readiness, before the Recreate replacement, while the durable phase prevents
forward enrollment until the shared readiness proof and credential retirement
finish. The request annotation can be removed by a topology-author
after topology is disabled; the Complete status and Node audit marker remain
identity state.

An interrupted upgrade from the earlier per-device model has one additional
recovery state: exact generated legacy RBAC can exist before its UID marker.
If topology is disabled in that window, the retained boundary detects the
cluster binding; the disabled manager audits the complete ServiceAccount and
bindings, writes the exact CiscoDevice-UID marker, and keeps that isolated
worker rather than guessing that the namespace-shared legacy identity is safe.
This is a supported fail-closed recovery state, not a completed retirement. To
remove it, re-enable topology, let forward enrollment prove the shared managed
worker and retire the generated identity/marker, then perform the normal
reverse handoff above before disabling topology again. Do not delete the marker
or any one binding by hand.

The live downgrade check rejects missing or UID-mismatched policy/ledger
objects, incomplete manager RBAC, any remaining `status.nodeIdentity`, and any
handoff phase other than `Complete`. The policy, ledger, versioned policy/binding
pairs, fixed functional profile roles, and supplemental manager role/binding carry
`helm.sh/resource-policy: keep`. Setting `topology.enabled=false` or
uninstalling Helm does not remove them. In-flight handoff workers and retained
manager-owned audit state still rely on native admission, so these objects are
an active safety boundary, not stale installation debris. Remove them only
after deleting every dependent split worker, functional ServiceAccount/binding,
Node marker, and durable device state.
Never delete/recreate only the ledger or a same-name CiscoDevice to clear a
failure; both changes produce new UIDs and invalidate durable authority.

Offline `helm template` and GitOps pruning cannot perform or prove this
retirement because Helm `lookup` has no live objects to inspect. Use the live
upgrade above. If an existing GitOps system must remain active during the
transition, exclude existing CVK RBAC and retained topology objects from prune
until the controller-reported handoff is complete and the live Helm check has
succeeded.

## Qualification checks

Run static chart checks:

```bash
helm lint charts/cisco-virtual-kubelet
charts/cisco-virtual-kubelet/tests/topology-render-test.sh
```

On a caller-selected disposable Kubernetes 1.35+ cluster with the default
scheduler, run:

```bash
charts/cisco-virtual-kubelet/tests/topology-kind-test.sh
```

The real-cluster test proves policy compilation plus the live API-server-stored
Spec digest, positive/negative worker Node and Pod status admission, generated
worker completion of an already-terminating own-Pod deletion, live/peer Pod
deletion denial, absence of `deletecollection`, unmarked peer denial,
purpose-specific Lease update and create/delete fencing, complete
CiscoDevice status/finalizer protection, campaign control separation, ledger
protection, native affinity/spread binding, initialization-taint exclusion, and
the known direct-`nodeName` bypass. With drain enabled in its disposable
values, it also proves the drain Pod marker/finalizer ownership boundary,
manager/worker status separation, immutable snapshot transitions, worker
configuration rollover only with fresh inventory, and purpose-bound Lease updates.
It grants adversarial direct-delete RBAC to prove that protected Pods still
reject DELETE, then proves a manager
`policy/v1` Eviction succeeds when the PDB permits it, a PDB denial returns 429
without marking the Pod for deletion, and exact finalizer cleanup completes an
accepted eviction. It does not run a device worker or contact a network device,
so provider teardown, device-clean promotion, IOS-XE behavior, and end-to-end
workload recovery remain physical-lab acceptance tests.

Before enabling drain outside a disposable cluster, an acceptance test must
separately prove that a PDB denial causes no eviction or gNOI promotion, an
accepted Eviction reaches device-clean evidence, replacement readiness is
required between selected Pods, worker configuration rotation during pending
teardown requires new device inventory, and manager/worker restarts at every
durable state preserve both workload and device fences.
With Docker and kind available, run the isolated shared-worker qualification;
it refuses to reuse an existing cluster and owns and removes the exact named
Kubernetes 1.35 cluster:

```bash
charts/cisco-virtual-kubelet/tests/managed-shared-worker-kind-test.sh \
  --cluster-name cvk-shared-worker-qualification
```

The first real-cluster test proves policy compilation plus the live API-server-stored
Spec digest, positive/negative worker Node and Pod status admission, unmarked
peer denial, purpose-specific Lease update and create/delete fencing, complete
CiscoDevice status/finalizer protection, campaign control separation, ledger
protection, native affinity/spread binding, initialization-taint exclusion, and
the known direct-`nodeName` bypass. The second owns and removes its named kind
cluster and qualifies the two namespace-shared functional accounts, reserved
ServiceAccount and TokenRequest fencing, a positive API request authenticated
with an API-server-issued Pod-bound token, result binding scope, and the
locked-namespace trust boundary. Neither test contacts a network device.

Before a production device test, also verify secure gNOI certificates/auth,
gNOI OS provisioning, image digest/compatibility, configuration persistence,
health evidence, and physical redundancy. Synthetic labels on co-located
switches are not evidence of real multi-site fault tolerance.

## Deferred roadmap and limitations

The following are intentionally not implemented in the current phases:

- durable prefetch, shared/PVC cache, independent transfer-only admission, or
  claims that the worker's location represents the device data path (later
  Phase 3 work, pending measured need and a complete cache-loss protocol);
- general-purpose drain, StatefulSet/PVC/DaemonSet/Job/custom-controller
  evacuation, forced deletion or PDB bypass, and independently durable
  install/stage/activate reservations (later Phase 4 work);
- an authoritative discovered graph, graph-cost workload scheduling, a custom
  scheduler, or automatic declared-topology mutation;
- a mandatory dependency on alpha native Workload/PodGroup/TAS APIs; and
- a generic NX-OS/IOS XR rollout CRD before a second driver demonstrates
  compatible lifecycle guarantees (Phase 5).

The initial public campaign is IOS-XE-specific by design. NX-OS and IOS XR can
reuse internal planning, identity, reservation, and control mechanics only
after their transfer, activation, rollback, and verification guarantees are
proven. Unsupported drivers fail before child creation.
