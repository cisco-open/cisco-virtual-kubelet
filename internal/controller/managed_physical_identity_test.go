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

package controller

import (
	"context"
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

type countingCiscoDeviceListReader struct {
	client.Reader
	mu    sync.Mutex
	lists int
}

func (r *countingCiscoDeviceListReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*ciskov1.CiscoDeviceList); ok {
		r.mu.Lock()
		r.lists++
		r.mu.Unlock()
	}
	return r.Reader.List(ctx, list, opts...)
}

func (r *countingCiscoDeviceListReader) listCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lists
}

func TestValidateManagedPhysicalIdentityIsClusterWideAndCaseCanonical(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-a", "site-a")
	device.UID = "device-a-uid"
	device.Spec.PhysicalIdentity = "FOC2416U0MV"
	duplicate := newDevice("switch-b", "site-b")
	duplicate.UID = "device-b-uid"
	duplicate.Spec.PhysicalIdentity = "foc2416u0mv"
	duplicate.Finalizers = []string{"test.cisco.vk/retain"}
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, duplicate).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()}

	if _, err := r.validateManagedPhysicalIdentity(ctx, device, policy); err == nil ||
		!strings.Contains(err.Error(), "site-b/switch-b") {
		t.Fatalf("duplicate physical identity error = %v", err)
	}
	// A terminating duplicate remains part of the safety inventory until the API
	// object is actually gone.
	if err := apiClient.Delete(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := r.validateManagedPhysicalIdentity(ctx, device, policy); err == nil ||
		!strings.Contains(err.Error(), "site-b/switch-b") {
		t.Fatalf("terminating duplicate physical identity error = %v", err)
	}
	var terminating ciskov1.CiscoDevice
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(duplicate), &terminating); err != nil {
		t.Fatal(err)
	}
	terminating.Finalizers = nil
	if err := apiClient.Update(ctx, &terminating); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(duplicate), &terminating); err == nil {
		if err := apiClient.Delete(ctx, &terminating); err != nil {
			t.Fatal(err)
		}
	} else if client.IgnoreNotFound(err) != nil {
		t.Fatal(err)
	}
	// A new reconciler instance must recover the same declared authority from the
	// persisted spec without trusting prior in-memory or Node status state.
	restarted := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}
	got, err := restarted.validateManagedPhysicalIdentity(ctx, device, policy)
	if err != nil || got != "foc2416u0mv" {
		t.Fatalf("restart identity = %q, %v", got, err)
	}
}

func TestValidateManagedPhysicalIdentityFailsClosed(t *testing.T) {
	device := newDevice("switch-a", "site-a")
	device.UID = "device-a-uid"
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device).Build()
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()}
	for _, test := range []struct {
		name     string
		identity string
		reader   client.Reader
		want     string
	}{
		{name: "missing declaration", reader: apiClient, want: "required"},
		{name: "invalid declaration", identity: "serial value", reader: apiClient, want: "contain only"},
		{name: "missing uncached reader", identity: "serial-a", want: "uncached API reader"},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := device.DeepCopy()
			current.Spec.PhysicalIdentity = test.identity
			r := &CiscoDeviceReconciler{Client: apiClient, APIReader: test.reader, Scheme: scheme}
			if _, err := r.validateManagedPhysicalIdentity(context.Background(), current, policy); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateManagedPhysicalIdentity() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateManagedPhysicalIdentityRejectsPrebindingForgeryAndDrift(t *testing.T) {
	device := newDevice("switch-a", "site-a")
	device.UID = "device-a-uid"
	device.Spec.PhysicalIdentity = "FOC2416U0MV"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "forged-node-uid",
		PhysicalIdentity: "attacker-selected-identity",
	}
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&ciskov1.CiscoDevice{}, ciscoDevicePhysicalIdentityIndex, physicalIdentityIndexValues).
		WithObjects(device).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()}

	if _, err := r.validateManagedPhysicalIdentity(context.Background(), device, policy); err == nil ||
		!strings.Contains(err.Error(), "does not match canonical") {
		t.Fatalf("forged status binding error = %v", err)
	}
	device.Status.NodeIdentity.PhysicalIdentity = "foc2416u0mv"
	if got, err := r.validateManagedPhysicalIdentity(context.Background(), device, policy); err != nil || got != "foc2416u0mv" {
		t.Fatalf("canonical persisted binding = %q, %v", got, err)
	}
	device.Spec.PhysicalIdentity = "replacement-identity"
	if _, err := r.validateManagedPhysicalIdentity(context.Background(), device, policy); err == nil ||
		!strings.Contains(err.Error(), "does not match canonical") {
		t.Fatalf("in-memory immutable-spec drift error = %v", err)
	}
}

func TestValidateManagedPhysicalIdentityScansFreshOnlyAtEnrollment(t *testing.T) {
	device := newDevice("switch-a", "site-a")
	device.UID = "device-a-uid"
	device.Spec.PhysicalIdentity = "serial-a"
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&ciskov1.CiscoDevice{}, ciscoDevicePhysicalIdentityIndex, physicalIdentityIndexValues).
		WithObjects(device).Build()
	reader := &countingCiscoDeviceListReader{Reader: apiClient}
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: reader, Scheme: scheme}
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()}

	if _, err := r.validateManagedPhysicalIdentity(context.Background(), device, policy); err != nil {
		t.Fatalf("enrollment validation failed: %v", err)
	}
	if got := reader.listCount(); got != 1 {
		t.Fatalf("enrollment uncached CiscoDevice lists = %d, want 1", got)
	}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid", PhysicalIdentity: "serial-a",
	}
	for i := 0; i < 3; i++ {
		if _, err := r.validateManagedPhysicalIdentity(context.Background(), device, policy); err != nil {
			t.Fatalf("steady-state validation %d failed: %v", i, err)
		}
	}
	if got := reader.listCount(); got != 1 {
		t.Fatalf("steady-state validation performed uncached full-list scans: total=%d, want 1", got)
	}
}

func TestValidateManagedPhysicalIdentityConcurrentDuplicateFailsClosed(t *testing.T) {
	first := newDevice("switch-a", "site-a")
	first.UID = "device-a-uid"
	first.Spec.PhysicalIdentity = "serial-a"
	second := newDevice("switch-b", "site-b")
	second.UID = "device-b-uid"
	second.Spec.PhysicalIdentity = "SERIAL-A"
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(first, second).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()}

	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, device := range []*ciskov1.CiscoDevice{first, second} {
		wg.Add(1)
		go func(device *ciskov1.CiscoDevice) {
			defer wg.Done()
			_, err := r.validateManagedPhysicalIdentity(context.Background(), device, policy)
			errors <- err
		}(device)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err == nil || !strings.Contains(err.Error(), "also declared") {
			t.Fatalf("concurrent duplicate admission error = %v", err)
		}
	}
}

func TestValidateManagedPhysicalIdentityPreservesNeverManagedStandaloneScope(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-managed", "site-a")
	device.UID = "managed-uid"
	device.Labels = map[string]string{"fleet": "managed"}
	device.Spec.PhysicalIdentity = "serial-a"
	standalone := newDevice("switch-standalone", "site-b")
	standalone.UID = "standalone-uid"
	standalone.Spec.PhysicalIdentity = "SERIAL-A"
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, standalone).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.SelectorFromSet(labels.Set{"fleet": "managed"})}

	if got, err := r.validateManagedPhysicalIdentity(ctx, device, policy); err != nil || got != "serial-a" {
		t.Fatalf("never-managed standalone peer changed opt-in semantics: identity=%q error=%v", got, err)
	}
	// Retained managed ownership remains in scope after selector exit; otherwise
	// an isolated legacy writer could target the same chassis as a managed worker.
	standalone.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{Phase: ciskov1.DeviceLegacyHandoffComplete}
	if err := apiClient.Update(ctx, standalone); err != nil {
		t.Fatal(err)
	}
	if _, err := r.validateManagedPhysicalIdentity(ctx, device, policy); err == nil ||
		!strings.Contains(err.Error(), "site-b/switch-standalone") {
		t.Fatalf("retained managed duplicate error = %v", err)
	}
}

func TestPhysicalIdentityPeerMapperUsesIndexOnlyForWakeups(t *testing.T) {
	device := newDevice("switch-a", "site-a")
	device.UID = "device-a-uid"
	device.Spec.PhysicalIdentity = "FOC2416U0MV"
	peer := newDevice("switch-b", "site-b")
	peer.UID = "device-b-uid"
	peer.Spec.PhysicalIdentity = "foc2416u0mv"
	unrelated := newDevice("switch-c", "site-c")
	unrelated.UID = "device-c-uid"
	unrelated.Spec.PhysicalIdentity = "different-serial"
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&ciskov1.CiscoDevice{}, ciscoDevicePhysicalIdentityIndex, physicalIdentityIndexValues).
		WithObjects(device, peer, unrelated).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}

	requests := r.mapPhysicalIdentityPeers(context.Background(), device)
	if len(requests) != 1 || requests[0].Namespace != peer.Namespace || requests[0].Name != peer.Name {
		t.Fatalf("physical identity peer requests = %#v, want only %s/%s", requests, peer.Namespace, peer.Name)
	}
	reverse := r.mapPhysicalIdentityPeers(context.Background(), peer)
	if len(reverse) != 1 || reverse[0].Namespace != device.Namespace || reverse[0].Name != device.Name {
		t.Fatalf("reverse physical identity peer requests = %#v, want only %s/%s", reverse, device.Namespace, device.Name)
	}
	invalid := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "invalid"}}
	if values := physicalIdentityIndexValues(invalid); len(values) != 0 {
		t.Fatalf("invalid identity indexed as %#v", values)
	}
}
