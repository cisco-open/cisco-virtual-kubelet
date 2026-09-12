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
    M --> N[Identity-bound Kubernetes Node]
    N --> S[Default kube-scheduler]
    S --> W[Device-hosted workload]
    R[IOSXESoftwareRollout] --> M
    M --> L[CAS reservation ledger]
    M --> U[Immutable IOSXESoftwareUpgrade leaf]
    U --> K[Identity-bound per-device worker]
    K --> G[Secure IOS-XE gNOI service]
```

The ownership split is strict:

| Surface | Authority |
| --- | --- |
| Desired stable topology and operational risk domains | protected `CiscoDevice.metadata.labels` |
| Stable physical chassis identity used for deduplication | write-once operator-declared `CiscoDevice.spec.physicalIdentity` |
| Node name, UID binding, projected labels, static/maintenance taints | manager |
| Node status and device execution | that Node's generated per-device worker identity |
| Pod placement | default kube-scheduler |
| Fleet plan, policy evaluation, reservations, child admission | manager |
| Image transfer, install, activation, verify, limited rollback | existing IOS-XE leaf executor in the device worker |

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

Enabling topology also removes the release-wide worker credential as a trust
boundary. Every CiscoDevice gets one controller-owned, device-UID-derived
ServiceAccount whose name embeds its resolved virtual Node. Selected devices
use `cisco-vk-managed-<node>-<uid-hash>` with the managed status-only role. Devices
outside the managed selector keep the legacy runtime and permissions under
`cisco-vk-legacy-<node>-<uid-hash>`, but native admission confines their Node
and Pod-status writes to that exact unmarked Node. The default,
topology-disabled mode retains the historical shared ServiceAccount unchanged.
The manager records `topology.cisco.vk/isolated-legacy-worker=<device UID>` on
each device assigned an isolated legacy identity. Admission forbids users from
creating, changing, or removing that authority marker; the manager may create
it only with the live CiscoDevice UID, and it is immutable thereafter.

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
- one authoritative managed-topology CVK release per cluster;
- per-device workers (`aggregator.enabled=false`);
- manager leader election (`controller.leaderElect=true`), which reduces
  overlapping reconciliation while ledger/CAS and admission remain the
  durable safety fence across failover;
- the fixed default VK role name (`serviceAccount.vkName` remains
  `cisco-virtual-kubelet`) and `rbac.profile=strict`;
- `gnoi.enableWriteClass=false`; Phase 2 admits only campaign-owned
  `IOSXESoftwareUpgrade`, not generic `IOSXEOperationalAction` mutations;
- CRDs applied before the manager Deployment is upgraded;
- all eight native admission policies and bindings installed with
  `failurePolicy: Fail` and `validationActions: [Deny]`;
- an administrator-owned, non-empty managed-fleet selector;
- complete, valid values for every required topology key on each enrolled
  `CiscoDevice`; and
- an explicit `spec.maxPods` from 1 through 110 on every selected device.

The `maxPods` bound is a managed-topology scheduling invariant. Standalone
mode deliberately keeps its historical compatibility behavior: values at or
below zero fall back to 16, and otherwise the existing `int32` value is used.

The default VK role name is currently an implementation invariant, not a
cosmetic restriction: dynamic per-device bindings still reference the fixed
`cisco-virtual-kubelet` and `cisco-virtual-kubelet-device` roles. Generated
ServiceAccount names are independent of this value. Do not remove the chart
validation without changing and testing that controller/chart contract for
both managed and unselected legacy devices.

The manager checks API discovery, policy generation, binding shape, contract
version, and built-in-resource expression warnings before enabling managed
workers. Each worker then performs positive and negative server-side dry runs
with its own credentials: a harmless Node-status write must succeed, while a
label write smuggled through `/status` must be denied. A version check or an
empty warning list alone is not proof that admission works.

## Configure and enable

Start from
[`examples/topology/managed-topology-values.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/topology/managed-topology-values.yaml).
The conservative defaults enroll only devices explicitly labeled
`topology.cisco.vk/managed=true`, require region and zone, and admit one
transfer and one unavailable member.

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

The first shared-to-isolated worker migration must be a live `helm upgrade`.
The chart uses Kubernetes `lookup` to preserve only the exact existing shared
RoleBinding and ClusterRoleBinding while the manager replaces their users.
Offline `helm template` output and GitOps pruning cannot discover those live,
cross-namespace identities. They are not a supported source of truth for this
one-time handoff unless pruning explicitly excludes existing CVK RBAC until
the manager reports retirement complete. Normal offline review of rendered
manifests remains useful; applying that output as a pruning migration is the
unsafe operation.

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
   resolved name must be at most 63 bytes so it can remain exact in each
   generated worker identity. Set an explicit short `spec.nodeName` before
   enrollment when a legacy object name is longer.
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
shared bindings as a marked migration bridge while the controller replaces
each old Deployment using `Recreate`. The controller removes the bridge only
after no Deployment, ReplicaSet, or Pod still uses the shared ServiceAccount
and every replacement identity has passed its binding audit. New managed
admission remains globally deferred while either shared binding still exists;
the interim workers use isolated per-device legacy identities, so a shared
token cannot enter the managed protocol. Do not manually delete the shared
bindings during this handoff. Fresh topology-enabled installs never create
them. ReplicaSet read/delete permission exists only in the retained
managed-topology manager role and is used solely to remove an exact,
owner-verified, zero-replica legacy ReplicaSet during that retirement; it is
absent from the default controller role.

For every unselected device, the manager also persists the exact UID-bound
isolated-worker marker before allowing its generated legacy worker. A stale or
forged marker is a fail-closed error; it is never sufficient by itself to
recover authority without the exact CiscoDevice-owned ServiceAccount and
canonical RoleBinding/ClusterRoleBinding pair.

The manager pre-creates or explicitly adopts the Node, records the Node UID in
`status.nodeIdentity`, creates a UID-derived worker ServiceAccount, and holds
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
| `WorkerProtocolPending` | UID-derived worker has not completed admission/writer handoff |
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

Managed worker RBAC removes Node metadata/spec writes, Pod main-resource
mutation, Pod logs, Pod exec, and Lease create/delete. It retains cluster-wide
Pod, ConfigMap, Secret, and Service reads because a virtual Node can receive
Pods from any namespace. The manager pre-creates purpose-bound heartbeat,
config-family, and mutation Leases; admission permits only the bound worker's
protocol-valid update. An unselected `cisco-vk-legacy-*` identity retains
legacy Node and Lease verbs for runtime compatibility, but Node and Pod
admission prevents it from crossing into a peer or managed Node. It does not
match the managed Lease protocol. Upgrade-leaf writes come only from the
per-device namespaced RoleBinding and are absent while the software-upgrade
gate is inactive.

`pods/status` remains a cluster-scoped RBAC grant, but native admission now
requires the immutable `Pod.spec.nodeName` to equal the virtual Node embedded
in either generated ServiceAccount family and rejects metadata/spec smuggling.
The Node policy similarly lets a legacy identity mutate only its exact
unmarked Node; managed Nodes remain manager/bound-managed-worker only. This is
still not equivalent to Node Authorizer/NodeRestriction: the worker can write
any schema-valid status for Pods on that Node, and cluster-wide Secret reads
cannot be limited to only Secrets referenced by those Pods. Provider-side Node
filtering is a correctness mechanism, not an authorization boundary. A future
hardening phase should qualify `system:node:<virtual-node>` authentication with
Node Authorizer/NodeRestriction, or move Pod-status mutation behind the manager.
Until then, treat workers as cluster-trusted workloads and audit every binding.

## Rollback and retirement

There are two distinct rollback procedures.

For a failed or undesired IOS-XE version, settle any ambiguous operation first,
then create and approve a new `IOSXESoftwareRollout` whose target version and
digest describe the qualified downgrade. Do not mutate or replay an old
campaign.

For managed-topology feature retirement:

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
   `topology.cisco.vk/legacy-handoff=<node UID>` audit marker, and the isolated
   legacy worker is Ready with a heartbeat later than `nodeReleasedAt`;
6. run a live Helm upgrade with the same release, policy namespace/name, and
   ledger name, keeping `controller.leaderElect=true` and
   `aggregator.enabled=false`, then set `topology.enabled=false`; and
7. verify every device still uses its UID-derived isolated legacy identity and
   that the old shared ServiceAccount remains unbound.

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
authorized immutable identity, `LegacyWriterPending` records Node release, and
`Complete` is published only after the generated legacy writer proves Ready.
Once `Preparing` exists, admission freezes the consumed
`topology.cisco.vk/request-legacy-handoff` value against change or removal
until the handoff reaches `Complete`; the controller does not re-read mutable
authorization midway through the protocol. Device deletion is likewise denied
until `Complete`, preventing lifecycle cleanup from interrupting the
single-writer transfer.
The manager atomically clears its Node identity/projection/health status only
at completion. The request annotation can be removed by a topology-author
after topology is disabled; the Complete status, isolated-worker marker, and
Node audit marker remain identity state.

The live downgrade check rejects missing or UID-mismatched policy/ledger
objects, incomplete manager RBAC, any remaining `status.nodeIdentity`, and any
handoff phase other than `Complete`. The policy, ledger, eight policy/binding
pairs, fixed managed-worker role, and supplemental manager role/binding carry
`helm.sh/resource-policy: keep`. Setting `topology.enabled=false` or
uninstalling Helm does not remove them. Completed isolated workers still rely
on Node/Pod admission, so these retained objects are an active safety boundary,
not stale installation debris. Remove them only after deleting every dependent
isolated worker, ServiceAccount/binding, Node marker, and durable device state.
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

On a disposable Kubernetes 1.35+ cluster with the default scheduler, run:

```bash
charts/cisco-virtual-kubelet/tests/topology-kind-test.sh
```

The real-cluster test proves policy compilation plus the live API-server-stored
Spec digest, positive/negative worker Node and Pod status admission, unmarked
peer denial, purpose-specific Lease update and create/delete fencing, complete
CiscoDevice status/finalizer protection, campaign control separation, ledger
protection, native affinity/spread binding, initialization-taint exclusion, and
the known direct-`nodeName` bypass. It does not contact a network device.

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
