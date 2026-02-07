# Configuration Reference

This document describes the configuration for the Cisco Virtual Kubelet Provider.

## Supported Devices

Currently supported:
- **Cisco Catalyst 8000V** (cat8kv) virtual routers running IOS-XE 17.15.4c
- **Cisco Catalyst 9000** (cat9k) virtual routers running IOS-XE 17.18.2

NOTE: Other versions that support Cisco App Hosting may work but have not been validated

## Device Prerequisites

The following IOS-XE configuration is required on the Catalyst 8000V:

```
! Enable IOx container platform
iox

! Enable RESTCONF API
restconf

! Disable app-hosting verification (required for unsigned containers)
app-hosting verification disable
no app-hosting signed-verification
```

### VirtualPortGroup and DHCP Configuration

Configure a VirtualPortGroup interface to serve as the gateway for container networking, along with a DHCP pool:

```
! Configure VirtualPortGroup0 as the gateway for containers
interface VirtualPortGroup0
 ip address 192.168.1.254 255.255.255.0
 ip nat inside
!
! Configure DHCP pool for app-hosting containers
ip dhcp pool app-hosting
 network 192.168.1.0 255.255.255.0
 default-router 192.168.1.254
 dns-server 192.168.8.8
```

The VirtualPortGroup IP address (192.168.1.254) becomes the default gateway for containers that receive DHCP addresses from this pool.

## Configuration File

The configuration file describes the **device** only.  Runtime settings (node name,
node IP, log level, etc.) are supplied via CLI flags or environment variables.

Default location: `/etc/virtual-kubelet/config.yaml`

## Minimal Configuration Example

```yaml
device:
  driver: XE
  address: "192.168.1.100"
  port: 443
  username: admin
  password: cisco123
  tls:
    enabled: true
    insecureSkipVerify: true
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true
          interface: "0"
          guestInterface: 0
```

## CLI Flags & Environment Variables

Runtime settings are **not** in the config file.  They are passed as flags or env vars:

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--nodename` | `VKUBELET_NODE_NAME` | `cisco-vk-<device-address>` | Kubernetes node name |
| `--config` / `-c` | - | `/etc/virtual-kubelet/config.yaml` | Path to device config file |
| `--kubeconfig` | `KUBECONFIG` | _(in-cluster)_ | Path to kubeconfig file |
| `--log-level` | `LOG_LEVEL` | `info` | Log level: debug, info, warn, error |

Precedence: **flag → environment variable → default**.

## Configuration Fields

### Device Section

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `driver` | string | Yes | - | Driver type: `XE`, `XR`, `NXOS`, `FAKE` |
| `address` | string | Yes | - | Device management IP address |
| `port` | int | No | 443 (TLS) / 80 | RESTCONF API port |
| `username` | string | Yes | - | Device username |
| `password` | string | Yes | - | Device password |
| `podCIDR` | string | No | - | CIDR for static IP allocation |

### TLS Section (`device.tls`)

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | false | Enable HTTPS |
| `insecureSkipVerify` | bool | No | false | Skip certificate verification |

### IOS-XE Networking Section (`device.xe.networking`)

Driver-specific networking lives under the driver key.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `interface.type` | string | Yes | - | `VirtualPortGroup`, `AppGigabitEthernet`, or `Management` |

#### VirtualPortGroup (`device.xe.networking.interface.virtualPortGroup`)

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `dhcp` | bool | No | false | Use DHCP for container IPs |
| `interface` | string | No | "0" | VirtualPortGroup interface number |
| `guestInterface` | int | No | 0 | Guest interface number |

#### AppGigabitEthernet (`device.xe.networking.interface.appGigabitEthernet`)

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `mode` | string | Yes | - | `access` or `trunk` |
| `dhcp` | bool | No | false | DHCP (access mode only) |
| `guestInterface` | int | No | 0 | Guest interface (access mode only) |
| `vlanIf.vlan` | int | No | - | VLAN ID (trunk mode only) |
| `vlanIf.dhcp` | bool | No | false | DHCP (trunk mode only) |
| `vlanIf.guestInterface` | int | No | 0 | Guest interface (trunk mode only) |

#### Management (`device.xe.networking.interface.management`)

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `dhcp` | bool | No | false | Use DHCP for container IPs |
| `guestInterface` | int | No | 0 | Guest interface number |

## Example Pod Manifest

Deploy a container to the cat8kv node:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: dhcp-test-pod
  namespace: default
spec:
  nodeName: cat8kv-node
  containers:
  - name: test-app
    image: flash:/hello-app.iosxe.tar
    resources:
      requests:
        memory: "64Mi"
        cpu: "250m"
      limits:
        memory: "128Mi"
        cpu: "500m"
```

NOTE:  Currently the container image must be pre-loaded onto the device flash storage.

## Verifying Device Configuration

Test RESTCONF connectivity:

```bash
curl -k -u admin:cisco \
  https://192.0.2.24/restconf/data/Cisco-IOS-XE-native:native/hostname
```

Verify IOx is running:

```
cat8kv# show iox-service
```

Check app-hosting status:

```
cat8kv# show app-hosting list
```