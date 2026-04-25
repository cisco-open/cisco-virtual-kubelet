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

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// resetRegistry swaps the global registry out for a clean one and
// returns a deferred function the test calls to restore. Used to
// keep these tests from leaking registrations into the
// platform-package init()s the rest of the test suite depends on.
func resetRegistry(t *testing.T) func() {
	t.Helper()
	registryMu.Lock()
	saved := registry
	registry = map[v1alpha1.DeviceDriver]Factory{}
	registryMu.Unlock()
	return func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	}
}

func TestRegisterAndNewDriverHappyPath(t *testing.T) {
	defer resetRegistry(t)()

	called := false
	Register("test-platform", func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
		called = true
		return nil, nil
	})

	spec := &v1alpha1.DeviceSpec{Driver: "test-platform"}
	if _, err := NewDriver(context.Background(), spec); err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	if !called {
		t.Error("Factory was registered but never invoked")
	}
}

func TestNewDriverUnknownKindEnumeratesRegistered(t *testing.T) {
	// Operators reading the error need to see what platforms are
	// loaded — that's how they figure out a typo (kind "xe"
	// instead of "XE") vs a missing blank-import in the binary.
	defer resetRegistry(t)()

	Register("XE", func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
		return nil, nil
	})
	Register("FAKE", func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
		return nil, nil
	})

	_, err := NewDriver(context.Background(), &v1alpha1.DeviceSpec{Driver: "ZX-Spectrum"})
	if err == nil {
		t.Fatal("expected error for unregistered kind")
	}
	if !strings.Contains(err.Error(), "FAKE") || !strings.Contains(err.Error(), "XE") {
		t.Errorf("error did not enumerate registered kinds: %v", err)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	// Duplicate registration is almost certainly a build-time bug
	// — two platforms claiming the same DeviceDriver constant.
	// Silently overwriting would mask it; we panic instead.
	defer resetRegistry(t)()

	Register("dup", func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
		return nil, nil
	})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	Register("dup", func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
		return nil, nil
	})
}

func TestRegisterNilFactoryPanics(t *testing.T) {
	defer resetRegistry(t)()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil factory")
		}
	}()
	Register("bad", nil)
}

func TestRegisteredAndRegisteredKinds(t *testing.T) {
	defer resetRegistry(t)()
	Register("B-platform", func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
		return nil, nil
	})
	Register("A-platform", func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
		return nil, nil
	})

	if !Registered("A-platform") || !Registered("B-platform") {
		t.Error("Registered() missed a platform we just added")
	}
	if Registered("not-registered") {
		t.Error("Registered() said yes for an unregistered kind")
	}
	got := RegisteredKinds()
	if len(got) != 2 || got[0] != "A-platform" || got[1] != "B-platform" {
		t.Errorf("RegisteredKinds() = %v; want sorted [A-platform B-platform]", got)
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	// Hammer Register/NewDriver/Registered concurrently to flush
	// out lock-ordering bugs. The point isn't perf — it's that the
	// race detector finds nothing.
	defer resetRegistry(t)()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			kind := v1alpha1.DeviceDriver("p" + string(rune('0'+i)))
			Register(kind, func(ctx context.Context, spec *v1alpha1.DeviceSpec) (CiscoKubernetesDeviceDriver, error) {
				return nil, nil
			})
		}()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = Registered("p0")
			_ = RegisteredKinds()
		}()
	}
	wg.Wait()
}

func TestNewDriverNilSpec(t *testing.T) {
	defer resetRegistry(t)()
	if _, err := NewDriver(context.Background(), nil); err == nil {
		t.Fatal("expected nil-spec error")
	}
}
