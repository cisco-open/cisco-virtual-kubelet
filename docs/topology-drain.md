# Evaluate PDB-aware workload drain

**For application owners and device operators working together.** This optional
controlled-evaluation procedure evacuates a narrow class of application workloads
before an IOS-XE OS campaign. It is not general-purpose `kubectl drain` or an
application/network continuity guarantee.

## Before enabling drain

Complete [managed enrollment](topology-enable.md) and the
[IOS-XE campaign prerequisites](topology-campaigns.md). Prove qualified alternative
scheduler capacity and application portability. Review the exact eligibility
list below with the workload owner, including its controller, PDB, storage and
placement restrictions. Use the matching chart/CRDs/manager/workers and complete
[disposable-cluster qualification](topology-enable.md#qualification-checks).
Keep the default `BlockIfRunning` policy when those prerequisites are unmet.

## 1. Opt in at administrator and campaign layers { #opt-in-pdb-aware-drain-development-preview }

The Phase 4 drain slice is intentionally narrower than a general-purpose
`kubectl drain`. It exists only for manager-created IOS-XE software-rollout
leaves, is disabled at both administrator and campaign layers, and fails
closed when workload portability cannot be proven. Qualification includes unit and disposable API-server coverage plus the
recorded bounded physical voluntary-drain test. It does not establish the
complete application/service-through-reload or rollback matrices. Keep
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
[`examples/topology/iosxe-software-rollout-drain.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/v2026.11.1/examples/topology/iosxe-software-rollout-drain.yaml)
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

### Workload eligibility

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
- `nodeSelector`, required node affinity and `DoNotSchedule` topology spread
  are preserved in an immutable placement digest. Before protection or
  Eviction, CVK must observe another Ready, schedulable Node that satisfies
  those constraints, relevant taints, allocatable Pod/resources and current
  hard-spread skew. This is a conservative feasibility check, not a capacity
  reservation; kube-scheduler remains authoritative and the replacement must
  still become natively Ready. Required Pod affinity/anti-affinity remains
  unsupported because its peer-set race has not been qualified. Preferred
  affinity and `ScheduleAnyway` spread remain scheduler hints;
- CVK performs an uncached unstructured read of the exact Pod UID and
  resourceVersion. If the served Pod contains native
  `spec.schedulingGroup`, drain fails closed. This prevents an older typed
  client from silently dropping Kubernetes 1.37 TAS membership; group-aware
  eviction remains disabled until the E08 physical lifecycle contract passes;
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
[`devices-and-workload.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/v2026.11.1/examples/topology/devices-and-workload.yaml)
shows the label, soft default-scheduler affinity/spread, and a matching PDB. CVK
still submits every termination through `policy/v1` Eviction. The API server's
live PDB decision is authoritative even when the frozen status showed
`disruptionsAllowed: 1`.

## 2. Observe serial eviction and recovery

The manager freezes exact identities and guards the Node. It serializes voluntary
Evictions, requires complete device-clean evidence for each selected Pod, and
rechecks workloads before permitting OS mutation. Recovery restores only the
session-owned scheduling state and holds the reservation through replacement
health and soak. Inspect the [retained leaf and worker evidence](topology-troubleshooting.md)
throughout; do not remove markers or bypass PDBs to force progress.

The detailed [drain state and lease contract](topology-reference.md#drain-state-and-recovery-contract)
explains each transition and the fail-closed worker-replacement boundary.

Replacement health uses the provider's Pod status and native controller/PDB
observations. On IOS-XE, a running application can become Pod Ready before
DHCP or an application endpoint is usable; this is not an HTTP readiness
guarantee. Validate the service independently during qualification. A PDB
shared by separate single-replica Deployments protects their aggregate
availability, not the availability of each application.
Deployment `minReadySeconds` can require sustained provider readiness before
the controller becomes available and CVK permits the next eviction. It is a
useful settling delay, not a substitute for application-level health checks.

## 3. Handle blocked or expired recovery

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

An old Lease request cannot silently authorize a new recovery window. Partial,
foreign or unexpired state remains blocked; neither expiry nor safe retirement
is device-clean evidence. Consult the
[revision and worker-replacement contract](topology-reference.md#drain-state-and-recovery-contract)
when a renewal or credential/worker rotation is involved.

## 4. Disable drain only after cleanup

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
release/platform combination independently. The original drain qualification
used the combined IOS-XE install/activate leaf. This branch also implements
separate PrepareOnly receipts and approved activation; that does not extend
the earlier drain test's physical coverage. See the
[separate-activation evidence and boundaries](evidence/topology-2026-10-02/e06-separate-activation.md).

## Verify the outcome

A completed drain requires the exact selected workload identities to have durable
device-clean evidence, only owned guards restored, current controller/PDB
identities and replica health verified, and the campaign's post-mutation soak and
reservation settlement completed. Verify the actual service separately: Pod Ready
may precede DHCP or application reachability.

If cleanup is unresolved, keep its retained RBAC and authority objects. Use the
[recovery guide](topology-recovery.md) for device ownership or preparation
retirement; return to the [campaign guide](topology-campaigns.md) for normal OS
change approval and control.
