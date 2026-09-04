# Software Lifecycle Management

!!! warning "Beta"
    The gNOI operations, write-class actions, and software lifecycle features
    described on this page are **Beta**. They are functional and tested on
    supported platforms but carry `v1alpha1` API versions and schemas that
    may change between releases. Write-class gNOI actions and software
    upgrades are disabled by default and require explicit runtime gates before
    use. Evaluate in non-production environments first.

!!! warning "Beta"
    The gNOI operations, write-class actions, and software lifecycle features
    described on this page are **Beta**. They are functional and tested on
    supported platforms but carry `v1alpha1` API versions and schemas that
    may change between releases. Write-class gNOI actions and software
    upgrades are disabled by default and require explicit runtime gates before
    use. Evaluate in non-production environments first.

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
| `gnoi.insecure` | `CISCO_VK_GNOI_INSECURE=1` | Force the legacy insecure IOS-XE gNXI listener. CVK does not send passwords on this connection. |
| `gnoi.port` | `CISCO_VK_GNOI_PORT=<port>` | Pin the gNOI listener port. Empty lets CVK infer `50052` for insecure and `9339` for secure gNOI. |
| `gnoi.disabled` | `CISCO_VK_GNOI_DISABLED=1` | Prevent the per-device gNOI client from being constructed. |
| `gnoi.enableSoftwareUpgrade` | `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1` | Enable `IOSXESoftwareUpgrade` reconciliation. |
| `gnoi.enableWriteClass` | `CISCO_VK_ENABLE_WRITE_CLASS_GNOI=1` | Enable destructive/write-class `IOSXEOperationalAction` reconciliation. |

## Secure IOS-XE gNXI

IOS-XE 17.18.x deployments that enforce password authentication, including
observed 17.18.4 systems, accept that traffic on the secure gNXI listener. The
recommended password-authenticated device configuration is:

```text
gnxi
gnxi secure-trustpoint <server-trustpoint>
gnxi secure-server
gnxi secure-password-auth
! The default secure port is 9339.
```

Do not add `gnxi secure-client-auth` for password-only authentication. That
command asks IOS-XE to authenticate a client certificate and is appropriate
only when you have deliberately configured mutual TLS and mounted a client
certificate and key into the VK pod.

Select the listener independently of the RESTCONF port in the `CiscoDevice`:

```yaml
spec:
  username: admin
  credentialSecretRef:
    name: cat9000-1-creds       # Secret contains only the key "password"
  tls:
    enabled: true
    insecureSkipVerify: false
    caFile: /etc/cisco-vk/device-ca/ca.crt
  gnoi:
    transportSecurity: tls
    port: 9339
```

The CA path is inside the per-device VK pod; see
[Security](security.md#secure-ios-xe-gnoi) before using file-backed TLS
settings. For a lab certificate, `insecureSkipVerify: true` can isolate trust
configuration problems, but it is not a production setting.

IOS-XE password authentication does **not** use an HTTP Basic
`Authorization` header. CVK derives per-RPC credentials without duplicating
the password in configuration or logs:

| gRPC metadata key | Value source |
|---|---|
| `username` | `CiscoDevice.spec.username` |
| `password` | The resolved `credentialSecretRef` key named `password` |

Both keys are attached to every unary or streaming gNOI RPC only when the
connection uses TLS. This matches Cisco's
[IOS-XE 17.18 username/password metadata format](https://www.cisco.com/c/en/us/td/docs/ios-xml/ios/prog/configuration/1718/b-1718-programmability-cg/gnmi.html)
and the
[IOS-XE 17.18 gNOI authentication guidance](https://www.cisco.com/c/en/us/td/docs/ios-xml/ios/prog/configuration/1718/b-1718-programmability-cg/gnoi.html).

For older lab devices with an unauthenticated listener, use an explicit
compatibility block:

```yaml
spec:
  gnoi:
    transportSecurity: plaintext
    port: 50052
```

Plaintext mode never transmits the device password. It requires an IOS-XE
listener on which authentication is disabled and should not be used on an
untrusted network. `CISCO_VK_GNOI_INSECURE=1` remains a temporary compatibility
override and takes precedence over `spec.gnoi.transportSecurity`.

### Provisioning the IOS-XE gNOI OS service

Password metadata authenticates the controller, but it does not move IOS-XE's
gNXI service into the `Provisioned` state required by `OS.Verify`,
`OS.Install`, and `OS.Activate`. If an authenticated `OS.Verify` returns
`FailedPrecondition: Device has not been provisioned`, CVK can perform an
explicit, gNOI-scoped certificate bootstrap.

Provisioning is opt-in. Create a dedicated same-namespace Secret containing a
pre-issued device identity and the complete desired target CA bundle:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cat9000-1-gnoi-identity
type: Opaque
stringData:
  tls.crt: |
    -----BEGIN CERTIFICATE-----
    <CA-signed device leaf certificate>
    -----END CERTIFICATE-----
  tls.key: |
    -----BEGIN PRIVATE KEY-----
    <matching unencrypted RSA private key>
    -----END PRIVATE KEY-----
  ca.crt: |
    -----BEGIN CERTIFICATE-----
    <any independent gNXI/gNMI peer-trust CA that must be preserved>
    -----END CERTIFICATE-----
    -----BEGIN CERTIFICATE-----
    <device leaf issuer chain; intermediate first and root last>
    -----END CERTIFICATE-----
```

Reference it only from the gNOI block:

```yaml
spec:
  address: cat9000-1.example.net
  gnoi:
    transportSecurity: tls
    port: 9339
    certificateProvisioning:
      certificateID: cvk-gnoi-os
      replaceTargetCABundle: true
      secretRef:
        name: cat9000-1-gnoi-identity
```

The leaf certificate must contain a DNS or IP SAN matching `spec.address`, be
currently valid, and use a matching RSA key of at least 2048 bits. Every
certificate in `ca.crt` must be a CA, and the bundle must contain a complete
chain that validates the leaf. It must also be the **complete desired
replacement** for IOS-XE's target-side gNXI/gNMI CA bundle, including every CA
needed to authenticate existing mTLS clients. Follow the gNOI ordering rule:
independent roots first, followed by each certificate chain with its root last.
The required `replaceTargetCABundle: true` is an explicit acknowledgement of
this device-side replacement. The controller mounts the Secret read-only only
in the device's VK pod; certificate and key bytes are not copied into the
ConfigMap, environment, status, events, or logs.

CVK activates this path only after the exact IOS-XE not-provisioned response.
It first queries the Certificate service, installs the identity only when the
configured certificate ID is absent, discards the pre-provisioning connection,
and lets reconciliation reconnect before retrying `OS.Verify`. An existing ID
with the same leaf is treated idempotently. An existing ID with a different
leaf fails closed: CVK never rotates, revokes, or overwrites a certificate
implicitly. Automatic certificate rotation is outside this bootstrap flow;
rotate an already-provisioned gNXI identity out of band (or through a future,
explicit Rotate workflow) as a separately reviewed operation.

Updating the Secret alone does not rotate an identity on a device whose OS
service is already provisioned. Keep the Secret consistent with the installed
identity; changing its CA can also make the next gNOI connection reject the
certificate currently presented by IOS-XE.

Only the verified root of the device leaf's chain is added to the gNOI
client's trust roots, so the post-install connection can validate the new
device identity without changing the TLS configuration used by RESTCONF,
NETCONF, app hosting, or telemetry.
The initial connection must still trust the certificate IOS-XE presents before
provisioning through `spec.tls.caFile`; `insecureSkipVerify: true` is suitable
only for a controlled lab bootstrap.

CVK uses the external-key form allowed by gNOI `LoadCertificate` so a
pre-issued leaf and key can be supplied. Cisco's IOS-XE 17.18 documentation
demonstrates the target-generated CSR flow, not this external-key variant;
treat external-key support as requiring a real-device IOS-XE 17.18.04
acceptance test before production rollout.

!!! warning "IOS-XE gNXI service restart"
    CVK confines its configuration and credentials to gNOI, but IOS-XE shares
    the provisioned identity and CA bundle with gNMI. gNOI Install replaces
    that complete target-side CA bundle, can restart gNXI, and changes the
    certificate presented to external gNMI clients. Put every peer CA that
    must remain trusted in `ca.crt`, ensure clients trust the new server CA,
    and schedule provisioning as a device-management change. RESTCONF,
    NETCONF, and the VK's other TLS settings are not modified by CVK.

Before provisioning, use `GNOICertGet` to verify secure connectivity and
password metadata because the Certificate service remains available in the
default state. Use `GNOIOSVerify` only after provisioning is configured or
`show gnxi state detail` reports `State: Provisioned` and the OS service is
operational. Controller-managed provisioning requires the per-device worker;
the aggregated config-only topology does not run gNOI lifecycle reconcilers.

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

IOS-XE support varies by release and platform. In particular, System or File
RPCs can return `Unimplemented` even when TLS and authentication are correct.
For a read-only secure-listener smoke test before IOS-XE is provisioned, use
`GNOICertGet`. After the device reports `State: Provisioned`, prefer
`GNOIOSVerify` over `GNOITime` because System service support varies by
platform.

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

## Upgrade Manifest Walkthrough

Each upgrade is a single Kubernetes CR. Create one manifest per device and
apply it when ready — the upgrade begins immediately and the CR records the
full audit trail.

### Preparing the image

Before creating the manifest, collect the image hash. Use the sha256 digest
for `imageSource.sha256`. The MD5 is included as a human-reference annotation
but CVK uses sha256 exclusively for verification:

```bash
# On a Linux host that holds the image:
sha256sum cat9k_iosxe.26.01.01.SPA.bin
md5sum    cat9k_iosxe.26.01.01.SPA.bin
stat --format='%s' cat9k_iosxe.26.01.01.SPA.bin
```

Serve the image from a source the device can reach — TFTP and HTTP are the
most common options in lab and production environments respectively.

### Example — TFTP upgrade (Catalyst 9300, IOS-XE 26.01.01)

This is a real-world production manifest. The in-cluster TFTP service
(`rust-tftp-otel-headless`) serves images to devices over the management
network. Labels and annotations carry out-of-band metadata that is useful
for audit queries, dashboards, and GitOps tooling — they are not processed
by CVK.

```yaml
# 9300-4 IOS-XE upgrade manifest for Cisco Virtual Kubelet software lifecycle.
#
# Target device:
#   CiscoDevice: default/cat9000-4
#   Management IP: 198.51.100.103
#   Hardware: C9300-24P, serial FOC2416U0MV
#
# Image source served by the in-cluster TFTP service:
#   tftp://rust-tftp-otel-headless.ng-pnp.svc.cluster.local:6969/images/cat9k_iosxe.26.01.01.SPA.bin
#   size:   1260618344 bytes
#   md5:    fd4a41c41a7de1a9d907c4f35f46e334
#   sha256: 7de3c6875e3c1c96d5920e8542c72b1bcb5d913d99645ef5687f44dd4024cdf4
#
# Apply when ready to perform the upgrade/reload:
#   kubectl apply -f 9300-4-upgrade-26.01.01.yaml
#   kubectl get iosxesoftwareupgrade upgrade-cat9000-4-26-01-01 -n default -o wide -w
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: upgrade-cat9000-4-26-01-01
  namespace: default
  labels:
    app.kubernetes.io/part-of: lifecycle-mgmt
    lifecycle.cisco.com/device: cat9000-4
    lifecycle.cisco.com/operation: iosxe-upgrade
  annotations:
    lifecycle.cisco.com/source-protocol: tftp
    lifecycle.cisco.com/tftp-url: tftp://rust-tftp-otel-headless.ng-pnp.svc.cluster.local:6969/images/cat9k_iosxe.26.01.01.SPA.bin
    lifecycle.cisco.com/image-file: cat9k_iosxe.26.01.01.SPA.bin
    lifecycle.cisco.com/image-size-bytes: "1260618344"
    lifecycle.cisco.com/image-md5: fd4a41c41a7de1a9d907c4f35f46e334
    lifecycle.cisco.com/image-sha256: 7de3c6875e3c1c96d5920e8542c72b1bcb5d913d99645ef5687f44dd4024cdf4
spec:
  deviceRef:
    name: cat9000-4
  imageSource:
    url: tftp://rust-tftp-otel-headless.ng-pnp.svc.cluster.local:6969/images/cat9k_iosxe.26.01.01.SPA.bin
    sha256: 7de3c6875e3c1c96d5920e8542c72b1bcb5d913d99645ef5687f44dd4024cdf4
  targetVersion: 26.01.01
  strategy: Reload
  rollbackOnFailure: false
  resumePolicy: Retry
  maxRetries: 3
  rebootTimeoutSeconds: 3600
```

**Field notes:**

- `targetVersion: 26.01.01` — shortest unambiguous prefix. CVK's prefix-aware
  comparison will match `26.01.01.0.340` or `26.01.01.0.340.1766116039`.
- `strategy: Reload` — OS.Activate is called with reboot allowed; CVK waits
  for the device to come back (up to `rebootTimeoutSeconds`) and then verifies
  the running version.
- `rollbackOnFailure: false` — the device is left on whatever version it
  landed on after a verify mismatch. Set to `true` in environments where
  automatic rollback is preferred.
- `rebootTimeoutSeconds: 3600` — allow a full hour for the device to reload.
  This is appropriate for Catalyst 9000; set to 1800 for faster platforms.

### Monitoring the upgrade

```bash
# Watch the phase progress in real time:
kubectl get iosxesoftwareupgrade upgrade-cat9000-4-26-01-01 -n default -o wide -w

NAME                          DEVICE      PHASE              TARGET     AGE
upgrade-cat9000-4-26-01-01    cat9000-4   Pending            26.01.01   0s
upgrade-cat9000-4-26-01-01    cat9000-4   Resolving          26.01.01   2s
upgrade-cat9000-4-26-01-01    cat9000-4   Transferring       26.01.01   8s
upgrade-cat9000-4-26-01-01    cat9000-4   Validating         26.01.01   4m22s
upgrade-cat9000-4-26-01-01    cat9000-4   Activating         26.01.01   4m35s
upgrade-cat9000-4-26-01-01    cat9000-4   AwaitingReachability 26.01.01 4m38s
upgrade-cat9000-4-26-01-01    cat9000-4   Verifying          26.01.01   16m14s
upgrade-cat9000-4-26-01-01    cat9000-4   Succeeded          26.01.01   16m31s
```

```bash
# See full status detail including conditions:
kubectl describe iosxesoftwareupgrade upgrade-cat9000-4-26-01-01

...
Status:
  Phase:           Succeeded
  Target Version:  26.01.01
  Running Version: 26.01.01.0.340
  Conditions:
    Type                Status  Reason
    ----                ------  ------
    TransferComplete    True    ImageTransferred
    ValidationPassed    True    PrefligthOK
    ActivationComplete  True    OSActivated
    VerificationPassed  True    VersionMatch
Events:
  Normal  PhaseTransition  16m  Transferring → Validating: image hash verified
  Normal  PhaseTransition  11m  Activating → AwaitingReachability: OS.Activate sent
  Normal  PhaseTransition  0s   Verifying → Succeeded: running 26.01.01.0.340
```

### Example — local flash path (image already on device)

Use `localPath` when the image has been pre-staged to device storage (e.g.
via ZTP or a previous TFTP transfer):

```yaml
spec:
  deviceRef:
    name: cat9000-3
  imageSource:
    localPath: flash:cat9k_iosxe.17.18.02.SPA.bin
    localPathSHA256: a1b2c3d4e5f6...  # optional; from gNOI File.Get
  targetVersion: 17.18.02
  strategy: Reload
  rollbackOnFailure: true
  rebootTimeoutSeconds: 1800
```

### Example — stage only, reload later (NoReboot)

`NoReboot` stages the image for activation without triggering an immediate
reload. Schedule the reload separately through `IOSXEOperationalAction` during
a maintenance window:

```yaml
spec:
  deviceRef:
    name: cat9000-5
  imageSource:
    url: tftp://rust-tftp-otel-headless.ng-pnp.svc.cluster.local:6969/images/cat9k_iosxe.26.01.01.SPA.bin
    sha256: 7de3c6875e3c1c96d5920e8542c72b1bcb5d913d99645ef5687f44dd4024cdf4
  targetVersion: 26.01.01
  strategy: NoReboot          # stage only; does not reboot
  rebootTimeoutSeconds: 0     # not applicable for NoReboot
```

The CR ends in `Succeeded` once the image is staged and activated
(without reload). Trigger the reload during maintenance:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXEOperationalAction
metadata:
  name: reload-cat9000-5-maint-window
  namespace: default
spec:
  deviceRef:
    name: cat9000-5
  confirm: cat9000-5          # must match deviceRef.name exactly
  action:
    kind: Reboot
    reboot:
      message: "Scheduled maintenance window reload"
      delay: 0
```

## Operator Workflow

1. Confirm the device exposes the secure gNXI listener (`gnxi secure-server`
   and `gnxi secure-password-auth` are enabled) and that `show gnxi state`
   reports it up. Use the plaintext listener only for explicit legacy lab
   compatibility.
2. Enable the software upgrade gate on the per-device VK pod via Helm
   (`gnoi.enableSoftwareUpgrade: true`) or the env var
   `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1`.
3. Grant RBAC: upgrade operators need `create`/`get`/`watch` on
   `IOSXESoftwareUpgrade`. Read-only users get `DeviceOperation` only.
4. Compute the image sha256 and prepare the manifest (see examples above).
5. Apply the manifest and monitor with `kubectl get iosxesoftwareupgrade -w`.
6. After `Succeeded`, delete the CR when the audit record is no longer needed,
   or retain it as an immutable upgrade record.

For `DeviceOperation` show-command and diagnostic examples see the
[Operations Runbook](operations.md).
