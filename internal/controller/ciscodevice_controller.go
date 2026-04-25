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
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
)

const (
	// ciscoDeviceFinalizer is added to every CiscoDevice so the controller can
	// clean up the VK node before the object is removed from the API server.
	ciscoDeviceFinalizer = "cisco.vk/device-cleanup"

	// configMapSuffix is appended to the CiscoDevice name for the ConfigMap.
	configMapSuffix = "-config"
	// deploymentSuffix is appended to the CiscoDevice name for the Deployment.
	deploymentSuffix = "-vk"
	// configMountPath is where the config YAML is mounted in the VK container.
	configMountPath = "/etc/virtual-kubelet"
	// configFileName is the key used inside the ConfigMap.
	configFileName = "config.yaml"
	// tlsGenMountPath is the writable directory where the VK process writes
	// its self-signed TLS certificate when no Secret-provided cert is found.
	// An emptyDir is mounted here so the path is writable even on a RORFS.
	varLibMountPath = "/var/lib/virtual-kubelet"
	// DefaultImage is the default container image for the VK deployment.
	DefaultImage = "ghcr.io/cisco/virtual-kubelet-cisco:latest"
	// DefaultServiceAccount is the shared service account used by all VK deployments.
	DefaultServiceAccount = "cisco-virtual-kubelet"
)

// configPrereqsTeardownPollInterval is how often the deletion-finalizer
// path requeues itself while waiting for the per-device reconciler to
// converge the empty-intent reconcile. Short enough that an operator
// kubectl-deleting a CiscoDevice doesn't perceive a stall, long enough
// that the controller doesn't spin while the engine is mid-tick.
const configPrereqsTeardownPollInterval = 5 * time.Second

// CiscoDeviceReconciler reconciles a CiscoDevice object.
// It creates (or updates) a ConfigMap containing the device spec and
// a Deployment that runs the cisco-vk binary with that configuration.
type CiscoDeviceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Image overrides the VK container image (defaults to DefaultImage).
	Image string
	// ServiceAccount is the name of the service account for VK pods (defaults to DefaultServiceAccount).
	ServiceAccount string

	// AggregatorEnabled mirrors the manager's --enable-config-aggregator
	// flag. Wave 1C: when true, the in-process aggregator owns the
	// per-device config-reconcile loop, so the controller must NOT
	// also spin up a per-device cisco-vk pod that runs its own
	// ConfigReconciler — that would produce a duplicate-writer hazard
	// against the same (device, family) lease scope.
	//
	// The behaviour split:
	//   - device.Spec.Driver has a registered config driver → no
	//     Deployment is created (aggregator owns the device).
	//   - device.Spec.Driver registered for apphosting only (no
	//     configdriver) → Deployment is still created, but with
	//     DISABLE_IN_POD_CONFIG_RECONCILER=true env so the cisco-vk
	//     binary skips its own ConfigReconciler.
	//
	// AggregatorEnabled=false (the default) keeps the historical
	// per-pod-per-device topology unchanged.
	AggregatorEnabled bool
}

// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs/status,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// The controller spawns per-device cisco-vk Deployments in the
// device's namespace and references a shared ServiceAccount
// `cisco-virtual-kubelet`. The chart only seeds that SA in the
// release namespace, so for any tenant namespace hosting a
// CiscoDevice the controller must ensure the SA + a RoleBinding to
// the existing `cisco-virtual-kubelet` ClusterRole exists locally.
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// Required by the API server's privilege-escalation check: to bind
// the ClusterRole into a tenant namespace the controller must hold
// the same permissions itself. The controller already does (via the
// chart's controller ClusterRole), but RBAC also requires the
// explicit `bind` verb on the target ClusterRole.
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames=cisco-virtual-kubelet,verbs=bind

// Reconcile ensures a ConfigMap and Deployment exist for each CiscoDevice.
func (r *CiscoDeviceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// ── 1. Fetch the CiscoDevice ────────────────────────────────────────
	var device ciskov1.CiscoDevice
	if err := r.Get(ctx, req.NamespacedName, &device); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("CiscoDevice not found – already deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch CiscoDevice: %w", err)
	}

	// ── 2. Handle deletion (finalizer) ───────────────────────────────────
	if !device.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&device, ciscoDeviceFinalizer) {
			logger.Info("CiscoDevice deleted – cleaning up VK node", "node", device.Name)

			// Wave 4A — drive prereq teardown BEFORE removing the
			// finalizer. Treat the device as if spec.configPrereqs
			// had been cleared: the empty-intent reconcile, prune,
			// and then CR delete play out the same way a deliberate
			// removal does. teardownComplete=false means the per-
			// device reconciler hasn't yet driven the empty intent
			// to InSync; we requeue without removing the finalizer
			// so the CiscoDevice (and its owned IOSXEConfig) stay
			// alive long enough for device-side cleanup to land.
			deviceCopy := device.DeepCopy()
			deviceCopy.Spec.ConfigPrereqs = nil
			done, err := r.reconcileConfigPrereqs(ctx, deviceCopy)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("prereq teardown during deletion: %w", err)
			}
			if !done {
				logger.Info("CiscoDevice deletion: awaiting prereq teardown",
					"device", device.Name)
				// Requeue: the per-device reconciler emits the empty
				// intent to the device, then flips status.phase to
				// InSync; the next finalizer pass picks that up and
				// completes the cleanup.
				return ctrl.Result{RequeueAfter: configPrereqsTeardownPollInterval}, nil
			}

			if err := r.deleteNode(ctx, device.Name); err != nil {
				return ctrl.Result{}, err
			}
			// The per-device ClusterRoleBinding is cluster-scoped and
			// therefore cannot be GC'd via ownerReferences from a
			// namespaced CiscoDevice. Delete it explicitly here.
			if err := r.deleteVKClusterRoleBinding(ctx, &device); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&device, ciscoDeviceFinalizer)
			if err := r.Update(ctx, &device); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// ── 3. Ensure finalizer is registered ───────────────────────────────
	if !controllerutil.ContainsFinalizer(&device, ciscoDeviceFinalizer) {
		controllerutil.AddFinalizer(&device, ciscoDeviceFinalizer)
		if err := r.Update(ctx, &device); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
	}

	// ── 4. Render the device config YAML ────────────────────────────────
	configData, err := renderDeviceConfig(&device.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to render device config: %w", err)
	}

	// ── 5. Reconcile the ConfigMap ──────────────────────────────────────
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      device.Name + configMapSuffix,
			Namespace: device.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = map[string]string{
			configFileName: configData,
		}
		return controllerutil.SetControllerReference(&device, cm, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile ConfigMap: %w", err)
	}
	logger.Info("ConfigMap reconciled", "name", cm.Name, "operation", op)

	// ── 5b. Ensure VK SA + RoleBinding exist in the device's namespace ──
	saName := r.ServiceAccount
	if saName == "" {
		saName = DefaultServiceAccount
	}
	if err := r.ensureVKAccess(ctx, &device, saName); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure VK access: %w", err)
	}

	// Wave 1C — aggregator/per-pod exclusivity. When the aggregator
	// owns this device (it has a registered configdriver factory),
	// skip Deployment creation entirely. Devices with apphosting only
	// (no configdriver) still get a pod, but with the in-pod
	// ConfigReconciler disabled via env so the aggregator and the
	// pod do not concurrently write the same (device, family).
	configDriverRegistered := drivers.ConfigDriverRegistered(device.Spec.Driver)
	if r.AggregatorEnabled && configDriverRegistered {
		// Garbage-collect any pre-existing Deployment from a previous
		// non-aggregator install. Owner-ref'd to the CiscoDevice, so
		// delete is safe and operator-visible via standard kubectl
		// describe / events on the device.
		stale := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      device.Name + deploymentSuffix,
				Namespace: device.Namespace,
			},
		}
		if err := r.Delete(ctx, stale); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete stale per-device Deployment under aggregator mode: %w", err)
		}
		logger.Info("aggregator owns this device; skipping per-device Deployment",
			"device", device.Name, "driver", device.Spec.Driver)
		// Still reconcile configPrereqs and update status. Teardown
		// completion is irrelevant on the upsert path — only the
		// finalizer cares about it.
		if _, err := r.reconcileConfigPrereqs(ctx, &device); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile configPrereqs: %w", err)
		}
		if err := r.updateStatus(ctx, &device, nil); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// ── 6. Reconcile the Deployment ─────────────────────────────────────
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      device.Name + deploymentSuffix,
			Namespace: device.Namespace,
		},
	}

	image := r.Image
	if image == "" {
		image = DefaultImage
	}

	serviceAccount := r.ServiceAccount
	if serviceAccount == "" {
		serviceAccount = DefaultServiceAccount
	}

	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		// Immutable labels used as selector.
		labels := map[string]string{
			"app.kubernetes.io/name":       "cisco-vk",
			"app.kubernetes.io/instance":   device.Name,
			"app.kubernetes.io/managed-by": "ciscodevice-controller",
		}

		var replicas int32 = 1
		deploy.Spec.Replicas = &replicas

		deploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: labels,
		}

		// Wave 6B (external-review-followup Finding #5): record
		// the referenced credential Secret's resourceVersion as a
		// pod-template annotation. Kubernetes does NOT restart pods
		// when a Secret-backed env var rotates (env values are
		// resolved at pod start), so without an explicit rollout
		// signal a credentialSecretRef rotation has no effect on
		// running pods until something else triggers a restart.
		// The annotation flips on every Secret update; controller-
		// runtime rolls the Deployment naturally because the pod
		// template has changed.
		credAnno := r.lookupCredentialResourceVersion(ctx, &device)

		annos := map[string]string{
			// Force a rollout whenever the ConfigMap content changes.
			"cisco.vk/config-hash": shortHash(configData),
		}
		if credAnno != "" {
			annos["cisco.vk/credential-resource-version"] = credAnno
		}
		deploy.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels:      labels,
			Annotations: annos,
		}

		// Build credential env vars. When a Secret reference is provided,
		// use valueFrom so the kubelet resolves the password at pod startup
		// (the controller never reads the Secret itself). Otherwise fall back
		// to injecting the plaintext password as a direct env var for backward
		// compatibility with CRDs that set spec.password directly.
		var credEnv []corev1.EnvVar
		if device.Spec.CredentialSecretRef != nil {
			credEnv = append(credEnv, corev1.EnvVar{
				Name: "VK_DEVICE_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: *device.Spec.CredentialSecretRef,
						Key:                  "password",
					},
				},
			})
		} else if device.Spec.Password != "" {
			credEnv = append(credEnv, corev1.EnvVar{
				Name:  "VK_DEVICE_PASSWORD",
				Value: device.Spec.Password,
			})
		}

		// Expose POD_NAMESPACE via the downward API so the config
		// reconciler can scope its coordination.k8s.io/v1 Leases
		// alongside the running pod.
		credEnv = append(credEnv, corev1.EnvVar{
			Name: "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
			},
		})
		// Wave 7A.3 — inject the pod's UID so the in-pod
		// ConfigReconciler can build a runtime-suffixed lease
		// holder identity ("<ns>/<name>#<podUID>"). Two pods
		// running the same CR identity during a Deployment
		// rollout (e.g. credential-secret rotation, image bump)
		// then have distinct lease holders and cannot both
		// renew the same lease.
		credEnv = append(credEnv, corev1.EnvVar{
			Name: "POD_UID",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"},
			},
		})

		// Propagate CONFIG_LEASE_NAMESPACE from the controller's own
		// environment so every cisco-vk pod arbitrates against the
		// same shared namespace. When unset, each pod falls back to
		// its own POD_NAMESPACE — historical per-pod-namespace
		// behaviour. The controller's env is fed by the Helm values
		// (configLeaseNamespace) so an operator who needs cross-
		// namespace arbitration sets it once cluster-wide rather
		// than per-device.
		if v := os.Getenv("CONFIG_LEASE_NAMESPACE"); v != "" {
			credEnv = append(credEnv, corev1.EnvVar{
				Name:  "CONFIG_LEASE_NAMESPACE",
				Value: v,
			})
		}

		// Wave 1C — aggregator/per-pod exclusivity. When the controller
		// is in aggregator mode AND this device's apphosting pod still
		// gets created (apphosting-only platforms whose configdriver
		// isn't registered), tell the cisco-vk binary to skip its own
		// in-pod ConfigReconciler. The aggregator owns the config
		// loop; the pod is here only to host apphosting workloads.
		// Devices whose driver IS configdriver-registered never
		// reach this code path (the §6 short-circuit above deletes
		// the Deployment).
		if r.AggregatorEnabled {
			credEnv = append(credEnv, corev1.EnvVar{
				Name:  "DISABLE_IN_POD_CONFIG_RECONCILER",
				Value: "true",
			})
		}

		deploy.Spec.Template.Spec = corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "cisco-vk",
					Image: image,
					Args:  vkContainerArgs(device.Name, device.Spec.LogLevel),
					Env:   credEnv,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "device-config",
							MountPath: configMountPath + "/" + configFileName,
							SubPath:   configFileName,
							ReadOnly:  true,
						},
						{
							Name:      "tls-gen",
							MountPath: varLibMountPath,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "device-config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: cm.Name,
							},
						},
					},
				},
				{
					// emptyDir provides a writable scratch space for the
					// self-signed TLS cert generated at startup. Using an
					// explicit emptyDir ensures this works on a RORFS.
					Name: "tls-gen",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			// Use shared service account with VK RBAC permissions
			ServiceAccountName: serviceAccount,
		}

		return controllerutil.SetControllerReference(&device, deploy, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile Deployment: %w", err)
	}
	logger.Info("Deployment reconciled", "name", deploy.Name, "operation", op)

	// ── 6b. Reconcile the owned IOSXEConfig (configPrereqs) ─────────────
	// Teardown completion is irrelevant on the upsert path — only the
	// deletion path's finalizer waits on it. Wave 4A.
	if _, err := r.reconcileConfigPrereqs(ctx, &device); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile configPrereqs: %w", err)
	}

	// ── 7. Update CiscoDevice status ────────────────────────────────────
	if err := r.updateStatus(ctx, &device, deploy); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// vkSharedClusterRole is the cluster-scoped Role the chart ships with
// the VK pod permissions baked in. The controller does not own this
// object — it only binds it for each device's SA via a per-device
// ClusterRoleBinding so cisco-vk pods spawned in any tenant namespace
// pick up the same permission set.
const vkSharedClusterRole = "cisco-virtual-kubelet"

// vkClusterRoleBindingName composes the deterministic, per-device CRB
// name. Including both namespace and device name avoids collisions
// when two devices in different namespaces share the same SA name.
func vkClusterRoleBindingName(namespace, deviceName string) string {
	return "cisco-virtual-kubelet-" + namespace + "-" + deviceName
}

// ensureVKAccess provisions the bits the chart cannot pre-bake for a
// dynamic device namespace:
//
//   - a ServiceAccount in the device's namespace, and
//   - a ClusterRoleBinding that binds the chart-supplied ClusterRole
//     `cisco-virtual-kubelet` to that SA.
//
// Why ClusterRoleBinding rather than namespace-scoped RoleBinding: the
// cisco-vk binary runs controller-runtime managers that establish
// cluster-scope informers (Secrets, IOSXEConfig, IOSXEConfigDefaults,
// Service, ConfigMap, pods-for-recovery). A namespace-scoped binding
// can't authorize those LIST/WATCH calls, and the watch-error
// reflectors loop indefinitely in the pod's logs. The privilege set
// is the same as the chart already grants to the SA in the release
// namespace; we're just extending it to the per-device SAs.
//
// The ServiceAccount is owner-ref'd to the CiscoDevice so it's GC'd
// with the device. The ClusterRoleBinding is cluster-scoped, so K8s
// forbids ownerReferences pointing at a namespaced object — instead
// we clean it up explicitly in the device's finalizer path.
func (r *CiscoDeviceReconciler) ensureVKAccess(ctx context.Context, device *ciskov1.CiscoDevice, saName string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: device.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		// Owner-ref the FIRST CiscoDevice in this namespace; subsequent
		// devices share the SA without re-claiming ownership.
		if len(sa.OwnerReferences) == 0 {
			return controllerutil.SetControllerReference(device, sa, r.Scheme)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("ServiceAccount %s/%s: %w", sa.Namespace, sa.Name, err)
	}

	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: vkClusterRoleBindingName(device.Namespace, device.Name),
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, crb, func() error {
		crb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     vkSharedClusterRole,
		}
		crb.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      saName,
			Namespace: device.Namespace,
		}}
		// Tag with a label so the finalizer can find these CRBs by
		// label-selector even if the device name has been mutated
		// (it shouldn't, but defence in depth).
		if crb.Labels == nil {
			crb.Labels = map[string]string{}
		}
		crb.Labels["cisco.vk/device-namespace"] = device.Namespace
		crb.Labels["cisco.vk/device-name"] = device.Name
		return nil
	}); err != nil {
		return fmt.Errorf("ClusterRoleBinding %s: %w", crb.Name, err)
	}
	return nil
}

// deleteVKClusterRoleBinding removes the per-device ClusterRoleBinding
// that ensureVKAccess created. Called from the finalizer path because
// cluster-scoped resources cannot be GC'd by ownerReferences pointing
// at a namespaced CiscoDevice.
func (r *CiscoDeviceReconciler) deleteVKClusterRoleBinding(ctx context.Context, device *ciskov1.CiscoDevice) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: vkClusterRoleBindingName(device.Namespace, device.Name),
		},
	}
	if err := r.Delete(ctx, crb); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete ClusterRoleBinding %s: %w", crb.Name, err)
	}
	return nil
}

// apphostingPrereqFamilies is the closed set of IOSXEConfig families
// the controller permits on a controller-owned configPrereqs CR. Any
// family outside this set in the inline body is silently dropped from
// ManagedFamilies so the operator cannot accidentally widen the
// controller's scope past the apphosting prerequisites.
var apphostingPrereqFamilies = []string{
	"interface_virtual_port_group",
	"dhcp",
	"access_list_extended",
}

// ownedIOSXEConfigName returns the deterministic name of the
// configPrereqs-owned IOSXEConfig CR; one per CiscoDevice.
func ownedIOSXEConfigName(deviceName string) string {
	return deviceName + "-prereqs"
}

// emptyPrereqInline returns a netascode-shaped empty-body
// runtime.RawExtension. Each prereq family is mapped to an empty
// object so the engine's per-family Diff sees "no entries desired"
// and the writer's PruneDiff returns DELETE ops for every entry
// observed on the device.
//
// Wave 4A-fu (external-review-followup Finding #2): the previous
// teardown set ManagedFamilies=nil + source=nil, which was rejected
// at admission (CRD MinItems=1 on managedFamilies) AND by the
// engine (validate() rejects empty ManagedFamilies). Keeping the
// families and emptying the source is the schema-valid + engine-
// accepted path that actually drives the prune.
func emptyPrereqInline() runtime.RawExtension {
	// Build a JSON object mapping each prereq family to an empty
	// object. Equivalent in netascode YAML:
	//   interface_virtual_port_group: {}
	//   dhcp: {}
	//   access_list_extended: {}
	body := []byte(`{"interface_virtual_port_group":{},"dhcp":{},"access_list_extended":{}}`)
	return runtime.RawExtension{Raw: body}
}

// isPrereqTearingDown reports whether the owned IOSXEConfig has
// already been driven into the teardown shape (Wave 4A-fu): the
// prereq family list intact, source.inline set to empty, and
// pruneOnRelinquish=true. The teardown step that mutates the CR
// runs once; subsequent ticks observe this predicate true and
// transition to step 2 (await InSync).
func isPrereqTearingDown(cr *configv1alpha1.IOSXEConfig) bool {
	if !cr.Spec.PruneOnRelinquish {
		return false
	}
	if cr.Spec.Source.Inline == nil {
		return false
	}
	// "Empty" for our purposes means the inline body is the
	// empty-prereq object literal we wrote in step 1. We compare
	// raw bytes — the body is generated, not operator-authored,
	// so a byte-equal check is sufficient and stable.
	return string(cr.Spec.Source.Inline.Raw) == string(emptyPrereqInline().Raw)
}

// reconcileConfigPrereqs creates, updates, or tears down an owned
// IOSXEConfig CR that mirrors CiscoDevice.spec.configPrereqs.
// Behaviour:
//
//   - spec.configPrereqs != nil → upsert an IOSXEConfig with
//     ManagedFamilies filtered to the prereq set, spec.source.inline
//     set to the provided configuration, AND pruneOnRelinquish=true
//     so a later relinquishment (operator removes spec.configPrereqs
//     or deletes the CiscoDevice) actually reverts those families on
//     the device.
//
//   - spec.configPrereqs == nil → drive a teardown sequence
//     (Wave 4A-fu, external-review-followup Finding #2; Wave 4A's
//     original take cleared ManagedFamilies and was rejected by
//     both the CRD's MinItems=1 and the engine's empty-list check).
//     Now:
//       1. KEEP the prereq family list (apphostingPrereqFamilies)
//          on the owned CR. Set spec.source.inline to an empty
//          netascode body. The engine then iterates each family
//          with empty desired and PruneOnRelinquish=true, so each
//          family's PruneCapable.PruneDiff returns DELETE ops for
//          every entry currently on the device.
//       2. Wait for status.phase == InSync (the per-device
//          reconciler has driven the empty intent fully). May take
//          1-2 reconcile ticks; the controller-runtime requeue
//          path handles the wait without busy-looping.
//       3. Once InSync, delete the owned CR.
//
// The owned CR's owner reference is still the CiscoDevice for safety,
// so a forced kubectl delete --cascade=foreground on the CiscoDevice
// still GC's the CR — but the *normal* path runs through teardown so
// the device state is reverted before the CR vanishes.
//
// teardownComplete reports whether the prereq cleanup finished: the
// caller can then either skip waiting (caller is in non-deletion path)
// or remove its finalizer (caller is in deletion path).
func (r *CiscoDeviceReconciler) reconcileConfigPrereqs(ctx context.Context, device *ciskov1.CiscoDevice) (teardownComplete bool, err error) {
	name := ownedIOSXEConfigName(device.Name)
	key := types.NamespacedName{Namespace: device.Namespace, Name: name}

	var existing configv1alpha1.IOSXEConfig
	getErr := r.Get(ctx, key, &existing)
	found := getErr == nil
	if getErr != nil && !errors.IsNotFound(getErr) {
		return false, fmt.Errorf("get owned IOSXEConfig: %w", getErr)
	}

	// Teardown path: configPrereqs removed (or device being deleted —
	// the deletion path also calls us with configPrereqs effectively
	// "should be nil").
	if device.Spec.ConfigPrereqs == nil {
		if !found {
			// Nothing to tear down; the cleanup completed previously
			// (or the operator never authored prereqs).
			return true, nil
		}
		// Step 1: drive the CR to empty source (NOT empty
		// ManagedFamilies — see Wave 4A-fu rationale above). The
		// engine then runs each family's PruneCapable.PruneDiff
		// against (empty desired, full observed), producing
		// VerbDelete ops for every entry the controller previously
		// wrote.
		if !isPrereqTearingDown(&existing) {
			updated := existing.DeepCopy()
			updated.Spec.ManagedFamilies = append([]string(nil), apphostingPrereqFamilies...)
			emptyInline := emptyPrereqInline()
			updated.Spec.Source = configv1alpha1.ConfigurationSource{Inline: &emptyInline}
			updated.Spec.PruneOnRelinquish = true
			if err := r.Update(ctx, updated); err != nil {
				return false, fmt.Errorf("drive owned IOSXEConfig to empty intent: %w", err)
			}
			log.FromContext(ctx).Info("configPrereqs teardown: empty-intent applied; awaiting InSync",
				"iosxeconfig", name)
			// Not yet complete; re-queue via the controller-runtime
			// path. The owned CR's status will eventually flip to
			// InSync and the next reconcile will hit step 2.
			return false, nil
		}
		// Step 2: wait for the per-device reconciler to converge.
		// Two conditions, both required:
		//
		//   - status.observedGeneration == metadata.generation:
		//     the per-device reconciler has SEEN and ACTED on the
		//     post-teardown spec, not a prior generation. Without
		//     this gate the previous reconcile's stale "InSync"
		//     could pass before the teardown intent was even
		//     observed, so the controller would delete the CR
		//     before any prune ran on the device.
		//   - status.phase == "InSync": the engine wrote it after
		//     a clean apply against the empty intent.
		//
		// Wave 7A.2 (external-review-next-actions Finding #2). The
		// pre-fix gate checked Phase only; Wave 4A-fu's unit test
		// passed because it set Phase=InSync directly via r.Update
		// — which is fine for the test, but a real per-device
		// reconciler updates status asynchronously and Phase can
		// be stale.
		if existing.Status.ObservedGeneration != existing.Generation {
			log.FromContext(ctx).V(1).Info("configPrereqs teardown: awaiting per-device reconciler to observe post-teardown generation",
				"iosxeconfig", name,
				"observedGeneration", existing.Status.ObservedGeneration,
				"generation", existing.Generation)
			return false, nil
		}
		if existing.Status.Phase != "InSync" {
			log.FromContext(ctx).V(1).Info("configPrereqs teardown: awaiting InSync",
				"iosxeconfig", name, "phase", existing.Status.Phase)
			return false, nil
		}
		// Step 3: prune complete; delete the CR.
		if err := r.Delete(ctx, &existing); err != nil && !errors.IsNotFound(err) {
			return false, fmt.Errorf("delete owned IOSXEConfig after teardown: %w", err)
		}
		log.FromContext(ctx).Info("configPrereqs teardown: complete", "iosxeconfig", name)
		return true, nil
	}

	// Upsert path.
	desired := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: device.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.Spec = configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device.Name},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: apphostingPrereqFamilies,
				Source: configv1alpha1.ConfigurationSource{
					Inline: &device.Spec.ConfigPrereqs.Configuration,
				},
				// Prereqs default to revert so the device stays in the shape
				// apphosting needs; operators can still opt a separate
				// IOSXEConfig into report/pause for wider declarative config.
				DriftPolicy: configv1alpha1.DriftPolicyRevert,
				// Wave 7A.4 (external-review-next-actions Finding #4):
				// Steady-state configPrereqs is ADDITIVE — the engine
				// merges the prereq source into the device but does NOT
				// prune unrelated entries operators may have added
				// out-of-band in the same families.
				//
				// pruneOnRelinquish triggers PruneCapable.PruneDiff
				// every reconcile while families are managed (engine
				// runs PruneDiff inside the per-family loop), which
				// would silently delete operator-added device-side
				// entries. That breaks operator trust and the
				// "configPrereqs is just bring-up" mental model.
				//
				// The teardown path (above) sets pruneOnRelinquish=true
				// only when driving the empty-source intent — the only
				// situation where the controller WANTS authoritative
				// pruning of the prereq family set.
				PruneOnRelinquish: false,
			},
		}
		return controllerutil.SetControllerReference(device, desired, r.Scheme)
	})
	if err != nil {
		return false, fmt.Errorf("upsert owned IOSXEConfig: %w", err)
	}
	log.FromContext(ctx).Info("configPrereqs reconciled",
		"iosxeconfig", name, "operation", op)
	return true, nil
}

// SetupWithManager registers the controller with the manager.
func (r *CiscoDeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ciskov1.CiscoDevice{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&configv1alpha1.IOSXEConfig{}).
		// Wave 6B: watch credential Secrets so a rotation of
		// CiscoDevice.spec.credentialSecretRef triggers a
		// reconcile that rolls the per-device pod via a fresh
		// pod-template annotation. Without this watch, K8s
		// silently keeps the pod running with the old credential
		// because env-var Secret values are resolved at pod start
		// — they don't auto-update on Secret rotation.
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToCiscoDevices)).
		Complete(r)
}

// mapSecretToCiscoDevices fans a Secret event out to every
// CiscoDevice in the same namespace whose
// spec.credentialSecretRef.name matches the Secret's name. Same
// pattern as the IOSXEConfig controller's secretRefs mapping but
// scoped to credential Secrets only.
func (r *CiscoDeviceReconciler) mapSecretToCiscoDevices(ctx context.Context, obj client.Object) []ctrl.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	var devices ciskov1.CiscoDeviceList
	if err := r.List(ctx, &devices, client.InNamespace(secret.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "list CiscoDevices for credential-secret mapping",
			"secret", secret.Name, "namespace", secret.Namespace)
		return nil
	}
	out := make([]ctrl.Request, 0)
	for i := range devices.Items {
		dev := &devices.Items[i]
		if dev.Spec.CredentialSecretRef == nil {
			continue
		}
		if dev.Spec.CredentialSecretRef.Name != secret.Name {
			continue
		}
		out = append(out, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: dev.Namespace,
				Name:      dev.Name,
			},
		})
	}
	return out
}

// lookupCredentialResourceVersion returns the resourceVersion of
// the Secret named by device.Spec.CredentialSecretRef. Used as a
// pod-template annotation value so a Secret rotation rolls the
// Deployment naturally — the annotation changes ⇒ pod template
// changes ⇒ ReplicaSet rolls. Returns "" when no Secret reference
// is set or the Secret cannot be read; in that case the
// annotation is omitted (no rotation signal needed).
//
// Reading the Secret here is safe under the existing chart RBAC:
// the controller already has secrets get/list/watch via the
// chart's controller ClusterRole. We never read the Secret's
// data — only its metadata.resourceVersion — so no credential
// material is touched by the controller itself.
func (r *CiscoDeviceReconciler) lookupCredentialResourceVersion(ctx context.Context, device *ciskov1.CiscoDevice) string {
	if device.Spec.CredentialSecretRef == nil {
		return ""
	}
	var sec corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: device.Namespace,
		Name:      device.Spec.CredentialSecretRef.Name,
	}, &sec); err != nil {
		return ""
	}
	return sec.ResourceVersion
}

// ──────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────

// renderDeviceConfig marshals the DeviceSpec into the YAML format expected
// by the VK binary (wrapped under a "device:" key).
// Credentials are stripped from the output so they never appear in the
// ConfigMap (and therefore etcd). The controller injects them separately
// via environment variables on the VK Deployment.
func renderDeviceConfig(spec *ciskov1.DeviceSpec) (string, error) {
	// The VK config loader expects:
	//   device:
	//     driver: ...
	//     address: ...

	// Copy the spec so we don't mutate the caller's object, then redact
	// fields that must not be persisted in a ConfigMap.
	sanitized := *spec
	sanitized.Password = ""
	sanitized.CredentialSecretRef = nil

	wrapper := struct {
		Device ciskov1.DeviceSpec `json:"device"`
	}{
		Device: sanitized,
	}
	out, err := yaml.Marshal(wrapper)
	if err != nil {
		return "", fmt.Errorf("yaml marshal: %w", err)
	}
	return string(out), nil
}

// vkContainerArgs builds the argument list for the VK container.
// If logLevel is set on the CiscoDevice spec it is forwarded via --log-level.
func vkContainerArgs(deviceName, logLevel string) []string {
	args := []string{
		"run",
		"--config", configMountPath + "/" + configFileName,
		"--nodename", deviceName,
	}
	if logLevel != "" {
		args = append(args, "--log-level", logLevel)
	}
	return args
}

// shortHash returns the first 8 hex chars of an FNV-1a hash of s.
// Used as a cheap change-detector for pod template annotations.
func shortHash(s string) string {
	var h uint32
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

// deleteNode deletes the Kubernetes Node that the VK registered. The node is
// cluster-scoped and cannot be owned by the namespaced CiscoDevice, so it
// must be cleaned up explicitly via this finalizer path.
func (r *CiscoDeviceReconciler) deleteNode(ctx context.Context, name string) error {
	logger := log.FromContext(ctx)
	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: name}, node); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("VK node already absent", "node", name)
			return nil
		}
		return fmt.Errorf("failed to get node %s: %w", name, err)
	}
	if err := r.Delete(ctx, node); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete node %s: %w", name, err)
	}
	logger.Info("Deleted VK node", "node", name)
	return nil
}

// updateStatus patches the CiscoDevice status based on the Deployment state.
// A nil deploy means "no per-device Deployment exists for this device"
// (Wave 1C aggregator-mode path for configdriver-registered drivers);
// status reflects the aggregator-managed phase instead of polling a
// non-existent Deployment.
func (r *CiscoDeviceReconciler) updateStatus(ctx context.Context, device *ciskov1.CiscoDevice, deploy *appsv1.Deployment) error {
	var phase string
	if deploy == nil {
		// Aggregator-managed device: liveness signal is the aggregator
		// worker's continued existence, not a Deployment's
		// ReadyReplicas count. Surface "Ready" since the device is
		// owned by the in-process worker; readiness probes on the
		// aggregator pod cover the worker's health.
		phase = "Ready"
	} else {
		// Re-fetch deployment to get latest status.
		var current appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Name: deploy.Name, Namespace: deploy.Namespace}, &current); err != nil {
			return fmt.Errorf("failed to fetch deployment for status: %w", err)
		}
		phase = "Provisioning"
		if current.Status.ReadyReplicas > 0 {
			phase = "Ready"
		}
	}

	if device.Status.Phase != phase {
		device.Status.Phase = phase
		if err := r.Status().Update(ctx, device); err != nil {
			return fmt.Errorf("failed to update CiscoDevice status: %w", err)
		}
	}
	return nil
}
