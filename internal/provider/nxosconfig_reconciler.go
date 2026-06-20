// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package provider

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	"k8s.io/client-go/tools/record"
)

const nxosConfigFinalizer = "config.cisco.vk/nxos-lease-cleanup"

// NXOSConfigReconciler is the per-device NX-OS facade over the common
// platform config reconciler.
type NXOSConfigReconciler struct {
	Client                client.Client
	DeviceName            string
	Transport             transport.Interface
	Lookup                func(family, release string) enginewriters.SectionWriter
	FamilyOrder           func([]string) []string
	DeviceVersion         string
	DefaultYANGVersion    string
	SupportedYANGVersions map[string]struct{}
	Leaser                *engine.FamilyLeaser
	Recorder              record.EventRecorder
	Interval              time.Duration
	RuntimeID             string

	SubscribeNotify <-chan struct{}
	SubscribeEvents <-chan event.GenericEvent

	transportSlot       atomic.Pointer[transport.Interface]
	versionMu           sync.RWMutex
	subscribeNotifyTime atomic.Int64
}

func (r *NXOSConfigReconciler) Run(ctx context.Context) error {
	return r.common().Run(ctx)
}

func (r *NXOSConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return r.common().SetupWithManager(mgr)
}

func (r *NXOSConfigReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	return r.common().Reconcile(ctx, req)
}

func (r *NXOSConfigReconciler) NotifySubscribeFired() {
	r.subscribeNotifyTime.Store(time.Now().UnixNano())
}

func (r *NXOSConfigReconciler) SetTransport(t transport.Interface) {
	if t == nil {
		r.transportSlot.Store(nil)
		return
	}
	r.transportSlot.Store(&t)
}

func (r *NXOSConfigReconciler) GetTransport() transport.Interface {
	if p := r.transportSlot.Load(); p != nil {
		return *p
	}
	return r.Transport
}

func (r *NXOSConfigReconciler) SetDeviceVersion(version string) {
	r.versionMu.Lock()
	defer r.versionMu.Unlock()
	r.DeviceVersion = version
}

func (r *NXOSConfigReconciler) deviceVersion() string {
	r.versionMu.RLock()
	defer r.versionMu.RUnlock()
	return r.DeviceVersion
}

func (r *NXOSConfigReconciler) common() *CommonConfigReconciler {
	return &CommonConfigReconciler{
		Client:                r.Client,
		DeviceName:            r.DeviceName,
		Transport:             r.GetTransport(),
		Lookup:                r.Lookup,
		FamilyOrder:           r.FamilyOrder,
		DeviceVersion:         r.deviceVersion(),
		DefaultYANGVersion:    r.DefaultYANGVersion,
		SupportedYANGVersions: r.SupportedYANGVersions,
		Leaser:                r.Leaser,
		Recorder:              r.Recorder,
		Interval:              r.Interval,
		RuntimeID:             r.RuntimeID,
		SubscribeNotify:       r.SubscribeNotify,
		SubscribeEvents:       r.SubscribeEvents,
		SubscribeNotifyTime:   &r.subscribeNotifyTime,
		Platform:              NXOSCommonConfigPlatform(),
	}
}

// NXOSCommonConfigPlatform describes NXOSConfig for the shared common
// spec/status reconciler.
func NXOSCommonConfigPlatform() CommonConfigPlatform {
	return CommonConfigPlatform{
		Name:           "nxos",
		Kind:           "NXOSConfig",
		ControllerName: "nxosconfig",
		SourceEnvelope: "nxos",
		ModelFormat:    configv1alpha1.NetAsCodeModelFormatNXOS,
		SupportedFamilies: append([]string(nil),
			nxosschema.Families...,
		),
		Finalizer:        nxosConfigFinalizer,
		PreserveEnvelope: true,
		NormalizeSource:  normalizeNXOSNetAsCodeSource,
		NewObject: func() client.Object {
			return &configv1alpha1.NXOSConfig{}
		},
		NewList: func() client.ObjectList {
			return &configv1alpha1.NXOSConfigList{}
		},
		Items: func(list client.ObjectList) []client.Object {
			typed, ok := list.(*configv1alpha1.NXOSConfigList)
			if !ok {
				return nil
			}
			out := make([]client.Object, 0, len(typed.Items))
			for i := range typed.Items {
				out = append(out, &typed.Items[i])
			}
			return out
		},
		Spec: func(obj client.Object) *configv1alpha1.CommonConfigSpec {
			typed, ok := obj.(*configv1alpha1.NXOSConfig)
			if !ok {
				return nil
			}
			return (*configv1alpha1.CommonConfigSpec)(&typed.Spec)
		},
		Status: func(obj client.Object) *configv1alpha1.CommonConfigStatus {
			typed, ok := obj.(*configv1alpha1.NXOSConfig)
			if !ok {
				panic(fmt.Sprintf("NXOSConfig platform adapter got %T", obj))
			}
			return (*configv1alpha1.CommonConfigStatus)(&typed.Status)
		},
	}
}
