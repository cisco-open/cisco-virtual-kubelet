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

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/deviceoperation"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic/adminserver"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

func startNXOSConfigReconciler(ctx context.Context, cfg *rest.Config, deviceName string, opts configReconcilerOptions) error {
	if cfg == nil {
		return fmt.Errorf("nil rest.Config")
	}
	if opts.Spec == nil {
		return fmt.Errorf("nil DeviceSpec")
	}

	scheme := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
	utilruntime.Must(opsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ciskov1.AddToScheme(scheme))
	utilruntime.Must(coordv1.AddToScheme(scheme))

	engine.RegisterMetrics(metrics.Registry)
	configtransport.RegisterTransportMetrics(metrics.Registry)

	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build typed client for events: %w", err)
	}
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: k8sClient.CoreV1().Events("")})
	go func() {
		<-ctx.Done()
		broadcaster.Shutdown()
	}()
	recorder := broadcaster.NewRecorder(scheme, corev1.EventSource{Component: "cisco-vk-nxos-config-reconciler", Host: deviceName})

	crlog.SetLogger(zap.New(zap.UseDevMode(true)))
	metricsAddr := os.Getenv("CONFIG_METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":8080"
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	if err != nil {
		return fmt.Errorf("build NXOSConfig manager: %w", err)
	}

	dctx, dErr := drivers.NewConfigDriver(ctx, opts.Spec, opts.Password, configDriverBuildOptions(opts))
	if dErr != nil {
		log.G(ctx).WithError(dErr).Warn("NXOSConfig driver transport failed at startup; reconciler will report Pending")
	}
	if dctx == nil {
		dctx = &drivers.ConfigDriverContext{}
	}

	var notify <-chan struct{}
	if dctx.Transport != nil && dctx.Transport.Capabilities().SupportsSubscribe && len(dctx.SubscribePaths) > 0 {
		n, err := provider.StartSubscribeWatcher(ctx, dctx.Transport, dctx.SubscribePaths, 100*time.Millisecond)
		if err != nil {
			log.G(ctx).WithError(err).Warn("NXOSConfig subscribe watcher unavailable; falling back to polling")
		} else {
			notify = n
		}
	}

	leaseNamespace := os.Getenv("CONFIG_LEASE_NAMESPACE")
	if leaseNamespace == "" {
		leaseNamespace = os.Getenv("POD_NAMESPACE")
	}
	if leaseNamespace == "" {
		leaseNamespace = "default"
	}
	var subscribeEvents chan event.GenericEvent
	r := &provider.NXOSConfigReconciler{
		Client:             mgr.GetClient(),
		DeviceName:         deviceName,
		Transport:          dctx.Transport,
		Lookup:             dctx.LookupWriter,
		FamilyOrder:        dctx.FamilyOrder,
		DeviceVersion:      dctx.DeviceVersion,
		DefaultYANGVersion: dctx.DefaultYANGVersion,
		Leaser: &engine.FamilyLeaser{
			Client:    mgr.GetClient(),
			Namespace: leaseNamespace,
		},
		Recorder:        recorder,
		SubscribeNotify: notify,
		RuntimeID:       os.Getenv("POD_UID"),
	}
	if notify != nil {
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
					select {
					case subscribeEvents <- event.GenericEvent{
						Object: &configv1alpha1.NXOSConfig{
							ObjectMeta: metav1.ObjectMeta{Namespace: "", Name: deviceName},
						},
					}:
					default:
					}
				}
			}
		}()
	}
	if err := r.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("NXOSConfig SetupWithManager: %w", err)
	}
	operationReconciler := &deviceoperation.Reconciler{
		Client:          mgr.GetClient(),
		Reader:          mgr.GetAPIReader(),
		Recorder:        recorder,
		Scheme:          mgr.GetScheme(),
		DeviceName:      deviceName,
		DeviceNamespace: operationNamespace(),
		Platform:        diagnostic.CommandPlatformNXOS,
		TP:              r,
	}
	if err := operationReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("NXOS DeviceOperation SetupWithManager: %w", err)
	}
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
			Platform:           diagnostic.CommandPlatformNXOS,
			BindAddr:           adminAddr,
		}
		stop := make(chan struct{})
		go func() {
			<-ctx.Done()
			close(stop)
		}()
		go func() {
			if err := admSrv.ListenAndServe(stop); err != nil {
				log.G(ctx).WithError(err).Warn("NXOS diagnostic admin server exited")
			}
		}()
	}
	if dctx.Transport == nil {
		recorder.Eventf(&ciskov1.CiscoDevice{
			ObjectMeta: metav1.ObjectMeta{Name: deviceName, Namespace: operationNamespace()},
		}, corev1.EventTypeWarning, "NXOSConfigTransportPending",
			"NXOSConfig transport is not available yet: %v", dErr)
		go retryNXOSConfigDriverDial(ctx, opts, r, dctx)
	}
	go func() {
		if runErr := mgr.Start(ctx); runErr != nil && runErr != context.Canceled {
			log.G(ctx).WithError(runErr).Warn("NXOSConfig manager stopped unexpectedly")
		}
	}()
	return nil
}

func retryNXOSConfigDriverDial(ctx context.Context, opts configReconcilerOptions, r *provider.NXOSConfigReconciler, current *drivers.ConfigDriverContext) {
	const interval = 30 * time.Second
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		next, err := drivers.NewConfigDriver(ctx, opts.Spec, opts.Password, configDriverBuildOptions(opts))
		if err != nil || next == nil || next.Transport == nil {
			log.G(ctx).WithError(err).
				WithField("attempt", attempt).
				Info("NXOSConfig driver dial still failing; will retry")
			continue
		}
		r.SetTransport(next.Transport)
		r.SetDeviceVersion(next.DeviceVersion)
		if current != nil {
			current.Transport = next.Transport
			current.DeviceVersion = next.DeviceVersion
			current.DefaultYANGVersion = next.DefaultYANGVersion
			current.FetchDeviceVersion = next.FetchDeviceVersion
		}
		log.G(ctx).Infof("NXOSConfig driver transport acquired after %d retry attempt(s)", attempt)
		if next.DeviceVersion != "" {
			log.G(ctx).WithField("version", next.DeviceVersion).
				Info("NXOSConfig device version bound after deferred dial success")
		}
		return
	}
}
