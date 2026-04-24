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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
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
}

// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs/status,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

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
			if err := r.deleteNode(ctx, device.Name); err != nil {
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

		deploy.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels: labels,
			Annotations: map[string]string{
				// Force a rollout whenever the ConfigMap content changes.
				"cisco.vk/config-hash": shortHash(configData),
			},
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
	if err := r.reconcileConfigPrereqs(ctx, &device); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile configPrereqs: %w", err)
	}

	// ── 7. Update CiscoDevice status ────────────────────────────────────
	if err := r.updateStatus(ctx, &device, deploy); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
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

// reconcileConfigPrereqs creates, updates, or deletes an owned
// IOSXEConfig CR that mirrors CiscoDevice.spec.configPrereqs. Behaviour:
//
//   - spec.configPrereqs == nil → delete any existing owned CR (covers
//     the "operator removed prereqs" case without a second finalizer).
//   - spec.configPrereqs != nil → upsert an IOSXEConfig with
//     ManagedFamilies filtered to the prereq set and
//     spec.source.inline set to the provided configuration.
//
// The owned CR's owner reference is the CiscoDevice, so deleting the
// device garbage-collects the CR, and the config-driver reverts
// those families on the device per netascode scope semantics.
func (r *CiscoDeviceReconciler) reconcileConfigPrereqs(ctx context.Context, device *ciskov1.CiscoDevice) error {
	name := ownedIOSXEConfigName(device.Name)
	key := types.NamespacedName{Namespace: device.Namespace, Name: name}

	var existing configv1alpha1.IOSXEConfig
	getErr := r.Get(ctx, key, &existing)
	found := getErr == nil
	if getErr != nil && !errors.IsNotFound(getErr) {
		return fmt.Errorf("get owned IOSXEConfig: %w", getErr)
	}

	// Removal path.
	if device.Spec.ConfigPrereqs == nil {
		if !found {
			return nil
		}
		if err := r.Delete(ctx, &existing); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete owned IOSXEConfig: %w", err)
		}
		return nil
	}

	// Upsert path.
	desired := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: device.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.Spec = configv1alpha1.IOSXEConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: device.Name},
			ManagedFamilies: apphostingPrereqFamilies,
			Source: configv1alpha1.ConfigurationSource{
				Inline: &device.Spec.ConfigPrereqs.Configuration,
			},
			// Prereqs default to revert so the device stays in the shape
			// apphosting needs; operators can still opt a separate
			// IOSXEConfig into report/pause for wider declarative config.
			DriftPolicy: configv1alpha1.DriftPolicyRevert,
		}
		return controllerutil.SetControllerReference(device, desired, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("upsert owned IOSXEConfig: %w", err)
	}
	log.FromContext(ctx).Info("configPrereqs reconciled",
		"iosxeconfig", name, "operation", op)
	return nil
}

// SetupWithManager registers the controller with the manager.
func (r *CiscoDeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ciskov1.CiscoDevice{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&configv1alpha1.IOSXEConfig{}).
		Complete(r)
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
func (r *CiscoDeviceReconciler) updateStatus(ctx context.Context, device *ciskov1.CiscoDevice, deploy *appsv1.Deployment) error {
	// Re-fetch deployment to get latest status.
	var current appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Name: deploy.Name, Namespace: deploy.Namespace}, &current); err != nil {
		return fmt.Errorf("failed to fetch deployment for status: %w", err)
	}

	phase := "Provisioning"
	if current.Status.ReadyReplicas > 0 {
		phase = "Ready"
	}

	if device.Status.Phase != phase {
		device.Status.Phase = phase
		if err := r.Status().Update(ctx, device); err != nil {
			return fmt.Errorf("failed to update CiscoDevice status: %w", err)
		}
	}
	return nil
}
