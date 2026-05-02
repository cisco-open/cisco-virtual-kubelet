# Operator CLI guide — kubectl interaction with cisco-vk CRDs

**Audience:** operators who deploy, observe, and troubleshoot cisco-virtual-kubelet via `kubectl`. Maintainers wanting the architectural reference should read [`./transport-architecture.md`](./transport-architecture.md); operators who need per-mode setup should start with [`./deployment-modes.md`](./deployment-modes.md).

This document is the single reference for the operator CLI surface: which `kubectl get` columns are printed today, what each status field means, what events fire, what metrics expose, and the per-CRD troubleshooting cookbook. It closes with a roadmap of operator-UX enrichments that would meaningfully improve `kubectl`-day usability.

**Branch:** `pr/johalley/nacxe`
**Sample outputs:** lifted verbatim from the 2026-04-27 live-device evidence bundle; captured outputs live in the source-branch git history.

---

## 1. CRD inventory at a glance

| API group/version | Kind | Scope | What it represents |
|---|---|---|---|
| `cisco.vk/v1alpha1` | `CiscoDevice` | Namespaced | Connection details for one IOS-XE device + apphosting state |
| `config.cisco.vk/v1alpha1` | `IOSXEConfig` | Namespaced | Per-device declarative configuration intent for a closed list of families |
| `config.cisco.vk/v1alpha1` | `IOSXEConfigBundle` | Namespaced | Selector-based fan-out of an `IOSXEConfig` template across multiple devices |
| `config.cisco.vk/v1alpha1` | `IOSXEConfigDefaults` | Cluster | Cluster-wide baseline merged into every resolved intent |
| `config.cisco.vk/v1alpha1` | `IOSXEDeviceGroupConfig` | Namespaced | Group-scoped configuration; CiscoDevices are matched by labels |
| `config.cisco.vk/v1alpha1` | `IOSXEInterfaceGroupConfig` | Namespaced | Per-interface configuration shared across devices |
| `config.cisco.vk/v1alpha1` | `IOSXETemplate` | Namespaced | Reusable parameterised configuration fragment |
| `config.cisco.vk/v1alpha1` | `IOSXEConfigApplyLog` | Namespaced | Per-device circular audit log of apply outcomes (replay-capable) |
| `config.cisco.vk/v1alpha1` | `IOSXEDiagnostic` | Namespaced | Read-only show-command capture (one-shot or scheduled) — see [`./diagnostics-guide.md`](./diagnostics-guide.md) |

Short kind names accepted by `kubectl`:

| Kind | Short name | Plural |
|---|---|---|
| `CiscoDevice` | `cd` | `ciscodevices` |
| `IOSXEConfig` | `iosxe`, `iosxeconfig` | `iosxeconfigs` |
| `IOSXEConfigBundle` | `iosxebundle` | `iosxeconfigbundles` |
| `IOSXEConfigDefaults` | `iosxedefaults` | `iosxeconfigdefaults` |
| `IOSXEConfigApplyLog` | `iosxelog` | `iosxeconfigapplylogs` |
| `IOSXETemplate` | `iosxetpl` | `iosxetemplates` |
| `IOSXEDeviceGroupConfig` | `iosxedgc` | `iosxedevicegroupconfigs` |
| `IOSXEInterfaceGroupConfig` | `iosxeigc` | `iosxeinterfacegroupconfigs` |
| `IOSXEDiagnostic` | `iosxediag` | `iosxediagnostics` |

Every config-side CRD has the `status` subresource (so `kubectl apply` to spec doesn't update status, and the controller's status writes don't require RBAC for the spec verb).

---

## 2. `CiscoDevice` — the apphosting half

This is the legacy CRD on `cisco.vk/v1alpha1`. It carries connection details (address, port, credential ref, transport, TLS) and reports apphosting lifecycle state. The configuration-driver subsystem reads `spec.transport` / `spec.address` from here at startup.

### 2.1 `kubectl get ciscodevice` — printer columns

`additionalPrinterColumns` ([`charts/cisco-virtual-kubelet/crds/cisco.vk_ciscodevices.yaml`](../../charts/cisco-virtual-kubelet/crds/cisco.vk_ciscodevices.yaml)):

| Column | JSONPath | Type |
|---|---|---|
| `DRIVER` | `.spec.driver` | string (XE / XR / NXOS / OPENCONFIG / FAKE) |
| `ADDRESS` | `.spec.address` | string |
| `PHASE` | `.status.phase` | string (Pending / Provisioning / Ready / Error / Deleting) |
| `AGE` | `.metadata.creationTimestamp` | date |

Sample output:
```
$ kubectl get ciscodevice -n cisco-vk-smoke
NAME           DRIVER   ADDRESS    PHASE   AGE
cat9k-smoke    XE       10.1.1.1   Ready   2d6h
```

### 2.2 Status shape

| Field | Type | Notes |
|---|---|---|
| `phase` | string | Lifecycle: `Pending` → `Provisioning` → `Ready`; `Error` on transient failures; `Deleting` after the finalizer kicks in |
| `conditions[]` | `metav1.Condition` | Standard Kubernetes shape; types depend on the controller (today minimal — see §13 roadmap) |

### 2.3 Common operator one-liners

```bash
# Phase summary across all devices
kubectl get cd -A -o custom-columns=NS:.metadata.namespace,NAME:.metadata.name,PHASE:.status.phase,TRANSPORT:.spec.transport,ADDR:.spec.address

# Find devices stuck in non-Ready
kubectl get cd -A -o jsonpath='{range .items[?(@.status.phase!="Ready")]}{.metadata.namespace}{"/"}{.metadata.name}{": "}{.status.phase}{"\n"}{end}'

# Inspect one device
kubectl describe cd cat9k-smoke -n cisco-vk-smoke

# Watch the per-device kubelet pod (per-pod-kubelet topology)
kubectl get pod -n cisco-vk-smoke -l app.kubernetes.io/instance=cat9k-smoke -w
```

### 2.4 Troubleshooting

| Symptom | Likely cause |
|---|---|
| `phase=Error` | Apphosting connectivity check failed at pod startup (port 443 RESTCONF unreachable); check device-side `restconf` / `ip http secure-server`. Apphosting is RESTCONF-only — see [`./deployment-modes.md`](./deployment-modes.md) §6 |
| `phase=Pending` for >30 s | Per-device pod hasn't been scheduled or the configdriver's deferred-dial is still retrying. `kubectl describe pod -n <ns> -l app.kubernetes.io/instance=<device>` |
| `phase=Ready` but configdriver phase=`Failed` | Apphosting connectivity is fine but configdriver transport (NETCONF/gNMI) failed; switch to the per-pod kubelet logs and search for `config_reconciler` lines |

---

## 3. `IOSXEConfig` — the most operator-facing CRD

This is what an operator interacts with most. Each `IOSXEConfig` declares intent for a closed list of families on one device.

### 3.1 `kubectl get iosxeconfig` — printer columns

`additionalPrinterColumns` ([`charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml`](../../charts/cisco-virtual-kubelet/crds/config.cisco.vk_iosxeconfigs.yaml)):

| Column | JSONPath | Type |
|---|---|---|
| `DEVICE` | `.spec.deviceRef.name` | string |
| `PHASE` | `.status.phase` | string (10-value enum, see §3.2) |
| `DRIFT` | `.spec.driftPolicy` | string (`report`, `revert`, `pause`) |
| `AGE` | `.metadata.creationTimestamp` | date |

Sample output:
```
$ kubectl get iosxe -n cisco-vk-smoke
NAME                          DEVICE         PHASE     DRIFT    AGE
test-06-driftpolicy-revert    cat9k-smoke    InSync    revert   2h
test-07-write-startup         cat9k-smoke    InSync    revert   2h
```

### 3.2 Status shape

`status.phase` is the 10-value coarse summary every operator dashboards on:

| Phase | Meaning |
|---|---|
| `Pending` | Reconcile loop hasn't observed the CR yet |
| `Validating` | Spec / source / template validation in progress |
| `Planning` | Engine computed Diff per family; ops list is being built |
| `Applying` | Engine is calling `Mutate` on the transport |
| `Verifying` | Post-apply Fetch + leavesEqual confirming the device matches intent |
| `InSync` | Device matches intent; no drift detected on last drift-detect tick |
| `Drifted` | Drift detected; engine action depends on `spec.driftPolicy` (report / revert / pause) |
| `Failed` | Reconcile error that the engine cannot self-recover from this tick (verify-failed, apply-failed, etc) |
| `Paused` | `driftPolicy: pause` — operator-explicit reconcile suspension |
| `LeaseBlocked` | Another CR holds the lease for an overlapping managed family; transient — resolves when the foreign CR releases |

Other status fields:

| Field | Meaning |
|---|---|
| `observedGeneration` | The `.metadata.generation` the driver last reconciled. Operator check: `observedGeneration == generation` means latest spec was acted on. |
| `lastAppliedHash` | Stable SHA-256 over the canonicalised resolved intent. Drives the engine's hash short-circuit; operators can compare hashes across pods to confirm both ran with identical intent. |
| `lastAppliedTime` | Wall-clock of the most recent successful apply. |
| `lastDeviceCheck` | Most recent reconcile that bypassed the hash short-circuit and actually fetched device state. Drives the `driftDetectInterval` clock. |
| `sourceYangVersion` | YANG release tag the driver translated intent against on last apply. Useful when correlating device state to a families.yaml schema version. |
| `familyStatus[]` | Per-family rollup. Each entry: `name`, `state` (`Pending` / `InSync` / `Drifted` / `ApplyError` / `Skipped` / `Unsupported`), optional `entries` (keyed-list count observed), `opCount` (writer ops emitted this tick), `message` (failure reason). |
| `drift[]` | Up to 50 drift entries. Each: `family`, `path`, optional `desired` / `observed`, `detected` (first-observed timestamp). Truncation past 50 increments `cisco_vk_config_drift_entries_truncated_total`. |
| `conditions[]` | Standard Kubernetes shape. Today populated: `Ready`, `Conflict`. |

Real-status sample (test 07 over NETCONF candidate-only, 2026-04-27):

```yaml
status:
  phase: InSync
  observedGeneration: 1
  lastAppliedHash: sha256:18bf932c8688d9ec96ae59d767bf24dec221bdd3c0177cd76b5f58e7e62b9df5
  lastAppliedTime: "2026-04-27T18:53:40Z"
  sourceYangVersion: "1791"
  familyStatus:
  - name: interface_loopback
    state: InSync
  conditions:
  - lastTransitionTime: "2026-04-27T18:51:27Z"
    message: device reconciled to declared intent
    reason: Succeeded
    status: "True"
    type: Ready
  - lastTransitionTime: "2026-04-27T18:43:41Z"
    message: no other CR claims this CR's managed families
    reason: NoOverlap
    status: "False"
    type: Conflict
```

### 3.3 Common operator one-liners

```bash
# Fleet-wide phase summary
kubectl get iosxe -A -o custom-columns=\
'NS:.metadata.namespace,NAME:.metadata.name,DEVICE:.spec.deviceRef.name,PHASE:.status.phase,FAMILIES:.spec.managedFamilies,LASTAPPLY:.status.lastAppliedTime'

# Find every CR that's not InSync
kubectl get iosxe -A -o jsonpath='{range .items[?(@.status.phase!="InSync")]}{.metadata.namespace}{"/"}{.metadata.name}{": "}{.status.phase}{"\n"}{end}'

# Drift detail for one CR
kubectl get iosxe <name> -n <ns> -o jsonpath='{range .status.drift[*]}{.family}{":"}{.path}{" desired="}{.desired}{" observed="}{.observed}{"\n"}{end}'

# Per-family state
kubectl get iosxe <name> -n <ns> -o jsonpath='{range .status.familyStatus[*]}{.name}{"="}{.state}{" ("}{.message}{")\n"}{end}'

# Generation-vs-observedGeneration sanity
kubectl get iosxe <name> -n <ns> -o jsonpath='gen={.metadata.generation} obsGen={.status.observedGeneration} phase={.status.phase}{"\n"}'

# Trigger an immediate reconcile (poke the annotation)
kubectl annotate iosxe <name> -n <ns> 'cisco.vk/poke='"$(date +%s)" --overwrite

# Tail apply events
kubectl get events -n <ns> --field-selector involvedObject.name=<name> --sort-by='.lastTimestamp' -w
```

### 3.4 Troubleshooting decision tree

```
phase != InSync?
├── phase=LeaseBlocked
│   └── Look at status.conditions[?type=="Conflict"].message
│       → another CR holds the family lease; wait or delete the foreign CR
├── phase=Failed
│   ├── status.familyStatus[*].message contains rpc-error → see deployment-modes.md §4.5/§5.5
│   ├── status.familyStatus[*].state=ApplyError stage=verify
│   │   → pre-fix image (commits 88ac685 / d27016d). Upgrade.
│   └── lastDeviceCheck older than driftDetectInterval × 2
│       → reconcile is stuck; check kubelet pod logs for transport errors
├── phase=Drifted
│   └── status.drift[*] lists divergences; spec.driftPolicy decides whether to revert
└── phase=Paused
    └── spec.driftPolicy: pause was applied — operator-explicit; clear when ready
```

---

## 4. `IOSXEConfigBundle` — fleet rollout

Selector-based fan-out: stamps a templated `IOSXEConfig` onto every CiscoDevice that matches `deviceSelector` or appears in `deviceRefs`. Each generated child is owned by the bundle (deletion cascades).

### 4.1 `kubectl get iosxebundle` — printer columns

| Column | JSONPath | Type |
|---|---|---|
| `DEVICES` | `.status.memberDevices` | integer |
| `AGE` | `.metadata.creationTimestamp` | date |

```
$ kubectl get iosxebundle -n cisco-vk-smoke
NAME                  DEVICES   AGE
lab-banner-rollout    12        4h
```

### 4.2 Status shape

| Field | Meaning |
|---|---|
| `observedGeneration` | Controller's last-reconciled generation |
| `memberDevices` | Devices currently in scope (selector + explicit refs union) |
| `generatedCRs[]` | Every child IOSXEConfig the bundle owns: `{name, device, phase}` triple, name-sorted for diff-friendly output |
| `conditions[]` | Standard shape (today minimal — see §13 roadmap) |

### 4.3 Common operator one-liners

```bash
# Roll-up status of every child
kubectl get iosxebundle <name> -n <ns> -o jsonpath='{range .status.generatedCRs[*]}{.name}{" device="}{.device}{" phase="}{.phase}{"\n"}{end}'

# Find children that aren't InSync
kubectl get iosxebundle <name> -n <ns> -o jsonpath='{range .status.generatedCRs[?(@.phase!="InSync")]}{.device}{": "}{.phase}{"\n"}{end}'

# Tear the entire bundle down (cascading delete via owner-refs)
kubectl delete iosxebundle <name> -n <ns>
```

---

## 5. `IOSXEConfigDefaults` — cluster baseline

Singleton (`metadata.name: default`), cluster-scoped, lowest-precedence merge layer. Every per-device resolved intent starts here.

### 5.1 `kubectl get iosxedefaults` — printer columns

| Column | JSONPath | Type |
|---|---|---|
| `AFFECTED` | `.status.affectedDevices` | integer |
| `AGE` | `.metadata.creationTimestamp` | date |

### 5.2 Status

| Field | Meaning |
|---|---|
| `observedGeneration` | Aggregator's last-read generation |
| `affectedDevices` | CiscoDevices whose resolved intent currently includes this defaults block |
| `conditions[]` | Standard shape |

### 5.3 Operator one-liners

```bash
# Show the defaults config inline
kubectl get iosxedefaults default -o jsonpath='{.spec.configuration}' | yq

# How many devices does it touch?
kubectl get iosxedefaults default -o jsonpath='{.status.affectedDevices}'
```

---

## 6. `IOSXEDeviceGroupConfig` and `IOSXEInterfaceGroupConfig`

Selector-scoped layers in the resolution chain. Device-group binds shared configuration to devices matching `deviceSelector.matchLabels`. Interface-group binds per-interface configuration shared across devices, so a "core uplinks" interface set lives in one CR.

### 6.1 Printer columns

| Kind | Column | JSONPath |
|---|---|---|
| `IOSXEDeviceGroupConfig` | `MEMBERS` | `.status.memberDevices` |
| `IOSXEInterfaceGroupConfig` | `MEMBERS` | `.status.memberInterfaces` |

Both add `AGE` from `metadata.creationTimestamp`.

### 6.2 Status fields

| Field (both) | Meaning |
|---|---|
| `observedGeneration` | Aggregator's last-read generation |
| `memberDevices` (DGC) / `memberDevices` + `memberInterfaces` (IGC) | Resolved scope counts |
| `conditions[]` | Standard shape |

### 6.3 Operator one-liners

```bash
# Which devices are in the access-switches group?
kubectl get iosxedgc access-switches -n <ns> -o jsonpath='{.spec.deviceSelector}'

# Which interfaces does the core-uplinks group bind to?
kubectl get iosxeigc core-uplinks -n <ns> -o jsonpath='{.spec.interfaceSelector}'
```

---

## 7. `IOSXETemplate` — parameterised reuse

Reusable configuration fragment expanded at resolve time. Referenced from `IOSXEConfig.spec.templateRefs[].values` (see [`./deployment-modes.md`](./deployment-modes.md) §13.3).

### 7.1 Printer column

| Column | JSONPath | Type |
|---|---|---|
| `REFS` | `.status.referencers` | integer |
| `AGE` | `.metadata.creationTimestamp` | date |

### 7.2 Status

| Field | Meaning |
|---|---|
| `observedGeneration` | Aggregator's last-read generation |
| `referencers` | IOSXEConfig CRs whose `spec.templateRefs` include this template |
| `conditions[]` | Standard shape |

### 7.3 Operator one-liners

```bash
# Which IOSXEConfig CRs depend on this template?
kubectl get iosxe -A -o jsonpath='{range .items[?(@.spec.templateRefs)]}{.metadata.namespace}{"/"}{.metadata.name}{": "}{.spec.templateRefs[*].name}{"\n"}{end}' | grep <template-name>

# Show the parameter list a template requires
kubectl get iosxetpl <name> -n <ns> -o jsonpath='{range .spec.parameters[*]}{.name}{"("}{.type}{") required="}{.required}{" default="}{.default}{"\n"}{end}'
```

---

## 8. `IOSXEConfigApplyLog` — audit + replay

One per device. Circular log of recent apply outcomes (default cap 50 entries; configurable via `spec.maxEntries`). Replay-capable via the `config.cisco.vk/replay-from-log: <entry-index>` annotation on the corresponding `IOSXEConfig`.

### 8.1 Printer columns

| Column | JSONPath | Type |
|---|---|---|
| `DEVICE` | `.spec.deviceRef.name` | string |
| `ENTRIES` | derived from `.status.entries[*].time` count | integer |
| `TRUNCATED` | `.status.truncatedTotal` | integer (cumulative drops) |
| `AGE` | `.metadata.creationTimestamp` | date |

### 8.2 Status

| Field | Meaning |
|---|---|
| `entries[]` | Chronological apply history (oldest at index 0). Each: `time`, `phase`, `hash`, `sourceCR` (`namespace/name@generation`), `families[]` (per-family `name/state/opCount`), `message` (on failure), `body` (full resolved intent — only when `spec.retainBody=true`) |
| `oldestRetainedAt` | Timestamp of `entries[0]` — read the retention window without iterating |
| `truncatedTotal` | Entries dropped since CR creation due to `MaxEntries` cap. Alert if growing fast — operator may want to increase MaxEntries or move to external audit |
| `conditions[]` | Standard shape |

### 8.3 Replay an earlier known-good intent

```bash
# 1. Find the entry index you want to replay (zero-based, oldest first):
kubectl get iosxelog cat9k-smoke -n cisco-vk-smoke \
  -o jsonpath='{range .status.entries[*]}{.time}{"  "}{.phase}{"  "}{.sourceCR}{"\n"}{end}' | nl -ba

# 2. Annotate the IOSXEConfig that drives this device with the chosen index:
kubectl annotate iosxe <iosxeconfig-name> -n <ns> \
  config.cisco.vk/replay-from-log=12 --overwrite

# 3. Watch the next reconcile pick it up:
kubectl get iosxe <name> -n <ns> -w
```

The engine clears the annotation on a successful replay; if it fails to clear, a `Warning ReplayAnnotationClearFailed` event is emitted but the reconcile completes.

### 8.4 Operator one-liners

```bash
# Compact apply history (one line per entry)
kubectl get iosxelog <device> -n <ns> -o jsonpath=\
'{range .status.entries[*]}{.time}{"  "}{.phase}{"  ops="}{range .families[*]}{.opCount}{","}{end}{"\n"}{end}'

# Just the most recent failed apply
kubectl get iosxelog <device> -n <ns> -o jsonpath=\
'{.status.entries[?(@.phase=="Failed")][-1:]}'
```

---

## 9. Events catalog

Every `IOSXEConfig` reconcile path can emit the following Kubernetes events. List them with `kubectl get events -n <ns> --field-selector involvedObject.name=<iosxeconfig>` or — for fleet view — drop the field-selector.

| Type | Reason | When |
|---|---|---|
| Warning | `NoTransport` | configdriver started without a usable transport (deferred-dial still retrying) |
| Normal | `DriftDetected` | family `<name>` diverged from intent (per-family event) |
| Warning | `ApplyFailed` | per-family `ApplyError` stage failure; message includes the rpc-error or HTTP-error |
| Warning | `FamilyUnsupported` | declared family not supported by the chosen transport / device combo |
| Normal | `FamilySkipped` | family deliberately skipped (e.g. already in-sync, hash short-circuit) |
| Normal | `AppliedSuccess` | terminal event when at least one family had `OpCount > 0` (suppresses no-op spam) |
| Warning | `ReconcileFailed` | terminal failure event; message is the surfacing error |
| Normal | `Paused` | `driftPolicy: pause` engaged |
| Warning | `SaveStartupFailed` | `writeStartup: true` succeeded on apply but the save-startup RPC failed (apply still landed) |
| Normal | `SaveStartupOK` | `writeStartup: true` save-startup RPC succeeded |
| Warning | `ConfirmedCommitFallback` | `confirmTimeoutSeconds > 0` but transport / capability unavailable; fell back to plain commit. Reason field tells you which fallback path |
| Normal | `ConfirmedCommitUsed` | confirmed-commit auto-revert timer engaged |
| Warning | `ReplayAnnotationClearFailed` | replay completed but annotation could not be cleared (retried next reconcile) |
| Warning | `ApplyLogUpdateFailed` | could not append to `IOSXEConfigApplyLog`; non-fatal — device state is authoritative |

Aggregator-mode also emits `(Warning, AggregatorCredentialFailed)`, `(Warning, AggregatorWorkerFailed)`, `(Warning, AggregatorWorkerExit)` against the `CiscoDevice` (not the IOSXEConfig).

---

## 10. Metrics catalog

The cisco-vk pod exposes Prometheus metrics on `:8080/metrics` (controller-runtime registry). Scrape via Service or `kubectl port-forward`. The labels every per-CR metric carries: `device` (always), `family` (per-family metrics), `transport` (where transport choice matters), `verb` (write metrics).

### 10.1 Reconcile + drift

| Metric | Type | Labels | What it answers |
|---|---|---|---|
| `cisco_vk_config_reconcile_duration_seconds` | Histogram | `device`, `phase` | "How long does each phase take?" |
| `cisco_vk_config_apply_duration_seconds` | Histogram | `device`, `family` | "Which families are slow to apply?" |
| `cisco_vk_config_drift_detected_total` | Counter | `device`, `family` | "Which families drift the most?" |
| `cisco_vk_config_drift_corrected_total` | Counter | `device`, `family` | "Is drift being remediated?" |
| `cisco_vk_config_drift_entries_truncated_total` | Counter | `device` | "Are we losing drift detail to the 50-entry cap?" |
| `cisco_vk_config_apply_errors_total` | Counter | `device`, `family`, `stage` (fetch / diff / apply / verify) | "What stage fails most?" |
| `cisco_vk_config_family_state` | Gauge | `device`, `family` | Current state (0=InSync, 1=Drifted, 2=ApplyError, 3=Skipped, 4=Unsupported). Use for alert rules. |

### 10.2 Transport-aware writes

| Metric | Labels |
|---|---|
| `cisco_vk_config_mutate_ops_total` | `device`, `transport`, `verb` |
| `cisco_vk_config_transactions_total` | `device`, `transport`, `outcome` (`commit` / `discard` / `start_failed` / `commit_failed`) |
| `cisco_vk_config_save_startup_total` | `device`, `transport`, `outcome` (`ok` / `failed`) |

Real numbers from the 2026-04-27 NETCONF candidate-only evidence:
```
cisco_vk_config_mutate_ops_total{device="cat9k-smoke",transport="netconf",verb="MERGE"}          1
cisco_vk_config_transactions_total{device="cat9k-smoke",outcome="commit",transport="netconf"}  27
cisco_vk_config_transactions_total{device="cat9k-smoke",outcome="start_failed",transport="netconf"} 2
cisco_vk_config_save_startup_total{device="cat9k-smoke",outcome="ok",transport="netconf"}      27
```

### 10.3 gNMI subscribe stream

| Metric | Type | Labels | When to alert |
|---|---|---|---|
| `cisco_vk_config_subscribe_events_dropped_total` | Counter | `device` | Rate > 0 → reconcile consumer is slower than device notification cadence |

### 10.4 Apphosting node telemetry

The cisco-vk pod also exposes apphosting-side device metrics that surface as Node capacity/usage in the kubelet's view:

| Metric | Labels |
|---|---|
| `cisco_device_cpu_usage_percent` | — |
| `cisco_device_memory_used_bytes` / `cisco_device_memory_total_bytes` | — |
| `cisco_device_storage_used_bytes` / `cisco_device_storage_total_bytes` | — |
| `cisco_device_interface_rx_bits_per_sec` / `..._tx_bits_per_sec` | `interface` |
| `cisco_device_interface_state` | `interface`, `state` (1=up, 0=down) |
| `cisco_device_cdp_neighbor_count` | — |
| `cisco_device_neighbor_link` | `target`, `interface`, `protocol` (`cdp`), `platform` |
| `cisco_device_ospf_neighbor_count` | — |

### 10.5 One-liners for metric scraping

```bash
# Port-forward and grep for one device
POD=$(kubectl get pod -n cisco-vk-smoke -l app.kubernetes.io/instance=cat9k-smoke -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n cisco-vk-smoke "$POD" 18080:8080 &
curl -s :18080/metrics | grep -E '^cisco_vk_config_(mutate_ops|drift_detected|family_state)' | grep cat9k-smoke

# Family states for fleet — useful as the alert-rule predicate
curl -s :18080/metrics | grep '^cisco_vk_config_family_state' | awk -F'[{}=" ]+' '{print $2"/"$3,$4,"="$NF}' | column -t
```

---

## 11. Troubleshooting cookbook

### 11.1 "Why is my CR stuck in Drifted?"

```bash
# 1. What's drifting?
kubectl get iosxe <name> -n <ns> -o jsonpath=\
'{range .status.drift[*]}{.family}{"  "}{.path}{"\n   desired="}{.desired}{"\n   observed="}{.observed}{"\n"}{end}'

# 2. Is the policy actually revert?
kubectl get iosxe <name> -n <ns> -o jsonpath='{.spec.driftPolicy}'

# 3. Is the engine even reaching the device? Check transactions metric.
curl -s :18080/metrics | grep transactions_total | grep "$(kubectl get iosxe <name> -n <ns> -o jsonpath='{.spec.deviceRef.name}')"

# 4. Recent events
kubectl get events -n <ns> --field-selector involvedObject.name=<name> --sort-by='.lastTimestamp' | tail -10
```

### 11.2 "LeaseBlocked — who's holding the lease?"

```bash
# The lease is in the device-namespace by default; kubectl get leases shows owner identity.
kubectl get leases -n <ns> | grep <device-name>

# Foreign holder will appear in conditions[Conflict].message:
kubectl get iosxe <name> -n <ns> -o jsonpath='{.status.conditions[?(@.type=="Conflict")].message}'
```

### 11.3 "Apply failed — what was the rpc-error?"

```bash
# Engine surfaces the error verbatim in familyStatus.message.
kubectl get iosxe <name> -n <ns> -o jsonpath=\
'{range .status.familyStatus[?(@.state=="ApplyError")]}{.name}{": "}{.message}{"\n"}{end}'

# Or watch live:
kubectl get events -n <ns> --field-selector involvedObject.name=<name>,reason=ApplyFailed -w
```

### 11.4 "I want to roll back to a known-good apply"

See §8.3 for the replay annotation flow. If the apply log doesn't have a satisfactory entry, treat it as a normal `kubectl apply` of the prior `IOSXEConfig` revision (assuming GitOps source-of-truth).

### 11.5 "Transport-mode confusion — which transport is being used?"

```bash
# Check the metric labels — they tell you which transport actually drove the writes
curl -s :18080/metrics | grep cisco_vk_config_mutate_ops_total | grep "$(kubectl get iosxe <name> -n <ns> -o jsonpath='{.spec.deviceRef.name}')"

# Confirm the device's transport spec (the configmap mounted into the per-device pod is regenerated by the controller):
kubectl get cd <device> -n <ns> -o jsonpath='transport={.spec.transport}{"\n"}'

# If the metric shows `transport="restconf"` but the spec says `netconf`, the pod is stale — restart it:
kubectl delete pod -n <ns> -l app.kubernetes.io/instance=<device>
```

---

## 12. Putting it together — a fleet health dashboard in one shell

```bash
#!/usr/bin/env bash
# Print a one-screen rollup of every IOSXEConfig in every namespace.

kubectl get iosxe -A -o json | jq -r '
  .items[] |
  [
    .metadata.namespace,
    .metadata.name,
    .spec.deviceRef.name,
    .status.phase // "Pending",
    (.spec.managedFamilies | join(",")),
    (.status.familyStatus // [] | map(select(.state != "InSync")) | length),
    .status.lastAppliedTime // "—"
  ] | @tsv
' | column -t -s $'\t' -N "NS,NAME,DEVICE,PHASE,FAMILIES,DRIFT,LAST-APPLY"
```

Sample output:
```
NS                NAME                          DEVICE         PHASE     FAMILIES                       DRIFT  LAST-APPLY
cisco-vk-smoke    test-06-driftpolicy-revert    cat9k-smoke    InSync    banner                         0      2026-04-27T18:53:40Z
cisco-vk-smoke    test-07-write-startup         cat9k-smoke    InSync    interface_loopback             0      2026-04-27T18:53:40Z
cisco-vk-prod     access-rollout                cat9k-access1  InSync    vlan,vrf,interface_ethernet    0      2026-04-27T19:01:12Z
cisco-vk-prod     access-rollout                cat9k-access2  Drifted   vlan,vrf,interface_ethernet    1      2026-04-27T18:55:04Z
```

---

## 13. Roadmap — operator-CLI enrichments

The current `kubectl` surface covers the basics: phase, drift policy, age, member counts. The following enrichments would materially improve operator UX with relatively small schema churn, and are deliberately ordered by impact-per-effort.

### 13.1 Tier-1 — printer-column expansion (low-effort, high-impact)

Each requires only a `+kubebuilder:printcolumn` marker plus a CRD regenerate.

| CRD | New column | JSONPath | Why |
|---|---|---|---|
| `IOSXEConfig` | `TRANSPORT` | `.status.activeTransport` (new field) | Today the transport actually used (after deferred-dial settles) is invisible; metrics carry it but `kubectl get` doesn't |
| `IOSXEConfig` | `LAST-APPLY` | `.status.lastAppliedTime` | One of the most asked-for fields when triaging "did this land?"; today it requires `kubectl describe` |
| `IOSXEConfig` | `DRIFT-COUNT` | `len(.status.drift)` (would need controller to set a scalar `status.driftCount`) | Operators want to see "is there drift" at a glance |
| `IOSXEConfigBundle` | `READY/TOTAL` | `<count InSync>/<status.memberDevices>` (needs new aggregate field) | Selector fan-out today shows only device count; rollup of children's phases is missing |
| `IOSXEConfigDefaults` | `GENERATION` | `.metadata.generation` | Defaults churn breaks every CR's hash short-circuit; surfacing generation aids triage |
| `IOSXEConfigApplyLog` | `LAST-PHASE` | `.status.entries[-1].phase` | Today only entry count + truncation count; most-recent outcome is more useful |
| `CiscoDevice` | `TRANSPORT` | `.spec.transport` | At-a-glance: which transport is configured |

### 13.2 Tier-2 — status-condition normalisation

Today only `IOSXEConfig` and `CiscoDevice` populate `status.conditions[]` consistently. The other config CRDs declare the field but rarely populate it. Standardise on:

- **`Ready` (every CRD)** — Status=True with reason=Succeeded once the CR has been validated AND its dependents (templates, defaults, secrets) have resolved.
- **`Healthy-<scope>`** — Status=True iff every member (device / interface / referencer) is in a clean state.

This makes `kubectl get <kind> -o jsonpath='{.items[?(@.status.conditions[?(@.type=="Ready")].status=="True")].metadata.name}'` work uniformly across CRDs.

### 13.3 Tier-2 — bundle health rollup

`IOSXEConfigBundle.status` should carry a synthetic `summary` block:
```yaml
status:
  summary:
    inSync: 11
    drifted: 1
    failed: 0
    leaseBlocked: 0
    paused: 0
  conditions:
  - type: AllChildrenReady
    status: "False"
    reason: ChildDrifted
    message: "1/12 child IOSXEConfigs drifted: cisco-vk-prod/access-rollout (cat9k-access2)"
```

So a `kubectl get iosxebundle` printer column can show `READY=11/12` directly, and dashboards have a single field to alert on.

### 13.4 Tier-2 — lease-holder identity in conditions

When `status.phase: LeaseBlocked`, today the operator must list `kubectl get leases` and correlate. Surface the holder identity directly on `status.conditions[?type=="Conflict"]`:
```yaml
- type: Conflict
  status: "True"
  reason: LeaseHeldByForeignCR
  message: 'family "vlan" lease held by namespace=cisco-vk-prod cr=access-rollout pod=cat9k-prod-vk-...'
```

### 13.5 Tier-2 — drift-cause classification on events

Today every drift event has `reason: DriftDetected`. Enrich to:
- `DriftAfterManualCLI` when a CLI-induced change is detected (heuristic: drift on a leaf the engine recently wrote)
- `DriftFromUpstream` when defaults / template / device-group changed and the per-device intent rolled
- `DriftDetected` (current behaviour) for the unclassified case

Lets `kubectl get events --field-selector reason=DriftAfterManualCLI` find every device where someone bypassed the operator.

### 13.6 Tier-3 — `kubectl ciscovk` plugin

A standalone plugin (`kubectl-ciscovk`) that builds on top of `client-go` to surface domain-aware views:

- `kubectl ciscovk diff <iosxe>` — compute and render the netascode-shape diff between desired (resolved intent) and observed device state
- `kubectl ciscovk explain <family>` — show the netascode field reference for a family
- `kubectl ciscovk replay <iosxe>` — interactive picker over `IOSXEConfigApplyLog.entries[]`, applies the chosen replay annotation
- `kubectl ciscovk health` — fleet-wide rollup combining IOSXEConfig phases, bundle summaries, and pod readiness
- `kubectl ciscovk exec <device> -- show ...` — ✅ **delivered** (Phases A–D of the diagnostics RFC, validated against Cat9300 / IOS-XE 17.18.01 on 2026-04-28). Runs IOS-XE show commands via SSH-CLI on the per-device kubelet pod's admin endpoint (port-forward gated by `pods/portforward` RBAC). See [`./diagnostics-guide.md`](./diagnostics-guide.md) for the operator-facing setup and examples; [`./diagnostics-rfc.md`](./diagnostics-rfc.md) §11 for the architectural rationale.

Plugin architecture: discoverable via `kubectl plugin list`; ships with the cisco-virtual-kubelet release artifact; can be Homebrew'd / krew'd separately.

### 13.7 Tier-3 — field selectors

Today `kubectl get iosxe --field-selector status.phase=Drifted` doesn't work because the API server doesn't index that field. Adding `--feature-gates=...` or a controller-side cache index would let operators avoid JSONPath gymnastics for the most common queries.

### 13.8 Tier-3 — compact apply-log printer

`IOSXEConfigApplyLog.entries[]` is human-readable today only as full YAML. Ship a `kubectl ciscovk applylog <device>` (plugin) or a custom printer that renders:
```
TIME                  PHASE     OPS  SOURCE-CR
2026-04-27T18:53:40Z  InSync    1    cisco-vk-smoke/test-07-write-startup@1
2026-04-27T18:31:37Z  Failed    0    cisco-vk-smoke/test-06-driftpolicy-revert@2  (drift persisted after revert)
2026-04-27T18:21:00Z  Drifted   0    cisco-vk-smoke/test-06-driftpolicy-revert@2
```

### 13.9 Tier-3 — body-retention in status (gated)

`IOSXEConfigApplyLog.spec.retainBody=true` already exists but is off by default (entries don't carry `body`). Operators wanting blameable audit need it on for compliance. Roadmap: a per-namespace `IOSXEConfigDefaults`-equivalent that makes retention default-on for production namespaces.

### 13.10 Effort vs impact summary

| Tier | Items | Effort | Audience benefit |
|---|---|---|---|
| 1 | §13.1 | A few hours per CRD; CRD regenerate | Every operator, every day |
| 2 | §13.2–§13.5 | 2–5 engineer-days each | Triage + dashboarding |
| 3 | §13.6–§13.9 | Multi-week (plugin); 1–2 weeks for indexing | Power users + audit |

The Tier-1 enrichments would be the natural next PR after this branch merges.

---

## See also

- [`./deployment-modes.md`](./deployment-modes.md) — operator-facing setup guide per transport mode
- [`./transport-architecture.md`](./transport-architecture.md) — maintainer-facing architecture reference
- [`./final/release-blocker-tests/RUNBOOK.md`](./final/release-blocker-tests/RUNBOOK.md) — live-device retest playbook
- Live-device validation outputs live in the source-branch git history.
