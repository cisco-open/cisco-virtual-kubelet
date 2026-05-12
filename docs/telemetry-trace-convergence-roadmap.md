<!--
Copyright 2026 Cisco Systems Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
-->

# Trace Convergence Roadmap: CVK Provisioning + Topology + MDT App-Hosting

> Status: superseded by `docs/cvk-unified-architecture-plan.md` Tranche I/II.
> The implementation now uses the upstream Virtual Kubelet trace adapter,
> W3C `cisco.vk/traceparent`, MDT state cache, and bounded correlation cache.
> Keep this document only as historical context for the original trace problem.

**Goal:** turn three currently-disconnected trace surfaces into one viable distributed trace that follows a Pod from `kubectl apply` to "running on a Catalyst with confirmed app-hosting state."

---

## 1. The three surfaces today

| Surface | Source | Trace shape today | Verified at | Layer |
|---|---|---|---|---|
| **CVK provisioning** | `internal/provider/*`, `internal/drivers/iosxe/configdriver/*`, `internal/controller/*` | **None.** No `tracer.Start` calls in any reconciler or driver writer. | absent | A (causal) |
| **Topology service map** | `internal/provider/otel_topology.go` | Cyclic snapshot. Each emission cycle: 1 `node.<host>` SERVER span + N `link.<if>->{neighbor}` CLIENT spans + M `hosted.<ns>/<pod>` CLIENT spans. Every cycle is a new trace ID. | shipped on `main` | B (snapshot) |
| **MDT app-hosting state transitions** | `internal/telemetry/emit/traces.go` | Recovery span on healthy ← unhealthy transition only. Per-entity (e.g. per app, per BGP neighbor). One span per recovery event. | shipped on this branch | C (causal but device-side) |

**The disconnect.** A user creates a Pod → operator pushes IOSXE config → device starts a container → MDT reports `app.state=RUNNING` → topology emitter notices the new app on the next cycle. That is **five causally connected events** that today produce **two disconnected traces** (topology snapshot + MDT recovery span) and **zero operator-side spans**.

---

## 2. Architectural target

```
                                                      ┌── Layer A (operator)
   kubectl apply -f pod.yaml                          │   "vk.pod.sync"
            │                                         │
            ▼                                         │
  ┌──────────────────────────────────┐                │
  │ VirtualKubelet pod adapter       │                │  <-- spans here
  │   ↓                              │                │
  │ IOSXEConfigReconciler.Reconcile  │                │  <-- spans here
  │   ↓                              │                │
  │ configdriver.intent.Build         │                │  <-- spans here
  │   ↓                              │                │
  │ transport.Write (gNMI Set)       │                │  <-- spans here
  └────────────────┬─────────────────┘                │
                   │ writes annotation                 │
   pod.annotations[cisco.vk/trace-id] = <T>            │
                   ▼                                  ──
          Device starts container                       ┌── Layer B (snapshot)
                   ▼                                    │
  ┌─────────────────────────────────────────┐           │
  │ TopologyExporter cycle (every 60s)      │           │  <-- LINK to T
  │   reads pod annotations,                │           │
  │   emits hosted.<ns>/<pod> with link    │           │
  │   to trace T                            │           │
  └─────────────────────────────────────────┘           │
                                                        ──
          Device MDT reports app.state=RUNNING          ┌── Layer C (causal device-side)
                   ▼                                    │
  ┌─────────────────────────────────────────┐           │
  │ MDT TracesEmitter.Emit                  │           │  <-- CHILD of T
  │   transitionTracker matches rule         │           │      (within 15-min window)
  │   correlation cache hit on pod-uid       │           │
  │   → SpanContext = parent of trace T      │           │
  │   → emit recovery span as child          │           │
  └─────────────────────────────────────────┘           ──
```

End state: a single trace `T` shows the Pod admission, the config push, the device confirming the container started (via MDT recovery span), and a span link from the next topology snapshot. Operators click through from "Pod Failed" alert → trace `T` → see exactly which step took how long.

---

## 3. Convergence plan

### Phase 0 — Foundation (mostly done)

| Item | Status | Where |
|---|---|---|
| Single OTel `TracerProvider` shared across topology + MDT | ✅ | Phase 4 of telemetry branch consolidated this; topology uses shared TP when `Providers.Tracer` is non-nil |
| Stable resource identity (`service.name`, `service.instance.id`, `host.name`, `k8s.pod.*`) | ✅ | v1-blocker batch wired the downward API + SemConv keys |
| Shared OTLP gRPC connection (gzip, 64 MiB, bounded queues) | ✅ | `internal/otelproviders/providers.go` |
| Same TracerProvider also serves the operator process | ⚠️ partial | Per-device pod has it. **System controller does not.** The operator-side reconcile spans (Phase A) need a TracerProvider in the system-controller process. Add `buildTelemetryProviders` invocation in `cmd/cisco-vk/manager.go`. |

**Action:** finish Phase 0 by wiring the system controller to the same OTLP endpoint with `service.name=cisco-vk-controller` and `service.instance.id` from its own POD_UID. ~30-line change.

---

### Phase A — Operator-side reconcile spans

**Why it's first.** Without operator spans there is no causal trace to attach anything to. Today every causal trace originates from "device emitted a transition" — too late, not actionable.

**Where the spans go.** Every reconciler entry point + every driver writer.

| Reconcile/operation | Span name | Span kind | Notable attributes |
|---|---|---|---|
| `CiscoDeviceReconciler.Reconcile` | `cvk.device.reconcile` | INTERNAL | `cisco.device.name`, `generation`, `phase` |
| `IOSXEConfigReconciler.Reconcile` | `cvk.config.reconcile` | INTERNAL | `cvk.cr.name`, `cvk.cr.namespace`, `cvk.lease.holder` |
| `IOSXETelemetryReconciler.Reconcile` | `cvk.telemetry.reconcile` | INTERNAL | `cvk.cr.name`, `subscriptionCount`, `phase` |
| Pod sync (Provider → vk-cmd) | `cvk.pod.sync` | INTERNAL | `k8s.pod.uid`, `k8s.pod.name`, `k8s.pod.namespace`, `cisco.device.name` |
| `configdriver/intent.Build` | `cvk.config.intent` | INTERNAL | `family`, `cli.lines` |
| `configdriver/transport.Write` | `cvk.config.push` | CLIENT | `net.peer.name`, `transport` (`gnmi-set`/`netconf-set`/`cli`), `bytes`, `result` |
| `pod_transforms.Apply` | `cvk.pod.transform` | INTERNAL | `transformerName`, `inputCount` |

**Implementation.** Add a `Tracer` field to each reconciler struct. Wrap `Reconcile` body in `ctx, span := r.Tracer.Start(ctx, "cvk.config.reconcile", ...)`. Pass `ctx` through to all sub-operations so writer spans become children. Use `defer span.End()`.

**Where to plumb.** `cmd/cisco-vk/manager.go` already has the `mgr` controller-runtime manager; `mgr.GetEventRecorderFor` is a good model. Add a `mgr.GetTracerProvider().Tracer("cisco-vk")` accessor and inject into each reconciler at SetupWithManager.

**Effort:** ~300 LoC across 6 files. Mechanical OTel boilerplate; no design tradeoffs.

---

### Phase B — Trace propagation via Pod annotations

**The idea.** When the operator opens a Pod-sync span, write the trace+span IDs into Pod annotations *before* doing the work. Anything downstream that knows about the Pod can recover the SpanContext and link/attach to it.

**Annotations.**
```yaml
metadata:
  annotations:
    cisco.vk/trace-id: 4bf92f3577b34da6a3ce929d0e0e4736
    cisco.vk/span-id:  00f067aa0ba902b7
    cisco.vk/trace-flags: "01"             # W3C trace flags (sampled / not)
    cisco.vk/trace-window-end: "2026-05-09T13:30:00Z"   # 15 min from start
```

The `trace-window-end` is operator-supplied and acts as a safety valve: after expiry, downstream consumers should NOT chain to the trace — emit standalone with `cvk.causal.unknown=true`.

**Carriers.**
- The trace-id+span-id encode the operator's parent SpanContext.
- `trace-flags` preserves W3C-spec sampling decisions across the propagation.
- `trace-window-end` prevents stale traces from being chained months later.

**Writer.** Add to `internal/provider/pod_handler.go` (the Pod admit/sync path) a helper:
```go
func annotatePodWithTraceContext(pod *corev1.Pod, sc trace.SpanContext, ttl time.Duration) {
    pod.Annotations["cisco.vk/trace-id"] = sc.TraceID().String()
    pod.Annotations["cisco.vk/span-id"]  = sc.SpanID().String()
    pod.Annotations["cisco.vk/trace-flags"] = sc.TraceFlags().String()
    pod.Annotations["cisco.vk/trace-window-end"] = time.Now().Add(ttl).UTC().Format(time.RFC3339)
}
```

**Reader.** A symmetric `loadPodTraceContext(pod)` returns either `(SpanContext, true)` or `(zero, false)`. Used by:
- `OTELTopologyExporter.emitHostedAppSpan` — when a hosted app's pod has a live trace context, emit the topology span with `oteltrace.WithLinks(SpanContext{trace-id, span-id})` instead of as an orphan.
- MDT `TracesEmitter` — see Phase D.

**Effort:** ~80 LoC + helper test. No new dependencies.

---

### Phase C — Topology snapshots become contributors, not islands

**Today.** Each cycle is its own root trace. The topology emitter's "node + N link spans + M app spans" is a 60-second-recurring orphan trace.

**Target.** Topology continues to emit *cyclically* (it has to; it's a snapshot), but each cycle's emission attaches to the right place:
- **Network neighbor links (`link.<if>->...`)** — no pod identity; remain as cyclic snapshots under a new long-running parent span `cvk.device.heartbeat` (one open span per device, lasts as long as the topology emitter runs). All cycles attach as children. This makes the topology view a single coherent trace per device, queryable by `service.instance.id`.
- **Hosted-app spans (`hosted.<ns>/<pod>`)** — when `cisco.vk/trace-id` annotation is present and the trace window has not expired, emit with `WithLinks(parent)`. Otherwise, attach as child of the `cvk.device.heartbeat` parent.

**Why span links and not children?** Topology cycles emit on a clock, not in causal response to any specific event. A child span implies "happened during execution of parent"; a link implies "this snapshot includes evidence about that operation." Span links are the right model semantically and keep Tempo's trace tree from growing 60-times-an-hour.

**Implementation note.** `cvk.device.heartbeat` is a long-running span — `oteltrace.WithSpanKind(oteltrace.SpanKindServer)`, opened on emitter start, ended on emitter shutdown. Use `span.AddEvent("cycle-emitted")` per cycle for searchability. Tempo handles long-running spans via the `keep_open` heuristic; verify against the configured Tempo install.

**Effort:** ~150 LoC in `otel_topology.go` + integration test.

---

### Phase D — MDT-side correlation cache

**Where the value lives.** The MDT `TracesEmitter` already produces causally meaningful spans (recovery events). Today it emits them as orphan spans — same problem the topology had. The fix is structurally analogous to Phase C but more sophisticated because MDT events are sparse and unpredictable.

**Two mechanisms compose:**

1. **Annotation lookup (cheap, eventually consistent).** When the mapper's `ResourceAttrExtractor` lifts `cisco.app_hosting.app_name` for an entity, ALSO look up the corresponding Pod via the controller's pod-by-app-id index (the index already exists in `internal/controller/ip_discovery.go`). If the Pod has a live `cisco.vk/trace-id` annotation, attach the trace context to the `MappedEvent`. The TracesEmitter then emits the recovery span with `WithLinks(podSpanContext)` AND the resource attribute `cvk.causal.parent.trace_id=<id>`.

2. **Time-window correlation cache (expensive, more accurate).** The reconciler maintains an in-memory map `(device, app_id) → recentReconcileSpan` with 15-minute TTL. Updated on every `cvk.config.push` span end with `success=true`. The TracesEmitter consults this map on transition events: if the (`device`, `app_id`) tuple matches and the span is < 15 min old, emit the recovery span as a **child** of the cached span (not just a link). The trace then shows the operator's config push as the parent, with the device's confirming MDT transition as a child span seconds-to-minutes later.

**Bookkeeping.** The cache lives in `IOSXETelemetryReconciler` (or a shared correlation service if multiple reconcilers want in). Memory-bounded: 4096 entries cap (matches the receiver's pattern). LRU on cap.

**Failure mode.** Transition arrives outside the window → emit standalone with `cvk.causal.unknown=true` resource attribute. Operators can search Tempo for these to investigate "device noise unrelated to my changes."

**Effort:** ~250 LoC across `iosxetelemetry_reconciler.go`, `traces.go`, plus a small `internal/telemetry/causalcache` package.

---

### Phase E — Cross-tool join (Grafana datasource correlations)

OTel resource identity makes this configuration-only. Once Phases 0-D land:

- **Tempo → Prometheus exemplars.** Configure Tempo's `metricsGenerator` to emit RED-method metrics with span_id exemplars. Click from Grafana metric panel into the trace.
- **Loki → Tempo trace links.** Loki labels include `traceID` (from log records emitted by `LogsEmitter` — the OTel SDK puts the active trace ID on each LogRecord automatically when a span is in context). Configure Grafana derived field `traceID -> Tempo`. Click from log line into trace.
- **Tempo → Tempo span-link traversal.** The "show linked spans" feature in Grafana 10+ surfaces our topology→provisioning links automatically.

**No code changes.** Just Helm values for the operator's bundled `collector` subchart and Grafana datasource config.

---

### Phase F — Pod-status echo via CR conditions

**Closing the loop the user sees.** Operators should be able to `kubectl describe pod` and see *which* trace ID covers the most recent provisioning attempt — even after the trace has finished.

```yaml
status:
  conditions:
    - type: Provisioned
      status: "True"
      reason: ConfigConfirmedByMDT
      message: "config push completed in 1.2s; MDT confirmed app RUNNING in 4.5s"
      observedGeneration: 12
  annotations:
    cisco.vk/last-trace-id: 4bf92f3577b34da6a3ce929d0e0e4736
    cisco.vk/last-trace-duration: "5.7s"
    cisco.vk/last-trace-window-end: "2026-05-09T13:30:00Z"
```

The reconciler writes the most recent trace ID + duration into a Pod (or CiscoDevice) status annotation when the trace ends. Operators paste that ID into Tempo for a 100% accurate replay of what happened.

**Effort:** ~50 LoC. Cosmetic but it's the user-facing surface that proves the system works.

---

## 4. Suggested phasing

| Tranche | Items | Verifiable signal | Effort |
|---|---|---|---|
| **A (foundation)** | Phase 0 follow-up + Phase A (operator-side spans) | Tempo shows `cvk.config.reconcile` traces with child `cvk.config.push` spans | ~2-3 days |
| **B (annotation propagation)** | Phase B (Pod annotations) + Phase C (topology heartbeat parent + span links) | Tempo shows topology cycles linked to most recent `cvk.config.push` per pod | ~2-3 days |
| **C (causal MDT joining)** | Phase D (correlation cache) | Tempo shows recovery spans as children of `cvk.config.push` when within 15-min window | ~3-5 days |
| **D (UX)** | Phase E (Grafana datasource config) + Phase F (status conditions) | Click from Pod status annotation → Grafana → full trace timeline | ~1 day |

Each tranche is independently shippable. After tranche A, traces are useful but isolated. After tranche B, topology becomes a navigable map. After tranche C, the full causal story emerges. After tranche D, operators can self-serve.

---

## 5. Decisions to make before starting

| Decision | Options | Recommendation |
|---|---|---|
| Where do operator-side spans live in process topology? | (a) System controller process; (b) per-device pod | **System controller.** Reconcile happens there. Per-device pod hosts MDT and pod-runtime spans. Two `service.instance.id`s — `cisco-vk-controller` + `cisco-vk-telemetry` — chained via SpanContext propagation. |
| Annotation namespace prefix? | `cisco.vk/...` (current pattern) vs `kubectl.cisco.com/...` | Stay with `cisco.vk/` — matches existing annotation conventions in `pod_transforms.go`. |
| TTL for trace-context annotations? | 5/15/60 min | **15 min.** Long enough for slow OS upgrades; short enough that stale traces don't poison new attempts. Receiver's experience shows 12-hour windows produce 12 GiB leaks. |
| Span links vs nested spans for topology cycles? | Links / children / both | **Links to provisioning, child of `cvk.device.heartbeat`.** Two-tier: long-running parent for grouping + cyclic snapshot children, plus links to the causally-relevant trace per pod. |
| Sampling? | Always-on / head-based / parent-based | **Parent-based.** Operator-side decides. If a Pod creation is sampled, downstream MDT transitions on the same trace are too. |
| Where does the correlation cache live? | Per-reconciler / shared service / external (Redis) | **Shared in-memory service.** ~4096 entries, ~15 min TTL. External cache adds operational dependency that does not pay for itself yet. |

---

## 6. What changes the user sees

| Before | After |
|---|---|
| Two disconnected traces in Tempo: topology (orphan, every 60s) + transition recoveries (orphan, on event) | One trace per Pod admission with: admit → reconcile → intent → push → MDT confirmation → topology link |
| Pod status: `Phase: Running` (no provenance) | Pod status: `Phase: Running` + `cisco.vk/last-trace-id` annotation pointing to the trace that proves it |
| Debugging "container won't start" requires: kubectl logs operator + grep MDT logs + look at topology dashboard | Click `cisco.vk/last-trace-id` annotation → Tempo → see `cvk.config.push` failed in `transport.Write` with `result=device-busy` |
| BGP flap on a neighbor: 1 isolated recovery span | BGP flap correlated to most recent `cvk.config.push` of a route-map config — operators see they caused it |

---

## 7. Risks and call-outs

- **Tempo long-running span support.** `cvk.device.heartbeat` may live for the operator's full uptime (hours/days). Validate against the deployed Tempo install — older Tempo defaults to closing un-finished spans after 5 min. Workaround: roll the heartbeat span on a 1-hour cadence (close + reopen), making each "heartbeat hour" a discrete trace.
- **Pod annotation update conflicts.** Concurrent Pod updates from us + virtual-kubelet upstream may conflict. Use `client.MergeFrom(base).Patch` with retry on `IsConflict`.
- **Trace ID leakage to devices.** We never push trace IDs to devices. Devices have no concept of our trace plane. All correlation is operator-side.
- **Cardinality explosion on `cvk.causal.unknown=true`.** This attribute is intentionally low-cardinality (boolean), but if downstream metrics-from-traces extracts it as a label, it could grow if many transitions arrive outside windows. Monitor.
- **Multi-replica HA.** When `controller.leaderElect=true`, only the leader emits operator-side spans for a given device. Standby replicas should still emit `cvk.controller.lease.lost` spans for diagnostic purposes but not full reconcile traces — would otherwise duplicate.
- **`service.instance.id` consistency.** The provisioning trace is in the system controller's pod; the topology + MDT traces are in the per-device pod. Two `service.instance.id`s appear on the same trace. Acceptable — it's literally the truth — but Grafana service maps will show both as nodes. Consider a derived `cvk.process.role` attribute to disambiguate.

---

## 8. Pre-requisite cleanups (do these first or interleaved)

- Add `Tracer` field + injection harness to all reconcilers (Phase A scaffolding).
- Wire system controller to OTLP endpoint with its own POD_UID-based `service.instance.id` (Phase 0 follow-up).
- Build a small `internal/causal` package with the correlation cache + annotation read/write helpers — both Phase B and Phase D depend on it.
- Add a `BenchmarkTracerOverhead` to confirm operator-side spans don't measurably regress reconcile latency. Acceptable budget: < 100µs / reconcile.

---

## 9. References

- W3C Trace Context: https://www.w3.org/TR/trace-context/
- OTel SemConv for k8s: https://opentelemetry.io/docs/specs/semconv/resource/k8s/
- Tempo span-link querying: https://grafana.com/docs/tempo/latest/operations/architecture/#span-links
- Existing topology emitter: [internal/provider/otel_topology.go](internal/provider/otel_topology.go)
- Existing MDT TracesEmitter: [internal/telemetry/emit/traces.go](internal/telemetry/emit/traces.go)
- Existing telemetry runbook: [docs/telemetry.md](docs/telemetry.md)
- Telemetry branch review: [docs/telemetry-branch-review.md](docs/telemetry-branch-review.md)
