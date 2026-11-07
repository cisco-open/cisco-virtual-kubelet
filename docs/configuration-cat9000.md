# Catalyst 9000

This page covers installation on the **Cisco Catalyst 9000** switch series. The typical networking mode on this platform is **AppGigabitEthernet**, the dedicated front-panel port for on-device applications. It supports both **access** and **trunk** (VLAN-tagged) configurations.

## Validated software

| Device | Validated IOS-XE |
|---|---|
| Cisco Catalyst 9000 series | 17.18.2 |

Other IOS-XE versions with App Hosting may work but are not validated.

## Device prerequisites

Apply the following IOS-XE config on the switch.

### Enable secure gNXI for gNOI software lifecycle

Image upgrade and downgrade additionally require secure gNXI with gNOI and
password authentication enabled. An unprovisioned device uses
`gnxi secure-init` as its temporary certificate bootstrap; a device that
already has the intended identity instead uses `gnxi secure-trustpoint` and
`gnxi secure-server`. Do not combine those starting-state command sets, and do
not enable `gnxi secure-client-auth` for a password-only CVK deployment.

Follow the [IOS-XE gNOI Upgrade and Downgrade
Runbook](gnoi-iosxe-upgrade-runbook.md#2-configure-secure-gnxi-on-ios-xe) for
the exact device commands, certificate requirements, Kubernetes objects, and
post-provision checks.

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

### Configure the AppGigabitEthernet port

AppGigabitEthernet is the dedicated on-box port for application networking. It is enabled per-switch.

```
! Enable app-hosting on AppGigabitEthernet port
interface AppGigabitEthernet1/0/1
 switchport mode trunk                 ! or 'access' — see mode details below
 switchport trunk allowed vlan 200     ! only for trunk mode
 no shutdown
```

Adjust the interface number to your chassis. For trunk mode, create the allowed VLAN interfaces elsewhere in the switch config:

```
vlan 200
 name app-hosting

interface Vlan200
 ip address 192.168.200.1 255.255.255.0

ip dhcp pool app-hosting-vlan200
 network 192.168.200.0 255.255.255.0
 default-router 192.168.200.1
 dns-server 8.8.8.8
```

## VK configuration

There are two modes for AppGigabitEthernet: **access** (all traffic on a single VLAN) and **trunk** (containers on a specific tagged VLAN). Trunk is the more common choice on the Catalyst 9000.

### Mode 1 — Trunk (recommended)

Containers attach to a tagged VLAN on the AppGigabitEthernet port. Use this when you want to isolate container traffic on its own VLAN.

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9000-1
  namespace: default
spec:
  driver: XE
  address: "192.168.1.100"       # management IP of the switch
  port: 443
  username: admin
  credentialSecretRef:
    name: cat9000-1-creds        # Secret with key: password
  tls:
    enabled: true
    insecureSkipVerify: false
  # allowUnsignedApps: true      # uncomment when running unsigned packages
                                  # — e.g. your own custom application builds.
                                  # See Troubleshooting → PackagePolicyInvalid.
  xe:
    networking:
      interface:
        type: AppGigabitEthernet
        appGigabitEthernet:
          mode: trunk
          vlanIf:
            dhcp: true              # IPs from the VLAN 200 DHCP pool
            vlan: 200
            guestInterface: 0       # container-side eth index (0 = eth0)
```

The device HTTPS certificate must match its address and be trusted by the
worker. For a private CA, use [Shared TLS](CONFIGURATION.md#shared-tls): managed
`caSecretRef` is introduced in `v2026.11.1` and requires matching CRDs, admission
policies, manager and workers. October `v2026.10.0` installations need system
trust or an explicit custom CA mount.
This app-hosting configuration leaves gNOI in legacy `auto` mode. Before invoking gNOI, configure
[explicit verified gNOI TLS](gnoi-software-lifecycle.md#secure-ios-xe-gnxi).

### Mode 2 — Access

Containers land directly on whatever VLAN the switchport is configured with, no tagging. Useful when one port = one flat container network.

```yaml
spec:
  xe:
    networking:
      interface:
        type: AppGigabitEthernet
        appGigabitEthernet:
          mode: access
          dhcp: true
          guestInterface: 0
```

### AppGigabitEthernet field reference

| Field | Type | Default | Notes |
|---|---|---|---|
| `mode` | enum | — | `access` or `trunk`. |
| `dhcp` | bool | `false` | Applies to access mode only when `vlanIf.vlan` is `0`; otherwise use `vlanIf.dhcp`. |
| `guestInterface` | uint8 (0–3) | `0` | Container-side interface index (0 = eth0). |
| `vlanIf.vlan` | uint16 (0–4094) | — | App-hosting VLAN ID; a nonzero value uses the VLAN-specific settings in access or trunk mode. |
| `vlanIf.dhcp` | bool | `false` | DHCP for trunk mode and access mode with a nonzero VLAN ID. |
| `vlanIf.guestInterface` | uint8 (0–3) | `0` | Container-side interface index on the VLAN. |
| `vlanIf.macForwardingEnabled` | bool | `false` | Pass VLAN MACs into the container (use with care). |
| `vlanIf.multicastEnabled` | bool | `false` | Allow multicast on the VLAN. |
| `vlanIf.mirrorEnabled` | bool | `false` | Mirror the VLAN into the container. |

### Static IPs (no DHCP)

For the nonzero-VLAN configuration below, setting `vlanIf.dhcp: false`
requires `podCIDR`, but this is not an address pool allocator. The current
implementation assigns only the first container a fixed address derived by
adding `10` to the network address's last octet. Separate Pods reuse that
address; later containers receive no static address from this calculation.
Access mode with VLAN `0` does not receive a static address from this path.
Use DHCP for multiple independent workloads. See the
[canonical static-address limitation](CONFIGURATION.md#node-and-pod-topology).

```yaml
spec:
  podCIDR: "192.168.200.0/24"   # first container: 192.168.200.10; reused across Pods
  xe:
    networking:
      interface:
        type: AppGigabitEthernet
        appGigabitEthernet:
          mode: trunk
          vlanIf:
            dhcp: false
            vlan: 200
            guestInterface: 0
```

## Verification

### On the cluster

```bash
kubectl get ciscodevice cat9000-1
# PHASE should reach Ready within ~15s

kubectl get node cat9000-1
# STATUS should be Ready
```

### On the device

```bash
ssh admin@192.168.1.100

# Confirm IOx is enabled
show iox-service

# Confirm the AppGigabitEthernet port is up and the right mode
show run interface AppGigabitEthernet1/0/1
show interface AppGigabitEthernet1/0/1

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

## Other interface modes on Catalyst 9000

The Management interface mode also works on Catalyst 9000 — containers share the management network rather than running on AppGigabitEthernet. See [Configuration → Other interface modes](CONFIGURATION.md#management-interface-both-platforms).

## See also

- [Getting Started](getting-started.md) — end-to-end deployment
- [IOS-XE gNOI Upgrade and Downgrade Runbook](gnoi-iosxe-upgrade-runbook.md) — secure gNXI, certificates, manifests, logs, and verification
- [Configuration](CONFIGURATION.md) — full field reference, applies to any platform
- [Troubleshooting](troubleshooting.md) — VLAN issues, DHCP issues, install failures
