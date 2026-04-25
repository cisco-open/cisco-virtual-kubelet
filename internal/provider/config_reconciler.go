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
	"encoding/json"
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

	// SupportedYANGVersions is the closed set of release tags
	// `spec.targetYangVersion` may name. Empty disables validation.
	// Wired from `schema/yang-versions.yaml` at process start.
	SupportedYANGVersions map[string]struct{}

	// DefaultYANGVersion is what the resolver assigns when the CR
	// doesn't pin one. Empty leaves status.sourceYangVersion empty
	// — the same shape it had pre-Phase-7.
	DefaultYANGVersion string

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

	// SubscribeNotify is an optional fast-path drift signal. When the
	// transport implements transport.SubscribeCapable (gNMI today),
	// the device-side change watcher writes to this channel; the
	// reconciler picks it up alongside the periodic ticker so an
	// out-of-band write is detected within milliseconds rather than
	// at the next tick. Nil means polling-only behaviour.
	SubscribeNotify <-chan struct{}
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
	r.reconcileAll(ctx, logger, triggerPoll)

	// Subscribe-driven fast path. Pulling the channel into a local
	// nil-able value lets the select compile cleanly even when no
	// watcher is configured.
	notify := r.SubscribeNotify
	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping IOSXEConfig reconcile loop")
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx, logger, triggerPoll)
		case <-notify:
			logger.Debug("subscribe-notify fired; running off-cycle reconcile")
			r.reconcileAll(ctx, logger, triggerSubscribe)
		}
	}
}

// reconcileAll lists every IOSXEConfig in the cluster, filters to this
// device, reports family-overlap conflicts on status, and dispatches
// each matching CR through the resolver + engine. The trigger is
// forwarded to reconcileOne so a subscribe-driven tick bypasses the
// hash short-circuit; a periodic tick respects it.
func (r *ConfigReconciler) reconcileAll(ctx context.Context, logger log.Logger, trigger reconcileTrigger) {
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
	eng := &engine.Engine{Transport: r.Transport, Lookup: lookup}

	for _, cr := range forDevice {
		if err := r.reconcileOne(ctx, logger, resolver, eng, cr, conflicts, trigger); err != nil {
			logger.WithError(err).
				WithField("name", cr.Name).
				WithField("namespace", cr.Namespace).
				Warn("reconcile IOSXEConfig failed")
		}
	}
}

// reconcileTrigger conveys to reconcileOne why this tick is running:
// a normal periodic poll, a subscribe-driven fast-path notification, or
// a controller-runtime CR event. The trigger participates in the hash
// short-circuit decision — a subscribe trigger always bypasses the
// short-circuit because the device just told us something changed.
type reconcileTrigger uint8

const (
	triggerEvent     reconcileTrigger = iota // CR Create/Update/Delete or scope-object event
	triggerPoll                              // periodic ticker (Run() polling path)
	triggerSubscribe                         // gNMI Subscribe / equivalent fast-path
)

// minDriftDetectInterval is the floor for spec.driftDetectInterval. A
// CR that asks for less is clamped silently — we don't want a
// misconfigured CR to hammer the device every second. A 30s floor is
// short enough for any realistic drift-correction SLO and long enough
// that the device's RESTCONF/NETCONF stack is comfortable.
const minDriftDetectInterval = 30 * time.Second

// defaultDriftDetectInterval is the fallback when spec.driftDetectInterval
// is empty or unparseable. Matches the kubebuilder default in the CRD.
const defaultDriftDetectInterval = 5 * time.Minute

// driftDetectInterval parses the CR's spec.driftDetectInterval into a
// Go duration, clamping below the floor and falling back to the
// default on parse error. Centralised so the reconciler and the
// controller-runtime requeue path agree on the interval for one CR.
func driftDetectInterval(cr *configv1alpha1.IOSXEConfig) time.Duration {
	if cr.Spec.DriftDetectInterval == "" {
		return defaultDriftDetectInterval
	}
	d, err := time.ParseDuration(cr.Spec.DriftDetectInterval)
	if err != nil {
		return defaultDriftDetectInterval
	}
	if d < minDriftDetectInterval {
		return minDriftDetectInterval
	}
	return d
}

// dueForDriftCheck reports whether the CR's status.lastDeviceCheck is
// older than spec.driftDetectInterval, which means the next reconcile
// must bypass the hash short-circuit and actually fetch from the
// device. A nil LastDeviceCheck (never reconciled, or migration from
// a pre-LastDeviceCheck status) returns true so the first tick after
// upgrade always reads the device.
func dueForDriftCheck(cr *configv1alpha1.IOSXEConfig) bool {
	if cr.Status.LastDeviceCheck == nil {
		return true
	}
	return time.Since(cr.Status.LastDeviceCheck.Time) >= driftDetectInterval(cr)
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
	trigger reconcileTrigger,
) error {
	resolved, err := resolver.Resolve(ctx, cr)
	if err != nil {
		return r.recordFailure(ctx, cr, fmt.Sprintf("resolve: %v", err))
	}

	// Replay annotation (Phase 7 time-travel): when the CR carries
	// `config.cisco.vk/replay-from-log: <log-name>:<index-or-hash>`,
	// the resolver's normal output is overridden by the body stored
	// on the named ApplyLogEntry. The reconcile then runs as if the
	// operator had authored the historical intent, the device
	// converges, and the annotation is removed by the same status
	// update that records the apply.
	replayed, applied, err := r.applyReplayAnnotation(ctx, cr, resolved)
	if err != nil {
		return r.recordFailure(ctx, cr, fmt.Sprintf("replay: %v", err))
	}
	if applied {
		resolved = replayed
	}

	// Hash-based short-circuit: if the CR's generation matches what the
	// driver last acted on AND the canonical intent hash is unchanged,
	// there is nothing to do. This keeps steady-state cost near zero.
	// Replay paths skip the short-circuit unconditionally — the
	// operator asked for the work even when the hash matches.
	h, err := intent.CanonicalHash(resolved)
	if err != nil {
		return r.recordFailure(ctx, cr, fmt.Sprintf("hash: %v", err))
	}
	// The hash short-circuit fires only when ALL of these hold:
	//   - the operator did NOT request a replay,
	//   - generation + hash + InSync match (intent unchanged), AND
	//   - we are NOT due for a periodic drift check, AND
	//   - this tick was not triggered by a subscribe notification.
	//
	// External-review Finding #2: the previous condition omitted the
	// last two clauses, so steady-state drift detection was bypassed
	// after the first clean apply and Subscribe events re-entered the
	// short-circuit. Splitting "intent freshness" (hash) from "device
	// freshness" (LastDeviceCheck) is the fix.
	if !applied &&
		trigger != triggerSubscribe &&
		cr.Status.ObservedGeneration == cr.Generation &&
		cr.Status.LastAppliedHash == h &&
		cr.Status.Phase == engine.PhaseInSync &&
		!dueForDriftCheck(cr) {
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
	return r.recordResult(ctx, cr, result, h, conflicts, resolved)
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
	resolved *intent.ResolvedIntent,
) error {
	r.emitEvents(cr, result)
	updated := cr.DeepCopy()
	updated.Status.Phase = result.Phase
	updated.Status.ObservedGeneration = cr.Generation
	// LastDeviceCheck is bumped on every recordResult call because by
	// definition we only get here after the engine ran (i.e., after
	// Fetch+Diff+optionally Apply happened against the device). This
	// is the "device freshness" timestamp distinct from
	// LastAppliedTime — the latter only updates on a successful apply,
	// the former on every reconcile that touched the device.
	deviceCheck := metav1.Now()
	updated.Status.LastDeviceCheck = &deviceCheck
	if result.Phase == engine.PhaseInSync {
		updated.Status.LastAppliedHash = hash
		updated.Status.LastAppliedTime = &deviceCheck
		if result.YangVersion != "" {
			updated.Status.SourceYangVersion = result.YangVersion
		}
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

	// Clear the replay annotation on a successful replay apply
	// so the reconciler doesn't keep re-doing the work every
	// tick. Any phase other than InSync leaves the annotation
	// in place — the next reconcile retries the replay.
	if result.Phase == engine.PhaseInSync {
		if _, ok := cr.Annotations[ReplayAnnotation]; ok {
			patched := cr.DeepCopy()
			delete(patched.Annotations, ReplayAnnotation)
			if err := r.Client.Patch(ctx, patched, client.MergeFrom(cr)); err != nil {
				// Non-fatal: another reconcile will pick the
				// annotation up; the worst case is a single
				// duplicate apply, which is idempotent.
				if r.Recorder != nil {
					r.Recorder.Eventf(cr, "Warning", "ReplayAnnotationClearFailed",
						"could not clear %s after successful replay: %v",
						ReplayAnnotation, err)
				}
			}
		}
	}

	// Audit log: append a row to any IOSXEConfigApplyLog CR that
	// targets this device. The lookup is by spec.deviceRef.name; if
	// no log exists, we silently skip — auditing is opt-in to keep
	// the controller's blast radius narrow.
	if err := r.appendApplyLog(ctx, cr, result, hash, resolved); err != nil {
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
//  1. A per-family event for every non-InSync family (Warning if
//     ApplyError / Unsupported, Normal for Drifted / Skipped).
//  2. A terminal Normal/Warning event describing the overall phase.
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

	// SaveStartup outcome (Wave 1A): a non-fatal warning when
	// requested-but-failed; a normal note when requested and saved.
	// The success event is gated on actually having saved (i.e.
	// SaveStartupOK true) so a CR with writeStartup=false stays
	// quiet on the event stream.
	switch {
	case result.SaveStartupErr != nil:
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "SaveStartupFailed",
			"startup-config save failed (apply itself succeeded): %v", result.SaveStartupErr)
	case result.SaveStartupOK:
		r.Recorder.Eventf(cr, corev1.EventTypeNormal, "SaveStartupOK",
			"startup-config saved")
	}
}

// appendApplyLog appends a row to every IOSXEConfigApplyLog CR
// targeting cr.Spec.DeviceRef.Name in the same namespace as the
// IOSXEConfig. Auditing is opt-in: a device with no log CR sees
// the function return nil after a list that found nothing. The
// circular-buffer trim happens here, capped at spec.maxEntries.
//
// resolved, when non-nil, is the resolved intent for the apply.
// It is captured into the ApplyLogEntry.Body field only on logs
// with spec.retainBody=true — necessary for annotation-driven
// time-travel replay (Phase 7).
func (r *ConfigReconciler) appendApplyLog(
	ctx context.Context,
	cr *configv1alpha1.IOSXEConfig,
	result engine.Result,
	hash string,
	resolved *intent.ResolvedIntent,
) error {
	var logs configv1alpha1.IOSXEConfigApplyLogList
	if err := r.Client.List(ctx, &logs, client.InNamespace(cr.Namespace)); err != nil {
		return fmt.Errorf("list apply logs: %w", err)
	}
	for i := range logs.Items {
		log := &logs.Items[i]
		if log.Spec.DeviceRef.Name != cr.Spec.DeviceRef.Name {
			continue
		}
		entry := buildApplyLogEntry(cr, result, hash)
		if log.Spec.RetainBody && resolved != nil && result.Phase == engine.PhaseInSync {
			body, err := encodeReplayBody(resolved)
			if err != nil {
				return fmt.Errorf("encode replay body for %s/%s: %w",
					log.Namespace, log.Name, err)
			}
			entry.Body = body
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

// replayBody is the on-disk shape stored on ApplyLogEntry.Body.
// Versioned so a future field addition doesn't break old entries
// the controller has to read post-upgrade.
type replayBody struct {
	Version       int               `json:"v"`
	Configuration map[string]any    `json:"configuration"`
	CLIBlocks     []intent.CLIBlock `json:"cliBlocks,omitempty"`
}

func encodeReplayBody(resolved *intent.ResolvedIntent) (string, error) {
	b := replayBody{
		Version:       1,
		Configuration: resolved.Configuration,
		CLIBlocks:     resolved.CLIBlocks,
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeReplayBody(raw string) (*replayBody, error) {
	var b replayBody
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return nil, err
	}
	if b.Version != 1 {
		return nil, fmt.Errorf("unsupported replay body version %d", b.Version)
	}
	return &b, nil
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

// ReplayAnnotation is the annotation an operator sets on an
// IOSXEConfig CR to ask the controller to re-apply a historical
// resolved-intent body from an IOSXEConfigApplyLog entry. The
// value is "<log-cr-name>:<entry-index-or-hash>".
//
// Examples:
//
//	config.cisco.vk/replay-from-log: edge-01-log:42
//	config.cisco.vk/replay-from-log: edge-01-log:sha256:abc123…
//
// Numeric values index into Status.Entries (oldest=0, newest=len-1).
// Non-numeric values are matched against ApplyLogEntry.Hash. The
// reconciler clears the annotation on a successful apply so
// repeated reconciles don't keep re-doing the work.
const ReplayAnnotation = "config.cisco.vk/replay-from-log"

// applyReplayAnnotation looks for ReplayAnnotation on cr; when
// present, it loads the named log entry's body, decodes it, and
// returns a ResolvedIntent that overrides the supplied resolved.
// applied is true iff the override happened; callers use that to
// bypass the hash short-circuit.
//
// The annotation value's format is intentionally narrow ("<log>:
// <index-or-hash>") so a typo never silently no-ops — anything
// that doesn't parse, or names a log that doesn't exist, returns
// an error and the reconciler records it as a failure on the CR.
func (r *ConfigReconciler) applyReplayAnnotation(
	ctx context.Context,
	cr *configv1alpha1.IOSXEConfig,
	resolved *intent.ResolvedIntent,
) (*intent.ResolvedIntent, bool, error) {
	raw, ok := cr.Annotations[ReplayAnnotation]
	if !ok || raw == "" {
		return resolved, false, nil
	}
	parts := splitReplayAnnotation(raw)
	if parts == nil {
		return resolved, false, fmt.Errorf(
			"%s=%q: expected '<log-name>:<index|hash>'",
			ReplayAnnotation, raw)
	}
	logName, selector := parts[0], parts[1]

	var log configv1alpha1.IOSXEConfigApplyLog
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: cr.Namespace, Name: logName}, &log); err != nil {
		return resolved, false, fmt.Errorf("get log %s/%s: %w", cr.Namespace, logName, err)
	}
	entry, err := pickReplayEntry(log.Status.Entries, selector)
	if err != nil {
		return resolved, false, err
	}
	if entry.Body == "" {
		return resolved, false, fmt.Errorf(
			"%s/%s entry %s has no retained body — set spec.retainBody=true on the log before relying on replay",
			cr.Namespace, logName, selector)
	}
	body, err := decodeReplayBody(entry.Body)
	if err != nil {
		return resolved, false, fmt.Errorf("decode replay body: %w", err)
	}
	// Override only the Configuration + CLIBlocks. Carry the rest
	// of the resolved intent (managed families, drift policy,
	// transactional flag, etc.) verbatim — the operator asked for
	// historical content, not historical CR shape.
	out := *resolved
	out.Configuration = body.Configuration
	out.CLIBlocks = body.CLIBlocks
	return &out, true, nil
}

// splitReplayAnnotation returns the [logName, selector] pair for a
// well-formed annotation value, or nil for any other shape.
// "edge-01-log:sha256:abc" splits as ["edge-01-log", "sha256:abc"];
// the colon-in-selector case (the canonical hash carries one)
// matters because a naive split-by-":" would mangle it.
func splitReplayAnnotation(raw string) []string {
	idx := -1
	for i := 0; i < len(raw); i++ {
		if raw[i] == ':' {
			idx = i
			break
		}
	}
	if idx <= 0 || idx == len(raw)-1 {
		return nil
	}
	return []string{raw[:idx], raw[idx+1:]}
}

// pickReplayEntry resolves selector against the entries slice.
// Numeric selectors index into the slice (negative not supported);
// otherwise the selector matches against ApplyLogEntry.Hash. A
// nil entry pointer is never returned — every error path is
// explicit.
func pickReplayEntry(entries []configv1alpha1.ApplyLogEntry, selector string) (configv1alpha1.ApplyLogEntry, error) {
	if len(entries) == 0 {
		return configv1alpha1.ApplyLogEntry{}, fmt.Errorf("log has no entries")
	}
	if i, err := strconvIndex(selector); err == nil {
		if i < 0 || i >= len(entries) {
			return configv1alpha1.ApplyLogEntry{}, fmt.Errorf(
				"index %d out of range [0, %d)", i, len(entries))
		}
		return entries[i], nil
	}
	for _, e := range entries {
		if e.Hash == selector {
			return e, nil
		}
	}
	return configv1alpha1.ApplyLogEntry{}, fmt.Errorf("no entry with hash %q", selector)
}

// strconvIndex avoids the strconv import for one tiny helper. Only
// non-negative integers are accepted; anything else surfaces as
// "not a number" and the caller falls back to hash matching.
func strconvIndex(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-numeric")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
