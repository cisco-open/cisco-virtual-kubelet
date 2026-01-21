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

package cmd

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
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	cfgFile    string
	kubeconfig string
	logLevel   string
)

var rootCmd = &cobra.Command{
	Use:   "virtual-kubelet",
	Short: "Cisco Virtual Kubelet for AppHosting",
	Long: `Cisco Virtual Kubelet implements the Kubelet interface to deploy
containers on Cisco devices using AppHosting.`,
	RunE: runVirtualKubelet,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "",
		"config file (default: /etc/virtual-kubelet/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "",
		"path to kubeconfig file (default: $KUBECONFIG or in-cluster)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "",
		"log level: debug, info, warn, error (default: $LOG_LEVEL or info)")
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func runVirtualKubelet(cmd *cobra.Command, args []string) error {
	// Load config
	appCfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
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

	// Log level: flag > env > default
	lvl := logLevel
	if lvl == "" {
		lvl = os.Getenv("LOG_LEVEL")
	}
	switch lvl {
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

	// Signal handling
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.G(ctx).Info("Received shutdown signal")
		cancel()
	}()

	// Kubeconfig: flag > env > in-cluster
	kubeconfigPath := kubeconfig
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}
	var restconfig *rest.Config
	if kubeconfigPath != "" {
		restconfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else {
		restconfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	opts := []nodeutil.NodeOpt{
		nodeutil.WithNodeConfig(nodeutil.NodeConfig{
			Client:         clientset,
			NodeSpec:       provider.GetInitialNodeSpec(appCfg),
			HTTPListenAddr: ":10250",
			NumWorkers:     5,
		}),
	}

	newProviderFunc := func(vkCfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		podHandler, err := provider.NewAppHostingProvider(ctx, appCfg, vkCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialise PodHandler: %w", err)
		}
		nodeHandler, err := provider.NewAppHostingNode(ctx, appCfg, vkCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialise nodeHandler: %w", err)
		}
		return podHandler, nodeHandler, nil
	}

	nodeName := provider.GetNodeName(appCfg)
	n, err := nodeutil.NewNode(nodeName, newProviderFunc, opts...)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	if err := n.Run(ctx); err != nil {
		return fmt.Errorf("node run failed: %w", err)
	}

	log.G(ctx).Info("Cisco Virtual Kubelet stopped")
	return nil
}
