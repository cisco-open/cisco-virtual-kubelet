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

// startConfigReconciler is the cisco-vk run-side entrypoint that
// stands up a per-device ConfigReconciler. After Phase 9 it has no
// platform-specific code: the per-platform `ConfigDriverFactory`
// registered in `internal/drivers/<platform>/register.go` returns
// the platform-specific transport, key rules, writer lookup, and
// Subscribe-watch paths. The cisco-vk binary just composes those
// with the platform-agnostic provider.ConfigReconciler.
//
// Adding a new platform never edits this file. The cost of a new
// platform is one register.go in the new package + the blank
// import in cmd/cisco-vk/drivers_register.go.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

// configReconcilerOptions is what startConfigReconciler needs from the
// surrounding cisco-vk-run setup: device spec (for transport build) and
// resolved password.
type configReconcilerOptions struct {
	Spec     *ciskov1.DeviceSpec
	Password string
	// SessionLock optionally serialises config-driver traffic
	// against the apphosting driver. Recommended in production.
	SessionLock *sync.Mutex
}

// startConfigReconciler builds a controller-runtime client, asks
// the platform-agnostic registry for a ConfigDriverContext that
// matches CiscoDevice.spec.driver, and starts the reconciler
// goroutine tied to ctx. Failures are non-fatal: a platform that
// is not registered, or whose context construction fails,
// silently leaves the device's config plane unmanaged. The
// apphosting side continues to run.
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

	if !drivers.ConfigDriverRegistered(opts.Spec.Driver) {
		log.G(ctx).WithField("driver", opts.Spec.Driver).
			Debug("no config driver registered for this device kind; skipping config reconciler")
		return nil
	}

	scheme := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ciskov1.AddToScheme(scheme))
	utilruntime.Must(coordv1.AddToScheme(scheme))

	// Register engine + transport metrics on the default Prometheus
	// registry so the existing /metrics endpoint scrapes them. Both
	// calls are idempotent: a second cisco-vk pod sharing the same
	// process (test fixtures, in-process callers) won't double-
	// register.
	engine.RegisterMetrics(prometheus.DefaultRegisterer)
	transport.RegisterTransportMetrics(prometheus.DefaultRegisterer)

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

	// Per-platform ConfigDriverContext via the registry. Transport
	// build failure is non-fatal: the per-driver factory returns
	// the partial context with Transport=nil and a wrapped error.
	// We log it and proceed in scaffold mode.
	dctx, dErr := drivers.NewConfigDriver(ctx, opts.Spec, opts.Password, drivers.ConfigDriverOptions{
		SessionLock: opts.SessionLock,
	})
	if dErr != nil {
		log.G(ctx).WithError(dErr).Warn("config driver context error; reconciler will run in scaffold mode")
	}
	if dctx == nil {
		// Defensive: NewConfigDriver should always return a context
		// even on partial failure, but if a future driver doesn't,
		// the reconciler still needs a non-nil context.
		dctx = &drivers.ConfigDriverContext{}
	}

	// Lease namespace selection — three-tier precedence:
	//   1. CONFIG_LEASE_NAMESPACE env (operator opt-in to a shared
	//      cluster namespace so IOSXEConfig CRs in different
	//      tenant namespaces actually arbitrate against each other).
	//   2. POD_NAMESPACE — historical default.
	//   3. "default" — out-of-cluster dev fallback.
	leaseNamespace := os.Getenv("CONFIG_LEASE_NAMESPACE")
	if leaseNamespace == "" {
		leaseNamespace = os.Getenv("POD_NAMESPACE")
	}
	if leaseNamespace == "" {
		leaseNamespace = "default"
	}

	// Subscribe-based drift fast path: gNMI today; per-driver
	// factory provides the path set so other drivers can attach
	// their own without changing this code.
	var notify <-chan struct{}
	if dctx.Transport != nil && dctx.Transport.Capabilities().SupportsSubscribe && len(dctx.SubscribePaths) > 0 {
		n, err := provider.StartSubscribeWatcher(ctx, dctx.Transport, dctx.SubscribePaths, 100*time.Millisecond)
		if err != nil {
			log.G(ctx).WithError(err).Warn("subscribe watcher unavailable; falling back to polling")
		} else {
			notify = n
		}
	}

	// Wave 7A.3 — runtime-identity-suffixed lease holder. The
	// per-pod path uses the pod UID injected by the controller via
	// the downward API (POD_UID env var). Two pods running the
	// same CR identity (old + new during a Deployment rollout)
	// then have distinct lease holders and cannot both renew the
	// same lease. Empty POD_UID falls back to the CR-only identity
	// (preserves test/local-run behaviour).
	runtimeID := os.Getenv("POD_UID")

	// Wave 6A — bridge the notify channel into a controller-runtime
	// event stream. The Reconciler's SetupWithManager registers a
	// source.Channel against this; each delivered GenericEvent
	// triggers a Reconcile for every IOSXEConfig targeting this
	// device. NotifySubscribeFired records the timestamp Reconcile
	// uses to differentiate subscribe-driven ticks (bypass hash
	// short-circuit) from normal CR/scope-object events.
	var subscribeEvents chan event.GenericEvent
	r := &provider.ConfigReconciler{
		Client:                mgr.GetClient(),
		DeviceName:            deviceName,
		Transport:             dctx.Transport,
		KeyRules:              dctx.KeyRules,
		SupportedYANGVersions: dctx.SupportedYANGVersions,
		DefaultYANGVersion:    dctx.DefaultYANGVersion,
		Lookup:                dctx.LookupWriter,
		Leaser: &engine.FamilyLeaser{
			Client:    mgr.GetClient(),
			Namespace: leaseNamespace,
		},
		Recorder:        recorder,
		SubscribeNotify: notify,
		RuntimeID:       runtimeID,
	}
	if notify != nil {
		// Buffer of 1 — the watcher coalesces events into "fire
		// at most once per tick", so a single-slot buffer is
		// sufficient and the bridge's send below uses a
		// non-blocking select to drop on full.
		subscribeEvents = make(chan event.GenericEvent, 1)
		r.SubscribeEvents = subscribeEvents
		go func() {
			defer close(subscribeEvents)
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-notify:
					if !ok {
						return
					}
					r.NotifySubscribeFired()
					// Single anonymous-CR event; the
					// SetupWithManager mapper enumerates every
					// IOSXEConfig targeting this device and
					// enqueues their reconciles. The carrier
					// object is intentionally minimal — its only
					// job is to wake the source.Channel.
					select {
					case subscribeEvents <- event.GenericEvent{
						Object: &configv1alpha1.IOSXEConfig{
							ObjectMeta: metav1.ObjectMeta{Namespace: "", Name: deviceName},
						},
					}:
					default:
						// channel full; the next reconcile cycle
						// will already pick up the in-flight event
					}
				}
			}
		}()
	}

	if err := r.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("SetupWithManager: %w", err)
	}

	go func() {
		if runErr := mgr.Start(ctx); runErr != nil && runErr != context.Canceled {
			log.G(ctx).WithError(runErr).Warn("config-reconciler manager exited with error")
		}
	}()
	return nil
}
