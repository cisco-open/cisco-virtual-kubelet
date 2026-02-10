# Cisco Virtual Kubelet Provider Makefile

# Build variables
BINARY_NAME=cisco-vk
VERSION?=1.0.0
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Detect Go installation method and set appropriate paths
SNAP_GO_PATH=/snap/go/current
GO_SNAP_DETECTED=$(shell test -d $(SNAP_GO_PATH) && echo yes || echo no)

ifeq ($(GO_SNAP_DETECTED),yes)
GOROOT=$(SNAP_GO_PATH)
GO_BIN=$(SNAP_GO_PATH)/bin/go
GO_INSTALL_TYPE=snap
else
GO_BIN=$(shell which go)
GO_INSTALL_TYPE=apt
endif

export GOROOT
export PATH:=$(GOROOT)/bin:$(PATH)

GO_VERSION=$(shell $(GO_BIN) version 2>/dev/null | awk '{print $$3}' || echo "unknown")

# Go build flags
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"

# Directories
BIN_DIR=bin
PKG_DIR=pkg
CMD_DIR=cmd/virtual-kubelet

# Installation directories
PREFIX?=/usr/local
INSTALL_DIR=$(PREFIX)/bin
CONFIG_DIR=/etc/cisco-vk
SYSTEMD_DIR=/etc/systemd/system

.PHONY: all build clean install uninstall test lint fmt deps help crd-gen

all: build

## Build targets

build: deps ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BIN_DIR)
	$(GO_BIN) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) ./$(CMD_DIR)
	@echo "Binary built: $(BIN_DIR)/$(BINARY_NAME)"

build-linux: deps
	@echo "Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 $(GO_BIN) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)

build-all: build-linux

## Installation targets

install: build
	@echo "Installing $(BINARY_NAME)..."
	sudo install -m 755 $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	sudo mkdir -p $(CONFIG_DIR)/certs
	sudo chmod 700 $(CONFIG_DIR)
	@echo "Installed to $(INSTALL_DIR)/$(BINARY_NAME)"

install-systemd:
	@echo "Installing systemd service template..."
	sudo cp examples/systemd/cisco-vk@.service $(SYSTEMD_DIR)/
	sudo systemctl daemon-reload
	@echo "Systemd template installed. Create instances with:"
	@echo "  sudo systemctl enable cisco-vk@<node-name>"

uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	sudo rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Note: Configuration files in $(CONFIG_DIR) were preserved"

## Development targets

deps: ## Download dependencies
	$(GO_BIN) mod download
	$(GO_BIN) mod tidy

test: ## Run tests
	$(GO_BIN) test -v -race ./...

test-coverage: ## Run tests with coverage
	$(GO_BIN) test -v -race -coverprofile=coverage.out ./...
	$(GO_BIN) tool cover -html=coverage.out -o coverage.html

lint: ## Run linter
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Run: $(GO_BIN) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

fmt: ## Format code
	$(GO_BIN) fmt ./...
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w .; \
	else \
		echo "goimports not installed. Run: $(GO_BIN) install golang.org/x/tools/cmd/goimports@latest"; \
	fi

vet: ## Run go vet
	$(GO_BIN) vet ./...

## Code generation targets

generate: crd-gen deepcopy-gen ## Run all code generators

crd-gen: ## Generate CRDs from ./api (controller-gen)
	@echo "Generating CRDs from ./api..."
	$(GO_BIN) run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5 \
		crd:crdVersions=v1 \
		paths=./api/... \
		output:crd:dir=./config/crd

deepcopy-gen: ## Generate DeepCopy methods for API types
	@echo "Generating DeepCopy methods..."
	$(GO_BIN) run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5 \
		object \
		paths=./api/...

## Utility targets

clean: ## Clean build artifacts
	rm -rf $(BIN_DIR)
	rm -f coverage.out coverage.html

version: ## Show version info
	@echo "Version: $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Go Version: $(GO_VERSION)"
	@echo "Go Installation: $(GO_INSTALL_TYPE)"
	@echo "Go Binary: $(GO_BIN)"
	@if [ "$(GO_INSTALL_TYPE)" = "snap" ]; then \
		echo "GOROOT: $(GOROOT)"; \
	fi

## Docker targets

docker-build: ## Build Docker image
	docker build -t cisco-virtual-kubelet:$(VERSION) .

## Help

help: ## Show this help
	@echo "Cisco Virtual Kubelet Provider"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'