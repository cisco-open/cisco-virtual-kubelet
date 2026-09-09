# Operations Runbook

## Upgrading CRDs

Cisco Virtual Kubelet ships CRD manifests alongside the Helm chart. When
upgrading to a new CVK version, apply the updated CRDs before upgrading the
chart — Helm does not manage CRD updates automatically.

```bash
# 1. Pull the exact release chart and apply its CRDs. For the September release:
helm pull oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.9.2 --untar
kubectl apply --server-side -f cisco-virtual-kubelet/crds/
kubectl wait --for=condition=Established --timeout=60s \
  crd/networkcontrollers.cisco.vk \
  crd/networkcontrollerconfigs.config.cisco.vk

# 2. Verify all CRDs registered at the new schema version:
kubectl get crds | grep cisco

# Example output (abbreviated; the chart currently ships 17 CRDs):
NAME                                    CREATED AT
ciscodevices.cisco.vk                   2026-01-10T09:00:00Z
deviceoperations.ops.cisco.vk           2026-01-10T09:00:00Z
iosxeconfigs.config.cisco.vk            2026-01-10T09:00:00Z
iosxesoftwareupgrades.ops.cisco.vk      2026-01-10T09:00:00Z
iosxeoperationalactions.ops.cisco.vk    2026-01-10T09:00:00Z
networkcontrollers.cisco.vk             2026-08-07T09:00:00Z
networkcontrollerconfigs.config.cisco.vk 2026-08-07T09:00:00Z

# 3. Upgrade the Helm release from the same immutable chart version:
helm upgrade cvk oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.9.2 \
  --namespace cvk-system

# 4. Confirm manager pod is running the new image:
kubectl rollout status deployment/cvk-cisco-virtual-kubelet-controller \
  --namespace cvk-system
```

If a pre-September release is upgraded before these two controller CRDs are
applied, the manager deliberately keeps existing CiscoDevice reconcilers
running and skips Alpha NetworkController registration for that process. Apply
the new CRDs, then restart the manager Deployment; dynamic CRD installation
alone does not add a controller to an already-running controller-runtime
manager. The September image contains no product adapter and the extension
boundary remains report-only even after both CRDs are installed.

### What to expect after a CRD upgrade

- **Existing CRs are preserved.** Kubernetes retains all existing
  `DeviceOperation`, `IOSXEConfig`, `CiscoDevice`, and related CRs through
  a schema version update. New optional fields default to their zero values.
- **New required fields.** If a release adds a required field, existing CRs
  that omit it will fail validation on the next write. Check the release notes
  for breaking schema changes before upgrading production clusters.
- **`v1alpha1` caveat.** While the CRDs carry `v1alpha1` versions, field
  additions are additive. Structural breaking changes (renames, removals) are
  noted explicitly in the release changelog and require manual CR migration
  before the old controller is shut down.

### Rolling back a CRD upgrade

CRD rollback is not directly supported by Kubernetes. If a CRD upgrade must be
reverted:

```bash
# Re-apply CRD manifests from the previous chart version:
kubectl apply -f charts/cisco-virtual-kubelet-<prev-version>/crds/

# Downgrade the Helm release:
helm upgrade cisco-vk ./charts/cisco-virtual-kubelet-<prev-version> \
  --namespace cisco-vk
```

!!! warning
    Rolling back a CRD schema that added new fields leaves any CRs that used
    those fields in an unknown state. Review and patch affected CRs before
    restarting the controller.

---

## DeviceOperation

!!! warning "Beta"
    All CRDs and features documented on this page are **Beta** (`v1alpha1`).
    Read-only `DeviceOperation` and gNOI probes are the most mature surface;
    write-class `IOSXEOperationalAction` and `IOSXESoftwareUpgrade` are newer,
    require explicit runtime gates, and should be tested thoroughly in
    non-production environments before use in production.

`DeviceOperation` is the sibling-CRD path for auditable, asynchronous,
non-Pod operations. For the higher-level gNOI architecture, runtime gates,
RBAC split, and IOS-XE software lifecycle model, see
[gNOI and Software Lifecycle](gnoi-software-lifecycle.md).

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: show-version
spec:
  deviceRef:
    name: cat9k-smoke
  operation:
    kind: ShowCommand
    commands:
      - show version
  ttlSecondsAfterFinished: 300
```

## Supported Read-Only Kinds

`ShowCommand` runs one or more read-only IOS-XE commands through the same allowlist used by `IOSXEDiagnostic`.

`ConfigDiff` captures `show running-config`. If `operation.args.baseline` is provided, status output contains a compact line diff between the baseline and observed running configuration.

Restrict `ConfigDiff` to specific namespaces via the per-device CR:

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata: {name: cat9k-smoke}
spec:
  driver: XE
  address: 10.1.1.1
  opsPolicy:
    configDiffAllowedNamespaces: ["ops", "tenant-a"]
```

The CiscoDevice controller renders `spec.opsPolicy.configDiffAllowedNamespaces`
as `CVK_OPS_CONFIGDIFF_ALLOWED_NAMESPACES` (comma-separated) on the per-device
VK pod. Requests from other namespaces fail with `Ready=False,
reason=NamespaceNotAuthorized`. An empty/absent list preserves the
unrestricted default. The CRD spec is the authoritative source — imperative
`kubectl set env` edits get reverted on the next reconcile.

`PacketCapture` reads an existing IOS-XE monitor capture buffer. Provide
`operation.args.name` or `operation.args.capture`; the reconciler synthesizes
only `show monitor capture <name> buffer dump`. The historical
`operation.args.command` escape hatch was removed because `PacketCapture` is a
read-only capture-buffer contract. Use `ShowCommand` with explicit `commands`
for other allowlisted show or monitor commands.

Packet-capture output larger than 256 KiB is written to a ConfigMap named
`<deviceoperation-name>-output` in the same namespace. The status keeps a
truncated preview in `.status.outputs[].output` and records
`.status.artifactURIs[]` as `configmap://<namespace>/<name>/<key>`, for example
`configmap://default/capture-output/output`. Captures larger than 900 KiB are
rejected with `Ready=False, reason=ArtifactTooLarge`.

Read-only gNOI kinds use the same CRD/status machinery:

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

Use `GNOICertGet` to test secure-gNXI connectivity before provisioning.
`GNOIOSVerify` succeeds only after IOS-XE reports `State: Provisioned`; before
that, its expected not-provisioned response can safely confirm that
authentication reached the OS service. It never mutates the device. Successful
TLS and password authentication does not imply that every System or File RPC
is implemented on that platform.

Write-class gNOI operations are implemented as a separate
`IOSXEOperationalAction` CRD. They are disabled unless the per-device VK is
started with `--enable-write-class-gnoi` / `CISCO_VK_ENABLE_WRITE_CLASS_GNOI`.
Keep the flag off for read-only DeviceOperation deployments.

## Implementation Boundary

The v1alpha1 controller intentionally keeps read-only kinds in one small
reconciler because they share the same validation, transport, redaction, inline
output, TTL, and status machinery.

Write-class operations intentionally do not reuse this reconciler. They are
handled by `IOSXEOperationalAction`, which has its own RBAC, finalizer,
confirmation guard, invocation ID, Kubernetes events, and one-shot dispatch
rules.

## Write-Class Actions

!!! danger "Beta — requires runtime gate"
    Write-class actions are **Beta** and **disabled by default**. The per-device
    VK pod must be started with `--enable-write-class-gnoi` or
    `CISCO_VK_ENABLE_WRITE_CLASS_GNOI=1`. These operations mutate device state
    (certificate provisioning, reboot, file write, factory reset) and may be
    irreversible. Apply strict namespace-scoped RBAC before enabling.

    Kubernetes RBAC cannot distinguish values of `spec.action.kind`. Any
    principal allowed to create `IOSXEOperationalAction` can also request
    `ProvisionCertificate` when that device has an enabled signer. Treat this
    grant as PKI replacement authority as well as reboot/file/factory-reset
    authority.

`IOSXEOperationalAction` supports:

- `Reboot`
- `CancelReboot`
- `KillProcess`
- `FilePut`
- `FileRemove`
- `FactoryReset`
- `ProvisionCertificate`

Every action targets exactly one `CiscoDevice` and must set
`spec.confirm` to the target device name. The spec is immutable after create,
and the action request must contain exactly the args block matching
`spec.action.kind`. `ProvisionCertificate` requires a `provisionCertificate`
block containing the configured certificate ID and the lowercase SHA-256 of
the exact `tls.crt` bytes followed by the exact `ca.crt` bytes. The worker
rejects a stale ID or digest before connecting to the device. This action is
the only path that can install the configured gNOI identity.
`GNOIOSVerify` remains read-only. Follow the
[secure gNOI provisioning workflow](gnoi-software-lifecycle.md#provisioning-the-ios-xe-gnoi-os-service)
before creating this action.

Example reboot:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXEOperationalAction
metadata:
  name: reload-cat9k-smoke
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

Lifecycle:

- `Pending` action CRs are validated and marked `Running` before the gNOI RPC
  is dispatched.
- A `Running` action is never dispatched a second time. After a controller
  restart, CVK waits for the original five-minute device RPC window and a short
  result-persistence grace. If no result was durably recorded, the action fails
  with `ActionOutcomeUnknown`; its shared mutation Lease remains quarantined
  until expiry, and the action is not replayed. Inspect device state before
  creating a new CR. For `ProvisionCertificate`, a
  `CertificateInstallIndeterminate` failure
  means the create-only result remained unknown after bounded, fresh-connection
  certificate and `OS.Verify` checks: use `GNOICertGet` and inspect device PKI
  state; never retry the same certificate ID blindly.
- Terminal phases are `Succeeded`, `Failed`, and `Rejected`.
- The finalizer is retained while an invocation is in progress so a delete
  request cannot erase the audit trail before completion.
- Normal events are emitted for `Running` and `Succeeded`; Warning events are
  emitted for `Rejected`, `Failed`, and delete-pending audit preservation.

A successful `Reboot` or `FactoryReset` means IOS-XE accepted the request, not
that the device has converged. Its Ready condition reason is
`AcceptedPendingConvergence`, and the shared mutation Lease remains quarantined
until expiry. Observe device reachability and state independently.

`FactoryReset` should be enabled last in any rollout. Prefer namespace-scoped
RBAC for the operators allowed to create these CRs, and keep read-only
`DeviceOperation` RBAC separate from write-class action RBAC.

### Shared device-mutation Lease

`IOSXEOperationalAction` and `IOSXESoftwareUpgrade` serialize through one
platform-neutral `device-disruptive-mutation` Lease per namespaced device. Its
safety TTL is fixed at 26 hours in this release. Definitive outcomes release
immediately; invoked action failures and upgrade outcomes that cannot be safely
correlated retain the Lease until expiry. A delayed reboot is held for its
delay plus that TTL. `CancelReboot` bypasses coordination and never releases
another holder.
Deleting an invoked action keeps its finalizer through the bounded
RPC-and-persistence window. If the outcome remains unknown, CVK records
`ActionOutcomeUnknown`, removes the finalizer so deletion can complete, and
retains the Lease until expiry. Deleting an upgrade does not cancel device work
or clear a required quarantine. The Lease does not fence manual CLI or
external automation, which must be coordinated separately.

Both mutation controllers run only in the IOS-XE per-device worker topology.
They are not registered in aggregator mode, so production use requires
`aggregator.enabled=false` before either gNOI mutation gate is enabled.

## Software Upgrades

!!! danger "Beta — requires runtime gate"
    Software upgrades are **Beta** and **disabled by default**. The per-device
    VK pod must be started with `--enable-iosxesoftwareupgrade` or
    `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1`. Activation reboots the device
    when `strategy: Reload` is used (the default). Test thoroughly on
    non-production devices first.

Enabling either write-class gNOI or software upgrades makes the per-device
worker Deployment use `Recreate`, preventing old and new worker generations
from sharing one at-most-once mutation identity. Plan for a brief
node-management interruption when that worker rolls. `RollingUpdate` returns
after both mutation gates are disabled (or gNOI is globally disabled) and any
signer cleanup has completed.

`IOSXESoftwareUpgrade` drives the gNOI OS install, activate, reachability, and
verify flow. It is disabled unless the per-device VK is started with
`--enable-iosxesoftwareupgrade` /
`CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE`.

Use exactly one image source:

- `url` plus `sha256`, with optional `urlSecretRef`, or `configMapRef`: CVK
  resolves the content and streams it through gNOI `OS.Install`.
- `preinstalled: {}`: activate one exact native inventory version; no transfer
  or registration occurs.
- `deviceFile` with `path` and `sha256`: verify with gNOI `File.Get`, register
  through the IOS-XE RESTCONF install RPC, then activate through gNOI.
- Deprecated `localPath` with optional `localPathSHA256`: inventory-only
  compatibility form; it never registers the file.

Device-file registration is IOS-XE and RESTCONF only, with no CLI fallback.
Ambiguous or non-activatable inventory state fails closed.

An authenticated URL must not contain user information. Its same-namespace
`urlSecretRef` must be explicitly labelled
`cisco.vk/purpose=software-image-source` and contain `allowedScheme`,
`allowedHost`, and `allowedPort` values that authorize the URL's canonical
endpoint before CVK will load credentials. See the
[gNOI software lifecycle guide](gnoi-software-lifecycle.md#software-lifecycle)
for the Secret contract and example.

If `rollbackOnFailure` is true and post-activation verification reports a
different running version than the requested target, the reconciler enters
`RollingBack` only when it captured the previous version and the lifecycle
backend proves that exact version remains activatable. It terminates as
`RolledBack` once `OS.Verify` confirms that version. CVK refuses automatic
rollback on an individual-supervisor path because it cannot yet prove a safe,
separately verified rollback sequence for both supervisors.

Upgrade strategies are `Reload`, `ISSU`, and `NoReboot`. `Reload` is the
default. `NoReboot` requests activation without an immediate reload, but does
not establish a hitless or non-disruptive upgrade; when the old version remains
running, it terminates as `StagedForNextBoot` and requires a separately
authorized reboot. `ISSU` is currently rejected during preflight until CVK can
verify that IOS-XE selected the ISSU path. If `OS.Verify` requires individual
supervisor handling, CVK installs active then standby, activates standby then
active, rejects `NoReboot`, and uses one shared install deadline.

Staging, activation, and rollback intent is persisted before the associated
RPC and is not replayed after an ambiguous result. Operators may need to
inspect device state when this at-most-once policy leaves an outcome unknown.

After upgrading an existing cluster, the controller safely adopts an unmarked
upgrade only in `Pending` or `Resolving`. Any later unmarked phase may represent
a mutation submitted by the previous controller, so it terminates with
`LegacyStateOutcomeUnknown` and holds the shared Lease as a quarantine instead
of replaying device work. Inspect the device before starting a replacement.
Older manifests may still contain `resumePolicy` and `maxRetries`, and stored
status may contain `retryCount` or terminal phase `Cancelled`; these fields are
preserved for API compatibility. Admission retains the historical `Retry` and
`3` defaults for safe controller rollback, but the retry fields are ignored by
the at-most-once reconciler.
An unknown non-empty execution-model marker is preserved as newer-controller
state; this controller holds the device Lease and issues no RPC rather than
rewriting or replaying it.

During this one-release migration, either mutation controller performs a
read-only scan of both mutation CRDs before it can claim the per-device Lease.
The scan chooses one deterministic legacy quarantine holder and fails closed
on API/RBAC errors, including when the legacy object's own controller is
disabled. Released invoked actions are retained for a bounded 26-hour safety
window plus any accepted reboot delay; this IOS-XE compatibility duration is
independent of future drivers' normal Lease settings.

## RBAC

The per-device VK service account watches `DeviceOperation` in order to run
operations targeting its device. It has `create` on the main resource only so
the localhost admin endpoint can synthesize transient operations, and `delete`
only for `ttlSecondsAfterFinished` cleanup. Operation results are written
through `deviceoperations/status`.

Operators who create `DeviceOperation` objects directly should receive their
own namespace-scoped RBAC. Write-class actions and software upgrades use
separate CRDs and should receive separate RBAC grants.

## Admin Exec Wrapper

The localhost admin endpoint `POST /v1/exec` now creates a transient `DeviceOperation` and polls status when the in-pod controller client is available. This preserves the existing plugin shape while routing execution through the CRD audit path.

## Status

Results are written to `.status.outputs[]`; large packet captures may also set
`.status.artifactURIs[]`. Terminal phase is one of `Succeeded`, `Failed`, or
`Cancelled`. `ttlSecondsAfterFinished` requests best-effort cleanup after
completion.

### DeviceOperation — example output

After applying a `ShowCommand` operation, watch the phase:

```bash
$ kubectl get deviceoperation show-version -w
NAME           PHASE       AGE
show-version   Pending     0s
show-version   Running     1s
show-version   Succeeded   3s
```

Full status after completion:

```bash
$ kubectl describe deviceoperation show-version
Name:         show-version
Namespace:    default
Labels:       <none>
API Version:  ops.cisco.vk/v1alpha1
Kind:         DeviceOperation
Spec:
  Device Ref:
    Name:  cat9k-smoke
  Operation:
    Commands:
      show version
    Kind:  ShowCommand
  Ttl Seconds After Finished:  300
Status:
  Conditions:
    Last Transition Time:  2026-05-30T10:14:03Z
    Message:               operation succeeded
    Reason:                Succeeded
    Status:                True
    Type:                  Ready
  Outputs:
    - Name:    show-version
      Output:  |
        Cisco IOS XE Software, Version 17.18.02
        Technical Support: http://www.cisco.com/techsupport
        ...
        cisco C9300-24P (X86) processor with 1392928K/6147K bytes of memory.
        Processor board ID FCW2144L0GH
        ...
        Configuration register is 0x102
  Phase:  Succeeded
Events:
  Type    Reason     Age  From                Message
  ----    ------     ---  ----                -------
  Normal  Running    3s   device-operation    dispatching ShowCommand to cat9k-smoke
  Normal  Succeeded  1s   device-operation    operation completed in 2.1s
```

For a `GNOIPing` probe:

```bash
$ kubectl describe deviceoperation ping-gateway
...
Status:
  Outputs:
    - Name:    ping-result
      Output:  |
        Source: 10.0.0.1
        Time: 4ms  [10.0.0.254 -> 10.0.0.1]
        Time: 3ms  [10.0.0.254 -> 10.0.0.1]
        Time: 4ms  [10.0.0.254 -> 10.0.0.1]
        Stats: Sent=3  Received=3  MinTime=3ms  AvgTime=3ms  MaxTime=4ms
  Phase:  Succeeded
```

### IOSXEOperationalAction — example output

```bash
$ kubectl get iosxeoperationalaction
NAME              PHASE      AGE
reload-cat9k      Succeeded  12m
```

```bash
$ kubectl describe iosxeoperationalaction reload-cat9k
...
Status:
  Conditions:
    Last Transition Time:  2026-05-30T10:00:00Z
    Reason:                AcceptedPendingConvergence
    Status:                True
    Type:                  Ready
  Invocation ID:           a3f2e1d0-8c7b-4a5f-9e6d-1b2c3d4e5f60
  Phase:                   Succeeded
Events:
  Type    Reason     Age   From                         Message
  ----    ------     ----  ----                         -------
  Normal  Running    12m   iosxe-operational-action     dispatching Reboot to cat9k-smoke
  Normal  Succeeded  10m   iosxe-operational-action     action accepted; convergence is not observed and the mutation Lease remains quarantined
```

### IOSXESoftwareUpgrade — example output

```bash
$ kubectl get iosxesoftwareupgrade -w
NAME            PHASE          AGE
upgrade-cat9k   Pending        0s
upgrade-cat9k   Resolving      2s
upgrade-cat9k   Transferring   8s
upgrade-cat9k   Validating     4m31s
upgrade-cat9k   Activating     4m45s
upgrade-cat9k   AwaitingReachability  4m51s
upgrade-cat9k   Verifying      17m
upgrade-cat9k   Succeeded      17m
```

For `deviceFile`, expect an additional `Staging` phase before `Validating`.
Use `kubectl describe iosxesoftwareupgrade upgrade-cat9k` to inspect the pinned
source digest, staging operation ID, inventory state, conditions, and events.

## Roadmap Gates

The following items are deliberately outside the current read-only v1alpha1
surface:

- Tenant ownership/admission checks before promoting write-class CRDs beyond
  tightly controlled namespaces.
- Conversion webhook scaffolding before promotion beyond `v1alpha1`.
- External artifact sinks beyond the in-namespace ConfigMap backing for large
  packet-capture output.
- Cross-device or multi-supervisor rollback policy beyond re-activating the
  previously observed single-device version.
