# CLI and Plugin Reference

Cisco Virtual Kubelet exposes two command-line surfaces:

- **`kubectl-ciscovk` plugin** — an operator tool for read-only, ad-hoc IOS-XE
  commands. The current implementation provides `exec`, `version`, and help.
- **`cisco-vk` binary** — the backend. Its `manager` subcommand starts the
  controller, `run` starts one per-device Virtual Kubelet provider, and the
  internal `controller-worker` subcommand starts one registered network-
  controller adapter. Helm and the manager normally own these invocations.

---

## kubectl-ciscovk plugin

### Install with Krew

The plugin is named `cisco-vk` in the public Krew index, following Krew's
hyphenated vendor-name guidance. Installation and upgrades are:

```bash
kubectl krew update
kubectl krew install cisco-vk

# Later releases:
kubectl krew upgrade cisco-vk

# Invoke the Krew-installed plugin:
kubectl cisco-vk version
```

The archive's executable remains `kubectl-ciscovk`, so existing manual/source
installs can continue to use `kubectl ciscovk`. Krew exposes the conventional
`kubectl cisco-vk` alias. Historical note: `v2026.08.0` predates the signed
plugin assets, and `v2026.8.1` was the first plugin-bearing release. The
project release workflow validates Linux and macOS on both amd64 and arm64;
Windows is not advertised yet.

### Install a signed release archive

Starting with `v2026.8.1`, each plugin-bearing GitHub release includes four
deterministic archives, a signed checksum manifest, and per-archive signatures.
The following example selects the local Linux/macOS architecture, verifies the
signed checksum authority, and installs the plugin:

```bash
(
set -euo pipefail

# Set this to an asset-bearing release shown on the Releases page.
VERSION=v2026.9.2
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  darwin|linux) ;;
  *) echo "unsupported operating system" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture" >&2; exit 1 ;;
esac
ASSET="kubectl-ciscovk_${VERSION}_${OS}_${ARCH}.tar.gz"
CHECKSUMS="kubectl-ciscovk_${VERSION}_checksums.txt"
BASE="https://github.com/cisco-open/cisco-virtual-kubelet/releases/download/${VERSION}"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT
cd "$WORK_DIR"

curl -fLO "${BASE}/${ASSET}"
curl -fLO "${BASE}/${CHECKSUMS}"
curl -fLO "${BASE}/${CHECKSUMS}.sigstore.json"
cosign verify-blob \
  --bundle "${CHECKSUMS}.sigstore.json" \
  --certificate-identity \
    "https://github.com/cisco-open/cisco-virtual-kubelet/.github/workflows/release.yml@refs/tags/${VERSION}" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "${CHECKSUMS}"

expected="$(awk -v name="$ASSET" '$2 == name {print $1}' "$CHECKSUMS")"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$ASSET" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$ASSET" | awk '{print $1}')"
fi
test -n "$expected" && test "$actual" = "$expected"

tar -xzf "$ASSET"
mkdir -p "$HOME/.local/bin"
install -m 0755 kubectl-ciscovk "$HOME/.local/bin/kubectl-ciscovk"
ln -sfn kubectl-ciscovk "$HOME/.local/bin/kubectl-cisco_vk"
)
```

Ensure `$HOME/.local/bin` is on your `PATH`, then verify the plugin:

```bash
kubectl ciscovk version
kubectl cisco-vk version
kubectl ciscovk --help
```

### Build from source

For development or a custom build, build the plugin from the current source
tree and place it on your `PATH` using the standard `kubectl-<name>` filename:

```bash
git clone https://github.com/cisco-open/cisco-virtual-kubelet.git
cd cisco-virtual-kubelet

make build-plugin
mkdir -p "$HOME/.local/bin"
install -m 0755 bin/kubectl-ciscovk "$HOME/.local/bin/kubectl-ciscovk"
ln -sfn kubectl-ciscovk "$HOME/.local/bin/kubectl-cisco_vk"
```

`make build-plugin` injects the nearest Git tag, commit, and build time into
the version output. Direct `go build` remains supported and reports a
`devel` build instead of impersonating a release. The symlink additionally
supports the same `kubectl cisco-vk` spelling used by Krew; the existing
`kubectl ciscovk` spelling remains available.

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

## cisco-vk version

Use either form to identify the backend binary before an install, upgrade, or
support request:

```bash
cisco-vk version
cisco-vk --version
```

Both print the same release provenance:

```text
cisco-vk v2026.9.2 (commit=<full-git-commit>, built=<RFC3339-time>)
```

A direct development build reports `devel` and may report `unknown` metadata.

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
| `CISCO_VK_GNOI_INSECURE` | IOS-XE only: `1` or `true` forces the legacy plaintext gNOI listener; it overrides `spec.gnoi.transportSecurity`, does not change REST/config TLS, and no password metadata is sent. |
| `CISCO_VK_GNOI_PORT` | IOS-XE only: override the gNOI port. It overrides `spec.gnoi.port` but does not by itself select TLS. Defaults are `50052` for plaintext and `9339` for TLS. |
| `CISCO_VK_GNOI_DISABLED` | IOS-XE only: `1` or `true` skips gNOI client construction entirely. |
| `CONFIG_YANG_VALIDATION` | IOS-XE config-driver YANG validation: `disabled` (default), `warn`, or `strict`. NX-OS always applies its structural/DME validation. |

IOS-XE example one-liners for development or a single ad-hoc run are shown
below. Outside a cluster, also pass `--kubeconfig` or set a valid `KUBECONFIG`.
Prefer `device.gnoi.transportSecurity: tls` and `device.gnoi.port: 9339` in a
local config (or `spec.gnoi` in a `CiscoDevice`) for IOS-XE 17.18.x. The
environment variables are retained for deployment-level compatibility and
take precedence over those fields.

```bash
# Use the unauthenticated insecure gNXI listener (legacy lab/dev only):
CISCO_VK_GNOI_INSECURE=1 cisco-vk run --config /etc/cisco-vk/cat9300-1.yaml

# Enable the software upgrade reconciler:
CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1 cisco-vk run --config /etc/cisco-vk/cat9300-1.yaml

# Disable gNOI client entirely (device has no gNxI listener):
CISCO_VK_GNOI_DISABLED=1 cisco-vk run --config /etc/cisco-vk/cat9300-1.yaml
```

---

## cisco-vk manager

Starts the Kubernetes controller manager. It watches `CiscoDevice` resources
and creates their per-device VK ConfigMaps and Deployments, watches
`NetworkController` resources and creates an isolated, namespace-scoped worker
for each registered adapter type, and runs the `IOSXEConfigBundle` fan-out
controller. The manager performs no product-controller API calls. Unknown or
unregistered types produce no worker Deployment. If a separately supplied
worker image has a different descriptor, its process exits before adapter
setup or an external controller request. Worker Pod arguments also bind the
`NetworkController` generation used to build its credential and CA projections;
a stale Pod rejects a live generation mismatch before adapter setup.

!!! warning "Alpha controller extension"
    The September image registers zero product adapters. The generic API and
    worker boundary are Alpha and report-only: no Catalyst Center, APIC,
    Meraki, or other controller is contacted, and there is no apply, prune, or
    remote-delete runtime or mutation RBAC role in this release.

### Flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `--metrics-bind-address` | — | `:8080` | Prometheus metrics endpoint. |
| `--health-probe-bind-address` | — | `:8081` | `/healthz` and `/readyz` probes. |
| `--leader-elect` | — | `false` | Enable leader election for HA deployments. |
| `--vk-image` | — | `ghcr.io/cisco/virtual-kubelet-cisco:latest` | Container image for per-device VK pods. |
| `--controller-worker-image` | — | `ghcr.io/cisco/virtual-kubelet-cisco:latest` | Adapter-bearing image for isolated `NetworkController` workers. Its registered descriptor must match the manager's descriptor digest. |
| `--controller-worker-image-pull-policy` | — | `IfNotPresent` | Worker image pull policy: `Always`, `IfNotPresent`, or `Never`. |
| `--vk-service-account` | — | `cisco-virtual-kubelet` | Service account injected into VK Deployments. |
| `--enable-config-aggregator` | — | `false` | Run `IOSXEConfig` reconciliation in-process rather than in each VK pod (experimental). |
| `--log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `--controller-info-log-rate-limit` | — | `100` | Max info log lines/sec from the reconciler. |

The image values above are binary direct-invocation fallbacks. The Helm chart
passes explicit values with separate meanings:

- `controllerImage` selects both the manager Deployment and the isolated
  network-controller worker image. This guarantees the worker contains the
  same adapter registrations as the manager; the descriptor digest provides a
  second fail-closed check.
- `controllerImage.pullPolicy` becomes
  `--controller-worker-image-pull-policy` as well as the manager Pod's pull
  policy.
- `vkImage` selects only per-device VK Deployments created for `CiscoDevice`.
  It does not select a network-controller worker image.
- The shared `image` is the fallback for both overrides. Its published default
  is `ghcr.io/cisco-open/cisco-virtual-kubelet:<chart-appVersion>`.

When starting `cisco-vk manager` outside Helm, set both worker image flags and
`--vk-image` explicitly for the runtime images and pull behavior you intend.
The manager-generated network-controller worker does not currently propagate
`imagePullSecrets`; use a registry the cluster can pull anonymously for this
Alpha scaffold. Private-registry worker support requires a future explicit
credential-propagation contract.

---

## Helm chart values

Install or upgrade with the bundled chart:

```bash
helm upgrade --install cvk ./charts/cisco-virtual-kubelet \
  --namespace cvk-system \
  --create-namespace \
  --set controller.leaderElect=true
```

The image tag defaults to the chart's `appVersion`. `controllerImage` always
applies to both the manager and its network-controller workers; `vkImage`
applies only to per-device VK pods. Use the overrides when those runtime
families must run different images.

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
