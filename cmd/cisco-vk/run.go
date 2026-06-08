// Copyright © 2026 Cisco Systems, Inc.
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
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	telemetrystate "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
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
)

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
		"kubernetes node name (default: $VKUBELET_NODE_NAME or 'cisco-virtual-kubelet')")
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

	ctx, cancel := context.WithCancel(context.Background())
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

	// Resolve runtime flags: flag > env > default
	effectiveNodeName := nodeName
	if effectiveNodeName == "" {
		effectiveNodeName = os.Getenv("VKUBELET_NODE_NAME")
	}
	effectiveNodeName = provider.GetNodeName(effectiveNodeName, appCfg.Device.Address)

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

	handlerWrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if innerHandler != nil {
			innerHandler.ServeHTTP(w, r)
			return
		}
		http.Error(w, "provider not yet initialised", http.StatusServiceUnavailable)
	})

	initialNode := provider.GetInitialNodeSpec(effectiveNodeName, appCfg.Device.Address)
	initialNode.Spec.Taints = append(initialNode.Spec.Taints, appCfg.Device.Taints...)

	opts := []nodeutil.NodeOpt{
		nodeutil.WithNodeConfig(nodeutil.NodeConfig{
			Client:         clientset,
			NodeSpec:       initialNode,
			HTTPListenAddr: ":10250",
			NumWorkers:     5,
			TLSConfig:      tlsCfg,
			Handler:        handlerWrapper,
		}),
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedv1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	eventRecorder := eventBroadcaster.NewRecorder(clientgoscheme.Scheme, v1.EventSource{Component: "cisco-virtual-kubelet"})

	vkProviders, vkShutdown, err := buildVKProviders(ctx, effectiveNodeName, &appCfg.Device)
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

	telemetryProviders, telemetryShutdown, err := buildTelemetryProviders(ctx, effectiveNodeName, configReconcilerOptions{
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

	newProviderFunc := func(vkCfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		// Create a single shared driver for both node and pod handlers
		sharedDriver, err := drivers.NewDriver(ctx, &appCfg.Device)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create device driver: %w", err)
		}

		nodeHandler := provider.NewAppHostingNode(ctx, effectiveNodeName, &appCfg.Device, sharedDriver)

		// Start OTEL topology exporter if configured and the driver supports topology
		if appCfg.Device.OTEL != nil && appCfg.Device.OTEL.Enabled && appCfg.Device.OTEL.Endpoint != "" {
			if topo, ok := sharedDriver.(drivers.TopologyProvider); ok {
				otelExporter, otelErr := provider.NewOTELTopologyExporter(ctx, sharedDriver, topo, appCfg.Device.OTEL, effectiveNodeName, appCfg.Device.Address, telemetryTracerProvider(telemetryProviders))
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
		podHandler.SetTraceCorrelation(effectiveNodeName, traceCorrelationCache)
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
	recoverStaleFailedPods(ctx, clientset, effectiveNodeName)

	n, err := nodeutil.NewNode(effectiveNodeName, newProviderFunc, opts...)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	// Run a background recovery loop that resets Failed/NotFound pods.
	// Uses exponential backoff: 15s → 30s → 60s → 5min cap. Resets to 15s
	// when a recovery actually occurs.
	go runPodRecoveryLoop(ctx, clientset, effectiveNodeName)

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
	} else if err := startConfigReconciler(ctx, kubeconfigCfg, effectiveNodeName, configReconcilerOptions{
		Spec:                       &appCfg.Device,
		Password:                   appCfg.Device.Password,
		EnableWriteClassGNOI:       flagOrEnvBool(enableWriteClassGNOI, envEnableWriteClassGNOI),
		EnableIOSXESoftwareUpgrade: flagOrEnvBool(enableIOSXESoftwareUpgrade, envEnableIOSXESoftwareUpgrade),
		TelemetryProviders:         telemetryProviders,
		StateCache:                 mdtStateCache,
		AppEventConsumer:           appEventConsumer,
		CorrelationCache:           traceCorrelationCache,
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
