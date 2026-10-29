# Catalyst 8000V

This page covers installation on the **Cisco Catalyst 8000V virtual router**. The typical networking mode on this platform is **VirtualPortGroup** — a logical L3 interface that containers share, with IPs handed out by a DHCP pool on the device.

## Validated software

| Device | Validated IOS-XE |
|---|---|
| Cisco Catalyst 8000V | 17.15.4c |

Other IOS-XE versions with App Hosting may work but are not validated.

## Device prerequisites

Apply the following IOS-XE config on the device.

### Enable IOx, RESTCONF, and App Hosting

```
! Enable IOx (on-device container platform)
iox

! Enable the RESTCONF HTTPS listener
restconf
```

Keep package signing enabled for signed packages. Only when deliberately using
unsigned lab/custom packages, either set `spec.allowUnsignedApps: true` or
configure both device controls below. Confirm the effective runtime state with
`show app-hosting infra`; see [package policy](troubleshooting.md#packagepolicyinvalid-false-positives).

```
app-hosting verification disable
no app-hosting signed-verification
```

### Create the VirtualPortGroup and DHCP pool

The VirtualPortGroup is the device's logical L3 interface used by App Hosting. Its IP address becomes the default gateway for every container that uses DHCP on this device.

```
interface VirtualPortGroup0
 ip address 192.168.1.254 255.255.255.0
 ip nat inside

ip dhcp pool app-hosting
 network 192.168.1.0 255.255.255.0
 default-router 192.168.1.254
 dns-server 8.8.8.8
```

Adjust the network and DNS server to match your environment.

## VK configuration

### Minimal CiscoDevice (controller-managed mode)

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat8kv-1
  namespace: default
spec:
  driver: XE
  address: "192.168.1.100"       # management IP of the router
  port: 443
  username: admin
  credentialSecretRef:
    name: cat8kv-1-creds         # Secret with key: password
  tls:
    enabled: true
    insecureSkipVerify: false
  # allowUnsignedApps: true      # uncomment when running unsigned packages
                                  # — e.g. your own custom application builds.
                                  # See Troubleshooting → PackagePolicyInvalid.
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true               # container IPs from the DHCP pool above
          interface: "0"            # VirtualPortGroup0
          guestInterface: 0         # container-side eth index (0 = eth0)
```

See [Security → Credential injection](security-credentials.md#credential-injection) for
creating the Secret. The device HTTPS certificate must match its address and
be trusted by the worker. For a private CA, use [Shared TLS](CONFIGURATION.md#shared-tls):
managed `caSecretRef` is introduced in `v2026.11.0` and requires matching CRDs, admission
policies, manager and workers. October `v2026.10.0` installations need system
trust or an explicit custom CA mount.
Before invoking gNOI, configure
[explicit verified gNOI TLS](gnoi-software-lifecycle.md#secure-ios-xe-gnxi).

### VirtualPortGroup field reference

| Field | Type | Default | Notes |
|---|---|---|---|
| `dhcp` | bool | `false` | When `true`, the device uses DHCP on this VirtualPortGroup. When `false`, CVK uses the limited static `podCIDR` calculation described below. |
| `interface` | string | — | VirtualPortGroup number (`"0"` for `VirtualPortGroup0`). |
| `guestInterface` | uint8 (0–3) | `0` | Container-side interface index (0 = eth0 inside the container). |

### Static IPs (no DHCP)

Setting `dhcp: false` requires `podCIDR`, but this is not an address pool
allocator. The current implementation assigns only the first container a
fixed address derived by adding `10` to the network address's last octet.
Separate Pods reuse that address; later containers receive no static address
from this calculation. Use DHCP for multiple independent workloads. See the
[canonical static-address limitation](CONFIGURATION.md#node-and-pod-topology).

```yaml
spec:
  podCIDR: "10.0.0.0/24"   # first container: 10.0.0.10; not unique across Pods
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: false
          interface: "0"
          guestInterface: 0
```

## Verification

### On the cluster

```bash
kubectl get ciscodevice cat8kv-1
# PHASE should reach Ready within ~15s

kubectl get node cat8kv-1
# STATUS should be Ready
```

### On the device

```bash
ssh admin@192.168.1.100

# Confirm IOx is enabled
show iox-service

# After a pod is scheduled: see the container
show app-hosting list
show app-hosting detail appid cvk00000_<pod-uid>
```

The container's `application-state` should transition through `DEPLOYED → ACTIVATED → RUNNING`.

### RESTCONF reachability (from any host)

```bash
curl --cacert /path/to/trusted-device-ca.crt --user admin \
  https://192.168.1.100/restconf/data/Cisco-IOS-XE-native:native/hostname
```

## Other interface modes on Catalyst 8000V

The Management interface mode also works on Catalyst 8000V — containers share the device's management network instead of a dedicated VirtualPortGroup. See [Configuration → Other interface modes](CONFIGURATION.md#management-interface-both-platforms).

## See also

- [Getting Started](getting-started.md) — end-to-end deployment
- [Configuration](CONFIGURATION.md) — full field reference, applies to any platform
- [Troubleshooting](troubleshooting.md) — DHCP issues, IP discovery, install failures
