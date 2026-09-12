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
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestSharedWorkerRetirementZeroDeviceCluster(t *testing.T) {
	r := reconcilerFor(t,
		chartSharedRoleBinding("system", "test-sa"),
		chartSharedClusterRoleBinding("system", "test-sa"),
	)
	r.ManagedTopology = true
	ctx := context.Background()
	retired, err := r.retireSharedWorkerAccessIfSafe(ctx)
	if err != nil || retired {
		t.Fatalf("first retirement = %v, %v; want deletion and requeue", retired, err)
	}
	retired, err = r.retireSharedWorkerAccessIfSafe(ctx)
	if err != nil || !retired {
		t.Fatalf("second retirement = %v, %v; want complete", retired, err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: "system", Name: "test-sa-device"}, &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("chart RoleBinding remains: %v", err)
	}
	if err := r.Get(ctx, types.NamespacedName{Name: "test-sa"}, &rbacv1.ClusterRoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("chart ClusterRoleBinding remains: %v", err)
	}
}

func TestSharedWorkerRetirementWaitsForEveryWorkloadGeneration(t *testing.T) {
	device := newDevice("switch-migrate", "edge")
	device.UID = "device-uid"
	deployment := topologyTestDeployment(device, "deployment-uid", "test-sa")
	dynamicRB := sharedDynamicRoleBinding(device.Namespace, "test-sa")
	dynamicCRB := sharedDynamicClusterRoleBinding(device.Namespace, "test-sa")
	r := reconcilerFor(t, device, deployment, dynamicRB, dynamicCRB)
	r.ManagedTopology = true
	ctx := context.Background()

	if retired, err := r.retireSharedWorkerAccessIfSafe(ctx); err != nil || retired {
		t.Fatalf("live Deployment retirement=%v, %v; want pending", retired, err)
	}
	deployment.Spec.Template.Spec.ServiceAccountName = topologyLegacyWorkerServiceAccountName(device)
	if err := r.Update(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "switch-migrate-old", UID: "rs-uid",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment", Name: deployment.Name,
			UID: deployment.UID, Controller: ptr.To(true),
		}},
	}, Spec: appsv1.ReplicaSetSpec{Replicas: &zero, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: "test-sa"}}}}
	if err := r.Create(ctx, replicaSet); err != nil {
		t.Fatal(err)
	}
	if retired, err := r.retireSharedWorkerAccessIfSafe(ctx); err != nil || retired {
		t.Fatalf("drained ReplicaSet retirement=%v, %v; want delete and requeue", retired, err)
	}
	if err := r.Get(ctx, clientKey(replicaSet), &appsv1.ReplicaSet{}); !apierrors.IsNotFound(err) {
		t.Fatalf("drained ReplicaSet remains: %v", err)
	}
	if retired, err := r.retireSharedWorkerAccessIfSafe(ctx); err != nil || retired {
		t.Fatalf("binding retirement=%v, %v; want delete and requeue", retired, err)
	}
	if retired, err := r.retireSharedWorkerAccessIfSafe(ctx); err != nil || !retired {
		t.Fatalf("final retirement=%v, %v", retired, err)
	}
}

func TestSharedWorkerRetirementRejectsUnrecognizedAdditiveGrant(t *testing.T) {
	canonicalRB := sharedDynamicRoleBinding("edge", "test-sa")
	canonicalCRB := sharedDynamicClusterRoleBinding("edge", "test-sa")
	foreign := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "foreign-shared-grant", UID: "foreign-uid"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole},
		Subjects:   exactWorkerSubject("edge", "test-sa"),
	}
	r := reconcilerFor(t, canonicalRB, canonicalCRB, foreign)
	r.ManagedTopology = true
	retired, err := r.retireSharedWorkerAccessIfSafe(context.Background())
	if err == nil || retired || !strings.Contains(err.Error(), "unrecognized") {
		t.Fatalf("foreign retirement=%v, %v", retired, err)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Name: foreign.Name}, &rbacv1.ClusterRoleBinding{}); err != nil {
		t.Fatalf("foreign binding was removed: %v", err)
	}
	if err := r.Get(context.Background(), clientKey(canonicalRB), &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("canonical binding was removed before the full grant set was validated: %v", err)
	}
	if err := r.Get(context.Background(), clientKey(canonicalCRB), &rbacv1.ClusterRoleBinding{}); err != nil {
		t.Fatalf("canonical cluster binding was removed before the full grant set was validated: %v", err)
	}
}

func TestSharedWorkerRetirementIgnoresUnrelatedSameNamedObjects(t *testing.T) {
	const (
		legacyNamespace  = "system"
		foreignNamespace = "unrelated"
		sharedSA         = "test-sa"
	)
	one := int32(1)
	actualSA := chartSharedServiceAccount(legacyNamespace, sharedSA)
	foreignSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: foreignNamespace, Name: sharedSA, UID: "foreign-sa-uid",
	}}
	foreignDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: legacyNamespace, Name: "unrelated", UID: "foreign-deploy-uid"},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: sharedSA}}},
	}
	foreignReplicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: legacyNamespace, Name: "unrelated-rs", UID: "foreign-rs-uid"},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &one, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: sharedSA}}},
	}
	foreignPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: legacyNamespace, Name: "unrelated-pod", UID: "foreign-pod-uid"},
		Spec:       corev1.PodSpec{ServiceAccountName: sharedSA, Containers: []corev1.Container{{Name: "other", Image: "other:test"}}},
	}
	foreignRB := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: foreignNamespace, Name: sharedSA + "-device", UID: "foreign-rb-uid"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole},
		Subjects:   exactWorkerSubject(foreignNamespace, sharedSA),
	}
	foreignCRB := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "unrelated-shared-grant", UID: "foreign-crb-uid"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole},
		Subjects:   exactWorkerSubject(foreignNamespace, sharedSA),
	}
	r := reconcilerFor(t,
		actualSA, foreignSA, foreignDeployment, foreignReplicaSet, foreignPod, foreignRB, foreignCRB,
		chartSharedRoleBinding(legacyNamespace, sharedSA), chartSharedClusterRoleBinding(legacyNamespace, sharedSA),
	)
	r.ManagedTopology = true
	ctx := context.Background()

	if retired, err := r.retireSharedWorkerAccessIfSafe(ctx); err != nil || retired {
		t.Fatalf("first retirement=%v, %v; want only exact CVK bindings deleted", retired, err)
	}
	if retired, err := r.retireSharedWorkerAccessIfSafe(ctx); err != nil || !retired {
		t.Fatalf("second retirement=%v, %v; unrelated same-name objects must not block", retired, err)
	}
	for _, check := range []struct {
		key    types.NamespacedName
		object client.Object
	}{
		{clientKey(actualSA), &corev1.ServiceAccount{}},
		{clientKey(foreignSA), &corev1.ServiceAccount{}},
		{clientKey(foreignDeployment), &appsv1.Deployment{}},
		{clientKey(foreignReplicaSet), &appsv1.ReplicaSet{}},
		{clientKey(foreignPod), &corev1.Pod{}},
		{clientKey(foreignRB), &rbacv1.RoleBinding{}},
		{clientKey(foreignCRB), &rbacv1.ClusterRoleBinding{}},
	} {
		if err := r.Get(ctx, check.key, check.object); err != nil {
			t.Errorf("unrelated object %s was touched: %v", check.key, err)
		}
	}
}

func TestSharedWorkerRetirementFailsClosedForGrantToChartOwnedIdentity(t *testing.T) {
	const namespace = "system"
	foreign := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "foreign-shared-grant", UID: "foreign-rb-uid"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "view"},
		Subjects:   exactWorkerSubject(namespace, "test-sa"),
	}
	r := reconcilerFor(t, chartSharedServiceAccount(namespace, "test-sa"), foreign)
	r.ManagedTopology = true
	retired, err := r.retireSharedWorkerAccessIfSafe(context.Background())
	if err == nil || retired || !strings.Contains(err.Error(), "refusing automatic retirement") {
		t.Fatalf("chart-owned additive grant retirement=%v, %v", retired, err)
	}
	if err := r.Get(context.Background(), clientKey(foreign), &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("suspicious grant was removed: %v", err)
	}
}

func TestSharedWorkerRetirementFailsClosedForOrphanedCVKReplicaSet(t *testing.T) {
	const namespace = "system"
	zero := int32(0)
	orphan := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace, Name: "switch-old-vk-deadbeef", UID: "orphan-rs-uid",
			Labels: perDeviceDeploymentLabels("switch-old"),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment", Name: "switch-old-vk",
				UID: "missing-deployment-uid", Controller: ptr.To(true),
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &zero,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: "test-sa"}},
		},
	}
	chartRB := chartSharedRoleBinding(namespace, "test-sa")
	r := reconcilerFor(t, chartSharedServiceAccount(namespace, "test-sa"), chartRB,
		chartSharedClusterRoleBinding(namespace, "test-sa"), orphan)
	r.ManagedTopology = true
	retired, err := r.retireSharedWorkerAccessIfSafe(context.Background())
	if err == nil || retired || !strings.Contains(err.Error(), "no exact live CiscoDevice Deployment owner") {
		t.Fatalf("orphaned generation retirement=%v, %v", retired, err)
	}
	if err := r.Get(context.Background(), clientKey(orphan), &appsv1.ReplicaSet{}); err != nil {
		t.Fatalf("orphaned generation was removed without an exact owner: %v", err)
	}
	if err := r.Get(context.Background(), clientKey(chartRB), &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("legacy authority was removed before the orphaned generation settled: %v", err)
	}
}

func TestTopologyLegacyToManagedRetiresBroadIdentityAfterQuiescence(t *testing.T) {
	device := newDevice("switch-promote", "edge")
	device.UID = "device-uid"
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	ctx := context.Background()
	if err := r.ensureVKAccess(ctx, device, legacySA, false); err != nil {
		t.Fatal(err)
	}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	if err := r.Status().Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	deployment := topologyTestDeployment(device, "deployment-uid", legacySA)
	if err := r.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	managedSA := managedWorkerServiceAccountName(device)
	if retired, err := r.retirePriorTopologyWorkerAccessIfSafe(ctx, device, managedSA); err != nil || retired {
		t.Fatalf("live legacy Deployment retirement=%v, %v; want pending", retired, err)
	}
	deployment.Spec.Template.Spec.ServiceAccountName = managedSA
	if err := r.Update(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	if retired, err := r.retirePriorTopologyWorkerAccessIfSafe(ctx, device, managedSA); err != nil || !retired {
		t.Fatalf("quiesced legacy retirement=%v, %v", retired, err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: legacySA}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
		t.Fatalf("legacy ServiceAccount remains: %v", err)
	}
}

func topologyTestDeployment(device *ciskov1.CiscoDevice, uid types.UID, serviceAccount string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: device.Name + deploymentSuffix, UID: uid,
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: ciskov1.GroupVersion.String(), Kind: "CiscoDevice", Name: device.Name,
			UID: device.UID, Controller: ptr.To(true),
		}},
	}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: serviceAccount}}}}
}

func sharedDynamicRoleBinding(namespace, sharedSA string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: sharedSA, UID: types.UID("rb-" + namespace)},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole},
		Subjects: exactWorkerSubject(namespace, sharedSA)}
}

func sharedDynamicClusterRoleBinding(namespace, sharedSA string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: vkAccessClusterRoleBindingName(namespace, sharedSA), UID: types.UID("crb-" + namespace)},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole},
		Subjects: exactWorkerSubject(namespace, sharedSA)}
}

func chartSharedRoleBinding(namespace, sharedSA string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: sharedSA + "-device", UID: "chart-rb-uid",
		Annotations: map[string]string{sharedWorkerRetirementAnnotation: sharedWorkerRetirementVersion}},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole},
		Subjects: exactWorkerSubject(namespace, sharedSA)}
}

func chartSharedClusterRoleBinding(namespace, sharedSA string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: sharedSA, UID: "chart-crb-uid",
		Annotations: map[string]string{sharedWorkerRetirementAnnotation: sharedWorkerRetirementVersion}},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole},
		Subjects: exactWorkerSubject(namespace, sharedSA)}
}

func chartSharedServiceAccount(namespace, sharedSA string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: sharedSA, UID: types.UID("chart-sa-" + namespace),
		Labels: map[string]string{
			"helm.sh/chart":                "cisco-virtual-kubelet-1.2.3",
			"app.kubernetes.io/managed-by": "Helm",
		},
	}}
}

func clientKey(object metav1.Object) types.NamespacedName {
	return types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}
}
