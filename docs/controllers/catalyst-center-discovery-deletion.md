# Discovery config deletion

Resolved, pure-discovery inventory configs can opt into owned cleanup with
`mode: apply` and `deletionPolicy: Delete`. Both inline and ConfigMap sources are
supported. `Retain` remains the default behavior. The example is
`examples/configs/catalyst-center/discovery-delete-apply.yaml`.

The manager enrolls `config.cisco.vk/catalyst-center-discovery-cleanup` before the
worker can apply Delete-policy intent. The existing feature cleanup finalizer is
unchanged. The common registry supports additional independent lifecycle families;
manager operations remain metadata-only, while workers retain status-only access.
A config cannot silently transfer an existing finalizer to a different family.
Multiple registered cleanup finalizers on one config retain their fence.

New pure-discovery ConfigMap configs first persist a `DiscoveryCleanupScope`
condition before mutation. Keep the source available until that step succeeds.
If the ConfigMap later disappears or is edited/replaced, deletion can use the
last qualified scope and original task/UUID receipts. This does not allow an
active config to execute from missing state or apply later unvalidated edits.
Legacy configs without that recorded scope still require their original source;
missing, corrupted or mismatched scope metadata retains the fence.

Before mutation, the worker loads and validates the entire source. Only discovery
is supported in this lifecycle: tags, inventory provisioning, mixed/grouped sources
and inline credentials are not silently discarded. An unsupported or unavailable
source retains the finalizer and publishes pending/unsupported status.

```bash
kubectl apply -f examples/configs/catalyst-center/discovery-delete-apply.yaml
kubectl get networkcontrollerconfig catc-owned-discovery -n cvk-live -o yaml
kubectl get networkcontroller catc -n cvk-live -o yaml
kubectl delete networkcontrollerconfig catc-owned-discovery -n cvk-live --wait=false
kubectl get networkcontrollerconfig catc-owned-discovery -n cvk-live -w
```

The inventory/templates section must not already belong to another config UID.
Use the example's existing controller/credential references only after resolving
them to your deployment's registered objects and credential descriptions.

On deletion, untouched preparations retire without submission. Original accepted
work drains through its retained task/source/UUID before cleanup begins. Each
owned job then receives an immutable deletion-bound operation, one exact-ID delete,
original-task completion checks, repeated native absence verification and an atomic
deletion receipt. An accepted task that exceeds `taskTimeout` becomes Unknown once. Supported
retained-task Unknown states remain observable without resubmission, including
late successful completion. Task-less uncertainty, failed tasks, native drift, unattested missing sources,
foreign operations and conflicting families remain fenced. Qualified ConfigMap
loss/change uses retained scope; unattested source loss remains fenced.

When known owned work is closed, the worker publishes a bound completion proof.
The manager rechecks config/controller identity and retained evidence and removes
only its discovery finalizer. Unrelated finalizers and unowned native jobs remain.
The config disappears when no finalizers remain. Empty/unowned configs can complete
without acquiring section ownership or deleting matching names.

Ownership and deletion tombstones remain in the controller journal for audit and
recovery. Newly completed pure-discovery configs also archive a retirement
attestation. After the original config UID disappears, an independently enrolled
Delete-policy successor can transfer the inventory reservation through repeated
native absence checks and status CAS. Handoff itself sends no native request;
normal planning follows on a later reconcile. The same config/job names can be
reused with a new native UUID while all earlier deletion receipts remain intact.
Missing/changed evidence and reappearing old UUIDs retain the fence. Legacy
owners without the new attestation cannot transfer automatically.
Unattested legacy source recovery, paused/changed-controller recovery, mixed/grouped or legacy
operations remain pending.

The public entry points are qualified with controlled native services, real CRD
status/metadata operations (with emulated manager cache indexes), and the Catalyst
Center lab using simulated Kubernetes persistence. These earlier checks do not establish
full pinned-model parity. See
[public lifecycle evidence](../evidence/catalyst-center-2026-10-18/discovery-public-lifecycle.md).

Retained-source recovery is qualified separately: see
[ConfigMap loss/change evidence](../evidence/catalyst-center-2026-10-18/discovery-retained-source.md).

Installed manager/worker source-loss recovery is now qualified in the isolated
lab cluster with a naturally timed-out Create, source deletion, worker restart,
exact native cleanup and manager release, without status injection. See
[installed qualification](../evidence/catalyst-center-2026-10-18/discovery-installed-source-recovery.md)
for the image boundary and retained ownership limitation.

[Ownership handoff evidence](../evidence/catalyst-center-2026-10-18/discovery-ownership-handoff.md)
and [installed two-incarnation qualification](../evidence/catalyst-center-2026-10-18/discovery-installed-handoff.md)
cover the controlled/API/native and deployed-process boundaries separately.
Existing receipt capacity limits remain; history is not automatically garbage collected.
