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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
func (s lifecycleBackendStub) ObserveInterruptedInstall(_ context.Context, request softwarelifecycle.InterruptedInstallRequest) (softwarelifecycle.InterruptedInstallObservation, error) {
	return softwarelifecycle.InterruptedInstallObservation{
		Image:       softwarelifecycle.InventoryImage{Version: s.version, State: softwarelifecycle.InventoryStateInstalled},
		CompletedAt: request.ObservedAt,
	}, nil
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
	observer, ok := backend.(softwarelifecycle.InterruptedInstallObserver)
	if !ok {
		t.Fatal("dynamic lifecycle does not forward interrupted-install observation")
	}
	now := time.Now()
	observation, err := observer.ObserveInterruptedInstall(context.Background(), softwarelifecycle.InterruptedInstallRequest{
		TargetVersion: "17.18.04", SourceSize: 1, NotBefore: now.Add(-time.Minute), ObservedAt: now,
	})
	if err != nil || observation.Image.Version != "17.18.04" || !observation.Image.State.Activatable() {
		t.Fatalf("interrupted observation=%+v err=%v", observation, err)
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

type retirementBackendStub struct {
	lifecycleBackendStub
	observe func(context.Context, softwarelifecycle.PreparationRetirementRequest) (softwarelifecycle.PreparationRetirementObservation, error)
}

func (s retirementBackendStub) ObservePreparationRetirement(ctx context.Context, request softwarelifecycle.PreparationRetirementRequest) (softwarelifecycle.PreparationRetirementObservation, error) {
	return s.observe(ctx, request)
}

// Exercise the same lazy factory boundary used by config_reconciler, not just
// the adapter directly. Optional capabilities must survive that wrapper and
// continue using the current transport after deferred connection recovery.
func TestDynamicSoftwareLifecyclePreparationRetirement(t *testing.T) {
	kind := lifecycleTestKind("test-retirement")
	provider := &lifecycleTransportProvider{}
	var selected softwarelifecycle.Backend = lifecycleBackendStub{}
	var factoryErr error
	var calls int
	RegisterSoftwareLifecycle(kind, func(tr transport.Interface) (softwarelifecycle.Backend, error) {
		calls++
		if tr != provider.current {
			t.Fatal("retirement used a stale transport")
		}
		return selected, factoryErr
	}, func(string) error { return nil })
	backend, err := NewSoftwareLifecycle(kind, provider)
	if err != nil {
		t.Fatal(err)
	}
	observer, ok := backend.(softwarelifecycle.PreparationRetirementObserver)
	if !ok {
		t.Fatal("runtime lifecycle wrapper drops preparation retirement capability")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := softwarelifecycle.PreparationRetirementRequest{
		TargetVersion: "17.18.02", RunningVersion: "17.18.03", SourceSize: 1247897709,
		InstallStartedAt: time.Now().Add(-time.Minute), PreparedAt: time.Now(),
	}
	if _, err := observer.ObservePreparationRetirement(ctx, request); err == nil || calls != 0 {
		t.Fatalf("missing transport did not fail before factory: calls=%d err=%v", calls, err)
	}
	provider.current = &lifecycleTransportStub{kind: transport.KindRESTCONF}
	if _, err := observer.ObservePreparationRetirement(ctx, request); !errors.Is(err, softwarelifecycle.ErrUnsupported) {
		t.Fatalf("unsupported retirement error = %v", err)
	}
	want := softwarelifecycle.PreparationRetirementObservation{TargetState: softwarelifecycle.InventoryStateInstalled, EvidenceHash: "native-proof"}
	proofErr := errors.New("native proof unavailable")
	var observationErr error
	selected = retirementBackendStub{observe: func(gotCtx context.Context, got softwarelifecycle.PreparationRetirementRequest) (softwarelifecycle.PreparationRetirementObservation, error) {
		if gotCtx != ctx || !reflect.DeepEqual(got, request) {
			t.Fatal("receipt interval, context or request changed in forwarding")
		}
		return want, observationErr
	}}
	for _, nextErr := range []error{nil, proofErr, context.Canceled} {
		provider.current = &lifecycleTransportStub{kind: transport.KindRESTCONF}
		observationErr = nextErr
		if nextErr == context.Canceled {
			cancel()
		}
		got, err := observer.ObservePreparationRetirement(ctx, request)
		if !reflect.DeepEqual(got, want) || !errors.Is(err, nextErr) {
			t.Fatalf("observation=%+v err=%v, want %+v/%v", got, err, want, nextErr)
		}
	}
	factoryErr = errors.New("factory failed")
	if _, err := observer.ObservePreparationRetirement(ctx, request); !errors.Is(err, factoryErr) {
		t.Fatalf("factory error lost: %v", err)
	}
	selected, factoryErr = nil, nil
	if _, err := observer.ObservePreparationRetirement(ctx, request); err == nil {
		t.Fatal("nil backend accepted")
	}
}

type retirementReadTransport struct {
	lifecycleTransportStub
	t     *testing.T
	raw   []byte
	reads int
}

func (s *retirementReadTransport) Fetch(_ context.Context, path string) ([]byte, error) {
	s.t.Helper()
	if !strings.Contains(path, "Cisco-IOS-XE-install-oper:install-oper-data") {
		s.t.Fatalf("unexpected retirement read: %s", path)
	}
	s.reads++
	return s.raw, nil
}

func (s *retirementReadTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	s.t.Fatal("retirement opened a mutation transaction")
	return "", errors.New("unexpected mutation")
}

func (s *retirementReadTransport) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	s.t.Fatal("retirement submitted a device mutation")
	return errors.New("unexpected mutation")
}

func TestDynamicSoftwareLifecycleIOSXERetirement(t *testing.T) {
	kind := lifecycleTestKind("test-iosxe-retirement")
	RegisterSoftwareLifecycle(kind, func(tr transport.Interface) (softwarelifecycle.Backend, error) {
		return iosxelifecycle.New(tr)
	}, iosxelifecycle.ValidateDevicePath)
	const inventory = `{"install-location-information":[{"oper-state":{"sys-activity":"install-no-activity"},"install-version-info":[{"version":"17.18.03","current":"install-version-state-provisioned-committed"},{"version":"17.18.02","current":"install-version-state-installed"}]}]}`
	provider := &lifecycleTransportProvider{}
	backend, err := NewSoftwareLifecycle(kind, provider)
	if err != nil {
		t.Fatal(err)
	}
	observer, ok := backend.(softwarelifecycle.PreparationRetirementObserver)
	if !ok {
		t.Fatal("runtime wrapper hides real IOS XE retirement observer")
	}
	for _, healthy := range []bool{true, false} {
		raw := inventory
		if !healthy {
			raw = strings.ReplaceAll(raw, "install-no-activity", "install-add")
		}
		tr := &retirementReadTransport{lifecycleTransportStub: lifecycleTransportStub{kind: transport.KindRESTCONF}, t: t, raw: []byte(raw)}
		provider.current = tr
		got, err := observer.ObservePreparationRetirement(context.Background(), softwarelifecycle.PreparationRetirementRequest{TargetVersion: "17.18.02", RunningVersion: "17.18.03"})
		if (err == nil) != healthy || tr.reads != 1 {
			t.Fatalf("healthy=%v reads=%d result=%+v error=%v", healthy, tr.reads, got, err)
		}
		if healthy && (got.TargetState != softwarelifecycle.InventoryStateInstalled || len(got.EvidenceHash) != 71) {
			t.Fatalf("native proof not forwarded: %+v", got)
		}
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
