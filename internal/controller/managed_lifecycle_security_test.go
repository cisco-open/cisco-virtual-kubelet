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
	"reflect"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configengine "github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

func TestManagedWorkerUsesOwnedIncarnationServiceAccount(t *testing.T) {
	device := newDevice("switch-owned-worker", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	r := reconcilerFor(t, device)
	saName := managedWorkerServiceAccountName(device)
	if err := r.ensureVKAccess(context.Background(), device, saName, true); err != nil {
		t.Fatalf("ensureVKAccess: %v", err)
	}

	var sa corev1.ServiceAccount
	key := types.NamespacedName{Namespace: device.Namespace, Name: saName}
	if err := r.Get(context.Background(), key, &sa); err != nil {
		t.Fatal(err)
	}
	if !managedServiceAccountOwnedByDevice(&sa, device) || !managedServiceAccountAnnotationsMatch(&sa, device) {
		t.Fatalf("managed ServiceAccount is not exactly device-bound: %#v", sa.ObjectMeta)
	}
	if got := r.serviceAccountForDevice(device); got != saName {
		t.Fatalf("durable managed ServiceAccount=%q, want %q", got, saName)
	}
	for _, expected := range generatedWorkerClusterRoleBindings(device.Namespace, saName, true) {
		var binding rbacv1.ClusterRoleBinding
		if err := r.Get(context.Background(), types.NamespacedName{Name: expected.name}, &binding); err != nil {
			t.Fatalf("managed ClusterRoleBinding %s: %v", expected.name, err)
		}
		if binding.RoleRef.Name != expected.role || !reflect.DeepEqual(binding.Subjects, exactWorkerSubject(device.Namespace, saName)) {
			t.Fatalf("managed ClusterRoleBinding %s = role %q subjects %+v", expected.name, binding.RoleRef.Name, binding.Subjects)
		}
	}
}

func TestManagedWorkerPodDeleteBindingRequiresAdmissionPreflight(t *testing.T) {
	device := newDevice("switch-preflight-gate", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	r := reconcilerFor(t, device)
	r.ManagedAdmissionVerified = false
	saName := managedWorkerServiceAccountName(device)
	err := r.ensureVKAccess(context.Background(), device, saName, true)
	if err == nil || !strings.Contains(err.Error(), "verified native admission contract") {
		t.Fatalf("unverified managed access error = %v", err)
	}
	var binding rbacv1.ClusterRoleBinding
	key := types.NamespacedName{Name: vkPodDeleteClusterRoleBindingName(device.Namespace, saName)}
	if err := r.Get(context.Background(), key, &binding); !apierrors.IsNotFound(err) {
		t.Fatalf("unverified preflight created Pod-delete binding: %v", err)
	}
	r.ManagedAdmissionVerified = true
	if err := r.ensureVKAccess(context.Background(), device, saName, true); err != nil {
		t.Fatalf("verified ensureVKAccess: %v", err)
	}
	if err := r.Get(context.Background(), key, &binding); err != nil {
		t.Fatalf("verified preflight did not create Pod-delete binding: %v", err)
	}
}

func TestTopologyLegacyWorkerUsesOwnedIncarnationServiceAccount(t *testing.T) {
	device := newDevice("switch-legacy-worker", "edge")
	device.UID = "legacy-device-uid"
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	saName := topologyLegacyWorkerServiceAccountName(device)
	if got := r.serviceAccountForDevice(device); got != saName {
		t.Fatalf("topology legacy ServiceAccount=%q, want %q", got, saName)
	}
	if err := r.ensureVKAccess(context.Background(), device, saName, false); err != nil {
		t.Fatalf("ensureVKAccess: %v", err)
	}

	var sa corev1.ServiceAccount
	key := types.NamespacedName{Namespace: device.Namespace, Name: saName}
	if err := r.Get(context.Background(), key, &sa); err != nil {
		t.Fatal(err)
	}
	wantAnnotations := workerServiceAccountAnnotations(device, false)
	if !managedServiceAccountOwnedByDevice(&sa, device) || !workerAnnotationsMatch(sa.Annotations, wantAnnotations) {
		t.Fatalf("topology legacy ServiceAccount is not exactly device-bound: %#v", sa.ObjectMeta)
	}
	if sa.Annotations[managedprotocol.AnnotationManaged] != "" ||
		sa.Annotations[managedprotocol.AnnotationWorkerMode] != managedprotocol.WorkerModeLegacy {
		t.Fatalf("legacy worker mode annotations=%v", sa.Annotations)
	}
	var crb rbacv1.ClusterRoleBinding
	if err := r.Get(context.Background(), types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, saName)}, &crb); err != nil {
		t.Fatal(err)
	}
	if crb.RoleRef.Name != vkSharedClusterRole {
		t.Fatalf("legacy worker role=%q, want %q", crb.RoleRef.Name, vkSharedClusterRole)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Name: vkPodDeleteClusterRoleBindingName(device.Namespace, saName)}, &rbacv1.ClusterRoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("legacy worker unexpectedly received managed Pod-delete binding: %v", err)
	}
}

func TestGeneratedWorkerRejectsEveryAdditiveBinding(t *testing.T) {
	for _, clusterScoped := range []bool{false, true} {
		name := "rolebinding"
		if clusterScoped {
			name = "clusterrolebinding"
		}
		t.Run(name, func(t *testing.T) {
			device := newDevice("switch-additive-"+name, "edge")
			device.UID = types.UID("device-uid-" + name)
			device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
				DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
			}
			r := reconcilerFor(t, device)
			r.ManagedTopology = true
			saName := managedWorkerServiceAccountName(device)
			if err := r.ensureVKAccess(context.Background(), device, saName, true); err != nil {
				t.Fatal(err)
			}
			subjects := exactWorkerSubject(device.Namespace, saName)
			if clusterScoped {
				extra := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "unexpected-broad-grant"},
					RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole}, Subjects: subjects}
				if err := r.Create(context.Background(), extra); err != nil {
					t.Fatal(err)
				}
			} else {
				extra := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "unexpected-grant"},
					RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole}, Subjects: subjects}
				if err := r.Create(context.Background(), extra); err != nil {
					t.Fatal(err)
				}
			}
			err := r.ensureVKAccess(context.Background(), device, saName, true)
			if err == nil || !strings.Contains(err.Error(), "unexpected additive") {
				t.Fatalf("additive binding audit error=%v", err)
			}
		})
	}
}

func TestGeneratedWorkerCleanupPreservesForeignBindingCollision(t *testing.T) {
	for _, target := range []string{"baseline", "pod-delete"} {
		t.Run(target, func(t *testing.T) {
			device := newDevice("switch-binding-collision-"+target, "edge")
			device.UID = types.UID("device-uid-" + target)
			device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
				DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
			}
			r := reconcilerFor(t, device)
			r.ManagedTopology = true
			saName := managedWorkerServiceAccountName(device)
			ctx := context.Background()
			if err := r.ensureVKAccess(ctx, device, saName, true); err != nil {
				t.Fatal(err)
			}
			expected := generatedWorkerClusterRoleBindings(device.Namespace, saName, true)[0]
			if target == "pod-delete" {
				expected = generatedWorkerClusterRoleBindings(device.Namespace, saName, true)[1]
			}
			key := types.NamespacedName{Name: expected.name}
			var binding rbacv1.ClusterRoleBinding
			if err := r.Get(ctx, key, &binding); err != nil {
				t.Fatal(err)
			}
			if err := r.Delete(ctx, &binding); err != nil {
				t.Fatal(err)
			}
			foreign := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: key.Name, UID: "foreign-binding-uid"},
				RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: expected.role},
				Subjects: exactWorkerSubject(device.Namespace, saName)}
			if err := r.Create(ctx, foreign); err != nil {
				t.Fatal(err)
			}
			err := r.cleanupVKClusterAccess(ctx, device, saName)
			if err == nil || !strings.Contains(err.Error(), "refusing to delete") {
				t.Fatalf("cleanup collision error=%v", err)
			}
			var remaining rbacv1.ClusterRoleBinding
			if err := r.Get(ctx, key, &remaining); err != nil {
				t.Fatalf("foreign binding was deleted: %v", err)
			}
		})
	}
}

func TestGeneratedWorkerCleanupRejectsTamperedCanonicalPodDeleteSubject(t *testing.T) {
	device := newDevice("switch-tampered-delete-subject", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	saName := managedWorkerServiceAccountName(device)
	ctx := context.Background()
	if err := r.ensureVKAccess(ctx, device, saName, true); err != nil {
		t.Fatal(err)
	}

	expectedBindings := generatedWorkerClusterRoleBindings(device.Namespace, saName, true)
	completer := expectedBindings[1]
	completerKey := types.NamespacedName{Name: completer.name}
	var tampered rbacv1.ClusterRoleBinding
	if err := r.Get(ctx, completerKey, &tampered); err != nil {
		t.Fatal(err)
	}
	foreignSubject := exactWorkerSubject(device.Namespace, "cisco-vk-managed-foreign-node-deadbeef")
	tampered.Subjects = foreignSubject
	if err := r.Update(ctx, &tampered); err != nil {
		t.Fatal(err)
	}

	err := r.cleanupVKClusterAccess(ctx, device, saName)
	if err == nil || !strings.Contains(err.Error(), "unexpected subjects") {
		t.Fatalf("tampered canonical cleanup error=%v", err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: saName}, &corev1.ServiceAccount{}); err != nil {
		t.Fatalf("cleanup deleted ServiceAccount before rejecting tampered canonical binding: %v", err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: saName}, &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("cleanup deleted RoleBinding before rejecting tampered canonical binding: %v", err)
	}
	for _, expected := range expectedBindings {
		var binding rbacv1.ClusterRoleBinding
		if err := r.Get(ctx, types.NamespacedName{Name: expected.name}, &binding); err != nil {
			t.Fatalf("cleanup deleted canonical ClusterRoleBinding %s before rejecting tamper: %v", expected.name, err)
		}
		if expected.name == completer.name && !reflect.DeepEqual(binding.Subjects, foreignSubject) {
			t.Fatalf("tampered completer subjects changed during rejected cleanup: %+v", binding.Subjects)
		}
	}
}

func TestManagedWorkerRefusesForeignServiceAccountCollision(t *testing.T) {
	device := newDevice("switch-collision", "edge")
	device.UID = "device-uid"
	saName := managedWorkerServiceAccountName(device)
	foreign := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: saName, UID: "foreign-sa-uid",
	}}
	r := reconcilerFor(t, device, foreign)
	err := r.ensureVKAccess(context.Background(), device, saName, true)
	if err == nil || !strings.Contains(err.Error(), "not controlled") {
		t.Fatalf("foreign ServiceAccount collision error=%v", err)
	}
	var binding rbacv1.RoleBinding
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: saName}, &binding); err == nil {
		t.Fatal("foreign ServiceAccount collision unexpectedly created a RoleBinding")
	}
}

func TestManagedWorkerServiceAccountCleanupIsUIDAndOwnerBound(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "owned"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			device := newDevice("switch-cleanup-"+name, "edge")
			device.UID = types.UID("device-uid-" + name)
			device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
				DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
			}
			saName := managedWorkerServiceAccountName(device)
			var objects []runtime.Object
			if foreign {
				objects = append(objects, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
					Namespace: device.Namespace, Name: saName, UID: "foreign-sa-uid",
				}})
			}
			r := reconcilerFor(t, append([]runtime.Object{device}, objects...)...)
			ctx := context.Background()
			if !foreign {
				if err := r.ensureVKAccess(ctx, device, saName, true); err != nil {
					t.Fatal(err)
				}
				var sa corev1.ServiceAccount
				if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: saName}, &sa); err != nil {
					t.Fatal(err)
				}
				// A real API server assigns this immutable identity. fake.Client does
				// not, so seed it to exercise the deletion precondition.
				sa.UID = "owned-sa-uid"
				if err := r.Update(ctx, &sa); err != nil {
					t.Fatal(err)
				}
			}
			err := r.cleanupVKClusterAccess(ctx, device, saName)
			if foreign {
				if err == nil || !strings.Contains(err.Error(), "not exactly owned") {
					t.Fatalf("foreign cleanup error=%v, want fail-closed ownership error", err)
				}
			} else if err != nil {
				t.Fatalf("cleanupVKClusterAccess: %v", err)
			}
			var remaining corev1.ServiceAccount
			getErr := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: saName}, &remaining)
			if foreign && getErr != nil {
				t.Fatalf("foreign ServiceAccount was removed: %v", getErr)
			}
			if !foreign && !apierrors.IsNotFound(getErr) {
				t.Fatalf("owned ServiceAccount remains or lookup failed: %v", getErr)
			}
			if !foreign {
				for _, binding := range generatedWorkerClusterRoleBindings(device.Namespace, saName, true) {
					getErr := r.Get(ctx, types.NamespacedName{Name: binding.name}, &rbacv1.ClusterRoleBinding{})
					if !apierrors.IsNotFound(getErr) {
						t.Fatalf("owned ClusterRoleBinding %s remains or lookup failed: %v", binding.name, getErr)
					}
				}
			}
		})
	}
}

func TestManagedReconcileAlwaysUsesRecreateStrategy(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "true")
	device := newDevice("switch-managed-strategy", "edge")
	device.UID = "device-uid"
	device.Spec.PhysicalIdentity = "serial-switch-managed-strategy"
	device.Labels = map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		topology.CiscoTopologyLabelPrefix + "site": "site-a",
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid",
		Labels: map[string]string{topology.LabelType: topology.TypeVirtualKubelet},
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationNodeName:        device.Name,
			managedprotocol.AnnotationNodeUID:         "node-uid",
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
			managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
		},
	}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	policy, ledger := managedPolicyAndLedger(t, nil)

	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &corev1.Node{}).
		WithObjects(device, node, policy, ledger).Build()
	r := &CiscoDeviceReconciler{
		Client: leaseUIDAssigningClient{Client: apiClient}, APIReader: apiClient, Scheme: scheme, Image: "cisco-vk:test",
		ManagedTopology: true, ManagedAdmissionVerified: true,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		LeaseNamespace: device.Namespace,
	}
	if _, err := r.Reconcile(context.Background(), reconcileRequest(device.Namespace, device.Name)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var deployment appsv1.Deployment
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &deployment); err != nil {
		t.Fatal(err)
	}
	if deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("managed Deployment strategy=%q, want Recreate", deployment.Spec.Strategy.Type)
	}
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	var sa corev1.ServiceAccount
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: managedWorkerServiceAccountName(&current)}, &sa); err != nil {
		t.Fatal(err)
	}
	if !managedServiceAccountOwnedByDevice(&sa, &current) {
		t.Fatal("managed reconcile did not bind the worker ServiceAccount to the CiscoDevice incarnation")
	}
}

func TestMaintenanceFenceRepairsOnlyManagedWorkerSubstrate(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "true")
	ctx := context.Background()
	device := newDevice("switch-fenced-worker", "edge")
	device.UID = "device-uid"
	device.Generation = 1
	device.Spec.PhysicalIdentity = "serial-switch-fenced-worker"
	device.Labels = map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		topology.CiscoTopologyLabelPrefix + "site": "site-a",
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid",
		Labels: map[string]string{topology.LabelType: topology.TypeVirtualKubelet},
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationNodeName:        device.Name,
			managedprotocol.AnnotationNodeUID:         "node-uid",
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
			managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
		},
	}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue,
	}}}}
	policy, ledger := managedPolicyAndLedger(t, nil)
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &corev1.Node{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithIndex(&ciskov1.CiscoDevice{}, ciscoDevicePhysicalIdentityIndex, physicalIdentityIndexValues).
		WithObjects(device, node, policy, ledger).Build()
	r := &CiscoDeviceReconciler{
		Client: leaseUIDAssigningClient{Client: apiClient}, APIReader: apiClient, Scheme: scheme,
		Image: "cisco-vk:old", ManagedTopology: true, ManagedAdmissionVerified: true,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		LeaseNamespace: device.Namespace,
	}
	request := reconcileRequest(device.Namespace, device.Name)
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatalf("initial Reconcile: %v", err)
	}

	deploymentKey := types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}
	var deployment appsv1.Deployment
	if err := apiClient.Get(ctx, deploymentKey, &deployment); err != nil {
		t.Fatal(err)
	}
	oldRevision := deployment.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision]
	if oldRevision == "" || deployment.Spec.Template.Spec.Containers[0].Image != "cisco-vk:old" {
		t.Fatalf("initial managed worker = %#v", deployment.Spec.Template)
	}
	// fake.Client does not assign server identities. Seed the immutable identity
	// required by the production pre-rollout fence.
	deployment.UID = "deployment-uid"
	deployment.Generation = 1
	if err := apiClient.Update(ctx, &deployment); err != nil {
		t.Fatal(err)
	}

	var current ciskov1.CiscoDevice
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	startedAt := metav1.NewTime(now.Add(-5 * time.Minute))
	token := "00000000-0000-4000-8000-000000000001"
	planHash := "sha256:" + strings.Repeat("a", 64)
	reservationID := "reservation"
	acquisitionID := strings.Repeat("b", 32)
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "cancelled-upgrade", UID: "cancelled-upgrade-uid",
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationNodeName:        node.Name,
			managedprotocol.AnnotationNodeUID:         string(node.UID),
			managedprotocol.AnnotationWorkerUsername:  node.Annotations[managedprotocol.AnnotationWorkerUsername],
			managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
			managedprotocol.AnnotationCampaignUID:     "campaign-uid",
			managedprotocol.AnnotationPlanHash:        planHash,
			managedprotocol.AnnotationLedgerUID:       "ledger-uid",
			managedprotocol.AnnotationReservationID:   reservationID,
		},
	}}
	leaf.Spec.DeviceRef.Name = device.Name
	if err := apiClient.Create(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(leaf), leaf); err != nil {
		t.Fatal(err)
	}
	currentRevision := int64(1)
	leaf.Status = opsv1alpha1.IOSXESoftwareUpgradeStatus{
		Phase:          opsv1alpha1.UpgradePhaseTransferring,
		ExecutionModel: opsv1alpha1.UpgradeExecutionModelAtMostOnceV1,
		ManagerAdmission: &opsv1alpha1.UpgradeManagerAdmissionStatus{
			ProtocolVersion:  opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
			State:            opsv1alpha1.UpgradeManagerAdmissionRevoked,
			RevocationReason: "CampaignCancelled", CampaignUID: "campaign-uid", PlanHash: planHash,
			PolicyUID: "policy-uid", PolicyResourceVersion: "1", PolicyEpoch: 1,
			LedgerUID: "ledger-uid", ReservationID: reservationID, TopologyLockID: acquisitionID,
			LeafUID: string(leaf.UID), DeviceUID: string(device.UID), DeviceGeneration: current.Generation,
			PhysicalIdentity: device.Spec.PhysicalIdentity, NodeUID: string(node.UID),
			ControlRevision: &currentRevision, UpdatedAt: metav1.NewTime(now),
		},
		ManagerControl: &opsv1alpha1.UpgradeManagerControlStatus{
			Revision: currentRevision, Cancel: true, UpdatedAt: metav1.NewTime(now), Reason: "CampaignCancelled",
		},
		ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			ProtocolVersion: opsv1alpha1.ManagedDrainProtocolPDBV1,
			State:           opsv1alpha1.UpgradeManagerDrainRecovering, SessionToken: token,
			ReservationID: reservationID, PolicyEpoch: 1, ControlRevision: currentRevision,
			NodeUID: string(node.UID), StartedAt: startedAt,
			DrainDeadline:    metav1.NewTime(now.Add(30 * time.Minute)),
			RecoveryDeadline: ptr.To(metav1.NewTime(now.Add(time.Hour))),
			UpdatedAt:        metav1.NewTime(now),
		},
	}
	if err := apiClient.Status().Update(ctx, leaf); err != nil {
		t.Fatal(err)
	}

	current.Status.Phase = "RecoverySentinel"
	meta.SetStatusCondition(&current.Status.Conditions, metav1.Condition{
		Type: ciskov1.CiscoDeviceConditionAggregatorOwning, Status: metav1.ConditionTrue,
		Reason: "RecoverySentinel", Message: "must remain unchanged during worker-only recovery",
		ObservedGeneration: current.Generation,
	})
	current.Status.TopologyLock = &ciskov1.DeviceTopologyLockStatus{
		State: ciskov1.DeviceTopologyLockActive, PolicyEpoch: 1, AcquisitionID: acquisitionID,
		CampaignNamespace: device.Namespace, CampaignName: "campaign", CampaignUID: "campaign-uid",
		PlanHash: planHash, ReservationID: reservationID, DeviceUID: string(device.UID),
		DeviceGeneration: current.Generation, NodeUID: string(node.UID),
		ProjectionHash: current.Status.TopologyProjection.EffectiveLabelHash, AcquiredAt: metav1.NewTime(now.Add(-time.Hour)),
	}
	if err := apiClient.Status().Update(ctx, &current); err != nil {
		t.Fatal(err)
	}

	leaseKey := types.NamespacedName{
		Namespace: device.Namespace,
		Name:      configengine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily),
	}
	var lease coordv1.Lease
	if err := apiClient.Get(ctx, leaseKey, &lease); err != nil {
		t.Fatal(err)
	}
	holder := mutationguard.UpgradeHolderIdentity(leaf)
	lease.Spec = coordv1.LeaseSpec{
		HolderIdentity: &holder, AcquireTime: ptr.To(metav1.NewMicroTime(now.Add(-time.Minute))),
		RenewTime: ptr.To(metav1.NewMicroTime(now)), LeaseDurationSeconds: ptr.To[int32](3600),
		LeaseTransitions: ptr.To[int32](1),
	}
	for key, value := range map[string]string{
		managedprotocol.AnnotationMaintenanceRequestVersion:  managedprotocol.DrainProtocolVersion,
		managedprotocol.AnnotationMaintenanceSessionToken:    token,
		managedprotocol.AnnotationMaintenanceRequestedAt:     startedAt.Format(time.RFC3339Nano),
		managedprotocol.AnnotationMaintenanceOperationNS:     leaf.Namespace,
		managedprotocol.AnnotationMaintenanceOperationName:   leaf.Name,
		managedprotocol.AnnotationMaintenanceOperationUID:    string(leaf.UID),
		managedprotocol.AnnotationMaintenanceControlRevision: "0",
		managedprotocol.AnnotationMaintenancePurpose:         managedprotocol.MaintenancePurposeSoftwareMutation,
	} {
		lease.Annotations[key] = value
	}
	if err := apiClient.Update(ctx, &lease); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	acknowledgedAt := metav1.NewTime(now.Add(-4 * time.Minute))
	current.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase:           ciskov1.DeviceMaintenanceSessionActive,
		ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
		Purpose:         ciskov1.DeviceMaintenancePurposeSoftwareMutation, SessionToken: token,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{
			DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: lease.Namespace, Name: lease.Name, UID: string(lease.UID),
			},
			Holder: holder,
		},
		Operation: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID),
		},
		DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID),
		RequestedAt: startedAt, AcknowledgedAt: &acknowledgedAt, ControlRevision: 0,
	}
	if err := apiClient.Status().Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	requestAnnotations := map[string]string{}
	for _, key := range []string{
		managedprotocol.AnnotationMaintenanceRequestVersion,
		managedprotocol.AnnotationMaintenanceSessionToken,
		managedprotocol.AnnotationMaintenanceRequestedAt,
		managedprotocol.AnnotationMaintenanceOperationNS,
		managedprotocol.AnnotationMaintenanceOperationName,
		managedprotocol.AnnotationMaintenanceOperationUID,
		managedprotocol.AnnotationMaintenanceControlRevision,
		managedprotocol.AnnotationMaintenancePurpose,
	} {
		requestAnnotations[key] = lease.Annotations[key]
	}
	assertOldRequestRetained := func() {
		t.Helper()
		var retained coordv1.Lease
		if err := apiClient.Get(ctx, leaseKey, &retained); err != nil {
			t.Fatal(err)
		}
		if retained.Spec.HolderIdentity == nil || *retained.Spec.HolderIdentity != holder ||
			string(retained.UID) != string(lease.UID) {
			t.Fatalf("manager changed retained Lease identity: %#v", retained)
		}
		for key, want := range requestAnnotations {
			if retained.Annotations[key] != want {
				t.Fatalf("manager rewrote old request annotation %s=%q, want %q", key, retained.Annotations[key], want)
			}
		}
	}

	r.Image = "cisco-vk:new"
	result, err := r.Reconcile(ctx, request)
	if err != nil || result.RequeueAfter != topologyRequeueInterval {
		t.Fatalf("pre-fence Reconcile = (%+v, %v), want managed maintenance fence", result, err)
	}
	if err := apiClient.Get(ctx, deploymentKey, &deployment); err != nil {
		t.Fatal(err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != "cisco-vk:old" {
		t.Fatal("worker Deployment changed before the replacement revision was fenced")
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.WorkerRevision == nil || current.Status.WorkerRevision.DesiredRevision == oldRevision ||
		current.Status.WorkerRevision.ObservedRevision != "" || current.Status.WorkerRevision.PodUID != "" {
		t.Fatalf("replacement worker pre-fence = %#v", current.Status.WorkerRevision)
	}
	assertOldRequestRetained()
	var guardedNode corev1.Node
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(node), &guardedNode); err != nil {
		t.Fatal(err)
	}
	if !hasTaintIdentity(guardedNode.Spec.Taints, taintIdentity(topologyInitializationTaint())) {
		t.Fatal("maintenance failure did not retain the scheduling guard")
	}

	result, err = r.Reconcile(ctx, request)
	if err != nil || result.RequeueAfter != topologyRequeueInterval {
		t.Fatalf("worker repair Reconcile = (%+v, %v), want managed maintenance fence", result, err)
	}
	if err := apiClient.Get(ctx, deploymentKey, &deployment); err != nil {
		t.Fatal(err)
	}
	newRevision := deployment.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision]
	if deployment.Spec.Template.Spec.Containers[0].Image != "cisco-vk:new" ||
		newRevision == "" || newRevision == oldRevision {
		t.Fatalf("guarded worker substrate did not rotate: image=%q revision=%q",
			deployment.Spec.Template.Spec.Containers[0].Image, newRevision)
	}
	assertOldRequestRetained()
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase != "RecoverySentinel" {
		t.Fatalf("guarded recovery crossed into normal status reconciliation: phase=%q", current.Status.Phase)
	}
	aggregator := findCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwning)
	if aggregator == nil || aggregator.Status != metav1.ConditionTrue || aggregator.Reason != "RecoverySentinel" {
		t.Fatalf("guarded recovery changed aggregator handover status: %#v", aggregator)
	}
	conflict := findCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyConflict)
	if conflict == nil || conflict.Status != metav1.ConditionTrue || conflict.Reason != "MaintenanceFenceFailed" {
		t.Fatalf("maintenance topology failure = %#v", conflict)
	}

	// Complete only the Kubernetes-side replacement evidence. The rejected old
	// request remains unchanged; the new worker receives identity, not mutation
	// authority, and can now persist cancellation before releasing the Lease.
	deployment.Status = appsv1.DeploymentStatus{
		ObservedGeneration: deployment.Generation, Replicas: 1, UpdatedReplicas: 1,
		ReadyReplicas: 1, AvailableReplicas: 1,
	}
	if err := apiClient.Status().Update(ctx, &deployment); err != nil {
		t.Fatal(err)
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "replacement-worker-rs", UID: "replacement-worker-rs-uid",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment", Name: deployment.Name,
			UID: deployment.UID, Controller: ptr.To(true),
		}},
	}}
	if err := apiClient.Create(ctx, replicaSet); err != nil {
		t.Fatal(err)
	}
	startTime := metav1.NewTime(time.Now().UTC().Add(-time.Second))
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "replacement-worker", UID: "replacement-worker-uid",
		Labels:      perDeviceDeploymentLabels(device.Name),
		Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: newRevision},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet", Name: replicaSet.Name,
			UID: replicaSet.UID, Controller: ptr.To(true),
		}},
	}, Status: corev1.PodStatus{
		StartTime:  &startTime,
		Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
	}}
	if err := apiClient.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(node), &guardedNode); err != nil {
		t.Fatal(err)
	}
	guardedNode.Annotations[managedprotocol.AnnotationWorkerObservedRevision] = newRevision
	if err := apiClient.Update(ctx, &guardedNode); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(node), &guardedNode); err != nil {
		t.Fatal(err)
	}
	guardedNode.Status.Conditions = append(guardedNode.Status.Conditions, corev1.NodeCondition{
		Type:   corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition),
		Status: corev1.ConditionTrue, Reason: managedprotocol.ManagedWorkerReadyReason,
		LastHeartbeatTime: metav1.NewTime(time.Now().UTC()),
	})
	if err := apiClient.Status().Update(ctx, &guardedNode); err != nil {
		t.Fatal(err)
	}
	result, err = r.Reconcile(ctx, request)
	if err != nil || result.RequeueAfter != topologyRequeueInterval {
		t.Fatalf("replacement proof Reconcile = (%+v, %v), want managed maintenance fence", result, err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.WorkerRevision == nil || current.Status.WorkerRevision.PodUID != string(pod.UID) ||
		current.Status.WorkerRevision.DesiredRevision != newRevision ||
		current.Status.WorkerRevision.ObservedRevision != newRevision {
		t.Fatalf("replacement worker proof = %#v", current.Status.WorkerRevision)
	}
	assertOldRequestRetained()
}

func TestManagedWorkerLeasesArePrecreatedAndExactlyBound(t *testing.T) {
	device := newDevice("switch-leases", "edge")
	device.UID = "device-uid"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, node).Build()
	r := &CiscoDeviceReconciler{Client: leaseUIDAssigningClient{Client: base}, APIReader: base, Scheme: scheme, LeaseNamespace: "shared-leases"}
	ctx := context.Background()
	if err := r.ensureManagedMutationLease(ctx, device, node); err != nil {
		t.Fatalf("ensure mutation Lease: %v", err)
	}
	if err := r.ensureManagedNodeHeartbeatLease(ctx, device, node); err != nil {
		t.Fatalf("ensure heartbeat Lease: %v", err)
	}
	if err := r.ensureManagedConfigLease(ctx, device, node, "interface_ethernet"); err != nil {
		t.Fatalf("ensure config Lease: %v", err)
	}

	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	checks := []struct {
		key     types.NamespacedName
		family  string
		purpose string
		owners  int
	}{
		{types.NamespacedName{Namespace: "shared-leases", Name: configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily)}, devicecoordination.MutationLeaseFamily, managedprotocol.LeasePurposeDeviceMutation, 0},
		{types.NamespacedName{Namespace: corev1.NamespaceNodeLease, Name: node.Name}, managedNodeHeartbeatFamily, managedprotocol.LeasePurposeNodeHeartbeat, 1},
		{types.NamespacedName{Namespace: "shared-leases", Name: configengine.LeaseName(deviceKey, "interface_ethernet")}, "interface_ethernet", managedprotocol.LeasePurposeConfigFamily, 0},
	}
	for _, check := range checks {
		var lease coordv1.Lease
		if err := base.Get(ctx, check.key, &lease); err != nil {
			t.Fatalf("get %s Lease: %v", check.purpose, err)
		}
		if lease.UID == "" || lease.Annotations[managedprotocol.AnnotationLeasePurpose] != check.purpose ||
			lease.Labels["cisco.vk/device"] != deviceKey || lease.Labels["cisco.vk/family"] != check.family ||
			lease.Annotations[managedprotocol.AnnotationWorkerUsername] != node.Annotations[managedprotocol.AnnotationWorkerUsername] ||
			len(lease.OwnerReferences) != check.owners {
			t.Fatalf("%s Lease is not exactly bound: %#v", check.purpose, lease.ObjectMeta)
		}
	}
}

func TestManagedHeartbeatAdoptsEmptyLegacyLeaseWithCanonicalIdentity(t *testing.T) {
	device := newDevice("switch-empty-heartbeat", "edge")
	device.UID = "device-uid"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	legacy := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: corev1.NamespaceNodeLease, Name: node.Name, UID: "legacy-heartbeat-uid",
	}}
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, node, legacy).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme}
	if err := r.ensureManagedNodeHeartbeatLease(context.Background(), device, node); err != nil {
		t.Fatalf("adopt empty heartbeat: %v", err)
	}
	var adopted coordv1.Lease
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(legacy), &adopted); err != nil {
		t.Fatal(err)
	}
	if adopted.Spec.HolderIdentity == nil || *adopted.Spec.HolderIdentity != node.Name ||
		adopted.Spec.LeaseDurationSeconds == nil || *adopted.Spec.LeaseDurationSeconds != managedNodeLeaseDuration ||
		adopted.Spec.RenewTime != nil || adopted.Spec.AcquireTime != nil || adopted.Spec.LeaseTransitions != nil {
		t.Fatalf("adopted heartbeat spec=%#v, want canonical holder/duration without a manager-authored heartbeat", adopted.Spec)
	}
	if len(adopted.OwnerReferences) != 1 || adopted.OwnerReferences[0].UID != node.UID ||
		adopted.Annotations[managedprotocol.AnnotationLeasePurpose] != managedprotocol.LeasePurposeNodeHeartbeat {
		t.Fatalf("adopted heartbeat identity=%#v", adopted.ObjectMeta)
	}
}

func TestManagedConfigLeaseRefusesForeignCanonicalCollision(t *testing.T) {
	device := newDevice("switch-lease-collision", "edge")
	device.UID = "device-uid"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	foreign := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: configengine.LeaseName(deviceKey, "vlan"), UID: "foreign-uid",
		Labels: map[string]string{"cisco.vk/device": "other-device", "cisco.vk/family": "vlan"},
	}}
	r := reconcilerFor(t, device, node, foreign)
	if err := r.ensureManagedConfigLease(context.Background(), device, node, "vlan"); err == nil ||
		!strings.Contains(err.Error(), "already bound") {
		t.Fatalf("foreign canonical Lease collision error = %v", err)
	}
}

type leaseUIDAssigningClient struct {
	client.Client
}

func (c leaseUIDAssigningClient) Create(ctx context.Context, object client.Object, opts ...client.CreateOption) error {
	if lease, ok := object.(*coordv1.Lease); ok && lease.UID == "" {
		lease.UID = "server-assigned-lease-uid"
	}
	return c.Client.Create(ctx, object, opts...)
}

func TestManagedDeviceDeletionFencesEveryUnresolvedAuthority(t *testing.T) {
	tests := map[string]func(*ciskov1.CiscoDevice, *coordv1.Lease, *opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger){
		"idle": func(*ciskov1.CiscoDevice, *coordv1.Lease, *opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger) {
		},
		"active session": func(device *ciskov1.CiscoDevice, _ *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{Phase: ciskov1.DeviceMaintenanceSessionActive, SessionToken: "active-session-token"}
		},
		"legacy handoff preparing": func(device *ciskov1.CiscoDevice, _ *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			device.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{Phase: ciskov1.DeviceLegacyHandoffPreparing}
		},
		"legacy writer pending": func(device *ciskov1.CiscoDevice, _ *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			device.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{Phase: ciskov1.DeviceLegacyHandoffLegacyWriterPending}
		},
		"held lease": func(_ *ciskov1.CiscoDevice, lease *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			lease.Spec.HolderIdentity = ptr.To("software-upgrade/edge/upgrade/leaf-uid")
		},
		"orphan request": func(_ *ciskov1.CiscoDevice, lease *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = "orphan-request-token"
		},
		"unsettled leaf": func(_ *ciskov1.CiscoDevice, _ *coordv1.Lease, leaf *opsv1alpha1.IOSXESoftwareUpgrade, _ *topologyrollout.Ledger) {
			leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionGranted
		},
		"ledger reservation": func(_ *ciskov1.CiscoDevice, _ *coordv1.Lease, _ *opsv1alpha1.IOSXESoftwareUpgrade, ledger *topologyrollout.Ledger) {
			ledger.Reservations["reservation-1"] = validDeletionReservation()
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r, device := managedDeletionFixture(t, mutate)
			err := r.ensureManagedDeviceDeletionSafe(context.Background(), device)
			if name == "idle" && err != nil {
				t.Fatalf("idle deletion blocked: %v", err)
			}
			if name != "idle" && err == nil {
				t.Fatal("unresolved managed authority did not block deletion")
			}
		})
	}
}

func TestManagedDeletionMissingMutationLeaseRequiresRevokedWorkerAccess(t *testing.T) {
	for _, bindingKind := range []string{"none", "RoleBinding", "ClusterRoleBinding"} {
		t.Run(bindingKind, func(t *testing.T) {
			r, device := managedDeletionFixture(t, func(*ciskov1.CiscoDevice, *coordv1.Lease, *opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger) {
			})
			ctx := context.Background()
			deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
			var lease coordv1.Lease
			leaseKey := types.NamespacedName{
				Namespace: device.Namespace,
				Name:      configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily),
			}
			if err := r.Get(ctx, leaseKey, &lease); err != nil {
				t.Fatal(err)
			}
			if err := r.Delete(ctx, &lease); err != nil {
				t.Fatal(err)
			}
			saName := managedWorkerServiceAccountName(device)
			switch bindingKind {
			case "RoleBinding":
				if err := r.Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
					Namespace: device.Namespace, Name: saName,
				}}); err != nil {
					t.Fatal(err)
				}
			case "ClusterRoleBinding":
				if err := r.Create(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{
					Name: vkAccessClusterRoleBindingName(device.Namespace, saName),
				}}); err != nil {
					t.Fatal(err)
				}
			}

			err := r.ensureManagedDeviceDeletionSafe(ctx, device)
			if bindingKind == "none" && err != nil {
				t.Fatalf("idempotent retry after access revocation was blocked: %v", err)
			}
			if bindingKind != "none" && (err == nil || !strings.Contains(err.Error(), "requires its canonical mutation Lease")) {
				t.Fatalf("missing Lease with live %s was not blocked: %v", bindingKind, err)
			}
		})
	}
}

func TestManagedLeaseCleanupValidatesCompleteSetBeforeDeleting(t *testing.T) {
	device := newDevice("switch-cleanup-leases", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid",
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, node).Build()
	r := &CiscoDeviceReconciler{Client: leaseUIDAssigningClient{Client: base}, APIReader: base, Scheme: scheme, LeaseNamespace: device.Namespace}
	ctx := context.Background()
	for _, family := range []string{"vlan", "interface_ethernet"} {
		if err := r.ensureManagedConfigLease(ctx, device, node, family); err != nil {
			t.Fatalf("ensure %s Lease: %v", family, err)
		}
	}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	badKey := types.NamespacedName{Namespace: device.Namespace, Name: configengine.LeaseName(deviceKey, "interface_ethernet")}
	var malformed coordv1.Lease
	if err := base.Get(ctx, badKey, &malformed); err != nil {
		t.Fatal(err)
	}
	malformed.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Secret", Name: "foreign", UID: "foreign-uid"}}
	if err := base.Update(ctx, &malformed); err != nil {
		t.Fatal(err)
	}

	if err := r.cleanupManagedWorkerLeases(ctx, device); err == nil {
		t.Fatal("malformed managed Lease did not block cleanup")
	}
	for _, family := range []string{"vlan", "interface_ethernet"} {
		key := types.NamespacedName{Namespace: device.Namespace, Name: configengine.LeaseName(deviceKey, family)}
		if err := base.Get(ctx, key, &coordv1.Lease{}); err != nil {
			t.Fatalf("Lease %s was deleted before complete-set validation: %v", family, err)
		}
	}
}

func TestManagedLeaseCleanupRetriesAfterBoundNodeDeletion(t *testing.T) {
	device := newDevice("switch-cleanup-node-gone", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid",
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
		},
	}}
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, node).Build()
	r := &CiscoDeviceReconciler{Client: leaseUIDAssigningClient{Client: base}, APIReader: base, Scheme: scheme, LeaseNamespace: device.Namespace}
	ctx := context.Background()
	if err := r.ensureManagedConfigLease(ctx, device, node, "vlan"); err != nil {
		t.Fatal(err)
	}
	if err := base.Delete(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := r.cleanupManagedWorkerLeases(ctx, device); err != nil {
		t.Fatalf("cleanup after bound Node deletion: %v", err)
	}
	key := types.NamespacedName{
		Namespace: device.Namespace,
		Name:      configengine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), "vlan"),
	}
	if err := base.Get(ctx, key, &coordv1.Lease{}); !apierrors.IsNotFound(err) {
		t.Fatalf("managed Lease remains after retry cleanup: %v", err)
	}
}

func managedDeletionFixture(
	t *testing.T,
	mutate func(*ciskov1.CiscoDevice, *coordv1.Lease, *opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger),
) (*CiscoDeviceReconciler, *ciskov1.CiscoDevice) {
	t.Helper()
	device := newDevice("switch-delete", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid"}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace,
		Name:      configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily),
		UID:       "lease-uid",
		Labels: map[string]string{
			"cisco.vk/device": deviceKey,
			"cisco.vk/family": devicecoordination.MutationLeaseFamily,
		},
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationNodeName:        device.Name,
			managedprotocol.AnnotationNodeUID:         "node-uid",
			managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device),
			managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
			managedprotocol.AnnotationLeasePurpose:    managedprotocol.LeasePurposeDeviceMutation,
			devicecoordination.RetainLeaseAnnotation:  "true",
		},
	}}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "settled-upgrade", UID: "leaf-uid",
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:       "true",
			managedprotocol.AnnotationDeviceUID:     string(device.UID),
			managedprotocol.AnnotationReservationID: "settled-reservation",
		},
	}}
	leaf.Spec.DeviceRef.Name = device.Name
	leaf.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
		State: opsv1alpha1.UpgradeManagerAdmissionSettled, DeviceUID: string(device.UID),
	}
	policy, ledgerCM := managedPolicyAndLedger(t, nil)
	ledger, err := topologyrollout.Decode([]byte(ledgerCM.Data[topologyrollout.LedgerDataKey]), string(ledgerCM.UID))
	if err != nil {
		t.Fatal(err)
	}
	mutate(device, lease, leaf, ledger)
	encoded, err := topologyrollout.Encode(ledger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	ledgerCM.Data[topologyrollout.LedgerDataKey] = string(encoded)

	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(device, lease, leaf, policy, ledgerCM).Build()
	r := &CiscoDeviceReconciler{
		Client: apiClient, APIReader: apiClient, Scheme: scheme,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		LeaseNamespace: device.Namespace,
	}
	return r, device
}

func managedPolicyAndLedger(t *testing.T, reservations map[string]topologyrollout.Reservation) (*corev1.ConfigMap, *corev1.ConfigMap) {
	t.Helper()
	const (
		namespace  = "cvk-system"
		ledgerName = "cvk-rollout-ledger"
		ledgerUID  = "ledger-uid"
	)
	cfg := topologyrollout.AdminPolicyConfig{
		Version:                      topologyrollout.PolicyVersion,
		FleetSelector:                metav1.LabelSelector{MatchLabels: map[string]string{managedprotocol.AnnotationManaged: "true"}},
		RequiredTopologyKeys:         []string{topology.CiscoTopologyLabelPrefix + "site"},
		ProjectedTopologyKeys:        []string{topology.CiscoTopologyLabelPrefix + "site"},
		GlobalMaxConcurrentTransfers: 1,
		GlobalMaxUnavailable:         1,
		DomainMaxConcurrentTransfers: map[string]int{topology.CiscoTopologyLabelPrefix + "site": 1},
		DomainMaxUnavailable:         map[string]int{topology.CiscoTopologyLabelPrefix + "site": 1},
		HealthFreshnessSeconds:       120,
		MaxCampaignTargets:           10,
		MaxActiveReservations:        32,
		MaxLedgerBytes:               64 * 1024,
		LedgerName:                   ledgerName,
	}
	data, err := topologyrollout.CanonicalPolicyJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: "topology-policy", UID: "policy-uid", ResourceVersion: "1",
		Annotations: map[string]string{
			topologyrollout.PolicyManagedAnnotation:   "true",
			topologyrollout.LedgerUIDAnnotation:       ledgerUID,
			topologyrollout.AdmissionPrefixAnnotation: "cvk-topology",
		},
	}, Data: map[string]string{topologyrollout.PolicyDataKey: data}}
	ledger, err := topologyrollout.NewLedger(ledgerUID)
	if err != nil {
		t.Fatal(err)
	}
	for id, reservation := range reservations {
		ledger.Reservations[id] = reservation
	}
	encoded, err := topologyrollout.Encode(ledger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	ledgerCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: ledgerName, UID: ledgerUID, ResourceVersion: "1",
	}, Data: map[string]string{topologyrollout.LedgerDataKey: string(encoded)}}
	return policy, ledgerCM
}

func validDeletionReservation() topologyrollout.Reservation {
	return topologyrollout.Reservation{
		ReservationRequest: topologyrollout.ReservationRequest{
			ID: "reservation-1", CampaignUID: "campaign-uid", PlanHash: "sha256:" + strings.Repeat("a", 64),
			PolicyUID: "policy-uid", PolicyVersion: "1", PolicyEpoch: 1, TopologyLockID: strings.Repeat("1", 32), PhysicalID: "serial-1", DeviceUID: "device-uid",
			NodeUID: "node-uid", ChildNamespace: "edge", ChildName: "upgrade", Domains: map[string]string{topology.CiscoTopologyLabelPrefix + "site": "site-a"},
		},
		State: topologyrollout.ReservationReserved,
	}
}
