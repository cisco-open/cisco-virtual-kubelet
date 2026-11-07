# Operations Runbook

Choose a guide for the task you need to complete. Maintaining the CVK
installation changes Kubernetes controllers and schemas; maintaining a device
can change its configuration or running operating system. Plan those changes
separately so the expected result and recovery path are clear.

| Task | Start here | Expected result |
|---|---|---|
| Upgrade or roll back CVK | [Installation maintenance](installation-maintenance.md) | CRDs, chart and controller image match the selected release. |
| Inspect a device without changing it | [Device diagnostics](device-diagnostics.md) | An auditable `DeviceOperation` contains output or a concrete failure. |
| Provision a certificate, reboot, or perform another device action | [Opt-in device actions](device-actions.md) | A separately authorized action records its invocation and outcome. |
| Upgrade or downgrade IOS-XE | [IOS-XE upgrade and downgrade runbook](gnoi-iosxe-upgrade-runbook.md) | The device's running version is independently verified. |
| Investigate health, metrics, or failures | [Observability](observability.md) and [Troubleshooting](troubleshooting.md) | Evidence identifies the affected controller, worker, or device. |

For a new installation, begin with [Getting started](getting-started.md).
For managed software campaigns, use the
[managed topology overview](topology-awareness.md) before creating lifecycle
requests. The single-device procedure and the campaign procedure have different
authorization and coordination requirements.

Read-only diagnostics and mutating requests use separate CRDs and RBAC. The
examples require a working `CiscoDevice` and its per-device worker; requests
must live in the target device's namespace. Keep the mutation gates disabled
when collecting diagnostics. Device actions and software upgrades remain Beta,
require explicit gates, and need a maintenance window and a tested recovery
path. A successful reboot acknowledgement alone does not prove device recovery.

<details markdown="1">
<summary>Links from earlier versions of this guide</summary>

The former combined runbook is now split by operator task. Existing section
links below lead to the corresponding procedure or reference.

<a id="upgrading-crds"></a>
[Upgrading CRDs](installation-maintenance.md#upgrade-the-crds-and-chart): back up, review, apply, then roll the controller.

<a id="what-to-expect-after-a-crd-upgrade"></a>
[After a CRD upgrade](installation-maintenance.md#what-to-expect-after-a-crd-upgrade): existing resources and schema compatibility.

<a id="rolling-back-a-crd-upgrade"></a>
[Rolling back](installation-maintenance.md#roll-back-a-release): check stored data before reverting schemas.

<a id="deviceoperation"></a>
[DeviceOperation](device-diagnostics.md#run-a-read-only-operation): prerequisites and a complete example.

<a id="supported-read-only-kinds"></a>
[Supported read-only kinds](device-diagnostics.md#supported-read-only-kinds): commands, captures and gNOI probes.

<a id="implementation-boundary"></a>
[Implementation boundary](device-diagnostics.md#implementation-boundary): shared read-only processing and separate mutation handling.

<a id="write-class-actions"></a>
[Write-class actions](device-actions.md#run-an-action): gates, confirmation and one-shot dispatch.

<a id="shared-device-mutation-lease"></a>
[Shared device-mutation Lease](device-actions.md#shared-device-mutation-lease): serialization, quarantine and deletion behavior.

<a id="software-upgrades"></a>
[Software upgrade API details](device-actions.md#software-upgrade-api-details): image sources, strategies and migration compatibility.

<a id="rbac"></a>
[RBAC](device-diagnostics.md#rbac): diagnostic permissions and separate grants for mutations.

<a id="admin-exec-wrapper"></a>
[Admin exec wrapper](device-diagnostics.md#admin-exec-wrapper): transient operations through the local admin endpoint.

<a id="status"></a>
[Status](device-diagnostics.md#status): outputs, artifacts, terminal phases and cleanup.

<a id="deviceoperation-example-output"></a>
[DeviceOperation output](device-diagnostics.md#deviceoperation-example-output): inspect results and events.

<a id="iosxeoperationalaction-example-output"></a>
[Action output](device-actions.md#iosxeoperationalaction-example-output): acceptance versus convergence.

<a id="iosxesoftwareupgrade-example-output"></a>
[Upgrade output](device-actions.md#iosxesoftwareupgrade-example-output): phases and verification evidence.

<a id="roadmap-gates"></a>
[Remaining boundaries](device-diagnostics.md#roadmap-gates): admission, API evolution, artifact storage and rollback scope.

</details>
