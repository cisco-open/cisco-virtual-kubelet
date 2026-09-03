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
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	otelmetric "go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/telemetry"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic/adminserver"
	metricclassifier "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/emit"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	telemetryyang "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/yang"
)

const (
	iosxeTelemetryFinalizer       = "config.cisco.vk/telemetry-cleanup"
	telemetryStatusCadence        = 10 * time.Second
	telemetryStatusBridgeDebounce = time.Second
)

// IOSXETelemetryReconciler watches IOSXETelemetry CRs for one CiscoDevice and
// manages that device's dedicated MDT-over-gNMI subscriber.
type IOSXETelemetryReconciler struct {
	Client     client.Client
	DeviceName string
	// DeviceNamespace is the namespace of the owning CiscoDevice CR. When
	// non-empty the reconciler refuses to act on IOSXETelemetry CRs from
	// any other namespace, even if their spec.deviceRef.name matches.
	// Mirrors the DeviceOperation guard so a tenant who can create the CR
	// in another namespace cannot drive a device pod outside their own.
	DeviceNamespace string
	Factory         telemetry.SubscribeClientFactory

	// OTel emitters are wired through the subscriber's drainEvents pump.
	// LoggerProvider is required for log emission; nil disables it.
	LoggerProvider otellog.LoggerProvider
	// MeterProvider is required for metric emission and telemetry self-metrics;
	// nil disables them via the MetricsEmitter noop fallback.
	MeterProvider otelmetric.MeterProvider
	// TracerProvider is required for transition trace spans; nil uses the
	// TracesEmitter noop fallback.
	TracerProvider oteltrace.TracerProvider
	// ResourceAttrs are added to every mapped event's resource (alongside
	// the per-CR Mapping.ResourceAttributes pinned leaves).
	ResourceAttrs map[string]string
	// StateCache receives MDT-derived app/interface/topology facts. It is a
	// read-through cache: emitters and notifiers can use it for freshness, but
	// driver calls remain authoritative.
	StateCache *state.Cache
	// AppEventConsumer receives app-hosting state events from the mapper. The
	// Virtual Kubelet provider uses this to wake its PodNotifier bridge.
	AppEventConsumer state.AppEventConsumer
	// CorrelationCache maps MDT app IDs back to the VK admission trace context
	// that created them, allowing recovery spans to nest under the causal trace.
	CorrelationCache *correlation.Cache
	// YangRegistry enables YANG-driven metric classification when configured.
	// Nil preserves the curated classifier behavior.
	YangRegistry *telemetryyang.Registry

	RootContext     context.Context
	StatusEvents    chan event.GenericEvent
	ChannelCapacity int

	mu          sync.Mutex
	subscriber  *telemetry.Subscriber
	owned       map[client.ObjectKey][]string
	bridgeStop  context.CancelFunc
	selfMetrics *emit.SelfMetrics

	legacyLogWarnings   map[client.ObjectKey]string
	payloadBudgetLimits map[client.ObjectKey]int
}

func (r *IOSXETelemetryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.DeviceName == "" {
		return fmt.Errorf("IOSXETelemetryReconciler: empty DeviceName")
	}
	devicePredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return r.telemetryTargetsThisDevice(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return r.telemetryTargetsThisDevice(e.ObjectNew) ||
				r.telemetryTargetsThisDevice(e.ObjectOld)
		},
		DeleteFunc:  func(e event.DeleteEvent) bool { return r.telemetryTargetsThisDevice(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return r.telemetryTargetsThisDevice(e.Object) },
	}

	mapAll := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
		var list configv1alpha1.IOSXETelemetryList
		listOpts := []client.ListOption{}
		if r.DeviceNamespace != "" {
			listOpts = append(listOpts, client.InNamespace(r.DeviceNamespace))
		}
		if err := r.Client.List(ctx, &list, listOpts...); err != nil {
			crlog.FromContext(ctx).Error(err, "mapAll list IOSXETelemetry")
			return nil
		}
		out := make([]reconcile.Request, 0, len(list.Items))
		for _, cr := range list.Items {
			if !r.telemetryTargetsThisDevice(&cr) {
				continue
			}
			out = append(out, reconcile.Request{
				NamespacedName: client.ObjectKey{Namespace: cr.Namespace, Name: cr.Name},
			})
		}
		return out
	})

	b := ctrl.NewControllerManagedBy(mgr).
		Named("iosxetelemetry-"+r.DeviceName).
		For(&configv1alpha1.IOSXETelemetry{}, builder.WithPredicates(devicePredicate))
	if r.StatusEvents != nil {
		b = b.WatchesRawSource(source.Channel(r.StatusEvents, mapAll))
	}
	return b.Complete(r)
}

func (r *IOSXETelemetryReconciler) Reconcile(ctx context.Context, req reconcile.Request) (result reconcile.Result, retErr error) {
	var cr configv1alpha1.IOSXETelemetry
	if err := r.Client.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			r.removeOwned(req.NamespacedName)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get IOSXETelemetry: %w", err)
	}

	if cr.Spec.DeviceRef.Name != r.DeviceName {
		// CR was retargeted away from this device. Drop any subscriptions
		// this reconciler still owns for it; otherwise the on-device
		// subscriptions and stream bookkeeping leak until the per-device
		// pod restarts.
		r.removeOwned(req.NamespacedName)
		return reconcile.Result{}, nil
	}
	if r.DeviceNamespace != "" && cr.Namespace != r.DeviceNamespace {
		// Cross-namespace tenancy boundary: refuse to act on a CR that
		// names this device but lives in a different namespace from the
		// CiscoDevice CR. Drop any state we may have accidentally
		// accumulated before the predicate caught up.
		r.removeOwned(req.NamespacedName)
		return reconcile.Result{}, nil
	}

	ctx, _ = correlation.ApplyAnnotations(ctx, cr.Annotations, time.Now())
	tracerProvider := r.TracerProvider
	if tracerProvider == nil {
		tracerProvider = otel.GetTracerProvider()
	}
	ctx, span := correlation.Start(ctx,
		tracerProvider.Tracer("cisco-virtual-kubelet/iosxetelemetry-reconciler"),
		"cvk.iosxetelemetry.reconcile",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(
			attribute.String("cisco.vk.device.name", r.DeviceName),
			attribute.String("k8s.namespace.name", cr.Namespace),
			attribute.String("k8s.resource.name", cr.Name),
			attribute.String("k8s.resource.kind", "IOSXETelemetry"),
		),
	)
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, "reconcile")
		}
		span.End()
	}()

	if !cr.GetDeletionTimestamp().IsZero() {
		r.removeOwned(req.NamespacedName)
		if containsFinalizer(cr.Finalizers, iosxeTelemetryFinalizer) {
			cr.Finalizers = removeFinalizer(cr.Finalizers, iosxeTelemetryFinalizer)
			if err := r.Client.Update(ctx, &cr); err != nil && !apierrors.IsNotFound(err) {
				return reconcile.Result{}, fmt.Errorf("remove telemetry finalizer: %w", err)
			}
		}
		return reconcile.Result{}, nil
	}

	if !containsFinalizer(cr.Finalizers, iosxeTelemetryFinalizer) {
		updated := cr.DeepCopy()
		updated.Finalizers = append(updated.Finalizers, iosxeTelemetryFinalizer)
		if err := r.Client.Update(ctx, updated); err == nil {
			cr.Finalizers = updated.Finalizers
			cr.ResourceVersion = updated.ResourceVersion
		} else if !apierrors.IsConflict(err) {
			return reconcile.Result{}, fmt.Errorf("add telemetry finalizer: %w", err)
		}
	}

	if err := configv1alpha1.ValidateIOSXETelemetry(&cr); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "InvalidSpec")
		span.SetAttributes(attribute.String("cvk.iosxetelemetry.phase", string(configv1alpha1.IOSXETelemetryPhaseFailed)))
		base := cr.DeepCopy()
		cr.Status.Phase = configv1alpha1.IOSXETelemetryPhaseFailed
		r.setReady(&cr, metav1.ConditionFalse, "InvalidSpec", err.Error())
		if updateErr := r.patchTelemetryStatus(ctx, base, &cr); updateErr != nil {
			return statusPatchResult(updateErr)
		}
		return reconcile.Result{}, nil
	}
	r.warnLegacyLogOutput(ctx, req.NamespacedName, cr.Spec.Output.Logs.LegacyMode)

	sub, err := r.ensureSubscriber()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "subscriber unavailable")
		base := cr.DeepCopy()
		cr.Status.Phase = configv1alpha1.IOSXETelemetryPhaseDegraded
		r.setReady(&cr, metav1.ConditionFalse, "SubscriberUnavailable", err.Error())
		if updateErr := r.patchTelemetryStatus(ctx, base, &cr); updateErr != nil {
			return statusPatchResult(updateErr)
		}
		return reconcile.Result{RequeueAfter: telemetryStatusCadence}, nil
	}

	reconnect := defaultReconnect(cr.Spec.Reconnect)
	sub.SetReconnectConfig(reconnect)
	budgets := configv1alpha1.DefaultBudgetConfig(cr.Spec.Budgets)
	r.updatePayloadBudgetLimit(req.NamespacedName, budgets.MaxPayloadBytesPerMinute)
	profile := telemetry.MappingProfile{
		Mapping:           cr.Spec.Mapping,
		Classifier:        telemetryClassifier(cr.Spec.Mapping, r.YangRegistry),
		Output:            cr.Spec.Output,
		Budgets:           budgets,
		CardinalityLimits: cr.Spec.CardinalityLimits,
		Timestamps:        cr.Spec.Timestamps,
	}
	for _, desired := range cr.Spec.Subscriptions {
		sub.SetSubscriptionProfile(telemetrySubscriptionOwnerKey(req.NamespacedName, desired.Name), profile)
	}
	activeNames, allNames, err := r.applyDesired(req.NamespacedName, sub, &cr)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "subscription configuration")
		base := cr.DeepCopy()
		cr.Status.Phase = configv1alpha1.IOSXETelemetryPhaseFailed
		r.setReady(&cr, metav1.ConditionFalse, "SubscribeConfigError", err.Error())
		if updateErr := r.patchTelemetryStatus(ctx, base, &cr); updateErr != nil {
			return statusPatchResult(updateErr)
		}
		return reconcile.Result{RequeueAfter: telemetryStatusCadence}, nil
	}

	base := cr.DeepCopy()
	phase, states := sub.StatusFor(allNames)
	states = telemetrySubscriptionStatusNames(req.NamespacedName, states)
	if len(activeNames) == 0 {
		phase = configv1alpha1.IOSXETelemetryPhasePending
	}
	cr.Status.Phase = phase
	span.SetAttributes(attribute.String("cvk.iosxetelemetry.phase", string(phase)))
	cr.Status.ObservedSubscriptionState = states
	switch phase {
	case configv1alpha1.IOSXETelemetryPhaseStreaming:
		span.SetStatus(codes.Ok, "")
		r.setReady(&cr, metav1.ConditionTrue, "Streaming", "telemetry subscription streams are active")
	case configv1alpha1.IOSXETelemetryPhaseFailed:
		span.SetStatus(codes.Error, "telemetry streams failed")
		r.setReady(&cr, metav1.ConditionFalse, "Failed", "one or more telemetry streams failed")
	case configv1alpha1.IOSXETelemetryPhaseDegraded:
		span.SetStatus(codes.Error, "telemetry streams degraded")
		r.setReady(&cr, metav1.ConditionFalse, "Degraded", "one or more telemetry streams are reconnecting")
	default:
		r.setReady(&cr, metav1.ConditionFalse, "Pending", "telemetry subscription streams are not active yet")
	}
	r.setInstrumentCapCondition(&cr)
	r.setBudgetCondition(&cr)
	if err := r.patchTelemetryStatus(ctx, base, &cr); err != nil {
		return statusPatchResult(err)
	}
	return reconcile.Result{RequeueAfter: telemetryStatusCadence}, nil
}

func (r *IOSXETelemetryReconciler) patchTelemetryStatus(
	ctx context.Context,
	base *configv1alpha1.IOSXETelemetry,
	updated *configv1alpha1.IOSXETelemetry,
) error {
	return r.Client.Status().Patch(ctx, updated, client.MergeFrom(base))
}

func statusPatchResult(err error) (reconcile.Result, error) {
	if apierrors.IsConflict(err) {
		return reconcile.Result{Requeue: true}, nil
	}
	return reconcile.Result{}, fmt.Errorf("status patch IOSXETelemetry: %w", err)
}

// TelemetryHealthSnapshot returns the per-subscription health summary that
// backs the diagnostic adminserver's GET /telemetry/health endpoint. It
// projects the subscriber's current StatusFor view onto the JSON shape.
func (r *IOSXETelemetryReconciler) TelemetryHealthSnapshot() adminserver.TelemetryHealth {
	r.mu.Lock()
	sub := r.subscriber
	owned := make(map[client.ObjectKey][]string, len(r.owned))
	for k, names := range r.owned {
		owned[k] = append([]string(nil), names...)
	}
	r.mu.Unlock()
	out := adminserver.TelemetryHealth{Device: r.DeviceName}
	if sub == nil {
		return out
	}
	seen := map[string]bool{}
	for _, names := range owned {
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			phase, states := sub.StatusFor([]string{name})
			if len(states) == 0 {
				continue
			}
			st := states[0]
			out.Subscriptions = append(out.Subscriptions, adminserver.TelemetrySubscriptionHealth{
				Name:                st.Name,
				Phase:               phase,
				MessagesReceived:    st.MessagesReceived,
				LogRecordsEmitted:   st.LogRecordsEmitted,
				MetricPointsEmitted: st.MetricPointsEmitted,
				Reconnects:          st.Reconnects,
				StreamID:            st.StreamID,
				LastError:           st.LastError,
				CurrentBackoff:      st.CurrentBackoff.Duration.String(),
			})
		}
	}
	sort.Slice(out.Subscriptions, func(i, j int) bool {
		return out.Subscriptions[i].Name < out.Subscriptions[j].Name
	})
	return out
}

// subscriberResourceAttrs returns the per-device resource attributes that the
// Subscriber pins onto every mapped event. The reconciler-level attrs
// override / extend whatever the spec-level Mapping.ResourceAttributes sets.
func (r *IOSXETelemetryReconciler) subscriberResourceAttrs() map[string]string {
	out := map[string]string{
		"cisco.device.name": r.DeviceName,
	}
	for k, v := range r.ResourceAttrs {
		out[k] = v
	}
	return out
}

func (r *IOSXETelemetryReconciler) ensureSubscriber() (*telemetry.Subscriber, error) {
	r.mu.Lock()
	if r.subscriber != nil {
		sub := r.subscriber
		r.mu.Unlock()
		return sub, nil
	}
	root := r.RootContext
	if root == nil {
		root = context.Background()
	}
	// The subscriber is shared across IOSXETelemetry objects and outlives every
	// reconcile. Never pin one object's lifecycle context on this singleton:
	// later streams may belong to a different run. The bounded setup work is
	// represented by the reconcile span above; long-lived events retain their
	// existing per-device/subscription attribution.
	if r.selfMetrics == nil {
		r.selfMetrics = emit.NewSelfMetrics(r.MeterProvider)
	}
	selfMetrics := r.selfMetrics
	metricsEmitter := emit.NewMetricsEmitter(r.MeterProvider, emit.WithMetricsSelfMetrics(selfMetrics))
	opts := []telemetry.SubscriberOption{
		telemetry.WithLogger(crlog.Log.WithName("iosxetelemetry").WithValues("device", r.DeviceName)),
		telemetry.WithChannelCapacity(r.ChannelCapacity),
		telemetry.WithMapper(mapper.New()),
		telemetry.WithLogsEmitter(emit.NewLogsEmitter(r.LoggerProvider, emit.WithLogsSelfMetrics(selfMetrics))),
		telemetry.WithMetricsEmitter(metricsEmitter),
		telemetry.WithTracesEmitter(emit.NewTracesEmitter(r.TracerProvider, r.MeterProvider, nil).WithSelfMetrics(selfMetrics)),
		telemetry.WithSelfMetrics(selfMetrics),
	}
	if r.StateCache != nil {
		opts = append(opts, telemetry.WithStateCache(r.StateCache))
	}
	if r.AppEventConsumer != nil {
		opts = append(opts, telemetry.WithAppEventConsumer(r.AppEventConsumer))
	}
	if r.CorrelationCache != nil {
		opts = append(opts, telemetry.WithCorrelationCache(r.CorrelationCache))
	}
	if attrs := r.subscriberResourceAttrs(); len(attrs) > 0 {
		opts = append(opts, telemetry.WithResourceAttributes(attrs))
	}
	sub := telemetry.NewSubscriber(r.DeviceName, r.Factory, opts...)
	r.subscriber = sub
	if r.owned == nil {
		r.owned = map[client.ObjectKey][]string{}
	}
	r.mu.Unlock()

	if err := sub.Start(root); err != nil {
		r.mu.Lock()
		if r.subscriber == sub {
			r.subscriber = nil
		}
		r.mu.Unlock()
		return nil, err
	}
	r.startStateBridge(root, sub)
	return sub, nil
}

func telemetryClassifier(mapping *configv1alpha1.MappingConfig, registry *telemetryyang.Registry) metricclassifier.Classifier {
	base := metricclassifier.CuratedClassifier()
	if registry != nil {
		base = telemetryyang.NewClassifier(registry, base)
	}
	if mapping == nil || len(mapping.MetricTypeOverrides) == 0 {
		return base
	}
	return metricclassifier.OverrideClassifier(mapping.MetricTypeOverrides, base)
}

func (r *IOSXETelemetryReconciler) startStateBridge(root context.Context, sub *telemetry.Subscriber) {
	if r.StatusEvents == nil {
		return
	}
	ctx, cancel := context.WithCancel(root)
	r.mu.Lock()
	if r.bridgeStop != nil {
		r.bridgeStop()
	}
	r.bridgeStop = cancel
	r.mu.Unlock()
	go func() {
		var lastEmit time.Time
		var trailing bool
		var timer *time.Timer
		var timerC <-chan time.Time
		stopTimer := func() {
			if timer == nil {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer = nil
			timerC = nil
		}
		armTrailing := func() {
			trailing = true
			delay := time.Until(lastEmit.Add(telemetryStatusBridgeDebounce))
			if delay < 0 {
				delay = 0
			}
			if timer == nil {
				timer = time.NewTimer(delay)
				timerC = timer.C
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)
			timerC = timer.C
		}
		emit := func() {
			select {
			case r.StatusEvents <- event.GenericEvent{
				Object: &configv1alpha1.IOSXETelemetry{
					ObjectMeta: metav1.ObjectMeta{Name: r.DeviceName},
				},
			}:
			default:
			}
			lastEmit = time.Now()
		}
		defer stopTimer()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-sub.StateChanged():
				if !ok {
					return
				}
				if !lastEmit.IsZero() && time.Now().Before(lastEmit.Add(telemetryStatusBridgeDebounce)) {
					armTrailing()
					continue
				}
				trailing = false
				stopTimer()
				emit()
			case <-timerC:
				timer = nil
				timerC = nil
				if trailing {
					trailing = false
					emit()
				}
			}
		}
	}()
}

func (r *IOSXETelemetryReconciler) applyDesired(
	key client.ObjectKey,
	sub *telemetry.Subscriber,
	cr *configv1alpha1.IOSXETelemetry,
) ([]string, []string, error) {
	active := make([]string, 0, len(cr.Spec.Subscriptions))
	all := make([]string, 0, len(cr.Spec.Subscriptions))
	for _, desired := range cr.Spec.Subscriptions {
		spec := defaultSubscription(desired)
		ownerName := telemetrySubscriptionOwnerKey(key, spec.Name)
		all = append(all, ownerName)
		spec.Name = ownerName
		if spec.Enabled != nil && !*spec.Enabled {
			sub.RemoveSubscription(ownerName)
			sub.RemoveSubscriptionProfile(ownerName)
			continue
		}
		if err := sub.AddSubscription(spec); err != nil {
			return nil, nil, err
		}
		active = append(active, ownerName)
	}
	sort.Strings(active)
	sort.Strings(all)

	r.mu.Lock()
	if r.owned == nil {
		r.owned = map[client.ObjectKey][]string{}
	}
	previous := append([]string(nil), r.owned[key]...)
	r.owned[key] = append([]string(nil), active...)
	r.mu.Unlock()

	activeSet := map[string]struct{}{}
	for _, name := range active {
		activeSet[name] = struct{}{}
	}
	for _, name := range previous {
		if _, ok := activeSet[name]; !ok {
			sub.RemoveSubscription(name)
			sub.RemoveSubscriptionProfile(name)
		}
	}
	return active, all, nil
}

func telemetrySubscriptionOwnerKey(key client.ObjectKey, subscriptionName string) string {
	return key.Namespace + "/" + key.Name + "/" + subscriptionName
}

func telemetrySubscriptionStatusNames(
	key client.ObjectKey,
	states []configv1alpha1.ObservedSubscriptionState,
) []configv1alpha1.ObservedSubscriptionState {
	prefix := key.Namespace + "/" + key.Name + "/"
	for i := range states {
		states[i].Name = strings.TrimPrefix(states[i].Name, prefix)
	}
	return states
}

func (r *IOSXETelemetryReconciler) removeOwned(key client.ObjectKey) {
	r.mu.Lock()
	delete(r.legacyLogWarnings, key)
	delete(r.payloadBudgetLimits, key)
	effectivePayloadBudget := r.effectivePayloadBudgetLimitLocked()
	if r.owned == nil {
		r.mu.Unlock()
		emit.SetPayloadByteBudgetLimit(r.DeviceName, effectivePayloadBudget)
		return
	}
	names := append([]string(nil), r.owned[key]...)
	delete(r.owned, key)
	sub := r.subscriber
	remaining := 0
	for _, owned := range r.owned {
		remaining += len(owned)
	}
	bridgeStop := r.bridgeStop
	if remaining == 0 {
		r.subscriber = nil
		r.bridgeStop = nil
	}
	r.mu.Unlock()
	emit.SetPayloadByteBudgetLimit(r.DeviceName, effectivePayloadBudget)

	for _, name := range names {
		if sub != nil {
			sub.RemoveSubscription(name)
			sub.RemoveSubscriptionProfile(name)
		}
	}
	if remaining == 0 && sub != nil {
		if bridgeStop != nil {
			bridgeStop()
		}
		sub.Stop()
	}
}

func (r *IOSXETelemetryReconciler) setReady(
	cr *configv1alpha1.IOSXETelemetry,
	status metav1.ConditionStatus,
	reason string,
	message string,
) {
	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cr.Generation,
		LastTransitionTime: metav1.Now(),
	})
}

// setInstrumentCapCondition surfaces the cumulative instrument-cap drop count
// from the shared SelfMetrics. Once any cap drop is recorded, the
// InstrumentCapExceeded condition flips to True so operators see it without
// scraping the OTel pipeline.
func (r *IOSXETelemetryReconciler) setInstrumentCapCondition(cr *configv1alpha1.IOSXETelemetry) {
	if r.selfMetrics == nil {
		return
	}
	dropped := r.selfMetrics.CapDropTotal()
	cond := metav1.Condition{
		Type:               "InstrumentCapExceeded",
		ObservedGeneration: cr.Generation,
		LastTransitionTime: metav1.Now(),
	}
	if dropped > 0 {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "MetricInstrumentsDropped"
		cond.Message = fmt.Sprintf("metric points dropped because the instrument cap was reached (cumulative=%d); raise spec.cardinalityLimits.maxInstruments", dropped)
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "WithinCap"
		cond.Message = "metric instrument count is within the configured cap"
	}
	meta.SetStatusCondition(&cr.Status.Conditions, cond)
}

func (r *IOSXETelemetryReconciler) setBudgetCondition(cr *configv1alpha1.IOSXETelemetry) {
	samples := emit.BudgetDropSnapshotForDevice(r.DeviceName)
	cond := metav1.Condition{
		Type:               "BudgetExceeded",
		ObservedGeneration: cr.Generation,
		LastTransitionTime: metav1.Now(),
	}
	if len(samples) == 0 {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "WithinBudget"
		cond.Message = "signal emission is within the configured budgets"
		meta.SetStatusCondition(&cr.Status.Conditions, cond)
		return
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].Signal == samples[j].Signal {
			return samples[i].Reason < samples[j].Reason
		}
		return samples[i].Signal < samples[j].Signal
	})
	reason := "SignalBudgetExceeded"
	switch {
	case hasBudgetReason(samples, "rate_limit_log_records"):
		reason = "LogRecordRateBudgetExceeded"
	case hasBudgetReason(samples, "rate_limit_payload_bytes"):
		reason = "PayloadByteBudgetExceeded"
	}
	total := int64(0)
	for _, sample := range samples {
		total += sample.Count
	}
	cond.Status = metav1.ConditionTrue
	cond.Reason = reason
	cond.Message = fmt.Sprintf("signal records dropped after budget enforcement (cumulative=%d)", total)
	meta.SetStatusCondition(&cr.Status.Conditions, cond)
}

func hasBudgetReason(samples []emit.BudgetDropSample, reason string) bool {
	for _, sample := range samples {
		if sample.Reason == reason {
			return true
		}
	}
	return false
}

func (r *IOSXETelemetryReconciler) warnLegacyLogOutput(ctx context.Context, key client.ObjectKey, mode string) {
	if mode == "" {
		return
	}
	r.mu.Lock()
	if r.legacyLogWarnings == nil {
		r.legacyLogWarnings = map[client.ObjectKey]string{}
	}
	if r.legacyLogWarnings[key] == mode {
		r.mu.Unlock()
		return
	}
	r.legacyLogWarnings[key] = mode
	r.mu.Unlock()
	crlog.FromContext(ctx).Info(
		"deprecated IOSXETelemetry output.logs compatibility mode",
		"iosxetelemetry", key.Namespace+"/"+key.Name,
		"mode", mode,
	)
}

func (r *IOSXETelemetryReconciler) updatePayloadBudgetLimit(key client.ObjectKey, limit int) {
	if limit <= 0 {
		return
	}
	r.mu.Lock()
	if r.payloadBudgetLimits == nil {
		r.payloadBudgetLimits = map[client.ObjectKey]int{}
	}
	r.payloadBudgetLimits[key] = limit
	effective := r.effectivePayloadBudgetLimitLocked()
	r.mu.Unlock()
	emit.SetPayloadByteBudgetLimit(r.DeviceName, effective)
}

func (r *IOSXETelemetryReconciler) effectivePayloadBudgetLimitLocked() int {
	defaultLimit := configv1alpha1.DefaultBudgetConfig(nil).MaxPayloadBytesPerMinute
	if len(r.payloadBudgetLimits) == 0 {
		return defaultLimit
	}
	effective := 0
	for _, limit := range r.payloadBudgetLimits {
		if limit <= 0 {
			continue
		}
		if effective == 0 || limit < effective {
			effective = limit
		}
	}
	if effective <= 0 {
		return defaultLimit
	}
	return effective
}

func defaultReconnect(in *configv1alpha1.ReconnectConfig) *configv1alpha1.ReconnectConfig {
	out := &configv1alpha1.ReconnectConfig{
		InitialBackoff: metav1.Duration{Duration: time.Second},
		MaxBackoff:     metav1.Duration{Duration: 30 * time.Second},
	}
	if in == nil {
		return out
	}
	*out = *in
	if out.InitialBackoff.Duration == 0 {
		out.InitialBackoff = metav1.Duration{Duration: time.Second}
	}
	if out.MaxBackoff.Duration == 0 {
		out.MaxBackoff = metav1.Duration{Duration: 30 * time.Second}
	}
	return out
}

func defaultSubscription(in configv1alpha1.TelemetrySubscription) configv1alpha1.TelemetrySubscription {
	out := in
	if out.Enabled == nil {
		v := true
		out.Enabled = &v
	}
	if out.Encoding == "" {
		out.Encoding = configv1alpha1.TelemetryEncodingProto
	}
	return out
}

// telemetryTargetsThisDevice returns true only when obj is an
// IOSXETelemetry CR whose spec targets this reconciler's device AND
// (when DeviceNamespace is configured) lives in the same namespace as
// the device. Cross-namespace name collisions are explicitly rejected
// to prevent a tenant in namespace A from steering a device that lives
// in namespace B.
func (r *IOSXETelemetryReconciler) telemetryTargetsThisDevice(obj client.Object) bool {
	cr, ok := obj.(*configv1alpha1.IOSXETelemetry)
	if !ok {
		return false
	}
	if cr.Spec.DeviceRef.Name != r.DeviceName {
		return false
	}
	if r.DeviceNamespace != "" && cr.Namespace != r.DeviceNamespace {
		return false
	}
	return true
}
