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

package aggregator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

const versionedAggDriver ciskov1.DeviceDriver = "AGGREGATOR_VERSIONED_TEST"

type releaseObservation struct {
	release string
	legacy  bool
}

var (
	registerVersionedAggOnce sync.Once
	versionedAggMu           sync.RWMutex
	versionedAggVersions     map[string]string
	versionedAggObservations chan releaseObservation
)

func registerVersionedAggDriver(t *testing.T) {
	t.Helper()
	registerVersionedAggOnce.Do(func() {
		drivers.RegisterConfigDriver(
			versionedAggDriver,
			func(_ context.Context, spec *ciskov1.DeviceSpec, _ string, _ drivers.ConfigDriverOptions) (*drivers.ConfigDriverContext, error) {
				versionedAggMu.RLock()
				version := versionedAggVersions[spec.Address]
				ch := versionedAggObservations
				versionedAggMu.RUnlock()
				return &drivers.ConfigDriverContext{
					Transport:     &stubTransport{},
					DeviceVersion: version,
					LookupWriter: func(family, release string) writers.SectionWriter {
						return &releaseProbeWriter{
							family:  family,
							release: release,
							ch:      ch,
						}
					},
				}, nil
			},
		)
	})
}

func configureVersionedAggTest(t *testing.T, versions map[string]string, ch chan releaseObservation) {
	t.Helper()
	registerVersionedAggDriver(t)
	versionedAggMu.Lock()
	versionedAggVersions = versions
	versionedAggObservations = ch
	versionedAggMu.Unlock()
	t.Cleanup(func() {
		versionedAggMu.Lock()
		versionedAggVersions = nil
		versionedAggObservations = nil
		versionedAggMu.Unlock()
	})
}

type releaseProbeWriter struct {
	family  string
	release string
	ch      chan<- releaseObservation
}

func (w *releaseProbeWriter) Family() string      { return w.family }
func (w *releaseProbeWriter) YANGPaths() []string { return []string{"/" + w.family} }
func (w *releaseProbeWriter) Fetch(context.Context, transport.Interface) (any, error) {
	return map[string]any{}, nil
}
func (w *releaseProbeWriter) Diff(any, any) ([]transport.Op, error) {
	resolver, err := writers.NewOverrideResolver(w.release)
	if err != nil {
		return nil, err
	}
	w.ch <- releaseObservation{
		release: w.release,
		legacy:  resolver.IsLegacyVersion("snmp_server"),
	}
	return nil, nil
}
func (w *releaseProbeWriter) Apply(context.Context, transport.Interface, []transport.Op) error {
	return nil
}

func TestAggregatorWorkersUsePerDeviceWriterResolvers(t *testing.T) {
	ch := make(chan releaseObservation, 4)
	configureVersionedAggTest(t, map[string]string{
		"10.0.16.1": "17.16.01a",
		"10.0.18.1": "17.18.2",
	}, ch)

	scheme := aggScheme(t)
	dev1716 := versionedDevice("edge-1716", "10.0.16.1")
	dev1718 := versionedDevice("edge-1718", "10.0.18.1")
	cr1716 := versionedConfig("cfg-1716", dev1716.Name)
	cr1718 := versionedConfig("cfg-1718", dev1718.Name)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		WithObjects(dev1716, dev1718, cr1716, cr1718).
		Build()

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &AggregatedReconciler{
		Client:  c,
		Scheme:  scheme,
		managed: map[string]*deviceWorker{},
		rootCtx: rootCtx,
	}

	var wg sync.WaitGroup
	for _, dev := range []*ciskov1.CiscoDevice{dev1716, dev1718} {
		wg.Add(1)
		go func(dev *ciskov1.CiscoDevice) {
			defer wg.Done()
			req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}}
			if _, err := r.Reconcile(rootCtx, req); err != nil {
				t.Errorf("Reconcile %s: %v", dev.Name, err)
			}
		}(dev)
	}
	wg.Wait()

	got := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case obs := <-ch:
			got[obs.release] = obs.legacy
		case <-deadline:
			t.Fatalf("timed out waiting for writer observations; got %v", got)
		}
	}
	if legacy, ok := got["17.16.01a"]; !ok || !legacy {
		t.Fatalf("17.16 writer observation = %v ok=%v, want legacy=true", legacy, ok)
	}
	if legacy, ok := got["17.18.2"]; !ok || legacy {
		t.Fatalf("17.18 writer observation = %v ok=%v, want legacy=false", legacy, ok)
	}
}

func TestAggregatorUnsupportedVersionDoesNotStartWorker(t *testing.T) {
	ch := make(chan releaseObservation, 1)
	configureVersionedAggTest(t, map[string]string{
		"10.0.99.1": "17.99.0",
	}, ch)

	scheme := aggScheme(t)
	dev := versionedDevice("edge-1799", "10.0.99.1")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dev).Build()
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := record.NewFakeRecorder(4)
	r := &AggregatedReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: rec,
		managed:  map[string]*deviceWorker{},
		rootCtx:  rootCtx,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}}

	if _, err := r.Reconcile(rootCtx, req); err == nil {
		t.Fatal("Reconcile returned nil error for unsupported device version")
	}
	r.mu.Lock()
	_, present := r.managed[req.String()]
	r.mu.Unlock()
	if present {
		t.Fatalf("unsupported device version started worker %q", req.String())
	}
	select {
	case obs := <-ch:
		t.Fatalf("writer was called for unsupported device version: %+v", obs)
	default:
	}
	assertEventContains(t, rec, "AggregatorUnsupportedDeviceVersion")
}

func TestAggregatorRetryDeviceVersionContinuesAfterUnsupported(t *testing.T) {
	oldInterval := deviceVersionRetryInterval
	deviceVersionRetryInterval = 10 * time.Millisecond
	t.Cleanup(func() { deviceVersionRetryInterval = oldInterval })

	versions := make(chan string, 3)
	versions <- ""
	versions <- "17.99.0"
	versions <- "17.16.01a"
	attempts := make(chan string, 3)

	rec := &provider.ConfigReconciler{RequireDeviceVersion: true}
	dctx := &drivers.ConfigDriverContext{
		Transport: &stubTransport{},
		FetchDeviceVersion: func(context.Context, transport.Interface) string {
			v := <-versions
			attempts <- v
			return v
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		retryDeviceVersion(ctx, rec, dctx)
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("retryDeviceVersion did not accept the later supported version")
	}
	if rec.DeviceVersion != "17.16.01a" {
		t.Fatalf("DeviceVersion=%q, want 17.16.01a", rec.DeviceVersion)
	}
	if rec.DeviceVersionError != nil {
		t.Fatalf("DeviceVersionError=%v, want nil", rec.DeviceVersionError)
	}
	if got := len(attempts); got != 3 {
		t.Fatalf("attempts=%d, want 3 (empty, unsupported, supported)", got)
	}
}

func versionedDevice(name, address string) *ciskov1.CiscoDevice {
	return &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agg-version-test"},
		Spec: ciskov1.DeviceSpec{
			Driver:   versionedAggDriver,
			Address:  address,
			Username: "u",
			Password: "inline",
		},
		Status: ciskov1.DeviceStatus{
			Conditions: []metav1.Condition{{
				Type:   ciskov1.CiscoDeviceConditionAggregatorOwned,
				Status: metav1.ConditionTrue,
				Reason: "AggregatorEnabled",
			}},
		},
	}
}

func versionedConfig(name, device string) *configv1alpha1.IOSXEConfig {
	return &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agg-version-test"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"snmp_server"},
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte(`{"snmp_server":{"contact":"noc@example"}}`)},
				},
			},
		},
	}
}

func assertEventContains(t *testing.T, rec *record.FakeRecorder, want string) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case got := <-rec.Events:
			if strings.Contains(got, want) {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for event containing %q", want)
		}
	}
}
