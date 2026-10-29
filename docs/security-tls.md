# TLS and transport trust

For installers connecting devices and network operators changing certificates
or image sources. Choose trust independently for each connection; a working
RESTCONF connection does not establish gNOI or kubelet trust. Keep the chart,
CRDs and worker image matched. The public-CA Secret projection below is
introduced in `v2026.11.0`; October `v2026.10.0` installations need the matching
CRD, admission-policy, manager and worker upgrade before using it.

| Connection | Configure and verify |
|---|---|
| Worker to device HTTPS | [`spec.tls`](#device-https): verified system trust, or an explicitly configured CA |
| Worker to IOS-XE gNOI | [Dedicated TLS/provisioning](#secure-ios-xe-gnoi): one trust source and explicit secure transport |
| API server or collector to worker | [Kubelet serving certificate](#kubelet-side-tls): client trust and matching address SANs |
| Worker/device to image source | [Image transport](#software-upgrade-image-transport): server trust as well as content integrity |
| NETCONF diagnostic probe over SSH | [Host-key pinning](#ssh-host-keys): a separately mounted `known_hosts` file |

Use the [credential guide](security-credentials.md) for passwords. The sections
below describe the trust and key-handling contract; linked runbooks own the
full device-command and operation sequence.

## Device HTTPS

Device-side TLS is configured under `spec.tls`. With verified TLS and no custom
CA, the device certificate must chain to the worker's system trust store and
match `spec.address`.

For controller-managed workers using a private CA, `v2026.11.0` supports a
same-namespace public-CA Secret:

```bash
kubectl -n default create secret generic switch-https-public-ca \
  --from-file=ca.crt=/path/to/trusted-device-ca.crt
```

```yaml
spec:
  tls:
    enabled: true
    insecureSkipVerify: false
    caSecretRef:
      name: switch-https-public-ca
```

!!! info "Introduced in v2026.11.0; upgrade October installations together"
    Upgrade the CRD, native admission contract, manager and workers together
    before using `caSecretRef`. The controller validates and projects only
    public CA certificates; it does not install a certificate on the switch
    or replace dedicated gNOI trust. Read the [projection, rotation and rollback
    requirements](CONFIGURATION.md#shared-tls). October deployments need a
    system-trusted device certificate or a custom Deployment with mounted CA
    files.

For a custom Deployment with explicit mounts, the `CiscoDevice` fragment is
below. In a standalone configuration file, put these same TLS fields under
`device.tls`, not `spec.tls`.

```yaml
spec:
  tls:
    enabled: true
    insecureSkipVerify: false
    caFile: /etc/ssl/certs/corp-ca.crt
    certFile: /etc/ssl/cvk/client.crt
    keyFile: /etc/ssl/cvk/client.key
```

| Field | Notes |
|---|---|
| `enabled` | Turn on HTTPS. Required in production. |
| `insecureSkipVerify` | `true` disables certificate verification. Acceptable for lab devices with self-signed certs; **do not** use in production. |
| `caSecretRef.name` | Projects the same-namespace Secret's validated public `ca.crt`; introduced in `v2026.11.0`, mutually exclusive with `caFile`, and requires verified TLS. |
| `caFile` | Trust anchor for verifying the device certificate. |
| `certFile`, `keyFile` | Optional client certificate — required only if the device enforces mutual TLS. |

The `certFile` / `keyFile` / `caFile` paths refer to files **inside the VK pod**.
Mount private certificates and keys from a Secret-backed volume; public CA
material may come from a Secret or ConfigMap. The controller does not currently
auto-mount these shared paths—you need a custom Deployment or chart extension.

The configdriver transports, MDT-over-gNMI telemetry, and the NX-API client on
NX-OS honour the full `spec.tls` block through one shared builder. IOS-XE
app-hosting RESTCONF constructs its TLS client separately. gNOI resolves port,
authentication, and exactly one trust source together: the system root pool,
verified shared `spec.tls`, dedicated `spec.gnoi.tls`, or IOS-XE provisioning
trust. Shared and gNOI-specific file configurations reject a `certFile` or
`keyFile` without its matching pair. During migration, remove an obsolete
orphan path or supply the complete pair: a lone shared `certFile` or `keyFile`
that an older release silently ignored now fails startup for every consumer of
the shared TLS builder.

## Secure IOS-XE gNOI

### Choose the gNOI trust source

With explicit `gnoi.transportSecurity: tls`, CVK sends the device username and
password as separate per-RPC metadata fields over verified TLS. Pre-existing
configurations that do not opt in retain their legacy HTTP Basic metadata
behavior, including on plaintext listeners, solely for compatibility. Migrate
them to explicit secure gNOI. Avoid metadata logging and limit access to the
credential Secret.

Kubernetes-specific `spec.gnoi.tls` accepts only a same-namespace `secretRef`.
Its Secret requires `ca.crt`; optional mutual TLS requires both `tls.crt` and
`tls.key`. The controller validates the PEM material, projects only those fixed
keys read-only, and rolls the worker after a valid Secret change when gNOI is
enabled in per-device topology. With global gNOI disablement, the per-device
worker remains but omits the trust projection and does not roll for trust-Secret
changes. Aggregated config-only topology has no per-device worker. Direct local
configuration uses `caFile` and an optional `certFile`/`keyFile` pair instead;
paths are rejected by the Kubernetes API. There is intentionally no gNOI
`enabled` or `insecureSkipVerify` field.

The controller manager watches these Secrets through its cached Kubernetes
client. Complete Secret objects—including unrecognized keys and provisioning
`ca.key` material—may therefore remain in the manager cache and process memory
for its lifetime, even though reconciliation acts only on the documented keys.
Manager RBAC, logs, debugging access, and pod exec are part of the trust
boundary. Secret bytes are never copied into the worker ConfigMap, status,
events, or logs. Restrict Secret reads and access to both the manager and the
per-device worker, where private keys are mounted only when required.

### Provisioning and signer retirement

The dedicated provisioning Secret referenced by
`spec.xe.gnoi.certificateProvisioning.secretRef` always supplies `tls.crt` and
`ca.crt`. It automatically becomes the isolated gNOI bootstrap and
steady-state trust source, so it cannot be combined with `spec.gnoi.tls`.
An insecure top-level `spec.tls` may remain for an unrelated legacy transport
only because gNOI never inherits it in this mode. `bootstrap.crt` is an
optional exact pin for the temporary IOS-XE leaf, and
`ca.key` is used only by the gated `ProvisionCertificate` action. The key is
kept separate from the read-only trust bundle behind a `CertificateSigner`
boundary. The included local PEM signer is transitional and can sign a new CSR
for a corrected immutable action after a definitive pre-Install failure;
create-only device Install remains at-most-once per action. The parsed key stays
in worker process memory until exit or a completed key-free rollout. No
external CA, KMS, or HSM signer backend is included. Each action must bind the
configured certificate ID and the lowercase
SHA-256 of the exact `tls.crt || ca.crt` bytes; a stale or mismatched public
intent is rejected before the worker acquires a gNOI client or marks the action
Running. `ca.key` and `bootstrap.crt` are projected only while write-class gNOI
is enabled and a non-empty `ca.key` is present. The signing key must belong to
a dedicated intermediate CA; root keys are rejected. Enable Secret
encryption at rest, restrict Secret reads and pod exec, and audit both.
Secret projection is not an authorization boundary: the standard VK
ClusterRole can read Secrets cluster-wide so VK nodes can serve pod volumes.
After provisioning is verified, promptly remove `ca.key` and `bootstrap.crt`;
disable the write-class gate too unless other write actions are still needed.
Never create or update a signer-bearing Secret with client-side
`kubectl apply`: its last-applied annotation retains the submitted key even
after `.data.ca.key` is removed. Use a secret manager, direct create, or
server-side apply, and follow the
[key-free cleanup procedure](gnoi-iosxe-upgrade-runbook.md#remove-bootstrap-secrets-immediately)
to remove any legacy annotation and prove the replacement worker is key-free.
Keep the public certificate and CA bundle for read-only gNOI trust. Deployments
use a non-overlapping `Recreate` strategy while a signer may be resident, for
the first key-free cleanup rollout, and whenever write-class gNOI actions or
IOS-XE software upgrades are enabled. This prevents two worker generations
from concurrently acting on the same durable mutation claim. Normal rolling
updates resume only after signer cleanup is complete and both mutation gates,
or gNOI globally, are disabled. Disabling gNOI entirely suppresses the
provisioning Secret mount and its rollout annotation.

The installed identity and CA bundle are scoped to gNOI in CVK, but IOS-XE
shares them with gNMI. Installation can restart gNXI, replace peer trust, and
change the server certificate seen by other clients; schedule it as a device
change. CVK does not reconfigure RESTCONF, NETCONF, app-hosting, or telemetry
client settings, so ensure every gNMI client already trusts the new issuer.

See the [secure gNOI and CSR provisioning workflow](gnoi-software-lifecycle.md#secure-ios-xe-gnxi)
for the complete Secret contract, one-shot action, trust-bundle warning, and
verification sequence.

## Hosted application image input

On NX-OS, a pod's `image` value is treated as untrusted CLI input. CVK accepts
only a single validated `bootflash:` path or HTTP(S) URL token before issuing
an app-hosting install command; whitespace, control characters, command
separators, user information, and other unsafe syntax are rejected before any
NX-API request. Prefer pre-staged `bootflash:` packages for production use.

The IOS XE `cisco.io/apphost-package-dest` annotation accepts only
`bootflash:`, `harddisk:`, `flash:`, `nvram:`, or `usb:` followed by path
segments using ASCII letters, digits, `-`, `.`, `_`, or `~`. Empty, `.` and
`..` segments are rejected. Rename existing destination paths containing
spaces, `+`, control characters, or other punctuation before upgrading.

## Software-upgrade image transport

URL image resolution is capped at 8 GiB by default and uses private-mode files
inside the worker's `/tmp` `emptyDir`; symlink and non-regular cache entries are
rejected. Configure the cap with `gnoi.softwareUpgrade.maxImageBytes`, budget
ephemeral storage for one full image plus normal pod and filesystem overhead,
and remember that SHA-256 integrity does not make plaintext HTTP, FTP, or TFTP
confidential or authenticate their servers. CVK verifies a private temporary
file and atomically promotes that same file to its digest cache, avoiding a
second full-image copy. Prefer HTTPS or host-key-verified SFTP/SCP for
production image repositories. Pod exec and node-root access remain inside the
image-file trust boundary.

## SSH host keys

NETCONF and the CLI side-channel ride SSH, not TLS. Pinning uses a standard
OpenSSH `known_hosts` file (multi-host files cover a fleet). Ship it into the
VK pod with a Secret- or ConfigMap-backed volume and point
`CONFIG_SSH_KNOWN_HOSTS` at the mounted path; the NETCONF diagnostic probe
(`CONFIG_NETCONF_PROBE`) then pins its dials against it and refuses to dial
unpinned if the file fails to load. Without the variable the probe keeps the
historical lab default of accepting any presented key — as with
`insecureSkipVerify`, do not rely on that in production.

## Kubelet-side TLS

The VK runs its own HTTPS listener on `:10250`. It first uses the configured
serving certificate/key, defaulting to `/etc/virtual-kubelet/tls/tls.crt` and
`tls.key`. If both are absent, it generates a self-signed pair under
`/var/lib/virtual-kubelet/tls/`; exactly one configured file being present is an
error. Supply a pair trusted by the connecting client, with matching address
SANs, using:

```
--tls-cert-file /etc/kubelet/tls.crt
--tls-key-file  /etc/kubelet/tls.key
```

Configure the API server or metrics collector's trust for this serving
certificate as appropriate to its connection path. Avoid disabling kubelet
certificate verification cluster-wide for one virtual Node. TLS trust alone
does not provide hosted-application streaming: logs, exec, attach and
port-forward return HTTP 501. Use `kubectl logs` on the **worker Pod**, which
runs on an ordinary Kubernetes node, for CVK diagnostics. Device/Node metrics
also do not provide per-Pod resource usage for HPA/VPA; see
[Observability](observability.md#stats-summary).

## Verify each connection separately

A Ready app-hosting worker confirms neither dedicated gNOI trust nor a metrics
collector's trust of the kubelet listener. Check the relevant worker's
sanitized connection errors, perform the read-only probe documented for that
transport, and confirm the certificate matches the address being used. After
gNOI provisioning, complete the [key-free cleanup and verification](gnoi-iosxe-upgrade-runbook.md#remove-bootstrap-secrets-immediately)
before treating the change as finished.

For field definitions use [Configuration](CONFIGURATION.md#shared-tls); for
operation procedures use the [gNOI runbook](gnoi-iosxe-upgrade-runbook.md).
Continue with [authorization](security-authorization.md) or return to the
[security overview](security.md).
