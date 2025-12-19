# Troubleshooting Guide

Common issues and solutions for the Cisco Virtual Kubelet Provider.

## Diagnostic Commands

### Check Provider Status

```bash
# Service status
sudo systemctl status cisco-vk@<node-name>

# View logs
sudo journalctl -u cisco-vk@<node-name> -f

# Recent errors only
sudo journalctl -u cisco-vk@<node-name> --since "10 minutes ago" -p err
```

### Check Kubernetes Status

```bash
# Node status
kubectl get nodes
kubectl describe node <cisco-node-name>

# Pod status
kubectl get pods -o wide
kubectl describe pod <pod-name>

# Events
kubectl get events --sort-by='.lastTimestamp'
```

### Check Device Status

```bash
# SSH to device
ssh admin@<device-ip>

# Check app-hosting
show app-hosting list
show app-hosting detail appid <app-id>
show app-hosting resource

# Check IOx
show iox-service
```

## Common Issues

### Node Shows NotReady

**Symptoms**: Node appears but status is NotReady

**Causes**:
1. Provider service not running
2. Kubernetes API connectivity issue
3. Node status callback not registered

**Solutions**:
```bash
# Check service is running
sudo systemctl status cisco-vk@<node-name>

# Restart if needed
sudo systemctl restart cisco-vk@<node-name>

# Check kubeconfig
kubectl get nodes --kubeconfig=/root/.kube/config
```

### Pod Stuck in Pending

**Symptoms**: Pod remains in Pending state

**Causes**:
1. nodeSelector doesn't match
2. Missing tolerations
3. Insufficient resources on device

**Solutions**:
```bash
# Check pod events
kubectl describe pod <pod-name> | grep -A20 Events

# Verify node labels
kubectl get node <cisco-node> --show-labels

# Check node capacity
kubectl describe node <cisco-node> | grep -A10 Capacity
```

### Container Installation Fails

**Symptoms**: Pod fails with ProviderFailed

**Causes**:
1. Image not found on device flash
2. Insufficient storage
3. RESTCONF API error

**Solutions**:
```bash
# Verify image exists
ssh admin@<device-ip> "dir flash: | include .tar"

# Check device storage
ssh admin@<device-ip> "show flash: | include bytes"

# Check provider logs
sudo journalctl -u cisco-vk@<node-name> | grep -i error
```

### RESTCONF Connection Refused

**Symptoms**: Connection errors in logs

**Causes**:
1. RESTCONF not enabled on device
2. Firewall blocking port 443
3. Wrong credentials

**Solutions**:
```bash
# Test connectivity
curl -k -u admin:password https://<device-ip>/restconf/data/Cisco-IOS-XE-native:native/hostname

# Check device configuration
ssh admin@<device-ip> "show running | include restconf"

# Verify HTTPS is enabled
ssh admin@<device-ip> "show running | include ip http"
```

### App State Stuck in DEPLOYED

**Symptoms**: App doesn't progress to RUNNING

**Causes**:
1. Activation failed
2. Missing network configuration
3. Resource constraints

**Solutions**:
```bash
# Check app detail on device
ssh admin@<device-ip> "show app-hosting detail appid <app-id>"

# Check for errors
ssh admin@<device-ip> "show logging | include app-hosting"

# Manual activation (for debugging)
ssh admin@<device-ip>
conf t
app-hosting appid <app-id>
  start
end
```

### Network Connectivity Issues

**Symptoms**: Container can't reach network

**Causes**:
1. VLAN misconfiguration
2. IP address not assigned
3. Missing gateway

**Solutions**:
```bash
# Check container network on device
ssh admin@<device-ip> "show app-hosting detail appid <app-id> | include IP"

# Verify VLAN
ssh admin@<device-ip> "show vlan brief"

# Check interface
ssh admin@<device-ip> "show app-hosting list"
```

## Error Messages

### "no devices available"

Provider cannot connect to any configured device.

```bash
# Check device connectivity
ping <device-ip>

# Test RESTCONF
curl -k https://<device-ip>/restconf/data

# Review configuration
cat /etc/cisco-vk/config.yaml
```

### "failed to install application"

Installation RPC failed on the device.

```bash
# Check if image exists
ssh admin@<device-ip> "dir flash:/<image>.tar"

# Check available storage
ssh admin@<device-ip> "dir flash: | include bytes free"

# Look for conflicts
ssh admin@<device-ip> "show app-hosting list"
```

### "timeout waiting for state RUNNING"

Application failed to reach running state.

```bash
# Check current state
ssh admin@<device-ip> "show app-hosting list"

# Get detailed status
ssh admin@<device-ip> "show app-hosting detail appid <app-id>"

# Check system logs
ssh admin@<device-ip> "show logging last 50 | include app"
```

## Recovery Procedures

### Clean Up Orphaned Apps

```bash
# List all apps on device
ssh admin@<device-ip> "show app-hosting list"

# Stop and remove manually
ssh admin@<device-ip>
conf t
no app-hosting appid <orphaned-app-id>
end
```

### Reset Provider State

```bash
# Stop service
sudo systemctl stop cisco-vk@<node-name>

# Delete node from Kubernetes
kubectl delete node <cisco-node-name>

# Restart service
sudo systemctl start cisco-vk@<node-name>
```

### Force Pod Deletion

```bash
# Normal delete
kubectl delete pod <pod-name>

# Force delete if stuck
kubectl delete pod <pod-name> --grace-period=0 --force
```

## Performance Tuning

### Increase Timeout for Slow Devices

Edit configuration to increase timeouts:

```yaml
devices:
  - name: slow-device
    timeout: 60  # Increase from default 30
```

### Reduce Polling Interval

For faster status updates (at cost of more API calls):

```go
// In provider configuration
pollInterval: 5s  # Default is 10s
```

## Getting Help

1. Check the [GitHub Issues](https://github.com/cisco/virtual-kubelet-cisco/issues)
2. Review [Cisco DevNet](https://developer.cisco.com) documentation
3. Enable debug logging: `LOG_LEVEL=debug`
