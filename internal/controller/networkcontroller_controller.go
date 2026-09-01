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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	controlleradapter "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

const (
	controllerWorkerConfigFileName  = "controller.yaml"
	controllerWorkerConfigMountDir  = "/etc/cisco-vk/controller"
	networkControllerEndpointIndex  = "spec.endpoint"
	networkControllerRefIndex       = "spec.controllerRef.name"
	maxControllerIntentSecretRefs   = 256
	networkControllerFinalizer      = "cisco.vk/network-controller-protection"
	networkControllerNameAnnotation = "cisco.vk/network-controller-name"
	networkControllerUIDAnnotation  = "cisco.vk/network-controller-uid"
	duplicateEndpointRequeueAfter   = 30 * time.Second
	workerObjectCollisionReason     = "WorkerObjectCollision"
)

// NetworkControllerReconciler materializes one isolated adapter worker for
// each registered NetworkController. It never imports a concrete adapter and
// never calls a product API from the central manager.
type NetworkControllerReconciler struct {
	client.Client
	Image           string
	ImagePullPolicy corev1.PullPolicy
	Recorder        record.EventRecorder
}

// +kubebuilder:rbac:groups=cisco.vk,resources=networkcontrollers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cisco.vk,resources=networkcontrollers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=networkcontrollerconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// The base worker ClusterRole is installed by Helm and bound into each
// controller namespace. Product-specific worker roles must be installed and
// explicitly added to this audited bind allow-list when their adapter lands.
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames=cisco-virtual-kubelet-controller-worker,verbs=bind

// Reconcile creates the non-sensitive bootstrap ConfigMap, dedicated Service
// Account and RoleBinding, and restricted worker Deployment for a registered
// controller type. Unknown or invalid types are quiesced fail-closed.
func (r *NetworkControllerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("networkController", req.NamespacedName)
	var controller ciskov1.NetworkController
	if err := r.Get(ctx, req.NamespacedName, &controller); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if controller.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&controller, networkControllerFinalizer) {
			controllerutil.AddFinalizer(&controller, networkControllerFinalizer)
			if err := r.Update(ctx, &controller); err != nil {
				return ctrl.Result{}, fmt.Errorf("add NetworkController protection finalizer: %w", err)
			}
		}
	} else {
		configs, err := r.dependentNetworkControllerConfigs(ctx, &controller)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(configs.Items) == 0 {
			quiesced, err := r.quiesceWorker(ctx, &controller)
			if err != nil {
				return ctrl.Result{}, err
			}
			if !quiesced {
				return ctrl.Result{Requeue: true}, nil
			}
			controllerutil.RemoveFinalizer(&controller, networkControllerFinalizer)
			if err := r.Update(ctx, &controller); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("remove NetworkController protection finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}
		logger.Info("retaining deleting NetworkController worker until dependent configs are removed",
			"dependentConfigs", len(configs.Items),
		)
	}

	if err := ciskov1.ValidateNetworkController(&controller); err != nil {
		quiesced, cleanupErr := r.quiesceWorker(ctx, &controller)
		if cleanupErr != nil {
			return ctrl.Result{}, cleanupErr
		}
		statusErr := r.updateUnavailableStatus(
			ctx,
			&controller,
			"InvalidSpec",
			"controller specification is invalid; inspect API validation details",
			false,
		)
		if statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{Requeue: !quiesced}, nil
	}

	endpointPeers, err := r.networkControllerEndpointPeers(ctx, &controller)
	if err != nil {
		return ctrl.Result{}, err
	}
	endpointWinner := networkControllerEndpointWinner(&controller, endpointPeers)
	if endpointWinner.Name != controller.Name || endpointWinner.UID != controller.UID {
		if _, cleanupErr := r.quiesceWorker(ctx, &controller); cleanupErr != nil {
			return ctrl.Result{}, cleanupErr
		}
		if statusErr := r.updateDuplicateEndpointStatus(ctx, &controller, endpointWinner); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: duplicateEndpointRequeueAfter}, nil
	}
	loserWorkersGone, err := r.quiesceDuplicateEndpointPeerWorkers(ctx, &controller, endpointPeers)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !loserWorkersGone {
		// Do not create or advance the elected worker until every managed worker
		// for a losing peer is fully gone. This makes the winner itself enforce
		// endpoint exclusivity even when a peer event was delayed or dropped.
		return ctrl.Result{Requeue: true}, nil
	}

	descriptor, registered := controlleradapter.DescriptorFor(string(controller.Spec.Type))
	if !registered {
		quiesced, cleanupErr := r.quiesceWorker(ctx, &controller)
		if cleanupErr != nil {
			return ctrl.Result{}, cleanupErr
		}
		message := fmt.Sprintf("no adapter is registered for controller type %q", controller.Spec.Type)
		if statusErr := r.updateUnavailableStatus(ctx, &controller, "AdapterNotRegistered", message, true); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{Requeue: !quiesced}, nil
	}
	intentSecretSources, skippedIntentSecrets, err := r.controllerIntentSecretProjections(ctx, &controller)
	if err != nil {
		// A transient cache/list failure must not tear down a healthy endpoint
		// worker and strand every config it owns.
		return ctrl.Result{}, err
	}
	if skippedIntentSecrets > 0 {
		logger.Info("skipped unauthorized or excess controller intent Secret projections",
			"count", skippedIntentSecrets,
		)
	}

	bootstrap, err := controlleradapter.NewWorkerConfig(&controller)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("build controller worker bootstrap: %w", err)
	}
	bootstrapData, err := yaml.Marshal(bootstrap)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("marshal controller worker bootstrap: %w", err)
	}

	workerName := networkControllerWorkerName(controller.Name)
	labels := networkControllerWorkerLabels(workerName)
	configMap, ready, err := r.reconcileWorkerConfigMap(ctx, &controller, labels, string(bootstrapData))
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ready {
		return ctrl.Result{Requeue: true}, nil
	}

	serviceAccount := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: workerName, Namespace: controller.Namespace}}
	if err := r.requireWorkerObjectOwnedOrAbsent(ctx, serviceAccount, &controller); err != nil {
		return ctrl.Result{}, err
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, serviceAccount, func() error {
		setNetworkControllerWorkerMetadata(serviceAccount, &controller, labels)
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile controller worker ServiceAccount: %w", err)
	}

	_, ready, err = r.reconcileWorkerRoleBinding(ctx, &controller, descriptor, serviceAccount, labels)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ready {
		return ctrl.Result{Requeue: true}, nil
	}

	deployment, ready, err := r.reconcileWorkerDeployment(
		ctx,
		&controller,
		descriptor,
		string(bootstrapData),
		configMap.Name,
		serviceAccount.Name,
		labels,
		intentSecretSources,
	)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ready {
		return ctrl.Result{Requeue: true}, nil
	}

	if err := r.updateAvailableStatus(ctx, &controller, descriptor, deployment); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("controller worker reconciled", "deployment", deployment.Name, "paused", controller.Spec.Paused)
	return ctrl.Result{}, nil
}

func (r *NetworkControllerReconciler) reconcileWorkerConfigMap(
	ctx context.Context,
	controller *ciskov1.NetworkController,
	labels map[string]string,
	bootstrap string,
) (*corev1.ConfigMap, bool, error) {
	key := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	desiredData := map[string]string{controllerWorkerConfigFileName: bootstrap}
	var existing corev1.ConfigMap
	if err := r.Get(ctx, key, &existing); err == nil {
		if !isNetworkControllerWorkerObject(&existing, controller) {
			return nil, false, r.workerObjectCollisionError(controller, &existing)
		}
		if existing.Immutable != nil && *existing.Immutable && (!reflect.DeepEqual(existing.Data, desiredData) || len(existing.BinaryData) != 0) {
			// ConfigMap data is immutable. Recreate only the narrowly owned
			// bootstrap object when its versioned private contract changes.
			if err := r.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
				return nil, false, fmt.Errorf("replace immutable controller worker ConfigMap %s: %w", key, err)
			}
			return nil, false, nil
		}
	} else if !apierrors.IsNotFound(err) {
		return nil, false, fmt.Errorf("get controller worker ConfigMap %s: %w", key, err)
	}

	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, configMap, func() error {
		setNetworkControllerWorkerMetadata(configMap, controller, labels)
		configMap.Data = desiredData
		configMap.BinaryData = nil
		configMap.Immutable = ptr.To(true)
		return nil
	}); err != nil {
		return nil, false, fmt.Errorf("reconcile controller worker ConfigMap: %w", err)
	}
	return configMap, true, nil
}

func (r *NetworkControllerReconciler) reconcileWorkerRoleBinding(
	ctx context.Context,
	controller *ciskov1.NetworkController,
	descriptor controlleradapter.Descriptor,
	serviceAccount *corev1.ServiceAccount,
	labels map[string]string,
) (*rbacv1.RoleBinding, bool, error) {
	key := client.ObjectKey{Namespace: controller.Namespace, Name: networkControllerWorkerName(controller.Name)}
	desiredRoleRef := rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "ClusterRole",
		Name:     descriptor.WorkerClusterRole,
	}
	var existing rbacv1.RoleBinding
	if err := r.Get(ctx, key, &existing); err == nil {
		if !isNetworkControllerWorkerObject(&existing, controller) {
			return nil, false, r.workerObjectCollisionError(controller, &existing)
		}
		if existing.RoleRef != desiredRoleRef {
			// RoleRef is immutable. Delete the narrowly owned binding and let the
			// next reconciliation recreate it with the descriptor's audited role.
			if err := r.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
				return nil, false, fmt.Errorf("replace controller worker RoleBinding %s: %w", key, err)
			}
			return nil, false, nil
		}
	} else if !apierrors.IsNotFound(err) {
		return nil, false, fmt.Errorf("get controller worker RoleBinding %s: %w", key, err)
	}

	roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, roleBinding, func() error {
		setNetworkControllerWorkerMetadata(roleBinding, controller, labels)
		roleBinding.RoleRef = desiredRoleRef
		roleBinding.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      serviceAccount.Name,
			Namespace: serviceAccount.Namespace,
		}}
		return nil
	}); err != nil {
		return nil, false, fmt.Errorf("reconcile controller worker RoleBinding: %w", err)
	}
	return roleBinding, true, nil
}

func (r *NetworkControllerReconciler) requireWorkerObjectOwnedOrAbsent(
	ctx context.Context,
	object client.Object,
	controller *ciskov1.NetworkController,
) error {
	key := client.ObjectKeyFromObject(object)
	if err := r.Get(ctx, key, object); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get controller worker %T %s: %w", object, key, err)
	}
	if !isNetworkControllerWorkerObject(object, controller) {
		return r.workerObjectCollisionError(controller, object)
	}
	return nil
}

func (r *NetworkControllerReconciler) workerObjectCollisionError(
	controller *ciskov1.NetworkController,
	object client.Object,
) error {
	kind := networkControllerWorkerObjectKind(object)
	key := client.ObjectKeyFromObject(object)
	if r.Recorder != nil {
		r.Recorder.Eventf(
			controller,
			corev1.EventTypeWarning,
			workerObjectCollisionReason,
			"Cannot reconcile worker: same-name %s %q already exists and is not managed by this NetworkController",
			kind,
			key.String(),
		)
	}
	return fmt.Errorf("controller worker %s %s is not owned by NetworkController", kind, key)
}

func networkControllerWorkerObjectKind(object client.Object) string {
	switch object.(type) {
	case *corev1.ConfigMap:
		return "ConfigMap"
	case *corev1.ServiceAccount:
		return "ServiceAccount"
	case *rbacv1.RoleBinding:
		return "RoleBinding"
	case *appsv1.Deployment:
		return "Deployment"
	default:
		return "Object"
	}
}

func (r *NetworkControllerReconciler) reconcileWorkerDeployment(
	ctx context.Context,
	controller *ciskov1.NetworkController,
	descriptor controlleradapter.Descriptor,
	bootstrap string,
	configMapName string,
	serviceAccountName string,
	labels map[string]string,
	intentSecretSources []corev1.VolumeProjection,
) (*appsv1.Deployment, bool, error) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:      networkControllerWorkerName(controller.Name),
		Namespace: controller.Namespace,
	}}
	if err := r.requireWorkerObjectOwnedOrAbsent(ctx, deployment, controller); err != nil {
		return nil, false, err
	}
	desiredSelector := &metav1.LabelSelector{MatchLabels: cloneStringMap(labels)}
	if deployment.ResourceVersion != "" && !reflect.DeepEqual(deployment.Spec.Selector, desiredSelector) {
		// Deployment selectors are immutable. Replace only the explicitly owned
		// worker and wait for foreground deletion so stale credentialed Pods are
		// gone before a replacement starts.
		foreground := metav1.DeletePropagationForeground
		if err := r.Delete(ctx, deployment, &client.DeleteOptions{PropagationPolicy: &foreground}); err != nil && !apierrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("replace controller worker Deployment selector: %w", err)
		}
		return deployment, false, nil
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		return r.mutateWorkerDeployment(
			controller,
			descriptor,
			bootstrap,
			deployment,
			configMapName,
			serviceAccountName,
			labels,
			intentSecretSources,
		)
	}); err != nil {
		return nil, false, fmt.Errorf("reconcile controller worker Deployment: %w", err)
	}
	return deployment, true, nil
}

func (r *NetworkControllerReconciler) mutateWorkerDeployment(
	controller *ciskov1.NetworkController,
	descriptor controlleradapter.Descriptor,
	bootstrap string,
	deployment *appsv1.Deployment,
	configMapName string,
	serviceAccountName string,
	labels map[string]string,
	intentSecretSources []corev1.VolumeProjection,
) error {
	image := r.Image
	if image == "" {
		image = DefaultImage
	}
	imagePullPolicy := r.ImagePullPolicy
	if imagePullPolicy == "" {
		imagePullPolicy = corev1.PullIfNotPresent
	}
	replicas := int32(1)
	if controller.Spec.Paused {
		replicas = 0
	}
	setNetworkControllerWorkerMetadata(deployment, controller, labels)
	deployment.Spec.Replicas = ptr.To(replicas)
	deployment.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: cloneStringMap(labels)}
	deployment.Spec.Template.ObjectMeta = metav1.ObjectMeta{
		Labels: cloneStringMap(labels),
		Annotations: map[string]string{
			"cisco.vk/controller-contract-hash": networkControllerContractHash(controller, descriptor, bootstrap),
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "bootstrap",
			MountPath: controllerWorkerConfigMountDir + "/" + controllerWorkerConfigFileName,
			SubPath:   controllerWorkerConfigFileName,
			ReadOnly:  true,
		},
		{
			Name:      "credentials",
			MountPath: controlleradapter.DefaultCredentialPath,
			ReadOnly:  true,
		},
		{Name: "tmp", MountPath: "/tmp"},
	}
	defaultMode := int32(0440)
	volumes := []corev1.Volume{
		{
			Name: "bootstrap",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
			}},
		},
		{
			Name: "credentials",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName:  controller.Spec.CredentialSecretRef.Name,
				DefaultMode: &defaultMode,
			}},
		},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}
	if controller.Spec.TLS != nil && controller.Spec.TLS.CAConfigMapRef != nil {
		ca := controller.Spec.TLS.CAConfigMapRef
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "controller-ca",
			MountPath: controlleradapter.DefaultCADirectory,
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "controller-ca",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: ca.Name},
				Items:                []corev1.KeyToPath{{Key: ca.Key, Path: "ca.crt", Mode: &defaultMode}},
			}},
		})
	}
	if len(intentSecretSources) > 0 {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "intent-secrets",
			MountPath: controlleradapter.DefaultIntentSecretPath,
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "intent-secrets",
			VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
				Sources:     intentSecretSources,
				DefaultMode: &defaultMode,
			}},
		})
	}

	deployment.Spec.Template.Spec = corev1.PodSpec{
		ServiceAccountName: serviceAccountName,
		EnableServiceLinks: ptr.To(false),
		Affinity:           perDeviceVKNodeAffinity(),
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: ptr.To(true),
			RunAsUser:    ptr.To(distrolessNonRootUID),
			RunAsGroup:   ptr.To(distrolessNonRootGID),
			FSGroup:      ptr.To(distrolessNonRootGID),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		Containers: []corev1.Container{{
			Name:            "controller-worker",
			Image:           image,
			ImagePullPolicy: imagePullPolicy,
			Args: []string{
				"controller-worker",
				"--config=" + controllerWorkerConfigMountDir + "/" + controllerWorkerConfigFileName,
				"--controller-namespace=" + controller.Namespace,
				"--controller-name=" + controller.Name,
				"--controller-uid=" + string(controller.UID),
				fmt.Sprintf("--controller-generation=%d", controller.Generation),
				"--controller-type=" + string(controller.Spec.Type),
				"--controller-descriptor-digest=" + controlleradapter.DescriptorDigest(descriptor),
				"--metrics-bind-address=:8080",
				"--health-probe-bind-address=:8081",
			},
			Ports: []corev1.ContainerPort{
				{Name: "metrics", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
				{Name: "health", ContainerPort: 8081, Protocol: corev1.ProtocolTCP},
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstrFromString("health")}},
				InitialDelaySeconds: 15,
				PeriodSeconds:       20,
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstrFromString("health")}},
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
			},
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
				ReadOnlyRootFilesystem:   ptr.To(true),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
			VolumeMounts: volumeMounts,
		}},
		Volumes: volumes,
	}
	return nil
}

func (r *NetworkControllerReconciler) controllerIntentSecretProjections(ctx context.Context, controller *ciskov1.NetworkController) ([]corev1.VolumeProjection, int, error) {
	configs, err := r.dependentNetworkControllerConfigs(ctx, controller)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(configs.Items, func(i, j int) bool { return configs.Items[i].Name < configs.Items[j].Name })

	authorized := make(map[string]ciskov1.NetworkControllerIntentSecretSource, len(controller.Spec.IntentSecretSources))
	for _, source := range controller.Spec.IntentSecretSources {
		authorized[source.Alias] = source
	}

	var sources []corev1.VolumeProjection
	skipped := 0
	for i := range configs.Items {
		config := &configs.Items[i]
		configSkipped := 0
		refs := append([]configv1alpha1.NetworkControllerSecretRef(nil), config.Spec.SecretRefs...)
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Section != refs[j].Section {
				return refs[i].Section < refs[j].Section
			}
			if refs[i].Path != refs[j].Path {
				return refs[i].Path < refs[j].Path
			}
			return refs[i].Source < refs[j].Source
		})
		for _, ref := range refs {
			authorizedSource, ok := authorized[ref.Source]
			if !ok {
				skipped++
				configSkipped++
				continue
			}
			if len(sources) >= maxControllerIntentSecretRefs {
				skipped++
				configSkipped++
				continue
			}
			relativePath, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{
				ConfigName:  config.Name,
				Section:     ref.Section,
				JSONPointer: ref.Path,
				SourceAlias: ref.Source,
				SecretName:  authorizedSource.Name,
				SecretKey:   authorizedSource.Key,
			})
			if err != nil {
				skipped++
				configSkipped++
				continue
			}
			sources = append(sources, corev1.VolumeProjection{Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{Name: authorizedSource.Name},
				Items:                []corev1.KeyToPath{{Key: authorizedSource.Key, Path: relativePath}},
				// Intent leaf Secrets are per-config dependencies. Missing data
				// must fail only that config, not prevent the shared endpoint worker
				// Pod from mounting every otherwise valid projection.
				Optional: ptr.To(true),
			}})
		}
		if configSkipped > 0 && r.Recorder != nil {
			r.Recorder.Eventf(
				config,
				corev1.EventTypeWarning,
				"IntentSecretProjectionSkipped",
				"Skipped %d unauthorized, invalid, or excess intent Secret projection(s); inspect IntentSecretsReady status",
				configSkipped,
			)
		}
	}
	return sources, skipped, nil
}

func (r *NetworkControllerReconciler) dependentNetworkControllerConfigs(ctx context.Context, controller *ciskov1.NetworkController) (*configv1alpha1.NetworkControllerConfigList, error) {
	var configs configv1alpha1.NetworkControllerConfigList
	if err := r.List(
		ctx,
		&configs,
		client.InNamespace(controller.Namespace),
		client.MatchingFields{networkControllerRefIndex: controller.Name},
	); err != nil {
		return nil, fmt.Errorf("list dependent NetworkControllerConfig resources: %w", err)
	}
	return &configs, nil
}

// networkControllerEndpointPeers returns the exact duplicate endpoint set in
// one namespace. Endpoint parsing or normalization is deliberately not part of
// identity: changing textual endpoint identity requires a new object.
func (r *NetworkControllerReconciler) networkControllerEndpointPeers(
	ctx context.Context,
	controller *ciskov1.NetworkController,
) ([]ciskov1.NetworkController, error) {
	var controllers ciskov1.NetworkControllerList
	if err := r.List(
		ctx,
		&controllers,
		client.InNamespace(controller.Namespace),
		client.MatchingFields{networkControllerEndpointIndex: controller.Spec.Endpoint},
	); err != nil {
		return nil, fmt.Errorf("list NetworkControllers for endpoint fencing: %w", err)
	}
	return controllers.Items, nil
}

func networkControllerEndpointWinner(
	controller *ciskov1.NetworkController,
	peers []ciskov1.NetworkController,
) *ciskov1.NetworkController {
	winner := controller.DeepCopy()
	for i := range peers {
		candidate := &peers[i]
		if candidate.Spec.Endpoint != controller.Spec.Endpoint {
			continue
		}
		if networkControllerEndpointPrecedes(candidate, winner) {
			winner = candidate.DeepCopy()
		}
	}
	return winner
}

func networkControllerEndpointPrecedes(left, right *ciskov1.NetworkController) bool {
	leftCreated := left.CreationTimestamp.Time
	rightCreated := right.CreationTimestamp.Time
	if !leftCreated.Equal(rightCreated) {
		return leftCreated.Before(rightCreated)
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return string(left.UID) < string(right.UID)
}

// quiesceDuplicateEndpointPeerWorkers makes the elected endpoint owner an
// active fence, rather than relying solely on each loser receiving its own
// informer event. Cleanup is deliberately staged by quiesceWorker: credentialed
// Pods disappear before RBAC, identity, and bootstrap resources are removed.
func (r *NetworkControllerReconciler) quiesceDuplicateEndpointPeerWorkers(
	ctx context.Context,
	winner *ciskov1.NetworkController,
	peers []ciskov1.NetworkController,
) (bool, error) {
	ordered := append([]ciskov1.NetworkController(nil), peers...)
	sort.Slice(ordered, func(i, j int) bool {
		return networkControllerEndpointPrecedes(&ordered[i], &ordered[j])
	})
	allGone := true
	for i := range ordered {
		peer := &ordered[i]
		if peer.Spec.Endpoint != winner.Spec.Endpoint ||
			(peer.Name == winner.Name && peer.UID == winner.UID) {
			continue
		}
		gone, err := r.quiesceWorker(ctx, peer)
		if err != nil {
			return false, fmt.Errorf("quiesce duplicate endpoint peer %s/%s: %w", peer.Namespace, peer.Name, err)
		}
		if !gone {
			allGone = false
		}
	}
	return allGone, nil
}

func (r *NetworkControllerReconciler) quiesceWorker(ctx context.Context, controller *ciskov1.NetworkController) (bool, error) {
	name := networkControllerWorkerName(controller.Name)
	key := client.ObjectKey{Name: name, Namespace: controller.Namespace}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: controller.Namespace}}
	if err := r.Get(ctx, key, deployment); err == nil {
		if isNetworkControllerWorkerObject(deployment, controller) {
			if deployment.DeletionTimestamp.IsZero() {
				foreground := metav1.DeletePropagationForeground
				if err := r.Delete(ctx, deployment, &client.DeleteOptions{PropagationPolicy: &foreground}); err != nil && !apierrors.IsNotFound(err) {
					return false, fmt.Errorf("foreground-delete controller worker Deployment: %w", err)
				}
			}
			// Retain the worker identity, RBAC, and bootstrap until foreground
			// deletion confirms every ReplicaSet and credentialed Pod is gone.
			return false, nil
		}
	} else if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("fetch controller worker Deployment: %w", err)
	}

	objects := []client.Object{
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: controller.Namespace}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: controller.Namespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: controller.Namespace}},
	}
	pending := false
	for _, object := range objects {
		if err := r.Get(ctx, key, object); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, fmt.Errorf("fetch stale controller worker %T: %w", object, err)
		}
		if !isNetworkControllerWorkerObject(object, controller) {
			continue
		}
		if err := r.Delete(ctx, object); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete stale controller worker %T: %w", object, err)
		}
		pending = true
	}
	return !pending, nil
}

func (r *NetworkControllerReconciler) updateDuplicateEndpointStatus(
	ctx context.Context,
	controller *ciskov1.NetworkController,
	winner *ciskov1.NetworkController,
) error {
	before := controller.DeepCopy()
	controller.Status.Phase = ciskov1.NetworkControllerPhaseError
	controller.Status.Worker = nil
	message := fmt.Sprintf("exact endpoint is already claimed by NetworkController %q in this namespace", winner.Name)
	setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionEndpointUnique, metav1.ConditionFalse, "DuplicateEndpoint", message)
	setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionWorkerAvailable, metav1.ConditionFalse, "DuplicateEndpoint", "controller worker is quiesced while another NetworkController owns this exact endpoint")
	setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionReady, metav1.ConditionFalse, "DuplicateEndpoint", "controller reconciliation is fenced by a duplicate endpoint")
	return r.updateNetworkControllerStatus(ctx, before, controller)
}

func (r *NetworkControllerReconciler) updateUnavailableStatus(
	ctx context.Context,
	controller *ciskov1.NetworkController,
	reason string,
	message string,
	endpointUniquenessConfirmed bool,
) error {
	before := controller.DeepCopy()
	controller.Status.Phase = ciskov1.NetworkControllerPhaseError
	controller.Status.NetAsCode = nil
	controller.Status.Worker = nil
	if endpointUniquenessConfirmed {
		setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionEndpointUnique, metav1.ConditionTrue, "EndpointUnique", "this NetworkController is the deterministic owner of its exact endpoint in this namespace")
	}
	setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionAdapterAvailable, metav1.ConditionFalse, reason, message)
	setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionWorkerAvailable, metav1.ConditionFalse, "WorkerQuiesced", "no controller worker is running")
	setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionReady, metav1.ConditionFalse, reason, "controller reconciliation is unavailable")
	return r.updateNetworkControllerStatus(ctx, before, controller)
}

func (r *NetworkControllerReconciler) updateAvailableStatus(
	ctx context.Context,
	controller *ciskov1.NetworkController,
	descriptor controlleradapter.Descriptor,
	deployment *appsv1.Deployment,
) error {
	before := controller.DeepCopy()
	previousAdapterCondition := meta.FindStatusCondition(controller.Status.Conditions, ciskov1.NetworkControllerConditionAdapterAvailable)
	previousEndpointCondition := meta.FindStatusCondition(controller.Status.Conditions, ciskov1.NetworkControllerConditionEndpointUnique)
	endpointFencedBefore := previousEndpointCondition != nil && previousEndpointCondition.Status == metav1.ConditionFalse && previousEndpointCondition.Reason == "DuplicateEndpoint"
	managerUnavailableBefore := previousAdapterCondition != nil && previousAdapterCondition.Status == metav1.ConditionFalse &&
		(previousAdapterCondition.Reason == "AdapterNotRegistered" || previousAdapterCondition.Reason == "InvalidSpec")
	netAsCode := descriptor.NetAsCode
	netAsCode.ModelVersions = append([]string(nil), descriptor.NetAsCode.ModelVersions...)
	netAsCode.Sections = append([]string(nil), descriptor.NetAsCode.Sections...)
	sort.Strings(netAsCode.ModelVersions)
	sort.Strings(netAsCode.Sections)
	controller.Status.NetAsCode = &netAsCode
	controller.Status.Worker = &ciskov1.NetworkControllerWorkerStatus{
		Name:           deployment.Name,
		DeploymentName: deployment.Name,
		ReadyReplicas:  deployment.Status.ReadyReplicas,
	}
	setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionEndpointUnique, metav1.ConditionTrue, "EndpointUnique", "this NetworkController is the deterministic owner of its exact endpoint in this namespace")
	setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionAdapterAvailable, metav1.ConditionTrue, "AdapterRegistered", fmt.Sprintf("adapter %q is registered", descriptor.Type))

	if controller.Spec.Paused {
		controller.Status.Phase = ciskov1.NetworkControllerPhasePaused
		setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionWorkerAvailable, metav1.ConditionFalse, "Paused", "controller worker is intentionally scaled to zero")
		setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionReady, metav1.ConditionFalse, "Paused", "controller reconciliation is paused")
	} else if deploymentReadyForCurrentGeneration(deployment) {
		if controller.Status.Phase == "" || controller.Status.Phase == ciskov1.NetworkControllerPhasePending || controller.Status.Phase == ciskov1.NetworkControllerPhasePaused || managerUnavailableBefore || endpointFencedBefore {
			controller.Status.Phase = ciskov1.NetworkControllerPhaseConnecting
		}
		setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionWorkerAvailable, metav1.ConditionTrue, "DeploymentAvailable", "controller worker Deployment has a ready replica")
	} else {
		// Phase and status.observedGeneration are adapter-owned while a
		// registered worker exists. Conditions below make a stale adapter phase
		// unambiguously not Ready during rollout without falsely claiming the new
		// NetworkController generation was processed remotely.
		if controller.Status.Phase == "" || controller.Status.Phase == ciskov1.NetworkControllerPhasePaused || managerUnavailableBefore || endpointFencedBefore {
			controller.Status.Phase = ciskov1.NetworkControllerPhasePending
		}
		setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionWorkerAvailable, metav1.ConditionFalse, "DeploymentProgressing", "controller worker Deployment has no ready replicas")
		setNetworkControllerCondition(&controller.Status.Conditions, controller.Generation, ciskov1.NetworkControllerConditionReady, metav1.ConditionFalse, "WorkerUnavailable", "controller worker Deployment has no ready replicas")
	}
	return r.updateNetworkControllerStatus(ctx, before, controller)
}

func deploymentReadyForCurrentGeneration(deployment *appsv1.Deployment) bool {
	if deployment.Status.ReadyReplicas < 1 {
		return false
	}
	// Generation is zero only in lightweight fake clients. Real API-server
	// objects start at generation one and require the Deployment controller to
	// acknowledge the current Pod template before the worker is available.
	if deployment.Generation == 0 {
		return true
	}
	return deployment.Status.ObservedGeneration >= deployment.Generation && deployment.Status.UpdatedReplicas >= 1
}

func (r *NetworkControllerReconciler) updateNetworkControllerStatus(ctx context.Context, before, after *ciskov1.NetworkController) error {
	if reflect.DeepEqual(before.Status, after.Status) {
		return nil
	}
	// NetworkController status is intentionally multi-writer: the manager owns
	// adapter/worker availability while the product adapter owns remote
	// connection conditions. Status().Update carries resourceVersion so a
	// concurrent adapter write conflicts and is retried instead of being
	// silently overwritten by a merge patch that replaces the conditions list.
	if err := r.Status().Update(ctx, after); err != nil {
		return fmt.Errorf("update NetworkController status: %w", err)
	}
	return nil
}

func setNetworkControllerCondition(conditions *[]metav1.Condition, generation int64, conditionType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	})
}

func networkControllerWorkerName(controllerName string) string {
	const suffix = "-controller-worker"
	// All managed child kinds accept DNS subdomains. Preserve dots so distinct
	// valid controller names such as "foo.bar" and "foo-bar" never collide.
	candidate := controllerName + suffix
	if len(candidate) <= 63 {
		return candidate
	}
	digest := sha256.Sum256([]byte(controllerName))
	hash := hex.EncodeToString(digest[:])[:10]
	prefixLength := 63 - len(suffix) - len(hash) - 1
	prefix := strings.TrimRight(controllerName[:prefixLength], "-.")
	if prefix == "" {
		prefix = "controller"
	}
	return prefix + "-" + hash + suffix
}

func networkControllerWorkerLabels(workerName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "cisco-vk",
		"app.kubernetes.io/instance":   workerName,
		"app.kubernetes.io/component":  "network-controller-worker",
		"app.kubernetes.io/managed-by": "networkcontroller-controller",
	}
}

func networkControllerContractHash(controller *ciskov1.NetworkController, descriptor controlleradapter.Descriptor, bootstrap string) string {
	payload, err := json.Marshal(struct {
		Spec       ciskov1.NetworkControllerSpec `json:"spec"`
		Descriptor controlleradapter.Descriptor  `json:"descriptor"`
		Bootstrap  string                        `json:"bootstrap"`
	}{
		Spec:       controller.Spec,
		Descriptor: descriptor,
		Bootstrap:  bootstrap,
	})
	if err != nil {
		// Both inputs are JSON-serializable by construction; retain a stable
		// fallback so an unexpected custom encoder cannot suppress rollouts.
		payload = []byte(fmt.Sprintf("%#v/%#v/%s", controller.Spec, descriptor, bootstrap))
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// setNetworkControllerWorkerMetadata records explicit manager ownership rather
// than an ownerReference. The NetworkController finalizer must retain workers
// while dependent configs exist even when endpoint deletion uses foreground
// cascading.
func setNetworkControllerWorkerMetadata(object metav1.Object, controller *ciskov1.NetworkController, labels map[string]string) {
	object.SetLabels(cloneStringMap(labels))
	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 2)
	}
	annotations[networkControllerNameAnnotation] = controller.Name
	annotations[networkControllerUIDAnnotation] = string(controller.UID)
	object.SetAnnotations(annotations)
	// Clear a legacy ownerReference after positively identifying it. This makes
	// an in-place upgrade safe against foreground garbage collection.
	object.SetOwnerReferences(nil)
}

func isNetworkControllerWorkerObject(object metav1.Object, controller *ciskov1.NetworkController) bool {
	if owner := metav1.GetControllerOf(object); owner != nil {
		return owner.APIVersion == ciskov1.GroupVersion.String() &&
			owner.Kind == "NetworkController" &&
			owner.Name == controller.Name &&
			owner.UID == controller.UID
	}
	annotations := object.GetAnnotations()
	return annotations[networkControllerNameAnnotation] == controller.Name &&
		annotations[networkControllerUIDAnnotation] == string(controller.UID)
}

func intstrFromString(value string) intstr.IntOrString {
	return intstr.FromString(value)
}

func networkControllerConfigRefIndexValues(object client.Object) []string {
	config, ok := object.(*configv1alpha1.NetworkControllerConfig)
	if !ok || config.Spec.ControllerRef.Name == "" {
		return nil
	}
	return []string{config.Spec.ControllerRef.Name}
}

func networkControllerEndpointIndexValues(object client.Object) []string {
	controller, ok := object.(*ciskov1.NetworkController)
	if !ok || controller.Spec.Endpoint == "" {
		return nil
	}
	return []string{controller.Spec.Endpoint}
}

// networkControllerEndpointPeerRequests fans an endpoint identity event out to
// every exact same-namespace peer. The winner independently quiesces peer
// workers during reconciliation, so a transient mapping/list failure delays but
// cannot bypass the endpoint fence.
func (r *NetworkControllerReconciler) networkControllerEndpointPeerRequests(
	ctx context.Context,
	object client.Object,
) []ctrl.Request {
	controller, ok := object.(*ciskov1.NetworkController)
	if !ok || controller.Name == "" || controller.Namespace == "" {
		return nil
	}

	requests := map[client.ObjectKey]struct{}{
		client.ObjectKeyFromObject(controller): {},
	}
	if controller.Spec.Endpoint != "" {
		peers, err := r.networkControllerEndpointPeers(ctx, controller)
		if err != nil {
			log.FromContext(ctx).Error(err, "map NetworkController endpoint peers",
				"networkController", client.ObjectKeyFromObject(controller),
			)
		} else {
			for i := range peers {
				peer := &peers[i]
				if peer.Spec.Endpoint != controller.Spec.Endpoint {
					continue
				}
				requests[client.ObjectKeyFromObject(peer)] = struct{}{}
			}
		}
	}

	ordered := make([]client.ObjectKey, 0, len(requests))
	for request := range requests {
		ordered = append(ordered, request)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Namespace != ordered[j].Namespace {
			return ordered[i].Namespace < ordered[j].Namespace
		}
		return ordered[i].Name < ordered[j].Name
	})
	out := make([]ctrl.Request, 0, len(ordered))
	for _, request := range ordered {
		out = append(out, ctrl.Request{NamespacedName: request})
	}
	return out
}

func networkControllerWorkerRequests(_ context.Context, object client.Object) []ctrl.Request {
	annotations := object.GetAnnotations()
	name := annotations[networkControllerNameAnnotation]
	if name == "" || annotations[networkControllerUIDAnnotation] == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{
		Namespace: object.GetNamespace(),
		Name:      name,
	}}}
}

// specOrDeletionChangedPredicate prevents status-only writes from every
// adapter worker from hot-looping the central orchestration controller while
// still observing deletion requests, which do not increment generation.
func specOrDeletionChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return true },
		UpdateFunc: func(update event.UpdateEvent) bool {
			if update.ObjectOld == nil || update.ObjectNew == nil {
				return true
			}
			return update.ObjectOld.GetGeneration() != update.ObjectNew.GetGeneration() ||
				!reflect.DeepEqual(update.ObjectOld.GetDeletionTimestamp(), update.ObjectNew.GetDeletionTimestamp()) ||
				!reflect.DeepEqual(update.ObjectOld.GetFinalizers(), update.ObjectNew.GetFinalizers())
		},
	}
}

// SetupWithManager registers the generic orchestration controller. Adapter
// descriptors are visible here, but factories are invoked only by the
// isolated controller-worker command.
func (r *NetworkControllerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&ciskov1.NetworkController{},
		networkControllerEndpointIndex,
		networkControllerEndpointIndexValues,
	); err != nil {
		return fmt.Errorf("index NetworkController endpoint: %w", err)
	}
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&configv1alpha1.NetworkControllerConfig{},
		networkControllerRefIndex,
		networkControllerConfigRefIndexValues,
	); err != nil {
		return fmt.Errorf("index NetworkControllerConfig controllerRef: %w", err)
	}
	workerHandler := handler.EnqueueRequestsFromMapFunc(networkControllerWorkerRequests)
	endpointPeerHandler := handler.EnqueueRequestsFromMapFunc(r.networkControllerEndpointPeerRequests)
	return ctrl.NewControllerManagedBy(mgr).
		For(&ciskov1.NetworkController{}, builder.WithPredicates(specOrDeletionChangedPredicate())).
		Watches(&ciskov1.NetworkController{}, endpointPeerHandler, builder.WithPredicates(specOrDeletionChangedPredicate())).
		Watches(&appsv1.Deployment{}, workerHandler).
		Watches(&corev1.ConfigMap{}, workerHandler).
		Watches(&corev1.ServiceAccount{}, workerHandler).
		Watches(&rbacv1.RoleBinding{}, workerHandler).
		Watches(&configv1alpha1.NetworkControllerConfig{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, object client.Object) []ctrl.Request {
				config, ok := object.(*configv1alpha1.NetworkControllerConfig)
				if !ok || config.Spec.ControllerRef.Name == "" {
					return nil
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKey{
					Namespace: config.Namespace,
					Name:      config.Spec.ControllerRef.Name,
				}}}
			},
		), builder.WithPredicates(specOrDeletionChangedPredicate())).
		Complete(r)
}
