# IOS-XE gNOI Upgrade and Downgrade Runbook

!!! warning "Beta and disruptive"
    `IOSXESoftwareUpgrade` is a Beta (`v1alpha1`) API. A `Reload` activation
    reboots the switch. Validate the exact image and recovery procedure in a
    lab, use an approved maintenance window, keep console or out-of-band access
    available, and upgrade one device at a time.

This is the complete operator path for upgrading and then downgrading an
IOS-XE device through CVK. The detailed API, security, and failure semantics
remain in [gNOI and Software Lifecycle](gnoi-software-lifecycle.md).

## The mental model

An upgrade and a planned downgrade are the same operation in CVK:

1. The per-device CVK worker downloads and SHA-256 verifies the selected image.
2. CVK streams those bytes to IOS-XE with gNOI `OS.Install`.
3. CVK asks IOS-XE to activate the exact version returned by Install.
4. IOS-XE reloads, CVK waits for it to return, and gNOI `OS.Verify` proves the
   running version.

The switch does **not** download an `imageSource.url`; the CVK worker does. The
URL must therefore be reachable from the worker's network, and the worker needs
enough ephemeral storage for the image.

A planned downgrade is **not** `rollbackOnFailure`. It is a new,
operator-authorized `IOSXESoftwareUpgrade` object with a unique name, the older
target version, the older image URL, and that image's digest. Automatic rollback
is failure recovery inside the original object.

Each lifecycle object targets one `CiscoDevice`. Three switches upgraded and
then downgraded therefore require six lifecycle objects, applied serially if
that is the maintenance policy.

## Which objects do I actually need?

| Object or data | When needed | Purpose | Keep afterward? |
|---|---|---|---|
| Device credential `Secret` | Once | Supplies the `password` for the username in `CiscoDevice.spec.username` | Yes |
| IOS-XE provisioning identity `Secret` | Once when CVK manages certificate provisioning | Supplies the public `tls.crt` profile and `ca.crt` trust bundle | Yes |
| Dedicated gNOI trust `Secret` | Alternative for an independently provisioned identity | Supplies `ca.crt`; a client `tls.crt`/`tls.key` pair is optional only for deliberate mTLS | Yes |
| `CiscoDevice` gNOI configuration | Once | Selects TLS, port `9339`, password metadata, and one verified trust source | Yes |
| `ca.key` and optional `bootstrap.crt` | Initial certificate provisioning only | Signs the device-generated CSR and optionally pins the temporary identity | **No**; remove immediately after verification |
| `ProvisionCertificate` action | Only when `show gnxi state detail` is not `Provisioned` | Moves the IOS-XE gNOI OS service into the provisioned state | Retain as audit evidence or delete later |
| `IOSXESoftwareUpgrade` | Once per device and direction | Performs one immutable upgrade or planned downgrade | Retain as audit evidence or delete later |
| `DeviceOperation` probes | Optional | Records extra pre/post `GNOICertGet`, `GNOIOSVerify`, or show-command evidence | Optional |

The many dated preflight, post-upgrade, pre-downgrade, and post-downgrade
manifests used in a lab report are optional evidence checkpoints. They are not
all prerequisites. After one-time device and certificate setup, the minimal
version-change input is one lifecycle manifest per device.

## Example names used below

Replace every example value before applying a manifest:

| Value | Example |
|---|---|
| Kubernetes namespace | `edge` |
| `CiscoDevice` name | `cat9000-1` |
| IOS-XE address | `192.0.2.10` |
| Current/older version | `17.18.02` |
| Newer version | `17.18.03` |
| CVK worker Deployment | `cat9000-1-vk` |

The image digests below are placeholders and deliberately cannot be used as-is.

## 1. Check the deployment and recovery prerequisites

The write-class and software lifecycle controllers run only in an IOS-XE
per-device worker. Confirm that aggregated config-only topology is disabled:

```yaml
aggregator:
  enabled: false
```

Before changing a switch, also confirm:

- the current CVK CRDs are installed and the manager and per-device worker have
  completed their rollouts;
- the existing AAA policy permits the account in `spec.username` to perform the
  required gNOI OS and Certificate RPCs;
- TCP port `9339` is reachable from the worker to the switch;
- device, worker-host and Kubernetes control-plane clocks are synchronized
  for certificate validity, maintenance windows and correlated evidence;
- the image is correct for the platform and the worker has enough ephemeral
  storage for it;
- the switch has enough storage and a healthy install state; and
- console or out-of-band recovery access is working.

Do not add blanket AAA commands from this runbook. Integrate the CVK account
with the site's existing AAA policy. With explicit gNOI TLS, CVK sends the
lowercase gRPC metadata keys `username` and `password`; it does not send an HTTP
Basic `Authorization` header.

### Size the worker, not the virtual Node

Set worker resources explicitly for large image transfers. These settings
control the real Kubernetes worker Pod, not the capacity advertised by the
virtual Node or resources assigned to hosted apps. For a roughly 1.3 GiB image,
this is a starting example, not a universal platform sizing guarantee:

```yaml
# Merge this block into the existing CiscoDevice spec.
spec:
  worker:
    resources:
      requests:
        cpu: 100m
        memory: 256Mi
        ephemeral-storage: 3Gi
      limits:
        cpu: "2"
        memory: 1Gi
        ephemeral-storage: 4Gi
    tmpSizeLimit: 3Gi
```

Release-wide Helm defaults use the same `worker.resources` and
`worker.tmpSizeLimit` keys. A device's `resources` block replaces the whole
default resources block; explicit `{}` clears inherited requests/limits.
`tmpSizeLimit` inherits independently when omitted. Unset settings preserve
legacy behavior. Requests/limits support only CPU, memory, and
`ephemeral-storage`; requests cannot exceed their corresponding limits.

The disk-backed `/tmp` cap is an eviction limit, not reserved capacity.
Leave room for the image cache, logs, and writable-layer overhead, and verify
capacity on the hosting Ubuntu/Kubernetes Node. Keep the per-image
`gnoi.softwareUpgrade.maxImageBytes` limit consistent with this budget. Do not
make a worker rollout or resource change during an unresolved device mutation.

## 2. Configure secure gNXI on IOS-XE

There are two valid starting states. Do not combine the two command sets.

### A. The device is not provisioned yet

Use the temporary self-signed bootstrap identity so CVK can call the gNOI
Certificate service:

```text
configure terminal
gnxi
gnxi enable-gnoi
gnxi secure-init
gnxi secure-password-auth
gnxi secure-port 9339
end
write memory
```

`gnxi enable-gnoi` is essential: without it, IOS-XE can accept `secure-init`
while leaving the Certificate Management service disabled. `secure-init`
starts the secure listener with a temporary identity; the one-time workflow in
the next sections replaces it with a CA-issued identity. Do not preconfigure
`secure-trustpoint` or add `secure-server` on this bootstrap path; IOS-XE binds
the first installed certificate ID as the service trustpoint.

That temporary identity is normally self-signed, so `bootstrap.crt` is
effectively required unless it already passes normal CA and hostname
verification. Obtain the exact leaf from the listener, then compare its
SHA-256 fingerprint with trusted device-side PKI output over console or another
out-of-band channel before accepting it as a pin. For example, save the first
leaf presented by the listener and print its fingerprint:

```bash
openssl s_client -connect 192.0.2.10:9339 -showcerts </dev/null 2>/dev/null | \
  openssl x509 -outform PEM -out bootstrap.crt
openssl x509 -in bootstrap.crt -noout -sha256 -fingerprint
```

Do not trust a certificate merely because it was returned by the same
unauthenticated network path that the pin is intended to protect.

### B. The device already has the intended provisioned identity

Bind that trustpoint and enable the secure server:

```text
configure terminal
gnxi
gnxi enable-gnoi
gnxi secure-trustpoint cvk-gnoi-os
gnxi secure-server
gnxi secure-password-auth
gnxi secure-port 9339
end
write memory
```

The trustpoint name must match the intended installed certificate ID. Port
`9339` is the secure default; configuring it explicitly makes the dependency
obvious. Cisco's [IOS-XE 17.18 gNOI
guide](https://www.cisco.com/c/en/us/td/docs/ios-xml/ios/prog/configuration/1718/b-1718-programmability-cg/gnoi.html)
describes the underlying secure gNXI and certificate services.

!!! warning "Password authentication is not mutual TLS"
    Do not configure `gnxi secure-client-auth` for this password-only CVK flow.
    That command requires IOS-XE to authenticate a client certificate. Use it
    only when mutual TLS has been deliberately designed and CVK has a matching
    client certificate and key.

Inspect the effective state:

```text
show running-config | include ^gnxi
show gnxi state detail
show crypto pki trustpoints
```

Before software lifecycle work, `show gnxi state detail` must ultimately show:

- secure server enabled on port `9339`;
- secure password authentication enabled;
- secure client authentication disabled for this password-only flow;
- the intended secure trustpoint;
- `State: Provisioned`; and
- Certificate Management and OS Image services enabled, operational, and
  supported.

If the state is already provisioned, continue with the persistent credentials
and select the matching trust path in the next section, then skip the one-time
provisioning action.

## 3. Configure the Kubernetes credentials and trust

All resources in this example must be in the same namespace. The credential
Secret key is exactly `password`. Deliver it through the site's approved
secret manager. For a direct one-time creation, use a protected file whose
contents are the exact password with no unintended trailing newline:

```bash
kubectl -n edge create secret generic cat9000-1-creds \
  --from-file=password=/secure/path/device-password
```

Never commit a real password, certificate private key, or bootstrap pin to the
repository. Do not use client-side `kubectl apply` for a manifest containing
these values: its `last-applied-configuration` annotation can retain a second
copy of the Secret data.

For a device that CVK must provision, create the temporary identity Secret
from the exact input files. This avoids PEM indentation or newline changes
between digest calculation and Secret creation:

```bash
kubectl -n edge create secret generic cat9000-1-gnoi-identity \
  --from-file=tls.crt=./tls.crt \
  --from-file=ca.crt=./ca.crt \
  --from-file=ca.key=./ca.key \
  --from-file=bootstrap.crt=./bootstrap.crt
```

Omit the `bootstrap.crt` argument only when the temporary listener already
passes normal CA and hostname verification. The resulting Secret contains the
keys named by the `--from-file` arguments; it does not contain the device
private key. If the Secret already exists, update it through the approved
secret manager or with server-side apply; do not introduce a client-side
last-applied annotation containing `ca.key`.

The certificate contract is intentionally strict:

- `tls.crt` is one current, non-CA server certificate used as a CSR profile.
  It must be valid for `spec.address`, chain through a dedicated intermediate,
  contain a two-letter C plus ST, O, and OU, and have at least one IP SAN for
  the IOS-XE CSR. An address expressed as a hostname also needs a matching DNS
  SAN. CVK uses the profile CN when present and otherwise uses `spec.address`.
  IOS-XE generates and retains the actual device private key.
- `ca.crt` contains only CA certificates, includes a self-signed root, validates
  the profile, and is the **complete desired replacement** for IOS-XE's shared
  gNXI/gNMI CA bundle. Include every peer CA needed by existing gNMI clients.
- `ca.key` is an unencrypted RSA key of at least 2048 bits for the dedicated
  intermediate that issued `tls.crt`. A root CA key is rejected.
- `bootstrap.crt` is optional and pins exactly one temporary leaf. Verify its
  SHA-256 fingerprint through a trusted or out-of-band channel before use;
  blindly capturing it over the connection being authenticated defeats pinning.

Provisioning restarts gNXI and changes the server identity and shared peer CA
bundle seen by gNMI clients. Treat it as a scheduled device-management change.

Merge this fragment into the existing `CiscoDevice`; do not replace unrelated
RESTCONF, networking, or app-hosting configuration:

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9000-1
  namespace: edge
spec:
  driver: XE
  address: 192.0.2.10
  username: cvk
  credentialSecretRef:
    name: cat9000-1-creds
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

Do not add `spec.gnoi.tls` to this object. It and
`spec.xe.gnoi.certificateProvisioning` are mutually exclusive trust sources;
the provisioning Secret supplies both bootstrap and steady-state gNOI trust.

If CVK previously provisioned this identity, keep the provisioning block and
the public-only `tls.crt`/`ca.crt` Secret but skip another action. If the device
was provisioned independently and only needs CVK to trust its existing
certificate, use the simpler
[dedicated gNOI TLS configuration](gnoi-software-lifecycle.md#secure-ios-xe-gnxi)
instead of the provisioning block and action.

## 4. Provision the IOS-XE OS service once

This section is required only when `show gnxi state detail` is not
`Provisioned` or `GNOIOSVerify` returns `Device has not been provisioned`.
`GNOIOSVerify` is read-only and will never install a certificate.

Temporarily enable the write-class gate, but leave software upgrades disabled:

!!! danger "The gates apply to the whole Helm release"
    `gnoi.enableWriteClass` registers write-class reconcilers in every
    applicable IOS-XE per-device worker managed by this release, not only the
    example switch. While it is enabled, any principal with permission to
    create `IOSXEOperationalAction` can request the supported destructive
    actions. Use narrow namespace RBAC, enable it only for the provisioning
    window, and disable it after the key-free rollout.

```yaml
gnoi:
  enableWriteClass: true
  enableSoftwareUpgrade: false
```

Apply the Helm values using the same release, chart, namespace, and immutable
image version used by the deployment. Wait for the `cat9000-1-vk` Deployment to
finish its non-overlapping rollout before creating the action.

Compute the action's public-material digest from the exact source files. The
input is `tls.crt` immediately followed by `ca.crt`, with no separator:

```bash
(cat tls.crt; cat ca.crt) | sha256sum
```

On macOS, use `(cat tls.crt; cat ca.crt) | shasum -a 256` instead.

Create one immutable action with the resulting lowercase 64-character digest:

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
      publicMaterialSHA256: <64-LOWERCASE-HEX-DIGEST>
```

Watch the action and its events:

```bash
kubectl -n edge get iosxeoperationalaction provision-cat9000-1-gnoi -w
```

Press Ctrl-C after the action reaches a terminal phase; a Kubernetes watch does
not exit automatically. Then inspect its full status and Events:

```bash
kubectl -n edge describe iosxeoperationalaction provision-cat9000-1-gnoi
```

Continue only after all of the following are true:

- the action is `Succeeded`;
- a fresh gNOI `OS.Verify` succeeds;
- the active TLS leaf is the installed certificate; and
- `show gnxi state detail` reports `State: Provisioned` with OS Image up and
  supported.

If the action reports `CertificateInstallIndeterminate`, preserve the action
and inspect `GNOICertGet`, IOS-XE PKI, and gNXI state. Certificate Install is
create-only and is never replayed; do not blindly create another action with
the same certificate ID.

### Remove bootstrap secrets immediately

After the checks succeed, remove the private signer and temporary pin from the
CVK Secret while retaining the two public files. First remove them from the
secret-manager or GitOps record that populates this Secret so another
reconciler cannot restore them. Retain the CA key only in approved CA custody
if policy requires it. For a directly managed Secret, use:

```bash
SECRET_RV="$(kubectl -n edge patch secret cat9000-1-gnoi-identity \
  --type=merge \
  -p='{"data":{"ca.key":null,"bootstrap.crt":null},"metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":null}}}' \
  -o jsonpath='{.metadata.resourceVersion}')"
kubectl -n edge wait deployment/cat9000-1-vk --timeout=2m \
  --for=jsonpath='{.spec.template.metadata.annotations.cisco\.vk/gnoi-provisioning-secret-resource-version}'="$SECRET_RV"
kubectl -n edge rollout status deployment/cat9000-1-vk
kubectl -n edge get deployment cat9000-1-vk \
  -o jsonpath='{range .spec.template.spec.volumes[?(@.name=="gnoi-provisioning")].projected.sources[*].secret.items[*]}{.key}{"\n"}{end}'
kubectl -n edge get secret cat9000-1-gnoi-identity \
  -o go-template='{{range $key, $value := .data}}{{printf "%s\n" $key}}{{end}}'
kubectl -n edge get secret cat9000-1-gnoi-identity \
  -o go-template='{{if index .metadata.annotations "kubectl.kubernetes.io/last-applied-configuration"}}last-applied-present{{else}}last-applied-absent{{end}}{{"\n"}}'
```

For an externally managed Secret, wait for that controller to remove the keys,
set `SECRET_RV` from the resulting Secret, and run the Deployment wait and
inspection commands above. Ensure that delivery mechanism also removes any
legacy last-applied annotation.

The steady-state Secret intentionally still contains `tls.crt` and `ca.crt`.
The former remains the public issuance profile; the latter verifies the device
chain. Wait for the Deployment annotation to equal the patched Secret's new
resourceVersion before checking rollout status; otherwise the old generation
could be mistaken for a completed cleanup. The inspection commands must show
only `tls.crt` and `ca.crt` as projected/Secret data keys and
`last-applied-absent`; they never print Secret values. The completed key-free
rollout removes the parsed signer from worker memory.

Disable `gnoi.enableWriteClass` unless another separately authorized
write-class operation is required. Certificate provisioning is not repeated
for each software version.

## 5. Enable only the software lifecycle gate

This means enabling only the software-mutation **capability class**. The Helm
value still applies to every applicable IOS-XE per-device worker under the
release. Each lifecycle CR is constrained by namespace and `deviceRef`, so
admit CR creation only for the intended operators and devices.

Use these steady-state Helm values for the maintenance operation:

```yaml
aggregator:
  enabled: false
gnoi:
  disabled: false
  enableWriteClass: false
  enableSoftwareUpgrade: true
```

Apply them through the deployment's normal Helm workflow and wait for the
target worker:

```bash
kubectl -n edge rollout status deployment/cat9000-1-vk
```

Confirm its startup log resolved the secure IOS-XE path:

```bash
kubectl -n edge logs deployment/cat9000-1-vk -c cisco-vk \
  --since=10m --timestamps | grep 'gNOI: pillar enabled'
```

For certificate-provisioned IOS-XE password authentication, expect categorical
fields equivalent to:

```text
tls=true, trust_source=xe-provisioning, auth_mode=iosxe-password-metadata
```

This proves the worker's resolved client configuration. It does not by itself
prove the IOS-XE OS service is provisioned; `GNOIOSVerify` and device state do.
CVK never writes certificate, key, or password contents to this log.

## 6. Publish and identify both images

Keep the upgrade and downgrade images available for the entire maintenance
window. Prefer HTTPS or authenticated SFTP/SCP in production. Plain HTTP is
appropriate only on a deliberately isolated lab management network.

Compute each digest from the exact bytes served by the URL:

```bash
sha256sum cat9k_iosxe.17.18.03.SPA.bin
sha256sum cat9k_iosxe.17.18.02.SPA.bin
```

On macOS, use `shasum -a 256` instead. Record each image's size and digest in
the change record. Confirm the URLs are reachable from the **CVK worker's**
network rather than only from an administrator laptop.

## 7. Apply the upgrade manifest

Replace the URL and digest. The digest must be exactly 64 lowercase hexadecimal
characters:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: cat9000-1-to-17-18-03
  namespace: edge
spec:
  deviceRef:
    name: cat9000-1
  imageSource:
    url: https://images.example.net/iosxe/cat9k_iosxe.17.18.03.SPA.bin
    sha256: <UPGRADE-IMAGE-64-LOWERCASE-HEX-SHA256>
  targetVersion: 17.18.03
  strategy: Reload
  rollbackOnFailure: true
  installTimeoutSeconds: 14400
  rebootTimeoutSeconds: 3600
```

`strategy: Reload` makes the expected reboot explicit.
`rollbackOnFailure: true` authorizes CVK to attempt automatic recovery when
final Verify reports a target-version mismatch or a non-empty activation
failure message. It does so only on a non-individual-supervisor flow when the
previous version is proven safe to reactivate. The explicit timeouts are
examples for a large physical-switch image; size them for the platform and
maintenance policy.

Do not add the deprecated `resumePolicy` or `maxRetries` fields. The current
at-most-once controller ignores them and never uses them to replay a device
mutation.

The spec is immutable. Review it before creation, then apply it once:

```bash
kubectl apply -f iosxe-upgrade.yaml
```

Never edit or recreate an in-flight object to force progress. CVK records
Install and Activate intent before dispatch and deliberately refuses to replay
an ambiguous device mutation.

### Optional maintenance window

To prevent new work outside an approved interval, insert this block at the
existing two-space indentation under `spec` before the CR is created:

```yaml
  maintenanceWindow:
    notBefore: "2030-01-15T22:00:00Z"
    notAfter: "2030-01-16T04:00:00Z"
```

Replace both example timestamps. The closing time prevents a new, unclaimed
mutation; it cannot cancel an RPC already submitted or in flight.

## 8. Follow status, events, and provider logs

For live observation, open these terminals before applying the lifecycle
manifest. If the CR already exists, its status preserves the latest persisted
state while the object remains; recent Events may still show prior transitions,
and the log command below reads the preceding 24 hours. Set these variables in
three operator terminals:

```bash
NS=edge
DEVICE=cat9000-1
RUN=cat9000-1-to-17-18-03
```

### Terminal 1: lifecycle status and transfer progress

```bash
kubectl -n "$NS" get xeupgrade "$RUN" -w -o wide
```

Press Ctrl-C after a terminal phase; the watch does not exit automatically.

For a URL image on a single-supervisor device, the usual path is:

```text
Pending -> Resolving -> Transferring -> Activating
        -> AwaitingReachability -> Verifying -> Succeeded
```

Transfer byte counts, percentage, and detailed wait messages are stored in CR
status; CVK does not emit a provider log line for every progress update. Inspect
the complete status at any time:

```bash
kubectl -n "$NS" get xeupgrade "$RUN" -o yaml
```

### Terminal 2: Kubernetes Events

```bash
kubectl -n "$NS" get events \
  --field-selector involvedObject.kind=IOSXESoftwareUpgrade,involvedObject.name="$RUN" \
  --sort-by=.lastTimestamp
kubectl -n "$NS" describe xeupgrade "$RUN"
```

CR status persists while the object is retained. Kubernetes Events are
TTL-limited and may be aggregated, and provider logs depend on the cluster's
log-retention policy. Export all three promptly into the durable change record
before deleting a completed CR or allowing evidence to expire.

### Terminal 3: the correct CVK provider log

Lifecycle reconciliation runs inside the target's `<CiscoDevice-name>-vk`
Deployment, in the same namespace as the `CiscoDevice` and lifecycle CR. It
does not run in the central chart controller.

```bash
kubectl -n "$NS" logs deployment/"${DEVICE}-vk" -c cisco-vk \
  --since=24h --timestamps -f | \
  grep --line-buffered -E \
  'gNOI: pillar enabled|gNOI: reset client leases|IOSXESoftwareUpgrade (phase advanced|transfer progress status update failed|supervisor sync status update failed)|dispatching gNOI OS\.(Install|Activate)'
```

The current provider emits these messages:

| Message | Level | Important fields | Meaning |
|---|---|---|---|
| `gNOI: pillar enabled (...)` | Info | endpoint, `tls`, `trust_source`, `auth_mode` | Secure client configuration resolved at worker startup |
| `IOSXESoftwareUpgrade phase advanced` | Info | `softwareUpgrade`, `from`, `to`, `reason` | The persisted lifecycle phase changed; a Kubernetes Event mirrors it |
| `dispatching gNOI OS.Install` | Warning | `softwareUpgrade`, `targetVersion`, `standbySupervisor` | The at-most-once Install marker was persisted and the RPC is being sent |
| `dispatching gNOI OS.Activate` | Warning | `softwareUpgrade`, exact `version`, `standbySupervisor`, `noReboot` | The at-most-once activation marker was persisted and the RPC is being sent |
| `gNOI: reset client leases for <address>:<port>` | Info | endpoint | A stale connection/capability cache was dropped, commonly during reload |
| `dispatching gNOI OS.Activate rollback` | Warning | `softwareUpgrade`, `version` | Automatic failure recovery inside the original CR; not a planned downgrade |
| `IOSXESoftwareUpgrade transfer progress status update failed` | Warning | `error` | A Kubernetes status write failed while the data RPC may still be active; preserve and inspect the existing CR |
| `IOSXESoftwareUpgrade supervisor sync status update failed` | Warning | `error` | A supervisor-sync status write failed; preserve the existing CR and inspect status/Events before acting |

The dispatch messages are Warning level because they mark device mutation, not
because the RPC necessarily failed.

Representative output looks like:

```text
level=warning msg="dispatching gNOI OS.Install" softwareUpgrade=edge/cat9000-1-to-17-18-03 standbySupervisor=false targetVersion=17.18.03
level=info msg="IOSXESoftwareUpgrade phase advanced" softwareUpgrade=edge/cat9000-1-to-17-18-03 from=Transferring to=Activating reason=Validated
level=warning msg="dispatching gNOI OS.Activate" softwareUpgrade=edge/cat9000-1-to-17-18-03 version=17.18.03.0... standbySupervisor=false noReboot=false
level=info msg="gNOI: reset client leases for 192.0.2.10:9339"
level=info msg="IOSXESoftwareUpgrade phase advanced" softwareUpgrade=edge/cat9000-1-to-17-18-03 from=AwaitingReachability to=Verifying reason=DeviceReachable
level=info msg="IOSXESoftwareUpgrade phase advanced" softwareUpgrade=edge/cat9000-1-to-17-18-03 from=Verifying to=Succeeded reason=Succeeded
```

During `Reload`, IOS-XE may close the gRPC connection before Activate returns.
An `ActivationResponseLost` Event can therefore be expected. It is acceptable
only while the same CR continues through reachability and final verification to
`Succeeded`. Do not submit a replacement activation.

## 9. Verify the upgraded device

Do not start the next device or the planned downgrade until all checks pass.

### Kubernetes and gNOI

Require:

- lifecycle phase `Succeeded`;
- `.status.runningVersion` matching `17.18.03`;
- the lifecycle `Ready` condition `True`;
- a fresh `GNOIOSVerify` result for the requested version;
- `CiscoDevice` Ready; and
- the matching virtual Node Ready.

```bash
kubectl -n edge get xeupgrade cat9000-1-to-17-18-03 -o wide
kubectl -n edge get ciscodevice cat9000-1
kubectl get node cat9000-1
```

`CiscoDevice` and Node Ready confirm that CVK management and kubelet
heartbeats recovered; they do not prove the running IOS-XE version. A virtual
Node's reported `kernelVersion` can also remain cached until its worker
restarts. Treat lifecycle status, fresh gNOI `OS.Verify`, and IOS-XE install
state as the authoritative version evidence.

`GNOIOSVerify` is an optional audit manifest because the lifecycle reconciler
already performs final `OS.Verify`. Use it when an independent post-check is
desired:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: cat9000-1-post-upgrade-os-verify
  namespace: edge
spec:
  deviceRef:
    name: cat9000-1
  operation:
    kind: GNOIOSVerify
```

Save it as a new, uniquely named manifest, then apply, watch, and describe it:

```bash
kubectl apply -f post-upgrade-os-verify.yaml
kubectl -n edge get deviceoperation cat9000-1-post-upgrade-os-verify -w
# Press Ctrl-C after the operation reaches a terminal phase.
kubectl -n edge describe deviceoperation cat9000-1-post-upgrade-os-verify
```

### IOS-XE

Require the intended committed version, provisioned gNXI, and healthy control
processors:

```text
show version
show install summary
show gnxi state detail
show platform software status control-processor brief
```

On a platform with redundant supervisors, verify both rather than relying only
on the active supervisor's version.

Management readiness is not forwarding or application acceptance. Record and
compare site-specific interface, routing, traffic, stack/redundancy, power/fan,
and hosted-application health before and after every direction. Re-establish
any external gNMI subscriptions after reload and verify their data freshness.
CVK does not infer service-level success from an OS version match.

## 10. Apply the planned downgrade manifest

Use a **new name** and the independently verified older image. Do not edit the
upgrade object and do not label this an automatic rollback:

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: cat9000-1-to-17-18-02
  namespace: edge
spec:
  deviceRef:
    name: cat9000-1
  imageSource:
    url: https://images.example.net/iosxe/cat9k_iosxe.17.18.02.SPA.bin
    sha256: <DOWNGRADE-IMAGE-64-LOWERCASE-HEX-SHA256>
  targetVersion: 17.18.02
  strategy: Reload
  rollbackOnFailure: true
  installTimeoutSeconds: 14400
  rebootTimeoutSeconds: 3600
```

Apply and observe it through the same three channels:

```bash
kubectl apply -f iosxe-downgrade.yaml
NS=edge DEVICE=cat9000-1 RUN=cat9000-1-to-17-18-02
kubectl -n "$NS" get xeupgrade "$RUN" -w -o wide
# Press Ctrl-C after the lifecycle CR reaches a terminal phase.
```

Repeat every check in the previous section, this time requiring
`.status.runningVersion` and IOS-XE to report `17.18.02`. The gNXI state must
remain `Provisioned`; no new certificate action should be necessary.

## 11. Stop conditions and safe interpretation

| Signal | Interpretation | Operator action |
|---|---|---|
| `ActivationResponseLost` followed by reachability and `Succeeded` | Expected connection loss during reload | Continue observing the same CR |
| `TransferInterrupted` | Install outcome is unknown; the phase is nonterminal and CVK observes without replay | Preserve the CR and logs; wait for reconciliation/deadline and inspect device state |
| `PreflightFailed`, `ValidationFailed`, `Failed`, `RebootTimeout`, `RolledBack`, or `Cancelled` | Terminal failure or recovery outcome | Stop the serial rollout; capture status/events/logs and inspect the switch |
| `DeviceNotProvisioned` | Authentication reached OS.Verify, but IOS-XE OS service provisioning is missing | Complete the one-time certificate workflow; do not retry software lifecycle first |
| `GNOIUnauthenticated` or `GNOIPermissionDenied` | Credentials or AAA rejected the RPC | Check username, exact Secret key `password`, password auth, and AAA authorization |
| `MutationLeaseBlocked` or quarantine messages | Another or ambiguous mutation still fences this device | Identify the Lease holder; never delete the Lease merely to bypass uncertainty |
| `AlreadyRunning` and `Succeeded` | CVK proved the target was already running and skipped mutation | Do not claim this as evidence of transfer or reload |
| `StagedForNextBoot` | A deliberate `NoReboot` request staged the target but did not prove it is running | Treat it as incomplete until a separately authorized reboot and verification; it is not expected for this `Reload` runbook |
| A duplicate Install, or a duplicate Activate for the same version and supervisor | Possible at-most-once invariant violation | Stop and preserve evidence; an explicitly labelled `Activate rollback` for the previous version is a separate authorized recovery mutation |

Never delete and recreate an ambiguous lifecycle CR as a retry. A delete cannot
cancel device work already in flight, and CVK may retain the shared mutation
Lease to prevent overlap.

For deeper failure classification, see
[Troubleshooting](troubleshooting.md#iosxesoftwareupgrade-fails-during-resolution-staging-or-transfer).

## 12. Finish the maintenance window

After every target has passed upgrade or downgrade verification:

1. Save the final lifecycle YAML, Events, relevant worker log interval, image
   digests, and IOS-XE verification output with the change record.
2. If CVK-managed provisioning was used, confirm its Secret contains only
   public `tls.crt` and `ca.crt`.
3. Disable the software mutation gate:

    ```yaml
    gnoi:
      enableSoftwareUpgrade: false
      enableWriteClass: false
    ```

4. Apply the Helm values and wait for each worker rollout. When both mutation
   gates are off and signer/old-mutation-worker cleanup has completed, the worker Deployment can
   return from non-overlapping `Recreate` updates to its normal rolling update
   strategy.

Do not disable a gate as a way to clear an uncertain operation. The IOS-XE
maintenance observer continues to respect a retained Lease and keeps the owned
maintenance taint until that Lease releases or expires. Neither gate changes
nor deleting the CR cancel work already accepted by IOS-XE.

Keep the device credential, selected public trust source, `CiscoDevice` gNOI
block, and device-side provisioned gNXI configuration. Those are steady-state
trust configuration, not per-upgrade artifacts.

## 13. Repeatable lab evidence and support limits

The repository's `scripts/iosxe-gnoi-lab-cycle.py` creates unique immutable
manifests, captures lifecycle status, Events and provider logs, and stops on an
unknown or failed result. It targets only explicitly supplied
`CiscoDevice`/address pairs, checks the expected worker image, and never
recreates a failed mutation as a retry. Run `--help` for the complete command.
Without `--execute`, it creates read-only evidence probes only. With
`--execute`, it upgrades each target serially, then downgrades each target
serially; both directions must actually activate, not just return
`AlreadyRunning`.

Use a new protected evidence directory per run and copy the entire directory
to the change record. Show-command output can contain sensitive operational
information even though the runner never reads Kubernetes Secret data. A
timeout or missing log interval is incomplete evidence, not a successful test.
The runner's platform checks are intentionally conservative; an unrecognized
output format requires investigation instead of automatic progression.

| Capability | Validation boundary |
|---|---|
| Secure IOS-XE password metadata and certificate provisioning | Explicit TLS and dedicated trust; initial provisioning is a separate, authorized write action |
| Physical C9K `17.18.02` ↔ `17.18.03` | Lab URL-streamed `Reload` path; retain exact runtime revision and per-run evidence |
| IOS-XE `17.18.04` or other images/platforms | Do not infer hardware validation from the `.02`/`.03` cycle; validate separately |
| `deviceFile` source | Requires gNOI File.Get support; the lab C9K returned `Unimplemented`. Use the verified URL-streamed path there |
| Dual supervisors, stacks, `NoReboot`, automatic failure rollback | Separate qualification required; standalone happy-path reloads do not validate these |
| Worker/API failure injection | Covered by deterministic controller tests; only claim hardware injection scenarios actually recorded in the run evidence |
| NX-OS and IOS XR | No software lifecycle implementation is implied. Future adapters must declare OS/certificate capabilities, version semantics, activation and recovery rules |

Before production, add the site's service-level acceptance checks, independent
recovery/console exercise, certificate-renewal ownership, durable audit/log
retention, and a single physical-device ownership policy across clusters.
The API remains Beta while that platform and failure qualification develops.
