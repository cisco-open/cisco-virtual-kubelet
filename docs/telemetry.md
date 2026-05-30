# IOS-XE Telemetry

!!! warning "Beta"
    `IOSXETelemetry` is **Beta** (`v1alpha1`). The MDT pipeline architecture
    and gNMI subscription mechanics are stable; the subscription schema and
    OpenTelemetry output shapes may change between releases. Evaluate in
    non-production environments before broader rollout.

The `IOSXETelemetry` CRD declares MDT-over-gNMI subscriptions for one
`CiscoDevice` and converts them into OpenTelemetry signals.

## Phase 1 — Subscription lifecycle

- `IOSXETelemetry` CRD: create, update, delete, status reporting
- Per-device subscriber in the `cvk-<device>` pod
- Dedicated gNMI `Subscribe` client connection (separate from the config
  driver's gNMI conn)
- STREAM subscriptions with `SAMPLE`, `ON_CHANGE`, or `TARGET_DEFINED`
  stream modes
- Multiplexing compatible subscriptions into one gNMI Subscribe RPC per
  `(encoding, sampleInterval)` bucket
- Bounded notification buffering with `buffer_overflow` drops counted in
  status
- Reconnect with exponential backoff (spec.reconnect)
- Per-stream `Path.Origin` populated from path syntax (first element's
  YANG-module prefix) or the explicit `subscriptions[].origin` field
- `subscriptions[].preservePathPrefix` keeps the module prefix on the first
  `PathElem.Name` instead of lifting it into `Path.Origin`. Required for
  IOS-XE native YANG paths because IOS-XE gnxi rejects `Cisco-IOS-XE-*`
  values as `Path.Origin` and instead expects RFC 7951-style
  module-qualified element names. Leave unset for OpenConfig paths.

  | Path style | spec.subscriptions[] |
  |---|---|
  | OpenConfig | `paths: ["/interfaces/interface/state"]` and `origin: openconfig` |
  | IOS-XE native | `paths: ["/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/apps"]` and `preservePathPrefix: true` |

## Phase 2 — Mapper, log emission, and OTel provider stack

Phase 2 adds the data path that turns each gNMI notification into an
OpenTelemetry record. Phase 2 shipped logs; Phase 3 extends the same
mapper output to metrics.

### Mapper

`internal/telemetry/mapper` is a pure library. It converts raw
`*gpb.Notification` values into `MappedEvent`s ready for emission, with
zero side effects:

- `FlattenPath` joins `prefix + path` and preserves the wire order of
  `PathElem` keys (no re-sorting)
- `Filter` runs in two stages: `wirePath` allow/deny on raw flattened
  paths (load-shed before alias); `metricName` allow/deny on the
  user-facing emitted name (semantic filtering after alias)
- `AliasResolver` matches longest-prefix-wins
- `ResourceAttrExtractor` reads pinned leaves into resource attributes
- `SeriesKeyCache` enforces `cardinalityLimits.maxSeriesPerSubscription`
  with `dropNewSeries` semantics (existing series keep flowing; new
  series after the cap are suppressed and counted into
  `droppedEvents.cardinality_limit`)
- Severity inference for log bodies: `UP`/`ESTABLISHED` → INFO, `DOWN`
  → WARN, `critical`/`error` → ERROR
- Timestamp policy honors `timestamps.useCollectorTimestamp`
  (default true): collector wall clock on the OTel record, device
  timestamp on `cisco.device.timestamp`

The mapper has no implicit driver dependency and is importable as a
standalone package — a future OpenTelemetry Collector receiver can
consume it directly.

### Logs emitter

`internal/telemetry/emit` writes OTel `LogRecord`s for:

- string/ASCII leaves (the body becomes the leaf value)
- `Delete` notifications (body: `deleted: <path>`, severity Info)

Per-record attributes carry the canonicalized `PathElem` keys.

### Three-provider OTel stack

`internal/otelproviders` constructs `TracerProvider`, `MeterProvider`,
and `LoggerProvider` over a single OTLP gRPC connection. Endpoint and
TLS are read from the standard SDK environment:

- `OTEL_EXPORTER_OTLP_ENDPOINT` (e.g. `otelcol.observability:4317`)
- `OTEL_EXPORTER_OTLP_INSECURE=true` to disable TLS

Phase 2 wires `Providers.Logger` to the `IOSXETelemetryReconciler` so
each subscriber emits log records via the shared exporter.

When `OTEL_EXPORTER_OTLP_ENDPOINT` is unset the providers are nil and
the emitters fall back to noop providers (no records leave the process).

### Status surface

`status.observedSubscriptionState[].logRecordsEmitted` reports the
running count of OTel log records emitted for that subscription.

## Phase 3 — Metrics GA

Phase 3 emits numeric MDT leaves as OpenTelemetry metrics. The mapper
creates `MappedEvent{Signal: metrics}` for `IntVal`, `UintVal`,
`FloatVal`, `DoubleVal`, `DecimalVal`, and JSON/JSON-IETF values whose
root value is numeric.

### Metric classifier

The reconciler builds the classifier chain per `IOSXETelemetry` CR:

```text
OverrideClassifier(spec.mapping.metricTypeOverrides, CuratedClassifier())
```

`metricTypeOverrides` use longest-prefix-wins matching, so operators can
pin a subtree to `gauge` or `sum` without waiting for curated defaults.
The curated classifier marks well-known monotonic counters as `sum`,
including OpenConfig interface counters, IOS-XE interface statistics,
BGP message counters, TCP/UDP packet counters, and app-hosting network
counters. CPU, memory, temperature, power, and PoE readings are gauges.
Unknown numeric paths default to gauge.

### Metrics emitter

`MetricsEmitter` records:

- `gauge` events with OTel `Float64Gauge.Record`
- `sum` events with OTel `Float64Counter.Add`

gNMI counters arrive as cumulative values. The emitter stores the last
value per series key and emits only the positive delta. If the current
value is lower than the previous value, the emitter treats it as a
counter reset, records `cisco_vk_telemetry_counter_resets_total`, stores
the new baseline, and skips the negative point.

The mapper also tracks a `StartTimestamp` per `(streamEpoch, seriesKey)`.
Each new Subscribe RPC gets a fresh stream epoch, so downstream consumers
can distinguish counter segments after stream restarts.

### Self metrics

The metrics path registers these counters on `Providers.Meter`:

- `cisco_vk_telemetry_metric_points_emitted_total`
  (`device`, `subscription`, `kind`)
- `cisco_vk_telemetry_classifier_decisions_total`
  (`device`, `subscription`, `kind`)
- `cisco_vk_telemetry_counter_resets_total`
  (`device`, `subscription`, `metric`)

`status.observedSubscriptionState[].metricPointsEmitted` and
`GET /telemetry/health` report metric points emitted per subscription.

## Phase 4 — YANG classification and transition traces

Phase 4 adds optional YANG-driven metric classification, recovery spans
for watched state transitions, and a shared trace provider path for
topology spans.

### YANG-driven classification

When `YANG_MODELS_DIR` points at a directory of `.yang` files, the
`cvk-<device>` process loads those modules once and compiles resolved
leaf types into the metric classifier chain:

```text
OverrideClassifier(
  spec.mapping.metricTypeOverrides,
  YangClassifier(yangRegistry, fallback: CuratedClassifier()),
)
```

The YANG loader resolves typedefs, imports, groupings, uses, leaves,
leaf-lists, containers, and lists. Types named `counter32` or
`counter64` classify as `sum`; integer, unsigned integer, decimal64,
string, and enumeration leaves classify as `gauge`. Unknown or
unresolved paths fall through to the curated classifier, so users
without `YANG_MODELS_DIR` keep the Phase 3 behavior.

YANG lookups are memoized in a bounded registry cache. The default cap
is 4096 compiled `(module, leafPath)` lookups; on cap hit, the cache
resets all entries rather than doing per-entry LRU eviction.

### State-transition spans

`spec.mapping.transitions` declares state leaves to watch. The mapper
emits trace candidate events for those paths when `output.signal`
includes `traces`. The `TracesEmitter` tracks each `(path, key-set)`
independently:

- healthy → unhealthy records the observation timestamp
- unhealthy → healthy emits a span from the latest unhealthy
  observation to the healthy observation
- healthy-only observations do not emit spans

Recovery span names use `state.transition.<aliasedName>`. Span
attributes include `from-state`, `to-state`, `duration`,
`cisco.gnmi.path`, and the path keys such as interface `name`.

The emitter also records
`cisco_vk_telemetry_state_transitions_total` on `Providers.Meter` with
`device`, `subscription`, `path`, `from`, and `to` attributes.

### Topology trace consolidation

The topology exporter now accepts a shared `trace.TracerProvider`.
When the MDT telemetry provider stack exists, topology spans use
`otelproviders.Providers.Tracer` and keep their own instrumentation
scope, `cisco-virtual-kubelet/topology`. When no shared provider is
available, the exporter preserves the older behavior and builds its own
per-device OTLP trace provider from `device.otel`.

When topology uses the shared provider, endpoint, TLS, headers, and
shutdown are owned by `internal/otelproviders`; topology-specific data
remains on span attributes and scope name rather than a separate
topology-only resource.

## Phase 2 example

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXETelemetry
metadata:
  name: c9300x-state
  namespace: network
spec:
  deviceRef:
    name: c9300x-01
  subscriptions:
    - name: interface-state
      enabled: true
      origin: openconfig
      paths:
        - /interfaces/interface/state
      mode: STREAM
      streamMode: SAMPLE
      sampleInterval: 10s
      encoding: PROTO
  reconnect:
    initialBackoff: 1s
    maxBackoff: 30s
  cardinalityLimits:
    maxSeriesPerSubscription: 10000
    onExceeded: dropNewSeries
  timestamps:
    useCollectorTimestamp: true
  mapping:
    pathAliases:
      - prefix: /interfaces/interface/state
        rename: oc.interface.state
    resourceAttributes:
      - path: /interfaces/interface/state/name
        key: cisco.interface.name
    filter:
      wirePath:
        deny:
          - "**/last-change-time"
      metricName:
        allow:
          - "oc.interface.state.*"
  output:
    signal:
      - logs
```

## Phase 3 example

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXETelemetry
metadata:
  name: c9300x-interface-metrics
  namespace: network
spec:
  deviceRef:
    name: c9300x-01
  subscriptions:
    - name: interface-counters
      enabled: true
      origin: openconfig
      paths:
        - /interfaces/interface/state/counters
      mode: STREAM
      streamMode: SAMPLE
      sampleInterval: 30s
      encoding: PROTO
    - name: cpu-memory
      enabled: true
      paths:
        - /Cisco-IOS-XE-process-cpu-oper:cpu-usage/cpu-utilization
        - /Cisco-IOS-XE-memory-oper:memory-statistics
      mode: STREAM
      streamMode: SAMPLE
      sampleInterval: 30s
      encoding: PROTO
  mapping:
    pathAliases:
      - prefix: /interfaces/interface/state/counters
        rename: cvk.interface.counters
    metricTypeOverrides:
      - prefix: /Cisco-IOS-XE-process-cpu-oper:cpu-usage/cpu-utilization
        type: gauge
      - prefix: /interfaces/interface/state/counters
        type: sum
  output:
    signal:
      - metrics
```

## Phase 4 example

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXETelemetry
metadata:
  name: c9300x-full-otel
  namespace: network
spec:
  deviceRef:
    name: c9300x-01
  subscriptions:
    - name: interface-counters
      enabled: true
      origin: openconfig
      paths:
        - /interfaces/interface/state
        - /interfaces/interface/state/counters
      mode: STREAM
      streamMode: SAMPLE
      sampleInterval: 30s
      encoding: PROTO
    - name: bgp-state
      enabled: true
      paths:
        - /network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/session-state
      mode: STREAM
      streamMode: ON_CHANGE
      encoding: PROTO
  mapping:
    pathAliases:
      - prefix: /interfaces/interface/state
        rename: oc.interface.state
      - prefix: /network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state
        rename: oc.bgp.neighbor.state
    transitions:
      - path: /interfaces/interface[name=*]/state/oper-status
        healthyValues:
          - UP
        unhealthyValues:
          - DOWN
          - LOWER_LAYER_DOWN
      - path: /network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor[neighbor-address=*]/state/session-state
        healthyValues:
          - ESTABLISHED
        unhealthyValues:
          - IDLE
          - ACTIVE
          - CONNECT
    metricTypeOverrides:
      - prefix: /interfaces/interface/state/counters
        type: sum
  output:
    signal:
      - logs
      - metrics
      - traces
```

## Worked examples

Ready-to-apply IOSXETelemetry CRs for common C9300X MDT-over-gNMI use cases:

- [Environmental sensors](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/iosxetelemetry/c9300x-environmental.yaml)
- [Interface counters and oper-status transitions](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/iosxetelemetry/c9300x-interfaces-counters.yaml)
- [BGP and OSPF counters plus adjacency transitions](https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/examples/iosxetelemetry/c9300x-bgp-and-ospf.yaml)

## Status output examples

After applying an `IOSXETelemetry` CR, watch the subscription come up:

```bash
$ kubectl get iosxetelemetry -w
NAME               DEVICE        PHASE     AGE
cat9k-interfaces   cat9k-smoke   Active    8s
```

Full status showing per-subscription health and buffer stats:

```bash
$ kubectl describe iosxetelemetry cat9k-interfaces
Name:         cat9k-interfaces
Namespace:    default
API Version:  config.cisco.vk/v1alpha1
Kind:         IOSXETelemetry
Spec:
  Device Ref:
    Name:  cat9k-smoke
  Subscriptions:
    Paths:
      /interfaces/interface/state/counters
    Sample Interval:  30000000000
Status:
  Conditions:
    Last Transition Time:  2026-05-30T10:00:08Z
    Message:               1 subscription active
    Reason:                Active
    Status:                True
    Type:                  Ready
  Phase:  Active
  Subscription Stats:
    Active Subscriptions:  1
    Buffer Overflow Total: 0
    Notifications Total:   142
Events:
  Type    Reason     Age   Message
  ----    ------     ----  -------
  Normal  Subscribed  8s   gNMI Subscribe stream established for /interfaces/interface/state/counters
```

When a subscription error occurs the phase transitions to `Degraded` and
the condition message carries the gRPC status code:

```bash
$ kubectl describe iosxetelemetry cat9k-interfaces
...
Status:
  Conditions:
    Last Transition Time:  2026-05-30T10:05:32Z
    Message:               rpc error: code = Unavailable desc = transport is closing
    Reason:                SubscriptionError
    Status:                False
    Type:                  Ready
  Phase:  Degraded
Events:
  Type     Reason       Age   Message
  ----     ------       ----  -------
  Warning  StreamError  12s   gNMI stream lost, reconnecting (attempt 1/5, backoff 2s)
  Normal   Subscribed   8s    gNMI Subscribe stream re-established
```

Metrics emitted to your OpenTelemetry collector carry the device name and
subscription path as attributes, for example:

```
# Prometheus remote-write preview (interface counters)
interfaces_interface_state_counters_in_octets{
  device="cat9k-smoke",
  interface_name="GigabitEthernet1/0/1"
} 1234567890 1748599200000
interfaces_interface_state_counters_out_octets{
  device="cat9k-smoke",
  interface_name="GigabitEthernet1/0/1"
} 987654321 1748599200000
```

## Future work

- Add curated transition presets for common IOS-XE and OpenConfig
  operational state paths.
