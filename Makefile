# Cisco Virtual Kubelet Provider Makefile

# Build variables
BINARY_NAME=cisco-vk
VERSION?=1.0.0
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GO_VERSION=$(shell go version | awk '{print $$3}')

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

.PHONY: all build clean install uninstall test lint fmt deps help

all: build

## Build targets

build: deps ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) ./$(CMD_DIR)
	@echo "Binary built: $(BIN_DIR)/$(BINARY_NAME)"

build-linux: deps ## Build for Linux (amd64)
	@echo "Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)

build-all: build-linux ## Build for all platforms

## Installation targets

install: build ## Install the binary and create directories
	@echo "Installing $(BINARY_NAME)..."
	sudo install -m 755 $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	sudo mkdir -p $(CONFIG_DIR)/certs
	sudo chmod 700 $(CONFIG_DIR)
	@echo "Installed to $(INSTALL_DIR)/$(BINARY_NAME)"

install-systemd: ## Install systemd service template
	@echo "Installing systemd service template..."
	sudo cp examples/systemd/cisco-vk@.service $(SYSTEMD_DIR)/
	sudo systemctl daemon-reload
	@echo "Systemd template installed. Create instances with:"
	@echo "  sudo systemctl enable cisco-vk@<node-name>"

uninstall: ## Remove installed binary and configs
	@echo "Uninstalling $(BINARY_NAME)..."
	sudo rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Note: Configuration files in $(CONFIG_DIR) were preserved"

## Development targets

deps: ## Download dependencies
	go mod download
	go mod tidy

test: ## Run tests
	go test -v -race ./...

test-coverage: ## Run tests with coverage
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint: ## Run linter
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

fmt: ## Format code
	go fmt ./...
	goimports -w .

vet: ## Run go vet
	go vet ./...

## Utility targets

clean: ## Clean build artifacts
	rm -rf $(BIN_DIR)
	rm -f coverage.out coverage.html

version: ## Show version info
	@echo "Version: $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Go Version: $(GO_VERSION)"

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
