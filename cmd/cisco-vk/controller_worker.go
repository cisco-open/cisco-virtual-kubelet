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
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	controlleradapter "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

var (
	controllerWorkerConfigPath       string
	controllerWorkerMetricsAddr      string
	controllerWorkerProbeAddr        string
	controllerWorkerNamespace        string
	controllerWorkerName             string
	controllerWorkerUID              string
	controllerWorkerGeneration       int64
	controllerWorkerType             string
	controllerWorkerDescriptorDigest string
)

var controllerWorkerCmd = &cobra.Command{
	Use:   "controller-worker",
	Short: "Run one isolated network-controller adapter",
	Long: `Run the adapter registered for one NetworkController in a
namespace-scoped controller-runtime manager. This internal command is normally
started by the central manager rather than invoked directly.`,
	Args: cobra.NoArgs,
	RunE: runControllerWorker,
}

func init() {
	controllerWorkerCmd.Flags().StringVar(&controllerWorkerConfigPath, "config", "",
		"Path to the projected NetworkController worker bootstrap document.")
	controllerWorkerCmd.Flags().StringVar(&controllerWorkerMetricsAddr, "metrics-bind-address", ":8080",
		"The address the worker metrics endpoint binds to.")
	controllerWorkerCmd.Flags().StringVar(&controllerWorkerProbeAddr, "health-probe-bind-address", ":8081",
		"The address the worker health endpoint binds to.")
	controllerWorkerCmd.Flags().StringVar(&controllerWorkerNamespace, "controller-namespace", "",
		"Immutable namespace identity for the NetworkController worker.")
	controllerWorkerCmd.Flags().StringVar(&controllerWorkerName, "controller-name", "",
		"Immutable name identity for the NetworkController worker.")
	controllerWorkerCmd.Flags().StringVar(&controllerWorkerUID, "controller-uid", "",
		"Immutable Kubernetes UID identity for the NetworkController worker.")
	controllerWorkerCmd.Flags().Int64Var(&controllerWorkerGeneration, "controller-generation", 0,
		"NetworkController generation used to build this worker Pod and its projected volumes.")
	controllerWorkerCmd.Flags().StringVar(&controllerWorkerType, "controller-type", "",
		"Immutable registered adapter type for the NetworkController worker.")
	controllerWorkerCmd.Flags().StringVar(&controllerWorkerDescriptorDigest, "controller-descriptor-digest", "",
		"Expected manager-side adapter descriptor fingerprint.")
	_ = controllerWorkerCmd.MarkFlagRequired("config")
	_ = controllerWorkerCmd.MarkFlagRequired("controller-namespace")
	_ = controllerWorkerCmd.MarkFlagRequired("controller-name")
	_ = controllerWorkerCmd.MarkFlagRequired("controller-uid")
	_ = controllerWorkerCmd.MarkFlagRequired("controller-generation")
	_ = controllerWorkerCmd.MarkFlagRequired("controller-type")
	_ = controllerWorkerCmd.MarkFlagRequired("controller-descriptor-digest")
}

func runControllerWorker(cmd *cobra.Command, _ []string) error {
	if controllerWorkerGeneration < 1 {
		return fmt.Errorf("controller generation must be greater than zero")
	}
	bootstrapData, err := os.ReadFile(controllerWorkerConfigPath)
	if err != nil {
		return fmt.Errorf("read controller worker config: %w", err)
	}
	var bootstrap controlleradapter.WorkerConfig
	if err := yaml.UnmarshalStrict(bootstrapData, &bootstrap); err != nil {
		return fmt.Errorf("decode controller worker config: %w", err)
	}
	if err := bootstrap.Validate(); err != nil {
		return fmt.Errorf("validate controller worker config: %w", err)
	}
	if err := bootstrap.ValidateIdentity(controllerWorkerNamespace, controllerWorkerName, controllerWorkerUID, controllerWorkerType); err != nil {
		return fmt.Errorf("bind controller worker config to pod identity: %w", err)
	}
	descriptor, registered := controlleradapter.DescriptorFor(bootstrap.Type)
	if !registered {
		return fmt.Errorf("controller adapter %q is not registered in worker image", bootstrap.Type)
	}
	if actual := controlleradapter.DescriptorDigest(descriptor); actual != controllerWorkerDescriptorDigest {
		return fmt.Errorf("controller adapter %q descriptor digest %q does not match manager digest %q", bootstrap.Type, actual, controllerWorkerDescriptorDigest)
	}

	workerScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(workerScheme))
	utilruntime.Must(ciskov1.AddToScheme(workerScheme))
	utilruntime.Must(configv1alpha1.AddToScheme(workerScheme))
	utilruntime.Must(opsv1alpha1.AddToScheme(workerScheme))
	if err := controlleradapter.InstallScheme(bootstrap.Type, workerScheme); err != nil {
		return err
	}

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes REST config: %w", err)
	}
	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 workerScheme,
		Cache:                  controllerWorkerCacheOptions(bootstrap),
		LeaderElection:         false,
		HealthProbeBindAddress: controllerWorkerProbeAddr,
		Metrics: metricsserver.Options{
			BindAddress: controllerWorkerMetricsAddr,
		},
	})
	if err != nil {
		return fmt.Errorf("build controller worker manager: %w", err)
	}

	fetchCtx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()
	var controller ciskov1.NetworkController
	key := client.ObjectKey{Namespace: bootstrap.ControllerRef.Namespace, Name: bootstrap.ControllerRef.Name}
	if err := mgr.GetAPIReader().Get(fetchCtx, key, &controller); err != nil {
		return fmt.Errorf("fetch NetworkController %s: %w", key, err)
	}
	if err := validateLiveControllerIdentity(&controller, bootstrap, controllerWorkerUID, controllerWorkerGeneration); err != nil {
		return fmt.Errorf("bind live NetworkController %s to worker pod: %w", key, err)
	}

	caPath := ""
	materialRoots := []string{
		controlleradapter.DefaultCredentialPath,
		controlleradapter.DefaultIntentSecretPath,
	}
	if controller.Spec.TLS != nil && controller.Spec.TLS.CAConfigMapRef != nil {
		caPath = controlleradapter.DefaultCAPath
		materialRoots = append(materialRoots, controlleradapter.DefaultCADirectory)
	}
	materialWatcher, err := controlleradapter.NewProjectedMaterialWatcher(
		controlleradapter.DefaultMaterialRotationPollInterval,
		materialRoots...,
	)
	if err != nil {
		return fmt.Errorf("initialize projected material rotation watcher: %w", err)
	}
	materialPolicy, err := materialWatcher.Policy(controlleradapter.DefaultMaxSessionLifetime)
	if err != nil {
		return fmt.Errorf("initialize projected material rotation policy: %w", err)
	}
	if err := mgr.Add(materialWatcher); err != nil {
		return fmt.Errorf("add projected material rotation watcher: %w", err)
	}
	adapter, err := controlleradapter.NewAdapter(bootstrap.Type, controlleradapter.Options{
		Controller:       &controller,
		CredentialPath:   controlleradapter.DefaultCredentialPath,
		CAPath:           caPath,
		IntentSecretPath: controlleradapter.DefaultIntentSecretPath,
		MaterialRotation: materialPolicy,
	})
	if err != nil {
		return err
	}
	if err := adapter.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("set up controller adapter %q: %w", bootstrap.Type, err)
	}
	// Process health deliberately reflects the manager lifecycle, not remote
	// controller reachability. Remote failures belong in API status and must
	// not create a restart storm.
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("add controller worker health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("add controller worker readiness check: %w", err)
	}

	ctrl.Log.WithName("controller-worker").Info("starting controller worker",
		"networkController", key,
		"type", bootstrap.Type,
	)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("run controller worker manager: %w", err)
	}
	return nil
}

func validateLiveControllerIdentity(controller *ciskov1.NetworkController, bootstrap controlleradapter.WorkerConfig, expectedUID string, expectedGeneration int64) error {
	if err := ciskov1.ValidateNetworkController(controller); err != nil {
		return fmt.Errorf("validate NetworkController: %w", err)
	}
	if string(controller.Spec.Type) != bootstrap.Type {
		return fmt.Errorf("type %q does not match worker bootstrap type %q", controller.Spec.Type, bootstrap.Type)
	}
	if string(controller.UID) != expectedUID {
		return fmt.Errorf("UID %q does not match worker pod UID identity %q", controller.UID, expectedUID)
	}
	if controller.Generation != expectedGeneration {
		return fmt.Errorf("generation %d does not match worker pod generation %d", controller.Generation, expectedGeneration)
	}
	return nil
}

func controllerWorkerCacheOptions(bootstrap controlleradapter.WorkerConfig) cache.Options {
	return cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			bootstrap.ControllerRef.Namespace: {},
		},
		// Keep the shared endpoint type bounded to this worker's immutable
		// identity. NetworkControllerConfig remains namespace-scoped because
		// Kubernetes 1.28 does not support CRD field-selectable watches.
		ByObject: map[client.Object]cache.ByObject{
			&ciskov1.NetworkController{}: {
				Field: fields.OneTermEqualSelector("metadata.name", bootstrap.ControllerRef.Name),
			},
		},
	}
}
