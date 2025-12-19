# Production Deployment Guide

This guide covers deploying the Cisco Virtual Kubelet provider in a production environment.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [High Availability](#high-availability)
- [Monitoring](#monitoring)
- [Backup and Recovery](#backup-and-recovery)

## Prerequisites

### Infrastructure Requirements

- **Kubernetes Cluster:** v1.28 or later
- **Cisco IOS XE Device:** 17.3.1 or later
- **Network Connectivity:**
  - RESTCONF (HTTPS 443) from VK to device
  - SSH (TCP 22) from VK to device
  - Port 10250 from API server to VK

### Device Configuration

```cisco
! Enable IOx
iox

! Configure App Hosting
app-hosting appid <appid>
 app-vnic gateway1 virtualportgroup 0 guest-interface 0
  guest-ipaddress 192.168.35.2 netmask 255.255.255.0
 app-default-gateway 192.168.35.1 guest-interface 0
 app-resource profile custom
  cpu 7400
  memory 4096
  persist-disk 8192

! Enable RESTCONF
restconf

! Configure authentication
username admin privilege 15 secret <password>
aaa new-model
aaa authentication login default local
aaa authorization exec default local
```

## Installation

### Method 1: Binary Installation

```bash
# Download latest release
VERSION=v1.0.0
curl -LO https://github.com/cisco/virtual-kubelet-cisco/releases/download/${VERSION}/cisco-vk-linux-amd64

# Install binary
sudo install -m 755 cisco-vk-linux-amd64 /usr/local/bin/cisco-vk

# Verify installation
cisco-vk --version
```

### Method 2: Build from Source

```bash
# Clone repository
git clone https://github.com/cisco/virtual-kubelet-cisco.git
cd virtual-kubelet-cisco/providers/cisco

# Build
go build -o cisco-vk ./cmd

# Install
sudo install -m 755 cisco-vk /usr/local/bin/
```

### Method 3: Container Deployment

```bash
# Pull image
docker pull ghcr.io/cisco/virtual-kubelet-cisco:v1.0.0

# Run container
docker run -d \
  --name cisco-vk \
  --network host \
  -v /root/.kube/config:/root/.kube/config:ro \
  -v /etc/cisco-vk:/etc/cisco-vk:ro \
  -e CISCO_DEVICE_ADDRESS=192.168.1.1 \
  -e CISCO_DEVICE_USERNAME=admin \
  -e CISCO_DEVICE_PASSWORD=password \
  ghcr.io/cisco/virtual-kubelet-cisco:v1.0.0
```

## Configuration

### TLS Certificates

Generate self-signed certificates for development:

```bash
mkdir -p /etc/cisco-vk/certs

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout /etc/cisco-vk/certs/key.pem \
  -out /etc/cisco-vk/certs/cert.pem \
  -days 365 \
  -subj "/CN=cisco-virtual-kubelet/O=Cisco Systems"
```

For production, use certificates signed by your cluster CA:

```bash
# Generate CSR
openssl req -new -newkey rsa:2048 -nodes \
  -keyout /etc/cisco-vk/certs/key.pem \
  -out /etc/cisco-vk/certs/csr.pem \
  -subj "/CN=cisco-vk.example.com/O=Cisco Systems"

# Sign with cluster CA (example with kubeadm)
sudo openssl x509 -req \
  -in /etc/cisco-vk/certs/csr.pem \
  -CA /etc/kubernetes/pki/ca.crt \
  -CAkey /etc/kubernetes/pki/ca.key \
  -CAcreateserial \
  -out /etc/cisco-vk/certs/cert.pem \
  -days 365
```

### Environment Configuration

Create `/etc/cisco-vk/config.env`:

```bash
# Device Connection
CISCO_DEVICE_ADDRESS=192.168.1.1
CISCO_DEVICE_USERNAME=admin
CISCO_DEVICE_PASSWORD=SecurePassword123!

# Kubernetes
KUBECONFIG=/root/.kube/config
NODE_NAME=cisco-c9k-production

# TLS
APISERVER_CERT_LOCATION=/etc/cisco-vk/certs/cert.pem
APISERVER_KEY_LOCATION=/etc/cisco-vk/certs/key.pem

# Network
VKUBELET_POD_IP=10.0.0.100

# Logging
LOG_LEVEL=info
```

**Security Best Practice:** Use Kubernetes Secrets instead:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cisco-credentials
  namespace: kube-system
type: Opaque
stringData:
  device-address: "192.168.1.1"
  username: "admin"
  password: "SecurePassword123!"
```

### systemd Service

Create `/etc/systemd/system/cisco-vk.service`:

```ini
[Unit]
Description=Cisco Virtual Kubelet Provider
After=network.target

[Service]
Type=simple
User=root
EnvironmentFile=/etc/cisco-vk/config.env
ExecStart=/usr/local/bin/cisco-vk \
  --provider cisco \
  --kubeconfig ${KUBECONFIG} \
  --nodename ${NODE_NAME} \
  --log-level ${LOG_LEVEL}

Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable cisco-vk
sudo systemctl start cisco-vk
sudo systemctl status cisco-vk
```

## High Availability

### Multi-Provider Setup

Deploy multiple VK instances for different devices:

```bash
# Device 1
NODE_NAME=cisco-c9k-1 CISCO_DEVICE_ADDRESS=192.168.1.1 cisco-vk &

# Device 2
NODE_NAME=cisco-c9k-2 CISCO_DEVICE_ADDRESS=192.168.1.2 cisco-vk &
```

### Health Checks

Monitor VK health:

```bash
# Check process
systemctl status cisco-vk

# Check node in Kubernetes
kubectl get node cisco-c9k-production

# Check logs
journalctl -u cisco-vk -f
```

### Graceful Shutdown

VK handles SIGTERM gracefully:

```bash
# Stop service
sudo systemctl stop cisco-vk

# Or send SIGTERM
kill -TERM $(pgrep -f cisco-vk)
```

## Monitoring

### Prometheus Metrics

VK exposes metrics on port 10255:

```bash
curl http://localhost:10255/metrics
```

Example metrics:
- `virtual_kubelet_pod_count` - Number of managed pods
- `virtual_kubelet_restconf_requests_total` - RESTCONF API calls
- `virtual_kubelet_errors_total` - Error count

### Logging

Configure structured logging:

```bash
# JSON format
LOG_FORMAT=json cisco-vk ...

# Debug level
LOG_LEVEL=debug cisco-vk ...
```

View logs:

```bash
# systemd
journalctl -u cisco-vk -f

# Docker
docker logs -f cisco-vk

# File
tail -f /var/log/cisco-vk.log
```

### Alerting

Example Prometheus alert rules:

```yaml
groups:
- name: cisco-vk
  rules:
  - alert: CiscoVKDown
    expr: up{job="cisco-vk"} == 0
    for: 5m
    annotations:
      summary: "Cisco VK is down"
  
  - alert: HighRESTCONFErrors
    expr: rate(virtual_kubelet_errors_total[5m]) > 0.1
    annotations:
      summary: "High RESTCONF error rate"
```

## Backup and Recovery

### State Backup

VK state is stored in:
1. Kubernetes (pod annotations)
2. Cisco device (running applications)
3. Memory (internal cache)

No separate backup needed - state reconciles on restart.

### Disaster Recovery

If VK crashes or is restarted:

1. **Pods remain in Kubernetes** - Marked as Running
2. **Apps remain on device** - Continue running
3. **VK reconciles on startup:**
   - Queries Kubernetes for pods
   - Queries device for apps
   - Matches and restores state
   - No duplicate deployments

### Device Failure

If Cisco device fails:

1. VK marks pods as Failed
2. Kubernetes reschedules (if Deployment/ReplicaSet)
3. New pods scheduled to available devices
4. Old device cleaned up when available

## Upgrades

### Rolling Upgrade

1. **Deploy new version alongside old:**
   ```bash
   # Download new version
   curl -LO .../cisco-vk-v1.1.0
   
   # Install as cisco-vk-new
   sudo install -m 755 cisco-vk-v1.1.0 /usr/local/bin/cisco-vk-new
   ```

2. **Test new version:**
   ```bash
   # Start with different node name
   NODE_NAME=cisco-c9k-test cisco-vk-new ...
   ```

3. **Migrate:**
   ```bash
   # Stop old version
   sudo systemctl stop cisco-vk
   
   # Replace binary
   sudo cp /usr/local/bin/cisco-vk-new /usr/local/bin/cisco-vk
   
   # Start new version
   sudo systemctl start cisco-vk
   ```

### Rollback

If issues occur:

```bash
# Stop new version
sudo systemctl stop cisco-vk

# Restore old binary
sudo cp /usr/local/bin/cisco-vk.old /usr/local/bin/cisco-vk

# Restart
sudo systemctl start cisco-vk
```

## Security Hardening

### Principle of Least Privilege

1. **Run as non-root (when possible):**
   ```ini
   [Service]
   User=cisco-vk
   Group=cisco-vk
   ```

2. **Restrict file permissions:**
   ```bash
   chmod 600 /etc/cisco-vk/config.env
   chmod 600 /etc/cisco-vk/certs/*
   ```

3. **Use Kubernetes RBAC:**
   ```yaml
   apiVersion: rbac.authorization.k8s.io/v1
   kind: ClusterRole
   metadata:
     name: cisco-vk-role
   rules:
   - apiGroups: [""]
     resources: ["pods", "nodes", "pods/status"]
     verbs: ["get", "list", "watch", "create", "update", "delete"]
   ```

### Network Security

1. **Firewall rules:**
   ```bash
   # Allow only necessary ports
   iptables -A INPUT -p tcp --dport 10250 -s <api-server-ip> -j ACCEPT
   iptables -A INPUT -p tcp --dport 10250 -j DROP
   ```

2. **TLS everywhere:**
   - VK HTTP API: TLS required
   - RESTCONF: HTTPS only
   - Use certificate verification

### Credential Management

1. **Use Kubernetes Secrets:**
   ```bash
   kubectl create secret generic cisco-credentials \
     --from-literal=username=admin \
     --from-literal=password=<password>
   ```

2. **Rotate regularly:**
   ```bash
   # Update on device first
   # Then update secret
   kubectl create secret generic cisco-credentials \
     --from-literal=password=<new-password> \
     --dry-run=client -o yaml | kubectl apply -f -
   ```

3. **Use HashiCorp Vault (optional):**
   ```bash
   vault kv put secret/cisco-vk \
     username=admin \
     password=<password>
   ```

## DaemonSet Scheduling Issues

When deploying the Cisco Virtual Kubelet, system DaemonSets (cilium, tetragon, etc.) may attempt to schedule on the Cisco node. The provider automatically adds a taint to prevent this, but existing DaemonSets may need configuration.

### Automatic Protection

The provider automatically applies:
- **Taint:** `virtual-kubelet.io/provider=cisco:NoSchedule`
- **Label:** `type=virtual-kubelet`

### Patching Existing DaemonSets

If DaemonSets still schedule on the Cisco node, patch them to exclude virtual-kubelet nodes:

```bash
# Patch cilium DaemonSet
kubectl patch daemonset cilium -n kube-system --type=strategic -p '{"spec":{"template":{"spec":{"affinity":{"nodeAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"type","operator":"NotIn","values":["virtual-kubelet"]}]}]}}}}}}}'

# Patch tetragon DaemonSet  
kubectl patch daemonset tetragon -n kube-system --type=strategic -p '{"spec":{"template":{"spec":{"affinity":{"nodeAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"type","operator":"NotIn","values":["virtual-kubelet"]}]}]}}}}}}}'
```

### Pod Toleration

Pods targeting the Cisco node must include the toleration:

```yaml
tolerations:
- key: "virtual-kubelet.io/provider"
  operator: "Equal"
  value: "cisco"
  effect: "NoSchedule"
```

## Troubleshooting

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for common issues and solutions.

## Support

- GitHub Issues: [Report problems](https://github.com/cisco/virtual-kubelet-cisco/issues)
- Documentation: [Full docs](../README.md)

---

**Last Updated:** October 21, 2025  
**Version:** 1.0.0
