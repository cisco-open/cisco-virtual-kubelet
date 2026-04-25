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

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// newTestScheme builds a runtime.Scheme with all types needed by the reconciler.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(ciskov1.AddToScheme(s))
	utilruntime.Must(configv1alpha1.AddToScheme(s))
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

func TestRenderDeviceConfig_StripsPassword(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Driver:   ciskov1.DeviceDriverXE,
		Address:  "10.0.0.1",
		Username: "admin",
		Password: "supersecret",
		CredentialSecretRef: &corev1.LocalObjectReference{
			Name: "my-creds",
		},
	}
	out, err := renderDeviceConfig(spec)
	if err != nil {
		t.Fatalf("renderDeviceConfig error: %v", err)
	}
	if strings.Contains(out, "supersecret") {
		t.Errorf("password should be stripped from ConfigMap output; got:\n%s", out)
	}
	if strings.Contains(out, "my-creds") {
		t.Errorf("credentialSecretRef should be stripped from ConfigMap output; got:\n%s", out)
	}
	// Original spec must not be mutated.
	if spec.Password != "supersecret" {
		t.Errorf("renderDeviceConfig mutated the original spec password")
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

// ─────────────────────────────────────────────────────────────────────────────
// Reconcile - credential injection
// ─────────────────────────────────────────────────────────────────────────────

func TestReconcile_SecretRefInjectsEnvFromSecret(t *testing.T) {
	device := newDevice("router-sec", "default")
	device.Spec.Password = ""
	device.Spec.CredentialSecretRef = &corev1.LocalObjectReference{Name: "device-creds"}
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-sec")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-sec" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	env := deploy.Spec.Template.Spec.Containers[0].Env
	pw := envByName(env, "VK_DEVICE_PASSWORD")
	if pw == nil {
		t.Fatalf("VK_DEVICE_PASSWORD env var missing, got %v", env)
	}
	if pw.ValueFrom == nil || pw.ValueFrom.SecretKeyRef == nil {
		t.Fatal("expected VK_DEVICE_PASSWORD to use valueFrom.secretKeyRef")
	}
	if pw.ValueFrom.SecretKeyRef.Name != "device-creds" {
		t.Errorf("expected secretKeyRef name 'device-creds', got %q", pw.ValueFrom.SecretKeyRef.Name)
	}
	if pw.ValueFrom.SecretKeyRef.Key != "password" {
		t.Errorf("expected secretKeyRef key 'password', got %q", pw.ValueFrom.SecretKeyRef.Key)
	}
	if envByName(env, "POD_NAMESPACE") == nil {
		t.Error("POD_NAMESPACE env var missing")
	}
}

func TestReconcile_DirectPasswordInjectsEnvValue(t *testing.T) {
	device := newDevice("router-pw", "default")
	device.Spec.Password = "directpass"
	device.Spec.CredentialSecretRef = nil
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-pw")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-pw" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	env := deploy.Spec.Template.Spec.Containers[0].Env
	pw := envByName(env, "VK_DEVICE_PASSWORD")
	if pw == nil {
		t.Fatalf("VK_DEVICE_PASSWORD env var missing, got %v", env)
	}
	if pw.Value != "directpass" {
		t.Errorf("expected direct password value 'directpass', got %q", pw.Value)
	}
	if envByName(env, "POD_NAMESPACE") == nil {
		t.Error("POD_NAMESPACE env var missing")
	}
}

func TestReconcile_PropagatesConfigLeaseNamespace(t *testing.T) {
	// When CONFIG_LEASE_NAMESPACE is set on the controller, every
	// cisco-vk pod the controller spawns must inherit it. That is
	// what gives operators a single dial for cross-namespace lease
	// arbitration (Phase 4 / §10.10). When the env is unset the
	// pod spec must NOT carry the var — the cisco-vk side falls
	// back to POD_NAMESPACE in that case.
	t.Setenv("CONFIG_LEASE_NAMESPACE", "cvk-leases")

	device := newDevice("router-lease", "default")
	device.Spec.Password = "x"
	device.Spec.CredentialSecretRef = nil
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-lease")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-lease" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	env := deploy.Spec.Template.Spec.Containers[0].Env
	if got := envByName(env, "CONFIG_LEASE_NAMESPACE"); got == nil || got.Value != "cvk-leases" {
		t.Errorf("CONFIG_LEASE_NAMESPACE env missing or wrong: %v", got)
	}
}

func TestReconcile_OmitsConfigLeaseNamespaceWhenUnset(t *testing.T) {
	// Default behaviour — no env on controller ⇒ no env on pod. We
	// must not invent a value here; the cisco-vk side relies on
	// the absence of CONFIG_LEASE_NAMESPACE to fall back to
	// POD_NAMESPACE.
	t.Setenv("CONFIG_LEASE_NAMESPACE", "")

	device := newDevice("router-no-lease", "default")
	device.Spec.Password = "x"
	device.Spec.CredentialSecretRef = nil
	r := reconcilerFor(t, device)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-no-lease")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-no-lease" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	env := deploy.Spec.Template.Spec.Containers[0].Env
	if got := envByName(env, "CONFIG_LEASE_NAMESPACE"); got != nil {
		t.Errorf("CONFIG_LEASE_NAMESPACE leaked into pod spec when controller env was empty: %#v", got)
	}
}

func TestReconcile_NoPasswordNoSecretRef_NoEnvVars(t *testing.T) {
	device := newDevice("router-nopass", "default")
	device.Spec.Password = ""
	device.Spec.CredentialSecretRef = nil
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-nopass")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-nopass" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	env := deploy.Spec.Template.Spec.Containers[0].Env
	if envByName(env, "VK_DEVICE_PASSWORD") != nil {
		t.Errorf("expected no VK_DEVICE_PASSWORD env var, got %v", env)
	}
	if envByName(env, "POD_NAMESPACE") == nil {
		t.Error("POD_NAMESPACE env var missing")
	}
}

// envByName returns the first EnvVar with the supplied name, or nil
// when absent. Kept here rather than in production code because it's
// only an assertion helper.
func envByName(env []corev1.EnvVar, name string) *corev1.EnvVar {
	for i := range env {
		if env[i].Name == name {
			return &env[i]
		}
	}
	return nil
}

func TestReconcile_PasswordStrippedFromConfigMap(t *testing.T) {
	device := newDevice("router-strip", "default")
	device.Spec.Password = "shouldnotappear"
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-strip")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var cm corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-strip" + configMapSuffix}, &cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	data := cm.Data[configFileName]
	if strings.Contains(data, "shouldnotappear") {
		t.Errorf("password should not appear in ConfigMap data; got:\n%s", data)
	}
}

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

func TestReconcile_ConfigPrereqsCreatesOwnedIOSXEConfig(t *testing.T) {
	device := newDevice("router-p", "default")
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		Configuration: runtime.RawExtension{Raw: []byte(
			`{"interface_virtual_port_group":{"interfaces":[{"id":0,"ipv4_address":"192.168.10.1","ipv4_address_mask":"255.255.255.0"}]}}`,
		)},
	}
	r := reconcilerFor(t, device)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-p")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var owned configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-p-prereqs"}, &owned); err != nil {
		t.Fatalf("expected owned IOSXEConfig: %v", err)
	}
	if owned.Spec.DeviceRef.Name != "router-p" {
		t.Errorf("deviceRef=%q", owned.Spec.DeviceRef.Name)
	}
	if len(owned.OwnerReferences) != 1 || owned.OwnerReferences[0].Name != "router-p" {
		t.Errorf("owner references = %+v", owned.OwnerReferences)
	}
	// ManagedFamilies must be pinned to the apphosting-prereq set,
	// regardless of what extra families appear in the inline body.
	want := map[string]struct{}{
		"interface_virtual_port_group": {},
		"dhcp":                         {},
		"access_list_extended":         {},
	}
	for _, f := range owned.Spec.ManagedFamilies {
		if _, ok := want[f]; !ok {
			t.Errorf("unexpected managed family %q on owned CR", f)
		}
	}
	if len(owned.Spec.ManagedFamilies) != len(want) {
		t.Errorf("ManagedFamilies=%v, want the 3-family prereq set", owned.Spec.ManagedFamilies)
	}
}

// TestReconcile_ConfigPrereqsRemovedDrivesEmptyIntentThenDeletes is the
// Wave 4A regression test for external-review Finding #8. Removing
// spec.configPrereqs no longer immediately deletes the owned CR (which
// orphaned device-side config because the engine never got to run an
// empty-intent reconcile). The controller now drives a teardown
// sequence: empty intent first, wait for InSync, then delete.
func TestReconcile_ConfigPrereqsRemovedDrivesEmptyIntentThenDeletes(t *testing.T) {
	device := newDevice("router-d", "default")
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		Configuration: runtime.RawExtension{Raw: []byte(`{"dhcp":{}}`)},
	}
	r := reconcilerFor(t, device)
	ctx := context.Background()

	// First pass creates the owned CR with managed families set and
	// pruneOnRelinquish=true (the load-bearing flag for Wave 4A).
	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-d")); err != nil {
		t.Fatalf("Reconcile (create): %v", err)
	}
	var owned configv1alpha1.IOSXEConfig
	ownedKey := types.NamespacedName{Namespace: "default", Name: "router-d-prereqs"}
	if err := r.Get(ctx, ownedKey, &owned); err != nil {
		t.Fatalf("expected owned CR: %v", err)
	}
	if !owned.Spec.PruneOnRelinquish {
		t.Errorf("owned CR must have pruneOnRelinquish=true so a later relinquishment reverts device state")
	}
	if len(owned.Spec.ManagedFamilies) == 0 {
		t.Fatal("owned CR should have non-empty ManagedFamilies after upsert")
	}

	// Operator removes prereqs.
	var updated ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-d"}, &updated); err != nil {
		t.Fatalf("get device: %v", err)
	}
	updated.Spec.ConfigPrereqs = nil
	if err := r.Update(ctx, &updated); err != nil {
		t.Fatalf("update device: %v", err)
	}

	// Tick 1: the controller drives the owned CR to empty intent;
	// the CR is NOT yet deleted (the engine hasn't run InSync).
	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-d")); err != nil {
		t.Fatalf("Reconcile (remove tick 1): %v", err)
	}
	var afterTick1 configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, ownedKey, &afterTick1); err != nil {
		t.Fatalf("owned CR must still exist after empty-intent step: %v", err)
	}
	if len(afterTick1.Spec.ManagedFamilies) != 0 {
		t.Errorf("owned CR ManagedFamilies should be empty after teardown step 1, got %v", afterTick1.Spec.ManagedFamilies)
	}
	if afterTick1.Spec.Source.Inline != nil {
		t.Errorf("owned CR Source.Inline should be nil after teardown step 1")
	}

	// Simulate the per-device reconciler reaching InSync on the empty
	// intent (in production this is the engine's apply-and-prune path).
	// Use a plain Update — the fake client used by reconcilerFor does
	// not register a status subresource for IOSXEConfig, so
	// Status().Update returns NotFound. The whole-object Update is
	// equivalent for the purposes of this test (reading status.phase
	// in the next reconcile tick).
	afterTick1.Status.Phase = "InSync"
	if err := r.Update(ctx, &afterTick1); err != nil {
		t.Fatalf("update simulating engine convergence: %v", err)
	}

	// Tick 2: the controller observes InSync + empty intent and
	// deletes the CR.
	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-d")); err != nil {
		t.Fatalf("Reconcile (remove tick 2): %v", err)
	}
	var stale configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, ownedKey, &stale); err == nil {
		t.Fatalf("owned CR must be deleted after teardown InSync: %+v", stale)
	}
}
