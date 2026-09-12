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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	telemetrystate "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	logruslib "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"go.opentelemetry.io/otel"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
)

// Interface Guards
var _ nodeutil.Provider = (*provider.AppHostingProvider)(nil)
var _ node.NodeProvider = (*provider.AppHostingNode)(nil)

var (
	cfgFile     string
	kubeconfig  string
	logLevel    string
	nodeName    string
	tlsCertFile string
	tlsKeyFile  string

	enableWriteClassGNOI       bool
	enableIOSXESoftwareUpgrade bool
)

const (
	envEnableWriteClassGNOI       = "CISCO_VK_ENABLE_WRITE_CLASS_GNOI"
	envEnableIOSXESoftwareUpgrade = "CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE"

	envDeviceNamespace = managedprotocol.EnvDeviceNamespace
	envDeviceName      = managedprotocol.EnvDeviceName
	envDeviceUID       = managedprotocol.EnvDeviceUID
	envNodeName        = managedprotocol.EnvNodeName
	envManagedTopology = managedprotocol.EnvManagedTopology
	envWorkerRevision  = managedprotocol.EnvWorkerRevision
	legacyEnvNodeName  = "VKUBELET_NODE_NAME"
)

// workerRuntimeIdentity keeps namespaced CiscoDevice identity separate from
// the cluster-scoped Kubernetes Node represented by this worker. DeviceUID and
// ManagedTopology are intentionally carried to the maintenance construction
// seam for the later manager request/acknowledgement protocol.
type workerRuntimeIdentity struct {
	DeviceNamespace string
	DeviceName      string
	DeviceUID       string
	NodeName        string
	ManagedTopology bool
	WorkerRevision  string
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the Virtual Kubelet provider",
	Long: `Start the Cisco Virtual Kubelet provider which registers a virtual
node in Kubernetes and manages pods on Cisco devices via AppHosting.`,
	RunE: runVirtualKubelet,
}

func init() {
	runCmd.Flags().StringVarP(&cfgFile, "config", "c", "",
		"config file (default: /etc/virtual-kubelet/config.yaml)")
	runCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "",
		"path to kubeconfig file (default: $KUBECONFIG or in-cluster)")
	runCmd.Flags().StringVar(&logLevel, "log-level", "",
		"log level: debug, info, warn, error (default: $LOG_LEVEL or info)")
	runCmd.Flags().StringVar(&nodeName, "nodename", "",
		"kubernetes node name (default: $CISCO_VK_NODE_NAME, $VKUBELET_NODE_NAME, device.nodeName, device address, or 'cisco-virtual-kubelet')")
	runCmd.Flags().StringVar(&tlsCertFile, "tls-cert-file", "",
		fmt.Sprintf("path to TLS certificate for the kubelet HTTPS listener (default: %s)", tlsutil.DefaultCertFile))
	runCmd.Flags().StringVar(&tlsKeyFile, "tls-key-file", "",
		fmt.Sprintf("path to TLS private key for the kubelet HTTPS listener (default: %s)", tlsutil.DefaultKeyFile))
	runCmd.Flags().BoolVar(&enableWriteClassGNOI, "enable-write-class-gnoi", false,
		"enable write-class gNOI reconcilers such as IOSXEOperationalAction (default: false)")
	runCmd.Flags().BoolVar(&enableIOSXESoftwareUpgrade, "enable-iosxesoftwareupgrade", false,
		"enable IOSXESoftwareUpgrade gNOI OS upgrade reconciler (default: false)")
}

// validateConfig checks if the config file exists at the given path
func validateConfig(configPath string) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("config file not found: %s\n\nSpecify a config file with --config or -c flag, or create the default config at /etc/virtual-kubelet/config.yaml", configPath)
	}
	return nil
}

// validateLogLevel checks if the provided log level is valid
func validateLogLevel(level string) error {
	switch level {
	case "", "info", "debug", "warn", "warning", "error":
		return nil
	default:
		return fmt.Errorf("invalid log level: %q\n\nValid options are: debug, info, warn, error", level)
	}
}

func flagOrEnvBool(flagValue bool, envName string) bool {
	if flagValue {
		return true
	}
	raw := os.Getenv(envName)
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(raw)
	return err == nil && parsed
}

func resolveWorkerRuntimeIdentity(flagNodeName string, spec *ciskov1.DeviceSpec) (workerRuntimeIdentity, error) {
	if spec == nil {
		return workerRuntimeIdentity{}, fmt.Errorf("resolve worker identity: nil DeviceSpec")
	}

	managed, err := strictOptionalBoolEnv(envManagedTopology)
	if err != nil {
		return workerRuntimeIdentity{}, err
	}
	if managed {
		identity := workerRuntimeIdentity{
			DeviceNamespace: os.Getenv(envDeviceNamespace),
			DeviceName:      os.Getenv(envDeviceName),
			DeviceUID:       os.Getenv(envDeviceUID),
			NodeName:        os.Getenv(envNodeName),
			ManagedTopology: true,
			WorkerRevision:  os.Getenv(envWorkerRevision),
		}
		for _, required := range []struct {
			name  string
			value string
		}{
			{name: envDeviceNamespace, value: identity.DeviceNamespace},
			{name: envDeviceName, value: identity.DeviceName},
			{name: envDeviceUID, value: identity.DeviceUID},
			{name: envNodeName, value: identity.NodeName},
			{name: envWorkerRevision, value: identity.WorkerRevision},
		} {
			if strings.TrimSpace(required.value) == "" {
				return workerRuntimeIdentity{}, fmt.Errorf("managed topology identity requires non-empty %s", required.name)
			}
		}

		// During the compatibility handoff the old flag/env/config inputs may
		// still be present. Accept duplicates only when they prove the same Node
		// binding; never let one silently redirect a managed worker.
		for _, legacy := range []struct {
			name  string
			value string
		}{
			{name: "--nodename", value: flagNodeName},
			{name: legacyEnvNodeName, value: os.Getenv(legacyEnvNodeName)},
			{name: "device.nodeName", value: spec.NodeName},
		} {
			if legacy.value != "" && legacy.value != identity.NodeName {
				return workerRuntimeIdentity{}, fmt.Errorf("managed topology Node identity conflict: %s=%q, %s=%q", legacy.name, legacy.value, envNodeName, identity.NodeName)
			}
		}

		// Existing reconcilers scope their caches through POD_NAMESPACE. Until
		// their options carry DeviceNamespace directly, require the downward-API
		// value to prove that they will watch the bound CiscoDevice namespace.
		podNamespace := os.Getenv("POD_NAMESPACE")
		if podNamespace == "" {
			return workerRuntimeIdentity{}, fmt.Errorf("managed topology identity requires POD_NAMESPACE to match %s", envDeviceNamespace)
		}
		if podNamespace != identity.DeviceNamespace {
			return workerRuntimeIdentity{}, fmt.Errorf("managed topology namespace conflict: POD_NAMESPACE=%q, %s=%q", podNamespace, envDeviceNamespace, identity.DeviceNamespace)
		}

		if err := identity.validate(); err != nil {
			return workerRuntimeIdentity{}, err
		}
		return identity, nil
	}

	resolvedNodeName := flagNodeName
	if resolvedNodeName == "" {
		resolvedNodeName = os.Getenv(envNodeName)
	}
	if resolvedNodeName == "" {
		resolvedNodeName = os.Getenv(legacyEnvNodeName)
	}
	if resolvedNodeName == "" {
		resolvedNodeName = spec.NodeName
	}
	resolvedNodeName = provider.GetNodeName(resolvedNodeName, spec.Address)

	deviceNamespace := os.Getenv(envDeviceNamespace)
	if deviceNamespace == "" {
		deviceNamespace = operationNamespace()
	}
	// startConfigReconciler's namespace is still sourced through
	// operationNamespace. Refuse an explicit split it cannot honor yet.
	if deviceNamespace != operationNamespace() {
		return workerRuntimeIdentity{}, fmt.Errorf("%s=%q does not match the config reconciler namespace %q", envDeviceNamespace, deviceNamespace, operationNamespace())
	}
	deviceName := os.Getenv(envDeviceName)
	if deviceName == "" {
		deviceName = resolvedNodeName
	}
	identity := workerRuntimeIdentity{
		DeviceNamespace: deviceNamespace,
		DeviceName:      deviceName,
		DeviceUID:       os.Getenv(envDeviceUID),
		NodeName:        resolvedNodeName,
	}
	if err := identity.validate(); err != nil {
		return workerRuntimeIdentity{}, err
	}
	return identity, nil
}

func strictOptionalBoolEnv(name string) (bool, error) {
	raw, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return value, nil
}

func (identity workerRuntimeIdentity) validate() error {
	if identity.ManagedTopology && identity.DeviceUID == "" {
		return fmt.Errorf("managed topology identity requires non-empty %s", envDeviceUID)
	}
	if identity.ManagedTopology {
		if !validWorkerRevision(identity.WorkerRevision) {
			return fmt.Errorf("managed topology identity requires %s to be a sha256 revision", envWorkerRevision)
		}
	}
	if problems := utilvalidation.IsDNS1123Label(identity.DeviceNamespace); len(problems) > 0 {
		return fmt.Errorf("invalid CiscoDevice namespace %q: %s", identity.DeviceNamespace, strings.Join(problems, "; "))
	}
	if problems := utilvalidation.IsDNS1123Subdomain(identity.DeviceName); len(problems) > 0 {
		return fmt.Errorf("invalid CiscoDevice name %q: %s", identity.DeviceName, strings.Join(problems, "; "))
	}
	if problems := utilvalidation.IsDNS1123Subdomain(identity.NodeName); len(problems) > 0 {
		return fmt.Errorf("invalid Kubernetes Node name %q: %s", identity.NodeName, strings.Join(problems, "; "))
	}
	if identity.ManagedTopology {
		if problems := utilvalidation.IsValidLabelValue(identity.NodeName); len(problems) > 0 {
			return fmt.Errorf("managed Kubernetes Node name %q cannot be used as its hostname label: %s", identity.NodeName, strings.Join(problems, "; "))
		}
	}
	if identity.DeviceUID != "" {
		if len(identity.DeviceUID) > 128 || strings.TrimSpace(identity.DeviceUID) != identity.DeviceUID || strings.ContainsAny(identity.DeviceUID, " \t\r\n") {
			return fmt.Errorf("invalid CiscoDevice UID %q: must be 1-128 non-whitespace characters", identity.DeviceUID)
		}
	}
	return nil
}

func validWorkerRevision(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func GetKubeConfig(kubeconfigFlag string) (*rest.Config, error) {

	if kubeconfigFlag != "" {
		if _, err := os.Stat(kubeconfigFlag); os.IsNotExist(err) {
			return nil, fmt.Errorf("kubeconfig file not found: %s", kubeconfigFlag)
		}
		return clientcmd.BuildConfigFromFlags("", kubeconfigFlag)
	}

	kubeconfigEnv := os.Getenv("KUBECONFIG")
	if kubeconfigEnv != "" {
		if _, err := os.Stat(kubeconfigEnv); os.IsNotExist(err) {
			return nil, fmt.Errorf("kubeconfig file from KUBECONFIG env not found: %s", kubeconfigEnv)
		}
		return clientcmd.BuildConfigFromFlags("", kubeconfigEnv)
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load in-cluster config and no kubeconfig provided: %w", err)
	}
	return config, nil
}

func runVirtualKubelet(cmd *cobra.Command, args []string) error {
	// Determine config path: flag > default
	configPath := cfgFile
	if configPath == "" {
		configPath = "/etc/virtual-kubelet/config.yaml"
	}

	// Validate config file exists
	if err := validateConfig(configPath); err != nil {
		return err
	}

	// Load config
	appCfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config from %s: %w", configPath, err)
	}

	// Resolve device password from environment variable when the controller
	// injects it via a Kubernetes Secret (VK_DEVICE_PASSWORD). This keeps
	// credentials out of the ConfigMap.
	if envPass := os.Getenv("VK_DEVICE_PASSWORD"); envPass != "" {
		appCfg.Device.Password = envPass
	}

	// Resolve and validate the complete manager/worker binding before opening
	// Kubernetes clients or starting any background work. A managed worker must
	// never begin operating with a partial or ambiguous identity.
	identity, err := resolveWorkerRuntimeIdentity(nodeName, &appCfg.Device)
	if err != nil {
		return fmt.Errorf("resolve worker runtime identity: %w", err)
	}
	projectionMode := topology.ProjectionModeStandaloneCompatibility
	initialNodeSpec := provider.GetInitialNodeSpec(identity.NodeName, &appCfg.Device)
	if identity.ManagedTopology {
		projectionMode = topology.ProjectionModeManaged
		initialNodeSpec, err = provider.GetInitialNodeSpecWithTopologyMode(identity.NodeName, &appCfg.Device, projectionMode)
		if err != nil {
			return fmt.Errorf("build managed Node projection: %w", err)
		}
		// The manager has already reserved and projected this Node. Upstream
		// Virtual Kubelet still needs the resolved name and initial status seed,
		// but passing stable metadata or spec here would make its status patch
		// compete with the manager's ownership contract.
		initialNodeSpec = managedWorkerInitialNode(initialNodeSpec)
	}

	ctx, cancel := context.WithCancel(context.Background()) // ctxlint:allow VK process root
	defer cancel()

	// Setup logging
	logrusLogger := logruslib.New()
	logrusLogger.SetReportCaller(true)
	logrusLogger.SetFormatter(&logruslib.TextFormatter{
		FullTimestamp: true,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			return "", fmt.Sprintf("%s:%d", path.Base(f.File), f.Line)
		},
	})

	// Log level: flag > env > config > default
	lvl := logLevel
	if lvl == "" {
		lvl = os.Getenv("LOG_LEVEL")
	}
	if lvl == "" {
		lvl = appCfg.Device.LogLevel
	}
	if err := validateLogLevel(lvl); err != nil {
		return err
	}
	switch lvl {
	case "", "info":
		logrusLogger.SetLevel(logruslib.InfoLevel)
	case "debug":
		logrusLogger.SetLevel(logruslib.DebugLevel)
	case "warn", "warning":
		logrusLogger.SetLevel(logruslib.WarnLevel)
	case "error":
		logrusLogger.SetLevel(logruslib.ErrorLevel)
	}

	logger := logrus.FromLogrus(logruslib.NewEntry(logrusLogger))
	ctx = log.WithLogger(ctx, logger)

	// Signal handling
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.G(ctx).Info("Received shutdown signal")
		cancel()
	}()

	// Kubeconfig: flag > env > in-cluster
	kubeconfigCfg, err := GetKubeConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(kubeconfigCfg)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	if identity.ManagedTopology {
		preflightCtx, preflightCancel := context.WithTimeout(ctx, 30*time.Second)
		defer preflightCancel()
		if err := verifyManagedWorkerAdmission(preflightCtx, clientset, identity.NodeName); err != nil {
			return fmt.Errorf("managed worker native admission preflight: %w", err)
		}
	}

	certFile := tlsCertFile
	if certFile == "" {
		certFile = tlsutil.DefaultCertFile
	}
	keyFile := tlsKeyFile
	if keyFile == "" {
		keyFile = tlsutil.DefaultKeyFile
	}
	tlsCfg, err := tlsutil.EnsureTLSConfig(certFile, keyFile, tlsutil.DefaultGenCertFile, tlsutil.DefaultGenKeyFile, appCfg.Device.Address)
	if err != nil {
		return fmt.Errorf("failed to configure kubelet TLS: %w", err)
	}

	// innerHandler is set inside newProviderFunc (after the provider is created) and
	// read by the handlerWrapper below. This closure pattern lets us satisfy the
	// NodeConfig.Handler requirement before the provider exists, while still wiring
	// the real mux once the provider is available.
	var innerHandler http.Handler
	mdtStateCache := telemetrystate.NewCache()
	traceCorrelationCache := correlation.NewCache(0, 0, 0)
	var appEventConsumer telemetrystate.AppEventConsumer
	var devicePodLister func(context.Context) ([]*v1.Pod, error)

	handlerWrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if innerHandler != nil {
			innerHandler.ServeHTTP(w, r)
			return
		}
		http.Error(w, "provider not yet initialised", http.StatusServiceUnavailable)
	})

	opts := []nodeutil.NodeOpt{
		nodeutil.WithNodeConfig(nodeutil.NodeConfig{
			Client:         clientset,
			NodeSpec:       initialNodeSpec,
			HTTPListenAddr: ":10250",
			NumWorkers:     5,
			TLSConfig:      tlsCfg,
			Handler:        handlerWrapper,
		}),
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedv1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	eventRecorder := eventBroadcaster.NewRecorder(clientgoscheme.Scheme, v1.EventSource{Component: "cisco-virtual-kubelet"})

	vkProviders, vkShutdown, err := buildVKProviders(ctx, identity.NodeName, &appCfg.Device)
	if err != nil {
		log.G(ctx).WithError(err).Warn("Virtual Kubelet OTel providers unavailable; continuing with global no-op provider")
	}
	if vkProviders != nil {
		if vkProviders.Tracer != nil {
			otel.SetTracerProvider(vkProviders.Tracer)
		}
		if vkProviders.Meter != nil {
			otel.SetMeterProvider(vkProviders.Meter)
		}
	}
	if vkShutdown != nil {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := vkShutdown(shutdownCtx); err != nil {
				log.G(ctx).WithError(err).Warn("Virtual Kubelet OTel providers shutdown error")
			}
		}()
	}

	telemetryProviders, telemetryShutdown, err := buildTelemetryProviders(ctx, identity.DeviceName, configReconcilerOptions{
		Spec: &appCfg.Device,
	})
	if err != nil {
		log.G(ctx).WithError(err).Warn("telemetry OTel providers unavailable; continuing with signal-specific fallbacks")
	}
	if telemetryShutdown != nil {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := telemetryShutdown(shutdownCtx); err != nil {
				log.G(ctx).WithError(err).Warn("telemetry OTel providers shutdown error")
			}
		}()
	}

	maintenanceCoordinator, err := newMaintenanceCoordinator(kubeconfigCfg, identity, configReconcilerOptions{
		Spec:                       &appCfg.Device,
		EnableWriteClassGNOI:       flagOrEnvBool(enableWriteClassGNOI, envEnableWriteClassGNOI),
		EnableIOSXESoftwareUpgrade: flagOrEnvBool(enableIOSXESoftwareUpgrade, envEnableIOSXESoftwareUpgrade),
	})
	if err != nil {
		return fmt.Errorf("configure device maintenance: %w", err)
	}
	if maintenanceCoordinator != nil {
		go maintenanceCoordinator.Run(ctx)
	}

	newProviderFunc := func(vkCfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		// Create a single shared driver for both node and pod handlers
		driverCtx := ctx
		if maintenanceCoordinator != nil {
			driverCtx = devicecoordination.WithMutationGuard(ctx, maintenanceCoordinator.AcquireWrite)
		}
		sharedDriver, err := drivers.NewDriver(driverCtx, &appCfg.Device)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create device driver: %w", err)
		}
		devicePodLister = sharedDriver.ListPods

		nodeHandler := provider.NewAppHostingNodeWithTopologyMode(ctx, identity.NodeName, &appCfg.Device, sharedDriver, projectionMode)
		if identity.ManagedTopology {
			nodeHandler.SetManagedWorkerRevision(identity.WorkerRevision)
		}

		// Start OTEL topology exporter if configured and the driver supports topology
		if appCfg.Device.OTEL != nil && appCfg.Device.OTEL.Enabled && appCfg.Device.OTEL.Endpoint != "" {
			if topo, ok := sharedDriver.(drivers.TopologyProvider); ok {
				otelExporter, otelErr := provider.NewOTELTopologyExporter(ctx, sharedDriver, topo, appCfg.Device.OTEL, identity.DeviceName, appCfg.Device.Address, telemetryTracerProvider(telemetryProviders))
				if otelErr != nil {
					log.G(ctx).WithError(otelErr).Warn("Failed to initialise OTEL topology exporter, continuing without it")
				} else {
					go otelExporter.Run(ctx)
				}
			} else {
				log.G(ctx).Warn("OTEL topology enabled but driver does not support TopologyProvider interface")
			}
		}

		podHandler, err := provider.NewAppHostingProvider(ctx, &appCfg.Device, vkCfg, sharedDriver, nodeHandler, eventRecorder)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialise PodHandler: %w", err)
		}
		podHandler.SetTraceCorrelation(identity.DeviceName, traceCorrelationCache)
		podHandler.SetMaintenance(maintenanceCoordinator)
		appEventConsumer = podHandler

		// Build a custom PodHandlerConfig that only wires supported operations.
		// Unsupported methods are left nil so the VK library returns HTTP 501
		// automatically via its built-in NotImplemented handler, rather than
		// calling through to the provider stub and returning HTTP 500.
		mux := http.NewServeMux()
		mux.Handle("/", api.PodHandler(api.PodHandlerConfig{
			GetPods: podHandler.GetPods,
			GetPodsFromKubernetes: func(ctx context.Context) ([]*v1.Pod, error) {
				return vkCfg.Pods.List(labels.Everything())
			},
			StreamIdleTimeout:     0,
			StreamCreationTimeout: 0,
			// Explicitly nil — library returns HTTP 501 for each of these:
			RunInContainer:     nil,
			AttachToContainer:  nil,
			GetContainerLogs:   nil,
			PortForward:        nil,
			GetStatsSummary:    podHandler.GetStatsSummary,
			GetMetricsResource: podHandler.GetMetricsResource,
		}, true))
		innerHandler = mux

		return podHandler, nodeHandler, nil
	}
	// Recover pods that were marked Failed/NotFound during a previous VK restart.
	// The upstream VK pod controller permanently ignores pods in Failed phase, so
	// we must reset them to Pending before the pod controller starts syncing.
	recoverStaleFailedPods(ctx, clientset, identity.NodeName)

	n, err := nodeutil.NewNode(identity.NodeName, newProviderFunc, opts...)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	// Run a background recovery loop that resets Failed/NotFound pods.
	// Uses exponential backoff: 15s → 30s → 60s → 5min cap. Resets to 15s
	// when a recovery actually occurs.
	go runPodRecoveryLoop(ctx, clientset, identity.NodeName)

	// Start the Phase-0 IOS-XE config reconciler. It watches IOSXEConfig CRs
	// that target this device and drives the (stub) configdriver.Driver.
	// Failure to build the controller-runtime client is not fatal — apphosting
	// continues to work; the operator sees the warning and addresses RBAC.
	//
	// External-review Finding #3 (Wave 1C): when the controller manager
	// is running in aggregator mode, it sets DISABLE_IN_POD_CONFIG_RECONCILER=true
	// on the per-device pod's env. The cisco-vk binary must then NOT
	// start its own in-pod ConfigReconciler — otherwise the aggregator
	// and the in-pod reconciler both write the same (device, family)
	// concurrently, defeating the whole point of single-manager
	// topology and producing a duplicate-writer hazard.
	if v := os.Getenv("DISABLE_IN_POD_CONFIG_RECONCILER"); v == "true" || v == "1" {
		log.G(ctx).Info("DISABLE_IN_POD_CONFIG_RECONCILER set; skipping in-pod ConfigReconciler (aggregator-mode topology)")
	} else if err := startConfigReconciler(ctx, kubeconfigCfg, identity.DeviceName, configReconcilerOptions{
		Spec:                       &appCfg.Device,
		Password:                   appCfg.Device.Password,
		DeviceNamespace:            identity.DeviceNamespace,
		DeviceUID:                  identity.DeviceUID,
		NodeName:                   identity.NodeName,
		ManagedTopology:            identity.ManagedTopology,
		WorkerRevision:             identity.WorkerRevision,
		CredentialSecretRevision:   os.Getenv(managedprotocol.EnvCredentialSecretRevision),
		GNOITLSSecretRevision:      os.Getenv(managedprotocol.EnvGNOITLSSecretRevision),
		GNOIProvisioningRevision:   os.Getenv(managedprotocol.EnvGNOIProvisioningRevision),
		EnableWriteClassGNOI:       flagOrEnvBool(enableWriteClassGNOI, envEnableWriteClassGNOI),
		EnableIOSXESoftwareUpgrade: flagOrEnvBool(enableIOSXESoftwareUpgrade, envEnableIOSXESoftwareUpgrade),
		TelemetryProviders:         telemetryProviders,
		StateCache:                 mdtStateCache,
		AppEventConsumer:           appEventConsumer,
		CorrelationCache:           traceCorrelationCache,
		Maintenance:                maintenanceCoordinator,
		DevicePodLister:            devicePodLister,
	}); err != nil {
		log.G(ctx).WithError(err).Warn("IOSXEConfig reconciler not started; continuing without declarative config")
	}

	// Diagnostic probe for finding #6(a). When CONFIG_NETCONF_PROBE is
	// set, fire a fresh ssh.Dial to the device's NETCONF port every
	// 30 seconds for 15 minutes and log the outcome. The probe runs in
	// parallel with apphosting + VK so an operator can compare the
	// cisco-vk pod's dial behavior to a side-by-side standalone probe
	// pod's behavior at the same wall-clock moment. Cheap, scoped,
	// gated by an explicit env var so production deployments don't pay
	// for it.
	if os.Getenv("CONFIG_NETCONF_PROBE") != "" {
		go runNETCONFProbe(ctx, &appCfg.Device, appCfg.Device.Password)
	}

	if err := n.Run(ctx); err != nil {
		return fmt.Errorf("node run failed: %w", err)
	}

	log.G(ctx).Info("Cisco Virtual Kubelet stopped")
	return nil
}

func managedWorkerInitialNode(node v1.Node) v1.Node {
	node.Labels = nil
	node.Annotations = nil
	node.Spec = v1.NodeSpec{}
	return node
}

// verifyManagedWorkerAdmission proves the native ownership boundary using the
// worker's own credentials. The positive dry run distinguishes a working
// status-only permission from a blanket RBAC denial and covers the two
// bookkeeping annotations upstream Virtual Kubelet writes with Node status.
// The negative dry run must then be rejected specifically when it tries to
// smuggle a manager-owned label through the Node status subresource. Neither
// probe persists data.
func verifyManagedWorkerAdmission(ctx context.Context, clientset kubernetes.Interface, nodeName string) error {
	if clientset == nil || strings.TrimSpace(nodeName) == "" {
		return fmt.Errorf("worker admission probe is missing its client or Node identity")
	}
	nodes := clientset.CoreV1().Nodes()
	positive, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{
			managedprotocol.VirtualKubeletLastAppliedObjectMeta: "{}",
			managedprotocol.VirtualKubeletLastAppliedNodeStatus: "{}",
		}},
		"status": map[string]any{},
	})
	if err != nil {
		return fmt.Errorf("encode positive status probe: %w", err)
	}
	if _, err := nodes.Patch(ctx, nodeName, types.MergePatchType, positive,
		metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}}, "status"); err != nil {
		return fmt.Errorf("legitimate Node status dry run was rejected: %w", err)
	}

	negative, err := json.Marshal(map[string]any{"metadata": map[string]any{"labels": map[string]string{
		topology.CiscoTopologyLabelPrefix + "admission-probe": "must-be-denied",
	}}})
	if err != nil {
		return fmt.Errorf("encode negative ownership probe: %w", err)
	}
	if _, err := nodes.Patch(ctx, nodeName, types.MergePatchType, negative,
		metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}}, "status"); err == nil {
		return fmt.Errorf("unsafe Node label write through the status subresource was accepted")
	} else if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
		return fmt.Errorf("negative Node ownership dry run failed for an unexpected reason: %w", err)
	}
	return nil
}

// runPodRecoveryLoop periodically checks for Failed/NotFound pods and resets
// them to Pending. Uses exponential backoff to reduce API server load once
// all pods are healthy.
func runPodRecoveryLoop(ctx context.Context, clientset kubernetes.Interface, nodeName string) {
	const (
		minInterval = 15 * time.Second
		maxInterval = 5 * time.Minute
	)
	interval := minInterval

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			recovered := recoverStaleFailedPods(ctx, clientset, nodeName)
			if recovered > 0 {
				interval = minInterval // reset on activity
			} else if interval < maxInterval {
				interval = interval * 2
				if interval > maxInterval {
					interval = maxInterval
				}
			}
		}
	}
}

// recoverStaleFailedPods resets pods on our node that are stuck in Failed phase
// with reason NotFound (or ProviderFailed) back to Pending so the VK pod
// controller will pick them up again. Returns the number of pods recovered.
func recoverStaleFailedPods(ctx context.Context, clientset kubernetes.Interface, nodeName string) int {
	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName + ",status.phase=Failed",
	})
	if err != nil {
		// Under H1's per-tenant-namespace RoleBinding the VK SA has no
		// cluster-scope pod list. Cross-namespace stuck-pod recovery is
		// unavailable in that mode; degrade silently so the boundary
		// surfaces only real defects (CI greps for "cannot list").
		if apierrors.IsForbidden(err) {
			log.G(ctx).WithError(err).Debug("stuck-pod recovery skipped: cluster-scope pod list not permitted")
			return 0
		}
		log.G(ctx).WithError(err).Warn("Failed to list failed pods for recovery")
		return 0
	}

	recovered := 0
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Reason != "NotFound" && pod.Status.Reason != "ProviderFailed" && pod.Status.Reason != "PackagePolicyInvalid" {
			continue
		}
		if pod.DeletionTimestamp != nil {
			continue
		}

		log.G(ctx).Infof("Recovering stuck pod %s/%s (reason=%s) → resetting to Pending",
			pod.Namespace, pod.Name, pod.Status.Reason)

		pod.Status.Phase = v1.PodPending
		pod.Status.Reason = ""
		pod.Status.Message = ""

		if _, err := clientset.CoreV1().Pods(pod.Namespace).UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
			log.G(ctx).WithError(err).Warnf("Failed to recover pod %s/%s", pod.Namespace, pod.Name)
		} else {
			recovered++
		}
	}
	return recovered
}
