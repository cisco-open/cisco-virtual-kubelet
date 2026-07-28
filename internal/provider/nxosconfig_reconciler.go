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

package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/validation"
	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	"k8s.io/client-go/tools/record"
)

const nxosConfigFinalizer = "config.cisco.vk/nxos-lease-cleanup"

// NXOSConfigReconciler is the per-device NX-OS facade over the common
// platform config reconciler.
//
// It builds ONE CommonConfigReconciler at first use and registers that
// same instance with the manager. Earlier this facade rebuilt a fresh
// CommonConfigReconciler on every call, so SetupWithManager registered a
// throwaway while deferred-dial SetTransport/SetDeviceVersion mutated only
// the facade — a device that was down at startup never recovered. All
// runtime state (transport, version, subscribe clock) now lives on the one
// registered common instance, and the facade methods forward to it.
type NXOSConfigReconciler struct {
	Client                  client.Client
	DeviceName              string
	DeviceNamespace         string
	Transport               transport.Interface
	Lookup                  func(family, release string) enginewriters.SectionWriter
	FamilyOrder             func([]string) []string
	DeviceVersion           string
	DefaultYANGVersion      string
	SupportedYANGVersions   map[string]struct{}
	FetchDeviceVersion      func(context.Context, transport.Interface) string
	ValidateDeviceVersion   enginewriters.VersionValidator
	IsUnsupportedVersion    enginewriters.VersionErrorClassifier
	ReleaseTagForVersion    func(string) (string, bool)
	RequireDeviceVersion    bool
	OperationValidator      validation.Validator
	OperationValidationMode validation.Mode
	Leaser                  *engine.FamilyLeaser
	Recorder                record.EventRecorder
	Interval                time.Duration
	RuntimeID               string

	SubscribeNotify <-chan struct{}
	SubscribeEvents <-chan event.GenericEvent

	commonOnce       sync.Once
	commonReconciler *CommonConfigReconciler
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
	r.common().NotifySubscribeFired()
}

func (r *NXOSConfigReconciler) SetTransport(t transport.Interface) {
	r.common().SetTransport(t)
}

func (r *NXOSConfigReconciler) GetTransport() transport.Interface {
	return r.common().GetTransport()
}

func (r *NXOSConfigReconciler) SetDeviceVersion(version string) {
	r.common().SetDeviceVersion(version)
}

func (r *NXOSConfigReconciler) SetDefaultYANGVersion(version string) {
	r.common().SetDefaultYANGVersion(version)
}

// common returns the single CommonConfigReconciler backing this facade,
// constructing it once from the facade's initial configuration.
func (r *NXOSConfigReconciler) common() *CommonConfigReconciler {
	r.commonOnce.Do(func() {
		r.commonReconciler = &CommonConfigReconciler{
			Client:                  r.Client,
			DeviceName:              r.DeviceName,
			DeviceNamespace:         r.DeviceNamespace,
			Transport:               r.Transport,
			Lookup:                  r.Lookup,
			FamilyOrder:             r.FamilyOrder,
			DeviceVersion:           r.DeviceVersion,
			DefaultYANGVersion:      r.DefaultYANGVersion,
			SupportedYANGVersions:   r.SupportedYANGVersions,
			FetchDeviceVersion:      r.FetchDeviceVersion,
			ValidateDeviceVersion:   r.ValidateDeviceVersion,
			IsUnsupportedVersion:    r.IsUnsupportedVersion,
			ReleaseTagForVersion:    r.ReleaseTagForVersion,
			RequireDeviceVersion:    r.RequireDeviceVersion,
			OperationValidator:      r.OperationValidator,
			OperationValidationMode: r.OperationValidationMode,
			Leaser:                  r.Leaser,
			Recorder:                r.Recorder,
			Interval:                r.Interval,
			RuntimeID:               r.RuntimeID,
			SubscribeNotify:         r.SubscribeNotify,
			SubscribeEvents:         r.SubscribeEvents,
			Platform:                NXOSCommonConfigPlatform(),
		}
	})
	return r.commonReconciler
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
		ReconcilePolicy: engine.ReconcilePolicy{
			StopOnRevertFailure: true,
		},
		ValidateModelSource:     validateNXOSNetAsCodeModelSource,
		ValidateModelDevicePair: validateNXOSModelDevicePair,
		ValidateTargetVersion:   validateNXOSTargetVersion,
		ValidateResolvedSource:  validateResolvedNXOSNetAsCodeSource,
		NormalizeSource:         normalizeNXOSNetAsCodeSource,
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
