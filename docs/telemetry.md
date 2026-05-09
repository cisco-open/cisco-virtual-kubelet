# IOS-XE Telemetry

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

## Phase 2 — Mapper, log emission, and OTel provider stack

Phase 2 adds the data path that turns each gNMI notification into an
OpenTelemetry record. The phase ships logs only; per-device metrics are
deferred to Phase 3.

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
`LogsEmitter` falls back to a noop `LoggerProvider` (no records leave
the process).

### Status surface

`status.observedSubscriptionState[].logRecordsEmitted` reports the
running count of OTel log records emitted for that subscription.

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

## What's still deferred

- Per-device metric emission (counters, gauges, classifier) — Phase 3
- Trace spans for state transitions (BGP up/down, link flap) — Phase 4
- RFC 6020/7950 YANG parser — Phase 4
- Topology trace exporter consolidation onto the shared
  `otelproviders` stack — Phase 4
