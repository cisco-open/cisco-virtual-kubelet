# CLI and Plugin Reference

Cisco Virtual Kubelet exposes two operator surfaces:

- **`kubectl-ciscovk` plugin** — extends `kubectl` with operational commands:
  run show commands on devices, query live state, tail logs, and validate
  configuration. This is the day-2 operator tool.
- **`cisco-vk` binary** — the backend. Two subcommands start the controller
  manager (`manager`) and the per-device Virtual Kubelet provider (`run`).
  These run inside the cluster; most operators never invoke them directly.

---

## Show commands on Cisco devices

CVK lets you run any IOS-XE `show` command from Kubernetes using the
`DeviceOperation` CRD. Results are stored in `.status.output` and are visible
via `kubectl describe` or `kubectl get -o yaml`.

### kubectl one-liner

```bash
kubectl ciscovk show cat9300-1 "show version"
```

This creates a `DeviceOperation` CR, waits for the result, prints the output,
and cleans up — all in one step.

### Common show command examples

```bash
# IOS-XE version and uptime
kubectl ciscovk show cat9300-1 "show version"

# Interface status summary
kubectl ciscovk show cat9300-1 "show interfaces status"

# IP routing table
kubectl ciscovk show cat9300-1 "show ip route"

# BGP neighbor summary
kubectl ciscovk show cat9300-1 "show bgp summary"

# CDP neighbors
kubectl ciscovk show cat9300-1 "show cdp neighbors detail"

# Running configuration
kubectl ciscovk show cat9300-1 "show running-config"

# App-hosting installed applications
kubectl ciscovk show cat9300-1 "show app-hosting list"

# Platform environment (sensors, fans, power)
kubectl ciscovk show cat9300-1 "show environment all"
```

### DeviceOperation CR — direct manifest

For automated pipelines or when `kubectl ciscovk show` is not available,
create the CR directly:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: show-version-cat9300-1
  namespace: default
spec:
  deviceRef:
    name: cat9300-1
  kind: ShowCommand
  showCommand:
    command: "show version"
```

```bash
kubectl apply -f show-version.yaml
kubectl get deviceoperation show-version-cat9300-1 -w

# Once phase is Succeeded:
kubectl get deviceoperation show-version-cat9300-1 -o jsonpath='{.status.output}'
```

### Config diff — compare intent vs device

Check whether the device has drifted from its declared `IOSXEConfig` intent:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: diff-cat9300-1-network
  namespace: default
spec:
  deviceRef:
    name: cat9300-1
  kind: ConfigDiff
  configDiff:
    configRef:
      name: cat9300-1-network
```

```bash
kubectl apply -f config-diff.yaml
kubectl describe deviceoperation diff-cat9300-1-network
```

---

## kubectl-ciscovk plugin

### Installation

Download the `kubectl-ciscovk` binary for your platform and place it anywhere
in your `$PATH` alongside `kubectl`:

```bash
# macOS (arm64)
curl -Lo kubectl-ciscovk https://github.com/cisco-open/cisco-virtual-kubelet/releases/latest/download/kubectl-ciscovk-darwin-arm64
chmod +x kubectl-ciscovk
sudo mv kubectl-ciscovk /usr/local/bin/

# macOS (amd64)
curl -Lo kubectl-ciscovk https://github.com/cisco-open/cisco-virtual-kubelet/releases/latest/download/kubectl-ciscovk-darwin-amd64
chmod +x kubectl-ciscovk
sudo mv kubectl-ciscovk /usr/local/bin/

# Linux (amd64)
curl -Lo kubectl-ciscovk https://github.com/cisco-open/cisco-virtual-kubelet/releases/latest/download/kubectl-ciscovk-linux-amd64
chmod +x kubectl-ciscovk
sudo mv kubectl-ciscovk /usr/local/bin/
```

Verify:

```bash
kubectl ciscovk --help
```

!!! note
    The plugin uses the same `KUBECONFIG` and active context as `kubectl`.
    Switch clusters or namespaces the same way you would for any `kubectl`
    command: `--context`, `--namespace`, or by setting `KUBECONFIG`.

### Fleet and node commands

```bash
# List all virtual (CiscoDevice-backed) nodes with status
kubectl ciscovk nodes

NAME          STATUS   DRIVER   ADDRESS          AGE
cat9300-1     Ready    iosxe    198.51.100.101   4d
cat9300-2     Ready    iosxe    198.51.100.102   4d
cat9000-4     Ready    iosxe    198.51.100.103   2d

# Live phase for all CiscoDevice CRs
kubectl ciscovk devices

NAME          PHASE     VK-POD                    AGE
cat9300-1     Running   cisco-vk-cat9300-1-xyz    4d
cat9300-2     Running   cisco-vk-cat9300-2-abc    4d

# Tail the VK pod logs for a specific device
kubectl ciscovk logs cat9300-1

# Follow logs (like kubectl logs -f)
kubectl ciscovk logs cat9300-1 -f
```

### Configuration commands

```bash
# Preview the fully-resolved IOSXEConfig intent before applying
kubectl ciscovk config preview cat9300-1-network

# Lint a local IOSXEConfig YAML against OPA policies before applying
kubectl ciscovk config lint ./my-network-config.yaml
```

---

## cisco-vk run

`cisco-vk run` starts the Virtual Kubelet provider for a single device. The
manager creates one pod per `CiscoDevice` CR running this command; direct
invocation is only needed for local development.

### Flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `-c, --config` | — | (required) | Path to device configuration YAML. |
| `--nodename` | `VK_NODE_NAME` | from config | Kubernetes virtual node name to register. |
| `--kubeconfig` | `KUBECONFIG` | in-cluster | Path to kubeconfig. Omit inside the cluster. |
| `--log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `--tls-cert-file` | — | auto-generated | TLS certificate for the kubelet HTTPS listener. |
| `--tls-key-file` | — | auto-generated | TLS private key for the kubelet HTTPS listener. |
| `--enable-write-class-gnoi` | `CISCO_VK_ENABLE_WRITE_CLASS_GNOI` | `false` | Enable `IOSXEOperationalAction` reconciliation (reboot, file ops, factory reset). |
| `--enable-iosxesoftwareupgrade` | `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE` | `false` | Enable `IOSXESoftwareUpgrade` reconciliation. |

### Additional environment variables

| Variable | Purpose |
|---|---|
| `VK_DEVICE_PASSWORD` | Device password. Injected from the `credentialSecretRef` Secret; never written to the ConfigMap. |
| `CISCO_VK_GNOI_INSECURE` | `1` to use the insecure gNxI listener. |
| `CISCO_VK_GNOI_PORT` | Override gNOI/gNMI port. Inferred as `50052` (insecure) or `9339` (TLS) when unset. |
| `CISCO_VK_GNOI_DISABLED` | `1` to skip gNOI client construction entirely. |
| `CONFIG_YANG_VALIDATION` | Config-driver YANG validation: `disabled` (default), `warn`, or `strict`. |

Example one-liners (useful for development or a single ad-hoc run):

```bash
# Use the insecure gNxI listener (lab/dev without TLS):
CISCO_VK_GNOI_INSECURE=1 cisco-vk run --config /etc/cisco-vk/cat9300-1.yaml

# Enable the software upgrade reconciler:
CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1 cisco-vk run --config /etc/cisco-vk/cat9300-1.yaml

# Disable gNOI client entirely (device has no gNxI listener):
CISCO_VK_GNOI_DISABLED=1 cisco-vk run --config /etc/cisco-vk/cat9300-1.yaml
```

---

## cisco-vk manager

Starts the Kubernetes controller manager. Watches `CiscoDevice` custom
resources and creates a ConfigMap + Deployment for each one. Also runs the
`IOSXEConfigBundle` fan-out controller.

### Flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `--metrics-bind-address` | — | `:8080` | Prometheus metrics endpoint. |
| `--health-probe-bind-address` | — | `:8081` | `/healthz` and `/readyz` probes. |
| `--leader-elect` | — | `false` | Enable leader election for HA deployments. |
| `--vk-image` | — | `ghcr.io/cisco/virtual-kubelet-cisco:latest` | Container image for per-device VK pods. |
| `--vk-service-account` | — | `cisco-virtual-kubelet` | Service account injected into VK Deployments. |
| `--enable-config-aggregator` | — | `false` | Run `IOSXEConfig` reconciliation in-process rather than in each VK pod (experimental). |
| `--log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `--controller-info-log-rate-limit` | — | `100` | Max info log lines/sec from the reconciler. |

---

## Helm chart values

Install or upgrade with the bundled chart:

```bash
helm upgrade --install cisco-vk ./charts/cisco-virtual-kubelet \
  --namespace cisco-vk \
  --create-namespace \
  --set image.tag=v1.2.0 \
  --set manager.leaderElect=true \
  --set gnoi.enableSoftwareUpgrade=false
```

Key values:

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
