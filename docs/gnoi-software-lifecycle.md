# gNOI and Software Lifecycle Management

This section covers the gNOI control plane for device operations and IOS-XE
software lifecycle management. It is separate from the pod app-hosting
lifecycle: pods still use RESTCONF app-hosting RPCs, while gNOI handles
device-level probes, file access, reboot, factory reset, and OS upgrade.

## Responsibility Split

CVK separates gNOI workflows by trust level so operators can grant narrow RBAC.

| Surface | CRD | Purpose | Runtime gate |
|---|---|---|---|
| Read-only operations | `DeviceOperation` | Show commands, config diff, packet capture, and read-only gNOI probes | none beyond normal CRD/RBAC access |
| Write-class actions | `IOSXEOperationalAction` | Reboot, cancel reboot, kill process, file put/remove, factory reset | `--enable-write-class-gnoi` or `CISCO_VK_ENABLE_WRITE_CLASS_GNOI` |
| Software lifecycle | `IOSXESoftwareUpgrade` | Install, activate, verify, and rollback IOS-XE software | `--enable-iosxesoftwareupgrade` or `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE` |

The write-class and software-upgrade gates are intentionally separate. Enabling
read-only gNOI does not enable reboot, file writes, factory reset, or OS
activation.

Helm exposes the same controls under the `gnoi` values block:

| Helm value | Environment rendered into VK pods | Effect |
|---|---|---|
| `gnoi.insecure` | `CISCO_VK_GNOI_INSECURE=1` | Use the insecure IOS-XE gNxI listener. |
| `gnoi.port` | `CISCO_VK_GNOI_PORT=<port>` | Pin the gNOI listener port. Empty lets CVK infer `50052` for insecure and `9339` for secure gNOI. |
| `gnoi.disabled` | `CISCO_VK_GNOI_DISABLED=1` | Prevent the per-device gNOI client from being constructed. |
| `gnoi.enableSoftwareUpgrade` | `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1` | Enable `IOSXESoftwareUpgrade` reconciliation. |
| `gnoi.enableWriteClass` | `CISCO_VK_ENABLE_WRITE_CLASS_GNOI=1` | Enable destructive/write-class `IOSXEOperationalAction` reconciliation. |

## Connection Model

The IOS-XE driver uses a workload-classed gRPC connection pool for gNOI and
gNMI work:

| Class | Used by | Why it is separate |
|---|---|---|
| `ClassControl` | Unary gNOI RPCs such as time, ping, reboot status, cert get, and OS verify | Keeps small control RPCs responsive. |
| `ClassTelemetry` | gNMI Subscribe streams | Keeps telemetry streams independent from operations. |
| `ClassBulkTransfer` | OS install and file put/get | Prevents large file transfers from back-pressuring control or telemetry traffic. |

The gNOI client validates IOS-XE filesystem prefixes for file paths and caches
per-service capability probes. gNMI capabilities do not enumerate gNOI
services, so CVK learns support by observing gNOI responses. A
`codes.Unimplemented` response marks that service unsupported in the in-process
cache and later calls fail fast with `ErrServiceUnsupported` until the cache
expires or the process restarts.

## Read-Only Operations

`DeviceOperation` contains the low-trust operational surface. gNOI-backed kinds
return structured output through the same status path as read-only show
commands and packet captures.

| Kind | gNOI service | Typical use |
|---|---|---|
| `GNOIPing` | System | Reachability probe from the device. |
| `GNOITraceroute` | System | Hop-by-hop path check from the device. |
| `GNOITime` | System | Device clock check. |
| `GNOIFileGet` | File | Read a bounded file preview or spill to ConfigMap. |
| `GNOIFileStat` | File | Validate staged files and metadata. |
| `GNOICertGet` | Cert | List installed certificates. |
| `GNOICanGenerateCSR` | Cert | Check CSR support for a key/certificate profile. |
| `GNOIRebootStatus` | System | Inspect pending or active reboot state. |
| `GNOIOSVerify` | OS | Verify the current running version and activation state. |

For concrete examples, see the [DeviceOperation runbook](operations.md).

## Write-Class Actions

`IOSXEOperationalAction` supports one-shot device-mutating gNOI actions:
`Reboot`, `CancelReboot`, `KillProcess`, `FilePut`, `FileRemove`, and
`FactoryReset`.

Every action targets exactly one `CiscoDevice` and must set `spec.confirm` to
the target device name. The spec is immutable after creation, the request must
contain exactly the args block matching `spec.action.kind`, and a `Running`
action is not dispatched a second time after controller restart. This gives the
operation an audit trail without turning transient controller restarts into
duplicate destructive RPCs.

`FilePut` is intentionally ConfigMap-backed in the current API: the bytes come
from `binaryData["content"]` in a same-namespace ConfigMap. File write/remove
paths must use IOS-XE filesystem prefixes such as `flash:`, `bootflash:`,
`harddisk:`, `usbflash0:`, or `usbflash1:`.

## Software Lifecycle

`IOSXESoftwareUpgrade` manages the device OS lifecycle as an auditable
Kubernetes object. Operators provide exactly one image source:

| Source | Required fields | Use case |
|---|---|---|
| URL | `imageSource.url` and `imageSource.sha256` | Fetch an image from `http`, `https`, `tftp`, `ftp`, `scp`, or `sftp` and verify the digest. |
| URL with credentials | URL fields plus `imageSource.urlSecretRef` | Fetch from authenticated FTP/SCP/SFTP sources. SCP/SFTP can use `knownHosts` unless the URL opts out with `insecureSkipHostKey=true`. |
| ConfigMap | `imageSource.configMapRef` | Stage small test artifacts from Kubernetes data. This is not for production-sized IOS-XE images. |
| Local path | `imageSource.localPath` | Activate an image already present on device storage. |

For `localPath`, use `localPathSHA256` when the device can report file hashes
through gNOI File.Get. Without that hash, CVK can activate a staged image but
cannot verify the local file before activation.

The upgrade strategy controls activation:

| Strategy | Behavior |
|---|---|
| `Reload` | Default. Calls gNOI `OS.Activate` with reboot allowed, then waits for the device to return and verifies the running version. |
| `ISSU` | Requests the normal activate path, then asserts after verify that the device selected the ISSU path. IOS-XE still makes the final choice based on platform and version compatibility. |
| `NoReboot` | Calls gNOI `OS.Activate` with `NoReboot=true`, stages the image for a later reload, and ends as `Succeeded`. Trigger the reload separately through `IOSXEOperationalAction` when ready. |

The normal lifecycle is:

| Phase | Meaning |
|---|---|
| `Pending` | CR accepted; no device operation started yet. Maintenance windows are checked here. |
| `Resolving` | Image source and target version are resolved. |
| `Transferring` | Image is copied or staged when needed. |
| `TransferInterrupted` | Transfer failed with a retryable error and can re-enter `Transferring`. |
| `Validating` | Staged image and preflight requirements are checked. |
| `Activating` | gNOI OS activation is requested. |
| `AwaitingReachability` | Device may be rebooting after activation. |
| `Verifying` | Running version and activation result are verified. |
| `RollingBack` | Previous version is being re-activated after a verify mismatch. |
| `Succeeded` | Requested version is active and verified. |

Terminal failure phases include `Failed`, `PreflightFailed`,
`ValidationFailed`, `RolledBack`, `RebootTimeout`, and `Cancelled`.

`OS.Activate` reboots the device when the chosen strategy requires it; CVK does
not issue a separate `System.Reboot` after activation. With rollback enabled,
CVK re-activates the previously observed running version if post-activation
verification does not match the requested target.

Important defaults:

| Field | Default | Notes |
|---|---:|---|
| `strategy` | `Reload` | Use `NoReboot` when activation should stage only. |
| `rollbackOnFailure` | `true` | Attempts to restore the previously observed version after verify mismatch. |
| `resumePolicy` | `Retry` | `Abort` makes transfer interruptions terminal. |
| `maxRetries` | `3` | Applies to transfer retries. |
| `rebootTimeoutSeconds` | `1800` | Controls how long `AwaitingReachability` waits. |

`targetVersion` accepts IOS-XE version shapes such as `17.15.01a`,
`26.01.01`, `26.01.01.0.340`, and `17.18.02.0.4112.1766116039`. Verification
uses a prefix-aware comparison, so operators may use the shortest unambiguous
form for the staged image.

## Operator Workflow

1. Confirm the device exposes the required gNxI/gNOI listener.
2. Enable only the required runtime gate on the per-device VK pod.
3. Grant RBAC by CRD type: read-only users get `DeviceOperation`, upgrade
   operators get `IOSXESoftwareUpgrade`, and break-glass operators get
   `IOSXEOperationalAction`.
4. Create one operation CR per device operation.
5. Watch `.status.phase`, `.status.conditions`, Kubernetes events, and the
   gNOI lifecycle metrics in [Observability](observability.md#gnoi-lifecycle-metrics).

For concrete YAML examples and status output, see the
[DeviceOperation runbook](operations.md).
