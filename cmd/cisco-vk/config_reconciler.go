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
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe"
	iosxetransport "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/devicegrpc"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/telemetry"
	"github.com/cisco/virtual-kubelet-cisco/internal/otelproviders"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/deviceoperation"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic/adminserver"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/operationalaction"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	telemetrystate "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	telemetryyang "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/yang"
)

// configReconcilerOptions is what startConfigReconciler needs from the
// surrounding cisco-vk-run setup: device spec (for transport build) and
// resolved password.
type configReconcilerOptions struct {
	Spec     *ciskov1.DeviceSpec
	Password string
	// EnableWriteClassGNOI opt-ins destructive IOSXEOperationalAction
	// handling. Default false keeps a gNOI-enabled read-only deployment
	// from gaining reboot/factory-reset/file-write authority implicitly.
	EnableWriteClassGNOI bool
	// EnableIOSXESoftwareUpgrade opt-ins the multi-phase gNOI OS upgrade
	// reconciler. Default false keeps upgrade RBAC and behavior explicit.
	EnableIOSXESoftwareUpgrade bool
	// SessionLock optionally serialises config-driver traffic
	// against the apphosting driver. Recommended in production.
	SessionLock *sync.Mutex
	// TelemetryProviders optionally carries the per-device OTel providers built
	// by run.go so topology and MDT telemetry share endpoint configuration.
	TelemetryProviders *otelproviders.Providers
	// StateCache receives MDT-derived state records from IOSXETelemetry.
	StateCache *telemetrystate.Cache
	// AppEventConsumer receives app-hosting state events and usually points at
	// the AppHostingProvider's PodNotifier bridge.
	AppEventConsumer telemetrystate.AppEventConsumer
	// CorrelationCache maps app IDs to the span context that created them.
	CorrelationCache *correlation.Cache
}

func configDriverBuildOptions(opts configReconcilerOptions) drivers.ConfigDriverOptions {
	return drivers.ConfigDriverOptions{
		SessionLock: opts.SessionLock,
	}
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

	starter, ok := lookupConfigRuntime(opts.Spec.Driver)
	if !ok {
		log.G(ctx).WithField("driver", opts.Spec.Driver).
			Debug("no config runtime registered for this device kind; skipping config reconciler")
		return nil
	}
	return starter(ctx, cfg, deviceName, opts)
}

func startIOSXEConfigReconciler(ctx context.Context, cfg *rest.Config, deviceName string, opts configReconcilerOptions) error {
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
			Debug("no config driver registered for this device kind; skipping IOSXEConfig reconciler")
		return nil
	}
	scheme := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
	utilruntime.Must(opsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ciskov1.AddToScheme(scheme))
	utilruntime.Must(coordv1.AddToScheme(scheme))

	// Register engine + transport metrics on controller-runtime's
	// registry — the registry the manager's metrics server actually
	// scrapes at :8080/metrics. Pre-fix, the engine registered on
	// prometheus.DefaultRegisterer, which is a separate package-level
	// var; the metrics endpoint exposed only the controller-runtime
	// + Go runtime collectors and silently dropped every cisco_vk
	// counter the verify scripts depend on. Caught against a live
	// Cat9300 retest where the metric scrape returned 577 lines but
	// not a single cisco_vk_* line. Both calls are idempotent: a
	// second cisco-vk pod sharing the same process (test fixtures,
	// in-process callers) won't double-register.
	engine.RegisterMetrics(metrics.Registry)
	transport.RegisterTransportMetrics(metrics.Registry)
	gnoi.RegisterMetrics(metrics.Registry)
	devicegrpc.RegisterMetrics(metrics.Registry)
	softwareupgrade.RegisterMetrics(metrics.Registry)
	operationalaction.RegisterMetrics(metrics.Registry)

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
	// Metrics: bind the controller-runtime + Prometheus collectors on
	// :8080. Operators rely on cisco_vk_config_* counters for the
	// release-blocker test suite and for production dashboards;
	// disabling the endpoint with `BindAddress: "0"` (the historical
	// default) leaves verify.sh metric assertions with nothing to
	// scrape. The bind address is configurable via CONFIG_METRICS_ADDR
	// (set to "0" or empty to opt out — same semantics as before).
	metricsAddr := os.Getenv("CONFIG_METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":8080"
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
	// Scope the per-device manager cache to the device namespace. The
	// config/ops CRDs (IOSXEConfig, scope objects, diagnostics, operations,
	// …) are device-namespace-scoped and bound via a namespaced RoleBinding,
	// so a cluster-wide informer ListWatch would be RBAC-forbidden. Scoping
	// the cache here makes every config informer namespaced (matching the
	// RBAC) and is the umbrella enforcement of the same-namespace deviceRef
	// contract. Leases may live in a shared CONFIG_LEASE_NAMESPACE, so they
	// get an explicit per-object namespace override.
	configCache := cache.Options{
		DefaultNamespaces: map[string]cache.Config{operationNamespace(): {}},
	}
	if leaseNamespace != operationNamespace() {
		configCache.ByObject = map[client.Object]cache.ByObject{
			&coordv1.Lease{}: {Namespaces: map[string]cache.Config{leaseNamespace: {}}},
		}
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		Cache:                  configCache,
	})
	if err != nil {
		return fmt.Errorf("build manager: %w", err)
	}

	// Per-platform ConfigDriverContext via the registry. Transport
	// build failure is non-fatal: the per-driver factory returns
	// the partial context with Transport=nil and a wrapped error.
	// We log it and proceed in scaffold mode.
	//
	// One synchronous dial attempt at startup, then a deferred async
	// retry loop. The 2026-04-27 NETCONF-probe experiment (recorded in
	// docs/rfcs/final/evidence/2026-04-27-live-c9300-netconf-probe-tier1)
	// proved the from-pod NETCONF dial races against the apphosting +
	// virtual-kubelet startup window: the very first ssh.Dial fires
	// while the runtime is still initialising HTTP/2 keep-alives, and
	// the device sends `read-empty: EOF`. The same code path
	// SUCCEEDS once the startup window settles (~60 s into pod
	// life). A bounded synchronous retry would either block pod
	// readiness for too long or give up before the window closes.
	// Spawn an async loop instead and proceed with whatever the
	// reconciler has now; SetTransport patches it in when the dial
	// eventually succeeds.
	dctx, dErr := drivers.NewConfigDriver(ctx, opts.Spec, opts.Password, configDriverBuildOptions(opts))
	if dErr != nil {
		log.G(ctx).WithError(dErr).Warn("config driver dial failed at startup; will retry in background")
	}
	if dctx == nil {
		// Defensive: NewConfigDriver should always return a context
		// even on partial failure, but if a future driver doesn't,
		// the reconciler still needs a non-nil context.
		dctx = &drivers.ConfigDriverContext{}
	}
	// Validate the device version before any IOSXEConfig write path
	// can start. Empty means the transport/version fetch is not ready
	// yet; the reconciler starts in Pending and the retry loop will
	// unblock writes only after a valid version is acquired.
	//
	// gNOI-backed lifecycle operations are intentionally decoupled
	// from the IOSXEConfig writer matrix. A device may be running a
	// release that is not yet supported by the NetAsCode writers, and
	// the software upgrade reconciler may be the exact tool needed to
	// move it back to a supported train.
	configWritesEnabled := true
	if err := dctx.ValidateDeviceVersion(); err != nil {
		entry := log.G(ctx).WithError(err).WithField("version", dctx.DeviceVersion)
		reason := "MalformedDeviceVersion"
		if drivers.IsUnsupportedDeviceVersionError(err) {
			reason = "UnsupportedDeviceVersion"
			entry.Error("device version is not in the supported release set; IOSXEConfig writes disabled")
		} else {
			entry.Error("device version is malformed; IOSXEConfig writes disabled")
		}
		recorder.Eventf(&ciskov1.CiscoDevice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deviceName,
				Namespace: operationNamespace(),
			},
		}, corev1.EventTypeWarning, reason,
			"device version %q rejected by writers: %v", dctx.DeviceVersion, err)
		configWritesEnabled = false
	} else if dctx.DeviceVersion != "" {
		log.G(ctx).WithField("version", dctx.DeviceVersion).Info("device version set for writers")
	} else {
		log.G(ctx).Warn("device version not available yet; IOSXEConfig writes remain pending until version is acquired")
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
		DeviceNamespace:       operationNamespace(),
		Transport:             dctx.Transport,
		DeviceVersion:         dctx.DeviceVersion,
		FetchDeviceVersion:    dctx.FetchDeviceVersion,
		RequireDeviceVersion:  true,
		KeyRules:              dctx.KeyRules,
		SupportedYANGVersions: dctx.SupportedYANGVersions,
		DefaultYANGVersion:    dctx.DefaultYANGVersion,
		Lookup:                dctx.LookupWriter,
		FamilyOrder:           dctx.FamilyOrder,
		YANGValidator:         dctx.YANGValidator,
		YANGValidationMode:    dctx.YANGValidationMode,
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

	if configWritesEnabled {
		if err := r.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("SetupWithManager: %w", err)
		}
	} else {
		log.G(ctx).Warn("IOSXEConfig reconciler not registered; continuing with diagnostics and gNOI lifecycle controllers")
	}

	// Diagnostics-RFC Phase B: the IOSXEDiagnostic reconciler runs in
	// the same controller-runtime manager as ConfigReconciler. It
	// borrows the configdriver's transport via the GetTransport
	// accessor — no separate dial, no separate auth.
	diagReconciler := &diagnostic.Reconciler{
		Client:          mgr.GetClient(),
		Recorder:        recorder,
		Scheme:          mgr.GetScheme(),
		DeviceName:      deviceName,
		DeviceNamespace: operationNamespace(),
		Platform:        diagnostic.CommandPlatformIOSXE,
		TP:              r,
	}
	if err := diagReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("diagnostic SetupWithManager: %w", err)
	}

	// gNOI pillar: build a per-device gRPC pool and gNOI client, then
	// wire the three reconcilers that consume it.
	//
	// Pool lifetime is bound to the surrounding ctx (the VK pod's run
	// context). Control is leased on first use, and bulk-transfer
	// conns are leased only for File.Get/Put and OS.Install streams.
	// If the gNOI server is not reachable at startup the dial is lazy
	// — the conn materialises on first RPC, so a device that comes up
	// after the VK pod is fine.
	gnoiProv, gnoiCleanup := setupGNOI(ctx, opts)
	if gnoiCleanup != nil {
		go func() {
			<-ctx.Done()
			gnoiCleanup()
		}()
	}

	operationReconciler := &deviceoperation.Reconciler{
		Client:   mgr.GetClient(),
		Reader:   mgr.GetAPIReader(),
		Recorder: recorder,
		Scheme:   mgr.GetScheme(),
		// DeviceNamespace is the namespace of the owning CiscoDevice CR,
		// which is the same as this VK pod's POD_NAMESPACE because the
		// controller always creates the per-device Deployment in
		// device.Namespace (see ciscodevice_controller.go). The reconciler
		// uses it to refuse cross-namespace DeviceOperation requests.
		DeviceName:      deviceName,
		DeviceNamespace: operationNamespace(),
		Platform:        diagnostic.CommandPlatformIOSXE,
		TP:              r,
		GNOI:            gnoiProv,
	}
	if err := operationReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("device operation SetupWithManager: %w", err)
	}

	if gnoiProv != nil && opts.EnableIOSXESoftwareUpgrade {
		upgradeReconciler := &softwareupgrade.Reconciler{
			Client:          mgr.GetClient(),
			Reader:          mgr.GetAPIReader(),
			Recorder:        recorder,
			Scheme:          mgr.GetScheme(),
			DeviceName:      deviceName,
			DeviceNamespace: operationNamespace(),
			GNOI:            gnoiProv,
			TP:              r,
			ImageResolver:   softwareupgrade.NewDefaultImageResolver(mgr.GetClient(), nil),
		}
		if err := upgradeReconciler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("software upgrade SetupWithManager: %w", err)
		}
	} else if gnoiProv != nil {
		log.G(ctx).Info("IOSXESoftwareUpgrade reconciler not registered; enable with --enable-iosxesoftwareupgrade or CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE=true")
	}

	if gnoiProv != nil && opts.EnableWriteClassGNOI {
		actionReconciler := &operationalaction.Reconciler{
			Client:          mgr.GetClient(),
			Reader:          mgr.GetAPIReader(),
			Recorder:        recorder,
			Scheme:          mgr.GetScheme(),
			DeviceName:      deviceName,
			DeviceNamespace: operationNamespace(),
			GNOI:            gnoiProv,
		}
		if err := actionReconciler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("operational action SetupWithManager: %w", err)
		}
	} else if gnoiProv != nil {
		log.G(ctx).Info("IOSXEOperationalAction reconciler not registered; enable with --enable-write-class-gnoi or CISCO_VK_ENABLE_WRITE_CLASS_GNOI=true")
	}

	telemetryFactory, err := telemetry.NewDefaultSubscribeClientFactoryForDevice(opts.Spec, opts.Password)
	if err != nil {
		return fmt.Errorf("telemetry subscriber factory: %w", err)
	}
	otelProviders := opts.TelemetryProviders
	var otelShutdown func(context.Context) error
	if otelProviders == nil {
		var err error
		otelProviders, otelShutdown, err = buildTelemetryProviders(ctx, deviceName, opts)
		if err != nil {
			return fmt.Errorf("telemetry OTel providers: %w", err)
		}
	}
	if otelShutdown != nil {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := otelShutdown(shutdownCtx); err != nil {
				log.G(ctx).WithError(err).Warn("telemetry OTel providers shutdown error")
			}
		}()
	}
	yangRegistry, err := telemetryyang.NewRegistryFromEnv()
	if err != nil {
		log.G(ctx).WithError(err).Warn("YANG registry unavailable; using curated telemetry classifier fallback")
		yangRegistry = nil
	}
	// CVK_RESOURCE_ATTRIBUTES is injected by Helm into the controller and
	// propagated into this per-device VK pod by the CiscoDevice controller.
	// Merge those operator-supplied attributes into the per-event resource
	// attributes used by MDT-over-gNMI OTel emissions.
	resourceAttrs, err := telemetryResourceAttributes(map[string]string{
		"cisco.device.address": opts.Spec.Address,
		"cisco.device.driver":  string(opts.Spec.Driver),
	})
	if err != nil {
		return err
	}
	telemetryEvents := make(chan event.GenericEvent, 1)
	telemetryReconciler := &provider.IOSXETelemetryReconciler{
		Client:     mgr.GetClient(),
		DeviceName: deviceName,
		// DeviceNamespace mirrors the DeviceOperation reconciler: the
		// VK pod always runs in the same namespace as its owning
		// CiscoDevice CR, so IOSXETelemetry CRs from other namespaces
		// must not be honored even when their DeviceRef matches.
		DeviceNamespace:  operationNamespace(),
		Factory:          telemetryFactory,
		LoggerProvider:   telemetryLoggerProvider(otelProviders),
		MeterProvider:    telemetryMeterProvider(otelProviders),
		TracerProvider:   telemetryTracerProvider(otelProviders),
		YangRegistry:     yangRegistry,
		ResourceAttrs:    resourceAttrs,
		StateCache:       opts.StateCache,
		AppEventConsumer: opts.AppEventConsumer,
		CorrelationCache: opts.CorrelationCache,
		RootContext:      ctx,
		StatusEvents:     telemetryEvents,
	}
	if err := telemetryReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("telemetry SetupWithManager: %w", err)
	}

	// Diagnostics-RFC Phase C: HTTP admin endpoint for ad-hoc
	// `kubectl ciscovk exec` invocations. Bound to 127.0.0.1:8082
	// inside the pod; operators reach it via `kubectl port-forward`,
	// which means pods/portforward RBAC is the auth gate. The
	// CONFIG_DIAG_ADMIN_ADDR env var lets ops opt out (set to "0"
	// or empty-disable) or override the bind address for tests.
	adminAddr := os.Getenv("CONFIG_DIAG_ADMIN_ADDR")
	if adminAddr == "" {
		adminAddr = adminserver.DefaultBindAddr
	}
	if adminAddr != "0" {
		admSrv := &adminserver.Server{
			DeviceName:         deviceName,
			TP:                 r,
			OperationClient:    mgr.GetClient(),
			OperationReader:    mgr.GetAPIReader(),
			OperationNamespace: operationNamespace(),
			Platform:           diagnostic.CommandPlatformIOSXE,
			BindAddr:           adminAddr,
			TelemetrySource:    telemetryReconciler.TelemetryHealthSnapshot,
		}
		stop := make(chan struct{})
		go func() {
			<-ctx.Done()
			close(stop)
		}()
		go func() {
			if err := admSrv.ListenAndServe(stop); err != nil {
				log.G(ctx).WithError(err).Warn("diagnostic admin server exited")
			}
		}()
	}

	go func() {
		if runErr := mgr.Start(ctx); runErr != nil && runErr != context.Canceled {
			log.G(ctx).WithError(runErr).Warn("config-reconciler manager exited with error")
		}
	}()

	// Deferred-dial loop: when the startup-time NewConfigDriver lost
	// the apphosting+VK race (Transport==nil), keep retrying until
	// the dial succeeds, then SetTransport on the live reconciler so
	// subsequent reconciles see a real transport instead of staying
	// in scaffold mode forever. 30-second cadence with no upper
	// bound — operators get a clear "config driver came online after
	// N attempts" log when the race resolves. Cancelled by ctx.
	if dctx.Transport == nil {
		go retryConfigDriverDial(ctx, opts, r, dctx)
	} else if dctx.DeviceVersion == "" {
		go retryDeviceVersion(ctx, r, dctx, dctx.Transport)
	}

	return nil
}

func operationNamespace() string {
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return "default"
}

func retryDeviceVersion(ctx context.Context, r *provider.ConfigReconciler, dctx *drivers.ConfigDriverContext, t transport.Interface) {
	const interval = 30 * time.Second
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		ver := iosxe.FetchDeviceVersion(ctx, t)
		if ver == "" {
			log.G(ctx).
				WithField("attempt", attempt).
				Info("device version still unavailable; config writes remain blocked")
			continue
		}
		dctx.DeviceVersion = ver
		if err := dctx.ValidateDeviceVersion(); err != nil {
			r.SetDeviceVersionState(ver, err)
			log.G(ctx).WithError(err).WithField("version", ver).
				Warn("device version retry produced rejected version; config writes remain blocked")
			if drivers.IsRetryableDeviceVersionError(err) {
				continue
			}
			return
		}
		r.SetDeviceVersionState(ver, nil)
		log.G(ctx).WithField("version", ver).
			Info("device version bound to writers after retry")
		return
	}
}

// retryConfigDriverDial attempts to build a real device transport
// every 30 seconds until success or ctx cancellation. On success it
// patches r.SetTransport so the reconciler picks up the live
// transport on its next tick.
//
// IMPORTANT: this calls `iosxetransport.For` directly rather than
// drivers.NewConfigDriver. The latter pulls iosxebuilder.LoadYANGReleaseTags
// on every invocation, which produces a burst of disk I/O + goroutine
// activity each retry tick — which is exactly the kind of runtime
// pressure that produced the original from-pod NETCONF dial overflow
// at startup. A side-by-side in-process probe goroutine that called
// `ssh.Dial` directly succeeded while NewConfigDriver-based attempts
// kept failing on the same wall clock; the schema-loading work was
// the only material difference. Build the transport in isolation
// here; the rest of the dctx (KeyRules, YANG versions, etc.) is
// loaded once at startup and is unaffected by the dial race.
func retryConfigDriverDial(ctx context.Context, opts configReconcilerOptions, r *provider.ConfigReconciler, dctx *drivers.ConfigDriverContext) {
	const interval = 30 * time.Second
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		t, err := iosxetransport.For(opts.Spec, opts.Password, iosxetransport.FactoryOptions{
			SessionLock: opts.SessionLock,
		})
		if err == nil && t != nil {
			// Now that we have a live transport, refetch the device
			// version and reapply it to writers. Without this the
			// version-conditional writer dispatch stays at whatever
			// state it was in at startup — which is "empty" if the
			// factory's own version fetch failed against the same
			// startup race.
			if ver := iosxe.FetchDeviceVersion(ctx, t); ver != "" {
				if dctx != nil {
					dctx.DeviceVersion = ver
				}
				if verr := dctx.ValidateDeviceVersion(); verr != nil {
					log.G(ctx).WithError(verr).WithField("version", ver).
						Warn("post-dial ValidateDeviceVersion rejected version; config writes remain blocked")
					r.SetDeviceVersionState(ver, verr)
					_ = t.Close()
					if drivers.IsRetryableDeviceVersionError(verr) {
						continue
					}
					return
				} else {
					r.SetDeviceVersionState(ver, nil)
					r.SetTransport(t)
					log.G(ctx).Infof("config driver transport acquired after %d retry attempt(s)", attempt)
					log.G(ctx).WithField("version", ver).
						Info("device version bound to writers after deferred dial success")
				}
			} else {
				log.G(ctx).Warn("deferred dial succeeded but version refetch returned empty; config writes remain blocked and dial will retry")
				_ = t.Close()
				continue
			}
			return
		}
		log.G(ctx).WithError(err).
			WithField("attempt", attempt).
			Info("config driver dial still failing; will retry")
	}
}
