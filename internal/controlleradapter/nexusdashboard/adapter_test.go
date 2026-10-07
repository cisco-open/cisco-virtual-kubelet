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

package nexusdashboard

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

func testController(endpoint string) *ciskov1.NetworkController {
	return &ciskov1.NetworkController{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nd1", Generation: 3},
		Spec: ciskov1.NetworkControllerSpec{
			Type:                ciskov1.NetworkControllerType(TypeName),
			Endpoint:            endpoint,
			CredentialSecretRef: ciskov1.NetworkControllerSecretReference{Name: "nd-creds"},
		},
	}
}

func TestRegisteredDescriptor(t *testing.T) {
	// init() would have panicked on an invalid descriptor.
	got, ok := controlleradapter.DescriptorFor(TypeName)
	if !ok {
		t.Fatal("nexus-dashboard not registered")
	}
	if got.WorkerClusterRole != controlleradapter.DefaultWorkerClusterRole || !reflect.DeepEqual(got.Capabilities, []string{"health", "inventory"}) {
		t.Fatalf("unexpected descriptor %+v", got)
	}
}

func TestFactoryDoesNotDial(t *testing.T) {
	f := newFakeND(t, false)
	opts := controlleradapter.Options{
		Controller:       testController(f.srv.URL),
		CredentialPath:   t.TempDir(),
		MaterialRotation: controlleradapter.MaterialRotationPolicy{Changes: make(chan struct{}), MaxSessionLifetime: time.Minute},
	}
	if _, err := newAdapter(opts); err != nil {
		t.Fatal(err)
	}
	if f.logins.Load() != 0 || f.probes.Load() != 0 {
		t.Fatal("factory must not touch the network")
	}
}

func newTestAdapter(t *testing.T, probe func(context.Context) error) (*adapter, *ciskov1.NetworkController, func() *ciskov1.NetworkController) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	nc := testController("https://nd.example")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nc).WithStatusSubresource(nc).Build()
	a := &adapter{
		key: types.NamespacedName{Namespace: "ns", Name: "nd1"}, statusWriter: cl, probe: probe,
		invalidate: func() {}, now: time.Now, interval: time.Hour,
	}
	get := func() *ciskov1.NetworkController {
		var out ciskov1.NetworkController
		if err := cl.Get(context.Background(), a.key, &out); err != nil {
			t.Fatal(err)
		}
		return &out
	}
	return a, nc, get
}

func TestPublishReadyThenAuthFailure(t *testing.T) {
	var fail atomic.Bool
	a, _, get := newTestAdapter(t, func(context.Context) error {
		if fail.Load() {
			return errAuthentication
		}
		return nil
	})
	a.check(context.Background())
	nc := get()
	for _, typ := range []string{"Authenticated", "APICompatible", "Ready"} {
		if !meta.IsStatusConditionTrue(nc.Status.Conditions, typ) {
			t.Fatalf("%s not true: %+v", typ, nc.Status.Conditions)
		}
	}
	if nc.Status.Phase != ciskov1.NetworkControllerPhaseReady || nc.Status.LastSuccessfulConnectionTime == nil || nc.Status.ObservedGeneration != 3 {
		t.Fatalf("unexpected status %+v", nc.Status)
	}
	if len(nc.Status.Capabilities) != 1 || !nc.Status.Capabilities[0].Supported {
		t.Fatalf("capabilities %+v", nc.Status.Capabilities)
	}

	fail.Store(true)
	a.check(context.Background())
	nc = get()
	auth := meta.FindStatusCondition(nc.Status.Conditions, "Authenticated")
	if auth == nil || auth.Status != metav1.ConditionFalse || auth.Reason != "AuthenticationFailed" {
		t.Fatalf("auth condition %+v", auth)
	}
	if meta.IsStatusConditionTrue(nc.Status.Conditions, "Ready") || nc.Status.Phase != ciskov1.NetworkControllerPhaseError {
		t.Fatalf("must not be Ready: %+v", nc.Status)
	}
	if nc.Status.LastSuccessfulConnectionTime == nil {
		t.Fatal("last-good time should be preserved")
	}
}

func TestPublishDoesNotTouchSpecOrMetadata(t *testing.T) {
	a, orig, get := newTestAdapter(t, func(context.Context) error { return errors.New("dial tcp: connection refused") })
	a.check(context.Background())
	nc := get()
	if !reflect.DeepEqual(nc.Spec, orig.Spec) || len(nc.Finalizers) != 0 {
		t.Fatal("spec/finalizers changed")
	}
	if nc.Status.Phase != ciskov1.NetworkControllerPhaseDegraded {
		t.Fatalf("phase = %s", nc.Status.Phase)
	}
}

func TestRotationInvalidatesAndRechecks(t *testing.T) {
	var probes, invalidations atomic.Int32
	a, _, _ := newTestAdapter(t, func(context.Context) error { probes.Add(1); return nil })
	rot := make(chan struct{}, 1)
	a.rotation = rot
	a.invalidate = func() { invalidations.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (&healthRunnable{adapter: a}).Start(ctx) }()

	deadline := time.After(5 * time.Second)
	for probes.Load() < 1 {
		select {
		case <-deadline:
			t.Fatal("no initial probe")
		case <-time.After(5 * time.Millisecond):
		}
	}
	rot <- struct{}{}
	for probes.Load() < 2 || invalidations.Load() < 1 {
		select {
		case <-deadline:
			t.Fatalf("rotation not handled: probes=%d inval=%d", probes.Load(), invalidations.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
