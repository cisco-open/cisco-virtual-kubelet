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

# Cisco Virtual Kubelet Provider Makefile

# Build variables
BINARY_NAME=cisco-vk
VERSION?=1.0.0
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
YANG_MODELS_URL=https://github.com/YangModels/yang.git
YANG_SCHEMA_DIR=internal/drivers/iosxe/configdriver/schema/yang
YANG_SKIP_BASELINE=internal/drivers/iosxe/configdriver/schema/yang-skip-baseline.yaml
YANG_RELEASE=$(if $(RELEASE),$(RELEASE),1718)
# Per-release commit pin: read from .provenance.yaml when not explicitly overridden.
YANG_MODELS_COMMIT ?= $(shell grep '^commit:' \
  $(YANG_SCHEMA_DIR)/$(YANG_RELEASE)/.provenance.yaml 2>/dev/null \
  | awk '{print $$2}')

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
CONTROLLER_GEN?=$(GO_BIN) run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5

# Go build flags
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"

# Directories
BIN_DIR=bin
PKG_DIR=pkg
CMD_DIR=cmd/cisco-vk

# Installation directories
PREFIX?=/usr/local
INSTALL_DIR=$(PREFIX)/bin
CONFIG_DIR=/etc/cisco-vk
SYSTEMD_DIR=/etc/systemd/system

.PHONY: all build clean install uninstall test test-envtest lint fmt deps help generate manifests crd-gen deepcopy-gen rbac-gen helm-sync-crds config-lint netascode-migrate config-docs yang-sync migrate-tool parity-matrix check-parity-matrix vendor-yang apphosting-ygot-gen ygot-validate-gen

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

# Real-apiserver smoke gate. The build-tag `envtest` keeps these tests
# out of the default `make test` run because they require a setup-envtest
# managed apiserver/etcd binary set. CI installs setup-envtest before
# invocation; locally, run:
#
#   go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20260305142021-f9589b9f2b9d
#   make test-envtest
#
# The KUBEBUILDER_ASSETS env var points the envtest harness at the
# downloaded apiserver binaries.
test-envtest: ## Run envtest real-apiserver smoke tests (requires setup-envtest in PATH)
	@command -v setup-envtest >/dev/null 2>&1 || { \
		echo "setup-envtest not found. Install with:"; \
		echo "  $(GO_BIN) install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20260305142021-f9589b9f2b9d"; \
		exit 1; \
	}
	@KUBEBUILDER_ASSETS="$$(setup-envtest use 1.35.0 -p path)" \
		$(GO_BIN) test -tags envtest -count=1 -v ./internal/provider/ -run TestEnvtest_

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

config-lint: ## Run cisco-vk-config-lint (pass arguments with ARGS="...")
	$(GO_BIN) run ./tools/cisco-vk-config-lint $(ARGS)

netascode-migrate: ## Inspect or emit IOSXEConfig from NetAsCode input (pass arguments with ARGS="...")
	$(GO_BIN) run ./tools/cvk-netascode-migrate $(ARGS)

config-docs: ## Generate IOS-XE config family reference docs
	$(GO_BIN) run ./tools/cisco-vk-config-docs $(ARGS)

yang-sync: ## Run the IOS-XE config YANG sync helper
	$(GO_BIN) run ./tools/cisco-vk-yang-sync $(ARGS)

migrate-tool: ## Build the netascode migration readiness helper
	@mkdir -p $(BIN_DIR)
	$(GO_BIN) build -o $(BIN_DIR)/cvk-netascode-migrate ./tools/cvk-netascode-migrate

parity-matrix: ## Regenerate docs/family-parity.md from schema/families.yaml
	$(GO_BIN) run ./tools/cvk-netascode-migrate matrix --output docs/family-parity.md

check-parity-matrix: ## Fail if docs/family-parity.md is stale
	@tmp="$$(mktemp)"; \
	$(GO_BIN) run ./tools/cvk-netascode-migrate matrix --output "$$tmp" >/dev/null; \
	if ! diff -u docs/family-parity.md "$$tmp"; then \
		rm -f "$$tmp"; \
		echo "docs/family-parity.md is stale; run make parity-matrix"; \
		exit 1; \
	fi; \
	rm -f "$$tmp"

vendor-yang: ## Vendor Cisco IOS-XE YANG modules for RELEASE (default 1718)
	@dest="$(YANG_SCHEMA_DIR)/$(YANG_RELEASE)"; \
	prov="$$dest/.provenance.yaml"; \
	if [ -f "$$prov" ] && grep -q '^commit: $(YANG_MODELS_COMMIT)$$' "$$prov" \
	   && [ -n "$$(find "$$dest" -name '*.yang' -print -quit 2>/dev/null)" ]; then \
		echo "$$dest already matches $(YANG_MODELS_COMMIT); skipping"; \
		exit 0; \
	fi; \
	if [ -z "$(YANG_MODELS_COMMIT)" ]; then \
		echo "error: YANG_MODELS_COMMIT is not set and no provenance file found for $(YANG_RELEASE)"; \
		exit 1; \
	fi; \
	upstream_release="$(YANG_RELEASE)"; \
	if [ -f "$$prov" ]; then \
		_prov_up="$$(grep '^upstreamRelease:' "$$prov" | awk '{print $$2}' | tr -d '"')"; \
		if [ -n "$$_prov_up" ]; then upstream_release="$$_prov_up"; fi; \
	fi; \
	tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	git init -q "$$tmp/yang"; \
	cd "$$tmp/yang"; \
	git remote add origin "$(YANG_MODELS_URL)"; \
	git fetch --depth=1 origin "$(YANG_MODELS_COMMIT)"; \
	git checkout -q FETCH_HEAD; \
	src="$$tmp/yang/vendor/cisco/xe/$$upstream_release"; \
	if [ ! -d "$$src" ]; then \
		echo "missing upstream Cisco IOS-XE YANG directory: $$src"; \
		exit 1; \
	fi; \
	cd "$(CURDIR)"; \
	mkdir -p "$$(dirname "$$dest")"; \
	rm -rf "$$dest.tmp"; \
	cp -R "$$src" "$$dest.tmp"; \
	rm -rf "$$dest"; \
	mv "$$dest.tmp" "$$dest"; \
	{ \
		echo "upstream: $(YANG_MODELS_URL)"; \
		echo "commit: $(YANG_MODELS_COMMIT)"; \
		echo "fetchDate: $$(date -u +"%Y-%m-%dT%H:%M:%SZ")"; \
		echo "release: \"$(YANG_RELEASE)\""; \
		echo "upstreamRelease: \"$$upstream_release\""; \
	} > "$$prov"; \
	echo "vendored $$src -> $$dest"

## Code generation targets

generate: crd-gen deepcopy-gen rbac-gen helm-sync-crds ygot-gen ## Run all code generators

manifests: crd-gen rbac-gen helm-sync-crds ## Generate Kubernetes and Helm manifests

crd-gen: ## Generate CRDs from ./api (controller-gen)
	@echo "Generating CRDs from ./api..."
	$(CONTROLLER_GEN) \
		crd:crdVersions=v1 \
		paths=./api/... \
		output:crd:dir=./config/crd

deepcopy-gen: ## Generate DeepCopy methods for API types
	@echo "Generating DeepCopy methods..."
	$(CONTROLLER_GEN) \
		object \
		paths=./api/...

# rbac-gen must scan every package that carries +kubebuilder:rbac markers.
# controller-gen v0.16.5 requires repeated paths= flags for multiple roots;
# comma-separated paths are not accepted. The aggregator markers grant leases
# and config CR access, so omitting that package causes generated RBAC drift.
rbac-gen: ## Generate controller ClusterRole into the Helm chart templates dir
	@echo "Generating controller RBAC ClusterRole into Helm chart templates..."
	$(CONTROLLER_GEN) \
		rbac:roleName=cisco-virtual-kubelet-controller \
		paths=./internal/controller/... \
		paths=./internal/aggregator/... \
		output:dir=./charts/cisco-virtual-kubelet/templates
	@echo "# This file is AUTO-GENERATED by 'make rbac-gen'." > /tmp/rbac-header.txt
	@echo "# Do NOT edit manually — run 'make rbac-gen' to regenerate from the" >> /tmp/rbac-header.txt
	@echo "# +kubebuilder:rbac markers in internal/controller/... and internal/aggregator/..." >> /tmp/rbac-header.txt
	@cat /tmp/rbac-header.txt ./charts/cisco-virtual-kubelet/templates/role.yaml > /tmp/rbac-merged.yaml
	@mv /tmp/rbac-merged.yaml ./charts/cisco-virtual-kubelet/templates/role.yaml
	@rm -f /tmp/rbac-header.txt

helm-sync-crds: crd-gen ## Copy generated CRDs into the Helm chart crds/ directory
	@echo "Syncing CRDs to Helm chart..."
	@mkdir -p ./charts/cisco-virtual-kubelet/crds
	cp ./config/crd/*.yaml ./charts/cisco-virtual-kubelet/crds/

ygot-gen: ## Regenerate ygot Go structs from YANG models
	@if [ -n "$(RELEASE)" ]; then \
		echo "Regenerating IOS-XE config ygot models for RELEASE=$(RELEASE)..."; \
		$(GO_BIN) run ./tools/cisco-vk-yang-sync \
			--yang-version=$(RELEASE) \
			--yang-dir=$(YANG_SCHEMA_DIR)/$(RELEASE) \
			--dry-run=false; \
	else \
		$(MAKE) apphosting-ygot-gen; \
	fi

apphosting-ygot-gen: ## Regenerate apphosting ygot Go structs from tests/yang
	@echo "Regenerating ygot models from tests/yang/..."
	$(GO_BIN) install github.com/openconfig/ygot/generator@v0.34.0
	$(shell $(GO_BIN) env GOPATH)/bin/generator \
		-path=tests/yang \
		-output_file=internal/drivers/iosxe/models.go \
		-package_name=iosxe \
		-generate_fakeroot \
		-fakeroot_name=Device \
		-compress_paths=false \
		-exclude_modules=ietf-inet-types,ietf-yang-types,cisco-semver,Cisco-IOS-XE-types,Cisco-IOS-XE-ios-common-oper,Cisco-IOS-XE-ospf-common \
		tests/yang/Cisco-IOS-XE-app-hosting-cfg.yang \
		tests/yang/Cisco-IOS-XE-app-hosting-oper.yang \
		tests/yang/Cisco-IOS-XE-rpc.yang \
		tests/yang/Cisco-IOS-XE-arp-oper.yang \
		tests/yang/Cisco-IOS-XE-device-hardware-oper.yang \
		tests/yang/Cisco-IOS-XE-cdp-oper.yang \
		tests/yang/Cisco-IOS-XE-ospf-oper.yang \
		tests/yang/Cisco-IOS-XE-interfaces-oper.yang

ygot-validate-gen: ## Generate per-family ygot schema packages for CI validation (requires RELEASE=<tag>)
	@if [ -z "$(RELEASE)" ]; then \
		echo "ERROR: RELEASE is required. Example: make ygot-validate-gen RELEASE=1718"; \
		exit 1; \
	fi
	@echo "Generating per-family ygot schema packages for RELEASE=$(RELEASE)..."
	$(GO_BIN) run ./tools/cisco-vk-yang-sync \
		--yang-version=$(RELEASE) \
		--yang-dir=$(YANG_SCHEMA_DIR)/$(RELEASE) \
		--per-family \
		--skip-baseline=$(YANG_SKIP_BASELINE) \
		--clean-per-family-output \
		--dry-run=false

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
