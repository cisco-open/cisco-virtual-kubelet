# Configuration Reference

This document describes the configuration for the Cisco Virtual Kubelet Provider.

## Supported Devices

Currently supported:
- **Cisco Catalyst 8000V** (cat8kv) virtual routers running IOS-XE 17.x+

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

The provider uses a YAML configuration file with two sections:
- **kubelet**: Virtual Kubelet node settings
- **device**: Cisco device connection settings

Default location: `/etc/cisco-vk/config.yaml`

## Minimal Configuration Example

```yaml
device:
  name: cat8kv-router
  driver: XE
  address: "192.0.2.24"
  port: 443
  username: admin
  password: cisco
  tls:
    enabled: true
    insecureSkipVerify: true
  networking:
    dhcpEnabled: true
    virtualPortGroup: "0"
    defaultVRF: ""

kubelet:
  node_name: "cat8kv-node"
  namespace: ""
  update_interval: "30s"
  os: "Linux"
  node_internal_ip: "192.0.2.24"
```

## Configuration Fields

### Kubelet Section

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `node_name` | string | Yes | - | Kubernetes node name |
| `namespace` | string | No | "" | Namespace filter (empty = all) |
| `update_interval` | string | No | "30s" | Node status update interval |
| `os` | string | No | "Linux" | Operating system label |
| `node_internal_ip` | string | No | - | Internal IP for the node |

### Device Section

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | string | Yes | - | Device identifier |
| `driver` | string | Yes | - | Driver type (use `XE` for cat8kv) |
| `address` | string | Yes | - | Device management IP address |
| `port` | int | No | 443 | RESTCONF API port |
| `username` | string | Yes | - | Device username |
| `password` | string | Yes | - | Device password |

### TLS Section

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | true | Enable HTTPS |
| `insecureSkipVerify` | bool | No | false | Skip certificate verification |

### Networking Section

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `dhcpEnabled` | bool | No | false | Use DHCP for container IPs |
| `virtualPortGroup` | string | No | "0" | VirtualPortGroup interface number |
| `defaultVRF` | string | No | "" | VRF for container traffic |

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

The container image must be pre-loaded onto the device flash storage.

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