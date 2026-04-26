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

package drivers

// This file is the configdriver counterpart of the apphosting
// registry in registry.go. The apphosting registry produces a
// CiscoKubernetesDeviceDriver per CiscoDevice; this one produces a
// ConfigDriverContext, which is everything the platform-agnostic
// `provider.ConfigReconciler` needs to talk to one device's
// configuration plane.
//
// Why two registries: a platform may ship apphosting first and
// configdriver later (the IOS-XE Phase 0/1 history) or vice versa
// (a platform whose apphosting story is "use upstream NXAPI" but
// which still wants config-side reconciliation). Splitting the
// registries lets each capability roll out independently — and lets
// a binary that is configdriver-only (e.g. the aggregator pod) avoid
// pulling in the apphosting Pod-lifecycle code.

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// ConfigDriverContext bundles the platform-specific knobs the
// platform-agnostic `provider.ConfigReconciler` needs to handle one
// device. Every platform's ConfigDriverFactory returns one of these.
//
// The transport package and the intent/writers packages live under
// `internal/drivers/iosxe/configdriver/` for historical reasons —
// the actual code is platform-agnostic at the contract level
// (transport.Interface accepts any RESTCONF/NETCONF/gNMI device;
// writers.SectionWriter is shape-driven, not platform-driven). A
// future cleanup may relocate these into `internal/configdriver/`
// to remove the misleading import path; until then, treat the
// `iosxe/configdriver/` namespace as the platform-agnostic core.
type ConfigDriverContext struct {
	// Transport is the open device channel. Each platform's
	// factory builds it per spec.Transport (restconf / netconf /
	// gnmi) plus any platform-specific RPC names (Cisco-IA for
	// IOS-XE, NX-API for NX-OS, etc.).
	Transport transport.Interface

	// KeyRules describes which leaf is the identity for each
	// keyed list the writers manage. Platform-specific because
	// every platform's YANG model has its own list shapes.
	KeyRules intent.KeyRules

	// SupportedYANGVersions is the closed set of release tags the
	// platform's writers know about. Empty disables validation.
	SupportedYANGVersions map[string]struct{}

	// DefaultYANGVersion is what the resolver assigns when an
	// IOSXEConfig (or analogous CR) doesn't pin one. Empty leaves
	// it empty.
	DefaultYANGVersion string

	// LookupWriter is the per-platform writer registry's Get
	// function. NX-OS and IOS-XE writer registries are independent
	// even though they share the writers.SectionWriter contract.
	LookupWriter func(family string) writers.SectionWriter

	// SubscribePaths is the union of YANG paths the platform's
	// writers care about. The drift-detect Subscribe watcher
	// (gNMI on-change) opens a stream against these. Empty
	// disables the subscribe fast path; the reconciler stays on
	// its periodic ticker.
	SubscribePaths []string

	// FamilyOrder is the optional cross-family ordering hook the
	// engine consults during Wave 10.3 atomic-replace reconciles.
	// Platforms that ship a topo-sortable schema (IOS-XE families.yaml
	// declares depends_on for cross-family dependencies like
	// interface_ethernet → vrf) provide a closure that returns the
	// input families in dependency order so adds run parent-first.
	// Nil means "operator-determined order" (the pre-Wave-10
	// default) and is the safe fallback when a platform's schema
	// doesn't declare dependencies.
	FamilyOrder func([]string) []string
}

// ConfigDriverFactory is the per-platform constructor signature.
// password is resolved by the caller (from spec.password or the
// referenced Secret) so platforms don't need to know about
// Kubernetes Secrets.
type ConfigDriverFactory func(ctx context.Context, spec *v1alpha1.DeviceSpec, password string, opts ConfigDriverOptions) (*ConfigDriverContext, error)

// ConfigDriverOptions carries call-site parameters that are not
// part of the device spec — typically a SessionLock to share with
// the apphosting driver, or factory-level timeouts.
type ConfigDriverOptions struct {
	// SessionLock optionally serialises config-driver writes
	// against apphosting writes on the same device. Mirrors the
	// *sync.Mutex transport.RESTCONFConfig.SessionLock accepts.
	SessionLock *sync.Mutex
}

var (
	configDriverRegistryMu sync.RWMutex
	configDriverRegistry   = map[v1alpha1.DeviceDriver]ConfigDriverFactory{}
)

// RegisterConfigDriver installs a ConfigDriverFactory for kind.
// Intended for init() in a platform package alongside the apphosting
// Register call. Same duplicate-registration panic policy as the
// apphosting registry.
func RegisterConfigDriver(kind v1alpha1.DeviceDriver, factory ConfigDriverFactory) {
	if factory == nil {
		panic(fmt.Sprintf("drivers.RegisterConfigDriver: nil factory for %q", kind))
	}
	configDriverRegistryMu.Lock()
	defer configDriverRegistryMu.Unlock()
	if _, dup := configDriverRegistry[kind]; dup {
		panic(fmt.Sprintf("drivers.RegisterConfigDriver: duplicate registration for %q", kind))
	}
	configDriverRegistry[kind] = factory
}

// NewConfigDriver looks up and invokes the factory registered for
// spec.Driver. Platforms with apphosting but no configdriver yet
// (e.g. an early-iteration NX-OS driver) report the absence here
// so the aggregator can silently skip them rather than crash.
func NewConfigDriver(ctx context.Context, spec *v1alpha1.DeviceSpec, password string, opts ConfigDriverOptions) (*ConfigDriverContext, error) {
	if spec == nil {
		return nil, fmt.Errorf("drivers.NewConfigDriver: nil DeviceSpec")
	}
	configDriverRegistryMu.RLock()
	f, ok := configDriverRegistry[spec.Driver]
	configDriverRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf(
			"drivers.NewConfigDriver: no config-driver factory registered for kind %q (registered: %s)",
			spec.Driver, registeredConfigDriverKinds())
	}
	return f(ctx, spec, password, opts)
}

// ConfigDriverRegistered reports whether kind has a ConfigDriverFactory
// available. Aggregator + cisco-vk run consult this before building
// a worker; an unregistered kind silently skips, so a binary without
// (say) the OpenConfig blank import doesn't crash on OPENCONFIG
// CiscoDevices, it just leaves them un-config-managed.
func ConfigDriverRegistered(kind v1alpha1.DeviceDriver) bool {
	configDriverRegistryMu.RLock()
	defer configDriverRegistryMu.RUnlock()
	_, ok := configDriverRegistry[kind]
	return ok
}

// RegisteredConfigDriverKinds is the configdriver counterpart of
// RegisteredKinds. Useful for `cisco-vk` log lines at startup so
// operators can see what's plugged in.
func RegisteredConfigDriverKinds() []v1alpha1.DeviceDriver {
	configDriverRegistryMu.RLock()
	defer configDriverRegistryMu.RUnlock()
	out := make([]v1alpha1.DeviceDriver, 0, len(configDriverRegistry))
	for k := range configDriverRegistry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}

func registeredConfigDriverKinds() string {
	kinds := RegisteredConfigDriverKinds()
	if len(kinds) == 0 {
		return "(none)"
	}
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = string(k)
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}
