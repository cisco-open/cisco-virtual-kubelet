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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"syscall"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
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

// Interface Guards
var _ nodeutil.Provider = (*provider.AppHostingProvider)(nil)
var _ node.NodeProvider = (*provider.AppHostingNode)(nil)

var (
	cfgFile    string
	kubeconfig string
	logLevel   string
	nodeName   string
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

	mux := http.NewServeMux()

	tlsCert, err := generateSelfSignedCert(effectiveNodeName, appCfg.Device.Address)
	if err != nil {
		return fmt.Errorf("failed to generate self-signed TLS cert: %w", err)
	}

	opts := []nodeutil.NodeOpt{
		nodeutil.WithNodeConfig(nodeutil.NodeConfig{
			Client:         clientset,
			NodeSpec:       provider.GetInitialNodeSpec(effectiveNodeName, appCfg.Device.Address),
			HTTPListenAddr: ":10250",
			NumWorkers:     5,
			Handler:        mux,
			TLSConfig: &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{tlsCert},
				ClientAuth:   tls.NoClientCert,
			},
		}),
		nodeutil.AttachProviderRoutes(mux),
	}

	newProviderFunc := func(vkCfg nodeutil.ProviderConfig) (nodeutil.Provider, node.NodeProvider, error) {
		// Create a single shared driver for both node and pod handlers
		sharedDriver, err := drivers.NewDriver(ctx, &appCfg.Device)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create device driver: %w", err)
		}

		nodeHandler := provider.NewAppHostingNode(ctx, effectiveNodeName, &appCfg.Device, sharedDriver)

		podHandler, err := provider.NewAppHostingProvider(ctx, &appCfg.Device, vkCfg, sharedDriver, nodeHandler)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialise PodHandler: %w", err)
		}
		return podHandler, nodeHandler, nil
	}
	n, err := nodeutil.NewNode(effectiveNodeName, newProviderFunc, opts...)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	if err := n.Run(ctx); err != nil {
		return fmt.Errorf("node run failed: %w", err)
	}

	log.G(ctx).Info("Cisco Virtual Kubelet stopped")
	return nil
}

// generateSelfSignedCert creates a self-signed TLS certificate for the kubelet API server.
// The certificate includes the node name and device address as SANs so that the
// Kubernetes API server can proxy requests to the VK's :10250 endpoint.
func generateSelfSignedCert(nodeName, deviceAddr string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to generate private key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   nodeName,
			Organization: []string{"Cisco Virtual Kubelet"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{nodeName, "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	// Add device address as a SAN (could be IP or hostname)
	if ip := net.ParseIP(deviceAddr); ip != nil {
		template.IPAddresses = append(template.IPAddresses, ip)
	} else if deviceAddr != "" {
		template.DNSNames = append(template.DNSNames, deviceAddr)
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to create certificate: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}
