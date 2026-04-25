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
	"time"

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
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/schema"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
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

	// Lease namespace selection — three-tier precedence:
	//   1. CONFIG_LEASE_NAMESPACE env (operator opt-in to a shared
	//      cluster namespace so IOSXEConfig CRs in different
	//      tenant namespaces actually arbitrate against each other,
	//      §10.10).
	//   2. POD_NAMESPACE — historical default; cross-namespace CRs
	//      that share the same device do *not* arbitrate under
	//      this setting because they each acquire a same-named
	//      lease in their own pod's namespace.
	//   3. "default" — out-of-cluster dev fallback when neither
	//      env is set.
	//
	// The Helm chart and the CiscoDevice controller surface the
	// shared-namespace value to every cisco-vk pod; operators who
	// want the historical (per-pod-namespace) behaviour leave
	// CONFIG_LEASE_NAMESPACE unset.
	leaseNamespace := os.Getenv("CONFIG_LEASE_NAMESPACE")
	if leaseNamespace == "" {
		leaseNamespace = os.Getenv("POD_NAMESPACE")
	}
	if leaseNamespace == "" {
		leaseNamespace = "default"
	}

	supportedYANG, defaultYANG := loadYANGReleaseTags(ctx)

	// Subscribe-based drift detection (Phase 6.5): if the
	// transport advertises SubscribeCapable (gNMI today), open a
	// stream against the union of YANG paths every registered
	// writer touches. The watcher pushes to a notify channel the
	// reconciler reads alongside its periodic ticker, so a write
	// outside CVK's apply path is detected within the coalesce
	// window (100 ms) instead of at the next 5 s tick.
	notify := startDriftSubscribe(ctx, t)

	r := &provider.ConfigReconciler{
		Client:                mgr.GetClient(),
		DeviceName:            deviceName,
		Transport:             t, // may be nil
		KeyRules:              keyRulesForPhase1(),
		SupportedYANGVersions: supportedYANG,
		DefaultYANGVersion:    defaultYANG,
		Leaser: &engine.FamilyLeaser{
			Client:    mgr.GetClient(),
			Namespace: leaseNamespace,
		},
		Recorder:        recorder,
		SubscribeNotify: notify,
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
		"vlan.vlans":                              "id",
		"vrf.vrfs":                                "name",
		"interface_ethernet.interfaces":           "name",
		"interface_loopback.interfaces":           "name",
		"interface_virtual_port_group.interfaces": "id",
		"dhcp.pools":                              "name",
		"access_list_extended.extended":           "name",
	}
}

// loadYANGReleaseTags reads schema/yang-versions.yaml and returns
// the set of release tags as a closed validator (set semantics) plus
// the default tag. A failure to load is non-fatal — we log and
// continue without validation, since YANG-version pinning is
// optional and a malformed file shouldn't take the controller down.
func loadYANGReleaseTags(ctx context.Context) (map[string]struct{}, string) {
	logger := log.G(ctx).WithField("component", "config-reconciler")
	releases, err := schema.LoadYANGReleases()
	if err != nil {
		logger.WithError(err).Warn("could not load yang-versions.yaml; spec.targetYangVersion validation disabled")
		return nil, ""
	}
	supported := make(map[string]struct{}, len(releases))
	var def string
	for _, r := range releases {
		supported[r.Version] = struct{}{}
		if r.Default {
			def = r.Version
		}
	}
	return supported, def
}

// startDriftSubscribe wires the per-pod gNMI Subscribe-based drift
// fast-path. Transports without SubscribeCapable return nil and
// the reconciler stays on its periodic ticker; that's the
// transport rollout pattern (RESTCONF + NETCONF have no Subscribe
// today and are not expected to gain one).
//
// The watch path set is the union of writer YANGPaths so a write
// to any leaf the engine cares about triggers a fast reconcile.
// 100 ms coalesce keeps a multi-leaf SetRequest from triggering N
// separate reconciles.
func startDriftSubscribe(ctx context.Context, t transport.Interface) <-chan struct{} {
	if t == nil {
		return nil
	}
	if !t.Capabilities().SupportsSubscribe {
		return nil
	}
	paths := unionWriterPaths()
	if len(paths) == 0 {
		return nil
	}
	notify, err := provider.StartSubscribeWatcher(ctx, t, paths, 100*time.Millisecond)
	if err != nil {
		log.G(ctx).WithError(err).Warn("subscribe watcher unavailable; falling back to polling")
		return nil
	}
	return notify
}

// unionWriterPaths gathers every YANG path advertised by the
// registered writers. Sorted output keeps gRPC-side request order
// stable across restarts.
func unionWriterPaths() []string {
	seen := map[string]struct{}{}
	for _, fam := range writers.Families() {
		w := writers.Get(fam)
		if w == nil {
			continue
		}
		for _, p := range w.YANGPaths() {
			seen[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}
