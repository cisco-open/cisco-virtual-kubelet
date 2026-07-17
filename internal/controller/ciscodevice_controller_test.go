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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
)

// newTestScheme builds a runtime.Scheme with all types needed by the reconciler.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(coordv1.AddToScheme(s))
	utilruntime.Must(rbacv1.AddToScheme(s))
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

type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.now = f.now.Add(d)
}

// reconcileRequest builds a ctrl.Request from a namespace and name.
func reconcileRequest(namespace, name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}

func findEnvVar(env []corev1.EnvVar, name string) (corev1.EnvVar, bool) {
	for _, item := range env {
		if item.Name == name {
			return item, true
		}
	}
	return corev1.EnvVar{}, false
}

func findWorkerCapability(items []ciskov1.WorkerCapabilityStatus, name ciskov1.WorkerCapabilityName) (ciskov1.WorkerCapabilityStatus, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return ciskov1.WorkerCapabilityStatus{}, false
}

func deleteCiscoDeviceForTest(t *testing.T, ctx context.Context, r *CiscoDeviceReconciler, namespace, name string) {
	t.Helper()
	var device ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &device); err != nil {
		t.Fatalf("get CiscoDevice %s/%s for deletion: %v", namespace, name, err)
	}
	if err := r.Delete(ctx, &device); err != nil {
		t.Fatalf("delete CiscoDevice %s/%s: %v", namespace, name, err)
	}
}

func assertVKAccessExists(t *testing.T, ctx context.Context, r *CiscoDeviceReconciler, key types.NamespacedName) {
	t.Helper()
	var sa corev1.ServiceAccount
	if err := r.Get(ctx, key, &sa); err != nil {
		t.Fatalf("ServiceAccount %s/%s not found: %v", key.Namespace, key.Name, err)
	}

	var rb rbacv1.RoleBinding
	if err := r.Get(ctx, key, &rb); err != nil {
		t.Fatalf("RoleBinding %s/%s not found: %v", key.Namespace, key.Name, err)
	}
	if rb.RoleRef.APIGroup != rbacv1.GroupName || rb.RoleRef.Kind != "ClusterRole" || rb.RoleRef.Name != vkDeviceClusterRole {
		t.Fatalf("RoleBinding RoleRef = %+v, want ClusterRole %s", rb.RoleRef, vkDeviceClusterRole)
	}
	if len(rb.Subjects) != 1 ||
		rb.Subjects[0].Kind != rbacv1.ServiceAccountKind ||
		rb.Subjects[0].Name != key.Name ||
		rb.Subjects[0].Namespace != key.Namespace {
		t.Fatalf("RoleBinding subjects = %+v, want ServiceAccount %s/%s", rb.Subjects, key.Namespace, key.Name)
	}

	var crb rbacv1.ClusterRoleBinding
	crbKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(key.Namespace, key.Name)}
	if err := r.Get(ctx, crbKey, &crb); err != nil {
		t.Fatalf("ClusterRoleBinding %s not found: %v", crbKey.Name, err)
	}
}

func assertVKAccessGone(t *testing.T, ctx context.Context, r *CiscoDeviceReconciler, key types.NamespacedName) {
	t.Helper()
	// The shared ServiceAccount is intentionally retained even after the last
	// device — deleting it would invalidate running VK pod tokens. Only the
	// permission-granting bindings are removed.
	var sa corev1.ServiceAccount
	if err := r.Get(ctx, key, &sa); err != nil {
		t.Fatalf("ServiceAccount %s/%s should be retained after last device: %v", key.Namespace, key.Name, err)
	}
	var rb rbacv1.RoleBinding
	if err := r.Get(ctx, key, &rb); !errors.IsNotFound(err) {
		t.Fatalf("RoleBinding %s/%s still present or get failed: %v", key.Namespace, key.Name, err)
	}
	var crb rbacv1.ClusterRoleBinding
	crbKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(key.Namespace, key.Name)}
	if err := r.Get(ctx, crbKey, &crb); !errors.IsNotFound(err) {
		t.Fatalf("ClusterRoleBinding %s still present or get failed: %v", crbKey.Name, err)
	}
}

func nonTelemetryEnv(env []corev1.EnvVar) []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(env))
	for _, item := range env {
		if isTelemetryEnvName(item.Name) || isDownwardAPIEnvName(item.Name) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func isDownwardAPIEnvName(name string) bool {
	switch name {
	case "POD_NAME", "POD_NAMESPACE", "POD_UID", "NODE_NAME":
		return true
	}
	return false
}

func isTelemetryEnvName(name string) bool {
	for _, candidate := range telemetryEnvPropagationNames {
		if name == candidate {
			return true
		}
	}
	return false
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
	affinity := deploy.Spec.Template.Spec.Affinity
	if affinity == nil ||
		affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil ||
		len(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) != 1 ||
		len(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions) != 1 {
		t.Fatalf("expected per-device VK pod to exclude virtual-kubelet nodes, got affinity=%#v", affinity)
	}
	requirement := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0]
	if requirement.Key != virtualKubeletNodeLabelKey ||
		requirement.Operator != corev1.NodeSelectorOpNotIn ||
		len(requirement.Values) != 1 ||
		requirement.Values[0] != virtualKubeletNodeLabelValue {
		t.Fatalf("unexpected virtual-kubelet node exclusion requirement: %#v", requirement)
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
	if len(deploy.Spec.Template.Spec.Containers[0].VolumeMounts) != 3 {
		t.Errorf("expected 3 volume mounts (device-config, tls-gen, tmp), got %d", len(deploy.Spec.Template.Spec.Containers[0].VolumeMounts))
	}

	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-b"}, &gotDevice); err != nil {
		t.Fatalf("CiscoDevice not found after reconcile: %v", err)
	}
	if gotDevice.Status.WorkerTopology != ciskov1.WorkerTopologyPerDevice {
		t.Fatalf("WorkerTopology=%q, want %q", gotDevice.Status.WorkerTopology, ciskov1.WorkerTopologyPerDevice)
	}
	capability, ok := findWorkerCapability(gotDevice.Status.WorkerCapabilities, ciskov1.WorkerCapabilityConfig)
	if !ok {
		t.Fatalf("status capabilities=%v missing config", gotDevice.Status.WorkerCapabilities)
	}
	if !capability.Enabled || capability.Runtime != ciskov1.WorkerRuntimePerDeviceWorker {
		t.Fatalf("config capability=%+v, want enabled on per-device worker", capability)
	}
	if gotDevice.Status.NetAsCode == nil ||
		gotDevice.Status.NetAsCode.Type != ciskov1.NetAsCodeModelDeviceCentric ||
		gotDevice.Status.NetAsCode.Stripe != "iosxe" {
		t.Fatalf("NetAsCode status=%+v, want iosxe device-centric", gotDevice.Status.NetAsCode)
	}
}

func TestReconcile_ProvisionsVKAccessInDeviceNamespace(t *testing.T) {
	device := newDevice("router-access", "tenant-a")
	device.UID = types.UID("router-access-uid")
	r := reconcilerFor(t, device)
	r.ServiceAccount = ""
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("tenant-a", "router-access")); err != nil {
		t.Fatalf("Reconcile returned unexpected error: %v", err)
	}

	var sa corev1.ServiceAccount
	key := types.NamespacedName{Namespace: "tenant-a", Name: DefaultServiceAccount}
	if err := r.Get(ctx, key, &sa); err != nil {
		t.Fatalf("ServiceAccount not found after reconcile: %v", err)
	}
	if metav1.IsControlledBy(&sa, device) {
		t.Fatalf("ServiceAccount owner references = %+v, want shared object not controlled by CiscoDevice", sa.OwnerReferences)
	}

	var rb rbacv1.RoleBinding
	if err := r.Get(ctx, key, &rb); err != nil {
		t.Fatalf("RoleBinding not found after reconcile: %v", err)
	}
	if metav1.IsControlledBy(&rb, device) {
		t.Fatalf("RoleBinding owner references = %+v, want shared object not controlled by CiscoDevice", rb.OwnerReferences)
	}
	if rb.RoleRef.APIGroup != rbacv1.GroupName || rb.RoleRef.Kind != "ClusterRole" || rb.RoleRef.Name != vkDeviceClusterRole {
		t.Fatalf("RoleBinding RoleRef = %+v, want ClusterRole %s (namespaced config role)", rb.RoleRef, vkDeviceClusterRole)
	}
	if len(rb.Subjects) != 1 {
		t.Fatalf("RoleBinding subjects = %+v, want exactly one ServiceAccount subject", rb.Subjects)
	}
	subject := rb.Subjects[0]
	if subject.Kind != rbacv1.ServiceAccountKind || subject.Name != DefaultServiceAccount || subject.Namespace != "tenant-a" {
		t.Fatalf("RoleBinding subject = %+v, want tenant ServiceAccount %s", subject, DefaultServiceAccount)
	}

	var crb rbacv1.ClusterRoleBinding
	crbKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName("tenant-a", DefaultServiceAccount)}
	if err := r.Get(ctx, crbKey, &crb); err != nil {
		t.Fatalf("ClusterRoleBinding not found after reconcile: %v", err)
	}
	if crb.RoleRef.APIGroup != rbacv1.GroupName || crb.RoleRef.Kind != "ClusterRole" || crb.RoleRef.Name != vkSharedClusterRole {
		t.Fatalf("ClusterRoleBinding RoleRef = %+v, want ClusterRole %s", crb.RoleRef, vkSharedClusterRole)
	}
	if len(crb.Subjects) != 1 {
		t.Fatalf("ClusterRoleBinding subjects = %+v, want exactly one ServiceAccount subject", crb.Subjects)
	}
	crbSubject := crb.Subjects[0]
	if crbSubject.Kind != rbacv1.ServiceAccountKind || crbSubject.Name != DefaultServiceAccount || crbSubject.Namespace != "tenant-a" {
		t.Fatalf("ClusterRoleBinding subject = %+v, want tenant ServiceAccount %s", crbSubject, DefaultServiceAccount)
	}
}

func TestReconcile_SharedVKAccessSurvivesUntilLastDeviceDeleted(t *testing.T) {
	first := newDevice("router-first", "tenant-shared")
	first.UID = types.UID("router-first-uid")
	second := newDevice("router-second", "tenant-shared")
	second.UID = types.UID("router-second-uid")
	r := reconcilerFor(t, first, second)
	r.ServiceAccount = ""
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("tenant-shared", "router-first")); err != nil {
		t.Fatalf("reconcile first device: %v", err)
	}
	if _, err := r.Reconcile(ctx, reconcileRequest("tenant-shared", "router-second")); err != nil {
		t.Fatalf("reconcile second device: %v", err)
	}

	key := types.NamespacedName{Namespace: "tenant-shared", Name: DefaultServiceAccount}
	deleteCiscoDeviceForTest(t, ctx, r, "tenant-shared", "router-first")
	if _, err := r.Reconcile(ctx, reconcileRequest("tenant-shared", "router-first")); err != nil {
		t.Fatalf("reconcile first device deletion: %v", err)
	}
	assertVKAccessExists(t, ctx, r, key)

	deleteCiscoDeviceForTest(t, ctx, r, "tenant-shared", "router-second")
	if _, err := r.Reconcile(ctx, reconcileRequest("tenant-shared", "router-second")); err != nil {
		t.Fatalf("reconcile second device deletion: %v", err)
	}
	assertVKAccessGone(t, ctx, r, key)
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
	pw, ok := findEnvVar(env, "VK_DEVICE_PASSWORD")
	if !ok {
		t.Fatalf("expected VK_DEVICE_PASSWORD env var, got %v", env)
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
	pw, ok := findEnvVar(env, "VK_DEVICE_PASSWORD")
	if !ok {
		t.Fatalf("expected VK_DEVICE_PASSWORD env var, got %v", env)
	}
	if pw.Value != "directpass" {
		t.Errorf("expected direct password value 'directpass', got %q", pw.Value)
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
	if got := nonTelemetryEnv(env); len(got) != 0 {
		t.Errorf("expected no non-telemetry env vars when neither password nor secretRef is set, got %v", got)
	}
}

func TestReconcile_PropagatesTelemetryEnvVars(t *testing.T) {
	t.Setenv(envOTELExporterOTLPEndpoint, "otelcol.observability:4317")
	t.Setenv(envOTELExporterOTLPInsecure, "true")
	// OTEL_EXPORTER_OTLP_HEADERS is intentionally NOT in the literal-value
	// propagation list (would leak collector auth tokens into per-device pod
	// specs). The SecretKeyRef path is exercised by
	// TestReconcile_PropagatesTelemetryHeadersAsSecretRef below.
	t.Setenv(envYANGModelsDir, "/opt/yang")
	t.Setenv(envCVKResourceAttributes, `{"deployment.environment":"lab","site.id":"sjc01"}`)
	t.Setenv(envCVKNXOSAllowExperimental, "true")

	device := newDevice("router-otel", "default")
	device.Spec.Password = ""
	device.Spec.CredentialSecretRef = nil
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-otel")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-otel" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	env := deploy.Spec.Template.Spec.Containers[0].Env
	want := map[string]string{
		envOTELExporterOTLPEndpoint: "otelcol.observability:4317",
		envOTELExporterOTLPInsecure: "true",
		envYANGModelsDir:            "/opt/yang",
		envCVKResourceAttributes:    `{"deployment.environment":"lab","site.id":"sjc01"}`,
		envCVKNXOSAllowExperimental: "true",
	}
	for name, value := range want {
		got, ok := findEnvVar(env, name)
		if !ok {
			t.Fatalf("expected propagated telemetry env var %s, got %v", name, env)
		}
		if got.Value != value {
			t.Errorf("%s=%q, want %q", name, got.Value, value)
		}
	}
	// Negative assertion: OTEL_EXPORTER_OTLP_HEADERS must not leak as a
	// literal value when no headersSecret is configured.
	if got, ok := findEnvVar(env, envOTELExporterOTLPHeaders); ok {
		t.Errorf("%s should not be propagated as literal value (security: leaks auth tokens to per-device pod readers); got %+v", envOTELExporterOTLPHeaders, got)
	}
}

// When the chart configures telemetry.otlp.headersSecret, the controller
// pod sees CVK_OTLP_HEADERS_SECRET_NAME / _KEY and mirrors that SecretKeyRef
// onto every per-device pod's OTEL_EXPORTER_OTLP_HEADERS env var.
func TestReconcile_PropagatesTelemetryHeadersAsSecretRef(t *testing.T) {
	t.Setenv(envCVKOTLPHeadersSecretName, "otlp-auth")
	t.Setenv(envCVKOTLPHeadersSecretKey, "headers")

	device := newDevice("router-otel-secret", "default")
	device.Spec.Password = ""
	device.Spec.CredentialSecretRef = nil
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-otel-secret")); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-otel-secret" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	env := deploy.Spec.Template.Spec.Containers[0].Env
	got, ok := findEnvVar(env, envOTELExporterOTLPHeaders)
	if !ok {
		t.Fatalf("expected %s on per-device pod, got %+v", envOTELExporterOTLPHeaders, env)
	}
	if got.Value != "" {
		t.Errorf("expected empty literal value (ValueFrom-only); got %q", got.Value)
	}
	if got.ValueFrom == nil || got.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected ValueFrom.SecretKeyRef; got %+v", got.ValueFrom)
	}
	if got.ValueFrom.SecretKeyRef.Name != "otlp-auth" || got.ValueFrom.SecretKeyRef.Key != "headers" {
		t.Errorf("SecretKeyRef={Name:%q,Key:%q}; want {otlp-auth, headers}",
			got.ValueFrom.SecretKeyRef.Name, got.ValueFrom.SecretKeyRef.Key)
	}
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
	if owned.Spec.PruneOnRelinquish {
		t.Errorf("steady-state configPrereqs CR must not set PruneOnRelinquish")
	}
}

func TestReconcile_ConfigPrereqsCreatesOwnedNXOSConfig(t *testing.T) {
	device := newDevice("nxos-p", "default")
	device.Spec.Driver = ciskov1.DeviceDriverNXOS
	device.Spec.Transport = "rest"
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		Configuration: runtime.RawExtension{Raw: []byte(
			`{"vlan":{"vlans":[{"id":123,"name":"apps"}]},"interface_ethernet":{"interfaces":[{"name":"Ethernet1/1"}]}}`,
		)},
	}
	r := reconcilerFor(t, device)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest("default", "nxos-p")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var owned configv1alpha1.NXOSConfig
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nxos-p-prereqs"}, &owned); err != nil {
		t.Fatalf("expected owned NXOSConfig: %v", err)
	}
	if owned.Spec.DeviceRef.Name != "nxos-p" {
		t.Errorf("deviceRef=%q", owned.Spec.DeviceRef.Name)
	}
	if len(owned.OwnerReferences) != 1 || owned.OwnerReferences[0].Name != "nxos-p" {
		t.Errorf("owner references = %+v", owned.OwnerReferences)
	}
	want := []string{"interface_ethernet", "vlan"}
	if len(owned.Spec.ManagedFamilies) != len(want) {
		t.Fatalf("ManagedFamilies=%v, want %v", owned.Spec.ManagedFamilies, want)
	}
	for i := range want {
		if owned.Spec.ManagedFamilies[i] != want[i] {
			t.Fatalf("ManagedFamilies=%v, want %v", owned.Spec.ManagedFamilies, want)
		}
	}
	if owned.Spec.Source.Inline == nil || !strings.Contains(string(owned.Spec.Source.Inline.Raw), `"vlan"`) {
		t.Fatalf("owned source=%v, want original NX-OS prereq source", owned.Spec.Source.Inline)
	}
	if owned.Spec.PruneOnRelinquish {
		t.Errorf("steady-state configPrereqs CR must not set PruneOnRelinquish")
	}
	var legacy configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nxos-p-prereqs"}, &legacy); !errors.IsNotFound(err) {
		t.Fatalf("expected no IOSXEConfig for NX-OS prereqs, got err=%v", err)
	}
}

func TestReconcile_ConfigPrereqsNXOSManagedFamiliesOverride(t *testing.T) {
	device := newDevice("nxos-override", "default")
	device.Spec.Driver = ciskov1.DeviceDriverNXOS
	device.Spec.Transport = "rest"
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		ManagedFamilies: []string{"vlan"},
		Configuration: runtime.RawExtension{Raw: []byte(
			`{"vlan":{"vlans":[{"id":123,"name":"apps"}]},"interface_ethernet":{"interfaces":[{"name":"Ethernet1/1"}]}}`,
		)},
	}
	r := reconcilerFor(t, device)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest("default", "nxos-override")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var owned configv1alpha1.NXOSConfig
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nxos-override-prereqs"}, &owned); err != nil {
		t.Fatalf("expected owned NXOSConfig: %v", err)
	}
	if got, want := owned.Spec.ManagedFamilies, []string{"vlan"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("ManagedFamilies=%v, want %v", got, want)
	}
}

func TestReconcile_ConfigPrereqsNXOSRejectsUnsupportedManagedFamilyOverride(t *testing.T) {
	device := newDevice("nxos-bad-override", "default")
	device.Spec.Driver = ciskov1.DeviceDriverNXOS
	device.Spec.Transport = "rest"
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		ManagedFamilies: []string{"banner"},
		Configuration: runtime.RawExtension{Raw: []byte(
			`{"banner":{"motd":"planned later"}}`,
		)},
	}
	r := reconcilerFor(t, device)
	ctx := context.Background()
	_, err := r.Reconcile(ctx, reconcileRequest("default", "nxos-bad-override"))
	if err == nil || !strings.Contains(err.Error(), "unsupported families") || !strings.Contains(err.Error(), "banner") {
		t.Fatalf("Reconcile error=%v, want unsupported banner family", err)
	}
	var owned configv1alpha1.NXOSConfig
	if getErr := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nxos-bad-override-prereqs"}, &owned); !errors.IsNotFound(getErr) {
		t.Fatalf("unsupported prereqs should not create NXOSConfig, get err=%v", getErr)
	}
}

func TestReconcile_ConfigPrereqsNXOSEnvelopeDerivesNormalizedFamilies(t *testing.T) {
	device := newDevice("nxos-envelope", "default")
	device.Spec.Driver = ciskov1.DeviceDriverNXOS
	device.Spec.Transport = "rest"
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		Configuration: runtime.RawExtension{Raw: []byte(`{
			"nxos": {
				"global": {
					"configuration": {
						"system": {"hostname": "baseline"}
					}
				},
				"devices": [{
					"name": "nxos-envelope",
					"configuration": {
						"vlan": {"vlans": [{"id": 123, "name": "apps"}]},
						"interfaces": {
							"ethernets": [{"id": "Ethernet1/1", "description": "apps"}]
						}
					}
				}]
			}
		}`)},
	}
	r := reconcilerFor(t, device)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest("default", "nxos-envelope")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var owned configv1alpha1.NXOSConfig
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nxos-envelope-prereqs"}, &owned); err != nil {
		t.Fatalf("expected owned NXOSConfig: %v", err)
	}
	want := []string{"interface_ethernet", "system", "vlan"}
	if len(owned.Spec.ManagedFamilies) != len(want) {
		t.Fatalf("ManagedFamilies=%v, want %v", owned.Spec.ManagedFamilies, want)
	}
	for i := range want {
		if owned.Spec.ManagedFamilies[i] != want[i] {
			t.Fatalf("ManagedFamilies=%v, want %v", owned.Spec.ManagedFamilies, want)
		}
	}
}

func TestReconcile_ConfigPrereqsNXOSEnvelopeRejectsUnsupportedDerivedFamily(t *testing.T) {
	device := newDevice("nxos-bad-envelope", "default")
	device.Spec.Driver = ciskov1.DeviceDriverNXOS
	device.Spec.Transport = "rest"
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		Configuration: runtime.RawExtension{Raw: []byte(`{
			"nxos": {
				"devices": [{
					"name": "nxos-bad-envelope",
					"configuration": {
						"banner": {"motd": "planned later"}
					}
				}]
			}
		}`)},
	}
	r := reconcilerFor(t, device)
	ctx := context.Background()
	_, err := r.Reconcile(ctx, reconcileRequest("default", "nxos-bad-envelope"))
	if err == nil || !strings.Contains(err.Error(), "unsupported families") || !strings.Contains(err.Error(), "banner") {
		t.Fatalf("Reconcile error=%v, want unsupported derived banner family", err)
	}
	var owned configv1alpha1.NXOSConfig
	if getErr := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nxos-bad-envelope-prereqs"}, &owned); !errors.IsNotFound(getErr) {
		t.Fatalf("unsupported prereqs should not create NXOSConfig, get err=%v", getErr)
	}
}

func TestReconcile_ConfigPrereqsRemovedDrivesEmptyIntentThenDeletes(t *testing.T) {
	device := newDevice("router-prune", "default")
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		Configuration: runtime.RawExtension{Raw: []byte(`{"dhcp":{}}`)},
	}
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-prune")); err != nil {
		t.Fatalf("Reconcile (create): %v", err)
	}
	ownedKey := types.NamespacedName{Namespace: "default", Name: "router-prune-prereqs"}
	var owned configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, ownedKey, &owned); err != nil {
		t.Fatalf("expected owned CR: %v", err)
	}
	if owned.Spec.PruneOnRelinquish {
		t.Errorf("owned CR steady-state must have pruneOnRelinquish=false")
	}
	owned.Finalizers = []string{"config.cisco.vk/lease-cleanup"}
	if err := r.Update(ctx, &owned); err != nil {
		t.Fatalf("add owned CR finalizer: %v", err)
	}

	var updated ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-prune"}, &updated); err != nil {
		t.Fatalf("get device: %v", err)
	}
	updated.Spec.ConfigPrereqs = nil
	if err := r.Update(ctx, &updated); err != nil {
		t.Fatalf("update device: %v", err)
	}

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-prune")); err != nil {
		t.Fatalf("Reconcile (remove tick 1): %v", err)
	}
	var afterTick1 configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, ownedKey, &afterTick1); err != nil {
		t.Fatalf("owned CR with finalizer must still exist after delete request: %v", err)
	}
	if len(afterTick1.Spec.ManagedFamilies) == 0 {
		t.Errorf("owned CR ManagedFamilies must remain non-empty during teardown")
	}
	if !afterTick1.Spec.PruneOnRelinquish {
		t.Errorf("owned CR PruneOnRelinquish must be true during teardown")
	}
	if afterTick1.DeletionTimestamp.IsZero() {
		t.Fatalf("owned CR should have deletion timestamp after teardown delete request")
	}

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-prune")); err != nil {
		t.Fatalf("Reconcile (observe delete): %v", err)
	}
	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-prune"}, &gotDevice); err != nil {
		t.Fatalf("get device after delete observation: %v", err)
	}
	cond := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionPrereqTeardownObserved)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("PrereqTeardownObserved=%+v, want True after seeing child deletion timestamp", cond)
	}
}

func TestPrereqsTeardownExternalDeleteIsRecreated(t *testing.T) {
	device := newDevice("router-external", "default")
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		Configuration: runtime.RawExtension{Raw: []byte(`{"dhcp":{}}`)},
	}
	recorder := record.NewFakeRecorder(10)
	r := reconcilerFor(t, device)
	r.Recorder = recorder
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", device.Name)); err != nil {
		t.Fatalf("Reconcile create: %v", err)
	}
	ownedKey := types.NamespacedName{Namespace: "default", Name: ownedIOSXEConfigName(device.Name)}
	var owned configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, ownedKey, &owned); err != nil {
		t.Fatalf("expected owned CR: %v", err)
	}
	owned.Status.Phase = "InSync"
	owned.Status.ObservedGeneration = owned.Generation
	if err := r.Update(ctx, &owned); err != nil {
		t.Fatalf("stage owned CR status: %v", err)
	}

	var updated ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: device.Name}, &updated); err != nil {
		t.Fatalf("get device: %v", err)
	}
	updated.Spec.ConfigPrereqs = nil
	if err := r.Update(ctx, &updated); err != nil {
		t.Fatalf("remove configPrereqs: %v", err)
	}
	if err := r.Delete(ctx, &owned); err != nil {
		t.Fatalf("external delete owned CR: %v", err)
	}

	result, err := r.Reconcile(ctx, reconcileRequest("default", device.Name))
	if err != nil {
		t.Fatalf("Reconcile teardown after external delete: %v", err)
	}
	if result.RequeueAfter != configPrereqsTeardownPollInterval {
		t.Fatalf("RequeueAfter=%v, want %v while recreated teardown CR converges", result.RequeueAfter, configPrereqsTeardownPollInterval)
	}
	var recreated configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, ownedKey, &recreated); err != nil {
		t.Fatalf("expected recreated empty-intent CR: %v", err)
	}
	if !recreated.Spec.PruneOnRelinquish {
		t.Fatalf("recreated CR pruneOnRelinquish=false, want true")
	}
	if recreated.Spec.Source.Inline == nil || string(recreated.Spec.Source.Inline.Raw) != string(emptyPrereqInline().Raw) {
		t.Fatalf("recreated CR inline=%v, want empty prereq intent", recreated.Spec.Source.Inline)
	}
	if len(recreated.OwnerReferences) != 1 || recreated.OwnerReferences[0].Name != device.Name {
		t.Fatalf("recreated CR ownerReferences=%+v", recreated.OwnerReferences)
	}
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "PrereqTeardownDeletedExternally") {
			t.Fatalf("event=%q, want PrereqTeardownDeletedExternally", event)
		}
	default:
		t.Fatal("expected PrereqTeardownDeletedExternally event")
	}
}

func TestNXOSPrereqsTeardownExternalDeleteSkipsWhenOwnershipStateGone(t *testing.T) {
	device := newDevice("nxos-external", "default")
	device.Spec.Driver = ciskov1.DeviceDriverNXOS
	device.Spec.Transport = "rest"
	device.Spec.ConfigPrereqs = &ciskov1.ConfigPrereqs{
		Configuration: runtime.RawExtension{Raw: []byte(`{"vlan":{"vlans":[{"id":123,"name":"apps"}]}}`)},
	}
	recorder := record.NewFakeRecorder(10)
	r := reconcilerFor(t, device)
	r.Recorder = recorder
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", device.Name)); err != nil {
		t.Fatalf("Reconcile create: %v", err)
	}
	ownedKey := types.NamespacedName{Namespace: "default", Name: ownedPrereqConfigName(device.Name)}
	var owned configv1alpha1.NXOSConfig
	if err := r.Get(ctx, ownedKey, &owned); err != nil {
		t.Fatalf("expected owned NXOSConfig: %v", err)
	}

	var updated ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: device.Name}, &updated); err != nil {
		t.Fatalf("get device: %v", err)
	}
	updated.Spec.ConfigPrereqs = nil
	if err := r.Update(ctx, &updated); err != nil {
		t.Fatalf("remove configPrereqs: %v", err)
	}
	if err := r.Delete(ctx, &owned); err != nil {
		t.Fatalf("external delete owned NXOSConfig: %v", err)
	}

	result, err := r.Reconcile(ctx, reconcileRequest("default", device.Name))
	if err != nil {
		t.Fatalf("Reconcile teardown after external delete: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter=%v, want no blocked recreate loop", result.RequeueAfter)
	}
	var recreated configv1alpha1.NXOSConfig
	if getErr := r.Get(ctx, ownedKey, &recreated); !errors.IsNotFound(getErr) {
		t.Fatalf("NXOSConfig should not be recreated without ownership state, get err=%v", getErr)
	}
	var gotDevice ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: device.Name}, &gotDevice); err != nil {
		t.Fatalf("get device after skipped teardown: %v", err)
	}
	cond := meta.FindStatusCondition(gotDevice.Status.Conditions, ciskov1.CiscoDeviceConditionPrereqTeardownObserved)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "NXOSConfigDeletedExternally" {
		t.Fatalf("PrereqTeardownObserved=%+v, want True/NXOSConfigDeletedExternally", cond)
	}
	sawSkipped := false
	for len(recorder.Events) > 0 {
		event := <-recorder.Events
		if strings.Contains(event, "PrereqTeardownSkipped") {
			sawSkipped = true
		}
	}
	if !sawSkipped {
		t.Fatal("expected PrereqTeardownSkipped event")
	}
}

func TestPrereqsTeardownLeaseBlockedHonoursForceAnnotation(t *testing.T) {
	now := metav1.NewTime(time.Now())
	device := newDevice("router-force", "default")
	device.Finalizers = []string{ciscoDeviceFinalizer}
	device.DeletionTimestamp = &now
	device.Status.Conditions = []metav1.Condition{{
		Type:   ciskov1.CiscoDeviceConditionPrereqTeardownObserved,
		Status: metav1.ConditionFalse,
		Reason: "PrereqsActive",
	}}
	owned := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:              ownedIOSXEConfigName(device.Name),
			Namespace:         device.Namespace,
			DeletionTimestamp: &now,
			Finalizers:        []string{"config.cisco.vk/lease-cleanup"},
		},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device.Name},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies:   append([]string(nil), apphostingPrereqFamilies...),
				Source:            configv1alpha1.ConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"dhcp":{}}`)}},
				PruneOnRelinquish: true,
			},
		},
		Status: configv1alpha1.IOSXEConfigStatus{
			AtomicReplaceOwnedKeys: map[string][]string{
				"dhcp": {"APPHOSTING"},
			},
		},
	}
	holder := "foreign/config#runtime"
	ttl := int32(30)
	renew := metav1.NewMicroTime(time.Now())
	lease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      engine.LeaseName(device.Name, "dhcp"),
			Namespace: "default",
		},
		Spec: coordv1.LeaseSpec{
			HolderIdentity:       &holder,
			LeaseDurationSeconds: &ttl,
			RenewTime:            &renew,
		},
	}
	recorder := record.NewFakeRecorder(10)
	r := reconcilerFor(t, device, owned, lease)
	r.Recorder = recorder
	ctx := context.Background()

	result, err := r.Reconcile(ctx, reconcileRequest("default", device.Name))
	if err != nil {
		t.Fatalf("Reconcile without force annotation: %v", err)
	}
	if result.RequeueAfter != configPrereqsTeardownPollInterval {
		t.Fatalf("RequeueAfter=%v, want %v while prereq CR is stuck deleting", result.RequeueAfter, configPrereqsTeardownPollInterval)
	}
	var blocked ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, &blocked); err != nil {
		t.Fatalf("get blocked device: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&blocked, ciscoDeviceFinalizer) {
		t.Fatalf("CiscoDevice finalizer removed without force annotation")
	}

	if blocked.Annotations == nil {
		blocked.Annotations = map[string]string{}
	}
	blocked.Annotations[ForcePrereqsSkipAnnotation] = "true"
	if err := r.Update(ctx, &blocked); err != nil {
		t.Fatalf("add force annotation: %v", err)
	}
	result, err = r.Reconcile(ctx, reconcileRequest("default", device.Name))
	if err != nil {
		t.Fatalf("Reconcile with force annotation: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Fatalf("result with force annotation=%+v, want no requeue", result)
	}
	var forced ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, &forced); err == nil {
		if controllerutil.ContainsFinalizer(&forced, ciscoDeviceFinalizer) {
			t.Fatalf("CiscoDevice finalizer still present after force annotation")
		}
	} else if !errors.IsNotFound(err) {
		t.Fatalf("get forced device: %v", err)
	}
	var child configv1alpha1.IOSXEConfig
	if err := r.Get(ctx, types.NamespacedName{Namespace: owned.Namespace, Name: owned.Name}, &child); err != nil {
		t.Fatalf("get child after force annotation: %v", err)
	}
	if child.Annotations[forceRelinquishSkipAnnotation] != "true" {
		t.Fatalf("child force relinquish annotation=%q, want true", child.Annotations[forceRelinquishSkipAnnotation])
	}
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "PrereqsSkipped") || !strings.Contains(event, "dhcp") {
			t.Fatalf("event=%q, want PrereqsSkipped listing dhcp", event)
		}
	default:
		t.Fatal("expected PrereqsSkipped event")
	}
}

func TestOpsPolicyEnvRendersConfigDiffAllowlist(t *testing.T) {
	cases := map[string]struct {
		policy *ciskov1.OpsPolicy
		want   []corev1.EnvVar
	}{
		"nil policy yields no env": {
			policy: nil,
			want:   nil,
		},
		"empty list yields no env (preserves unrestricted default)": {
			policy: &ciskov1.OpsPolicy{ConfigDiffAllowedNamespaces: nil},
			want:   nil,
		},
		"single namespace": {
			policy: &ciskov1.OpsPolicy{ConfigDiffAllowedNamespaces: []string{"ops"}},
			want: []corev1.EnvVar{
				{Name: "CVK_OPS_CONFIGDIFF_ALLOWED_NAMESPACES", Value: "ops"},
			},
		},
		"multi-namespace, dedupe + trim": {
			policy: &ciskov1.OpsPolicy{ConfigDiffAllowedNamespaces: []string{
				"ops", "  ops  ", "", "tenant-a", "tenant-a",
			}},
			want: []corev1.EnvVar{
				{Name: "CVK_OPS_CONFIGDIFF_ALLOWED_NAMESPACES", Value: "ops,tenant-a"},
			},
		},
		"only-empty-strings yields no env": {
			policy: &ciskov1.OpsPolicy{ConfigDiffAllowedNamespaces: []string{"", "  "}},
			want:   nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := opsPolicyEnv(tc.policy)
			if len(got) != len(tc.want) {
				t.Fatalf("env len=%d want %d (got=%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Name != tc.want[i].Name || got[i].Value != tc.want[i].Value {
					t.Fatalf("env[%d] = %+v want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestReconcile_DeploymentSecurityContexts asserts the per-device VK pod is
// stamped with the Pod Security Standards "restricted" profile, and that the
// writable emptyDir mounts which make readOnlyRootFilesystem viable
// (generated-TLS dir and /tmp for upgrade image staging) are present.
func TestReconcile_DeploymentSecurityContexts(t *testing.T) {
	device := newDevice("router-sec", "default")
	r := reconcilerFor(t, device)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-sec")); err != nil {
		t.Fatalf("Reconcile returned unexpected error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-sec" + deploymentSuffix}, &deploy); err != nil {
		t.Fatalf("Deployment not found after reconcile: %v", err)
	}

	pod := deploy.Spec.Template.Spec
	if pod.SecurityContext == nil ||
		pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot ||
		pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser != distrolessNonRootUID ||
		pod.SecurityContext.RunAsGroup == nil || *pod.SecurityContext.RunAsGroup != distrolessNonRootGID ||
		pod.SecurityContext.FSGroup == nil || *pod.SecurityContext.FSGroup != distrolessNonRootGID ||
		pod.SecurityContext.SeccompProfile == nil ||
		pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod securityContext does not meet the restricted profile: %#v", pod.SecurityContext)
	}

	sc := pod.Containers[0].SecurityContext
	if sc == nil ||
		sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation ||
		sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem ||
		sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("container securityContext does not meet the restricted profile: %#v", sc)
	}

	var haveTmpMount, haveTmpVolume bool
	for _, m := range pod.Containers[0].VolumeMounts {
		if m.Name == "tmp" && m.MountPath == "/tmp" {
			haveTmpMount = true
		}
	}
	for _, v := range pod.Volumes {
		if v.Name == "tmp" && v.EmptyDir != nil {
			haveTmpVolume = true
		}
	}
	if !haveTmpMount || !haveTmpVolume {
		t.Fatalf("expected a tmp emptyDir mounted at /tmp (mount=%v volume=%v)", haveTmpMount, haveTmpVolume)
	}
}
