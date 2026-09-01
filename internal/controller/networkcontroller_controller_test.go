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
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	controlleradapter "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

const testNetworkControllerType = "foundation-test-controller"

type foundationTestAdapter struct{}

func (*foundationTestAdapter) SetupWithManager(ctrl.Manager) error { return nil }

var registerFoundationTestController sync.Once

func ensureFoundationTestControllerRegistered() {
	registerFoundationTestController.Do(func() {
		controlleradapter.Register(controlleradapter.Registration{
			Descriptor: controlleradapter.Descriptor{
				Type:        testNetworkControllerType,
				DisplayName: "Foundation Test Controller",
				NetAsCode: ciskov1.NetworkControllerNetAsCodeStatus{
					Format:        "netascode-foundation-test",
					Stripe:        "foundation_test",
					ModelVersions: []string{"2.0", "1.0"},
					Sections:      []string{"sites", "inventory"},
				},
				Capabilities:      []string{"inventory", "config"},
				WorkerClusterRole: controlleradapter.DefaultWorkerClusterRole,
			},
			Factory: func(controlleradapter.Options) (controlleradapter.Adapter, error) {
				return &foundationTestAdapter{}, nil
			},
		})
	})
}

func TestNetworkControllerReconcileCreatesIsolatedWorker(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("primary", testNetworkControllerType)
	intentConfig := &configv1alpha1.NetworkControllerConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "campus-intent", Namespace: controller.Namespace},
		Spec: configv1alpha1.NetworkControllerConfigSpec{
			ControllerRef: configv1alpha1.NetworkControllerRef{Name: controller.Name},
			SecretRefs: []configv1alpha1.NetworkControllerSecretRef{{
				Section: "sites",
				Path:    "/credentials/password",
				Source:  "site-credentials",
			}},
		},
	}
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller, intentConfig)
	reconcileNetworkController(t, reconciler, controller)

	workerName := networkControllerWorkerName(controller.Name)
	key := client.ObjectKey{Namespace: controller.Namespace, Name: workerName}
	var configMap corev1.ConfigMap
	if err := kubeClient.Get(context.Background(), key, &configMap); err != nil {
		t.Fatalf("get worker ConfigMap: %v", err)
	}
	if configMap.Immutable == nil || !*configMap.Immutable {
		t.Fatalf("worker bootstrap ConfigMap must be immutable: %+v", configMap.Immutable)
	}
	bootstrap := configMap.Data[controllerWorkerConfigFileName]
	for _, forbidden := range []string{controller.Spec.Endpoint, controller.Spec.CredentialSecretRef.Name, "site-credentials-secret", "radius-password", "password", "token"} {
		if strings.Contains(bootstrap, forbidden) {
			t.Fatalf("bootstrap ConfigMap contains forbidden value %q:\n%s", forbidden, bootstrap)
		}
	}
	for _, required := range []string{controller.Namespace, controller.Name, testNetworkControllerType} {
		if !strings.Contains(bootstrap, required) {
			t.Fatalf("bootstrap ConfigMap missing %q:\n%s", required, bootstrap)
		}
	}

	var serviceAccount corev1.ServiceAccount
	if err := kubeClient.Get(context.Background(), key, &serviceAccount); err != nil {
		t.Fatalf("get worker ServiceAccount: %v", err)
	}
	var roleBinding rbacv1.RoleBinding
	if err := kubeClient.Get(context.Background(), key, &roleBinding); err != nil {
		t.Fatalf("get worker RoleBinding: %v", err)
	}
	if roleBinding.RoleRef.Kind != "ClusterRole" || roleBinding.RoleRef.Name != controlleradapter.DefaultWorkerClusterRole {
		t.Fatalf("RoleBinding roleRef=%+v", roleBinding.RoleRef)
	}
	if len(roleBinding.Subjects) != 1 || roleBinding.Subjects[0].Name != serviceAccount.Name || roleBinding.Subjects[0].Namespace != controller.Namespace {
		t.Fatalf("RoleBinding subjects=%+v", roleBinding.Subjects)
	}

	var deployment appsv1.Deployment
	if err := kubeClient.Get(context.Background(), key, &deployment); err != nil {
		t.Fatalf("get worker Deployment: %v", err)
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 1 {
		t.Fatalf("Deployment replicas=%v, want 1", deployment.Spec.Replicas)
	}
	if deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("Deployment strategy=%q, want Recreate", deployment.Spec.Strategy.Type)
	}
	if deployment.Spec.Template.Spec.ServiceAccountName != serviceAccount.Name {
		t.Fatalf("Deployment serviceAccount=%q, want %q", deployment.Spec.Template.Spec.ServiceAccountName, serviceAccount.Name)
	}
	if deployment.Spec.Template.Spec.Affinity == nil || deployment.Spec.Template.Spec.Affinity.NodeAffinity == nil {
		t.Fatal("Deployment does not exclude virtual-kubelet nodes")
	}
	if len(deployment.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("Deployment containers=%d, want 1", len(deployment.Spec.Template.Spec.Containers))
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if container.Image != "example.test/cisco-vk:controller-foundation" {
		t.Fatalf("worker image=%q", container.Image)
	}
	if container.ImagePullPolicy != corev1.PullAlways {
		t.Fatalf("worker imagePullPolicy=%q, want Always", container.ImagePullPolicy)
	}
	if len(container.Env) != 0 {
		t.Fatalf("worker must not receive literal or Secret env vars: %+v", container.Env)
	}
	if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation || container.SecurityContext.ReadOnlyRootFilesystem == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatalf("worker container security context=%+v", container.SecurityContext)
	}
	if container.SecurityContext.Capabilities == nil || len(container.SecurityContext.Capabilities.Drop) != 1 || container.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("worker capabilities=%+v", container.SecurityContext.Capabilities)
	}
	assertReadOnlyMount(t, container.VolumeMounts, "credentials", controlleradapter.DefaultCredentialPath)
	assertReadOnlyMount(t, container.VolumeMounts, "controller-ca", controlleradapter.DefaultCADirectory)
	for _, arg := range []string{
		"--controller-namespace=" + controller.Namespace,
		"--controller-name=" + controller.Name,
		"--controller-uid=" + string(controller.UID),
		"--controller-generation=1",
		"--controller-type=" + string(controller.Spec.Type),
	} {
		if !containsString(container.Args, arg) {
			t.Fatalf("worker args missing immutable identity %q: %v", arg, container.Args)
		}
	}
	descriptor, ok := controlleradapter.DescriptorFor(testNetworkControllerType)
	if !ok || !containsString(container.Args, "--controller-descriptor-digest="+controlleradapter.DescriptorDigest(descriptor)) {
		t.Fatalf("worker args missing descriptor identity: %v", container.Args)
	}
	assertSecretVolume(t, deployment.Spec.Template.Spec.Volumes, "credentials", controller.Spec.CredentialSecretRef.Name)
	expectedIntentPath, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{
		ConfigName:  intentConfig.Name,
		Section:     "sites",
		JSONPointer: "/credentials/password",
		SourceAlias: "site-credentials",
		SecretName:  "site-credentials-secret",
		SecretKey:   "radius-password",
	})
	if err != nil {
		t.Fatalf("derive expected intent Secret path: %v", err)
	}
	assertProjectedSecretVolume(t, deployment.Spec.Template.Spec.Volumes, "intent-secrets", "site-credentials-secret", "radius-password", expectedIntentPath)
	assertReadOnlyMount(t, container.VolumeMounts, "intent-secrets", controlleradapter.DefaultIntentSecretPath)
	if got := deployment.Spec.Template.Annotations["cisco.vk/controller-contract-hash"]; len(got) != 64 {
		t.Fatalf("controller contract hash=%q, want 64 hex characters", got)
	}
	for _, object := range []metav1.Object{&configMap, &serviceAccount, &roleBinding, &deployment} {
		assertExplicitWorkerOwnership(t, object, controller)
	}

	var updated ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &updated); err != nil {
		t.Fatalf("get NetworkController status: %v", err)
	}
	if updated.Status.Phase != ciskov1.NetworkControllerPhasePending || updated.Status.Worker == nil || updated.Status.Worker.DeploymentName != workerName {
		t.Fatalf("NetworkController status=%+v", updated.Status)
	}
	if got := meta.FindStatusCondition(updated.Status.Conditions, ciskov1.NetworkControllerConditionAdapterAvailable); got == nil || got.Status != metav1.ConditionTrue {
		t.Fatalf("AdapterAvailable condition=%+v", got)
	}
	if got := meta.FindStatusCondition(updated.Status.Conditions, ciskov1.NetworkControllerConditionWorkerAvailable); got == nil || got.Status != metav1.ConditionFalse || got.Reason != "DeploymentProgressing" {
		t.Fatalf("WorkerAvailable condition=%+v", got)
	}
	if updated.Status.NetAsCode == nil || strings.Join(updated.Status.NetAsCode.ModelVersions, ",") != "1.0,2.0" || strings.Join(updated.Status.NetAsCode.Sections, ",") != "inventory,sites" {
		t.Fatalf("NetAsCode status=%+v", updated.Status.NetAsCode)
	}
	if len(updated.Status.Capabilities) != 0 {
		t.Fatalf("manager invented endpoint-discovered capabilities=%+v", updated.Status.Capabilities)
	}
}

func TestNetworkControllerManagerPreservesAdapterCapabilities(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("adapter-capabilities", testNetworkControllerType)
	controller.Status.Capabilities = []ciskov1.NetworkControllerCapabilityStatus{{
		Name:      "inventory",
		Supported: false,
		Message:   "endpoint license does not expose inventory",
	}}
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller)
	reconcileNetworkController(t, reconciler, controller)

	var current ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get NetworkController: %v", err)
	}
	if len(current.Status.Capabilities) != 1 || current.Status.Capabilities[0] != controller.Status.Capabilities[0] {
		t.Fatalf("manager changed adapter capability status: got=%+v want=%+v", current.Status.Capabilities, controller.Status.Capabilities)
	}
}

func TestNetworkControllerReconcileTracksReadyAndPausedWorker(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("lifecycle", testNetworkControllerType)
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller)
	reconcileNetworkController(t, reconciler, controller)

	key := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	var deployment appsv1.Deployment
	if err := kubeClient.Get(context.Background(), key, &deployment); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	deployment.Status.ReadyReplicas = 1
	if err := kubeClient.Status().Update(context.Background(), &deployment); err != nil {
		t.Fatalf("update Deployment status: %v", err)
	}
	reconcileNetworkController(t, reconciler, controller)

	var current ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get NetworkController: %v", err)
	}
	if current.Status.Phase != ciskov1.NetworkControllerPhaseConnecting || current.Status.Worker == nil || current.Status.Worker.ReadyReplicas != 1 {
		t.Fatalf("ready worker status=%+v", current.Status)
	}
	if got := meta.FindStatusCondition(current.Status.Conditions, ciskov1.NetworkControllerConditionWorkerAvailable); got == nil || got.Status != metav1.ConditionTrue {
		t.Fatalf("WorkerAvailable condition=%+v", got)
	}

	current.Spec.Paused = true
	if err := kubeClient.Update(context.Background(), &current); err != nil {
		t.Fatalf("pause NetworkController: %v", err)
	}
	reconcileNetworkController(t, reconciler, &current)
	if err := kubeClient.Get(context.Background(), key, &deployment); err != nil {
		t.Fatalf("get paused Deployment: %v", err)
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 {
		t.Fatalf("paused Deployment replicas=%v, want 0", deployment.Spec.Replicas)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get paused NetworkController: %v", err)
	}
	if current.Status.Phase != ciskov1.NetworkControllerPhasePaused {
		t.Fatalf("paused phase=%q", current.Status.Phase)
	}
}

func TestReadyWorkerDoesNotHideAdapterErrorPhase(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("adapter-error", testNetworkControllerType)
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller)
	reconcileNetworkController(t, reconciler, controller)

	key := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	var current ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get NetworkController: %v", err)
	}
	current.Status.Phase = ciskov1.NetworkControllerPhaseError
	if err := kubeClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatalf("seed adapter error status: %v", err)
	}
	reconcileNetworkController(t, reconciler, controller)
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get reconciled NetworkController: %v", err)
	}
	if current.Status.Phase != ciskov1.NetworkControllerPhaseError {
		t.Fatalf("progressing worker hid adapter error phase as %q", current.Status.Phase)
	}
	var deployment appsv1.Deployment
	if err := kubeClient.Get(context.Background(), key, &deployment); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	deployment.Status.ReadyReplicas = 1
	if err := kubeClient.Status().Update(context.Background(), &deployment); err != nil {
		t.Fatalf("update Deployment status: %v", err)
	}
	reconcileNetworkController(t, reconciler, controller)
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get ready reconciled NetworkController: %v", err)
	}
	if current.Status.Phase != ciskov1.NetworkControllerPhaseError {
		t.Fatalf("ready worker hid adapter error phase as %q", current.Status.Phase)
	}
}

func TestNetworkControllerUnauthorizedIntentSecretDoesNotQuiesceWorker(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("unauthorized-intent", testNetworkControllerType)
	intentConfig := &configv1alpha1.NetworkControllerConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "unauthorized-intent", Namespace: controller.Namespace},
		Spec: configv1alpha1.NetworkControllerConfigSpec{
			ControllerRef: configv1alpha1.NetworkControllerRef{Name: controller.Name},
			SecretRefs: []configv1alpha1.NetworkControllerSecretRef{{
				Section: "sites",
				Path:    "/credentials/password",
				Source:  "not-authorized",
			}},
		},
	}
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller, intentConfig)
	reconcileNetworkController(t, reconciler, controller)

	var deployment appsv1.Deployment
	key := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	if err := kubeClient.Get(context.Background(), key, &deployment); err != nil {
		t.Fatalf("worker must remain available for other configs: %v", err)
	}
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == "intent-secrets" {
			t.Fatalf("unauthorized intent Secret was projected: %+v", volume)
		}
	}
}

func TestNetworkControllerRecreatesImmutableBootstrapAndRoleRefDrift(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("upgrade", testNetworkControllerType)
	owner := *metav1.NewControllerRef(controller, ciskov1.GroupVersion.WithKind("NetworkController"))
	immutable := true
	workerName := networkControllerWorkerName(controller.Name)
	staleConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: workerName, Namespace: controller.Namespace, OwnerReferences: []metav1.OwnerReference{owner}},
		Immutable:  &immutable,
		Data:       map[string]string{controllerWorkerConfigFileName: "old-private-contract"},
	}
	staleRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: workerName, Namespace: controller.Namespace, OwnerReferences: []metav1.OwnerReference{owner}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "old-controller-role"},
	}
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller, staleConfigMap, staleRoleBinding)
	// Each immutable object is deleted on one pass and recreated on the next.
	for i := 0; i < 3; i++ {
		reconcileNetworkController(t, reconciler, controller)
	}

	key := client.ObjectKey{Namespace: controller.Namespace, Name: workerName}
	var configMap corev1.ConfigMap
	if err := kubeClient.Get(context.Background(), key, &configMap); err != nil {
		t.Fatalf("get recreated ConfigMap: %v", err)
	}
	if configMap.Data[controllerWorkerConfigFileName] == "old-private-contract" || configMap.Immutable == nil || !*configMap.Immutable {
		t.Fatalf("bootstrap ConfigMap was not safely recreated: %+v", configMap)
	}
	var roleBinding rbacv1.RoleBinding
	if err := kubeClient.Get(context.Background(), key, &roleBinding); err != nil {
		t.Fatalf("get recreated RoleBinding: %v", err)
	}
	if roleBinding.RoleRef.Name != controlleradapter.DefaultWorkerClusterRole {
		t.Fatalf("RoleBinding roleRef=%+v", roleBinding.RoleRef)
	}
	assertExplicitWorkerOwnership(t, &configMap, controller)
	assertExplicitWorkerOwnership(t, &roleBinding, controller)
	var deployment appsv1.Deployment
	if err := kubeClient.Get(context.Background(), key, &deployment); err != nil {
		t.Fatalf("get worker Deployment after bootstrap replacement: %v", err)
	}
	descriptor, ok := controlleradapter.DescriptorFor(testNetworkControllerType)
	if !ok {
		t.Fatal("foundation test descriptor missing")
	}
	oldHash := networkControllerContractHash(controller, descriptor, "old-private-contract")
	if got := deployment.Spec.Template.Annotations["cisco.vk/controller-contract-hash"]; got == oldHash {
		t.Fatalf("bootstrap replacement did not roll worker template hash %q", got)
	}
}

func TestNetworkControllerReplacesImmutableDeploymentSelectorDrift(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("selector-drift", testNetworkControllerType)
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller)
	reconcileNetworkController(t, reconciler, controller)

	key := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	var deployment appsv1.Deployment
	if err := kubeClient.Get(context.Background(), key, &deployment); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"stale": "selector"}}
	if err := kubeClient.Update(context.Background(), &deployment); err != nil {
		t.Fatalf("seed selector drift: %v", err)
	}
	reconcileNetworkController(t, reconciler, controller)
	if err := kubeClient.Get(context.Background(), key, &deployment); !apierrors.IsNotFound(err) {
		t.Fatalf("selector-drifted Deployment was not deleted: %v", err)
	}
	reconcileNetworkController(t, reconciler, controller)
	if err := kubeClient.Get(context.Background(), key, &deployment); err != nil {
		t.Fatalf("get replacement Deployment: %v", err)
	}
	if !reflect.DeepEqual(deployment.Spec.Selector.MatchLabels, networkControllerWorkerLabels(key.Name)) {
		t.Fatalf("replacement selector=%v", deployment.Spec.Selector.MatchLabels)
	}
}

func TestNetworkControllerDuplicateEndpointFencesAndPromotesLoser(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	winner := validNetworkController("z-oldest", testNetworkControllerType)
	loser := validNetworkController("a-newer", testNetworkControllerType)
	winner.Spec.Endpoint = "https://duplicate.example.test/controller"
	loser.Spec.Endpoint = winner.Spec.Endpoint
	winner.CreationTimestamp = metav1.NewTime(time.Unix(100, 0).UTC())
	loser.CreationTimestamp = metav1.NewTime(time.Unix(200, 0).UTC())

	owner := *metav1.NewControllerRef(loser, ciskov1.GroupVersion.WithKind("NetworkController"))
	staleWorker := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:            networkControllerWorkerName(loser.Name),
		Namespace:       loser.Namespace,
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	reconciler, kubeClient := newNetworkControllerReconciler(t, winner, loser, staleWorker)
	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(loser)})
	if err != nil {
		t.Fatalf("reconcile duplicate endpoint loser: %v", err)
	}
	if result.Requeue || result.RequeueAfter != duplicateEndpointRequeueAfter {
		t.Fatalf("duplicate endpoint requeue=%+v, want fixed delay %s", result, duplicateEndpointRequeueAfter)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(staleWorker), &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("duplicate endpoint loser worker was not quiesced: %v", err)
	}

	var fenced ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(loser), &fenced); err != nil {
		t.Fatalf("get fenced NetworkController: %v", err)
	}
	if fenced.Status.Phase != ciskov1.NetworkControllerPhaseError || fenced.Status.Worker != nil {
		t.Fatalf("fenced status=%+v", fenced.Status)
	}
	for _, conditionType := range []string{
		ciskov1.NetworkControllerConditionEndpointUnique,
		ciskov1.NetworkControllerConditionWorkerAvailable,
		ciskov1.NetworkControllerConditionReady,
	} {
		condition := meta.FindStatusCondition(fenced.Status.Conditions, conditionType)
		if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "DuplicateEndpoint" {
			t.Fatalf("%s condition=%+v", conditionType, condition)
		}
		if conditionType == ciskov1.NetworkControllerConditionEndpointUnique && !strings.Contains(condition.Message, winner.Name) {
			t.Fatalf("EndpointUnique condition does not identify deterministic winner: %+v", condition)
		}
	}

	if err := kubeClient.Delete(context.Background(), winner); err != nil {
		t.Fatalf("delete endpoint winner: %v", err)
	}
	result, err = reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(loser)})
	if err != nil {
		t.Fatalf("reconcile promoted endpoint owner: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("promoted endpoint owner unexpectedly requeued: %+v", result)
	}
	workerKey := client.ObjectKey{Namespace: loser.Namespace, Name: networkControllerWorkerName(loser.Name)}
	if err := kubeClient.Get(context.Background(), workerKey, &appsv1.Deployment{}); err != nil {
		t.Fatalf("promoted endpoint owner did not create worker: %v", err)
	}
	var promoted ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(loser), &promoted); err != nil {
		t.Fatalf("get promoted NetworkController: %v", err)
	}
	endpointCondition := meta.FindStatusCondition(promoted.Status.Conditions, ciskov1.NetworkControllerConditionEndpointUnique)
	if endpointCondition == nil || endpointCondition.Status != metav1.ConditionTrue || endpointCondition.Reason != "EndpointUnique" {
		t.Fatalf("promoted EndpointUnique condition=%+v", endpointCondition)
	}
	if promoted.Status.Phase != ciskov1.NetworkControllerPhasePending {
		t.Fatalf("promoted phase=%q, want Pending", promoted.Status.Phase)
	}
}

func TestNetworkControllerEqualTimestampWinnerQuiescesIncumbentBeforeStarting(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	created := metav1.NewTime(time.Unix(100, 0).UTC())
	incumbent := validNetworkController("z-incumbent", testNetworkControllerType)
	incumbent.CreationTimestamp = created
	incumbent.Spec.Endpoint = "https://equal-time.example.test/controller"

	reconciler, kubeClient := newNetworkControllerReconciler(t, incumbent)
	reconcileNetworkController(t, reconciler, incumbent)
	incumbentKey := client.ObjectKey{
		Namespace: incumbent.Namespace,
		Name:      networkControllerWorkerName(incumbent.Name),
	}
	if err := kubeClient.Get(context.Background(), incumbentKey, &appsv1.Deployment{}); err != nil {
		t.Fatalf("incumbent worker was not running before peer creation: %v", err)
	}

	winner := validNetworkController("a-winner", testNetworkControllerType)
	winner.CreationTimestamp = created
	winner.Spec.Endpoint = incumbent.Spec.Endpoint
	if err := kubeClient.Create(context.Background(), winner); err != nil {
		t.Fatalf("create equal-timestamp winner: %v", err)
	}
	winnerKey := client.ObjectKey{
		Namespace: winner.Namespace,
		Name:      networkControllerWorkerName(winner.Name),
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(winner)})
	if err != nil {
		t.Fatalf("reconcile equal-timestamp winner during incumbent pod cleanup: %v", err)
	}
	if !result.Requeue {
		t.Fatalf("winner cleanup result=%+v, want immediate staged requeue", result)
	}
	if err := kubeClient.Get(context.Background(), incumbentKey, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("incumbent Deployment was not removed first: %v", err)
	}
	if err := kubeClient.Get(context.Background(), winnerKey, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("winner Deployment started during incumbent pod cleanup: %v", err)
	}

	result, err = reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(winner)})
	if err != nil {
		t.Fatalf("reconcile equal-timestamp winner during incumbent identity cleanup: %v", err)
	}
	if !result.Requeue {
		t.Fatalf("winner identity cleanup result=%+v, want immediate staged requeue", result)
	}
	for _, object := range []client.Object{
		&corev1.ConfigMap{},
		&corev1.ServiceAccount{},
		&rbacv1.RoleBinding{},
	} {
		if err := kubeClient.Get(context.Background(), incumbentKey, object); !apierrors.IsNotFound(err) {
			t.Fatalf("incumbent %T remained after staged cleanup: %v", object, err)
		}
	}
	if err := kubeClient.Get(context.Background(), winnerKey, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("winner Deployment started before every incumbent resource was gone: %v", err)
	}

	result, err = reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(winner)})
	if err != nil {
		t.Fatalf("reconcile equal-timestamp winner after incumbent cleanup: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("winner unexpectedly requeued after incumbent cleanup: %+v", result)
	}
	if err := kubeClient.Get(context.Background(), winnerKey, &appsv1.Deployment{}); err != nil {
		t.Fatalf("winner Deployment did not start after incumbent cleanup: %v", err)
	}
}

func TestNetworkControllerEndpointPeerRequestsIncludeExactPeers(t *testing.T) {
	created := metav1.NewTime(time.Unix(100, 0).UTC())
	winner := validNetworkController("a-winner", testNetworkControllerType)
	incumbent := validNetworkController("z-incumbent", testNetworkControllerType)
	winner.CreationTimestamp = created
	incumbent.CreationTimestamp = created
	winner.Spec.Endpoint = "https://peer-map.example.test/controller"
	incumbent.Spec.Endpoint = winner.Spec.Endpoint

	reconciler, _ := newNetworkControllerReconciler(t, winner, incumbent)
	requests := reconciler.networkControllerEndpointPeerRequests(context.Background(), winner)
	want := []ctrl.Request{
		{NamespacedName: client.ObjectKeyFromObject(winner)},
		{NamespacedName: client.ObjectKeyFromObject(incumbent)},
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("endpoint peer requests=%v, want %v", requests, want)
	}
}

func TestNetworkControllerEndpointFenceIsExactAndNamespaceScoped(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("exact-scope", testNetworkControllerType)
	controller.CreationTimestamp = metav1.NewTime(time.Unix(300, 0).UTC())

	otherNamespace := validNetworkController("older-other-namespace", testNetworkControllerType)
	otherNamespace.Namespace = "another-controller-namespace"
	otherNamespace.Spec.Endpoint = controller.Spec.Endpoint
	otherNamespace.CreationTimestamp = metav1.NewTime(time.Unix(100, 0).UTC())

	textVariant := validNetworkController("older-text-variant", testNetworkControllerType)
	textVariant.Spec.Endpoint = controller.Spec.Endpoint + "/"
	textVariant.CreationTimestamp = metav1.NewTime(time.Unix(100, 0).UTC())

	reconciler, kubeClient := newNetworkControllerReconciler(t, controller, otherNamespace, textVariant)
	reconcileNetworkController(t, reconciler, controller)
	workerKey := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	if err := kubeClient.Get(context.Background(), workerKey, &appsv1.Deployment{}); err != nil {
		t.Fatalf("namespace-scoped exact endpoint owner did not create worker: %v", err)
	}
	var current ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get exact endpoint owner: %v", err)
	}
	condition := meta.FindStatusCondition(current.Status.Conditions, ciskov1.NetworkControllerConditionEndpointUnique)
	if condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("EndpointUnique condition=%+v", condition)
	}
}

func TestNetworkControllerEndpointWinnerTieBreaksByNameThenUID(t *testing.T) {
	created := metav1.NewTime(time.Unix(100, 0).UTC())
	left := validNetworkController("a-controller", testNetworkControllerType)
	right := validNetworkController("b-controller", testNetworkControllerType)
	left.CreationTimestamp = created
	right.CreationTimestamp = created
	if !networkControllerEndpointPrecedes(left, right) || networkControllerEndpointPrecedes(right, left) {
		t.Fatal("equal creation timestamps were not deterministically ordered by name")
	}

	right.Name = left.Name
	left.UID = types.UID("a-uid")
	right.UID = types.UID("b-uid")
	if !networkControllerEndpointPrecedes(left, right) || networkControllerEndpointPrecedes(right, left) {
		t.Fatal("equal creation timestamps and names were not deterministically ordered by UID")
	}
}

func TestNetworkControllerDoesNotAdoptForeignWorkerObjects(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	tests := []struct {
		name       string
		newForeign func(name, namespace string) client.Object
	}{
		{
			name: "ConfigMap",
			newForeign: func(name, namespace string) client.Object {
				return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Data: map[string]string{"foreign": "preserve"}}
			},
		},
		{
			name: "ServiceAccount",
			newForeign: func(name, namespace string) client.Object {
				return &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
			},
		},
		{
			name: "RoleBinding",
			newForeign: func(name, namespace string) client.Object {
				return &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
			},
		},
		{
			name: "Deployment",
			newForeign: func(name, namespace string) client.Object {
				return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := validNetworkController("collision-"+strings.ToLower(test.name), testNetworkControllerType)
			foreign := test.newForeign(networkControllerWorkerName(controller.Name), controller.Namespace)
			foreign.SetAnnotations(map[string]string{"sensitive.example/value": "do-not-emit"})
			reconciler, kubeClient := newNetworkControllerReconciler(t, controller, foreign)
			recorder := record.NewFakeRecorder(10)
			reconciler.Recorder = recorder

			_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(controller)})
			if err == nil || !strings.Contains(err.Error(), "not owned by NetworkController") {
				t.Fatalf("Reconcile error=%v, want foreign-object ownership failure", err)
			}
			current := foreign.DeepCopyObject().(client.Object)
			if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(foreign), current); err != nil {
				t.Fatalf("get foreign %s: %v", test.name, err)
			}
			if isNetworkControllerWorkerObject(current, controller) || current.GetAnnotations()["sensitive.example/value"] != "do-not-emit" {
				t.Fatalf("foreign %s was modified or adopted: %+v", test.name, current)
			}

			select {
			case event := <-recorder.Events:
				if !strings.Contains(event, corev1.EventTypeWarning+" "+workerObjectCollisionReason) || !strings.Contains(event, test.name) {
					t.Fatalf("collision event=%q", event)
				}
				for _, forbidden := range []string{
					controller.Spec.Endpoint,
					controller.Spec.CredentialSecretRef.Name,
					string(controller.UID),
					"do-not-emit",
				} {
					if strings.Contains(event, forbidden) {
						t.Fatalf("collision event leaked %q: %q", forbidden, event)
					}
				}
			default:
				t.Fatal("missing WorkerObjectCollision Warning Event")
			}
		})
	}
}

func TestDeletingNetworkControllerRetainsWorkerUntilConfigsAreGone(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("deleting", testNetworkControllerType)
	now := metav1.Now()
	controller.DeletionTimestamp = &now
	controller.Finalizers = []string{networkControllerFinalizer}
	config := &configv1alpha1.NetworkControllerConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "dependent", Namespace: controller.Namespace},
		Spec: configv1alpha1.NetworkControllerConfigSpec{
			ControllerRef: configv1alpha1.NetworkControllerRef{Name: controller.Name},
		},
	}
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller, config)
	reconcileNetworkController(t, reconciler, controller)

	workerKey := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	if err := kubeClient.Get(context.Background(), workerKey, &appsv1.Deployment{}); err != nil {
		t.Fatalf("worker removed before dependent config cleanup: %v", err)
	}
	if err := kubeClient.Delete(context.Background(), config); err != nil {
		t.Fatalf("delete dependent config: %v", err)
	}
	reconcileNetworkController(t, reconciler, controller)
	if err := kubeClient.Get(context.Background(), workerKey, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("foreground worker Deployment still present in fake client: %v", err)
	}
	var deleting ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &deleting); err != nil {
		t.Fatalf("get deleting NetworkController: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&deleting, networkControllerFinalizer) {
		t.Fatal("protection finalizer removed before staged worker cleanup completed")
	}
	// One pass removes the now-unused RBAC/identity resources; the next observes
	// all children gone and releases the endpoint name.
	reconcileNetworkController(t, reconciler, controller)
	reconcileNetworkController(t, reconciler, controller)
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &deleting); err == nil && controllerutil.ContainsFinalizer(&deleting, networkControllerFinalizer) {
		t.Fatal("protection finalizer retained after every worker resource was gone")
	}
}

func TestNetworkControllerUnknownAdapterFailsClosed(t *testing.T) {
	controller := validNetworkController("unknown", "unregistered-controller")
	owner := *metav1.NewControllerRef(controller, ciskov1.GroupVersion.WithKind("NetworkController"))
	workerName := networkControllerWorkerName(controller.Name)
	staleDeployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:            workerName,
		Namespace:       controller.Namespace,
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	staleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Name:            workerName,
		Namespace:       controller.Namespace,
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	staleServiceAccount := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name:            workerName,
		Namespace:       controller.Namespace,
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	staleConfigMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:            workerName,
		Namespace:       controller.Namespace,
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller, staleDeployment, staleBinding, staleServiceAccount, staleConfigMap)
	for i := 0; i < 3; i++ {
		reconcileNetworkController(t, reconciler, controller)
	}

	key := client.ObjectKey{Namespace: controller.Namespace, Name: workerName}
	if err := kubeClient.Get(context.Background(), key, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("stale Deployment still present or unexpected error: %v", err)
	}
	if err := kubeClient.Get(context.Background(), key, &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("stale RoleBinding still present or unexpected error: %v", err)
	}
	if err := kubeClient.Get(context.Background(), key, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
		t.Fatalf("stale ServiceAccount still present or unexpected error: %v", err)
	}
	if err := kubeClient.Get(context.Background(), key, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Fatalf("stale ConfigMap still present or unexpected error: %v", err)
	}

	var current ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get unknown NetworkController: %v", err)
	}
	if current.Status.Phase != ciskov1.NetworkControllerPhaseError || current.Status.Worker != nil {
		t.Fatalf("unknown adapter status=%+v", current.Status)
	}
	if got := meta.FindStatusCondition(current.Status.Conditions, ciskov1.NetworkControllerConditionAdapterAvailable); got == nil || got.Status != metav1.ConditionFalse || got.Reason != "AdapterNotRegistered" {
		t.Fatalf("AdapterAvailable condition=%+v", got)
	}
}

func TestNetworkControllerSpecUpdateRollsWorker(t *testing.T) {
	ensureFoundationTestControllerRegistered()
	controller := validNetworkController("rollout", testNetworkControllerType)
	reconciler, kubeClient := newNetworkControllerReconciler(t, controller)
	reconcileNetworkController(t, reconciler, controller)
	workerKey := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	var deployment appsv1.Deployment
	if err := kubeClient.Get(context.Background(), workerKey, &deployment); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	firstHash := deployment.Spec.Template.Annotations["cisco.vk/controller-contract-hash"]

	var current ciskov1.NetworkController
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get NetworkController: %v", err)
	}
	current.Status.Phase = ciskov1.NetworkControllerPhaseReady
	current.Status.ObservedGeneration = 1
	setNetworkControllerCondition(&current.Status.Conditions, 1, ciskov1.NetworkControllerConditionReady, metav1.ConditionTrue, "Connected", "adapter processed generation one")
	if err := kubeClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatalf("seed adapter status: %v", err)
	}
	deployment.Generation = 2
	if err := kubeClient.Update(context.Background(), &deployment); err != nil {
		t.Fatalf("seed pending Deployment generation: %v", err)
	}
	if err := kubeClient.Get(context.Background(), workerKey, &deployment); err != nil {
		t.Fatalf("get pending Deployment: %v", err)
	}
	deployment.Status.ObservedGeneration = 1
	deployment.Status.ReadyReplicas = 1
	deployment.Status.UpdatedReplicas = 1
	if err := kubeClient.Status().Update(context.Background(), &deployment); err != nil {
		t.Fatalf("seed old worker readiness: %v", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("refresh NetworkController: %v", err)
	}
	current.Generation = 2
	current.Spec.CredentialSecretRef.Name = "rotated-controller-credentials"
	if err := kubeClient.Update(context.Background(), &current); err != nil {
		t.Fatalf("update NetworkController: %v", err)
	}
	reconcileNetworkController(t, reconciler, &current)
	if err := kubeClient.Get(context.Background(), workerKey, &deployment); err != nil {
		t.Fatalf("get rolled Deployment: %v", err)
	}
	secondHash := deployment.Spec.Template.Annotations["cisco.vk/controller-contract-hash"]
	if firstHash == secondHash {
		t.Fatalf("controller spec update did not change rollout hash %q", firstHash)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(controller), &current); err != nil {
		t.Fatalf("get rollout NetworkController status: %v", err)
	}
	if current.Status.ObservedGeneration != 1 || current.Status.Phase != ciskov1.NetworkControllerPhaseReady {
		t.Fatalf("manager claimed new generation or overwrote adapter phase: %+v", current.Status)
	}
	if ready := meta.FindStatusCondition(current.Status.Conditions, ciskov1.NetworkControllerConditionReady); ready == nil || ready.Status != metav1.ConditionFalse || ready.ObservedGeneration != 2 {
		t.Fatalf("Ready condition did not expose pending rollout: %+v", ready)
	}
}

func TestNetworkControllerWorkerNameIsStableDNSLabel(t *testing.T) {
	for _, name := range []string{
		strings.Repeat("a", 253),
		strings.Repeat("a.", 17) + strings.Repeat("b", 50),
		"foo.bar",
		"foo-bar",
	} {
		first := networkControllerWorkerName(name)
		second := networkControllerWorkerName(name)
		if first != second || len(first) > 63 || !strings.HasSuffix(first, "-controller-worker") || len(utilvalidation.IsDNS1123Subdomain(first)) != 0 || len(utilvalidation.IsValidLabelValue(first)) != 0 {
			t.Fatalf("source=%q worker name=%q len=%d second=%q", name, first, len(first), second)
		}
	}
	if networkControllerWorkerName("foo.bar") == networkControllerWorkerName("foo-bar") {
		t.Fatal("distinct DNS subdomain controller names collide after worker-name derivation")
	}
}

func TestNetworkControllerPredicateIncludesFinalizerAndDeletionChanges(t *testing.T) {
	pred := specOrDeletionChangedPredicate()
	old := validNetworkController("predicate", testNetworkControllerType)
	old.Finalizers = []string{networkControllerFinalizer}

	statusOnly := old.DeepCopy()
	statusOnly.Status.Phase = ciskov1.NetworkControllerPhaseReady
	if pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: statusOnly}) {
		t.Fatal("status-only update must not enqueue central orchestration")
	}

	withoutFinalizer := old.DeepCopy()
	withoutFinalizer.Finalizers = nil
	if !pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: withoutFinalizer}) {
		t.Fatal("finalizer removal must enqueue reconciliation so protection is restored")
	}

	deleting := old.DeepCopy()
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	if !pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: deleting}) {
		t.Fatal("deletionTimestamp change must enqueue finalizer cleanup")
	}
}

func TestNetworkControllerLegacyOwnershipRequiresExactIdentity(t *testing.T) {
	controller := validNetworkController("legacy-owner", testNetworkControllerType)
	object := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: ciskov1.GroupVersion.String(),
			Kind:       "CiscoDevice",
			Name:       controller.Name,
			UID:        controller.UID,
			Controller: ptr.To(true),
		}},
	}}
	if isNetworkControllerWorkerObject(object, controller) {
		t.Fatal("same-UID foreign GVK was accepted as a legacy NetworkController child")
	}
	object.OwnerReferences[0] = *metav1.NewControllerRef(controller, ciskov1.GroupVersion.WithKind("NetworkController"))
	if !isNetworkControllerWorkerObject(object, controller) {
		t.Fatal("exact legacy NetworkController owner identity was rejected")
	}
}

func validNetworkController(name, typeName string) *ciskov1.NetworkController {
	return &ciskov1.NetworkController{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "controller-tests",
			UID:        types.UID(name + "-uid"),
			Generation: 1,
		},
		Spec: ciskov1.NetworkControllerSpec{
			Type:                ciskov1.NetworkControllerType(typeName),
			Endpoint:            "https://controller.example.test",
			CredentialSecretRef: ciskov1.NetworkControllerSecretReference{Name: "controller-credentials"},
			TLS: &ciskov1.NetworkControllerTLSConfig{CAConfigMapRef: &ciskov1.NetworkControllerConfigMapKeyReference{
				Name: "controller-ca",
				Key:  "ca.pem",
			}},
			IntentSecretSources: []ciskov1.NetworkControllerIntentSecretSource{{
				Alias: "site-credentials",
				Name:  "site-credentials-secret",
				Key:   "radius-password",
			}},
		},
	}
}

func newNetworkControllerReconciler(t *testing.T, objects ...client.Object) (*NetworkControllerReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Cisco scheme: %v", err)
	}
	if err := configv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add config scheme: %v", err)
	}
	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&ciskov1.NetworkController{}, &appsv1.Deployment{}).
		WithIndex(&ciskov1.NetworkController{}, networkControllerEndpointIndex, networkControllerEndpointIndexValues).
		WithIndex(&configv1alpha1.NetworkControllerConfig{}, networkControllerRefIndex, networkControllerConfigRefIndexValues).
		WithObjects(objects...).
		Build()
	return &NetworkControllerReconciler{
		Client:          kubeClient,
		Image:           "example.test/cisco-vk:controller-foundation",
		ImagePullPolicy: corev1.PullAlways,
	}, kubeClient
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func reconcileNetworkController(t *testing.T, reconciler *NetworkControllerReconciler, controller *ciskov1.NetworkController) {
	t.Helper()
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(controller)})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func assertExplicitWorkerOwnership(t *testing.T, object metav1.Object, controller *ciskov1.NetworkController) {
	t.Helper()
	if len(object.GetOwnerReferences()) != 0 {
		t.Fatalf("worker %T has ownerReferences that can bypass Retain: %+v", object, object.GetOwnerReferences())
	}
	annotations := object.GetAnnotations()
	if annotations[networkControllerNameAnnotation] != controller.Name || annotations[networkControllerUIDAnnotation] != string(controller.UID) {
		t.Fatalf("worker %T ownership annotations=%v", object, annotations)
	}
}

func assertReadOnlyMount(t *testing.T, mounts []corev1.VolumeMount, name, path string) {
	t.Helper()
	for _, mount := range mounts {
		if mount.Name == name {
			if mount.MountPath != path || !mount.ReadOnly {
				t.Fatalf("mount %q=%+v, want read-only path %q", name, mount, path)
			}
			return
		}
	}
	t.Fatalf("mount %q missing from %+v", name, mounts)
}

func assertSecretVolume(t *testing.T, volumes []corev1.Volume, name, secretName string) {
	t.Helper()
	for _, volume := range volumes {
		if volume.Name == name {
			if volume.Secret == nil || volume.Secret.SecretName != secretName {
				t.Fatalf("volume %q=%+v, want Secret %q", name, volume, secretName)
			}
			return
		}
	}
	t.Fatalf("volume %q missing from %+v", name, volumes)
}

func assertProjectedSecretVolume(t *testing.T, volumes []corev1.Volume, name, secretName, key, path string) {
	t.Helper()
	for _, volume := range volumes {
		if volume.Name != name {
			continue
		}
		if volume.Projected == nil || len(volume.Projected.Sources) != 1 {
			t.Fatalf("volume %q=%+v, want one projected source", name, volume)
		}
		projection := volume.Projected.Sources[0].Secret
		if projection == nil || projection.Name != secretName || projection.Optional == nil || !*projection.Optional || len(projection.Items) != 1 || projection.Items[0].Key != key || projection.Items[0].Path != path {
			t.Fatalf("volume %q projection=%+v, want optional Secret %q key %q path %q", name, projection, secretName, key, path)
		}
		return
	}
	t.Fatalf("volume %q missing from %+v", name, volumes)
}
