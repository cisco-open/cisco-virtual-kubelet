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

// Package drivers exposes the platform-agnostic driver contract
// plus a registry that platform packages populate via init() side
// effects. The registry is the foundation's only knowledge of
// which platforms exist; concrete platforms (iosxe, nxos, iosxr,
// openconfig, …) live under sibling packages and self-register
// without the foundation needing to import them.
//
// The standard pattern, mirroring database/sql and image/png:
//
//	// In the per-platform package:
//	package iosxe
//
//	func init() {
//	  drivers.Register(v1alpha1.DeviceDriverXE,
//	    func(ctx, spec) (drivers.CiscoKubernetesDeviceDriver, error) {
//	      return NewAppHostingDriver(ctx, spec)
//	    })
//	}
//
//	// In the binary's main:
//	import _ "…/internal/drivers/iosxe"   // side-effect: registers
//
// Adding a new platform never edits this file or any of its
// callers. The cost of a new driver is one register.go in the new
// package plus a `_ "…"` blank import in the binary.
package drivers

import (
	"context"
	"fmt"
	"sort"
	"sync"

	v1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

// CiscoKubernetesDeviceDriver is the apphosting-side contract every
// platform must satisfy. The configdriver-side contract is
// register.RegisterConfigDriver; the two are kept independent so a
// platform can ship apphosting first and configdriver later (the
// IOS-XE Phase-0 history) or vice versa.
//
// DeployPod takes secret and configmap listers scoped to the pod's
// namespace so a platform can pull image-pull credentials and
// referenced ConfigMap data at deploy time without the driver having
// to hold cluster-wide informers. Drivers that don't need them pass
// the listers through as `_`.
type CiscoKubernetesDeviceDriver interface {
	GetDeviceResources(ctx context.Context) (*v1.ResourceList, error)
	GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error)
	DeployPod(ctx context.Context, pod *v1.Pod, secretLister corev1listers.SecretNamespaceLister, configMapLister corev1listers.ConfigMapNamespaceLister) error
	UpdatePod(ctx context.Context, pod *v1.Pod) error
	DeletePod(ctx context.Context, pod *v1.Pod) error
	GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error)
	ListPods(ctx context.Context) ([]*v1.Pod, error)
	GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error)
}

// PodResourceListerSetter is implemented by drivers that must resolve pod
// Secret or ConfigMap references after the initial DeployPod call. The
// provider supplies its shared informer listers once at construction time;
// implementations must still scope every lookup to the pod namespace.
type PodResourceListerSetter interface {
	SetPodResourceListers(corev1listers.SecretLister, corev1listers.ConfigMapLister)
}

// TopologyProvider is an optional interface that drivers may implement to
// expose network topology and hosted-app data for observability features
// (OTEL traces, node annotations, metrics). Consumers should use a type
// assertion to check whether the driver supports it.
type TopologyProvider interface {
	GetCDPNeighbors(ctx context.Context) ([]common.CDPNeighbor, error)
	GetOSPFNeighbors(ctx context.Context) ([]common.OSPFNeighbor, error)
	GetInterfaceStats(ctx context.Context) ([]common.InterfaceStats, error)
	GetInterfaceIPs(ctx context.Context) ([]common.InterfaceIP, error)
	GetHostedApps(ctx context.Context) ([]common.HostedApp, error)
}

// GNOICapable is an optional interface that drivers implement to expose
// a per-device gNOI client. The DeviceOperation reconciler (read-only
// gNOI ops), IOSXESoftwareUpgrade reconciler (OS install/activate/
// verify), and IOSXEOperationalAction reconciler (write-class gNOI
// ops) all type-assert to this interface; absent it, those reconcilers
// fail fast with reason GNOIUnsupported instead of attempting RPCs.
//
// Implementations should construct the gNOI client lazily on first
// call and cache it for the lifetime of the device worker — the
// underlying conn is leased from a devicegrpc.Pool and the lease is
// released when the worker tears down.
type GNOICapable interface {
	// GNOIClient returns a non-nil *gnoi.Client. The return type is
	// declared as any here to keep this package free of the gnoi
	// import; consumers cast to *gnoi.Client at the call site. This
	// keeps internal/drivers/registry.go from depending on the gnoi
	// package, preserving the import-graph cleanliness that lets
	// drivers/registry.go be imported by both drivers and provider-
	// side reconcilers.
	GNOIClient(ctx context.Context) (any, error)
}

// Factory is the per-platform constructor signature. Every Register
// call hands one of these in.
type Factory func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error)

var (
	registryMu sync.RWMutex
	registry   = map[v1alpha1.DeviceDriver]Factory{}
)

// Register installs a Factory for kind. Intended to be called from
// init() in a platform package. Registering the same kind twice
// panics — that's almost always a build-time bug (two platforms
// claiming the same DeviceDriver constant) and silently
// overwriting the first registration would mask it.
func Register(kind v1alpha1.DeviceDriver, factory Factory) {
	if factory == nil {
		panic(fmt.Sprintf("drivers.Register: nil factory for %q", kind))
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[kind]; dup {
		panic(fmt.Sprintf("drivers.Register: duplicate registration for %q", kind))
	}
	registry[kind] = factory
}

// NewDriver looks the registered Factory up by spec.Driver and
// invokes it. Returns a clear "unregistered driver" error when the
// platform package wasn't imported into the binary — which is the
// only way a Driver kind can be missing from the registry at
// runtime.
func NewDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
	if spec == nil {
		return nil, fmt.Errorf("drivers.NewDriver: nil DeviceSpec")
	}
	registryMu.RLock()
	f, ok := registry[spec.Driver]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf(
			"drivers.NewDriver: driver kind %q is not registered (registered: %s)",
			spec.Driver, registeredKinds())
	}
	return f(ctx, spec)
}

// Registered reports whether kind has a Factory available. Useful
// for the aggregator's "should I take this device?" guard so it
// can silently skip platforms it doesn't have a writer set for.
func Registered(kind v1alpha1.DeviceDriver) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[kind]
	return ok
}

// RegisteredKinds returns the sorted list of registered driver
// kinds. Used by tooling that wants to enumerate the platforms
// the running binary knows about.
func RegisteredKinds() []v1alpha1.DeviceDriver {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]v1alpha1.DeviceDriver, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}

// registeredKinds is the stringified form for error messages.
// Held under the lock by RegisteredKinds; the helper here re-reads
// to avoid a recursive lock acquire.
func registeredKinds() string {
	kinds := RegisteredKinds()
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = string(k)
	}
	if len(parts) == 0 {
		return "(none)"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}
