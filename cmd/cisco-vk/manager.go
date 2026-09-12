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

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	controllermetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/aggregator"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

var (
	metricsAddr                     string
	enableLeaderElect               bool
	probeAddr                       string
	vkImage                         string
	controllerWorkerImage           string
	controllerWorkerImagePullPolicy string
	vkServiceAccount                string
	enableAggregator                bool
	controllerInfoLogRateLimit      int
	enableManagedTopology           bool
	topologyPolicyNamespace         string
	topologyPolicyName              string
)

var managerCmd = &cobra.Command{
	Use:   "manager",
	Short: "Start the CRD controller manager",
	Long: `Start the Kubernetes controller manager that watches CiscoDevice and
NetworkController custom resources and manages their isolated deployments.`,
	RunE: runManager,
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ciskov1.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
	utilruntime.Must(opsv1alpha1.AddToScheme(scheme))

	managerCmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the metric endpoint binds to.")
	managerCmd.Flags().StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"The address the probe endpoint binds to.")
	managerCmd.Flags().BoolVar(&enableLeaderElect, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	managerCmd.Flags().StringVar(&vkImage, "vk-image", controller.DefaultImage,
		"Container image to use for per-device Virtual Kubelet deployments.")
	managerCmd.Flags().StringVar(&controllerWorkerImage, "controller-worker-image", controller.DefaultImage,
		"Adapter-bearing controller image to use for isolated network-controller workers.")
	managerCmd.Flags().StringVar(&controllerWorkerImagePullPolicy, "controller-worker-image-pull-policy", string(corev1.PullIfNotPresent),
		"Image pull policy for isolated network-controller workers (Always, IfNotPresent, or Never).")
	managerCmd.Flags().StringVar(&vkServiceAccount, "vk-service-account", controller.DefaultServiceAccount,
		"Service account name for Virtual Kubelet pods.")
	managerCmd.Flags().BoolVar(&enableAggregator, "enable-config-aggregator", false,
		"Run an in-process per-device ConfigReconciler instead of "+
			"spawning one cisco-vk pod per CiscoDevice. Trades the "+
			"per-pod isolation for one /metrics + one log stream + "+
			"lower per-fleet overhead. The cisco-vk pod-spawning "+
			"flow continues to operate alongside this for non-IOSXE devices.")
	managerCmd.Flags().BoolVar(&enableManagedTopology, "enable-managed-topology", false,
		"Enable manager-owned Node identity/topology and topology-aware rollout safety. Requires installed native admission policies.")
	managerCmd.Flags().StringVar(&topologyPolicyNamespace, "topology-policy-namespace", "",
		"Namespace containing the administrator topology policy ConfigMap (required with --enable-managed-topology).")
	managerCmd.Flags().StringVar(&topologyPolicyName, "topology-policy-name", "",
		"Name of the administrator topology policy ConfigMap (required with --enable-managed-topology).")
	managerCmd.Flags().StringVar(&logLevel, "log-level", "",
		"log level: debug, info, warn, error (default: $LOG_LEVEL or info)")
	managerCmd.Flags().IntVar(&controllerInfoLogRateLimit, "controller-info-log-rate-limit", 100,
		"Maximum controller INFO log records per second exported to OpenTelemetry.")
}

func runManager(cmd *cobra.Command, args []string) error {
	switch corev1.PullPolicy(controllerWorkerImagePullPolicy) {
	case corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever:
	default:
		return fmt.Errorf("invalid --controller-worker-image-pull-policy %q", controllerWorkerImagePullPolicy)
	}
	if err := validateManagedTopologyManagerOptions(
		enableManagedTopology,
		enableLeaderElect,
		topologyPolicyNamespace,
		topologyPolicyName,
	); err != nil {
		return err
	}
	controllerDebug, err := controllerDebugLogging()
	if err != nil {
		return err
	}
	rolloutControllerEnabled := managedRolloutControllerEnabled(enableManagedTopology)

	controllerProviders, controllerShutdown, err := buildControllerProviders(context.Background())
	controllerHandler, controllerLogsToStderr := newControllerSlogHandler(telemetryLoggerProvider(controllerProviders))
	ctrl.SetLogger(newControllerRuntimeLogger(controllerHandler, controllerInfoLogRateLimit, controllerDebug))
	setupLog = ctrl.Log.WithName("setup")
	if controllerLogsToStderr {
		reason := "OTLP endpoint unset"
		if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
			reason = "OTel provider build failed"
		}
		slog.New(controllerHandler).Warn(fmt.Sprintf("controller logs going to stderr only — %s", reason))
	}

	cfg := ctrl.GetConfigOrDie()
	signalCtx := ctrl.SetupSignalHandler()

	missingRequired, crdErr := missingRequiredCRDs(cfg)
	if crdErr != nil {
		setupLog.Error(crdErr, "required CRD preflight failed")
		os.Exit(1)
	}
	blockingCRDs, missingControllerCRDs := partitionMissingRequiredCRDs(missingRequired)
	if rolloutControllerEnabled {
		missingTopologyCRDs, err := missingCRDs(cfg, managedTopologyCRDs)
		if err != nil {
			return fmt.Errorf("managed topology CRD preflight: %w", err)
		}
		blockingCRDs = append(blockingCRDs, missingTopologyCRDs...)
	}
	if len(blockingCRDs) > 0 {
		for _, name := range blockingCRDs {
			setupLog.Error(nil, fmt.Sprintf("required CRD %s not present — apply charts/cisco-virtual-kubelet/crds/ before starting cisco-vk", name))
		}
		os.Exit(1)
	}
	controllerFoundationAvailable := len(missingControllerCRDs) == 0
	if !controllerFoundationAvailable {
		for _, name := range missingControllerCRDs {
			setupLog.Info("network-controller CRD not present; preserving existing manager reconcilers and disabling the network-controller scaffold for this process", "crd", name)
		}
		setupLog.Info("apply charts/cisco-virtual-kubelet/crds/ and restart this Deployment to enable NetworkController reconciliation")
	}

	if err != nil {
		setupLog.Error(err, "controller OTel providers unavailable; continuing with global no-op provider")
	} else if controllerProviders != nil {
		if controllerProviders.Tracer != nil {
			otel.SetTracerProvider(controllerProviders.Tracer)
		}
		if controllerProviders.Meter != nil {
			otel.SetMeterProvider(controllerProviders.Meter)
		}
	}
	if controllerShutdown != nil {
		go func() {
			<-signalCtx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := controllerShutdown(shutdownCtx); err != nil {
				setupLog.Error(err, "controller OTel providers shutdown error")
			}
		}()
	}

	// Pre-flight CRD field-drift check. Helm doesn't upgrade CRDs
	// across releases (only first-install), so on a stale cluster
	// the manager would happily start and the per-pod kubelet's
	// reconcile would later fail with `unknown field` errors. The
	// check below queries the API server's discovery for known
	// IOSXEConfig fields and logs a prominent WARNING when any
	// field this binary expects is missing. Non-fatal — the
	// manager still starts; the warning is enough to point
	// operators at `kubectl apply -f charts/.../crds/`.
	if drift := checkCRDFieldDrift(cfg); drift != "" {
		setupLog.Info("══════════════════════════════════════════════════════════════════")
		setupLog.Info("CRD FIELD DRIFT DETECTED — apply the chart's CRDs to fix")
		setupLog.Info(drift)
		setupLog.Info("Run: kubectl apply -f charts/cisco-virtual-kubelet/crds/")
		setupLog.Info("══════════════════════════════════════════════════════════════════")
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		LeaderElection:         enableLeaderElect,
		LeaderElectionID:       "ciscodevice.cisco.vk",
		HealthProbeBindAddress: probeAddr,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	retirementMode := false
	if !enableManagedTopology {
		retirementMode, err = managedTopologyStatePresent(signalCtx, mgr.GetAPIReader())
		if err != nil {
			return fmt.Errorf("detect retained managed topology state: %w", err)
		}
		if retirementMode {
			if !enableLeaderElect {
				return fmt.Errorf("retained managed topology state requires --leader-elect during reverse writer handoff")
			}
			if topologyPolicyNamespace == "" || topologyPolicyName == "" {
				return fmt.Errorf("retained managed topology state requires --topology-policy-namespace and --topology-policy-name until every handoff completes")
			}
		}
	}
	if enableManagedTopology || retirementMode {
		topology.RegisterMetrics(controllermetrics.Registry)
		policyKey := types.NamespacedName{Namespace: topologyPolicyNamespace, Name: topologyPolicyName}
		policyInput, err := topologyrollout.ReadAdminPolicyPreflight(signalCtx, mgr.GetAPIReader(), policyKey)
		if err != nil {
			return fmt.Errorf("managed topology policy read-only preflight: %w", err)
		}
		// Admission is the ownership boundary for the policy and ledger. Prove
		// that boundary before BootstrapAdminPolicy is allowed to initialize or
		// bind either ConfigMap.
		if err := verifyManagedAdmissionContract(
			signalCtx,
			cfg,
			policyInput.AdmissionPrefix,
			policyKey,
			policyInput.Config.LedgerName,
		); err != nil {
			return fmt.Errorf("managed topology native admission preflight: %w", err)
		}
		if enableManagedTopology {
			if _, err := topologyrollout.BootstrapAdminPolicy(
				signalCtx,
				mgr.GetClient(),
				mgr.GetAPIReader(),
				policyKey,
			); err != nil {
				return fmt.Errorf("managed topology policy preflight: %w", err)
			}
		} else {
			// Retirement is read-only with respect to fleet authority. A missing,
			// unbound, or recreated ledger must block release rather than be
			// initialized by a controller whose managed feature is disabled.
			var policyCM corev1.ConfigMap
			if err := mgr.GetAPIReader().Get(signalCtx, policyKey, &policyCM); err != nil {
				return fmt.Errorf("managed topology retirement policy: %w", err)
			}
			policy, err := topologyrollout.ParseAdminPolicy(&policyCM)
			if err != nil {
				return fmt.Errorf("managed topology retirement policy: %w", err)
			}
			if _, _, err := (topologyrollout.Store{
				Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
				Key:         types.NamespacedName{Namespace: policy.Namespace, Name: policy.Config.LedgerName},
				ExpectedUID: types.UID(policy.LedgerUID),
			}).Read(signalCtx); err != nil {
				return fmt.Errorf("managed topology retirement ledger: %w", err)
			}
		}
	}

	if err = (&controller.CiscoDeviceReconciler{
		Client:                  mgr.GetClient(),
		APIReader:               mgr.GetAPIReader(),
		Scheme:                  mgr.GetScheme(),
		Image:                   vkImage,
		ServiceAccount:          vkServiceAccount,
		AggregatorEnabled:       enableAggregator,
		ManagedTopology:         enableManagedTopology,
		TopologyPolicyNamespace: topologyPolicyNamespace,
		TopologyPolicyName:      topologyPolicyName,
		LeaseNamespace:          os.Getenv("CONFIG_LEASE_NAMESPACE"),
		Recorder:                mgr.GetEventRecorderFor("ciscodevice-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CiscoDevice")
		os.Exit(1)
	}
	if rolloutControllerEnabled {
		topologyrollout.RegisterMetrics(controllermetrics.Registry)
		if err = (&controller.IOSXESoftwareRolloutReconciler{
			Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Scheme: mgr.GetScheme(),
			TopologyPolicyNamespace: topologyPolicyNamespace,
			TopologyPolicyName:      topologyPolicyName,
			Recorder:                mgr.GetEventRecorderFor("iosxe-software-rollout-controller"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "IOSXESoftwareRollout")
			os.Exit(1)
		}
	} else if enableManagedTopology {
		setupLog.Info("managed topology scheduling is enabled; IOS XE rollout orchestration is disabled because software-upgrade mutation is not enabled")
	}

	if controllerFoundationAvailable {
		if err = (&controller.NetworkControllerReconciler{
			Client:          mgr.GetClient(),
			Image:           controllerWorkerImage,
			ImagePullPolicy: corev1.PullPolicy(controllerWorkerImagePullPolicy),
			Recorder:        mgr.GetEventRecorderFor("networkcontroller-controller"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "NetworkController")
			os.Exit(1)
		}
	}

	if err = (&controller.IOSXEConfigBundleReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "IOSXEConfigBundle")
		os.Exit(1)
	}

	if enableAggregator {
		// The aggregator is platform-agnostic post-Phase-9: it
		// pulls the per-device ConfigDriverContext (transport,
		// key rules, writer lookup, subscribe paths) from the
		// platform registry. New platforms become aggregator-
		// addressable by registering — no edit here.
		if err = (&aggregator.AggregatedReconciler{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			Recorder:       mgr.GetEventRecorderFor("config-aggregator"),
			LeaseNamespace: os.Getenv("CONFIG_LEASE_NAMESPACE"),
		}).SetupWithManager(signalCtx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ConfigAggregator")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(signalCtx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

	return nil
}

// managedRolloutControllerEnabled keeps topology projection useful for ordinary
// scheduling without granting rollout mutation authority unless the existing
// IOS XE software-upgrade gate is also explicitly enabled. The global gNOI
// kill switch always wins.
func managedRolloutControllerEnabled(managedTopology bool) bool {
	return managedTopology && envEnabled(envEnableIOSXESoftwareUpgrade) && !envEnabled(gNOIDisabledEnv)
}

func validateManagedTopologyManagerOptions(managedTopology, leaderElection bool, policyNamespace, policyName string) error {
	if !managedTopology {
		return nil
	}
	if !leaderElection {
		return fmt.Errorf("--enable-managed-topology requires --leader-elect to prevent overlapping rollout reconcilers during controller replacement; the reservation ledger remains the safety fence")
	}
	if policyNamespace == "" || policyName == "" {
		return fmt.Errorf("--topology-policy-namespace and --topology-policy-name are required with --enable-managed-topology")
	}
	return nil
}

// managedTopologyStatePresent detects every durable or API-authority remnant
// that still depends on the retained native admission contract. In particular,
// completed isolated legacy workers continue to need Node/Pod admission because
// Kubernetes RBAC cannot scope their cluster role to one Node by resourceName.
func managedTopologyStatePresent(ctx context.Context, reader client.Reader) (bool, error) {
	if reader == nil {
		return false, fmt.Errorf("API reader is nil")
	}
	var devices ciskov1.CiscoDeviceList
	if err := reader.List(ctx, &devices); err != nil {
		return false, fmt.Errorf("list CiscoDevices: %w", err)
	}
	for i := range devices.Items {
		device := &devices.Items[i]
		if device.Status.NodeIdentity != nil || device.Status.LegacyHandoff != nil ||
			device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker] != "" {
			return true, nil
		}
	}

	// An annotation on a namespaced ServiceAccount is not proof of topology
	// authority: a tenant able to create ServiceAccounts could otherwise force
	// every default-off manager restart into retirement mode. The cluster-wide
	// binding is the actual authority which depends on native admission, and it
	// is itself cluster-admin/manager controlled. Detect only an exact generated
	// identity/role/subject shape here; a harmless orphan ServiceAccount can be
	// garbage-collected without retaining the admission contract.
	var clusterRoleBindings rbacv1.ClusterRoleBindingList
	if err := reader.List(ctx, &clusterRoleBindings); err != nil {
		return false, fmt.Errorf("list ClusterRoleBindings: %w", err)
	}
	for i := range clusterRoleBindings.Items {
		binding := &clusterRoleBindings.Items[i]
		annotations := binding.Annotations
		managed := annotations[managedprotocol.AnnotationManaged] == "true" &&
			binding.RoleRef.Name == managedprotocol.ManagedWorkerClusterRole
		legacy := annotations[managedprotocol.AnnotationWorkerMode] == managedprotocol.WorkerModeLegacy &&
			binding.RoleRef.Name == "cisco-virtual-kubelet"
		if (!managed && !legacy) || binding.RoleRef.APIGroup != rbacv1.GroupName || binding.RoleRef.Kind != "ClusterRole" ||
			annotations[managedprotocol.AnnotationWorkerProtocol] != managedprotocol.Version ||
			annotations[managedprotocol.AnnotationDeviceNamespace] == "" || annotations[managedprotocol.AnnotationDeviceName] == "" ||
			annotations[managedprotocol.AnnotationDeviceUID] == "" || len(binding.Subjects) != 1 {
			continue
		}
		subject := binding.Subjects[0]
		if subject.Kind == rbacv1.ServiceAccountKind && subject.APIGroup == "" &&
			subject.Namespace == annotations[managedprotocol.AnnotationDeviceNamespace] && subject.Name != "" {
			return true, nil
		}
	}

	var nodes corev1.NodeList
	if err := reader.List(ctx, &nodes); err != nil {
		return false, fmt.Errorf("list Nodes: %w", err)
	}
	for i := range nodes.Items {
		annotations := nodes.Items[i].Annotations
		if annotations[managedprotocol.AnnotationManaged] == "true" ||
			annotations[managedprotocol.AnnotationLegacyHandoff] != "" {
			return true, nil
		}
	}
	return false, nil
}
