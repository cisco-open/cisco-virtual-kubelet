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
	"errors"
	"fmt"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// defaultConfigReconcileInterval is the poll cadence for listing
// IOSXEConfig CRs. Phase-1 still polls; Phase-2 swaps in an informer.
const defaultConfigReconcileInterval = 5 * time.Second

// ConfigReconciler is the outer per-device loop that resolves
// IOSXEConfig CRs targeting its device and dispatches each to the
// engine. It owns the informer-less polling, conflict reporting, and
// status writes; the engine owns per-family state machine execution.
type ConfigReconciler struct {
	// Client is a controller-runtime client already wired to a scheme
	// that has config.cisco.vk/v1alpha1 and cisco.vk/v1alpha1 registered.
	Client client.Client

	// DeviceName is the CiscoDevice this cisco-vk run owns.
	DeviceName string

	// Transport is the device channel used by the engine. Nil is allowed
	// in Phase 0/1 scaffolds where no transport has been constructed
	// (e.g. stub driver path) — the reconciler records status but skips
	// device I/O.
	Transport transport.Interface

	// Interval is the poll cadence; zero means defaultConfigReconcileInterval.
	Interval time.Duration

	// KeyRules carries the family-aware path → key-field map. Typically
	// assembled at startup from schema/families.yaml.
	KeyRules intent.KeyRules

	// Lookup overrides the writer lookup for tests. Nil means the
	// process-global writers registry.
	Lookup func(family string) writers.SectionWriter

	// Leaser serialises per-family writes across IOSXEConfig CRs
	// targeting the same device. Nil means advisory-only conflict
	// reporting (the Phase-1 default behaviour).
	Leaser *engine.FamilyLeaser

	// Recorder emits Kubernetes events on the reconciled IOSXEConfig.
	// Nil is allowed — the reconciler silently skips event emission so
	// tests and in-process reconcilers that do not have an event
	// broadcaster continue to work.
	Recorder record.EventRecorder
}

// Run blocks until ctx is cancelled. It returns ctx.Err() on exit.
func (r *ConfigReconciler) Run(ctx context.Context) error {
	if r.Client == nil {
		return errors.New("ConfigReconciler: nil Client")
	}
	if r.DeviceName == "" {
		return errors.New("ConfigReconciler: empty DeviceName")
	}

	interval := r.Interval
	if interval <= 0 {
		interval = defaultConfigReconcileInterval
	}

	logger := log.G(ctx).
		WithField("component", "config-reconciler").
		WithField("device", r.DeviceName)
	logger.WithField("interval", interval).Info("starting IOSXEConfig reconcile loop")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run one pass immediately so status appears before the first tick.
	r.reconcileAll(ctx, logger)

	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping IOSXEConfig reconcile loop")
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx, logger)
		}
	}
}

// reconcileAll lists every IOSXEConfig in the cluster, filters to this
// device, reports family-overlap conflicts on status, and dispatches
// each matching CR through the resolver + engine.
func (r *ConfigReconciler) reconcileAll(ctx context.Context, logger log.Logger) {
	var list configv1alpha1.IOSXEConfigList
	if err := r.Client.List(ctx, &list); err != nil {
		logger.WithError(err).Warn("list IOSXEConfig failed; skipping tick")
		return
	}

	forDevice := make([]*configv1alpha1.IOSXEConfig, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].Spec.DeviceRef.Name == r.DeviceName {
			forDevice = append(forDevice, &list.Items[i])
		}
	}
	conflicts := engine.ConflictCheck(r.DeviceName, forDevice)

	resolver := &intent.Resolver{Client: r.Client, KeyRules: r.KeyRules}
	lookup := r.Lookup
	if lookup == nil {
		lookup = writers.Get
	}
	eng := &engine.Engine{Transport: r.Transport, Lookup: lookup}

	for _, cr := range forDevice {
		if err := r.reconcileOne(ctx, logger, resolver, eng, cr, conflicts); err != nil {
			logger.WithError(err).
				WithField("name", cr.Name).
				WithField("namespace", cr.Namespace).
				Warn("reconcile IOSXEConfig failed")
		}
	}
}

// reconcileOne executes one CR's tick: resolve intent → run engine →
// write status. Any transient failure (resource-conflict, list failure)
// is logged and swallowed; the next tick retries.
func (r *ConfigReconciler) reconcileOne(
	ctx context.Context,
	logger log.Logger,
	resolver *intent.Resolver,
	eng *engine.Engine,
	cr *configv1alpha1.IOSXEConfig,
	conflicts map[string][]string,
) error {
	resolved, err := resolver.Resolve(ctx, cr)
	if err != nil {
		return r.recordFailure(ctx, cr, fmt.Sprintf("resolve: %v", err))
	}

	// Hash-based short-circuit: if the CR's generation matches what the
	// driver last acted on AND the canonical intent hash is unchanged,
	// there is nothing to do. This keeps steady-state cost near zero.
	h, err := intent.CanonicalHash(resolved)
	if err != nil {
		return r.recordFailure(ctx, cr, fmt.Sprintf("hash: %v", err))
	}
	if cr.Status.ObservedGeneration == cr.Generation &&
		cr.Status.LastAppliedHash == h &&
		cr.Status.Phase == engine.PhaseInSync {
		// Optimise the hot path: nothing to do.
		return nil
	}

	// If the transport is not yet wired (scaffold / stub path), record
	// Pending and return — the engine cannot run without it, and we
	// prefer a clear "waiting for transport" state over a spurious
	// Failed.
	if r.Transport == nil {
		return r.recordPending(ctx, cr)
	}

	// Acquire per-family leases before running the engine. Families we
	// fail to lock are dropped from the intent's ManagedFamilies and
	// recorded as Conflict in the per-family status so the operator
	// sees the contention immediately.
	leasedIntent, leaseConflicts := r.acquireLeases(ctx, resolved, cr)
	result := eng.Reconcile(ctx, leasedIntent)
	for family, holder := range leaseConflicts {
		result.FamilyStatuses = append(result.FamilyStatuses, engine.FamilyStatus{
			Name:    family,
			State:   "Skipped",
			Message: fmt.Sprintf("family leased by %q", holder),
		})
	}
	return r.recordResult(ctx, cr, result, h, conflicts)
}

// acquireLeases filters resolved.ManagedFamilies to the ones this CR
// owns the lease for. When Leaser is nil the filter is pass-through —
// the advisory ConflictCheck on status still surfaces overlap.
func (r *ConfigReconciler) acquireLeases(
	ctx context.Context,
	resolved *intent.ResolvedIntent,
	cr *configv1alpha1.IOSXEConfig,
) (*intent.ResolvedIntent, map[string]string) {
	if r.Leaser == nil {
		return resolved, nil
	}

	identity := cr.Namespace + "/" + cr.Name
	owned := make([]string, 0, len(resolved.ManagedFamilies))
	conflicts := map[string]string{}
	for _, family := range resolved.ManagedFamilies {
		res, err := r.Leaser.Acquire(ctx, r.DeviceName, family, identity)
		if err != nil {
			// Lease backend error — treat as not-owned so we do not
			// interleave with another holder. Will retry next tick.
			conflicts[family] = fmt.Sprintf("lease error: %v", err)
			continue
		}
		if !res.Owned {
			conflicts[family] = res.Holder
			continue
		}
		owned = append(owned, family)
	}

	// Shallow copy so we do not mutate the resolver's output, which a
	// caller might cache.
	filtered := *resolved
	filtered.ManagedFamilies = owned
	return &filtered, conflicts
}

// recordPending is the Phase-0 fallback when no transport is wired.
func (r *ConfigReconciler) recordPending(ctx context.Context, cr *configv1alpha1.IOSXEConfig) error {
	if cr.Status.Phase == engine.PhasePending && cr.Status.ObservedGeneration == cr.Generation {
		return nil
	}
	if r.Recorder != nil {
		// Emit once per transition, not every tick.
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "NoTransport",
			"config driver has no device transport configured (scaffold)")
	}
	updated := cr.DeepCopy()
	updated.Status.Phase = engine.PhasePending
	updated.Status.ObservedGeneration = cr.Generation
	setCondition(&updated.Status, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "NoTransport",
		Message: "config driver has no device transport configured (scaffold)",
	})
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

// recordFailure writes a Failed phase with the supplied message and
// returns the original error unwrapped so the caller's log captures it.
func (r *ConfigReconciler) recordFailure(ctx context.Context, cr *configv1alpha1.IOSXEConfig, msg string) error {
	updated := cr.DeepCopy()
	updated.Status.Phase = engine.PhaseFailed
	updated.Status.ObservedGeneration = cr.Generation
	setCondition(&updated.Status, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "ReconcileFailed",
		Message: msg,
	})
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

// recordResult serialises an engine.Result into the CR's status and
// emits Kubernetes events describing the tick. It also writes the
// per-family list, current drift, the hash, and a Conflict condition
// if the CR shares a family with another CR.
func (r *ConfigReconciler) recordResult(
	ctx context.Context,
	cr *configv1alpha1.IOSXEConfig,
	result engine.Result,
	hash string,
	conflicts map[string][]string,
) error {
	r.emitEvents(cr, result)
	updated := cr.DeepCopy()
	updated.Status.Phase = result.Phase
	updated.Status.ObservedGeneration = cr.Generation
	if result.Phase == engine.PhaseInSync {
		now := metav1.Now()
		updated.Status.LastAppliedHash = hash
		updated.Status.LastAppliedTime = &now
	}

	updated.Status.FamilyStatus = updated.Status.FamilyStatus[:0]
	for _, fs := range result.FamilyStatuses {
		updated.Status.FamilyStatus = append(updated.Status.FamilyStatus,
			configv1alpha1.FamilyStatus{
				Name:    fs.Name,
				State:   fs.State,
				Entries: fs.Entries,
				Message: fs.Message,
			})
	}

	updated.Status.Drift = updated.Status.Drift[:0]
	capped, dropped := engine.CapDrift(result.Drift)
	for _, d := range capped {
		updated.Status.Drift = append(updated.Status.Drift, configv1alpha1.DriftEntry{
			Family:   d.Family,
			Path:     d.Path,
			Desired:  d.Desired,
			Observed: d.Observed,
			Detected: metav1.Now(),
		})
	}
	if dropped > 0 {
		engine.RecordDriftTruncated(cr.Spec.DeviceRef.Name, dropped)
	}

	readyStatus := metav1.ConditionTrue
	readyReason := "Succeeded"
	readyMsg := "device reconciled to declared intent"
	if result.Phase != engine.PhaseInSync {
		readyStatus = metav1.ConditionFalse
		readyReason = result.Phase
		if result.Err != nil {
			readyMsg = result.Err.Error()
		} else {
			readyMsg = "not in sync"
		}
	}
	setCondition(&updated.Status, metav1.Condition{
		Type: "Ready", Status: readyStatus, Reason: readyReason, Message: readyMsg,
	})

	if owners, overlaps := conflicts[familiesKey(cr)]; overlaps {
		setCondition(&updated.Status, metav1.Condition{
			Type:    "Conflict",
			Status:  metav1.ConditionTrue,
			Reason:  "FamilyOverlap",
			Message: fmt.Sprintf("overlaps with %v", owners),
		})
	} else {
		setCondition(&updated.Status, metav1.Condition{
			Type: "Conflict", Status: metav1.ConditionFalse,
			Reason:  "NoOverlap",
			Message: "no other CR claims this CR's managed families",
		})
	}

	if err := ignoreConflict(r.Client.Status().Update(ctx, updated)); err != nil {
		return err
	}

	// Audit log: append a row to any IOSXEConfigApplyLog CR that
	// targets this device. The lookup is by spec.deviceRef.name; if
	// no log exists, we silently skip — auditing is opt-in to keep
	// the controller's blast radius narrow.
	if err := r.appendApplyLog(ctx, cr, result, hash); err != nil {
		// Don't fail the reconcile when the log update fails — the
		// device-side state is authoritative, and a transient log
		// CR conflict (status race, intermittent etcd) shouldn't
		// take the device's apply down with it. Surface the error
		// as an event instead.
		if r.Recorder != nil {
			r.Recorder.Eventf(cr, "Warning", "ApplyLogUpdateFailed",
				"could not append apply-log entry: %v", err)
		}
	}
	return nil
}

// familiesKey returns a value usable to look up this CR in a conflict
// map. The map in engine.ConflictCheck is keyed by family, so we use
// the first family as a quick probe — a CR that overlaps on any family
// still shows up as "conflicted" through the detailed condition message.
func familiesKey(cr *configv1alpha1.IOSXEConfig) string {
	if len(cr.Spec.ManagedFamilies) == 0 {
		return ""
	}
	return cr.Spec.ManagedFamilies[0]
}

// setCondition is a tiny, allocation-light upsert: the API server's
// merge-patch logic would also work, but the status.Update path
// replaces the whole slice anyway.
func setCondition(status *configv1alpha1.IOSXEConfigStatus, c metav1.Condition) {
	if c.LastTransitionTime.IsZero() {
		c.LastTransitionTime = metav1.Now()
	}
	for i := range status.Conditions {
		if status.Conditions[i].Type == c.Type {
			if status.Conditions[i].Status == c.Status {
				// Preserve the transition timestamp across ticks with the
				// same status so UIs don't flap.
				c.LastTransitionTime = status.Conditions[i].LastTransitionTime
			}
			status.Conditions[i] = c
			return
		}
	}
	status.Conditions = append(status.Conditions, c)
}

// ignoreConflict drops IsConflict errors because the next tick will
// re-read fresh state and succeed.
func ignoreConflict(err error) error {
	if err == nil || apierrors.IsConflict(err) {
		return nil
	}
	return err
}

// emitEvents produces the per-tick Kubernetes event stream. Emission
// order:
//   1. A per-family event for every non-InSync family (Warning if
//      ApplyError / Unsupported, Normal for Drifted / Skipped).
//   2. A terminal Normal/Warning event describing the overall phase.
//
// Event reason strings are drawn from a closed set so operators can
// filter deterministically: AppliedSuccess, ApplyFailed, DriftDetected,
// FamilyUnsupported, FamilySkipped, Paused, NoTransport, ReconcileFailed.
//
// The recorder is nil-tolerant: tests and callers that don't attach a
// broadcaster see a no-op.
func (r *ConfigReconciler) emitEvents(cr *configv1alpha1.IOSXEConfig, result engine.Result) {
	if r.Recorder == nil {
		return
	}
	for _, fs := range result.FamilyStatuses {
		switch fs.State {
		case "InSync":
			// No per-family event for the steady-state case; the
			// terminal event below captures a clean tick.
		case "Drifted":
			r.Recorder.Eventf(cr, corev1.EventTypeNormal, "DriftDetected",
				"family %q drifted: %s", fs.Name, fs.Message)
		case "ApplyError":
			r.Recorder.Eventf(cr, corev1.EventTypeWarning, "ApplyFailed",
				"family %q: %s", fs.Name, fs.Message)
		case "Unsupported":
			r.Recorder.Eventf(cr, corev1.EventTypeWarning, "FamilyUnsupported",
				"family %q: %s", fs.Name, fs.Message)
		case "Skipped":
			r.Recorder.Eventf(cr, corev1.EventTypeNormal, "FamilySkipped",
				"family %q: %s", fs.Name, fs.Message)
		}
	}

	switch result.Phase {
	case engine.PhaseInSync:
		// Only emit a success event when work was done, to avoid
		// filling the event stream with no-op ticks.
		var applied int
		for _, fs := range result.FamilyStatuses {
			if fs.OpCount > 0 {
				applied++
			}
		}
		if applied > 0 {
			r.Recorder.Eventf(cr, corev1.EventTypeNormal, "AppliedSuccess",
				"applied %d family change(s) successfully", applied)
		}
	case engine.PhaseFailed:
		msg := "reconcile failed"
		if result.Err != nil {
			msg = result.Err.Error()
		}
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "ReconcileFailed", "%s", msg)
	case engine.PhasePaused:
		r.Recorder.Eventf(cr, corev1.EventTypeNormal, "Paused",
			"driftPolicy=pause; reconcile suspended")
	}
}

// appendApplyLog appends a row to every IOSXEConfigApplyLog CR
// targeting cr.Spec.DeviceRef.Name in the same namespace as the
// IOSXEConfig. Auditing is opt-in: a device with no log CR sees
// the function return nil after a list that found nothing. The
// circular-buffer trim happens here, capped at spec.maxEntries.
func (r *ConfigReconciler) appendApplyLog(
	ctx context.Context,
	cr *configv1alpha1.IOSXEConfig,
	result engine.Result,
	hash string,
) error {
	var logs configv1alpha1.IOSXEConfigApplyLogList
	if err := r.Client.List(ctx, &logs, client.InNamespace(cr.Namespace)); err != nil {
		return fmt.Errorf("list apply logs: %w", err)
	}
	entry := buildApplyLogEntry(cr, result, hash)
	for i := range logs.Items {
		log := &logs.Items[i]
		if log.Spec.DeviceRef.Name != cr.Spec.DeviceRef.Name {
			continue
		}
		updated := log.DeepCopy()
		max := int(updated.Spec.MaxEntries)
		if max <= 0 {
			max = 50
		}
		updated.Status.Entries = append(updated.Status.Entries, entry)
		if len(updated.Status.Entries) > max {
			over := len(updated.Status.Entries) - max
			updated.Status.Entries = updated.Status.Entries[over:]
			updated.Status.TruncatedTotal += int64(over)
		}
		if len(updated.Status.Entries) > 0 {
			oldest := updated.Status.Entries[0].Time
			updated.Status.OldestRetainedAt = &oldest
		}
		if err := r.Client.Status().Update(ctx, updated); err != nil {
			return fmt.Errorf("update apply log %s/%s: %w",
				updated.Namespace, updated.Name, err)
		}
	}
	return nil
}

// buildApplyLogEntry compresses an engine.Result into the
// audit-friendly ApplyLogEntry shape. Hash is only set when the
// reconcile reached InSync — a failed apply doesn't pin a new
// intent, and storing the would-be hash there would be misleading.
func buildApplyLogEntry(
	cr *configv1alpha1.IOSXEConfig,
	result engine.Result,
	hash string,
) configv1alpha1.ApplyLogEntry {
	families := make([]configv1alpha1.FamilyApplyOutcome, 0, len(result.FamilyStatuses))
	for _, fs := range result.FamilyStatuses {
		families = append(families, configv1alpha1.FamilyApplyOutcome{
			Family:  fs.Name,
			State:   fs.State,
			OpCount: int32(fs.OpCount),
		})
	}
	entry := configv1alpha1.ApplyLogEntry{
		Time:     metav1.Now(),
		Phase:    result.Phase,
		SourceCR: fmt.Sprintf("%s/%s@%d", cr.Namespace, cr.Name, cr.Generation),
		Families: families,
	}
	if result.Phase == engine.PhaseInSync {
		entry.Hash = hash
	}
	if result.Err != nil {
		entry.Message = result.Err.Error()
	}
	return entry
}
