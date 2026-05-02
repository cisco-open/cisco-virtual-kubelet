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

// Wave 1C regression tests for external-review Finding #3: when the
// controller manager is in aggregator mode, it must NOT create a
// per-device VK Deployment for devices whose driver is registered as
// a configdriver — that produces a duplicate writer (the aggregator
// AND the in-pod ConfigReconciler both target the same lease scope).
//
// Devices whose driver is registered for apphosting only (no
// configdriver) still get a Deployment, but with the env that
// disables the in-pod ConfigReconciler.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/aggregator"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// stubExclusivityCDRegistration registers a configdriver factory under
// the FAKE driver kind so the controller's drivers.ConfigDriverRegistered
// check returns true. sync.Once-guarded so multiple tests in the same
// `go test` invocation don't trigger the registry's duplicate-panic.
var stubExclusivityCDRegistrationOnce sync.Once
var stubExclusivityCDFactoryCalls atomic.Int32

func registerExclusivityStubFakeCD(t *testing.T) {
	t.Helper()
	stubExclusivityCDRegistrationOnce.Do(func() {
		drivers.RegisterConfigDriver(
			ciskov1.DeviceDriverFAKE,
			func(_ context.Context, _ *ciskov1.DeviceSpec, _ string, _ drivers.ConfigDriverOptions) (*drivers.ConfigDriverContext, error) {
				stubExclusivityCDFactoryCalls.Add(1)
				return &drivers.ConfigDriverContext{Transport: nil}, nil
			},
		)
	})
	_ = transport.ErrUnsupported // keep transport import live for clarity
}

type recordingDeleteClient struct {
	client.Client
	propagation *metav1.DeletionPropagation
}

func (c *recordingDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	options := (&client.DeleteOptions{}).ApplyOptions(opts)
	c.propagation = options.PropagationPolicy
	return c.Client.Delete(ctx, obj, opts...)
}

func TestReconcile_AggregatorMode_SkipsDeploymentForConfigDriverRegisteredDevice(t *testing.T) {
	registerExclusivityStubFakeCD(t)

	dev := newDevice("aggro-fake", "default")
	dev.Spec.Driver = ciskov1.DeviceDriverFAKE // registered config driver via the stub above
	r := reconcilerFor(t, dev)
	r.AggregatorEnabled = true

	if _, err := r.Reconcile(context.Background(), reconcileRequest("default", "aggro-fake")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var d appsv1.Deployment
	err := r.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "aggro-fake" + deploymentSuffix},
		&d)
	if !errors.IsNotFound(err) {
		t.Fatalf("expected NotFound for the per-device Deployment under aggregator mode, got err=%v deploy=%+v", err, d)
	}
}

func TestAggregatorTopologyShiftWaitsForPodsToQuiesce(t *testing.T) {
	registerExclusivityStubFakeCD(t)

	dev := newDevice("shift-fake", "default")
	dev.UID = types.UID("11111111-1111-1111-1111-111111111111")
	dev.Spec.Driver = ciskov1.DeviceDriverFAKE

	controller := true
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dev.Name + deploymentSuffix,
			Namespace: dev.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "cisco.vk/v1alpha1",
				Kind:       "CiscoDevice",
				Name:       dev.Name,
				UID:        dev.UID,
				Controller: &controller,
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dev.Name + deploymentSuffix + "-old",
			Namespace: dev.Namespace,
			Labels:    perDeviceDeploymentLabels(dev.Name),
		},
	}
	r := reconcilerFor(t, dev, deploy, pod)
	recorder := &recordingDeleteClient{Client: r.Client}
	r.Client = recorder
	r.AggregatorEnabled = true

	result, err := r.Reconcile(context.Background(), reconcileRequest("default", dev.Name))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.RequeueAfter != aggregatorTopologyPollInterval {
		t.Fatalf("RequeueAfter=%v, want %v while stale Pods remain", result.RequeueAfter, aggregatorTopologyPollInterval)
	}
	if recorder.propagation == nil || *recorder.propagation != metav1.DeletePropagationForeground {
		t.Fatalf("Deployment delete propagation=%v, want Foreground", recorder.propagation)
	}
	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}, &gotDevice); err != nil {
		t.Fatalf("get CiscoDevice: %v", err)
	}
	cond := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "AggregatorEnabled" {
		t.Fatalf("AggregatorOwned condition=%+v, want True/AggregatorEnabled", cond)
	}
	var gone appsv1.Deployment
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: deploy.Namespace, Name: deploy.Name}, &gone); !errors.IsNotFound(err) {
		t.Fatalf("stale Deployment should be deleted, got err=%v deploy=%+v", err, gone)
	}

	if err := r.Delete(context.Background(), pod); err != nil {
		t.Fatalf("delete stale Pod: %v", err)
	}
	result, err = r.Reconcile(context.Background(), reconcileRequest("default", dev.Name))
	if err != nil {
		t.Fatalf("Reconcile after Pod quiesce: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Fatalf("result after Pod quiesce=%+v, want no requeue", result)
	}
}

func TestAggregatorWorkerRefusesUnownedDevice(t *testing.T) {
	registerExclusivityStubFakeCD(t)
	stubExclusivityCDFactoryCalls.Store(0)

	dev := newDevice("unowned-fake", "default")
	dev.Spec.Driver = ciskov1.DeviceDriverFAKE
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dev).
		Build()
	r := &aggregator.AggregatedReconciler{
		Client: c,
		Scheme: scheme,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if calls := stubExclusivityCDFactoryCalls.Load(); calls != 0 {
		t.Fatalf("aggregator started device-side work without AggregatorOwned condition; factory calls=%d", calls)
	}
}

func TestReconcile_AggregatorMode_SetsDisableEnvOnApphostingOnlyDevice(t *testing.T) {
	// XR is in the placeholder set in this branch — registered for
	// neither apphosting nor configdriver. Because the controller
	// pre-flight skips Deployment creation only when the configdriver
	// IS registered, an unregistered driver still gets a Deployment;
	// in aggregator mode that Deployment must carry the env that
	// disables the in-pod ConfigReconciler.
	dev := newDevice("aggro-apphosting", "default")
	dev.Spec.Driver = ciskov1.DeviceDriverXR // not configdriver-registered
	r := reconcilerFor(t, dev)
	r.AggregatorEnabled = true

	if _, err := r.Reconcile(context.Background(), reconcileRequest("default", "aggro-apphosting")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var d appsv1.Deployment
	if err := r.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "aggro-apphosting" + deploymentSuffix},
		&d); err != nil {
		t.Fatalf("expected Deployment to exist for apphosting-only device under aggregator mode: %v", err)
	}

	c := d.Spec.Template.Spec.Containers[0]
	found := false
	for _, env := range c.Env {
		if env.Name == "DISABLE_IN_POD_CONFIG_RECONCILER" && env.Value == "true" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected DISABLE_IN_POD_CONFIG_RECONCILER=true env on apphosting-only device under aggregator mode; env=%v", c.Env)
	}
}

func TestReconcile_NonAggregatorMode_CreatesDeploymentEvenForConfigDriverDevice(t *testing.T) {
	// Sanity backstop: with AggregatorEnabled=false (the default),
	// every device gets a Deployment regardless of configdriver
	// registration. The historical per-pod-per-device topology must
	// stay unchanged.
	registerExclusivityStubFakeCD(t)

	dev := newDevice("solo-fake", "default")
	dev.Spec.Driver = ciskov1.DeviceDriverFAKE
	r := reconcilerFor(t, dev)
	r.AggregatorEnabled = false

	if _, err := r.Reconcile(context.Background(), reconcileRequest("default", "solo-fake")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var d appsv1.Deployment
	if err := r.Get(context.Background(),
		types.NamespacedName{Namespace: "default", Name: "solo-fake" + deploymentSuffix},
		&d); err != nil {
		t.Fatalf("expected Deployment to exist under per-pod topology (default): %v", err)
	}
	for _, env := range d.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "DISABLE_IN_POD_CONFIG_RECONCILER" {
			t.Errorf("DISABLE_IN_POD_CONFIG_RECONCILER must NOT be set when AggregatorEnabled=false; saw %q", env.Value)
		}
	}
}
