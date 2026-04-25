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

	vklog "github.com/virtual-kubelet/virtual-kubelet/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// reconcileTracerName is the instrumentation name used for the root
// per-tick span. Kept here so all spans emitted by the config
// reconciler share one name and downstream OTel collectors can route
// them by instrumentation library.
const reconcileTracerName = "cisco-virtual-kubelet/config-reconciler"

// Reconcile implements reconcile.Reconciler. It is the hot-path entry
// for the controller-runtime-driven path (see SetupWithManager); the
// legacy polling entry in Run() uses the same inner reconcileOne so
// both paths behave identically.
//
// Event handling:
//   - IOSXEConfig events matching this device fire directly.
//   - IOSXEConfigDefaults / IOSXEDeviceGroupConfig / IOSXETemplate /
//     referenced ConfigMap events are mapped via handler.Funcs to the
//     IOSXEConfigs they influence, so a scope-object mutation triggers
//     targeted re-reconciles rather than a full resync.
func (r *ConfigReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	// Per-tick reconcile span. When no OTel TracerProvider is wired
	// (the unit-test default), this resolves to a no-op tracer and
	// adds zero overhead. When the topology exporter is configured,
	// the span lands on the same OTLP collector with full apply-time
	// attribution and per-CR identity attributes — strictly better
	// than the existing histogram + event pair for tracing single
	// reconcile attempts end to end.
	ctx, span := otel.Tracer(reconcileTracerName).Start(
		ctx,
		"ConfigReconciler.Reconcile",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(
			attribute.String("cisco.vk.device.name", r.DeviceName),
			attribute.String("cisco.vk.iosxeconfig.namespace", req.Namespace),
			attribute.String("cisco.vk.iosxeconfig.name", req.Name),
		),
	)
	defer span.End()

	logger := crlog.FromContext(ctx).
		WithValues("component", "config-reconciler", "device", r.DeviceName)

	var cr configv1alpha1.IOSXEConfig
	if err := r.Client.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			// Informer cache may still hold a deleted CR for a brief
			// window; treating NotFound as a no-op is correct because
			// owner-ref cleanup (if any) is handled elsewhere and our
			// status writes are unreachable anyway.
			span.SetAttributes(attribute.String("cisco.vk.reconcile.outcome", "not-found"))
			return reconcile.Result{}, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get IOSXEConfig")
		return reconcile.Result{}, fmt.Errorf("get IOSXEConfig: %w", err)
	}

	// Defence in depth: a CR reaching us that targets a different
	// device is ignored. In production the predicate filter below
	// prevents this entirely; the check stays because the polling
	// Run() path does not install a predicate.
	if cr.Spec.DeviceRef.Name != r.DeviceName {
		span.SetAttributes(attribute.String("cisco.vk.reconcile.outcome", "wrong-device"))
		return reconcile.Result{}, nil
	}

	// Build the same resolver + engine the polling path uses so
	// behaviour stays identical regardless of how we were triggered.
	resolver := &intent.Resolver{Client: r.Client, KeyRules: r.KeyRules}
	lookup := r.Lookup
	if lookup == nil {
		lookup = writers.Get
	}
	eng := &engine.Engine{Transport: r.Transport, Lookup: lookup}

	// Compute conflicts across every CR targeting this device. Listing
	// is cheap when backed by an informer cache.
	var all configv1alpha1.IOSXEConfigList
	if err := r.Client.List(ctx, &all); err != nil {
		logger.Error(err, "list IOSXEConfig")
	}
	forDevice := make([]*configv1alpha1.IOSXEConfig, 0, len(all.Items))
	for i := range all.Items {
		if all.Items[i].Spec.DeviceRef.Name == r.DeviceName {
			forDevice = append(forDevice, &all.Items[i])
		}
	}
	conflicts := engine.ConflictCheck(r.DeviceName, forDevice)

	// reconcileOne expects a virtual-kubelet logger (the polling path's
	// convention). Bridge from the controller-runtime logger by using
	// the ctx-bound VK logger; both end up going to the same sink in
	// cisco-vk run and the VK logger is the one reconcileOne already
	// threads through to driver/engine calls.
	vkLogger := vklog.G(ctx).
		WithField("component", "config-reconciler").
		WithField("device", r.DeviceName)
	_ = logger // silence unused when VK logging is the chosen path
	span.SetAttributes(
		attribute.Int("cisco.vk.iosxeconfig.cohort_size", len(forDevice)),
		attribute.Int("cisco.vk.managed_families.count", len(cr.Spec.ManagedFamilies)),
		attribute.String("cisco.vk.drift_policy", string(cr.Spec.DriftPolicy)),
	)
	if err := r.reconcileOne(ctx, vkLogger, resolver, eng, &cr, conflicts, triggerEvent); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reconcileOne")
		span.SetAttributes(attribute.String("cisco.vk.reconcile.outcome", "error"))
		return reconcile.Result{}, err
	}
	// Post-reconcile status fields are written into cr.Status during
	// reconcileOne; surface the rolled-up phase as a span attribute so
	// trace consumers can pivot on it without having to also pull the
	// status subresource.
	span.SetAttributes(
		attribute.String("cisco.vk.reconcile.outcome", "ok"),
		attribute.String("cisco.vk.iosxeconfig.phase", cr.Status.Phase),
		attribute.Int("cisco.vk.drift.count", len(cr.Status.Drift)),
	)
	// Steady-state drift detection: requeue at the spec'd interval so
	// even an InSync CR is re-checked against the device. controller-
	// runtime's existing on-error backoff still applies on top.
	// External-review Finding #2: without RequeueAfter, the controller-
	// runtime path never re-enters Reconcile until something else
	// (event, scope-object change) wakes it.
	return reconcile.Result{RequeueAfter: driftDetectInterval(&cr)}, nil
}

// SetupWithManager wires the reconciler into mgr with device-scoped
// predicates and handler.Funcs that map scope-object changes to the
// IOSXEConfigs they influence. Use this in cisco-vk run; the polling
// Run() method is preserved for unit tests that don't stand up a
// full manager.
func (r *ConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.DeviceName == "" {
		return fmt.Errorf("SetupWithManager: empty DeviceName")
	}

	// Predicate for the primary watch — only IOSXEConfigs targeting
	// this device ever enter the informer's work queue.
	devicePredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return crTargetsDevice(e.Object, r.DeviceName) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return crTargetsDevice(e.ObjectNew, r.DeviceName) ||
				crTargetsDevice(e.ObjectOld, r.DeviceName)
		},
		DeleteFunc:  func(e event.DeleteEvent) bool { return crTargetsDevice(e.Object, r.DeviceName) },
		GenericFunc: func(e event.GenericEvent) bool { return crTargetsDevice(e.Object, r.DeviceName) },
	}

	// Map scope objects (Defaults, DeviceGroup, Template) and any
	// ConfigMap to the IOSXEConfigs they might influence for this
	// device. Keep the mapping broad: an unnecessary reconcile is
	// cheap thanks to the canonical-hash short-circuit.
	mapAll := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var list configv1alpha1.IOSXEConfigList
		if err := r.Client.List(ctx, &list); err != nil {
			crlog.FromContext(ctx).Error(err, "mapAll list IOSXEConfig")
			return nil
		}
		out := make([]reconcile.Request, 0, len(list.Items))
		for _, cr := range list.Items {
			if cr.Spec.DeviceRef.Name != r.DeviceName {
				continue
			}
			out = append(out, reconcile.Request{
				NamespacedName: client.ObjectKey{Namespace: cr.Namespace, Name: cr.Name},
			})
		}
		return out
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named("iosxeconfig-"+r.DeviceName).
		For(&configv1alpha1.IOSXEConfig{}, builder.WithPredicates(devicePredicate)).
		Watches(&configv1alpha1.IOSXEConfigDefaults{}, mapAll).
		Watches(&configv1alpha1.IOSXEDeviceGroupConfig{}, mapAll).
		Watches(&configv1alpha1.IOSXEInterfaceGroupConfig{}, mapAll).
		Watches(&configv1alpha1.IOSXETemplate{}, mapAll).
		Watches(&corev1.ConfigMap{}, mapAll).
		Complete(r)
}

// crTargetsDevice returns true when obj is an IOSXEConfig whose
// spec.deviceRef.name matches name. Non-IOSXEConfig objects or a
// missing deviceRef are reported as false so the predicate filters
// them out — scope objects reach the reconciler via the mapAll
// handler.Funcs, not the primary watch.
func crTargetsDevice(obj client.Object, name string) bool {
	cr, ok := obj.(*configv1alpha1.IOSXEConfig)
	if !ok {
		return false
	}
	return cr.Spec.DeviceRef.Name == name
}
