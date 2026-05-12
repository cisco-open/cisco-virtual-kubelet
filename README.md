# Cisco Virtual Kubelet Provider

[![Go Version](https://img.shields.io/badge/Go-1.23+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

A [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) provider that enables [Kubernetes](https://kubernetes.io/docs/home/) to schedule container workloads on Cisco Catalyst series switches and other IOS-XE devices with [App-Hosting](https://developer.cisco.com/docs/app-hosting/) capabilities.

## Overview

This provider allows Kubernetes pods to be deployed as containers directly on Cisco devices, enabling edge computing scenarios where compute workloads run on network infrastructure. The provider communicates with Cisco devices via RESTCONF APIs to manage the container lifecycle.

### Key Features

- **Native Kubernetes Integration**: Deploy containers to Cisco devices using standard `kubectl` commands
- **Driver-Based Architecture**: Extensible driver pattern currently supporting IOS-XE devices
- **Full Lifecycle Management**: Create, monitor, and delete containers via RESTCONF
- **Health Monitoring**: Continuous node health checks and status reporting
- **IOS-XE Telemetry CRD**: Declare MDT-over-gNMI subscriptions and emit OpenTelemetry metrics, logs, and state-transition traces
- **Resource Management**: CPU, memory, and storage allocation per container
- **Environment Variables**: Full support for container environment variables from direct values, Secrets, and ConfigMaps
- **Flexible Networking**: Support both DHCP IP allocation via Virtual Port Groups or AppGigabitEthernet
- **DHCP Integration**: Automatic IP discovery from device operational data or ARP tables

### Supported Devices

- Cisco Catalyst 8000V virtual routers
- Cisco Catalyst 9000 switches

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

### Build and push a custom image

```bash
# Build
docker build -t <your-registry>/cisco-vk:latest .

# Push
docker push <your-registry>/cisco-vk:latest
```

### Install the Helm chart

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
  username: admin
  password: cisco123
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

The controller will create a VK deployment and a matching Kubernetes node. Pods scheduled to that node are deployed to the device via AppHosting.

## Documentation

- [Configuration Reference](docs/CONFIGURATION.md) - Configuration options and device setup
- [Architecture](docs/ARCHITECTURE.md) - Technical architecture details
- [API Reference](docs/API.md) - RESTCONF API details
- [Environment Variables](docs/environment-variables.md) - Complete guide to environment variable support

## Project Structure

```
cisco-virtual-kubelet/
├── api/
│   └── v1alpha1/               # CRD API types (DeviceSpec, CiscoDevice)
├── cmd/
│   └── cisco-vk/               # Unified binary entry point
│       ├── main.go             # cobra root command
│       ├── run.go              # 'run' subcommand — standalone VK provider
│       └── manager.go          # 'manager' subcommand — CRD controller manager
├── charts/
│   └── cisco-virtual-kubelet/  # Helm chart for controller deployment
│       ├── crds/               # CRD (synced from config/crd by make generate)
│       └── templates/          # RBAC, Deployment (role.yaml auto-generated)
├── config/
│   └── crd/                    # Generated CRDs (source of truth for make generate)
├── internal/
│   ├── config/                 # YAML/viper config loader
│   ├── controller/             # CiscoDevice reconciler (+kubebuilder:rbac markers)
│   ├── provider/               # Virtual Kubelet provider implementation
│   └── drivers/                # Device driver implementations (XE, fake)
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
| `--nodename` | `VKUBELET_NODE_NAME` | `cisco-virtual-kubelet` | Kubernetes node name |
| `--config` / `-c` | - | `/etc/virtual-kubelet/config.yaml` | Path to device config file |
| `--kubeconfig` | `KUBECONFIG` | _(in-cluster)_ | Path to kubeconfig file |
| `--log-level` | `LOG_LEVEL` | `info` | Log level: debug, info, warn, error |

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
