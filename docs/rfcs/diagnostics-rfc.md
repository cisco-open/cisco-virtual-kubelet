# RFC — Diagnostic show-command surface for cisco-vk

**Status:** proposal, not yet implemented
**Author:** carry-over from the operator-CLI guide roadmap (§13.6) — promoted to a dedicated RFC because this changes the CRD surface
**Audience:** maintainers and operators
**Branch context:** `pr/johalley/ciscoconfig_xe`

---

## 1. Motivation

Operators today run `show running-config`, `show ip route`, `show ip ospf neighbor`, `show version`, and similar commands directly against IOS-XE devices to triage routing, hardware, and software state. None of this is reachable through cisco-virtual-kubelet today. The configuration driver is **write-only for free-form CLI** (via `VerbCLI` over Cisco-IA's `cli-config-data` RPC) and **read-only for YANG-modeled state** (via NETCONF `<get>` / RESTCONF `/restconf/data/...-oper`). Neither path returns raw show-command text.

The gap matters because:

- During incidents, operators want a `kubectl`-native one-liner (no separate SSH session, no separate credential lookup).
- GitOps audits want repeatable, declarative diagnostic snapshots: "for every device labeled `role=core`, capture `show ip ospf neighbor` once an hour and retain for 7 days."
- The kubelet-shaped `kubectl exec ciscodevice/...` UX is a natural fit for ad-hoc commands but doesn't exist.

This RFC scopes the surface, not the implementation timeline.

---

## 2. Current state — what's there, what's missing

### 2.1 What's there

| Mechanism | Direction | What it covers | What it doesn't |
|---|---|---|---|
| `VerbCLI` over Cisco-IA `cli-config-data` ([`netconf.go:507`](../../internal/drivers/iosxe/configdriver/transport/netconf.go#L507), [`restconf.go:156`](../../internal/drivers/iosxe/configdriver/transport/restconf.go#L156)) | **Write** | Push CLI config lines (`interface ...`, `ip route ...`) | No way to read; returns no payload |
| NETCONF `<get>` with subtree filter ([`fetchFromSource`](../../internal/drivers/iosxe/configdriver/transport/netconf.go#L182)) | **Read** | YANG-modeled operational state (`Cisco-IOS-XE-isis-oper:isis-oper-data`, etc.) | Returns RFC 7951 JSON — not the textual show output operators are used to; no coverage for state Cisco hasn't modeled |
| RESTCONF GET on `*-oper` modules | **Read** | Same as above | Same caveat |

### 2.2 What's missing

- A read path equivalent to `VerbCLI` that invokes **Cisco-IA's `cli-exec` RPC** (or `cli-oper-data`) and returns text. The transport layer doesn't currently expose this RPC.
- A CRD or kubectl-extension surface to **drive** the request and **surface** the result.
- A model for **output retention** that respects the etcd 1 MB per-object limit while supporting multi-MB show outputs (`show running-config` on a busy box).
- An **authorization model** distinct from configuration writes — operators with read-only diagnostic rights shouldn't need RBAC for `IOSXEConfig`.

---

## 3. Proposal — three layers, smallest first

### 3.1 Layer 1 — transport extension (foundational)

Extend `transport.Interface` with an optional capability:

```go
// DiagnosticExec runs a list of IOS-XE operational ("show") commands
// and returns their textual output. Implements the Cisco-IA cli-exec
// (NETCONF) / cli-oper-data (RESTCONF) RPC.
//
// Optional — implemented by RESTCONF and NETCONF; gNMI returns
// ErrUnsupported (no equivalent in current Cisco gNMI surface).
type DiagnosticExecer interface {
    DiagnosticExec(ctx context.Context, commands []string) ([]CommandResult, error)
}

type CommandResult struct {
    Command string  // e.g. "show ip route"
    Output  string  // raw text the device returned
    Err     string  // empty on success
}
```

Implementation:

- **NETCONF:** wrap commands in `<cli-exec xmlns="http://cisco.com/yang/cisco-ia"><cmd>show ip route</cmd></cli-exec>`. Reply `<data>` carries the text. ~30 lines parallel to the existing `pushCLI` ([`netconf.go:510`](../../internal/drivers/iosxe/configdriver/transport/netconf.go#L510)).
- **RESTCONF:** POST to `/restconf/operations/cisco-ia:cli-exec` with `{"cisco-ia:input": {"cli-exec-data": {"cmd": ["show ip route"]}}}`. Mirror of `pushCLI` in [`restconf.go:156`](../../internal/drivers/iosxe/configdriver/transport/restconf.go#L156).
- **gNMI:** return `ErrUnsupported`. Cisco's current gNMI surface has no equivalent; if/when they ship one, the interface absorbs it.

This layer is **the load-bearing piece**. Everything in §3.2 and §3.3 is a consumer.

### 3.2 Layer 2 — declarative diagnostics CRD (`IOSXEDiagnostic`)

For repeatable, GitOps-tracked, scheduled diagnostic captures.

**Group/Version/Kind:** `config.cisco.vk/v1alpha1`, `IOSXEDiagnostic`. Namespaced.

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: ospf-neighbors-snapshot
  namespace: cisco-vk-smoke
spec:
  deviceRef:
    name: cat9k-smoke
  commands:
    - show version
    - show ip ospf neighbor
    - show ip route summary
  schedule:                          # optional — cron-style
    interval: 1h                     # OR cron: "0 * * * *"
  retention:
    maxResults: 24                   # last 24 captures (one per spec.schedule.interval)
    truncateAt: 64KiB                # per-command output cap; longer lines move to ConfigMap
  outputSink:
    inline: true                     # populate status.results inline (default)
    # configMapRef:                  # alternative: write to ConfigMap when above truncateAt
    #   namePrefix: ospf-snapshot-
    #   namespace: cisco-vk-smoke
status:
  observedGeneration: 1
  lastCapture: "2026-04-28T13:00:04Z"
  nextCapture:  "2026-04-28T14:00:00Z"
  results:
    - capturedAt: "2026-04-28T13:00:04Z"
      commands:
        - command: "show version"
          output: |
            Cisco IOS XE Software, Version 17.18.2
            ...
          truncated: false
        - command: "show ip ospf neighbor"
          output: |
            Neighbor ID  Pri  State    Dead Time  Address
            10.0.0.2     1    FULL/DR  00:00:38   10.1.1.2
          truncated: false
        - command: "show ip route summary"
          output: "..."
          truncated: false
  conditions:
    - type: Ready
      status: "True"
      reason: Captured
      lastTransitionTime: "2026-04-28T13:00:04Z"
```

#### printer columns

| Column | JSONPath |
|---|---|
| `DEVICE` | `.spec.deviceRef.name` |
| `COMMANDS` | `.spec.commands[*]` count |
| `LAST` | `.status.lastCapture` |
| `NEXT` | `.status.nextCapture` |
| `AGE` | `.metadata.creationTimestamp` |

#### controller behavior

- Reuses the existing per-device-pod's transport (no new dial — the configdriver's `transportSlot atomic.Pointer` is already shared across reconcilers).
- One-shot mode (no `spec.schedule`): execute once on Create and on every spec generation bump; subsequent reconciles hash-short-circuit.
- Scheduled mode: requeue at `spec.schedule.interval` cadence; respects cluster-side leases for fairness.
- Per-command failure is non-fatal — populates `commands[].err`, marks the result `Ready=False reason=PartialFailure` only if every command failed.

#### output sink

`outputSink.inline` (default) writes results into `status.results[]`. With multi-MB outputs (`show running-config`), inline blows past etcd's 1 MB per-object limit. Two mitigations:

- **Per-line truncation** at `spec.retention.truncateAt` (e.g. 64 KiB per command), with `truncated: true` flagged when applied.
- **ConfigMap sink** when `outputSink.configMapRef` is set: each capture lands in a fresh ConfigMap (one per `capturedAt`), `status.results[].configMapRef` references it, retention deletes the oldest when count exceeds `spec.retention.maxResults`.

#### RBAC

`IOSXEDiagnostic` lives in its own RBAC verb scope. A new ClusterRole `cisco-vk-diagnostic-reader`:
- `get / list / watch` on `iosxediagnostics`
- `get / list / watch` on `configmaps` whose label selector matches `cisco.vk/diagnostic-output: true`

Operators with this role can read all diagnostic captures without seeing secrets, ConfigMaps with intent, or `IOSXEConfig` CRs.

### 3.3 Layer 3 — `kubectl ciscovk exec` plugin (ad-hoc)

For interactive triage where waiting for a controller-driven CRD is too slow.

```bash
$ kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke -- show ip ospf neighbor
Neighbor ID  Pri  State    Dead Time  Address
10.0.0.2     1    FULL/DR  00:00:38   10.1.1.2

$ kubectl ciscovk exec cat9k-smoke -- "show running-config | section interface" > running-snapshot.txt
```

#### Plugin architecture

The plugin is a separate binary (`kubectl-ciscovk`) shipped alongside the cisco-vk release. Discovery: `kubectl plugin list` finds it on PATH.

Two implementation options, in increasing-complexity order:

**Option A — port-forward + plugin-side gRPC.** Plugin opens a `kubectl port-forward` to the per-device pod, calls a new gRPC endpoint on the pod's metrics/admin server (`:8082`?), receives streamed output, prints to stdout. Pro: no Kubernetes API-server changes; works on any cluster. Con: requires `port-forward` RBAC.

**Option B — APIService aggregation (`/apis/diag.cisco.vk/v1alpha1/.../exec`).** Cisco-vk runs an extension API server registered via `APIService`; `kubectl exec`-shaped routes pass through to the per-device pod. Pro: native `kubectl exec` UX, integrates with cluster RBAC. Con: APIService aggregation adds operational complexity (extra controller, extra TLS material, new failure mode).

Recommendation: ship Option A first (simpler delivery), evaluate Option B once the diagnostic surface has real-world usage data.

### 3.4 Layer 3 alternative — virtual-kubelet exec passthrough

Because the per-device pod IS a virtual kubelet that exposes a kubelet API, one could imagine `kubectl exec ciscodevice/cat9k-smoke -- show ip route` working through the same code path that `kubectl exec` uses for normal Pods. This is more elegant but requires the virtual-kubelet `RunInContainer` interface to map to `cli-exec` instead of `app-hosting iox connect`. Tracked as a stretch goal — we'd want to avoid surprising operators who run `kubectl exec` expecting an IOx app shell.

---

## 4. Interaction with the existing CRDs

| Existing CRD | Interaction |
|---|---|
| `IOSXEConfig` | Independent. Diagnostics never write config; no lease overlap. |
| `IOSXEConfigBundle` | Out of scope — bundles are for write fan-out. A future `IOSXEDiagnosticBundle` could mirror the pattern (selector-driven snapshot capture) but is deliberately not in this RFC. |
| `IOSXEConfigApplyLog` | Diagnostics are a separate audit stream — an apply log records what we wrote; a diagnostic captures what's there. They serve different audit needs and shouldn't be merged. |
| `CiscoDevice` | Diagnostics target a CiscoDevice via `spec.deviceRef`. No new fields on CiscoDevice itself. |

---

## 5. Risk inventory

| Risk | Mitigation |
|---|---|
| `cli-exec` rate limits on the device | Coalesce concurrent diagnostic captures per-device; configurable per-device cap; warn on rate-limit responses |
| Multi-MB show output blows past etcd limits | Per-command truncation + opt-in ConfigMap sink; aggressive default `truncateAt: 64KiB` |
| RBAC creep — operators want diagnostics but not configs | Distinct CRD + dedicated ClusterRole; explicit aggregation rule |
| Show output contains sensitive data (passwords in `show running-config`) | Default truncation strips lines matching `r"^enable secret\\b\|^username \\S+ secret\\b\|^snmp-server community\\b\|^tacacs-server key\\b\|^radius-server key\\b\|^ip ftp password\\b\|^crypto ipsec.*key\\b"`; opt-in `spec.allowSecrets: true` to disable. |
| CRD spec drift between releases | Start in `v1alpha1` like the others; promote alongside `IOSXEConfig` when CRD `v1` cut happens |

---

## 6. Phasing — minimum-viable rollout

1. **Phase A — transport layer** (1 PR, ~200 LoC).
   - Add `DiagnosticExecer` to `transport.Interface`.
   - Implement on RESTCONF + NETCONF; mock for tests.
   - Unit tests with sample show-output fixtures.

2. **Phase B — `IOSXEDiagnostic` CRD** (1 PR, ~600 LoC).
   - API types + CRD generate.
   - Reconciler in [`internal/provider/`](../../internal/provider/) that reuses the per-device-pod transport.
   - Inline `status.results` only (no ConfigMap sink yet); deliberate scope cut.
   - Default secret-redaction filter.
   - 5–8 envtest cases covering: one-shot capture, scheduled capture, partial failure, truncation, RBAC.

3. **Phase C — `kubectl ciscovk exec` plugin (Option A)** (1 PR, ~400 LoC).
   - Standalone binary in [`tools/kubectl-ciscovk/`](../../tools/).
   - Port-forward + gRPC streaming to a new admin endpoint on the per-device pod.
   - Unit tests against a fake pod; manual e2e against the lab device.

4. **Phase D — ConfigMap output sink** (1 PR, ~200 LoC) — closes the multi-MB output story.

5. **Phase E — APIService aggregation (Option B)** — only if real-world usage proves the plugin's port-forward UX insufficient.

Phases A + B alone deliver the headline feature ("declare a diagnostic, get show output via `kubectl get iosxediagnostic`"). Phase C adds the interactive UX. Phase D closes the only meaningful gap (large outputs). Phase E is optional.

---

## 7. Examples

### 7.1 One-shot diagnostic from kubectl

```yaml
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: incident-123-snapshot
  namespace: cisco-vk-smoke
spec:
  deviceRef: { name: cat9k-smoke }
  commands:
    - show version
    - show ip route
    - show ip ospf neighbor
    - show running-config | section interface
  retention: { maxResults: 1, truncateAt: 1MiB }
```

```bash
$ kubectl apply -f incident-snapshot.yaml
iosxediagnostic.config.cisco.vk/incident-123-snapshot created

$ kubectl get iosxediag -n cisco-vk-smoke
NAME                        DEVICE         COMMANDS   LAST                    NEXT   AGE
incident-123-snapshot       cat9k-smoke    4          2026-04-28T13:00:04Z    —      8s

$ kubectl get iosxediag incident-123-snapshot -n cisco-vk-smoke \
  -o jsonpath='{.status.results[-1:].commands[?(@.command=="show ip ospf neighbor")].output}'
Neighbor ID  Pri  State    Dead Time  Address
10.0.0.2     1    FULL/DR  00:00:38   10.1.1.2
```

### 7.2 Scheduled fleet snapshot

```yaml
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: ospf-hourly
  namespace: cisco-vk-smoke
spec:
  deviceRef: { name: cat9k-smoke }
  commands: [ "show ip ospf neighbor" ]
  schedule: { interval: 1h }
  retention: { maxResults: 168, truncateAt: 64KiB }    # 7 days
```

Operators query history via `kubectl get iosxediag ospf-hourly -o yaml` or via a future `kubectl ciscovk applylog`-style compact printer.

### 7.3 Plugin one-shot

```bash
$ kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke -- show ip route 10.0.0.0/8 | head -5
Routing entry for 10.0.0.0/8
  Known via "ospf 1", distance 110, metric 11, type inter area
  Last update from 10.1.1.2 on GigabitEthernet1/0/1, 1d22h ago
  Routing Descriptor Blocks:
    10.1.1.2, from 192.168.10.1, 1d22h ago, via GigabitEthernet1/0/1
```

### 7.4 ConfigMap sink for `show running-config`

```yaml
spec:
  deviceRef: { name: cat9k-smoke }
  commands: [ "show running-config" ]
  outputSink:
    configMapRef:
      namePrefix: running-snapshot-
      namespace: cisco-vk-smoke
```

Each capture writes a new ConfigMap `running-snapshot-<timestamp>` whose `data["show-running-config"]` holds the output. `status.results[].configMapRef` lists them; retention drops the oldest beyond `spec.retention.maxResults`.

---

## 8. What this RFC does NOT propose

- **Destructive commands** — `clear ip ospf process`, `reload`, `write erase`, etc. — are **a separate RFC** with their own RBAC tiers, two-person rule, maintenance window, and tamper-evident audit chain. See [`./device-operations-rfc.md`](./device-operations-rfc.md) for the design. The transport-layer extension this RFC introduces (`DiagnosticExecer`) generalises to `OperationalExecer` once that work lands; both reuse the same Cisco-IA `cli-exec` RPC.
- **A general OS-shell.** `kubectl ciscovk exec` (this RFC) is **show-only** — it invokes `cli-exec` (operational reads), not `cli-config-data` (configuration writes). The destructive-ops RFC adds a separate, RBAC-gated path for state-changing commands.
- **A streaming subscription model.** Diagnostic captures are point-in-time. Telemetry / drift signals stay on gNMI Subscribe.
- **Cross-device aggregation.** Out of scope for the first cut. A future `IOSXEDiagnosticBundle` can fan out the same way `IOSXEConfigBundle` does.

---

## 9. Decision

**Status:** RFC is open. Recommended path forward: implement Phases A + B as the next post-merge PR after this branch. Phase C lands as a follow-up; Phases D + E are gated on usage data.

The transport-layer extension (Phase A) is small enough that operators wanting a private Phase B implementation could fork it cleanly. Shipping Phase A unconditionally is therefore low-risk and high-leverage.

---

## See also

- [`./operator-cli-guide.md`](./operator-cli-guide.md) — the source for §13.6 ("`kubectl ciscovk` plugin"), which this RFC promotes to a full design
- [`./device-operations-rfc.md`](./device-operations-rfc.md) — the destructive-ops sibling: RBAC-tiered `IOSXEMaintenance` (clears) and `IOSXEDeviceOp` (reload, write-erase) with two-person approval, maintenance windows, and cryptographically chained audit
- [`./transport-architecture.md`](./transport-architecture.md) §11 — the apphosting / configdriver split that scopes which transport this RFC's Phase A binds to
- [`./driver-extension-guide.md`](./driver-extension-guide.md) — for vendor-driver authors who'd implement `DiagnosticExecer` against NX-OS / IOS-XR / OpenConfig
