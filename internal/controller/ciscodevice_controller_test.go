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
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// newTestScheme builds a runtime.Scheme with all types needed by the reconciler.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(ciskov1.AddToScheme(s))
	return s
}

// newDevice constructs a minimal CiscoDevice for use in tests.
func newDevice(name, namespace string) *ciskov1.CiscoDevice {
	return &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverXE,
			Address:  "192.0.2.1",
			Username: "admin",
			Password: "secret",
		},
	}
}

// reconcilerFor builds a CiscoDeviceReconciler backed by a fake client that
// already contains the provided objects.
func reconcilerFor(t *testing.T, objs ...runtime.Object) *CiscoDeviceReconciler {
	t.Helper()
	s := newTestScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithRuntimeObjects(objs...).
		Build()
	return &CiscoDeviceReconciler{
		Client:         fakeClient,
		Scheme:         s,
		Image:          "cisco-vk:test",
		ServiceAccount: "test-sa",
	}
}

// reconcileRequest builds a ctrl.Request from a namespace and name.
func reconcileRequest(namespace, name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconcile happy path
// ─────────────────────────────────────────────────────────────────────────────

func TestReconcile_CreatesConfigMap(t *testing.T) {
	device := newDevice("router-a", "default")
	r := reconcilerFor(t, device)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, reconcileRequest("default", "router-a"))
	if err != nil {
		t.Fatalf("Reconcile returned unexpected error: %v", err)
	}

	var cm corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-a" + configMapSuffix}, &cm); err != nil {
		t.Fatalf("ConfigMap not found after reconcile: %v", err)
	}

	data, ok := cm.Data[configFileName]
	if !ok {
		t.Fatalf("ConfigMap missing key %q", configFileName)
	}
	if !strings.Contains(data, "192.0.2.1") {
		t.Errorf("ConfigMap data does not contain device address; got:\n%s", data)
	}
}

func TestReconcile_CreatesDeployment(t *testing.T) {
	device := newDevice("router-b", "default")
	r := reconcilerFor(t, device)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, reconcileRequest("default", "router-b"))
	if err != nil {
		t.Fatalf("Reconcile returned unexpected error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-b" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found after reconcile: %v", err)
	}

	if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 1 {
		t.Errorf("expected 1 replica, got %v", deploy.Spec.Replicas)
	}
	if len(deploy.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(deploy.Spec.Template.Spec.Containers))
	}
	if got := deploy.Spec.Template.Spec.Containers[0].Image; got != "cisco-vk:test" {
		t.Errorf("expected image cisco-vk:test, got %q", got)
	}
	if got := deploy.Spec.Template.Spec.ServiceAccountName; got != "test-sa" {
		t.Errorf("expected service account test-sa, got %q", got)
	}
	args := deploy.Spec.Template.Spec.Containers[0].Args
	if len(args) == 0 || args[0] != "run" {
		t.Errorf("expected first arg to be 'run', got %v", args)
	}
	found := false
	for i, a := range args {
		if a == "--nodename" && i+1 < len(args) && args[i+1] == "router-b" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --nodename router-b in container args, got %v", args)
	}
	if len(deploy.Spec.Template.Spec.Containers[0].VolumeMounts) != 2 {
		t.Errorf("expected 2 volume mounts (device-config, tls-gen), got %d", len(deploy.Spec.Template.Spec.Containers[0].VolumeMounts))
	}
}

func TestReconcile_DeploymentHasConfigHashAnnotation(t *testing.T) {
	device := newDevice("router-c", "default")
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-c")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-c" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	if _, ok := deploy.Spec.Template.Annotations["cisco.vk/config-hash"]; !ok {
		t.Error("expected cisco.vk/config-hash annotation on pod template, not found")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconcile - not found (device deleted)
// ─────────────────────────────────────────────────────────────────────────────

func TestReconcile_NotFound_ReturnsNoError(t *testing.T) {
	r := reconcilerFor(t)
	ctx := context.Background()

	result, err := r.Reconcile(ctx, reconcileRequest("default", "does-not-exist"))
	if err != nil {
		t.Fatalf("expected no error for missing device, got: %v", err)
	}
	if result.Requeue {
		t.Errorf("expected no requeue for missing device")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconcile - idempotency
// ─────────────────────────────────────────────────────────────────────────────

func TestReconcile_Idempotent(t *testing.T) {
	device := newDevice("router-d", "default")
	r := reconcilerFor(t, device)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-d")); err != nil {
			t.Fatalf("Reconcile %d returned error: %v", i+1, err)
		}
	}

	var cmList corev1.ConfigMapList
	if err := r.List(ctx, &cmList); err != nil {
		t.Fatalf("listing ConfigMaps: %v", err)
	}
	if len(cmList.Items) != 1 {
		t.Errorf("expected 1 ConfigMap after idempotent reconcile, got %d", len(cmList.Items))
	}

	var deployList appsv1.DeploymentList
	if err := r.List(ctx, &deployList); err != nil {
		t.Fatalf("listing Deployments: %v", err)
	}
	if len(deployList.Items) != 1 {
		t.Errorf("expected 1 Deployment after idempotent reconcile, got %d", len(deployList.Items))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconcile - config hash changes when spec changes
// ─────────────────────────────────────────────────────────────────────────────

func TestReconcile_ConfigHashChangesOnSpecUpdate(t *testing.T) {
	device := newDevice("router-e", "default")
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-e")); err != nil {
		t.Fatalf("first Reconcile error: %v", err)
	}
	var deployBefore appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-e" + deploymentSuffix}, &deployBefore); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	hashBefore := deployBefore.Spec.Template.Annotations["cisco.vk/config-hash"]

	var updated ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-e"}, &updated); err != nil {
		t.Fatalf("fetching device for update: %v", err)
	}
	updated.Spec.Address = "192.0.2.99"
	if err := r.Update(ctx, &updated); err != nil {
		t.Fatalf("updating device: %v", err)
	}

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-e")); err != nil {
		t.Fatalf("second Reconcile error: %v", err)
	}
	var deployAfter appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-e" + deploymentSuffix}, &deployAfter); err != nil {
		t.Fatalf("Deployment not found after update: %v", err)
	}
	hashAfter := deployAfter.Spec.Template.Annotations["cisco.vk/config-hash"]

	if hashBefore == hashAfter {
		t.Errorf("expected config-hash to change after address update, both are %q", hashBefore)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconcile - default image fallback
// ─────────────────────────────────────────────────────────────────────────────

func TestReconcile_DefaultImageUsedWhenEmpty(t *testing.T) {
	device := newDevice("router-f", "default")
	s := newTestScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithRuntimeObjects(device).
		Build()
	r := &CiscoDeviceReconciler{
		Client:         fakeClient,
		Scheme:         s,
		Image:          "",
		ServiceAccount: DefaultServiceAccount,
	}

	if _, err := r.Reconcile(context.Background(), reconcileRequest("default", "router-f")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	var deploy appsv1.Deployment
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "router-f" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	if got := deploy.Spec.Template.Spec.Containers[0].Image; got != DefaultImage {
		t.Errorf("expected default image %q, got %q", DefaultImage, got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconcile - owner references
// ─────────────────────────────────────────────────────────────────────────────

func TestReconcile_OwnerReferenceSet(t *testing.T) {
	device := newDevice("router-g", "default")
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-g")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var cm corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-g" + configMapSuffix}, &cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}
	if len(cm.OwnerReferences) == 0 {
		t.Error("expected ConfigMap to have an owner reference, got none")
	}
	if cm.OwnerReferences[0].Name != "router-g" {
		t.Errorf("expected owner reference to router-g, got %q", cm.OwnerReferences[0].Name)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-g" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	if len(deploy.OwnerReferences) == 0 {
		t.Error("expected Deployment to have an owner reference, got none")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// vkContainerArgs helper
// ─────────────────────────────────────────────────────────────────────────────

func TestVkContainerArgs_NoLogLevel(t *testing.T) {
	args := vkContainerArgs("router-x", "")
	for _, a := range args {
		if a == "--log-level" {
			t.Fatal("--log-level should not be present when logLevel is empty")
		}
	}
	if args[0] != "run" {
		t.Errorf("expected first arg 'run', got %q", args[0])
	}
}

func TestVkContainerArgs_WithLogLevel(t *testing.T) {
	args := vkContainerArgs("router-x", "debug")
	found := false
	for i, a := range args {
		if a == "--log-level" && i+1 < len(args) && args[i+1] == "debug" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --log-level debug in args, got %v", args)
	}
}

func TestReconcile_LogLevelPassedToDeployment(t *testing.T) {
	device := newDevice("router-ll", "default")
	device.Spec.LogLevel = "debug"
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-ll")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-ll" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	args := deploy.Spec.Template.Spec.Containers[0].Args
	found := false
	for i, a := range args {
		if a == "--log-level" && i+1 < len(args) && args[i+1] == "debug" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --log-level debug in container args, got %v", args)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure helper: renderDeviceConfig
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderDeviceConfig_ContainsExpectedFields(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Driver:   ciskov1.DeviceDriverXE,
		Address:  "10.0.0.1",
		Username: "admin",
		Password: "pass",
		Port:     443,
	}
	out, err := renderDeviceConfig(spec)
	if err != nil {
		t.Fatalf("renderDeviceConfig error: %v", err)
	}
	for _, want := range []string{"driver", "XE", "address", "10.0.0.1", "username", "admin"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q; got:\n%s", want, out)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "device:") {
		t.Errorf("expected output wrapped under device:, got:\n%s", out)
	}
}

func TestRenderDeviceConfig_ZeroValueSpec(t *testing.T) {
	_, err := renderDeviceConfig(&ciskov1.DeviceSpec{})
	if err != nil {
		t.Errorf("unexpected error for zero DeviceSpec: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure helper: shortHash
// ─────────────────────────────────────────────────────────────────────────────

func TestShortHash_Deterministic(t *testing.T) {
	if h1, h2 := shortHash("hello world"), shortHash("hello world"); h1 != h2 {
		t.Errorf("shortHash not deterministic: %q != %q", h1, h2)
	}
}

func TestShortHash_DifferentInputs(t *testing.T) {
	if h1, h2 := shortHash("a"), shortHash("b"); h1 == h2 {
		t.Errorf("expected different hashes for different inputs, both got %q", h1)
	}
}

func TestShortHash_Length(t *testing.T) {
	if got := len(shortHash("anything")); got != 8 {
		t.Errorf("expected hash length 8, got %d", got)
	}
}

func TestShortHash_EmptyString(t *testing.T) {
	if got := len(shortHash("")); got != 8 {
		t.Errorf("expected 8-char hash for empty string, got length %d", got)
	}
}
