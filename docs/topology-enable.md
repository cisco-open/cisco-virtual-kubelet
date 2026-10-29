# Enable managed topology and enroll devices

**For cluster administrators.** Establish the managed Node/worker boundary before
application placement or IOS-XE software campaigns. This procedure changes
identity and authorization, even when software mutation remains disabled.

Use the chart and CRDs from the same reviewed checkout as the manager and workers.
The examples use release `cvk` in `cvk-system` and device namespace
`network-devices`. For an existing installation, retain its actual release,
namespace, policy and ledger identities; substituting a new name is not a migration.

1. Check the prerequisites and namespace trust boundary below.
2. Choose worker profiles, apply CRDs and perform the live Helm transition.
3. Supply protected inventory labels and enroll devices through the writer handoff.
4. Verify identity, topology and worker readiness; run qualification only on an
   explicitly selected disposable cluster before a production change.

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

For the exact role attestation, producer and token boundaries, see the
[managed-profile validation contract](topology-reference.md#managed-profile-validation).
These checks supplement the namespace restrictions above; they do not make a
cluster administrator or compromised Helm actor untrusted.

## Configure and enable

Start from
[`examples/topology/managed-topology-values.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/v2026.11.0/examples/topology/managed-topology-values.yaml).
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
  --namespace cvk-system \
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
  -n cvk-system \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/ledger-uid}{"\n"}'

kubectl get configmap cvk-cisco-virtual-kubelet-topology-ledger \
  -n cvk-system \
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

## Verify the enrollment outcome

Success means the exact device/Node UID binding, protected labels and worker
revision agree; the required `NodeIdentityReady` and `TopologyReady` conditions
are current and the initialization guard is removed through the normal handoff.
For the network-observer baseline, software mutation remains unavailable.
Do not remove a guard or legacy identity manually to make readiness appear green.

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

## Next step

Proceed to [application placement](topology-scheduling.md). If you also need an
OS change, follow the separate [IOS-XE campaign prerequisites](topology-campaigns.md).
Use [observation commands](topology-troubleshooting.md) for blocked enrollment and
[retirement](topology-recovery.md) before changing retained ownership identities.
