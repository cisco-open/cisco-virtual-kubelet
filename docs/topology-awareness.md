# Managed topology and topology-aware IOS-XE rollouts

Managed topology is an opt-in CVK operating mode that gives the default
Kubernetes scheduler reliable device topology and gives the CVK manager a
bounded, topology-aware admission path for IOS-XE gNOI software campaigns. It
uses only Kubernetes-native API machinery: Nodes, labels, taints, RBAC,
ValidatingAdmissionPolicy, ConfigMaps, Leases, status conditions, Events, and
the default kube-scheduler. It installs no alternate scheduler, scheduling
plugin, webhook, or third-party topology controller.

The initial implementation deliberately covers roadmap Phases 0–2:

- correct Node identity, ownership, topology projection, capacity, and
  maintenance fencing;
- scheduling with ordinary affinity and topology-spread constraints; and
- a manager-side `IOSXESoftwareRollout` campaign above the existing,
  per-device `IOSXESoftwareUpgrade` executor.

Phases 3–5 are not hidden behind incomplete API fields. Mirror selection,
prefetch/cache optimization, PDB-aware drain, independently durable
stage/activate, an observed graph API, mandatory native TAS, and a public
multi-driver rollout API remain separate, evidence-gated work. See
[Deferred roadmap](#deferred-roadmap-and-limitations).

!!! warning

    Managed topology changes a cluster security and Node-writer boundary. It
    is disabled by default. Do not enable it on a production fleet until the
    admission probes, migration checks, secure gNOI path, and rollback
    procedure in this guide have passed on that cluster.

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
version, and built-in-resource expression warnings before enabling managed
workers. Each worker then performs positive and negative server-side dry runs
with its own credentials: a harmless Node-status write must succeed, while a
label write smuggled through `/status` must be denied. A version check or an
empty warning list alone is not proof that admission works.

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

Do not use `helm upgrade --force`. The policy and ledger are identity-bound by
their Kubernetes UIDs. Replacement is intentionally treated as a safety
failure, not as an empty new fleet.

The chart creates the administrator policy and an empty ledger. The manager
validates the whole `policy.json`, initializes `ledger.json` with the live
ledger UID, and adds that UID to the protected policy annotation. Confirm that
the UIDs match:

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
example shows one fleet member plus a two-replica hard spread; apply the
workload only after at least two Ready enrolled CVK Nodes expose the selected
site in distinct zones. It also includes a matching `policy/v1`
`PodDisruptionBudget` so availability intent is ready for ordinary voluntary
disruption and the deferred Phase 4 drain work. Phase 2 does not evict Pods or
consult the PDB: `BlockIfRunning` fails closed while either Kubernetes or the
device reports a live workload.

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
applications, but remember that the Phase 2 rollout does not evict Pods.

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
device fence. This post-fence check is required even when scheduling policy is
correct.

### Experimental in-tree Workload/PodGroup/TAS

Native Workload/PodGroup/TAS remains feature-gated upstream. Kubernetes 1.36
introduced [topology-aware workload scheduling](https://kubernetes.io/docs/concepts/workloads/workload-api/topology-aware-scheduling/)
as an alpha, disabled-by-default feature. Kubernetes 1.37 exposes the
[Workload and PodGroup resources](https://kubernetes.io/docs/concepts/workloads/podgroup-api/)
through `scheduling.k8s.io/v1beta1`, still disabled by default, and requires
the `GenericWorkload` and `TopologyAwareWorkloadScheduling` feature gates on
the relevant control-plane components. The v1beta1 API must also be enabled in
the API server runtime configuration. Its single topology constraint co-locates
the group in one label domain; this is different from topology spread, which
distributes replicas across domains.

For example, the repository's pinned kind qualification uses:

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
featureGates:
  GenericWorkload: true
  TopologyAwareWorkloadScheduling: true
runtimeConfig:
  scheduling.k8s.io/v1beta1: "true"
```

`WorkloadWithJob` is unnecessary because the example creates its runtime
PodGroup explicitly. `CompositePodGroup` is also intentionally disabled; it is
a separate alpha surface and is not required for single-level CVK topology.

The separately gated
[`experimental-native-tas-v1.37.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/topology/experimental-native-tas-v1.37.yaml)
shows a Workload template, standalone PodGroup, and Pods using only the default
scheduler and CVK-projected Node labels. It is deliberately excluded from the
Kubernetes 1.35 qualification test and must not be applied until discovery and
`kubectl explain` confirm the v1.37 fields on the target cluster. CVK does not
create or watch these APIs, so its current client-library line is not an
adapter; Node labels are the compatibility layer. The earlier v1.36 alpha
versioned schema is not promised by this example.

CI runs the example in a separate, pinned Kubernetes 1.37 kind cluster. The
test uses two synthetic Ready Nodes with different `topology.cisco.vk/site`
values, proves that a valid group is co-located, and proves that Pods forced
into different site domains remain unschedulable. This is API and scheduler
conformance only: it does not run a CVK worker, contact a device, or change the
Kubernetes 1.35 production support floor.

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

The managed campaign API accepts one digest-pinned source:

- anonymous HTTPS with verified TLS and no URL credentials; or
- SFTP with an endpoint-bound Secret and verified `knownHosts` data.

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

The manager freezes the Secret UID into the plan and managed leaf. In-place
credential rotation under that UID is allowed, but every use revalidates source
purpose, endpoint, and trust policy. Delete/recreate changes the UID, fences new
claims, and requires a newly frozen and approved rollout.

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
- fresh device and Node health, including current producer observations for
  `NodeIdentityReady`, `TopologyReady`, and `GNOIConfigurationReady`;
- no running workload (`BlockIfRunning` is the only Phase 2 policy), proven
  both from Kubernetes Pods and a live driver `ListPods` device inventory;
- global and every independent domain transfer/unavailability ceiling;
- existing unhealthy or maintained fleet members, including non-targets; and
- no conflicting device mutation.

Health freshness is manager-authenticated rather than inferred from whichever
status field changed most recently. The manager writes
`CiscoDevice.status.healthObservation` with its observation time, the exact
bound Node `Ready` heartbeat time, and a SHA-256 hash of the current device
phase and complete condition set. Planning, admission, and post-operation
settlement re-read both live objects: the Node heartbeat must still equal the
snapshot and the device-condition hash must still match. The effective health
time is the older of the manager observation and Node heartbeat (and is capped
at current time), so refreshing device conditions cannot make an old worker
heartbeat fresh and a future-dated heartbeat cannot extend authority. A source
change invalidates the snapshot until the manager observes the new pair; the
post-operation gate additionally requires that authenticated time to be
strictly later than leaf completion.

The first Node binding deliberately leaves `healthObservation` absent; identity
creation is not health evidence. Each fixed readiness condition also requires
an explicit, current manager producer observation. The controller does not
fall back to a condition's `lastTransitionTime`, because an old transition can
remain unchanged while its external evidence becomes stale or unavailable.
`GNOIConfigurationReady=True` additionally requires the manager-owned worker
revision proof above. Deployment availability alone is insufficient: during a
Secret-driven `Recreate`, an old Pod or a heartbeat that predates the new Pod
cannot authorize gNOI work.

Phase 2 health is intentionally narrower than end-to-end network assurance.
Automated campaign gates prove a fresh bound Node `Ready` heartbeat,
manager-owned identity/topology/gNOI configuration observations, and the
upgrade leaf's lifecycle result. They do not yet prove production packet
forwarding, expected interface or routing-adjacency state, or complete
stack/supervisor role health. Those checks require lab prequalification and
explicit manual operational evidence until typed, freshness-bound IOS-XE
producers are implemented; absent evidence never counts as an automatic pass.

The single ConfigMap ledger is updated with resourceVersion compare-and-swap.
It is bounded to 256 records and 256 KiB by default. The MVP conservatively
holds both transfer and disruption reservations for the whole leaf. A Lease
expiry never settles an accepted device mutation.

The final worker-side workload check is fail closed. Terminating Kubernetes
Pods still block; retained `Succeeded` or `Failed` Pods alone do not. Any live
app or orphan returned by the device driver blocks, and a missing callback or
inventory error blocks rather than assuming the device is empty. The callback
contract is driver-neutral for future platforms, although Phase 2 campaign
registration remains explicitly IOS XE only.

Campaign phases are `AwaitingApproval`, `Paused`, `Executing`, `Soaking`,
`Cancelling`, `Succeeded`, `Failed`, and `Cancelled`. The explicitly frozen
canary from every qualification cohort is wave zero; non-canaries are wave one
and remain ineligible until every cohort's canary has settled its
post-operation checks. Budgets still serialize admission within a wave. A
completed leaf is not settled immediately: its bound Node must report
`Ready=True` with a `lastHeartbeatTime` strictly later than the leaf completion,
and the device and Node must then remain continuously healthy for the configured
canary or wave soak interval. A stale or unhealthy observation resets progress
rather than consuming an elapsed timer. `status.counts.cancelled` distinguishes
targets fenced by cancellation from failed targets.

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
| `PolicyChanged` / policy identity failure | structural policy/ledger identity or incompatible target-cap change; stop new claims and create a new plan |
| `PolicyTransition` | a compatible numeric policy edit is fencing zero-claim leaves before publishing a stricter monotonic safety epoch |
| target identity/generation change | CiscoDevice was replaced or its executable spec changed; stop new claims and create a new approved plan |
| `SourceIdentityChanged` | source Secret UID or frozen endpoint changed |
| `MutationOutcomeUnresolved` | switch may have accepted a mutation; do not retry or release from a timer |
| ledger UID/decoding error | retain all guards; reconcile ledger and physical state through audited break-glass |

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
Generated-account compromise uses the same authority-first rule inside the
device namespace: every RoleBinding with the exact UID-derived account subject
is removed regardless of its name, role, or mutable metadata before the owned
ServiceAccount UID is deleted. Foreign or arbitrary cluster-wide bindings stay
inside the explicit cluster-admin trust boundary and keep reconciliation
failed closed for operator review.

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

The following are intentionally not implemented in Phases 0–2:

- topology-local mirror selection, durable prefetch, shared cache, or claims
  that the worker's location represents the device data path (Phase 3);
- automatic eviction/drain, PDB orchestration, or independently durable
  install/stage/activate reservations (Phase 4);
- an authoritative discovered graph, graph-cost workload scheduling, a custom
  scheduler, or automatic declared-topology mutation;
- a mandatory dependency on alpha native Workload/PodGroup/TAS APIs; and
- a generic NX-OS/IOS XR rollout CRD before a second driver demonstrates
  compatible lifecycle guarantees (Phase 5).

The initial public campaign is IOS-XE-specific by design. NX-OS and IOS XR can
reuse internal planning, identity, reservation, and control mechanics only
after their transfer, activation, rollback, and verification guarantees are
proven. Unsupported drivers fail before child creation.
