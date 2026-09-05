# Configuration Reference

Every field accepted by a `CiscoDevice` custom resource.

All fields documented below live under `spec` on the CR. The controller reads the CR, strips credentials, and materializes the rest into a ConfigMap that the VK pod reads — you do not edit the ConfigMap directly.

For device-side prerequisites (IOS-XE CLI config, DHCP pools, VLANs, etc.) and per-platform networking examples, see:

- [Catalyst 8000V](configuration-cat8000v.md) — VirtualPortGroup install
- [Catalyst 9000](configuration-cat9000.md) — AppGigabitEthernet install (access and trunk)

## Minimal spec

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9000-1
  namespace: default
spec:
  driver: XE
  address: "192.168.1.100"
  port: 443
  username: admin
  credentialSecretRef:
    name: cat9000-1-creds       # Secret with key: password
  tls:
    enabled: true
    insecureSkipVerify: true
  # allowUnsignedApps: true      # enable when running unsigned packages
                                  # (your own builds, or devices without
                                  # signed-verification enforcement)
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true
          interface: "0"
          guestInterface: 0
```

## CLI flags & environment variables

Runtime settings are **not** in the config file — they are passed as flags or env vars. Precedence: **flag → environment variable → default**.

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--nodename` | `VKUBELET_NODE_NAME` | `cisco-vk-<device-address>` | Kubernetes node name |
| `--config` / `-c` | — | `/etc/virtual-kubelet/config.yaml` | Device config path |
| `--kubeconfig` | `KUBECONFIG` | in-cluster | kubeconfig path |
| `--log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--tls-cert-file` | — | auto-generated | Kubelet HTTPS listener certificate |
| `--tls-key-file` | — | auto-generated | Kubelet HTTPS listener key |
| — | `VK_DEVICE_PASSWORD` | — | Device password. Overrides `device.password` from the config file. Set by the controller when `credentialSecretRef` is used. |

## Configuration fields

### Core

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `driver` | enum | yes | — | `XE`, `NXOS`, `XR`, `OPENCONFIG`, `FAKE`. `XE` is the broadest production driver. `NXOS` supports NX-API CLI app-hosting plus the initial NX-API REST/DME `NXOSConfig` Network as Code slice (`system`, `feature`, `feature_set`, `vlan`, `interface_ethernet`). `XR` and `OPENCONFIG` remain reserved placeholders. |
| `address` | string | yes | — | Management IP or hostname |
| `port` | int (1–65535) | no | 443 (TLS) / 80 | Device API port. IOS-XE uses RESTCONF/NETCONF/gNMI depending on transport; NX-OS uses NX-API CLI and NX-API REST/DME over HTTP(S). |
| `username` | string | yes | — | Device username |
| `password` | string | no | — | Device password. **Do not set in controller mode** — use `credentialSecretRef` instead. |
| `credentialSecretRef` | LocalObjectReference | no | — | Reference to a `Secret` in the same namespace containing key `password`. See [Security](security.md#credential-injection). |

### TLS

```yaml
tls:
  enabled: true
  insecureSkipVerify: true
  certFile: /path/to/client.crt
  keyFile: /path/to/client.key
  caFile:  /path/to/ca.crt
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `tls.enabled` | bool | `false` | Enable HTTPS to the device |
| `tls.insecureSkipVerify` | bool | `false` | Skip certificate verification |
| `tls.certFile` | string | — | Client certificate path; must be configured together with `tls.keyFile`. |
| `tls.keyFile` | string | — | Client key path; must be configured together with `tls.certFile`. |
| `tls.caFile` | string | — | CA bundle path |

### gNOI

| Field | Type | Default | Notes |
|---|---|---|---|
| `gnoi.transportSecurity` | enum | `auto` | `tls` forces TLS; `auto` follows `tls.enabled` and preserves legacy inference. |
| `gnoi.port` | int (1–65535) | legacy inference | Overrides only the gNOI/gNXI endpoint port. Without an override, CVK uses `9339` for TLS, `50052` for plaintext, or preserves an existing nonstandard device port. |
| `xe.gnoi.certificateProvisioning.certificateID` | string | — | IOS-XE only. Required for opt-in provisioning; 1–64 letters, digits, `.`, `_`, or `-`. |
| `xe.gnoi.certificateProvisioning.secretRef.name` | string | — | IOS-XE only. Required same-namespace Secret. `tls.crt` and `ca.crt` are required; `ca.key` is required only to create a new identity, and `bootstrap.crt` is an optional exact leaf pin. |
| `xe.gnoi.certificateProvisioning.replaceTargetCABundle` | bool | — | IOS-XE only. Must be `true`; confirms that `ca.crt` is the complete desired replacement for IOS-XE's shared gNXI/gNMI CA bundle. |

For IOS-XE, explicit `spec.gnoi.transportSecurity: tls` selects its TLS-only
username/password metadata adapter. IOS-XE certificate provisioning is
intentionally scoped under `spec.xe.gnoi`; it requires explicit, verified TLS
in `spec.gnoi`, and `insecureSkipVerify: true` is rejected. Provisioning is
performed only by the gated `ProvisionCertificate` action; read-only
`GNOIOSVerify` never installs a certificate.
See the [secure authentication and provisioning workflow](gnoi-software-lifecycle.md#secure-ios-xe-gnxi)
and its [security guidance](security.md#secure-ios-xe-gnoi). Environment
overrides remain documented in the [CLI reference](cisco-vk-cli.md#additional-environment-variables).

### Node and pod topology

| Field | Type | Default | Notes |
|---|---|---|---|
| `podCIDR` | string | — | CIDR range the VK allocates from when DHCP is off. The VK hands out addresses sequentially from this range to each new container. |
| `labels` | map[string]string | `{}` | Extra labels applied to the virtual node |
| `taints` | []v1.Taint | `[]` | Extra taints applied to the virtual node |
| `maxPods` | int32 | `16` | Maximum pods the device can host |
| `region` | string | — | Populates `topology.kubernetes.io/region` on the node |
| `zone` | string | — | Populates `topology.kubernetes.io/zone` on the node |

### Resource limits

Default and maximum resource bounds reported as node capacity/allocatable and used when a pod omits requests.

```yaml
resourceLimits:
  defaultCPU: "500m"
  defaultMemory: "512Mi"
  defaultStorage: "500Mi"
  maxCPU: "2000m"
  maxMemory: "4Gi"
  maxStorage: "2Gi"
  others:
    maxApps: "4"
```

| Field | Type | Notes |
|---|---|---|
| `resourceLimits.defaultCPU` | string | Default CPU if pod omits requests |
| `resourceLimits.defaultMemory` | string | Default memory if pod omits requests |
| `resourceLimits.defaultStorage` | string | Default ephemeral storage |
| `resourceLimits.maxCPU` | string | Upper limit per container |
| `resourceLimits.maxMemory` | string | Upper limit per container |
| `resourceLimits.maxStorage` | string | Upper limit per container |
| `resourceLimits.others` | map[string]string | Arbitrary custom resources. For NX-OS, `maxApps` overrides the conservative app-hosting slot guard. |

NX-OS maps each Kubernetes container to one NX-OS app-hosting app. CVK defaults
the NX-OS app-slot guard to `4`, matching the observed Nexus 9300v lab ceiling,
and rejects oversized pods before sending config. Raise `resourceLimits.others.maxApps`
only after validating a higher platform-specific limit. NX-OS app-resource
profile values are bounded to the platform's 16-bit CLI fields; CPU milli-units
or memory MiB above `65535` are rejected before any app-hosting configuration is
sent.

### Logging

```yaml
logLevel: info   # debug | info | warn | error
```

The `logLevel` field is passed to the VK pod as the `--log-level` flag on the container.

### App packaging

| Field | Type | Default | Notes |
|---|---|---|---|
| `allowUnsignedApps` | bool | `false` | When `true`, CVK performs two actions: **(1)** on first connect it PUTs `Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/controls` with `sign-verification: false`, disabling the device-level package signature check; **(2)** the reconciler treats `iox-pkg-policy-invalid` during `INSTALLING` as a transient signal rather than a fatal error. Enable for unsigned packages (custom builds or test images). If the device-side PUT fails, a warning is logged and installs may still be blocked by device policy. See [Troubleshooting → PackagePolicyInvalid](troubleshooting.md#packagepolicyinvalid-false-positives). |

### OpenTelemetry topology

```yaml
otel:
  enabled: true
  endpoint: "otel-collector.observability.svc:4317"
  insecure: true
  serviceName: "cisco-network"
  intervalSecs: 60
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `otel.enabled` | bool | `false` | Toggle topology trace emission |
| `otel.endpoint` | string | — | OTLP gRPC collector endpoint. **Required** when `enabled: true`. |
| `otel.insecure` | bool | `true` | Skip TLS on the gRPC connection |
| `otel.serviceName` | string | `"cisco-network"` | Base service name. The device hostname is appended → `"cisco-network.<hostname>"`. |
| `otel.intervalSecs` | int (min 10) | `60` | Interval between trace emissions |

OTEL export only happens when the driver implements `TopologyProvider` (the IOS-XE driver does). See [Observability → OpenTelemetry](observability.md#opentelemetry-topology-export).

### XEConfig — IOS-XE networking

Required when `driver: XE`. The `type` field selects which mode is in use; exactly one of the mode-specific blocks must be set to match.

```yaml
xe:
  networking:
    interface:
      type: VirtualPortGroup      # | AppGigabitEthernet | Management
      virtualPortGroup: { ... }
      appGigabitEthernet: { ... }
      management: { ... }
```

| `type` | Typical platform | Details |
|---|---|---|
| `VirtualPortGroup` | Catalyst 8000V | [configuration-cat8000v.md](configuration-cat8000v.md) |
| `AppGigabitEthernet` | Catalyst 9000 | [configuration-cat9000.md](configuration-cat9000.md) |
| `Management` | Either (containers on management network) | [Below](#management-interface-both-platforms) |

## Management interface (both platforms)

The Management interface mode places containers on the device's management network rather than on a dedicated App Hosting interface. It is supported on both Catalyst 8000V and Catalyst 9000.

```yaml
xe:
  networking:
    interface:
      type: Management
      management:
        dhcp: true
        guestInterface: 0
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `dhcp` | bool | `false` | DHCP on the management interface. If `false`, allocate from `podCIDR`. |
| `guestInterface` | uint8 (0–3) | `0` | Container-side interface index (0 = eth0). |

Note that putting containers on the management network exposes them directly to management traffic and any ACLs in place there. For workload isolation, prefer VirtualPortGroup (on Catalyst 8000V) or AppGigabitEthernet (on Catalyst 9000).

## Example pod manifest

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: hello-app
  namespace: default
spec:
  nodeName: cat9000-1
  tolerations:
  - key: virtual-kubelet.io/provider
    operator: Exists
  containers:
  - name: hello
    image: flash:/hello-app.iosxe.tar
    resources:
      requests:
        memory: "256Mi"
        cpu: "500m"
      limits:
        memory: "512Mi"
        cpu: "1000m"
```

!!! note
    The container image reference can be either:

    - **A flash path** (`flash:/...`, `bootflash:/...`) — image tar must be pre-loaded on the device.
    - **An HTTP(S) URL** — the VK attempts a device-native pull first; if the platform cannot pull, it downloads the image itself and installs from flash (see [Image delivery](#image-delivery) below).

    Package policy (signed vs unsigned) can be managed via `spec.allowUnsignedApps: true` in the `CiscoDevice` spec — CVK configures the device and adjusts reconciler behaviour automatically. The equivalent IOS-XE CLI is `no app-hosting signed-verification`.

## Image delivery

By default the VK passes the container image URL directly to the IOS-XE install RPC and waits for the app to reach `RUNNING`. On platforms that support device-native HTTP pulls, no extra steps are needed.

On platforms that cannot pull from HTTP URLs, the VK automatically falls back to a **copy-then-install** path: it downloads the image to device flash via the `copy` RESTCONF RPC, then reinstalls from the local flash path. This fallback can take several minutes (the copy RPC is synchronous and blocks until the device finishes downloading).

### `imagePullPolicy`

Behaves analogously to the Kubernetes `imagePullPolicy` field, with one important platform constraint:

**IOS-XE App Hosting does not cache images on flash when using the device-native pull path.** When the device installs an app directly from an HTTP URL (`app-hosting install appid ... package <url>`), the image is fetched and loaded into the container runtime without leaving a copy on flash. This means `IfNotPresent` and `Always` are **functionally identical on the device-native pull path** — the image is always re-downloaded from the URL on each deploy.

The `IfNotPresent` flash-cache optimization only applies to the **copy fallback path**: if the VK previously copied a tar to flash (because the device-native pull timed out), that cached copy is reused on subsequent deploys.

| Value | Behaviour |
|---|---|
| `Always` | Always attempt device-native pull; if that times out, always re-download via copy RPC. The flash cache is never reused. |
| `IfNotPresent` (default) | Always attempt device-native pull first. If that times out, try installing from a previously cached flash tar before re-downloading. **If the device-native pull succeeds, no flash cache is created and no difference from `Always` is observable.** |
| `Never` | Image must already exist on flash. HTTP URLs are rejected immediately — no install RPC is issued. |

To reliably benefit from `IfNotPresent` caching, deliberately use the copy path: either configure a short `imagePullPolicy` timeout so the device-native pull always times out quickly (triggering copy fallback), or pre-stage the image using `imagePullPolicy: Never` with a flash path after a first manual copy.

### `imagePullSecrets`

When the image is served from a registry that requires authentication, provide credentials via a Kubernetes `Secret` and reference it from `spec.imagePullSecrets`. The VK embeds the credentials as HTTP basic auth in the copy RPC URL.

```yaml
spec:
  imagePullSecrets:
  - name: registry-credentials
```

The referenced Secret should be of type `kubernetes.io/dockerconfigjson` or contain a `token` key. The VK tries the following fields in priority order: `token` key → `.dockerconfigjson` `identitytoken` → `.dockerconfigjson` `registrytoken` → `.dockerconfigjson` `username`/`password` → `.dockerconfigjson` `auth` (base64 `user:pass`).

!!! note
    Authentication is applied only to the copy RPC (device-initiated download). The device-native pull path does not support credential injection via this mechanism.

### Pod annotations for the copy path

Two optional annotations tune the copy fallback behaviour:

| Annotation | Default | Description |
|---|---|---|
| `cisco.io/apphost-package-dest` | `flash:/virtual-kubelet/<app-id>.tar` | On-device flash path where the image is copied. Must use an IOS-XE filesystem prefix (`flash:`, `bootflash:`, `harddisk:`, `usb:`, `nvram:`); each path segment may contain only ASCII letters, digits, `-`, `.`, `_`, or `~`. |
| `cisco.io/apphost-package-timeout` | `180s` (3 min) | How long to wait for the app to reach `RUNNING`. Accepts Go duration strings (`3m`, `300s`) or bare seconds (`180`). Clamped to [10s, 30m]. |

```yaml
metadata:
  annotations:
    cisco.io/apphost-package-dest:    "flash:/virtual-kubelet/my-app.tar"
    cisco.io/apphost-package-timeout: "5m"
```

### Kubernetes pod events

During image delivery the VK emits pod events visible in `kubectl describe pod`:

| Event | Reason | When |
|---|---|---|
| Normal | `Pulling` | Device-native HTTP pull started |
| Warning | `ImagePullFallback` | Device-native pull timed out; copy fallback begins |
| Normal | `Copying` | Copy RPC started (shows source URL and destination path) |
| Normal | `Pulled` | Image successfully copied to device flash |
| Normal | `Started` | App reached RUNNING via copy fallback |

## See also

- [Catalyst 8000V](configuration-cat8000v.md) — VirtualPortGroup install walkthrough
- [Catalyst 9000](configuration-cat9000.md) — AppGigabitEthernet install walkthrough
- [Examples](https://github.com/cisco-open/cisco-virtual-kubelet/tree/main/examples/configs) — full working configs for every interface mode
- [Security](security.md) — credential injection via Secrets
- [Observability](observability.md) — OTEL and metrics configuration in detail
- [Troubleshooting](troubleshooting.md) — common configuration issues
