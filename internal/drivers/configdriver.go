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

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/validation"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	"k8s.io/apimachinery/pkg/runtime"
)

// ConfigDriverContext bundles the platform-specific knobs the
// platform-agnostic `provider.ConfigReconciler` needs to handle one
// device. Every platform's ConfigDriverFactory returns one of these.
//
// The neutral contracts live under internal/configengine. During
// the extraction they still alias the mature IOS-XE implementation
// underneath, but new platform code should depend on the neutral
// package surface rather than the historical IOS-XE path.
type ConfigDriverContext struct {
	// PlatformName is the stable lowercase platform key, for example
	// "iosxe" or "nxos".
	PlatformName string

	// ModelFormat is the NetAsCode model format the platform expects when
	// the CR records spec.modelSource.format.
	ModelFormat configv1alpha1.NetAsCodeModelFormat

	// ConfigObject and ConfigList identify the platform CRD handled by this
	// context. They let startup/runtime registries reason about CRD shape
	// without switch statements. The objects are prototypes and must not be
	// mutated by callers.
	ConfigObject runtime.Object
	ConfigList   runtime.Object

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
	LookupWriter func(family, release string) writers.SectionWriter

	// SubscribePaths is the union of YANG paths the platform's
	// writers care about. The drift-detect Subscribe watcher
	// (gNMI on-change) opens a stream against these. Empty
	// disables the subscribe fast path; the reconciler stays on
	// its periodic ticker.
	SubscribePaths []string

	// DeviceVersion is the platform software version string reported
	// by the device (e.g. IOS-XE "17.16.01a"). When non-empty, the startup
	// code validates this against the version-conditional writer
	// support table. The factory may leave this empty if the version
	// isn't known at construction time; retry loops set it lazily on
	// the first successful device query.
	DeviceVersion string

	// DeviceVersionPolicy owns platform-native release validation and error
	// classification. A nil Validate callback disables release gating for a
	// platform that has not published a support matrix yet.
	DeviceVersionPolicy DeviceVersionPolicy

	// FetchDeviceVersion optionally refreshes DeviceVersion from the
	// live transport. Aggregator mode uses this to retry startup-time
	// empty version reads without importing platform-specific packages.
	FetchDeviceVersion func(context.Context, transport.Interface) string

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

	// OperationValidator validates writer-produced device operations before
	// mutation. IOS XE decorates the common structural validator with YANG
	// release profiles; NX-OS validates DME scope and envelope structure.
	OperationValidator      validation.Validator
	OperationValidationMode validation.Mode
}

// DeviceVersionPolicy isolates platform release parsing from common startup
// and reconciliation code.
type DeviceVersionPolicy struct {
	Validate      writers.VersionValidator
	IsUnsupported writers.VersionErrorClassifier
	IsMalformed   writers.VersionErrorClassifier
	ReleaseTag    func(version string) (string, bool)
	Require       bool
}

// ValidateDeviceVersion validates DeviceVersion against the version-
// conditional writer support table. It does not mutate writer state;
// writer instances bind immutable per-device resolvers at lookup time.
// No-op when DeviceVersion is empty or the platform has not supplied a
// validator. Callers must propagate a validation error so writes fail closed.
//
// Both startup paths (cmd/cisco-vk and the aggregator) call this
// after constructing the context. The function lives on the context
// rather than in cisco-vk so the aggregator can stay free of any
// platform-specific imports.
func (c *ConfigDriverContext) ValidateDeviceVersion() error {
	if c == nil || c.DeviceVersion == "" || c.DeviceVersionPolicy.Validate == nil {
		return nil
	}
	return c.DeviceVersionPolicy.Validate(c.DeviceVersion)
}

// IsUnsupportedDeviceVersionError reports whether err is the
// "device version is not in the supported release set" sentinel.
// The classifier is supplied by the platform policy.
func (c *ConfigDriverContext) IsUnsupportedDeviceVersionError(err error) bool {
	return c != nil && c.DeviceVersionPolicy.IsUnsupported != nil && c.DeviceVersionPolicy.IsUnsupported(err)
}

// IsMalformedDeviceVersionError reports whether err means the device
// version string could not be parsed as major.minor.
func (c *ConfigDriverContext) IsMalformedDeviceVersionError(err error) bool {
	return c != nil && c.DeviceVersionPolicy.IsMalformed != nil && c.DeviceVersionPolicy.IsMalformed(err)
}

// IsRetryableDeviceVersionError reports validation failures that can be
// transient during device boot or image upgrade. Callers should keep
// retrying version discovery instead of pinning the reconciler until a
// pod restart.
func (c *ConfigDriverContext) IsRetryableDeviceVersionError(err error) bool {
	return c.IsUnsupportedDeviceVersionError(err) || c.IsMalformedDeviceVersionError(err)
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
	// against apphosting writes on the same device. Platform
	// factories attach it to transports that share a stateful device
	// session, such as IOS-XE RESTCONF.
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
