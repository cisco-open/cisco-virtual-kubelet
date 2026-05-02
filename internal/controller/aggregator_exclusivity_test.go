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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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
	if cond != nil && cond.Status == metav1.ConditionTrue {
		t.Fatalf("AggregatorOwned condition=%+v, want not True while stale Pods remain", cond)
	}
	owning := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwning)
	if owning == nil || owning.Status != metav1.ConditionTrue || owning.Reason != "HandoverInProgress" {
		t.Fatalf("AggregatorOwning condition=%+v, want True/HandoverInProgress", owning)
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
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}, &gotDevice); err != nil {
		t.Fatalf("get CiscoDevice after quiesce: %v", err)
	}
	cond = meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "AggregatorEnabled" {
		t.Fatalf("AggregatorOwned condition after quiesce=%+v, want True/AggregatorEnabled", cond)
	}
	owning = meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwning)
	if owning == nil || owning.Status != metav1.ConditionFalse || owning.Reason != "HandoverComplete" {
		t.Fatalf("AggregatorOwning condition after quiesce=%+v, want False/HandoverComplete", owning)
	}
}

func TestQuiescenceWaitsForDeploymentToBeGone(t *testing.T) {
	registerExclusivityStubFakeCD(t)

	dev := newDevice("deploy-still-present", "default")
	dev.UID = types.UID("44444444-4444-4444-4444-444444444444")
	dev.Spec.Driver = ciskov1.DeviceDriverFAKE

	controller := true
	deleteAt := metav1.NewTime(time.Date(2026, 5, 2, 13, 0, 0, 0, time.UTC))
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              dev.Name + deploymentSuffix,
			Namespace:         dev.Namespace,
			UID:               types.UID("44444444-4444-4444-4444-deploy000001"),
			DeletionTimestamp: &deleteAt,
			Finalizers:        []string{"example.com/hold"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "cisco.vk/v1alpha1",
				Kind:       "CiscoDevice",
				Name:       dev.Name,
				UID:        dev.UID,
				Controller: &controller,
			}},
		},
	}
	r := reconcilerFor(t, dev, deploy)
	r.AggregatorEnabled = true

	quiesced, pods, err := r.perDevicePodsQuiesced(context.Background(), dev)
	if err != nil {
		t.Fatalf("perDevicePodsQuiesced: %v", err)
	}
	if quiesced {
		t.Fatal("perDevicePodsQuiesced=true, want false while Deployment object is still present")
	}
	if len(pods) != 0 {
		t.Fatalf("pods=%v, want none; Deployment presence alone should block quiescence", pods)
	}

	result, err := r.Reconcile(context.Background(), reconcileRequest(dev.Namespace, dev.Name))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.RequeueAfter != aggregatorTopologyPollInterval {
		t.Fatalf("RequeueAfter=%v, want %v while Deployment remains", result.RequeueAfter, aggregatorTopologyPollInterval)
	}

	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}, &gotDevice); err != nil {
		t.Fatalf("get CiscoDevice: %v", err)
	}
	owned := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
	if owned != nil && owned.Status == metav1.ConditionTrue {
		t.Fatalf("AggregatorOwned=%+v, want not True while Deployment remains", owned)
	}
}

func TestQuiescenceCatchesLabelDriftedPods(t *testing.T) {
	registerExclusivityStubFakeCD(t)

	dev := newDevice("label-drift", "default")
	dev.Spec.Driver = ciskov1.DeviceDriverFAKE

	controller := true
	deletedDeployUID := types.UID("55555555-5555-5555-5555-deploy000001")
	rsUID := types.UID("55555555-5555-5555-5555-rs0000000001")
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dev.Name + deploymentSuffix + "-abc123",
			Namespace: dev.Namespace,
			UID:       rsUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(),
				Kind:       "Deployment",
				Name:       dev.Name + deploymentSuffix,
				UID:        deletedDeployUID,
				Controller: &controller,
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dev.Name + deploymentSuffix + "-stripped-labels",
			Namespace: dev.Namespace,
			UID:       types.UID("55555555-5555-5555-5555-pod000000001"),
			Labels:    map[string]string{"unrelated": "true"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(),
				Kind:       "ReplicaSet",
				Name:       rs.Name,
				UID:        rsUID,
				Controller: &controller,
			}},
		},
	}
	r := reconcilerFor(t, dev, rs, pod)
	r.AggregatorEnabled = true

	quiesced, pods, err := r.perDevicePodsQuiesced(context.Background(), dev)
	if err != nil {
		t.Fatalf("perDevicePodsQuiesced: %v", err)
	}
	if quiesced {
		t.Fatal("perDevicePodsQuiesced=true, want false while label-drifted Pod has stale owner ancestry")
	}
	if len(pods) != 1 || pods[0].Name != pod.Name {
		t.Fatalf("pods=%v, want stale label-drifted Pod %q", pods, pod.Name)
	}

	result, err := r.Reconcile(context.Background(), reconcileRequest(dev.Namespace, dev.Name))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.RequeueAfter != aggregatorTopologyPollInterval {
		t.Fatalf("RequeueAfter=%v, want %v while stale Pod remains", result.RequeueAfter, aggregatorTopologyPollInterval)
	}
	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}, &gotDevice); err != nil {
		t.Fatalf("get CiscoDevice: %v", err)
	}
	owned := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
	if owned != nil && owned.Status == metav1.ConditionTrue {
		t.Fatalf("AggregatorOwned=%+v, want not True while stale label-drifted Pod remains", owned)
	}
}

func TestQuiescenceTrueWhenAllGone(t *testing.T) {
	registerExclusivityStubFakeCD(t)

	dev := newDevice("all-gone", "default")
	dev.Spec.Driver = ciskov1.DeviceDriverFAKE
	r := reconcilerFor(t, dev)
	r.AggregatorEnabled = true

	quiesced, pods, err := r.perDevicePodsQuiesced(context.Background(), dev)
	if err != nil {
		t.Fatalf("perDevicePodsQuiesced: %v", err)
	}
	if !quiesced || len(pods) != 0 {
		t.Fatalf("perDevicePodsQuiesced=%v pods=%v, want true with no stale Pods", quiesced, pods)
	}

	result, err := r.Reconcile(context.Background(), reconcileRequest(dev.Namespace, dev.Name))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Fatalf("result=%+v, want no requeue after quiescence", result)
	}

	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}, &gotDevice); err != nil {
		t.Fatalf("get CiscoDevice: %v", err)
	}
	owned := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
	if owned == nil || owned.Status != metav1.ConditionTrue || owned.Reason != "AggregatorEnabled" {
		t.Fatalf("AggregatorOwned=%+v, want True/AggregatorEnabled", owned)
	}
	owning := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwning)
	if owning == nil || owning.Status != metav1.ConditionFalse || owning.Reason != "HandoverComplete" {
		t.Fatalf("AggregatorOwning=%+v, want False/HandoverComplete", owning)
	}
}

func TestAggregatorTopologyShiftStuckSurfacesAfterTimeout(t *testing.T) {
	registerExclusivityStubFakeCD(t)

	start := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: start}
	dev := newDevice("shift-stuck", "default")
	dev.UID = types.UID("22222222-2222-2222-2222-222222222222")
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
			Name:       dev.Name + deploymentSuffix + "-old",
			Namespace:  dev.Namespace,
			Labels:     perDeviceDeploymentLabels(dev.Name),
			Finalizers: []string{"example.com/hold"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	recorder := record.NewFakeRecorder(10)
	r := reconcilerFor(t, dev, deploy, pod)
	r.AggregatorEnabled = true
	r.Recorder = recorder
	r.clock = clk

	result, err := r.Reconcile(context.Background(), reconcileRequest("default", dev.Name))
	if err != nil {
		t.Fatalf("Reconcile start: %v", err)
	}
	if result.RequeueAfter != aggregatorTopologyPollInterval {
		t.Fatalf("RequeueAfter=%v, want %v while stale Pods remain", result.RequeueAfter, aggregatorTopologyPollInterval)
	}

	clk.Advance(aggregatorTopologyShiftTimeout + time.Second)
	result, err = r.Reconcile(context.Background(), reconcileRequest("default", dev.Name))
	if err != nil {
		t.Fatalf("Reconcile after timeout: %v", err)
	}
	if result.RequeueAfter != aggregatorTopologyPollInterval {
		t.Fatalf("RequeueAfter after timeout=%v, want %v", result.RequeueAfter, aggregatorTopologyPollInterval)
	}

	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}, &gotDevice); err != nil {
		t.Fatalf("get CiscoDevice: %v", err)
	}
	stuck := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorTopologyStuck)
	if stuck == nil || stuck.Status != metav1.ConditionTrue || stuck.Reason != "PodQuiesceTimeout" {
		t.Fatalf("AggregatorTopologyStuck=%+v, want True/PodQuiesceTimeout", stuck)
	}
	for _, want := range []string{pod.Name, "example.com/hold", string(corev1.PodRunning)} {
		if !strings.Contains(stuck.Message, want) {
			t.Fatalf("AggregatorTopologyStuck message=%q, want it to contain %q", stuck.Message, want)
		}
	}
	owned := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
	if owned != nil && owned.Status == metav1.ConditionTrue {
		t.Fatalf("AggregatorOwned=%+v, want not True while topology shift is stuck", owned)
	}
	select {
	case event := <-recorder.Events:
		for _, want := range []string{"AggregatorTopologyShiftStuck", pod.Name, "example.com/hold", string(corev1.PodRunning)} {
			if !strings.Contains(event, want) {
				t.Fatalf("event=%q, want it to contain %q", event, want)
			}
		}
	default:
		t.Fatal("expected AggregatorTopologyShiftStuck warning event")
	}
}

func TestAggregatorTopologyShiftStuckClearsOnRecovery(t *testing.T) {
	registerExclusivityStubFakeCD(t)

	start := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: start}
	dev := newDevice("shift-recover", "default")
	dev.UID = types.UID("33333333-3333-3333-3333-333333333333")
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
			Name:       dev.Name + deploymentSuffix + "-old",
			Namespace:  dev.Namespace,
			Labels:     perDeviceDeploymentLabels(dev.Name),
			Finalizers: []string{"example.com/hold"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r := reconcilerFor(t, dev, deploy, pod)
	r.AggregatorEnabled = true
	r.clock = clk

	if _, err := r.Reconcile(context.Background(), reconcileRequest("default", dev.Name)); err != nil {
		t.Fatalf("Reconcile start: %v", err)
	}
	clk.Advance(aggregatorTopologyShiftTimeout + time.Second)
	if _, err := r.Reconcile(context.Background(), reconcileRequest("default", dev.Name)); err != nil {
		t.Fatalf("Reconcile stuck: %v", err)
	}

	var held corev1.Pod
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}, &held); err != nil {
		t.Fatalf("get held Pod: %v", err)
	}
	held.Finalizers = nil
	if err := r.Update(context.Background(), &held); err != nil {
		t.Fatalf("remove Pod finalizer: %v", err)
	}
	if err := r.Delete(context.Background(), &held); err != nil {
		t.Fatalf("delete recovered Pod: %v", err)
	}

	result, err := r.Reconcile(context.Background(), reconcileRequest("default", dev.Name))
	if err != nil {
		t.Fatalf("Reconcile after recovery: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Fatalf("result after recovery=%+v, want no requeue", result)
	}

	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}, &gotDevice); err != nil {
		t.Fatalf("get CiscoDevice after recovery: %v", err)
	}
	stuck := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorTopologyStuck)
	if stuck == nil || stuck.Status != metav1.ConditionFalse || stuck.Reason != "Resolved" {
		t.Fatalf("AggregatorTopologyStuck after recovery=%+v, want False/Resolved", stuck)
	}
	owned := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
	if owned == nil || owned.Status != metav1.ConditionTrue || owned.Reason != "AggregatorEnabled" {
		t.Fatalf("AggregatorOwned after recovery=%+v, want True/AggregatorEnabled", owned)
	}
	owning := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwning)
	if owning == nil || owning.Status != metav1.ConditionFalse || owning.Reason != "HandoverComplete" {
		t.Fatalf("AggregatorOwning after recovery=%+v, want False/HandoverComplete", owning)
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
