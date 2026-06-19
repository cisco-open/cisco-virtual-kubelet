# Welcome to Cisco Virtual Kubelet

A [Virtual Kubelet](https://virtual-kubelet.io/) provider that lets Kubernetes
schedule container workloads directly onto Cisco IOS-XE and NX-OS devices with
App-Hosting capabilities.

**Make your network infrastructure a first-class Kubernetes citizen.**

## Concepts at a glance

Four ideas you'll see referenced throughout the docs:

- **Virtual Kubelet** - an open-source project that lets any system impersonate
  a Kubernetes node. Instead of running `kubelet` on a real VM or bare-metal
  host, a Virtual Kubelet provider registers a virtual node in your cluster and
  handles pod lifecycle however it likes. This project is a provider for Cisco
  devices.
- **IOx / App-Hosting** - Cisco's on-device container runtime, available on
  Catalyst 8000V, Catalyst 9000, IR1100 Series, IE3500 Series, and supported
  NX-OS platforms. It runs OCI-like container packages (`.tar` files) directly
  on the device alongside normal network functions.
- **Network as Code CRDs** - Kubernetes resources such as `IOSXEConfig`,
  `NXOSConfig`, `IOSXEConfigBundle`, `IOSXETelemetry`, `DeviceOperation`, and
  `IOSXESoftwareUpgrade` that express device configuration, telemetry,
  diagnostics, and operations as Kubernetes API objects.
- **RESTCONF, NETCONF, gNMI, gNOI, and NX-API** - management protocols used by
  the provider. IOS-XE app-hosting lifecycle uses RESTCONF; NX-OS app-hosting
  uses NX-API CLI and NX-OS configuration uses NX-API REST/DME; declarative
  IOS-XE config can use RESTCONF, NETCONF, or gNMI; telemetry and software
  operations use gNMI/gNOI.

Put those together: each Cisco device becomes a virtual node in your cluster.
Pods scheduled to that node run as App-Hosting containers on the device, while
configuration and operational workflows stay Kubernetes-native.

```text
Kubernetes API
  -> CiscoDevice
  -> per-device cisco-vk pod
  -> virtual node
  -> pods as device app-hosting containers

Kubernetes API
  -> config.cisco.vk and ops.cisco.vk CRDs
  -> config, telemetry, operation, and software lifecycle reconcilers
  -> devices through RESTCONF, NETCONF, gNMI, gNOI, or NX-API
```

## What it does

- **Native Kubernetes integration** - deploy to Cisco devices with standard
  `kubectl apply`. No separate lifecycle is required for app-hosted pods.
- **Driver-based architecture** - extensible driver pattern with IOS-XE
  (Catalyst 8000V, Catalyst 9000, IR1100 Series, and IE3500 Series) and the
  initial NX-OS runtime slice available today.
- **Full pod lifecycle** - create, update, recover, and delete containers via
  the platform driver transport, with automatic state reconciliation and pod
  recovery.
- **Network as Code** - declarative IOS-XE and NX-OS configuration CRDs with
  drift detection and verify-after-apply reconciliation. IOS-XE includes
  defaults, group targeting, templates, bundles, revisions, and apply logs;
  NX-OS starts with per-device `NXOSConfig` over NX-API REST/DME.
- **Operations and upgrades** - read-only diagnostics, gNOI probes,
  write-class operational actions, and multi-phase IOS-XE software upgrades
  behind explicit RBAC and runtime gates.
- **Observability built in** - Prometheus metrics for device CPU, memory,
  storage, and interfaces; OpenTelemetry topology traces with CDP, OSPF, and
  hosted-app context; node annotations carrying router ID, hostname, and
  neighbor counts.
- **Secure credentials** - device passwords are injected via Kubernetes Secrets
  and `valueFrom.secretKeyRef`, never embedded in ConfigMaps.
- **Flexible networking** - DHCP or static allocation across VirtualPortGroup,
  AppGigabitEthernet, and Management interfaces. Pod IP discovery uses device
  operational data first and ARP as a fallback.

## Status

This project is under active development and is published as open source under
`cisco-open`.

- **Releases** - official releases are cut monthly and tagged on GitHub. The
  [latest release](https://github.com/cisco-open/cisco-virtual-kubelet/releases/latest)
  is the recommended starting point; `main` may contain unreleased in-flight
  changes.
- **CRD versions** - `cisco.vk/v1alpha1`, `config.cisco.vk/v1alpha1`, and
  `ops.cisco.vk/v1alpha1`. Breaking changes are still possible as the schemas
  stabilise.
- **Drivers** - `XE` is production-focused; `NXOS` has working NX-API CLI
  app-hosting and an NX-API REST/DME `NXOSConfig` runtime slice; `FAKE` is for
  testing; `XR` and `OPENCONFIG` are reserved driver names in the API surface.
- **Images** - images are not yet published to a public container registry.
  Build locally from a release tag or `main`, then push to a registry your
  cluster can pull from. See [Getting Started](getting-started.md).

### Feature Maturity

Not all feature areas have the same level of maturity. The table below
summarises the current state for the June 2026 release.

| Feature area | Maturity | Notes |
|---|---|---|
| Pod lifecycle (App-Hosting create / update / delete) | **Stable** | Supported on Catalyst 8000V 17.15+, Catalyst 9000 17.18+, IR1100 Series 17.12+, and IE3500 Series 17.18+. |
| `CiscoDevice` and VK deployment lifecycle | **Stable** | Controller-managed per-device VK pods. |
| **Network as Code config driver** (`IOSXEConfig`, `NXOSConfig`) | **Beta** | Declarative IOS-XE and NX-OS config CRDs with drift detection, revisions, and verification. IOS-XE has broader family coverage; NX-OS starts with `system`, `vlan`, and `interface_ethernet` over NX-API REST/DME. Schema is `v1alpha1`; family coverage and wire-format behaviour are still expanding. |
| **Operations** (`DeviceOperation`, `IOSXEOperationalAction`) | **Beta** | Read-only diagnostics and gNOI probes are stable in intent; write-class actions require an explicit runtime gate and carry additional operational risk. |
| **Software Lifecycle** (`IOSXESoftwareUpgrade`) | **Beta** | Multi-phase gNOI OS install/activate/verify. Disabled by default; requires `--enable-iosxesoftwareupgrade`. Tested on limited platforms. |
| **Telemetry** (`IOSXETelemetry`) | **Beta** | MDT-over-gNMI subscriptions converted to OpenTelemetry signals. Pipeline architecture is stable; subscription schema is `v1alpha1`. |
| Observability (Prometheus metrics, OTEL topology traces) | **Beta** | Metrics catalog and trace shapes may change between releases. |

!!! warning "Beta features"
    Features marked **Beta** are functional and tested but carry `v1alpha1` API
    versions. Breaking schema changes are still possible. They should be
    evaluated in non-production environments before broader rollout. Runtime
    gates exist for the highest-risk surfaces (write-class gNOI, software
    upgrades) and must be opted into explicitly.

## Where to next

- [Getting Started](getting-started.md) - first deployment path
- [Architecture](ARCHITECTURE.md) - how the pieces fit together
- [Configuration](CONFIGURATION.md) - `CiscoDevice` and VK configuration fields
- [CRD Reference](crds.md) - every shipped CRD and when to use it
- [Family Reference](reference/families/README.md) - generated Network as Code config family coverage
- [gNOI and Software Lifecycle](gnoi-software-lifecycle.md) - device operations, write-class actions, and IOS-XE software upgrades
- [Operations Runbook](operations.md) - DeviceOperation, operational actions, and upgrade examples
- [Telemetry](telemetry.md) - gNMI subscriptions and OpenTelemetry output
- [Observability](observability.md) - metrics catalog and topology traces
- [Security](security.md) - credential injection, TLS, and RBAC
- [API Reference](API.md) - Kubernetes CRDs, device protocols, and VK-side kubelet endpoints
- [Troubleshooting](troubleshooting.md) - common issues and how to diagnose them

## Glossary

| Term | Meaning |
|---|---|
| **App-Hosting** | Cisco's on-device container platform. Runs `.tar` container packages on IOS-XE devices. |
| **CDP** | Cisco Discovery Protocol, used for Layer 2 neighbor discovery. |
| **CR / CRD** | Custom Resource / Custom Resource Definition, Kubernetes' API extension mechanism. |
| **gNMI** | gRPC Network Management Interface, used for model-driven telemetry and optional config transport. |
| **gNOI** | gRPC Network Operations Interface, used for read-only probes, file operations, reboot, factory reset, and software upgrade flows. |
| **IOx** | Cisco's on-device application hosting framework, including App-Hosting. |
| **Network as Code** | Declarative intent shape consumed by `IOSXEConfig`, `NXOSConfig`, and related config CRDs. |
| **OTEL / OpenTelemetry** | Vendor-neutral observability framework; this project emits OTEL traces and metrics. |
| **RESTCONF** | HTTP/JSON management API for network devices, defined by [RFC 8040](https://datatracker.ietf.org/doc/html/rfc8040), modeled by YANG. |
| **Virtual Kubelet** | [Upstream project](https://virtual-kubelet.io/) letting any system appear as a Kubernetes node. |
| **VK** | Short for Virtual Kubelet. |
| **VPG / VirtualPortGroup** | A logical L3 interface on IOS-XE used to bridge app-hosted containers into the device network. |
| **YANG** | Data modeling language used to describe configuration and state. |
