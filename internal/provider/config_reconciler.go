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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	result := eng.Reconcile(ctx, resolved)
	return r.recordResult(ctx, cr, result, h, conflicts)
}

// recordPending is the Phase-0 fallback when no transport is wired.
func (r *ConfigReconciler) recordPending(ctx context.Context, cr *configv1alpha1.IOSXEConfig) error {
	if cr.Status.Phase == engine.PhasePending && cr.Status.ObservedGeneration == cr.Generation {
		return nil
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

// recordResult serialises an engine.Result into the CR's status. It
// also writes the per-family list, current drift, the hash, and a
// Conflict condition if the CR shares a family with another CR.
func (r *ConfigReconciler) recordResult(
	ctx context.Context,
	cr *configv1alpha1.IOSXEConfig,
	result engine.Result,
	hash string,
	conflicts map[string][]string,
) error {
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
	for _, d := range result.Drift {
		updated.Status.Drift = append(updated.Status.Drift, configv1alpha1.DriftEntry{
			Family:   d.Family,
			Path:     d.Path,
			Desired:  d.Desired,
			Observed: d.Observed,
			Detected: metav1.Now(),
		})
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

	return ignoreConflict(r.Client.Status().Update(ctx, updated))
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
