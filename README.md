# Cisco Virtual Kubelet Provider

[![Go Version](https://img.shields.io/badge/Go-1.26.7%2B-blue.svg)](https://go.dev/)
[![Project code license](https://img.shields.io/badge/Project%20code-Apache%202.0-blue.svg)](LICENSE)

A [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) provider that enables [Kubernetes](https://kubernetes.io/docs/home/) to schedule container workloads on **Cisco Catalyst** series switches and other **IOS-XE devices** — with Beta support for Cisco Nexus (NX-OS) switches — that offer [App-Hosting](https://developer.cisco.com/docs/app-hosting/) capabilities.

## Overview

This provider allows Kubernetes pods to be deployed as containers directly on Cisco devices, enabling edge computing scenarios where compute workloads run on network infrastructure. The provider communicates with Cisco devices over their native management APIs — RESTCONF on IOS-XE, and NX-API (CLI and REST/DME) on NX-OS — to manage the full container and device lifecycle.

### Key Features

| Feature | Status | Description |
| ------- | ------ | ----------- |
| **Native Kubernetes Integration** | GA | Deploy containers to Cisco devices using standard `kubectl` commands |
| **Driver-Based Architecture** | GA | Extensible driver pattern supporting IOS-XE devices, with Beta support for NX-OS |
| **Full App-Hosting Lifecycle** | GA | Create, monitor, and delete containers via RESTCONF (IOS-XE) or NX-API CLI (NX-OS) |
| **Network as Code** | GA | Declare device configuration in Kubernetes (`IOSXEConfig`, plus `NXOSConfig` CRD) with continuous drift detection and transactional apply |
| **Health Monitoring**| GA | Continuous node health checks, kubelet metrics (`/stats/summary`, `/metrics/resource`), and device annotations |
| **Resource Management** | GA | CPU, memory, and storage allocation per container |
| **Flexible Networking** | GA | DHCP via Virtual Port Groups or AppGigabitEthernet; automatic IP discovery from device operational data |
| **NX-OS Support** | Beta | App-hosting lifecycle over NX-API CLI and declarative `NXOSConfig` over NX-API REST/DME; covers the `system`, `feature`, `feature_set`, `vlan`, and `interface_ethernet` families |
| **Software Lifecycle** | Beta | Stream verified images with gNOI or register IOS-XE device files through RESTCONF, then activate and verify through the `IOSXESoftwareUpgrade` CRD |
| **Device Operations** | Beta | Run auditable `show` commands and read-only gNOI probes from Kubernetes via `DeviceOperation` CRD |
| **Secure IOS-XE gNOI** | Beta | Verified TLS, IOS-XE secure-password metadata, and opt-in CSR-based OS-service certificate provisioning |
| **IOS-XE Telemetry**| Beta | Declare MDT-over-gNMI subscriptions and emit OpenTelemetry metrics, logs, and state-transition traces |
| **Topology Observability** | Beta | Emit CDP/OSPF topology and hosted-app traces to any OTLP-compatible backend |
| **Managed Topology and Fleet Rollouts** | Opt-in | Project protected inventory labels for native kube-scheduler affinity/spread and admit bounded IOS-XE campaigns across failure domains |
| **Network Controller Extension API**| Alpha | Generic `NetworkController` and `NetworkControllerConfig` contracts for future controller adapters; ships with zero product adapters (report-only) |
| **Topology-Aware Image Distribution** | Preview | Freeze each target's image URL, digest and Secret identity from topology-scoped sources in an approved `IOSXESoftwareRollout` plan |
| **PDB-Aware Workload Drain** | Dev Preview | Kubernetes Eviction for the documented eligible workload subset before a device upgrade; not general-purpose evacuation or a zero-downtime guarantee |

### Supported Devices

- **Cisco Catalyst 8000V virtual routers**
- **Cisco Catalyst 9000 switches**
- **Cisco Nexus switches (NX-OS)** *(Beta)*

See [Production Readiness](docs/production-readiness.md) for the current NX-OS runtime-parity scope and hardening roadmap.
See [Managed Topology and Rollouts](docs/topology-awareness.md) before enabling
the Kubernetes 1.35+ manager-owned Node and IOS-XE campaign trust boundary.
This development branch extends that foundation with staged IOS-XE activation,
accepted network evidence, diagnostic graphs and bounded drain/recovery
hardening. See [PR #197 scope and future work](docs/topology-merge-scope.md)
for the tested cohort, opt-in safeguards and remaining qualification. These
additions are not part of the historical October feature table below.
See the [October 2026 release candidate notes](docs/releases/v2026.10.0.md)
for the release scope, gates, compatibility boundary, and deferred roadmap.

<details>
<summary><strong>What's new in October 2026 Release?</strong></summary>

Compared with the published September release, October adds secure gNOI
OS-service provisioning and topology-aware software campaigns alongside
native workload placement:

| Operator task | October capability | Boundary |
| --- | --- | --- |
| Place app-hosted Pods | Protected Node labels with Kubernetes affinity and topology spread | The native scheduler places Pods; it does not assess network forwarding safety |
| Upgrade or downgrade IOS-XE devices | Approved `IOSXESoftwareRollout` plans, canaries, domain budgets, pause/cancel and recovery | Combined transfer/activation/verification; no independently approved durable staging |
| Select an image endpoint | Topology-scoped source selection frozen per target | Endpoint selection, not a persistent distributed cache |
| Prepare secure gNOI | Verified TLS, secure-password metadata and opt-in CSR-based certificate provisioning | Isolated gNOI trust; read-only Verify does not install certificates |
| Relocate eligible workloads | Opt-in PDB-aware Eviction and device-clean evidence | Development preview with outstanding qualification; unsupported placement/packages block |
| Separate permissions | Shared app-hosting and network-management ServiceAccounts with RO/RW role options | Managed mode only; native admission and exact worker binding remain required |

> **NOTE**
>
> An operator can declare sites and failure domains, place apps using native
> scheduling constraints, and limit a software campaign's disruption per declared
> domain while selecting a site-specific image server. CVK does not infer
> redundant paths, available link headroom or critical-service availability from
> those labels. Validate those conditions operationally. Kubernetes 1.35+ is the
> managed-mode floor; optional 1.37 TAS conformance is experimental, not a
> requirement or proof of the complete physical group lifecycle.

Read the [topology guide](docs/topology-awareness.md) and
[gNOI upgrade/downgrade runbook](docs/gnoi-iosxe-upgrade-runbook.md) before opting
in. The [October readiness ledger](docs/releases/v2026.10.0-readiness.md)
separates implemented features from the remaining publication gates.

</details>

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                         │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                   Kubernetes API Server                  │  │
│  └──────────────────────────────────────────────────────────┘  │
│                              │                                 │
│              ┌───────────────┼───────────────┐                 │
│              ▼               ▼               ▼                 │
│  ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐   │
│  │  VK Provider    │ │  VK Provider    │ │  VK Provider    │   │
│  │  (Device 1)     │ │  (Device 2)     │ │  (Device N)     │   │
│  └────────┬────────┘ └────────┬────────┘ └────────┬────────┘   │
└───────────┼───────────────────┼───────────────────┼────────────┘
            │ RESTCONF          │ RESTCONF          │ RESTCONF
            ▼                   ▼                   ▼
    ┌───────────────┐   ┌───────────────┐   ┌───────────────┐
    │  Cisco IOS-XE │   │  Cisco IOS-XE │   │  Cisco IOS-XE │
    │  ┌─────────┐  │   │  ┌─────────┐  │   │  ┌─────────┐  │
    │  │Container│  │   │  │Container│  │   │  │Container│  │
    │  └─────────┘  │   │  └─────────┘  │   │  └─────────┘  │
    └───────────────┘   └───────────────┘   └───────────────┘
```

## Quick Start

Explore the [Getting Started](https://cisco-open.github.io/cisco-virtual-kubelet/docs/getting-started/) documentation for the full installation details.

**Prerequisites**

- A Kubernetes cluster
- Helm 3.21+ or 4.2+
- Cisco IOS-XE device with:
  - IOx enabled (`iox` configuration)
  - RESTCONF enabled
  - App-hosting support
  - Container image (tar file) on device flash

### Controller Deployment

The controller is deployed on a Kubernetes cluster and watches `CiscoDevice` Custom Resources (CRs). It automatically creates a Virtual Kubelet (VK) pod per device. Use the Helm chart installation.

#### Step 1: Install the published chart (Recommended)

The examples target October `v2026.10.0`. Until it appears on the public
[Releases page](https://github.com/cisco-open/cisco-virtual-kubelet/releases),
use the published `v2026.9.2` chart instead; October artifacts are not yet available.

The chart and its container image are published to GitHub Container Registry with every release — no clone or custom build required:

```bash
helm install cvk oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.10.0 \
  --namespace cvk-system --create-namespace
```

**NOTE:** This deploys the signed `ghcr.io/cisco-open/cisco-virtual-kubelet` image by default — no `--set image.*` needed. The chart `--version` matches the release's SemVer-compatible CalVer without the leading `v` (for example, `v2026.10.0` → `2026.10.0`); see [Releases](https://github.com/cisco-open/cisco-virtual-kubelet/releases) for the current version.

**Optionally:** Verify the chart signature before installing.

```bash
cosign verify ghcr.io/cisco-open/charts/cisco-virtual-kubelet:2026.10.0 \
  --certificate-identity-regexp "https://github.com/cisco-open/cisco-virtual-kubelet/.github/workflows/release.yml@refs/tags/v.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

<details>
<summary><strong>Upgrade From Earlier Releases</strong></summary>

Helm does not upgrade files under `crds/`. Pull the new chart and apply its
CRDs **before** `helm upgrade`. Back up the live definitions and review the
server-side diff before the explicit ownership handoff from Helm:

```bash
helm pull oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.10.0 --untar
kubectl get customresourcedefinitions.apiextensions.k8s.io -o yaml \
  > cvk-crds-before-upgrade.yaml
kubectl diff --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade -f cisco-virtual-kubelet/crds/
kubectl apply --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade -f cisco-virtual-kubelet/crds/
kubectl wait --for=condition=Established --timeout=60s \
  crd/ciscodevices.cisco.vk \
  crd/iosxesoftwareupgrades.ops.cisco.vk \
  crd/iosxeoperationalactions.ops.cisco.vk \
  crd/iosxesoftwarerollouts.ops.cisco.vk
helm upgrade cvk oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.10.0 --namespace cvk-system
```

`kubectl diff` returns status 1 when differences exist. The force flag applies
only to the exact reviewed CVK CRD files; see the operations runbook before
using it in production.

See the [operations runbook](docs/operations.md#upgrading-crds). If the two new
controller CRDs are absent, CVK preserves existing device reconcilers but
leaves the Alpha controller scaffold disabled until the CRDs are applied and
the manager Deployment is restarted.

</details>

<details>
<summary><strong>(Optional) Install kubectl Plugin</strong></summary>

The client-side `kubectl-ciscovk` plugin is not required to run the controller.
It adds read-only, ad-hoc IOS-XE commands and manager-accepted topology graph
diagnostics for operators. The topology output is observational and cannot
grant rollout authority. Optional administrator-declared peer mappings and
links come from a separate `graph.json` key in the admission-protected
topology-policy ConfigMap and are included in output provenance, not campaign
approval hashes. The plugin is available from the public Krew index,
so install and upgrade it without building from source:

```bash
kubectl krew update
kubectl krew install cisco-vk
kubectl cisco-vk version

# Later releases
kubectl krew upgrade cisco-vk
```

Historical note: `v2026.08.0` predates the plugin assets, and `v2026.8.1` was
the first plugin-bearing release. See the
[CLI & Plugin Reference](docs/cisco-vk-cli.md) for Krew, signed-archive, and
source-build installation paths.

</details>

<details>
<summary><strong>(Optional) Build and Push Custom Image</strong></summary>

```bash
# Build
docker build -t <your-registry>/cisco-vk:latest .

# Push
docker push <your-registry>/cisco-vk:latest
```

#### Install From Source with Custom Image

For development, or to run your own build instead of the published image, install the chart from the source tree and point it at your registry:

```bash
# Install CRDs and the controller into the cvk-system namespace
helm install cvk ./charts/cisco-virtual-kubelet \
  --namespace cvk-system --create-namespace \
  --set image.repository=<your-registry>/cisco-vk \
  --set image.tag=latest
```

Both the controller pod and the VK pods it spawns use the same image by default. To use different images:

```bash
helm install cvk ./charts/cisco-virtual-kubelet \
  --namespace cvk-system --create-namespace \
  --set controllerImage.repository=<your-registry>/cisco-vk-controller \
  --set controllerImage.tag=latest \
  --set vkImage.repository=<your-registry>/cisco-vk \
  --set vkImage.tag=latest
```

</details>

#### Step 2: Create Device Credentials

Store device credentials in a Kubernetes Secret before creating the `CiscoDevice` CR:

```bash
kubectl create secret generic cat9000-1-creds \
  --from-literal=password='replace-me'
```

#### Step 3: Create CiscoDevice CR

Once the controller is running, create a `CiscoDevice` resource to provision a VK node:

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
    name: cat9000-1-creds
  tls:
    enabled: true
    insecureSkipVerify: true    # lab only; do not use this transport for gNOI
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true
          interface: "0"
          guestInterface: 0
```

The controller creates a VK Deployment and a matching Kubernetes virtual node.
Pods scheduled to that node are deployed to the device via App-Hosting. The
minimal example above leaves gNOI in backward-compatible `auto` mode; before
using a gNOI operation, configure
[explicit verified gNOI TLS](docs/gnoi-software-lifecycle.md#secure-ios-xe-gnxi).
For IOS-XE image changes, follow the complete
[gNOI upgrade and downgrade runbook](docs/gnoi-iosxe-upgrade-runbook.md).

## Documentation

- [Getting Started](https://cisco-open.github.io/cisco-virtual-kubelet/docs/getting-started/) — Installation, first device, and first pod
- [Architecture](https://cisco-open.github.io/cisco-virtual-kubelet/docs/ARCHITECTURE/) — Technical architecture and component deep-dive
- [Configuration Reference](https://cisco-open.github.io/cisco-virtual-kubelet/docs/CONFIGURATION/) — `CiscoDevice` spec options and device setup
- [CRD Reference](https://cisco-open.github.io/cisco-virtual-kubelet/docs/crds/) — All custom resource definitions
- [Security](https://cisco-open.github.io/cisco-virtual-kubelet/docs/security/) — TLS, RBAC, and credential management
- [Troubleshooting](https://cisco-open.github.io/cisco-virtual-kubelet/docs/troubleshooting/) — Common issues and debug techniques

## Development

For local development and testing, the VK provider can be run directly against a cluster without deploying it to Kubernetes.

### Prerequisites

- [Go](https://go.dev/doc/devel/release) 1.26.7+ on the 1.26 line, or 1.27.0+
  on the 1.27 line

### Build and run locally

```bash
make build

cisco-vk run \
  --config dev/deviceConfig.yaml \
  --kubeconfig ~/.kube/config \
  --nodename my-test-node
```

The device config file follows the same schema as the `CiscoDevice` CR `spec`. See [examples](examples/configs/device-configs.yaml) for interface/networking options.

**Runtime flags:**

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--nodename` | `VKUBELET_NODE_NAME` | `cisco-vk-<device-address>` | Kubernetes virtual node name; falls back to `cisco-virtual-kubelet` without an address |
| `--config` / `-c` | — | `/etc/virtual-kubelet/config.yaml` | Path to device config YAML |
| `--kubeconfig` | `KUBECONFIG` | in-cluster | Path to kubeconfig file |
| `--log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |

See [CLI Reference](docs/cisco-vk-cli.md) for the full flag and environment variable reference.

### Regenerate RBAC and CRDs

```bash
# Regenerates CRDs → config/crd, RBAC → chart templates, syncs CRDs into chart
make generate
```

## Contact and Community

[![Slack](https://img.shields.io/badge/Slack-Join%20Us-4A154B?logo=slack&logoColor=white)](https://cloud-native.slack.com/archives/C0AN1AGDFRS)

Contributions are always welcome — report bugs, improve docs, or submit code via [GitHub Issues](https://github.com/cisco-open/cisco-virtual-kubelet/issues). Read the [Contributing Guide](CONTRIBUTING.md) for details on our code of conduct and the process for submitting pull requests.

## License

Cisco-authored project code is licensed under the [Apache License 2.0](LICENSE).
Third-party code, generated material, fonts, and documentation tooling retain
their applicable upstream terms; the release artifacts and documentation sites
carry generated license and notice bundles.

## Support

- **GitHub Issues:** For bug reports and feature requests
- **Cisco DevNet:** [developer.cisco.com](https://developer.cisco.com)

## Acknowledgments

- [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) project
- Cisco IOS-XE and IOx teams
