# CLI and Plugin Reference

Cisco Virtual Kubelet exposes two command-line surfaces:

- **`kubectl-ciscovk` plugin** — an operator tool for read-only, ad-hoc IOS-XE
  commands. The current implementation provides `exec`, `version`, and help.
- **`cisco-vk` binary** — the backend. Its `manager` subcommand starts the
  controller, while `run` starts one per-device Virtual Kubelet provider.
  Helm normally manages both invocations.

---

## kubectl-ciscovk plugin

### Build and install

Prebuilt plugin binaries are not currently attached to GitHub releases. Build
the plugin from the current source tree and place it on your `PATH` using the
standard `kubectl-<name>` filename:

```bash
git clone https://github.com/cisco-open/cisco-virtual-kubelet.git
cd cisco-virtual-kubelet

make build-plugin
mkdir -p "$HOME/.local/bin"
install -m 0755 bin/kubectl-ciscovk "$HOME/.local/bin/kubectl-ciscovk"
```

Ensure `$HOME/.local/bin` is on your `PATH`, then verify the plugin:

```bash
kubectl ciscovk version
kubectl ciscovk --help
```

`make build-plugin` injects the nearest Git tag, commit, and build time into
the version output. Direct `go build` remains supported and reports a
`devel` build instead of impersonating a release.

The plugin invokes the `kubectl` binary from `PATH`. By default it inherits
`KUBECONFIG` and the active context. Pass `--context` and/or `--kubeconfig` to
select a target explicitly without changing the user's active context; the
plugin forwards both options to pod discovery and port-forwarding.

### Execute a read-only IOS-XE command

`exec` locates the per-device VK pod, opens a local `kubectl port-forward` to
its admin endpoint, submits the command, prints the result, and closes the
tunnel:

```bash
kubectl ciscovk exec cat9k-smoke -n cvk-system -- show version
kubectl ciscovk exec cat9k-smoke -n cvk-system \
  --context lab --kubeconfig "$HOME/.kube/lab.conf" \
  -- "show running-config | section interface"
```

The server applies its read-only command allowlist, and the plugin separately
rejects known destructive command prefixes. `exec` is currently IOS-XE-only.
It does not provide `show`, `nodes`, `devices`, `logs`, or `config`
subcommands.

Flags for `exec`:

| Flag | Default | Description |
|---|---|---|
| `-n`, `--namespace` | current namespace | Namespace containing the per-device VK pod. |
| `--allow-secrets` | `false` | Reserved for a future SAR-gated path; currently a no-op on the server. |
| `--truncate-bytes` | `65536` | Maximum output bytes per command; `0` disables truncation. |
| `--port` | random free port | Local port used for `kubectl port-forward`. |
| `--timeout` | `30s` | Overall command timeout. |
| `--context` | active context | Kubeconfig context forwarded to `kubectl`. |
| `--kubeconfig` | `KUBECONFIG`/kubectl default | Kubeconfig path forwarded to `kubectl`. |
| `--kubectl` | `kubectl` from `PATH` | Alternate path to the `kubectl` executable. |

The active Kubernetes identity needs permission to get/list pods in the
selected namespace and create `pods/portforward` connections. `exec` requires
the per-device deployment mode and its localhost diagnostic admin endpoint; it
is not available in aggregator mode.

### DeviceOperation CR — auditable asynchronous path

For automation that needs a durable Kubernetes object and status, create a
`DeviceOperation` directly:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: show-version-cat9300-1
  namespace: default
spec:
  deviceRef:
    name: cat9300-1
  operation:
    kind: ShowCommand
    commands:
      - show version
  ttlSecondsAfterFinished: 300
```

```bash
kubectl apply -f show-version.yaml
kubectl get deviceoperation show-version-cat9300-1 -w

# Once phase is Succeeded:
kubectl get deviceoperation show-version-cat9300-1 \
  -o jsonpath='{.status.outputs[0].output}{"\n"}'
```

`ConfigDiff`, packet-capture reads, and read-only gNOI operation kinds use the
same CRD with operation-specific inputs. See the
[Operations Runbook](operations.md#deviceoperation) for their current schema,
status, RBAC, and maturity.

---

## cisco-vk run

`cisco-vk run` starts the Virtual Kubelet provider for a single device. The
manager creates one pod per `CiscoDevice` CR running this command; direct
invocation is only needed for local development. Generic runtime flags support
both `XE` and `NXOS`, selected by `device.driver`; IOS-XE-only settings are
called out below. Controller-managed pods pass the `CiscoDevice` name
explicitly as `--nodename`.

### Flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `-c, --config` | — | `/etc/virtual-kubelet/config.yaml` | Path to device configuration YAML. |
| `--nodename` | `VKUBELET_NODE_NAME` | `cisco-vk-<device-address>` | Kubernetes virtual node name; falls back to `cisco-virtual-kubelet` when no address is available. |
| `--kubeconfig` | `KUBECONFIG` | in-cluster | Path to kubeconfig. Omit inside the cluster. |
| `--log-level` | `LOG_LEVEL` | `info` | Resolution is flag, environment, `device.logLevel`, then `info`; accepts `debug`, `info`, `warn`/`warning`, or `error`. |
| `--tls-cert-file` | — | `/etc/virtual-kubelet/tls/tls.crt` | Kubelet HTTPS certificate; when both configured files are absent, a self-signed pair is generated under `/var/lib/virtual-kubelet/tls/`. |
| `--tls-key-file` | — | `/etc/virtual-kubelet/tls/tls.key` | Kubelet HTTPS private key; exactly one certificate/key file being present is an error. |
| `--enable-write-class-gnoi` | `CISCO_VK_ENABLE_WRITE_CLASS_GNOI` | `false` | IOS-XE only: enable `IOSXEOperationalAction` reconciliation (reboot, file ops, factory reset). |
| `--enable-iosxesoftwareupgrade` | `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE` | `false` | IOS-XE only: enable `IOSXESoftwareUpgrade` reconciliation. |

### Additional environment variables

| Variable | Purpose |
|---|---|
| `VK_DEVICE_PASSWORD` | Non-empty value overrides the config-file password. With `credentialSecretRef`, the controller injects it through `secretKeyRef` and keeps it out of the ConfigMap. |
| `CISCO_VK_GNOI_INSECURE` | IOS-XE only: `1` or `true` selects the insecure gNOI listener; it does not change REST/config TLS. |
| `CISCO_VK_GNOI_PORT` | IOS-XE only: override the gNOI port. Defaults to `50052` (insecure) or `9339` (TLS), unless a nonstandard device port is configured. |
| `CISCO_VK_GNOI_DISABLED` | IOS-XE only: `1` or `true` skips gNOI client construction entirely. |
| `CONFIG_YANG_VALIDATION` | IOS-XE config-driver YANG validation: `disabled` (default), `warn`, or `strict`. NX-OS always applies its structural/DME validation. |

IOS-XE example one-liners for development or a single ad-hoc run are shown
below. Outside a cluster, also pass `--kubeconfig` or set a valid `KUBECONFIG`.

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

The `--vk-image` value above is the binary's direct-invocation fallback. The
Helm chart always passes an explicit value resolved from `image`/`vkImage`; its
published default is
`ghcr.io/cisco-open/cisco-virtual-kubelet:<chart-appVersion>`. When starting
`cisco-vk manager` outside Helm, set `--vk-image` explicitly to the image tag
you intend each per-device VK Deployment to run.

---

## Helm chart values

Install or upgrade with the bundled chart:

```bash
helm upgrade --install cvk ./charts/cisco-virtual-kubelet \
  --namespace cvk-system \
  --create-namespace \
  --set controller.leaderElect=true
```

The image tag defaults to the chart's `appVersion`. Use `controllerImage` or
`vkImage` only when the controller and per-device VK pods must run different
images.

Key values:

```yaml
image:
  repository: ghcr.io/cisco-open/cisco-virtual-kubelet
  tag: "" # empty selects the chart appVersion
  pullPolicy: IfNotPresent

controller:
  leaderElect: false
  metricsBindAddress: ":8080"
  healthProbeBindAddress: ":8081"

aggregator:
  enabled: false

gnoi:
  insecure: false
  port: ""
  disabled: false
  enableSoftwareUpgrade: false
  enableWriteClass: false
```
