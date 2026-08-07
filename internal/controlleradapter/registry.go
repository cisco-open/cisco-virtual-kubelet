// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package controlleradapter defines the product-neutral network-controller runtime
// contract and the registry populated by concrete adapter packages.
//
// The registration pattern mirrors database/sql and the CVK device-driver
// registry: a concrete package registers from init, while the cisco-vk
// composition root opts products in with blank imports. This package never
// imports a product adapter, device driver, or device configuration engine.
package controlleradapter

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

// Descriptor is the stable, product-neutral metadata needed by orchestration
// and worker deployment code before an adapter is constructed.
//
// Type and Capabilities are deliberately open strings. Adding a controller or
// an optional capability therefore does not require a closed CRD enum or a
// switch in the foundation.
type Descriptor struct {
	// Type is the stable DNS-label selector stored in
	// NetworkController.spec.type, for example "catalyst-center".
	Type string

	// DisplayName is the human-readable product or integration name.
	DisplayName string

	// NetAsCode declares the canonical controller-centric Network as Code
	// stripe, format, and top-level sections consumed by the adapter.
	NetAsCode ciskov1.NetworkControllerNetAsCodeStatus

	// Capabilities lists independent runtime surfaces such as config,
	// inventory, events, diagnostics, or software. The set is descriptive;
	// future adapters expose behavior through their own narrow reconcilers.
	Capabilities []string

	// WorkerClusterRole is the static, install-time-audited ClusterRole that a
	// namespaced RoleBinding grants to this controller's worker ServiceAccount.
	// The registry never accepts or synthesizes policy rules at runtime.
	WorkerClusterRole string
}

// Registration is the complete composition-root entry for one controller
// product. AddToScheme is optional when an adapter needs no product-specific
// API types beyond the shared NetworkController and NetworkControllerConfig.
type Registration struct {
	Descriptor  Descriptor
	AddToScheme func(*runtime.Scheme) error
	Factory     Factory
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Registration{}
)

// Register installs a controller registration. Invalid, nil-factory, and
// duplicate registrations panic because each represents a deterministic build
// or composition bug; silently accepting one would make worker selection
// dependent on package initialization order.
func Register(reg Registration) {
	if err := validateRegistration(reg); err != nil {
		panic(fmt.Sprintf("controlleradapter.Register: %v", err))
	}
	reg = cloneRegistration(reg)
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, duplicate := registry[reg.Descriptor.Type]; duplicate {
		panic(fmt.Sprintf("controlleradapter.Register: duplicate registration for %q", reg.Descriptor.Type))
	}
	registry[reg.Descriptor.Type] = reg
}

// Registered reports whether the running binary contains an adapter for typeName.
func Registered(typeName string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[typeName]
	return ok
}

// RegisteredTypes returns all compiled-in controller types in stable order.
func RegisteredTypes() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for typeName := range registry {
		out = append(out, typeName)
	}
	sort.Strings(out)
	return out
}

// Lookup returns an ownership-independent registration for typeName.
func Lookup(typeName string) (Registration, bool) {
	registryMu.RLock()
	reg, ok := registry[typeName]
	registryMu.RUnlock()
	if !ok {
		return Registration{}, false
	}
	return cloneRegistration(reg), true
}

// DescriptorFor returns an ownership-independent descriptor for typeName.
func DescriptorFor(typeName string) (Descriptor, bool) {
	reg, ok := Lookup(typeName)
	if !ok {
		return Descriptor{}, false
	}
	return reg.Descriptor, true
}

// InstallScheme installs optional product API types for typeName. Shared API
// types remain the composition root's responsibility.
func InstallScheme(typeName string, scheme *runtime.Scheme) error {
	reg, ok := Lookup(typeName)
	if !ok {
		return unknownTypeError(typeName)
	}
	if reg.AddToScheme == nil {
		return nil
	}
	if scheme == nil {
		return fmt.Errorf("controlleradapter.InstallScheme: nil scheme for %q", typeName)
	}
	if err := reg.AddToScheme(scheme); err != nil {
		return fmt.Errorf("controlleradapter.InstallScheme %q: %w", typeName, err)
	}
	return nil
}

// NewAdapter constructs the registered adapter for typeName. The explicit
// selector must agree with the NetworkController startup snapshot, preventing
// a caller from accidentally invoking one product factory for another type.
func NewAdapter(typeName string, opts Options) (Adapter, error) {
	reg, ok := Lookup(typeName)
	if !ok {
		return nil, unknownTypeError(typeName)
	}
	if opts.Controller == nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: nil NetworkController", typeName)
	}
	if err := ciskov1.ValidateNetworkController(opts.Controller); err != nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: invalid NetworkController: %w", typeName, err)
	}
	if string(opts.Controller.Spec.Type) != typeName {
		return nil, fmt.Errorf(
			"controlleradapter.NewAdapter: selector %q does not match NetworkController spec.type %q",
			typeName, opts.Controller.Spec.Type,
		)
	}
	if err := validateWorkerMountPath("credentialPath", opts.CredentialPath, false); err != nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: %w", typeName, err)
	}
	if opts.CredentialPath != DefaultCredentialPath {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: credentialPath must equal the fixed runtime path %q", typeName, DefaultCredentialPath)
	}
	if err := validateWorkerMountPath("intentSecretPath", opts.IntentSecretPath, false); err != nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: %w", typeName, err)
	}
	if opts.IntentSecretPath != DefaultIntentSecretPath {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: intentSecretPath must equal the fixed runtime path %q", typeName, DefaultIntentSecretPath)
	}
	if err := validateWorkerMountPath("caPath", opts.CAPath, true); err != nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: %w", typeName, err)
	}
	if opts.CAPath != "" && opts.CAPath != DefaultCAPath {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: caPath must be empty or equal the fixed runtime path %q", typeName, DefaultCAPath)
	}
	if opts.MaterialRotation.Changes == nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: material rotation change channel is required", typeName)
	}
	if opts.MaterialRotation.MaxSessionLifetime <= 0 || opts.MaterialRotation.MaxSessionLifetime > 24*time.Hour {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: max session lifetime must be greater than zero and at most 24h", typeName)
	}
	cloned, err := opts.cloned()
	if err != nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: %w", typeName, err)
	}
	adapter, err := reg.Factory(cloned)
	if err != nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: %w", typeName, err)
	}
	if adapter == nil {
		return nil, fmt.Errorf("controlleradapter.NewAdapter %q: factory returned a nil adapter", typeName)
	}
	return adapter, nil
}

func validateRegistration(reg Registration) error {
	d := reg.Descriptor
	if d.Type == "" {
		return fmt.Errorf("empty controller type")
	}
	if problems := utilvalidation.IsDNS1123Label(d.Type); len(problems) > 0 {
		return fmt.Errorf("controller type %q is not a DNS-1123 label: %s", d.Type, strings.Join(problems, "; "))
	}
	if d.Type[0] < 'a' || d.Type[0] > 'z' {
		return fmt.Errorf("controller type %q must start with a lowercase letter", d.Type)
	}
	if strings.TrimSpace(d.DisplayName) == "" {
		return fmt.Errorf("controller %q has an empty display name", d.Type)
	}
	if d.DisplayName != strings.TrimSpace(d.DisplayName) {
		return fmt.Errorf("controller %q display name has leading or trailing whitespace", d.Type)
	}
	if !strings.HasPrefix(d.NetAsCode.Format, "netascode-") || len(d.NetAsCode.Format) == len("netascode-") {
		return fmt.Errorf("controller %q Network as Code format %q must use the netascode-* namespace", d.Type, d.NetAsCode.Format)
	}
	if problems := utilvalidation.IsDNS1123Label(d.NetAsCode.Format); len(problems) > 0 {
		return fmt.Errorf("controller %q Network as Code format %q is invalid: %s", d.Type, d.NetAsCode.Format, strings.Join(problems, "; "))
	}
	if strings.TrimSpace(d.NetAsCode.Stripe) == "" {
		return fmt.Errorf("controller %q has an empty Network as Code stripe", d.Type)
	}
	if !validNetAsCodeIdentifier(d.NetAsCode.Stripe) {
		return fmt.Errorf("controller %q Network as Code stripe %q must match ^[a-z][a-z0-9_]*$ and contain at most 63 characters", d.Type, d.NetAsCode.Stripe)
	}
	if len(d.NetAsCode.Sections) == 0 {
		return fmt.Errorf("controller %q has no Network as Code sections", d.Type)
	}
	if len(d.NetAsCode.Sections) > 64 {
		return fmt.Errorf("controller %q has %d Network as Code sections, maximum is 64", d.Type, len(d.NetAsCode.Sections))
	}
	if len(d.NetAsCode.ModelVersions) == 0 {
		return fmt.Errorf("controller %q has no qualified Network as Code model versions", d.Type)
	}
	if len(d.NetAsCode.ModelVersions) > 16 {
		return fmt.Errorf("controller %q has %d qualified Network as Code model versions, maximum is 16", d.Type, len(d.NetAsCode.ModelVersions))
	}
	if err := validateUniqueOpenValues("Network as Code model version", d.NetAsCode.ModelVersions); err != nil {
		return fmt.Errorf("controller %q: %w", d.Type, err)
	}
	for _, version := range d.NetAsCode.ModelVersions {
		if len(version) > 128 {
			return fmt.Errorf("controller %q Network as Code model version %q exceeds 128 characters", d.Type, version)
		}
	}
	if err := validateUniqueOpenValues("Network as Code section", d.NetAsCode.Sections); err != nil {
		return fmt.Errorf("controller %q: %w", d.Type, err)
	}
	for _, section := range d.NetAsCode.Sections {
		if !validNetAsCodeIdentifier(section) {
			return fmt.Errorf("controller %q Network as Code section %q must match ^[a-z][a-z0-9_]*$ and contain at most 63 characters", d.Type, section)
		}
	}
	if err := validateCapabilities(d.Capabilities); err != nil {
		return fmt.Errorf("controller %q: %w", d.Type, err)
	}
	if d.WorkerClusterRole == "" {
		return fmt.Errorf("controller %q has an empty worker ClusterRole", d.Type)
	}
	if problems := utilvalidation.IsDNS1123Subdomain(d.WorkerClusterRole); len(problems) > 0 {
		return fmt.Errorf("controller %q worker ClusterRole %q is invalid: %s", d.Type, d.WorkerClusterRole, strings.Join(problems, "; "))
	}
	if !auditedWorkerClusterRole(d.WorkerClusterRole) {
		return fmt.Errorf("controller %q worker ClusterRole %q is not in the audited allow-list", d.Type, d.WorkerClusterRole)
	}
	if reg.Factory == nil {
		return fmt.Errorf("controller %q has a nil factory", d.Type)
	}
	return nil
}

// auditedWorkerClusterRole is deliberately closed. Kubernetes permits a
// RoleBinding creator to bind any role whose permissions it already holds,
// even without an explicit bind grant for that role. Descriptor validation
// must therefore enforce the same narrow list as the manager RBAC markers;
// relying only on the API server's bind check could delegate manager authority
// to a worker after a descriptor typo. A future mutation role must be added
// here, to the chart, and to the manager bind markers in one reviewed change.
func auditedWorkerClusterRole(name string) bool {
	switch name {
	case DefaultWorkerClusterRole:
		return true
	default:
		return false
	}
}

func validateUniqueOpenValues(label string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", label)
		}
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("%s %q has leading or trailing whitespace", label, value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("duplicate %s %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateCapabilities(capabilities []string) error {
	if len(capabilities) > 32 {
		return fmt.Errorf("controller has %d capabilities, maximum is 32", len(capabilities))
	}
	if err := validateUniqueOpenValues("capability", capabilities); err != nil {
		return err
	}
	for _, capability := range capabilities {
		if problems := utilvalidation.IsDNS1123Label(capability); len(problems) > 0 {
			return fmt.Errorf("capability %q is not a DNS-1123 label: %s", capability, strings.Join(problems, "; "))
		}
	}
	return nil
}

func validNetAsCodeIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for i := 1; i < len(value); i++ {
		character := value[i]
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '_' {
			return false
		}
	}
	return true
}

func unknownTypeError(typeName string) error {
	registered := RegisteredTypes()
	known := "(none)"
	if len(registered) > 0 {
		known = strings.Join(registered, ", ")
	}
	return fmt.Errorf("controller type %q is not registered (registered: %s)", typeName, known)
}

func cloneRegistration(in Registration) Registration {
	out := in
	out.Descriptor = cloneDescriptor(in.Descriptor)
	return out
}

func cloneDescriptor(in Descriptor) Descriptor {
	out := in
	out.NetAsCode.ModelVersions = append([]string(nil), in.NetAsCode.ModelVersions...)
	out.NetAsCode.Sections = append([]string(nil), in.NetAsCode.Sections...)
	out.Capabilities = append([]string(nil), in.Capabilities...)
	return out
}
