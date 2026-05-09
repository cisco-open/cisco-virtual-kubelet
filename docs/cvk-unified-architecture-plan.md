<!--
Copyright 2026 Cisco Systems Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
-->

# Cisco Virtual Kubelet: Unified Architecture Plan

**Author note:** this is a synthesis pass over (a) the upstream Virtual Kubelet contract, (b) OpenTelemetry semantic conventions and APM best practices, (c) the existing cisco-vk codebase, and (d) the future scope the user named — config functions, operations plugin, lifecycle management, rollback, and software upgrades for routers/switches.

It supersedes (in narrative, not in fact) the earlier branch-scoped docs:
- `docs/telemetry-branch-review.md` (current state of MDT-over-gNMI)
- `docs/telemetry-trace-convergence-roadmap.md` (six-phase trace plan — re-shaped here)

---

## 1. Executive summary

Cisco Virtual Kubelet treats network devices (Catalysts, ASR, NCS) as Kubernetes nodes that host containers via app-hosting. The codebase has matured through five telemetry phases, has a working transactional config engine (NETCONF candidate + RFC 6241 confirmed-commit), and has placeholder drivers for IOS-XR / NX-OS. The MDT-over-gNMI pipeline shipping on `pr/johalley/mdt-gnmi-full` (commit `d7a61f8`) is verified end-to-end against `cat9k-smoke`.

**The opportunity now is not more features — it's coherence.** Five surfaces (provisioning, telemetry, topology, configuration, lifecycle) are evolving independently. Future scope (rollback, upgrade, ops plugin) will calcify those silos unless we lock down a unifying spine. That spine is **OpenTelemetry as the cross-cutting observability and correlation plane**, and **the Virtual Kubelet sibling-CRD pattern as the cross-cutting reconciliation model**.

**Headline recommendations:**

1. **Adopt the upstream `trace.T` adapter pattern** instead of calling OTel directly. cisco-vk already imports `virtual-kubelet/trace`; flipping the trace plane on means every upstream `CreatePod` / `DeletePod` span becomes a parent for our driver work, with zero LoC added in the upstream paths.
2. **Use the upstream `PodNotifier` callback as the bridge between MDT and Pod status.** Today MDT updates a CR status; the upstream pod controller never hears about it without polling. Wiring `PodNotifier` is a one-page change that closes the "device says container is RUNNING → kubectl shows Pod Running" loop.
3. **Reshape the trace convergence roadmap per Codex's peer review:** drop the long-running `cvk.device.heartbeat` span, drop the four-annotation propagation in favor of W3C `traceparent` + expiry, and ship a minimum-viable causal trace BEFORE topology + correlation cache. Per-Codex MVS is 1–2 days, not the 12–14 day six-phase plan.
4. **Future scope is sibling CRDs**, never extensions to `PodLifecycleHandler`. Liqo, Admiralty, AKS, Fargate all do this. Roll out `DeviceConfigRevision`, `DeviceOperation`, `DeviceUpgrade` as separate CRDs with their own reconcilers in the same binary, sharing `service.name=cisco-vk-controller` and the same OTel pipeline.
5. **Reuse the existing transactional engine for rollback.** NETCONF candidate + confirmed-commit + the per-Apply `RollbackToken` are already there. Don't build a separate rollback machinery; surface the existing one through a `DeviceConfigRevision` history CRD.
6. **MDT can supplement but not replace RESTCONF for topology.** Hybrid: MDT-fed cache for interface/app-hosting/BGP-OSPF state; RESTCONF one-shot for identity (hostname, serial, version) and for any YANG path the device doesn't actually stream. Empirically validate four specific gNMI paths against `cat9k-smoke` before committing the architecture (commands listed in §10).

**What this plan does NOT do:** introduce a new control plane (no Crossplane composition layer, no operator-framework rewrite, no separate management cluster). Everything fits in cisco-vk's existing controller-runtime topology.

---

## 2. Foundations

### 2.1 Where the codebase stands today

```
                                 ┌────────────────────────────┐
                                 │ kube-apiserver             │
                                 └────────┬───────────────────┘
                                          │
       ┌──────────────────────────────────┴──────────────────────────────┐
       │                                                                  │
   ┌───▼────────────────────────────┐                ┌───────────────────▼─────────┐
   │ system controller (cisco-vk-system)              │ per-device pod (cisco-vk-<dev>)
   │   - CiscoDeviceReconciler                        │   - VirtualKubelet provider
   │   - IOSXEConfigReconciler (or aggregated)        │   - PodLifecycleHandler impl
   │   - IOSXEConfigBundleReconciler                  │   - IOSXETelemetryReconciler
   │   - IOSXETelemetryReconciler                     │   - gNMI Subscribe / mapper /
   │   - aggregator/ (single-mgr mode)                │     emitters / OTel SDK
   │   - Admin server                                 │   - configdriver intent→engine→
   │                                                   │     transport→writers
   └─────────────────────────────────┘                └──────────────────────────────┘
```

**Working surfaces:**
- VK provider: `internal/provider/provider.go:85-178` (full `PodLifecycleHandler`)
- Driver registry pattern with placeholders: `internal/drivers/registry.go:66-75`
- Configuration engine state machine (Pending → Validating → Planning → Applying → Verifying → InSync|Failed|Drifted): `internal/drivers/iosxe/configdriver/engine.go:141-591`
- Transactional capabilities: `transport.Capabilities{SupportsTransactions, SupportsConfirmedCommit, ...}` at `internal/drivers/iosxe/configdriver/transport.go:33-104`
- NETCONF candidate + commit-confirmed (RFC 6241 §8.4): `internal/drivers/iosxe/configdriver/transport/netconf.go:399-454`
- RESTCONF best-effort PreImage rollback: `transport.go:256-299`
- Atomic-replace owned-key tracking: in `IOSXEConfig.status.atomicReplaceOwnedKeys`
- Telemetry pipeline + per-device OTel SDK: shipped at `d7a61f8`
- Adminserver `POST /v1/exec`: `internal/provider/diagnostic/adminserver/server.go:1-80`

**Gaps that block future scope:**
- No `PodNotifier` wiring — async device→k8s pod-status push is missing.
- No `trace.T` adapter — every reconciler that wants a span has to call OTel directly, breaking auto-nesting under upstream's existing reconcile spans.
- No system-controller OTel provider — only per-device pods emit telemetry today.
- No revision history for `IOSXEConfig` — engine has rollback tokens but they're transient (per-Apply); no durable "last good revision" record.
- No `DeviceOperation` / `DeviceUpgrade` CRDs.
- IOS-XR / NX-OS drivers are placeholders (`internal/drivers/iosxr/register.go`, `nxos/register.go`).

### 2.2 Upstream Virtual Kubelet contract — what we should NOT fight

Upstream VK is intentionally minimal. The contract:
- `PodLifecycleHandler` (6 methods): `CreatePod`, `UpdatePod`, `DeletePod`, `GetPod`, `GetPods`, `GetPodStatus`. Idempotent; errors should implement `errdefs` so retry vs terminal is unambiguous.
- `PodNotifier`: async push of pod-status changes back to the controller.
- `NodeProvider`: `Ping` + `NotifyNodeStatus`. Use `WithNodeEnableLeaseV1` (coordination/v1 Lease) — it's the upstream-recommended replacement for `Ping`-based heartbeat at scale.
- `nodeutil.Provider` interface adds `GetContainerLogs`, `RunInContainer`, `AttachToContainer`, `GetStatsSummary`, `GetMetricsResource`, `PortForward` — the kubelet HTTP API surface. Optional, return 501 if unimplemented.
- `trace.T` is a thin VK-specific abstraction with an OTel adapter (`virtual-kubelet/trace/opentelemetry`). Set it once at `main`; every upstream span auto-nests OTel.

**Liqo, Admiralty, Azure ACI, AWS Fargate** all keep VK strictly to Pod CRUD + node lifecycle. Anything else (cluster peering, image upgrades, ad-hoc operations) is a **sibling CRD** reconciled by another controller-runtime controller in the same binary. **There is no VK extension point for non-Pod CRDs and we should not invent one.**

### 2.3 OpenTelemetry — what to honor

- **Resource attributes are identity.** A signal carries the resource it came from. `service.name`, `service.instance.id`, `host.name`, `k8s.pod.name`, `k8s.namespace.name`, `k8s.node.name`, `net.peer.name` are the join keys across Tempo / Loki / Prometheus / Mimir.
- **Network semantics.** `network.peer.address`, `network.protocol.name=gnmi|netconf|restconf`, `server.address`, `server.port` for transport spans.
- **Spans are causal.** Open one per logical operation (`cvk.config.push`, `cvk.config.intent`). Don't open long-running open-ended parents — they break Tempo's lifecycle.
- **Links are acausal evidence.** Topology snapshots, post-hoc correlation, fan-out-to-many.
- **Baggage is context propagation.** Low-cardinality (e.g. `app.id`, `pod.uid`). NEVER for high-cardinality data.
- **Sampling is a policy.** `ParentBased(AlwaysSample)` is fine for early rollout. Tail sampling via the Collector for production.
- **Exemplars are bridges.** Trace IDs on metric data points let Grafana click-through. Cardinality-bounded by SDK; receiver's documented 190 MB/label-set risk was an older reservoir behavior.
- **Native histograms** are GrafanaCon's recommendation for distribution data. Bounded cardinality, instant percentiles. Cisco-vk uses gauges/sums today.
- **OpAMP** is the OTel control-plane spec for managing collectors/agents at scale. Probably out of scope for cisco-vk v1, but worth knowing.

---

## 3. The OpenTelemetry spine

This section is the load-bearing change for everything else.

### 3.1 Trace plane: VK adapter, not direct OTel

**Today:** `internal/telemetry/emit/traces.go` calls `tracer.Start` directly. None of the reconcilers do.

**Target:**
```go
// cmd/cisco-vk/main.go (once at startup, before any reconciler runs)
import vktrace "github.com/virtual-kubelet/virtual-kubelet/trace"
import vkotel "github.com/virtual-kubelet/virtual-kubelet/trace/opentelemetry"

vktrace.T = vkotel.Adapter{} // every upstream span now flows through OTel
otel.SetTracerProvider(providers.Tracer)
```

Then reconcilers and drivers use upstream's helper:
```go
ctx, span := vktrace.StartSpan(ctx, "cvk.config.reconcile")
defer span.End()
```

**Why this matters for everything else:** upstream VK already opens spans around `CreatePod` / `UpdatePod` / `DeletePod` / `GetPodStatus` / `syncPodStatusFromProvider` / informer `AddFunc`/`UpdateFunc`. As soon as `vktrace.T = vkotel.Adapter{}`, those existing spans become parents of any driver span you open inside them. **Phase A of the trace convergence roadmap collapses by 70%** — the operator-side hierarchy already exists; we just need to plug into it.

### 3.2 Resource attribute discipline

Every emitted signal — log, metric, span — carries a resource. Three process types in cisco-vk; each has a distinct identity:

| Process | `service.name` | `service.instance.id` | `cvk.process.role` |
|---|---|---|---|
| System controller (`cisco-vk-system`) | `cisco-vk-controller` | `<POD_UID>` | `controller` |
| Per-device VK pod (`cisco-vk-<device>-vk`) | `cisco-vk-vk` | `<POD_UID>` | `vk-provider` |
| Per-device telemetry pipeline (same pod, separate `service.name`) | `cisco-vk-telemetry` | `<POD_UID>:<device>` | `telemetry-emitter` |

Plus standard SemConv across all three:
- `host.name` = device name (when applicable; for system controller it's the actual cluster node)
- `net.peer.name` = device address (for VK pod and telemetry)
- `k8s.pod.name`, `k8s.namespace.name`, `k8s.node.name`, `k8s.pod.uid` (downward API — already wired in v1-blocker batch)
- `cvk.driver.kind` = `XE` | `XR` | `NXOS` | `OPENCONFIG` | `FAKE`

**Action:** finalize the `cvk.process.role` attribute introduced loosely in the convergence roadmap; codify it in `cmd/cisco-vk/telemetry_providers.go` so all three processes set it correctly.

### 3.3 Operation taxonomy

Every span follows a stable name pattern. Operators learn one shape and apply it everywhere.

| Surface | Span name pattern | Example |
|---|---|---|
| K8s reconcile entry | `cvk.<resource>.reconcile` | `cvk.iosxeconfig.reconcile`, `cvk.iosxetelemetry.reconcile` |
| Device-bound transport | `cvk.transport.<protocol>.<verb>` | `cvk.transport.netconf.commit`, `cvk.transport.gnmi.subscribe`, `cvk.transport.restconf.get` |
| Config engine phase | `cvk.config.<phase>` | `cvk.config.intent`, `cvk.config.plan`, `cvk.config.apply`, `cvk.config.verify` |
| MDT pipeline | `cvk.telemetry.<stage>` | `cvk.telemetry.map`, `cvk.telemetry.transition` |
| Device-side state event | `cvk.device.<eventclass>` | `cvk.device.app.transition`, `cvk.device.interface.transition` |
| Operator-driven non-Pod ops | `cvk.op.<kind>` | `cvk.op.exec`, `cvk.op.upgrade`, `cvk.op.rollback` |

This is not bureaucracy — it's the difference between Tempo's search panel showing 12 distinct services or showing one "everything" service.

### 3.4 Metric naming

Already settled by the v1-blocker batch: `cisco_vk_telemetry_*` for self-metrics; `cvk.<area>.<measure>` for device-derived metrics. Driver namespace prefix (`cvk.iosxe.iface.counters.in.octets`) is deferred to v1-follow-up but should land before a second driver lights up.

### 3.5 Logs

Today only the MDT pipeline emits OTel logs. The system controller and configdriver use logr. That's fine — logr can be wrapped via `go-logr/logr/funcr` to also emit OTel `LogRecord`s when an active span is in context (so the log line gets the trace ID). Doing this in the controller adds searchability: "give me every log line for trace `T`" — Loki + Tempo cross-joins.

**Action (future):** logr → OTel log bridge in the controller. ~50 LoC, deferred until trace foundation lands.

---

## 4. Reassessment of the trace convergence roadmap

Codex's peer review correctly flagged six issues. Below: what changes.

| Original phase | Issue | Revised |
|---|---|---|
| Phase A: 6 reconciler spans | "Long-running heartbeat span breaks Tempo lifecycle" was theoretical; the bigger issue is that operator spans were 70% existing-but-not-flipped-on. | **Replace with: install `vktrace.T = vkotel.Adapter{}` + 3-5 driver-layer spans (`cvk.config.intent`, `cvk.config.apply`, `cvk.transport.<verb>`).** Upstream VK spans for `CreatePod` etc. become parents automatically. Effort: 1 day, not 2-3. |
| Phase B: 4 annotation keys | Pod annotations are not a W3C carrier and the four-key shape was idiosyncratic. | **Use a single `cisco.vk/traceparent` annotation matching the W3C `traceparent` header format, plus `cisco.vk/trace-window-end` for staleness.** Strict parse + reject-on-malformed + sampled-flag preserved per W3C spec. |
| Phase C: long-running heartbeat span | Codex: "OTel SDK exports spans on End. A parent held open for hours has unbounded event lists, doesn't appear in Tempo until shutdown, and child spans arrive without a visible parent." Correct. | **Drop the heartbeat span entirely.** Topology cycles emit independent root spans with stable identity attributes (`cisco.device.name`, `topology.cycle.id`, `topology.cycle.epoch`). Grafana groups by attribute, not parent. |
| Phase D: pod-by-app-id index | Doc cited an index that doesn't exist. | **Add `internal/telemetry/correlation` package** with: traceparent parser, bounded LRU cache `(device, app_id) → SpanContext` with 15-min TTL and 4096 cap, and a per-CR pod-by-app-id resolver that uses the upstream Pod informer (already in the controller). |
| Phase E: "configuration only" | Codex: log records don't carry trace IDs unless the active context is set before emission. | **Add a small `correlation.WithSpan(ctx)` helper** that the MDT subscriber drainEvents wraps each emit-batch in. ~20 LoC; not "configuration only" but still small. |
| Phase F: status echo | OK as-is. | **Expand to include `cisco.vk/last-trace-id` AND `cisco.vk/last-trace-duration` AND `cisco.vk/last-error-trace-id` (set when a failed reconcile finishes).** That last one is the gold — operators paste failure trace IDs into Tempo and see the failed `cvk.transport.netconf.commit` span. |

**Net effect:** the original 6-phase, 12-14 day roadmap collapses to 4 phases, 6-8 days, with stronger guarantees because we're piggybacking on upstream's instrumentation rather than rebuilding it.

---

## 5. Future scope mapped onto VK-idiomatic shapes

### 5.1 Configuration functions

**What's there:** `IOSXEConfig` CRD with intent → engine → transport → writers. `IOSXEConfigBundle` for selector-based fan-out. Atomic-replace owned-key tracking.

**What's missing:** composable functions that take an intent and produce a richer intent. Crossplane's "compositions" pattern.

**Proposal:**
- New CRD `IOSXEConfigFunction` (cluster-scoped, like `IOSXETemplate`).
- Each function takes a fragment of intent (e.g. "configure VRF X with route-target Y", "enable BGP IPv4 neighbor"), validates it, and outputs the canonical YANG-keyed shape the writer consumes.
- Functions can be implemented as: (a) embedded Go code (compiled-in for performance-critical patterns), (b) CEL expressions (for ad-hoc operator-supplied logic), (c) WASM modules (for sealed third-party functions). Start with (a).
- `IOSXEConfig.spec` gains `functionRefs: []` analogous to existing `templateRefs`.
- Functions are **deterministic and pure** — same inputs produce same intent. The engine hashes input+function-version+output for short-circuit detection.

**OTel:** every function execution is a child span of `cvk.config.intent`: `cvk.config.function.<name>` with attributes `cvk.function.kind`, `cvk.function.input.size`, `cvk.function.output.size`, `cvk.function.duration_ms`.

**Effort:** medium. CRD + reconciler-side resolution + intent-resolver integration + tests. ~2-3 weeks.

### 5.2 Operations plugin

**What's there:** `POST /v1/exec` on adminserver, scoped to read-only show commands, gated by `pods/portforward` RBAC.

**What's missing:** a CRD-driven async operations model. Auditable. Idempotent. Cancelable.

**Proposal: `DeviceOperation` CRD** (Liqo `Resource` style):
```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata: {name: capture-001, namespace: ops}
spec:
  deviceRef: {name: cat9k-smoke}
  operation:
    kind: PacketCapture                  # PacketCapture | ShowCommand | Reload | ConfigDiff
    args:
      interface: TenGigE0/0/0/1
      filter: "host 10.1.1.1"
      duration: 30s
      output: cmpHTTPS://artifacts.example.com/captures/
  ttlSecondsAfterFinished: 3600
status:
  phase: Pending|Running|Succeeded|Failed|Cancelled
  startTime: ...
  completionTime: ...
  artifactURIs: [...]
  conditions: [...]
```

The reconciler:
- On `Pending` → opens `cvk.op.<kind>` span, calls driver-side `OpExecutor.<kind>`, transitions to `Running`.
- Streams driver output to a backing object (S3-compatible URI / k8s ConfigMap for small results).
- Sets `phase=Succeeded|Failed`, attaches artifacts, ends span.

**Why a CRD not a kubectl plugin:** auditability (k8s audit log captures every CR creation), retention (TTL controller cleans up), idempotency (same name = same op), cancellation (delete the CR), workflow integration (Argo, Tekton can fan out `DeviceOperation` CRs).

**Driver contract:** new interface `OpExecutor` co-located with `CiscoKubernetesDeviceDriver`. Each driver implements supported ops via a registration map; unsupported ops return `errdefs.NotImplemented`.

**Adminserver `/v1/exec` becomes a thin wrapper:** synthesizes a transient `DeviceOperation` CR, polls until done, returns result. Backward-compat preserved.

**Effort:** medium-large. New CRD package + reconciler + per-driver op registries + adminserver wrap. ~3-4 weeks.

### 5.3 Lifecycle management

**What's there:** `PodLifecycleHandler` 6 methods. Pod state transitions: INSTALLING → ACTIVE → TERMINATING. RunOpts labels for idempotency.

**What's missing:** `PodNotifier` wiring. Today the controller learns about device-side state changes only via reconcile-loop polling (`GetPodStatus`). MDT already streams these — we should bridge.

**Proposal:**
1. Provider implements `PodNotifier`. Stores the callback at provider init.
2. MDT `TracesEmitter` (or a sibling consumer of MappedEvent stream) feeds app-hosting state transitions into the callback.
3. Mapping: `cisco.app_hosting.state == RUNNING` and the app's `RunOpts` label points to a known Pod UID → fire `notifyFn(pod)` with updated `PodStatus.ContainerStatuses[0].State`.
4. The upstream Pod controller receives the notification, patches the Pod object, K8s reflects "Running" instantly.

**Side-effect benefit:** `kubectl get pod` reflects device-side reality within seconds (MDT push) instead of the next reconcile tick (~10s polling).

**Health probes:** standard kubelet HTTP/gRPC probes don't reach app-hosting containers. Map `app.health-status` from MDT → `ContainerStatus.Ready`. Same path as state-transition spans.

**Graceful drain:** `node.Cordon()` → upstream evicts pods → `DeletePod` runs per pod → driver waits for app-hosting to confirm uninstall → notifyFn(terminal). Standard upstream-VK pattern; just needs the notifier wiring.

**Effort:** small. ~1-2 days. **High value for low cost — ship this in the first roadmap tranche.**

### 5.4 Rollback

**What's there:** transactional engine with NETCONF candidate + RFC 6241 confirmed-commit; per-Apply RollbackToken with PreImage replay for RESTCONF; `IOSXEConfigApplyLog` records per-attempt history.

**What's missing:** durable revision history. Engine rollback is per-Apply (within the active reconcile cycle); there's no "revert to revision N" semantic.

**Proposal: `IOSXEConfigRevision` CRD** (Crossplane-style `revisionHistoryLimit`):
- Each `IOSXEConfig` controller, on successful Apply, mints an `IOSXEConfigRevision` CR with the resolved intent + atomicReplaceOwnedKeys snapshot.
- `IOSXEConfig.spec.revisionHistoryLimit` (default 10) — older revisions garbage-collected.
- Operator action: `kubectl patch iosxeconfig X --type=merge -p '{"spec":{"rollbackTo":"X-rev-3"}}'` → reconciler reads revision 3's intent, treats it as the new desired state, performs full plan/apply.
- Combined with NETCONF confirmed-commit: rollback is itself a transactional commit. If the rollback fails, device auto-reverts.

**OTel:** `cvk.config.rollback` span with attribute `cvk.rollback.target_revision`. Failed rollbacks emit a special-attributed span operators search for in Tempo.

**Effort:** medium. ~2 weeks.

### 5.5 Software upgrade

**What's there:** nothing. No image-state CRD, no upgrade orchestration.

**Proposal: `DeviceUpgrade` CRD** (sibling reconciler):
```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceUpgrade
metadata: {name: upgrade-c9300x-17.18.2, namespace: fleet-mgmt}
spec:
  deviceRef: {name: cat9k-smoke}
  targetVersion: "17.18.2"
  imageURI: "tftp://192.168.129.50/cat9k_iosxe.17.18.2.SPA.bin"
  strategy:
    drainPodsTimeout: 5m
    confirmCommitTimeout: 10m
    healthGates:
      - kind: MDTSubscriptionStreaming        # MDT must be flowing post-reload
      - kind: ConfigInSync                    # IOSXEConfig hash must match
      - kind: AppHostingApps                  # all apps return to RUNNING within timeout
status:
  phase: Pending|Draining|Installing|Reloading|Verifying|Succeeded|Failed|RollingBack
  preUpgradeVersion: "17.18.1"
  postUpgradeVersion: "17.18.2"
  conditions: [...]
  events: [...]
```

Reconciler workflow:
1. Cordon node (`node.Cordon`) → emit NodeCondition `ImageUpgrading=True`.
2. Drain pods: rely on upstream pod-eviction; watch all pods on this node go to terminal phase via `GetPodStatus` informer. Hard timeout = `drainPodsTimeout`.
3. Push image (driver-specific: `install add file …` / `request system swap` / etc).
4. Reload device. Observe gNMI Subscribe disconnect. Emit `cvk.device.reloaded` event.
5. Wait for gNMI Subscribe reconnect.
6. Run health gates in order. Each gate is implemented as a check function; failure triggers rollback if device supports image-rollback (most IOS-XE does).
7. Uncordon. Emit `ImageUpgrading=False`. Phase=Succeeded.

**OTel:** root span `cvk.op.upgrade` covers the entire workflow (typically 10-30 min). Child spans per phase. Long but bounded — never exceeds `drainPodsTimeout + confirmCommitTimeout + 30min` ≈ ~45 min worst case.

**Why this is hard:** image transfer reliability, device reboot timing variance, and the fact that gNMI Subscribe + RESTCONF will both go away during reload. Need careful state machine + idempotent retries.

**Effort:** large. ~4-6 weeks per driver. Start with IOS-XE; defer XR/NXOS until those drivers are otherwise wired.

---

## 6. Reassessment — does the existing roadmap need to change?

Yes. Three substantive changes:

### 6.1 The trace convergence roadmap was too inward-looking

Original six phases focused on connecting three trace surfaces that already exist. After integrating the future scope, those three surfaces will be **eight or nine** (add config functions, ops, upgrades, rollbacks). Trying to retrofit each one into the existing roadmap will force ad-hoc decisions.

**Replace with: an OTel SemConv compliance pass** as the foundation, then operation-specific spans that follow the §3.3 taxonomy. Every new feature comes with its operation pattern already named. No more "now where does this span go?" debates.

### 6.2 The MDT-as-topology-source debate had the wrong framing

Codex correctly said "hybrid." But the deeper point: **topology is just one consumer of MDT state**. PodNotifier (§5.3), config drift detection, and ops-side health gates are also consumers. The right architecture is:

```
            MDT pipeline (mapper)
                    │
         ┌──────────┼──────────┬───────────┐
         ▼          ▼          ▼           ▼
   StateCache    Emitters    PodNotifier   HealthGate
   (interface,   (logs/      (RUNNING →    (DeviceUpgrade
    app, BGP)    metrics/     Pod.Ready)    consumes)
                 spans)
```

`StateCache` is a new package consuming from the same `MappedEvent` channel the emitters consume from. It's the single source of truth for "what does the device think its state is, right now." Topology emitter reads from it. PodNotifier reads from it. Health gates poll it.

**Replace `internal/telemetry/correlation` proposal with: `internal/telemetry/state` (the cache) + `internal/telemetry/correlation` (the trace-context join logic).** Two narrow packages, clear single-responsibility.

### 6.3 v1.x vs v1.0 vs v2 boundaries

The old roadmap deferred too much to "v1.x" and "v1beta1." With future scope landing, we need a clearer boundary. Proposal:

- **v1.0 (this branch + 2-3 weeks):** trace foundation, PodNotifier, MDT-fed StateCache, exporter-failure self-metric, `cvk.process.role` attribute, doc consolidation. **No new CRDs.**
- **v1.1 (subsequent quarter):** `IOSXEConfigRevision` CRD + rollback semantics; `DeviceOperation` CRD (read-only ops only — show, capture, ConfigDiff); MDT-supplemented topology (hybrid).
- **v1.2:** `DeviceOperation` write ops (Reload, ConfigPush, FactoryReset gated by extra RBAC); IOS-XR driver telemetry parity.
- **v1.3:** `DeviceUpgrade` CRD (IOS-XE only initially).
- **v2.0:** NX-OS driver, `IOSXEConfigFunction` CRD with CEL/WASM, OpAMP integration if community appetite exists.

Each milestone is independently shippable and provides operator value. v1.0 is a closure event for the current branch and a launching pad for everything that follows.

---

## 7. Unified phased roadmap

### Tranche I — Trace foundation + pod-status bridge (2 weeks)

| Item | File / package | Effort |
|---|---|---|
| Set `vktrace.T = vkotel.Adapter{}` at startup; configure `otel.SetTracerProvider` | `cmd/cisco-vk/main.go`, `cmd/cisco-vk/manager.go` | 1 day |
| System controller gets its own `service.name=cisco-vk-controller` + downward-API SemConv | `cmd/cisco-vk/manager.go` (new `buildControllerProviders` analogous to `buildTelemetryProviders`) | 1 day |
| `cvk.process.role` resource attribute on all three processes | `cmd/cisco-vk/telemetry_providers.go`, `manager.go` | 0.5 day |
| Driver-layer spans: `cvk.config.intent`, `cvk.config.plan`, `cvk.config.apply`, `cvk.config.verify`, `cvk.transport.<protocol>.<verb>` | `internal/drivers/iosxe/configdriver/{engine,transport}.go` | 2-3 days |
| Implement `PodNotifier` in provider; bridge MDT app-hosting transitions to `notifyFn(pod)` | `internal/provider/provider.go`, `internal/telemetry/state/` (new package) | 2-3 days |
| `internal/telemetry/state` package: MDT-fed cache keyed by `(deviceID, kind, key)` with TTL + lastSeen | new package | 2-3 days |
| Exporter-failure self-metric: `cisco_vk_telemetry_exporter_failures_total{signal,reason}` | `internal/otelproviders/providers.go` | 1 day |
| **Verification:** trace `T` shows `Pod admit (upstream span) → cvk.config.reconcile → cvk.config.apply → cvk.transport.netconf.commit`; `kubectl get pod` flips to Running within 5s of MDT-reported state change | lab against cat9k-smoke | 1 day |

**Cumulative outcome:** operator-side traces work, pods see their own state. **Closes the v1.0 gate.**

### Tranche II — MDT-supplemented topology + correlation cache (2 weeks)

| Item | Effort |
|---|---|
| Empirical YANG-path validation against cat9k-smoke (commands in §10) | 1 day |
| `TopologyProvider.GetCDPNeighbors` etc. become cache-readers; per-driver `state` filler subscribes via existing MDT mapper | 3-4 days |
| Topology emitter: stable cycle root spans with `topology.cycle.id` attribute (no heartbeat span) | 2 days |
| `internal/telemetry/correlation` package: traceparent parser, bounded LRU cache, link/parent decisioning | 2-3 days |
| Pod annotations: single `cisco.vk/traceparent` + `cisco.vk/trace-window-end`; reader/writer helpers | 1 day |
| MDT TracesEmitter consults correlation cache; on hit, emits transition spans as children of recent reconcile spans | 2 days |
| **Verification:** trace `T` from Tranche I now ALSO contains the MDT recovery span as a child when within 15-min window; topology cycle adjacent traces show span links | lab |

**Cumulative outcome:** distributed trace per Pod admission, end-to-end. Topology becomes a consumer of MDT state, not an independent poller.

### Tranche III — Configuration revision history + rollback (2-3 weeks)

| Item | Effort |
|---|---|
| `IOSXEConfigRevision` CRD + lister + GC controller honoring `revisionHistoryLimit` | 3 days |
| Engine-side: on Apply success, mint a Revision; on rollback request, load Revision and re-plan | 3-4 days |
| `IOSXEConfig.spec.rollbackTo` field + reconciler dispatch | 2 days |
| `cvk.config.rollback` span with target revision attribute | 0.5 day |
| Status: `Rolled-Back` condition with `LastRollbackedTo: "rev-N"` | 1 day |
| Tests: rollback to N revisions back; confirm-timeout interaction; failed rollback emits event | 2-3 days |
| **Verification:** apply config v1, apply v2, set `rollbackTo: rev-1`; device returns to v1 state; trace shows `cvk.config.rollback` parent over `cvk.config.apply` child | lab |

### Tranche IV — DeviceOperation CRD (read-only ops + adminserver wrap) (2 weeks)

| Item | Effort |
|---|---|
| `DeviceOperation` CRD package + types | 1-2 days |
| Reconciler with phase machine | 2-3 days |
| Driver `OpExecutor` interface; IOS-XE implementations of `ShowCommand`, `ConfigDiff`, `PacketCapture` | 3-4 days |
| Artifact backing: ConfigMap for small (<256KB), URI ref for large | 2 days |
| Adminserver `/v1/exec` becomes a thin wrapper synthesizing transient CRs | 1 day |
| **Verification:** `kubectl create -f packet-capture.yaml` on a real device produces an artifact, status reflects progress, trace shows the operation lifecycle | lab |

### Tranche V — DeviceUpgrade CRD (IOS-XE only initially) (4-6 weeks)

| Item | Effort |
|---|---|
| `DeviceUpgrade` CRD package + types | 2-3 days |
| Reconciler with deep phase machine (Pending → Draining → Installing → Reloading → Verifying → Succeeded|Failed|RollingBack) | 1-2 weeks |
| Driver image-management contract: `ImageManager` interface; IOS-XE impl using `install add/install activate/install commit` flow | 1 week |
| Health gates: `MDTSubscriptionStreaming`, `ConfigInSync`, `AppHostingApps`; pluggable | 1 week |
| Pod drain coordination: cordon node, watch pods to terminal, hard timeout; then proceed | 3-4 days |
| Failure handling: rollback to pre-upgrade image (IOS-XE supports natively); state-machine retries | 1 week |
| **Verification:** upgrade cat9k-smoke from 17.18.1 → 17.18.2 → roll back; full trace shows the workflow; pods evict and restore correctly | extensive lab |

### Tranche VI — Multi-driver parity (ongoing, can run in parallel with II-V)

IOS-XR and NX-OS drivers light up telemetry, then config, then ops, then upgrade — each tranche per driver per feature. Most code is already structured to make this additive.

---

## 8. Risks and decisions to make

### 8.1 Blocking decisions (need answers before tranche I starts)

| Decision | Default if no answer |
|---|---|
| **Does cisco-vk track upstream VK >= v1.13 (with OTel adapter)?** Currently on v1.12.0. The adapter exists in v1.12 but newer versions have improvements. | Stay on v1.12 for now; revisit during v1.1. |
| **OTel Collector deployment posture: bundled subchart vs operator-supplied vs both?** | Both; default disabled, document operator-supplied as the production path. |
| **Sampling: head-based parent-decision vs tail at the Collector?** | Head-based parent-`AlwaysSample` for v1.0 (simpler). Tail sampling at Collector for v1.1. |
| **Trace window TTL: 15 min hard-coded vs CRD-configurable?** | 15 min hard-coded for v1.0. Make it a knob in `IOSXETelemetry.spec.correlation` for v1.1 if operators ask. |

### 8.2 Risk register

| Risk | Mitigation |
|---|---|
| Upstream VK adapter has bugs we discover at scale | Active upstream community; we can patch + PR upstream. The adapter is small (~200 LoC). |
| MDT state cache becomes a coordination hotspot | Sharded by device. Each per-device VK pod owns its slice. No cross-pod cache. |
| `DeviceUpgrade` reload races with active gNMI Subscribe | Stream manager already handles disconnect/reconnect. Add explicit "reload-in-progress" attribute to suppress reconnect-storm self-metrics. |
| `DeviceOperation` writes (Reload, ConfigPush) become a backdoor around `IOSXEConfig` | Strict RBAC: write-class operations require an additional `cisco.vk/op-write` annotation on the operator's user that admission-webhook checks. |
| Annotation-as-trace-carrier RBAC leak (Codex flagged) | Document that Pod read access ≠ trace lookup access; tenant isolation must be at the Tempo layer (multi-tenant Tempo with `X-Scope-OrgID` headers). |
| Tempo cycle-root burst from topology emit: 200 neighbors × 60s = 12K spans/min/device | Already mitigated by exporter queue 8192 + batch 512. Add hard cap on link-spans-per-cycle (default 256, configurable). |
| Sibling-CRD proliferation | One operator-team review per new CRD. `DeviceConfigRevision`, `DeviceOperation`, `DeviceUpgrade` are the three sanctioned new ones for v1.x. |

### 8.3 Success metrics

For each tranche, the success criterion is the verification step. At a higher level:

- **Operator can answer "why did Pod X not start?" in 1 click** by the end of Tranche II (paste `cisco.vk/last-error-trace-id` into Tempo).
- **Time-to-rollback < 60 seconds** for a config change, by the end of Tranche III.
- **Operations runbook for IOS-XE upgrade reduces from 14 manual steps to 1 CR apply** by the end of Tranche V.
- **Cross-driver feature parity within one tranche-cycle** of feature shipping on IOS-XE, by end of Tranche VI for at least one of XR / NXOS.

---

## 9. Documentation deltas (replace, supersede, or update)

| Existing doc | Action |
|---|---|
| `docs/telemetry-branch-review.md` | Keep — accurate snapshot of d7a61f8. |
| `docs/telemetry-trace-convergence-roadmap.md` | **Supersede with Tranche II of this plan.** Add a banner at the top redirecting readers. |
| `docs/telemetry.md` | Update §"Future work" to point at this plan. |
| `docs/telemetry-cardinality.md` | Keep, expand with `DeviceOperation` and `DeviceUpgrade` cardinality guidance once those land. |
| `docs/observability.md` | **Major rewrite as per §3 of this plan** — make this the canonical "OTel spine" doc. |
| `docs/architecture.md` | Update the diagram to include the State Cache and OTel spine. |
| (new) `docs/lifecycle.md` | Document the `PodNotifier` pattern + lifecycle states. |
| (new) `docs/operations.md` | Document `DeviceOperation` once Tranche IV lands. |
| (new) `docs/upgrades.md` | Document `DeviceUpgrade` once Tranche V lands. |

---

## 10. Empirical validations to run before tranche II starts

These verify whether the YANG paths needed for MDT-supplemented topology are actually streamed by IOS-XE 17.18.1. Run against `cat9k-smoke` (10.1.1.1):

```bash
# Capability sniff
gnmic -a 10.1.1.1:50052 -u "$GNMI_USER" -p "$GNMI_PASS" --skip-verify capabilities

# CDP — uncertain support
gnmic -a 10.1.1.1:50052 -u "$GNMI_USER" -p "$GNMI_PASS" --skip-verify subscribe \
  --mode stream --stream-mode sample --sample-interval 30s --encoding proto \
  --path '/Cisco-IOS-XE-cdp-oper:cdp-neighbor-details/cdp-neighbor-detail'

# OSPF — example CR uses native; verify native works
gnmic -a 10.1.1.1:50052 -u "$GNMI_USER" -p "$GNMI_PASS" --skip-verify subscribe \
  --mode stream --stream-mode sample --sample-interval 30s --encoding proto \
  --path '/Cisco-IOS-XE-ospf-oper:ospf-oper-data/ospfv2-instance/ospfv2-area/ospfv2-interface/ospfv2-neighbor'

# Interfaces (already proven; verify IPv4 path is in same notification)
gnmic -a 10.1.1.1:50052 -u "$GNMI_USER" -p "$GNMI_PASS" --skip-verify subscribe \
  --mode stream --stream-mode sample --sample-interval 30s --encoding proto \
  --path '/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics' \
  --path '/Cisco-IOS-XE-interfaces-oper:interfaces/interface/ipv4'

# App-hosting (already proven on this branch)
gnmic -a 10.1.1.1:50052 -u "$GNMI_USER" -p "$GNMI_PASS" --skip-verify subscribe \
  --mode stream --stream-mode sample --sample-interval 30s --encoding proto \
  --path '/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app'
```

Decision tree:
- All four stream OK → pure MDT-fed cache, RESTCONF only for identity (hostname/serial/version).
- CDP fails → keep CDP RESTCONF poll; everything else MDT.
- OSPF fails → keep OSPF RESTCONF poll; everything else MDT.
- Multiple failures → revert to RESTCONF-primary, MDT-supplemental for stats only.

---

## 11. Codex adversarial review — integrated findings

The plan above was reviewed adversarially by Codex. Findings are folded back into the plan here. Where the original text overstates a claim, the corrected position takes precedence. Each item carries the original Codex severity rating.

### 11.1 Trace adapter is not a 1-day plug-in [critical]

**Original claim (§3.1):** install `vktrace.T = vkotel.Adapter{}` once at startup; ~1 day.

**Correction.** Codex verified against `~/go/pkg/mod/github.com/virtual-kubelet/virtual-kubelet@v1.12.0/trace/{trace.go,opentelemetry/opentelemetry.go}`:
- VK adapter calls `otel.Tracer(name).Start(...)` at span-start time (`opentelemetry.go:54`), so the global `otel.SetTracerProvider(...)` MUST be set before any VK goroutine opens its first span. Today the conditional `vktrace.T = vkotel.Adapter{}` lives inside per-provider topology setup at `run.go:290` — that's the wrong place; it must move to process startup before `node.NewNodeController`.
- Controller-runtime does not propagate OTel span context through reconcile loops automatically. Every reconciler that replaces `ctx` (e.g. `context.Background()` for an out-of-band write) will orphan its spans. This means an audit pass over every reconciler, every driver entrypoint, and every place we hand a child goroutine a non-derived context.

**Revised effort for Tranche I trace work:** 3–5 days, not 1 day. Add an explicit "OTel context-propagation audit" sub-task and a lint check (`scripts/lint-ctx.sh`) to keep new code from regressing.

### 11.2 PodNotifier wiring is not 1–2 days [critical]

**Original claim (§5.3):** wire `PodNotifier` from MDT app-hosting transitions; "small. ~1-2 days. **High value for low cost — ship this in the first roadmap tranche.**"

**Correction.** Codex flagged the upstream contract (`podcontroller.go:87`): `NotifyPods` must not block. The MDT subscriber goroutine cannot drive the callback inline because:
- Sub-second interface flaps would race the controller's enqueue path.
- The pod controller's workqueue coalesces by key but cannot protect a producer that blocks before reaching the queue.
- A telemetry goroutine blocking on pod-controller cache visibility is exactly the back-pressure path that turned into a 12 GiB leak in the receiver community.

**Required design:** non-blocking ring buffer between MDT consumers and the `PodNotifier` callback. Producer is the MDT mapper drainEvents loop; consumer is a single goroutine that drains the ring at the callback's pace. Drops on overflow are counted via a new self-metric `cisco_vk_telemetry_notifier_dropped_total{reason}`.

**Revised effort:** 3–5 days, dependent on the state cache being in place. Still high-value, but no longer "the cheap one in Tranche I."

### 11.3 Topology hybrid is hypothesis until lab-validated [critical]

**Original claim (§5, §6.2):** MDT can supplement RESTCONF for topology with hybrid cache.

**Correction.** Native YANG schemas exist in repo test fixtures, but actual gNMI streaming for `Cisco-IOS-XE-cdp-oper:cdp-neighbor-details` and `Cisco-IOS-XE-ospf-oper:ospf-oper-data/...` is unverified on production IOS-XE releases. Treat the hybrid topology as a **prototype risk**, not a confirmed capability. The §10 empirical-test step is mandatory before Tranche II commits to any architectural change in the topology emitter.

If the lab tests fail for either CDP or OSPF, the hybrid plan downgrades to "MDT for interfaces + app-hosting; RESTCONF stays for CDP/OSPF and identity," and the topology emitter retains its current poll loop for those paths.

### 11.4 IOSXEConfigFunction misrepresents Crossplane [moderate]

**Original claim (§5.1):** functions implementable as embedded Go, CEL, or WASM.

**Correction.** Crossplane composition functions are OCI-packaged pipeline steps invoked by the Crossplane runtime — not a CEL/Go/WASM intrinsic. Determinism is a policy choice imposed by authors, not a runtime guarantee. cisco-vk has no WASM host, sandbox, ABI, cache, or supply-chain model.

**Revised plan:** v1.x ships only embedded-Go function implementations with a clear runner contract. CEL is deferred to v2.0. WASM is removed from the roadmap entirely until there's a concrete user demand and a security-review cycle to design the host environment.

### 11.5 DeviceOperation should not use per-driver registration [moderate]

**Original claim (§5.2):** new `OpExecutor` interface with per-driver kind registration.

**Correction.** Per-op reconciler with a driver capability map is the better default because operations differ in validation, RBAC, idempotency, artifact shape, cancellation, and status. A generic registration shape masks those differences and recreates the same uncontrolled-privileged-channel risk that the §5.2 plan already conceded for `/v1/exec`.

**Revised design:**
- Each operation kind (`ShowCommand`, `PacketCapture`, `ConfigDiff`, `Reload`, …) gets its own reconciler with its own RBAC.
- Drivers register capabilities (`SupportsPacketCapture bool`, `SupportsReload bool`) in a capability map; reconcilers consult before dispatching.
- Cross-cutting concerns (status conditions, TTL cleanup, audit-log emission) live in a shared `internal/ops/common` helper package.

This adds ~30% code over the original sketch but is much easier to RBAC-gate.

### 11.6 IOSXEConfigRevision conflates rollback with confirmed-commit [critical]

**Original claim (§5.4):** "engine has rollback tokens but they're transient (per-Apply); no durable revision."

**Correction.** Codex verified: a `RollbackToken` scaffold exists at `configdriver/types.go:92`, but the engine result at `engine.go:136` carries no rollback token, and the stub rollback at `stub.go:49` is not implemented. The plan's "use the existing engine rollback" is wishful — it doesn't exist yet.

**Required pre-work for Tranche III:**
1. Implement `RollbackToken` flow on the engine Apply path. Token captures pre-Apply observed state for RESTCONF; for NETCONF the candidate datastore IS the rollback primitive.
2. Wire the token into `IOSXEConfigApplyLog` so every successful Apply has a recoverable token reference.

Then `IOSXEConfigRevision` becomes thin: it stores the resolved intent at the time of Apply. `spec.rollbackTo: rev-N` synthesizes the desired config from revision N and runs a normal reconcile — completely separate from confirmed-commit. **Confirmed-commit is a transport-level timer mechanism; `rollbackTo` is declarative intent.** Codex's design is correct: keep them apart.

**Revised Tranche III scope:** 3–4 weeks (was 2–3), because RollbackToken implementation is now in scope.

### 11.7 DeviceUpgrade is underspecified [critical]

**Original claim (§5.5):** workflow is "install add | install activate | install commit"; "most IOS-XE supports image rollback."

**Correction.** Codex flagged multiple gaps:
- **Image integrity verification** (sha256, signature) — missing from spec.
- **Package expansion timing** — varies by platform; no checkpoints in plan.
- **Activation side effects** — reload behavior differs per platform variant (Catalyst vs ASR vs NCS).
- **Smart Licensing Using Policy** requirements are release- and device-state-dependent; pre/post checks needed.
- **Dual-RP / ISSU** behavior is platform-specific. Plan has no capability discovery or platform matrix.
- **"Most IOS-XE supports image rollback"** is a hypothesis until tied to a release+platform matrix.

**Required pre-work for Tranche V:**
1. Build a `PlatformCapability` matrix (driver-side): per-platform support flags for ISSU, dual-RP, package install, image rollback, smart-licensing-mode.
2. Design durable phase checkpoints: every accepted-by-device command writes a checkpoint with idempotency key. Controller restart resumes from checkpoint, not from scratch.
3. Specify finalizer semantics during a reload: device is unreachable for 5–15 min; controller must NOT delete the CR finalizer until reachability is re-established and the post-reload health gates pass.
4. Validate behavior on at least two distinct platforms before the CRD is declared v1alpha1-ready.

**Revised Tranche V scope:** 6–8 weeks for IOS-XE single-RP only. Dual-RP / ISSU is a separate v1.4 milestone.

### 11.8 Tranche sequencing has hidden dependencies [critical]

**Original claim (§7):** Tranches I-V are 2-2-2½-2-5 weeks.

**Correction.** Codex enumerated cross-tranche dependencies the original sizing ignored:
- Tranche I's trace work and PodNotifier are themselves sequential (notifier needs state cache).
- Tranche II depends on Tranche I being complete and load-tested before MDT can drive pod status safely.
- Tranche III's `IOSXEConfigFunction` is independent of topology; OK to parallelize.
- Tranche IV's revision history requires the (now-acknowledged) RollbackToken implementation as a prerequisite.
- Tranche V depends on the platform capability matrix and DR model, neither of which exist.

**Revised tranche sizing:**
| Tranche | Original | Revised | Why |
|---|---|---|---|
| I (trace + PodNotifier + state cache) | 2w | **3–4w** | Context rewiring + non-blocking ring buffer |
| II (MDT-supplemented topology + correlation) | 2w | **3w** (gated on I) | Tempo cycle-roots + correlation cache + lab YANG validation |
| III (config revision + rollback) | 2-3w | **3–4w** | RollbackToken implementation now in scope |
| IV (DeviceOperation read-only) | 2w | **3w** | Per-op reconcilers, not per-driver registration |
| V (DeviceUpgrade IOS-XE only) | 4-6w | **6-8w** | Platform matrix + DR + checkpoints |

Total v1.0→v1.3 critical path: **18–25 weeks** (was 12–18). Multi-driver parity (Tranche VI) runs alongside.

### 11.9 Sibling CRDs should stay separate [moderate]

**Original consideration (§8):** could `DeviceOperation` and `DeviceUpgrade` merge?

**Codex verdict: no, keep separate.** Their schemas already diverge: ops is a generic command envelope; upgrade carries image URI, checksum, maintenance window, drain policy, multi-phase state, and reboot semantics. Merging would force a `spec.params` bag that's harder to validate and RBAC-gate than two focused CRDs.

**Revised plan:** define a shared `DeviceOperationConditions` package for cross-CRD condition vocabulary (`Pending`, `Running`, `Succeeded`, `Failed`, `Cancelled`) but keep CRD types separate.

### 11.10 Multi-tenancy posture is missing [critical]

**Codex finding:** the security doc claims namespace-scoped Secret isolation, but Helm RBAC grants the VK service account cluster-wide pod and Secret/ConfigMap read access. Every new write-capable CRD widens the blast radius without a tenant model.

**Required additions to the plan:**
1. **Device ownership annotation.** Every `CiscoDevice` carries `cisco.vk/owner-tenant: <id>`. Admission webhook rejects cross-tenant `IOSXEConfig` / `DeviceOperation` / `DeviceUpgrade` references.
2. **Namespace admission policy.** `IOSXEConfig` in tenant-A namespace cannot reference a `CiscoDevice` owned by tenant-B.
3. **Output sink confinement.** Operation artifacts (`DeviceOperation.status.artifactURIs`) restricted to a per-tenant prefix.
4. **Trace/artifact isolation.** Tempo `X-Scope-OrgID` set per tenant; OTLP exporter headers configured via per-CR or per-namespace policy.
5. **RBAC separation.** VK pod-scheduling RBAC distinct from operation/upgrade RBAC. Today they share one ClusterRole.

**Tranche placement:** the multi-tenancy model is a v1.0 PREREQUISITE before any write-capable CRD ships. Tranche IV (`DeviceOperation` with even read-only ops) should NOT land before this.

### 11.11 DeviceUpgrade disaster recovery is a design gap, not an implementation detail [critical]

**Codex finding:** A rebooting device drops transport while the controller may restart. Without durable phase checkpoints, idempotency keys per install command, finalizer-during-reload semantics, and a "last command accepted" model, the controller cannot distinguish "activate sent, reload pending" from "activate never happened" after a crash.

**Required design (must precede Tranche V scope):**
- **Phase checkpoint store.** Each `DeviceUpgrade` phase transition writes to a durable side-channel (a `DeviceUpgradeCheckpoint` ConfigMap or a status sub-resource). Controller restart reads it.
- **Idempotency keys.** Every command sent to the device carries an op-id; device-side reconcile-on-restart asks "did you process op-id X?" before re-sending.
- **Finalizer-during-reload.** CR cannot be deleted while phase ∈ {Reloading, Verifying}. Forces operator to explicitly cancel + clean up.
- **Reconnect deadlines.** Hard deadline (default 30min) on post-reload reconnect. Past deadline = phase=Failed regardless of subsequent reachability.
- **"Last accepted" model.** Device-side query (e.g. `show install summary`) returns last-accepted command; controller compares to its checkpoint to detect drift.

### 11.12 CRD schema evolution requires a baseline now [moderate]

**Codex finding:** repo has only `v1alpha1` API packages; no conversion webhook code. Kubernetes requires one storage version and explicit conversion strategy when serving multiple versions. Before sibling CRDs proliferate, baseline rules need to land.

**Required additions before Tranche III:**
1. Additive-only field policy on `v1alpha1` packages — no field renames or removals.
2. Immutable identity field list per CRD (e.g. `IOSXEConfig.spec.deviceRef.name` is immutable post-creation).
3. Condition type conventions: vocabulary frozen at the unified set (`Ready`, `Reconciling`, `Pending`, plus per-CRD specifics).
4. Defaulting webhooks (one per CRD).
5. Structural schema pruning — already done for `IOSXETelemetry`; verify for the others.
6. Conversion-test scaffold for the eventual v1alpha1 → v1beta1 transition.

### 11.13 v1alpha1 baseline is correct for new CRDs [moderate]

**Codex finding (positive):** new CRDs should start `v1alpha1`. Given unresolved API shape questions on rollback / op execution / upgrade recovery / multi-tenancy, `v1beta1` would overstate stability.

**Confirmed:** all new CRDs (`DeviceConfigRevision`, `DeviceOperation`, `DeviceUpgrade`) ship as `v1alpha1`. Promote to `v1beta1` only after: one real production upgrade cycle, conversion-webhook coverage, admission-policy land, and compatibility tests.

---

## 12. Net effect of Codex review on the headline plan

| Original claim | Status after review |
|---|---|
| "1.0 is 2-3 weeks away" (§1, §6.3) | **Closer to 4-5 weeks.** Trace work alone is 3-5 days; PodNotifier is 3-5 days; state cache + load test add another week. |
| "v1.x ladder is 12-18 weeks total" (§7) | **Revised to 18-25 weeks** for v1.0 → v1.3. v1.4 (dual-RP upgrades) adds another quarter. |
| "Trace adapter is a 1-day flip" | **3-5 days** with context-propagation audit + lint check. |
| "PodNotifier is a 1-2 day notifier wire-up" | **3-5 days** with non-blocking ring buffer + drop self-metric + load test. |
| "Use existing engine RollbackToken" | **Build it first.** Token is a scaffold today, not a working flow. Adds 1 week to Tranche III. |
| "DeviceUpgrade is just install add/activate/commit" | **Adds platform-capability matrix + DR design as v1.3 prerequisites.** ~6-8 weeks per single-RP platform; ISSU/dual-RP is v1.4. |
| "Multi-tenancy implicit via namespaces" | **Explicit tenant model required before any write-capable CRD ships.** Webhook + RBAC split + trace-tenant isolation add ~1 week to v1.0. |
| "Hybrid topology cache is sound" | **Hypothesis pending lab validation.** §10 commands are mandatory; results gate the architecture choice. |

**Bottom line:** the unified plan is structurally correct (sibling CRDs, OTel as spine, MDT as state source) but every effort estimate was 30-50% optimistic, and three preliminaries (RollbackToken, multi-tenancy, DR for upgrade) need to land before their dependent tranches start.

---

## 13. References

- Upstream VK contracts: `~/go/pkg/mod/github.com/virtual-kubelet/virtual-kubelet@v1.12.0/{node,trace,errdefs,nodeutil}`
- Liqo sibling-CRD pattern: https://github.com/liqotech/liqo
- OTel SemConv: https://opentelemetry.io/docs/specs/semconv/
- W3C Trace Context: https://www.w3.org/TR/trace-context/
- RFC 6241 (NETCONF) §8.4 confirmed-commit: https://datatracker.ietf.org/doc/html/rfc6241#section-8.4
- Existing branch state: `docs/telemetry-branch-review.md`
- Earlier joint review with Codex: `/tmp/cvk-architecture-assessment.md`
