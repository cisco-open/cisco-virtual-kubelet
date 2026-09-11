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
	"sync"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

// SoftwareLifecycleTransportProvider exposes the current per-device
// configuration transport. The getter is intentionally evaluated for every
// operation because deferred device dial recovery can replace the transport
// after the controller has started.
type SoftwareLifecycleTransportProvider interface {
	GetTransport() configtransport.Interface
}

// SoftwareLifecycleFactory constructs a platform adapter over an existing
// device transport. It must not open an independent management session.
type SoftwareLifecycleFactory func(configtransport.Interface) (softwarelifecycle.Backend, error)

// SoftwareLifecyclePathValidator validates a platform-local image path without
// consulting a device or transport. It must enforce the same policy as the
// backend and return softwarelifecycle.ErrInvalidDevicePath for rejected input.
// Keeping it separate from the factory lets preflight validation run while a
// deferred device dial is still recovering.
type SoftwareLifecyclePathValidator func(path string) error

type softwareLifecycleRegistration struct {
	factory      SoftwareLifecycleFactory
	validatePath SoftwareLifecyclePathValidator
}

var (
	softwareLifecycleMu       sync.RWMutex
	softwareLifecycleRegistry = map[v1alpha1.DeviceDriver]softwareLifecycleRegistration{}
)

// RegisterSoftwareLifecycle registers an optional native software lifecycle
// capability for a platform. Registration is independent from app hosting and
// configuration support so future drivers can add it without widening either
// contract.
func RegisterSoftwareLifecycle(
	kind v1alpha1.DeviceDriver,
	factory SoftwareLifecycleFactory,
	validatePath SoftwareLifecyclePathValidator,
) {
	if factory == nil {
		panic(fmt.Sprintf("drivers.RegisterSoftwareLifecycle: nil factory for %q", kind))
	}
	if validatePath == nil {
		panic(fmt.Sprintf("drivers.RegisterSoftwareLifecycle: nil path validator for %q", kind))
	}
	softwareLifecycleMu.Lock()
	defer softwareLifecycleMu.Unlock()
	if _, exists := softwareLifecycleRegistry[kind]; exists {
		panic(fmt.Sprintf("drivers.RegisterSoftwareLifecycle: duplicate registration for %q", kind))
	}
	softwareLifecycleRegistry[kind] = softwareLifecycleRegistration{
		factory:      factory,
		validatePath: validatePath,
	}
}

// NewSoftwareLifecycle returns a lazy backend that always uses the provider's
// current transport. Unsupported platforms fail here; temporarily unavailable
// transports surface only when an operation is attempted.
func NewSoftwareLifecycle(kind v1alpha1.DeviceDriver, provider SoftwareLifecycleTransportProvider) (softwarelifecycle.Backend, error) {
	if provider == nil {
		return nil, errors.New("drivers.NewSoftwareLifecycle: nil transport provider")
	}
	softwareLifecycleMu.RLock()
	registration, exists := softwareLifecycleRegistry[kind]
	softwareLifecycleMu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("drivers.NewSoftwareLifecycle: driver %q: %w", kind, softwarelifecycle.ErrUnsupported)
	}
	return &dynamicSoftwareLifecycle{
		provider:     provider,
		factory:      registration.factory,
		validatePath: registration.validatePath,
	}, nil
}

type dynamicSoftwareLifecycle struct {
	provider     SoftwareLifecycleTransportProvider
	factory      SoftwareLifecycleFactory
	validatePath SoftwareLifecyclePathValidator
}

func (d *dynamicSoftwareLifecycle) backend() (softwarelifecycle.Backend, error) {
	tr := d.provider.GetTransport()
	if tr == nil {
		return nil, errors.New("software lifecycle transport is not available")
	}
	backend, err := d.factory(tr)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, errors.New("software lifecycle factory returned a nil backend")
	}
	return backend, nil
}

func (d *dynamicSoftwareLifecycle) Inspect(ctx context.Context, targetVersion string) (softwarelifecycle.InventoryImage, error) {
	backend, err := d.backend()
	if err != nil {
		return softwarelifecycle.InventoryImage{}, err
	}
	return backend.Inspect(ctx, targetVersion)
}

func (d *dynamicSoftwareLifecycle) ValidateDeviceFilePath(path string) error {
	return d.validatePath(path)
}

func (d *dynamicSoftwareLifecycle) RegisterDeviceFile(ctx context.Context, request softwarelifecycle.DeviceFileRequest) (softwarelifecycle.DeviceFileRegistration, error) {
	backend, err := d.backend()
	if err != nil {
		return softwarelifecycle.DeviceFileRegistration{}, err
	}
	return backend.RegisterDeviceFile(ctx, request)
}

func (d *dynamicSoftwareLifecycle) ObserveDeviceFile(ctx context.Context, operationID, targetVersion string) (softwarelifecycle.DeviceFileObservation, error) {
	backend, err := d.backend()
	if err != nil {
		return softwarelifecycle.DeviceFileObservation{}, err
	}
	return backend.ObserveDeviceFile(ctx, operationID, targetVersion)
}
