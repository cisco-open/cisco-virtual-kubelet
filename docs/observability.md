# Observability

Cisco Virtual Kubelet uses OpenTelemetry as the shared correlation plane for
controller reconciliation, per-device Virtual Kubelet work, MDT-over-gNMI
telemetry, configuration, topology, and read-only device operations.

The important rule is that every signal carries stable resource identity. That
lets Grafana, Prometheus, Jaeger, Loki, Tempo, Splunk Observability, or another
OTel backend join traces, metrics, and logs by device, pod, process role, and
Kubernetes namespace without relying on log text.

## OTel Spine

### Process identity

CVK emits from three process roles. They may run in one Kubernetes pod, but they
are separate OTel resources by design:

| Process | `service.name` | `cvk.process.role` | Notes |
|---|---|---|---|
| System controller | `cisco-vk-controller` | `controller` | Watches `CiscoDevice` and reconciles controller-managed Kubernetes resources. |
| Per-device VK provider | `cisco-vk-vk` | `vk-provider` | Owns the upstream Virtual Kubelet node and Pod lifecycle. |
| Per-device telemetry emitter | `cisco-vk-telemetry` | `telemetry-emitter` | Owns MDT-over-gNMI subscriptions and telemetry mapping. |

Common attributes include `service.instance.id`, `host.name`,
`net.peer.name`, `k8s.pod.name`, `k8s.namespace.name`, `k8s.node.name`,
`k8s.pod.uid`, `cisco.device.name`, `cisco.device.address`, and
`cvk.driver.kind`.

### Trace taxonomy

Span names use one stable shape across surfaces:

| Surface | Span name pattern | Examples |
|---|---|---|
| Kubernetes reconcile | `cvk.<resource>.reconcile` | `cvk.iosxeconfig.reconcile` |
| Pod lifecycle | `cvk.pod.<operation>` | `cvk.pod.create`, `cvk.pod.status-reconcile` |
| Device transport | `cvk.transport.<protocol>.<verb>` | `cvk.transport.netconf.get`, `cvk.transport.restconf.post` |
| Config engine | `cvk.config.<phase>` | `cvk.config.reconcile`, `cvk.config.plan`, `cvk.config.apply` |
| Topology cycle | `cvk.topology.cycle` | root span per bounded topology emission |
| Device operation | `cvk.op.<kind>` | `cvk.op.show_command`, `cvk.op.config_diff` |

The process installs the upstream Virtual Kubelet OTel adapter at startup, so
VK spans such as Pod admission, status sync, and lease updates can parent CVK
driver spans automatically when context is propagated. The repository keeps a
small `scripts/lint-ctx.sh` guard to catch new unreviewed
`context.Background()` usage in controller or driver paths.

### Correlation

Pod admission stores a bounded `(device, app_id)` correlation entry containing
the W3C span context and validated lifecycle ID. MDT app-hosting recovery events
consult that cache so recent device-side state transitions retain the correct
Pod admission link and lifecycle search key. The cache is deliberately bounded
and short-lived: it is for causality, not durable storage.

Pipeline ingress supplies the carrier annotations in the first five rows below.
CVK validates and copies only that allowlist to generated child-object metadata;
it neither mints nor refreshes those ingress values. Configuration reconciliation
writes only the final three `last-*` diagnostic annotations:

| Annotation | Meaning |
|---|---|
| `cisco.vk/traceparent` | W3C carrier for the current reconcile window. |
| `cisco.vk/tracestate` | W3C vendor state associated with the direct parent. |
| `cisco.vk/upstream-traceparent` | Asynchronous upstream carrier (for example a GitHub wrapper or parent Argo step), represented as a span link. |
| `cisco.vk/trace-window-end` | Expiry for using that trace context. |
| `cisco.vk/lifecycle-id` | Stable lifecycle search key emitted as `cvk.lifecycle.id`. |
| `cisco.vk/last-trace-id` | Most recent reconcile trace ID. |
| `cisco.vk/last-trace-duration` | Most recent reconcile duration. |
| `cisco.vk/last-error-trace-id` | Failed reconcile trace ID, when present. |

### CI/CD lifecycle handoff

The validation pipelines use these annotations to connect the supported
GitHub → Argo → ubuntu12 → CVK path. GitHub workflow/job/step traces use
deterministic IDs compatible with OpenTelemetry Collector `githubreceiver`.
The trusted dispatcher passes its dispatch-step context to the lab wrapper;
the wrapper then derives the deterministic context of its own exact Argo
submission step. The Argo lifecycle bridge links its native trace to that
wrapper step. These dispatch and scheduling boundaries are asynchronous, so
they use span links rather than invented synchronous parentage.

The trusted GitHub observer also joins release stages that GitHub exposes as
separate runs: a merged-PR smoke run to the exact `main` smoke run, that run to
the tag-triggered release workflow for the same commit, the immutable
`release.published` event to that release workflow, and automatic docs/Krew
publication runs back to the publication event. A rerun links to its previous
attempt. Direct pushes and manually dispatched documentation builds are left
explicitly unlinked when GitHub does not provide enough authoritative metadata
to prove the relationship. If a required transition is missing, ambiguous, or
temporarily unreadable, the observer exports the standalone base trace with its
correlation state and then fails visibly; it never guesses a link or silently
drops the completed run.

Argo 4.1 injects the current W3C `TRACEPARENT` into every main workflow
container. The trusted lab SSH bootstrap forwards that value and annotates the
ephemeral Kubernetes resources it creates on ubuntu12. CVK validates the
carrier before using it in the following paths:

- `CiscoDevice` reconciliation and its generated ConfigMap, Deployment, and
  config-prerequisite children;
- `IOSXEConfigBundle` and generated `IOSXEConfig` children;
- IOS-XE and NX-OS configuration reconciliation;
- `DeviceOperation`, including gNOI-backed operations;
- `IOSXESoftwareUpgrade` phase transitions;
- `IOSXETelemetry` subscription setup and bounded health reconciliation (the
  shared long-lived stream remains device/subscription attributed rather than
  being pinned to one lifecycle); and
- Pod create, update, delete, get/list, status, and recovery paths, including
  the device-driver work they invoke.

A fresh primary carrier is a direct remote parent only until
`cisco.vk/trace-window-end`, with an absolute maximum of 15 minutes from the
time CVK consumes it. An expired carrier becomes a span link, while a deadline
farther than 15 minutes in the future is never accepted as a parent. The
GitHub upstream carrier is always a link. `tracestate` is capped at 512 bytes;
the lifecycle ID is capped at 128 characters and restricted to a small
identifier alphabet. Those checks bound format and size, not sensitivity:
producers must never place credentials or other secrets in correlation fields.
Malformed values are ignored without echoing them into logs or telemetry.

In Splunk, start with the exact `cvk.lifecycle.id`, for example
`cvk-pr181-h<full-head-sha>` or `cvk-release-v2026.9.2`. Open a CVK reconcile
span to see its bounded Argo parent or link, then follow the lifecycle bridge
and GitHub cross-run links through the hosted workflows. This bounded
parent/link model keeps long-running controllers from holding a release trace
open indefinitely.

### Config Revision History

`IOSXEConfigRevision` objects store resolved intent for successful applies so
`spec.rollbackTo` can replay a prior revision through the normal reconcile
path. When an `IOSXEConfig` uses `spec.secretRefs`, revisions are still
created, but secret-backed family blocks are omitted from `spec.body` and
`spec.secretMaterialOmitted` is set. Rollback to those revisions is allowed
only when the current `IOSXEConfig` references the same Secret names; otherwise
status reports `RollbackBlocked=True` with reason
`RevisionMissingSecretMaterial`.

### Self metrics

CVK emits self metrics with the `cisco_vk_telemetry_*` prefix. Important
pipeline health metrics include:

| Metric | Meaning |
|---|---|
| `cisco_vk_telemetry_active_streams` | Active gNMI subscription streams by device and subscription. |
| `cisco_vk_telemetry_stream_reconnects_total` | Stream reconnect attempts. |
| `cisco_vk_telemetry_log_records_emitted_total` | OTel log records emitted by the MDT mapper. |
| `cisco_vk_telemetry_instrument_cap_drops_total` | Metric instruments dropped by cardinality cap. |
| `cisco_vk_telemetry_transitions_dropped_total` | State transition events dropped by transition buffering. |
| `cisco_vk_telemetry_notifier_dropped_total` | PodNotifier events dropped because the callback queue was full. |
| `cisco_vk_telemetry_exporter_failures_total` | OTLP exporter failures by signal and reason. |

### Current topology boundary

The MDT state cache is currently used for telemetry state, PodNotifier, and
trace correlation. It is not yet the source of topology. The topology provider
still reads the device through the existing driver paths. Moving CDP/OSPF
topology to MDT remains gated on the lab validation of the YANG paths listed in
the unified architecture plan.

## Existing Surfaces

Cisco Virtual Kubelet also exposes four Kubernetes-facing observability
surfaces:

- **Prometheus metrics** on the standard kubelet `/metrics/resource` endpoint.
- **Kubernetes stats/summary** on `/stats/summary` — powers `kubectl top node`.
- **OpenTelemetry topology traces** emitted on a configurable interval. OpenTelemetry (OTEL) is a vendor-neutral observability framework; here we use it to emit a device-centric topology trace that backends like Splunk Observability Cloud can render as a service map.
- **Kubernetes node annotations** populated on every status sync, surfacing basic network context like router ID and neighbor counts.

Three of these surfaces — metrics (topology subset), OTEL traces, and node annotations — depend on the driver implementing the optional `TopologyProvider` interface. The IOS-XE driver does; other drivers may not, in which case their VKs simply omit the topology-derived data without erroring.

**CDP** (Cisco Discovery Protocol) and **OSPF** (Open Shortest Path First) are referenced throughout this page. Both are neighbor-discovery protocols the device participates in — CDP at Layer 2 for directly-connected Cisco gear, OSPF at Layer 3 for routing peers.

## Metrics

### Catalog

All metrics are type `gauge`. The base set works on any driver; the topology-derived metrics require a driver with `TopologyProvider`.

#### Device resources (always present)

| Metric | Labels | Notes |
|---|---|---|
| `cisco_device_cpu_usage_percent` | — | IOx subsystem CPU usage |
| `cisco_device_memory_used_bytes` | — | IOx subsystem memory in use |
| `cisco_device_memory_total_bytes` | — | IOx subsystem memory quota |
| `cisco_device_storage_used_bytes` | — | IOx subsystem storage in use |
| `cisco_device_storage_total_bytes` | — | IOx subsystem storage quota |

#### gNOI lifecycle metrics

These metrics are registered when the corresponding controllers and gNOI
client packages are linked into the process. The write-class and software
upgrade reconcilers still require their enablement flags before they act on
CRs.

| Metric | Labels | Notes |
|---|---|---|
| `cisco_vk_gnoi_rpc_total` | `service`, `outcome` | gNOI RPC outcomes, including `ok`, `unimplemented`, `unavailable`, and other error classes. |
| `cisco_vk_gnoi_capability_cache_total` | `service`, `result` | Capability cache hit, miss, expiration, pin, and fail-fast decisions. |
| `cisco_vk_devicegrpc_lease_events_total` | `class`, `event` | Workload-classed gRPC pool lease and release events. |
| `cisco_vk_devicegrpc_outstanding_leases` | `class` | Current outstanding gRPC pool leases. |
| `cisco_vk_devicegrpc_close_leak_detected_total` | `class` | Outstanding leases observed when a pool closes. |
| `cisco_vk_iosxe_software_upgrade_phase_transitions_total` | `device`, `target_version`, `from`, `to`, `reason` | Software upgrade state transitions. |
| `cisco_vk_iosxe_operational_action_transitions_total` | `device`, `kind`, `phase`, `reason` | Write-class action phase transitions and terminal outcomes. |

#### Interfaces (TopologyProvider)

| Metric | Labels | Notes |
|---|---|---|
| `cisco_device_interface_rx_bits_per_sec` | `interface` | Current receive rate |
| `cisco_device_interface_tx_bits_per_sec` | `interface` | Current transmit rate |
| `cisco_device_interface_state` | `interface`, `state` | `1` when `state="up"`, `0` otherwise |

#### Neighbors (TopologyProvider)

| Metric | Labels | Notes |
|---|---|---|
| `cisco_device_cdp_neighbor_count` | — | Number of CDP neighbors |
| `cisco_device_ospf_neighbor_count` | — | Number of OSPF neighbors |
| `cisco_device_neighbor_link` | `target`, `interface`, `protocol`, `platform` | Fixed value `1` per discovered neighbor; drop/cardinality control lives on the collector side |

### Scraping

The metrics are served on the node's kubelet HTTPS listener (port 10250) at `/metrics/resource`. Any workload that already scrapes kubelet metrics picks them up automatically — for example, `kube-prometheus-stack`'s `ServiceMonitor` for nodes, or the [Prometheus kubelet scrape config](https://github.com/prometheus/prometheus/blob/main/documentation/examples/prometheus-kubernetes.yml).

No extra configuration on the VK side is required — the handler is always wired.

## Stats / summary

`GET /stats/summary` returns a Kubernetes `statsv1alpha1.Summary` for this node:

- **CPU**: `UsageNanoCores` derived from IOx CPU percentage
- **Memory**: `UsageBytes`, `AvailableBytes` (in bytes, converted from MB)
- **Filesystem**: `CapacityBytes`, `UsedBytes`, `AvailableBytes` for IOx storage
- **Network**: Per-interface `RxBytes` / `TxBytes` (when `TopologyProvider` is implemented)

This is what `kubectl top node cat9000-1` reads. It also feeds the Kubernetes Vertical/Horizontal pod autoscalers if you use them against VK nodes.

## OpenTelemetry topology export

Enable per-device via the `otel:` block in the device config / CR:

```yaml
otel:
  enabled: true
  endpoint: "otel-collector.observability.svc:4317"
  insecure: true
  serviceName: "cisco-network"
  intervalSecs: 60
  maxLinkSpans: 256
```

The exporter connects to the OTLP gRPC endpoint, emits one trace per interval, and shuts down cleanly on context cancel (5 s grace).

### Resource attributes

Every span carries:

| Key | Value |
|---|---|
| `service.name` | `"{serviceName}.{hostname}"` — e.g. `cisco-network.cat9000-1` |
| `service.namespace` | `"network.infrastructure"` |
| `host.name` | VK node name |
| `device.address` | Management IP/hostname |

### Span hierarchy

Each emission cycle produces one trace:

```
root span: cvk.topology.cycle           [SERVER]
├── link.<localIface>-><peerDeviceID>   [CLIENT]  (one per CDP/OSPF neighbor)
├── link.<localIface>-><peerDeviceID>   [CLIENT]
├── …
└── hosted.<podNs>/<podName>            [CLIENT]  (one per hosted container)
```

#### Root span (`cvk.topology.cycle`)

| Attribute | Source |
|---|---|
| `topology.cycle.id` | Unique ID for one bounded topology emission cycle |
| `topology.emitted_link_count` | Link spans emitted after applying `maxLinkSpans` |
| `topology.dropped_link_count` | Links omitted because the cap was reached |
| `node.name` | Device hostname |
| `node.type` | `"network_device"` |
| `node.role` | `"router"` |
| `node.neighbor_count` | count of observed consolidated neighbors before span capping |
| `node.interface_count` | count of interfaces with IPs |
| `router.id` | `DeviceInfo.RouterID` (OSPF/BGP) |
| `router.platform` | `DeviceInfo.ProductID` |
| `router.os.version` | `DeviceInfo.SoftwareVersion` |
| `router.serial` | `DeviceInfo.SerialNumber` |
| `router.ip.addresses` | comma-joined interface IPs |
| `network.layer` | `"L3"` |
| `network.type` | `"routed"` |

#### Link spans (`link.{localIface}->{peerDeviceID}`)

CDP and OSPF neighbors are consolidated per local interface — a single span represents a link even when both protocols are active.

| Attribute | Notes |
|---|---|
| `topology.cycle.id` | Matches the cycle root span |
| `peer.service` | `"{serviceName}.{peerDeviceID}"` — matches the root span of the peer if it's also exporting, enabling service-map correlation |
| `net.peer.name` | `peerDeviceID` |
| `net.peer.ip` | Peer management IP |
| `net.host.interface` | Local interface (e.g. `GigabitEthernet0/0/1`) |
| `net.peer.interface` | Remote interface |
| `link.type` | `"physical"` |
| `link.protocols` | `"+"`-joined — e.g. `"cdp+ospf"` |
| `link.state` | `"up"` normally, `"degraded"` when OSPF state is not `"full"` or `"2way"` |
| `link.utilization.in.bps` | From interface stats (if available) |
| `link.utilization.out.bps` | From interface stats (if available) |
| `link.speed.bps` | Interface speed |
| `ospf.neighbor.state` | OSPF-only, when OSPF is a protocol on this link |
| `ospf.area` | OSPF-only |
| `peer.platform`, `peer.capabilities` | From CDP |
| `topology.link_id` | `"{hostname}:{localIf}->{peerID}:{remoteIf}"` — stable identifier |

#### Hosted-app spans (`hosted.<namespace>/<podName>`)

| Attribute | Notes |
|---|---|
| `topology.cycle.id` | Matches the cycle root span |
| `peer.service` | `"app.{namespace}/{podName}"` — distinct namespace from network neighbors |
| `service.type` | `"app-hosting"` |
| `deployment.environment` | `"edge-compute"` |
| `app.id` | Device app ID (e.g. `cvk00000_<uid>`) |
| `app.state` | Device lifecycle state (RUNNING, DEPLOYED, …) |
| `k8s.pod.name`, `k8s.pod.namespace`, `k8s.pod.uid`, `k8s.container.name` | Pod identity |
| `app.ip`, `app.mac` | When oper-data has resolved them |
| `net.host.interface` | Attached device interface |
| `topology.link_id` | `"{hostname}->{peerService}"` |

### What you get

- **Service map** — In a backend like Splunk Observability Cloud, devices appear as services and links between them render as edges. Pods hosted on a device appear as downstream services of that device.
- **Change detection** — Each interval is a full snapshot. Diffing consecutive traces shows topology changes (new/lost neighbors, state transitions).
- **Correlation** — `topology.link_id` is stable across emissions, so queries that group or filter by link are consistent over time.

### Failure modes

OTEL initialisation failure is **non-fatal**. If the OTLP endpoint is unreachable at startup the VK pod logs a warning and continues without OTEL. Intermittent topology-data errors (CDP, OSPF, interfaces) are logged at debug level and the affected attributes are simply omitted from that emission.

## Node annotations

On every node status sync the provider populates these annotations from the driver:

| Annotation | Source |
|---|---|
| `cisco.io/router-id` | `DeviceInfo.RouterID` (OSPF/BGP) |
| `cisco.io/hostname` | `DeviceInfo.Hostname` |
| `cisco.io/cdp-neighbor-count` | Count from `GetCDPNeighbors()` |
| `cisco.io/ospf-neighbor-count` | Count from `GetOSPFNeighbors()` |
| `cisco.io/protocols` | Comma-joined list of protocols with at least one neighbor (`cdp`, `ospf`) |

Use them for:

- `kubectl get nodes -L cisco.io/router-id`
- Dashboards that filter by active protocol
- Basic alerting: `cisco.io/cdp-neighbor-count == 0` → isolation alarm

Annotation size is deliberately kept small (scalar counts, not full neighbor lists). For full topology data use OTEL; for interface detail use the Prometheus metrics.

## End-to-end example: Splunk Observability Cloud

Splunk Observability Cloud ingests both Prometheus metrics and OTLP traces through a single OpenTelemetry Collector, so a typical deployment is:

1. Install the [Splunk OpenTelemetry Collector for Kubernetes](https://github.com/signalfx/splunk-otel-collector-chart), pointed at your Splunk Observability Cloud realm and access token. It:
    - scrapes kubelet `/metrics/resource` automatically — the `cisco_device_*` metrics appear without extra config;
    - exposes an OTLP gRPC endpoint (default `:4317`) for traces.
2. On each `CiscoDevice`, point the VK's OTEL exporter at the collector:
   ```yaml
   spec:
     otel:
       enabled: true
       endpoint: "splunk-otel-collector-agent.observability.svc:4317"
       insecure: true
       intervalSecs: 60
   ```

Splunk Observability Cloud dashboards can then:

- Plot per-interface throughput (`cisco_device_interface_*_bits_per_sec`)
- Alert on neighbor loss (`cisco_device_cdp_neighbor_count < previous`)
- Visualise the network as a service map from the OTEL traces

## Related reading

- [Configuration → OpenTelemetry topology](CONFIGURATION.md#opentelemetry-topology) — config field reference
- [Architecture → Observability](ARCHITECTURE.md#observability) — how the data flows
- [Troubleshooting](troubleshooting.md) — what to do when a metric or trace is missing
