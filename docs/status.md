# Status and feature maturity

This is the capability summary for **v2026.11.0 · November 2026**. The
[Releases page](https://github.com/cisco-open/cisco-virtual-kubelet/releases)
determines publication status and identifies installable versions and artifacts.
Preparation notes alone do not establish publication. Earlier records remain
available in the [release archive](releases/index.md).

The project is open source under `cisco-open`. Device and operational CRDs use
`v1alpha1` APIs; schema changes are still possible. Signed images are published at
`ghcr.io/cisco-open/cisco-virtual-kubelet`, with the chart at
`oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet`.

Managed topology separates app-hosting and network-management ServiceAccounts.
Native admission and exact worker binding complement RBAC; see the
[security boundary](security.md) before enabling operational changes.

## Feature maturity

| Feature area | Maturity | Scope and limits |
| --- | --- | --- |
| IOS-XE App-Hosting and `CiscoDevice` lifecycle | **Stable** | Supported Catalyst 8000V 17.15+, Catalyst 9000 17.18+, IR1100 Series 17.12+, and IE3500 Series 17.18+ paths. Check the platform guide before deployment. |
| NX-OS runtime | **Beta** | NX-API CLI app hosting and a bounded NX-API REST/DME configuration slice. |
| Network as Code configuration | **Beta** | IOS-XE and NX-OS drift detection and verification. Family coverage differs; NX-OS has no revision rollback. |
| Device operations and IOS-XE software lifecycle | **Beta** | Diagnostics and gated operational changes. Write-class gNOI and software upgrades require explicit opt-in. |
| Controller adapters | **Alpha** | `sdwan`: fixture-qualified apply, `Retain` only. `nexus-dashboard`: health, inventory and opt-in `CiscoDevice` adoption, without ND configuration management. Other controller types have no adapter. |
| Managed topology and IOS-XE campaigns (`IOSXESoftwareRollout`) | **Alpha / opt-in** | Protected Node identity, native scheduling and bounded software campaigns. Requires Kubernetes 1.35+; no generic NX-OS or IOS-XR rollout API. |
| PDB-aware drain | **Development preview** | Disabled by default, with limited workload eligibility and outstanding physical/service qualification. No zero-downtime or continuity guarantee. |
| Native TAS | **Experimental** | Separate Kubernetes 1.37 scheduler-conformance lane; not a managed-topology dependency or full physical group-lifecycle qualification. |
| Telemetry and observability | **Beta** | gNMI subscriptions, device metrics and OpenTelemetry signals; schemas and signal shapes may change. |

Evaluate Beta and Alpha features in non-production environments before broader
rollout. `FAKE` is a testing driver; `XR` and `OPENCONFIG` are reserved names,
not implemented device integrations. For the exact topology boundary, read the
[bounded merge scope](topology-merge-scope.md) and
[managed topology guide](topology-awareness.md).


## Release and qualification context

For a deployed system, match the image and chart to a
[published release](https://github.com/cisco-open/cisco-virtual-kubelet/releases).
The previous baseline is October
[v2026.10.0](https://github.com/cisco-open/cisco-virtual-kubelet/releases/tag/v2026.10.0).
The controller adapters, managed public-CA projection and staged topology
activation/evidence/recovery extensions described here are introduced in
`v2026.11.0`. When upgrading from October, update the matching CRDs, admission
policies, chart, manager and workers together; an older image does not gain
these capabilities by applying a newer example.

Qualification records apply to the revision, platform and test conditions they
name. Consult the [NX-OS qualification plan](production-readiness.md),
[SD-WAN live qualification backlog](controllers/sdwan-live-test-backlog.md), and
[topology roadmap](topology-roadmap.md) for the remaining work in those areas.

[Return to documentation home](index.md)
