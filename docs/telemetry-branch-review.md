<!--
Copyright 2026 Cisco Systems Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
-->

# CVK MDT-over-gNMI → OpenTelemetry: Branch Review

**Scope:** `pr/johalley/mdt-gnmi-full` vs `main`
**Head:** `d7a61f8` (after the v1-blocker batch)
**Footprint:** 78 files changed, ~15,361 insertions, ~108 deletions

---

## 1. Executive summary

This branch introduces a **full MDT-over-gNMI → OpenTelemetry telemetry pipeline** to Cisco Virtual Kubelet — a feature that does not exist on `main` at all. The pipeline lets operators express telemetry intent as Kubernetes CRDs, opens gNMI Subscribe RPCs to IOS-XE devices, maps notifications to OTel logs / metrics / traces, and exports them via OTLP gRPC.

The work was delivered across five formal phases (1–5) plus three follow-on commits that close architectural gaps surfaced by joint review with Codex. The pipeline is verified end-to-end against `cat9k-smoke` (10.1.1.1) with Prometheus / Loki / Jaeger / Grafana on `ux-agx`.

### Headline capabilities

| Capability | Status |
|---|---|
| `IOSXETelemetry` CRD with subscription, mapping, cardinality, output spec | ✅ Shipped |
| Per-device gNMI Subscribe lifecycle (open / drain / reconnect / cancel) | ✅ Shipped |
| Notification → OTel logs (string leaves, deletes) | ✅ Shipped |
| Notification → OTel metrics (numeric leaves, sum/gauge classifier) | ✅ Shipped |
| Notification → OTel state-transition spans | ✅ Shipped |
| Per-entity scoped resource attributes (k8s-style container view) | ✅ Shipped |
| List-key-stripped metric names by default; keys as labels | ✅ Shipped |
| Configurable instrument cap with operator-visible cap-drop self-metric | ✅ Shipped |
| Per-CR mapping profile isolation (multi-CR / multi-tenant safe) | ✅ Shipped |
| Path-level Subscribe RPC dedup | ✅ Shipped |
| OTel SemConv resource identity (k8s.pod.*, service.instance.id, host.name) | ✅ Shipped |
| Bounded exporter queues + drop visibility | ✅ Shipped |
| Processing-duration histogram + transition span rate limiter | ✅ Shipped |
| Helm: YANG ConfigMap mount, downward API env, leader-election guard | ✅ Shipped |
| Grafana + Splunk dashboards | ✅ Shipped |

### Out of scope for this branch

- Native OTel histogram emission for distribution metrics (deferred — see §9)
- Trace exemplars on metric data points (deferred — production receiver flagged exemplar storage cardinality risk)
- IOS-XR / NX-OS telemetry parity (driver registers exist but only IOS-XE is wired)
- Curated `IOSXETelemetryProfile` CRD for reusable subscription bundles (deferred to v1beta1)

---

## 2. Architecture

```
┌──────────────────┐     gNMI Subscribe      ┌────────────────┐
│ IOS-XE device    │◄─────────────────────── │ Subscriber     │
│ (gnxi 50052)     │  (TLS / token auth)     │ /streamMgr     │
└────────┬─────────┘                         └────────┬───────┘
         │ Notification (proto/JSON-IETF)             │
         ▼                                            ▼
   ┌──────────┐    ┌────────────────────────────────────────────┐
   │ events ch│    │ drainEvents → per-subscription profile     │
   └────┬─────┘    │   ↓                                        │
        │          │ mapper.Process (per Notification)          │
        │          │   FlattenPath → AliasResolver →            │
        │          │   classifier.Classify → SeriesKeyCache →   │
        │          │   ResourceAttrExtractor (scoped) →         │
        │          │   metricLeafName / pathLabelAttributes     │
        │          │   ↓                                        │
        │          │ MappedEvent[]   { Logs | Metrics | Trace } │
        │          └────────┬─────────┬──────────┬──────────────┘
        │                   ▼         ▼          ▼
        │             LogsEmitter  MetricsEmit  TracesEmit
        │              (OTLP log)   (OTLP gauge/  (OTLP span,
        │                            sum + cap     rate-limited)
        │                            self-mtrcs)
        ▼
        ▶  OTLP gRPC connection (gzip, 64 MiB max msg, batched)
                 ▼
        ┌──────────────────────────────────┐
        │ otel-collector / Prometheus /    │
        │ Loki / Tempo                     │
        └──────────────────────────────────┘
```

**Process boundary.** The pipeline runs in the **per-device VK pod** (`cat9k-smoke-vk-…`), not the system controller. The system controller (`cisco-vk-system`) only watches the `IOSXETelemetry` CR and propagates env vars; the per-device pod owns the gNMI Subscribe RPC, the mapper, the emitters, and the OTLP connection.

**Mapper-as-library.** `internal/telemetry/mapper` has zero driver dependencies. A future custom OpenTelemetry Collector receiver can import it directly without pulling in cisco-vk runtime.

---

## 3. CRD surface (`IOSXETelemetry` v1alpha1)

Defined at [api/config/v1alpha1/iosxetelemetry_types.go](api/config/v1alpha1/iosxetelemetry_types.go); validation helpers at [api/config/v1alpha1/iosxetelemetry_validation.go](api/config/v1alpha1/iosxetelemetry_validation.go).

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXETelemetry
metadata: {name: melt, namespace: cisco-vk-smoke}
spec:
  deviceRef: {name: cat9k-smoke}             # CEL: must be non-empty
  subscriptions:                             # MaxItems=64; +listType=map listMapKey=name
    - name: app-hosting-oper                 # DNS-1123, MaxLength=63
      enabled: true                          # default true
      origin: rfc7951                        # gNMI Path.Origin (or empty for native)
      preservePathPrefix: true               # keep "Cisco-IOS-XE-*" on PathElem (IOS-XE native)
      paths: ["/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"]   # MaxItems=256, +listType=set
      mode: STREAM                           # only STREAM in v1alpha1
      streamMode: SAMPLE                     # SAMPLE | ON_CHANGE | TARGET_DEFINED
      sampleInterval: 30s
      heartbeatInterval: 60s                 # CEL: only valid with ON_CHANGE
      suppressRedundant: true                # CEL: only valid with ON_CHANGE
      encoding: PROTO                        # PROTO | JSON_IETF
  reconnect:                                 # exponential backoff, default 1s/30s/forever
    initialBackoff: 1s
    maxBackoff: 30s
    maxRetries: 0                            # 0 = retry forever
  cardinalityLimits:
    maxSeriesPerSubscription: 5000           # mapper-side per-subscription cap (dropNewSeries)
    maxInstruments: 1024                     # emitter-side instrument-name cap (cap-drops self-metric)
    onExceeded: dropNewSeries                # only value in v1alpha1
  timestamps:
    useCollectorTimestamp: true              # default true; device ts → cisco.device.timestamp attr
  mapping:
    includeListKeysInMetricName: false       # default false: list keys → labels, names collapse
    pathAliases:                             # MaxItems=512
      - {prefix: "/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data", rename: cvk.app_hosting}
    metricTypeOverrides:                     # MaxItems=512
      - {prefix: /interfaces/interface/state/counters, type: sum}
    transitions:                             # MaxItems=256
      - path: /interfaces/interface[name=*]/state/oper-status
        healthyValues: [UP]
        unhealthyValues: [DOWN, LOWER_LAYER_DOWN]
    resourceAttributes:                      # MaxItems=512; per-entity scoped
      - {path: ".../app/details/state",       key: cisco.app_hosting.state}
      - {path: ".../app/network-interfaces/network-interface/ipv4-address",
                                              key: cisco.app_hosting.ipv4_address}
    filter:
      wirePath:    {allow: [...], deny: [...]}   # raw flattened paths
      metricName:  {allow: [...], deny: [...]}   # post-alias names
  output:
    signal: [logs, metrics, traces]
status:
  phase: Streaming                           # Pending | Streaming | Degraded | Failed
  conditions:
    - {type: Ready, status: "True", reason: Streaming, ...}
    - {type: InstrumentCapExceeded, status: "True", reason: MetricInstrumentsDropped,
       message: "metric points dropped because the instrument cap was reached (cumulative=N); raise spec.cardinalityLimits.maxInstruments"}
  observedSubscriptionState:
    - {name: app-hosting-oper, streamID: proto-30s-4, messagesReceived: 21,
       logRecordsEmitted: 2040, metricPointsEmitted: 10568, droppedEvents: {cardinality_limit: 20230},
       reconnects: 0, currentBackoff: 0s, lastError: ""}
```

**Validation hardening (v1-blocker batch):**
- `deviceRef.name` CEL-validated non-empty
- All `Subscription` / `PathAlias` / `MetricTypeOverride` / `ResourceAttribute` fields require `MinLength=1`
- List caps: 64 subscriptions, 256 paths, 512 aliases, 256 transitions
- `paths` is `+listType=set` so the apiserver rejects duplicates server-side

---

## 4. Runtime pipeline (file walk)

### 4.1 Reconciler — [internal/provider/iosxetelemetry_reconciler.go](internal/provider/iosxetelemetry_reconciler.go)

Owns the CR watch and per-CR lifecycle. Constructs a single `Subscriber` per device (lazy on first matching CR), then for each CR reconcile:
- Validates `cr.Spec` via `configv1alpha1.ValidateIOSXETelemetry`.
- Pushes a `MappingProfile` per subscription via `sub.SetSubscriptionProfile(name, profile)` — **per-CR isolation**, multiple CRs on one device cannot stomp each other.
- Calls `applyDesired` to add/remove subscriptions and clean up profile entries when CRs are deleted.
- Computes status from `sub.StatusFor`, layers in the `InstrumentCapExceeded` condition based on `selfMetrics.CapDropTotal()`.

### 4.2 Subscriber + StreamManager — [internal/drivers/iosxe/telemetry/subscriber.go](internal/drivers/iosxe/telemetry/subscriber.go), [stream_manager.go](internal/drivers/iosxe/telemetry/stream_manager.go)

- One gNMI client connection per device (separate from the config-driver's connection).
- `StreamManager` buckets compatible subscriptions on `(encoding, sampleInterval)` and runs one Subscribe RPC per bucket.
- **Path dedup** (v1-blocker): within a bucket, identical `(path, mode, suppressRedundant, heartbeat, origin, preservePrefix)` tuples generate **one** wire-level `gpb.Subscription`; multi-CR fan-out is preserved via `pathBySub`.
- Reconnect with exponential backoff (`reconnect.go`); `active_streams` and `stream_reconnects_total` self-metrics increment from the Subscribe lifecycle.
- `drainEvents` pulls notifications, looks up the per-subscription profile, calls `mapper.Process`, then dispatches to logs / metrics / traces emitters.

### 4.3 Mapper — [internal/telemetry/mapper](internal/telemetry/mapper)

| File | Role |
|---|---|
| `path.go` | `FlattenPath(prefix, path)` joins gNMI paths into a canonical string and emits `ListKeyTuple` (the full nested-list trail). `stripOriginPrefix` strips `module:` from elements. |
| `alias.go` | `AliasResolver` longest-prefix-wins; pre-strips list keys so prefix matches across all instances. |
| `attrs.go` | `ResourceAttrExtractor` reads pinned leaves into resource attributes. `ExtractByEntity` groups by full list-key scope; `entityScopeKey` produces stable scope keys for nested lists. |
| `series.go` | `SeriesKeyCache` enforces `maxSeriesPerSubscription` (dropNewSeries semantics). |
| `starts.go` | `StartTimestampCache` per-`(streamEpoch, seriesKey)` for OTel cumulative counters. |
| `filter.go` | Two-stage allow/deny — `wirePath` (pre-alias raw flattened paths) and `metricName` (post-alias). |
| `mapper.go` | The `Process` orchestration. For each Update: flatten path → strip list keys for name → alias resolve → resourceForEntity (longest-match scope merge) → classify → produce `MappedEvent` per signal. |

**Key behavioral guarantees (v1-blocker batch):**
- `eventName` strips `[k=v]` from canonical before alias / metric naming, so `app[name=c9ktest]/details/cpu` and `app[name=cvk0000]/details/cpu` collapse to a single instrument with `app_name` as a label.
- `ListKeyTuple` plumbs nested-list identity through every event. `pathLabelAttributes` produces list-qualified label names (`app_name`, `storage_util_name`) when multiple lists are present, plain `name` when a single list. Prevents BGP-per-VRF cross-talk.
- `resourceForEntity` walks longest→shortest scope prefixes and merges per-scope attrs; outer-list attrs flow to inner-list events.

### 4.4 Emitters — [internal/telemetry/emit](internal/telemetry/emit)

| File | Role |
|---|---|
| `logs.go` | `LogsEmitter.Emit` writes OTel `LogRecord` per log-shaped event. Increments `cisco_vk_telemetry_log_records_emitted_total` via `SelfMetrics`. |
| `metrics.go` | `MetricsEmitter.Emit` records gauges and sums. Counter delta tracking + reset detection (`cisco_vk_telemetry_counter_resets_total`). Configurable instrument cap (`SetMaxInstruments`, defaults 1024). On cap hit, calls `selfMetrics.IncInstrumentCapDrops(metric=…)`. |
| `traces.go` | `TracesEmitter.Emit` opens recovery spans on healthy ← unhealthy transitions. **Per-(rule, entity) token bucket**: 100 tokens/min, capacity 100. Drops report via `cisco_vk_telemetry_transitions_dropped_total`. |
| `selfmetrics.go` | Shared `SelfMetrics` registry — owns the eight self-instruments. Thread-safe; nil-safe (every method short-circuits). |

### 4.5 Classifier — [internal/telemetry/classifier/classifier.go](internal/telemetry/classifier/classifier.go)

Layered: `OverrideClassifier(spec.metricTypeOverrides, YangClassifier(registry, fallback: CuratedClassifier()))`.
- Curated covers OpenConfig interfaces, IOS-XE interface stats, BGP messages, TCP/UDP, app-hosting; CPU/memory/temperature/power are gauges.
- YANG classifier (when `YANG_MODELS_DIR` is mounted) resolves typedefs; `counter32`/`counter64` → sum, integer/decimal/string/enum → gauge.
- Unknown paths default to gauge.

### 4.6 YANG registry — [internal/telemetry/yang](internal/telemetry/yang)

`parser.go` (goyang-based), `cache.go` (bounded — 4096 lookups; reset-on-cap), `registry.go` (per-`(module, leafPath)`), `classifier.go` (decision integration). Hot-load only when `YANG_MODELS_DIR` is set.

### 4.7 OTel provider stack — [internal/otelproviders/providers.go](internal/otelproviders/providers.go)

Single OTLP gRPC connection feeding three providers (Tracer/Meter/Logger). Production tuning:
- gzip compression (registered globally + per-call `WithCompressor("gzip")`)
- 64 MiB max send/recv message size (cumulative interface counters routinely exceed default 4 MiB)
- **Bounded queues + timeouts** (v1-blocker batch): trace `MaxQueueSize=8192`, batch=512, flush=5s, export=30s. Metric reader 15s/30s. Log batcher analogous.
- Endpoint normalization for bare `host:port` inputs. URL-scheme requirement of OTel SDK is back-filled with `http://` when `OTEL_EXPORTER_OTLP_INSECURE=true`.

---

## 5. Self-observability surface

The pipeline is heavily self-instrumented. All metrics carry `device` and `subscription` labels.

| Metric | Type | Source | Use |
|---|---|---|---|
| `cisco_vk_telemetry_active_streams` | UpDownCounter | StreamManager | how many gNMI Subscribe RPCs are open |
| `cisco_vk_telemetry_stream_reconnects_total` | Counter | StreamManager | Subscribe RPC reconnect attempts |
| `cisco_vk_telemetry_metric_points_emitted_total` | Counter | MetricsEmitter | per-subscription emission rate (RED method R) |
| `cisco_vk_telemetry_log_records_emitted_total` | Counter | LogsEmitter | per-subscription log emission rate |
| `cisco_vk_telemetry_classifier_decisions_total` | Counter | MetricsEmitter | gauge/sum/unclassified breakdown |
| `cisco_vk_telemetry_counter_resets_total` | Counter | MetricsEmitter | per-(device, sub, metric) counter resets |
| `cisco_vk_telemetry_state_transitions_total` | Counter | TracesEmitter | recovery spans emitted (with from/to/path) |
| `cisco_vk_telemetry_transitions_dropped_total` | Counter | TracesEmitter | spans rate-limited out by token bucket |
| `cisco_vk_telemetry_instrument_cap_drops_total` | Counter | MetricsEmitter | metric points dropped because instrument cap was hit (with `metric` label naming the dropped instrument) |
| `cisco_vk_telemetry_processing_duration_seconds` | Histogram | drainEvents | mapper + emitter latency (RED method D), buckets 100µs–10s |

Plus the CR `status.observedSubscriptionState[]` carries `messagesReceived`, `logRecordsEmitted`, `metricPointsEmitted`, `droppedEvents{reason}`, `reconnects`, `currentBackoff`, `lastError`.

CR conditions: `Ready` (Streaming/Failed/Degraded/Pending) and `InstrumentCapExceeded` (operator-actionable).

---

## 6. Operability

### Helm chart — [charts/cisco-virtual-kubelet](charts/cisco-virtual-kubelet)
- New top-level `telemetry` block (`otlp.endpoint`, `otlp.insecure`, `otlp.headers`, `yangModelsDir`, `yangModels.{configMapName,mountPath}`, `resourceAttributes`).
- Optional `collector.enabled` deploys a bundled OpenTelemetry Collector for smoke tests.
- **v1-blocker additions:** downward API env (POD_NAME / POD_NAMESPACE / POD_UID / NODE_NAME) on the controller and per-device pod; YANG ConfigMap volume mount; render-time validateLeaderElection helper that **fails the chart load** if `replicaCount > 1` with `controller.leaderElect=false`.
- `values.schema.json` updated to require `telemetry.yangModels`.

### Per-device pod template — [internal/controller/ciscodevice_controller.go](internal/controller/ciscodevice_controller.go)
`downwardAPIEnv()` injects pod identity into every per-device pod the controller creates; `propagatedTelemetryEnv()` mirrors controller env into pod env.

### Dashboards
- Grafana: [charts/cisco-virtual-kubelet/dashboards/grafana/cvk-mdt-overview.json](charts/cisco-virtual-kubelet/dashboards/grafana/cvk-mdt-overview.json) — k8s-style per-container view, app-hosting metadata-rich panels, classifier rate panels.
- Splunk: [charts/cisco-virtual-kubelet/dashboards/splunk/cvk-overview.json](charts/cisco-virtual-kubelet/dashboards/splunk/cvk-overview.json) — overview tiles (active streams, reconnects, transitions, counter resets).

### Diagnostic admin server — [internal/provider/diagnostic/adminserver/server.go](internal/provider/diagnostic/adminserver/server.go)
`GET /telemetry/health` — JSON snapshot of per-CR phase and per-subscription counters. Useful for ad-hoc kubectl exec curl checks.

### Worked examples — [examples/iosxetelemetry/](examples/iosxetelemetry/)
- `c9300x-environmental.yaml` — env sensors, fan/PSU
- `c9300x-interfaces-counters.yaml` — interface counters + oper-status transitions
- `c9300x-bgp-and-ospf.yaml` — protocol counters + adjacency transitions

### Runbook — [docs/telemetry.md](docs/telemetry.md), [docs/telemetry-cardinality.md](docs/telemetry-cardinality.md)

---

## 7. Delta vs main

### 7.1 Commit grouping (newest → oldest)

| Commit | Group | One-line |
|---|---|---|
| `d7a61f8` | v1 blockers | multi-CR isolation, k8s SemConv, exporter controls, span rate limit, CRD hardening, Helm guard |
| `85a76c0` | Mapper correctness | scoped entity attrs, list-key-stripped names, configurable instrument cap |
| `0f4b779`, `7b1846b`, `e61c989`, `0b6719b`, `062695d`, `1950fe8` | Phase 5 / dashboard iterations | Grafana + Splunk dashboards refined into k8s-style container view |
| `a46945e` | Phase 5 follow-on | per-entity resource attribute extraction (precursor to scoped entity keys) |
| `db0463b` | Phase 1 follow-on | `preservePathPrefix` opt for IOS-XE native YANG paths |
| `546b1ea`, `aa27720`, `5b82ef5`, `ce8ca84` | OTel exporter | gzip on connection, 64 MiB max msg, gzip via grpc.UseCompressor |
| `f7b63c0`, `b80fa02` | Operator override | `CISCO_VK_TELEMETRY_INSECURE` / `_PORT` env vars for lab installs |
| `2a5e841` | Bugfix | accept bare `host:port` for OTEL_EXPORTER_OTLP_ENDPOINT |
| `be8e3f6` | Test harness | live OTLP roundtrip smoke test |
| `d9f5d1c` | Phase 5 GA | Helm OTel wiring, optional collector chart, dashboards, examples, runbook |
| `de1b923` | Phase 4 | YANG classifier, transition trace spans, topology consolidation |
| `9a0f661` | Phase 3 | metrics emitter + counter classifier + counter-reset tracking |
| `d28e355`, `4d12967` | Phase 2 | wire mapper + logs emitter through subscriber + reconciler |
| `ee16cc6` | Test harness | live-device gNMI Subscribe smoke test |
| `d7f0e60` | Phase 1 hot-fixes | stream rebuild, status conflict, TLS downgrade, path origin, sample-mode validation |
| `1837ec7` | Phase 1 | `IOSXETelemetry` CRD + subscriber lifecycle |

### 7.2 New packages introduced

| Package | Purpose |
|---|---|
| `internal/telemetry/mapper` | gNMI Notification → MappedEvent (logs/metrics/traces). Pure library. |
| `internal/telemetry/emit` | OTel emitters (logs, metrics, traces, self-metrics). |
| `internal/telemetry/classifier` | Curated + override metric-type classifier. |
| `internal/telemetry/yang` | Optional YANG-driven classifier extension. |
| `internal/drivers/iosxe/telemetry` | Per-device subscriber, stream manager, reconnect logic, factory. |
| `internal/otelproviders` | Single-connection three-provider OTel SDK setup. |

### 7.3 Existing packages modified

- `api/config/v1alpha1` — new `IOSXETelemetry` CRD + validation.
- `internal/provider` — new `IOSXETelemetryReconciler`; `otel_topology` consolidated to share TracerProvider when telemetry is enabled.
- `internal/controller/ciscodevice_controller` — env propagation, downward API.
- `cmd/cisco-vk` — `telemetry_providers.go` builds the per-device OTel SDK; `config_reconciler.go` wires the reconciler into the manager.
- `charts/cisco-virtual-kubelet` — `telemetry` values block, optional collector subchart, downward API env, YANG ConfigMap, leader-election guard, dashboards, runbooks.

### 7.4 Test surface

- `mapper_test.go` (591 LoC), `metrics_test.go` (172 LoC mapper-side + 265 LoC emitter-side), `traces_test.go` (152 LoC), `logs_test.go` (175 LoC), `classifier_test.go`, `parser_test.go`, `subscriber_test.go`, `stream_manager_test.go`, `reconcile_test.go`, `iosxetelemetry_reconciler_test.go` (369 LoC).
- Behavioral coverage: list-key strip, alias-across-keys, scoped multi-list entity, BGP-per-neighbor, single-list back-compat, instrument cap drops, dynamic cap raise.
- Live tests gated behind build tags: `smoke_live_test.go` (real-device gNMI Subscribe), `uxagx_smoke_test.go` (real-collector OTLP roundtrip).

### 7.5 Lab verification matrix

| Capability | Verified against `cat9k-smoke` | Mechanism |
|---|---|---|
| Subscription lifecycle (open/recv/reconnect) | ✅ | 4 streams active, ~10K metric points/min |
| List-key stripped metric names | ✅ | Prometheus shows `cvk_app_hosting_app_details_resource_admission_cpu` (single instrument) |
| Scoped multi-list labels | ✅ | `app_name=c9ktest` + `storage_util_name=disk` on storage metrics |
| Per-entity resource attrs (k8s-style container view) | ✅ | 20 `cisco.app_hosting.*` labels populated |
| Instrument cap + cap-drop self-metric | ✅ | 87 cap-drop series with `metric=` naming what was dropped |
| `InstrumentCapExceeded` CR condition | ✅ | True with cumulative count message |
| k8s SemConv resource identity | ✅ | `k8s.pod.name`, `k8s.namespace.name`, `k8s.node.name`, `service.instance.id` (UID), `host.name`, `net.peer.name` flowing |
| Processing-duration histogram | ✅ | Per-subscription buckets populated |
| Helm leader-election guard | ✅ | `helm template --set replicaCount=2` fails with explanatory error |

---

## 8. Verified vs theoretical

**Verified end-to-end in lab (cat9k-smoke + ux-agx):** subscription lifecycle, list-key stripping, scoped attrs, instrument cap, cap-drop self-metric, `InstrumentCapExceeded` condition, k8s SemConv attrs, processing-duration histogram, Helm leader-election guard.

**Verified at unit-test level only (no production exercise):** path-level dedup of duplicate paths across CRs, multi-CR profile isolation (single CR + same-CR variations covered, true cross-CR conflict not stress-tested), transition span rate limiter token bucket, exporter queue overflow (we set bounds; have not stressed them).

**Implemented but quiet:** `cisco_vk_telemetry_stream_reconnects_total = 0` in lab because streams are stable. The instrument exists and the wire-up is correct, but real reconnect verification needs a device flap or network partition.

**Architectural acceptable / not exercised:** trace exemplars (we deliberately omit per Codex's review of receiver experience), histogram metric kind (deferred), `IOSXETelemetryProfile` (deferred), IOS-XR/NX-OS (drivers not wired).

---

## 9. Roadmap / pending items

Pulled from the joint architecture review (`/tmp/cvk-architecture-assessment.md` → Codex joint report). All v1-blockers are now landed. Remaining items group as follows:

### v1.0 follow-up (post-merge, low risk)

| Item | Why | Where |
|---|---|---|
| Cross-CR conflict integration test | Lock in the multi-CR isolation refactor with envtest exercise | `internal/provider/iosxetelemetry_reconciler_test.go` |
| `BenchmarkMapperProcess` with realistic-shape notification (~1000 updates, 3 nested lists) | Establish hot-path baseline; receiver lessons say compile-time regex / per-data-point allocs hurt at scale | `internal/telemetry/mapper/mapper_test.go` |
| Driver-namespace prefix for default canonical metric names | Prevent IOS-XR/NX-OS collision with IOS-XE when those drivers light up | `internal/telemetry/mapper/mapper.go`, `internal/drivers/{iosxr,nxos}/telemetry/` |
| Curated path heuristic fallback in classifier (`_total`, `_count`, `_packets`, `_octets` → counter; memory-bytes guard → gauge) | Receiver's hard-won heuristics improve accuracy for unknown paths without YANG files | `internal/telemetry/classifier/classifier.go` |
| `cisco_vk_telemetry_exporter_failures_total` self-metric | Today queue overflow is silent at the OTLP boundary; instrument export path with retry/timeout outcome counters | `internal/otelproviders/providers.go` |

### v1beta1 (deserves a spec-level evolution)

| Item | Why | Where |
|---|---|---|
| `IOSXETelemetryProfile` CRD (cluster-scoped) with `IOSXETelemetry.spec.profileRefs: [...]` | Curated bundles ("standard-interfaces", "app-hosting"); operators stop copy-pasting 50-line YAML | `api/config/v1beta1/` |
| Native OTel histogram emission for opted-in metrics (`metricTypeOverrides[].type: histogram` + `buckets: [...]`) | GrafanaCon native-histogram value: instant percentile queries, bounded cardinality vs Sum+rate() | `api/config/v1beta1/`, `internal/telemetry/emit/metrics.go` |
| Per-CR `maxInstrumentsContrib` + global `maxInstruments` admission | Explicit per-tenant cardinality contracts; KubeCon "We Deleted Half Our Metrics" | `api/config/v1beta1/`, admission webhook |
| Two-tier YANG distribution model (in-image curated + ConfigMap-supplied dynamic + JSON cache) | Match production receiver's pattern: 20+ pre-curated modules, RFC parser fallback, on-disk cache for fast restart | `internal/telemetry/yang/` |
| Trace exemplars on metric data points | Click-through from Grafana metric spike to the trace explaining it. Receiver's CHANGELOG flagged exemplar storage cardinality risk; gate behind a per-CR opt-in | `internal/telemetry/emit/metrics.go` |
| `kubectl describe` printer columns: subscription count, instrument-cap utilization %, last-error timestamp | Operator UX | `api/config/v1alpha1/iosxetelemetry_types.go` printer columns |

### v1.x / longer horizon

| Item | Why |
|---|---|
| IOS-XR + NX-OS telemetry parity | Drivers exist as placeholders; full pipeline parity needs dedicated work per driver. |
| Custom OTel Collector receiver | Push the mapper into a Collector receiver; CVK becomes a thin gNMI-to-OTLP bridge. Operators with existing Collector pipelines get drop-in compatibility. |
| Topology + telemetry trace correlation | Topology spans are cross-cluster k8s-resource-graph spans; transition spans are device state spans. Linking them via consistent `service.instance.id` would let operators see "container died → topology event → BGP recovered span" in one trace view. |
| `transitions` heuristic preset library | Operators shouldn't have to type out `oper-status: UP/DOWN/LOWER_LAYER_DOWN` for every interface CR. |
| Adaptive sampling on hot subscriptions | Receiver does not have this; we'd be ahead of the production state of the art. |

---

## 10. Risks and known limitations

| Risk | Mitigation |
|---|---|
| Trace exemplars unwired — Grafana cannot click-through from metric to trace | Acceptable for v1; deferred to v1beta1 with explicit cardinality gating. |
| Span rate limiter has no operator-tunable knob | Hard-coded 100/min/entity. Conservative for current traffic; expose as CRD field if drops become real. |
| Instrument cap is per-device subscriber, not global | Multi-device deployments with shared backends (Prometheus) carry the integration risk. Per-CR / global caps slated for v1beta1. |
| Multi-replica HA path requires `controller.leaderElect=true` | Helm guard prevents the misconfiguration but operators should explicitly opt in. |
| `OTEL_EXPORTER_OTLP_INSECURE` defaults to TLS but accepts bare `host:port`; could mis-route if endpoint is mistyped | Endpoint normalisation logs warnings; prod deployments should always set the full URL. |
| YANG file shipping is operator-supplied | No bundled `.yang` archive; operators provide the ConfigMap. Curated classifier covers the common path even without YANG files. |

---

## 11. References

- Architecture assessment + Codex joint review: `/tmp/cvk-architecture-assessment.md`
- Phase-by-phase runbook: [docs/telemetry.md](docs/telemetry.md)
- Cardinality tuning: [docs/telemetry-cardinality.md](docs/telemetry-cardinality.md)
- Worked examples: [examples/iosxetelemetry/](examples/iosxetelemetry/)
- Reference receiver compared against: `~/Git/otel-grpc-cisco-receiver`
- gNMI POC reference: `~/Git/otel-gnmi`
