<div class="cvk-hub" markdown>

# Operations

Choose the system you are working on and the outcome you need. Maintaining
CVK's Kubernetes installation, deploying applications, and changing a device's
operating system are separate tasks.

## Choose a task

<div class="grid cards" markdown>

-   **Inspect a device**

    Run supported read-only diagnostics and understand the result before
    planning a change.

    [Device diagnostics](device-diagnostics.md) ·
    [CLI and plugin](cisco-vk-cli.md)

-   **Observe or troubleshoot**

    Find device metrics, telemetry and lifecycle events, or start with a symptom.

    [Observability](observability.md) ·
    [Telemetry](telemetry.md) ·
    [Troubleshooting](troubleshooting.md)

-   **Maintain the CVK installation**

    Upgrade the chart, image and CRDs together, with verification and rollback
    steps for the Kubernetes deployment.

    [Installation maintenance](installation-maintenance.md)

-   **Perform a device action**

    Review the permissions and explicit opt-in gates for supported IOS-XE
    write-class actions.

    [Device actions](device-actions.md) ·
    [NX-OS configuration recovery](nxos-recovery.md)

</div>

## Plan IOS-XE operating-system maintenance { #check-the-boundary-of-a-change }

Use the [single-device upgrade and downgrade runbook](gnoi-iosxe-upgrade-runbook.md)
for one device. For a managed fleet, start with the
[topology overview](topology-awareness.md), complete
[enablement and enrollment](topology-enable.md), then follow the
[campaign guide](topology-campaigns.md).

| During a campaign | Open |
| --- | --- |
| Decide whether the optional drain preview fits your workload | [Workload drain](topology-drain.md) |
| Follow progress and investigate a blocked target | [Campaign observability and troubleshooting](topology-troubleshooting.md) |
| Recover interrupted work or retire retained ownership | [Recovery and retirement](topology-recovery.md) |

Each runbook includes its own prerequisites, approval boundary and verification.
Write-class gNOI and IOS-XE software maintenance require explicit opt-in.
Managed topology requires Kubernetes 1.35+; the disabled-by-default drain preview
has limited workload eligibility and does not guarantee service continuity.
Check [feature availability](status.md) for your release before following an
example.

## Find a reference

Use [Security](security.md) for credentials, trust and permissions,
[Reference](reference/index.md) for API fields, and the
[software lifecycle design](gnoi-software-lifecycle.md) for implementation
behavior. Historical qualification records remain under Development and the
release archive.

</div>
