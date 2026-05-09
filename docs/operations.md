# DeviceOperation

`DeviceOperation` is the sibling-CRD path for auditable, asynchronous, non-Pod operations.

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

`PacketCapture` reads an existing IOS-XE monitor capture buffer. Provide either `operation.args.name`/`capture`, which expands to `show monitor capture <name> buffer dump`, or an explicit allowlisted `operation.args.command`.

Write-class operations such as reload, factory reset, and config push are intentionally not implemented. They require the later multi-tenancy, admission, and RBAC split described in the unified architecture plan.

## Admin Exec Wrapper

The localhost admin endpoint `POST /v1/exec` now creates a transient `DeviceOperation` and polls status when the in-pod controller client is available. This preserves the existing plugin shape while routing execution through the CRD audit path.

## Status

Results are written to `.status.outputs[]`; terminal phase is one of `Succeeded`, `Failed`, or `Cancelled`. `ttlSecondsAfterFinished` requests best-effort cleanup after completion.
