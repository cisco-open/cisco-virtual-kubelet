# ux-agx / C9K qualification — 1 November 2026

Status: **not qualified: required gNOI File.Get returned `Unimplemented`.**

The user selected `ux-agx` and `10.1.1.1`, then authorized proceeding with the
presented SSH fingerprint. Strict verification used a task-local known_hosts
file; the shared saved entry was preserved. Authenticated inspection identified
`Edge-9K`, a **C9300-24UX**, serial `FCW2247C1AJ`, running **IOS-XE 17.18.01 /
17.18.1 RELEASE fc2**.

## Physical results

| Check | Result |
| --- | --- |
| Authenticated hardware/native application baseline | Captured; `iris` RUNNING and `c9ktest` DEPLOYED |
| gNOI TLS identity and authentication | Verified using the issuer obtained through pinned, authenticated SSH; IP SAN `10.1.1.1`, port 9339 |
| gNOI OS.Verify positive control | Succeeded; version `17.18.01.0.163.1754452555` |
| Required gNOI File.Get capability | Failed with gRPC `Unimplemented` for a nonexistent, run-scoped flash filename |
| File.Put, size/SHA256 read-back and terminal hash | Not attempted after the required read service failed |
| Deployment, checksum fault injections, worker replacement, update and rollback | Blocked; no physical lifecycle pass |
| Final native inventory | `iris` RUNNING and `c9ktest` DEPLOYED, matching baseline |
| Switch/application changes | None; no package upload, configuration change or fixture app created |

The successful OS.Verify control establishes a functioning authenticated gNOI
connection. File.Get was unavailable through this endpoint/profile; this result
does not establish support on other firmware or devices. The current Verified
implementation requires this read-back service and therefore cannot qualify on
the tested profile. File.Put support remains unproven.

HTTPS RESTCONF has an additional prerequisite: the existing self-signed port 443
certificate lacks an IP SAN. No certificate/trustpoint was changed. A suitable
certificate and worker trust configuration are required before deployment.

The final inventory verifies native states only. Independent application
data-plane service health, package-signature acceptance and managed worker/RBAC
behavior were not tested.

## Candidate, fixtures and lab infrastructure

The dirty-worktree candidate is bound by its runtime source fingerprint, binary
SHA256 and local image config digest in [qualification.json](qualification.json).
Its Linux/amd64 binary SHA256 is
`d26123fe5c99a0df3574f776da33a00305544efd7613c0076f573a7e40103f3e`.
No candidate CVK manager or worker was deployed.

Two small scratch-based fixture images with HTTP release markers A and B were
built and retained. No signed descriptors or device packages were published.
These fixtures are not evidence of vulnerable/fixed release assessment.

The shared ux-agx Kubernetes `v1.34.5+k3s1` control plane remained Ready. Its CVK
version/admission gate and configuration were not changed. Three isolated kind
startup attempts failed and were retained as failures. The requested longer
kubeadm timeout patch did not take effect; it is not a validated fix.

A subsequent isolated `v1.35.9+k3s1` control plane reached Ready with `/readyz`
returning `ok`. The owned kind and k3s containers were removed after the hardware
capability failure. The first k3s removal encountered a zombie-process error;
removal succeeded after the container exited, without restarting shared Docker.
Task-private diagnostics, build artifacts, k3s data/output and cached images
remain under `/tmp/cvk-verified-qual-20261101` on ux-agx.

## Evidence

- [qualification.json](qualification.json): exact candidate/target binding,
  physical matrix, limitations and evidence SHA256 values.
- [preflight.json](preflight.json): final trust, device and infrastructure report.
- [device-baseline.json](device-baseline.json) and
  [device-prerequisites.json](device-prerequisites.json): authenticated CLI
  observations and public certificate metadata.
- [file-get-capability.json](file-get-capability.json) and
  [os-verify-control.json](os-verify-control.json): real RPC results.
- [probe-provenance.json](probe-provenance.json): probe source/build provenance
  and timestamp limits. The original File.Get-only binary digest was not captured;
  the retained binary digest identifies the later OS.Verify-capable build.
- [final-inventory.json](final-inventory.json),
  [k3s-readiness.json](k3s-readiness.json) and
  [final-cleanup.json](final-cleanup.json): final native states, isolated cluster
  readiness and cleanup.
- [preparation.json](preparation.json) and
  [preflight-before-identity-confirmation.json](preflight-before-identity-confirmation.json):
  historical preparation snapshots, superseded by the authenticated results.
- [Physical test procedure](../../verified-iosxe-qualification.md) and
  [earlier access attempt](../verified-iosxe-2026-10-31/README.md).

## Next implementation step

Provide an optional secure IOS-XE transport for devices without gNOI File.Get,
with pinned target identity, exact staged-file identity, bounded transfer,
on-device SHA256 verification and equivalent durable recovery checks. A candidate
is SSH/SCP plus native checksum verification, subject to capability validation
and its own negative tests. Keep transport selection explicit; an `Unimplemented`
response must never silently disable verification or fall back to Standard.

Alternatively, select a firmware/device profile that implements the required
gNOI file service. Resolve RESTCONF TLS prerequisites, then rerun the complete
managed deployment/restart/update/rollback matrix. Neither option is qualified by
this attempt. Standard remains the default and Governed remains deferred.
