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
	"syscall"

	cisco "github.com/cisco/virtual-kubelet-cisco/pkg"
	logruslib "github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	providerName    = "cisco"
	defaultNodeName = "cisco-virtual-node"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup logging
	logrusLogger := logruslib.New()

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

	// Get configuration from environment
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		nodeName = defaultNodeName
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}

	log.G(ctx).WithFields(map[string]interface{}{
		"provider":   providerName,
		"nodeName":   nodeName,
		"kubeconfig": kubeconfig,
	}).Info("Starting Cisco Virtual Kubelet")

	// Create Kubernetes client configuration
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		log.G(ctx).WithError(err).Fatal("Failed to load kubeconfig")
	}

	// Create Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.G(ctx).WithError(err).Fatal("Failed to create Kubernetes client")
	}

	// Create NewProviderFunc for virtual-kubelet
	newProviderFunc := func(cfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		ciscoProvider, err := NewCiscoProvider(ctx)
		if err != nil {
			return nil, nil, err
		}

		// CRITICAL: Set node capacity on the provided node object
		// This is how virtual-kubelet v1.11.0 expects capacity to be configured
		if cfg.Node != nil {
			capacity := ciscoProvider.GetDeviceCapacity()

			fmt.Printf("[CISCO-VK] Setting node capacity via ProviderConfig: CPU:%s Memory:%s Storage:%s Pods:%s\n",
				capacity.CPU.String(),
				capacity.Memory.String(),
				capacity.Storage.String(),
				capacity.Pods.String())

			cfg.Node.Status.Capacity = v1.ResourceList{
				v1.ResourceCPU:     capacity.CPU,
				v1.ResourceMemory:  capacity.Memory,
				v1.ResourceStorage: capacity.Storage,
				v1.ResourcePods:    capacity.Pods,
			}
			cfg.Node.Status.Allocatable = cfg.Node.Status.Capacity

			// Set node labels
			if cfg.Node.Labels == nil {
				cfg.Node.Labels = make(map[string]string)
			}
			cfg.Node.Labels["cisco.com/provider"] = "cisco"
			cfg.Node.Labels["kubernetes.io/os"] = "Linux"
		}

		// Return provider as both Provider and NodeProvider
		return ciscoProvider, ciscoProvider, nil
	}

	// Create node with proper configuration
	opts := []nodeutil.NodeOpt{
		nodeutil.WithClient(clientset),
	}

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

// NewCiscoProvider creates a new Cisco provider instance
func NewCiscoProvider(ctx context.Context) (*cisco.CiscoProvider, error) {
	// Get configuration
	configPath := os.Getenv("CISCO_CONFIG_PATH")
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		nodeName = defaultNodeName
	}

	operatingSystem := "Linux"
	internalIP := os.Getenv("VKUBELET_POD_IP")
	if internalIP == "" {
		internalIP = "127.0.0.1"
	}

	// Initialize actual Cisco provider
	provider, err := cisco.NewCiscoProvider(configPath, nodeName, operatingSystem, internalIP, 10250)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cisco provider: %w", err)
	}

	log.G(ctx).Info("Cisco provider initialization complete")
	return provider, nil
}
