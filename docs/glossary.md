# Glossary

Terms used throughout the guides and reference pages. For resource fields and
workflows, use the [CRD reference](crds.md) and [reference overview](reference/index.md).

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
