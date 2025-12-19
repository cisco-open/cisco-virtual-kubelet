# Architecture

This document describes the technical architecture of the Cisco Virtual Kubelet Provider.

## Overview

The Cisco Virtual Kubelet Provider implements the [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) provider interface, enabling Kubernetes to treat Cisco IOS-XE devices as compute nodes.

## Component Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│                         Kubernetes Cluster                            │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │                      API Server                                 │  │
│  └─────────────────────────────┬──────────────────────────────────┘  │
│                                │                                      │
│  ┌─────────────────────────────┴──────────────────────────────────┐  │
│  │                    Virtual Kubelet Provider                     │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │  │
│  │  │    Node      │  │     Pod      │  │   Provider           │  │  │
│  │  │  Controller  │  │  Controller  │  │   (CiscoProvider)    │  │  │
│  │  └──────────────┘  └──────────────┘  └──────────┬───────────┘  │  │
│  │                                                  │              │  │
│  │  ┌───────────────────────────────────────────────┴───────────┐  │  │
│  │  │                    Core Components                         │  │  │
│  │  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────────────┐  │  │  │
│  │  │  │   Device    │ │  Network    │ │    App Hosting      │  │  │  │
│  │  │  │   Manager   │ │  Manager    │ │    Manager          │  │  │  │
│  │  │  └─────────────┘ └─────────────┘ └─────────────────────┘  │  │  │
│  │  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────────────┐  │  │  │
│  │  │  │  Resource   │ │  Lifecycle  │ │    RESTCONF         │  │  │  │
│  │  │  │  Monitor    │ │  Manager    │ │    Client           │  │  │  │
│  │  │  └─────────────┘ └─────────────┘ └─────────────────────┘  │  │  │
│  │  └───────────────────────────────────────────────────────────┘  │  │
│  └─────────────────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ RESTCONF/HTTPS
                                    ▼
┌───────────────────────────────────────────────────────────────────────┐
│                        Cisco IOS-XE Device                            │
│  ┌─────────────────────────────────────────────────────────────────┐  │
│  │                       IOx Platform                               │  │
│  │  ┌───────────────┐  ┌───────────────┐  ┌───────────────────┐   │  │
│  │  │  Container 1  │  │  Container 2  │  │   Container N     │   │  │
│  │  │   (nginx)     │  │   (app)       │  │   (service)       │   │  │
│  │  └───────────────┘  └───────────────┘  └───────────────────┘   │  │
│  └─────────────────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────────────┘
```

## Core Components

### CiscoProvider

The main provider struct that implements the Virtual Kubelet interfaces:

```go
type CiscoProvider struct {
    nodeName           string
    operatingSystem    string
    internalIP         string
    daemonEndpointPort int32
    
    config         *CiscoConfig
    deviceManager  *DeviceManager
    networkManager *NetworkManager
    monitor        *ResourceMonitor
    
    pods       sync.Map
    containers sync.Map
}
```

**Implemented Interfaces**:
- `node.PodLifecycleHandler` - Pod creation, update, deletion
- `node.PodNotifier` - Pod status notifications
- `node.NodeProvider` - Node status and configuration

### DeviceManager

Manages connections to multiple Cisco devices:

```go
type DeviceManager struct {
    devices map[string]*Device
    mutex   sync.RWMutex
}

type Device struct {
    Config        DeviceConfig
    Client        *IOSXEClient
    AppHostingMgr *AppHostingManager
    Status        DeviceStatus
}
```

**Responsibilities**:
- Device connection pooling
- Health monitoring
- Load balancing across devices
- Capacity tracking

### AppHostingManager

Orchestrates the container lifecycle on devices:

```go
type AppHostingManager struct {
    client        *IOSXEClient
    restconfClient *RESTCONFAppHostingClient
    lifecycle     *LifecycleManager
    useRESTCONF   bool
}
```

**Container Lifecycle**:
1. `Configure` - Create app-hosting configuration
2. `Install` - Install container package from flash
3. `Activate` - Activate the application
4. `Start` - Start the container
5. `Stop` - Stop the container
6. `Deactivate` - Deactivate the application
7. `Uninstall` - Remove the application

### RESTCONFAppHostingClient

Handles all RESTCONF API communication:

```go
type RESTCONFAppHostingClient struct {
    baseURL    string
    httpClient *http.Client
    username   string
    password   string
}
```

**Key Methods**:
- `Configure(ctx, appID, config)` - Configure app-hosting
- `Install(ctx, appID, imagePath)` - Install RPC
- `Activate(ctx, appID)` - Activate RPC
- `Start(ctx, appID)` - Start RPC
- `Stop(ctx, appID)` - Stop RPC
- `Deactivate(ctx, appID)` - Deactivate RPC
- `Uninstall(ctx, appID)` - Uninstall RPC
- `GetApplicationState(ctx, appID)` - Query state

### NetworkManager

Manages IP address allocation for containers:

```go
type NetworkManager struct {
    config     NetworkConfig
    allocatedIPs map[string]string
    ipPool     []string
    mutex      sync.Mutex
}
```

### ResourceMonitor

Tracks resource usage across devices:

```go
type ResourceMonitor struct {
    devices map[string]*DeviceResources
    mutex   sync.RWMutex
}

type DeviceResources struct {
    TotalCPU     resource.Quantity
    UsedCPU      resource.Quantity
    TotalMemory  resource.Quantity
    UsedMemory   resource.Quantity
    TotalStorage resource.Quantity
    UsedStorage  resource.Quantity
}
```

## API Communication

### RESTCONF Endpoints

| Operation | Method | Endpoint |
|-----------|--------|----------|
| Configure | POST | `/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps` |
| Install | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Activate | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Start | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Stop | POST | `/restconf/operations/Cisco-IOS-XE-rpc:app-hosting` |
| Get State | GET | `/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data` |

### YANG Models Used

- `Cisco-IOS-XE-app-hosting-cfg.yang` - Configuration
- `Cisco-IOS-XE-app-hosting-oper.yang` - Operational state
- `Cisco-IOS-XE-rpc.yang` - RPC operations

## Data Flow

### Pod Creation Flow

```
1. kubectl apply -f pod.yaml
         │
         ▼
2. Kubernetes API Server
         │
         ▼
3. Pod Controller (Virtual Kubelet)
         │
         ▼
4. CiscoProvider.CreatePod()
         │
         ▼
5. DeviceManager.SelectDevice()
         │
         ▼
6. AppHostingManager.DeployApplication()
         │
         ├─► Configure (RESTCONF POST)
         ├─► Install (RESTCONF RPC)
         ├─► WaitForState(DEPLOYED)
         ├─► Activate (RESTCONF RPC)
         ├─► WaitForState(ACTIVATED)
         ├─► Start (RESTCONF RPC)
         └─► WaitForState(RUNNING)
         │
         ▼
7. Update Pod Status → Running
```

### Pod Deletion Flow

```
1. kubectl delete pod <name>
         │
         ▼
2. CiscoProvider.DeletePod()
         │
         ▼
3. AppHostingManager.UndeployApplication()
         │
         ├─► Stop (RESTCONF RPC)
         ├─► Deactivate (RESTCONF RPC)
         └─► Uninstall (RESTCONF RPC)
         │
         ▼
4. NetworkManager.ReleaseIP()
         │
         ▼
5. Remove from internal state
```

## State Management

### Pod State Mapping

| Kubernetes State | Device App State |
|-----------------|------------------|
| Pending | INSTALLING |
| Pending | DEPLOYED |
| Pending | ACTIVATED |
| Running | RUNNING |
| Succeeded | STOPPED |
| Failed | ERROR |

### Internal State Storage

```go
// Pods indexed by namespace/name
pods sync.Map  // map[string]*v1.Pod

// Containers indexed by container ID
containers sync.Map  // map[string]*Container
```

## Health Monitoring

### Node Health

The provider reports node conditions every 30 seconds:

- `Ready` - Provider can accept pods
- `OutOfDisk` - Storage capacity check
- `MemoryPressure` - Memory capacity check
- `DiskPressure` - Disk usage check
- `PIDPressure` - Process limit check
- `NetworkUnavailable` - Network connectivity

### Container Health

Device state is polled periodically:

```go
func (m *DeviceMonitor) checkContainerHealth(ctx context.Context, appID string) {
    state, err := m.client.GetApplicationState(ctx, appID)
    // Update container status based on state
}
```

## Error Handling

### Retry Logic

Failed operations are retried with exponential backoff:

```go
func (r *RESTCONFAppHostingClient) WaitForState(ctx context.Context, appID, expectedState string, timeout time.Duration) error {
    pollInterval := 5 * time.Second
    deadline := time.Now().Add(timeout)
    
    for time.Now().Before(deadline) {
        state, err := r.GetApplicationState(ctx, appID)
        if state == expectedState {
            return nil
        }
        time.Sleep(pollInterval)
    }
    return fmt.Errorf("timeout waiting for state %s", expectedState)
}
```

### Error Recovery

- Connection failures trigger device health checks
- Failed deployments are rolled back
- Orphaned containers are cleaned up on reconciliation

## Security

### TLS Communication

All RESTCONF communication uses HTTPS:

```go
transport := &http.Transport{
    TLSClientConfig: &tls.Config{
        InsecureSkipVerify: !config.VerifyTLS,
    },
}
```

### Credential Management

- Device credentials stored in configuration
- Support for environment variables
- Kubernetes secrets integration (planned)

## Extensibility

### Adding New Device Types

1. Implement device-specific client
2. Register in DeviceManager
3. Map API endpoints

### Custom Resource Profiles

Extend the `resourceDefaults` configuration:

```yaml
resourceDefaults:
  cpuUnits: 1000
  memoryMB: 512
  customField: value
```
