# Installation Guide

This guide provides detailed instructions for installing the Cisco Virtual Kubelet Provider.

## Prerequisites

### System Requirements

- **Operating System**: Linux (Ubuntu 20.04+, RHEL 8+, or similar)
- **Go**: Version 1.21 or later (for building from source)
- **Build Tools**: make, git, gcc (for cgo dependencies)
- **Kubernetes**: Version 1.25 or later
- **Network**: Connectivity to Cisco devices on HTTPS port 443

### Installing Build Dependencies

#### Ubuntu/Debian

```bash
# Update package list
sudo apt update

# Install build essentials and git
sudo apt install -y build-essential git curl wget

# Install Go 1.21 (required for virtual-kubelet v1.11.0 compatibility)
# Note: Go 1.21.x is recommended. Newer versions may have dependency conflicts.
GO_VERSION=1.21.13
wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
rm go${GO_VERSION}.linux-amd64.tar.gz

# Add Go to PATH (add to ~/.bashrc for persistence)
export PATH=$PATH:/usr/local/go/bin
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin

# Verify installation
go version
```

#### RHEL/CentOS/Fedora

```bash
# Install development tools
sudo dnf groupinstall -y "Development Tools"
sudo dnf install -y git curl wget

# Install Go 1.21 (required for virtual-kubelet v1.11.0 compatibility)
GO_VERSION=1.21.13
wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
rm go${GO_VERSION}.linux-amd64.tar.gz

# Add Go to PATH
export PATH=$PATH:/usr/local/go/bin
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin

# Verify installation
go version
```

#### Make Go PATH Permanent

Add the following to your `~/.bashrc` or `~/.profile`:

```bash
# Go environment
export PATH=$PATH:/usr/local/go/bin
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin
```

Then reload:
```bash
source ~/.bashrc
```

#### Alternative: Install Go via Snap (Ubuntu)

```bash
sudo snap install go --classic
go version
```

#### Verify Build Environment

```bash
# Check all required tools
echo "Checking build dependencies..."
command -v go >/dev/null && echo "✓ Go $(go version | awk '{print $3}')" || echo "✗ Go not found"
command -v make >/dev/null && echo "✓ make installed" || echo "✗ make not found"
command -v git >/dev/null && echo "✓ git installed" || echo "✗ git not found"
command -v gcc >/dev/null && echo "✓ gcc installed" || echo "✗ gcc not found"
```

### Cisco Device Requirements

- **IOS-XE Version**: 17.x or later with IOx support
- **Platform**: Catalyst 9300/9400/9500 or Catalyst 8000 series
- **Features Required**:
  - IOx enabled
  - RESTCONF enabled
  - App-hosting capability
  - NETCONF-YANG (optional, for enhanced features)

### Device Configuration

Ensure your Cisco device has the following configuration:

```
! Enable IOx
iox

! Enable RESTCONF
restconf

! Enable HTTPS
ip http secure-server

! Create a user for API access
username admin privilege 15 secret 0 YourPassword

! Enable app-hosting
app-hosting appid test
 app-vnic management guest-interface 0
```

## Installation Methods

### Method 1: Build from Source (Recommended)

> **Note**: Ensure you have installed Go 1.21+ and build tools as described in the Prerequisites section above.

```bash
# Clone the repository
git clone https://github.com/cisco/virtual-kubelet-cisco.git
cd virtual-kubelet-cisco

# Download Go module dependencies
make deps

# Build the binary
make build

# Verify the build succeeded
ls -la bin/cisco-vk

# Install system-wide
sudo make install

# Verify installation
cisco-vk --version
```

#### Build Troubleshooting

If you encounter build errors:

```bash
# Clean and rebuild
make clean
go mod tidy
make build

# If module errors occur
go mod download
go mod verify
```

### Method 2: Download Pre-built Binary

```bash
# Download the latest release
VERSION=1.0.0
curl -LO https://github.com/cisco/virtual-kubelet-cisco/releases/download/v${VERSION}/cisco-vk-linux-amd64

# Make executable and install
chmod +x cisco-vk-linux-amd64
sudo mv cisco-vk-linux-amd64 /usr/local/bin/cisco-vk
```

### Method 3: Docker Container

```bash
# Pull the container image
docker pull ghcr.io/cisco/virtual-kubelet-cisco:latest

# Run with configuration mounted
docker run -d \
  -v /etc/cisco-vk:/etc/cisco-vk:ro \
  -v ~/.kube/config:/root/.kube/config:ro \
  ghcr.io/cisco/virtual-kubelet-cisco:latest
```

## Post-Installation Setup

### 1. Create Configuration Directory

```bash
sudo mkdir -p /etc/cisco-vk/certs
sudo chmod 700 /etc/cisco-vk
```

### 2. Generate TLS Certificates

```bash
# Generate self-signed certificates for the kubelet API
openssl req -x509 -newkey rsa:4096 \
  -keyout /etc/cisco-vk/certs/key.pem \
  -out /etc/cisco-vk/certs/cert.pem \
  -days 365 -nodes \
  -subj "/CN=cisco-vk"

sudo chmod 600 /etc/cisco-vk/certs/*.pem
```

### 3. Create Device Configuration

```bash
sudo tee /etc/cisco-vk/config.yaml << 'EOF'
nodeName: cisco-switch-01

devices:
  - name: my-c9k-switch
    type: c9k
    address: 192.168.1.100
    port: 443
    username: admin
    password: cisco123
    useHTTPS: true
    verifyTLS: false

networkConfig:
  defaultGateway: "192.168.1.1"
  subnetMask: "255.255.255.0"
  ipPoolStart: "192.168.1.200"
  ipPoolEnd: "192.168.1.210"
  vlanID: 100
  managementInterface: 0

resourceDefaults:
  cpuUnits: 1000
  memoryMB: 512
  diskMB: 1024
  vcpu: 1
  profile: "custom"
EOF

sudo chmod 600 /etc/cisco-vk/config.yaml
```

### 4. Setup Systemd Service

```bash
# Copy the service template
sudo cp examples/systemd/cisco-vk@.service /etc/systemd/system/

# Create environment file
sudo tee /etc/cisco-vk/env << 'EOF'
CISCO_CONFIG_PATH=/etc/cisco-vk/config.yaml
NODE_NAME=cisco-switch-01
KUBECONFIG=/root/.kube/config
APISERVER_CERT_LOCATION=/etc/cisco-vk/certs/cert.pem
APISERVER_KEY_LOCATION=/etc/cisco-vk/certs/key.pem
LOG_LEVEL=info
EOF

# Enable and start the service
sudo systemctl daemon-reload
sudo systemctl enable cisco-vk@cisco-switch-01
sudo systemctl start cisco-vk@cisco-switch-01
```

### 5. Verify Installation

```bash
# Check service status
sudo systemctl status cisco-vk@cisco-switch-01

# Check logs
sudo journalctl -u cisco-vk@cisco-switch-01 -f

# Verify node registration in Kubernetes
kubectl get nodes
```

## Preparing Container Images

### Upload Container Image to Device

Container images must be available on the device's flash storage:

```bash
# Save Docker image as tar
docker pull nginx:alpine
docker save nginx:alpine -o nginx.tar

# Copy to device via SCP
scp nginx.tar admin@192.168.1.100:flash:/nginx.tar
```

### Verify Image on Device

```
C9K# dir flash:/*.tar
Directory of flash:/*.tar
  12345  -rw-         23929344  Dec 15 2024 12:00:00  nginx.tar
```

## Troubleshooting Installation

### Common Issues

1. **Binary not found**
   ```bash
   # Ensure /usr/local/bin is in PATH
   export PATH=$PATH:/usr/local/bin
   ```

2. **Permission denied on config files**
   ```bash
   sudo chown -R root:root /etc/cisco-vk
   sudo chmod 700 /etc/cisco-vk
   sudo chmod 600 /etc/cisco-vk/*.yaml
   ```

3. **Cannot connect to device**
   ```bash
   # Test RESTCONF connectivity
   curl -k -u admin:password https://192.168.1.100/restconf/data/Cisco-IOS-XE-native:native/hostname
   ```

4. **Node not appearing in Kubernetes**
   ```bash
   # Check VK logs for errors
   sudo journalctl -u cisco-vk@cisco-switch-01 --since "5 minutes ago"
   ```

## Next Steps

- [Configuration Reference](CONFIGURATION.md) - Detailed configuration options
- [Deployment Guide](DEPLOYMENT.md) - Deploying containers
- [Troubleshooting](TROUBLESHOOTING.md) - Common issues and solutions
