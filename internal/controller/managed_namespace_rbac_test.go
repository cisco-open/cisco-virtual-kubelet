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
	"fmt"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func TestWorkerNameScopeIncludesTruncatedGeneratedPods(t *testing.T) {
	device := managedAccessDevice("switch-with-a-long-device-name-that-must-not-truncate-worker-uid")
	device.UID = "11111111-1111-4111-8111-111111111111"
	scope := newManagedWorkerNameScope(nil, device)
	for _, prefix := range []string{
		device.Name + deploymentSuffix + "-",
		fmt.Sprintf("n%d-%s-u%s%s-", len(device.Name), device.Name, device.UID, networkDeploymentSuffix),
		networkDeploymentName(string(device.UID)) + "-",
	} {
		if len(prefix) > 58 {
			prefix = prefix[:58]
		}
		if !scope.includes("pods/exec", prefix+"abcde") {
			t.Fatalf("missed exact generated worker Pod %q", prefix+"abcde")
		}
	}
	if scope.includes("pods/exec", "ordinary-workload-abcde") {
		t.Fatal("ordinary workload was classified as a reserved worker")
	}
}

func TestNetworkDeploymentNamePreservesFullUIDInGeneratedPod(t *testing.T) {
	uid := "11111111-1111-4111-8111-111111111111"
	deployment := networkDeploymentName(uid)
	prefix := deployment + "-1234567890-"
	if len(prefix) > 58 {
		prefix = prefix[:58]
	}
	if !strings.HasPrefix(prefix+"abcde", "u"+uid+"-network-") {
		t.Fatalf("generated Pod truncated the network credential: %q", prefix)
	}
	if deployment == networkDeploymentName("22222222-2222-4222-8222-222222222222") {
		t.Fatal("recreated device reused the old network name")
	}
}

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
			want: "RBAC bind or escalate authority",
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
			name: "deployment status with resource name",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{"apps"}, Resources: []string{"deployments/status"}, Verbs: []string{"patch"},
				ResourceNames: []string{"switch-risk-vk"},
			},
			want: "managed worker deployments/status",
		},
		{
			name: "replicaset status wildcard",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{"*"}, Resources: []string{"replicasets/status"}, Verbs: []string{"update"},
			},
			want: "managed worker replicasets/status",
		},
		{
			name: "worker eviction",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{""}, Resources: []string{"pods/eviction"}, Verbs: []string{"create"},
			},
			want: "eviction of managed worker Pods",
		},
		{
			name: "pod binding subresource",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{""}, Resources: []string{"pods/binding"}, Verbs: []string{"create"},
			},
			want: "binding of managed worker Pods through pods/binding",
		},
		{
			name: "legacy binding resource with managed name",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{""}, Resources: []string{"bindings"}, Verbs: []string{"create"},
				ResourceNames: []string{"switch-risk-vk-generated"},
			},
			want: "binding of managed worker Pods through bindings",
		},
		{
			name: "constrained service account impersonation",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{"authentication.k8s.io"}, Resources: []string{"serviceaccounts"},
				Verbs: []string{"impersonate:serviceaccount"},
			},
			want: "constrained impersonation of ServiceAccounts",
		},
		{
			name: "constrained service account impersonation-on",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{"apps"}, Resources: []string{"deployments"},
				Verbs: []string{"impersonate-on:serviceaccount:update"},
			},
			want: "constrained ServiceAccount impersonation action",
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

func TestManagedNamespaceRBACRejectsDelegationSpecialVerbs(t *testing.T) {
	tests := []struct {
		name string
		rule rbacv1.PolicyRule
	}{
		{
			name: "bind named ClusterRole",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{rbacv1.GroupName}, Resources: []string{"clusterroles"},
				Verbs: []string{"bind"}, ResourceNames: []string{"edit"},
			},
		},
		{
			name: "escalate any Role",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{rbacv1.GroupName}, Resources: []string{"roles"}, Verbs: []string{"escalate"},
			},
		},
		{
			name: "wildcard resource and verb",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{rbacv1.GroupName}, Resources: []string{"*"}, Verbs: []string{"*"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			device := managedAccessDevice("switch-delegation")
			role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
				Namespace: device.Namespace, Name: "delegation-authority",
			}, Rules: []rbacv1.PolicyRule{test.rule}}
			binding := namespacedRBACBinding(device.Namespace, "delegation-authority", "Role", role.Name)
			r := reconcilerFor(t, device, role, binding)
			r.ManagedTopology = true

			err := r.ensureManagedSharedWorkerAccess(context.Background(), device)
			if err == nil || !strings.Contains(err.Error(), "RBAC bind or escalate authority") {
				t.Fatalf("delegation special-verb error=%v", err)
			}
			assertNoSharedWorkerAuthority(t, r, device.Namespace)
		})
	}
}

func TestManagedNamespaceRBACAuditUsesAPIReader(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-api-reader")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	riskyRole := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "just-created-worker-shell",
	}, Rules: []rbacv1.PolicyRule{{
		APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"},
	}}}
	riskyBinding := namespacedRBACBinding(device.Namespace, "just-created-worker-shell", "Role", riskyRole.Name)
	r.APIReader = fake.NewClientBuilder().WithScheme(r.Scheme).
		WithObjects(device.DeepCopy(), riskyRole, riskyBinding).Build()

	// The cached writer deliberately has neither just-created RBAC object. The
	// security audit must nevertheless observe both through the direct reader.
	if err := r.Get(ctx, client.ObjectKeyFromObject(riskyBinding), &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("cached writer unexpectedly contains risk fixture: %v", err)
	}
	err := r.inspectManagedWorkerNamespaceRBAC(ctx, device,
		r.appHostingServiceAccountName(), r.networkManagementServiceAccountName())
	if err == nil || !strings.Contains(err.Error(), "worker pods/exec access") {
		t.Fatalf("APIReader namespace audit error=%v", err)
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
		{networkDeploymentName(string(device.UID)), perDeviceNetworkDeploymentLabels(device.Name), r.networkManagementServiceAccountName()},
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
	if err == nil || !strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("quarantine error = %v", err)
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
	for _, name := range []string{device.Name + deploymentSuffix, networkDeploymentName(string(device.UID))} {
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
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: account}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
			t.Fatalf("quarantine did not rotate reserved ServiceAccount %s: %v", account, err)
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
	leaseRole := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: "cvk-leases", Name: "lease-local"}}
	leaseRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: "cvk-leases", Name: "lease-local"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: leaseRole.Name},
		Subjects:   sharedWorkerSubject("edge", managedprotocol.NetworkManagementServiceAccount),
	}
	leaseClusterBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: "cvk-leases", Name: "lease-shared-addon"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "lease-shared-addon"},
		Subjects:   sharedWorkerSubject("edge", managedprotocol.NetworkManagementServiceAccount),
	}
	sharedBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-addon"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "shared-addon"},
		Subjects:   sharedWorkerSubject("edge", managedprotocol.AppHostingServiceAccount),
	}
	r := reconcilerFor(t, edgeA, edgeB, other, matching, unrelated, leaseRole, leaseRoleBinding, leaseClusterBinding, sharedBinding)
	r.ManagedTopology = true

	wantEdge := []ctrl.Request{
		{NamespacedName: types.NamespacedName{Namespace: "edge", Name: "switch-a"}},
		{NamespacedName: types.NamespacedName{Namespace: "edge", Name: "switch-b"}},
	}
	for _, object := range []client.Object{
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "local-role"}},
		matching,
		leaseRole,
		leaseRoleBinding,
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
	if got := r.mapClusterRoleToCiscoDevices(context.Background(), &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "lease-shared-addon"}}); !requestsEqual(got, wantEdge) {
		t.Fatalf("lease-namespace ClusterRole mapping = %#v, want %#v", got, wantEdge)
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
	if err := r.Delete(ctx, foreign); err != nil {
		t.Fatal(err)
	}

	networkAccount := r.networkManagementServiceAccountName()
	workload := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "lease-scope-network-worker", UID: "lease-scope-network-worker-uid",
	}, Spec: corev1.PodSpec{ServiceAccountName: networkAccount}}
	if err := r.Create(ctx, workload); err != nil {
		t.Fatal(err)
	}

	leaseAdditive := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: r.LeaseNamespace, Name: "unexpected-lease-grant"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementLeaseReadWriteClusterRole,
		},
		Subjects: sharedWorkerSubject(device.Namespace, networkAccount),
	}
	if err := r.Create(ctx, leaseAdditive); err != nil {
		t.Fatal(err)
	}
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "unexpected RoleBinding") ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("lease-namespace additive binding error = %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(leaseAdditive), &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("lease-namespace additive binding survived quarantine: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(workload), &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("network workload survived lease-namespace quarantine: %v", err)
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
	for _, account := range []string{r.appHostingServiceAccountName(), networkAccount} {
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: account}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
			t.Fatalf("shared ServiceAccount %s survived lease-namespace quarantine: %v", account, err)
		}
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
