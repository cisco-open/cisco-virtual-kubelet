# Observe and troubleshoot managed topology

**For operators investigating current state.** These commands inspect enrollment,
campaigns and worker evidence; they do not grant mutation or recovery authority.
Use the [setup checks](topology-enable.md) for initial enrollment and the
[recovery procedure](topology-recovery.md) before changing retained authority.

## Select the right installation and device

Examples use release `cvk` in `cvk-system` and devices in `network-devices`.
Substitute your existing release/namespace and exact device, Node and leaf names.
For split workers, the app plane handles application teardown and the network
plane handles gNOI. Select the exact current worker Pod; a manager log alone does
not establish a device-side outcome.

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

kubectl logs -n cvk-system \
  deploy/cvk-cisco-virtual-kubelet-controller -c controller -f

kubectl get pods -n network-devices \
  -l 'app.kubernetes.io/name in (cisco-vk,cisco-vk-network),app.kubernetes.io/instance=DEVICE_NAME' \
  -L app.kubernetes.io/name
kubectl logs -n network-devices POD_NAME -c cisco-vk -f
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
- `cisco_vk_topology_rollout_reconcile_duration_seconds` for bounded
  p50/p95/p99 latency queries;
- `cisco_vk_topology_rollout_target_transitions_total`;
- `cisco_vk_topology_rollout_admission_waits_total`;
- `cisco_vk_topology_rollout_ledger_conflict_retries_total`;
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

## Read the result before taking action

A blocked condition is a safety boundary. Resolve the stated identity, workload,
policy or health cause instead of deleting the leaf, ledger or guard. Keep full
identities in redacted logs/audit records; never copy credential material into
metrics or an incident report. Success means the current exact objects agree and
the normal reconciler advances their state—not merely that a log stopped changing.

## Read-only accepted topology graph

`kubectl ciscovk topology graph` renders a bounded diagnostic using only the
manager-owned `acceptedNetwork` snapshots. It carries local and remote ports,
protocol/routing context, freshness and sample provenance. It never opens a
device session, edits labels or becomes rollout authority.

For protocol-local names such as CDP device IDs, enable `topology.graph` and
declare exact `peerMappings` only after verifying the mapping from
authenticated device inventory. Helm stores the resulting `graph.json` beside
`policy.json` in the existing admission-protected topology-policy ConfigMap,
but the keys have separate semantics: graph data is excluded from campaign
policy and approval hashes. `declaredLinks` can then report missing or
unexpected accepted links. Use `external: true` for a verified endpoint that
is intentionally outside CVK management. Discovery never writes mappings or
declarations back to Kubernetes.

Operators pass the exact ConfigMap coordinate with `--policy-configmap`; JSON
output binds the ConfigMap UID, resourceVersion and canonical content hash
into graph provenance. This proves which accepted observations and declared
model produced a finding. It does not prove forwarding reachability,
redundancy, service health, bandwidth headroom or permission to upgrade.

## Next step

Return to [application placement](topology-scheduling.md) or
[campaign execution](topology-campaigns.md) when their gates are satisfied. For
unresolved mutations, unused preparation or ownership changes, use
[recovery and retirement](topology-recovery.md). See the
[design reference](topology-reference.md) for precise state and freshness rules.
