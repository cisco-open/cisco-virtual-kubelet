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
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	vktrace "github.com/virtual-kubelet/virtual-kubelet/trace"
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
	// ForcePrereqsSkipAnnotation lets an operator unblock CiscoDevice deletion
	// when prereq relinquish cannot converge and accepted orphaned config.
	ForcePrereqsSkipAnnotation    = "config.cisco.vk/force-prereqs-skip"
	forceRelinquishSkipAnnotation = "config.cisco.vk/force-relinquish-skip"

	envOTELExporterOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTELExporterOTLPInsecure = "OTEL_EXPORTER_OTLP_INSECURE"
	envOTELExporterOTLPHeaders  = "OTEL_EXPORTER_OTLP_HEADERS"
	envYANGModelsDir            = "YANG_MODELS_DIR"
	envCVKResourceAttributes    = "CVK_RESOURCE_ATTRIBUTES"
	envCVKTelemetryInsecure     = "CISCO_VK_TELEMETRY_INSECURE"
	envCVKTelemetryPort         = "CISCO_VK_TELEMETRY_PORT"
)

// telemetryEnvPropagationNames is the set of env vars whose literal values
// the controller copies into every per-device VK pod's env block.
//
// OTEL_EXPORTER_OTLP_HEADERS is intentionally excluded — those values can
// carry collector auth tokens and copying them as literal `EnvVar.value`
// makes them visible to anyone with `get pod` on the per-device pod's
// namespace. When the chart configures a secret reference (env vars
// CVK_OTLP_HEADERS_SECRET_NAME / CVK_OTLP_HEADERS_SECRET_KEY are set on the
// controller pod), propagatedTelemetryHeadersEnvVar mirrors that secret
// reference into per-device pods. Operators must ensure the named Secret
// exists in each device.Namespace (same pattern as imagePullSecrets).
var telemetryEnvPropagationNames = []string{
	envOTELExporterOTLPEndpoint,
	envOTELExporterOTLPInsecure,
	envYANGModelsDir,
	envCVKResourceAttributes,
	envCVKTelemetryInsecure,
	envCVKTelemetryPort,
}

const (
	envCVKOTLPHeadersSecretName = "CVK_OTLP_HEADERS_SECRET_NAME"
	envCVKOTLPHeadersSecretKey  = "CVK_OTLP_HEADERS_SECRET_KEY"
)

// configPrereqsTeardownPollInterval is how often the deletion-finalizer path
// requeues while waiting for the owned IOSXEConfig to drive empty intent and
// finish its own deletion/finalizer cleanup.
const configPrereqsTeardownPollInterval = 5 * time.Second

// aggregatorTopologyPollInterval is how often aggregator-mode topology shifts
// requeue while old per-device Pods drain after the Deployment delete.
const aggregatorTopologyPollInterval = 4 * time.Second

// aggregatorTopologyShiftTimeout is how long the controller waits for stale
// per-device Pods to vanish before surfacing the topology shift as stuck.
const aggregatorTopologyShiftTimeout = 5 * time.Minute

type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

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
	// flag. When true, the in-process aggregator owns the per-device
	// config-reconcile loop for configdriver-registered platforms, so
	// per-device cisco-vk pods are skipped for those devices. Platforms
	// without a registered configdriver still get apphosting pods, but
	// with the in-pod ConfigReconciler disabled.
	AggregatorEnabled bool
	Recorder          record.EventRecorder
	clock             clock
}

// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs/status,verbs=get
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxetelemetries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxetelemetries/status,verbs=get;list;watch;create;update;patch;delete
// The controller spawns per-device cisco-vk Deployments in the device's
// namespace and references a shared ServiceAccount. The chart only seeds that
// ServiceAccount in the release namespace, so tenant namespaces need their own
// local ServiceAccount and RoleBinding to the chart-supplied ClusterRole.
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch
// Required by the API server's privilege-escalation check when binding the
// chart-supplied ClusterRole into a tenant namespace.
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames=cisco-virtual-kubelet,verbs=bind

// Reconcile ensures a ConfigMap and Deployment exist for each CiscoDevice.
func (r *CiscoDeviceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	ctx, span := vktrace.StartSpan(ctx, "cvk.ciscodevice.reconcile")
	ctx = span.WithField(ctx, "cisco.device.name", req.Name)
	ctx = span.WithField(ctx, "cisco.device.namespace", req.Namespace)
	defer func() {
		span.WithField(ctx, "cvk.reconcile.result", reconcileResultAttribute(result))
		if retErr != nil {
			span.SetStatus(retErr)
		}
		span.End()
	}()

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
	ctx = span.WithField(ctx, "cvk.driver.kind", string(device.Spec.Driver))

	// ── 2. Handle deletion (finalizer) ───────────────────────────────────
	if !device.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&device, ciscoDeviceFinalizer) {
			logger.Info("CiscoDevice deleted – cleaning up VK node", "node", device.Name)
			deviceCopy := device.DeepCopy()
			deviceCopy.Spec.ConfigPrereqs = nil
			done, err := r.reconcileConfigPrereqs(ctx, deviceCopy)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("prereq teardown during deletion: %w", err)
			}
			if !done {
				logger.Info("CiscoDevice deletion: awaiting prereq teardown", "device", device.Name)
				return ctrl.Result{RequeueAfter: configPrereqsTeardownPollInterval}, nil
			}

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

	// Wave 1C: aggregator/per-pod exclusivity. If the manager is running
	// the config aggregator and this driver's configdriver is registered,
	// the aggregator owns config reconciliation for this device. Do not run
	// a per-device cisco-vk pod that could start a second in-pod
	// ConfigReconciler for the same lease scope.
	configDriverRegistered := drivers.ConfigDriverRegistered(device.Spec.Driver)
	if r.AggregatorEnabled && configDriverRegistered {
		stale := &appsv1.Deployment{}
		staleKey := types.NamespacedName{
			Name:      device.Name + deploymentSuffix,
			Namespace: device.Namespace,
		}
		staleControlled := false
		if err := r.Get(ctx, staleKey, stale); err != nil {
			if !errors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("fetch stale per-device Deployment under aggregator mode: %w", err)
			}
		} else {
			staleControlled = metav1.IsControlledBy(stale, &device)
		}

		owned := meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
		owning := meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwning)
		handoverActive := staleControlled || conditionTrue(owning) || !conditionTrue(owned)
		if handoverActive {
			if err := r.markAggregatorHandoverInProgress(ctx, &device); err != nil {
				return ctrl.Result{}, err
			}
		}

		if staleControlled {
			fg := metav1.DeletePropagationForeground
			if err := r.Delete(ctx, stale, &client.DeleteOptions{PropagationPolicy: &fg}); err != nil && !errors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("delete stale per-device Deployment under aggregator mode: %w", err)
			}
		}
		quiesced, pods, err := r.perDevicePodsQuiesced(ctx, &device)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("list stale per-device Pods under aggregator mode: %w", err)
		}
		if !quiesced {
			if !handoverActive {
				if err := r.markAggregatorHandoverInProgress(ctx, &device); err != nil {
					return ctrl.Result{}, err
				}
			}
			if err := r.surfaceAggregatorTopologyStuckIfTimedOut(ctx, &device, pods); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("aggregator owns config reconciliation; waiting for stale per-device Pods to exit",
				"device", device.Name, "driver", device.Spec.Driver)
			return ctrl.Result{RequeueAfter: aggregatorTopologyPollInterval}, nil
		}
		if err := r.clearAggregatorTopologyStuck(ctx, &device); err != nil {
			return ctrl.Result{}, err
		}
		if handoverActive {
			if err := r.setCiscoDeviceCondition(ctx, &device, metav1.Condition{
				Type:               ciskov1.CiscoDeviceConditionAggregatorOwning,
				Status:             metav1.ConditionFalse,
				Reason:             "HandoverComplete",
				ObservedGeneration: device.Generation,
				Message:            "per-device Pods are quiesced; aggregator ownership may proceed",
			}); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.setCiscoDeviceCondition(ctx, &device, metav1.Condition{
				Type:               ciskov1.CiscoDeviceConditionAggregatorOwned,
				Status:             metav1.ConditionTrue,
				Reason:             "AggregatorEnabled",
				ObservedGeneration: device.Generation,
				Message:            "config reconciliation is owned by the manager aggregator",
			}); err != nil {
				return ctrl.Result{}, err
			}
		}
		logger.Info("aggregator owns config reconciliation; skipping per-device Deployment",
			"device", device.Name, "driver", device.Spec.Driver)
		done, err := r.reconcileConfigPrereqs(ctx, &device)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile configPrereqs: %w", err)
		}
		if !done {
			return ctrl.Result{RequeueAfter: configPrereqsTeardownPollInterval}, nil
		}
		if err := r.updateStatus(ctx, &device, nil); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// -- 5b. Ensure VK SA + RoleBinding exist in the device's namespace --
	serviceAccount := r.ServiceAccount
	if serviceAccount == "" {
		serviceAccount = DefaultServiceAccount
	}
	if err := r.ensureVKAccess(ctx, &device, serviceAccount); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure VK access: %w", err)
	}

	// ── 6. Reconcile the Deployment ─────────────────────────────────────
	if err := r.clearAggregatorHandoverConditions(ctx, &device); err != nil {
		return ctrl.Result{}, err
	}

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

	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		// Immutable labels used as selector.
		labels := perDeviceDeploymentLabels(device.Name)

		var replicas int32 = 1
		deploy.Spec.Replicas = &replicas

		deploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: labels,
		}

		annos := map[string]string{
			// Force a rollout whenever the ConfigMap content changes.
			"cisco.vk/config-hash": shortHash(configData),
		}
		if credRV := r.lookupCredentialResourceVersion(ctx, &device); credRV != "" {
			annos["cisco.vk/credential-resource-version"] = credRV
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
		if r.AggregatorEnabled {
			credEnv = append(credEnv, corev1.EnvVar{
				Name:  "DISABLE_IN_POD_CONFIG_RECONCILER",
				Value: "true",
			})
		}
		// Helm injects telemetry env vars into the controller Deployment.
		// The per-device VK pod is the process that owns MDT-over-gNMI
		// subscriptions and OTel exporters, so propagate those controller
		// env values into the pod spec the controller creates.
		podEnv := append([]corev1.EnvVar{}, credEnv...)
		podEnv = append(podEnv, downwardAPIEnv()...)
		podEnv = append(podEnv, propagatedTelemetryEnv()...)
		if hdr := propagatedTelemetryHeadersEnvVar(); hdr != nil {
			podEnv = append(podEnv, *hdr)
		}
		podEnv = append(podEnv, opsPolicyEnv(device.Spec.OpsPolicy)...)

		deploy.Spec.Template.Spec = corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "cisco-vk",
					Image: image,
					Args:  vkContainerArgs(device.Name, device.Spec.LogLevel),
					Env:   podEnv,
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
	done, err := r.reconcileConfigPrereqs(ctx, &device)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile configPrereqs: %w", err)
	}
	if !done {
		return ctrl.Result{RequeueAfter: configPrereqsTeardownPollInterval}, nil
	}

	// ── 7. Update CiscoDevice status ────────────────────────────────────
	if err := r.updateStatus(ctx, &device, deploy); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func reconcileResultAttribute(result ctrl.Result) string {
	if result.RequeueAfter > 0 {
		return "requeue-after:" + result.RequeueAfter.String()
	}
	if result.Requeue {
		return "requeue"
	}
	return "done"
}

// vkSharedClusterRole is the cluster-scoped Role the chart ships with the VK
// pod permissions baked in. The controller binds it into the device namespace
// so per-device cisco-vk pods can run outside the chart release namespace.
const vkSharedClusterRole = "cisco-virtual-kubelet"

// ensureVKAccess provisions the per-namespace bits the chart cannot: a
// ServiceAccount and a RoleBinding to the chart-supplied ClusterRole. Both
// objects are owned by the CiscoDevice so removing the device garbage-collects
// them.
func (r *CiscoDeviceReconciler) ensureVKAccess(ctx context.Context, device *ciskov1.CiscoDevice, saName string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: device.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		if len(sa.OwnerReferences) == 0 {
			return controllerutil.SetControllerReference(device, sa, r.Scheme)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("ServiceAccount %s/%s: %w", sa.Namespace, sa.Name, err)
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: device.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     vkSharedClusterRole,
		}
		rb.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      saName,
			Namespace: device.Namespace,
		}}
		if len(rb.OwnerReferences) == 0 {
			return controllerutil.SetControllerReference(device, rb, r.Scheme)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("RoleBinding %s/%s: %w", rb.Namespace, rb.Name, err)
	}
	return nil
}

// apphostingPrereqFamilies is the closed set of IOSXEConfig families the
// controller owns for CiscoDevice.spec.configPrereqs.
var apphostingPrereqFamilies = []string{
	"interface_virtual_port_group",
	"dhcp",
	"access_list_extended",
}

func ownedIOSXEConfigName(deviceName string) string {
	return deviceName + "-prereqs"
}

func emptyPrereqInline() runtime.RawExtension {
	return runtime.RawExtension{Raw: []byte(`{"interface_virtual_port_group":{},"dhcp":{},"access_list_extended":{}}`)}
}

func perDeviceDeploymentLabels(deviceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "cisco-vk",
		"app.kubernetes.io/instance":   deviceName,
		"app.kubernetes.io/managed-by": "ciscodevice-controller",
	}
}

// downwardAPIEnv returns the per-pod identity env vars (POD_NAME,
// POD_NAMESPACE, POD_UID, NODE_NAME) the per-device VK process needs to
// emit OTel SemConv resource attributes (k8s.pod.*, k8s.node.*,
// service.instance.id). These attributes let multi-replica deployments
// disambiguate metric series downstream.
func downwardAPIEnv() []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
		{Name: "POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}},
		{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}},
	}
}

func propagatedTelemetryEnv() []corev1.EnvVar {
	env := make([]corev1.EnvVar, 0, len(telemetryEnvPropagationNames))
	for _, name := range telemetryEnvPropagationNames {
		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		env = append(env, corev1.EnvVar{Name: name, Value: value})
	}
	return env
}

// propagatedTelemetryHeadersEnvVar mirrors the controller's
// OTEL_EXPORTER_OTLP_HEADERS configuration onto per-device pods as a
// SecretKeyRef-backed env var when the chart has wired one. Returning nil
// means "no headers propagation configured" — operators who need OTLP auth
// must either set telemetry.otlp.headersSecret in the chart or inject the
// env var manually on the per-device pod via custom workload tooling.
//
// Why not propagate the literal value: OTEL_EXPORTER_OTLP_HEADERS commonly
// carries collector auth tokens; copying it as a literal `EnvVar.value` puts
// those tokens into per-device pod specs that any holder of `get pod` on
// the device's namespace can read.
func propagatedTelemetryHeadersEnvVar() *corev1.EnvVar {
	name := strings.TrimSpace(os.Getenv(envCVKOTLPHeadersSecretName))
	if name == "" {
		return nil
	}
	key := strings.TrimSpace(os.Getenv(envCVKOTLPHeadersSecretKey))
	if key == "" {
		key = envOTELExporterOTLPHeaders
	}
	return &corev1.EnvVar{
		Name: envOTELExporterOTLPHeaders,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: name},
				Key:                  key,
			},
		},
	}
}

// opsPolicyEnv translates DeviceSpec.OpsPolicy into env vars on the per-device
// VK pod. Centralising the translation here keeps the CRD the authoritative
// source: imperative `kubectl set env` edits get reverted by the controller's
// next reconcile, while flipping spec.opsPolicy persists.
func opsPolicyEnv(policy *ciskov1.OpsPolicy) []corev1.EnvVar {
	if policy == nil {
		return nil
	}
	var env []corev1.EnvVar
	if names := dedupeNonEmpty(policy.ConfigDiffAllowedNamespaces); len(names) > 0 {
		env = append(env, corev1.EnvVar{
			Name:  "CVK_OPS_CONFIGDIFF_ALLOWED_NAMESPACES",
			Value: strings.Join(names, ","),
		})
	}
	return env
}

func dedupeNonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func matchesPerDeviceLabels(labels map[string]string, deviceName string) bool {
	for key, value := range perDeviceDeploymentLabels(deviceName) {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func isPrereqTearingDown(cr *configv1alpha1.IOSXEConfig) bool {
	return cr.Spec.PruneOnRelinquish &&
		cr.Spec.Source.Inline != nil &&
		string(cr.Spec.Source.Inline.Raw) == string(emptyPrereqInline().Raw)
}

// reconcileConfigPrereqs creates, updates, or tears down the IOSXEConfig CR
// owned by CiscoDevice.spec.configPrereqs. Teardown delegates cleanup to the
// IOSXEConfig controller's own pruneOnRelinquish finalizer: mark the owned CR
// for relinquish, delete it with foreground propagation, observe it enter
// deletion, then wait for it to vanish.
func (r *CiscoDeviceReconciler) reconcileConfigPrereqs(ctx context.Context, device *ciskov1.CiscoDevice) (bool, error) {
	name := ownedIOSXEConfigName(device.Name)
	key := types.NamespacedName{Namespace: device.Namespace, Name: name}

	var existing configv1alpha1.IOSXEConfig
	getErr := r.Get(ctx, key, &existing)
	found := getErr == nil
	if getErr != nil && !errors.IsNotFound(getErr) {
		return false, fmt.Errorf("get owned IOSXEConfig: %w", getErr)
	}

	if device.Spec.ConfigPrereqs == nil {
		if device.Annotations[ForcePrereqsSkipAnnotation] == "true" {
			if found {
				if err := r.forceSkipOwnedPrereqs(ctx, &existing); err != nil {
					return false, err
				}
			}
			r.emitPrereqsSkipped(device, prereqOrphanFamilies(&existing, found))
			return true, nil
		}
		if !found {
			if prereqTeardownObserved(device) {
				return true, nil
			}
			if prereqTeardownStarted(device) {
				if r.Recorder != nil {
					r.Recorder.Eventf(device, corev1.EventTypeWarning, "PrereqTeardownDeletedExternally",
						"owned IOSXEConfig %s/%s disappeared before deletion was observed; recreating empty intent to drive pruneOnRelinquish cleanup",
						device.Namespace, name)
				}
				if err := r.recreatePrereqTeardownIOSXEConfig(ctx, device); err != nil {
					return false, err
				}
				return false, nil
			}
			return true, nil
		}
		if !existing.DeletionTimestamp.IsZero() {
			if err := r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
				Type:               ciskov1.CiscoDeviceConditionPrereqTeardownObserved,
				Status:             metav1.ConditionTrue,
				Reason:             "IOSXEConfigDeleting",
				ObservedGeneration: device.Generation,
				Message:            "owned prereq IOSXEConfig deletion has been observed",
			}); err != nil {
				return false, err
			}
			return false, nil
		}
		updated, err := r.patchOwnedPrereqsForTeardown(ctx, &existing, false)
		if err != nil {
			return false, err
		}
		fg := metav1.DeletePropagationForeground
		if err := r.Delete(ctx, updated, &client.DeleteOptions{PropagationPolicy: &fg}); err != nil && !errors.IsNotFound(err) {
			return false, fmt.Errorf("delete owned IOSXEConfig for prereq teardown: %w", err)
		}
		log.FromContext(ctx).Info("configPrereqs teardown: delete requested", "iosxeconfig", name)
		return false, nil
	}

	desired := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: device.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.Spec = configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device.Name},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: append([]string(nil), apphostingPrereqFamilies...),
				Source: configv1alpha1.ConfigurationSource{
					Inline: &device.Spec.ConfigPrereqs.Configuration,
				},
				DriftPolicy:       configv1alpha1.DriftPolicyRevert,
				PruneOnRelinquish: false,
			},
		}
		return controllerutil.SetControllerReference(device, desired, r.Scheme)
	})
	if err != nil {
		return false, fmt.Errorf("upsert owned IOSXEConfig: %w", err)
	}
	if err := r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionPrereqTeardownObserved,
		Status:             metav1.ConditionFalse,
		Reason:             "PrereqsActive",
		ObservedGeneration: device.Generation,
		Message:            "owned prereq IOSXEConfig is active",
	}); err != nil {
		return false, err
	}
	log.FromContext(ctx).Info("configPrereqs reconciled", "iosxeconfig", name, "operation", op)
	return true, nil
}

// SetupWithManager registers the controller with the manager.
func (r *CiscoDeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ciskov1.CiscoDevice{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&configv1alpha1.IOSXEConfig{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToCiscoDevices)).
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
	sanitized.ConfigPrereqs = nil

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

func (r *CiscoDeviceReconciler) perDevicePodsQuiesced(ctx context.Context, device *ciskov1.CiscoDevice) (bool, []corev1.Pod, error) {
	deployKey := types.NamespacedName{
		Namespace: device.Namespace,
		Name:      device.Name + deploymentSuffix,
	}
	var deploy appsv1.Deployment
	err := r.Get(ctx, deployKey, &deploy)
	deploymentGone := errors.IsNotFound(err)
	if err != nil && !deploymentGone {
		return false, nil, fmt.Errorf("inspect per-device Deployment for quiescence: %w", err)
	}

	staleAncestorUIDs := map[types.UID]struct{}{}
	if !deploymentGone && deploy.UID != "" {
		staleAncestorUIDs[deploy.UID] = struct{}{}
	}

	var replicasets appsv1.ReplicaSetList
	if err := r.List(ctx, &replicasets, client.InNamespace(device.Namespace)); err != nil {
		return false, nil, fmt.Errorf("list ReplicaSets for quiescence: %w", err)
	}
	for {
		added := false
		for i := range replicasets.Items {
			rs := &replicasets.Items[i]
			if _, found := staleAncestorUIDs[rs.UID]; found {
				continue
			}
			if ownedByPerDeviceDeployment(rs.OwnerReferences, deployKey.Name, deploy.UID, deploymentGone, staleAncestorUIDs) && rs.UID != "" {
				staleAncestorUIDs[rs.UID] = struct{}{}
				added = true
			}
		}
		if !added {
			break
		}
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return false, nil, fmt.Errorf("list Pods for quiescence: %w", err)
	}
	stalePods := make([]corev1.Pod, 0)
	for i := range pods.Items {
		pod := &pods.Items[i]
		if matchesPerDeviceLabels(pod.Labels, device.Name) ||
			ownedByPerDeviceDeployment(pod.OwnerReferences, deployKey.Name, deploy.UID, deploymentGone, staleAncestorUIDs) {
			stalePods = append(stalePods, *pod)
		}
	}
	if !deploymentGone {
		return false, stalePods, nil
	}
	return len(stalePods) == 0, stalePods, nil
}

func ownedByPerDeviceDeployment(
	owners []metav1.OwnerReference,
	deploymentName string,
	deploymentUID types.UID,
	deploymentGone bool,
	staleAncestorUIDs map[types.UID]struct{},
) bool {
	for _, owner := range owners {
		if owner.UID != "" {
			if _, found := staleAncestorUIDs[owner.UID]; found {
				return true
			}
		}
		if owner.APIVersion != appsv1.SchemeGroupVersion.String() ||
			owner.Kind != "Deployment" ||
			owner.Name != deploymentName {
			continue
		}
		if deploymentGone || deploymentUID == "" || owner.UID == deploymentUID {
			return true
		}
	}
	return false
}

func conditionTrue(cond *metav1.Condition) bool {
	return cond != nil && cond.Status == metav1.ConditionTrue
}

func (r *CiscoDeviceReconciler) markAggregatorHandoverInProgress(ctx context.Context, device *ciskov1.CiscoDevice) error {
	if err := r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionAggregatorOwning,
		Status:             metav1.ConditionTrue,
		Reason:             "HandoverInProgress",
		ObservedGeneration: device.Generation,
		Message:            "config reconciliation is transferring to the manager aggregator; waiting for per-device Pods to quiesce",
	}); err != nil {
		return err
	}
	if err := r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionAggregatorOwned,
		Status:             metav1.ConditionFalse,
		Reason:             "HandoverInProgress",
		ObservedGeneration: device.Generation,
		Message:            "aggregator ownership is pending until per-device Pods quiesce",
	}); err != nil {
		return err
	}
	return nil
}

func (r *CiscoDeviceReconciler) clearAggregatorHandoverConditions(ctx context.Context, device *ciskov1.CiscoDevice) error {
	if err := r.setCiscoDeviceConditionIfPresent(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionAggregatorOwned,
		Status:             metav1.ConditionFalse,
		Reason:             "PerDeviceTopology",
		ObservedGeneration: device.Generation,
		Message:            "config reconciliation is owned by the per-device Deployment",
	}); err != nil {
		return err
	}
	if err := r.setCiscoDeviceConditionIfPresent(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionAggregatorOwning,
		Status:             metav1.ConditionFalse,
		Reason:             "PerDeviceTopology",
		ObservedGeneration: device.Generation,
		Message:            "aggregator handover is inactive while per-device topology is enabled",
	}); err != nil {
		return err
	}
	if err := r.setCiscoDeviceConditionIfPresent(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionAggregatorTopologyStuck,
		Status:             metav1.ConditionFalse,
		Reason:             "PerDeviceTopology",
		ObservedGeneration: device.Generation,
		Message:            "aggregator topology shift is inactive while per-device topology is enabled",
	}); err != nil {
		return err
	}
	return nil
}

func (r *CiscoDeviceReconciler) surfaceAggregatorTopologyStuckIfTimedOut(ctx context.Context, device *ciskov1.CiscoDevice, pods []corev1.Pod) error {
	owning := meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwning)
	if !conditionTrue(owning) || owning.LastTransitionTime.IsZero() {
		return nil
	}
	if r.now().Sub(owning.LastTransitionTime.Time) < aggregatorTopologyShiftTimeout {
		return nil
	}

	message := describePerDevicePods(pods)
	existing := meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorTopologyStuck)
	emitEvent := existing == nil ||
		existing.Status != metav1.ConditionTrue ||
		existing.Reason != "PodQuiesceTimeout" ||
		existing.Message != message

	if err := r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionAggregatorTopologyStuck,
		Status:             metav1.ConditionTrue,
		Reason:             "PodQuiesceTimeout",
		ObservedGeneration: device.Generation,
		Message:            message,
	}); err != nil {
		return err
	}
	if emitEvent && r.Recorder != nil {
		r.Recorder.Eventf(device, corev1.EventTypeWarning, "AggregatorTopologyShiftStuck", "%s", message)
	}
	return nil
}

func (r *CiscoDeviceReconciler) clearAggregatorTopologyStuck(ctx context.Context, device *ciskov1.CiscoDevice) error {
	if meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorTopologyStuck) == nil {
		return nil
	}
	return r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionAggregatorTopologyStuck,
		Status:             metav1.ConditionFalse,
		Reason:             "Resolved",
		ObservedGeneration: device.Generation,
		Message:            "per-device Pods have quiesced",
	})
}

func describePerDevicePods(pods []corev1.Pod) string {
	if len(pods) == 0 {
		return fmt.Sprintf("per-device Deployment still present after %s; no stale Pods currently observed",
			aggregatorTopologyShiftTimeout)
	}
	descriptions := make([]string, 0, len(pods))
	for _, pod := range pods {
		finalizers := append([]string(nil), pod.Finalizers...)
		sort.Strings(finalizers)
		finalizerList := strings.Join(finalizers, ",")
		if finalizerList == "" {
			finalizerList = "<none>"
		}
		phase := string(pod.Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		descriptions = append(descriptions, fmt.Sprintf("%s/%s phase=%s finalizers=[%s]",
			pod.Namespace, pod.Name, phase, finalizerList))
	}
	sort.Strings(descriptions)
	return fmt.Sprintf("per-device Pods still present after %s: %s",
		aggregatorTopologyShiftTimeout, strings.Join(descriptions, "; "))
}

func (r *CiscoDeviceReconciler) setCiscoDeviceConditionIfPresent(ctx context.Context, device *ciskov1.CiscoDevice, cond metav1.Condition) error {
	if meta.FindStatusCondition(device.Status.Conditions, cond.Type) == nil {
		return nil
	}
	return r.setCiscoDeviceCondition(ctx, device, cond)
}

func (r *CiscoDeviceReconciler) now() time.Time {
	if r.clock != nil {
		return r.clock.Now()
	}
	return realClock{}.Now()
}

func (r *CiscoDeviceReconciler) setCiscoDeviceCondition(ctx context.Context, device *ciskov1.CiscoDevice, cond metav1.Condition) error {
	existing := meta.FindStatusCondition(device.Status.Conditions, cond.Type)
	if existing != nil &&
		existing.Status == cond.Status &&
		existing.Reason == cond.Reason &&
		existing.Message == cond.Message &&
		existing.ObservedGeneration == cond.ObservedGeneration {
		return nil
	}
	if cond.LastTransitionTime.IsZero() {
		cond.LastTransitionTime = metav1.NewTime(r.now())
	}
	meta.SetStatusCondition(&device.Status.Conditions, cond)
	if err := r.Status().Update(ctx, device); err != nil {
		return fmt.Errorf("failed to update CiscoDevice condition %s: %w", cond.Type, err)
	}
	return nil
}

func prereqTeardownObserved(device *ciskov1.CiscoDevice) bool {
	cond := meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionPrereqTeardownObserved)
	return cond != nil && cond.Status == metav1.ConditionTrue
}

func prereqTeardownStarted(device *ciskov1.CiscoDevice) bool {
	return meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionPrereqTeardownObserved) != nil
}

func (r *CiscoDeviceReconciler) patchOwnedPrereqsForTeardown(
	ctx context.Context,
	existing *configv1alpha1.IOSXEConfig,
	forceRelinquishSkip bool,
) (*configv1alpha1.IOSXEConfig, error) {
	updated := existing.DeepCopy()
	changed := false
	if !updated.Spec.PruneOnRelinquish {
		updated.Spec.PruneOnRelinquish = true
		changed = true
	}
	if forceRelinquishSkip {
		if updated.Annotations == nil {
			updated.Annotations = map[string]string{}
		}
		if updated.Annotations[forceRelinquishSkipAnnotation] != "true" {
			updated.Annotations[forceRelinquishSkipAnnotation] = "true"
			changed = true
		}
	}
	if !changed {
		return updated, nil
	}
	if err := r.Patch(ctx, updated, client.MergeFrom(existing.DeepCopy())); err != nil {
		return nil, fmt.Errorf("patch owned IOSXEConfig for prereq teardown: %w", err)
	}
	return updated, nil
}

func (r *CiscoDeviceReconciler) forceSkipOwnedPrereqs(ctx context.Context, existing *configv1alpha1.IOSXEConfig) error {
	updated, err := r.patchOwnedPrereqsForTeardown(ctx, existing, true)
	if err != nil {
		return err
	}
	if !updated.DeletionTimestamp.IsZero() {
		return nil
	}
	fg := metav1.DeletePropagationForeground
	if err := r.Delete(ctx, updated, &client.DeleteOptions{PropagationPolicy: &fg}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete force-skipped owned IOSXEConfig: %w", err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) recreatePrereqTeardownIOSXEConfig(ctx context.Context, device *ciskov1.CiscoDevice) error {
	name := ownedIOSXEConfigName(device.Name)
	emptyInline := emptyPrereqInline()
	desired := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: device.Namespace},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device.Name},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies:   append([]string(nil), apphostingPrereqFamilies...),
				Source:            configv1alpha1.ConfigurationSource{Inline: &emptyInline},
				DriftPolicy:       configv1alpha1.DriftPolicyRevert,
				PruneOnRelinquish: true,
			},
		},
	}
	if err := controllerutil.SetControllerReference(device, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on recreated prereq IOSXEConfig: %w", err)
	}
	if err := r.Create(ctx, desired); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("recreate prereq IOSXEConfig teardown driver: %w", err)
	}
	return nil
}

func prereqOrphanFamilies(cr *configv1alpha1.IOSXEConfig, found bool) []string {
	if !found {
		return append([]string(nil), apphostingPrereqFamilies...)
	}
	if len(cr.Status.AtomicReplaceOwnedKeys) > 0 {
		out := make([]string, 0, len(cr.Status.AtomicReplaceOwnedKeys))
		for family, keys := range cr.Status.AtomicReplaceOwnedKeys {
			if len(keys) > 0 {
				out = append(out, family)
			}
		}
		if len(out) > 0 {
			sort.Strings(out)
			return out
		}
	}
	if len(cr.Spec.ManagedFamilies) > 0 {
		out := append([]string(nil), cr.Spec.ManagedFamilies...)
		sort.Strings(out)
		return out
	}
	return append([]string(nil), apphostingPrereqFamilies...)
}

func (r *CiscoDeviceReconciler) emitPrereqsSkipped(device *ciskov1.CiscoDevice, families []string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(device, corev1.EventTypeWarning, "PrereqsSkipped",
		"force-prereqs-skip annotation set; orphaning prereq families [%s] on device %q",
		strings.Join(families, ", "), device.Name)
}

// mapSecretToCiscoDevices fans a Secret event out to CiscoDevices in the same
// namespace that reference it through spec.credentialSecretRef.
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
	requests := make([]ctrl.Request, 0, len(devices.Items))
	for i := range devices.Items {
		dev := &devices.Items[i]
		if dev.Spec.CredentialSecretRef == nil || dev.Spec.CredentialSecretRef.Name != secret.Name {
			continue
		}
		requests = append(requests, ctrl.Request{NamespacedName: types.NamespacedName{
			Namespace: dev.Namespace,
			Name:      dev.Name,
		}})
	}
	return requests
}

// lookupCredentialResourceVersion returns the referenced Secret's
// resourceVersion for use as a pod-template rollout annotation. It never reads
// Secret data.
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

// updateStatus patches the CiscoDevice status based on the Deployment state.
func (r *CiscoDeviceReconciler) updateStatus(ctx context.Context, device *ciskov1.CiscoDevice, deploy *appsv1.Deployment) error {
	var phase string
	if deploy == nil {
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
