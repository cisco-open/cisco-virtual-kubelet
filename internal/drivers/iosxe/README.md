# IOS-XE Driver Package

This package implements the `CiscoKubernetesDeviceDriver` interface for Cisco IOS-XE devices using the AppHosting feature over RESTCONF.

## Design Intent

The code is organised into files with clear separation of concerns, roughly following these layers:

| Layer | File(s) | Responsibility |
|-------|---------|----------------|
| **Constructor** | `driver.go` | `XEDriver` struct, factory constructor, marshallers/unmarshallers |
| **Types** | `types.go` | Shared types (`AppHostingConfig`, `AppPhase`, `networkConfig`, etc.) |
| **Device** | `device.go` | Device connectivity checks and node resource reporting |
| **Transport** | `client.go` | Low-level device operations (install, activate, start, …). Named protocol-agnostically to support future NETCONF transport |
| **Reconciler** | `reconciler.go` | App lifecycle state machine — drives apps toward their desired state |
| **Pod Lifecycle** | `pod_lifecycle.go` | Kubernetes-facing interface methods (`DeployPod`, `DeletePod`, `GetPodStatus`, `ListPods`) |
| **Pod Transforms** | `pod_transforms.go` | Pod.Spec → IOS-XE AppHosting config conversion (network, resources, labels) |
| **Status Transforms** | `status_transforms.go` | App operational data → Pod.Status / ContainerStatus conversion |
| **IP Discovery** | `ip_discovery.go` | Pod IP resolution from app-hosting oper data with ARP table fallback |
| **Models** | `models.go` | YANG/ygot generated structs (do not edit by hand) |

## Data Flow

```
Pod.Spec
  │
  ▼
pod_transforms.go   ──►  AppHostingConfig (types.go)
                              │
                              ▼
                         client.go  ◄──►  IOS-XE Device (RESTCONF)
                              │
                              ▼
                        reconciler.go  (state machine: "" → DEPLOYED → ACTIVATED → RUNNING)
                              │
                              ▼
                     ip_discovery.go  +  status_transforms.go
                              │
                              ▼
                         Pod.Status
```

## Adding a New Transport

`client.go` is intentionally named without a protocol prefix. To add NETCONF support, introduce a transport interface and provide RESTCONF / NETCONF implementations behind it, keeping the method signatures in `client.go` stable.
