# Architecture

This document describes the technical architecture of the Cisco Virtual Kubelet Provider.

## Overview

The Cisco Virtual Kubelet Provider implements the [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) provider interface, enabling Kubernetes to treat Cisco IOS-XE devices as compute nodes for container workloads.

## Component Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│                         Kubernetes Cluster                           │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │                      API Server                                │  │
│  │           CiscoDevice CRD   +   core/v1 resources              │  │
│  └──────────────────────┬─────────────────────────────────────────┘  │
│                         │                                            │
│  ┌──────────────────────┴─────────────────────────────────────────┐  │
│  │             Controller Manager  (cisco-vk manager)             │  │
│  │  Watches CiscoDevice CRs → creates VK Deployment + ConfigMap   │  │
│  └──────────────────────┬─────────────────────────────────────────┘  │
│                         │  (one VK pod per CiscoDevice)              │
│                         ▼                                            │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │             Virtual Kubelet Pod  (cisco-vk run)              │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌────────────────────┐  │    │
│  │  │    Node      │  │     Pod      │  │ AppHostingProvider │  │    │
│  │  │  Controller  │  │  Controller  │  │ AppHostingNode     │  │    │
│  │  └──────────────┘  └──────────────┘  └────────┬───────────┘  │    │
│  │                                               │              │    │
│  │  ┌────────────────────────────────────────────┴───────────┐  │    │
│  │  │                    Driver Layer                        │  │    │
│  │  │  ┌──────────────────────────────────────────────────┐  │  │    │
│  │  │  │            XEDriver (IOS-XE)                     │  │  │    │
│  │  │  │  ┌────────────┐ ┌────────────┐ ┌─────────────┐   │  │  │    │
│  │  │  │  │ Reconciler │ │    Pod     │ │  RESTCONF   │   │  │  │    │
│  │  │  │  │ + Device   │ │  Lifecycle │ │   Client    │   │  │  │    │
│  │  │  │  └────────────┘ └────────────┘ └─────────────┘   │  │  │    │
│  │  │  └──────────────────────────────────────────────────┘  │  │    │
│  │  └────────────────────────────────────────────────────────┘  │    │
│  └──────────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ RESTCONF/HTTPS
                                    ▼
┌──────────────────────────────────────────────────────────────────────┐
│                   Cisco Catalyst 8000V (IOS-XE)                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │                       IOx Platform                             │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐      │  │
│  │  │  Container 1 │  │  Container 2 │  │   Container N    │      │  │
│  │  │   (app)      │  │   (app)      │  │   (app)          │      │  │
│  │  └──────────────┘  └──────────────┘  └──────────────────┘      │  │
│  │                           │                                    │  │
│  │  ┌────────────────────────┴──────────────────────────────────┐ │  │
│  │  │          VirtualPortGroup / AppGigEth / Mgmt + DHCP       │ │  │
│  │  └───────────────────────────────────────────────────────────┘ │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```

## Core Components

### AppHostingProvider

The main provider struct that implements the Virtual Kubelet `nodeutil.Provider` interface:

```go
// internal/provider/provider.go
type AppHostingProvider struct {
    ctx             context.Context
    deviceSpec      *v1alpha1.DeviceSpec
    driver          drivers.CiscoKubernetesDeviceDriver
    podsLister      corev1listers.PodLister
    configMapLister corev1listers.ConfigMapLister
    secretLister    corev1listers.SecretLister
    serviceLister   corev1listers.ServiceLister
}
```

**Implemented Interface Methods**:

- `CreatePod(ctx, pod)` - Deploy container to device
- `UpdatePod(ctx, pod)` - Update container configuration
- `DeletePod(ctx, pod)` - Remove container from device
- `GetPod(ctx, namespace, name)` - Get pod with status
- `GetPodStatus(ctx, namespace, name)` - Get pod status only
- `GetPods(ctx)` - List all pods on device

### AppHostingNode

Implements the `node.NodeProvider` interface for node heartbeat management:

```go
// internal/provider/provider.go
type AppHostingNode struct{}

func (a *AppHostingNode) Ping(ctx context.Context) error
func (a *AppHostingNode) NotifyNodeStatus(ctx context.Context, cb func(*v1.Node))
```

### Driver Factory

The driver factory pattern allows extensible device support:

```go
// internal/drivers/factory.go
func NewDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
    switch spec.Driver {
    case v1alpha1.DeviceDriverFAKE:
        return fake.NewAppHostingDriver(ctx, spec)
    case v1alpha1.DeviceDriverXE:
        return iosxe.NewAppHostingDriver(ctx, spec)
    case v1alpha1.DeviceDriverXR:
        return nil, fmt.Errorf("unsupported device type")
    default:
        return nil, fmt.Errorf("unsupported device type")
    }
}

type CiscoKubernetesDeviceDriver interface {
    GetDeviceResources(ctx context.Context) (*v1.ResourceList, error)
    DeployPod(ctx context.Context, pod *v1.Pod) error
    UpdatePod(ctx context.Context, pod *v1.Pod) error
    DeletePod(ctx context.Context, pod *v1.Pod) error
    GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error)
    ListPods(ctx context.Context) ([]*v1.Pod, error)
}
```

### XEDriver (IOS-XE Driver)

Implements the device driver for Cisco IOS-XE app-hosting:

```go
// internal/drivers/iosxe/driver.go
type XEDriver struct {
    config       *v1alpha1.DeviceSpec
    client       common.NetworkClient
    marshaller   func(any) ([]byte, error)
    unmarshaller UnmarshalFunc
}
```

**Key Methods**:
- `CheckConnection(ctx)` - Validate device connectivity
- `GetDeviceResources(ctx)` - Report available resources
- `DeployPod(ctx, pod)` - Full pod deployment lifecycle
- `DeletePod(ctx, pod)` - Full pod deletion lifecycle

### RestconfClient

HTTP client for RESTCONF API communication:

```go
// internal/drivers/common/restconf_client.go
type RestconfClient struct {
    BaseURL    string
    HTTPClient *http.Client
    Username   string
    Password   string
}

func (c *RestconfClient) Get(ctx, path, result, unmarshal) error
func (c *RestconfClient) Post(ctx, path, payload, marshal) error
func (c *RestconfClient) Patch(ctx, path, payload, marshal) error
func (c *RestconfClient) Delete(ctx, path) error
```

## API Communication

### RESTCONF Endpoints

| Operation | Method | Endpoint |
|-----------|--------|----------|
| Configure App | POST | `/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps` |
| Install | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Activate | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Start | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Stop | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Deactivate | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Uninstall | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Delete Config | DELETE | `/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app={appID}` |
| Get State | GET | `/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data` |
| Get ARP | GET | `/restconf/data/Cisco-IOS-XE-arp-oper:arp-data` |

### YANG Models Used

- `Cisco-IOS-XE-app-hosting-cfg.yang` - App-hosting configuration
- `Cisco-IOS-XE-app-hosting-oper.yang` - App-hosting operational state
- `Cisco-IOS-XE-rpc.yang` - RPC operations (install, activate, start, etc.)
- `Cisco-IOS-XE-arp-oper.yang` - ARP table for IP discovery

## Data Flow

### Pod Creation Flow

```
1. kubectl apply -f pod.yaml
         │
         ▼
2. Kubernetes API Server
         │
         ▼
3. Virtual Kubelet Pod Controller
         │
         ▼
4. AppHostingProvider.CreatePod()
         │
         ▼
5. XEDriver.DeployPod()
         │
         ▼
6. XEDriver.CreatePodApps()
         │
         ├─► Configure app-hosting (RESTCONF POST)
         │   - Set VirtualPortGroup interface
         │   - Set resource profile (CPU, memory, disk)
         │   - Set container labels for discovery
         │
         ├─► InstallApp (RESTCONF RPC: install)
         │
         ├─► WaitForAppStatus("DEPLOYED")
         │
         ├─► ActivateApp (RESTCONF RPC: activate)
         │
         ├─► WaitForAppStatus("ACTIVATED")
         │
         └─► Start is automatic (start: true in config)
         │
         ▼
7. Container receives DHCP IP from device pool
         │
         ▼
8. Pod status updated via GetPodStatus()
   - IP discovered from oper-data or ARP table
```

### Pod Deletion Flow

```
1. kubectl delete pod <name>
         │
         ▼
2. AppHostingProvider.DeletePod()
         │
         ▼
3. XEDriver.DeletePod()
         │
         ▼
4. For each container:
         │
         ├─► StopApp (RESTCONF RPC: stop)
         │
         ├─► WaitForAppStatus("ACTIVATED")
         │
         ├─► DeactivateApp (RESTCONF RPC: deactivate)
         │
         ├─► WaitForAppStatus("DEPLOYED")
         │
         ├─► UninstallApp (RESTCONF RPC: uninstall)
         │
         ├─► WaitForAppNotPresent()
         │
         └─► Delete config (RESTCONF DELETE)
```

### Pod Status Discovery

The provider discovers pod status by:

1. **Container Discovery**: Query app-hosting config for apps with matching pod UID labels
2. **State Mapping**: Map IOS-XE app states to Kubernetes container states
3. **IP Discovery**:
   - First, check `app-hosting-oper-data` for IPv4 address
   - Fallback: Look up container MAC address in ARP table

## State Management

### Pod State Mapping

| Kubernetes Phase | IOS-XE App State |
|-----------------|------------------|
| Pending | INSTALLING, DEPLOYED, ACTIVATED |
| Running | RUNNING |
| Succeeded | STOPPED |
| Failed | ERROR |

### Container Labels

Containers are tagged with labels in the `--run-opts` for discovery:

```
--label cisco.vk/pod-name=<pod-name>
--label cisco.vk/pod-namespace=<namespace>
--label cisco.vk/pod-uid=<uid>
--label cisco.vk/container-name=<container-name>
```

### App Naming Convention

App IDs are generated from pod metadata:
```
{pod-uid-without-hyphens}-{container-name-hash}
```

## Networking

### DHCP Mode

When `dhcpEnabled: true`:
1. App-hosting is configured with only the VirtualPortGroup interface number
2. Container requests IP from device DHCP pool
3. Provider discovers IP from:
   - `app-hosting-oper-data` network interfaces (preferred)
   - ARP table lookup using container MAC address (fallback)

### Network Configuration Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                    Catalyst 8000V                                │
│                                                                  │
│  ┌────────────┐    ┌───────────────────┐    ┌─────────────────┐  │
│  │ Container  │───►│ VirtualPortGroup0 │───►│   DHCP Pool     │  │
│  │ (eth0)     │    │  192.168.1.254    │    │   192.168.1.0/24│  │
│  │            │◄───│  (gateway)        │◄───│   assigns IP    │  │
│  └────────────┘    └───────────────────┘    └─────────────────┘  │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

## Deployment Model

The project uses a **Helm chart** to deploy a Controller Manager into the cluster.
For each `CiscoDevice` custom resource the controller creates:

1. A **ConfigMap** containing the device configuration YAML.
2. A **Deployment** running the `cisco-vk run` binary, which registers a
   Virtual Kubelet node for that device.

This means adding a new device is as simple as `kubectl apply -f ciscodevice.yaml`.

## Project Structure

```
cisco-virtual-kubelet/
├── api/v1alpha1/                 # CRD API types
│   ├── doc.go
│   ├── groupversion_info.go
│   ├── types.go                  # DeviceSpec, CiscoDevice, shared types
│   ├── xe_types.go               # IOS-XE driver-specific types
│   └── zz_generated.deepcopy.go  # Generated DeepCopy methods
├── charts/cisco-virtual-kubelet/ # Helm chart
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── crds/                     # CRD installed by Helm
│   └── templates/                # Deployment, RBAC, ServiceAccount
├── cmd/cisco-vk/                 # Binary entry point
│   ├── main.go                   # Cobra root command
│   ├── manager.go                # "manager" subcommand (controller)
│   └── run.go                    # "run" subcommand (VK provider)
├── config/crd/                   # Standalone CRD YAML
│   └── cisco.vk_ciscodevices.yaml
├── examples/configs/             # Example device configurations
├── internal/
│   ├── config/                   # Configuration loader
│   │   └── config.go             # YAML/viper loader → DeviceSpec
│   ├── controller/               # Kubernetes controller
│   │   └── ciscodevice_controller.go  # CiscoDevice reconciler
│   ├── provider/                 # VK Provider
│   │   ├── provider.go           # AppHostingProvider + AppHostingNode
│   │   └── defaults.go           # Node defaults
│   └── drivers/                  # Device drivers
│       ├── factory.go            # Driver factory
│       ├── common/               # Shared code
│       │   ├── restconf_client.go
│       │   ├── types.go
│       │   ├── naming.go
│       │   └── helpers.go
│       ├── iosxe/                # IOS-XE driver
│       │   ├── driver.go         # XEDriver + factory
│       │   ├── client.go         # RESTCONF client wrapper
│       │   ├── device.go         # Device info queries
│       │   ├── reconciler.go     # App-hosting reconciler
│       │   ├── pod_lifecycle.go  # Pod deploy / delete
│       │   ├── pod_transforms.go # K8s pod → IOS-XE config
│       │   ├── status_transforms.go # IOS-XE state → K8s status
│       │   ├── ip_discovery.go   # IP lookup (oper-data + ARP)
│       │   ├── types.go          # Driver-internal types
│       │   └── models.go         # YANG structs
│       └── fake/                 # Test driver
│           └── driver.go
└── docs/                         # Documentation + website
```