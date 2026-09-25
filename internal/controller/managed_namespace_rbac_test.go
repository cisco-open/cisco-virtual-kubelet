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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func namespacedRBACBinding(namespace, name, kind, role string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: kind, Name: role},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.GroupKind, Name: "device-operators", APIGroup: rbacv1.GroupName}},
	}
}

func TestManagedNamespaceRBACRejectsBuiltInEditStyleClusterRole(t *testing.T) {
	device := managedAccessDevice("switch-edit")
	edit := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "edit"}, Rules: []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{""}, Resources: []string{"serviceaccounts"}, Verbs: []string{"impersonate"}},
		{APIGroups: []string{""}, Resources: []string{"pods/exec", "pods/attach", "pods/portforward"}, Verbs: []string{"get", "create"}},
		{APIGroups: []string{"apps"}, Resources: []string{"deployments/scale", "replicasets/scale"}, Verbs: []string{"get", "update", "patch"}},
	}}
	binding := namespacedRBACBinding(device.Namespace, "namespace-editors", "ClusterRole", edit.Name)
	r := reconcilerFor(t, device, edit, binding)
	r.ManagedTopology = true

	err := r.ensureManagedSharedWorkerAccess(context.Background(), device)
	if err == nil || !strings.Contains(err.Error(), "unsafe RoleBinding edge/namespace-editors") ||
		!strings.Contains(err.Error(), "read access to Secrets") {
		t.Fatalf("edit-style RBAC error = %v", err)
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
}

func TestManagedNamespaceRBACAcceptsSafeCustomRole(t *testing.T) {
	device := managedAccessDevice("switch-observer")
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "device-observer"}, Rules: []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"events", "resourcequotas", "services"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"coordination.k8s.io"}, Resources: []string{"leases"}, Verbs: []string{"get"}},
		// A RoleBinding cannot grant this cluster-scoped resource; it is not a
		// namespaced network-configuration disclosure.
		{APIGroups: []string{"config.cisco.vk"}, Resources: []string{"iosxeconfigdefaults"}, Verbs: []string{"get"}},
	}}
	binding := namespacedRBACBinding(device.Namespace, "device-observers", "Role", role.Name)
	r := reconcilerFor(t, device, role, binding)
	r.ManagedTopology = true

	if err := r.ensureManagedSharedWorkerAccess(context.Background(), device); err != nil {
		t.Fatalf("safe custom Role rejected: %v", err)
	}
	assertClusterBindingRole(t, r,
		vkAccessClusterRoleBindingName(device.Namespace, r.appHostingServiceAccountName()),
		managedprotocol.AppHostingReadWriteClusterRole)
}

func TestManagedNamespaceRBACRejectsSensitiveNamespaceReads(t *testing.T) {
	tests := []struct {
		name string
		rule rbacv1.PolicyRule
		want string
	}{
		{
			name: "secrets",
			rule: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}},
			want: "read access to Secrets",
		},
		{
			name: "device specs",
			rule: rbacv1.PolicyRule{APIGroups: []string{"cisco.vk"}, Resources: []string{"ciscodevices/status"}, Verbs: []string{"get"}},
			want: "read access to CiscoDevice specs",
		},
		{
			name: "network configuration",
			rule: rbacv1.PolicyRule{APIGroups: []string{"config.cisco.vk"}, Resources: []string{"iosxeconfigs"}, Verbs: []string{"watch"}},
			want: "read access to network configuration objects",
		},
		{
			name: "inline credential workload",
			rule: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
			want: "inline device password",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := managedAccessDevice("switch-sensitive")
			role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "sensitive-reader"}, Rules: []rbacv1.PolicyRule{tt.rule}}
			binding := namespacedRBACBinding(device.Namespace, "sensitive-reader", "Role", role.Name)
			r := reconcilerFor(t, device, role, binding)
			r.ManagedTopology = true

			err := r.ensureManagedSharedWorkerAccess(context.Background(), device)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("sensitive read error = %v, want %q", err, tt.want)
			}
			assertNoSharedWorkerAuthority(t, r, device.Namespace)
		})
	}
}

func TestManagedNamespaceRBACAllowsWorkloadObservationWithSecretCredentials(t *testing.T) {
	device := managedAccessDevice("switch-secret-backed")
	device.Spec.Password = ""
	device.Spec.CredentialSecretRef = &corev1.LocalObjectReference{Name: "device-credentials"}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "workload-observer"}, Rules: []rbacv1.PolicyRule{{
		APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "watch"},
	}}}
	binding := namespacedRBACBinding(device.Namespace, "workload-observer", "Role", role.Name)
	r := reconcilerFor(t, device, role, binding)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(context.Background(), device); err != nil {
		t.Fatalf("secret-backed workload observer rejected: %v", err)
	}
}

func TestManagedNamespaceRBACRejectsWildcardAndReservedImpersonation(t *testing.T) {
	tests := []struct {
		name string
		rule rbacv1.PolicyRule
		want string
	}{
		{
			name: "wildcard",
			rule: rbacv1.PolicyRule{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}},
			want: "read access to Secrets",
		},
		{
			name: "reserved service account impersonation",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{""}, Resources: []string{"serviceaccounts"}, Verbs: []string{"impersonate"},
				ResourceNames: []string{managedprotocol.NetworkManagementServiceAccount},
			},
			want: "impersonation of reserved ServiceAccount",
		},
		{
			name: "worker scale with resource name",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{"apps"}, Resources: []string{"deployments/scale"}, Verbs: []string{"patch"},
				ResourceNames: []string{"switch-risk-vk"},
			},
			want: "managed worker deployments/scale",
		},
		{
			name: "worker eviction",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{""}, Resources: []string{"pods/eviction"}, Verbs: []string{"create"},
			},
			want: "eviction of managed worker Pods",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := managedAccessDevice("switch-risk")
			role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "risky"}, Rules: []rbacv1.PolicyRule{tt.rule}}
			binding := namespacedRBACBinding(device.Namespace, "risky", "Role", role.Name)
			r := reconcilerFor(t, device, role, binding)
			r.ManagedTopology = true

			err := r.ensureManagedSharedWorkerAccess(context.Background(), device)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("unsafe rule error = %v, want %q", err, tt.want)
			}
			assertNoSharedWorkerAuthority(t, r, device.Namespace)
		})
	}
}

func TestManagedNamespaceRBACRiskQuarantinesExistingSharedBindings(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-quarantine")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed shared access: %v", err)
	}
	for _, worker := range []struct {
		name    string
		labels  map[string]string
		account string
	}{
		{device.Name + deploymentSuffix, perDeviceDeploymentLabels(device.Name), r.appHostingServiceAccountName()},
		{networkDeploymentName(device.Name, string(device.UID)), perDeviceNetworkDeploymentLabels(device.Name), r.networkManagementServiceAccountName()},
	} {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: device.Namespace, Name: worker.name, UID: types.UID(worker.name + "-uid"),
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice")),
				},
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: worker.labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: worker.labels},
					Spec:       corev1.PodSpec{ServiceAccountName: worker.account},
				},
			},
		}
		if err := r.Create(ctx, deployment); err != nil {
			t.Fatal(err)
		}
	}

	riskyRole := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "worker-shell"}, Rules: []rbacv1.PolicyRule{{
		APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"},
	}}}
	riskyBinding := namespacedRBACBinding(device.Namespace, "worker-shell", "Role", riskyRole.Name)
	if err := r.Create(ctx, riskyRole); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, riskyBinding); err != nil {
		t.Fatal(err)
	}

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "worker processes are draining") {
		t.Fatalf("quarantine error = %v", err)
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
	for _, name := range []string{device.Name + deploymentSuffix, networkDeploymentName(device.Name, string(device.UID))} {
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: name}, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
			t.Fatalf("quarantine did not foreground-delete worker Deployment %s: %v", name, err)
		}
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err == nil ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("settled quarantine error = %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(riskyBinding), &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("audit deleted operator-owned unsafe RoleBinding: %v", err)
	}
	for _, account := range []string{r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()} {
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: account}, &corev1.ServiceAccount{}); err != nil {
			t.Fatalf("quarantine should retain reserved ServiceAccount %s for diagnosis/recovery: %v", account, err)
		}
	}
}

func TestManagedNamespaceRBACQuarantinesBroadenedTrustedRole(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-role-drift")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed shared access: %v", err)
	}
	var role rbacv1.ClusterRole
	if err := r.Get(ctx, types.NamespacedName{Name: managedprotocol.NetworkManagementReadWriteClusterRole}, &role); err != nil {
		t.Fatal(err)
	}
	role.Rules = append(role.Rules, rbacv1.PolicyRule{
		APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"},
	})
	if err := r.Update(ctx, &role); err != nil {
		t.Fatal(err)
	}

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "invalid fixed role") ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("broadened trusted role error = %v", err)
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
}

func TestManagedNamespaceRBACEventMappings(t *testing.T) {
	edgeA := managedAccessDevice("switch-a")
	edgeB := managedAccessDevice("switch-b")
	other := managedAccessDevice("switch-other")
	other.Namespace = "other"
	matching := namespacedRBACBinding("edge", "editors", "ClusterRole", "edit")
	unrelated := namespacedRBACBinding("other", "viewers", "ClusterRole", "view")
	sharedBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-addon"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "shared-addon"},
		Subjects:   sharedWorkerSubject("edge", managedprotocol.AppHostingServiceAccount),
	}
	r := reconcilerFor(t, edgeA, edgeB, other, matching, unrelated, sharedBinding)
	r.ManagedTopology = true

	wantEdge := []ctrl.Request{
		{NamespacedName: types.NamespacedName{Namespace: "edge", Name: "switch-a"}},
		{NamespacedName: types.NamespacedName{Namespace: "edge", Name: "switch-b"}},
	}
	for _, object := range []client.Object{
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "local-role"}},
		matching,
	} {
		if got := r.mapNamespacedRBACToCiscoDevices(context.Background(), object); !requestsEqual(got, wantEdge) {
			t.Fatalf("namespaced mapping for %T = %#v, want %#v", object, got, wantEdge)
		}
	}
	if got := r.mapClusterRoleToCiscoDevices(context.Background(), &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "edit"}}); !requestsEqual(got, wantEdge) {
		t.Fatalf("ClusterRole mapping = %#v, want %#v", got, wantEdge)
	}
	if got := r.mapClusterRoleToCiscoDevices(context.Background(), &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "shared-addon"}}); !requestsEqual(got, wantEdge) {
		t.Fatalf("shared ClusterRole mapping = %#v, want %#v", got, wantEdge)
	}
	if got := r.mapClusterRoleBindingToCiscoDevices(context.Background(), sharedBinding); !requestsEqual(got, wantEdge) {
		t.Fatalf("ClusterRoleBinding mapping = %#v, want %#v", got, wantEdge)
	}
	if got := r.mapClusterRoleToCiscoDevices(context.Background(), &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "unused"}}); len(got) != 0 {
		t.Fatalf("unused ClusterRole mapping = %#v, want none", got)
	}
}

func TestManagedSharedBindingAuditScopesRoleBindingsToDeviceAndLeaseNamespaces(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-binding-scope")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.LeaseNamespace = "cvk-leases"
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed shared access: %v", err)
	}

	// A RoleBinding can grant a foreign namespace's resources only. It is not
	// part of this managed namespace or its explicit coordination namespace.
	foreign := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: "unrelated", Name: "foreign-reader"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: managedprotocol.AppHostingDeviceReadClusterRole,
		},
		Subjects: sharedWorkerSubject(device.Namespace, r.appHostingServiceAccountName()),
	}
	if err := r.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("irrelevant namespace RoleBinding was scanned: %v", err)
	}

	leaseAdditive := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: r.LeaseNamespace, Name: "unexpected-lease-grant"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementLeaseReadWriteClusterRole,
		},
		Subjects: sharedWorkerSubject(device.Namespace, r.networkManagementServiceAccountName()),
	}
	if err := r.Create(ctx, leaseAdditive); err != nil {
		t.Fatal(err)
	}
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "unexpected additive RoleBinding") {
		t.Fatalf("lease-namespace additive binding error = %v", err)
	}
}

func requestsEqual(got, want []ctrl.Request) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func assertNoSharedWorkerAuthority(t *testing.T, r *CiscoDeviceReconciler, namespace string) {
	t.Helper()
	ctx := context.Background()
	var roleBindings rbacv1.RoleBindingList
	if err := r.List(ctx, &roleBindings); err != nil {
		t.Fatal(err)
	}
	var clusterRoleBindings rbacv1.ClusterRoleBindingList
	if err := r.List(ctx, &clusterRoleBindings); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()} {
		for i := range roleBindings.Items {
			if hasWorkerSubject(roleBindings.Items[i].Subjects, namespace, account) {
				t.Fatalf("shared account %s retains RoleBinding %s/%s", account,
					roleBindings.Items[i].Namespace, roleBindings.Items[i].Name)
			}
		}
		for i := range clusterRoleBindings.Items {
			if hasWorkerSubject(clusterRoleBindings.Items[i].Subjects, namespace, account) {
				t.Fatalf("shared account %s retains ClusterRoleBinding %s", account, clusterRoleBindings.Items[i].Name)
			}
		}
	}
	for _, account := range []string{r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()} {
		err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: account}, &corev1.ServiceAccount{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatal(err)
		}
	}
}
