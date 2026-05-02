# RFC — Device-operations surface for cisco-vk (clears, reload, write-erase)

**Status:** proposal, not yet implemented
**Author:** carry-over from [`./diagnostics-rfc.md`](./diagnostics-rfc.md) §8 "What this RFC does NOT propose" — promoted here because the safety model is substantial enough that bundling it into the diagnostics RFC would muddy both
**Audience:** maintainers, SRE / NetEng operators, security reviewers
**Branch context:** `pr/johalley/nacxe`

---

## 1. Motivation

The diagnostics RFC scopes a read-only show-command surface and explicitly defers destructive commands. But operators need them:

- **`clear ip ospf process`, `clear counters`, `clear ip route *`** — bread-and-butter incident debugging. State resets are non-destructive (no config or data loss) but interrupt running protocol state, so they belong in a different authorization tier than `show`.
- **`reload`, `reload in 5`, `reload at 03:00`** — chaos engineering, planned firmware activation, and the second half of a provisioning sequence. The device disappears for a few minutes; recoverable; substantial blast radius if mis-targeted.
- **`write erase`, `delete flash:vlan.dat`, `format flash:`** — greenfield re-provisioning workflows. Wipes some or all of the device's persistent state; the device returns alive but un-configured; only valid in a deliberate provisioning context.

These categories share one property: **they're not "show" commands and shouldn't share `show`'s RBAC.** They differ from each other in:

- recoverability (clears: trivially → reload: minutes → write-erase: needs reprovisioning),
- impact radius (per-process → per-device → per-fleet if mis-targeted),
- and authorization signal (an oncall NetEng routinely runs `clear` during a fire; nobody routinely runs `write erase`).

This RFC scopes a **tiered CRD model** that makes the right thing easy and the dangerous thing hard, with Kubernetes-native RBAC as the load-bearing gate.

---

## 2. Risk classification

Five classes; the first two are already covered in the diagnostics RFC. The other three are this RFC's subject.

| Class | Examples | Recoverable? | Downtime | Audit weight | This RFC? |
|---|---|---|---|---|---|
| **read** | `show running-config`, `show ip route`, `show ip ospf neighbor`, `show version` | ✅ trivially | none | low | covered by [diagnostics-rfc](./diagnostics-rfc.md) `IOSXEDiagnostic` |
| **filesystem-read** | `dir`, `more flash:foo.txt`, `show file information` | ✅ trivially | none | low | same — `IOSXEDiagnostic` |
| **clear** | `clear counters`, `clear ip ospf process`, `clear arp-cache`, `clear ip route *` | ✅ state-only reset | seconds (protocol re-converges) | medium | **this RFC — `IOSXEMaintenance`** |
| **reload** | `reload`, `reload in 5`, `reload at hh:mm`, `reload cancel` | ✅ device returns with same config | minutes | high | **this RFC — `IOSXEDeviceOp` action=reload** |
| **erase** | `write erase`, `delete flash:vlan.dat`, `format flash:` | ⚠️ partial — config wiped, device returns un-configured | minutes + reprovisioning | very high | **this RFC — `IOSXEDeviceOp` action=erase / erase-and-reload** |

The class names are deliberately chosen to map onto Kubernetes verbs (`get/create` per class) so RBAC scopes are obvious.

---

## 3. CRD layering

Three CRDs covering the five classes:

| CRD | Classes | Side-effect window | Lives in |
|---|---|---|---|
| `IOSXEDiagnostic` (existing — see [diagnostics-rfc](./diagnostics-rfc.md)) | read, filesystem-read | none | `config.cisco.vk/v1alpha1` |
| `IOSXEMaintenance` (new) | clear | seconds | `config.cisco.vk/v1alpha1` |
| `IOSXEDeviceOp` (new) | reload, erase, erase-and-reload | minutes | `config.cisco.vk/v1alpha1` |

Three CRDs is the right granularity: each maps to a distinct RBAC tier (§4), each has different audit / approval requirements (§5), and a fourth split (e.g. separating reload from erase) would create RBAC fatigue without operational benefit since both share the "device disappears for minutes" semantics.

### 3.1 `IOSXEMaintenance` — clear-class operations

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEMaintenance
metadata:
  name: incident-2026-04-28-ospf-reset
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  commands:
    - clear ip ospf process
    - clear counters
  notBefore: "2026-04-28T13:00:00Z"     # optional — won't run before this
  notAfter:  "2026-04-28T13:30:00Z"     # optional — won't run after this; reconcile gives up
  retention:
    truncateAt: 64KiB                   # for any output the clear command produces
status:
  phase: Completed                      # Pending | Running | Completed | Failed | Expired
  startedAt:   "2026-04-28T13:00:04Z"
  completedAt: "2026-04-28T13:00:06Z"
  results:
    - command: clear ip ospf process
      output: "Reset selected OSPF processes? [no]: yes"     # interactive prompts auto-confirmed
      err: ""
    - command: clear counters
      output: "Clear \"show interface\" counters on all interfaces [confirm]"
      err: ""
  conditions:
    - type: Ready
      status: "True"
      reason: Succeeded
      message: 2 commands completed
```

#### Printer columns

| Column | JSONPath |
|---|---|
| `DEVICE` | `.spec.deviceRef.name` |
| `PHASE` | `.status.phase` |
| `WINDOW` | `notBefore..notAfter` (synthesised) |
| `AGE` | `.metadata.creationTimestamp` |

#### Semantics

- **One-shot** — no scheduling. A clear is a moment, not a cadence.
- **Idempotent** — once `phase: Completed`, the CR is read-only state. To re-run, create a new CR.
- **Interactive prompts auto-confirmed** — IOS-XE often asks "Reset OSPF process? [no]:". The reconciler answers `yes` automatically. This is intentional: the operator's act of creating the CR IS the confirmation.

### 3.2 `IOSXEDeviceOp` — reload / erase / erase-and-reload

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDeviceOp
metadata:
  name: chaos-2026-04-28-cat9k-smoke
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  action: reload                        # reload | erase | erase-and-reload
  reload:                               # only when action contains reload
    inMinutes: 5                        # OR atTime: "2026-04-28T15:00:00Z"
    saveConfigBefore: true              # write running-config to startup before reload
    cancelOnVerifyFailure: true         # cancel timer if a pre-reload health check fails
  notBefore: "2026-04-28T14:00:00Z"     # MAINTENANCE WINDOW — required for reload/erase
  notAfter:  "2026-04-28T16:00:00Z"
  confirmToken: "<sha256-derived-token>" # see §5.1
  requireApprovalFrom:                  # see §5.2 — two-person rule
    - kind: ServiceAccount
      name: netops-approver
      namespace: cisco-vk-system
  dryRun: false                         # see §5.3
  rateLimit:
    perDeviceMinSeconds: 3600           # default — at most one destructive op per device per hour
status:
  phase: Pending                        # Pending | AwaitingApproval | Scheduled | Executing | Completed | Failed | Cancelled | Expired
  approvedBy: ""                        # populated when an approver acknowledges
  approvedAt: ""
  scheduledFor: "2026-04-28T15:00:00Z"
  conditions:
    - type: Ready
      status: "False"
      reason: AwaitingApproval
      message: 'no approval from {kind=ServiceAccount, name=netops-approver, namespace=cisco-vk-system}'
```

When `action: erase-and-reload`, the reconciler runs `write erase` first, then immediately schedules a reload (because a device that's been erased but not reloaded comes up with the OLD config on the next boot — operators always want them sequenced).

#### Printer columns

| Column | JSONPath |
|---|---|
| `DEVICE` | `.spec.deviceRef.name` |
| `ACTION` | `.spec.action` |
| `PHASE` | `.status.phase` |
| `APPROVED-BY` | `.status.approvedBy` |
| `SCHEDULED-FOR` | `.status.scheduledFor` |
| `AGE` | `.metadata.creationTimestamp` |

---

## 4. RBAC model

Four ClusterRoles. Each is additive — operators get the union of their bindings.

| ClusterRole | Verbs on resources | Typical persona |
|---|---|---|
| `cisco-vk-device-viewer` | `get`, `list`, `watch` on `iosxediagnostics`, `iosxemaintenances`, `iosxedeviceops` (read-only on all three CRDs); `get` on `configmaps` with label `cisco.vk/diagnostic-output: true` | NOC, on-call viewers, dashboards |
| `cisco-vk-device-operator` | viewer + `create`/`update`/`delete` on `iosxediagnostics` and `iosxemaintenances` | Day-2 NetEng, oncall responder |
| `cisco-vk-device-administrator` | operator + `create`/`update`/`delete` on `iosxedeviceops` | Senior NetEng, SRE running chaos exercises |
| `cisco-vk-device-approver` | viewer + `update` on `iosxedeviceops/approval` (subresource) ONLY | Designated approver pool for two-person rule |

The third role's `update` permission is scoped to the `/approval` subresource (proposed below), not the full CR — meaning an approver cannot also self-author an `IOSXEDeviceOp`. This enforces the two-person rule structurally.

### 4.1 RBAC YAML

```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cisco-vk-device-viewer
rules:
- apiGroups: [config.cisco.vk]
  resources: [iosxediagnostics, iosxemaintenances, iosxedeviceops]
  verbs: [get, list, watch]
- apiGroups: [""]
  resources: [configmaps]
  verbs: [get, list, watch]
  resourceNames: []                       # operators may want to scope this further with a ResourceQuota
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cisco-vk-device-operator
aggregationRule:
  clusterRoleSelectors:
  - matchLabels: { rbac.authorization.k8s.io/aggregate-to-operator: "true" }
rules: []                                 # populated via aggregation from the items below
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cisco-vk-device-operator-extras
  labels: { rbac.authorization.k8s.io/aggregate-to-operator: "true" }
rules:
- apiGroups: [config.cisco.vk]
  resources: [iosxediagnostics, iosxemaintenances]
  verbs: [get, list, watch, create, update, patch, delete]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cisco-vk-device-administrator
aggregationRule:
  clusterRoleSelectors:
  - matchLabels: { rbac.authorization.k8s.io/aggregate-to-administrator: "true" }
rules: []
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cisco-vk-device-administrator-extras
  labels: { rbac.authorization.k8s.io/aggregate-to-administrator: "true" }
rules:
- apiGroups: [config.cisco.vk]
  resources: [iosxedeviceops]
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [config.cisco.vk]
  resources: [iosxediagnostics, iosxemaintenances]
  verbs: [get, list, watch, create, update, patch, delete]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cisco-vk-device-approver
rules:
- apiGroups: [config.cisco.vk]
  resources: [iosxediagnostics, iosxemaintenances, iosxedeviceops]
  verbs: [get, list, watch]
- apiGroups: [config.cisco.vk]
  resources: [iosxedeviceops/approval]      # subresource — see §5.2
  verbs: [update]
```

The aggregation pattern means a future namespace-admin role can opt-in to operator privileges by labeling itself, without editing the canonical roles.

### 4.2 What "the wrong operator" looks like, by class

| Operator has | Tries to | Outcome |
|---|---|---|
| `viewer` | Create `IOSXEDiagnostic` | Forbidden by API server |
| `operator` | Create `IOSXEMaintenance` (clear) | Allowed |
| `operator` | Create `IOSXEDeviceOp` (reload) | Forbidden by API server |
| `administrator` | Create `IOSXEDeviceOp` AND approve their own CR | Author succeeds; approval verb is on a separate role; `administrator` doesn't have `iosxedeviceops/approval`; CR sits in `AwaitingApproval` |
| `administrator + approver` (same identity) | Same as above | Two-person rule enforced by ServiceAccount distinction (§5.2): approver subresource update must come from a *different* identity than the CR's `metadata.creationTimestamp` author. Validating webhook rejects same-identity approval. |

---

## 5. Safety belts

Five mechanisms layered on `IOSXEDeviceOp`. Each is independently bypassable by an operator with sufficient privilege; in combination they make accidental and casual destructive actions very hard.

### 5.1 Mandatory `confirmToken`

`spec.confirmToken` must equal `SHA-256(<device.metadata.uid> + "\n" + <action> + "\n" + <YYYY-MM-DD> + "\n" + <namespace>/<name>)`.

A validating admission webhook rejects CRs with a missing or wrong token. Effect: copy-pasting an old CR YAML between devices doesn't accidentally fire — the device-uid component changes, the token mismatches, the API server rejects. Authoring tools (the `kubectl ciscovk` plugin) can compute the token interactively after a `--confirm` prompt that shows the operator the device hostname + action.

This is structurally the same idea as `kubectl delete --confirm` in some plugins, but enforced server-side instead of client-side.

### 5.2 Two-person rule via `requireApprovalFrom`

`spec.requireApprovalFrom[]` lists identities (ServiceAccount or User) whose approval is required before reconcile fires. Approval is an `update` to the CR's `/approval` subresource:

```bash
kubectl patch iosxedeviceop chaos-2026-04-28-cat9k-smoke -n cisco-vk-smoke \
  --subresource=approval --type=merge -p \
  '{"approval":{"approvedBy":"system:serviceaccount:cisco-vk-system:netops-approver","approvedAt":"2026-04-28T14:30:00Z"}}'
```

A validating webhook enforces:
- The identity matches one of `spec.requireApprovalFrom[]`.
- The identity is **different** from the CR's create-time author (recorded via the `cisco.vk/created-by` annotation the admission webhook stamps on Create).
- The CR's spec hasn't changed since approval (approval signs over `spec` hash; webhook recomputes and compares).

Without the subresource the entire CR is mutable by the author after approval, which would defeat the rule — so the API surface is split deliberately.

### 5.3 Maintenance window — `notBefore` / `notAfter`

Required (not optional) on every `IOSXEDeviceOp`. The reconciler refuses to act outside the window. A CR created at 09:00 with `notBefore=15:00 / notAfter=15:30` sits in `phase: Scheduled` and reaches `phase: Executing` no earlier than 15:00, returning `phase: Expired` if reconcile hasn't run by 15:30.

Effect: a typo in `metadata.name` that targets the wrong CR's annotation can't immediately fire — there's always a wall-clock gate.

### 5.4 Dry-run mode — `spec.dryRun: true`

| Action | Dry-run behavior |
|---|---|
| `reload` | Runs `show reload` (which reports any pending reload timer); reports the device's current reload-pending state without scheduling a new one. Status carries the would-be `scheduledFor`. |
| `erase` | Walks the device's running-config and writes a redacted summary to `status.dryRunSummary` (e.g. "would erase 12 interfaces, 3 VLANs, 2 OSPF processes"); device-side state untouched. |
| `erase-and-reload` | Combines both; dry-run output explicitly notes the reload would NOT fire after the dry-run erase. |

Dry-run is a safe opt-in to test a maintenance window or approval flow without committing.

### 5.5 Per-device rate limit

`spec.rateLimit.perDeviceMinSeconds` (default 3600) — at most one destructive op per device per N seconds. The reconciler queries the `IOSXEDeviceOpAuditLog` (§6) to find the last completed op against the device; if newer than `now - rateLimit`, the CR moves to `phase: Failed reason=RateLimited`.

Operators can override (`perDeviceMinSeconds: 0`) but must explicitly do so — a default of "one per hour" matches typical operational caution. Cluster-wide policy can pin a minimum via a ValidatingAdmissionPolicy that rejects CRs with too-short rate limits.

---

## 6. Audit chain — `IOSXEDeviceOpAuditLog`

Append-only, tamper-evident log of every `IOSXEDeviceOp` reconcile. One per device (singleton, similar to `IOSXEConfigApplyLog`) but distinct because:

- it persists past `IOSXEDeviceOp` deletion
- entries are cryptographically chained: `entry.hash = SHA-256(prev.hash || canonical(entry))`
- truncation past `MaxEntries` is forbidden — the controller refuses to write when the log would overflow, surfacing a `Warning AuditLogFull` event so operators can manually archive

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDeviceOpAuditLog
metadata:
  name: cat9k-smoke
  namespace: cisco-vk-smoke
spec:
  deviceRef: { name: cat9k-smoke }
  maxEntries: 1000
status:
  entries:
    - time: "2026-04-28T15:00:04Z"
      action: reload
      sourceCR: cisco-vk-smoke/chaos-2026-04-28-cat9k-smoke@1
      author:   "system:serviceaccount:cisco-vk-smoke:chaos-runner"
      approver: "system:serviceaccount:cisco-vk-system:netops-approver"
      result:   Completed
      message:  "device reloaded; reachable again at 15:04:12"
      hash:     "sha256:ab12...ef"           # H(prev.hash || canonical(this entry))
      prevHash: "sha256:33aa...bc"
  conditions:
    - type: ChainValid
      status: "True"
      reason: Succeeded
      lastTransitionTime: "2026-04-28T15:04:13Z"
```

A separate cluster cronjob (or just a `kubectl ciscovk audit verify`) re-walks the chain weekly and posts a `(Warning, AuditChainBroken)` event if any link fails. This is the structural backstop for the "an admin tampered with status" concern.

---

## 7. Workflow examples

### 7.1 Incident clear (operator role, no approval needed)

```bash
$ cat <<EOF | kubectl apply -f -
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEMaintenance
metadata:
  name: incident-2026-04-28-ospf-reset
  namespace: cisco-vk-smoke
spec:
  deviceRef: { name: cat9k-smoke }
  commands: [ "clear ip ospf process" ]
EOF
iosxemaintenance.config.cisco.vk/incident-2026-04-28-ospf-reset created

$ kubectl get iosxemaintenance -n cisco-vk-smoke
NAME                                  DEVICE         PHASE       WINDOW   AGE
incident-2026-04-28-ospf-reset        cat9k-smoke    Completed   —        4s
```

### 7.2 Chaos reload (admin role + approver)

Two operators, one CR:

```bash
# Operator A (chaos-runner SA, has cisco-vk-device-administrator):
TOKEN=$(kubectl ciscovk confirm-token --device cat9k-smoke --action reload --namespace cisco-vk-smoke)
cat <<EOF | kubectl apply -f -
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDeviceOp
metadata: { name: chaos-2026-04-28-cat9k-smoke, namespace: cisco-vk-smoke }
spec:
  deviceRef: { name: cat9k-smoke }
  action: reload
  reload:
    inMinutes: 5
    saveConfigBefore: true
  notBefore: "2026-04-28T15:00:00Z"
  notAfter:  "2026-04-28T16:00:00Z"
  confirmToken: ${TOKEN}
  requireApprovalFrom:
    - kind: ServiceAccount
      name: netops-approver
      namespace: cisco-vk-system
EOF

$ kubectl get iosxedeviceop -n cisco-vk-smoke
NAME                                DEVICE         ACTION   PHASE              APPROVED-BY   SCHEDULED-FOR        AGE
chaos-2026-04-28-cat9k-smoke        cat9k-smoke    reload   AwaitingApproval                                       30s

# Operator B (netops-approver SA, has cisco-vk-device-approver) — different identity:
$ kubectl patch iosxedeviceop chaos-2026-04-28-cat9k-smoke -n cisco-vk-smoke \
    --subresource=approval --type=merge \
    -p '{"approval":{"approvedAt":"2026-04-28T14:30:00Z"}}'

$ kubectl get iosxedeviceop -n cisco-vk-smoke
NAME                                DEVICE         ACTION   PHASE       APPROVED-BY                                   SCHEDULED-FOR        AGE
chaos-2026-04-28-cat9k-smoke        cat9k-smoke    reload   Scheduled   system:serviceaccount:cisco-vk-system:...    2026-04-28T15:05:00  4m
```

### 7.3 Greenfield re-provisioning (admin role + approver)

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDeviceOp
metadata: { name: provision-2026-04-29-newdevice, namespace: cisco-vk-smoke }
spec:
  deviceRef: { name: newdevice-1 }
  action: erase-and-reload                # write erase, then reload
  reload:
    inMinutes: 1                          # reload immediately after erase
    saveConfigBefore: false               # we're erasing — don't save first!
  notBefore: "2026-04-29T02:00:00Z"
  notAfter:  "2026-04-29T03:00:00Z"
  confirmToken: <sha256>
  requireApprovalFrom:
    - kind: ServiceAccount
      name: provisioning-approver
      namespace: cisco-vk-system
  rateLimit:
    perDeviceMinSeconds: 0                # provisioning is one-shot per device; no rate limit
```

After completion, a separate provisioning workflow (typically a `IOSXEConfigBundle` matching the new device's labels) recreates configuration.

---

## 8. Phasing

| Phase | Scope | Effort |
|---|---|---|
| **A** | `IOSXEMaintenance` CRD + reconciler + cli-exec extension to support clear-class commands | ~600 LoC |
| **B** | `IOSXEDeviceOp` CRD with `action: reload` only (no erase yet); approval subresource; confirmToken webhook; maintenance window enforcement; rate limit | ~1200 LoC |
| **C** | `IOSXEDeviceOpAuditLog` CRD with cryptographic chaining + `kubectl ciscovk audit verify` plugin command | ~400 LoC |
| **D** | `action: erase` and `action: erase-and-reload` | ~200 LoC |
| **E** | Cross-device chaos coordination (`IOSXEDeviceOpBundle`, rolling reload with health gates between hops) | ~800 LoC |

Phase A delivers the headline ergonomics (`clear` from kubectl). Phase B is the load-bearing safety work for everything destructive. Phase C closes the "admin tampers with status" concern. Phase D is the smallest delta on top of B. Phase E is gated on real-world demand.

A and B together are the minimum viable destructive-ops surface.

---

## 9. Open questions

1. **Should approval be K8s-native (`requireApprovalFrom: [ServiceAccount]`) or external (webhook to ServiceNow / Jira / PagerDuty)?**
   The proposal here is K8s-native, which avoids new infrastructure but means the approval signal is K8s-only. A webhook escape hatch (`spec.requireApprovalFrom: [{kind: Webhook, url: ...}]`) is a clean future extension.

2. **How does the controller observe a reload's completion?**
   The per-device pod's transport dial fails for the duration of the reload window. The reconciler should mark `phase: Executing` when the reload command lands, then poll connectivity until the device returns. If polling times out (`status.timeoutAfter`), `phase: Failed reason=DidNotReturn`. The audit log's `result` field reflects this terminal state.

3. **Default rate-limit value.**
   `perDeviceMinSeconds: 3600` is conservative. Chaos-engineering teams running staged outages may want shorter; greenfield provisioners may want longer. The default should be cautious; per-namespace ResourceQuotas could enforce minimums for production namespaces.

4. **Cluster-wide ValidatingAdmissionPolicy for namespace gating.**
   Some operators may want to restrict `IOSXEDeviceOp` to specific namespaces (e.g. only `cisco-vk-prod-maintenance` can host them). A documented VAP recipe in the chart would be useful.

5. **Interaction with `IOSXEConfig` reconciles.**
   When a device is reloading, every `IOSXEConfig` reconcile against it will fail. Should the engine pause those reconciles (set `phase: Paused reason=DeviceOpInProgress`) when an `IOSXEDeviceOp` is `Executing`? Probably yes — surfacing transient apply failures during a planned reload is noise. A future cross-CR awareness loop closes this.

---

## 10. Decision

**Status:** RFC is open. Recommended path forward: implement Phase A as a follow-up to the diagnostics RFC's Phase A+B (since both depend on the `DiagnosticExecer` transport extension, which becomes a more general `OperationalExecer` once `clear`-class commands are in scope). Phase B is the next milestone.

The CRD-per-tier model is non-negotiable: collapsing classes into one `IOSXEDeviceCommand` CRD with a `spec.class` enum would force every RBAC consumer to understand which class is allowed via fine-grained verbs, which is brittle and audit-hostile. Three tiers + four roles is the right granularity.

---

## See also

- [`./diagnostics-rfc.md`](./diagnostics-rfc.md) — the read-only sibling; its `DiagnosticExecer` transport extension generalises to support this RFC's clear/reload/erase commands without re-implementing the RPC plumbing
- [`./operator-cli-guide.md`](./operator-cli-guide.md) — operator-side kubectl reference; `IOSXEMaintenance` and `IOSXEDeviceOp` printer columns and event taxonomy will land alongside Phase A and Phase B respectively
- [`./transport-architecture.md`](./transport-architecture.md) — the `transport.Interface` extension surface this RFC builds on
