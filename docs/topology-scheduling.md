# Schedule application workloads

**For application operators.** Place device-hosted application Pods with the
normal Kubernetes scheduler. This does not schedule an IOS-XE OS upgrade.

## Before you schedule

Complete [managed enrollment](topology-enable.md). Verify the device Node is
Ready, its protected labels are complete, and the app-hosting plane is read-write.
Choose a package and network configuration supported by the target platform.
For a first application, use [Getting started](getting-started.md); in managed
mode, replace direct `nodeName` placement with the scheduler rules below.

## Workload scheduling

Use the default scheduler. Do not set `schedulerName`, and avoid direct
`spec.nodeName` assignments. The
[`devices-and-workload.yaml`](https://github.com/cisco-open/cisco-virtual-kubelet/blob/v2026.11.1/examples/topology/devices-and-workload.yaml)
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
campaign opt into the [drain procedure](topology-drain.md).

Hard node affinity, selectors and spread remain valid with `BlockIfRunning`.
The `v2026.11.1` preview `Drain` contract can also preserve these constraints in
an immutable placement digest, but must observe a currently feasible replacement
Node before protection or Eviction. Required Pod affinity and anti-affinity remain
unsupported. This feasibility check does not reserve capacity: the scheduler is
still authoritative, and the replacement must become Ready within the bounded
recovery window. Before opting in, qualify alternative capacity and actual
application portability. Soft preferences and `ScheduleAnyway` remain hints,
not placement guarantees. See the complete [drain eligibility](topology-drain.md#workload-eligibility).

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

## Verify placement

Inspect the Pod's assigned Node and events, then compare that Node's protected
labels and taints with the intended affinity/spread. A Pending Pod is the correct
result when a hard constraint cannot be met. Changing a label does not relocate an
already-bound Pod, and Pod Ready alone does not establish service reachability.

```bash
kubectl get pods -n WORKLOAD_NAMESPACE -o wide
kubectl describe pod POD_NAME -n WORKLOAD_NAMESPACE
kubectl get node NODE_NAME --show-labels
```

Replace the namespace and object placeholders with your application. Verify the
service independently on its supported network path before declaring placement
successful.

## Next step

Use [observability](observability.md) to follow the application. If a later OS
campaign requires evacuation, separately review the [drain eligibility and gates](topology-drain.md)
before changing its workload policy. The optional Kubernetes 1.37
[Workload/PodGroup/TAS lane](topology-reference.md#experimental-in-tree-workloadpodgrouptas)
is experimental and is not required for this placement workflow.
