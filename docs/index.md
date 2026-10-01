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
  `IOSXESoftwareUpgrade` and `IOSXESoftwareRollout` that express device configuration, telemetry,
  diagnostics, and operations as Kubernetes API objects. The generic
  `NetworkController` and `NetworkControllerConfig` scaffold extends the same
  approach to controller-centric models, but requires a matching registered
  adapter before it can reconcile an external controller.
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

Kubernetes API
  -> NetworkController + NetworkControllerConfig
  -> isolated worker for a registered adapter type
  -> external controller native API
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
- **Controller extension scaffold (Alpha)** - generic `NetworkController`
  endpoint and `NetworkControllerConfig` intent APIs with an isolated-worker
  registry. The current scaffold registers zero product adapters and the
  boundary is report-only; it cannot apply, prune, or remotely delete state.
- **Operations and upgrades** - read-only diagnostics, gNOI probes,
  write-class operational actions, and multi-phase IOS-XE software upgrades
  behind explicit RBAC and runtime gates.
- **Managed topology and fleet rollout admission** - opt-in manager-owned Node
  identity and protected topology labels for the default scheduler, plus
  bounded IOS-XE campaigns across independent failure domains. This path uses
  native Kubernetes admission/RBAC and no third-party scheduler or operator.
- **Topology-aware image selection** - freeze each target's source URL,
  digest and Secret identity in an approved `IOSXESoftwareRollout`; canaries,
  domain budgets and recovery bound the combined upgrade/downgrade workflow.
- **Secure IOS-XE gNOI** - isolated verified TLS, secure-password metadata and
  opt-in CSR-based OS-service certificate provisioning. Read-only OS.Verify
  never installs certificates; app-hosting and gNMI trust are not bypassed.
- **PDB-aware drain (development preview)** - optional Kubernetes Eviction for
  explicitly eligible workloads, with device-clean and recovery evidence;
  disabled by default and not a zero-downtime or general evacuation promise.
- **Two functional managed identities** - shared app-hosting and
  network-management ServiceAccounts with read-only/read-write role options;
  native admission and exact worker binding complement RBAC.
- **Observability built in** - Prometheus metrics for device CPU, memory,
  storage, and interfaces; OpenTelemetry topology traces with CDP, OSPF, and
  hosted-app context; node annotations carrying router ID, hostname, and
  neighbor counts.
- **Secure credentials** - device passwords are injected via Kubernetes Secrets
  and `valueFrom.secretKeyRef`, never embedded in ConfigMaps.
- **Flexible networking** - DHCP or static allocation across VirtualPortGroup,
  AppGigabitEthernet, and Management interfaces. Pod IP discovery uses device
  operational data first and ARP as a fallback.

For the topology ownership model, scheduler examples, rollout workflow,
security boundary, and deferred roadmap, see
[Managed topology and topology-aware IOS-XE rollouts](topology-awareness.md).

## What is new for October

The [October release notes](releases/v2026.10.0.md) describe changes since
published `v2026.9.2`; `main` remains a candidate until publication. The
[readiness ledger](releases/v2026.10.0-readiness.md) tracks unresolved release gates.

Native Kubernetes scheduling and CVK rollout admission are separate decisions:

| Scenario | Native Kubernetes / CVK behavior | What is not inferred |
| --- | --- | --- |
| Place applications in sites/racks | Scheduler affinity and spread use protected, declared Node topology | Whether redundant network paths are currently healthy |
| Upgrade a declared failure domain | CVK freezes targets, approval, canaries and domain/concurrency budgets | Link oversubscription, automatic maintenance windows for critical services or zero outage |
| Choose a nearby image server | CVK matches topology-specific image sources and freezes URL/digest/Secret identity | A durable shared image cache or measured cheapest network path |
| Relocate eligible applications before reload | Preview PDB-aware Eviction plus device inventory/recovery checks | Universal workload portability, arbitrary hard placement or endpoint continuity |

Managed topology needs Kubernetes 1.35+. Native 1.37 TAS remains an optional
experimental scheduler lane, not a mandatory component. Independent durable
staging/activation, discovered-graph admission, utilization/critical-service
gates and broader platform/group lifecycle are future work, not October claims.

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
- **Controller adapters** - the generic Alpha controller CRDs and
  isolated-worker scaffold are installed, but the current scaffold contains no
  product adapter. It does not integrate Catalyst Center or another external
  controller.
- **Images and chart** - signed monthly images are published at
  `ghcr.io/cisco-open/cisco-virtual-kubelet`, with the Helm chart at
  `oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet`. Build locally only
  when you need a custom image. See [Getting Started](getting-started.md).
- **Operator plugin** - the optional `kubectl-ciscovk` plugin provides
  read-only, ad-hoc IOS-XE diagnostics and is available in the public Krew
  index. `v2026.8.1` was the first plugin-bearing release. Signed release
  archives and a source-build path are documented in the
  [CLI & Plugin Reference](cisco-vk-cli.md).

### Feature Maturity

Not all feature areas have the same level of maturity. The table below
summarises the current release state.

| Feature area | Maturity | Notes |
|---|---|---|
| Pod lifecycle (App-Hosting create / update / delete) | **Stable** | Supported on Catalyst 8000V 17.15+, Catalyst 9000 17.18+, IR1100 Series 17.12+, and IE3500 Series 17.18+. |
| `CiscoDevice` and VK deployment lifecycle | **Stable** | Controller-managed per-device VK pods. |
| **Network controller scaffold** (`NetworkController`, `NetworkControllerConfig`) | **Alpha** | Generic endpoint, controller-centric Network as Code intent, registry, and isolated-worker contracts. Zero adapters ship; the boundary is report-only and does not integrate an external controller. |
| **Network as Code config driver** (`IOSXEConfig`, `NXOSConfig`) | **Beta** | Declarative IOS-XE and NX-OS config CRDs with drift detection and verification. IOS-XE also provides revision/apply-log history and broader family coverage; NX-OS starts with `system`, `feature`, `feature_set`, `vlan`, and `interface_ethernet` over NX-API REST/DME, without revision rollback. Schema is `v1alpha1`; family coverage and wire-format behaviour are still expanding. |
| **Operations** (`DeviceOperation`, `IOSXEOperationalAction`) | **Beta** | Read-only diagnostics and gNOI probes are stable in intent; write-class actions require an explicit runtime gate and carry additional operational risk. |
| **Software Lifecycle** (`IOSXESoftwareUpgrade`) | **Beta** | Content-addressed gNOI install/activate/verify with optional IOS-XE RESTCONF device-file registration. Disabled by default; unsupported or ambiguous native lifecycle state fails closed. |
| **Managed topology / campaigns** (`IOSXESoftwareRollout`) | **Alpha / opt-in** | Protected Node identity, native scheduling and separately coordinated combined IOS-XE rollouts with frozen sources. Kubernetes 1.35+; no generic NX-OS/IOS-XR rollout API. |
| **PDB-aware drain** | **Development preview** | Disabled by default; limited workload eligibility and outstanding physical/service qualification. Not a production continuity guarantee. |
| **Native TAS** | **Experimental** | Separate Kubernetes 1.37 scheduler-conformance lane; not full physical CVK group-lifecycle qualification. |
| **Telemetry** (`IOSXETelemetry`) | **Beta** | MDT-over-gNMI subscriptions converted to OpenTelemetry signals. Pipeline architecture is stable; subscription schema is `v1alpha1`. |
| Observability (Prometheus metrics, OTEL topology traces) | **Beta** | Metrics catalog and trace shapes may change between releases. |

!!! warning "Beta features"
    Features marked **Beta** are functional and tested but carry `v1alpha1` API
    versions. Breaking schema changes are still possible. They should be
    evaluated in non-production environments before broader rollout. Runtime
    gates exist for the highest-risk surfaces (write-class gNOI, software
    upgrades) and must be opted into explicitly.

!!! warning "Alpha scaffold"
    These APIs are not a usable product integration by themselves. The
    current scaffold has no adapter and no remote-mutation runtime. Treat the
    exact endpoint string within one namespace as the duplicate-fencing key;
    the namespace remains the Kubernetes trust and RBAC boundary.

## Where to next

- [Getting Started](getting-started.md) - first deployment path
- [Architecture](ARCHITECTURE.md) - how the pieces fit together
- [Managed Topology and Rollouts](topology-awareness.md) - native Pod placement, campaign approval, image sources, budgets, identity migration and preview drain
- [Network Controller Extensions](controller-extension-guide.md) - add controller-centric Network as Code adapters safely
- [CLI & Plugin Reference](cisco-vk-cli.md) - install and use the optional
  `kubectl-ciscovk` operator plugin
- [Configuration](CONFIGURATION.md) - `CiscoDevice` and VK configuration fields
- [CRD Reference](crds.md) - every shipped CRD and when to use it
- [Family Reference](reference/families/README.md) - generated Network as Code config family coverage
- [IOS-XE gNOI Upgrade and Downgrade Runbook](gnoi-iosxe-upgrade-runbook.md) - required manifests, certificate setup, provider logs, and end-to-end verification
- [gNOI and Software Lifecycle](gnoi-software-lifecycle.md) - architecture, security rules, and lifecycle API reference
- [Device Operations Runbook](operations.md) - DeviceOperation probes, show commands, and write-class actions
- [Telemetry](telemetry.md) - gNMI subscriptions and OpenTelemetry output
- [Observability](observability.md) - metrics catalog and topology traces
- [Security](security.md) - credential injection, TLS, and RBAC
- [NX-OS Configuration Recovery](nxos-recovery.md) - non-transactional failure handling and family-specific compensation
- [Production Readiness](production-readiness.md) - merge gates and NX-OS hardening roadmap
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
| **Network as Code** | Declarative intent shape consumed by `IOSXEConfig`, `NXOSConfig`, and, with a registered adapter, controller-centric `NetworkControllerConfig`. |
| **OTEL / OpenTelemetry** | Vendor-neutral observability framework; this project emits OTEL traces and metrics. |
| **RESTCONF** | HTTP/JSON management API for network devices, defined by [RFC 8040](https://datatracker.ietf.org/doc/html/rfc8040), modeled by YANG. |
| **Virtual Kubelet** | [Upstream project](https://virtual-kubelet.io/) letting any system appear as a Kubernetes node. |
| **VK** | Short for Virtual Kubelet. |
| **VPG / VirtualPortGroup** | A logical L3 interface on IOS-XE used to bridge app-hosted containers into the device network. |
| **YANG** | Data modeling language used to describe configuration and state. |
