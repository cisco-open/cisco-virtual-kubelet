// Copyright © 2025 Cisco Systems, Inc.
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
	"os/signal"
	"path"
	"runtime"
	"syscall"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
	logruslib "github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Interface Guard
var _ nodeutil.Provider = (*provider.AppHostingProvider)(nil)
var _ node.NodeProvider = (*provider.AppHostingNode)(nil)

func main() {

	appCfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup logging
	logrusLogger := logruslib.New()

	// Set log formatting
	logrusLogger.SetReportCaller(true)
	logrusLogger.SetFormatter(&logruslib.TextFormatter{
		FullTimestamp: true,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			// Return empty for the function name to remove it entirely
			// Use path.Base to show only the filename:line
			return "", fmt.Sprintf("%s:%d", path.Base(f.File), f.Line)
		},
	})

	// Set log level from environment
	logLevel := os.Getenv("LOG_LEVEL")
	switch logLevel {
	case "debug":
		logrusLogger.SetLevel(logruslib.DebugLevel)
	case "warn", "warning":
		logrusLogger.SetLevel(logruslib.WarnLevel)
	case "error":
		logrusLogger.SetLevel(logruslib.ErrorLevel)
	default:
		logrusLogger.SetLevel(logruslib.InfoLevel)
	}

	logger := logrus.FromLogrus(logruslib.NewEntry(logrusLogger))
	ctx = log.WithLogger(ctx, logger)

	// Handle shutdown gracefully
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.G(ctx).Info("Received shutdown signal")
		cancel()
	}()

	// TODO: Allow setting of kubeconfig path in config
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}

	// Create Kubernetes client configuration
	var restconfig *rest.Config

	if kubeconfig != "" {
		restconfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		restconfig, err = rest.InClusterConfig()
	}
	if err != nil {
		log.G(ctx).WithError(err).Fatal("Failed to load kubeconfig")
	}

	// Create Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(restconfig)
	if err != nil {
		log.G(ctx).WithError(err).Fatal("Failed to create Kubernetes client")
	}

	// Values here should either be static or derive from appCfg
	opts := []nodeutil.NodeOpt{
		nodeutil.WithNodeConfig(nodeutil.NodeConfig{
			Client:         clientset,
			NodeSpec:       provider.GetInitialNodeSpec(appCfg), // Reduce scope of appCfg to appCfg.Kubelet?
			HTTPListenAddr: ":10250",
			NumWorkers:     5,
		}),
	}

	newProviderFunc := func(vkCfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {

		PodHandler, err := provider.NewAppHostingProvider(ctx, appCfg, vkCfg)
		if err != nil {
			log.G(ctx).WithError(err).Fatal("Failed to initialise PodHandler")
		}

		nodeHandler, err := provider.NewAppHostingNode(ctx, appCfg, vkCfg)
		if err != nil {
			log.G(ctx).WithError(err).Fatal("Failed to initialise nodeHandler")
		}

		return PodHandler, nodeHandler, nil
	}

	nodeName := provider.GetNodeName(appCfg) // Reduce scope of appCfg to appCfg.Kubelet?
	n, err := nodeutil.NewNode(nodeName, newProviderFunc, opts...)
	if err != nil {
		log.G(ctx).WithError(err).Fatal("Failed to create node")
	}

	// Run node controller
	if err := n.Run(ctx); err != nil {
		log.G(ctx).WithError(err).Fatal("Node run failed")
	}

	log.G(ctx).Info("Cisco Virtual Kubelet stopped")
}
