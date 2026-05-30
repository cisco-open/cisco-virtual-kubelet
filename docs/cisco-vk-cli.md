# cisco-vk CLI Reference

`cisco-vk` is the single binary that powers Cisco Virtual Kubelet. It provides
two subcommands: one that runs a Virtual Kubelet provider for a single device,
and one that runs the Kubernetes controller that manages the fleet of VK pods.

```
cisco-vk [command] [flags]

Available Commands:
  run       Start the Virtual Kubelet provider for one device
  manager   Start the CRD controller manager
  help      Help about any command
```

## cisco-vk run

Starts a Virtual Kubelet provider process for a single Cisco device. One
process per device; the manager spawns these automatically when it
creates a `CiscoDevice` CR.

```
cisco-vk run [flags]
```

### Flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `-c, --config` | — | (required) | Path to the device configuration YAML (rendered by the manager from the `CiscoDevice` CR into a ConfigMap). |
| `--nodename` | `VK_NODE_NAME` | `<device-name>` | Kubernetes virtual node name to register. Defaults to the device name from the config file. |
| `--kubeconfig` | `KUBECONFIG` | in-cluster | Path to a kubeconfig file. Omit when running inside the cluster (default). |
| `--log-level` | `LOG_LEVEL` | `info` | Log verbosity. One of `debug`, `info`, `warn`, `error`. |
| `--tls-cert-file` | — | auto-generated | Path to the TLS certificate for the kubelet HTTPS listener (`:10250`). A self-signed cert is generated at startup when this is not set. |
| `--tls-key-file` | — | auto-generated | Path to the TLS private key for the kubelet HTTPS listener. |
| `--enable-write-class-gnoi` | `CISCO_VK_ENABLE_WRITE_CLASS_GNOI` | `false` | Enable write-class gNOI reconcilers (`IOSXEOperationalAction`: reboot, file put/remove, factory reset). **Off by default.** |
| `--enable-iosxesoftwareupgrade` | `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE` | `false` | Enable the `IOSXESoftwareUpgrade` gNOI OS upgrade reconciler. **Off by default.** |

### Additional environment variables

| Variable | Purpose |
|---|---|
| `VK_DEVICE_PASSWORD` | Device password. Injected by the manager from the `credentialSecretRef` Secret; never written to the ConfigMap. |
| `CISCO_VK_GNOI_INSECURE` | Set to `1` to use the insecure IOS-XE gNxI listener instead of TLS. Equivalent to Helm `gnoi.insecure`. |
| `CISCO_VK_GNOI_PORT` | Override the gNOI/gNMI port. If unset, CVK infers `50052` (insecure) or `9339` (TLS). |
| `CISCO_VK_GNOI_DISABLED` | Set to `1` to skip gNOI client construction entirely (e.g. when the device has no gNxI listener). |
| `CONFIG_YANG_VALIDATION` | Config-driver YANG validation mode: `disabled` (default), `warn`, or `strict`. |

### Example — run directly (development)

```bash
cisco-vk run \
  --config /etc/cisco-vk/cat9300-1.yaml \
  --nodename cat9300-1 \
  --kubeconfig ~/.kube/config \
  --log-level debug \
  --enable-iosxesoftwareupgrade
```

In production the manager generates the config file and injects it as a
mounted ConfigMap. Direct invocation is useful for local development or
when debugging a specific device.

## cisco-vk manager

Starts the Kubernetes controller manager. Watches `CiscoDevice` custom
resources and, for each one, creates a ConfigMap (device config without
credentials) and a Deployment that runs a single `cisco-vk run` pod. It also
runs the `IOSXEConfigBundle` fan-out controller.

```
cisco-vk manager [flags]
```

### Flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `--metrics-bind-address` | — | `:8080` | Address for the controller-runtime Prometheus metrics endpoint. |
| `--health-probe-bind-address` | — | `:8081` | Address for `/healthz` and `/readyz` probes. |
| `--leader-elect` | — | `false` | Enable leader election for HA deployments (multiple manager replicas). |
| `--vk-image` | — | `ghcr.io/cisco/virtual-kubelet-cisco:latest` | Container image used for each per-device VK pod. Override in Helm with `image.repository` and `image.tag`. |
| `--vk-service-account` | — | `cisco-virtual-kubelet` | Service account name injected into VK pod Deployments. |
| `--enable-config-aggregator` | — | `false` | Enable the in-process config aggregator, which runs `IOSXEConfig` reconciliation directly in the manager instead of in each device's VK pod (experimental). |
| `--log-level` | `LOG_LEVEL` | `info` | Log verbosity. One of `debug`, `info`, `warn`, `error`. |
| `--controller-info-log-rate-limit` | — | `100` | Maximum number of info-level log lines per second from the controller reconciler to prevent log flood during bulk CRD operations. |

### Example — run directly (development)

```bash
cisco-vk manager \
  --vk-image my-registry/cisco-virtual-kubelet:dev \
  --leader-elect \
  --log-level debug
```

In production, use the Helm chart which sets all flags via `values.yaml`.

## Helm chart values

All `cisco-vk run` and `cisco-vk manager` flags have corresponding Helm
values. Key values:

```yaml
image:
  repository: ghcr.io/cisco/virtual-kubelet-cisco
  tag: latest
  pullPolicy: IfNotPresent

manager:
  leaderElect: false
  metricsBindAddress: ":8080"
  healthProbeBindAddress: ":8081"
  enableConfigAggregator: false
  controllerInfoLogRateLimit: 100
  logLevel: info

gnoi:
  insecure: false
  port: ""
  disabled: false
  enableSoftwareUpgrade: false
  enableWriteClass: false
```

## kubectl-ciscovk plugin

The `kubectl-ciscovk` plugin extends `kubectl` with CVK-specific
subcommands. Install it by placing the binary in your `$PATH` alongside
`kubectl`:

```bash
# List all virtual nodes (CiscoDevice-backed nodes)
kubectl ciscovk nodes

# Show live phase for all devices
kubectl ciscovk devices

# Tail the VK pod logs for a specific device
kubectl ciscovk logs cat9300-1

# Show the resolved IOSXEConfig intent for a device (pre-apply preview)
kubectl ciscovk config preview cat9300-1-network

# Lint a local IOSXEConfig YAML against OPA policies before applying
kubectl ciscovk config lint ./my-network-config.yaml
```

The plugin uses the same `KUBECONFIG` and context as `kubectl`. Run
`kubectl ciscovk --help` for the full subcommand list.
