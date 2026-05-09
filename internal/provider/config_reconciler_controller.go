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
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/source"

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

// iosxeConfigFinalizer makes the reconciler responsible for releasing
// any family leases this CR holds before Kubernetes deletes the
// object. Without it, the lease coordination object lingers on its
// own TTL after the CR is gone — the next CR claiming the same family
// observes LeaseBlocked even though the original holder no longer
// exists in the cluster. Caught against a live Cat9300 retest where
// successive tests serialised on stale leases.
const iosxeConfigFinalizer = "config.cisco.vk/lease-cleanup"

func containsFinalizer(fs []string, target string) bool {
	for _, f := range fs {
		if f == target {
			return true
		}
	}
	return false
}

func removeFinalizer(fs []string, target string) []string {
	out := fs[:0]
	for _, f := range fs {
		if f != target {
			out = append(out, f)
		}
	}
	return out
}

// releaseLeasesForCR releases every family lease this CR could be
// holding. A non-owner Release is a no-op on the leaser side, so we
// can iterate over spec.managedFamilies without first checking lease
// state — the leaser deletes only leases whose holderIdentity matches
// this reconciler's identity for the CR.
func (r *ConfigReconciler) releaseLeasesForCR(ctx context.Context, cr *configv1alpha1.IOSXEConfig) error {
	if r.Leaser == nil {
		return nil
	}
	identity := cr.Namespace + "/" + cr.Name
	if r.RuntimeID != "" {
		identity = identity + "#" + r.RuntimeID
	}
	for _, fam := range cr.Spec.ManagedFamilies {
		if err := r.Leaser.Release(ctx, r.DeviceName, fam, identity); err != nil {
			return fmt.Errorf("release lease for %s: %w", fam, err)
		}
	}
	return nil
}

// ForceRelinquishSkipAnnotation is the operator-controlled escape
// hatch for delete-time relinquish. When set to "true" on an
// IOSXEConfig that's stuck in Terminating, the controller skips the
// relinquish reconcile entirely, records a Warning event listing the
// owned keys it gave up cleaning up, releases leases, and removes the
// finalizer. Use this when a permanent failure (decommissioned
// device, unsupported family that needs a writer uplift, persistent
// auth failure) makes retry pointless and the operator accepts the
// orphaned device config.
const ForceRelinquishSkipAnnotation = "config.cisco.vk/force-relinquish-skip"

// relinquishOwnedKeys runs a CR-delete reconcile that drops every
// list-key this CR owned (per status.atomicReplaceOwnedKeys) from the
// device. F2 fix (2026-05-01): without this pass, deleting a CR that
// configured e.g. VLAN 4001 leaves 4001 stranded on the device with
// no Kubernetes object to track it.
//
// The implementation reuses the engine's existing pruneOnRelinquish
// path by synthesising a ResolvedIntent with empty desired for each
// managed family. The engine's scope-to-owned helper restricts the
// prune set to {priorOwned ∪ desired} = {priorOwned}, so only this
// CR's owned keys are deleted; baseline state is left alone.
//
// Review follow-ups from 2026-05-02:
//
//	B1 — Use AcquireIfFree, NOT Acquire. The takeover-capable
//	     Acquire would let a deleting CR claim a foreign lease whose
//	     holder hasn't heartbeat through a long Fetch/Apply (looks
//	     expired but is still in flight) — and then prune the in-
//	     flight CR's keys mid-write. AcquireIfFree refuses any
//	     foreign holder regardless of expiry; stale-but-in-flight
//	     recovery stays the responsibility of the normal-reconcile
//	     path that holds and renews its own lease.
//
//	B2 — On failure, release every lease this attempt acquired so
//	     a stuck terminating CR doesn't permanently pin those
//	     families for other CRs. The next retry will Acquire-IfFree
//	     again, so the pattern is "acquire → mutate → release on
//	     success-or-failure" instead of the old "hold until
//	     finalizer removal".
func (r *ConfigReconciler) relinquishOwnedKeys(ctx context.Context, cr *configv1alpha1.IOSXEConfig) (returnedErr error) {
	tr := r.GetTransport()
	if tr == nil {
		return fmt.Errorf("relinquish: transport not yet available")
	}
	lookup := r.Lookup
	if lookup == nil {
		lookup = writers.Get
	}

	// Per-family AcquireIfFree. Only families we successfully claim
	// without takeover are eligible for relinquish; foreign-held
	// families are reported and force the caller to retry. The
	// identity matches the per-tick identity used in reconcileOne so
	// our normal-tick lease (if any) survives the Acquire call.
	identity := cr.Namespace + "/" + cr.Name
	if r.RuntimeID != "" {
		identity = identity + "#" + r.RuntimeID
	}
	owned := make([]string, 0, len(cr.Spec.ManagedFamilies))
	leaseBlocked := make([]string, 0, len(cr.Spec.ManagedFamilies))
	if r.Leaser != nil {
		for _, fam := range cr.Spec.ManagedFamilies {
			res, err := r.Leaser.AcquireIfFree(ctx, r.DeviceName, fam, identity)
			if err != nil {
				// Release whatever we already claimed so other CRs
				// aren't pinned by a partial relinquish attempt.
				r.releaseAcquiredFamilies(ctx, owned, identity)
				return fmt.Errorf("relinquish: acquire lease for %s: %w", fam, err)
			}
			if !res.Owned {
				leaseBlocked = append(leaseBlocked, fam)
				continue
			}
			owned = append(owned, fam)
		}
	} else {
		// Non-leasing topology (single-pod tests). Trust the
		// caller's spec.
		owned = append(owned, cr.Spec.ManagedFamilies...)
	}

	// Defer the lease release so every error path — including
	// engine.Reconcile failures and lease-blocked-tail returns —
	// hands the families back. On success we release too: the CR is
	// about to be GC'd, so holding the lease past relinquish only
	// stalls the next CR.
	defer func() {
		if r.Leaser != nil {
			r.releaseAcquiredFamilies(ctx, owned, identity)
		}
	}()

	if len(owned) == 0 {
		if len(leaseBlocked) > 0 {
			return fmt.Errorf("relinquish: every managed family is held by another CR: %v; "+
				"will retry until the holders release", leaseBlocked)
		}
		// No families to relinquish (CR never reached InSync).
		return nil
	}

	eng := &engine.Engine{
		Transport:   tr,
		Lookup:      lookup,
		FamilyOrder: r.FamilyOrder,
	}
	// Build empty desired for each owned family. coerceList in the
	// writer side accepts a missing/empty entry as "no entries
	// declared", which is what we want — the prune path then deletes
	// every owned key.
	conf := make(map[string]any, len(owned))
	for _, fam := range owned {
		conf[fam] = []any{}
	}
	resIntent := &intent.ResolvedIntent{
		DeviceName:             cr.Spec.DeviceRef.Name,
		ManagedFamilies:        owned,
		Configuration:          conf,
		DriftPolicy:            configv1alpha1.DriftPolicyRevert,
		PruneOnRelinquish:      true,
		AtomicReplaceOwnedKeys: cr.Status.AtomicReplaceOwnedKeys,
	}
	out := eng.Reconcile(ctx, resIntent)
	if out.Phase == engine.PhaseFailed {
		// Surface the first family-level error so the controller
		// log + Event recorder show what to fix before retry.
		for _, fs := range out.FamilyStatuses {
			if fs.State == "ApplyError" || fs.State == "Unsupported" {
				return fmt.Errorf("relinquish reconcile: family %s (%s): %s",
					fs.Name, fs.State, fs.Message)
			}
		}
		return fmt.Errorf("relinquish reconcile failed: phase=%s", out.Phase)
	}
	if len(leaseBlocked) > 0 {
		return fmt.Errorf("relinquish: %d/%d managed families relinquished; "+
			"families still held by another CR: %v; will retry",
			len(owned), len(owned)+len(leaseBlocked), leaseBlocked)
	}
	return nil
}

// releaseAcquiredFamilies releases every lease in `families` held by
// `identity`. Used both on relinquish success (we're done with these
// leases anyway) and on relinquish failure (so a stuck terminating CR
// doesn't pin families for other CRs while it retries). Best-effort:
// any per-family release error is logged, never returned.
func (r *ConfigReconciler) releaseAcquiredFamilies(ctx context.Context, families []string, identity string) {
	if r.Leaser == nil {
		return
	}
	logger := crlog.FromContext(ctx)
	for _, fam := range families {
		if err := r.Leaser.Release(ctx, r.DeviceName, fam, identity); err != nil {
			logger.Error(err, "release lease after relinquish attempt", "family", fam)
		}
	}
}

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
		"cvk.iosxeconfig.reconcile",
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

	// Finalizer + deletion path. On delete, release every family
	// lease this CR could be holding so the next CR claiming the
	// same family does not have to wait for lease TTL. On non-delete
	// reconciles, ensure the finalizer is in place so the deletion
	// handler will run.
	if !cr.GetDeletionTimestamp().IsZero() {
		if containsFinalizer(cr.Finalizers, iosxeConfigFinalizer) {
			// F2 fix (2026-05-01): when pruneOnRelinquish is true and
			// the CR has accumulated ownedKeys in status, run a
			// relinquish reconcile against the device before lease
			// release + finalizer removal. Without this the CR's
			// owned entries stay on the device as orphaned config.
			//
			// A2 fix (2026-05-01, codex/HEAD~1 review): relinquish
			// failure is now retryable. We return the error from
			// Reconcile so controller-runtime requeues, keeping the
			// finalizer in place. status.atomicReplaceOwnedKeys
			// stays available so the next attempt has the same input.
			// Operators with a permanently failing relinquish (e.g.
			// device decommissioned mid-cleanup) can patch the
			// finalizer off explicitly — the standard Kubernetes
			// escape hatch — once they accept the orphan.
			if cr.Spec.PruneOnRelinquish && len(cr.Status.AtomicReplaceOwnedKeys) > 0 {
				// B2 escape hatch (codex /codex:adversarial-review
				// 2026-05-02): an operator who has accepted that
				// relinquish will not succeed — decommissioned device,
				// permanent auth failure, family that needs a writer
				// uplift — sets cisco.vk/force-relinquish-skip=true.
				// We skip the relinquish reconcile, emit a Warning
				// event recording the orphan list, and proceed to
				// finalizer removal. Strictly more controlled than
				// `kubectl patch finalizers: []` because the
				// orphaned keys land in the audit trail.
				if cr.Annotations[ForceRelinquishSkipAnnotation] == "true" {
					if r.Recorder != nil {
						r.Recorder.Eventf(&cr, "Warning", "RelinquishSkipped",
							"force-relinquish-skip annotation set; orphaning %v on device %q",
							cr.Status.AtomicReplaceOwnedKeys, r.DeviceName)
					}
					span.SetAttributes(attribute.String("cisco.vk.reconcile.outcome", "relinquish-skipped"))
				} else if err := r.relinquishOwnedKeys(ctx, &cr); err != nil {
					logger.Error(err, "relinquish owned keys; will retry "+
						"(set annotation "+ForceRelinquishSkipAnnotation+"=true to give up)")
					span.RecordError(err)
					span.SetStatus(codes.Error, "relinquish")
					span.SetAttributes(attribute.String("cisco.vk.reconcile.outcome", "relinquish-blocked"))
					if r.Recorder != nil {
						r.Recorder.Eventf(&cr, "Warning", "RelinquishBlocked",
							"deletion blocked while pruneOnRelinquish cleanup retries: %v "+
								"(set annotation %s=true to give up)",
							err, ForceRelinquishSkipAnnotation)
					}
					return reconcile.Result{}, fmt.Errorf("relinquish: %w", err)
				}
			}
			if err := r.releaseLeasesForCR(ctx, &cr); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "release leases")
				return reconcile.Result{}, fmt.Errorf("release leases: %w", err)
			}
			cr.Finalizers = removeFinalizer(cr.Finalizers, iosxeConfigFinalizer)
			if err := r.Client.Update(ctx, &cr); err != nil {
				if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
					return reconcile.Result{}, nil
				}
				return reconcile.Result{}, fmt.Errorf("remove finalizer: %w", err)
			}
		}
		span.SetAttributes(attribute.String("cisco.vk.reconcile.outcome", "deleted"))
		return reconcile.Result{}, nil
	}
	// Add the finalizer eagerly when missing so a delete that races
	// the first reconcile still hits our cleanup path. Update is
	// best-effort; if the API conflicts we let the next tick re-add
	// it. Continuing into the reconcile keeps the existing per-tick
	// contract — the same tick still produces the status update the
	// caller observes — and matches the unit-test fixtures that run
	// Reconcile once per scenario.
	if r.Leaser != nil && !containsFinalizer(cr.Finalizers, iosxeConfigFinalizer) {
		updated := cr.DeepCopy()
		updated.Finalizers = append(updated.Finalizers, iosxeConfigFinalizer)
		if err := r.Client.Update(ctx, updated); err == nil {
			cr.Finalizers = updated.Finalizers
			cr.ResourceVersion = updated.ResourceVersion
		}
	}

	// Build the same resolver + engine the polling path uses so
	// behaviour stays identical regardless of how we were triggered.
	// Wave 2B (external-review Finding #6): pass SupportedYANGVersions
	// and DefaultYANGVersion through to the resolver. Without these
	// the production controller-runtime path silently accepted any
	// spec.targetYangVersion value and never recorded
	// status.sourceYangVersion, while the polling/aggregator paths
	// did both. The two topologies must agree.
	resolver := &intent.Resolver{
		Client:                r.Client,
		KeyRules:              r.KeyRules,
		SupportedYANGVersions: r.SupportedYANGVersions,
		DefaultYANGVersion:    r.DefaultYANGVersion,
	}
	lookup := r.Lookup
	if lookup == nil {
		lookup = writers.Get
	}
	eng := &engine.Engine{
		Transport:   r.GetTransport(),
		Lookup:      lookup,
		FamilyOrder: r.FamilyOrder,
	}

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
	// Wave 6A — pick the right trigger. If a Subscribe notification
	// fired since this CR's last device-check, the controller-runtime
	// reconcile is in fact a subscribe-driven tick and must bypass
	// the hash short-circuit (so the device-side change is detected
	// even when intent + generation are unchanged). Otherwise this
	// is a normal CR/scope-object event.
	trigger := triggerEvent
	if r.subscribeFiredSince(cr.Status.LastDeviceCheck) {
		trigger = triggerSubscribe
	}
	result, err := r.reconcileOne(ctx, vkLogger, resolver, eng, &cr, conflicts, trigger)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reconcileOne")
		span.SetAttributes(attribute.String("cisco.vk.reconcile.outcome", "error"))
		return reconcile.Result{}, err
	}
	// Phase + drift attribution sourced from the engine result rather
	// than the stale pre-update CR. recordResult writes status via a
	// deep copy and never mutates `cr`, so reading cr.Status here would
	// see the previous tick's phase and miss e.g. PhaseLeaseBlocked
	// just written by this tick. Wave 9.2
	// (external-review-wave8-followup Finding #2).
	span.SetAttributes(
		attribute.String("cisco.vk.reconcile.outcome", "ok"),
		attribute.String("cisco.vk.iosxeconfig.phase", result.Phase),
		attribute.Int("cisco.vk.drift.count", len(result.Drift)),
	)
	// Steady-state drift detection: requeue at the spec'd interval so
	// even an InSync CR is re-checked against the device. controller-
	// runtime's existing on-error backoff still applies on top.
	// External-review Finding #2: without RequeueAfter, the controller-
	// runtime path never re-enters Reconcile until something else
	// (event, scope-object change) wakes it.
	//
	// Wave 8.2: sub-TTL requeue under PhaseLeaseBlocked so the next
	// tick has a fair chance to find the lease available. Default
	// driftDetectInterval (5m) is far longer than the 30s lease TTL;
	// using it for a lease-blocked CR would freeze the reconciler
	// for minutes while the contention window is seconds long.
	//
	// Wave 9.2: phase argument is the just-written result.Phase, not
	// cr.Status.Phase. Pre-fix the requeue read the stale CR copy and
	// used the normal drift interval even on a tick that just wrote
	// LeaseBlocked, defeating Wave 8.2's contention-aware requeue.
	return reconcile.Result{RequeueAfter: requeueIntervalFor(&cr, result.Phase)}, nil
}

// requeueIntervalFor returns the controller-runtime RequeueAfter
// to use after a Reconcile call. Default is the spec'd
// driftDetectInterval; lease-blocked ticks use a sub-TTL value so
// the next tick re-checks while contention is still likely
// resolving. Bounded above 1s so the reconciler never busy-loops.
//
// phase is the just-written result.Phase from reconcileOne, NOT
// cr.Status.Phase — recordResult writes status via a deep copy and
// never mutates the caller's CR object, so reading cr.Status.Phase
// here would see the stale pre-update value.
func requeueIntervalFor(cr *configv1alpha1.IOSXEConfig, phase string) time.Duration {
	full := driftDetectInterval(cr)
	if phase == engine.PhaseLeaseBlocked {
		// Half the default lease TTL (30s) → 15s. Adjust if the
		// engine's FamilyLeaser default TTL changes.
		const leaseBlockedRequeue = 15 * time.Second
		if leaseBlockedRequeue < full {
			return leaseBlockedRequeue
		}
	}
	return full
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

	b := ctrl.NewControllerManagedBy(mgr).
		Named("iosxeconfig-"+r.DeviceName).
		For(&configv1alpha1.IOSXEConfig{}, builder.WithPredicates(devicePredicate)).
		Watches(&configv1alpha1.IOSXEConfigDefaults{}, mapAll).
		Watches(&configv1alpha1.IOSXEDeviceGroupConfig{}, mapAll).
		Watches(&configv1alpha1.IOSXEInterfaceGroupConfig{}, mapAll).
		Watches(&configv1alpha1.IOSXETemplate{}, mapAll).
		Watches(&corev1.ConfigMap{}, mapAll).
		// Wave 2D (external-review Finding #11): Secret rotations that
		// back spec.secretRefs[] must enqueue a reconcile. The mapper
		// is the same broad mapAll the ConfigMap watch uses — every
		// IOSXEConfig in the cluster that targets this device is
		// requeued. The hash short-circuit dedupes ticks where the
		// rotation didn't actually change resolved intent.
		Watches(&corev1.Secret{}, mapAll)

	// Wave 6A (external-review-followup Finding #3): Subscribe
	// fast-path. The cmd/cisco-vk wiring bridges the transport's
	// notify channel into per-CR GenericEvents and hands them to
	// us via SubscribeEvents. Register a source.Channel that
	// enqueues a reconcile request for every event it sees. Reconcile
	// distinguishes subscribe vs CR-event by reading
	// r.subscribeNotifyTime against cr.Status.LastDeviceCheck.
	//
	// Without this watch the per-pod controller-runtime topology
	// (the production default) saw subscribe notifications go
	// nowhere — only the polling Run loop and the aggregator
	// consumed them.
	if r.SubscribeEvents != nil {
		b = b.WatchesRawSource(source.Channel(r.SubscribeEvents,
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, _ client.Object) []reconcile.Request {
				// The bridge fires events without populating the
				// object reference (the Subscribe stream isn't
				// per-CR). Re-use mapAll's broad enumeration:
				// requeue every IOSXEConfig that targets this
				// device. This matches the polling Run loop's
				// behaviour, which also re-enumerates on each
				// notify rather than carrying a CR-specific signal.
				var list configv1alpha1.IOSXEConfigList
				if err := r.Client.List(context.Background(), &list); err != nil {
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
			})))
	}

	return b.Complete(r)
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
