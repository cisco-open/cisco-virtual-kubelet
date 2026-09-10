# Software Lifecycle Management

!!! tip "Start with the operator runbook"
    For a linear, copyable procedure that distinguishes required manifests
    from optional audit probes, configures IOS-XE certificates, performs both
    upgrade and planned downgrade, and explains the provider logs, use the
    [IOS-XE gNOI Upgrade and Downgrade Runbook](gnoi-iosxe-upgrade-runbook.md).
    This page is the detailed design and API reference.

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
| Write-class actions | `IOSXEOperationalAction` | Certificate provisioning, reboot, process control, file writes/removal, and factory reset | `--enable-write-class-gnoi` or `CISCO_VK_ENABLE_WRITE_CLASS_GNOI` |
| Software lifecycle | `IOSXESoftwareUpgrade` | Install, activate, verify, and rollback IOS-XE software | `--enable-iosxesoftwareupgrade` or `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE` |

The write-class and software-upgrade gates are intentionally separate. Enabling
read-only gNOI does not enable reboot, file writes, factory reset, or OS
activation.
Enabling either mutation gate also changes the per-device worker Deployment to
`Recreate`, preventing overlap during managed Deployment rollouts. This is not
physical-device fencing: manual Pod deletion, duplicate device registrations,
or another controller installation still require operational coordination.
Expect a brief node-management interruption when that
worker rolls; `RollingUpdate` is restored after both gates are disabled (or
gNOI is globally disabled) and cleanup rollouts have removed both signer-bearing
and mutation-enabled worker generations. Disabling a gate therefore still uses
`Recreate` for the disabling rollout; it does not allow the old enabled worker
to overlap its replacement.

Helm exposes the same controls under the `gnoi` values block:

| Helm value | Environment rendered into VK pods | Effect |
|---|---|---|
| `gnoi.insecure` | `CISCO_VK_GNOI_INSECURE=1` | Force the legacy insecure IOS-XE gNXI listener for non-opt-in configurations. It cannot override explicit `transportSecurity: tls`; legacy Basic metadata may expose credentials. |
| `gnoi.port` | `CISCO_VK_GNOI_PORT=<port>` | Pin the gNOI listener port. Empty preserves legacy inference (`50052` insecure, `9339` secure, or an existing nonstandard device port). The chart accepts only `1`–`65535`; an invalid direct environment override fails gNOI setup. |
| `gnoi.disabled` | `CISCO_VK_GNOI_DISABLED=1` | Prevent the per-device gNOI client from being constructed. |
| `gnoi.enableSoftwareUpgrade` | `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1` | Enable `IOSXESoftwareUpgrade` reconciliation. |
| `gnoi.softwareUpgrade.maxImageBytes` | `CISCO_VK_UPGRADE_MAX_IMAGE_BYTES=<bytes>` | Set the hard per-image limit for URL and ConfigMap sources. The default is 8 GiB (`8589934592` bytes); the environment value must be a positive base-10 byte count. |
| `gnoi.enableWriteClass` | `CISCO_VK_ENABLE_WRITE_CLASS_GNOI=1` | Enable destructive/write-class `IOSXEOperationalAction` reconciliation. |

## Secure IOS-XE gNXI

IOS-XE 17.18.x password authentication uses the secure gNXI listener (port
`9339` by default). For an identity that is already provisioned, use:

```text
gnxi
gnxi enable-gnoi
gnxi secure-trustpoint <server-trustpoint>
gnxi secure-server
gnxi secure-password-auth
```

That example assumes an identity is already bound to the secure trustpoint. Do
not also run `secure-init` on that path. For the initial gNOI
Certificate-service bootstrap, use `gnxi`, `gnxi enable-gnoi`,
`gnxi secure-init`, and `gnxi secure-password-auth`; `secure-init` supplies the
temporary secure identity, so `secure-server` and a pre-existing
`secure-trustpoint` are not additional bootstrap prerequisites.
Without `enable-gnoi`, IOS-XE accepts `secure-init` syntactically but leaves the
Certificate Management service disabled. IOS-XE binds the first newly
installed certificate ID as the service trustpoint; verify the resulting
binding with `show gnxi state detail`.

Do not add `gnxi secure-client-auth` for password-only authentication. That
command asks IOS-XE to authenticate a client certificate and is appropriate
only when you have deliberately configured mutual TLS and mounted a client
certificate and key into the VK pod.

Select the listener independently of the RESTCONF port in the `CiscoDevice`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cat9000-1-gnoi-tls
  namespace: edge
type: Opaque
stringData:
  ca.crt: |
    -----BEGIN CERTIFICATE-----
    <CA that issued the certificate presented by secure gNXI>
    -----END CERTIFICATE-----
  # For deliberate mutual TLS, add both tls.crt and tls.key.
---
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9000-1
  namespace: edge
spec:
  driver: XE
  address: cat9000-1.example.net
  username: admin
  credentialSecretRef:
    name: cat9000-1-creds       # Secret contains only the key "password"
  gnoi:
    transportSecurity: tls
    port: 9339
    tls:
      secretRef:
        name: cat9000-1-gnoi-tls
  xe: {}
```

The Secret must be in the `CiscoDevice` namespace. `ca.crt` is required;
`tls.crt` and `tls.key` are an optional pair. The controller validates those
fixed keys, projects only them read-only, and restarts the worker after a valid
Secret change when gNOI is enabled in per-device topology. With global gNOI
disablement, the existing worker omits this projection and does not roll for
trust-Secret changes. Aggregated config-only topology has no per-device worker.
Host paths, `enabled`, and `insecureSkipVerify` are not valid under
`spec.gnoi.tls`.

A direct local configuration uses `device.gnoi.tls.caFile` and may set both
`certFile` and `keyFile`; `secretRef` is Kubernetes-only. In either mode the
TLS block requires `transportSecurity: tls`. Omit the dedicated block to use
the system root pool or verified shared TLS. If IOS-XE certificate provisioning
is configured, omit it because the provisioning Secret supplies isolated
bootstrap and steady-state gNOI trust automatically.

The explicit IOS-XE 17.18 secure-password-auth mode does **not** use an HTTP
Basic `Authorization` header. CVK derives per-RPC credentials without
duplicating the password in configuration or logs:

| gRPC metadata key | Value source |
|---|---|
| `username` | `CiscoDevice.spec.username` |
| `password` | The resolved device password (`credentialSecretRef` in controller mode; legacy inline/local sources remain supported for compatibility) |

Both keys are attached to unary and streaming gNOI RPCs only over TLS, matching
Cisco's [IOS-XE 17.18 gNOI authentication guidance](https://www.cisco.com/c/en/us/td/docs/ios-xml/ios/prog/configuration/1718/b-1718-programmability-cg/gnoi.html).
This behavior is enabled only by explicit `gnoi.transportSecurity: tls`, so
pre-existing gNOI configurations retain their former HTTP Basic metadata
contract. That legacy contract can expose credentials on a plaintext listener;
it is retained solely for compatibility and cannot be used for certificate
provisioning. Migrate those devices to explicit verified TLS.

### Provisioning the IOS-XE gNOI OS service

Password metadata authenticates the controller, but it does not move IOS-XE's
gNXI service into the `Provisioned` state required by `OS.Verify`, `OS.Install`,
and `OS.Activate`. `GNOIOSVerify` is always read-only: a not-provisioned result
never triggers certificate installation. Provisioning happens only when an
operator creates a write-class `IOSXEOperationalAction` with kind
`ProvisionCertificate`.

Provisioning uses IOS-XE's target-generated CSR flow, so the device private key
never leaves IOS-XE. Configure `spec.xe.gnoi.certificateProvisioning` with a
dedicated same-namespace Secret. `tls.crt` and `ca.crt` are always required.
The built-in `ProvisionCertificate` action is registered only while write-class
gNOI is enabled and a valid `ca.key` is mounted—even when `OS.Verify` would make
that particular action a no-op. `bootstrap.crt` is needed only when the
temporary certificate cannot pass normal CA and hostname validation. That
Secret is also the gNOI client's isolated trust source before and after
installation; do not also configure `spec.gnoi.tls`. This separation allows
unrelated shared transports to retain their own TLS policy without weakening
gNOI verification:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cat9000-1-gnoi-identity
  namespace: edge
type: Opaque
stringData:
  tls.crt: |
    -----BEGIN CERTIFICATE-----
    <CA-signed device certificate used as the CSR profile template>
    -----END CERTIFICATE-----
  ca.crt: |
    -----BEGIN CERTIFICATE-----
    <any independent gNXI/gNMI peer-trust CA that must be preserved>
    -----END CERTIFICATE-----
    -----BEGIN CERTIFICATE-----
    <device leaf issuer chain; intermediate first and root last>
    -----END CERTIFICATE-----
  # Optional at rest; required for a new ProvisionCertificate install.
  ca.key: |
    -----BEGIN RSA PRIVATE KEY-----
    <unencrypted key for the dedicated intermediate that issued tls.crt>
    -----END RSA PRIVATE KEY-----
  # Optional exact leaf pin for IOS-XE's temporary bootstrap identity.
  bootstrap.crt: |
    -----BEGIN CERTIFICATE-----
    <exact certificate currently presented by the secure gNXI listener>
    -----END CERTIFICATE-----
```

!!! danger "Do not copy the signer into a kubectl annotation"
    Do not use client-side `kubectl apply` for a Secret manifest containing
    `ca.key`. It stores the submitted manifest in the
    `kubectl.kubernetes.io/last-applied-configuration` annotation, where the
    key would survive a later `.data.ca.key` deletion. Use the approved secret
    manager, direct `kubectl create secret`, or server-side apply. The
    [key-free cleanup procedure](gnoi-iosxe-upgrade-runbook.md#remove-bootstrap-secrets-immediately)
    also removes a legacy last-applied annotation without printing its value.

`tls.crt` is a profile template, not the certificate installed on IOS-XE. It
must be a valid server certificate for `spec.address` and provide the CSR fields
IOS-XE 17.18.04 requires: C, ST, O, OU, and an IP SAN. CVK uses its CN when
present and otherwise uses `spec.address`. It must chain through a dedicated
intermediate CA rather than directly to a root. When present, the unencrypted
RSA `ca.key` must match that intermediate; root CA private keys are rejected.

The certificate workflow uses an explicit `CertificateSigner` boundary. CVK
validates the target-generated CSR before signing and validates the returned
certificate against the CSR and configured profile before sending it to IOS-XE.
The only signer included in this repository is the transitional local adapter
for `ca.key`. It can sign a new target CSR for a corrected immutable action
after a definitive failure before `Cert.Install`; the action controller still
submits each create-only network Install at most once. No external CA, KMS, or
HSM signer backend is implemented or configurable here.

Reference generic transport settings from `spec.gnoi` and IOS-XE provisioning
policy from `spec.xe.gnoi`:

```yaml
spec:
  address: cat9000-1.example.net
  gnoi:
    transportSecurity: tls
    port: 9339
  xe:
    gnoi:
      certificateProvisioning:
        certificateID: cvk-gnoi-os
        replaceTargetCABundle: true
        secretRef:
          name: cat9000-1-gnoi-identity
```

`spec.gnoi.tls` and `spec.xe.gnoi.certificateProvisioning` are mutually
exclusive trust sources. Admission and local/runtime validation reject both
together, or either one without explicit `transportSecurity: tls`.

CVK requires a target-generated RSA key of at least 2048 bits. Every certificate
in `ca.crt` must be a CA, the bundle must validate the template, and it must be
the **complete desired replacement** for IOS-XE's shared gNXI/gNMI trust bundle,
including peer CAs needed by existing mTLS clients. Order independent roots
first, then each issuer chain with its root last. Setting
`replaceTargetCABundle: true` acknowledges this replacement.

`bootstrap.crt` is an exact, validity-checked leaf pin, not a trust anchor. CVK
first attempts normal CA and hostname verification, falls back to the pin only
before the new CA-valid identity has been observed, and never adds the pinned
certificate to a CA pool. Omit it when the certificate already validates
normally.

!!! danger "Protect the signing key"
    Use a dedicated, narrowly scoped intermediate CA. Enable Kubernetes Secret
    encryption at rest, restrict Secret reads and pod exec, and audit both. The
    signing key and bootstrap pin are projected read-only only while
    write-class gNOI is enabled and a non-empty `ca.key` is present, but
    projection is not an authorization boundary: the standard VK
    ClusterRole can read Secrets cluster-wide to serve scheduled pod volumes.
    Any principal that can use the VK service account, read the Secret, or enter
    the signer-bearing pod can use the key. The built-in local signer retains
    the parsed key for the lifetime of that process so a definitive pre-Install
    failure can be corrected with a new immutable action. Key bytes are not
    copied into ConfigMaps, environment, status, events, or logs. Remove
    `ca.key` promptly after provisioning; a key-free rollout is required to
    remove it from process memory. The Deployment uses a non-overlapping
    `Recreate` strategy while a signer can be resident, for that cleanup
    rollout, and whenever write-class gNOI actions or IOS-XE software upgrades
    are enabled. This prevents overlapping generations during managed
    Deployment rollouts; it does not fence manual Pod replacement or a second
    registration of the physical device. Normal rolling updates resume only after signer
    cleanup is complete, both mutation gates or gNOI globally are disabled,
    and no old mutation-enabled worker generation remains.

!!! warning "IOS-XE gNXI service restart"
    CVK confines its configuration and credentials to gNOI, but IOS-XE shares
    the provisioned identity and CA bundle with gNMI. gNOI Install replaces
    that complete target-side CA bundle, can restart gNXI, and changes the
    certificate presented to external gNMI clients. Put every peer CA that
    must remain trusted in `ca.crt`, ensure clients trust the new server CA,
    and schedule provisioning as a device-management change. RESTCONF,
    NETCONF, and the VK's other TLS settings are not modified by CVK.

Use this explicit workflow:

!!! danger "Complete mixed-version rollouts first"
    If upgrading from a preview build that accepted `ProvisionCertificate`
    without an intent block, complete or delete those legacy actions, apply the
    updated CRD, and fully roll the manager and every per-device worker before
    creating an intent-bound action. Do not provision during a mixed-version
    window: an older worker cannot enforce the certificate ID and digest.

1. Enable `gnxi enable-gnoi` and `gnxi secure-init`, configure the Secret and
   `spec.xe.gnoi.certificateProvisioning` block, and enable the write-class
   gNOI runtime gate.
2. Run `GNOICertGet` to test TLS and password metadata. Run `GNOIOSVerify` if
   desired to confirm the not-provisioned response; it does not mutate IOS-XE.
3. Compute the immutable public-material digest from the exact files used to
   populate the Secret. The order has no separator: `tls.crt` bytes first,
   immediately followed by `ca.crt` bytes.

    ```bash
    (cat tls.crt; cat ca.crt) | sha256sum
    ```

   Create the action below with that lowercase digest. The worker compares both
   fields with its loaded bundle before it acquires a gNOI client or marks the
   action Running, so a Secret/config rollout race is rejected without touching
   the device. Installation proceeds only when `OS.Verify` returns the exact
   not-provisioned error. If `OS.Verify` already succeeds, the action returns
   `serviceAlreadyProvisioned` with `certificateChanged: false` and performs no
   certificate inventory or Install call. Its requested ID and digest are only
   an echo of operator intent, not proof of the active device identity. A
   conflicting certificate ID observed during an install attempt fails closed.

    ```yaml
    apiVersion: ops.cisco.vk/v1alpha1
    kind: IOSXEOperationalAction
    metadata:
      name: provision-cat9000-1-gnoi
      namespace: edge
    spec:
      deviceRef:
        name: cat9000-1
      confirm: cat9000-1
      action:
        kind: ProvisionCertificate
        provisionCertificate:
          certificateID: cvk-gnoi-os
          # Replace with the digest computed above.
          publicMaterialSHA256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    ```

4. A mutating action reports `provisioned` with `certificateChanged: true` only
   after a fresh connection proves that the exact installed certificate is its
   active TLS leaf and a subsequent `OS.Verify` succeeds over that same peer;
   the result includes the certificate ID, public-material digest, and running
   IOS-XE version. If `OS.Verify` worked before mutation, the no-op result is
   `serviceAlreadyProvisioned` with `certificateChanged: false`; its requested
   fields remain intent only. CVK retries post-install read-only checks during
   the expected gNXI restart but never retries the create-only Install.
   Also require `show gnxi state detail` to report `State: Provisioned`. If the
   action fails with `CertificateInstallIndeterminate`, do not immediately
   create another action: use `GNOICertGet` and inspect device PKI state to
   determine whether the certificate ID was committed.
5. Follow the [key-free cleanup
   procedure](gnoi-iosxe-upgrade-runbook.md#remove-bootstrap-secrets-immediately)
   to remove `ca.key`, `bootstrap.crt`, and any client-side last-applied copy;
   wait until the Deployment template records the new Secret resourceVersion
   before waiting for the non-overlapping rollout. `Recreate` remains in
   effect while write-class actions or software upgrades are enabled, so
   disable each mutation gate when it is no longer needed (or disable gNOI
   globally) to restore normal rolling updates. Keep `tls.crt`, `ca.crt`, and
   the provisioning block so read-only gNOI can validate the installed
   identity.

Changing a generic `spec.gnoi.tls` Secret first validates the new material and,
when gNOI is enabled in per-device topology, rolls the worker so new connections
use it; it never changes device state. A provisioning Secret update also rolls
that worker, but it does not rotate an installed IOS-XE identity. With global
gNOI disablement, the per-device worker remains but ignores both trust Secrets;
aggregated config-only topology has no per-device worker. Install is
create-only: CVK does not rotate, revoke, or overwrite an existing certificate
ID.
Controller-managed provisioning requires the per-device worker; the aggregated
config-only topology does not run gNOI lifecycle reconcilers.

### Invalid Secret recovery and certificate lifetime

A missing, malformed, or expired referenced gNOI Secret sets the affected
`CiscoDevice` condition `GNOIConfigurationReady=False`. The manager reconciles
that worker with gNOI disabled and removes its trust/signer projections; a
required key-cleanup rollout still uses `Recreate`. Invalid public certificate
material therefore cannot prevent removal of a previously loaded signing key.
Unrelated credential/configuration reconciliation continues. Repairing the
Secret restores gNOI through a worker rollout. A transient Kubernetes API read
failure is different: it retries without treating unknown Secret state as a
confirmed invalid configuration. This condition validates local configuration,
not the device's gNOI reachability or provisioning state.

Certificate renewal remains an operator-owned PKI operation. `ProvisionCertificate`
is create-only, not a renewal controller; changing the profile `tls.crt` does
not rotate the installed device certificate. Assign a renewal owner and a
maintenance/recovery procedure before production use. `GNOICertGet` exposes
parseable X.509 `NotBefore`, `NotAfter`, and `FingerprintSHA256` inventory fields
and updates [certificate freshness/expiry metrics](observability.md#gnoi-lifecycle-metrics).
Run a fresh inventory probe periodically and before each change. Inventory can
include inactive certificates; verify which certificate is actually bound to
the secure gNXI trustpoint before renewal or deletion. An external signing
service can implement the signer boundary in a future change; there is no
configured external-signer or automatic rotation implementation today.

## Connection Model

The IOS-XE `gnoiruntime.Provider` owns a dedicated per-device,
workload-classed gRPC pool for gNOI; command wiring supplies configuration and
credentials but does not own the runtime workflow. Current production wiring
uses these classes:

| Class | Used by | Why it is separate |
|---|---|---|
| `ClassControl` | Unary and short-stream gNOI RPCs such as time, ping, reboot status, cert get, and OS verify | Keeps small control RPCs responsive. |
| `ClassBulkTransfer` | OS install and file put/get | Prevents large file transfers from back-pressuring control traffic. |

gNMI configuration and telemetry currently dial independently. Although the
pool defines `ClassTelemetry` and the transports accept injected connections,
production wiring does not compose them with the gNOI pool and callers must
not assume shared transport or authentication state.

The gNOI client validates IOS-XE filesystem prefixes for file paths and caches
per-service capability probes. gNMI capabilities do not enumerate gNOI
services, so CVK learns support by observing gNOI responses. A
`codes.Unimplemented` response marks that service unsupported in the in-process
cache and later calls fail fast with `ErrServiceUnsupported` until the cache
expires or the process restarts.

Certificate installation is not a capability of that base provider. A separate
`gnoiruntime.Provisioner` is made available to the operational-action
reconciler only when the provisioning configuration is present and write-class
gNOI is enabled. Even then, it runs only for an explicitly confirmed,
create-only `ProvisionCertificate` action after `OS.Verify` returns the exact
not-provisioned response.

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
`FactoryReset`, plus the certificate bootstrap action `ProvisionCertificate`.

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

### Shared device-mutation coordination

Write-class actions and software upgrades contend for one platform-neutral
`device-disruptive-mutation` Kubernetes Lease per namespaced `CiscoDevice`.
This serialization boundary lives above the IOS-XE backend so future NX-OS and
IOS XR mutation workflows can join it without importing IOS-XE code. The Lease
is stored in `CONFIG_LEASE_NAMESPACE` when configured, otherwise in the worker
namespace, and has a fixed 26-hour safety TTL in this release.

Definitively completed mutations release the Lease. Every action that fails
after invocation retains it until expiry because an error cannot prove that the
device rejected the request; uncertain upgrade terminals do the same.
Successful `Reboot` and `FactoryReset` also retain it because their
acknowledgements do not prove convergence. A delayed reboot extends the hold
through its delay plus the fixed safety TTL. `CancelReboot` is the only bypass
and never releases another operation's Lease. Deleting an invoked action
keeps its finalizer through the bounded RPC-and-persistence window. If the
outcome remains unknown, CVK records `ActionOutcomeUnknown`, removes the
finalizer so deletion can complete, and retains the Lease until expiry.
Deleting an upgrade cannot cancel device work and therefore leaves any
required quarantine in place. Manual or external device changes are outside
this Kubernetes fence and must still be coordinated operationally.

The identity is the **namespace and CiscoDevice name**, not its IP address.
Register each physical device once and coordinate ownership across clusters;
different names or clusters do not share a fence.

Per-device IOS-XE workers also coordinate ordinary configuration writes and
app-hosting mutations with that Lease. Read-only probes and report-only config
remain available. Before dispatching disruptive gNOI work, CVK adds the owned
`cisco.vk/device-maintenance=gnoi:NoSchedule` Node taint. It prevents new
scheduler placement, does not evict existing Pods, and is removed only after
the disruptive Lease is released or expires. Direct `nodeName` assignments do
not bypass the device-write guard. Disabling mutation gates does not discard
an existing quarantine. Operator-owned taints are preserved.
Taint removal is observed on a 30-second polling interval; setting it before
dispatch is synchronous. The compatibility scan lists both mutation kinds in
the device namespace, so large fleets should measure Kubernetes API cost and
use appropriately scoped namespaces rather than assuming constant fleet-wide
overhead.

The safety observer needs namespace-scoped `list` permission on both mutation
CRDs even when their runtime gates are off; the strict-RBAC chart grants that
read-only access without enabling their controllers or write/status verbs.

Ordinary write transactions renew a shorter Lease while running, with a
30-minute execution bound. An error, cancellation, or lost worker retains the
remaining 31-minute Lease instead of assuming device work stopped. This is a
bounded recovery assumption, not proof that asynchronous IOS-XE work has
finished. Investigate device-side install/app-hosting state before resuming
after expiry. A Kubernetes/API outage fails closed for coordinated writes.

## Software Lifecycle

`IOSXESoftwareUpgrade` manages the device OS lifecycle as an auditable
Kubernetes object. Operators provide exactly one image source:

!!! warning "Per-device topology required"
    `IOSXESoftwareUpgrade` and `IOSXEOperationalAction` are reconciled inside
    the IOS-XE per-device worker. They are unavailable when
    `aggregator.enabled=true`; set `aggregator.enabled=false` before enabling
    either mutation gate.

| Source | Required fields | Use case |
|---|---|---|
| URL | `imageSource.url` and `imageSource.sha256` | CVK fetches and verifies `http`, `https`, `tftp`, `ftp`, `scp`, or `sftp` content, then streams the bytes with gNOI `OS.Install`. |
| URL with credentials | URL fields plus `imageSource.urlSecretRef` | Same byte-stream flow for authenticated FTP/SCP/SFTP. The explicitly opted-in Secret must authorize the URL's exact scheme, host, and port. SCP/SFTP also require verified host keys unless the separately gated lab bypass is enabled. |
| ConfigMap | `imageSource.configMapRef` | CVK hashes `binaryData["image"]`, then streams it with gNOI `OS.Install`. Kubernetes size limits make this suitable only for small test artifacts. |
| Preinstalled | `imageSource.preinstalled: {}` | Activate `targetVersion` only when it resolves to one exact, activatable entry in the native install inventory. No bytes are transferred or registered. |
| Device file | `imageSource.deviceFile.path` and `.sha256` | Verify a file already on IOS-XE with gNOI `File.Get`, register it through the IOS-XE RESTCONF install RPC, then activate the exact resulting inventory version through gNOI. |
| Legacy local path | `imageSource.localPath` and optional `localPathSHA256` | Deprecated compatibility form of `preinstalled`. The path is never submitted to an install command; use `deviceFile` to register a file. |

URL and ConfigMap content is handled by CVK; IOS-XE is not asked to fetch the
URL. CVK persists the algorithm-qualified digest, and the resolved size when
known, before starting `OS.Install`. A later reconciliation fails if resolution
produces different content rather than silently changing bytes for the same CR.

FTP, SCP, and SFTP transfer credentials are accepted only from an
endpoint-bound Secret. URL userinfo such as
`sftp://user:password@host/image.bin` is rejected. Query strings and fragments
remain part of the immutable URL stored in the upgrade CR, although CVK redacts
them from its own errors; do not put credentials or bearer tokens there. The
Secret owner must opt in with
`cisco.vk/purpose=software-image-source` and provide all three endpoint fields;
scheme and DNS host comparisons are case-insensitive, a trailing DNS dot is
ignored, IP addresses and ports are normalized, and an omitted URL port uses
the protocol default (`21` for FTP and `22` for SCP/SFTP):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: iosxe-image-source
  labels:
    cisco.vk/purpose: software-image-source
type: Opaque
stringData:
  allowedScheme: sftp
  allowedHost: images.example.net
  allowedPort: "22"
  username: image-reader
  password: replace-me
  # For key authentication, use privateKey and optional passphrase instead.
  # SCP/SFTP also require knownHosts (or known_hosts).
```

An upgrade author cannot use that Secret with a different scheme, host, or
port. Treat permission to create or modify a purpose-labelled image-source
Secret as credential-administration authority; keep it separate from ordinary
upgrade-CR creation RBAC.

`preinstalled` and deprecated `localPath` are inventory-only intents. A file
merely appearing in `dir flash:` is not an installed version and will not be
activated. If `localPathSHA256` is supplied, CVK verifies the file before it
inspects inventory, but the hash does not opt the request into registration.

`deviceFile` registration is currently an IOS-XE-only driver capability and
requires the RESTCONF transport. Paths are limited to validated `flash:`,
`bootflash:`, or `harddisk:` locations. CVK verifies the required SHA-256
digest before invoking IOS-XE's install-by-path RPC with install-only behavior;
activation remains in the generic gNOI lifecycle. There is no CLI or SSH
fallback. Unsupported transports, an unavailable lifecycle backend, ambiguous
version matches, source-path conflicts, and non-activatable inventory states
fail closed before activation. An activatable or in-progress target that exists
before this CR records its staging request is also rejected as uncorrelated.
After the marker is recorded, only the matching operation ID returned by the
correlated device-file observer can authorize activation; a generic inventory
match is not sufficient provenance.

### Image resolver storage and transport security

URL and ConfigMap sources have a hard per-image limit of 8 GiB by default.
Set `gnoi.softwareUpgrade.maxImageBytes` in Helm, or set
`CISCO_VK_UPGRADE_MAX_IMAGE_BYTES` directly to a positive base-10 byte count,
to choose a lower or higher limit. Helm puts the value on the manager and the
controller propagates it to per-device VK pods. The aggregated config-only
topology does not run the lifecycle resolver. Invalid values fail resolution;
a declared HTTP size, remote file size, SCP header, or live byte stream that
exceeds the limit is rejected before the current cache file can grow beyond
that limit.

URL downloads are streamed and SHA-256 verified into a mode-`0600` temporary
file inside `/tmp/cvk-upgrade-cache`, then atomically promoted to their
digest-keyed cache path. The verified file descriptor is reused for gNOI
transfer, so first resolution does not make a second full-image copy. Before
resolving a different digest, CVK removes the prior managed cache entry and any
orphaned CVK temporary files left by a crashed resolver; unrelated directory
entries are preserved. At most one completed image digest is retained per
resolver directory. A pod restart clears the `emptyDir`. Budget slightly more
than the largest permitted image plus normal pod overhead, and size node
ephemeral storage and any `ephemeral-storage` requests/limits accordingly—a
kubelet eviction or full filesystem interrupts the transfer even when the image
itself is below the configured maximum.

The resolver has a four-hour cap per materialization attempt, while the
lifecycle bounds all attempts by the remaining `installTimeoutSeconds`; with
defaults, the effective overall limit is therefore one hour. Transient HTTP,
Kubernetes API, and supported file-transfer connection failures are retried
only inside that overall pre-install deadline. Invalid credentials, endpoint
authorization, host-key or TLS verification, source validation, size, digest,
and unsafe-cache failures remain terminal. HTTP requests inherit the attempt
deadline; FTP data and control connections receive deadlines and are closed on
cancellation; SCP and SFTP sessions are closed on cancellation; and TFTP
additionally retains its bounded transport retry policy. A failed attempt
removes its partial temporary file and does not create a cache entry.

The cache directory is forced to mode `0700`; cache and temporary image files
are mode `0600`. Symlinks and other non-regular cache entries are rejected, and
CVK streams from the same open file descriptor it verified rather than
reopening the pathname. These controls protect against accidental cross-user
access and pathname substitution inside the pod; they do not protect against a
principal with pod exec, node-root, or equivalent access.

Choose the URL transport separately from content integrity. SHA-256 detects
content changes but does not provide confidentiality, endpoint authentication,
or credential protection. `http`, `ftp`, and `tftp` are plaintext (`ftp` also
exposes its password on the network); reserve them for isolated, trusted
management networks. Prefer `https`, `sftp`, or `scp` in production. SFTP and
SCP require a trusted `knownHosts` entry unless both the URL carries
`?insecureSkipHostKey=true` (or `?insecure=true`) and a custom/local deployment
sets `CISCO_VK_UPGRADE_ALLOW_INSECURE_SSH=true`. The stock Helm chart does not
expose this lab-only bypass. The default HTTP client follows redirects, so
control and audit redirect targets as part of repository policy.

The upgrade strategy controls activation:

| Strategy | Behavior |
|---|---|
| `Reload` | Default. Calls gNOI `OS.Activate` with reboot allowed, then waits for the device to return and verifies the running version. |
| `ISSU` | Currently rejected during preflight. CVK will not advertise a non-disruptive upgrade until the platform backend can verify that IOS-XE actually selected an ISSU path. |
| `NoReboot` | Calls gNOI `OS.Activate` with `NoReboot=true`, then performs `OS.Verify`. If the old version remains active, the terminal phase is `StagedForNextBoot`; only an independently authorized reboot can complete the transition. It does not prove ISSU, hitless forwarding, or readiness after that reboot. |

The normal lifecycle is:

| Phase | Meaning |
|---|---|
| `Pending` | CR accepted; no device operation started yet. The maintenance window gates initial work. |
| `Resolving` | gNOI preflight runs and source intent or exact native inventory is resolved. |
| `Staging` | An IOS-XE device file is submitted once for native install-inventory registration. |
| `Transferring` | URL or ConfigMap content is materialized, digest-pinned, and streamed with gNOI `OS.Install`. |
| `TransferInterrupted` | A dispatched gNOI install has an indeterminate outcome and is observed without replay until its deadline. |
| `Validating` | gNOI install or native registration is observed until an exact inventory version is activatable. |
| `Activating` | gNOI OS activation is requested. |
| `AwaitingReachability` | Device may be rebooting after activation. |
| `Verifying` | Running version and activation result are verified. |
| `RollingBack` | Previous version is being re-activated after a verify mismatch. |
| `Succeeded` | Final `OS.Verify` proves the requested version is running (including the unusual case where a `NoReboot` request already resulted in the target). |
| `StagedForNextBoot` | `NoReboot` was accepted, `OS.Verify` still reports the captured previous version, and a separately authorized reboot is required. |

Terminal failure phases include `Failed`, `PreflightFailed`,
`ValidationFailed`, `RolledBack`, and `RebootTimeout`.

`OS.Activate` reboots the device when the chosen strategy requires it; CVK does
not issue a separate `System.Reboot` after activation. With rollback enabled,
CVK re-activates the previously observed running version only after the
lifecycle backend proves that exact version remains activatable.

When `OS.Verify` requires individual-supervisor handling, byte-stream sources
install the active supervisor first and the standby second, using the same
install deadline. The standby must return exactly the same validated version as
the primary, not merely a value matching the requested prefix. Activation is
deliberately reversed: standby first, then the active supervisor, with
verification between steps. `NoReboot` is rejected for this path because CVK
cannot prove the required boot sequence. Automatic rollback is also refused if
a safe, separately verified per-supervisor rollback sequence cannot be
established. The durable per-supervisor request and completion markers prevent
replay after a controller restart.

The reconciler records staging, activation, and rollback intent before issuing
each device-mutating RPC and does not replay a recorded request after a restart
or an ambiguous transport result. This is intentionally at-most-once, not
exactly-once: a controller failure after the status write but before device
receipt can leave the outcome unknown and require operator inspection. In
particular, an ambiguous `NoReboot` response is terminal rather than retried.

The controller persists `status.executionModel: AtMostOnceV1` before a new
upgrade leaves its safe initial state. During an upgrade from an earlier
controller, an unmarked `Pending` or `Resolving` object is adopted because no
device mutation could yet have been submitted. An unmarked `Staging`,
`Transferring`, `TransferInterrupted`, `Validating`, `Activating`,
`AwaitingReachability`, `Verifying`, or `RollingBack` object instead terminates
as `ValidationFailed` with reason `LegacyStateOutcomeUnknown`; CVK retains the
shared mutation Lease and never guesses whether it should replay the request.
Inspect IOS XE and wait for the quarantine Lease to expire before authorizing a
replacement operation. The deprecated `spec.resumePolicy`, `spec.maxRetries`,
and `status.retryCount` fields remain accepted for stored objects and older
manifests. Admission still supplies the historical `Retry` and `3` defaults so
objects remain usable after a controller rollback, but the current reconciler
ignores all three fields; they do not enable mutation retries. The released
`Cancelled` phase is retained as a terminal compatibility value.
An unknown non-empty execution-model value is treated as state from a newer
controller: CVK preserves its status verbatim, installs the cleanup finalizer,
holds the shared Lease, and performs no device RPC until a compatible
controller is restored.
Future controllers that add a phase capable of device mutation must write a
new execution-model value with it; this pairing lets an older controller
recognize and fence the entire newer state without interpreting its phase.

For this one-release migration, every mutation controller scans both
`IOSXESoftwareUpgrade` and `IOSXEOperationalAction` in the device namespace
before claiming the shared Lease. All replicas select the same deterministic
legacy holder, so a released in-flight upgrade or action is fenced even when
its own controller gate is disabled; an API-list or RBAC error fails closed.
The guard also recognizes released invoked actions for a bounded 26-hour
outcome window, extended by any accepted reboot delay. This IOS-XE legacy
horizon is owned by the compatibility adapter and is not shortened by another
driver's normal Lease policy.

Important defaults:

| Field | Default | Notes |
|---|---:|---|
| `strategy` | `Reload` | `NoReboot` requests activation without an immediate reload; it is not a non-disruptive guarantee. |
| `rollbackOnFailure` | `true` | Attempts to restore the previously observed version after verify mismatch when a safe rollback sequence can be proven. |
| `installTimeoutSeconds` | `3600` | Bounds two consecutive windows: pre-install gNOI readiness, source resolution, and device-file `File.Get`; then, starting at the first `OS.Install` or native-registration claim, all per-supervisor installs/registration and inventory convergence share a fresh window. Repeated work within either window receives only its remaining time. |
| `rebootTimeoutSeconds` | `1800` | Separately bounds initial activation-control readiness (`activationControlStartTime`), then activation/reachability/final verification from the first actual activation claim. Rollback receives an independent timer when `RollingBack` begins, including pre-dispatch reachability. Each RPC uses only the remaining sequence time. |

`ActivationControlTimeout` and `RollbackDidNotConverge` retain the shared Lease
when a mutation was durably claimed, just like an uncertain install outcome.
This quarantine prevents a new CVK mutation from overlapping device work whose
completion cannot be proven.

After a durable activation claim, even a definitive authentication or
authorization error from a later `OS.Verify` cannot prove that activation
stopped. Terminal outcomes release the fence only when device mutation is
known settled; `DeviceMutationSettled=True` records that evidence. Never infer
that a terminal `Failed` phase alone means another mutation is safe. Before
any activation claim, an expired maintenance window is checked even if the
gNOI control client remains unavailable.

`targetVersion` accepts IOS-XE version shapes such as `17.15.01a`,
`26.01.01`, `26.01.01.0.340`, and `17.18.02.0.4112.1766116039`. Verification
uses a prefix-aware comparison, so operators may use the shortest unambiguous
form for the staged image.

## Upgrade Examples

For the complete upgrade and planned-downgrade manifests, the certificate
prerequisites, and commands that correlate CR status with worker logs, follow
the [IOS-XE upgrade and downgrade runbook](gnoi-iosxe-upgrade-runbook.md).

Each CR is immutable and starts one upgrade after preflight. `notBefore` delays
initial work; `notAfter` is rechecked before every not-yet-claimed device
mutation, but cannot cancel a submitted or in-flight RPC. The shared mutation
Lease allows only one CVK-managed write action or upgrade to own the device at
a time; a quarantined prior outcome can intentionally block a new request until
the Lease expires.

Stream verified URL content through gNOI:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: cat9000-4-to-26-01-01
spec:
  deviceRef:
    name: cat9000-4
  imageSource:
    url: https://images.example.net/cat9k_iosxe.26.01.01.SPA.bin
    sha256: 7de3c6875e3c1c96d5920e8542c72b1bcb5d913d99645ef5687f44dd4024cdf4
  targetVersion: 26.01.01
  strategy: Reload
  rollbackOnFailure: true
  installTimeoutSeconds: 3600
  rebootTimeoutSeconds: 3600
```

Activate an exact version already in install inventory:

```yaml
spec:
  deviceRef:
    name: cat9000-4
  imageSource:
    preinstalled: {}
  targetVersion: 26.01.01
  strategy: Reload
```

Register a verified IOS-XE device file, then activate it:

```yaml
spec:
  deviceRef:
    name: cat9000-4
  imageSource:
    deviceFile:
      path: flash:cat9k_iosxe.26.01.01.SPA.bin
      sha256: 7de3c6875e3c1c96d5920e8542c72b1bcb5d913d99645ef5687f44dd4024cdf4
  targetVersion: 26.01.01
  strategy: Reload
  installTimeoutSeconds: 3600
```

Monitor status and events with:

```bash
kubectl get iosxesoftwareupgrade -A -w
kubectl describe iosxesoftwareupgrade <name> -n <namespace>
```

Before treating `NoReboot` as part of a maintenance procedure, independently
validate the platform-specific boot state and recovery plan. CVK does not claim
that this option is a non-disruptive upgrade.

## Operator Workflow

1. Follow the [IOS-XE upgrade and downgrade
   runbook](gnoi-iosxe-upgrade-runbook.md) for the device's distinct bootstrap
   or already-provisioned secure-gNXI path. Require `show gnxi state detail` to
   report `State: Provisioned` before software lifecycle work. Use the
   plaintext listener only for explicit legacy lab compatibility.
2. Enable the software upgrade gate on the per-device VK pod via Helm
   (`gnoi.enableSoftwareUpgrade: true`) or the env var
   `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=1`. Confirm that
   `gnoi.softwareUpgrade.maxImageBytes` and pod/node ephemeral-storage capacity
   accommodate one cached image plus normal overhead before creating an upgrade.
3. Grant RBAC: upgrade operators need `create`/`get`/`watch` on
   `IOSXESoftwareUpgrade`. Read-only users get `DeviceOperation` only.
4. Compute the image sha256 and prepare the manifest (see examples above).
5. Apply the manifest and monitor with `kubectl get iosxesoftwareupgrade -w`.
6. After `Succeeded` or `StagedForNextBoot`, retain the CR as an immutable audit
   record or delete it when it is no longer needed. For `StagedForNextBoot`,
   authorize and observe the required reboot separately; the upgrade CR does
   not claim that the target is running.

For `DeviceOperation` show-command and diagnostic examples see the
[Operations Runbook](operations.md).
