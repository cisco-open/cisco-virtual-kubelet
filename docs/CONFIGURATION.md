# Configuration Reference

Every field accepted by a `CiscoDevice` custom resource.

All fields documented below live under `spec` on the CR. The controller reads the CR, strips credentials, and materializes the rest into a ConfigMap that the VK pod reads — you do not edit the ConfigMap directly.

For device-side prerequisites (IOS-XE CLI config, DHCP pools, VLANs, etc.) and per-platform networking examples, see:

- [Catalyst 8000V](configuration-cat8000v.md) — VirtualPortGroup install
- [Catalyst 9000](configuration-cat9000.md) — AppGigabitEthernet install (access and trunk)

## Minimal spec

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9000-1
  namespace: default
spec:
  driver: XE
  address: "192.168.1.100"
  port: 443
  username: admin
  credentialSecretRef:
    name: cat9000-1-creds       # Secret with key: password
  tls:
    enabled: true
    insecureSkipVerify: true
  # allowUnsignedApps: true      # enable when running unsigned packages
                                  # (your own builds, or devices without
                                  # signed-verification enforcement)
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true
          interface: "0"
          guestInterface: 0
```

## CLI flags & environment variables

Runtime settings are **not** in the config file — they are passed as flags or env vars. Precedence: **flag → environment variable → default**.

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--nodename` | `VKUBELET_NODE_NAME` | `cisco-vk-<device-address>` | Kubernetes node name |
| `--config` / `-c` | — | `/etc/virtual-kubelet/config.yaml` | Device config path |
| `--kubeconfig` | `KUBECONFIG` | in-cluster | kubeconfig path |
| `--log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--tls-cert-file` | — | auto-generated | Kubelet HTTPS listener certificate |
| `--tls-key-file` | — | auto-generated | Kubelet HTTPS listener key |
| — | `VK_DEVICE_PASSWORD` | — | Device password. Overrides `device.password` from the config file. Set by the controller when `credentialSecretRef` is used. |

## Configuration fields

### Core

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `driver` | enum | yes | — | `XE`, `XR`, `NXOS`, `FAKE`. Only `XE` and `FAKE` are production-usable today. |
| `address` | string | yes | — | Management IP or hostname |
| `port` | int (1–65535) | no | 443 (TLS) / 80 | RESTCONF port |
| `username` | string | yes | — | Device username |
| `password` | string | no | — | Device password. **Do not set in controller mode** — use `credentialSecretRef` instead. |
| `credentialSecretRef` | LocalObjectReference | no | — | Reference to a `Secret` in the same namespace containing key `password`. See [Security](security.md#credential-injection). |

### TLS

```yaml
tls:
  enabled: true
  insecureSkipVerify: true
  certFile: /path/to/client.crt
  keyFile: /path/to/client.key
  caFile:  /path/to/ca.crt
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `tls.enabled` | bool | `false` | Enable HTTPS to the device |
| `tls.insecureSkipVerify` | bool | `false` | Skip certificate verification |
| `tls.certFile` | string | — | Client certificate path |
| `tls.keyFile` | string | — | Client key path |
| `tls.caFile` | string | — | CA bundle path |

### Node and pod topology

| Field | Type | Default | Notes |
|---|---|---|---|
| `podCIDR` | string | — | CIDR range the VK allocates from when DHCP is off. The VK hands out addresses sequentially from this range to each new container. |
| `labels` | map[string]string | `{}` | Extra labels applied to the virtual node |
| `taints` | []v1.Taint | `[]` | Extra taints applied to the virtual node |
| `maxPods` | int32 | `16` | Maximum pods the device can host |
| `region` | string | — | Populates `topology.kubernetes.io/region` on the node |
| `zone` | string | — | Populates `topology.kubernetes.io/zone` on the node |

### Resource limits

Default and maximum resource bounds reported as node capacity/allocatable and used when a pod omits requests.

```yaml
resourceLimits:
  defaultCPU: "500m"
  defaultMemory: "512Mi"
  defaultStorage: "500Mi"
  maxCPU: "2000m"
  maxMemory: "4Gi"
  maxStorage: "2Gi"
  others:
    gpu: "1"
```

| Field | Type | Notes |
|---|---|---|
| `resourceLimits.defaultCPU` | string | Default CPU if pod omits requests |
| `resourceLimits.defaultMemory` | string | Default memory if pod omits requests |
| `resourceLimits.defaultStorage` | string | Default ephemeral storage |
| `resourceLimits.maxCPU` | string | Upper limit per container |
| `resourceLimits.maxMemory` | string | Upper limit per container |
| `resourceLimits.maxStorage` | string | Upper limit per container |
| `resourceLimits.others` | map[string]string | Arbitrary custom resources |

### Logging

```yaml
logLevel: info   # debug | info | warn | error
```

The `logLevel` field is passed to the VK pod as the `--log-level` flag on the container.

### App packaging

| Field | Type | Default | Notes |
|---|---|---|---|
| `allowUnsignedApps` | bool | `false` | When `true`, the reconciler skips the `pkg-policy-invalid` guard and treats the transient YANG default as an install-in-progress signal. Enable this when you're running unsigned container packages — most commonly your own custom application builds, or on devices where signed-verification is not enforced. See [Troubleshooting → PackagePolicyInvalid](troubleshooting.md#packagepolicyinvalid-false-positives). |

### OpenTelemetry topology

```yaml
otel:
  enabled: true
  endpoint: "otel-collector.observability.svc:4317"
  insecure: true
  serviceName: "cisco-network"
  intervalSecs: 60
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `otel.enabled` | bool | `false` | Toggle topology trace emission |
| `otel.endpoint` | string | — | OTLP gRPC collector endpoint. **Required** when `enabled: true`. |
| `otel.insecure` | bool | `true` | Skip TLS on the gRPC connection |
| `otel.serviceName` | string | `"cisco-network"` | Base service name. The device hostname is appended → `"cisco-network.<hostname>"`. |
| `otel.intervalSecs` | int (min 10) | `60` | Interval between trace emissions |

OTEL export only happens when the driver implements `TopologyProvider` (the IOS-XE driver does). See [Observability → OpenTelemetry](observability.md#opentelemetry-topology-export).

### XEConfig — IOS-XE networking

Required when `driver: XE`. The `type` field selects which mode is in use; exactly one of the mode-specific blocks must be set to match.

```yaml
xe:
  networking:
    interface:
      type: VirtualPortGroup      # | AppGigabitEthernet | Management
      virtualPortGroup: { ... }
      appGigabitEthernet: { ... }
      management: { ... }
```

| `type` | Typical platform | Details |
|---|---|---|
| `VirtualPortGroup` | Catalyst 8000V | [configuration-cat8000v.md](configuration-cat8000v.md) |
| `AppGigabitEthernet` | Catalyst 9000 | [configuration-cat9000.md](configuration-cat9000.md) |
| `Management` | Either (containers on management network) | [Below](#management-interface-both-platforms) |

## Management interface (both platforms)

The Management interface mode places containers on the device's management network rather than on a dedicated App Hosting interface. It is supported on both Catalyst 8000V and Catalyst 9000.

```yaml
xe:
  networking:
    interface:
      type: Management
      management:
        dhcp: true
        guestInterface: 0
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `dhcp` | bool | `false` | DHCP on the management interface. If `false`, allocate from `podCIDR`. |
| `guestInterface` | uint8 (0–3) | `0` | Container-side interface index (0 = eth0). |

Note that putting containers on the management network exposes them directly to management traffic and any ACLs in place there. For workload isolation, prefer VirtualPortGroup (on Catalyst 8000V) or AppGigabitEthernet (on Catalyst 9000).

## Example pod manifest

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: hello-app
  namespace: default
spec:
  nodeName: cat9000-1
  tolerations:
  - key: virtual-kubelet.io/provider
    operator: Exists
  containers:
  - name: hello
    image: flash:/hello-app.iosxe.tar
    resources:
      requests:
        memory: "256Mi"
        cpu: "500m"
      limits:
        memory: "512Mi"
        cpu: "1000m"
```

!!! note
    The container image reference is a path on the device's flash storage (`flash:/...`), not a container registry. The tar must be pre-loaded on the device. Package policy (signed vs unsigned) is controlled by the device config (`no app-hosting signed-verification`) or by the CRD (`spec.allowUnsignedApps: true`).

## See also

- [Catalyst 8000V](configuration-cat8000v.md) — VirtualPortGroup install walkthrough
- [Catalyst 9000](configuration-cat9000.md) — AppGigabitEthernet install walkthrough
- [Examples](https://github.com/cisco-open/cisco-virtual-kubelet/tree/main/examples/configs) — full working configs for every interface mode
- [Security](security.md) — credential injection via Secrets
- [Observability](observability.md) — OTEL and metrics configuration in detail
- [Troubleshooting](troubleshooting.md) — common configuration issues
