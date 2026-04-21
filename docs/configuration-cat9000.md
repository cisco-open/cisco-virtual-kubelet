# Catalyst 9000

This page covers installation on the **Cisco Catalyst 9000** switch series. The typical networking mode on this platform is **AppGigabitEthernet**, the dedicated front-panel port for on-device applications. It supports both **access** and **trunk** (VLAN-tagged) configurations.

## Validated software

| Device | Validated IOS-XE |
|---|---|
| Cisco Catalyst 9000 series | 17.18.2 |

Other IOS-XE versions with App Hosting may work but are not validated.

## Device prerequisites

Apply the following IOS-XE config on the switch.

### Enable IOx, RESTCONF, and App Hosting

```
! Enable IOx (on-device container platform)
iox

! Enable the RESTCONF HTTPS listener
restconf

! Required if you plan to run unsigned container packages.
! Alternatively, enforce per-device via spec.allowUnsignedApps: true.
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
    insecureSkipVerify: true
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
| `dhcp` | bool | `false` | Access-mode DHCP. |
| `guestInterface` | uint8 (0–3) | `0` | Container-side interface index (0 = eth0). |
| `vlanIf.vlan` | uint16 (0–4094) | — | Tagged VLAN ID (trunk mode only). |
| `vlanIf.dhcp` | bool | `false` | DHCP on the tagged VLAN. |
| `vlanIf.guestInterface` | uint8 (0–3) | `0` | Container-side interface index on the VLAN. |
| `vlanIf.macForwardingEnabled` | bool | `false` | Pass VLAN MACs into the container (use with care). |
| `vlanIf.multicastEnabled` | bool | `false` | Allow multicast on the VLAN. |
| `vlanIf.mirrorEnabled` | bool | `false` | Mirror the VLAN into the container. |

### Static IPs (no DHCP)

If you'd rather allocate IPs yourself, set `dhcp: false` on the chosen mode and specify `podCIDR` at the spec level. The VK allocates addresses sequentially from that range.

```yaml
spec:
  podCIDR: "192.168.200.0/24"
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
curl -k -u admin:<password> \
  https://192.168.1.100/restconf/data/Cisco-IOS-XE-native:native/hostname
```

## Other interface modes on Catalyst 9000

The Management interface mode also works on Catalyst 9000 — containers share the management network rather than running on AppGigabitEthernet. See [Configuration → Other interface modes](CONFIGURATION.md#management-interface-both-platforms).

## See also

- [Getting Started](getting-started.md) — end-to-end deployment
- [Configuration](CONFIGURATION.md) — full field reference, applies to any platform
- [Troubleshooting](troubleshooting.md) — VLAN issues, DHCP issues, install failures
