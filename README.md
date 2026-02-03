# Cisco Virtual Kubelet Provider

[![Go Version](https://img.shields.io/badge/Go-1.23+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

A [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) provider that enables [Kubernetes](https://kubernetes.io/docs/home/) to schedule container workloads on Cisco Catalyst series switches and other IOS-XE devices with [App-Hosting](https://developer.cisco.com/docs/app-hosting/) capabilities.

## Overview

This provider allows Kubernetes pods to be deployed as containers directly on Cisco devices, enabling edge computing scenarios where compute workloads run on network infrastructure. The provider communicates with Cisco devices via RESTCONF APIs to manage the container lifecycle.

### Key Features

- **Native Kubernetes Integration**: Deploy containers to Cisco devices using standard `kubectl` commands
- **Driver-Based Architecture**: Extensible driver pattern currently supporting Catalyst 8Kv device type (IOS-XE)
- **Full Lifecycle Management**: Create, monitor, and delete containers via RESTCONF
- **Health Monitoring**: Continuous node health checks and status reporting
- **Resource Management**: CPU, memory, and storage allocation per container
- **Flexible Networking**: Support both DHCP IP allocation via Virtual Port Groups
- **DHCP Integration**: Automatic IP discovery from device operational data or ARP tables

### Supported Devices

- Cisco Catalyst 8000V virtual routers

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
    │  Cisco C8Kv1  │   │  Cisco C8Kv2  │   │  Cisco C8KvN  │
    │  ┌─────────┐  │   │  ┌─────────┐  │   │  ┌─────────┐  │
    │  │Container│  │   │  │Container│  │   │  │Container│  │
    │  └─────────┘  │   │  └─────────┘  │   │  └─────────┘  │
    └───────────────┘   └───────────────┘   └───────────────┘
```

## Quick Start

### Prerequisites

- [Go](https://go.dev/doc/devel/release) 1.23 or later
- A Kubernetes cluster
- Cisco IOS-XE device with:
  - IOx enabled (`iox` configuration)
  - RESTCONF enabled
  - App-hosting support
  - Container image (tar file) on device flash

### Installation

```bash
# Clone the repository
git clone https://github.com/cisco-open/cisco-virtual-kubelet.git
cd cisco-virtual-kubelet/

# Ensure the correct Go version is available
sudo which go
sudo go version

# Build the provider
make build

# Install the binary
sudo make install
```

### Configuration

The provider uses a two-tier YAML configuration with `device` and `kubelet` sections:

```yaml
# ./dev/config-dhcp-test.yaml
device:
  name: cat8kv-router
  driver: XE
  address: "192.0.2.24" # Update with Router IP Address
  port: 443
  username: admin
  password: cisco
  tls:
    enabled: true
    insecureSkipVerify: true
  networking:
    dhcpEnabled: true
    virtualPortGroup: "0"
    defaultVRF: ""

kubelet:
  node_name: "cat8kv-node"
  namespace: ""
  update_interval: "30s"
  os: "Linux"
  node_internal_ip: "192.0.2.24" # Update with Router IP Address
```

See [Configuration Reference](docs/CONFIGURATION.md) for all options.

**Export KUBECONFIG**

```bash
export KUBECONFIG=~/.kube/config # Location of the Kubernetes cluster kubeconfig
```


**Start Provider**

```bash
cd ~/cisco-virtual-kubelet
cisco-vk --config dev/config-dhcp-test.yaml
```

**Deploy test Pod**

```yaml
# ./dev/test-pod-dhcp.yaml
apiVersion: v1
kind: Pod
metadata:
  name: dhcp-test-pod
  namespace: default
spec:
  nodeName: cat8kv-node # Virtual Kubelet Kubernetes Node name
  containers:
  - name: test-app
    image: flash:/hello-app.iosxe.tar # Docker image on flash filesystem
    resources:
      requests:
        memory: "64Mi"
        cpu: "250m"
      limits:
        memory: "128Mi"
        cpu: "500m"
```

```bash
kubectl apply -f ./dev/test-pod-dhcp.yaml
```

## Documentation

- [Configuration Reference](docs/CONFIGURATION.md) - Configuration options and device setup
- [Architecture](docs/ARCHITECTURE.md) - Technical architecture details
- [API Reference](docs/API.md) - RESTCONF API details
- [API Reference](docs/API.md) - RESTCONF API details

## Project Structure

```
cisco-virtual-kubelet/
├── cmd/
│   └── virtual-kubelet/        # Main entry point
│       ├── main.go
│       └── root.go             # CLI command setup
├── internal/                   # Internal packages
│   ├── config/                 # Configuration loading and types
│   │   ├── config.go           # Config loading logic
│   │   └── types.go            # Config struct definitions
│   ├── provider/               # Virtual Kubelet provider
│   │   ├── provider.go         # AppHostingProvider implementation
│   │   └── defaults.go         # Default node configuration
│   └── drivers/                # Device driver implementations
│       ├── factory.go          # Driver factory pattern
│       ├── common/             # Shared driver utilities
│       │   ├── restconf_client.go  # RESTCONF HTTP client
│       │   ├── types.go        # Common types
│       │   ├── naming.go       # App naming conventions
│       │   └── helpers.go      # Utility functions
│       ├── iosxe/              # IOS-XE driver
│       │   ├── driver.go       # XEDriver implementation
│       │   ├── app_hosting.go  # App lifecycle operations
│       │   ├── pod_lifecycle.go # Pod CRUD operations
│       │   ├── converters.go   # K8s to IOS-XE conversion
│       │   └── models.go       # YANG model structs
│       └── fake/               # Fake driver for testing
│           └── driver.go
├── examples/
│   ├── configs/                # Example configuration files
│   └── manifests/              # Example Kubernetes manifests
├── dev/                        # Development environment setup
├── docs/                       # Documentation
├── Makefile                    # Build automation
├── go.mod                      # Go module definition (Go 1.23.4)
└── README.md
```

## Integration with Virtual Kubelet

The provider implements the Virtual Kubelet provider interface:

```go
import (
    "github.com/virtual-kubelet/virtual-kubelet/node"
    "github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
    "github.com/cisco/virtual-kubelet-cisco/internal/config"
    "github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

func main() {
    // Load configuration
    cfg, _ := config.LoadConfig(configPath)

    // Create provider factory function
    newProviderFunc := func(vkCfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
        p, err := provider.NewAppHostingProvider(ctx, cfg, vkCfg)
        if err != nil {
            return nil, nil, err
        }
        n, _ := provider.NewAppHostingNode(ctx, cfg, vkCfg)
        return p, n, nil
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
