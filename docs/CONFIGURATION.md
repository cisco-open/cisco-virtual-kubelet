# Configuration Reference

This document describes all configuration options for the Cisco Virtual Kubelet Provider.

## Configuration File

The provider uses a YAML configuration file. Default location: `/etc/cisco-vk/config.yaml`

### Complete Configuration Example

```yaml
# Node name as it will appear in Kubernetes
nodeName: cisco-switch-01

# Device configurations (one or more devices per provider instance)
devices:
  - name: c9k-primary          # Unique identifier for this device
    type: c9k                   # Device type: c9k, c8k, or c8kv
    address: 192.168.1.100      # Device management IP or hostname
    port: 443                   # RESTCONF port (default: 443)
    username: admin             # Device username
    password: cisco123          # Device password
    useHTTPS: true              # Use HTTPS for RESTCONF (recommended)
    verifyTLS: false            # Verify TLS certificates
    timeout: 30                 # Connection timeout in seconds

# Network configuration for containers
networkConfig:
  defaultGateway: "192.168.1.1"   # Default gateway for containers
  subnetMask: "255.255.255.0"     # Subnet mask
  ipPoolStart: "192.168.1.200"    # Start of IP pool for containers
  ipPoolEnd: "192.168.1.210"      # End of IP pool
  vlanID: 100                     # VLAN ID for container traffic
  managementInterface: 0          # Management interface number

# Default resource allocations for containers
resourceDefaults:
  cpuUnits: 1000                 # CPU units (1000 = 1 core)
  memoryMB: 512                  # Memory in MB
  diskMB: 1024                   # Persistent disk in MB
  vcpu: 1                        # Number of vCPUs
  profile: "custom"              # Resource profile name
```

## Environment Variables

All settings can also be configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `CISCO_CONFIG_PATH` | Path to configuration file | `/etc/cisco-vk/config.yaml` |
| `NODE_NAME` | Kubernetes node name | (required) |
| `KUBECONFIG` | Path to kubeconfig | `~/.kube/config` |
| `CISCO_DEVICE_ADDRESS` | Device IP address | (from config file) |
| `CISCO_DEVICE_USERNAME` | Device username | (from config file) |
| `CISCO_DEVICE_PASSWORD` | Device password | (from config file) |
| `APISERVER_CERT_LOCATION` | TLS certificate path | (optional) |
| `APISERVER_KEY_LOCATION` | TLS private key path | (optional) |
| `VKUBELET_POD_IP` | Internal IP for the node | `127.0.0.1` |
| `LOG_LEVEL` | Logging level | `info` |

## Command-Line Flags

```
Usage: cisco-vk [flags]

Flags:
      --config string        Path to provider configuration file
      --kubeconfig string    Path to kubeconfig file
      --nodename string      Kubernetes node name (required)
      --os string            Operating system (default "Linux")
      --internal-ip string   Internal IP address (default "127.0.0.1")
      --port int             Kubelet API server port (default 10250)
      --cert string          Path to TLS certificate
      --key string           Path to TLS private key
      --log-level string     Log level: debug, info, warn, error (default "info")
      --disable-tls          Disable TLS for API server
      --version              Show version information
```

## Device Types

### Catalyst 9000 Series (c9k)

```yaml
devices:
  - name: c9k-switch
    type: c9k
    address: 192.168.1.100
    port: 443
    username: admin
    password: cisco
    useHTTPS: true
    verifyTLS: false
```

**Supported Models**: C9300, C9400, C9500, C9600

**Requirements**:
- IOS-XE 17.x or later
- IOx enabled
- Minimum 8GB flash storage
- App-hosting license

### Catalyst 8000 Series (c8k)

```yaml
devices:
  - name: c8k-router
    type: c8k
    address: 192.168.2.100
    port: 443
    username: admin
    password: cisco
    useHTTPS: true
    verifyTLS: false
```

**Supported Models**: C8200, C8300, C8500

### Catalyst 8000V (c8kv)

```yaml
devices:
  - name: c8kv-virtual
    type: c8kv
    address: 192.168.3.100
    port: 443
    username: admin
    password: cisco
    useHTTPS: true
    verifyTLS: false
```

## Network Configuration Options

### Management Interface

The management interface connects the container to the network:

```yaml
networkConfig:
  managementInterface: 0      # Interface 0 is typically AppGigabitEthernet
```

### IP Address Pool

Define a range of IP addresses for containers:

```yaml
networkConfig:
  ipPoolStart: "192.168.1.200"
  ipPoolEnd: "192.168.1.210"
```

The provider automatically assigns IPs from this pool to new containers.

### VLAN Configuration

```yaml
networkConfig:
  vlanID: 100                 # VLAN for container traffic
```

## Resource Profiles

### Default Resources

```yaml
resourceDefaults:
  cpuUnits: 1000              # Equivalent to 1 CPU core
  memoryMB: 512               # 512 MB RAM
  diskMB: 1024                # 1 GB persistent storage
  vcpu: 1                     # 1 virtual CPU
  profile: "custom"           # Profile name
```

### Per-Pod Resource Requests

Resources can be specified per-pod in the Kubernetes manifest:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
spec:
  containers:
  - name: app
    image: flash:/app.tar
    resources:
      requests:
        cpu: "500m"           # 0.5 CPU cores
        memory: "256Mi"       # 256 MB RAM
      limits:
        cpu: "1000m"          # 1 CPU core max
        memory: "512Mi"       # 512 MB RAM max
```

## Multi-Device Configuration

Run multiple devices under one provider instance:

```yaml
nodeName: cisco-datacenter

devices:
  - name: switch-rack1
    type: c9k
    address: 192.168.1.100
    username: admin
    password: cisco
    useHTTPS: true

  - name: switch-rack2
    type: c9k
    address: 192.168.1.101
    username: admin
    password: cisco
    useHTTPS: true

  - name: router-edge
    type: c8k
    address: 192.168.2.100
    username: admin
    password: cisco
    useHTTPS: true
```

## Security Recommendations

### TLS Verification

For production, enable TLS verification:

```yaml
devices:
  - name: production-switch
    verifyTLS: true           # Verify device certificates
```

### Credential Management

Use environment variables or secrets management:

```bash
# Use environment variables instead of config file
export CISCO_DEVICE_PASSWORD=$(vault read -field=password secret/cisco)
```

### Network Segmentation

- Place management traffic on a dedicated VLAN
- Use ACLs to restrict RESTCONF access
- Enable HTTPS only (disable HTTP)

## Logging Configuration

### Log Levels

| Level | Description |
|-------|-------------|
| `debug` | Verbose debugging information |
| `info` | General operational information |
| `warn` | Warning messages |
| `error` | Error messages only |

### Example with Debug Logging

```bash
cisco-vk --config /etc/cisco-vk/config.yaml --log-level debug
```

## Configuration Validation

Test your configuration before deployment:

```bash
# Validate YAML syntax
yamllint /etc/cisco-vk/config.yaml

# Test device connectivity
curl -k -u admin:password https://192.168.1.100/restconf/data/Cisco-IOS-XE-native:native/hostname
```
