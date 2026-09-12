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
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configengine "github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	iosxegnoi "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/platforms"
	configprovider "github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
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
	// gNOI certificate provisioning material is projected from a dedicated
	// per-device Secret only when the CiscoDevice explicitly opts in.
	gnoiProvisioningVolumeName = "gnoi-provisioning"
	gnoiProvisioningMountPath  = "/var/run/secrets/cisco-vk/gnoi-provisioning"
	// The worker only needs a non-empty reference to validate that provisioning
	// is configured; it reads material from the fixed mount above. Do not copy
	// the source Secret name into the worker ConfigMap.
	gnoiProvisioningWorkerSecretRefName = "projected"
	// Generic gNOI TLS trust is projected separately from IOS-XE certificate
	// provisioning. The worker config contains only these fixed internal paths;
	// the user-supplied Secret name and all Secret bytes stay out of ConfigMaps.
	gnoiTLSVolumeName = "gnoi-tls"
	gnoiTLSMountPath  = "/var/run/secrets/cisco-vk/gnoi-tls"
	// gnoiSignerMountedAnnotation records that the current signer lifecycle has
	// not yet completed a private-key-free rollout. It is cleared only after the
	// Deployment controller reports that the cleanup template is fully available.
	gnoiSignerMountedAnnotation = "cisco.vk/gnoi-provisioning-signer-mounted"
	// Retain non-overlapping rollouts when disabling mutation gates until every
	// old enabled worker has stopped, including workers without a mounted signer.
	gnoiMutationWorkerAnnotation = "cisco.vk/gnoi-mutation-worker"
	// DefaultImage is the default container image for the VK deployment.
	DefaultImage = "ghcr.io/cisco/virtual-kubelet-cisco:latest"
	// DefaultServiceAccount is the shared service account used by all VK deployments.
	DefaultServiceAccount = "cisco-virtual-kubelet"
	// distrolessNonRootUID/GID are the stable numeric identity of the nonroot
	// account in gcr.io/distroless images. Using numbers is required because
	// kubelet cannot verify a named OCI image user against runAsNonRoot.
	distrolessNonRootUID int64 = 65532
	distrolessNonRootGID int64 = 65532
	// virtualKubeletNodeLabelKey/Value are applied to nodes registered by the
	// per-device VK process. The controller-created per-device VK pods must
	// avoid those nodes or Kubernetes can recursively schedule one device's VK
	// process as an app-hosted workload on another device.
	virtualKubeletNodeLabelKey   = "type"
	virtualKubeletNodeLabelValue = "virtual-kubelet"
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
	envCVKGNOIInsecure          = "CISCO_VK_GNOI_INSECURE"
	envCVKGNOIPort              = "CISCO_VK_GNOI_PORT"
	envCVKGNOIDisabled          = "CISCO_VK_GNOI_DISABLED"
	envCVKEnableWriteClassGNOI  = "CISCO_VK_ENABLE_WRITE_CLASS_GNOI"
	envCVKEnableSoftwareUpgrade = "CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE"
	envCVKUpgradeMaxImageBytes  = "CISCO_VK_UPGRADE_MAX_IMAGE_BYTES"
	envConfigYANGValidation     = "CONFIG_YANG_VALIDATION"
	envCVKNXOSAllowExperimental = "CVK_NXOS_ALLOW_EXPERIMENTAL_RELEASES"
)

// telemetryEnvPropagationNames is the legacy name for the set of controller
// env vars whose literal values are copied into every per-device VK pod's env
// block. Most entries are telemetry-related; CONFIG_YANG_VALIDATION is also
// propagated so the NetAsCode -> YANG validation policy is identical in
// controller/aggregator mode and per-device-pod mode.
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
	envCVKGNOIInsecure,
	envCVKGNOIPort,
	envCVKGNOIDisabled,
	envCVKEnableWriteClassGNOI,
	envCVKEnableSoftwareUpgrade,
	envCVKUpgradeMaxImageBytes,
	envConfigYANGValidation,
	envCVKNXOSAllowExperimental,
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
	Scheme    *runtime.Scheme
	APIReader client.Reader
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
	// ManagedTopology enables the opt-in manager-owned Node identity,
	// projection, and per-device worker authorization contract.
	ManagedTopology         bool
	TopologyPolicyNamespace string
	TopologyPolicyName      string
	LeaseNamespace          string
	Recorder                record.EventRecorder
	clock                   clock
}

// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs/status,verbs=get
// +kubebuilder:rbac:groups=config.cisco.vk,resources=nxosconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=nxosconfigs/status,verbs=get
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxetelemetries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxetelemetries/status,verbs=get;list;watch;create;update;patch;delete
// The controller spawns per-device cisco-vk Deployments in the device's
// namespace and references a shared ServiceAccount. The chart only seeds that
// ServiceAccount in the release namespace, so tenant namespaces need their own
// local ServiceAccount plus bindings to the chart-supplied ClusterRole.
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// The controller manages its own per-device ClusterRoleBindings named by
// vkAccessClusterRoleBindingName; resourceNames pinning is infeasible because
// those names are derived dynamically from each device namespace and SA. The
// manager's cached client reads ClusterRoleBindings (CreateOrUpdate Get), which
// starts a cluster-wide informer, so list+watch are required; only patch is
// unused (CreateOrUpdate uses Update, not Patch).
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;delete
// Required by the API server's privilege-escalation check when binding the
// chart-supplied ClusterRole into a tenant namespace.
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames=cisco-virtual-kubelet;cisco-virtual-kubelet-device,verbs=bind

// Reconcile ensures a ConfigMap and Deployment exist for each CiscoDevice.
func (r *CiscoDeviceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	// ── 1. Fetch the CiscoDevice ────────────────────────────────────────
	var device ciskov1.CiscoDevice
	if err := r.Get(ctx, req.NamespacedName, &device); err != nil {
		if errors.IsNotFound(err) {
			log.FromContext(ctx).Info("CiscoDevice not found – already deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch CiscoDevice: %w", err)
	}

	// The object must be fetched before the span starts so its bounded CI
	// carrier can become the parent (or, after the window, a causal link).
	ctx, _ = correlation.ApplyAnnotations(ctx, device.Annotations, r.now())
	ctx, span := correlation.Start(ctx,
		otel.Tracer("cisco-virtual-kubelet/ciscodevice-controller"),
		"cvk.ciscodevice.reconcile",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(
			attribute.String("cisco.device.name", req.Name),
			attribute.String("cisco.device.namespace", req.Namespace),
			attribute.String("cvk.driver.kind", string(device.Spec.Driver)),
		),
	)
	defer func() {
		span.SetAttributes(attribute.String("cvk.reconcile.result", reconcileResultAttribute(result)))
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, "reconcile")
		}
		span.End()
	}()

	logger := log.FromContext(ctx)

	// ── 2. Handle deletion (finalizer) ───────────────────────────────────
	if !device.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&device, ciscoDeviceFinalizer) {
			if err := r.ensureManagedDeviceDeletionSafe(ctx, &device); err != nil {
				return ctrl.Result{RequeueAfter: topologyRequeueInterval}, err
			}
			logger.Info("CiscoDevice deleted – cleaning up VK node", "node", resolvedNodeName(&device))
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

			if device.Status.NodeIdentity != nil {
				if r.ManagedTopology {
					retiredPrior, err := r.retirePriorTopologyWorkerAccessIfSafe(
						ctx, &device, managedWorkerServiceAccountName(&device),
					)
					if err != nil {
						return ctrl.Result{}, err
					}
					if !retiredPrior {
						return ctrl.Result{RequeueAfter: topologyRequeueInterval}, nil
					}
				}
				// Revoke API authority before removing any pre-created identity.
				// DeletionTimestamp makes managed mutation guards reject new device
				// work, while the safety check above proves prior work is settled.
				if err := r.cleanupVKClusterAccess(ctx, &device, r.serviceAccountForDevice(&device)); err != nil {
					return ctrl.Result{}, err
				}
				if err := r.cleanupManagedWorkerLeases(ctx, &device); err != nil {
					return ctrl.Result{}, err
				}
				if err := r.deleteDeviceNode(ctx, &device); err != nil {
					return ctrl.Result{}, err
				}
			} else {
				serviceAccount := r.serviceAccountForDevice(&device)
				isolatedLegacy := device.Status.LegacyHandoff != nil ||
					device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker] == string(device.UID)
				if isolatedLegacy {
					// Cleanup may trust the marker only to choose a deletion target;
					// cleanupGeneratedWorkerAccess independently verifies exact owner,
					// annotations, role, subjects, and UID before deleting anything.
					serviceAccount = topologyLegacyWorkerServiceAccountName(&device)
					// Revoke the isolated worker before deleting its Node. Otherwise a
					// still-running worker can recreate the same Node name in the gap
					// between manager deletion and credential cleanup.
					if err := r.cleanupVKClusterAccess(ctx, &device, serviceAccount); err != nil {
						return ctrl.Result{}, err
					}
				}
				if err := r.deleteDeviceNode(ctx, &device); err != nil {
					return ctrl.Result{}, err
				}
				if !isolatedLegacy {
					if err := r.cleanupVKClusterAccess(ctx, &device, serviceAccount); err != nil {
						return ctrl.Result{}, err
					}
				}
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

	managed, err := r.reconcileManagedTopology(ctx, &device)
	if err != nil {
		topology.RecordProjectionReconcile("error")
		return ctrl.Result{RequeueAfter: topologyRequeueInterval}, err
	}
	if managed.Managed {
		topology.RecordProjectionReconcile("projected")
	} else if r.ManagedTopology {
		topology.RecordProjectionReconcile("skipped")
	}

	// ── 4. Resolve projected gNOI trust and render device config YAML ───
	// Kubernetes-facing gNOI TLS uses a same-namespace Secret reference.
	// Validate its fixed keys before creating a pod, then render only stable
	// internal file paths for the worker. Local-file configuration never
	// passes through this controller path.
	configDriverRegistered := drivers.ConfigDriverRegistered(device.Spec.Driver)
	perDeviceWorkerExpected := !(r.AggregatorEnabled && configDriverRegistered)
	var gnoiTLSState gnoiTLSProjectionState
	var gnoiConfigurationErr error
	if perDeviceWorkerExpected && gnoiTLSSecretRef(&device.Spec) != nil && !gNOIDisabled() {
		var inspectErr error
		gnoiTLSState, inspectErr = r.inspectGNOITLSSecret(ctx, &device)
		if inspectErr != nil {
			var readErr *gnoiSecretReadError
			if stderrors.As(inspectErr, &readErr) {
				return ctrl.Result{}, inspectErr
			}
			if r.Recorder != nil {
				r.Recorder.Eventf(&device, corev1.EventTypeWarning, "GNOITLSInvalid", "%v", inspectErr)
			}
			gnoiConfigurationErr = inspectErr
		}
	}
	configData, err := renderDeviceConfigForWorker(&device.Spec, gnoiTLSState.clientCertificate)
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
		cm.Annotations = propagatedCorrelationAnnotations(cm.Annotations, device.Annotations, r.now())
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
	serviceAccount := r.serviceAccountForDevice(&device, managed)
	managedWorker := managed.Managed && !managed.LegacyWorker
	if err := r.ensureVKAccess(ctx, &device, serviceAccount, managedWorker, managed.LegacyWorker); err != nil {
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
	worker, err := resolveDeviceWorkerConfig(device.Spec.Worker)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("worker configuration: %w", err)
	}
	provisioning := xeGNOICertificateProvisioning(&device.Spec)
	provisioningTrustEnabled := provisioning != nil && !gNOIDisabled()
	provisioningSecretRV := ""
	provisioningSignerAvailable := false
	if provisioningTrustEnabled {
		var err error
		provisioningSecretRV, provisioningSignerAvailable, err = r.gnoiProvisioningSecretState(
			ctx,
			device.Namespace,
			provisioning.SecretRef.Name,
			provisioning.CertificateID,
			device.Spec.Address,
			writeClassGNOIEnabled(),
		)
		if err != nil {
			var readErr *gnoiSecretReadError
			if stderrors.As(err, &readErr) {
				return ctrl.Result{}, err
			}
			if r.Recorder != nil {
				r.Recorder.Eventf(&device, corev1.EventTypeWarning, "GNOIProvisioningSecretInvalid", "%v", err)
			}
			gnoiConfigurationErr = err
			provisioningTrustEnabled = false
		}
	}
	provisioningWritesEnabled := provisioningTrustEnabled &&
		provisioningSignerAvailable &&
		writeClassGNOIEnabled()
	credentialSecretRV, credentialSecretErr := r.lookupCredentialResourceVersion(ctx, &device)
	if credentialSecretErr != nil && r.ManagedTopology {
		return ctrl.Result{}, credentialSecretErr
	}
	desiredWorkerRevision := ""

	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		previousWorkerRevision := deploy.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision]
		// Immutable labels used as selector.
		labels := perDeviceDeploymentLabels(device.Name)

		var replicas int32 = 1
		deploy.Spec.Replicas = &replicas

		deploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: labels,
		}
		signerLifecyclePending := deploy.Annotations[gnoiSignerMountedAnnotation] == "true"
		templateProjectsSigner := podTemplateProjectsGNOIPrivateKey(&deploy.Spec.Template.Spec)
		cleanupComplete := signerLifecyclePending &&
			!provisioningWritesEnabled &&
			!templateProjectsSigner &&
			deploymentRolloutComplete(deploy)
		signerMayBeResident := provisioningWritesEnabled ||
			templateProjectsSigner ||
			(signerLifecyclePending && !cleanupComplete)
		// Write-class gNOI and software lifecycle use durable, CR-scoped
		// at-most-once markers so a replacement worker can recover an operation.
		// They cannot safely distinguish an overlapping old worker from a crashed
		// one, so prevent Deployment rollouts from running both managers at once.
		gnoiMutationsEnabled := device.Spec.Driver == ciskov1.DeviceDriverXE &&
			!gNOIDisabled() && (writeClassGNOIEnabled() || softwareUpgradeEnabled())
		mutationLifecyclePending := deploy.Annotations[gnoiMutationWorkerAnnotation] == "true"
		templateMutationsEnabled := device.Spec.Driver == ciskov1.DeviceDriverXE &&
			podTemplateEnablesGNOIMutations(&deploy.Spec.Template.Spec)
		mutationCleanupComplete := mutationLifecyclePending && !gnoiMutationsEnabled &&
			!templateMutationsEnabled && deploymentRolloutComplete(deploy)
		mutationWorkerMayBeRunning := gnoiMutationsEnabled || templateMutationsEnabled ||
			(mutationLifecyclePending && !mutationCleanupComplete)
		if r.ManagedTopology || managed.Managed || managed.LegacyWorker || signerMayBeResident || mutationWorkerMayBeRunning {
			deploy.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		} else {
			deploy.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}
		}

		annos := map[string]string{
			// Force a rollout whenever the ConfigMap content changes.
			"cisco.vk/config-hash": shortHash(configData),
		}
		// Recreate the isolated legacy Pod after the manager releases Node
		// ownership. A Pod started while the Node was still managed cannot prove
		// the reverse writer handoff even if it later observes a stale heartbeat.
		if managed.LegacyWorker && device.Status.LegacyHandoff != nil &&
			device.Status.LegacyHandoff.NodeReleasedAt != nil {
			annos[managedprotocol.AnnotationLegacyHandoffRelease] =
				device.Status.LegacyHandoff.NodeReleasedAt.UTC().Format(time.RFC3339Nano)
		}
		if credentialSecretRV != "" {
			annos[managedprotocol.AnnotationCredentialSecretRevision] = credentialSecretRV
		}
		if provisioningTrustEnabled {
			// Copy only resourceVersion to trigger rotation; key material stays in the Secret volume.
			if provisioningSecretRV != "" {
				annos[managedprotocol.AnnotationGNOIProvisioningRevision] = provisioningSecretRV
			}
		}
		if gnoiTLSState.enabled && gnoiTLSState.resourceVersion != "" {
			annos[managedprotocol.AnnotationGNOITLSSecretRevision] = gnoiTLSState.resourceVersion
		}
		// Keep lifecycle carriers on the Deployment object for audit/search.
		// Do not copy them into the PodTemplate: a trace-only annotation change
		// would otherwise roll the long-lived per-device VK even though the
		// process does not consume its own Pod annotations.
		deploy.Annotations = propagatedCorrelationAnnotations(deploy.Annotations, device.Annotations, r.now())
		if signerMayBeResident {
			if deploy.Annotations == nil {
				deploy.Annotations = make(map[string]string)
			}
			deploy.Annotations[gnoiSignerMountedAnnotation] = "true"
		} else if cleanupComplete {
			delete(deploy.Annotations, gnoiSignerMountedAnnotation)
		}
		if mutationWorkerMayBeRunning {
			if deploy.Annotations == nil {
				deploy.Annotations = make(map[string]string)
			}
			deploy.Annotations[gnoiMutationWorkerAnnotation] = "true"
		} else if mutationCleanupComplete {
			delete(deploy.Annotations, gnoiMutationWorkerAnnotation)
		}
		deploy.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels:      labels,
			Annotations: annos,
		}

		// Build credential env vars. When a Secret reference is provided,
		// use valueFrom so the kubelet resolves the password at pod startup.
		// The reconciler separately fetches the typed Secret to observe its
		// resourceVersion; it does not inspect Data, but the API object includes it.
		// Otherwise fall back
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
		if managedWorker {
			podEnv = append(podEnv, managedWorkerIdentityEnv(&device, managed.NodeName)...)
		}
		if r.LeaseNamespace != "" {
			podEnv = append(podEnv, corev1.EnvVar{Name: "CONFIG_LEASE_NAMESPACE", Value: r.LeaseNamespace})
		}
		podEnv = append(podEnv, downwardAPIEnv()...)
		podEnv = append(podEnv, propagatedTelemetryEnv()...)
		if hdr := propagatedTelemetryHeadersEnvVar(); hdr != nil {
			podEnv = append(podEnv, *hdr)
		}
		podEnv = append(podEnv, opsPolicyEnv(device.Spec.OpsPolicy)...)
		if gnoiConfigurationErr != nil {
			// Invalid optional trust must not block credential/config updates or
			// signer revocation. Restart this worker with gNOI disabled and no
			// gNOI Secret mounts until the referenced material is repaired.
			podEnv = slices.DeleteFunc(podEnv, func(env corev1.EnvVar) bool { return env.Name == envCVKGNOIDisabled })
			podEnv = append(podEnv, corev1.EnvVar{Name: envCVKGNOIDisabled, Value: "1"})
		}

		deploy.Spec.Template.Spec = corev1.PodSpec{
			// Pod Security Standards "restricted" profile. Pinning the numeric
			// distroless identity lets kubelet verify runAsNonRoot and keeps the
			// process and writable volume ownership consistent.
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: ptr.To(true),
				RunAsUser:    ptr.To(distrolessNonRootUID),
				RunAsGroup:   ptr.To(distrolessNonRootGID),
				FSGroup:      ptr.To(distrolessNonRootGID),
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []corev1.Container{
				{
					Name:      "cisco-vk",
					Image:     image,
					Args:      vkContainerArgs(managedOrLegacyNodeName(&device, managed), device.Spec.LogLevel),
					Env:       podEnv,
					Resources: workerResourceRequirements(worker.Resources),
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: ptr.To(false),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
						// The two writable paths the runtime needs — the
						// generated-TLS dir and /tmp (upgrade image staging,
						// SFTP known_hosts scratch in imageresolver.go) — are
						// both backed by emptyDir mounts below, so the root
						// filesystem itself can be read-only.
						ReadOnlyRootFilesystem: ptr.To(true),
					},
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
						{
							Name:      "tmp",
							MountPath: "/tmp",
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
				{
					// Writable /tmp for the read-only root filesystem:
					// software-upgrade image staging and cache
					// (os.CreateTemp / os.TempDir in imageresolver.go) land
					// here. An optional size limit bounds its disk consumption.
					Name: "tmp",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: worker.TmpSizeLimit},
					},
				},
			},
			// Use shared service account with VK RBAC permissions
			ServiceAccountName: serviceAccount,
			Affinity:           perDeviceVKNodeAffinity(),
		}
		if provisioningTrustEnabled {
			sources := []corev1.VolumeProjection{{Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{Name: provisioning.SecretRef.Name},
				Items: []corev1.KeyToPath{
					{Key: "tls.crt", Path: "tls.crt"},
					{Key: "ca.crt", Path: "ca.crt"},
				},
			}}}
			if provisioningWritesEnabled {
				sources = append(sources, corev1.VolumeProjection{Secret: &corev1.SecretProjection{
					LocalObjectReference: corev1.LocalObjectReference{Name: provisioning.SecretRef.Name},
					Optional:             ptr.To(true),
					Items: []corev1.KeyToPath{
						{Key: "bootstrap.crt", Path: "bootstrap.crt"},
						{Key: "ca.key", Path: "ca.key"},
					},
				}})
			}
			deploy.Spec.Template.Spec.Containers[0].VolumeMounts = append(
				deploy.Spec.Template.Spec.Containers[0].VolumeMounts,
				corev1.VolumeMount{Name: gnoiProvisioningVolumeName, MountPath: gnoiProvisioningMountPath, ReadOnly: true},
			)
			deploy.Spec.Template.Spec.Volumes = append(deploy.Spec.Template.Spec.Volumes, corev1.Volume{
				Name: gnoiProvisioningVolumeName,
				VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
					DefaultMode: ptr.To[int32](0o440),
					Sources:     sources,
				}},
			})
		}
		if gnoiTLSState.enabled {
			items := []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}
			if gnoiTLSState.clientCertificate {
				items = append(items,
					corev1.KeyToPath{Key: "tls.crt", Path: "tls.crt"},
					corev1.KeyToPath{Key: "tls.key", Path: "tls.key"},
				)
			}
			deploy.Spec.Template.Spec.Containers[0].VolumeMounts = append(
				deploy.Spec.Template.Spec.Containers[0].VolumeMounts,
				corev1.VolumeMount{Name: gnoiTLSVolumeName, MountPath: gnoiTLSMountPath, ReadOnly: true},
			)
			deploy.Spec.Template.Spec.Volumes = append(deploy.Spec.Template.Spec.Volumes, corev1.Volume{
				Name: gnoiTLSVolumeName,
				VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
					DefaultMode: ptr.To[int32](0o440),
					Sources: []corev1.VolumeProjection{{Secret: &corev1.SecretProjection{
						LocalObjectReference: corev1.LocalObjectReference{Name: gnoiTLSState.secretName},
						Items:                items,
					}}},
				}},
			})
		}
		if managedWorker {
			deploy.Spec.Template.Spec.Containers[0].Env = append(
				deploy.Spec.Template.Spec.Containers[0].Env,
				corev1.EnvVar{Name: managedprotocol.EnvCredentialSecretRevision, Value: credentialSecretRV},
				corev1.EnvVar{Name: managedprotocol.EnvGNOITLSSecretRevision, Value: gnoiTLSState.resourceVersion},
				corev1.EnvVar{Name: managedprotocol.EnvGNOIProvisioningRevision, Value: provisioningSecretRV},
			)
			var revisionErr error
			desiredWorkerRevision, revisionErr = managedWorkerPodTemplateRevision(&deploy.Spec.Template)
			if revisionErr != nil {
				return revisionErr
			}
			// Fence the old process in CiscoDevice status before changing an
			// existing Deployment's desired template. Without this two-phase
			// transition, the old Recreate Pod could claim a granted mutation in
			// the interval between the Deployment update and the status refresh.
			if managedWorkerRevisionNeedsPreFence(
				deploy.UID, previousWorkerRevision, desiredWorkerRevision, device.Status.WorkerRevision,
			) {
				return &managedWorkerRevisionFence{desiredRevision: desiredWorkerRevision}
			}
			if deploy.Spec.Template.Annotations == nil {
				deploy.Spec.Template.Annotations = map[string]string{}
			}
			deploy.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision] = desiredWorkerRevision
			deploy.Spec.Template.Spec.Containers[0].Env = append(
				deploy.Spec.Template.Spec.Containers[0].Env,
				corev1.EnvVar{Name: managedprotocol.EnvWorkerRevision, Value: desiredWorkerRevision},
			)
		}

		return controllerutil.SetControllerReference(&device, deploy, r.Scheme)
	})
	if err != nil {
		var fence *managedWorkerRevisionFence
		if stderrors.As(err, &fence) {
			if err := r.fenceManagedWorkerRevision(ctx, &device, fence.desiredRevision); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: topologyRequeueInterval}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to reconcile Deployment: %w", err)
	}
	logger.Info("Deployment reconciled", "name", deploy.Name, "operation", op)
	if err := r.updateGNOIConfigurationCondition(ctx, &device, deploy, desiredWorkerRevision, gnoiConfigurationErr); err != nil {
		return ctrl.Result{}, err
	}

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
	if r.ManagedTopology {
		retiredPrior, err := r.retirePriorTopologyWorkerAccessIfSafe(ctx, &device, serviceAccount)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !retiredPrior {
			return ctrl.Result{RequeueAfter: topologyRequeueInterval}, nil
		}
		retired, err := r.retireSharedWorkerAccessIfSafe(ctx)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("retire legacy shared-worker access: %w", err)
		}
		if !retired {
			return ctrl.Result{RequeueAfter: topologyRequeueInterval}, nil
		}
	}
	if managed.RequeueAfter > 0 {
		return ctrl.Result{RequeueAfter: managed.RequeueAfter}, nil
	}
	if r.ManagedTopology {
		// Managed rollout freshness includes independently revalidated device
		// conditions. Periodic reconciliation refreshes those producer
		// observations without relying on unrelated Node heartbeat events.
		return ctrl.Result{RequeueAfter: managedConditionProbeInterval}, nil
	}
	if gnoiConfigurationErr != nil {
		return ctrl.Result{RequeueAfter: time.Minute}, nil
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
// pod's cluster-wide permissions (node, pod-hosting, leases, CiscoDevice
// watch). The controller binds it cluster-wide for per-device cisco-vk pods.
const vkSharedClusterRole = "cisco-virtual-kubelet"

// vkDeviceClusterRole holds the config-management CRD permissions
// (config.cisco.vk / ops.cisco.vk). It is bound with a namespaced
// RoleBinding ONLY — never cluster-wide — so a per-device pod cannot read or
// write another tenant namespace's config CRs.
const vkDeviceClusterRole = vkSharedClusterRole + "-device"

func (r *CiscoDeviceReconciler) vkServiceAccountName() string {
	if r.ServiceAccount != "" {
		return r.ServiceAccount
	}
	return DefaultServiceAccount
}

func (r *CiscoDeviceReconciler) serviceAccountForDevice(device *ciskov1.CiscoDevice, topologyResult ...managedTopologyResult) string {
	if len(topologyResult) > 0 && topologyResult[0].LegacyWorker {
		return topologyLegacyWorkerServiceAccountName(device)
	}
	// A reverse handoff deliberately retains its completed identity marker so
	// disabling managed topology never falls back to the release-wide shared
	// ServiceAccount on a later manager restart.
	if device.Status.LegacyHandoff != nil {
		return topologyLegacyWorkerServiceAccountName(device)
	}
	// NodeIdentity is a durable writer-handoff marker. Keep using the
	// incarnation-bound identity during deletion or a fail-closed manager
	// restart even when the feature flag was subsequently disabled.
	if device.Status.NodeIdentity != nil {
		return managedWorkerServiceAccountName(device)
	}
	if r.ManagedTopology {
		return topologyLegacyWorkerServiceAccountName(device)
	}
	return r.vkServiceAccountName()
}

func managedOrLegacyNodeName(device *ciskov1.CiscoDevice, managed managedTopologyResult) string {
	if managed.Managed {
		return managed.NodeName
	}
	return resolvedNodeName(device)
}

func vkAccessClusterRoleBindingName(namespace, saName string) string {
	raw := namespace + "-" + saName
	suffix := "-" + shortHash(raw)
	const prefix = "cisco-vk-"
	maxRaw := 253 - len(prefix) - len(suffix)
	if len(raw) > maxRaw {
		raw = strings.TrimRight(raw[:maxRaw], "-")
	}
	return prefix + raw + suffix
}

// ensureVKAccess provisions the access bits the chart cannot: a ServiceAccount
// in the device namespace, a namespaced RoleBinding to the config-management
// role (vkDeviceClusterRole — so config CRD access is scoped to THIS device's
// namespace, never cluster-wide), and a ClusterRoleBinding to the
// cluster-scoped role (vkSharedClusterRole — Nodes, pod hosting, Leases, and
// the CiscoDevice cluster watch).
func (r *CiscoDeviceReconciler) ensureVKAccess(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	saName string,
	managed bool,
	isolatedLegacy ...bool,
) error {
	generatedLegacy := len(isolatedLegacy) > 0 && isolatedLegacy[0]
	generated := managed || r.ManagedTopology || device.Status.LegacyHandoff != nil || generatedLegacy
	if generated {
		expectedName := topologyLegacyWorkerServiceAccountName(device)
		if managed {
			expectedName = managedWorkerServiceAccountName(device)
		}
		if saName != expectedName {
			return fmt.Errorf("generated worker ServiceAccount name %q does not match bound identity %q", saName, expectedName)
		}
	}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: device.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		if generated {
			expectedAnnotations := workerServiceAccountAnnotations(device, managed)
			if serviceAccountHasIdentity(sa) {
				if !managedServiceAccountOwnedByDevice(sa, device) {
					return fmt.Errorf("existing generated worker ServiceAccount is not controlled by this CiscoDevice incarnation")
				}
				if err := validateReservedWorkerAnnotations(sa.Annotations, expectedAnnotations); err != nil {
					return fmt.Errorf("existing generated worker ServiceAccount binding is invalid: %w", err)
				}
			}
			if sa.Annotations == nil {
				sa.Annotations = map[string]string{}
			}
			for key, value := range expectedAnnotations {
				sa.Annotations[key] = value
			}
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
	// roleRef is immutable. An older release bound this name to the
	// (cluster-wide) cisco-virtual-kubelet role; the RBAC split rebinds it to
	// the namespaced cisco-virtual-kubelet-device role. CreateOrUpdate cannot
	// change roleRef in place, so delete the stale binding first and let it be
	// recreated with the new ref.
	existingRB := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(rb), existingRB); err == nil {
		if existingRB.RoleRef.Name != vkDeviceClusterRole {
			if generated {
				return fmt.Errorf("generated worker RoleBinding %s/%s has unexpected role %q", rb.Namespace, rb.Name, existingRB.RoleRef.Name)
			}
			if err := r.Delete(ctx, existingRB); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("delete stale RoleBinding %s/%s: %w", rb.Namespace, rb.Name, err)
			}
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("get RoleBinding %s/%s: %w", rb.Namespace, rb.Name, err)
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if generated && objectMetaHasIdentity(&rb.ObjectMeta) {
			if err := validateGeneratedRoleBinding(rb, device, saName); err != nil {
				return err
			}
		}
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     vkDeviceClusterRole,
		}
		rb.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      saName,
			Namespace: device.Namespace,
		}}
		if generated {
			if err := applyGeneratedWorkerBindingMetadata(rb, device, managed, r.Scheme); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("RoleBinding %s/%s: %w", rb.Namespace, rb.Name, err)
	}

	workerClusterRole := vkSharedClusterRole
	if managed {
		workerClusterRole = managedprotocol.ManagedWorkerClusterRole
	}
	crbName := vkAccessClusterRoleBindingName(device.Namespace, saName)
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: crbName},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, crb, func() error {
		if generated && objectMetaHasIdentity(&crb.ObjectMeta) {
			if err := validateGeneratedClusterRoleBinding(crb, device, saName, workerClusterRole); err != nil {
				return err
			}
		}
		crb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     workerClusterRole,
		}
		crb.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      saName,
			Namespace: device.Namespace,
		}}
		if generated {
			applyWorkerBindingAnnotations(&crb.ObjectMeta, device, managed)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("ClusterRoleBinding %s: %w", crbName, err)
	}
	if generated {
		if err := r.auditGeneratedWorkerBindings(ctx, device, saName, workerClusterRole); err != nil {
			return err
		}
	}
	return nil
}

func workerServiceAccountAnnotations(device *ciskov1.CiscoDevice, managed bool) map[string]string {
	annotations := map[string]string{
		managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName:      device.Name,
		managedprotocol.AnnotationDeviceUID:       string(device.UID),
		managedprotocol.AnnotationNodeName:        resolvedNodeName(device),
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
	}
	if managed {
		annotations[managedprotocol.AnnotationManaged] = "true"
	} else {
		annotations[managedprotocol.AnnotationWorkerMode] = managedprotocol.WorkerModeLegacy
	}
	return annotations
}

var reservedWorkerAnnotationKeys = []string{
	managedprotocol.AnnotationManaged,
	managedprotocol.AnnotationDeviceNamespace,
	managedprotocol.AnnotationDeviceName,
	managedprotocol.AnnotationDeviceUID,
	managedprotocol.AnnotationNodeName,
	managedprotocol.AnnotationWorkerProtocol,
	managedprotocol.AnnotationWorkerMode,
}

// validateReservedWorkerAnnotations permits an exact legacy object with
// previously absent, newly introduced protocol keys to be adopted, but never
// overwrites a conflicting identity. The caller subsequently writes every
// expected key and strict post-create/audit checks require the complete set.
func validateReservedWorkerAnnotations(actual, expected map[string]string) error {
	for _, key := range reservedWorkerAnnotationKeys {
		value, present := actual[key]
		expectedValue, expectedPresent := expected[key]
		if !present {
			continue
		}
		if !expectedPresent || value != expectedValue {
			return fmt.Errorf("reserved annotation %s=%q does not match the generated worker identity", key, value)
		}
	}
	return nil
}

func workerAnnotationsMatch(actual, expected map[string]string) bool {
	for _, key := range reservedWorkerAnnotationKeys {
		value, present := actual[key]
		expectedValue, expectedPresent := expected[key]
		if present != expectedPresent || value != expectedValue {
			return false
		}
	}
	return true
}

func applyWorkerBindingAnnotations(meta *metav1.ObjectMeta, device *ciskov1.CiscoDevice, managed bool) {
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	for key, value := range workerServiceAccountAnnotations(device, managed) {
		meta.Annotations[key] = value
	}
}

func applyGeneratedWorkerBindingMetadata(
	binding *rbacv1.RoleBinding,
	device *ciskov1.CiscoDevice,
	managed bool,
	scheme *runtime.Scheme,
) error {
	applyWorkerBindingAnnotations(&binding.ObjectMeta, device, managed)
	return controllerutil.SetControllerReference(device, binding, scheme)
}

func objectMetaHasIdentity(meta *metav1.ObjectMeta) bool {
	return meta != nil && (meta.UID != "" || meta.ResourceVersion != "" || !meta.CreationTimestamp.IsZero())
}

func exactWorkerSubject(namespace, name string) []rbacv1.Subject {
	return []rbacv1.Subject{{
		Kind: rbacv1.ServiceAccountKind, Name: name, Namespace: namespace,
	}}
}

func hasWorkerSubject(subjects []rbacv1.Subject, namespace, name string) bool {
	for i := range subjects {
		subject := &subjects[i]
		if subject.Kind == rbacv1.ServiceAccountKind && subject.Name == name && subject.Namespace == namespace {
			return true
		}
	}
	return false
}

func validateGeneratedRoleBinding(binding *rbacv1.RoleBinding, device *ciskov1.CiscoDevice, saName string) error {
	if binding.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole}) {
		return fmt.Errorf("generated worker RoleBinding %s/%s has unexpected roleRef", binding.Namespace, binding.Name)
	}
	if !reflect.DeepEqual(binding.Subjects, exactWorkerSubject(device.Namespace, saName)) {
		return fmt.Errorf("generated worker RoleBinding %s/%s has unexpected subjects", binding.Namespace, binding.Name)
	}
	if owner := metav1.GetControllerOf(binding); owner != nil &&
		(owner.APIVersion != ciskov1.GroupVersion.String() || owner.Kind != "CiscoDevice" || owner.Name != device.Name || owner.UID != device.UID) {
		return fmt.Errorf("generated worker RoleBinding %s/%s is controlled by another object", binding.Namespace, binding.Name)
	}
	return validateReservedWorkerAnnotations(binding.Annotations, workerServiceAccountAnnotations(device, binding.Annotations[managedprotocol.AnnotationManaged] == "true"))
}

func validateGeneratedClusterRoleBinding(
	binding *rbacv1.ClusterRoleBinding,
	device *ciskov1.CiscoDevice,
	saName, roleName string,
) error {
	if binding.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: roleName}) {
		return fmt.Errorf("generated worker ClusterRoleBinding %s has unexpected roleRef", binding.Name)
	}
	if !reflect.DeepEqual(binding.Subjects, exactWorkerSubject(device.Namespace, saName)) {
		return fmt.Errorf("generated worker ClusterRoleBinding %s has unexpected subjects", binding.Name)
	}
	if len(binding.OwnerReferences) != 0 {
		return fmt.Errorf("generated worker ClusterRoleBinding %s has unexpected ownerReferences", binding.Name)
	}
	return validateReservedWorkerAnnotations(binding.Annotations, workerServiceAccountAnnotations(device, binding.Annotations[managedprotocol.AnnotationManaged] == "true"))
}

// auditGeneratedWorkerBindings rejects every additive grant to the generated
// ServiceAccount, not just drift of the two expected bindings. RBAC is
// additive: validating only the canonical names would leave a second broad
// binding as a complete bypass of the topology admission contract.
func (r *CiscoDeviceReconciler) auditGeneratedWorkerBindings(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	saName, workerClusterRole string,
) error {
	expectedAnnotations := workerServiceAccountAnnotations(device, workerClusterRole == managedprotocol.ManagedWorkerClusterRole)
	expectedRB := types.NamespacedName{Namespace: device.Namespace, Name: saName}
	seenRB := false
	var roleBindings rbacv1.RoleBindingList
	if err := r.reader().List(ctx, &roleBindings); err != nil {
		return fmt.Errorf("audit generated worker RoleBindings: %w", err)
	}
	for i := range roleBindings.Items {
		binding := &roleBindings.Items[i]
		if !hasWorkerSubject(binding.Subjects, device.Namespace, saName) {
			continue
		}
		if client.ObjectKeyFromObject(binding) != expectedRB {
			return fmt.Errorf("generated worker ServiceAccount %s/%s has unexpected additive RoleBinding %s/%s", device.Namespace, saName, binding.Namespace, binding.Name)
		}
		if err := validateGeneratedRoleBinding(binding, device, saName); err != nil {
			return err
		}
		if !workerAnnotationsMatch(binding.Annotations, expectedAnnotations) || !managedServiceAccountOwnedByDeviceMeta(&binding.ObjectMeta, device) {
			return fmt.Errorf("generated worker RoleBinding %s/%s is not exactly incarnation-bound", binding.Namespace, binding.Name)
		}
		seenRB = true
	}
	if !seenRB {
		return fmt.Errorf("generated worker RoleBinding %s is missing during access audit", expectedRB)
	}

	expectedCRB := vkAccessClusterRoleBindingName(device.Namespace, saName)
	seenCRB := false
	var clusterRoleBindings rbacv1.ClusterRoleBindingList
	if err := r.reader().List(ctx, &clusterRoleBindings); err != nil {
		return fmt.Errorf("audit generated worker ClusterRoleBindings: %w", err)
	}
	for i := range clusterRoleBindings.Items {
		binding := &clusterRoleBindings.Items[i]
		if !hasWorkerSubject(binding.Subjects, device.Namespace, saName) {
			continue
		}
		if binding.Name != expectedCRB {
			return fmt.Errorf("generated worker ServiceAccount %s/%s has unexpected additive ClusterRoleBinding %s", device.Namespace, saName, binding.Name)
		}
		if err := validateGeneratedClusterRoleBinding(binding, device, saName, workerClusterRole); err != nil {
			return err
		}
		if !workerAnnotationsMatch(binding.Annotations, expectedAnnotations) {
			return fmt.Errorf("generated worker ClusterRoleBinding %s is not exactly incarnation-bound", binding.Name)
		}
		seenCRB = true
	}
	if !seenCRB {
		return fmt.Errorf("generated worker ClusterRoleBinding %s is missing during access audit", expectedCRB)
	}
	return nil
}

func (r *CiscoDeviceReconciler) cleanupVKClusterAccess(ctx context.Context, device *ciskov1.CiscoDevice, saName string) error {
	generatedManaged := saName == managedWorkerServiceAccountName(device) && device.Status.NodeIdentity != nil
	generatedLegacy := saName == topologyLegacyWorkerServiceAccountName(device) &&
		(r.ManagedTopology || device.Status.LegacyHandoff != nil ||
			device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker] == string(device.UID))
	if generatedManaged || generatedLegacy {
		return r.cleanupGeneratedWorkerAccess(ctx, device, saName, generatedManaged)
	}
	if saName == r.vkServiceAccountName() {
		var devices ciskov1.CiscoDeviceList
		if err := r.List(ctx, &devices, client.InNamespace(device.Namespace)); err != nil {
			return fmt.Errorf("list CiscoDevices for VK access cleanup: %w", err)
		}
		for i := range devices.Items {
			other := &devices.Items[i]
			if other.Name == device.Name || !other.DeletionTimestamp.IsZero() {
				continue
			}
			return nil
		}
	}
	crbName := vkAccessClusterRoleBindingName(device.Namespace, saName)
	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: crbName}}
	if err := r.Delete(ctx, crb); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete ClusterRoleBinding %s: %w", crbName, err)
	}
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: device.Namespace,
		},
	}
	if err := r.Delete(ctx, rb); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete RoleBinding %s/%s: %w", rb.Namespace, rb.Name, err)
	}
	// Shared standalone ServiceAccounts are intentionally retained: deleting
	// one would invalidate projected tokens of other workers in the namespace.
	return nil
}

func (r *CiscoDeviceReconciler) cleanupGeneratedWorkerAccess(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	saName string,
	managed bool,
) error {
	expectedAnnotations := workerServiceAccountAnnotations(device, managed)
	workerRole := vkSharedClusterRole
	if managed {
		workerRole = managedprotocol.ManagedWorkerClusterRole
	}

	var sa corev1.ServiceAccount
	saKey := types.NamespacedName{Namespace: device.Namespace, Name: saName}
	if err := r.reader().Get(ctx, saKey, &sa); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("read generated worker ServiceAccount %s: %w", saKey, err)
		}
	} else if !managedServiceAccountOwnedByDevice(&sa, device) || !workerAnnotationsMatch(sa.Annotations, expectedAnnotations) {
		return fmt.Errorf("refusing to clean generated worker access: ServiceAccount %s is not exactly owned by this CiscoDevice incarnation", saKey)
	}

	var rb rbacv1.RoleBinding
	rbKey := types.NamespacedName{Namespace: device.Namespace, Name: saName}
	if err := r.reader().Get(ctx, rbKey, &rb); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("read generated worker RoleBinding %s: %w", rbKey, err)
		}
	} else {
		if err := validateGeneratedRoleBinding(&rb, device, saName); err != nil ||
			!workerAnnotationsMatch(rb.Annotations, expectedAnnotations) || !managedServiceAccountOwnedByDeviceMeta(&rb.ObjectMeta, device) {
			if err == nil {
				err = fmt.Errorf("binding metadata is not exactly incarnation-bound")
			}
			return fmt.Errorf("refusing to delete generated worker RoleBinding %s: %w", rbKey, err)
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, &rb); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete generated worker RoleBinding %s: %w", rbKey, err)
		}
	}

	crbKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, saName)}
	var crb rbacv1.ClusterRoleBinding
	if err := r.reader().Get(ctx, crbKey, &crb); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("read generated worker ClusterRoleBinding %s: %w", crbKey, err)
		}
	} else {
		if err := validateGeneratedClusterRoleBinding(&crb, device, saName, workerRole); err != nil ||
			!workerAnnotationsMatch(crb.Annotations, expectedAnnotations) {
			if err == nil {
				err = fmt.Errorf("binding metadata is not exactly incarnation-bound")
			}
			return fmt.Errorf("refusing to delete generated worker ClusterRoleBinding %s: %w", crbKey, err)
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, &crb); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete generated worker ClusterRoleBinding %s: %w", crbKey, err)
		}
	}

	if sa.Name != "" {
		if err := deleteWithUIDPrecondition(ctx, r.Client, &sa); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete generated worker ServiceAccount %s: %w", saKey, err)
		}
	}
	return nil
}

func deleteWithUIDPrecondition(ctx context.Context, kubeClient client.Client, object client.Object) error {
	uid := object.GetUID()
	if uid == "" {
		// Kubernetes always assigns UIDs to persisted objects. fake.Client does
		// not, so retain production CAS semantics while allowing unit fixtures to
		// exercise the rest of the cleanup proof.
		return kubeClient.Delete(ctx, object)
	}
	return kubeClient.Delete(ctx, object, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
}

func serviceAccountHasIdentity(sa *corev1.ServiceAccount) bool {
	return sa != nil && (sa.UID != "" || sa.ResourceVersion != "" || !sa.CreationTimestamp.IsZero())
}

func managedServiceAccountAnnotations(device *ciskov1.CiscoDevice) map[string]string {
	return workerServiceAccountAnnotations(device, true)
}

func managedServiceAccountAnnotationsMatch(sa *corev1.ServiceAccount, device *ciskov1.CiscoDevice) bool {
	if sa == nil {
		return false
	}
	return workerAnnotationsMatch(sa.Annotations, managedServiceAccountAnnotations(device))
}

func managedServiceAccountOwnedByDevice(sa *corev1.ServiceAccount, device *ciskov1.CiscoDevice) bool {
	if sa == nil {
		return false
	}
	return managedServiceAccountOwnedByDeviceMeta(&sa.ObjectMeta, device)
}

func managedServiceAccountOwnedByDeviceMeta(objectMeta *metav1.ObjectMeta, device *ciskov1.CiscoDevice) bool {
	if objectMeta == nil || device == nil || device.UID == "" {
		return false
	}
	owner := metav1.GetControllerOf(objectMeta)
	return owner != nil && owner.APIVersion == ciskov1.GroupVersion.String() && owner.Kind == "CiscoDevice" &&
		owner.Name == device.Name && owner.UID == device.UID
}

// apphostingPrereqFamilies is the legacy closed IOS-XE family set the
// controller owns for CiscoDevice.spec.configPrereqs.
var apphostingPrereqFamilies = []string{
	"interface_virtual_port_group",
	"dhcp",
	"access_list_extended",
}

func ownedPrereqConfigName(deviceName string) string {
	return deviceName + "-prereqs"
}

func ownedIOSXEConfigName(deviceName string) string {
	return ownedPrereqConfigName(deviceName)
}

func emptyPrereqInline() runtime.RawExtension {
	return runtime.RawExtension{Raw: []byte(`{"interface_virtual_port_group":{},"dhcp":{},"access_list_extended":{}}`)}
}

func emptyPrereqInlineFor(policy platforms.ConfigPrereqPolicy) runtime.RawExtension {
	if strings.TrimSpace(policy.EmptyIntentJSON) == "" {
		return runtime.RawExtension{Raw: []byte(`{}`)}
	}
	return runtime.RawExtension{Raw: []byte(policy.EmptyIntentJSON)}
}

func perDeviceDeploymentLabels(deviceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "cisco-vk",
		"app.kubernetes.io/instance":   deviceName,
		"app.kubernetes.io/managed-by": "ciscodevice-controller",
	}
}

func perDeviceVKNodeAffinity() *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      virtualKubeletNodeLabelKey,
								Operator: corev1.NodeSelectorOpNotIn,
								Values:   []string{virtualKubeletNodeLabelValue},
							},
						},
					},
				},
			},
		},
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

func managedWorkerIdentityEnv(device *ciskov1.CiscoDevice, nodeName string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "CISCO_VK_MANAGED_TOPOLOGY", Value: "true"},
		{Name: "CISCO_VK_DEVICE_NAMESPACE", Value: device.Namespace},
		{Name: "CISCO_VK_DEVICE_NAME", Value: device.Name},
		{Name: "CISCO_VK_DEVICE_UID", Value: string(device.UID)},
		{Name: "CISCO_VK_NODE_NAME", Value: nodeName},
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

func isPrereqTearingDown(cr client.Object) bool {
	policy := prereqPolicyForKind(prereqConfigKind(cr))
	return getPrereqPrune(cr) &&
		getPrereqSourceInline(cr) != nil &&
		string(getPrereqSourceInline(cr).Raw) == string(emptyPrereqInlineFor(policy).Raw)
}

// reconcileConfigPrereqs creates, updates, or tears down the platform config CR
// owned by CiscoDevice.spec.configPrereqs. Teardown delegates cleanup to the
// platform config controller's own pruneOnRelinquish finalizer: mark the owned
// CR for relinquish, delete it with foreground propagation, observe it enter
// deletion, then wait for it to vanish.
func (r *CiscoDeviceReconciler) reconcileConfigPrereqs(ctx context.Context, device *ciskov1.CiscoDevice) (bool, error) {
	policy := prereqPolicyForDevice(device)
	name := ownedPrereqConfigName(device.Name)
	key := types.NamespacedName{Namespace: device.Namespace, Name: name}

	existing, found, err := r.getOwnedPrereqConfig(ctx, key, policy.Kind)
	if err != nil {
		return false, err
	}

	if device.Spec.ConfigPrereqs == nil {
		if device.Annotations[ForcePrereqsSkipAnnotation] == "true" {
			if found {
				if err := r.forceSkipOwnedPrereqs(ctx, existing); err != nil {
					return false, err
				}
			}
			r.emitPrereqsSkipped(device, prereqOrphanFamilies(existing, found, policy))
			return true, nil
		}
		if !found {
			if prereqTeardownObserved(device) {
				return true, nil
			}
			if prereqTeardownStarted(device) {
				if r.Recorder != nil {
					r.Recorder.Eventf(device, corev1.EventTypeWarning, "PrereqTeardownDeletedExternally",
						"owned %s %s/%s disappeared before deletion was observed; evaluating cleanup recovery",
						policy.Kind, device.Namespace, name)
				}
				if !canRecreatePrereqTeardownConfig(policy) {
					if r.Recorder != nil {
						r.Recorder.Eventf(device, corev1.EventTypeWarning, "PrereqTeardownSkipped",
							"owned %s %s/%s disappeared before deletion was observed and cannot be safely recreated without prior ownership status; device-side cleanup may be orphaned",
							policy.Kind, device.Namespace, name)
					}
					if err := r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
						Type:               ciskov1.CiscoDeviceConditionPrereqTeardownObserved,
						Status:             metav1.ConditionTrue,
						Reason:             fmt.Sprintf("%sDeletedExternally", policy.Kind),
						ObservedGeneration: device.Generation,
						Message:            fmt.Sprintf("owned prereq %s disappeared before deletion was observed; cleanup skipped because prior ownership status is unavailable", policy.Kind),
					}); err != nil {
						return false, err
					}
					return true, nil
				}
				if err := r.recreatePrereqTeardownConfig(ctx, device, policy); err != nil {
					return false, err
				}
				return false, nil
			}
			return true, nil
		}
		if deletionTimestamp := existing.GetDeletionTimestamp(); deletionTimestamp != nil && !deletionTimestamp.IsZero() {
			if err := r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
				Type:               ciskov1.CiscoDeviceConditionPrereqTeardownObserved,
				Status:             metav1.ConditionTrue,
				Reason:             fmt.Sprintf("%sDeleting", prereqConfigKind(existing)),
				ObservedGeneration: device.Generation,
				Message:            fmt.Sprintf("owned prereq %s deletion has been observed", prereqConfigKind(existing)),
			}); err != nil {
				return false, err
			}
			return false, nil
		}
		updated, err := r.patchOwnedPrereqsForTeardown(ctx, existing, false)
		if err != nil {
			return false, err
		}
		fg := metav1.DeletePropagationForeground
		if err := r.Delete(ctx, updated, &client.DeleteOptions{PropagationPolicy: &fg}); err != nil && !errors.IsNotFound(err) {
			return false, fmt.Errorf("delete owned %s for prereq teardown: %w", prereqConfigKind(updated), err)
		}
		log.FromContext(ctx).Info("configPrereqs teardown: delete requested", "configKind", prereqConfigKind(updated), "config", name)
		return false, nil
	}

	families, err := desiredPrereqFamilies(device.Name, device.Spec.ConfigPrereqs, policy)
	if err != nil {
		return false, err
	}
	if len(families) == 0 {
		return false, fmt.Errorf("configPrereqs for %s produced no managed families", device.Name)
	}
	desired, err := newPrereqConfigObject(policy.Kind, key)
	if err != nil {
		return false, err
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.SetAnnotations(propagatedCorrelationAnnotations(desired.GetAnnotations(), device.Annotations, r.now()))
		if err := setPrereqConfigSpec(desired, device.Name, families, &device.Spec.ConfigPrereqs.Configuration, false); err != nil {
			return err
		}
		return controllerutil.SetControllerReference(device, desired, r.Scheme)
	})
	if err != nil {
		return false, fmt.Errorf("upsert owned %s: %w", prereqConfigKind(desired), err)
	}
	if err := r.setCiscoDeviceCondition(ctx, device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionPrereqTeardownObserved,
		Status:             metav1.ConditionFalse,
		Reason:             "PrereqsActive",
		ObservedGeneration: device.Generation,
		Message:            fmt.Sprintf("owned prereq %s is active", prereqConfigKind(desired)),
	}); err != nil {
		return false, err
	}
	log.FromContext(ctx).Info("configPrereqs reconciled", "configKind", prereqConfigKind(desired), "config", name, "operation", op)
	return true, nil
}

func propagatedCorrelationAnnotations(destination, source map[string]string, now time.Time) map[string]string {
	out := make(map[string]string, len(destination)+5)
	for key, value := range destination {
		out[key] = value
	}
	for _, key := range []string{
		correlation.TraceparentAnnotation,
		correlation.TracestateAnnotation,
		correlation.TraceWindowEndAnnotation,
		correlation.UpstreamTraceparentAnnotation,
		correlation.LifecycleIDAnnotation,
	} {
		delete(out, key)
	}
	for key, value := range correlation.SanitizedAnnotationsAt(source, now) {
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SetupWithManager registers the controller with the manager.
func (r *CiscoDeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&ciskov1.CiscoDevice{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&configv1alpha1.IOSXEConfig{}).
		Owns(&configv1alpha1.NXOSConfig{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToCiscoDevices))
	if r.ManagedTopology {
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), // ctxlint:allow manager field-index registration root
			&ciskov1.CiscoDevice{}, ciscoDevicePhysicalIdentityIndex, physicalIdentityIndexValues); err != nil {
			return fmt.Errorf("index CiscoDevice physical identities: %w", err)
		}
		if err := mgr.Add(&sharedWorkerRetirementRunnable{reconciler: r}); err != nil {
			return fmt.Errorf("register legacy shared-worker retirement runnable: %w", err)
		}
		builder = builder.
			Watches(&ciskov1.CiscoDevice{}, handler.EnqueueRequestsFromMapFunc(r.mapPhysicalIdentityPeers)).
			Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.mapManagedNodeToCiscoDevice)).
			Watches(&coordv1.Lease{}, handler.EnqueueRequestsFromMapFunc(r.mapManagedMaintenanceToCiscoDevice)).
			Watches(&opsv1alpha1.IOSXESoftwareUpgrade{}, handler.EnqueueRequestsFromMapFunc(r.mapManagedMaintenanceToCiscoDevice))
	}
	return builder.Complete(r)
}

func (r *CiscoDeviceReconciler) mapManagedMaintenanceToCiscoDevice(_ context.Context, obj client.Object) []ctrl.Request {
	annotations := obj.GetAnnotations()
	if annotations[managedprotocol.AnnotationManaged] != "true" {
		return nil
	}
	namespace := annotations[managedprotocol.AnnotationDeviceNamespace]
	name := annotations[managedprotocol.AnnotationDeviceName]
	if namespace == "" || name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
}

func (r *CiscoDeviceReconciler) mapManagedNodeToCiscoDevice(_ context.Context, obj client.Object) []ctrl.Request {
	node, ok := obj.(*corev1.Node)
	if !ok || node.Annotations[managedprotocol.AnnotationManaged] != "true" {
		return nil
	}
	namespace := node.Annotations[managedprotocol.AnnotationDeviceNamespace]
	name := node.Annotations[managedprotocol.AnnotationDeviceName]
	if namespace == "" || name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
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
	return renderDeviceConfigForWorker(spec, false)
}

func renderDeviceConfigForWorker(spec *ciskov1.DeviceSpec, gnoiTLSClientCertificate bool) (string, error) {
	// The VK config loader expects:
	//   device:
	//     driver: ...
	//     address: ...

	// Deep-copy the spec so nested redaction cannot mutate the API object.
	sanitized := spec.DeepCopy()
	sanitized.Password = ""
	sanitized.CredentialSecretRef = nil
	sanitized.ConfigPrereqs = nil
	sanitized.Worker = nil
	if sanitized.GNOI != nil && sanitized.GNOI.TLS != nil {
		gnoiTLS := sanitized.GNOI.TLS
		if gnoiTLS.SecretRef != nil {
			gnoiTLS.SecretRef = nil
			gnoiTLS.CAFile = gnoiTLSMountPath + "/ca.crt"
			gnoiTLS.CertFile = ""
			gnoiTLS.KeyFile = ""
			if gnoiTLSClientCertificate {
				gnoiTLS.CertFile = gnoiTLSMountPath + "/tls.crt"
				gnoiTLS.KeyFile = gnoiTLSMountPath + "/tls.key"
			}
		}
	}
	if provisioning := xeGNOICertificateProvisioning(sanitized); provisioning != nil {
		provisioning.SecretRef.Name = gnoiProvisioningWorkerSecretRefName
	}

	wrapper := struct {
		Device ciskov1.DeviceSpec `json:"device"`
	}{
		Device: *sanitized,
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

// managedWorkerPodTemplateRevision content-addresses every desired PodTemplate
// input while excluding only the two fields that carry the resulting digest.
// Kubernetes Secret bytes are never part of the template; their API
// resourceVersions already appear in its rotation annotations.
func managedWorkerPodTemplateRevision(template *corev1.PodTemplateSpec) (string, error) {
	if template == nil {
		return "", fmt.Errorf("managed worker PodTemplate is nil")
	}
	canonical := template.DeepCopy()
	delete(canonical.Annotations, managedprotocol.AnnotationWorkerConfigRevision)
	for i := range canonical.Spec.Containers {
		canonical.Spec.Containers[i].Env = slices.DeleteFunc(
			canonical.Spec.Containers[i].Env,
			func(env corev1.EnvVar) bool { return env.Name == managedprotocol.EnvWorkerRevision },
		)
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode managed worker PodTemplate revision: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// ensureManagedDeviceDeletionSafe keeps the identity-bound Node and worker
// credentials alive until every source of device-mutation authority agrees
// the device is idle. Managed reservations are deliberately not time-reaped;
// a missing child or expired Lease cannot prove the physical outcome.
func (r *CiscoDeviceReconciler) ensureManagedDeviceDeletionSafe(ctx context.Context, device *ciskov1.CiscoDevice) error {
	if handoff := device.Status.LegacyHandoff; handoff != nil && handoff.Phase != ciskov1.DeviceLegacyHandoffComplete {
		return fmt.Errorf("managed CiscoDevice deletion is blocked while legacy writer handoff is %q", handoff.Phase)
	}
	return r.ensureManagedDeviceAuthoritiesSettled(ctx, device)
}

// ensureManagedDeviceAuthoritiesSettled proves the shared mutation state is
// idle. Reverse writer handoff uses this proof while its own handoff status is
// necessarily in flight; deletion adds the stricter phase gate above.
func (r *CiscoDeviceReconciler) ensureManagedDeviceAuthoritiesSettled(ctx context.Context, device *ciskov1.CiscoDevice) error {
	bound := device.Status.NodeIdentity
	if bound == nil {
		return nil
	}
	if bound.DeviceUID != string(device.UID) || bound.NodeName == "" || bound.NodeUID == "" {
		return fmt.Errorf("managed CiscoDevice deletion is blocked by an incomplete Node identity binding")
	}
	if lock := device.Status.TopologyLock; lock != nil {
		return fmt.Errorf("managed CiscoDevice deletion is blocked by topology lock %q in state %q", lock.ReservationID, lock.State)
	}
	if session := device.Status.MaintenanceSession; session != nil && session.Phase != ciskov1.DeviceMaintenanceSessionSettled {
		return fmt.Errorf("managed CiscoDevice deletion is blocked by unresolved maintenance session %q in phase %q", session.SessionToken, session.Phase)
	}

	leaseNamespace := r.LeaseNamespace
	if leaseNamespace == "" {
		leaseNamespace = device.Namespace
	}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	leaseKey := types.NamespacedName{
		Namespace: leaseNamespace,
		Name:      configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily),
	}
	var lease coordv1.Lease
	if err := r.reader().Get(ctx, leaseKey, &lease); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("read canonical mutation Lease %s before managed CiscoDevice deletion: %w", leaseKey, err)
		}
		// A prior deletion attempt revokes both bindings before deleting the
		// manager-owned Leases. Permit an idempotent retry only after that exact
		// API authority is demonstrably gone; a missing Lease while either
		// binding remains is not evidence that device mutation has settled.
		revoked, verifyErr := r.managedWorkerAccessRevoked(ctx, device)
		if verifyErr != nil {
			return verifyErr
		}
		if !revoked {
			return fmt.Errorf("managed CiscoDevice deletion requires its canonical mutation Lease %s", leaseKey)
		}
	} else {
		expectedAnnotations, expectedLabels := managedMutationLeaseMetadata(
			device,
			bound.NodeName,
			bound.NodeUID,
			fmt.Sprintf("system:serviceaccount:%s:%s", device.Namespace, managedWorkerServiceAccountName(device)),
		)
		if err := validateManagedMutationLeaseMetadata(&lease, expectedAnnotations, expectedLabels); err != nil {
			return fmt.Errorf("managed CiscoDevice deletion is blocked by invalid mutation Lease identity: %w", err)
		}
		if lease.Spec.HolderIdentity != nil && strings.TrimSpace(*lease.Spec.HolderIdentity) != "" {
			return fmt.Errorf("managed CiscoDevice deletion is blocked while mutation Lease %s is held", leaseKey)
		}
		for _, annotation := range []string{
			managedprotocol.AnnotationMaintenanceRequestVersion,
			managedprotocol.AnnotationMaintenanceSessionToken,
			managedprotocol.AnnotationMaintenanceRequestedAt,
			managedprotocol.AnnotationMaintenanceOperationNS,
			managedprotocol.AnnotationMaintenanceOperationName,
			managedprotocol.AnnotationMaintenanceOperationUID,
			managedprotocol.AnnotationMaintenanceControlRevision,
		} {
			if _, present := lease.Annotations[annotation]; present {
				return fmt.Errorf("managed CiscoDevice deletion is blocked by unresolved mutation Lease request metadata")
			}
		}
	}

	var upgrades opsv1alpha1.IOSXESoftwareUpgradeList
	if err := r.reader().List(ctx, &upgrades, client.InNamespace(device.Namespace)); err != nil {
		return fmt.Errorf("list managed software upgrades before CiscoDevice deletion: %w", err)
	}
	for i := range upgrades.Items {
		upgrade := &upgrades.Items[i]
		if upgrade.Spec.DeviceRef.Name != device.Name {
			continue
		}
		managedBinding := upgrade.Annotations[managedprotocol.AnnotationManaged] == "true" &&
			upgrade.Annotations[managedprotocol.AnnotationDeviceUID] == string(device.UID)
		if !managedBinding {
			continue
		}
		admission := upgrade.Status.ManagerAdmission
		if admission == nil || admission.DeviceUID != string(device.UID) {
			return fmt.Errorf("managed CiscoDevice deletion is blocked by incomplete software-upgrade admission %s/%s", upgrade.Namespace, upgrade.Name)
		}
		if admission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
			return fmt.Errorf("managed CiscoDevice deletion is blocked by unsettled software upgrade %s/%s", upgrade.Namespace, upgrade.Name)
		}
	}

	if r.TopologyPolicyNamespace == "" || r.TopologyPolicyName == "" {
		return fmt.Errorf("managed CiscoDevice deletion cannot verify the rollout ledger without the topology policy identity")
	}
	var policyCM corev1.ConfigMap
	policyKey := types.NamespacedName{Namespace: r.TopologyPolicyNamespace, Name: r.TopologyPolicyName}
	if err := r.reader().Get(ctx, policyKey, &policyCM); err != nil {
		return fmt.Errorf("read topology policy before managed CiscoDevice deletion: %w", err)
	}
	policy, err := topologyrollout.ParseAdminPolicy(&policyCM)
	if err != nil {
		return fmt.Errorf("validate topology policy before managed CiscoDevice deletion: %w", err)
	}
	_, ledger, err := (topologyrollout.Store{
		Client: r.Client, APIReader: r.reader(),
		Key: types.NamespacedName{Namespace: policy.Namespace, Name: policy.Config.LedgerName}, ExpectedUID: types.UID(policy.LedgerUID),
	}).Read(ctx)
	if err != nil {
		return fmt.Errorf("read rollout ledger before managed CiscoDevice deletion: %w", err)
	}
	for _, reservation := range ledger.Reservations {
		if reservation.DeviceUID == string(device.UID) {
			return fmt.Errorf("managed CiscoDevice deletion is blocked by active rollout reservation %q", reservation.ID)
		}
	}
	return nil
}

// managedWorkerAccessRevoked verifies the two controller-generated bindings
// which grant a per-device worker API authority are absent. It intentionally
// uses the uncached reader: this check is the sole proof that a retry after
// canonical Lease cleanup cannot be driven by the former worker identity.
func (r *CiscoDeviceReconciler) managedWorkerAccessRevoked(ctx context.Context, device *ciskov1.CiscoDevice) (bool, error) {
	saName := managedWorkerServiceAccountName(device)
	var roleBinding rbacv1.RoleBinding
	roleBindingKey := types.NamespacedName{Namespace: device.Namespace, Name: saName}
	if err := r.reader().Get(ctx, roleBindingKey, &roleBinding); err == nil {
		return false, nil
	} else if !errors.IsNotFound(err) {
		return false, fmt.Errorf("verify managed worker RoleBinding revocation %s: %w", roleBindingKey, err)
	}

	var clusterRoleBinding rbacv1.ClusterRoleBinding
	clusterRoleBindingKey := types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, saName)}
	if err := r.reader().Get(ctx, clusterRoleBindingKey, &clusterRoleBinding); err == nil {
		return false, nil
	} else if !errors.IsNotFound(err) {
		return false, fmt.Errorf("verify managed worker ClusterRoleBinding revocation %s: %w", clusterRoleBindingKey, err)
	}
	return true, nil
}

// deleteDeviceNode deletes only the Node incarnation bound in status. Legacy
// standalone devices without a managed binding retain the historical
// name-based cleanup path for compatibility.
func (r *CiscoDeviceReconciler) deleteDeviceNode(ctx context.Context, device *ciskov1.CiscoDevice) error {
	logger := log.FromContext(ctx)
	name := resolvedNodeName(device)
	bound := device.Status.NodeIdentity
	legacyHandoff := device.Status.LegacyHandoff
	if bound != nil {
		if bound.DeviceUID != string(device.UID) || bound.NodeName == "" || bound.NodeUID == "" {
			return fmt.Errorf("refusing Node deletion: CiscoDevice status binding is incomplete or stale")
		}
		name = bound.NodeName
	} else if legacyHandoff != nil && legacyHandoff.Phase == ciskov1.DeviceLegacyHandoffComplete {
		if legacyHandoff.DeviceUID != string(device.UID) || legacyHandoff.NodeName == "" || legacyHandoff.NodeUID == "" {
			return fmt.Errorf("refusing Node deletion: completed legacy handoff binding is incomplete or stale")
		}
		name = legacyHandoff.NodeName
	}
	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: name}, node); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("VK node already absent", "node", name)
			return nil
		}
		return fmt.Errorf("failed to get node %s: %w", name, err)
	}
	if bound != nil {
		if string(node.UID) != bound.NodeUID || !managedNodeMatchesDevice(node, device) || node.Annotations[managedprotocol.AnnotationNodeUID] != bound.NodeUID {
			return fmt.Errorf("refusing Node deletion: %s now belongs to a different identity", name)
		}
	} else if legacyHandoff != nil && legacyHandoff.Phase == ciskov1.DeviceLegacyHandoffComplete {
		if !legacyHandoffNodeMatches(node, legacyHandoff) {
			return fmt.Errorf("refusing Node deletion: %s no longer matches the completed legacy handoff", name)
		}
	}
	uid := node.UID
	if err := r.Delete(ctx, node, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !errors.IsNotFound(err) {
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

func prereqPolicyForDevice(device *ciskov1.CiscoDevice) platforms.ConfigPrereqPolicy {
	if descriptor, ok := platforms.ForDriver(device.Spec.Driver); ok && descriptor.ConfigPrereqs.Kind != "" {
		return descriptor.ConfigPrereqs
	}
	return platforms.ConfigPrereqPolicy{
		Kind:                 platforms.ConfigKindIOSXE,
		FixedManagedFamilies: append([]string(nil), apphostingPrereqFamilies...),
		EmptyIntentJSON:      string(emptyPrereqInline().Raw),
	}
}

func prereqPolicyForKind(kind platforms.ConfigKind) platforms.ConfigPrereqPolicy {
	for _, driver := range platforms.KnownDrivers() {
		descriptor, ok := platforms.ForDriver(driver)
		if ok && descriptor.ConfigPrereqs.Kind == kind {
			return descriptor.ConfigPrereqs
		}
	}
	return platforms.ConfigPrereqPolicy{Kind: kind, EmptyIntentJSON: `{}`}
}

func desiredPrereqFamilies(deviceName string, prereqs *ciskov1.ConfigPrereqs, policy platforms.ConfigPrereqPolicy) ([]string, error) {
	var families []string
	if prereqs == nil {
		families = dedupeNonEmpty(policy.FixedManagedFamilies)
		return validatePrereqManagedFamilies(families, policy)
	}
	if len(prereqs.ManagedFamilies) > 0 {
		families = dedupeNonEmpty(prereqs.ManagedFamilies)
		return validatePrereqManagedFamilies(families, policy)
	}
	if len(policy.FixedManagedFamilies) > 0 {
		families = dedupeNonEmpty(policy.FixedManagedFamilies)
		return validatePrereqManagedFamilies(families, policy)
	}
	if policy.DeriveManagedFamiliesFromSource {
		var err error
		families, err = deriveManagedFamiliesFromSource(deviceName, prereqs.Configuration.Raw, policy)
		if err != nil {
			return nil, err
		}
		return validatePrereqManagedFamilies(families, policy)
	}
	return nil, nil
}

func validatePrereqManagedFamilies(families []string, policy platforms.ConfigPrereqPolicy) ([]string, error) {
	if len(policy.SupportedManagedFamilies) == 0 {
		return families, nil
	}
	supported := make(map[string]struct{}, len(policy.SupportedManagedFamilies))
	for _, family := range policy.SupportedManagedFamilies {
		supported[family] = struct{}{}
	}
	unsupported := make([]string, 0)
	for _, family := range families {
		if _, ok := supported[family]; !ok {
			unsupported = append(unsupported, family)
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return nil, fmt.Errorf("configPrereqs managedFamilies contains unsupported families %q for %s",
			unsupported, policy.Kind)
	}
	return families, nil
}

func deriveManagedFamiliesFromSource(deviceName string, raw []byte, policy platforms.ConfigPrereqPolicy) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("configPrereqs.configuration is empty; managedFamilies must be supplied")
	}
	var payload map[string]any
	if err := yaml.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("derive configPrereqs managedFamilies: %w", err)
	}
	if policy.Kind == platforms.ConfigKindNXOS {
		normalized, err := configprovider.NormalizeNXOSNetAsCodeSource(payload, deviceName)
		if err != nil {
			return nil, fmt.Errorf("derive NX-OS configPrereqs managedFamilies: %w", err)
		}
		payload = normalized
	}
	out := make([]string, 0, len(payload))
	for family := range payload {
		family = strings.TrimSpace(family)
		if family != "" {
			out = append(out, family)
		}
	}
	sort.Strings(out)
	return out, nil
}

func prereqConfigKind(obj client.Object) platforms.ConfigKind {
	switch obj.(type) {
	case *configv1alpha1.IOSXEConfig:
		return platforms.ConfigKindIOSXE
	case *configv1alpha1.NXOSConfig:
		return platforms.ConfigKindNXOS
	default:
		return platforms.ConfigKind("")
	}
}

func newPrereqConfigObject(kind platforms.ConfigKind, key types.NamespacedName) (client.Object, error) {
	switch kind {
	case platforms.ConfigKindIOSXE, "":
		return &configv1alpha1.IOSXEConfig{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		}, nil
	case platforms.ConfigKindNXOS:
		return &configv1alpha1.NXOSConfig{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported configPrereqs child kind %q", kind)
	}
}

func prereqKindFallbacks(preferred platforms.ConfigKind) []platforms.ConfigKind {
	out := []platforms.ConfigKind{preferred}
	for _, kind := range []platforms.ConfigKind{platforms.ConfigKindIOSXE, platforms.ConfigKindNXOS} {
		if kind != preferred {
			out = append(out, kind)
		}
	}
	return out
}

func (r *CiscoDeviceReconciler) getOwnedPrereqConfig(
	ctx context.Context,
	key types.NamespacedName,
	preferred platforms.ConfigKind,
) (client.Object, bool, error) {
	for _, kind := range prereqKindFallbacks(preferred) {
		obj, err := newPrereqConfigObject(kind, key)
		if err != nil {
			return nil, false, err
		}
		getErr := r.Get(ctx, key, obj)
		if getErr == nil {
			return obj, true, nil
		}
		if !errors.IsNotFound(getErr) {
			return nil, false, fmt.Errorf("get owned %s: %w", kind, getErr)
		}
	}
	obj, err := newPrereqConfigObject(preferred, key)
	if err != nil {
		return nil, false, err
	}
	return obj, false, nil
}

func setPrereqConfigSpec(
	obj client.Object,
	deviceName string,
	families []string,
	source *runtime.RawExtension,
	prune bool,
) error {
	if source == nil {
		return fmt.Errorf("nil configPrereqs source")
	}
	sourceCopy := source.DeepCopy()
	switch typed := obj.(type) {
	case *configv1alpha1.IOSXEConfig:
		typed.Spec = configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: deviceName},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies:   append([]string(nil), families...),
				Source:            configv1alpha1.ConfigurationSource{Inline: sourceCopy},
				DriftPolicy:       configv1alpha1.DriftPolicyRevert,
				PruneOnRelinquish: prune,
			},
		}
	case *configv1alpha1.NXOSConfig:
		typed.Spec = configv1alpha1.NXOSConfigSpec(configv1alpha1.CommonConfigSpec{
			DeviceRef:         configv1alpha1.DeviceRef{Name: deviceName},
			ManagedFamilies:   append([]string(nil), families...),
			Source:            configv1alpha1.ConfigurationSource{Inline: sourceCopy},
			DriftPolicy:       configv1alpha1.DriftPolicyRevert,
			PruneOnRelinquish: prune,
		})
	default:
		return fmt.Errorf("unsupported configPrereqs object %T", obj)
	}
	return nil
}

func getPrereqPrune(obj client.Object) bool {
	switch typed := obj.(type) {
	case *configv1alpha1.IOSXEConfig:
		return typed.Spec.PruneOnRelinquish
	case *configv1alpha1.NXOSConfig:
		return (*configv1alpha1.CommonConfigSpec)(&typed.Spec).PruneOnRelinquish
	default:
		return false
	}
}

func setPrereqPrune(obj client.Object, prune bool) bool {
	switch typed := obj.(type) {
	case *configv1alpha1.IOSXEConfig:
		if typed.Spec.PruneOnRelinquish == prune {
			return false
		}
		typed.Spec.PruneOnRelinquish = prune
		return true
	case *configv1alpha1.NXOSConfig:
		spec := (*configv1alpha1.CommonConfigSpec)(&typed.Spec)
		if spec.PruneOnRelinquish == prune {
			return false
		}
		spec.PruneOnRelinquish = prune
		return true
	default:
		return false
	}
}

func getPrereqSourceInline(obj client.Object) *runtime.RawExtension {
	switch typed := obj.(type) {
	case *configv1alpha1.IOSXEConfig:
		return typed.Spec.Source.Inline
	case *configv1alpha1.NXOSConfig:
		return (*configv1alpha1.CommonConfigSpec)(&typed.Spec).Source.Inline
	default:
		return nil
	}
}

func getPrereqManagedFamilies(obj client.Object) []string {
	switch typed := obj.(type) {
	case *configv1alpha1.IOSXEConfig:
		return append([]string(nil), typed.Spec.ManagedFamilies...)
	case *configv1alpha1.NXOSConfig:
		return append([]string(nil), (*configv1alpha1.CommonConfigSpec)(&typed.Spec).ManagedFamilies...)
	default:
		return nil
	}
}

func getPrereqAtomicKeys(obj client.Object) map[string][]string {
	switch typed := obj.(type) {
	case *configv1alpha1.IOSXEConfig:
		return typed.Status.AtomicReplaceOwnedKeys
	case *configv1alpha1.NXOSConfig:
		return (*configv1alpha1.CommonConfigStatus)(&typed.Status).AtomicReplaceOwnedKeys
	default:
		return nil
	}
}

func (r *CiscoDeviceReconciler) patchOwnedPrereqsForTeardown(
	ctx context.Context,
	existing client.Object,
	forceRelinquishSkip bool,
) (client.Object, error) {
	updated, ok := existing.DeepCopyObject().(client.Object)
	if !ok {
		return nil, fmt.Errorf("deep-copy owned prereq %T did not return client.Object", existing)
	}
	changed := false
	changed = setPrereqPrune(updated, true) || changed
	if forceRelinquishSkip {
		annotations := updated.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		if annotations[forceRelinquishSkipAnnotation] != "true" {
			annotations[forceRelinquishSkipAnnotation] = "true"
			updated.SetAnnotations(annotations)
			changed = true
		}
	}
	if !changed {
		return updated, nil
	}
	base, ok := existing.DeepCopyObject().(client.Object)
	if !ok {
		return nil, fmt.Errorf("deep-copy owned prereq %T did not return client.Object", existing)
	}
	if err := r.Patch(ctx, updated, client.MergeFrom(base)); err != nil {
		return nil, fmt.Errorf("patch owned %s for prereq teardown: %w", prereqConfigKind(existing), err)
	}
	return updated, nil
}

func (r *CiscoDeviceReconciler) forceSkipOwnedPrereqs(ctx context.Context, existing client.Object) error {
	updated, err := r.patchOwnedPrereqsForTeardown(ctx, existing, true)
	if err != nil {
		return err
	}
	if deletionTimestamp := updated.GetDeletionTimestamp(); deletionTimestamp != nil && !deletionTimestamp.IsZero() {
		return nil
	}
	fg := metav1.DeletePropagationForeground
	if err := r.Delete(ctx, updated, &client.DeleteOptions{PropagationPolicy: &fg}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete force-skipped owned %s: %w", prereqConfigKind(updated), err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) recreatePrereqTeardownConfig(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	policy platforms.ConfigPrereqPolicy,
) error {
	families := dedupeNonEmpty(policy.FixedManagedFamilies)
	if !canRecreatePrereqTeardownConfig(policy) {
		return fmt.Errorf("cannot recreate deleted %s prereq teardown CR without fixed managed families", policy.Kind)
	}
	key := types.NamespacedName{Namespace: device.Namespace, Name: ownedPrereqConfigName(device.Name)}
	desired, err := newPrereqConfigObject(policy.Kind, key)
	if err != nil {
		return err
	}
	emptyInline := emptyPrereqInlineFor(policy)
	if err := setPrereqConfigSpec(desired, device.Name, families, &emptyInline, true); err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(device, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on recreated prereq %s: %w", prereqConfigKind(desired), err)
	}
	if err := r.Create(ctx, desired); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("recreate prereq %s teardown driver: %w", prereqConfigKind(desired), err)
	}
	return nil
}

func canRecreatePrereqTeardownConfig(policy platforms.ConfigPrereqPolicy) bool {
	return len(dedupeNonEmpty(policy.FixedManagedFamilies)) > 0
}

func prereqOrphanFamilies(cr client.Object, found bool, policy platforms.ConfigPrereqPolicy) []string {
	if !found {
		return dedupeNonEmpty(policy.FixedManagedFamilies)
	}
	if keysByFamily := getPrereqAtomicKeys(cr); len(keysByFamily) > 0 {
		out := make([]string, 0, len(keysByFamily))
		for family, keys := range keysByFamily {
			if len(keys) > 0 {
				out = append(out, family)
			}
		}
		if len(out) > 0 {
			sort.Strings(out)
			return out
		}
	}
	if families := getPrereqManagedFamilies(cr); len(families) > 0 {
		out := append([]string(nil), families...)
		sort.Strings(out)
		return out
	}
	return dedupeNonEmpty(policy.FixedManagedFamilies)
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
// namespace that reference it through device credentials, generic gNOI TLS,
// or the IOS-XE-only gNOI certificate-provisioning block.
func (r *CiscoDeviceReconciler) mapSecretToCiscoDevices(ctx context.Context, obj client.Object) []ctrl.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	var devices ciskov1.CiscoDeviceList
	if err := r.List(ctx, &devices, client.InNamespace(secret.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "list CiscoDevices for referenced-secret mapping",
			"secret", secret.Name, "namespace", secret.Namespace)
		return nil
	}
	requests := make([]ctrl.Request, 0, len(devices.Items))
	for i := range devices.Items {
		dev := &devices.Items[i]
		credentialMatch := dev.Spec.CredentialSecretRef != nil && dev.Spec.CredentialSecretRef.Name == secret.Name
		gnoiTLSRef := gnoiTLSSecretRef(&dev.Spec)
		gnoiTLSMatch := gnoiTLSRef != nil && gnoiTLSRef.Name == secret.Name
		provisioning := xeGNOICertificateProvisioning(&dev.Spec)
		provisioningMatch := provisioning != nil && provisioning.SecretRef.Name == secret.Name
		if !credentialMatch && !gnoiTLSMatch && !provisioningMatch {
			continue
		}
		requests = append(requests, ctrl.Request{NamespacedName: types.NamespacedName{
			Namespace: dev.Namespace,
			Name:      dev.Name,
		}})
	}
	return requests
}

type gnoiTLSProjectionState struct {
	enabled           bool
	secretName        string
	resourceVersion   string
	clientCertificate bool
}

// Unexpected API failures are retryable without changing a working Deployment.
// Missing or invalid Secret material instead disables only this worker's gNOI.
type gnoiSecretReadError struct{ error }

func (e *gnoiSecretReadError) Unwrap() error { return e.error }

type managedWorkerRevisionFence struct {
	desiredRevision string
}

func managedWorkerRevisionNeedsPreFence(
	deploymentUID types.UID,
	previousRevision, desiredRevision string,
	status *ciskov1.DeviceWorkerRevisionStatus,
) bool {
	return deploymentUID != "" && desiredRevision != "" && previousRevision != desiredRevision &&
		(status == nil || status.DesiredRevision != desiredRevision)
}

func (e *managedWorkerRevisionFence) Error() string {
	return "managed worker revision status must be fenced before Deployment rollout"
}

func (r *CiscoDeviceReconciler) fenceManagedWorkerRevision(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	desiredRevision string,
) error {
	if device == nil || desiredRevision == "" {
		return fmt.Errorf("managed worker revision fence is incomplete")
	}
	before := device.DeepCopy()
	device.Status.WorkerRevision = &ciskov1.DeviceWorkerRevisionStatus{
		DesiredRevision: desiredRevision,
		ObservedAt:      metav1.NewTime(r.now()),
	}
	if err := r.applyCiscoDeviceConditionObserved(device, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
		Status:             metav1.ConditionFalse,
		Reason:             "WorkerRolloutPending",
		Message:            "managed worker configuration changed; the old worker is fenced before Deployment rollout",
		ObservedGeneration: device.Generation,
	}); err != nil {
		return err
	}
	if statusesEqual(before.Status, device.Status) {
		return nil
	}
	if err := r.Status().Update(ctx, device); err != nil {
		return fmt.Errorf("persist managed worker revision pre-rollout fence: %w", err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) updateGNOIConfigurationCondition(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	deployment *appsv1.Deployment,
	desiredRevision string,
	configErr error,
) error {
	configured := gnoiTLSSecretRef(&device.Spec) != nil || xeGNOICertificateProvisioning(&device.Spec) != nil
	if !configured && meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionGNOIConfigurationReady) == nil {
		if !r.ManagedTopology {
			return nil
		}
	}
	var (
		workerStatus *ciskov1.DeviceWorkerRevisionStatus
		workerReady  = !r.ManagedTopology
	)
	if r.ManagedTopology && deployment != nil && desiredRevision != "" {
		var err error
		workerStatus, workerReady, err = r.observeManagedWorkerRevision(ctx, device, deployment, desiredRevision)
		if err != nil {
			return err
		}
	}
	condition := metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
		Status:             metav1.ConditionTrue,
		Reason:             "Validated",
		Message:            "Referenced gNOI Secret material is valid; device connectivity has not been checked",
		ObservedGeneration: device.Generation,
	}
	switch {
	case configErr != nil:
		condition.Status = metav1.ConditionFalse
		condition.Reason = "InvalidSecret"
		condition.Message = "Worker gNOI is disabled until its Secret is repaired: " + configErr.Error()
	case gNOIDisabled() || !configured:
		condition.Status = metav1.ConditionFalse
		condition.Reason = "NotConfigured"
		condition.Message = "Referenced gNOI Secret validation is inactive"
	case !workerReady:
		condition.Status = metav1.ConditionFalse
		condition.Reason = "WorkerRolloutPending"
		condition.Message = "validated gNOI configuration is waiting for the exact managed worker revision to become ready"
	}
	if r.ManagedTopology {
		before := device.DeepCopy()
		if workerStatus != nil && workerRevisionEvidenceEqual(device.Status.WorkerRevision, workerStatus) {
			workerStatus.ObservedAt = device.Status.WorkerRevision.ObservedAt
		}
		device.Status.WorkerRevision = workerStatus
		if err := r.applyCiscoDeviceConditionObserved(device, condition); err != nil {
			return err
		}
		if statusesEqual(before.Status, device.Status) {
			return nil
		}
		if err := r.Status().Update(ctx, device); err != nil {
			return fmt.Errorf("failed to update CiscoDevice gNOI/worker revision status: %w", err)
		}
		return nil
	}
	return r.setCiscoDeviceCondition(ctx, device, condition)
}

func workerRevisionEvidenceEqual(a, b *ciskov1.DeviceWorkerRevisionStatus) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.DesiredRevision == b.DesiredRevision &&
		a.ObservedRevision == b.ObservedRevision &&
		a.DeploymentUID == b.DeploymentUID &&
		a.DeploymentGeneration == b.DeploymentGeneration &&
		a.PodUID == b.PodUID &&
		timePointersEqual(a.PodStartTime, b.PodStartTime) &&
		timePointersEqual(a.ReadyHeartbeatTime, b.ReadyHeartbeatTime)
}

func timePointersEqual(a, b *metav1.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(b)
}

// observeManagedWorkerRevision proves that the sole ready Pod of the exact
// desired Deployment has reported its injected PodTemplate revision after that
// Pod started. Deployment readiness alone is insufficient during Recreate: an
// old ready Pod may remain visible after a Secret-driven template update.
func (r *CiscoDeviceReconciler) observeManagedWorkerRevision(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	deployment *appsv1.Deployment,
	desiredRevision string,
) (*ciskov1.DeviceWorkerRevisionStatus, bool, error) {
	return observeManagedWorkerRevision(ctx, r.reader(), r.now(), device, deployment, desiredRevision)
}

func observeManagedWorkerRevision(
	ctx context.Context,
	reader client.Reader,
	now time.Time,
	device *ciskov1.CiscoDevice,
	deployment *appsv1.Deployment,
	desiredRevision string,
) (*ciskov1.DeviceWorkerRevisionStatus, bool, error) {
	if device == nil || deployment == nil || desiredRevision == "" {
		return nil, false, nil
	}
	var current appsv1.Deployment
	key := types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}
	if err := reader.Get(ctx, key, &current); err != nil {
		return nil, false, fmt.Errorf("read managed worker Deployment revision: %w", err)
	}
	if current.UID == "" || current.Generation < 1 || !metav1.IsControlledBy(&current, device) {
		return nil, false, nil
	}
	if current.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision] != desiredRevision {
		return nil, false, nil
	}
	recomputed, err := managedWorkerPodTemplateRevision(&current.Spec.Template)
	if err != nil {
		return nil, false, err
	}
	if recomputed != desiredRevision {
		return nil, false, nil
	}
	status := &ciskov1.DeviceWorkerRevisionStatus{
		DesiredRevision:      desiredRevision,
		DeploymentUID:        string(current.UID),
		DeploymentGeneration: current.Generation,
		ObservedAt:           metav1.NewTime(now),
	}

	var node corev1.Node
	if device.Status.NodeIdentity == nil {
		return status, false, nil
	}
	if err := reader.Get(ctx, types.NamespacedName{Name: device.Status.NodeIdentity.NodeName}, &node); err != nil {
		if errors.IsNotFound(err) {
			return status, false, nil
		}
		return nil, false, fmt.Errorf("read managed worker Node revision: %w", err)
	}
	if string(node.UID) != device.Status.NodeIdentity.NodeUID {
		return status, false, nil
	}
	status.ObservedRevision = node.Annotations[managedprotocol.AnnotationWorkerObservedRevision]
	if !deploymentRolloutComplete(&current) {
		return status, false, nil
	}

	var pods corev1.PodList
	if err := reader.List(ctx, &pods,
		client.InNamespace(current.Namespace),
		client.MatchingLabels(perDeviceDeploymentLabels(device.Name)),
	); err != nil {
		return nil, false, fmt.Errorf("list managed worker Pods for revision proof: %w", err)
	}
	var readyPod *corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		owned, err := podOwnedByDeployment(ctx, reader, pod, &current)
		if err != nil {
			return nil, false, err
		}
		if !owned {
			continue
		}
		// A terminating predecessor can still execute with the same worker
		// ServiceAccount until its process exits. Do not authenticate the new
		// revision while any Pod owned by this Deployment remains terminating.
		if pod.DeletionTimestamp != nil {
			return status, false, nil
		}
		if readyPod != nil {
			return status, false, nil
		}
		readyPod = pod
	}
	if readyPod == nil || readyPod.UID == "" || readyPod.Status.StartTime == nil ||
		readyPod.Annotations[managedprotocol.AnnotationWorkerConfigRevision] != desiredRevision ||
		!podConditionTrue(readyPod, corev1.PodReady) {
		return status, false, nil
	}
	status.PodUID = string(readyPod.UID)
	status.PodStartTime = readyPod.Status.StartTime.DeepCopy()

	for i := range node.Status.Conditions {
		condition := &node.Status.Conditions[i]
		if condition.Type != corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition) {
			continue
		}
		if condition.Status != corev1.ConditionTrue ||
			condition.Reason != managedprotocol.ManagedWorkerReadyReason ||
			status.ObservedRevision != desiredRevision ||
			condition.LastHeartbeatTime.IsZero() ||
			condition.LastHeartbeatTime.Time.Before(readyPod.Status.StartTime.Time) {
			return status, false, nil
		}
		heartbeat := condition.LastHeartbeatTime.DeepCopy()
		status.ReadyHeartbeatTime = heartbeat
		return status, true, nil
	}
	return status, false, nil
}

func podOwnedByDeployment(
	ctx context.Context,
	reader client.Reader,
	pod *corev1.Pod,
	deployment *appsv1.Deployment,
) (bool, error) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != appsv1.SchemeGroupVersion.String() || owner.Kind != "ReplicaSet" || owner.UID == "" {
		return false, nil
	}
	var replicaSet appsv1.ReplicaSet
	if err := reader.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, &replicaSet); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read managed worker ReplicaSet ownership: %w", err)
	}
	if replicaSet.UID != owner.UID {
		return false, nil
	}
	deploymentOwner := metav1.GetControllerOf(&replicaSet)
	return deploymentOwner != nil && deploymentOwner.APIVersion == appsv1.SchemeGroupVersion.String() &&
		deploymentOwner.Kind == "Deployment" && deploymentOwner.Name == deployment.Name &&
		deploymentOwner.UID == deployment.UID, nil
}

func podConditionTrue(pod *corev1.Pod, conditionType corev1.PodConditionType) bool {
	for i := range pod.Status.Conditions {
		if pod.Status.Conditions[i].Type == conditionType {
			return pod.Status.Conditions[i].Status == corev1.ConditionTrue
		}
	}
	return false
}

func (r *CiscoDeviceReconciler) applyCiscoDeviceConditionObserved(
	device *ciskov1.CiscoDevice,
	condition metav1.Condition,
) error {
	existing := meta.FindStatusCondition(device.Status.Conditions, condition.Type)
	if condition.LastTransitionTime.IsZero() {
		if existing != nil && existing.Status == condition.Status {
			condition.LastTransitionTime = existing.LastTransitionTime
		} else {
			condition.LastTransitionTime = metav1.NewTime(r.now())
		}
	}
	meta.SetStatusCondition(&device.Status.Conditions, condition)
	if device.Status.HealthObservation == nil {
		return nil
	}
	changed := observeManagedDeviceConditions(device, r.now(), condition.Type)
	conditionsHash, err := deviceConditionsHash(device)
	if err != nil {
		return fmt.Errorf("hash observed CiscoDevice condition: %w", err)
	}
	if device.Status.HealthObservation.DeviceConditionsHash != conditionsHash {
		device.Status.HealthObservation.DeviceConditionsHash = conditionsHash
		changed = true
	}
	if changed {
		device.Status.HealthObservation.ObservedAt = metav1.NewTime(r.now())
	}
	return nil
}

func (r *CiscoDeviceReconciler) setCiscoDeviceConditionObserved(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	condition metav1.Condition,
) error {
	before := device.DeepCopy()
	if err := r.applyCiscoDeviceConditionObserved(device, condition); err != nil {
		return err
	}
	if statusesEqual(before.Status, device.Status) {
		return nil
	}
	if err := r.Status().Update(ctx, device); err != nil {
		return fmt.Errorf("failed to update CiscoDevice condition %s and its observation: %w", condition.Type, err)
	}
	return nil
}

func gnoiTLSSecretRef(spec *ciskov1.DeviceSpec) *ciskov1.GNOITLSSecretReference {
	if spec == nil || spec.GNOI == nil || spec.GNOI.TLS == nil {
		return nil
	}
	return spec.GNOI.TLS.SecretRef
}

// inspectGNOITLSSecret validates the fixed Secret contract without persisting,
// copying, or logging its contents outside the Kubernetes client cache. The
// worker receives only a read-only projection, and Secret resourceVersion
// drives a controlled restart on rotation because tls.Config is intentionally
// immutable after startup.
func (r *CiscoDeviceReconciler) inspectGNOITLSSecret(ctx context.Context, device *ciskov1.CiscoDevice) (gnoiTLSProjectionState, error) {
	ref := gnoiTLSSecretRef(&device.Spec)
	if ref == nil || ref.Name == "" {
		return gnoiTLSProjectionState{}, nil
	}
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: device.Namespace, Name: ref.Name}
	if err := r.reader().Get(ctx, key, &secret); err != nil {
		if errors.IsNotFound(err) {
			return gnoiTLSProjectionState{}, fmt.Errorf("gNOI TLS Secret %s/%s was not found", key.Namespace, key.Name)
		}
		return gnoiTLSProjectionState{}, &gnoiSecretReadError{fmt.Errorf("read gNOI TLS Secret %s/%s: %w", key.Namespace, key.Name, err)}
	}
	caPEM := secret.Data["ca.crt"]
	if len(caPEM) == 0 {
		return gnoiTLSProjectionState{}, fmt.Errorf("gNOI TLS Secret %s/%s requires non-empty key ca.crt", key.Namespace, key.Name)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return gnoiTLSProjectionState{}, fmt.Errorf("gNOI TLS Secret %s/%s key ca.crt contains no parseable certificates", key.Namespace, key.Name)
	}
	certPEM := secret.Data["tls.crt"]
	keyPEM := secret.Data["tls.key"]
	hasCert := len(certPEM) > 0
	hasKey := len(keyPEM) > 0
	if hasCert != hasKey {
		return gnoiTLSProjectionState{}, fmt.Errorf("gNOI TLS Secret %s/%s keys tls.crt and tls.key must be configured together", key.Namespace, key.Name)
	}
	if hasCert {
		if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
			return gnoiTLSProjectionState{}, fmt.Errorf("gNOI TLS Secret %s/%s has an invalid client certificate pair: %w", key.Namespace, key.Name, err)
		}
	}
	return gnoiTLSProjectionState{
		enabled:           true,
		secretName:        ref.Name,
		resourceVersion:   secret.ResourceVersion,
		clientCertificate: hasCert,
	}, nil
}

func xeGNOICertificateProvisioning(spec *ciskov1.DeviceSpec) *ciskov1.XEGNOICertificateProvisioning {
	if spec == nil || spec.Driver != ciskov1.DeviceDriverXE || spec.XE == nil || spec.XE.GNOI == nil {
		return nil
	}
	return spec.XE.GNOI.CertificateProvisioning
}

func writeClassGNOIEnabled() bool {
	value := strings.TrimSpace(os.Getenv(envCVKEnableWriteClassGNOI))
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

func softwareUpgradeEnabled() bool {
	value := strings.TrimSpace(os.Getenv(envCVKEnableSoftwareUpgrade))
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

func gNOIDisabled() bool {
	value := strings.TrimSpace(os.Getenv(envCVKGNOIDisabled))
	return value == "1" || strings.EqualFold(value, "true")
}

func deploymentRolloutComplete(deployment *appsv1.Deployment) bool {
	if deployment == nil || deployment.Generation <= 0 ||
		deployment.Status.ObservedGeneration < deployment.Generation {
		return false
	}
	desiredReplicas := int32(1)
	if deployment.Spec.Replicas != nil {
		desiredReplicas = *deployment.Spec.Replicas
	}
	if deployment.Status.TerminatingReplicas != nil && *deployment.Status.TerminatingReplicas > 0 {
		return false
	}
	return deployment.Status.UpdatedReplicas == desiredReplicas &&
		deployment.Status.Replicas == desiredReplicas &&
		deployment.Status.ReadyReplicas == desiredReplicas &&
		deployment.Status.AvailableReplicas == desiredReplicas &&
		deployment.Status.UnavailableReplicas == 0
}

// Detect enabled workers created before the lifecycle annotation existed.
// The manager writes these gate values directly into the cisco-vk container.
func podTemplateEnablesGNOIMutations(spec *corev1.PodSpec) bool {
	for _, container := range spec.Containers {
		if container.Name != "cisco-vk" {
			continue
		}
		enabled, disabled := false, false
		for _, env := range container.Env {
			value, _ := strconv.ParseBool(strings.TrimSpace(env.Value))
			switch env.Name {
			case envCVKEnableSoftwareUpgrade, envCVKEnableWriteClassGNOI:
				enabled = enabled || value || env.ValueFrom != nil
			case envCVKGNOIDisabled:
				disabled = value
			}
		}
		return enabled && !disabled
	}
	return false
}

func podTemplateProjectsGNOIPrivateKey(spec *corev1.PodSpec) bool {
	if spec == nil {
		return false
	}
	for _, volume := range spec.Volumes {
		if volume.Name != gnoiProvisioningVolumeName {
			continue
		}
		// Early versions of the provisioning feature used a direct Secret
		// volume. Preserve migration safety for already-created Deployments:
		// an empty item list projects every key and must be treated as if signer
		// material may be resident.
		if volume.Secret != nil && projectsGNOIPrivateKey(volume.Secret.Items) {
			return true
		}
		if volume.Projected == nil {
			continue
		}
		for _, source := range volume.Projected.Sources {
			if source.Secret != nil && projectsGNOIPrivateKey(source.Secret.Items) {
				return true
			}
		}
	}
	return false
}

func projectsGNOIPrivateKey(items []corev1.KeyToPath) bool {
	if len(items) == 0 {
		return true
	}
	for _, item := range items {
		if item.Key == "ca.key" || item.Key == "tls.key" {
			return true
		}
	}
	return false
}

// lookupCredentialResourceVersion returns the referenced Secret's
// resourceVersion for use as a pod-template rollout annotation. Reconciliation
// does not inspect Secret data, but the typed object returned by the API/cache
// contains it; manager Secret RBAC and memory remain in the trust boundary.
func (r *CiscoDeviceReconciler) lookupCredentialResourceVersion(ctx context.Context, device *ciskov1.CiscoDevice) (string, error) {
	if device.Spec.CredentialSecretRef == nil {
		return "", nil
	}
	return r.lookupSecretResourceVersion(ctx, device.Namespace, device.Spec.CredentialSecretRef.Name)
}

func (r *CiscoDeviceReconciler) lookupSecretResourceVersion(ctx context.Context, namespace, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	var sec corev1.Secret
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &sec); err != nil {
		return "", fmt.Errorf("read credential Secret %s/%s: %w", namespace, name, err)
	}
	return sec.ResourceVersion, nil
}

// gnoiProvisioningSecretState validates the same public bundle, optional
// bootstrap pin, and gated signer that the IOS-XE worker will load. Reusing the
// driver validator prevents a bad Secret rotation from enabling gNOI with
// invalid material. Key bytes never leave the client cache
// through Deployment, ConfigMap, annotations, status, events, or logs.
func (r *CiscoDeviceReconciler) gnoiProvisioningSecretState(
	ctx context.Context,
	namespace, name, certificateID, expectedServerName string,
	validateSigner bool,
) (resourceVersion string, signerAvailable bool, err error) {
	if name == "" {
		return "", false, fmt.Errorf("gNOI provisioning Secret name is empty")
	}
	var secret corev1.Secret
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
		if errors.IsNotFound(err) {
			return "", false, fmt.Errorf("gNOI provisioning Secret %s/%s was not found", namespace, name)
		}
		return "", false, &gnoiSecretReadError{fmt.Errorf("read gNOI provisioning Secret %s/%s: %w", namespace, name, err)}
	}
	for _, requiredKey := range []string{"tls.crt", "ca.crt"} {
		if len(secret.Data[requiredKey]) == 0 {
			return "", false, fmt.Errorf("gNOI provisioning Secret %s/%s requires non-empty key %s", namespace, name, requiredKey)
		}
	}
	bundle, err := iosxegnoi.NewProvisioningBundle(
		certificateID,
		expectedServerName,
		secret.Data["tls.crt"],
		secret.Data["ca.crt"],
	)
	if err != nil {
		return "", false, fmt.Errorf("gNOI provisioning Secret %s/%s public material is invalid: %w", namespace, name, err)
	}
	caKey := secret.Data["ca.key"]
	if !validateSigner || len(caKey) == 0 {
		return secret.ResourceVersion, false, nil
	}
	if err := bundle.ConfigureClientTLS(
		&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: x509.NewCertPool()},
		secret.Data["bootstrap.crt"],
	); err != nil {
		return "", false, fmt.Errorf("gNOI provisioning Secret %s/%s bootstrap material is invalid: %w", namespace, name, err)
	}
	if _, err := iosxegnoi.NewLocalCertificateSigner(bundle, caKey); err != nil {
		return "", false, fmt.Errorf("gNOI provisioning Secret %s/%s signer is invalid: %w", namespace, name, err)
	}
	return secret.ResourceVersion, true, nil
}

// updateStatus patches the CiscoDevice status based on the Deployment state.
func (r *CiscoDeviceReconciler) updateStatus(ctx context.Context, device *ciskov1.CiscoDevice, deploy *appsv1.Deployment) error {
	var phase string
	topology := ciskov1.WorkerTopologyPerDevice
	if deploy == nil {
		phase = "Ready"
		if r.AggregatorEnabled && drivers.ConfigDriverRegistered(device.Spec.Driver) {
			topology = ciskov1.WorkerTopologyAggregated
		} else {
			topology = ciskov1.WorkerTopologyNone
		}
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

	capabilities := platforms.WorkerCapabilityStatuses(device.Spec.Driver, topology)
	var netAsCode *ciskov1.NetAsCodeModelStatus
	if descriptor, ok := platforms.ForDriver(device.Spec.Driver); ok {
		model := descriptor.NetAsCode
		netAsCode = &model
	}

	if device.Status.Phase != phase ||
		device.Status.WorkerTopology != topology ||
		!reflect.DeepEqual(device.Status.WorkerCapabilities, capabilities) ||
		!reflect.DeepEqual(device.Status.NetAsCode, netAsCode) {
		device.Status.Phase = phase
		device.Status.WorkerTopology = topology
		device.Status.WorkerCapabilities = capabilities
		device.Status.NetAsCode = netAsCode
		if err := r.Status().Update(ctx, device); err != nil {
			return fmt.Errorf("failed to update CiscoDevice status: %w", err)
		}
	}
	return nil
}
