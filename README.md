# Cisco Virtual Kubelet Provider

[![Go Version](https://img.shields.io/badge/Go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

A [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) provider that enables [Kubernetes](https://kubernetes.io/docs/home/) to schedule container workloads on Cisco Catalyst series switches and other IOS-XE devices — with Beta support for Cisco Nexus (NX-OS) switches — that offer [App-Hosting](https://developer.cisco.com/docs/app-hosting/) capabilities.

## Overview

This provider allows Kubernetes pods to be deployed as containers directly on Cisco devices, enabling edge computing scenarios where compute workloads run on network infrastructure. The provider communicates with Cisco devices over their native management APIs — RESTCONF on IOS-XE, and NX-API (CLI and REST/DME) on NX-OS — to manage the full container and device lifecycle.

### Key Features

- **Native Kubernetes Integration** — deploy containers to Cisco devices using standard `kubectl` commands
- **Driver-Based Architecture** — extensible driver pattern supporting IOS-XE devices, with Beta support for NX-OS
- **Full App-Hosting Lifecycle** — create, monitor, and delete containers via RESTCONF (IOS-XE) or NX-API CLI (NX-OS)
- **Network as Code** — declare device configuration in Kubernetes (`IOSXEConfig`, plus the `NXOSConfig` CRD *(Beta)*) with continuous drift detection and transactional apply
- **NX-OS Support** *(Beta)* — app-hosting lifecycle over NX-API CLI and declarative `NXOSConfig` over NX-API REST/DME; an initial runtime slice covering the `system`, `feature`, `feature_set`, `vlan`, and `interface_ethernet` families
- **Software Lifecycle** *(Beta)* — drive IOS-XE software upgrades via the `IOSXESoftwareUpgrade` CRD using gNOI OS install/activate/verify
- **Device Operations** *(Beta)* — run auditable `show` commands and read-only gNOI probes from Kubernetes via `DeviceOperation` CRD
- **IOS-XE Telemetry** *(Beta)* — declare MDT-over-gNMI subscriptions and emit OpenTelemetry metrics, logs, and state-transition traces
- **Topology Observability** *(Beta)* — emit CDP/OSPF topology and hosted-app traces to any OTLP-compatible backend
- **Health Monitoring** — continuous node health checks, kubelet metrics (`/stats/summary`, `/metrics/resource`), and device annotations
- **Resource Management** — CPU, memory, and storage allocation per container
- **Flexible Networking** — DHCP via Virtual Port Groups or AppGigabitEthernet; automatic IP discovery from device operational data

### Supported Devices

- Cisco Catalyst 8000V virtual routers
- Cisco Catalyst 9000 switches
- Cisco Nexus switches (NX-OS) *(Beta)*

See [Production Readiness](docs/production-readiness.md) for the current NX-OS runtime-parity scope and hardening roadmap.

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

### Prerequisites

- A Kubernetes cluster
- `helm` v3
- Cisco IOS-XE device with:
  - IOx enabled (`iox` configuration)
  - RESTCONF enabled
  - App-hosting support
  - Container image (tar file) on device flash

## Controller Deployment (Kubernetes)

The controller watches `CiscoDevice` CRs and automatically creates a VK pod per device. Deploy it via the included Helm chart.

### Install the published chart (recommended)

The chart and its container image are published to GitHub Container Registry with every release — no clone or custom build required:

```bash
helm install cvk oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.8.0 \
  --namespace cvk-system --create-namespace
```

This deploys the signed `ghcr.io/cisco-open/cisco-virtual-kubelet` image by default — no `--set image.*` needed. The chart `--version` is the release normalised to SemVer (e.g. `v2026.08.0` → `2026.8.0`); see [Releases](https://github.com/cisco-open/cisco-virtual-kubelet/releases) for the current version.

Optionally verify the chart signature before installing:

```bash
cosign verify ghcr.io/cisco-open/charts/cisco-virtual-kubelet:2026.8.0 \
  --certificate-identity-regexp "https://github.com/cisco-open/cisco-virtual-kubelet/.github/workflows/release.yml@refs/tags/v.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

> **Upgrades:** Helm installs CRDs only on first install. When upgrading across releases that change CRD schemas, apply the CRDs once after `helm upgrade` — see the post-install notes (`helm get notes cvk -n cvk-system`).

### Install the optional kubectl plugin

The client-side `kubectl-ciscovk` plugin is not required to run the controller.
It adds read-only, ad-hoc IOS-XE diagnostics for operators. After the first
plugin-bearing release following `v2026.08.0` is published and `cisco-vk` is
accepted into the public Krew index, install and upgrade it without building
from source:

```bash
kubectl krew update
kubectl krew install cisco-vk
kubectl cisco-vk version

# Later releases
kubectl krew upgrade cisco-vk
```

The `v2026.08.0` release predates the plugin assets and public index entry.
Before the first plugin-bearing release, use the source-build fallback; between
that release and index acceptance, use its signed archive. See the
[CLI & Plugin Reference](docs/cisco-vk-cli.md) for both verified installation
paths and the exact availability gate.

### Build and push a custom image

```bash
# Build
docker build -t <your-registry>/cisco-vk:latest .

# Push
docker push <your-registry>/cisco-vk:latest
```

### Install from source with a custom image

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

### Create device credentials

Store device credentials in a Kubernetes Secret before creating the `CiscoDevice` CR:

```bash
kubectl create secret generic cat9000-1-creds \
  --from-literal=username=admin \
  --from-literal=password=<device-password>
```

### Create a CiscoDevice CR

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
  credentialSecretRef:
    name: cat9000-1-creds
  tls:
    enabled: true
    insecureSkipVerify: true
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true
          interface: "0"
          guestInterface: 0
```

The controller creates a VK Deployment and a matching Kubernetes virtual node. Pods scheduled to that node are deployed to the device via App-Hosting.

## Documentation

- [Getting Started](docs/getting-started.md) — Installation, first device, and first pod
- [Architecture](docs/ARCHITECTURE.md) — Technical architecture and component deep-dive
- [Configuration Reference](docs/CONFIGURATION.md) — `CiscoDevice` spec options and device setup
- [Network as Code](docs/netascode-config.md) — Declarative `IOSXEConfig`, drift detection, and transactional apply
- [CLI & Plugin Reference](docs/cisco-vk-cli.md) — `cisco-vk` binary and `kubectl-ciscovk` plugin
- [Software Lifecycle](docs/gnoi-software-lifecycle.md) *(Beta)* — IOS-XE upgrades via `IOSXESoftwareUpgrade`
- [Operations Runbook](docs/operations.md) — `DeviceOperation` show commands, CRD upgrade guide
- [Telemetry](docs/telemetry.md) *(Beta)* — MDT-over-gNMI subscriptions and OpenTelemetry
- [Observability](docs/observability.md) — Metrics, traces, and Splunk integration
- [CRD Reference](docs/crds.md) — All custom resource definitions
- [API Reference](docs/API.md) — RESTCONF and kubelet endpoint reference
- [Environment Variables](docs/environment-variables.md) — Complete environment variable reference
- [Security](docs/security.md) — TLS, RBAC, and credential management
- [Troubleshooting](docs/troubleshooting.md) — Common issues and debug techniques

## Project Structure

```
cisco-virtual-kubelet/
├── api/
│   ├── v1alpha1/               # Core CRDs: CiscoDevice, XEConfig
│   ├── config/v1alpha1/        # Config CRDs: IOSXEConfig, IOSXEConfigBundle, IOSXETelemetry
│   └── ops/v1alpha1/           # Ops CRDs: DeviceOperation, IOSXESoftwareUpgrade, IOSXEOperationalAction
├── cmd/
│   └── cisco-vk/               # Unified binary entry point
│       ├── main.go             # cobra root command
│       ├── run.go              # 'run' subcommand — per-device VK provider
│       └── manager.go          # 'manager' subcommand — CRD controller manager
├── charts/
│   └── cisco-virtual-kubelet/  # Helm chart
│       ├── crds/               # CRD manifests (synced by make generate)
│       └── templates/          # RBAC, Deployments, ServiceAccounts
├── config/
│   └── crd/                    # Generated CRDs (source of truth)
├── internal/
│   ├── aggregator/             # In-process config aggregator (experimental)
│   ├── controller/             # CiscoDevice and IOSXEConfigBundle reconcilers
│   ├── drivers/                # Device driver implementations (IOS-XE, fake)
│   │   └── iosxe/
│   │       └── configdriver/   # Network as Code engine, writers, intent resolver
│   ├── provider/               # Virtual Kubelet provider, config reconciler, telemetry
│   └── telemetry/              # MDT gNMI mapper, OTel emit, classifier, correlation
├── examples/
├── dev/                        # Development configs and test resources
├── docs/
├── Makefile
├── go.mod
└── README.md
```

## Development

For local development and testing, the VK provider can be run directly against a cluster without deploying it to Kubernetes.

### Prerequisites

- [Go](https://go.dev/doc/devel/release) 1.23 or later

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

## Contributing

Contributions are welcome! Please read our [Contributing Guide](CONTRIBUTING.md) for details on our code of conduct and the process for submitting pull requests.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Support

- GitHub Issues: For bug reports and feature requests
- Cisco DevNet: [developer.cisco.com](https://developer.cisco.com)

## Acknowledgments

- [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) project
- Cisco IOS-XE and IOx teams
