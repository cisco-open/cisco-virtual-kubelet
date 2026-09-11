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
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	iosxelifecycle "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/softwarelifecycle"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

var lifecycleTestKindCounter atomic.Uint64

func lifecycleTestKind(prefix string) v1alpha1.DeviceDriver {
	return v1alpha1.DeviceDriver(fmt.Sprintf("%s-%d", prefix, lifecycleTestKindCounter.Add(1)))
}

type lifecycleTransportProvider struct{ current transport.Interface }

func (p *lifecycleTransportProvider) GetTransport() transport.Interface { return p.current }

type lifecycleBackendStub struct{ version string }

func (lifecycleBackendStub) ValidateDeviceFilePath(string) error { return nil }

func (s lifecycleBackendStub) Inspect(context.Context, string) (softwarelifecycle.InventoryImage, error) {
	return softwarelifecycle.InventoryImage{Version: s.version, State: softwarelifecycle.InventoryStatePresent}, nil
}
func (lifecycleBackendStub) RegisterDeviceFile(context.Context, softwarelifecycle.DeviceFileRequest) (softwarelifecycle.DeviceFileRegistration, error) {
	return softwarelifecycle.DeviceFileRegistration{}, nil
}
func (lifecycleBackendStub) ObserveDeviceFile(context.Context, string, string) (softwarelifecycle.DeviceFileObservation, error) {
	return softwarelifecycle.DeviceFileObservation{}, nil
}

func TestNewSoftwareLifecycleUnsupported(t *testing.T) {
	_, err := NewSoftwareLifecycle(v1alpha1.DeviceDriver("test-no-lifecycle"), &lifecycleTransportProvider{})
	if !errors.Is(err, softwarelifecycle.ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestDynamicSoftwareLifecycleUsesCurrentTransport(t *testing.T) {
	kind := lifecycleTestKind("test-dynamic-lifecycle")
	var factoryCalls int
	RegisterSoftwareLifecycle(
		kind,
		func(tr transport.Interface) (softwarelifecycle.Backend, error) {
			factoryCalls++
			if tr == nil {
				t.Fatal("factory received nil transport")
			}
			return lifecycleBackendStub{version: "17.18.04"}, nil
		},
		func(string) error { return nil },
	)
	provider := &lifecycleTransportProvider{}
	backend, err := NewSoftwareLifecycle(kind, provider)
	if err != nil {
		t.Fatalf("NewSoftwareLifecycle: %v", err)
	}
	if _, err := backend.Inspect(context.Background(), "17.18.04"); err == nil {
		t.Fatal("Inspect succeeded without a current transport")
	}
	provider.current = &lifecycleTransportStub{}
	image, err := backend.Inspect(context.Background(), "17.18.04")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if image.Version != "17.18.04" || factoryCalls != 1 {
		t.Fatalf("image=%+v factoryCalls=%d", image, factoryCalls)
	}
}

func TestDynamicSoftwareLifecycleValidatesIOSXEPathWithoutTransport(t *testing.T) {
	kind := lifecycleTestKind("test-iosxe-path-validation")
	var factoryCalls int
	RegisterSoftwareLifecycle(
		kind,
		func(tr transport.Interface) (softwarelifecycle.Backend, error) {
			factoryCalls++
			return iosxelifecycle.New(tr)
		},
		iosxelifecycle.ValidateDevicePath,
	)

	backend, err := NewSoftwareLifecycle(kind, &lifecycleTransportProvider{})
	if err != nil {
		t.Fatalf("NewSoftwareLifecycle: %v", err)
	}
	if err := backend.ValidateDeviceFilePath("flash:cat9k_iosxe.17.18.04.SPA.bin"); err != nil {
		t.Fatalf("valid IOS XE path rejected without transport: %v", err)
	}
	if err := backend.ValidateDeviceFilePath("flash:../cat9k.bin"); !errors.Is(err, softwarelifecycle.ErrInvalidDevicePath) {
		t.Fatalf("invalid IOS XE path error = %v, want ErrInvalidDevicePath", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want 0 for pure path validation", factoryCalls)
	}
}

func TestDynamicSoftwareLifecyclePreservesUnsupportedTransportClassification(t *testing.T) {
	kind := lifecycleTestKind("test-iosxe-unsupported-transport")
	RegisterSoftwareLifecycle(
		kind,
		func(tr transport.Interface) (softwarelifecycle.Backend, error) {
			return iosxelifecycle.New(tr)
		},
		iosxelifecycle.ValidateDevicePath,
	)
	provider := &lifecycleTransportProvider{
		current: &lifecycleTransportStub{kind: transport.KindGNMI},
	}
	backend, err := NewSoftwareLifecycle(kind, provider)
	if err != nil {
		t.Fatalf("NewSoftwareLifecycle: %v", err)
	}
	if err := backend.ValidateDeviceFilePath("bootflash:cat9k.bin"); err != nil {
		t.Fatalf("valid IOS XE path rejected: %v", err)
	}
	if _, err := backend.Inspect(context.Background(), "17.18.04"); !errors.Is(err, softwarelifecycle.ErrUnsupported) {
		t.Fatalf("Inspect error = %v, want ErrUnsupported", err)
	}
}

type lifecycleTransportStub struct{ kind transport.Kind }

func (s *lifecycleTransportStub) Capabilities() transport.Capabilities {
	return transport.Capabilities{Kind: s.kind}
}
func (*lifecycleTransportStub) Fetch(context.Context, string) ([]byte, error) { return nil, nil }
func (*lifecycleTransportStub) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", nil
}
func (*lifecycleTransportStub) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	return nil
}
func (*lifecycleTransportStub) Commit(context.Context, transport.TxHandle) error  { return nil }
func (*lifecycleTransportStub) Discard(context.Context, transport.TxHandle) error { return nil }
func (*lifecycleTransportStub) SaveStartup(context.Context) error                 { return nil }
func (*lifecycleTransportStub) Close() error                                      { return nil }
