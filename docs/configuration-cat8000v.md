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

! Required if you plan to run unsigned container packages.
! Alternatively, enforce per-device via spec.allowUnsignedApps: true.
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
    insecureSkipVerify: true
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

See [Security → Credential injection](security.md#credential-injection) for creating the Secret.

### VirtualPortGroup field reference

| Field | Type | Default | Notes |
|---|---|---|---|
| `dhcp` | bool | `false` | When `true`, each container gets an IP from the pool on this VirtualPortGroup. When `false`, the VK allocates sequentially from `podCIDR` at the top level of the spec. |
| `interface` | string | — | VirtualPortGroup number (`"0"` for `VirtualPortGroup0`). |
| `guestInterface` | uint8 (0–3) | `0` | Container-side interface index (0 = eth0 inside the container). |

### Static IPs (no DHCP)

If you'd rather allocate IPs yourself, set `dhcp: false` and specify `podCIDR` at the spec level. The VK allocates addresses sequentially from that range.

```yaml
spec:
  podCIDR: "10.0.0.0/24"   # VK hands out 10.0.0.1, 10.0.0.2, ...
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
curl -k -u admin:<password> \
  https://192.168.1.100/restconf/data/Cisco-IOS-XE-native:native/hostname
```

## Other interface modes on Catalyst 8000V

The Management interface mode also works on Catalyst 8000V — containers share the device's management network instead of a dedicated VirtualPortGroup. See [Configuration → Other interface modes](CONFIGURATION.md#management-interface-both-platforms).

## See also

- [Getting Started](getting-started.md) — end-to-end deployment
- [Configuration](CONFIGURATION.md) — full field reference, applies to any platform
- [Troubleshooting](troubleshooting.md) — DHCP issues, IP discovery, install failures
