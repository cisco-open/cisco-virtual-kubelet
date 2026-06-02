# Operations Runbook

## Upgrading CRDs

Cisco Virtual Kubelet ships CRD manifests alongside the Helm chart. When
upgrading to a new CVK version, apply the updated CRDs before upgrading the
chart — Helm does not manage CRD updates automatically.

```bash
# 1. Apply updated CRD manifests from the new chart version:
kubectl apply -f charts/cisco-virtual-kubelet/crds/

# 2. Verify all CRDs registered at the new schema version:
kubectl get crds | grep cisco

NAME                                    CREATED AT
ciscodevices.cisco.vk                   2026-01-10T09:00:00Z
deviceoperations.ops.cisco.vk           2026-01-10T09:00:00Z
iosxeconfigs.config.cisco.vk            2026-01-10T09:00:00Z
iosxesoftwareupgrades.ops.cisco.vk      2026-01-10T09:00:00Z
iosxeoperationalactions.ops.cisco.vk    2026-01-10T09:00:00Z

# 3. Upgrade the Helm release:
helm upgrade cisco-vk ./charts/cisco-virtual-kubelet \
  --namespace cisco-vk \
  --set image.tag=<new-version>

# 4. Confirm manager pod is running the new image:
kubectl rollout status deployment/cisco-vk-manager -n cisco-vk
```

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
    (reboot, file write, factory reset) and are irreversible. Apply strict
    namespace-scoped RBAC before enabling.

`IOSXEOperationalAction` supports:

- `Reboot`
- `CancelReboot`
- `KillProcess`
- `FilePut`
- `FileRemove`
- `FactoryReset`

Every action targets exactly one `CiscoDevice` and must set
`spec.confirm` to the target device name. The spec is immutable after create,
and the action request must contain exactly the args block matching
`spec.action.kind`.

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
- A `Running` action is never dispatched a second time. If the controller dies
  after the device-side invocation, operators must create a new CR to retry.
- Terminal phases are `Succeeded`, `Failed`, and `Rejected`.
- The finalizer is retained while an invocation is in progress so a delete
  request cannot erase the audit trail before completion.
- Normal events are emitted for `Running` and `Succeeded`; Warning events are
  emitted for `Rejected`, `Failed`, and delete-pending audit preservation.

`FactoryReset` should be enabled last in any rollout. Prefer namespace-scoped
RBAC for the operators allowed to create these CRs, and keep read-only
`DeviceOperation` RBAC separate from write-class action RBAC.

## Software Upgrades

!!! danger "Beta — requires runtime gate"
    Software upgrades are **Beta** and **disabled by default**. The per-device
    VK pod must be started with `--enable-iosxesoftwareupgrade` or
    `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1`. Activation reboots the device
    when `strategy: Reload` is used (the default). Test thoroughly on
    non-production devices first.

`IOSXESoftwareUpgrade` drives the gNOI OS install, activate, reachability, and
verify flow. It is disabled unless the per-device VK is started with
`--enable-iosxesoftwareupgrade` /
`CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE`.

Use exactly one image source:

- `url` plus `sha256`, with optional `urlSecretRef`
- `configMapRef`
- `localPath` and optional `localPathSHA256`

For `localPath`, use `localPathSHA256` when the device supports gNOI File.Get
hash reporting. Without that field, CVK can activate a staged image but cannot
verify the local flash file before activation.

If `rollbackOnFailure` is true and post-activation verification reports a
different running version than the requested target, the reconciler enters
`RollingBack`, re-activates the previously observed running version, and
terminates as `RolledBack` once `OS.Verify` confirms that version.

Upgrade strategies are `Reload`, `ISSU`, and `NoReboot`. `Reload` is the
default. `NoReboot` stages the image and leaves the actual reload to a later
operator action. `ISSU` requests the normal activate path and then verifies
that the device selected the ISSU path when IOS-XE reports that detail.

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
    Reason:                Succeeded
    Status:                True
    Type:                  Ready
  Invocation ID:           a3f2e1d0-8c7b-4a5f-9e6d-1b2c3d4e5f60
  Phase:                   Succeeded
Events:
  Type    Reason     Age   From                         Message
  ----    ------     ----  ----                         -------
  Normal  Running    12m   iosxe-operational-action     dispatching Reboot to cat9k-smoke
  Normal  Succeeded  10m   iosxe-operational-action     gNOI Reboot RPC accepted
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

```bash
$ kubectl describe iosxesoftwareupgrade upgrade-cat9k
...
Spec:
  Device Ref:
    Name:  cat9k-smoke
  Image Source:
    Local Path:          bootflash:cat9k_iosxe.17.18.02.SPA.bin
    Local Path SHA256:   a3f2e1d0...
  Rollback On Failure:   true
  Strategy:              Reload
  Target Version:        17.18.02
Status:
  Completed At:          2026-05-30T10:28:32Z
  Conditions:
    Last Transition Time:  2026-05-30T10:28:32Z
    Reason:                Succeeded
    Status:                True
    Type:                  Ready
  Phase:                   Succeeded
  Running Version:         17.18.02.0.4112.1766116039
  Started At:              2026-05-30T10:00:00Z
Events:
  Type    Reason                Age   Message
  ----    ------                ----  -------
  Normal  Resolving             28m   resolved target version 17.18.02
  Normal  Transferring          28m   skipping transfer: localPath source
  Normal  Validating            23m   staged image validated: sha256 match
  Normal  Activating            23m   gNOI OS.Activate requested (strategy: Reload)
  Normal  AwaitingReachability  23m   device rebooting; polling for reachability
  Normal  Verifying             11m   device reachable; verifying running version
  Normal  Succeeded             11m   running version 17.18.02.0.4112 matches target
```

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
