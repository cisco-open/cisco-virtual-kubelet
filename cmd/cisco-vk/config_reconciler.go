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
	"os"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

// configReconcilerOptions is what startConfigReconciler needs from the
// surrounding cisco-vk-run setup: device spec (for transport build) and
// resolved password. Kept as a struct so signatures don't grow.
type configReconcilerOptions struct {
	Spec     *ciskov1.DeviceSpec
	Password string
	// SessionLock optionally serialises config-driver RESTCONF traffic
	// against the apphosting driver. Recommended in production.
	SessionLock *sync.Mutex
}

// startConfigReconciler builds a controller-runtime client, assembles a
// transport according to CiscoDevice.spec.transport, and starts the
// IOSXEConfig reconciler goroutine tied to ctx. Failure to build any
// piece is returned to the caller — apphosting continues without the
// config driver rather than taking the process down.
func startConfigReconciler(ctx context.Context, cfg *rest.Config, deviceName string, opts configReconcilerOptions) error {
	if cfg == nil {
		return fmt.Errorf("nil rest.Config")
	}
	if deviceName == "" {
		return fmt.Errorf("empty device name")
	}
	if opts.Spec == nil {
		return fmt.Errorf("nil DeviceSpec")
	}

	scheme := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ciskov1.AddToScheme(scheme))
	utilruntime.Must(coordv1.AddToScheme(scheme))

	// Register engine metrics on the default Prometheus registry so the
	// existing /metrics endpoint scrapes them. Idempotent.
	engine.RegisterMetrics(prometheus.DefaultRegisterer)

	// Event recorder: the reconciler emits one event per non-trivial
	// per-family outcome and a terminal event per tick. The broadcaster
	// is tied to ctx so the goroutine exits cleanly at shutdown.
	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build typed client for events: %w", err)
	}
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: k8sClient.CoreV1().Events(""),
	})
	go func() {
		<-ctx.Done()
		broadcaster.Shutdown()
	}()
	recorder := broadcaster.NewRecorder(scheme, corev1.EventSource{
		Component: "cisco-vk-config-reconciler",
		Host:      deviceName,
	})

	// Controller-runtime manager: owns an informer-backed cache, the
	// controller's work queue, and a short-circuited /metrics server.
	// We disable the manager's own metrics server (MetricsBindAddress
	// = "0") because the VK process already exposes /metrics on
	// :10250 via the apphosting path; registering twice would fight
	// over the port.
	crlog.SetLogger(zap.New(zap.UseDevMode(true)))
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	if err != nil {
		return fmt.Errorf("build manager: %w", err)
	}

	// Transport construction failure is not fatal: the reconciler can
	// still run in scaffold mode (status=Pending, condition NoTransport).
	t, tErr := transport.For(opts.Spec, opts.Password, transport.FactoryOptions{
		SessionLock: opts.SessionLock,
	})
	if tErr != nil {
		log.G(ctx).WithError(tErr).Warn("IOSXEConfig transport unavailable; driver will report Pending")
	}

	// Lease namespace tracks the cisco-vk run pod's namespace so the
	// leases land alongside the process that owns them. In-cluster
	// deployments always have POD_NAMESPACE; out-of-cluster dev falls
	// back to "default".
	leaseNamespace := os.Getenv("POD_NAMESPACE")
	if leaseNamespace == "" {
		leaseNamespace = "default"
	}

	r := &provider.ConfigReconciler{
		Client:     mgr.GetClient(),
		DeviceName: deviceName,
		Transport:  t, // may be nil
		KeyRules:   keyRulesForPhase1(),
		Leaser: &engine.FamilyLeaser{
			Client:    mgr.GetClient(),
			Namespace: leaseNamespace,
		},
		Recorder: recorder,
	}

	if err := r.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("SetupWithManager: %w", err)
	}

	// Start the manager on a goroutine bound to ctx. When ctx cancels,
	// Start returns and the reconciler's informers shut down cleanly.
	go func() {
		if runErr := mgr.Start(ctx); runErr != nil && runErr != context.Canceled {
			log.G(ctx).WithError(runErr).Warn("IOSXEConfig manager exited with error")
		}
	}()
	return nil
}

// keyRulesForPhase1 returns the path → key-field rules the merger uses
// for YANG-keyed lists in the Phase-1 families. Phase-4 replaces this
// with a rule set derived from schema/families.yaml.
func keyRulesForPhase1() intent.KeyRules {
	return intent.KeyRules{
		"vlan.vlans":                      "id",
		"vrf.vrfs":                        "name",
		"interface_ethernet.interfaces":   "name",
		"interface_loopback.interfaces":   "name",
		"interface_virtual_port_group.interfaces": "id",
		"dhcp.pools":                      "name",
		"access_list_extended.extended":   "name",
	}
}
