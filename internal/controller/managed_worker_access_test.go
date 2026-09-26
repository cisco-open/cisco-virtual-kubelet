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
	"fmt"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
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

func TestManagedSharedBindingPostAuditUsesAPIReader(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-binding-api-reader")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	additive := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "just-created-additive-network-grant",
	}, RoleRef: rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementReadOnlyClusterRole,
	}, Subjects: sharedWorkerSubject(device.Namespace, r.networkManagementServiceAccountName())}
	r.APIReader = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(device.DeepCopy(), additive).Build()

	// The cached writer deliberately does not contain the just-created grant.
	// The post-bind audit must still observe it through the direct reader.
	if err := r.Get(ctx, client.ObjectKeyFromObject(additive), &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("cached writer unexpectedly contains additive grant: %v", err)
	}
	appRole, appAccess, networkRole, networkAccess, globalReadRole, err := r.managedWorkerProfiles()
	if err != nil {
		t.Fatal(err)
	}
	err = r.auditManagedSharedWorkerBindings(ctx, device.Namespace, device.Namespace,
		r.appHostingServiceAccountName(), r.networkManagementServiceAccountName(),
		vkAccessClusterRoleBindingName(device.Namespace, r.appHostingServiceAccountName()),
		vkAccessClusterRoleBindingName(device.Namespace, r.networkManagementServiceAccountName()+"-global-read"), "",
		appRole, r.appHostingDeviceReadRoleName(), networkRole, networkManagementLeaseRole(networkAccess),
		globalReadRole, appAccess, networkAccess)
	if err == nil || !strings.Contains(err.Error(), "unexpected additive RoleBinding") {
		t.Fatalf("APIReader post-bind audit error=%v", err)
	}
}

type postBindRoleBindingInjector struct {
	client.Reader
	writer     client.Client
	triggerCRB string
	additive   *rbacv1.RoleBinding
	injected   bool
}

func (r *postBindRoleBindingInjector) List(ctx context.Context, list client.ObjectList,
	opts ...client.ListOption) error {
	if _, ok := list.(*rbacv1.RoleBindingList); ok && !r.injected {
		var trigger rbacv1.ClusterRoleBinding
		if err := r.Reader.Get(ctx, types.NamespacedName{Name: r.triggerCRB}, &trigger); err == nil {
			if err := r.writer.Create(ctx, r.additive.DeepCopy()); err != nil {
				return err
			}
			r.injected = true
		}
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestManagedSharedBindingPostAuditQuarantinesConcurrentGrant(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-binding-race-closure")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	networkAccount := r.networkManagementServiceAccountName()
	additive := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "concurrent-additive-network-grant",
	}, RoleRef: rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementReadOnlyClusterRole,
	}, Subjects: sharedWorkerSubject(device.Namespace, networkAccount)}
	workload := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "concurrent-grant-network-process", UID: "concurrent-grant-network-process-uid",
	}, Spec: corev1.PodSpec{ServiceAccountName: networkAccount}}
	if err := r.Create(ctx, workload); err != nil {
		t.Fatal(err)
	}
	injector := &postBindRoleBindingInjector{
		Reader: r.Client, writer: r.Client, additive: additive,
		triggerCRB: vkAccessClusterRoleBindingName(device.Namespace, r.appHostingServiceAccountName()),
	}
	r.APIReader = injector

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "unexpected additive RoleBinding") ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("post-bind race quarantine error=%v", err)
	}
	if !injector.injected {
		t.Fatal("test did not inject the additive RoleBinding after canonical access was bound")
	}
	for _, object := range []client.Object{
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: additive.Namespace, Name: additive.Name}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: workload.Namespace, Name: workload.Name}},
	} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(object), object); !apierrors.IsNotFound(err) {
			t.Fatalf("post-bind race object survived quarantine: %T %v", object, err)
		}
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
	for _, account := range []string{r.appHostingServiceAccountName(), networkAccount} {
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: account}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
			t.Fatalf("post-bind race left shared ServiceAccount %s authorized: %v", account, err)
		}
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

func TestManagedFunctionalAccountsRotateAtReservedPolicyEpoch(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-epoch")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed pre-upgrade shared worker access: %v", err)
	}
	prepareManagedAccessSettlement(t, r)
	decoy := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "token-audit-decoy", UID: "token-audit-decoy-uid",
	}}
	if err := r.Create(ctx, decoy); err != nil {
		t.Fatal(err)
	}

	for _, worker := range []struct {
		plane   string
		account string
	}{
		{managedprotocol.WorkerModeAppHosting, r.appHostingServiceAccountName()},
		{managedprotocol.WorkerModeNetworkManagement, r.networkManagementServiceAccountName()},
	} {
		deployment, _, _ := managedWorkerObjects(device, worker.plane, worker.account)
		if err := r.Create(ctx, deployment); err != nil {
			t.Fatal(err)
		}
		// A token signed before admission protection may remain valid after its
		// mutable annotation is changed to a different live, non-reserved account.
		// Epoch rotation—not metadata inspection—invalidates the claimed old UID.
		hidden := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: "annotation-disguised-token-" + worker.plane,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: decoy.Name,
				corev1.ServiceAccountUIDKey:  string(decoy.UID),
			},
		}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{"token": []byte("previously-signed-jwt")}}
		if err := r.Create(ctx, hidden); err != nil {
			t.Fatal(err)
		}
	}
	rogueSharedPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "pre-policy-network-process", UID: "pre-policy-network-process-uid",
	}, Spec: corev1.PodSpec{ServiceAccountName: r.networkManagementServiceAccountName()}}
	if err := r.Create(ctx, rogueSharedPod); err != nil {
		t.Fatal(err)
	}

	tracker := &generatedAccessDeleteOrderClient{Client: r.Client}
	r.Client = tracker
	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	var transition *sharedWorkerAccessTransition
	if !stderrors.As(err, &transition) ||
		!sharedTransitionContains(transition.planes, managedprotocol.WorkerModeAppHosting) ||
		!sharedTransitionContains(transition.planes, managedprotocol.WorkerModeNetworkManagement) {
		t.Fatalf("shared policy-epoch rotation error=%v, transition=%#v", err, transition)
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
	for _, account := range []string{r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()} {
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: account}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
			t.Fatalf("old shared ServiceAccount %s survived epoch rotation: %v", account, err)
		}
	}
	for _, name := range []string{
		device.Name + deploymentSuffix,
		networkDeploymentName(device.Name, string(device.UID)),
	} {
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: name}, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
			t.Fatalf("old shared worker Deployment %s survived epoch rotation: %v", name, err)
		}
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(rogueSharedPod), &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("unproven process using old shared account survived epoch quarantine: %v", err)
	}

	// Once both old planes are observed gone, each functional account is
	// recreated with the same verified policy+binding epoch and a fresh UID on
	// a real API server. The annotation-disguised Secrets are never trusted or
	// mutated.
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("recreate shared worker access after epoch rotation: %v", err)
	}
	for _, account := range []string{r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()} {
		var rotated corev1.ServiceAccount
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: account}, &rotated); err != nil {
			t.Fatal(err)
		}
		if got := rotated.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy]; got != r.WorkerServiceAccountPolicyEpoch {
			t.Fatalf("rotated shared ServiceAccount %s policy epoch=%q, want %q", account, got, r.WorkerServiceAccountPolicyEpoch)
		}
	}
}

func TestManagedFunctionalAccountPolicyEpochWaitsForNetworkMutations(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-epoch-active")
	device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase: ciskov1.DeviceMaintenanceSessionActive, SessionToken: "active-gnoi-session",
	}
	r := reconcilerFor(t, device)
	uidClient := &serviceAccountUIDAssigningClient{Client: r.Client}
	r.Client = uidClient
	r.ManagedTopology = true
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed pre-upgrade shared worker access: %v", err)
	}
	appAccount := r.appHostingServiceAccountName()
	networkAccount := r.networkManagementServiceAccountName()
	var oldApp, oldNetwork corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: appAccount}, &oldApp); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: networkAccount}, &oldNetwork); err != nil {
		t.Fatal(err)
	}
	networkDeployment, _, _ := managedWorkerObjects(device, managedprotocol.WorkerModeNetworkManagement, networkAccount)
	if err := r.Create(ctx, networkDeployment); err != nil {
		t.Fatal(err)
	}

	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	var transition *sharedWorkerAccessTransition
	if !stderrors.As(err, &transition) ||
		!strings.Contains(err.Error(), "planned network worker policy-epoch rotation is waiting") ||
		!strings.Contains(err.Error(), "unresolved maintenance session") {
		t.Fatalf("active network mutation epoch transition error=%v", err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: appAccount},
		&corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
		t.Fatalf("first rotation pass did not retire old app account: %v", err)
	}

	// A second reconcile observes the retired app UID and regrants that plane
	// independently. Only the network identity remains fenced by its active
	// mutation, so app hosting is not unavailable for the mutation's lifetime.
	err = r.ensureManagedSharedWorkerAccess(ctx, device)
	if !stderrors.As(err, &transition) ||
		sharedTransitionContains(transition.planes, managedprotocol.WorkerModeAppHosting) ||
		!sharedTransitionContains(transition.planes, managedprotocol.WorkerModeNetworkManagement) {
		t.Fatalf("second epoch transition error=%v, transition=%#v", err, transition)
	}
	var rotatedApp corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: appAccount}, &rotatedApp); err != nil {
		t.Fatalf("app account was not restored while network rotation remained blocked: %v", err)
	}
	if rotatedApp.UID == "" || rotatedApp.UID == oldApp.UID {
		t.Fatalf("app account UID was not rotated: old=%q current=%q", oldApp.UID, rotatedApp.UID)
	}
	if got := rotatedApp.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy]; got != r.WorkerServiceAccountPolicyEpoch {
		t.Fatalf("restored app account epoch=%q, want %q", got, r.WorkerServiceAccountPolicyEpoch)
	}
	var currentNetwork corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: networkAccount}, &currentNetwork); err != nil {
		t.Fatalf("planned migration revoked network account before mutation settled: %v", err)
	}
	if currentNetwork.UID != oldNetwork.UID {
		t.Fatalf("planned migration rotated network UID while mutation active: old=%q current=%q", oldNetwork.UID, currentNetwork.UID)
	}
	if got := currentNetwork.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy]; got != "sha256:old-policy-and-binding-generation" {
		t.Fatalf("planned migration changed network epoch while mutation active: %q", got)
	}
	assertClusterBindingRole(t, r,
		vkAccessClusterRoleBindingName(device.Namespace, appAccount), managedprotocol.AppHostingReadWriteClusterRole)
	assertRoleBindingRole(t, r,
		types.NamespacedName{Namespace: device.Namespace, Name: appAccount}, managedprotocol.AppHostingDeviceReadClusterRole)
	assertRoleBindingRole(t, r,
		types.NamespacedName{Namespace: device.Namespace, Name: networkAccount},
		managedprotocol.NetworkManagementReadWriteClusterRole)
	if err := r.Get(ctx, client.ObjectKeyFromObject(networkDeployment), &appsv1.Deployment{}); err != nil {
		t.Fatalf("planned network rotation drained the old worker while mutation active: %v", err)
	}
}

func TestManagedReconcileRestoresAppWorkerWhileNetworkEpochRotationIsBlocked(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "true")
	ctx := context.Background()
	device := newDevice("switch-shared-epoch-controller", "edge")
	device.UID = "switch-shared-epoch-controller-uid"
	device.Spec.PhysicalIdentity = "serial-switch-shared-epoch-controller"
	device.Labels = map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		topology.CiscoTopologyLabelPrefix + "site": "site-a",
	}
	appUsername := "system:serviceaccount:" + device.Namespace + ":" + managedprotocol.AppHostingServiceAccount
	networkUsername := "system:serviceaccount:" + device.Namespace + ":" + managedprotocol.NetworkManagementServiceAccount
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "switch-shared-epoch-controller-node-uid",
		Labels: map[string]string{topology.LabelType: topology.TypeVirtualKubelet},
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:               "true",
			managedprotocol.AnnotationDeviceNamespace:       device.Namespace,
			managedprotocol.AnnotationDeviceName:            device.Name,
			managedprotocol.AnnotationDeviceUID:             string(device.UID),
			managedprotocol.AnnotationNodeName:              device.Name,
			managedprotocol.AnnotationNodeUID:               "switch-shared-epoch-controller-node-uid",
			managedprotocol.AnnotationWorkerUsername:        appUsername,
			managedprotocol.AnnotationAppWorkerUsername:     appUsername,
			managedprotocol.AnnotationNetworkWorkerUsername: networkUsername,
			managedprotocol.AnnotationWorkerProtocol:        managedprotocol.Version,
		},
	}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue,
	}}}}
	policy, ledger := managedPolicyAndLedger(t, nil)
	r := reconcilerFor(t, device, node, policy, ledger)
	if err := opsv1alpha1.AddToScheme(r.Scheme); err != nil {
		t.Fatal(err)
	}
	uidClient := &serviceAccountUIDAssigningClient{Client: r.Client}
	r.Client = leaseUIDAssigningClient{Client: uidClient}
	r.APIReader = r.Client
	r.ManagedTopology = true
	r.TopologyPolicyNamespace = policy.Namespace
	r.TopologyPolicyName = policy.Name
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	request := reconcileRequest(device.Namespace, device.Name)
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatalf("seed managed worker planes: %v", err)
	}

	appAccount := r.appHostingServiceAccountName()
	networkAccount := r.networkManagementServiceAccountName()
	var oldApp, oldNetwork corev1.ServiceAccount
	for key, target := range map[types.NamespacedName]*corev1.ServiceAccount{
		{Namespace: device.Namespace, Name: appAccount}:     &oldApp,
		{Namespace: device.Namespace, Name: networkAccount}: &oldNetwork,
	} {
		if err := r.Get(ctx, key, target); err != nil {
			t.Fatal(err)
		}
	}
	networkKey := types.NamespacedName{
		Namespace: device.Namespace, Name: networkDeploymentName(device.Name, string(device.UID)),
	}
	var networkBefore appsv1.Deployment
	if err := r.Get(ctx, networkKey, &networkBefore); err != nil {
		t.Fatal(err)
	}

	var liveDevice ciskov1.CiscoDevice
	if err := r.Get(ctx, client.ObjectKeyFromObject(device), &liveDevice); err != nil {
		t.Fatal(err)
	}
	liveDevice.Status.TopologyLock = &ciskov1.DeviceTopologyLockStatus{
		State: ciskov1.DeviceTopologyLockActive, ReservationID: "active-network-rollout",
	}
	if err := r.Status().Update(ctx, &liveDevice); err != nil {
		t.Fatal(err)
	}
	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"

	// The first pass revokes the old app identity and worker. The second pass
	// must restore the app plane while continuing to fence the network plane.
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatalf("retire old app epoch: %v", err)
	}
	result, err := r.Reconcile(ctx, request)
	if err != nil {
		t.Fatalf("restore app plane under network-only blocker: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("network-only epoch blocker did not request a retry")
	}

	var rotatedApp, retainedNetwork corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: appAccount}, &rotatedApp); err != nil {
		t.Fatalf("rotated app ServiceAccount was not restored: %v", err)
	}
	if rotatedApp.UID == "" || rotatedApp.UID == oldApp.UID {
		t.Fatalf("app ServiceAccount UID was not rotated: old=%q current=%q", oldApp.UID, rotatedApp.UID)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: networkAccount}, &retainedNetwork); err != nil {
		t.Fatalf("network ServiceAccount was removed during an active mutation: %v", err)
	}
	if retainedNetwork.UID != oldNetwork.UID {
		t.Fatalf("network ServiceAccount UID changed during an active mutation: old=%q current=%q",
			oldNetwork.UID, retainedNetwork.UID)
	}
	var appDeployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix},
		&appDeployment); err != nil {
		t.Fatalf("app Deployment was not restored under network-only blocker: %v", err)
	}
	if appDeployment.Spec.Template.Spec.ServiceAccountName != appAccount {
		t.Fatalf("restored app Deployment uses ServiceAccount %q, want %q",
			appDeployment.Spec.Template.Spec.ServiceAccountName, appAccount)
	}
	var networkAfter appsv1.Deployment
	if err := r.Get(ctx, networkKey, &networkAfter); err != nil {
		t.Fatalf("network Deployment was removed during an active mutation: %v", err)
	}
	if networkAfter.UID != networkBefore.UID || networkAfter.ResourceVersion != networkBefore.ResourceVersion ||
		!reflect.DeepEqual(networkAfter.Spec, networkBefore.Spec) {
		t.Fatalf("network Deployment changed during its epoch blocker: before=%#v after=%#v",
			networkBefore, networkAfter)
	}
}

type serviceAccountUIDAssigningClient struct {
	client.Client
	next int
}

func (c *serviceAccountUIDAssigningClient) Create(ctx context.Context, object client.Object,
	opts ...client.CreateOption) error {
	switch typed := object.(type) {
	case *corev1.ServiceAccount:
		if typed.UID != "" {
			break
		}
		c.next++
		typed.UID = types.UID(fmt.Sprintf("server-assigned-service-account-uid-%d", c.next))
	case *appsv1.Deployment:
		if typed.UID != "" {
			break
		}
		c.next++
		typed.UID = types.UID(fmt.Sprintf("server-assigned-deployment-uid-%d", c.next))
	}
	return c.Client.Create(ctx, object, opts...)
}

func TestSharedPolicyEpochQuarantinesForeignAppBeforeNetworkMutationWait(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-epoch-mixed")
	device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase: ciskov1.DeviceMaintenanceSessionActive, SessionToken: "active-gnoi-session",
	}
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed pre-upgrade shared worker access: %v", err)
	}

	appAccount := r.appHostingServiceAccountName()
	appKey := types.NamespacedName{Namespace: device.Namespace, Name: appAccount}
	var oldApp corev1.ServiceAccount
	if err := r.Get(ctx, appKey, &oldApp); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, &oldApp); err != nil {
		t.Fatal(err)
	}
	foreignApp := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: appKey.Namespace, Name: appKey.Name, UID: "foreign-app-account-uid",
	}}
	appPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "foreign-app-process", UID: "foreign-app-process-uid",
	}, Spec: corev1.PodSpec{ServiceAccountName: appAccount}}
	for _, object := range []client.Object{foreignApp, appPod} {
		if err := r.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}

	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "without exact controller provenance") {
		t.Fatalf("mixed foreign-app/network-mutation error=%v", err)
	}
	var retainedApp corev1.ServiceAccount
	if err := r.Get(ctx, appKey, &retainedApp); err != nil || retainedApp.UID != foreignApp.UID {
		t.Fatalf("foreign app ServiceAccount was not retained: account=%#v err=%v", retainedApp, err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(appPod), &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("foreign app process survived concrete-compromise quarantine: %v", err)
	}
	for _, object := range []client.Object{
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: vkAccessClusterRoleBindingName(device.Namespace, appAccount)}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: appAccount}},
	} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(object), object); !apierrors.IsNotFound(err) {
			t.Fatalf("exact app grant survived foreign-name quarantine: %T %v", object, err)
		}
	}

	// The unrelated network account is an epoch-only planned migration. Its
	// active mutation keeps the old UID and grants intact until settlement.
	networkAccount := r.networkManagementServiceAccountName()
	var retainedNetwork corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: networkAccount}, &retainedNetwork); err != nil {
		t.Fatalf("planned network rotation ignored active mutation: %v", err)
	}
	if got := retainedNetwork.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy]; got != "sha256:old-policy-and-binding-generation" {
		t.Fatalf("planned network rotation changed epoch while mutation active: %q", got)
	}
	assertRoleBindingRole(t, r, types.NamespacedName{Namespace: device.Namespace, Name: networkAccount},
		managedprotocol.NetworkManagementReadWriteClusterRole)
}

func TestManagedFunctionalAccountLegacyTokenQuarantinesDespiteNetworkMutation(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-epoch-compromised")
	device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase: ciskov1.DeviceMaintenanceSessionActive, SessionToken: "active-gnoi-session",
	}
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed pre-upgrade shared worker access: %v", err)
	}
	networkAccount := r.networkManagementServiceAccountName()
	legacyToken := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "attributable-network-token",
		Annotations: map[string]string{corev1.ServiceAccountNameKey: networkAccount},
	}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{"token": []byte("signed-network-token")}}
	if err := r.Create(ctx, legacyToken); err != nil {
		t.Fatal(err)
	}

	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	var transition *sharedWorkerAccessTransition
	if !stderrors.As(err, &transition) || !strings.Contains(err.Error(), "legacy ServiceAccount token Secret") ||
		strings.Contains(err.Error(), "planned network worker policy-epoch rotation is waiting") {
		t.Fatalf("compromise quarantine error=%v", err)
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
	for _, account := range []string{r.appHostingServiceAccountName(), networkAccount} {
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: account}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
			t.Fatalf("compromise quarantine retained shared account %s: %v", account, err)
		}
	}
}

func TestSharedPolicyEpochQuarantinesNetworkDespiteForeignAppCollision(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-epoch-collision")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed pre-upgrade shared worker access: %v", err)
	}

	appKey := types.NamespacedName{Namespace: device.Namespace, Name: r.appHostingServiceAccountName()}
	var oldApp corev1.ServiceAccount
	if err := r.Get(ctx, appKey, &oldApp); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, &oldApp); err != nil {
		t.Fatal(err)
	}
	foreignApp := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: appKey.Namespace, Name: appKey.Name, UID: "foreign-app-account-uid",
	}}
	if err := r.Create(ctx, foreignApp); err != nil {
		t.Fatal(err)
	}

	networkAccount := r.networkManagementServiceAccountName()
	legacyToken := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "attributable-network-token-with-app-collision",
		Annotations: map[string]string{corev1.ServiceAccountNameKey: networkAccount},
	}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{"token": []byte("signed-network-token")}}
	networkPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "network-process-with-app-collision", UID: "network-process-with-app-collision-uid",
	}, Spec: corev1.PodSpec{ServiceAccountName: networkAccount}}
	for _, object := range []client.Object{legacyToken, networkPod} {
		if err := r.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}

	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"
	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "without exact controller provenance") {
		t.Fatalf("foreign app collision error=%v", err)
	}
	var retainedApp corev1.ServiceAccount
	if err := r.Get(ctx, appKey, &retainedApp); err != nil || retainedApp.UID != foreignApp.UID {
		t.Fatalf("foreign app ServiceAccount was not retained: account=%#v err=%v", retainedApp, err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: networkAccount},
		&corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
		t.Fatalf("compromised network ServiceAccount survived unrelated app collision: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(networkPod), &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("network process survived unrelated app collision: %v", err)
	}
	assertNoSharedWorkerAuthority(t, r, device.Namespace)
}

func TestSharedLegacyTokenRevocationContinuesPastForeignBinding(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-mixed-revocation")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed shared worker access: %v", err)
	}

	networkAccount := r.networkManagementServiceAccountName()
	roleBindingKey := types.NamespacedName{Namespace: device.Namespace, Name: networkAccount}
	var drifted rbacv1.RoleBinding
	if err := r.Get(ctx, roleBindingKey, &drifted); err != nil {
		t.Fatal(err)
	}
	drifted.Subjects = []rbacv1.Subject{{Kind: "User", Name: "foreign-operator"}}
	if err := r.Update(ctx, &drifted); err != nil {
		t.Fatal(err)
	}

	err := r.revokeSharedWorkerForLegacyToken(ctx, device.Namespace, networkAccount)
	if err == nil || !strings.Contains(err.Error(), "retain foreign shared worker RoleBinding") {
		t.Fatalf("mixed exact/drifted revocation error=%v", err)
	}
	if err := r.Get(ctx, roleBindingKey, &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("drifted RoleBinding should be retained for operator review: %v", err)
	}
	clusterBindingKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(
		device.Namespace, networkAccount+"-global-read")}
	if err := r.Get(ctx, clusterBindingKey, &rbacv1.ClusterRoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("exact global ClusterRoleBinding survived independent revocation: %v", err)
	}
}

func TestSharedLegacyTokenQuarantineDeletesAdditivelyDriftedCanonicalGrants(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-additive-quarantine")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed shared worker access: %v", err)
	}

	networkAccount := r.networkManagementServiceAccountName()
	rbKey := types.NamespacedName{Namespace: device.Namespace, Name: networkAccount}
	crbKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, networkAccount+"-global-read")}
	var rb rbacv1.RoleBinding
	if err := r.Get(ctx, rbKey, &rb); err != nil {
		t.Fatal(err)
	}
	rb.Subjects = append(rb.Subjects, rbacv1.Subject{Kind: "User", Name: "attacker"})
	rb.Annotations["attacker.example/drift"] = "true"
	if err := r.Update(ctx, &rb); err != nil {
		t.Fatal(err)
	}
	var crb rbacv1.ClusterRoleBinding
	if err := r.Get(ctx, crbKey, &crb); err != nil {
		t.Fatal(err)
	}
	crb.Subjects = append(crb.Subjects, rbacv1.Subject{Kind: "User", Name: "attacker"})
	crb.Annotations["attacker.example/drift"] = "true"
	if err := r.Update(ctx, &crb); err != nil {
		t.Fatal(err)
	}
	legacyToken := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "network-token-with-additive-grants",
		Annotations: map[string]string{corev1.ServiceAccountNameKey: networkAccount},
	}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{"token": []byte("signed-network-token")}}
	if err := r.Create(ctx, legacyToken); err != nil {
		t.Fatal(err)
	}

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "unexpected RoleBinding") ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("concrete-risk additive-grant quarantine error=%v", err)
	}
	for _, object := range []client.Object{
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: crbKey.Name}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: rbKey.Namespace, Name: rbKey.Name}},
	} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(object), object); !apierrors.IsNotFound(err) {
			t.Fatalf("additively drifted canonical grant survived compromise quarantine: %T %v", object, err)
		}
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: networkAccount},
		&corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
		t.Fatalf("compromised network ServiceAccount survived additive-grant quarantine: %v", err)
	}
}

func TestSharedUnsafeRBACQuarantineDeletesCanonicalBindingWithUnexpectedRole(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-role-recreate")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed shared worker access: %v", err)
	}

	unsafeRole := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "attacker-worker-shell"}, Rules: []rbacv1.PolicyRule{{
		APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"},
	}}}
	if err := r.Create(ctx, unsafeRole); err != nil {
		t.Fatal(err)
	}
	networkAccount := r.networkManagementServiceAccountName()
	rbKey := types.NamespacedName{Namespace: device.Namespace, Name: networkAccount}
	var recreated rbacv1.RoleBinding
	if err := r.Get(ctx, rbKey, &recreated); err != nil {
		t.Fatal(err)
	}
	// Model an attacker deleting and recreating the deterministic canonical
	// name: the immutable roleRef changes, but the reserved SA subject remains.
	recreated.RoleRef.Name = unsafeRole.Name
	if err := r.Update(ctx, &recreated); err != nil {
		t.Fatal(err)
	}

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "unexpected RoleBinding") ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("unexpected-role canonical binding quarantine error=%v", err)
	}
	if err := r.Get(ctx, rbKey, &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("canonical binding with attacker-selected role survived quarantine: %v", err)
	}
	crbKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, networkAccount+"-global-read")}
	if err := r.Get(ctx, crbKey, &rbacv1.ClusterRoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("exact global network grant survived mixed quarantine: %v", err)
	}
}

func TestSharedCompromiseQuarantineDeletesArbitraryReservedSubjectBindingAndAccount(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-shared-arbitrary-binding")
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
		t.Fatalf("seed shared worker access: %v", err)
	}

	networkAccount := r.networkManagementServiceAccountName()
	additive := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "attacker-network-secret-reader"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementReadWriteClusterRole,
		},
		Subjects: sharedWorkerSubject(device.Namespace, networkAccount),
	}
	legacyToken := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "network-token-with-arbitrary-binding",
		Annotations: map[string]string{corev1.ServiceAccountNameKey: networkAccount},
	}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{"token": []byte("signed-network-token")}}
	workload := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "network-process-with-arbitrary-binding", UID: "network-process-with-arbitrary-binding-uid",
	}, Spec: corev1.PodSpec{ServiceAccountName: networkAccount}}
	for _, object := range []client.Object{additive, legacyToken, workload} {
		if err := r.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}

	err := r.ensureManagedSharedWorkerAccess(ctx, device)
	if err == nil || !strings.Contains(err.Error(), "unexpected RoleBinding") ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("arbitrary-binding compromise quarantine error=%v", err)
	}
	for _, object := range []client.Object{
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: additive.Namespace, Name: additive.Name}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: networkAccount}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: workload.Namespace, Name: workload.Name}},
	} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(object), object); !apierrors.IsNotFound(err) {
			t.Fatalf("compromised shared-account object survived quarantine: %T %v", object, err)
		}
	}
	crbKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, networkAccount+"-global-read")}
	if err := r.Get(ctx, crbKey, &rbacv1.ClusterRoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("canonical network ClusterRoleBinding survived arbitrary-binding quarantine: %v", err)
	}
}

func sharedTransitionContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
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

	tracker := &generatedAccessDeleteOrderClient{Client: r.Client}
	r.Client = tracker
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
	if len(tracker.workloadDeletesWithoutUIDPrecondition) != 0 {
		t.Fatalf("lifecycle drain deleted a workload without observed UID precondition: %v",
			tracker.workloadDeletesWithoutUIDPrecondition)
	}
	if len(tracker.controllerDeletesWithoutForeground) != 0 {
		t.Fatalf("lifecycle drain deleted a controller without foreground propagation: %v",
			tracker.controllerDeletesWithoutForeground)
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
