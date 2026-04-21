# Welcome to Cisco Virtual Kubelet

A [Virtual Kubelet](https://virtual-kubelet.io/) provider that lets Kubernetes schedule container workloads directly onto Cisco Catalyst series switches and IOS-XE devices with App-Hosting capabilities.

**Make your network infrastructure a first-class Kubernetes citizen.**

## Concepts at a glance

Three ideas you'll see referenced throughout the docs:

- **Virtual Kubelet** — an open-source project that lets any system impersonate a Kubernetes node. Instead of running `kubelet` on a real VM or bare-metal host, a Virtual Kubelet *provider* registers a virtual node in your cluster and handles pod lifecycle however it likes. This project is a provider for Cisco devices.
- **IOx / App-Hosting** — Cisco's on-device container runtime, available on Catalyst 8000V and Catalyst 9000 platforms. It runs OCI-like container packages (`.tar` files) directly on the device alongside normal network functions.
- **RESTCONF** — the HTTP/JSON management API exposed by IOS-XE, modeled by YANG data models. Everything this project does with a device goes over RESTCONF.

Put those together: each Cisco device becomes a virtual node in your cluster. Pods scheduled to that node run as App-Hosting containers on the device, using standard `kubectl` workflows. The provider translates pod specs into RESTCONF calls.

## What it does

- **Native Kubernetes integration** — Deploy to Cisco devices with standard `kubectl apply`. No new CLI, no separate lifecycle.
- **Driver-based architecture** — Extensible driver pattern with IOS-XE (Catalyst 8000V, Catalyst 9000) available today.
- **Full pod lifecycle** — Create, update, recover, and delete containers via RESTCONF, with automatic state reconciliation and pod recovery.
- **Observability built in** — Prometheus metrics for device CPU, memory, storage, and interfaces; OpenTelemetry topology traces with CDP and OSPF neighbor plus hosted-app context; node annotations carrying router-id, hostname, and neighbor counts.
- **Secure credentials** — Device passwords are injected via Kubernetes Secrets and `valueFrom.secretKeyRef` — never embedded in ConfigMaps or etcd in plaintext.
- **Flexible networking** — DHCP or static allocation across VirtualPortGroup, AppGigabitEthernet (access and trunk with VLAN), and Management interfaces. Auto IP discovery from device operational data or ARP tables.

## Status

This project is under active development and is published as open source under `cisco-open`.

- **Releases** — official releases are cut **monthly** and tagged on GitHub. The [latest release](https://github.com/cisco-open/cisco-virtual-kubelet/releases/latest) is the recommended starting point; `main` may contain unreleased in-flight changes.
- **CRD version** — `cisco.vk/v1alpha1`. Breaking changes are still possible as the schema stabilises.
- **Drivers** — `XE` (Cisco IOS-XE) is production-focused; `FAKE` is for testing; `XR` and `NXOS` are reserved names, not implemented.
- **Images** — **not yet published to a public container registry**. You build the image locally from a release tag (or `main`) and push it to a registry your cluster can pull from. See [Getting Started](getting-started.md).

## Where to next

- [Getting Started](getting-started.md) — First deployment in under 10 minutes
- [Architecture](ARCHITECTURE.md) — How the pieces fit together
- [Configuration](CONFIGURATION.md) — Every field of the CiscoDevice CR and the VK config file
- [Observability](observability.md) — Metrics catalog and OpenTelemetry trace schema
- [Security](security.md) — Credential injection, TLS, and RBAC
- [API Reference](API.md) — RESTCONF endpoints and IOS-XE app-hosting states
- [Troubleshooting](troubleshooting.md) — Common issues and how to diagnose them

## Glossary

Short definitions for terms used throughout the docs:

| Term | Meaning |
|---|---|
| **App-Hosting** | Cisco's on-device container platform. Runs `.tar` container packages on IOS-XE devices. |
| **CDP** | Cisco Discovery Protocol — Layer 2 neighbor discovery between directly connected devices. |
| **CR / CRD** | Custom Resource (Definition) — Kubernetes-native extension mechanism. `CiscoDevice` is this project's CRD. |
| **IOx** | Umbrella name for Cisco's on-device application hosting framework, including App-Hosting. |
| **OSPF** | Open Shortest Path First — Layer 3 link-state routing protocol used for neighbor discovery. |
| **OTEL / OpenTelemetry** | Vendor-neutral observability framework; this project emits OTEL traces for topology. |
| **RESTCONF** | HTTP/JSON management API for network devices, defined by [RFC 8040](https://datatracker.ietf.org/doc/html/rfc8040), modeled by YANG. |
| **TACACS / AAA** | Authentication-Authorization-Accounting protocols used in enterprise network devices. |
| **Virtual Kubelet** | [Upstream project](https://virtual-kubelet.io/) letting any system appear as a Kubernetes node. |
| **VK** | Short for Virtual Kubelet. |
| **VPG / VirtualPortGroup** | A logical L3 interface on IOS-XE used to bridge app-hosted containers into the device network. |
| **YANG** | Data modeling language used to describe configuration and state; RESTCONF payloads are YANG-modeled. |
| **ygot** | Go library that generates Go structs from YANG models; this project uses it to type-check RESTCONF payloads. |
