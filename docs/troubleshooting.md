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
- **TLS verification failing** — look for `x509: certificate signed by unknown authority`. Either supply `tls.caFile`, or temporarily set `tls.insecureSkipVerify: true` to confirm.

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
  -o jsonpath='{.spec.username}{"\n"}{.spec.gnoi}{"\n"}{.spec.tls}{"\n"}'

kubectl -n <device-namespace> logs deploy/<device-name>-vk --tail=200 | grep -iE 'gnoi|gnxi|grpc|x509'
```

For a password-authenticated listener, IOS-XE should have `gnxi`,
`gnxi secure-server`, a server trustpoint, and `gnxi secure-password-auth`
configured. The matching CR is:

```yaml
spec:
  username: admin
  credentialSecretRef:
    name: device-creds
  tls:
    enabled: true
    # Use caFile in production; this is only a transport-isolation example.
    insecureSkipVerify: true
  gnoi:
    transportSecurity: tls
    port: 9339
```

CVK sends the resolved credentials as two raw gRPC metadata keys,
`username` and `password`, on TLS RPCs. It does not send an HTTP Basic
`Authorization` header and never sends the password in plaintext mode. Do not
log or manually print the Secret while diagnosing the connection.

| Symptom / gRPC status | Most likely cause | Check |
|---|---|---|
| `Unauthenticated` | Missing or rejected username/password metadata | Ensure `spec.username` is non-empty, the referenced Secret contains the exact key `password`, and IOS-XE has `gnxi secure-password-auth`. Recreate accidentally newline-terminated values with `kubectl create secret ... --from-literal=password=... --dry-run=client -o yaml | kubectl apply -f -`. |
| `PermissionDenied` | Authentication succeeded but AAA denied the RPC | Check the IOS-XE AAA method list, user privilege, and authorization policy for the requested operation. |
| `x509: certificate signed by unknown authority` | The device certificate is not trusted | Mount the correct CA and set `tls.caFile`. Use `insecureSkipVerify: true` only briefly to isolate the trust problem. |
| Certificate hostname/SAN error | `spec.address` does not match the device certificate | Use a hostname present in the certificate or issue a certificate with the required IP/DNS SAN. |
| TLS handshake error | Plaintext/TLS mode mismatch, or IOS-XE requires a client certificate | Use `transportSecurity: tls` with port `9339`. If and only if `gnxi secure-client-auth` is configured, mount and set `tls.certFile` and `tls.keyFile`. |
| `Unavailable`, connection refused, or deadline exceeded | Wrong port, listener down, firewall, route, or gNXI VRF restriction | Verify `show gnxi state`, `spec.gnoi.port`, reachability from the VK pod, and the device's gNXI VRF. Setting only port `9339` is insufficient if the client still uses plaintext. |
| `FailedPrecondition: Device has not been provisioned` from an OS RPC | Password authentication succeeded, but IOS-XE has not provisioned the gNXI identity required by the OS service | Use `GNOICertGet` to confirm the secure connection, then configure the opt-in `gnoi.certificateProvisioning` Secret or provision a CA-signed gNXI trustpoint out of band. |
| `InvalidArgument: Internal parameters...` while generating the CSR | The `tls.crt` profile lacks an IOS-XE-required CSR field | Reissue the profile template with C, ST, O, OU, and an IP SAN for the management address (or use an IP literal for `spec.address`). Add CN when it should differ from `spec.address`; otherwise CVK uses `spec.address`. Confirm that `ca.key` matches the template issuer. |
| `Aborted: Timeout waiting for event` while loading the certificate | IOS-XE 17.18.04 was given the external-key Install variant | Add the matching `ca.key` to select target-generated CSR mode. If the failed request left its certificate ID behind, inspect it and use a new unused `certificateID` for the next bootstrap attempt. |
| Certificate ID conflict / already exists | gNOI Install is create-only, or an earlier attempt left a trustpoint at that ID | Compare `GNOICertGet` with `show crypto pki trustpoints`. Do not blindly retry or overwrite the ID; clean up the stale PKI object out of band under change control, or retry with a new unique `certificateID`. |
| `Unimplemented` | The IOS-XE release/platform does not implement that gNOI service | This is not an authentication failure. IOS-XE 17.18.x may support OS/Certificate/Factory Reset while omitting System or File. Before provisioning use `GNOICertGet`; after provisioning use `GNOIOSVerify` rather than assuming System is present. |

`gnoi.transportSecurity: auto` follows `tls.enabled`; `tls` forces a TLS gNOI
connection and reuses the trust/client-certificate fields from `spec.tls`.
`CISCO_VK_GNOI_INSECURE=1` overrides either setting and forces the legacy
plaintext listener, so check the rendered Deployment environment when the
observed mode differs from the CR. `CISCO_VK_GNOI_PORT` similarly overrides
`spec.gnoi.port`.

Before the first Install, confirm that `show running-config | include ^gnxi`
contains `gnxi secure-init`. IOS-XE uses that bootstrap setting to bind the
first newly installed certificate ID to the secure gNXI service. A secure
listener and `gnxi secure-password-auth` can authenticate CVK while still
leaving the OS service unprovisioned if `secure-init` is absent.

When certificate provisioning is configured, inspect `show gnxi state detail`.
The expected result after the listener restarts is `State: Provisioned`, the
secure trustpoint is the requested `certificateID`, and the OS service is
operational. Compare that binding with `show crypto pki trustpoints` (and
`show crypto pki certificates <certificateID>` where supported). CVK installs
only a missing configured certificate ID; gNOI Install cannot reuse an ID and
CVK never overwrites a conflicting certificate.

If a prior attempt created the ID but the secure trustpoint still names an old
or self-signed identity, treat it as stale device state. Do not repeatedly
submit Install with the same ID. Either remove or rebind the stale PKI state
manually under the device's change procedure, or restore `gnxi secure-init`
and retry with a new unique ID such as `cvk-gnoi-os-2`. Changing the ID requires
only an update to `certificateProvisioning.certificateID`; keep the Secret's
validated certificate profile and CA material consistent. If the desired
certificate is installed and bound but IOS-XE remains unprovisioned, inspect
the platform's gNOI OS-service configuration rather than deleting the
Kubernetes Secret.

Because Install replaces the shared target CA bundle, ensure `ca.crt` includes
all gNXI/gNMI peer CAs that must survive the change and set the required
`replaceTargetCABundle: true` acknowledgement. See
the [provisioning workflow](gnoi-software-lifecycle.md#provisioning-the-ios-xe-gnoi-os-service)
for the Secret format and the shared gNXI/gNMI restart caveat.

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
The reconciler will not dispatch the same CR again after a restart.

---

## IOSXESoftwareUpgrade fails during image resolution or transfer

For URL sources, `imageSource.sha256` is required. Credential-bearing URLs are
redacted before they are written to CR status, events, or logs. For SCP/SFTP,
host-key verification is required unless the operator explicitly enables the
lab-only escape hatch:

```bash
CISCO_VK_UPGRADE_ALLOW_INSECURE_SSH=true
```

When using `localPath`, add `localPathSHA256` if the device supports gNOI
File.Get. A mismatch fails before activation with `LocalPathHashMismatch`.

Transfer interruptions move to `TransferInterrupted` and retry according to
`spec.maxRetries` unless `spec.resumePolicy: Abort` is set.

With `rollbackOnFailure: true`, a verify mismatch enters `RollingBack` and
attempts to re-activate the previously observed running version. If no previous
version was captured, the CR fails with `RollbackVersionMissing`.

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
