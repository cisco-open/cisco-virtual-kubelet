#!/bin/bash
# Cisco Virtual Kubelet Provider Installation Script
set -e

# Configuration
# Go 1.21.x is required for virtual-kubelet v1.11.0 compatibility
# Do not use Go 1.22+ as it causes dependency conflicts
GO_VERSION="${GO_VERSION:-1.21.13}"
INSTALL_DEPS="${INSTALL_DEPS:-false}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --install-deps    Automatically install missing dependencies"
    echo "  --go-version VER  Specify Go version to install (default: $GO_VERSION)"
    echo "  --help            Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                     # Check deps and build"
    echo "  $0 --install-deps      # Install missing deps, then build"
    exit 0
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --install-deps)
            INSTALL_DEPS=true
            shift
            ;;
        --go-version)
            GO_VERSION="$2"
            shift 2
            ;;
        --help)
            usage
            ;;
        *)
            echo "Unknown option: $1"
            usage
            ;;
    esac
done

echo -e "${GREEN}Cisco Virtual Kubelet Provider Installer${NC}"
echo "=========================================="

# Detect OS
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$NAME
    OS_ID=$ID
else
    OS=$(uname -s)
    OS_ID="unknown"
fi

echo "Detected OS: $OS"
echo ""

# Function to install Go
install_go() {
    echo -e "${BLUE}Installing Go ${GO_VERSION}...${NC}"
    
    # Download Go
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz
    
    # Remove existing Go installation
    sudo rm -rf /usr/local/go
    
    # Extract new Go
    sudo tar -C /usr/local -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    
    # Set up PATH for current session
    export PATH=$PATH:/usr/local/go/bin
    export GOPATH=$HOME/go
    export PATH=$PATH:$GOPATH/bin
    
    echo -e "${GREEN}✓${NC} Go ${GO_VERSION} installed"
    echo ""
    echo -e "${YELLOW}NOTE: Add the following to your ~/.bashrc for persistence:${NC}"
    echo "  export PATH=\$PATH:/usr/local/go/bin"
    echo "  export GOPATH=\$HOME/go"
    echo "  export PATH=\$PATH:\$GOPATH/bin"
    echo ""
}

# Function to install build dependencies
install_build_deps() {
    echo -e "${BLUE}Installing build dependencies...${NC}"
    
    case $OS_ID in
        ubuntu|debian)
            sudo apt update
            sudo apt install -y build-essential git curl wget openssl
            ;;
        rhel|centos|fedora|rocky|almalinux)
            sudo dnf groupinstall -y "Development Tools" || sudo yum groupinstall -y "Development Tools"
            sudo dnf install -y git curl wget openssl || sudo yum install -y git curl wget openssl
            ;;
        *)
            echo -e "${RED}Unsupported OS for automatic dependency installation${NC}"
            echo "Please install manually: make, gcc, git, curl, wget, openssl"
            return 1
            ;;
    esac
    
    echo -e "${GREEN}✓${NC} Build dependencies installed"
}

# Check and install dependencies
echo -e "${BLUE}Checking build dependencies...${NC}"
echo ""

MISSING_DEPS=false

# Check for make
if command -v make &> /dev/null; then
    echo -e "${GREEN}✓${NC} make installed"
else
    echo -e "${RED}✗${NC} make not found"
    MISSING_DEPS=true
fi

# Check for git
if command -v git &> /dev/null; then
    echo -e "${GREEN}✓${NC} git installed"
else
    echo -e "${RED}✗${NC} git not found"
    MISSING_DEPS=true
fi

# Check for gcc
if command -v gcc &> /dev/null; then
    echo -e "${GREEN}✓${NC} gcc installed"
else
    echo -e "${RED}✗${NC} gcc not found"
    MISSING_DEPS=true
fi

# Check for Go
if command -v go &> /dev/null; then
    CURRENT_GO_VERSION=$(go version | awk '{print $3}')
    echo -e "${GREEN}✓${NC} Go installed: $CURRENT_GO_VERSION"
else
    echo -e "${RED}✗${NC} Go not found"
    MISSING_DEPS=true
    
    if [ "$INSTALL_DEPS" = true ]; then
        install_go
    fi
fi

# Check for kubectl (optional)
if command -v kubectl &> /dev/null; then
    echo -e "${GREEN}✓${NC} kubectl installed"
else
    echo -e "${YELLOW}⚠${NC} kubectl not found (optional, needed for deployment)"
fi

echo ""

# Handle missing dependencies
if [ "$MISSING_DEPS" = true ]; then
    if [ "$INSTALL_DEPS" = true ]; then
        install_build_deps
        
        # Re-check Go after installing deps
        if ! command -v go &> /dev/null; then
            install_go
        fi
    else
        echo -e "${RED}Missing required dependencies.${NC}"
        echo ""
        echo "Options:"
        echo "  1. Run with --install-deps flag to auto-install"
        echo "  2. Install manually (see docs/INSTALL.md)"
        echo ""
        echo "For Ubuntu/Debian:"
        echo "  sudo apt install -y build-essential git curl wget"
        echo ""
        echo "For RHEL/CentOS/Fedora:"
        echo "  sudo dnf groupinstall -y 'Development Tools'"
        echo "  sudo dnf install -y git curl wget"
        echo ""
        echo "For Go installation:"
        echo "  wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
        echo "  sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz"
        echo "  export PATH=\$PATH:/usr/local/go/bin"
        exit 1
    fi
fi

# Verify Go is available
if ! command -v go &> /dev/null; then
    echo -e "${RED}Go is still not available. Please install Go manually.${NC}"
    exit 1
fi

# Create directories
echo ""
echo "Creating directories..."
sudo mkdir -p /etc/cisco-vk/certs
sudo chmod 700 /etc/cisco-vk

# Build the binary
echo ""
echo "Building cisco-vk..."
if [ -f "Makefile" ]; then
    make build
else
    mkdir -p bin
    go build -o bin/cisco-vk ./cmd/virtual-kubelet
fi

# Install binary
echo ""
echo "Installing binary..."
sudo install -m 755 bin/cisco-vk /usr/local/bin/cisco-vk

# Generate self-signed certificates
echo ""
echo "Generating TLS certificates..."
if [ ! -f /etc/cisco-vk/certs/cert.pem ]; then
    if sudo openssl req -x509 -newkey rsa:4096 \
        -keyout /etc/cisco-vk/certs/key.pem \
        -out /etc/cisco-vk/certs/cert.pem \
        -days 365 -nodes \
        -subj "/CN=cisco-vk" 2>&1; then
        sudo chmod 600 /etc/cisco-vk/certs/key.pem /etc/cisco-vk/certs/cert.pem
        echo -e "${GREEN}✓${NC} Certificates generated"
    else
        echo -e "${YELLOW}⚠${NC} Certificate generation failed. Generate manually:"
        echo "  sudo openssl req -x509 -newkey rsa:4096 \\"
        echo "    -keyout /etc/cisco-vk/certs/key.pem \\"
        echo "    -out /etc/cisco-vk/certs/cert.pem \\"
        echo "    -days 365 -nodes -subj \"/CN=cisco-vk\""
    fi
else
    echo -e "${GREEN}✓${NC} Certificates already exist"
fi

# Install systemd service template
echo ""
echo "Installing systemd service..."
if [ -f "examples/systemd/cisco-vk@.service" ]; then
    sudo cp examples/systemd/cisco-vk@.service /etc/systemd/system/
    sudo systemctl daemon-reload
    echo -e "${GREEN}✓${NC} Systemd service installed"
fi

echo ""
echo -e "${GREEN}Installation complete!${NC}"
echo ""
echo "Next steps:"
echo "1. Create configuration: /etc/cisco-vk/config.yaml"
echo "2. Create environment file: /etc/cisco-vk/<node-name>.env"
echo "3. Enable service: sudo systemctl enable cisco-vk@<node-name>"
echo "4. Start service: sudo systemctl start cisco-vk@<node-name>"
echo ""
echo "See docs/INSTALL.md for detailed instructions"
