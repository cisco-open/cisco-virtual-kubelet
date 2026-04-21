# Observability

Cisco Virtual Kubelet exposes four observability surfaces:

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
root span: node.<hostname>              [SERVER]
├── link.<localIface>-><peerDeviceID>   [CLIENT]  (one per CDP/OSPF neighbor)
├── link.<localIface>-><peerDeviceID>   [CLIENT]
├── …
└── hosted.<podNs>/<podName>            [CLIENT]  (one per hosted container)
```

#### Root span (`node.<hostname>`)

| Attribute | Source |
|---|---|
| `node.type` | `"network_device"` |
| `node.role` | `"router"` |
| `node.neighbor_count` | count of consolidated neighbors |
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
