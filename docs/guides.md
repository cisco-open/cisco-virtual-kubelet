<div class="cvk-hub" markdown>

# Guides

Choose a task, then follow the guide for your platform. For a first deployment,
the [getting-started guide](getting-started.md) takes you from installation to a
running application.

## Prepare a device

| Platform | Start with |
| --- | --- |
| Catalyst 8000V | [VirtualPortGroup and App-Hosting setup](configuration-cat8000v.md) |
| Catalyst 9000 | [AppGigabitEthernet and App-Hosting setup](configuration-cat9000.md) |
| IR1100 Series | [Device and networking prerequisites](configuration-ir1101.md) |
| IE3500 Series | [Device and networking prerequisites](configuration-ie3500.md) |
| Supported NX-OS devices · Beta | [NX-OS getting-started example](getting-started.md#deploying-to-nx-os-beta) |

Check the [feature status](status.md) and the platform guide for support limits.

## Build your workflow

<div class="grid cards" markdown>

-   **Run applications**

    Connect a device, make its application images accessible, and schedule a pod
    to its virtual node.

    [Getting started](getting-started.md) ·
    [Image distribution](image-distribution.md)

-   **Declare configuration**

    Express device intent with Network as Code and follow reconciliation,
    verification and drift.

    [Network as Code](netascode-config.md) ·
    [Migration guide](netascode-migration.md)

-   **Use topology**

    Understand protected device identity and labels, then place application
    Pods with native Kubernetes scheduling.

    [Topology overview](topology-awareness.md) ·
    [Enable and enroll](topology-enable.md) ·
    [Schedule workloads](topology-scheduling.md)

-   **Connect a controller**

    Choose an Alpha adapter with the capabilities your workflow needs. SD-WAN
    apply is fixture-qualified; Nexus Dashboard covers health, inventory and
    opt-in device adoption.

    [Catalyst SD-WAN](controllers/sdwan.md) ·
    [Nexus Dashboard](controllers/nexus-dashboard.md)

</div>

## Plan access and trust { #before-connecting-a-production-environment }

Use the [security overview](security.md) to choose the credential, TLS and
authorization guidance for your role. Keep device administration separate from
application-author permissions, and match your deployment to a
[published release](releases/index.md). This documentation targets `v2026.11.0`;
check publication status and upgrade older installations before using features
introduced in November.

For day-to-day diagnostics, telemetry and planned changes, continue to
[Operations](operator-guide.md). For field definitions and commands, use
[Reference](reference/index.md).

</div>
