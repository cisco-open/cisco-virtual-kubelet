# Device diagnostics

Use `DeviceOperation` to collect auditable, asynchronous diagnostic results
without creating a Pod or changing device state. This guide is for operators
with permission to inspect a device and create read-only operations. The
expected result is a terminal operation with output, artifact references or a
specific failure reason.

## Prerequisites

- A working `CiscoDevice` and its per-device worker, with the current
  `DeviceOperation` CRD installed. Examples use the IOS-XE device
  `default/cat9k-smoke`; replace both namespace and name together.
- Namespace-scoped permission to create and inspect `DeviceOperation` objects.
  The operation must be in the **same namespace as its target device**;
  cross-namespace requests are rejected with `NamespaceMismatch`.
- The device transport and credentials needed for the selected operation.
  gNOI probes additionally need gNOI enabled and a working secure connection;
  RPC availability depends on the device and software version. See
  [gNOI and software lifecycle](gnoi-software-lifecycle.md) for that setup.

The API is Beta (`v1alpha1`). Leave write-class and software-upgrade gates off
for read-only use; these diagnostics do not require either mutation gate.

## Run a read-only operation

Save this as `show-version.yaml`, substituting your existing device and namespace:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: show-version
  namespace: default
spec:
  deviceRef:
    name: cat9k-smoke
  operation:
    kind: ShowCommand
    commands:
      - show version
  ttlSecondsAfterFinished: 300
```

```bash
kubectl apply -f show-version.yaml
kubectl -n default get deviceoperation show-version -w
```

When it reaches a terminal phase, stop the watch and
[inspect its output and events](#deviceoperation-example-output). The example
requests cleanup five minutes after completion, so collect needed evidence
before then. Use a new operation name for a later probe to keep its audit record
separate.

## Supported read-only kinds

| Kind | What it reads | Request details |
|---|---|---|
| `ShowCommand` | Allowlisted IOS-XE show/monitor commands | `operation.commands`; the allowlist is shared with `IOSXEDiagnostic`. |
| `ConfigDiff` | `show running-config`, optionally compared with a baseline | Optional `operation.args.baseline` produces a compact line diff. |
| `PacketCapture` | An existing IOS-XE capture buffer | `operation.args.name` or `operation.args.capture`. |
| `GNOIPing` | Reachability from the device (System service) | Typed `operation.gnoi.ping` arguments. |
| `GNOITraceroute` | Hop-by-hop path from the device (System) | Typed `operation.gnoi.traceroute` arguments. |
| `GNOITime` | Device clock (System) | No mutation. |
| `GNOIFileGet` | Bounded file content (File) | Preview or ConfigMap artifact. |
| `GNOIFileStat` | File metadata (File) | Inspect a staged file. |
| `GNOICertGet` | Installed certificates (Cert) | Check secure-gNXI connectivity before provisioning. |
| `GNOICanGenerateCSR` | CSR capability (Cert) | Check a key/certificate profile. |
| `GNOIRebootStatus` | Pending or active reboot state (System) | Observe an existing request. |
| `GNOIOSVerify` | Running version and activation state (OS) | Requires IOS-XE OS-service provisioning for success. |

`GNOIOSVerify` never provisions or changes the device. Before IOS-XE reports
`State: Provisioned`, its expected not-provisioned response can confirm that
authentication reached the OS service. Successful TLS/password authentication
does not prove every System or File RPC is supported. For certificate setup,
follow the [provisioning procedure](gnoi-software-lifecycle.md#provisioning-the-ios-xe-gnoi-os-service).

### Restrict ConfigDiff

Running configurations can contain sensitive data. A platform operator can
restrict ConfigDiff using `CiscoDevice.spec.opsPolicy.configDiffAllowedNamespaces`.
For an existing device in `default`, this patch allows ConfigDiff in `default`:

```bash
kubectl -n default patch ciscodevice cat9k-smoke --type=merge \
  -p '{"spec":{"opsPolicy":{"configDiffAllowedNamespaces":["default"]}}}'
```

This list is an **additional restriction**, not permission to target a device
from another namespace. An empty or absent list adds no ConfigDiff-specific
restriction, but the same-namespace rule and Kubernetes RBAC still apply.
Requests denied by the list have `Ready=False`, reason `NamespaceNotAuthorized`.

The controller renders the list as `CVK_OPS_CONFIGDIFF_ALLOWED_NAMESPACES` on
the worker. Change the `CiscoDevice` spec; imperative `kubectl set env` edits are
reverted during reconciliation.

### Read a capture buffer

`PacketCapture` synthesizes only `show monitor capture <name> buffer dump`.
It does not start a capture. The former `operation.args.command` escape hatch
has been removed; use `ShowCommand` with explicit `commands` for other
allowlisted show or monitor commands.

For example, the operation portion of a request for an existing buffer is:

```yaml
operation:
  kind: PacketCapture
  args:
    name: capture1
```

Capture output over 256 KiB is stored in a ConfigMap in the operation's
namespace. Standalone workers use `<deviceoperation-name>-output`; managed
topology uses an identity-bound generated name. Read the actual reference from
status, which retains a truncated inline preview and an artifact URI such as
`configmap://default/capture-output/output`. Captures over 900 KiB fail with
`Ready=False`, reason `ArtifactTooLarge`; use a smaller existing buffer rather
than assuming output was retained.

## Status

Inspect `.status.phase`, `.status.conditions`, `.status.outputs[]` and
`.status.artifactURIs[]`. Terminal phases are `Succeeded`, `Failed` and
`Cancelled`. `ttlSecondsAfterFinished` requests best-effort cleanup after
completion; absent or zero disables that cleanup. Export needed evidence before
a TTL removes the operation and its owned artifacts.

### DeviceOperation — example output

```bash
kubectl -n default get deviceoperation show-version -o yaml
kubectl -n default describe deviceoperation show-version
```

A successful show-command request has a status shaped like this (output is
abbreviated; actual device text varies):

```yaml
status:
  phase: Succeeded
  conditions:
    - type: Ready
      status: "True"
      reason: Succeeded
  outputs:
    - command: show version
      output: "Cisco IOS XE Software, Version ..."
```

Events record dispatch and completion. A `GNOIPing` result uses the same output
path and includes probe replies and sent/received/latency statistics. For a
failed probe, read the condition message and events before retrying; a transport
or unsupported-RPC failure is different from evidence that the target is down.

## RBAC

Give diagnostic operators their own namespace-scoped permissions on
`deviceoperations.ops.cisco.vk`. Artifact readers also need permission to read
the referenced ConfigMaps. Keep these grants separate from
[`IOSXEOperationalAction` and software-upgrade authority](device-actions.md#rbac-and-runtime-gates).

The per-device worker service account watches operations targeting its device.
Its `create` permission supports the local admin wrapper, and `delete` supports
TTL cleanup; result writes use `deviceoperations/status`. These worker grants
do not replace the operator's own authorization.

## Admin exec wrapper

When its in-pod controller client is available, the local admin endpoint
`POST /v1/exec` creates a transient `DeviceOperation` and polls status. This
preserves the plugin response shape while routing execution through the CRD
audit path.

## Implementation boundary

Read-only kinds share validation, transport, redaction, bounded output, TTL and
status handling. Mutating actions use a separate reconciler and CRD with their
own RBAC, confirmation guard, finalizer, invocation ID, events and at-most-once
dispatch rules. See [device actions](device-actions.md) before enabling them.

## Roadmap gates

The current boundaries remain explicit: broader write-class use needs tenant
ownership/admission controls; API promotion beyond `v1alpha1` needs conversion
handling; large diagnostic artifacts currently use in-namespace ConfigMaps;
and automatic rollback does not cover cross-device or independently verified
multi-supervisor recovery. These are not capabilities implied by a successful
read-only probe.
