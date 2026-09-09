# Troubleshooting

Common issues and how to diagnose them.

## First — gather the basics

The Helm release name is `cvk` throughout this page (matching the [Getting Started](getting-started.md) guide). If you installed with a different release name, substitute it wherever `cvk` appears below.

```bash
# CiscoDevice full state — usually the most useful starting point
kubectl describe ciscodevice <name>
kubectl get ciscodevice <name> -o yaml

# Controller logs
kubectl -n cvk-system logs deploy/cvk-controller --tail=200

# VK pod logs (one per device)
kubectl -n <device-namespace> logs deploy/<device-name>-vk --tail=200

# Virtual node status
kubectl describe node <device-name>

# Pods on the virtual node
kubectl get pods --field-selector spec.nodeName=<device-name>
```

On the device:

```
show iox-service
show app-hosting list
show app-hosting detail appid <app-id>
```

---

## Apps are unexpectedly removed when a second VK worker starts

CVK currently requires exactly one active app-hosting worker per physical
device endpoint. This applies to IOS-XE and NX-OS, including different
hostnames or addresses that identify the same device.

At startup, a worker compares CVK-managed apps discovered device-wide with
Pods assigned to its own virtual node. A second worker with a different node
identity cannot see the incumbent node's Pods, so it can classify their apps as
dangling and stop, deactivate, or uninstall them. Config-operation leases do
not serialize this pod-lifecycle cleanup.

Stop the duplicate worker immediately and leave only one device owner running.
For a controlled handoff, choose one path:

- To retain existing Pods and apps, stop the incumbent worker, wait until its
  process has terminated, then start the replacement with the same virtual-node
  identity while preserving the existing Pod objects.
- To change identity, keep the incumbent running while you drain or delete Pods
  assigned to the old node and verify their device apps have been removed.
  Then stop the incumbent, wait until it has terminated, and start the
  replacement.

Never overlap old and new app-hosting workers against the same physical device.
For parallel diagnostics, use read-only transport or `DeviceOperation` checks
that do not start another Virtual Kubelet app runtime.

---

## CiscoDevice stuck in `Provisioning`

`Provisioning` means the controller has created the ConfigMap and Deployment but no VK pod is Ready yet.

**Check the VK Deployment:**

```bash
kubectl get deploy <device-name>-vk -o yaml
kubectl describe pod -l app.kubernetes.io/name=cisco-vk,app.kubernetes.io/instance=<device-name>
```

**Common causes:**

- **Image pull error** — make sure `image.repository`/`vkImage.repository` points at a registry the cluster can pull from.
- **Bad credentials** — look for `401 Unauthorized` in VK pod logs. Verify the Secret key is spelled `password` (not `PASSWORD`, not `pass`).
- **Device unreachable** — look for `dial tcp: i/o timeout` in VK pod logs. Check routing, firewall, and that RESTCONF is enabled (`restconf` in device config).
- **TLS verification failing** — look for `x509: certificate signed by unknown authority`. In local/custom deployments, supply a mounted `tls.caFile`; for controller-managed gNOI, use `spec.gnoi.tls.secretRef`. Use `tls.insecureSkipVerify: true` only as a temporary, non-gNOI lab diagnostic.

---

## Pod stuck in `Pending`

`Pending` means the VK has accepted the pod but the device has not yet reached `RUNNING`.

**Walk the state machine.** Check VK logs for the reconcile line:

```
ReconcileApp cvk00000_<uid>: observed="INSTALLING" desired=Running phase=Converging
```

Known intermediate states that are expected:

- `INSTALLING` — normal during the first 5–30 seconds.
- `DEPLOYED` — very brief; VK will issue `activate` on the next poll.
- `ACTIVATED` — very brief; VK will issue `start` on the next poll.

If the pod stays in the same state for more than a minute, there is something wrong. See specific sections below.

---

## PackagePolicyInvalid false positives

### Symptom

Pod shows `Failed` with:

```
status:
  phase: Failed
  reason: PackagePolicyInvalid
  message: "install blocked: app package policy is invalid ..."
```

### Why it happens

IOS-XE reports `pkg-policy = iox-pkg-policy-invalid` as the YANG default during the first 1–3 seconds of every install, before signature verification completes. A confirming install notification only appears when the device actually rejects the package. The reconciler tries to distinguish the two by waiting for the notification, but if the notification ordering is off or the device never emits one, you can get stuck.

### Fix

If you're running unsigned packages on purpose — your own custom application builds or test images:

```yaml
spec:
  allowUnsignedApps: true
```

This does two things:

1. **Device-side** — CVK PUTs `app-hosting-cfg-data/controls` with `sign-verification: false` on first connect, disabling the IOS-XE package signature check. Equivalent to `no app-hosting signed-verification`.
2. **Reconciler-side** — treats `iox-pkg-policy-invalid` during `INSTALLING` as a transient (non-fatal) signal.

If the device-side PUT fails (e.g. platform does not support the YANG leaf, or insufficient privilege), CVK logs a warning and the device policy may still block unsigned installs.

If you want signing enforced:

1. Verify the package is actually signed (`show app-hosting detail appid <id>` → `Signature verified: YES`).
2. If the package is signed but the check is firing, check logs for the full notification text — the device explains what failed:
   ```
   kubectl logs deploy/<device-name>-vk | grep "install blocked"
   ```

The pod recovery loop will automatically retry these failed pods with exponential backoff; you don't need to `kubectl delete` them.

---

## Pod never gets an IP (shows `0.0.0.0`)

IP discovery runs in two stages. If both come up empty the pod stays at `0.0.0.0`.

**1. Oper-data path**

```bash
# On the device
show app-hosting detail appid <app-id> | include ipv4
```

If the oper-data shows a real IP but the pod doesn't, the VK isn't scraping it — check VK logs for errors calling `app-hosting-oper-data`.

**2. ARP fallback**

```
show arp
```

If the container's MAC appears but the IP is still `0.0.0.0` at the pod, the VK's ARP lookup is failing. Most common cause: the MAC in oper-data doesn't match the ARP entry because the container hasn't finished DHCP handshake yet. Give it 30 s; the reconciler will retry.

**If no ARP entry exists at all:**

- DHCP pool is misconfigured (wrong network, exhausted pool).
- VirtualPortGroup interface is down or has no IP.
- App-hosting does not have the `guest-ipaddress` fields populated (check `show app-hosting list detailed`).

---

## `kubectl top node` returns an error

```
error: Metrics not available for node <name>
```

Verify the stats endpoint is reachable:

```bash
kubectl get --raw "/api/v1/nodes/<name>/proxy/stats/summary" | head
```

If that works but `kubectl top` fails, the metrics-server does not trust the kubelet certificate. On k3s:

```
# /etc/rancher/k3s/config.yaml
kubelet-certificate-authority: ""
```

On upstream Kubernetes, either supply a signed kubelet cert via `--tls-cert-file` / `--tls-key-file`, or add `--kubelet-insecure-tls` to the metrics-server deployment.

---

## Prometheus metrics missing

`cisco_device_*` metrics are served from the VK pod's kubelet endpoint (`/metrics/resource`).

Expected setup: your Prometheus is already scraping kubelets (`kube-prometheus-stack` does this by default).

**Check:**

```bash
# Are the metrics there at all?
kubectl get --raw "/api/v1/nodes/<name>/proxy/metrics/resource" | grep cisco_device
```

---

## gNOI actions or software upgrades do nothing

Both write-class gNOI surfaces are opt-in on the per-device VK pod:

- `IOSXEOperationalAction` requires `--enable-write-class-gnoi` or
  `CISCO_VK_ENABLE_WRITE_CLASS_GNOI=true`.
- `IOSXESoftwareUpgrade` requires `--enable-iosxesoftwareupgrade` or
  `CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=true`.

Check the VK pod args and logs:

```bash
kubectl -n <device-namespace> get deploy <device-name>-vk -o yaml | grep -E "enable-write-class-gnoi|enable-iosxesoftwareupgrade"
kubectl -n <device-namespace> logs deploy/<device-name>-vk --tail=200 | grep -i gnoi
```

If the CR remains untouched, verify the `spec.deviceRef.name` matches the
device worker's `CiscoDevice` name and that the VK service account can update
the CR status and finalizer subresources.

---

## Secure gNOI authentication or connection fails

IOS-XE 17.18.x serves password-authenticated gNOI on the secure gNXI listener,
port `9339` by default. Start by comparing the device and `CiscoDevice`
settings:

```bash
# IOS-XE CLI
show running-config | include ^gnxi
show gnxi state detail
show crypto pki trustpoints

# Kubernetes; this displays references and transport settings, not Secret data
kubectl -n <device-namespace> get ciscodevice <device-name> \
  -o jsonpath='{.spec.username}{"\n"}{.spec.gnoi}{"\n"}{.spec.xe.gnoi}{"\n"}{.spec.tls}{"\n"}'

kubectl -n <device-namespace> logs deploy/<device-name>-vk --tail=200 | grep -iE 'gnoi|gnxi|grpc|x509'
```

The worker startup log reports `trust_source=system`, `shared`, `gnoi`, or
`xe-provisioning` and `auth_mode=iosxe-password-metadata` (or `none`) for
explicit TLS without logging certificate or credential contents.

| Symptom / gRPC status | Most likely cause | Check |
|---|---|---|
| Admission rejects `spec.gnoi.tls` | The transport is not explicitly TLS, file paths were placed in a Kubernetes object, or provisioning is also configured | Set `spec.gnoi.transportSecurity: tls`, use only `gnoi.tls.secretRef` in Kubernetes, and choose either generic gNOI TLS or `spec.xe.gnoi.certificateProvisioning`, not both. |
| `GNOITLSInvalid` event or reconcile error | The referenced Secret is missing, `ca.crt` is empty/invalid, or only one client-certificate key is present | Keep the Secret in the `CiscoDevice` namespace. Supply a parseable `ca.crt` and either both `tls.crt` and `tls.key` or neither. |
| `Unauthenticated` | Missing or rejected metadata | Check `spec.username`, the referenced Secret's exact `password` key, and `gnxi secure-password-auth`. CVK sends separate lowercase `username` and `password` metadata only over TLS. |
| `PermissionDenied` | Authentication succeeded but AAA denied the RPC | Check the IOS-XE AAA method list, user privilege, and authorization policy for the requested operation. |
| `x509` trust or hostname error | The selected trust source does not validate the certificate or `spec.address` | Check the logged trust source. Update the generic gNOI TLS Secret's `ca.crt`, or, for provisioning only, supply the exact temporary leaf as `bootstrap.crt`. Explicit secure/dedicated gNOI has no skip-verification mode. |
| Trust Secret changed but failures persist | The enabled per-device worker has not completed its Secret-driven restart | Check the per-device Deployment rollout and controller events. Generic TLS Secret rotation affects new connections only and never rotates the device certificate. There is no worker restart in aggregated config-only topology or while gNOI is globally disabled. |
| Startup reports `gNOI: resolve configuration` | The selected trust sources conflict, certificate files are invalid, explicit TLS inherited insecure shared TLS, or `CISCO_VK_GNOI_PORT` is invalid | Correct the named validation error. CVK does not silently downgrade transport, ignore an invalid port override, or fall back to another trust source. |
| `Unavailable`, connection refused, or deadline exceeded | Listener, port, route, firewall, VRF, or expected gNXI restart | Check port `9339`, `show gnxi state detail`, pod reachability, and recent provisioning events. |
| `FailedPrecondition: Device has not been provisioned` | Authentication works, but the OS service lacks a provisioned identity | Confirm `gnxi enable-gnoi` and `gnxi secure-init`, configure `spec.xe.gnoi.certificateProvisioning`, enable write-class gNOI, and create a `ProvisionCertificate` action. `GNOIOSVerify` never installs. |
| `ProvisioningUnavailable` | The worker has no authorized certificate provisioner | Confirm `spec.xe.gnoi.certificateProvisioning` and the write-class gate, and ensure the gated Secret projection contains a valid `ca.key`; the action is rejected before device access. |
| `ProvisioningIntentMismatch` | The action was created for a different certificate ID or public-material digest than the worker loaded | Recompute SHA-256 over the exact `tls.crt` bytes followed by `ca.crt`, wait for the Secret-driven worker rollout, then create a new immutable action. No device RPC was sent for the rejected action. |
| Missing `ca.key` | The Secret is in post-provision/read-only form, or the write-class gate is disabled | The built-in provisioner/action is available only with the dedicated intermediate CA key and write-class gate, even if `OS.Verify` might make the action a no-op. Never supply a root CA key. |
| CSR `InvalidArgument` | The profile or signer is invalid | Check that `tls.crt` has C, ST, O, OU, and an IP SAN and chains through the intermediate whose key is in `ca.key`. |
| Certificate ID conflict / already exists | The create-only ID is stale or belongs to another identity | Compare `GNOICertGet` with device trustpoints. Resolve or remove stale state out of band, or choose a new ID; CVK will not overwrite it. |
| `Unimplemented` | That platform does not implement the requested service | Use `GNOICertGet` as the broad pre-provision connectivity probe. `GNOIOSVerify` is also read-only before provisioning, but only the exact not-provisioned response is expected; success requires `State: Provisioned`. |

Before creating the action, confirm `gnxi enable-gnoi`, `gnxi secure-server`,
`gnxi secure-password-auth`, and `gnxi secure-init`. After a mutation, the
action reconnects and succeeds only when the exact installed certificate is
the active TLS leaf and `OS.Verify` works over that same peer; it never retries
Install. If `OS.Verify` already succeeds, the action is a no-op and its
requested certificate ID/digest are intent only, not an attestation of the
active identity. Confirm the trustpoint and `State: Provisioned` on the device,
then remove `ca.key` and `bootstrap.crt` and disable write-class gNOI. See the
[canonical secure gNOI workflow](gnoi-software-lifecycle.md#secure-ios-xe-gnxi)
for configuration, Secret contents, and the shared gNXI/gNMI CA-bundle warning.

---

## IOSXEOperationalAction is rejected

Common rejection reasons:

- `ConfirmMismatch` — `spec.confirm` must exactly equal
  `spec.deviceRef.name`.
- `InvalidAction` — exactly one typed args block must match
  `spec.action.kind`.
- Kubernetes admission rejects updates because `spec` is immutable after
  creation. Create a new action CR for a changed request.

For actions that reach `Running` and then fail, inspect both events and status:

```bash
kubectl describe iosxeoperationalaction <name>
kubectl get events --field-selector involvedObject.name=<name>
```

`Running` means the controller may already have invoked the device-side RPC.
The reconciler will not dispatch the same CR again after a restart. It waits
for the bounded RPC-and-persistence window, then records
`ActionOutcomeUnknown`, removes the finalizer so deletion can complete, and
retains the shared mutation Lease until its normal expiry. Verify the device's
actual state before submitting a replacement action.

---

## IOSXESoftwareUpgrade fails during resolution, staging, or transfer

For URL sources, `imageSource.sha256` is required. URL userinfo is rejected;
query strings and fragments remain visible in the immutable CR even though CVK
redacts them from status, events, logs, and errors, so do not put secrets there.
For SCP/SFTP, host-key verification is required unless a custom/local
deployment explicitly enables both parts of the lab-only escape hatch (the
stock Helm chart intentionally does not expose it):

```yaml
imageSource:
  url: sftp://images.example.net/image.bin?insecureSkipHostKey=true
```

```bash
export CISCO_VK_UPGRADE_ALLOW_INSECURE_SSH=true
```

`image exceeds configured size limit` means an HTTP `Content-Length`, remote
file-size report, SCP header, or the streamed bytes exceeded
`CISCO_VK_UPGRADE_MAX_IMAGE_BYTES`. The Helm equivalent is
`gnoi.softwareUpgrade.maxImageBytes`; it defaults to 8 GiB. Do not simply raise
the limit: URL resolution stores one full image plus small filesystem and pod
overhead in the pod's `/tmp` `emptyDir`. The resolver hashes into a private
temporary file, atomically promotes that same file to the cache, retains at
most one completed managed digest, and removes owned crash residue before the
next resolution. Check pod eviction events, node ephemeral-storage pressure,
and storage requests/limits.

Transient network, HTTP `408`/`425`/`429`/`5xx`, and Kubernetes API read
failures remain in `Transferring` with reason `ImageResolveRetry` and retry only
until `installTimeoutSeconds` expires. A resolver's shorter per-attempt timeout
does not end the upgrade while that overall deadline remains. Credential,
endpoint-binding, host-key or TLS verification, malformed source, size, digest,
and unsafe-cache errors fail immediately as `ImageResolveFailed`.

An `unsafe image cache entry` error means the digest path is a symlink or is
not a regular file. CVK will not follow or overwrite it. Treat unexpected cache
mutation as a security signal. After confirming no upgrade RPC is in flight,
replace the affected worker pod to clear its ephemeral cache.

`preinstalled: {}` and deprecated `localPath` require `targetVersion` to resolve
to one exact, activatable install-inventory entry. A matching file on flash is
not sufficient. `localPathSHA256`, when supplied, verifies the legacy path but
does not register it.

Use `deviceFile.path` plus required `deviceFile.sha256` when a file already on
IOS-XE must be registered. CVK reads it through gNOI `File.Get` before the
IOS-XE RESTCONF install RPC. `DeviceFileHashFailed`,
`SoftwareLifecycleUnsupported`, `AmbiguousTargetVersion`, or
`TargetNotActivatable` are fail-closed outcomes; there is no CLI fallback.
`UncorrelatedDeviceFileInventory` means the target was already installed or in
progress before this CR recorded its staging request, so CVK cannot prove that
the verified file produced that inventory entry. If status reports an unknown
staging or activation outcome, inspect the IOS-XE install inventory and
operation UUID before creating a new CR. The original CR is not replayed after
an ambiguous device mutation. `installTimeoutSeconds` first bounds gNOI
readiness and device-file `File.Get`. The native-registration claim then starts
a fresh window shared by registration and inventory convergence; retries inside
either window receive only its remaining time.

`MutationLeaseBlocked` means another CVK-managed write action, upgrade, or
uncertain prior outcome owns the device's shared disruptive-mutation Lease.
Inspect that object's status and the Lease holder identity. Do not delete the
Lease merely to bypass an unknown device result; wait for the safety TTL or
establish device state before taking an explicitly controlled recovery action.

After gNOI `OS.Install` is dispatched, a transport interruption moves the CR
to `TransferInterrupted`. CVK retains the durable attempt marker and observes
without replay until `installTimeoutSeconds` expires, even when native
inventory reports the target absent. The CR then fails with
`InstallOutcomeUnknown`; inspect the device before creating a new upgrade CR.

With `rollbackOnFailure: true`, a verify mismatch enters `RollingBack` and
attempts to re-activate the previously observed running version only when the
lifecycle backend proves it remains activatable. If no previous version was
captured, the CR fails with `RollbackVersionMissing`. Rollback receives a new
`rebootTimeoutSeconds` timer when that phase begins, including any pre-dispatch
reachability wait; its activation RPC is capped by the remaining time. The
primary activation sequence follows the same remaining-time cap. After a
durable mutation claim, `ActivationControlTimeout` or
`RollbackDidNotConverge` retains the shared Lease until its safety TTL expires.

`strategy: ISSU` fails preflight with `ISSUVerificationUnsupported`. CVK does
not claim a non-disruptive upgrade until the platform backend can verify that
IOS-XE selected the ISSU path; `NoReboot` is not that verification.

**If the raw endpoint returns metrics but Prometheus doesn't see them:**

- The node ServiceMonitor isn't matching (check labels).
- The scrape job for kubelets doesn't use the `/metrics/resource` path — some configurations only scrape `/metrics/cadvisor`.

**If the raw endpoint returns only `cisco_device_cpu_*`/`memory_*`/`storage_*` but no `interface_*` or `cdp_*`:**

- The driver does not implement `TopologyProvider`. This is always the case for the FAKE driver and will be the case for future drivers that don't implement topology.
- Or the device has no CDP/OSPF neighbors to report.

---

## OTEL traces not appearing

**Check it's enabled:**

```bash
kubectl get ciscodevice <name> -o yaml | yq .spec.otel
```

**Verify VK pod startup:**

```bash
kubectl logs deploy/<device-name>-vk | grep -i otel
```

You should see one of:

- `OTEL topology exporter started` — good, emitting
- `Failed to initialise OTEL topology exporter` — endpoint unreachable or config invalid
- `driver does not implement TopologyProvider` — wrong driver (FAKE doesn't, XE does)

**Common misconfigurations:**

- `endpoint` has scheme prefix (wrong): `https://otel:4317`. Use `host:port` only.
- `insecure: false` against a plaintext gRPC collector — use `insecure: true` for typical in-cluster OTLP collectors without TLS.
- `intervalSecs` set below 10 — the minimum is enforced to 10 s; values below will silently use 60 s.

**No traces after 60 s**: check the collector logs — it will receive spans in batches. Splunk Observability Cloud can sometimes take a minute to surface the first trace.

---

## Pod stuck in `PullingImage` waiting state

### Symptom

`kubectl describe pod <name>` shows the container in a waiting state:

```
State:          Waiting
  Reason:       PullingImage
  Message:      Copying image to device flash; this may take several minutes
```

### What is happening

The VK attempted a device-native HTTP pull that timed out (default 3 minutes), and is now running the copy fallback: it downloads the image tar from the HTTP URL and copies it to device flash via RESTCONF, then reinstalls from that local path. The copy RPC is synchronous and can take several minutes depending on image size and network speed.

You can monitor progress via pod events:

```bash
kubectl describe pod <name>
# Look for the Events section at the bottom:
#
#   Normal  Pulling          <time>   cisco-virtual-kubelet  Pulling image https://...
#   Warning ImagePullFallback <time>  cisco-virtual-kubelet  Device-native pull timed out...
#   Normal  Copying          <time>   cisco-virtual-kubelet  Copying image ... to flash:/...
#   Normal  Pulled           <time>   cisco-virtual-kubelet  Image successfully copied to ...
#   Normal  Started          <time>   cisco-virtual-kubelet  App ... is running
```

### Wait times

- A 500 MB image over a 100 Mb/s management link takes roughly 40 seconds for the copy alone, plus 30 seconds for app activation. Allow 3–5 minutes total.
- If the pod does not transition to `Running` after 10 minutes, check VK logs for errors:

```bash
kubectl -n <device-namespace> logs deploy/<device-name>-vk | grep -E "copy|fallback|error|Error"
```

### Avoiding the copy fallback

To use the copy path intentionally and skip the device-native pull attempt entirely, set `imagePullPolicy: Never` and pre-copy the tar to flash yourself. Then reference the flash path directly in the pod spec:

```yaml
image: flash:/virtual-kubelet/my-app.tar
imagePullPolicy: Never
```

---

## `imagePullPolicy: IfNotPresent` still re-downloads the image every time

### Symptom

You set `imagePullPolicy: IfNotPresent` expecting the image to be reused from a local cache, but each pod creation issues a fresh download. `dir flash:` shows no cached tar.

### Why

IOS-XE App Hosting does not leave a copy of the image on flash when using the device-native install path (`app-hosting install appid ... package <url>`). The device fetches and loads the image directly into the container runtime without writing it to flash. Since no flash copy is ever created, there is nothing for `IfNotPresent` to reuse — it behaves identically to `Always` on the device-native pull path.

The `IfNotPresent` flash-cache optimization only activates when the VK's **copy fallback path** has run at least once (i.e., the device-native pull timed out and the VK copied the tar to flash itself). After that first copy, subsequent deploys with `IfNotPresent` will reuse the cached tar.

### Workaround

To reliably benefit from local caching, force the copy path by one of the following:

- Pre-copy the image to flash manually on the device, then use `imagePullPolicy: Never` with a flash path (`flash:/virtual-kubelet/my-app.tar`).
- Accept that on platforms where the device-native pull succeeds quickly, re-downloading from the registry on each deploy is the expected behaviour.

---

## `imagePullPolicy: Never` with HTTP image URL

### Symptom

Pod immediately goes to `Failed` with:

```
status:
  phase: Failed
  message: "app ...: imagePullPolicy is Never but image is an HTTP URL ..."
```

### Why

`imagePullPolicy: Never` means the image must already exist on device flash and no download of any kind will be attempted. Using an HTTP or HTTPS URL with this policy is invalid.

### Fix

Either:

1. Change the image reference to a flash path: `flash:/virtual-kubelet/my-app.tar`
2. Or change the `imagePullPolicy` to `IfNotPresent` or `Always` to allow the VK to download it.

---

## Pod stuck `Failed` forever

Usually one of:

- `reason: NotFound` — VK pod was restarted and lost state. The pod recovery loop handles this automatically.
- `reason: ProviderFailed` — transient device issue. Recovery loop handles this.
- `reason: PackagePolicyInvalid` — see [above](#packagepolicyinvalid-false-positives).

The pod recovery loop resets matching Failed pods to `Pending` with exponential backoff (15 s → 5 min). You should see this in VK logs:

```
Recovered <n> stale failed pods
```

If the loop isn't running, check the VK pod is healthy (not crash-looping). The recovery goroutine starts with the rest of the VK and stops when the VK exits.

---

## Virtual node lingers after `CiscoDevice` deletion

Normally the controller deletes the virtual `Node` as part of finalizer cleanup. If you see a lingering node:

```bash
kubectl get node <device-name>
# Status: NotReady
```

This usually means the finalizer was skipped (force-delete of the CR, or the controller was down when deletion happened). Clean up by hand:

```bash
kubectl delete node <device-name>
```

Check no orphaned Deployment / ConfigMap remains:

```bash
kubectl -n <ns> get deploy,cm | grep <device-name>
```

Avoid `kubectl delete ciscodevice --force --grace-period=0` — it skips the finalizer and will cause this.

---

## A Secret update did not roll the VK pod

The controller normally watches referenced credential Secrets and copies their
`resourceVersion` into the Deployment pod template, which triggers an automatic
rollout. Compare the source and observed versions and check the manager:

```bash
kubectl -n <ns> get secret <secret-name> \
  -o jsonpath='{.metadata.resourceVersion}{"\n"}'
kubectl -n <ns> get deploy/<device-name>-vk \
  -o jsonpath='{.spec.template.metadata.annotations.cisco\.vk/credential-resource-version}{"\n"}'
kubectl -n cisco-vk-system get deploy,pods
kubectl -n cisco-vk-system logs deploy/cisco-vk-cisco-virtual-kubelet --tail=200
```

Confirm the `CiscoDevice` references that same-namespace Secret. After fixing
the manager or reference, the watch-driven reconcile should roll the pod. For
an immediate troubleshooting fallback, force one manually:

```bash
kubectl -n <ns> rollout restart deploy/<device-name>-vk
```

---

## Where to look next

- [Architecture](ARCHITECTURE.md) — internal state machines and data flow
- [Configuration](CONFIGURATION.md) — every field and its defaults
- [Observability](observability.md) — metrics and OTEL details
- GitHub issues — if your problem isn't listed here, file an issue with:
  - CiscoDevice spec (redact credentials)
  - `kubectl describe ciscodevice` output
  - VK pod logs (`--tail=200`)
  - `show app-hosting detail appid <id>` from the device
