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
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

func TestManagedWorkersUseSharedFunctionalServiceAccounts(t *testing.T) {
	device := newDevice("switch-owned-worker", "edge")
	device.UID = "device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(context.Background(), device); err != nil {
		t.Fatalf("ensureManagedSharedWorkerAccess: %v", err)
	}
	for _, identity := range []struct{ name, account string }{
		{r.appHostingServiceAccountName(), managedprotocol.WorkerModeAppHosting},
		{r.networkManagementServiceAccountName(), managedprotocol.WorkerModeNetworkManagement},
	} {
		var sa corev1.ServiceAccount
		key := types.NamespacedName{Namespace: device.Namespace, Name: identity.name}
		if err := r.Get(context.Background(), key, &sa); err != nil {
			t.Fatal(err)
		}
		if len(sa.OwnerReferences) != 0 || sa.Annotations[annotationSharedWorkerAccount] != identity.account {
			t.Fatalf("shared functional ServiceAccount is device-owned or incorrectly classified: %#v", sa.ObjectMeta)
		}
	}
	if got := r.serviceAccountForDevice(device); got != r.appHostingServiceAccountName() {
		t.Fatalf("durable managed app ServiceAccount=%q, want %q", got, r.appHostingServiceAccountName())
	}
}

func TestTopologyLegacyWorkerUsesOwnedIncarnationServiceAccount(t *testing.T) {
	device := newDevice("switch-legacy-worker", "edge")
	device.UID = "legacy-device-uid"
	deviceRole := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: vkDeviceClusterRole}, Rules: []rbacv1.PolicyRule{{
		APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list", "watch"},
	}}}
	legacyBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "test-sa"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole},
		Subjects:   exactWorkerSubject(device.Namespace, "test-sa"),
	}
	r := reconcilerFor(t, device, deviceRole, legacyBinding)
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
			assertWorkerAccessAbsent(t, r.Client, device, saName)
		})
	}
}

func TestGeneratedWorkerCompromiseQuarantineDeletesEveryDeviceNamespaceRoleBinding(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(context.Context, *CiscoDeviceReconciler, *ciskov1.CiscoDevice, string) error
		bindingName     func(string) string
		wantErrContains string
	}{
		{
			name: "canonical binding with unexpected role",
			mutate: func(ctx context.Context, r *CiscoDeviceReconciler, device *ciskov1.CiscoDevice, serviceAccount string) error {
				var binding rbacv1.RoleBinding
				if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: serviceAccount}, &binding); err != nil {
					return err
				}
				binding.RoleRef.Name = "attacker-selected-role"
				return r.Update(ctx, &binding)
			},
			bindingName:     func(serviceAccount string) string { return serviceAccount },
			wantErrContains: "generated worker RoleBinding is invalid",
		},
		{
			name: "arbitrary binding name",
			mutate: func(ctx context.Context, r *CiscoDeviceReconciler, device *ciskov1.CiscoDevice, serviceAccount string) error {
				return r.Create(ctx, &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "attacker-generated-worker-grant"},
					RoleRef: rbacv1.RoleRef{
						APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "attacker-selected-role",
					},
					Subjects: exactWorkerSubject(device.Namespace, serviceAccount),
				})
			},
			bindingName:     func(string) string { return "attacker-generated-worker-grant" },
			wantErrContains: "unexpected additive RoleBinding",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			device := newDevice("switch-generated-binding-quarantine", "edge")
			device.UID = "generated-binding-quarantine-device-uid"
			r := reconcilerFor(t, device)
			r.ManagedTopology = true
			serviceAccount := topologyLegacyWorkerServiceAccountName(device)
			if err := r.ensureVKAccess(ctx, device, serviceAccount, false, true); err != nil {
				t.Fatalf("seed generated access: %v", err)
			}
			if err := test.mutate(ctx, r, device, serviceAccount); err != nil {
				t.Fatal(err)
			}

			err := r.ensureVKAccess(ctx, device, serviceAccount, false, true)
			if err == nil || !strings.Contains(err.Error(), test.wantErrContains) ||
				!strings.Contains(err.Error(), "generated bindings were revoked") {
				t.Fatalf("generated binding compromise quarantine error=%v", err)
			}
			bindingKey := types.NamespacedName{Namespace: device.Namespace, Name: test.bindingName(serviceAccount)}
			if err := r.Get(ctx, bindingKey, &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
				t.Fatalf("generated worker RoleBinding %s survived compromise quarantine: %v", bindingKey, err)
			}
			assertWorkerAccessAbsent(t, r.Client, device, serviceAccount)
		})
	}
}

func TestGeneratedWorkerNamespaceRBACRiskRevokesAuthority(t *testing.T) {
	tests := []struct {
		name       string
		rule       func(*ciskov1.CiscoDevice, *ciskov1.CiscoDevice) rbacv1.PolicyRule
		capability string
	}{
		{
			name: "exact legacy identity impersonation",
			rule: func(device, _ *ciskov1.CiscoDevice) rbacv1.PolicyRule {
				return rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"serviceaccounts"},
					Verbs: []string{"impersonate"}, ResourceNames: []string{topologyLegacyWorkerServiceAccountName(device)}}
			},
			capability: "impersonation of reserved or generated worker ServiceAccount",
		},
		{
			name: "exact managed identity for namespace peer",
			rule: func(_, peer *ciskov1.CiscoDevice) rbacv1.PolicyRule {
				return rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"serviceaccounts"},
					Verbs: []string{"impersonate"}, ResourceNames: []string{managedWorkerServiceAccountName(peer)}}
			},
			capability: "impersonation of reserved or generated worker ServiceAccount",
		},
		{
			name: "wildcard identity impersonation",
			rule: func(_, _ *ciskov1.CiscoDevice) rbacv1.PolicyRule {
				return rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"serviceaccounts"},
					Verbs: []string{"impersonate"}, ResourceNames: []string{"*"}}
			},
			capability: "impersonation of reserved or generated worker ServiceAccount",
		},
		{
			name: "unscoped identity impersonation",
			rule: func(_, _ *ciskov1.CiscoDevice) rbacv1.PolicyRule {
				return rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"serviceaccounts"}, Verbs: []string{"impersonate"}}
			},
			capability: "impersonation of reserved or generated worker ServiceAccounts",
		},
		{
			name: "service account collection deletion",
			rule: func(_, _ *ciskov1.CiscoDevice) rbacv1.PolicyRule {
				return rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"serviceaccounts"}, Verbs: []string{"deletecollection"}}
			},
			capability: "collection deletion of ServiceAccounts",
		},
		{
			name: "wildcard verb includes collection deletion",
			rule: func(_, _ *ciskov1.CiscoDevice) rbacv1.PolicyRule {
				return rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"serviceaccounts"}, Verbs: []string{"*"}}
			},
			capability: "impersonation of reserved or generated worker ServiceAccounts",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			device := newDevice("switch-generated-risk", "edge")
			device.UID = "generated-risk-device-uid"
			peer := managedAccessDevice("switch-generated-peer")
			deviceRole := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: vkDeviceClusterRole}, Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list", "watch"},
			}}}
			r := reconcilerFor(t, device, peer, deviceRole)
			r.ManagedTopology = true
			serviceAccount := topologyLegacyWorkerServiceAccountName(device)
			if err := r.ensureVKAccess(ctx, device, serviceAccount, false, true); err != nil {
				t.Fatalf("seed generated access with canonical credential-reading RoleBinding: %v", err)
			}

			role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "generated-risk"},
				Rules: []rbacv1.PolicyRule{test.rule(device, peer)}}
			binding := namespacedRBACBinding(device.Namespace, "generated-risk", "Role", role.Name)
			if err := r.Create(ctx, role); err != nil {
				t.Fatal(err)
			}
			if err := r.Create(ctx, binding); err != nil {
				t.Fatal(err)
			}

			err := r.ensureVKAccess(ctx, device, serviceAccount, false, true)
			if err == nil || !strings.Contains(err.Error(), test.capability) ||
				!strings.Contains(err.Error(), "generated bindings were revoked") {
				t.Fatalf("generated namespace risk error=%v, want capability %q and revocation", err, test.capability)
			}
			assertWorkerAccessAbsent(t, r.Client, device, serviceAccount)
		})
	}
}

func TestGeneratedWorkerLegacyTokenRevokesAndDrains(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-generated-token", "edge")
	device.UID = "generated-token-device-uid"
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	serviceAccount := topologyLegacyWorkerServiceAccountName(device)
	if err := r.ensureVKAccess(ctx, device, serviceAccount, false, true); err != nil {
		t.Fatalf("seed generated access: %v", err)
	}
	labels := perDeviceDeploymentLabels(device.Name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: device.Name + deploymentSuffix, UID: "generated-deployment-uid",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice"))},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{ServiceAccountName: serviceAccount}},
		},
	}
	if err := r.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureVKAccess(ctx, device, serviceAccount, false, true); err != nil {
		t.Fatalf("exact generated workload was rejected by the pre-bind inventory: %v", err)
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "legacy-generated-token",
		Annotations: map[string]string{corev1.ServiceAccountNameKey: serviceAccount},
	}, Type: corev1.SecretTypeServiceAccountToken}
	if err := r.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}

	err := r.ensureVKAccess(ctx, device, serviceAccount, false, true)
	if err == nil || !strings.Contains(err.Error(), "long-lived ServiceAccount token Secret") ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("generated token quarantine error=%v", err)
	}
	assertWorkerAccessAbsent(t, r.Client, device, serviceAccount)
	if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("generated worker Deployment remains after token quarantine: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(secret), &corev1.Secret{}); err != nil {
		t.Fatalf("quarantine deleted operator-owned legacy token Secret: %v", err)
	}
}

func TestGeneratedWorkerRogueWorkloadBlocksPreBindAccess(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-generated-rogue", "edge")
	device.UID = "generated-rogue-device-uid"
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	serviceAccount := topologyLegacyWorkerServiceAccountName(device)
	rogue := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "unrelated-workload", UID: "rogue-deployment-uid"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rogue"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "rogue"}},
				Spec:       corev1.PodSpec{ServiceAccountName: serviceAccount},
			},
		},
	}
	rogueReplicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "unrelated-replicaset", UID: "rogue-rs-uid"},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{ServiceAccountName: serviceAccount},
		}},
	}
	roguePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "unrelated-pod", UID: "rogue-pod-uid"},
		Spec:       corev1.PodSpec{ServiceAccountName: serviceAccount},
	}
	for _, object := range []client.Object{rogue, rogueReplicaSet, roguePod} {
		if err := r.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	tracker := &generatedAccessDeleteOrderClient{Client: r.Client}
	r.Client = tracker
	err := r.ensureVKAccess(ctx, device, serviceAccount, false, true)
	if err == nil || !strings.Contains(err.Error(), "pre-existing Deployment") ||
		!strings.Contains(err.Error(), "lacks exact CiscoDevice provenance") {
		t.Fatalf("rogue generated workload error=%v", err)
	}
	assertWorkerAccessAbsent(t, r.Client, device, serviceAccount)
	for _, object := range []client.Object{rogue, rogueReplicaSet, roguePod} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(object), object.DeepCopyObject().(client.Object)); !apierrors.IsNotFound(err) {
			t.Fatalf("quarantine retained unproven %T using reserved account: %v", object, err)
		}
	}
	if len(tracker.workloadDeletesWithoutUIDPrecondition) != 0 {
		t.Fatalf("quarantine issued workload deletes without exact UID preconditions: %v",
			tracker.workloadDeletesWithoutUIDPrecondition)
	}
	if len(tracker.controllerDeletesWithoutForeground) != 0 {
		t.Fatalf("quarantine issued controller deletes without foreground propagation: %v",
			tracker.controllerDeletesWithoutForeground)
	}
}

type generatedAccessDeleteOrderClient struct {
	client.Client
	order                                 []string
	workloadDeletesWithoutUIDPrecondition []string
	controllerDeletesWithoutForeground    []string
}

func (c *generatedAccessDeleteOrderClient) Delete(ctx context.Context, object client.Object,
	opts ...client.DeleteOption) error {
	c.order = append(c.order, fmt.Sprintf("%T:%s", object, client.ObjectKeyFromObject(object)))
	switch object.(type) {
	case *appsv1.Deployment, *appsv1.ReplicaSet, *corev1.Pod:
		options := &client.DeleteOptions{}
		for _, option := range opts {
			option.ApplyToDelete(options)
		}
		if object.GetUID() == "" || options.Preconditions == nil || options.Preconditions.UID == nil ||
			*options.Preconditions.UID != object.GetUID() {
			c.workloadDeletesWithoutUIDPrecondition = append(c.workloadDeletesWithoutUIDPrecondition,
				fmt.Sprintf("%T:%s", object, client.ObjectKeyFromObject(object)))
		}
		switch object.(type) {
		case *appsv1.Deployment, *appsv1.ReplicaSet:
			if options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationForeground {
				c.controllerDeletesWithoutForeground = append(c.controllerDeletesWithoutForeground,
					fmt.Sprintf("%T:%s", object, client.ObjectKeyFromObject(object)))
			}
		}
	}
	return c.Client.Delete(ctx, object, opts...)
}

func TestGeneratedWorkerPolicyEpochRotatesHiddenTokenAndCanonicalWorkload(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-generated-epoch", "edge")
	device.UID = "generated-epoch-device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "generated-epoch-node-uid",
	}
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	prepareManagedAccessSettlement(t, r)
	serviceAccount := topologyLegacyWorkerServiceAccountName(device)
	if err := r.ensureVKAccess(ctx, device, serviceAccount, false, true); err != nil {
		t.Fatalf("seed pre-upgrade generated access: %v", err)
	}

	labels := perDeviceDeploymentLabels(device.Name)
	canonicalButMalicious := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: device.Name + deploymentSuffix, UID: "pre-policy-deployment-uid",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice"))},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{ServiceAccountName: serviceAccount, Containers: []corev1.Container{{
					Name: "attacker", Image: "example.invalid/pre-policy-attacker:latest",
				}}},
			},
		},
	}
	if err := r.Create(ctx, canonicalButMalicious); err != nil {
		t.Fatal(err)
	}
	// Kubernetes 1.35 may continue accepting the signed legacy JWT after the
	// mutable annotation is changed to a different live, non-reserved account;
	// legacy authentication uses the JWT claims and bytes, not that annotation.
	// Policy-generation rotation invalidates it through the old claimed SA UID.
	decoy := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "token-audit-decoy", UID: "token-audit-decoy-uid",
	}}
	if err := r.Create(ctx, decoy); err != nil {
		t.Fatal(err)
	}
	hiddenToken := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "annotation-disguised-generated-token",
		Annotations: map[string]string{
			corev1.ServiceAccountNameKey: decoy.Name,
			corev1.ServiceAccountUIDKey:  string(decoy.UID),
		},
	}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{"token": []byte("previously-signed-jwt")}}
	if err := r.Create(ctx, hiddenToken); err != nil {
		t.Fatal(err)
	}

	tracker := &generatedAccessDeleteOrderClient{Client: r.Client}
	r.Client = tracker
	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"
	err := r.ensureVKAccess(ctx, device, serviceAccount, false, true)
	if err == nil || !strings.Contains(err.Error(), "requires UID rotation") ||
		!strings.Contains(err.Error(), "worker processes were quiesced") {
		t.Fatalf("policy-epoch rotation error=%v", err)
	}
	assertWorkerAccessAbsent(t, r.Client, device, serviceAccount)
	if len(tracker.order) < 4 || !strings.HasPrefix(tracker.order[0], "*v1.ClusterRoleBinding:") ||
		!strings.HasPrefix(tracker.order[1], "*v1.RoleBinding:") ||
		!strings.HasPrefix(tracker.order[2], "*v1.ServiceAccount:") ||
		!strings.HasPrefix(tracker.order[3], "*v1.Deployment:") {
		t.Fatalf("epoch quarantine delete order=%v, want CRB, RB, SA, then Deployment", tracker.order)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(canonicalButMalicious), &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("canonical-shape pre-policy workload survived epoch rotation: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(hiddenToken), &corev1.Secret{}); err != nil {
		t.Fatalf("epoch rotation mutated annotation-disguised token Secret: %v", err)
	}

	// With the old workload plane gone, recreation stamps the verified epoch.
	if err := r.ensureVKAccess(ctx, device, serviceAccount, false, true); err != nil {
		t.Fatalf("recreate generated access after epoch rotation: %v", err)
	}
	var rotated corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: serviceAccount}, &rotated); err != nil {
		t.Fatal(err)
	}
	if got := rotated.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy]; got != r.WorkerServiceAccountPolicyEpoch {
		t.Fatalf("rotated ServiceAccount policy epoch=%q, want %q", got, r.WorkerServiceAccountPolicyEpoch)
	}
}

func TestGeneratedWorkerPolicyEpochWaitsForActiveMutation(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-generated-epoch-active", "edge")
	device.UID = "generated-epoch-active-device-uid"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "generated-epoch-active-node-uid",
	}
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	serviceAccount := managedWorkerServiceAccountName(device)
	if err := r.ensureVKAccess(ctx, device, serviceAccount, true); err != nil {
		t.Fatalf("seed pre-upgrade generated access: %v", err)
	}
	deployment, _, _ := managedWorkerObjects(device, managedprotocol.WorkerModeAppHosting, serviceAccount)
	if err := r.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase: ciskov1.DeviceMaintenanceSessionActive, SessionToken: "active-gnoi-session",
	}

	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"
	err := r.ensureVKAccess(ctx, device, serviceAccount, true)
	if err == nil || !strings.Contains(err.Error(), "planned generated worker policy-epoch rotation is blocked") ||
		!strings.Contains(err.Error(), "unresolved maintenance session") {
		t.Fatalf("active mutation epoch transition error=%v", err)
	}
	var retained corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: serviceAccount}, &retained); err != nil {
		t.Fatalf("planned rotation revoked generated account during active mutation: %v", err)
	}
	if got := retained.Annotations[managedprotocol.AnnotationWorkerServiceAccountPolicy]; got != "sha256:old-policy-and-binding-generation" {
		t.Fatalf("planned rotation changed generated epoch during active mutation: %q", got)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), &appsv1.Deployment{}); err != nil {
		t.Fatalf("planned rotation interrupted worker during active mutation: %v", err)
	}
	for _, object := range []client.Object{
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: vkAccessClusterRoleBindingName(device.Namespace, serviceAccount)}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: serviceAccount}},
	} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(object), object); err != nil {
			t.Fatalf("planned rotation revoked generated grant during active mutation: %T %v", object, err)
		}
	}
}

func TestPhaseZeroGeneratedPolicyEpochRequiresExplicitWorkloadQuiescence(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-phase-zero-epoch", "edge")
	device.UID = "phase-zero-epoch-device-uid"
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	r.WorkerServiceAccountPolicyEpoch = "sha256:old-policy-and-binding-generation"
	serviceAccount := topologyLegacyWorkerServiceAccountName(device)
	if err := r.ensureVKAccess(ctx, device, serviceAccount, false, true); err != nil {
		t.Fatalf("seed phase-zero generated access: %v", err)
	}
	deployment, _, _ := managedWorkerObjects(device, managedprotocol.WorkerModeAppHosting, serviceAccount)
	if err := r.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}

	r.WorkerServiceAccountPolicyEpoch = "sha256:new-policy-and-binding-generation"
	err := r.ensureVKAccess(ctx, device, serviceAccount, false, true)
	if err == nil || !strings.Contains(err.Error(), "phase-zero legacy worker") ||
		!strings.Contains(err.Error(), "remove its worker workload") {
		t.Fatalf("phase-zero epoch transition error=%v", err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: serviceAccount},
		&corev1.ServiceAccount{}); err != nil {
		t.Fatalf("phase-zero rotation revoked account before explicit quiescence: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), &appsv1.Deployment{}); err != nil {
		t.Fatalf("phase-zero rotation deleted workload without an authority proof: %v", err)
	}

	if err := r.Delete(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	err = r.ensureVKAccess(ctx, device, serviceAccount, false, true)
	if err == nil || !strings.Contains(err.Error(), "requires UID rotation") {
		t.Fatalf("quiesced phase-zero account did not enter UID rotation: %v", err)
	}
	assertWorkerAccessAbsent(t, r.Client, device, serviceAccount)
}

func TestGeneratedWorkerCleanupRevokesExactObjectsAndPreservesForeignBindingCollision(t *testing.T) {
	device := newDevice("switch-binding-collision", "edge")
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
	key := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, saName)}
	var binding rbacv1.ClusterRoleBinding
	if err := r.Get(ctx, key, &binding); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, &binding); err != nil {
		t.Fatal(err)
	}
	foreign := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: key.Name, UID: "foreign-binding-uid"},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole},
		Subjects: exactWorkerSubject(device.Namespace, saName)}
	if err := r.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	err := r.cleanupVKClusterAccess(ctx, device, saName)
	if err == nil || !strings.Contains(err.Error(), "retain drifted canonical generated ClusterRoleBinding") {
		t.Fatalf("cleanup collision error=%v", err)
	}
	for _, object := range []client.Object{
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: saName}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: saName}},
	} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(object), object); !apierrors.IsNotFound(err) {
			t.Fatalf("cleanup retained exact canonical %T after collision quarantine: %v", object, err)
		}
	}
	var retained rbacv1.ClusterRoleBinding
	if err := r.Get(ctx, key, &retained); err != nil || retained.UID != foreign.UID {
		t.Fatalf("cleanup did not retain foreign canonical-name collision: binding=%#v err=%v", retained, err)
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
	if err == nil || !strings.Contains(err.Error(), "not exactly bound") {
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
				if err == nil || !strings.Contains(err.Error(), "foreign or drifted canonical generated ServiceAccount") {
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
	objects := []client.Object{device, node, policy, ledger}
	for name, rules := range managedprotocol.WorkerClusterRoleContracts() {
		objects = append(objects, &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: rules,
		})
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &corev1.Node{}).
		WithIndex(&corev1.Pod{}, podNodeNameIndex, func(object client.Object) []string {
			pod := object.(*corev1.Pod)
			if pod.Spec.NodeName == "" {
				return nil
			}
			return []string{pod.Spec.NodeName}
		}).
		WithObjects(objects...).Build()
	r := &CiscoDeviceReconciler{
		Client: leaseUIDAssigningClient{Client: apiClient}, APIReader: apiClient, Scheme: scheme, Image: "cisco-vk:test",
		ManagedTopology: true, TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		WorkerServiceAccountPolicyEpoch: testWorkerServiceAccountPolicyEpoch,
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
	var sa corev1.ServiceAccount
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: r.appHostingServiceAccountName()}, &sa); err != nil {
		t.Fatal(err)
	}
	if len(sa.OwnerReferences) != 0 || sa.Annotations[annotationSharedWorkerAccount] != managedprotocol.WorkerModeAppHosting {
		t.Fatal("managed reconcile did not provision the shared app-hosting ServiceAccount")
	}
	var networkDeployment appsv1.Deployment
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: networkDeploymentName(string(device.UID))}, &networkDeployment); err != nil {
		t.Fatal(err)
	}
	if networkDeployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("network Deployment strategy=%q, want Recreate", networkDeployment.Spec.Strategy.Type)
	}
	container := networkDeployment.Spec.Template.Spec.Containers[0]
	if container.ReadinessProbe == nil || container.ReadinessProbe.HTTPGet == nil ||
		container.ReadinessProbe.HTTPGet.Path != "/readyz" || container.LivenessProbe == nil ||
		container.LivenessProbe.HTTPGet == nil || container.LivenessProbe.HTTPGet.Path != "/healthz" {
		t.Fatalf("network worker health probes are incomplete: readiness=%#v liveness=%#v",
			container.ReadinessProbe, container.LivenessProbe)
	}
}

func TestPhaseZeroGeneratedLegacyAccessRecordsUIDMarker(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "true")
	ctx := context.Background()
	device := newDevice("switch-phase-zero-marker", "edge")
	device.UID = "phase-zero-marker-device-uid"
	device.Spec.PhysicalIdentity = "serial-phase-zero-marker"
	device.Labels = map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		topology.CiscoTopologyLabelPrefix + "site": "site-a",
	}
	policy, ledger := managedPolicyAndLedger(t, nil)
	sharedBinding := sharedDynamicClusterRoleBinding(device.Namespace, DefaultServiceAccount)

	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	objects := []client.Object{device, policy, ledger, sharedBinding}
	for name, rules := range managedprotocol.WorkerClusterRoleContracts() {
		objects = append(objects, &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: rules,
		})
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &corev1.Node{}).
		WithIndex(&corev1.Pod{}, podNodeNameIndex, func(object client.Object) []string {
			pod := object.(*corev1.Pod)
			if pod.Spec.NodeName == "" {
				return nil
			}
			return []string{pod.Spec.NodeName}
		}).
		WithObjects(objects...).Build()
	r := &CiscoDeviceReconciler{
		Client: leaseUIDAssigningClient{Client: apiClient}, APIReader: apiClient,
		Scheme: scheme, Image: "cisco-vk:test", ManagedTopology: true,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		WorkerServiceAccountPolicyEpoch: testWorkerServiceAccountPolicyEpoch,
	}
	if _, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name)); err != nil {
		t.Fatalf("phase-zero Reconcile: %v", err)
	}
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if marker := current.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]; marker != string(device.UID) {
		t.Fatalf("phase-zero marker=%q, want device UID %q", marker, device.UID)
	}
	present, err := r.isolatedLegacyWorkerAccessPresent(ctx, &current)
	if err != nil || !present {
		t.Fatalf("phase-zero generated access present=%v, err=%v", present, err)
	}
}

func TestPhaseZeroDeletionRevokesExactGeneratedAccessAndRejectsDrift(t *testing.T) {
	tests := []struct {
		name             string
		mutate           func(*testing.T, context.Context, *CiscoDeviceReconciler, *ciskov1.CiscoDevice, string)
		wantErrContains  string
		retainedCRB      bool
		retainedAdditive string
	}{
		{name: "complete crash-window identity"},
		{
			name: "partial exact ServiceAccount and RoleBinding",
			mutate: func(t *testing.T, ctx context.Context, r *CiscoDeviceReconciler, device *ciskov1.CiscoDevice, serviceAccount string) {
				t.Helper()
				binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{
					Name: vkAccessClusterRoleBindingName(device.Namespace, serviceAccount),
				}}
				if err := r.Delete(ctx, binding); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "drifted ClusterRoleBinding",
			mutate: func(t *testing.T, ctx context.Context, r *CiscoDeviceReconciler, device *ciskov1.CiscoDevice, serviceAccount string) {
				t.Helper()
				var binding rbacv1.ClusterRoleBinding
				key := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, serviceAccount)}
				if err := r.Get(ctx, key, &binding); err != nil {
					t.Fatal(err)
				}
				binding.Annotations[managedprotocol.AnnotationDeviceUID] = "another-device-incarnation"
				if err := r.Update(ctx, &binding); err != nil {
					t.Fatal(err)
				}
			},
			wantErrContains: "existing generated worker ClusterRoleBinding is invalid",
			retainedCRB:     true,
		},
		{
			name: "additive RoleBinding cannot pin broad authority",
			mutate: func(t *testing.T, ctx context.Context, r *CiscoDeviceReconciler, device *ciskov1.CiscoDevice, serviceAccount string) {
				t.Helper()
				binding := &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "attacker-added-benign-binding"},
					RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "view"},
					Subjects:   exactWorkerSubject(device.Namespace, serviceAccount),
				}
				if err := r.Create(ctx, binding); err != nil {
					t.Fatal(err)
				}
			},
			wantErrContains:  "unexpected additive RoleBinding",
			retainedAdditive: "attacker-added-benign-binding",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			device := newDevice("switch-delete-phase-zero", "edge")
			device.UID = "phase-zero-delete-device-uid"
			device.Finalizers = []string{ciscoDeviceFinalizer}
			r := reconcilerFor(t, device)
			r.ManagedTopology = true
			serviceAccount := topologyLegacyWorkerServiceAccountName(device)
			if err := r.ensureVKAccess(ctx, device, serviceAccount, false, true); err != nil {
				t.Fatalf("create phase-zero access: %v", err)
			}
			if test.mutate != nil {
				test.mutate(t, ctx, r, device, serviceAccount)
			}

			var deleting ciskov1.CiscoDevice
			if err := r.Get(ctx, client.ObjectKeyFromObject(device), &deleting); err != nil {
				t.Fatal(err)
			}
			if marker := deleting.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]; marker != "" {
				t.Fatalf("crash-window fixture unexpectedly has marker %q", marker)
			}
			if err := r.Delete(ctx, &deleting); err != nil {
				t.Fatal(err)
			}
			_, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name))
			if test.wantErrContains != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrContains) {
					t.Fatalf("drifted phase-zero deletion error=%v", err)
				}
				if err := r.Get(ctx, client.ObjectKeyFromObject(device), &deleting); err != nil {
					t.Fatalf("drifted CiscoDevice was deleted: %v", err)
				}
				if len(deleting.Finalizers) != 1 || deleting.Finalizers[0] != ciscoDeviceFinalizer {
					t.Fatalf("drifted CiscoDevice finalizers=%v", deleting.Finalizers)
				}
				for _, object := range []client.Object{
					&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: serviceAccount}},
					&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: serviceAccount}},
				} {
					if err := r.Get(ctx, client.ObjectKeyFromObject(object), object); !apierrors.IsNotFound(err) {
						t.Fatalf("phase-zero cleanup retained exact canonical %T after failure: %v", object, err)
					}
				}
				canonicalCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{
					Name: vkAccessClusterRoleBindingName(device.Namespace, serviceAccount),
				}}
				getCRBErr := r.Get(ctx, client.ObjectKeyFromObject(canonicalCRB), canonicalCRB)
				if test.retainedCRB && getCRBErr != nil {
					t.Fatalf("drifted canonical ClusterRoleBinding was deleted: %v", getCRBErr)
				}
				if !test.retainedCRB && !apierrors.IsNotFound(getCRBErr) {
					t.Fatalf("exact broad ClusterRoleBinding survived additive-object cleanup: %v", getCRBErr)
				}
				if test.retainedAdditive != "" {
					var additive rbacv1.RoleBinding
					if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: test.retainedAdditive}, &additive); err != nil {
						t.Fatalf("operator-owned additive RoleBinding was deleted: %v", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("delete phase-zero device: %v", err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(device), &deleting); !apierrors.IsNotFound(err) {
				t.Fatalf("phase-zero CiscoDevice remains after finalizer cleanup: %v", err)
			}
			assertWorkerAccessAbsent(t, r.Client, device, serviceAccount)
		})
	}
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

func TestTopologyDisabledManagedDeletionPreservesSharedWorkerAccessForPeers(t *testing.T) {
	for _, peerDeleting := range []bool{false, true} {
		name := "active-peer"
		if peerDeleting {
			name = "deleting-peer-with-live-workers"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			r, device := managedDeletionFixture(t, func(*ciskov1.CiscoDevice, *coordv1.Lease,
				*opsv1alpha1.IOSXESoftwareUpgrade, *topologyrollout.Ledger) {
			})
			for role, rules := range managedprotocol.WorkerClusterRoleContracts() {
				if err := r.Create(ctx, &rbacv1.ClusterRole{
					ObjectMeta: metav1.ObjectMeta{Name: role}, Rules: rules,
				}); err != nil {
					t.Fatal(err)
				}
			}

			peer := managedAccessDevice("switch-delete-peer")
			if peerDeleting {
				peer.Finalizers = []string{ciscoDeviceFinalizer}
			}
			if err := r.Create(ctx, peer); err != nil {
				t.Fatal(err)
			}
			r.ManagedTopology = true
			if err := r.ensureManagedSharedWorkerAccess(ctx, device); err != nil {
				t.Fatalf("provision shared functional access: %v", err)
			}

			appUsername := "system:serviceaccount:" + device.Namespace + ":" + r.appHostingServiceAccountName()
			networkUsername := "system:serviceaccount:" + device.Namespace + ":" + r.networkManagementServiceAccountName()
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: device.Status.NodeIdentity.NodeName, UID: types.UID(device.Status.NodeIdentity.NodeUID),
				Annotations: map[string]string{
					managedprotocol.AnnotationManaged:               "true",
					managedprotocol.AnnotationDeviceNamespace:       device.Namespace,
					managedprotocol.AnnotationDeviceName:            device.Name,
					managedprotocol.AnnotationDeviceUID:             string(device.UID),
					managedprotocol.AnnotationNodeUID:               device.Status.NodeIdentity.NodeUID,
					managedprotocol.AnnotationWorkerUsername:        appUsername,
					managedprotocol.AnnotationAppWorkerUsername:     appUsername,
					managedprotocol.AnnotationNetworkWorkerUsername: networkUsername,
					managedprotocol.AnnotationWorkerProtocol:        managedprotocol.Version,
				},
			}}
			if err := r.Create(ctx, node); err != nil {
				t.Fatal(err)
			}
			deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
			var mutationLease coordv1.Lease
			if err := r.Get(ctx, types.NamespacedName{
				Namespace: device.Namespace,
				Name:      configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily),
			}, &mutationLease); err != nil {
				t.Fatal(err)
			}
			mutationLease.Annotations[managedprotocol.AnnotationWorkerUsername] = networkUsername
			mutationLease.Annotations[managedprotocol.AnnotationNetworkWorkerUsername] = networkUsername
			if err := r.Update(ctx, &mutationLease); err != nil {
				t.Fatal(err)
			}

			workerDeployment := func(owner *ciskov1.CiscoDevice, name, account string) *appsv1.Deployment {
				return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
					Namespace: owner.Namespace, Name: name,
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(owner, ciskov1.GroupVersion.WithKind("CiscoDevice")),
					},
				}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{ServiceAccountName: account},
				}}}
			}
			for _, owner := range []*ciskov1.CiscoDevice{device, peer} {
				for _, worker := range []struct{ name, account string }{
					{owner.Name + deploymentSuffix, r.appHostingServiceAccountName()},
					{networkDeploymentName(string(owner.UID)), r.networkManagementServiceAccountName()},
				} {
					if err := r.Create(ctx, workerDeployment(owner, worker.name, worker.account)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if peerDeleting {
				var currentPeer ciskov1.CiscoDevice
				if err := r.Get(ctx, client.ObjectKeyFromObject(peer), &currentPeer); err != nil {
					t.Fatal(err)
				}
				if err := r.Delete(ctx, &currentPeer); err != nil {
					t.Fatal(err)
				}
			}

			var current ciskov1.CiscoDevice
			if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
				t.Fatal(err)
			}
			current.Finalizers = append(current.Finalizers, ciscoDeviceFinalizer)
			if err := r.Update(ctx, &current); err != nil {
				t.Fatal(err)
			}
			if err := r.Delete(ctx, &current); err != nil {
				t.Fatal(err)
			}

			// Simulate a restart with managed topology disabled. Durable status,
			// rather than the current flag, must select the shared cleanup path.
			r.ManagedTopology = false
			deleted := false
			for attempt := 0; attempt < 4; attempt++ {
				_, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name))
				if err != nil {
					t.Fatalf("topology-disabled deletion reconcile %d: %v", attempt+1, err)
				}
				if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); apierrors.IsNotFound(err) {
					deleted = true
					break
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if !deleted {
				t.Fatal("topology-disabled managed deletion did not complete")
			}

			for _, identity := range []struct{ account, binding string }{
				{r.appHostingServiceAccountName(), vkAccessClusterRoleBindingName(device.Namespace, r.appHostingServiceAccountName())},
				{r.networkManagementServiceAccountName(), vkAccessClusterRoleBindingName(device.Namespace, r.networkManagementServiceAccountName()+"-global-read")},
			} {
				if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: identity.account}, &corev1.ServiceAccount{}); err != nil {
					t.Fatalf("shared ServiceAccount %s was disrupted: %v", identity.account, err)
				}
				if err := r.Get(ctx, types.NamespacedName{Name: identity.binding}, &rbacv1.ClusterRoleBinding{}); err != nil {
					t.Fatalf("shared ClusterRoleBinding %s was disrupted: %v", identity.binding, err)
				}
				if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: identity.account}, &rbacv1.RoleBinding{}); err != nil {
					t.Fatalf("shared RoleBinding %s was disrupted: %v", identity.account, err)
				}
			}
			for _, names := range [][2]string{
				{device.Name + deploymentSuffix, peer.Name + deploymentSuffix},
				{networkDeploymentName(string(device.UID)), networkDeploymentName(string(peer.UID))},
			} {
				if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: names[0]}, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
					t.Fatalf("deleted device worker %s remains: %v", names[0], err)
				}
				if err := r.Get(ctx, types.NamespacedName{Namespace: peer.Namespace, Name: names[1]}, &appsv1.Deployment{}); err != nil {
					t.Fatalf("peer worker %s was disrupted: %v", names[1], err)
				}
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
		WithIndex(&corev1.Pod{}, podNodeNameIndex, func(object client.Object) []string {
			pod := object.(*corev1.Pod)
			if pod.Spec.NodeName == "" {
				return nil
			}
			return []string{pod.Spec.NodeName}
		}).
		WithObjects(device, lease, leaf, policy, ledgerCM).Build()
	r := &CiscoDeviceReconciler{
		Client: apiClient, APIReader: apiClient, Scheme: scheme,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		LeaseNamespace: device.Namespace, WorkerServiceAccountPolicyEpoch: testWorkerServiceAccountPolicyEpoch,
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
		Version:                             topologyrollout.PolicyVersion,
		AppHostingServiceAccountName:        managedprotocol.AppHostingServiceAccount,
		NetworkManagementServiceAccountName: managedprotocol.NetworkManagementServiceAccount,
		FleetSelector:                       metav1.LabelSelector{MatchLabels: map[string]string{managedprotocol.AnnotationManaged: "true"}},
		RequiredTopologyKeys:                []string{topology.CiscoTopologyLabelPrefix + "site"},
		ProjectedTopologyKeys:               []string{topology.CiscoTopologyLabelPrefix + "site"},
		GlobalMaxConcurrentTransfers:        1,
		GlobalMaxUnavailable:                1,
		DomainMaxConcurrentTransfers:        map[string]int{topology.CiscoTopologyLabelPrefix + "site": 1},
		DomainMaxUnavailable:                map[string]int{topology.CiscoTopologyLabelPrefix + "site": 1},
		HealthFreshnessSeconds:              120,
		MaxCampaignTargets:                  10,
		MaxActiveReservations:               32,
		MaxLedgerBytes:                      64 * 1024,
		LedgerName:                          ledgerName,
	}
	data, err := topologyrollout.CanonicalPolicyJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: "topology-policy", UID: "policy-uid", ResourceVersion: "1",
		Annotations: map[string]string{
			topologyrollout.PolicyManagedAnnotation:        "true",
			topologyrollout.LedgerUIDAnnotation:            ledgerUID,
			topologyrollout.AdmissionPrefixAnnotation:      "cvk-topology",
			topologyrollout.ConfigLeaseNamespaceAnnotation: cfg.ConfigLeaseNamespace,
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
