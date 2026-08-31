#!/bin/bash
# Copyright 2026 Cisco Systems Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Cisco Virtual Kubelet source-build installation script.
set -euo pipefail

# Configuration
PINNED_GO_VERSION="1.26.7"
if [[ -n "${GO_VERSION+x}" ]]; then
    GO_VERSION_EXPLICIT=true
else
    GO_VERSION="$PINNED_GO_VERSION"
    GO_VERSION_EXPLICIT=false
fi
GO_SHA256="${GO_SHA256:-}"
INSTALL_DEPS="${INSTALL_DEPS:-false}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

usage() {
    local status=${1:-0}
    echo "Usage: bash $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --install-deps    Automatically install missing dependencies"
    echo "  --go-version VER  Override the pinned Go version (default: $GO_VERSION)"
    echo "  --help            Show this help message"
    echo ""
    echo "Examples:"
    echo "  bash $0                     # Check deps and build"
    echo "  bash $0 --install-deps      # Install missing deps, then build"
    exit "$status"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --install-deps)
            INSTALL_DEPS=true
            shift
            ;;
        --go-version)
            if [[ $# -lt 2 ]]; then
                echo "--go-version requires a value" >&2
                exit 2
            fi
            GO_VERSION="$2"
            GO_VERSION_EXPLICIT=true
            shift 2
            ;;
        --help)
            usage 0
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage 2 >&2
            ;;
    esac
done

echo -e "${GREEN}Cisco Virtual Kubelet Provider Installer${NC}"
echo "=========================================="

# Detect OS
if [ -f /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS=${NAME:-Linux}
    OS_ID=${ID:-unknown}
else
    OS=$(uname -s)
    OS_ID="unknown"
fi

echo "Detected OS: $OS"
echo ""

# Return success only for an audited Go minor at or above its current security
# floor. Do not accept end-of-life lines or unaudited future minor versions.
# For example, go1.26.2 predates security fixes required by this release.
go_version_is_supported() {
    local actual=${1#go}
    local actual_major actual_minor actual_patch

    if [[ ! "$actual" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
        return 1
    fi
    actual_major=${BASH_REMATCH[1]}
    actual_minor=${BASH_REMATCH[2]}
    actual_patch=${BASH_REMATCH[3]}
    (( actual_major == 1 && actual_minor == 26 && actual_patch >= 7 )) ||
        (( actual_major == 1 && actual_minor == 27 && actual_patch >= 0 ))
}

# An explicit override is an exact toolchain selection, not merely a request
# for any supported Go line. Without an override, any audited patched version
# is acceptable.
go_version_satisfies_request() {
    local actual=$1
    go_version_is_supported "$actual" || return 1
    [[ "$GO_VERSION_EXPLICIT" = false ]] ||
        [[ "${actual#go}" = "$GO_VERSION" ]]
}

if [[ "$GO_VERSION_EXPLICIT" = true ]] &&
   ! go_version_is_supported "go${GO_VERSION}"; then
    echo "Requested Go ${GO_VERSION} is outside the patched supported lines (go1.26.7+ or go1.27.0+)" >&2
    exit 2
fi

go_download_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        *)
            echo "Automatic Go installation is unsupported on architecture $(uname -m)" >&2
            return 1
            ;;
    esac
}

pinned_go_checksum() {
    local version=$1
    local arch=$2
    case "${version}/${arch}" in
        1.26.7/amd64) echo ffb5f8de10c62550dfddab66b36b57030721e0a44a3218e9e1181d7b59f121ca ;;
        1.26.7/arm64) echo 5a4ec883379d51ee9ce1040d5e87f8d35e20387574dd8c947feb01eabc3c1b37 ;;
        1.27.0/amd64) echo 675c26c449cbb18fc24b74650de1eabbae6e16f64326fd85a283fb3b58280685 ;;
        1.27.0/arm64) echo 51798d2c42d0e1c6ed7fd9f48728b4193abac9e8aad6dbac2fe96a81f5909bda ;;
        *)
            if [[ "$GO_SHA256" =~ ^[0-9a-f]{64}$ ]]; then
                echo "$GO_SHA256"
            else
                echo "No pinned checksum for Go ${version} on linux/${arch}; set GO_SHA256 explicitly" >&2
                return 1
            fi
            ;;
    esac
}

verify_sha256() {
    local expected=$1
    local file=$2
    if command -v sha256sum >/dev/null 2>&1; then
        echo "${expected}  ${file}" | sha256sum --check --strict
    elif command -v shasum >/dev/null 2>&1; then
        echo "${expected}  ${file}" | shasum -a 256 --check
    else
        echo "A SHA-256 verifier (sha256sum or shasum) is required" >&2
        return 1
    fi
}

harden_staged_toolchain() {
    local root=$1
    sudo chown -R 0:0 "$root"
    sudo chmod -R go-w "$root"
    # sudo mktemp creates the staging root as 0700. Make the final root
    # traversable only after every file is root-owned and non-writable by
    # group/other; child directory and executable modes come from Go's
    # checksum-verified archive.
    sudo chmod 0755 "$root"
}

# Install a private, versioned Go toolchain for this build. Do not overwrite a
# system Go installation: /usr/local/go may belong to another administrator.
install_go() {
    if [[ "$(uname -s)" != "Linux" ]]; then
        echo "Automatic Go installation is supported only on Linux" >&2
        return 1
    fi
    local arch archive checksum temp_dir toolchain_base toolchain_root staged_root staged_version
    arch=$(go_download_arch)
    archive="go${GO_VERSION}.linux-${arch}.tar.gz"
    checksum=$(pinned_go_checksum "$GO_VERSION" "$arch")
    temp_dir=$(mktemp -d)
    toolchain_base="/usr/local/lib/cisco-vk"
    toolchain_root="${toolchain_base}/go${GO_VERSION}"
    staged_root=""
    trap '
        rm -rf -- "$temp_dir"
        if [[ -n "$staged_root" ]] && [[ "$staged_root" == "$toolchain_base"/.go*.install.* ]]; then
            sudo rm -rf -- "$staged_root"
        fi
    ' RETURN

    # Keep the privileged replacement target closed over the validated numeric
    # version. Never let an environment value widen the recursive-delete path.
    if [[ ! "$GO_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
       [[ "$toolchain_root" != "/usr/local/lib/cisco-vk/go${GO_VERSION}" ]]; then
        echo "Refusing unsafe Go toolchain target: $toolchain_root" >&2
        return 1
    fi

    echo -e "${BLUE}Installing Go ${GO_VERSION}...${NC}"
    if command -v curl >/dev/null 2>&1; then
        curl --fail --location --proto '=https' --tlsv1.2 \
            --output "$temp_dir/$archive" "https://go.dev/dl/$archive"
    elif command -v wget >/dev/null 2>&1; then
        wget --https-only --output-document "$temp_dir/$archive" \
            "https://go.dev/dl/$archive"
    else
        echo "curl or wget is required to install Go" >&2
        return 1
    fi
    verify_sha256 "$checksum" "$temp_dir/$archive"
    tar -C "$temp_dir" -xzf "$temp_dir/$archive"

    # Stage a fresh, checksum-verified tree under the root-owned product
    # directory on every run. A previous unprivileged installer must never be
    # able to retain write access to a toolchain later users place on PATH.
    sudo install -d -o 0 -g 0 -m 0755 "$toolchain_base"
    staged_root=$(sudo mktemp -d "${toolchain_base}/.go${GO_VERSION}.install.XXXXXX")
    if [[ "$staged_root" != "$toolchain_base"/.go${GO_VERSION}.install.* ]]; then
        echo "Refusing unexpected Go staging path: $staged_root" >&2
        return 1
    fi
    sudo cp -R "$temp_dir/go/." "$staged_root/"
    harden_staged_toolchain "$staged_root"
    test -x "$staged_root/bin/go"
    staged_version=$("$staged_root/bin/go" env GOVERSION)
    if [[ "$staged_version" != "go${GO_VERSION}" ]]; then
        echo "Verified archive installed unexpected toolchain $staged_version" >&2
        return 1
    fi

    # The target is an exact versioned directory beneath the fixed product
    # root. Replace it instead of trusting a pre-existing executable tree.
    if [[ -e "$toolchain_root" || -L "$toolchain_root" ]]; then
        sudo rm -rf -- "$toolchain_root"
    fi
    sudo mv "$staged_root" "$toolchain_root"
    staged_root=""
    if [[ "$(stat -c '%u:%g' "$toolchain_root")" != "0:0" ]] ||
       [[ -n "$(find "$toolchain_root" -xdev -perm /022 -print -quit)" ]]; then
        echo "Installed Go toolchain ownership or permissions are unsafe: $toolchain_root" >&2
        return 1
    fi
    export PATH="$toolchain_root/bin:$PATH"
    echo -e "${GREEN}✓${NC} Go ${GO_VERSION} installed"
}

# Function to install build dependencies
install_build_deps() {
    echo -e "${BLUE}Installing build dependencies...${NC}"
    
    case $OS_ID in
        ubuntu|debian)
            sudo apt update
            sudo apt install -y build-essential ca-certificates curl git
            ;;
        rhel|centos|fedora|rocky|almalinux)
            sudo dnf groupinstall -y "Development Tools" || sudo yum groupinstall -y "Development Tools"
            sudo dnf install -y ca-certificates curl git || sudo yum install -y ca-certificates curl git
            ;;
        *)
            echo -e "${RED}Unsupported OS for automatic dependency installation${NC}"
            echo "Please install manually: make, gcc, git, curl, and CA certificates"
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

# Check for a patched, module-compatible Go toolchain.
NEED_GO=false
if command -v go &> /dev/null; then
    CURRENT_GO_VERSION=$(go env GOVERSION)
    if go_version_satisfies_request "$CURRENT_GO_VERSION"; then
        echo -e "${GREEN}✓${NC} Go installed: $CURRENT_GO_VERSION"
    else
        if [[ "$GO_VERSION_EXPLICIT" = true ]] &&
           [[ "${CURRENT_GO_VERSION#go}" != "$GO_VERSION" ]]; then
            echo -e "${RED}✗${NC} Go $CURRENT_GO_VERSION is installed, but exact Go ${GO_VERSION} was requested"
        else
            echo -e "${RED}✗${NC} Go $CURRENT_GO_VERSION is outside the patched supported lines (go1.26.7+ or go1.27.0+)"
        fi
        NEED_GO=true
        MISSING_DEPS=true
    fi
else
    echo -e "${RED}✗${NC} Go not found"
    NEED_GO=true
    MISSING_DEPS=true
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
        
        if [ "$NEED_GO" = true ]; then
            install_go
        fi
    else
        echo -e "${RED}Missing required dependencies.${NC}"
        echo ""
        echo "Options:"
        echo "  1. Run with --install-deps flag to auto-install"
        echo "  2. Install the dependencies manually, including patched Go 1.26.7+ or 1.27.0+"
        echo ""
        echo "For Ubuntu/Debian:"
        echo "  sudo apt install -y build-essential ca-certificates curl git"
        echo ""
        echo "For RHEL/CentOS/Fedora:"
        echo "  sudo dnf groupinstall -y 'Development Tools'"
        echo "  sudo dnf install -y ca-certificates curl git"
        echo ""
        echo "For Go installation:"
        echo "  See https://go.dev/doc/install for a verified Go installation"
        exit 1
    fi
fi

# Verify the selected Go remains compatible after dependency installation.
if ! command -v go &> /dev/null || \
   ! go_version_satisfies_request "$(go env GOVERSION)"; then
    echo -e "${RED}Patched Go 1.26.7+ or 1.27.0+ is required.${NC}"
    if [[ "$GO_VERSION_EXPLICIT" = true ]]; then
        echo -e "${RED}The explicitly requested go${GO_VERSION} toolchain must be first on PATH.${NC}"
    fi
    exit 1
fi

# Build the binary
echo ""
echo "Building cisco-vk..."
if [[ ! -f Makefile || ! -f go.mod || ! -d cmd/cisco-vk ]]; then
    echo "Run this script from the cisco-virtual-kubelet repository root" >&2
    exit 1
fi
make build

# Install binary
echo ""
echo "Installing binary..."
sudo mkdir -p "$INSTALL_DIR"
sudo install -m 0755 bin/cisco-vk "$INSTALL_DIR/cisco-vk"

echo ""
echo -e "${GREEN}Installation complete!${NC}"
echo ""
echo "Next steps:"
echo "1. Run: $INSTALL_DIR/cisco-vk --help"
echo "2. For the supported cluster deployment, follow docs/getting-started.md"
echo "3. For direct development runs, follow docs/cisco-vk-cli.md#cisco-vk-run"
echo ""
echo "The installer intentionally does not create TLS material or a systemd unit."
