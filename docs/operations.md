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

Packet-capture output larger than 256 KiB is written to a ConfigMap named
`<deviceoperation-name>-output` in the same namespace. The status keeps a
truncated preview in `.status.outputs[].output` and records
`.status.artifactURIs[]` as `configmap://<namespace>/<name>/<key>`, for example
`configmap://default/capture-output/output`. Captures larger than 900 KiB are
rejected with `Ready=False, reason=ArtifactTooLarge`.

Write-class operations such as reload, factory reset, and config push are intentionally not implemented. They require the later multi-tenancy, admission, and RBAC split described in the unified architecture plan.

## Implementation Boundary

The v1alpha1 controller intentionally keeps the three read-only kinds in one
small reconciler because they share the same validation, transport, redaction,
inline output, TTL, and status machinery.

That is not the intended shape for write-class operations. Before any operation
can change device state, each kind must move behind its own reconciler or
capability-specific dispatcher with independent validation, RBAC, audit policy,
idempotency, cancellation, and artifact handling. This is the planned split for
reload, config push, factory reset, and future packet-capture setup flows.

## RBAC

The per-device VK service account watches `DeviceOperation` in order to run
operations targeting its device. It has `create` on the main resource only so
the localhost admin endpoint can synthesize transient operations, and `delete`
only for `ttlSecondsAfterFinished` cleanup. Operation results are written
through `deviceoperations/status`.

Operators who create `DeviceOperation` objects directly should receive their
own namespace-scoped RBAC. Write-class operations will require separate
resources or verbs plus admission policy; they should not reuse the read-only
v1alpha1 permission set.

## Admin Exec Wrapper

The localhost admin endpoint `POST /v1/exec` now creates a transient `DeviceOperation` and polls status when the in-pod controller client is available. This preserves the existing plugin shape while routing execution through the CRD audit path.

## Status

Results are written to `.status.outputs[]`; large packet captures may also set
`.status.artifactURIs[]`. Terminal phase is one of `Succeeded`, `Failed`, or
`Cancelled`. `ttlSecondsAfterFinished` requests best-effort cleanup after
completion.

## Roadmap Gates

The following items are deliberately outside the current read-only v1alpha1
surface:

- Per-kind reconcilers and capability maps before write operations.
- Tenant ownership/admission checks before write-class CRDs.
- Conversion webhook scaffolding before promotion beyond `v1alpha1`.
- External artifact sinks beyond the in-namespace ConfigMap backing for large
  packet-capture output.
