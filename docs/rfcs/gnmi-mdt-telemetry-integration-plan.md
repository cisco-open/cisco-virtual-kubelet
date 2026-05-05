# gNMI MDT dial-in telemetry integration plan

**Status:** implementation plan
**Depends on:** PR #111, `feat(driver): netascode-style IOS-XE configuration driver`
**Target branch after merge:** branch from `main` after PR #111 lands
**Primary packages touched:** `api/v1alpha1`, `cmd/cisco-vk`, `internal/provider`, `internal/drivers/iosxe/configdriver/transport`

## 1. Executive decision

Implement MDT dial-in telemetry as a CVK-native gNMI subscription feature on top of the gNMI transport introduced by PR #111.

The desired flow is:

```text
IOS-XE gnxi/gNMI server
        ^
        | gNMI Subscribe, initiated by CVK
        |
CVK telemetry manager
        |
        | normalize gNMI Notification -> path/value/key events
        v
CVK IOS-XE metric mapper
        |
        | curated YANG path mappings -> Prometheus metric families
        v
CVK metrics surface
        |
        | scrape
        v
OpenTelemetry Collector / Prometheus / Splunk / other backend
```

CVK is the component that converts MDT/gNMI updates into usable metrics. The OpenTelemetry Collector should not receive raw MDT/gNMI payloads in the first implementation. It should scrape or receive already-normalized metrics from CVK.

This keeps the operator experience aligned with the user's objective: reuse the PR #111 gNMI device channel and avoid deploying another device-facing telemetry protocol or another custom OTel Collector receiver.

## 2. Terminology

In this plan, "MDT dial-in" means controller-initiated telemetry collection using gNMI Subscribe. CVK dials the IOS-XE gNMI server, asks for operational-state paths, and receives a stream of updates.

This is different from the `jeremycohoe/otel-grpc-cisco-receiver` model, which is Cisco MDT gRPC dial-out using kvGPB. In dial-out, the device connects to a collector-side gRPC server. In this CVK plan, the device does not connect to the collector. CVK connects to the device.

## 3. What PR #111 provides

PR #111 gives us the foundation, but not the full telemetry data plane:

- `api/v1alpha1.DeviceSpec.Transport` can select `gnmi`.
- `internal/drivers/iosxe/configdriver/transport.GNMIConfig` and `NewGNMI` create a gNMI client.
- `transport.SubscribeCapable` exists for drift detection.
- `gnmiTransport.Subscribe` can open a gNMI STREAM subscription and return flattened `SubscribeEvent` values.
- `cmd/cisco-vk/config_reconciler.go` wires gNMI Subscribe into config drift reconciliation.

That existing Subscribe path should remain a drift signal. It is intentionally lossy and simple:

- one mode for all paths;
- no per-subscription sample interval;
- no heartbeat interval;
- no preserved gNMI timestamp;
- no preserved prefix/update split;
- no raw `TypedValue`;
- no subscription identity/profile metadata;
- event drops are acceptable because a single wake-up is enough for drift reconciliation.

Telemetry needs a richer stream contract. Do not overload the drift watcher.

## 4. Non-goals for v1

- Do not embed `otel-grpc-cisco-receiver` as a Collector receiver inside CVK.
- Do not add Cisco MDT gRPC dial-out listener support to CVK in v1.
- Do not emit every YANG leaf as a metric.
- Do not use path strings as open-ended Prometheus labels.
- Do not require direct OTLP metric push in the first release.
- Do not make telemetry subscriptions part of `IOSXEConfig` desired configuration. Telemetry is read-only observation, while `IOSXEConfig` is device mutation.

## 5. API design

Add a telemetry block to `CiscoDevice.spec` in `api/v1alpha1/types.go`.

Recommended shape:

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9k-smoke
  namespace: cisco-vk
spec:
  address: 10.1.1.1
  driver: XE
  username: cisco
  credentialSecretRef:
    name: cat9k-creds
  port: 443
  tls:
    enabled: true
    insecureSkipVerify: true
  transport: netconf

  telemetry:
    enabled: true
    mode: gnmi
    endpoint:
      port: 50052
      tls:
        enabled: false
    profiles:
      - iosxe-interfaces
      - iosxe-system
    resourceAttributes:
      site: lab
      role: access
    limits:
      maxSeries: 20000
      eventQueue: 4096
      staleAfter: 5m
    subscriptions:
      - name: interface-counters
        origin: openconfig
        path: /interfaces/interface/state/counters
        mode: SAMPLE
        sampleInterval: 30s
      - name: interface-state
        origin: openconfig
        path: /interfaces/interface/state/oper-status
        mode: ON_CHANGE
      - name: cpu
        origin: cisco-ios-xe
        path: /Cisco-IOS-XE-process-cpu-oper:cpu-usage/cpu-utilization
        mode: SAMPLE
        sampleInterval: 30s
```

Go types to add:

```go
type TelemetryConfig struct {
    Enabled bool `json:"enabled,omitempty" mapstructure:"enabled,omitempty"`
    Mode string `json:"mode,omitempty" mapstructure:"mode,omitempty"`
    Endpoint *TelemetryEndpoint `json:"endpoint,omitempty" mapstructure:"endpoint,omitempty"`
    Profiles []string `json:"profiles,omitempty" mapstructure:"profiles,omitempty"`
    ResourceAttributes map[string]string `json:"resourceAttributes,omitempty" mapstructure:"resourceAttributes,omitempty"`
    Limits *TelemetryLimits `json:"limits,omitempty" mapstructure:"limits,omitempty"`
    Subscriptions []TelemetrySubscription `json:"subscriptions,omitempty" mapstructure:"subscriptions,omitempty"`
}

type TelemetryEndpoint struct {
    Address string `json:"address,omitempty" mapstructure:"address,omitempty"`
    Port int `json:"port,omitempty" mapstructure:"port,omitempty"`
    TLS *TLSConfig `json:"tls,omitempty" mapstructure:"tls,omitempty"`
}

type TelemetrySubscription struct {
    Name string `json:"name,omitempty" mapstructure:"name,omitempty"`
    Origin string `json:"origin,omitempty" mapstructure:"origin,omitempty"`
    Path string `json:"path" mapstructure:"path"`
    Mode string `json:"mode,omitempty" mapstructure:"mode,omitempty"`
    SampleInterval metav1.Duration `json:"sampleInterval,omitempty" mapstructure:"sampleInterval,omitempty"`
    HeartbeatInterval metav1.Duration `json:"heartbeatInterval,omitempty" mapstructure:"heartbeatInterval,omitempty"`
    SuppressRedundant *bool `json:"suppressRedundant,omitempty" mapstructure:"suppressRedundant,omitempty"`
}

type TelemetryLimits struct {
    MaxSeries int `json:"maxSeries,omitempty" mapstructure:"maxSeries,omitempty"`
    EventQueue int `json:"eventQueue,omitempty" mapstructure:"eventQueue,omitempty"`
    StaleAfter metav1.Duration `json:"staleAfter,omitempty" mapstructure:"staleAfter,omitempty"`
}
```

Validation rules:

- `mode` enum: `gnmi` only for v1.
- subscription `mode` enum: `SAMPLE`, `ON_CHANGE`, `TARGET_DEFINED`.
- `sampleInterval` required for `SAMPLE`, default `30s` if omitted.
- `endpoint.address` defaults to `spec.address`.
- `endpoint.port` defaults to `50052` for IOS-XE `gnxi` insecure, but must not reuse `spec.port`.
- `endpoint.tls` is separate from `spec.tls` because `spec.tls` is currently apphosting RESTCONF-shaped.
- `limits.maxSeries` default should be conservative, for example `20000`.

Add status fields only after the manager exists:

```go
type TelemetryStatus struct {
    Enabled bool `json:"enabled,omitempty"`
    Connected bool `json:"connected,omitempty"`
    LastConnectedTime *metav1.Time `json:"lastConnectedTime,omitempty"`
    LastError string `json:"lastError,omitempty"`
    ActiveSeries int `json:"activeSeries,omitempty"`
    DroppedEvents int64 `json:"droppedEvents,omitempty"`
    DroppedSamples int64 `json:"droppedSamples,omitempty"`
}
```

Keep status updates rate-limited. Repeated stream reconnects must not create API-server write noise.

## 6. Device configuration model

Telemetry subscriptions should live under `CiscoDevice.spec.telemetry`, not under `IOSXEConfig`.

If CVK should also configure the IOS-XE `gnxi` prerequisite, use `spec.configPrereqs` or an operator-authored `IOSXEConfig` to apply the required device-side lines, for example:

```text
gnxi
gnxi server
```

Secure `gnxi` may require different device-side commands and a different port. That should be configured as prerequisite device config, while telemetry runtime dials through `spec.telemetry.endpoint`.

This gives a clean separation:

| Concern | CVK component |
|---|---|
| Enable `gnxi` on the device | `configPrereqs` or `IOSXEConfig` |
| Subscribe to operational state | `CiscoDevice.spec.telemetry` |
| Convert stream updates to metrics | CVK telemetry manager |
| Forward metrics to backend | Prometheus scrape / OTel Collector |

## 7. Transport changes

Add a second, richer subscription API beside `SubscribeCapable`.

Recommended file:

- `internal/drivers/iosxe/configdriver/transport/telemetry.go`

Recommended interface:

```go
type TelemetrySubscribeCapable interface {
    SubscribeTelemetry(ctx context.Context, req TelemetrySubscribeRequest) (<-chan TelemetryEvent, error)
}

type TelemetrySubscribeRequest struct {
    Subscriptions []TelemetrySubscriptionSpec
    Encoding string
    QueueSize int
}

type TelemetrySubscriptionSpec struct {
    Name string
    Origin string
    Path string
    Mode SubscribeMode
    SampleInterval time.Duration
    HeartbeatInterval time.Duration
    SuppressRedundant bool
}

type TelemetryEvent struct {
    Target string
    Subscription string
    Timestamp time.Time
    Origin string
    Prefix Path
    Path Path
    PathString string
    Value *gnmi.TypedValue
    JSON []byte
    Delete bool
    Sync bool
    Err error
}
```

Implementation details:

- Build a single gNMI STREAM `SubscriptionList` with per-subscription modes.
- Set `SampleInterval` in nanoseconds for `SAMPLE`.
- Set `HeartbeatInterval` and `SuppressRedundant` when requested.
- Preserve `Notification.Timestamp`.
- Preserve `Notification.Prefix` and each update/delete path.
- Preserve the raw `TypedValue`, not just JSON bytes.
- Emit `Sync` events from `SyncResponse`, so the manager can mark the stream initialized.
- Include a bounded output channel and explicit dropped-event metrics.
- Keep the existing `Subscribe(ctx, paths, mode)` behavior unchanged for drift detection.

This interface can initially live in the current `transport` package to minimize the PR #111 follow-up. A later cleanup can extract common gNMI client code out of `configdriver/transport` if the package name becomes misleading.

## 8. Telemetry manager

Add a provider-owned manager that runs for each enabled device.

Recommended package:

- `internal/provider/telemetry`

Recommended files:

- `manager.go`
- `config.go`
- `cache.go`
- `collector.go`
- `metrics.go`
- `mapper.go`
- `path.go`
- `profiles.go`

Responsibilities:

- Resolve telemetry config defaults.
- Resolve the device password using the same credential path as the existing driver.
- Build a gNMI telemetry subscriber using `spec.telemetry.endpoint`.
- Start one stream per device.
- Reconnect with exponential backoff and jitter.
- Convert each `TelemetryEvent` into zero or more metric samples.
- Store latest samples in a bounded in-memory metric cache.
- Expire stale series.
- Export samples through the existing CVK metrics surface.
- Record internal health metrics.
- Emit Kubernetes events only for meaningful state changes.

Suggested manager lifecycle:

```go
func (m *Manager) Run(ctx context.Context) {
    for ctx.Err() == nil {
        err := m.runStream(ctx)
        if ctx.Err() != nil {
            return
        }
        m.recordDisconnect(err)
        sleep := m.backoff.Next()
        select {
        case <-time.After(sleep):
        case <-ctx.Done():
            return
        }
    }
}
```

Start the manager from the per-device CVK runtime path in `cmd/cisco-vk/run.go`, near existing topology/OTEL startup. If aggregator mode needs telemetry later, add a manager-per-device worker there as a second phase.

## 9. Metric mapping

Use curated metric definitions, not a generic YANG leaf exporter.

Recommended definition:

```go
type MetricDef struct {
    Name string
    Help string
    Unit string
    Type MetricType
    Match PathMatcher
    Labels []LabelExtractor
    Value ValueExtractor
    Transform TransformFunc
}
```

Conversion rules:

- Normalize `prefix + update.path` into one canonical path.
- Extract YANG list keys into bounded labels, for example `interface`, `component`, `sensor`.
- Convert numeric `TypedValue` values directly to `float64`.
- Convert booleans to `0` or `1`.
- Convert enums only where the mapping is explicitly bounded.
- Drop unknown strings unless a metric definition explicitly maps them.
- Drop unsupported objects/arrays unless the profile has a known flattening rule.
- Never use full path, raw value, serial number, neighbor name, or error text as an unbounded label.

Initial profile set:

| Profile | Paths | Example metrics |
|---|---|---|
| `iosxe-interfaces` | OpenConfig or IOS-XE interface counters/state | `cisco_iosxe_interface_in_octets_total`, `cisco_iosxe_interface_out_octets_total`, `cisco_iosxe_interface_up` |
| `iosxe-system` | CPU and memory operational state | `cisco_iosxe_cpu_usage_percent`, `cisco_iosxe_memory_used_bytes`, `cisco_iosxe_memory_free_bytes` |
| `iosxe-environment` | temperature, fan, power supply where available | `cisco_iosxe_temperature_celsius`, `cisco_iosxe_fan_up`, `cisco_iosxe_power_supply_up` |
| `iosxe-poe` | PoE state and draw where available | `cisco_iosxe_poe_power_watts`, `cisco_iosxe_poe_port_admin_enabled` |

Example conversion:

```text
gNMI path:
/interfaces/interface[name=GigabitEthernet1/0/1]/state/counters/in-octets

value:
123456789

metric:
cisco_iosxe_interface_in_octets_total{
  device="cat9k-smoke",
  interface="GigabitEthernet1/0/1"
} 123456789
```

The metric mapper should include golden tests for every built-in metric.

## 10. Export path

Use Prometheus exposition first.

Preferred v1 export path:

```text
CVK telemetry manager -> in-memory metric cache -> CVK /metrics/resource -> OTel Collector scrape -> backend
```

Why `/metrics/resource` first:

- Existing CVK observability already serves device metrics there.
- The endpoint is per virtual node/device.
- The existing docs already point Prometheus users at kubelet resource metrics.
- It keeps telemetry data beside CPU/memory/interface metrics already exposed by `internal/provider/metrics.go`.

Implementation detail:

- Extend `AppHostingProvider` with an optional telemetry sample source.
- Merge telemetry metric families into `buildMetricsResource`.
- Keep existing non-telemetry metrics unchanged.
- Avoid blocking `/metrics/resource` on the gNMI stream. Scrapes read from cache only.

Secondary export path:

- Register internal telemetry health metrics on controller-runtime's `/metrics` when running the manager/controller form.
- Do not emit high-cardinality per-interface telemetry on the controller manager metrics endpoint unless the deployment mode is explicitly aggregator-owned and cardinality limits are enforced.

Direct OTLP metric export can be a later phase after Prometheus semantics, metric naming, and cardinality are stable.

Example OTel Collector shape:

```yaml
receivers:
  prometheus:
    config:
      scrape_configs:
        - job_name: cisco-vk-mdt
          metrics_path: /metrics/resource
          scrape_interval: 30s
          kubernetes_sd_configs:
            - role: node
          relabel_configs:
            - source_labels: [__meta_kubernetes_node_name]
              target_label: node

processors:
  batch: {}

exporters:
  otlp:
    endpoint: otel-backend.example:4317

service:
  pipelines:
    metrics:
      receivers: [prometheus]
      processors: [batch]
      exporters: [otlp]
```

The exact Kubernetes discovery block may differ by deployment mode. The contract CVK must satisfy is simpler: metrics are already Prometheus-shaped when scraped.

## 11. Internal metrics and health

Add CVK internal metrics for the telemetry subsystem:

```text
cisco_vk_telemetry_stream_up{device}
cisco_vk_telemetry_reconnects_total{device,reason}
cisco_vk_telemetry_events_total{device,subscription}
cisco_vk_telemetry_events_dropped_total{device,reason}
cisco_vk_telemetry_samples_total{device,profile,metric}
cisco_vk_telemetry_samples_dropped_total{device,reason}
cisco_vk_telemetry_active_series{device}
cisco_vk_telemetry_parse_errors_total{device,profile,reason}
cisco_vk_telemetry_last_timestamp_seconds{device,subscription}
```

Keep labels bounded. `reason`, `profile`, `metric`, and `subscription` must come from configured or enumerated values.

## 12. What to reuse from `jeremycohoe/otel-grpc-cisco-receiver`

Reuse ideas, not the main wire-protocol implementation.

Good candidates to borrow:

- Two-pass key propagation for list context before emitting leaf metrics.
- Cardinality discipline and explicit metric descriptors.
- Internal receiver health metrics.
- Capture/replay style test fixtures.
- A curated mapping mindset: path/value plus YANG context becomes metric name, labels, unit, and type.
- Any stable IOS-XE metric naming or dashboard conventions that are independent of kvGPB.

Avoid copying:

- Cisco MDT gRPC dial-out server.
- kvGPB protobuf decoding.
- OTel Collector receiver factory/lifecycle code.
- Splunk HEC/exporter-specific assumptions.
- Dynamic "parse any YANG tree into metrics" behavior for v1.

The protocol overlap is small. The semantic overlap is useful.

## 13. Implementation phases

### Phase 0: Rebase after PR #111 merge

Steps:

1. Update local `main`.
2. Create a feature branch, for example `codex/gnmi-mdt-telemetry`.
3. Run the full current test suite before changing code.
4. Confirm `transport.Subscribe` behavior remains unchanged.
5. Confirm gNMI port/TLS defaults from PR #111 are still present.

Acceptance:

- Baseline tests pass.
- The feature branch contains no unrelated PR #111 merge conflict cleanup.

### Phase 1: API and docs

Files:

- `api/v1alpha1/types.go`
- `api/v1alpha1/zz_generated.deepcopy.go`
- `docs/CONFIGURATION.md`
- `docs/observability.md`
- `docs/rfcs/deployment-modes.md`

Tasks:

1. Add `DeviceSpec.Telemetry *TelemetryConfig`.
2. Add endpoint, subscription, and limit types.
3. Add kubebuilder validation markers.
4. Generate CRDs/deepcopy.
5. Document the separation between `spec.transport` and `spec.telemetry.endpoint`.
6. Document that `spec.transport` controls config writes, while telemetry can use gNMI even when config writes use RESTCONF or NETCONF.

Acceptance:

- CRD generation succeeds.
- Existing `CiscoDevice` manifests remain valid.
- A telemetry-enabled manifest validates.

### Phase 2: Rich gNMI telemetry subscription

Files:

- `internal/drivers/iosxe/configdriver/transport/telemetry.go`
- `internal/drivers/iosxe/configdriver/transport/gnmi.go`
- `internal/drivers/iosxe/configdriver/transport/gnmi_test.go`
- `internal/drivers/iosxe/configdriver/transport/metrics.go`

Tasks:

1. Add `TelemetrySubscribeCapable`.
2. Implement `SubscribeTelemetry` on `gnmiTransport`.
3. Preserve timestamp, prefix, raw `TypedValue`, deletes, and sync responses.
4. Support per-subscription mode and sample interval.
5. Add queue overflow accounting.
6. Add bufconn tests for request construction and event flattening.

Acceptance:

- Existing drift Subscribe tests still pass.
- New tests prove `SAMPLE` intervals are sent.
- New tests prove timestamp and keys survive flattening.
- Stream errors surface as terminal events.

### Phase 3: Telemetry manager and metric cache

Files:

- `internal/provider/telemetry/manager.go`
- `internal/provider/telemetry/cache.go`
- `internal/provider/telemetry/config.go`
- `internal/provider/telemetry/metrics.go`
- `cmd/cisco-vk/run.go`

Tasks:

1. Add a manager that starts only when `spec.telemetry.enabled`.
2. Resolve default endpoint from `spec.telemetry.endpoint`, not `spec.port`.
3. Connect using the same device credentials.
4. Reconnect with bounded exponential backoff.
5. Store latest samples in a bounded, TTL-based cache.
6. Add internal manager health metrics.
7. Ensure scrapes never dial the device.

Acceptance:

- A fake subscriber can drive samples into the cache.
- Stream failure does not crash the provider.
- Cache enforces max-series and stale-series expiry.
- `go test -race` passes for telemetry package tests.

### Phase 4: Metric mapper and built-in profiles

Files:

- `internal/provider/telemetry/path.go`
- `internal/provider/telemetry/mapper.go`
- `internal/provider/telemetry/profiles.go`
- `internal/provider/telemetry/testdata/*.json`

Tasks:

1. Normalize gNMI path prefix/update pairs.
2. Extract list keys.
3. Implement metric definitions for interface counters/state.
4. Implement CPU/memory definitions.
5. Add environment and PoE definitions where device paths are verified.
6. Add golden tests for each path-to-metric conversion.
7. Add drop reasons for unsupported values.

Acceptance:

- Known fixture updates produce expected metric family names, labels, and values.
- Unsupported leaves are dropped with explicit reason counters.
- No mapper uses raw full path as a Prometheus label.

### Phase 5: Expose metrics

Files:

- `internal/provider/provider.go`
- `internal/provider/metrics.go`
- `internal/provider/telemetry/collector.go`
- `docs/observability.md`
- `docs/troubleshooting.md`

Tasks:

1. Add optional telemetry source to `AppHostingProvider`.
2. Merge telemetry families into `GetMetricsResource`.
3. Ensure scrape output is stable when the stream is down.
4. Add troubleshooting docs for stream down, empty metrics, and parse drops.
5. Add OTel Collector scrape examples.

Acceptance:

- `/metrics/resource` includes telemetry metrics after fixture events.
- Existing `cisco_device_*` metrics are unchanged.
- OTel Collector can scrape and forward the metrics without a custom receiver.

### Phase 6: Device validation

Test at least:

- Cat9k IOS-XE 17.18.x with insecure `gnxi` on `50052`.
- Secure gNMI endpoint if available in the lab.
- Device configured for NETCONF config writes plus gNMI telemetry.
- Device configured for RESTCONF config writes plus gNMI telemetry.
- Stream outage and reconnect.
- High interface-count device for cardinality and memory behavior.

Evidence to capture:

- `show running-config | include gnxi`
- gNMI subscription success logs.
- `/metrics/resource` sample output.
- OTel Collector received metric names.
- Stream reconnect log and metric increments.
- CPU/memory footprint of the CVK pod under sample load.

## 14. Testing plan

Unit tests:

- API defaulting/validation where supported by existing test patterns.
- gNMI subscription request generation.
- gNMI event flattening.
- path normalization.
- key extraction.
- typed value conversion.
- enum transforms.
- metric cache insert/update/expire.
- max-series enforcement.
- Prometheus `MetricFamily` rendering.

Integration-style tests:

- bufconn gNMI server sends update/delete/sync/error.
- fake telemetry manager streams events into provider metrics.
- scrape handler reads cached values without blocking on stream.

Regression tests:

- Existing drift `Subscribe` stays lossy/coalesced and does not receive telemetry-only behavior.
- Existing `buildMetricsResource` CPU/memory/interface metrics are byte-for-byte compatible where practical.
- Existing configdriver RESTCONF/NETCONF/gNMI tests are unaffected.

Recommended commands:

```sh
go test ./...
GOCACHE=/tmp/cvk-gocache go test -race ./...
```

## 15. Security and operational concerns

Credentials:

- Use the existing `CiscoDevice` credential secret by default.
- Do not add a second secret reference until there is a clear operational need.
- Redact endpoint credentials and gRPC metadata in logs.

TLS:

- Keep telemetry TLS config separate from apphosting RESTCONF TLS config.
- Default IOS-XE `gnxi` insecure `50052` should be explicit in docs.
- Secure `gnxi` should require explicit `telemetry.endpoint.tls.enabled: true`.

RBAC:

- If telemetry status writes are added, extend only the existing `CiscoDevice/status` permissions.
- Metrics scraping should not require extra device credentials.

Cardinality:

- Cap active series.
- Expire stale series.
- Prefer bounded labels.
- Reject user-defined labels from raw path/value material.

Backpressure:

- Stream reader must not block indefinitely on conversion.
- Drop with counters when queues are full.
- Cache scrape path must not block stream ingestion for long.

## 16. Risks and mitigations

| Risk | Mitigation |
|---|---|
| gNMI endpoint config is confused with apphosting `spec.port` | Add separate `spec.telemetry.endpoint` and document it heavily. |
| Existing drift Subscribe becomes overloaded | Add `SubscribeTelemetry`; leave `Subscribe` untouched. |
| Generic YANG export creates too many series | Use curated profiles and max-series limits. |
| IOS-XE path support differs by release | Profiles are opt-in and tested against named releases. |
| String/enum leaves become high-cardinality labels | Only explicit enum mappings are exported. Unknown strings are dropped or logged as events. |
| Stream flaps cause API/status noise | Rate-limit status updates and events. |
| Direct OTLP locks in bad semantic choices early | Start with Prometheus scrape and add direct OTLP later. |

## 17. Acceptance criteria for the feature

The first production-usable release is complete when:

1. A `CiscoDevice` can keep `spec.transport: netconf` or `restconf` for config while enabling `spec.telemetry.mode: gnmi`.
2. CVK opens a gNMI Subscribe stream to IOS-XE using telemetry-specific endpoint settings.
3. CVK converts at least interface counters/state plus CPU/memory into stable Prometheus metrics.
4. `/metrics/resource` exposes the telemetry metrics without deploying a custom OTel receiver.
5. An OTel Collector can scrape those metrics and forward them to the backend.
6. Stream reconnect, event drops, parse drops, and active series are observable.
7. Existing PR #111 configdriver behavior and drift detection remain unchanged.

## 18. Suggested implementation order after merge

1. Rebase and verify PR #111 baseline.
2. Add API types and docs.
3. Add `SubscribeTelemetry` and bufconn tests.
4. Add telemetry manager/cache behind `enabled: false` default.
5. Add mapper with interface counters/state only.
6. Wire `/metrics/resource`.
7. Validate against one lab Cat9k.
8. Add CPU/memory.
9. Add environment/PoE only after verifying device paths.
10. Add collector examples and troubleshooting docs.

This order produces an early working vertical slice before broadening metric coverage.

## 19. Decisions to resolve before coding

Resolve these early so the first implementation does not drift:

1. Confirm the first lab IOS-XE release and device model for path validation.
2. Decide whether the initial interface profile should prefer OpenConfig paths, IOS-XE native paths, or both behind separate profile names.
3. Confirm whether secure `gnxi` is in scope for the first validation pass or documented as a follow-up.
4. Confirm whether telemetry status belongs in `CiscoDevice.status` immediately or waits until stream behavior is proven.
5. Confirm whether the first release supports only per-device CVK pods, with aggregator-mode telemetry as a later phase.
6. Confirm the maximum accepted default cardinality per device.
7. Decide whether built-in profiles are enough for v1 or whether a separate telemetry profile CRD is required before release.
