# Opt-in device actions

Use this guide to authorize and inspect a one-shot device mutation. It is for
operators responsible for IOS-XE maintenance, with namespace-scoped write
permissions and a tested device recovery path. A completed action records what
was invoked and its outcome; reboot or factory-reset acceptance still requires
independent convergence checks.

For a complete operating-system change, follow the
[IOS-XE upgrade and downgrade runbook](gnoi-iosxe-upgrade-runbook.md). The
[software API details](#software-upgrade-api-details) below supplement that
procedure. For read-only requests, use [Device diagnostics](device-diagnostics.md).

## Prerequisites

- A working IOS-XE `CiscoDevice`, per-device worker, current mutation CRDs and
  secure gNOI connection. The request and target device must share a namespace.
- A maintenance window, out-of-band recovery access and authorization for the
  specific action. Validate the workflow on a non-production device first.
- `aggregator.enabled=false`: neither mutation controller runs in aggregator
  mode. Direct `IOSXEOperationalAction` requests also require the standalone
  topology; the chart rejects `gnoi.enableWriteClass=true` when
  `topology.enabled=true`. Managed topology admits campaign-owned software
  upgrade leaves through its separate maintenance protocol.

## RBAC and runtime gates

Both mutation APIs are Beta (`v1alpha1`) and disabled by default. Keep diagnostic
permissions separate from permissions to create `iosxeoperationalactions` or
`iosxesoftwareupgrades` in the `ops.cisco.vk` API group.

!!! danger "Creating an action grants broad device authority"
    Kubernetes RBAC cannot distinguish `spec.action.kind`. Permission to create
    `IOSXEOperationalAction` includes reboot, file mutation and factory-reset
    requests. When a signer is configured, it also includes
    `ProvisionCertificate` and therefore PKI replacement authority. Namespace
    RBAC is not an action-kind allowlist; restrict the principals that receive
    it and any additional admission controls before enabling the gate.

For an existing **standalone** installation, review these settings in your
Helm values, preserving the rest of the installation's configuration:

```yaml
aggregator:
  enabled: false
topology:
  enabled: false
gnoi:
  disabled: false
  enableWriteClass: true
  enableSoftwareUpgrade: false
```

Apply them through the [installation-maintenance procedure](installation-maintenance.md),
using the same release, namespace and immutable chart version. Do not disable
managed topology merely to copy this example. The write-class runtime flag is
`--enable-write-class-gnoi`; its environment equivalent is
`CISCO_VK_ENABLE_WRITE_CLASS_GNOI=1`.

Software maintenance has a separate `gnoi.enableSoftwareUpgrade` gate
(`--enable-iosxesoftwareupgrade` or
`CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1`). Follow the OS runbook to enable only
the gate that task needs. Chart-level gates affect the applicable workers in
the release; they do not authorize individual operators or operations.

Enabling either mutation gate makes IOS-XE worker Deployments use `Recreate`
to prevent overlapping generations from sharing one at-most-once mutation
identity. Allow for a brief management interruption. Disabling both gates (or
all gNOI) does not immediately guarantee `RollingUpdate`: old mutation workers
and signer cleanup must settle, and managed topology or mounted device trust
can independently require `Recreate`.

## Run an action

`IOSXEOperationalAction` supports `Reboot`, `CancelReboot`, `KillProcess`,
`FilePut`, `FileRemove`, `FactoryReset` and `ProvisionCertificate`. Each request
has one `deviceRef`, `spec.confirm` equal to that device name, and exactly the
argument block matching `spec.action.kind`. The spec is immutable after create.

This example requests an immediate cold reboot of `default/cat9k-smoke`. Only
create it after completing the prerequisites and enabling the write-class gate.
Save it as `reload-cat9k-smoke.yaml`, replacing both device references and the
namespace with the authorized target:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXEOperationalAction
metadata:
  name: reload-cat9k-smoke
  namespace: default
spec:
  deviceRef:
    name: cat9k-smoke
  confirm: cat9k-smoke
  action:
    kind: Reboot
    reboot:
      method: COLD
      delaySeconds: 0
      message: "maintenance reload"
```

```bash
kubectl create -f reload-cat9k-smoke.yaml
kubectl -n default get iosxeoperationalaction reload-cat9k-smoke -w
```

Stop the watch at a terminal phase, then inspect the status and the device.
Do not create another action simply because the worker restarted or the device
became temporarily unreachable.

For `ProvisionCertificate`, the `provisionCertificate` block must contain the
configured `certificateID` and `publicMaterialSHA256`: lowercase SHA-256 of
the exact `tls.crt` bytes followed by the exact `ca.crt` bytes. Stale IDs or
digests are rejected before connecting. This action is the only path that
installs the configured gNOI identity; `GNOIOSVerify` remains read-only. Use the
[certificate provisioning workflow](gnoi-software-lifecycle.md#provisioning-the-ios-xe-gnoi-os-service)
for signer setup and cleanup. Treat factory reset as the last capability to
exercise in a rollout; RBAC alone cannot selectively disable that action kind.

## Invocation and recovery

An action is validated in `Pending` and recorded as `Running` **before** the
RPC. A `Running` action is never dispatched again. Terminal phases are
`Succeeded`, `Failed` and `Rejected`; normal events describe running/successful
requests, and warning events describe rejection, failure or pending deletion.

After a controller restart, CVK waits for the original five-minute RPC window
and a short persistence grace. Without a durably recorded result, the action
fails with `ActionOutcomeUnknown` and its Lease stays quarantined until expiry.
Inspect the device before creating a replacement request.

For certificate provisioning, `CertificateInstallIndeterminate` means the
create-only result remained unknown after bounded certificate and `OS.Verify`
checks over fresh connections. Use `GNOICertGet` and inspect device PKI state;
never blindly retry the same certificate ID.

### IOSXEOperationalAction — example output

```bash
kubectl -n default get iosxeoperationalaction reload-cat9k-smoke -o yaml
kubectl -n default describe iosxeoperationalaction reload-cat9k-smoke
```

An accepted reboot can have the following abbreviated status:

```yaml
status:
  phase: Succeeded
  invocationID: "a3f2e1d0-8c7b-4a5f-9e6d-1b2c3d4e5f60"
  conditions:
    - type: Ready
      status: "True"
      reason: AcceptedPendingConvergence
```

For `Reboot` and `FactoryReset`, this means the device accepted the RPC.
It does **not** establish recovery or convergence, and the mutation Lease
remains quarantined until expiry. Verify reachability and resulting device
state independently before declaring maintenance complete.

## Shared device-mutation Lease

Actions and software upgrades share a platform-neutral
`device-disruptive-mutation` Lease for each namespaced device. Its safety TTL is
26 hours in this release. Definitive outcomes release the Lease; every invoked
action failure, accepted reboot/factory reset, and uncertain upgrade outcome
retains it until expiry. Delayed reboots add their accepted delay to the hold.
`CancelReboot` bypasses coordination and never releases another holder's Lease.

An in-flight action retains its finalizer through the bounded RPC and
persistence window. If its outcome remains unknown, CVK records
`ActionOutcomeUnknown`, allows deletion to complete and retains quarantine.
Deleting a software-upgrade CR does not cancel device work or clear a required
quarantine. Disabling a gate also does not discard an existing quarantine.

The fence identifies a device by namespace and `CiscoDevice` name, not its IP.
Register a physical device once and coordinate ownership across clusters.
Manual CLI and external automation remain outside this fence. See
[gNOI mutation coordination](gnoi-software-lifecycle.md#shared-device-mutation-coordination)
for the maintenance taint, app-hosting/configuration-write guard and managed
campaign behavior.

## Software upgrade API details

Use the [OS upgrade and downgrade runbook](gnoi-iosxe-upgrade-runbook.md) for the
complete procedure, credentials, recovery prerequisites and verification.
This reference retains the source-selection, strategy and compatibility
contracts needed to interpret `IOSXESoftwareUpgrade` objects.

### Image sources

Select exactly one source in `spec.imageSource`:

| Source | Behavior and constraint |
|---|---|
| `url` with `sha256` | Fetch and verify the content, then stream it through gNOI `OS.Install`. |
| `configMapRef` | Stream `binaryData["image"]` through `OS.Install`; the roughly 900 KiB ConfigMap limit makes this a testing-only source. |
| `preinstalled: {}` | Activate an exact native-inventory version without transfer or registration. |
| `deviceFile` with `path` and `sha256` | Verify through gNOI `File.Get`, register through IOS-XE RESTCONF install, then activate through gNOI. |
| Deprecated `localPath`, optionally `localPathSHA256` | Inventory-only compatibility form; never registers a file. |

Device-file registration has no CLI fallback. Ambiguous or non-activatable
inventory fails closed. URL credentials must not appear as URL user information.
For FTP/SCP/SFTP, an optional same-namespace `urlSecretRef` must have label
`cisco.vk/purpose=software-image-source` and keys `allowedScheme`, `allowedHost`
and `allowedPort` matching the canonical endpoint before credentials are
loaded. See the [Secret and transfer contract](gnoi-software-lifecycle.md#software-lifecycle).

### Strategies, preparation and rollback

| Strategy | Current behavior |
|---|---|
| `Reload` (default) | Activates the selected image and reloads the device. |
| `ISSU` | Rejected during preflight until CVK can verify IOS-XE selected the ISSU path. |
| `NoReboot` | Requests activation without immediate reload; if the old version remains running, ends as `StagedForNextBoot` and needs a separately authorized reboot. It is not a hitless-upgrade guarantee. |
| `PrepareOnly` | Accepts URL, ConfigMap or `deviceFile` sources, never calls `OS.Activate`, verifies the old version remains running, and ends as `Prepared` with an immutable content-addressed receipt. |

A retained preparation receipt blocks both direct upgrades for the same device
and a competing managed campaign for the same Device UID. For managed
activation, the completed rollout needs append-only `spec.activationApproval`,
the exact frozen plan and receipt set, a bounded UTC window and the distinct
`activate` permission. CVK creates a new preinstalled activation leaf and
reservation, then revalidates inventory, trust, source, policy, identity, health
and workload safety before claiming activation. The prepared leaf remains audit
evidence; deleting it is never an authorization workflow. Follow
[separate preparation and activation](topology-campaigns.md#separate-preparation-from-activation)
for that approval procedure.

When `OS.Verify` requires individual-supervisor handling, CVK installs active
then standby, activates standby then active, rejects `NoReboot`, and uses one
shared install deadline.

With `rollbackOnFailure: true`, a different running version after activation
can enter `RollingBack` only if the previous version was captured and the
backend proves it remains activatable. `RolledBack` requires `OS.Verify` to
confirm that version. Automatic rollback is rejected for individual-supervisor
handling because a safe, separately verified sequence for both supervisors is
not yet established.

Staging, activation and rollback intent is persisted before its RPC and is not
replayed after an ambiguous result. Inspect the device when an outcome remains
unknown; submitting a replacement is a new maintenance decision.

### IOSXESoftwareUpgrade — example output

For an upgrade created through the OS runbook, substitute its actual namespace
and name:

```bash
kubectl -n edge get iosxesoftwareupgrade cat9000-1-to-17-18-03 -w
kubectl -n edge describe iosxesoftwareupgrade cat9000-1-to-17-18-03
```

A successful transfer/reload can progress through `Pending` → `Resolving` →
`Transferring` → `Activating` → `AwaitingReachability` → `Verifying`
→ `Succeeded`. A `deviceFile` source uses `Staging` and `Validating` before
activation. Inspect the pinned source digest, staging operation ID, inventory,
conditions and events; terminal preparation or staging is not proof that the
target version is running.

### Migrating existing mutation objects

The current at-most-once reconciler adopts an unmarked legacy upgrade only in
`Pending` or `Resolving`. A later unmarked phase may already have submitted a
mutation, so it ends with `LegacyStateOutcomeUnknown` and a quarantined Lease.
Inspect device state before replacing it.

Older manifests may retain `resumePolicy` and `maxRetries`; stored status may
retain `retryCount` or phase `Cancelled`. Admission preserves the historical
`Retry` and `3` defaults for controller rollback compatibility, but the current
reconciler ignores those retry fields. An unknown non-empty execution-model
marker is preserved as newer-controller state: CVK holds the Lease and issues
no RPC rather than rewriting or replaying the operation.

During this migration, either mutation controller reads **both** mutation CRDs
before claiming a Lease, including when the other controller is disabled. It
chooses a deterministic legacy quarantine holder and fails closed on API or
RBAC errors. Released invoked actions retain a bounded 26-hour safety window
plus any accepted reboot delay, independent of future drivers' normal Lease
settings.
