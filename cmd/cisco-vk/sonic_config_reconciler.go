// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"context"
	"fmt"
	"os"

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
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

func startSONICConfigReconciler(ctx context.Context, cfg *rest.Config, deviceName string, opts configReconcilerOptions) error {
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
	recorder := broadcaster.NewRecorder(scheme, corev1.EventSource{Component: "cisco-vk-sonic-config-reconciler", Host: deviceName})

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
		return fmt.Errorf("build SONIC config manager: %w", err)
	}

	applier, err := provider.NewSONICConfigApplier(opts.Spec, opts.Password)
	if err != nil {
		recorder.Eventf(&ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: deviceName, Namespace: operationNamespace()}}, corev1.EventTypeWarning, "SONICConfigApplierFailed", "could not build SONiC config applier: %v", err)
		return err
	}
	r := &provider.SONICConfigReconciler{
		Client:     mgr.GetClient(),
		DeviceName: deviceName,
		Applier:    applier,
		Recorder:   recorder,
	}
	if err := r.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("SONICConfig SetupWithManager: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = applier.Close()
	}()
	go func() {
		if runErr := mgr.Start(ctx); runErr != nil && runErr != context.Canceled {
			log.G(ctx).WithError(runErr).Warn("SONICConfig manager stopped unexpectedly")
		}
	}()
	return nil
}
