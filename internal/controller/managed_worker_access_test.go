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
	stderrors "errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func managedAccessDevice(name string) *ciskov1.CiscoDevice {
	device := newDevice(name, "edge")
	device.UID = types.UID(name + "-uid")
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: name, NodeUID: name + "-node-uid",
	}
	return device
}

func managedAccessNode(device *ciskov1.CiscoDevice, unschedulable bool) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Status.NodeIdentity.NodeName,
		UID:  types.UID(device.Status.NodeIdentity.NodeUID),
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationNodeUID:         device.Status.NodeIdentity.NodeUID,
		},
	}, Spec: corev1.NodeSpec{Unschedulable: unschedulable}}
}

func convergeManagedSharedWorkerAccess(t *testing.T, r *CiscoDeviceReconciler, device *ciskov1.CiscoDevice) {
	t.Helper()
	for attempt := 0; attempt < 5; attempt++ {
		err := r.ensureManagedSharedWorkerAccess(context.Background(), device)
		if err == nil {
			return
		}
		var transition *sharedWorkerAccessTransition
		if !stderrors.As(err, &transition) || len(transition.blockers) == 0 {
			t.Fatalf("converge shared worker access: %v", err)
		}
	}
	t.Fatal("shared worker access did not converge")
}

func prepareManagedAccessSettlement(t *testing.T, r *CiscoDeviceReconciler) {
	t.Helper()
	if err := opsv1alpha1.AddToScheme(r.Scheme); err != nil {
		t.Fatal(err)
	}
	policy, ledger := managedPolicyAndLedger(t, nil)
	for _, object := range []client.Object{policy, ledger} {
		object.SetResourceVersion("")
		if err := r.Create(context.Background(), object); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	r.TopologyPolicyNamespace = policy.Namespace
	r.TopologyPolicyName = policy.Name
}

func TestManagedFunctionalAccountsAreSharedPerNamespace(t *testing.T) {
	ctx := context.Background()
	first := managedAccessDevice("switch-a")
	second := managedAccessDevice("switch-b")
	r := reconcilerFor(t, first, second)
	r.ManagedTopology = true
	for _, device := range []*ciskov1.CiscoDevice{first, second} {
		if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
			t.Fatalf("ensure access for %s: %v", device.Name, err)
		}
	}

	var accounts corev1.ServiceAccountList
	if err := r.List(ctx, &accounts); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		r.appHostingServiceAccountName():        false,
		r.networkManagementServiceAccountName(): false,
	}
	for i := range accounts.Items {
		if _, expected := want[accounts.Items[i].Name]; expected {
			if len(accounts.Items[i].OwnerReferences) != 0 {
				t.Fatalf("shared ServiceAccount %s unexpectedly has an owner", accounts.Items[i].Name)
			}
			want[accounts.Items[i].Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("shared ServiceAccount %s is missing", name)
		}
	}

	// Deleting one device cannot revoke namespace-shared authority used by the
	// other device.
	if err := r.cleanupManagedSharedWorkerAccess(ctx, first); err != nil {
		t.Fatal(err)
	}
	for name := range want {
		var account corev1.ServiceAccount
		if err := r.Get(ctx, types.NamespacedName{Namespace: first.Namespace, Name: name}, &account); err != nil {
			t.Fatalf("shared ServiceAccount %s was removed while another device exists: %v", name, err)
		}
	}
}

func TestManagedFunctionalAccountAccessModeTransitions(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-modes")
	node := managedAccessNode(device, false)
	r := reconcilerFor(t, device, node)
	r.ManagedTopology = true
	r.LeaseNamespace = "cvk-leases"
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatal(err)
	}
	prepareManagedAccessSettlement(t, r)
	leaseBindingKey := types.NamespacedName{
		Namespace: r.LeaseNamespace,
		Name:      networkLeaseRoleBindingName(device.Namespace, r.networkManagementServiceAccountName()),
	}
	assertRoleBindingRole(t, r, leaseBindingKey, managedprotocol.NetworkManagementLeaseReadWriteClusterRole)

	r.AppHostingAccessMode = managedprotocol.WorkerAccessReadOnly
	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessReadOnly
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	var transition *sharedWorkerAccessTransition
	if !stderrors.As(err, &transition) || !strings.Contains(err.Error(), "manager-owned Node cordons") {
		t.Fatalf("read-write to read-only cordon transition: %v", err)
	}
	var cordoned corev1.Node
	if err := r.Get(ctx, client.ObjectKeyFromObject(node), &cordoned); err != nil {
		t.Fatal(err)
	}
	if !cordoned.Spec.Unschedulable ||
		cordoned.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID] != string(device.UID) {
		t.Fatalf("managed Node was not UID-marked and cordoned before downgrade: %#v", cordoned)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("read-write to read-only transition after cordon: %v", err)
	}
	assertClusterBindingRole(t, r, vkAccessClusterRoleBindingName(device.Namespace, r.appHostingServiceAccountName()),
		managedprotocol.AppHostingReadOnlyClusterRole)
	assertRoleBindingRole(t, r, types.NamespacedName{Namespace: device.Namespace, Name: r.networkManagementServiceAccountName()},
		managedprotocol.NetworkManagementReadOnlyClusterRole)
	assertRoleBindingRole(t, r, leaseBindingKey, managedprotocol.NetworkManagementLeaseReadOnlyClusterRole)
	var appDeviceRead rbacv1.RoleBinding
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: r.appHostingServiceAccountName()}, &appDeviceRead); err == nil {
		t.Fatal("app-hosting device-read binding remains in read-only mode")
	}

	r.AppHostingAccessMode = managedprotocol.WorkerAccessDisabled
	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessDisabled
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("read-only to disabled transition: %v", err)
	}
	var roleBindings rbacv1.RoleBindingList
	if err := r.List(ctx, &roleBindings); err != nil {
		t.Fatal(err)
	}
	for i := range roleBindings.Items {
		for _, account := range []string{r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()} {
			if hasWorkerSubject(roleBindings.Items[i].Subjects, device.Namespace, account) {
				t.Fatalf("disabled account %s retains RoleBinding %s/%s", account,
					roleBindings.Items[i].Namespace, roleBindings.Items[i].Name)
			}
		}
	}
	var clusterRoleBindings rbacv1.ClusterRoleBindingList
	if err := r.List(ctx, &clusterRoleBindings); err != nil {
		t.Fatal(err)
	}
	for i := range clusterRoleBindings.Items {
		for _, account := range []string{r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()} {
			if hasWorkerSubject(clusterRoleBindings.Items[i].Subjects, device.Namespace, account) {
				t.Fatalf("disabled account %s retains ClusterRoleBinding %s", account, clusterRoleBindings.Items[i].Name)
			}
		}
	}
	if err := r.cleanupDisabledSharedWorkerAccounts(ctx, device,
		managedprotocol.WorkerAccessDisabled, managedprotocol.WorkerAccessDisabled); err != nil {
		t.Fatal(err)
	}

	r.AppHostingAccessMode = managedprotocol.WorkerAccessReadWrite
	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessReadWrite
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("disabled to read-write transition: %v", err)
	}
	assertClusterBindingRole(t, r, vkAccessClusterRoleBindingName(device.Namespace, r.appHostingServiceAccountName()),
		managedprotocol.AppHostingReadWriteClusterRole)
	assertRoleBindingRole(t, r, types.NamespacedName{Namespace: device.Namespace, Name: r.networkManagementServiceAccountName()},
		managedprotocol.NetworkManagementReadWriteClusterRole)
	assertRoleBindingRole(t, r, leaseBindingKey, managedprotocol.NetworkManagementLeaseReadWriteClusterRole)
}

func TestManagedFunctionalAccountRejectsReservedNameCollision(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-collision")
	reserved := &corev1.ServiceAccount{}
	reserved.Namespace = device.Namespace
	reserved.Name = managedprotocol.AppHostingServiceAccount
	r := reconcilerFor(t, device, reserved)
	r.ManagedTopology = true

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "without exact controller provenance") {
		t.Fatalf("reserved-name collision error = %v", err)
	}
	var current corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: reserved.Namespace, Name: reserved.Name}, &current); err != nil {
		t.Fatal(err)
	}
	if len(current.Annotations) != 0 || len(current.OwnerReferences) != 0 {
		t.Fatalf("foreign reserved ServiceAccount was adopted or modified: %#v", current.ObjectMeta)
	}
	var bindings rbacv1.ClusterRoleBindingList
	if err := r.List(ctx, &bindings); err != nil {
		t.Fatal(err)
	}
	if len(bindings.Items) != 0 {
		t.Fatalf("collision created ClusterRoleBindings: %#v", bindings.Items)
	}
}

func TestManagedFunctionalAccountRejectsLegacyTokenSecret(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-legacy-token")
	legacyToken := &corev1.Secret{}
	legacyToken.Namespace = device.Namespace
	legacyToken.Name = "legacy-app-worker-token"
	legacyToken.Type = corev1.SecretTypeServiceAccountToken
	legacyToken.Annotations = map[string]string{
		corev1.ServiceAccountNameKey: managedprotocol.AppHostingServiceAccount,
	}
	r := reconcilerFor(t, device, legacyToken)
	r.ManagedTopology = true

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "legacy ServiceAccount token Secret") {
		t.Fatalf("legacy token Secret error = %v", err)
	}
	var accounts corev1.ServiceAccountList
	if err := r.List(ctx, &accounts, client.InNamespace(device.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(accounts.Items) != 0 {
		t.Fatalf("legacy token audit still provisioned shared accounts: %#v", accounts.Items)
	}
}

func TestManagedFunctionalAccountRejectsBroadenedFixedRole(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-broadened-role")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	var role rbacv1.ClusterRole
	if err := r.Get(ctx, types.NamespacedName{Name: managedprotocol.NetworkManagementReadWriteClusterRole}, &role); err != nil {
		t.Fatal(err)
	}
	role.Rules = append(role.Rules, rbacv1.PolicyRule{
		APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"},
	})
	if err := r.Update(ctx, &role); err != nil {
		t.Fatal(err)
	}

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "compiled managed worker contract") {
		t.Fatalf("broadened fixed role error = %v", err)
	}
	var bindings rbacv1.RoleBindingList
	if err := r.List(ctx, &bindings); err != nil {
		t.Fatal(err)
	}
	if len(bindings.Items) != 0 {
		t.Fatalf("broadened fixed role was bound: %#v", bindings.Items)
	}
}

func TestManagedFunctionalAccountEscalationDrainsOldBoundTokens(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-escalation")
	r := reconcilerFor(t, device, managedAccessNode(device, false))
	r.ManagedTopology = true
	r.AppHostingAccessMode = managedprotocol.WorkerAccessReadOnly
	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessReadOnly
	convergeManagedSharedWorkerAccess(t, r, device)

	account := r.networkManagementServiceAccountName()
	labels := perDeviceNetworkDeploymentLabels(device.Name)
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: networkDeploymentName(device.Name, string(device.UID)), UID: "network-deployment-uid",
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice")),
		},
	}, Spec: appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{MatchLabels: labels},
		Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{ServiceAccountName: account}},
	}}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "network-rs", UID: "network-rs-uid",
		Labels: labels,
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment")),
		},
	}, Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{ServiceAccountName: account},
	}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "network-pod", UID: "network-pod-uid",
		Labels: labels,
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(replicaSet, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
		},
	}, Spec: corev1.PodSpec{ServiceAccountName: account}}
	for _, object := range []client.Object{deployment, replicaSet, pod} {
		if err := r.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}

	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessReadWrite
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	var transition *sharedWorkerAccessTransition
	if !stderrors.As(err, &transition) {
		t.Fatalf("RO to RW with live old Pod error = %v, want drain transition", err)
	}
	assertRoleBindingRole(t, r,
		types.NamespacedName{Namespace: device.Namespace, Name: account},
		managedprotocol.NetworkManagementReadOnlyClusterRole)
	if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("old network Deployment remains during escalation: %v", err)
	}

	// Even after the Deployment is gone, its bound Pod and ReplicaSet keep the
	// old token live, so the reusable account must still have only RO authority.
	err = r.ensureManagedSharedWorkerAccess(ctx, device)
	if !stderrors.As(err, &transition) {
		t.Fatalf("live old Pod did not retain drain transition: %v", err)
	}
	assertRoleBindingRole(t, r,
		types.NamespacedName{Namespace: device.Namespace, Name: account},
		managedprotocol.NetworkManagementReadOnlyClusterRole)
	if err := r.Delete(ctx, pod); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, replicaSet); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("complete RO to RW transition after drain: %v", err)
	}
	assertRoleBindingRole(t, r,
		types.NamespacedName{Namespace: device.Namespace, Name: account},
		managedprotocol.NetworkManagementReadWriteClusterRole)
}

func managedWorkerObjects(device *ciskov1.CiscoDevice, plane, account string) (*appsv1.Deployment,
	*appsv1.ReplicaSet, *corev1.Pod) {
	deploymentName := device.Name + deploymentSuffix
	labels := perDeviceDeploymentLabels(device.Name)
	if plane == managedprotocol.WorkerModeNetworkManagement {
		deploymentName = networkDeploymentName(device.Name, string(device.UID))
		labels = perDeviceNetworkDeploymentLabels(device.Name)
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: deploymentName, UID: types.UID(deploymentName + "-uid"),
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice")),
		},
	}, Spec: appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{MatchLabels: labels},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{ServiceAccountName: account},
		},
	}}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: deploymentName + "-rs", UID: types.UID(deploymentName + "-rs-uid"),
		Labels: labels,
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment")),
		},
	}, Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{ServiceAccountName: account},
	}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: deploymentName + "-pod", UID: types.UID(deploymentName + "-pod-uid"),
		Labels: labels,
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(replicaSet, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
		},
	}, Spec: corev1.PodSpec{ServiceAccountName: account}}
	return deployment, replicaSet, pod
}

func TestManagedAppDowngradeWaitsForWorkloadsOnEveryManagedNode(t *testing.T) {
	ctx := context.Background()
	first := managedAccessDevice("switch-app-a")
	second := managedAccessDevice("switch-app-b")
	firstNode := managedAccessNode(first, false)
	secondNode := managedAccessNode(second, false)
	r := reconcilerFor(t, first, second, firstNode, secondNode)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, first); err != nil {
		t.Fatal(err)
	}
	prepareManagedAccessSettlement(t, r)
	deployment, _, _ := managedWorkerObjects(first, managedprotocol.WorkerModeAppHosting,
		r.appHostingServiceAccountName())
	if err := r.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}

	r.AppHostingAccessMode = managedprotocol.WorkerAccessReadOnly
	err := r.ensureManagedSharedWorkerAccess(ctx, first)
	var transition *sharedWorkerAccessTransition
	if !stderrors.As(err, &transition) || !strings.Contains(err.Error(), "manager-owned Node cordons") {
		t.Fatalf("app downgrade initial cordon error = %v, want cordon transition", err)
	}
	assertClusterBindingRole(t, r, vkAccessClusterRoleBindingName(first.Namespace, r.appHostingServiceAccountName()),
		managedprotocol.AppHostingReadWriteClusterRole)
	if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), &appsv1.Deployment{}); err != nil {
		t.Fatalf("cordon phase deleted worker before workload observation: %v", err)
	}
	for _, node := range []*corev1.Node{firstNode, secondNode} {
		var current corev1.Node
		if err := r.Get(ctx, client.ObjectKeyFromObject(node), &current); err != nil {
			t.Fatal(err)
		}
		if !current.Spec.Unschedulable ||
			current.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID] !=
				current.Annotations[managedprotocol.AnnotationDeviceUID] {
			t.Fatalf("Node %s was not cordoned with its exact device UID: %#v", current.Name, current)
		}
	}

	// Model a scheduling decision that was already in flight when the manager
	// wrote the cordons. The mandatory reconcile boundary must observe this Pod
	// before it drains the app worker or replaces the write profile.
	workload := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "application", Name: "still-running",
	}, Spec: corev1.PodSpec{NodeName: second.Status.NodeIdentity.NodeName}}
	if err := r.Create(ctx, workload); err != nil {
		t.Fatal(err)
	}

	err = r.ensureManagedSharedWorkerAccess(ctx, first)
	if !stderrors.As(err, &transition) || !strings.Contains(err.Error(), "workload Pod application/still-running") {
		t.Fatalf("app downgrade with assigned workload error = %v, want visible transition blocker", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), &appsv1.Deployment{}); err != nil {
		t.Fatalf("blocked app downgrade deleted worker before workloads settled: %v", err)
	}

	if err := r.Delete(ctx, workload); err != nil {
		t.Fatal(err)
	}
	err = r.ensureManagedSharedWorkerAccess(ctx, first)
	if !stderrors.As(err, &transition) {
		t.Fatalf("app downgrade did not drain old worker: %v", err)
	}
	assertClusterBindingRole(t, r, vkAccessClusterRoleBindingName(first.Namespace, r.appHostingServiceAccountName()),
		managedprotocol.AppHostingReadWriteClusterRole)
	if err := r.ensureManagedSharedWorkerAccess(ctx, first); err != nil {
		t.Fatalf("complete app downgrade after workload and worker drain: %v", err)
	}
	assertClusterBindingRole(t, r, vkAccessClusterRoleBindingName(first.Namespace, r.appHostingServiceAccountName()),
		managedprotocol.AppHostingReadOnlyClusterRole)
}

func TestManagedAppCordonRestoresOnlyManagerOwnedIntent(t *testing.T) {
	for _, tc := range []struct {
		name              string
		operatorCordoned  bool
		wantUnschedulable bool
	}{
		{name: "manager cordon is restored", wantUnschedulable: false},
		{name: "operator cordon is preserved", operatorCordoned: true, wantUnschedulable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			device := managedAccessDevice("switch-cordon")
			node := managedAccessNode(device, tc.operatorCordoned)
			r := reconcilerFor(t, device, node)
			r.ManagedTopology = true
			if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
				t.Fatal(err)
			}

			r.AppHostingAccessMode = managedprotocol.WorkerAccessReadOnly
			convergeManagedSharedWorkerAccess(t, r, device)
			var cordoned corev1.Node
			if err := r.Get(ctx, client.ObjectKeyFromObject(node), &cordoned); err != nil {
				t.Fatal(err)
			}
			marker := cordoned.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID]
			if tc.operatorCordoned && marker != "" {
				t.Fatalf("operator-owned cordon gained manager marker %q", marker)
			}
			if !tc.operatorCordoned && marker != string(device.UID) {
				t.Fatalf("manager cordon marker=%q, want device UID %q", marker, device.UID)
			}

			r.AppHostingAccessMode = managedprotocol.WorkerAccessReadWrite
			if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
				t.Fatalf("restore read-write grants: %v", err)
			}
			if err := r.restoreManagedAppHostingCordon(ctx, device); err != nil {
				t.Fatal(err)
			}
			var beforeReady corev1.Node
			if err := r.Get(ctx, client.ObjectKeyFromObject(node), &beforeReady); err != nil {
				t.Fatal(err)
			}
			if !beforeReady.Spec.Unschedulable {
				t.Fatal("Node was uncordoned before exact app worker readiness proof")
			}

			before := beforeReady.DeepCopy()
			if beforeReady.Annotations == nil {
				beforeReady.Annotations = map[string]string{}
			}
			beforeReady.Annotations[managedprotocol.AnnotationAppWorkerUsername] =
				"system:serviceaccount:" + device.Namespace + ":" + r.appHostingServiceAccountName()
			beforeReady.Annotations[managedprotocol.AnnotationAppWorkerPodName] = "app-worker"
			beforeReady.Annotations[managedprotocol.AnnotationAppWorkerPodUID] = "app-worker-uid"
			if err := r.Patch(ctx, &beforeReady, client.MergeFrom(before)); err != nil {
				t.Fatal(err)
			}
			now := metav1.Now()
			device.Status.WorkerRevision = &ciskov1.DeviceWorkerRevisionStatus{
				DesiredRevision:    "sha256:" + strings.Repeat("a", 64),
				ObservedRevision:   "sha256:" + strings.Repeat("a", 64),
				PodUID:             "app-worker-uid",
				ReadyHeartbeatTime: &now,
			}
			if err := r.restoreManagedAppHostingCordon(ctx, device); err != nil {
				t.Fatal(err)
			}
			var restored corev1.Node
			if err := r.Get(ctx, client.ObjectKeyFromObject(node), &restored); err != nil {
				t.Fatal(err)
			}
			if restored.Spec.Unschedulable != tc.wantUnschedulable {
				t.Fatalf("restored spec.unschedulable=%t, want %t", restored.Spec.Unschedulable, tc.wantUnschedulable)
			}
			if !tc.operatorCordoned && restored.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID] != "" {
				t.Fatalf("manager cordon marker remains after readiness: %#v", restored.Annotations)
			}
		})
	}
}

func TestManagedAppCordonRejectsStaleDeviceMarker(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-stale-cordon")
	node := managedAccessNode(device, true)
	node.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID] = "replaced-device-uid"
	r := reconcilerFor(t, device, node)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatal(err)
	}
	r.AppHostingAccessMode = managedprotocol.WorkerAccessReadOnly
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "cordon marker for device UID") {
		t.Fatalf("stale cordon marker error = %v", err)
	}
	assertClusterBindingRole(t, r, vkAccessClusterRoleBindingName(device.Namespace, r.appHostingServiceAccountName()),
		managedprotocol.AppHostingReadWriteClusterRole)
}

func TestManagedNetworkDowngradeSettlesEveryConsumerAndDrainsNamespace(t *testing.T) {
	ctx := context.Background()
	first := managedAccessDevice("switch-network-a")
	second := managedAccessDevice("switch-network-b")
	second.Status.TopologyLock = &ciskov1.DeviceTopologyLockStatus{
		State: ciskov1.DeviceTopologyLockActive, ReservationID: "active-reservation",
	}
	r := reconcilerFor(t, first, second)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, first); err != nil {
		t.Fatal(err)
	}
	prepareManagedAccessSettlement(t, r)
	deployments := []*appsv1.Deployment{}
	for _, device := range []*ciskov1.CiscoDevice{first, second} {
		deployment, _, _ := managedWorkerObjects(device, managedprotocol.WorkerModeNetworkManagement,
			r.networkManagementServiceAccountName())
		if err := r.Create(ctx, deployment); err != nil {
			t.Fatal(err)
		}
		deployments = append(deployments, deployment)
	}

	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessReadOnly
	err := r.ensureManagedSharedWorkerAccess(ctx, first)
	var transition *sharedWorkerAccessTransition
	if !stderrors.As(err, &transition) || !strings.Contains(err.Error(), "switch-network-b") ||
		!strings.Contains(err.Error(), "topology lock") {
		t.Fatalf("network downgrade with peer authority error = %v, want peer blocker", err)
	}
	assertRoleBindingRole(t, r,
		types.NamespacedName{Namespace: first.Namespace, Name: r.networkManagementServiceAccountName()},
		managedprotocol.NetworkManagementReadWriteClusterRole)
	for _, deployment := range deployments {
		if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), &appsv1.Deployment{}); err != nil {
			t.Fatalf("blocked network downgrade deleted %s early: %v", deployment.Name, err)
		}
	}

	var storedSecond ciskov1.CiscoDevice
	if err := r.Get(ctx, client.ObjectKeyFromObject(second), &storedSecond); err != nil {
		t.Fatal(err)
	}
	storedSecond.Status.TopologyLock = nil
	if err := r.Status().Update(ctx, &storedSecond); err != nil {
		t.Fatal(err)
	}
	err = r.ensureManagedSharedWorkerAccess(ctx, first)
	if !stderrors.As(err, &transition) {
		t.Fatalf("settled network downgrade did not enter namespace drain: %v", err)
	}
	for _, deployment := range deployments {
		if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
			t.Fatalf("network drain did not foreground-delete %s: %v", deployment.Name, err)
		}
	}
	assertRoleBindingRole(t, r,
		types.NamespacedName{Namespace: first.Namespace, Name: r.networkManagementServiceAccountName()},
		managedprotocol.NetworkManagementReadWriteClusterRole)
	if err := r.ensureManagedSharedWorkerAccess(ctx, first); err != nil {
		t.Fatalf("complete network downgrade after namespace drain: %v", err)
	}
	assertRoleBindingRole(t, r,
		types.NamespacedName{Namespace: first.Namespace, Name: r.networkManagementServiceAccountName()},
		managedprotocol.NetworkManagementReadOnlyClusterRole)
}

func TestManagedNetworkDowngradeWaitsForReplicaSetAndPod(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-network-descendants")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatal(err)
	}
	prepareManagedAccessSettlement(t, r)
	account := r.networkManagementServiceAccountName()
	deployment, replicaSet, pod := managedWorkerObjects(device,
		managedprotocol.WorkerModeNetworkManagement, account)
	for _, object := range []client.Object{deployment, replicaSet, pod} {
		if err := r.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessReadOnly
	var transition *sharedWorkerAccessTransition
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); !stderrors.As(err, &transition) {
		t.Fatalf("network downgrade with live Deployment error = %v, want drain", err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); !stderrors.As(err, &transition) {
		t.Fatalf("network downgrade with live descendants error = %v, want drain", err)
	}
	assertRoleBindingRole(t, r, types.NamespacedName{Namespace: device.Namespace, Name: account},
		managedprotocol.NetworkManagementReadWriteClusterRole)
	if err := r.Delete(ctx, pod); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); !stderrors.As(err, &transition) {
		t.Fatalf("network downgrade ignored live ReplicaSet: %v", err)
	}
	if err := r.Delete(ctx, replicaSet); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("complete network downgrade after Deployment/ReplicaSet/Pod drain: %v", err)
	}
	assertRoleBindingRole(t, r, types.NamespacedName{Namespace: device.Namespace, Name: account},
		managedprotocol.NetworkManagementReadOnlyClusterRole)
}

func TestManagedAccessTransitionRejectsForeignReservedAccountDeployment(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-foreign-worker")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessReadOnly
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatal(err)
	}
	account := r.networkManagementServiceAccountName()
	foreign := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: networkDeploymentName(device.Name, string(device.UID)), UID: "foreign-uid",
	}, Spec: appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"foreign": "true"}},
		Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"foreign": "true"},
		}, Spec: corev1.PodSpec{ServiceAccountName: account}},
	}}
	if err := r.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	r.NetworkManagementAccessMode = managedprotocol.WorkerAccessReadWrite
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "lacks exact managed-worker provenance") {
		t.Fatalf("foreign reserved-account Deployment transition error = %v", err)
	}
	assertRoleBindingRole(t, r, types.NamespacedName{Namespace: device.Namespace, Name: account},
		managedprotocol.NetworkManagementReadOnlyClusterRole)
	if err := r.Get(ctx, client.ObjectKeyFromObject(foreign), &appsv1.Deployment{}); err != nil {
		t.Fatalf("foreign Deployment was deleted during rejected transition: %v", err)
	}
}

func TestSharedWorkerCleanupIgnoresCompletedPeerAndWaitsForReplicaSet(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-cleanup")
	completedPeer := newDevice("switch-legacy", device.Namespace)
	completedPeer.UID = "legacy-uid"
	r := reconcilerFor(t, device, completedPeer)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatal(err)
	}
	appSA := r.appHostingServiceAccountName()
	leftover := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "leftover-app-rs",
	}, Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{ServiceAccountName: appSA},
	}}}
	if err := r.Create(ctx, leftover); err != nil {
		t.Fatal(err)
	}
	if err := r.cleanupManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatal(err)
	}
	var account corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: appSA}, &account); err != nil {
		t.Fatalf("live ReplicaSet did not retain shared account: %v", err)
	}
	if err := r.Delete(ctx, leftover); err != nil {
		t.Fatal(err)
	}
	if err := r.cleanupManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{appSA, r.networkManagementServiceAccountName()} {
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: name}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
			t.Fatalf("completed/non-managed peer leaked shared ServiceAccount %s: %v", name, err)
		}
	}
}

func TestDisabledSharedWorkerCleanupWaitsForReplicaSet(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-disabled-cleanup")
	r := reconcilerFor(t, device)
	appSA := r.appHostingServiceAccountName()
	if err := r.ensureSharedServiceAccount(ctx, device.Namespace, appSA,
		managedprotocol.WorkerModeAppHosting, managedprotocol.AppHostingReadOnlyClusterRole); err != nil {
		t.Fatal(err)
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "disabled-app-rs",
	}, Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{ServiceAccountName: appSA},
	}}}
	if err := r.Create(ctx, replicaSet); err != nil {
		t.Fatal(err)
	}
	if err := r.cleanupDisabledSharedWorkerAccounts(ctx, device,
		managedprotocol.WorkerAccessDisabled, managedprotocol.WorkerAccessReadWrite); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: appSA}, &corev1.ServiceAccount{}); err != nil {
		t.Fatalf("disabled cleanup removed ServiceAccount with live ReplicaSet: %v", err)
	}
	if err := r.Delete(ctx, replicaSet); err != nil {
		t.Fatal(err)
	}
	if err := r.cleanupDisabledSharedWorkerAccounts(ctx, device,
		managedprotocol.WorkerAccessDisabled, managedprotocol.WorkerAccessReadWrite); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: appSA}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
		t.Fatalf("disabled cleanup retained drained ServiceAccount: %v", err)
	}
}

func assertRoleBindingRole(t *testing.T, r *CiscoDeviceReconciler, key types.NamespacedName, role string) {
	t.Helper()
	var binding rbacv1.RoleBinding
	if err := r.Get(context.Background(), key, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != role {
		t.Fatalf("RoleBinding %s role=%q, want %q", key, binding.RoleRef.Name, role)
	}
}

func assertClusterBindingRole(t *testing.T, r *CiscoDeviceReconciler, name, role string) {
	t.Helper()
	var binding rbacv1.ClusterRoleBinding
	if err := r.Get(context.Background(), types.NamespacedName{Name: name}, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != role {
		t.Fatalf("ClusterRoleBinding %s role=%q, want %q", name, binding.RoleRef.Name, role)
	}
}
