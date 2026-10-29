# Authorization and worker identities

For cluster administrators and security reviewers deciding who may read
credentials, schedule applications, change network intent or run device
operations. These permissions protect Kubernetes API access; device accounts
and external automation still need their own authorization controls.

Start with the operating mode and the trust boundary it requires:

| Runtime | Identity and decision |
|---|---|
| Manager | Cluster-wide orchestration and Secret reads; the optional in-process config aggregator also runs device configuration here |
| Legacy combined worker | Broad cluster grants for workloads, plus a namespaced device role |
| Managed app hosting | Shared functional identity per device namespace; read-write app hosting still exposes cluster-wide Secret reads |
| Managed network management | Separate namespace-scoped mutation identity; defaults to `readOnly` |
| Controller adapter | Unique worker identity, but namespace-wide intent/status authority; use dedicated namespaces for untrusted endpoints |

The chart defaults to `rbac.profile: default`, `topology.enabled: false`,
app-hosting access `readWrite`, network-management access `readOnly`, and both
gNOI mutation gates disabled. Choosing `strict` narrows the legacy role; it does
not enable managed topology or provide per-device isolation. Review the
[hardening requirements](#production-rbac-hardening) before delegating access.

## RBAC model

Managed topology remains subject to the [qualification and migration limits](topology-enable.md#prerequisites). The `sdwan` and `nexus-dashboard` adapter roles below are introduced in `v2026.11.0`; upgrade October `v2026.10.0` installations with the matching CRDs, chart and image before using them. Both adapters remain Alpha.

The Helm chart installs the manager and VK service accounts plus the reusable
ClusterRoles needed by isolated runtimes. The manager creates a unique service
account for each network-controller worker when that runtime is requested.

### Controller service account (`cisco-virtual-kubelet-controller`)

Used by the `manager` Pod. The default name is set by `serviceAccount.controllerName`; it is created in the Helm release namespace. The table is a selection of its broad grants, not the complete policy. Review the chart's generated `role.yaml` and any managed-topology additions for the installed version.

| Resource | Verbs | Scope |
|---|---|---|
| `cisco.vk/ciscodevices` | get, list, watch, update, patch | cluster |
| `cisco.vk/ciscodevices/status` | get, update, patch | cluster |
| `configmaps` | get, list, watch, create, update, patch, delete | cluster |
| `secrets` | get, list, watch | cluster; used for `CiscoDevice` credential-rotation watches and by the optional in-process config aggregator; installed unconditionally |
| `deployments` (`apps`) | get, list, watch, create, update, patch, delete | cluster |
| `nodes` | get, list, watch, update, patch, delete | cluster |

The network-controller orchestration path does not request Secret values: it
writes only authorized references into Pod volume sources, and the kubelet
resolves them. However, the shared manager ServiceAccount has cluster-wide
read-only Secret permission, the `CiscoDevice` reconciler watches typed Secret
objects for credential rotation, and the optional in-process aggregator also
uses the grant. Consequently Secret data can enter the manager process/cache
even when network-controller workers consume only projected files. The grant
is generated and installed even when aggregator mode and the optional
network-controller workers are off.

### VK pod service account (`cisco-virtual-kubelet`)

The topology-disabled worker uses `serviceAccount.vkName` (default `cisco-virtual-kubelet`) and the legacy roles. These selected cluster-scoped grants come from `templates/vk-rbac.yaml`; the separate device role covers namespaced configuration and operation CRDs. The API permissions do not imply support for hosted-application logs or exec.

| Resource | Verbs | Rationale |
|---|---|---|
| `pods` | get, list, watch, create, update, patch, delete | Legacy Pod lifecycle API |
| `pods/status` | get, update, patch | Publish Pod status |
| `pods/log` | get | Legacy Kubernetes API grant; hosted-app logs remain unsupported |
| `pods/exec` | create | Legacy Kubernetes API grant; hosted-app exec remains unsupported |
| `nodes` | get, list, watch, create, update, patch, delete | Register and maintain the virtual Node |
| `nodes/status` | get, update, patch | Publish Node status |
| `secrets` | get, list, watch | Cluster-wide workload-input reads |
| `configmaps` | get, list, watch, create, update, patch, delete | Workload reads and legacy diagnostic-output writes |
| `services` | get, list, watch | Service discovery surface for pods |
| `persistentvolumes`, `persistentvolumeclaims` | get, list, watch | Not used directly today; reserved for future volume support |
| `events` | create, patch | Emit pod lifecycle events |
| `leases` (cluster-wide grant; used in `kube-node-lease`, worker and configured lease namespaces) | get, list, watch, create, update, patch, delete | Node heartbeat, config arbitration, and the shared disruptive-mutation fence |

### Managed functional identities

The table above describes the topology-disabled legacy identity. Managed
topology replaces the per-device account model with exactly two reusable
functional identities in each managed CiscoDevice namespace, independent of
device count. They are namespaced ServiceAccounts, not two cluster-global
accounts. Selecting `disabled` removes the corresponding account, bindings,
and worker plane; no per-device account replaces it:

| Account | `readOnly` | `readWrite` |
|---|---|---|
| app hosting | Observe Nodes and Pods; no schedulable Node or workload execution | Adds Node/Pod status, exact terminating-Pod cleanup, workload inputs, Events, and manager-precreated Lease updates |
| network management | Read configuration/device/operation objects and publish narrowly scoped diagnostic, telemetry, and read-only operation results | Adds configuration apply/history, mutation coordination, and explicitly enabled software/action APIs |

Each account is bound to exactly one profile; read-only and read-write roles
are never additive. `disabled` omits that plane. The network profile is
RoleBound only in its tenant namespace. A separate Lease-only support role is
bound in a configured dedicated Lease namespace. App read-write also receives an implementation-only namespaced
support role for its exact CiscoDevice and maintenance-risk scan. For PDB drain,
this namespaced role also permits upgrade status patches; native admission
restricts the exact Pod-bound app identity to `workerDrain` inventory only.
It cannot change gNOI phase, network acknowledgement, mutation claims, or
manager grants. Inventory carries the app revision and Pod UID separately
from the network worker's revision, so a restart invalidates previous clean
evidence. These CRD permissions are not placed in its cluster-bound profile. A separate non-selectable network
cluster-read role supplies only `IOSXEConfigDefaults`, Node/Pod observations
needed to fence maintenance, and no tenant CRD read. Its only create verb is
`SelfSubjectReview`, used to verify the caller's Pod-bound token; it cannot
mutate a persisted object. The app profile
must be ClusterRoleBound because Pods placed on a virtual Node can originate
in any namespace.

Empty account names derive release-qualified DNS labels and are the safe
default. Admission cluster-reserves each resolved name to prevent an identity
from being reused by another CVK control plane; explicit overrides must be
unique across all releases, despite the ServiceAccounts being namespaced.

The access mode is also enforced inside the worker process. A network
`readOnly` worker starts telemetry, diagnostics, and read-only operations but
does not construct configuration or gNOI mutation reconcilers. Setting a gNOI
mutation gate while network management is not `readWrite` fails Helm
validation. App-hosting `readOnly` is an intentional observer mode: it cannot
advertise a Ready schedulable Node or execute Pods.

Two native-authorization gaps remain explicit. The app read-write account can
read Secrets cluster-wide for cross-namespace Pod volume resolution; RBAC
cannot limit that grant to Secrets referenced by Pods assigned to this
namespace's virtual Nodes. Because the account is shared within a tenant
namespace, compromise of one app worker also exposes the Kubernetes API
permissions for peer device workers using that account. Admission still
confines object writes to manager-bound Nodes/Pods, but provider cache
filtering is not an authorization boundary. Use namespaces as trust boundaries
and keep device credentials unique. The real kubelets that request projected
worker tokens must authenticate as `system:node:<nodeName>` under the Node and
RBAC authorizers with `NodeRestriction` enabled. That prerequisite protects the
kubelet-side token path; it does not make the ServiceAccount token node-scoped
or remove the app account's cluster-wide reads.

Write-class gNOI actions and software upgrades share one
`device-disruptive-mutation` Lease per namespaced device. Definitive outcomes
release it. Every post-invocation action failure and any upgrade outcome that
cannot be safely correlated remains quarantined. The legacy disruptive-mutation
quarantine uses a fixed 26-hour TTL; managed campaign reservations can retain
authority for longer bounded windows. Follow the [managed recovery procedure](topology-recovery.md#rollback-and-retirement) rather than clearing a Lease manually; accepted `Reboot` and `FactoryReset` requests are also retained because
acknowledgement does not prove convergence.
`CancelReboot` bypasses the fence so an authorized operator can cancel a
pending reboot, but it never releases another holder's Lease. This is a CVK
coordination boundary, not a device lock: external automation and manual CLI
changes still require operational controls.

### Network-controller CRD validation prerequisite

The controller APIs use CEL transition rules (`oldSelf`) to enforce immutable
endpoint and intent identity. Kubernetes 1.28 is supported only with the
`CustomResourceValidationExpressions` feature gate enabled; that feature is
beta and enabled by default in 1.28. It is GA in Kubernetes 1.29 and later.
CVK's runtime validation is defense in depth for the current object shape, but
it cannot compare the previous object and does not replace CEL immutability.
Do not operate controller workers on an API server where CEL transition
validation is disabled.

### Network-controller worker service accounts

Each `NetworkController` gets a unique ServiceAccount and a **namespaced**
RoleBinding to a reusable ClusterRole selected by its adapter's registered
descriptor: the base `cisco-virtual-kubelet-controller-worker` role, or one of
the adapter roles described below. There is deliberately no
ClusterRoleBinding. The worker can read controller resources, controller
intent, and source ConfigMaps across its namespace; update only the two CRDs'
`/status` subresources; and emit bounded Events. The base role deliberately has
no coordination Lease permission. It cannot update or patch either main
resource, including its spec, metadata, and finalizers. It also cannot read
Secrets, Nodes, Pods, workloads, or another namespace.

Adapter roles extend the base role by the smallest set of extra grants, and the
manager may bind only roles on an explicit allow-list; registry validation
rejects every other role name. Neither adapter role can read Secrets.

| Role | Extra grants beyond the base role | Used by |
|---|---|---|
| `cisco-virtual-kubelet-controller-worker` | None (status-only, read of controller resources) | Adapters that need nothing more |
| `cisco-virtual-kubelet-sdwan-worker` | `coordination.k8s.io` Leases: get, create, update (section ownership Leases) | `sdwan` |
| `cisco-virtual-kubelet-nd-worker` | `ciscodevices`: get, list, watch, create, update, patch, delete in its namespace | `nexus-dashboard` |

The `nd-worker` delete verb exists only to serve the opt-in `Prune` removal
policy. RBAC cannot depend on that policy, so every `nexus-dashboard` worker
holds it, while the adapter itself deletes only devices carrying its
controller-UID ownership label, after a grace period, never on an empty
inventory and behind a mass-delete guard. There is no `deletecollection`. Treat
`CiscoDevice` write access in a controller namespace as part of that
namespace's trust boundary.

A unique worker ServiceAccount provides distinct identity and auditing, not
endpoint-scoped authorization. Its RoleBinding authorizes update/patch of the
`/status` subresource for **every** `NetworkController` and
`NetworkControllerConfig` in the namespace, not just the endpoint named in its
bootstrap. The endpoint object cache is name-filtered and the common contract
gate constrains a conforming adapter, but neither is an authorization boundary;
configuration/source caches and the RoleBinding remain namespace-scoped. The
namespace is therefore the Kubernetes API trust, RBAC, status-write, and cache
boundary. Per-endpoint workers separately isolate process lifetime, mounted
credentials, native sessions, failure, and rate limits. Use one
`NetworkController` per dedicated namespace whenever endpoints, adapter code,
or configuration authors are not mutually trusted.

Within one namespace, the manager fences multiple controller objects whose
stored `spec.endpoint` values are exactly equal. The deterministic owner is the
earliest creation timestamp, then lexicographically smallest name, then UID;
non-owners remain quiesced and periodically requeue so they can activate after
owner deletion. The elected owner must actively remove every managed loser
worker before it can start its own, so delayed peer events cannot permit an
overlap. This first guard deliberately does not normalize URLs or detect
equivalent endpoints across namespaces or clusters, so those stronger identity
policies remain an operational and future-design responsibility.

A future per-controller cache optimization may use a manager-owned
key-existence label such as `nc.cisco.vk/<uid-hash>: "true"` on configs and
source objects; a shared ConfigMap may carry several controller keys.
Manager-owned stamping/cleanup and a bounded, collision-tested UID hash are
prerequisites. This can reduce watch fan-out and memory, but it is non-security
work: labels and cache filters do not reduce the namespace-wide API authority
described above.

The manager associates worker resources with their endpoint by explicit
controller name/UID annotations, not Kubernetes owner references. Its
protection finalizer retains those resources while dependent controller configs
exist and removes them explicitly only after the dependency check succeeds.
This prevents foreground garbage collection from bypassing the default
`Retain` lifecycle. If a generated name is already occupied by an object with
missing or different identity annotations, the manager refuses adoption,
leaves the foreign object unchanged, and emits a sanitized Warning Event with
reason `WorkerObjectCollision`. Operators can inspect it with:

```bash
kubectl events -n <namespace> \
  --for networkcontroller/<name> \
  --types=Warning
```

The endpoint credential Secret is mounted read-only by the kubelet instead of
being fetched through the Kubernetes API. Any future adapter-specific
permission set must be a separate, statically reviewed ClusterRole selected by
the adapter descriptor and accepted by the registry's closed allow-list. Its
chart definition and the manager's exact `bind` grant must land in the same
reviewed change. See the
[Network Controller Extension Guide](controller-extension-guide.md#security-boundary)
for the complete worker and credential contract.

Projected credential, CA, and intent-Secret files can rotate without a Pod
restart, but a projection update does not inherently enqueue an adapter
reconcile. A generic worker-owned watcher therefore checks Kubernetes
AtomicWriter `..data` symlinks every five seconds without reading or hashing
projected values. It passes the adapter one coalescing material-change channel
and a fifteen-minute maximum session lifetime. The adapter must consume and
internally fan out that signal, discard cached authentication/TLS state,
re-read mounted files, and requeue affected work before its next external
request. It must independently rebuild clients and sessions by the maximum
lifetime even if no signal arrives.

Remote mutation is a gated profile. An adapter may honor `apply` only with the
shared controller-neutral `SectionLeaser`, ownership/renewal/takeover/API-loss
conformance tests, and a **separate**, statically reviewed role containing only
the required coordination Lease authority. The `sdwan` adapter meets this gate
for its fixture-qualified `apply` mode through
`cisco-virtual-kubelet-sdwan-worker`. Remote pruning and remote deletion remain
unimplemented (`Retain`). The manager binds adapter roles only through an
explicit audited allow-list, and registry validation rejects every other role
name. Lease verbs must not be added back to the base worker role.

### Controller intent Secret authorization

Controller-intent Secrets use a two-resource authorization boundary.
Administrators define
`NetworkController.spec.intentSecretSources`, an alias-to-same-namespace
Secret name/key allow-list. Configuration authors may then use
`NetworkControllerConfig.spec.secretRefs[].source` to select an alias and
destination section/path, but cannot submit an arbitrary Secret name or key.

The manager projects only administrator-approved aliases into the worker Pod.
This projection path does not inspect Secret `.data`, and the worker has no
Secret API permission; the kubelet resolves the read-only volume. The shared
manager cache caveat described above still applies. This prevents a user who
can create controller configs from using the projection logic as a confused
Secret-read deputy.
Cluster RBAC must therefore reserve `NetworkController` writes for the
administrator role and delegate `NetworkControllerConfig` independently.

For an unauthorized, invalid, or excess projection, the manager skips that
projection and emits a sanitized `IntentSecretProjectionSkipped` warning/event;
it does not set config status. A missing or unresolved projected file remains
optional at Pod-mount time. The adapter skips only the affected config and
reports `IntentSecretsReady=False` through its status. Neither failure stops
the endpoint worker or prevents other valid controller configs from
reconciling.

### Production RBAC hardening

Topology-disabled installs preserve the broad legacy Virtual Kubelet identity
for compatibility. Managed topology is the opt-in split: app hosting and
network management run as separate processes and identities, with namespaced
network mutation authority and explicit access modes. The manager can bind
only the four fixed profile roles plus implementation-only app-read,
network-global-read, and Lease-only support roles;
workers have no RBAC or service-account-token creation permission. Profile
rules are deterministic across releases: network read-write carries the
bounded mutation API verbs, while runtime gates and native admission decide
whether and where those verbs can be exercised.

Admission reserves both functional ServiceAccount objects to the topology
manager. A `serviceaccounts/token` request for either name must come from a
kubelet and name one exact bound Pod UID, while legacy
`kubernetes.io/service-account-token` Secrets for the accounts are forbidden.
Workers then use `SelfSubjectReview` plus API-server Pod token claims; a shared
username by itself is never accepted as a device identity.

Managed CiscoDevices must live in dedicated, administrator-controlled
namespaces. Do not bind tenant `edit`/`admin` or any role that permits
ServiceAccount impersonation; use of either functional ServiceAccount; worker
Pod logs, `exec`, `attach`, `portforward`, or `proxy`; Deployment/ReplicaSet
`scale`; or deletion of the worker, RBAC, Lease, Secret, or admission-related
objects. Those capabilities can reuse the shared username, extract its bound
token or mounted device credentials, bypass a controlled drain, or remove the
enforcement surface. A Kubernetes ServiceAccount impersonated in one namespace
can exercise every cluster-scoped grant held by that account. The controller's
namespaced binding audit fails closed, but cluster-admin and principals able to
create or mutate ClusterRoleBindings remain a separate root trust boundary;
admission does not attempt to constrain them.
See the Kubernetes documentation for the upstream
[impersonation](https://kubernetes.io/docs/reference/access-authn-authz/user-impersonation/),
[Node authorization](https://kubernetes.io/docs/reference/access-authn-authz/node/),
and
[NodeRestriction](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#noderestriction)
semantics behind these requirements.

If `CONFIG_LEASE_NAMESPACE` differs from the device namespace, the network
account receives only the matching read-only or read-write Lease support role
there. The tenant network profile is never rebound into that namespace, so
config, operation, and Secret permissions do not leak into a shared lease
namespace. Resolved worker account names are retained in `policy.json` and
protected policy annotations. They cannot be renamed in place; retirement and
clean re-enrollment are required. The retained policy coordinates and
admission-policy prefix are locked to the owning Helm release as well, so a
`fullnameOverride`, `nameOverride`, policy-name, or policy-namespace change
also requires retirement and clean re-enrollment.

The resolved `CONFIG_LEASE_NAMESPACE` is locked in the same retained policy
(empty means the CiscoDevice namespace). It cannot be changed after topology
bootstrap: retire managed topology and all old Lease authority, then re-enroll
with the new value. This prevents a profile change from leaving a writable
binding in the former Lease namespace.

Profile changes are manager-controlled drains. A write-to-read-only or
write-to-disabled transition waits for topology locks, maintenance, rollout
reservations, and device mutations to settle; the app plane also requires that
no scheduled workloads depend on it. The manager removes the applicable
Deployment, ReplicaSet, and Pod before revoking or replacing its binding, and
an escalation waits for the old worker incarnation to disappear. Operators
must not edit generated bindings directly.

This is capability separation, not per-device isolation. Namespace placement
therefore matters: put mutually untrusted fleets in separate namespaces, audit
the two bindings after every chart upgrade, and use `kubectl auth can-i` against
both accounts. Any future cluster-wide verb must retain a documented rationale
and a rendered-RBAC regression test.

Functional app hosting has a broader inherited upstream Virtual Kubelet trust
boundary: app read-write must list/watch Pods, Secrets, ConfigMaps, and Services
cluster-wide because `nodeutil` builds cluster-wide informers for workloads
that can originate in any namespace. Pod-bound tokens and admission confine
writes, but they cannot scope those reads. Treat compromise of any app worker
as cluster-wide Secret-read exposure and run app hosting only in a dedicated
trusted workload cluster, or in a cluster whose workload namespaces explicitly
share that trust boundary. Namespace isolation does not mitigate this read
grant. Network-management workers do not receive it.

The chart exposes the first strict-profile control as:

```yaml
rbac:
  profile: strict
```

`profile: default` preserves the historical VK ClusterRole. `profile: strict`
removes reserved persistent volume reads and withholds write-class gNOI and
software-upgrade CRD permissions unless the matching runtime gates are enabled:

```yaml
gnoi:
  enableSoftwareUpgrade: true
  enableWriteClass: true
```

### Finalizer and deletion

The controller adds `cisco.vk/device-cleanup` to each `CiscoDevice`. On deletion it first checks whether managed work is safe to stop and tears down configuration prerequisites. Managed workers are quiesced before their access and Leases are retired; the controller deletes only the bound Node incarnation. Legacy cleanup removes its worker access and virtual Node through the corresponding compatibility path.

Only after cleanup succeeds does the controller remove the finalizer. Kubernetes can then garbage-collect owned ConfigMaps and Deployments; cluster-scoped or explicitly managed resources require the controller's cleanup. Do not remove the finalizer to bypass a blocked deletion: that can leave a Node or API authority behind. Force-deletion flags are not a safe replacement for this process. Use the [retirement procedure](topology-recovery.md#rollback-and-retirement) for managed devices.

## Principles

- **Credentials stay out of ordinary CRs and ConfigMaps.** They live in
  Secrets, which are protected at rest only when the cluster enables
  encryption at rest; Secret RBAC remains part of the security boundary.
- **No requested Secret values in the network-controller path.** Controller
  orchestration writes authorized volume references and workers consume
  projected files; the kubelet resolves the values. The shared manager still
  has cluster-wide Secret `get`, `list`, and `watch`, and typed Secret objects
  can enter its cache through the `CiscoDevice` credential-rotation watch and
  optional aggregator. Isolate and protect the manager accordingly.
- **Administrator-approved controller intent Secrets.** Config authors select aliases only; administrators own the underlying Secret name/key allow-list.
- **Per-device credentials.** Each `CiscoDevice` can reference a different Secret.
- **Finalizer-managed cleanup.** Cluster-scoped resources owned by namespaced CRs are cleaned up explicitly.

## Verify the authorization boundary

Render the chart with the intended values and review the resulting bindings
before installation. After deployment, inspect each worker's ServiceAccount
and bindings and check the allowed actions with `kubectl auth can-i` using an
authorized impersonation identity. Expected results differ by plane: app
hosting may need cluster-wide reads, while network mutation stays in its
approved device and Lease namespaces. Confirm disabled planes have no worker
or active binding and that enabled write operations have the required runtime
gate. Use the [managed-topology qualification checks](topology-enable.md#qualification-checks)
for the native-admission and Pod-bound-token contract; an RBAC check alone is
not that verification.

For practical setup, continue with [credentials](security-credentials.md) and
[TLS](security-tls.md). Adapter implementers should use the
[controller extension contract](controller-extension-guide.md#security-boundary).
Return to the [security overview](security.md).
