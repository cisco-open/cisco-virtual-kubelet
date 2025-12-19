# Cisco Virtual Kubelet Provider

[![Go Version](https://img.shields.io/badge/Go-1.21+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

A [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) provider that enables Kubernetes to schedule container workloads on Cisco Catalyst 9000 series switches and other IOS-XE devices with app-hosting capabilities.

## Overview

This provider allows Kubernetes pods to be deployed as containers directly on Cisco network devices, enabling edge computing scenarios where compute workloads run on network infrastructure. The provider communicates with Cisco devices via RESTCONF APIs to manage the complete container lifecycle.

### Key Features

- **Native Kubernetes Integration**: Deploy containers to Cisco devices using standard `kubectl` commands
- **Multi-Node Support**: Manage multiple Cisco devices as Kubernetes nodes
- **Full Lifecycle Management**: Create, monitor, and delete containers via RESTCONF
- **Health Monitoring**: Continuous node health checks and status reporting
- **Resource Management**: CPU, memory, and storage allocation per container
- **Network Configuration**: Automatic IP address and VLAN configuration

### Supported Devices

- Cisco Catalyst 9300/9400/9500 series switches
- Cisco Catalyst 8000 series routers
- Any IOS-XE device with IOx/app-hosting support (17.x+)

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                          │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                   Kubernetes API Server                   │  │
│  └──────────────────────────────────────────────────────────┘  │
│                              │                                  │
│              ┌───────────────┼───────────────┐                 │
│              ▼               ▼               ▼                 │
│  ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐  │
│  │  VK Provider    │ │  VK Provider    │ │  VK Provider    │  │
│  │  (Device 1)     │ │  (Device 2)     │ │  (Device N)     │  │
│  └────────┬────────┘ └────────┬────────┘ └────────┬────────┘  │
└───────────┼───────────────────┼───────────────────┼────────────┘
            │ RESTCONF          │ RESTCONF          │ RESTCONF
            ▼                   ▼                   ▼
    ┌───────────────┐   ┌───────────────┐   ┌───────────────┐
    │  Cisco C9K    │   │  Cisco C9K    │   │  Cisco C8K    │
    │  ┌─────────┐  │   │  ┌─────────┐  │   │  ┌─────────┐  │
    │  │Container│  │   │  │Container│  │   │  │Container│  │
    │  └─────────┘  │   │  └─────────┘  │   │  └─────────┘  │
    └───────────────┘   └───────────────┘   └───────────────┘
```

## Quick Start

### Prerequisites

- Go 1.21 or later
- Kubernetes cluster (k3s, k8s, or similar)
- Cisco IOS-XE device with:
  - IOx enabled (`iox` configuration)
  - RESTCONF enabled
  - App-hosting support
  - Container image (tar file) on device flash

### Installation

```bash
# Clone the repository
git clone https://github.com/cisco/virtual-kubelet-cisco.git
cd virtual-kubelet-cisco

# Build the provider
make build

# Install the binary
sudo make install
```

### Configuration

1. Create a device configuration file:

```yaml
# /etc/cisco-vk/config.yaml
nodeName: cisco-switch-01

devices:
  - name: c9k-switch
    type: c9k
    address: 192.168.1.100
    port: 443
    username: admin
    password: cisco123
    useHTTPS: true
    verifyTLS: false

networkConfig:
  defaultGateway: "192.168.1.1"
  subnetMask: "255.255.255.0"
  ipPoolStart: "192.168.1.200"
  ipPoolEnd: "192.168.1.210"
```

2. Start the provider:

```bash
cisco-vk --config /etc/cisco-vk/config.yaml
```

3. Deploy a container:

```yaml
# nginx-pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: nginx-on-switch
spec:
  nodeSelector:
    kubernetes.io/hostname: cisco-switch-01
  containers:
  - name: nginx
    image: flash:/nginx.tar
    resources:
      requests:
        cpu: "500m"
        memory: "256Mi"
```

```bash
kubectl apply -f nginx-pod.yaml
```

## Documentation

- [Installation Guide](docs/INSTALL.md) - Detailed installation instructions
- [Configuration Reference](docs/CONFIGURATION.md) - All configuration options
- [Deployment Guide](docs/DEPLOYMENT.md) - Production deployment patterns
- [Architecture](docs/ARCHITECTURE.md) - Technical architecture details
- [Troubleshooting](docs/TROUBLESHOOTING.md) - Common issues and solutions
- [API Reference](docs/API.md) - RESTCONF API details

## Project Structure

```
virtual-kubelet-cisco/
├── cmd/
│   └── virtual-kubelet/     # Main entry point
│       └── main.go
├── pkg/                     # Cisco provider implementation
│   ├── provider.go          # Main provider interface
│   ├── app_hosting_manager.go
│   ├── restconf_app_hosting.go
│   ├── device_manager.go
│   ├── ios_xe_client.go
│   ├── config.go
│   └── ...
├── examples/
│   ├── configs/             # Example configuration files
│   ├── manifests/           # Example Kubernetes manifests
│   └── systemd/             # Systemd service files
├── docs/                    # Documentation
├── scripts/                 # Helper scripts
├── Makefile                 # Build automation
├── go.mod                   # Go module definition
└── README.md
```

## Integration with Virtual Kubelet

This provider implements the Virtual Kubelet provider interface and can be used as a drop-in provider for the main Virtual Kubelet project:

```go
import (
    "github.com/virtual-kubelet/virtual-kubelet/node"
    "github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
    cisco "github.com/cisco/virtual-kubelet-cisco/pkg"
)

func main() {
    // Create provider factory function
    newProviderFunc := func(cfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
        provider, err := cisco.NewCiscoProvider(configPath, nodeName, "Linux", internalIP, 10250)
        if err != nil {
            return nil, nil, err
        }
        return provider, provider, nil
    }
    
    // Create and run node
    n, _ := nodeutil.NewNode(nodeName, newProviderFunc, nodeutil.WithClient(clientset))
    n.Run(ctx)
}
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
