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

package diagnostic

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

// Phase constants mirror the kubebuilder enum on
// IOSXEDiagnosticStatus.Phase. Kept as named constants here so the
// reconciler can't typo a phase value.
const (
	PhasePending   = "Pending"
	PhaseCapturing = "Capturing"
	PhaseCompleted = "Completed"
	PhaseFailed    = "Failed"
	PhaseScheduled = "Scheduled"
	PhaseExpired   = "Expired"
)

// minScheduleInterval is the smallest schedule cadence the
// reconciler accepts. Anything below this is rejected at admission;
// the reconciler floors here as a defensive backstop in case the
// admission webhook is bypassed.
const minScheduleInterval = 30 * time.Second

// defaultMaxResults / defaultTruncateAt back-fill the corresponding
// retention fields when the operator leaves them empty.
const (
	defaultMaxResults = 24
	defaultTruncateAt = 64 * 1024 // 64 KiB
)

// TransportProvider abstracts the per-device-pod's ConfigReconciler
// so the diagnostic reconciler can reuse the configdriver's existing
// transport (deferred-dial, hot-swap, lock-arbitration). Tests
// satisfy this with a stub.
type TransportProvider interface {
	GetTransport() transport.Interface
}

// Reconciler watches IOSXEDiagnostic CRs and runs the requested
// commands via the device transport. Per-pod-kubelet topology runs
// one Reconciler instance per device, scoped to the device's
// namespace via the SetupWithManager filter.
type Reconciler struct {
	Client     client.Client
	Recorder   record.EventRecorder
	Scheme     *runtime.Scheme
	DeviceName string
	// DeviceNamespace is the namespace of the CiscoDevice this reconciler
	// serves. deviceRef is same-namespace by contract, so a CR in another
	// namespace must never be reconciled here even if the device name
	// matches. Empty disables the filter (unit tests).
	DeviceNamespace string
	Platform        CommandPlatform

	// TP is the per-device-pod's transport source. The diagnostic
	// reconciler does not dial — it borrows the configdriver's live
	// transport so a single auth/session is shared. When TP returns
	// nil (deferred-dial still pending), reconcile re-queues.
	TP TransportProvider

	// Now is injected for testability. nil → time.Now.
	Now func() time.Time
}

// SetupWithManager registers the reconciler with controller-runtime.
// The per-device manager cache is cluster-wide, so the targeting filter
// (spec.deviceRef.name == r.DeviceName AND cr.namespace == DeviceNamespace)
// is applied at reconcile time to keep cross-namespace CRs out.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.IOSXEDiagnostic{}).
		Complete(r)
}

// Reconcile runs the IOSXEDiagnostic state machine for one CR.
//
// State transitions:
//
//	Pending   → reconcile not yet observed (or generation bumped)
//	Scheduled → schedule is set; waiting for next cadence tick
//	Capturing → cli-exec batch in flight (ephemeral; not usually
//	            observed at rest because the RPC is synchronous)
//	Completed → terminal for one-shot CRs; rolling state for
//	            scheduled CRs that have at least one capture
//	Failed    → transport-level failure; per-command failures alone
//	            DO NOT move to Failed (operator wants the partial
//	            results)
//	Expired   → notAfter has passed before any capture occurred
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	now := r.now()

	var diag configv1alpha1.IOSXEDiagnostic
	if err := r.Client.Get(ctx, req.NamespacedName, &diag); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get IOSXEDiagnostic: %w", err)
	}

	// Filter: ignore CRs targeting other devices, or living in another
	// namespace (deviceRef is same-namespace by contract — a same-named
	// device in a different namespace is a different device).
	if diag.Spec.DeviceRef.Name != r.DeviceName ||
		(r.DeviceNamespace != "" && diag.Namespace != r.DeviceNamespace) {
		return reconcile.Result{}, nil
	}

	// Surface a scalar command count for the printer column. Runs
	// ahead of every status-write path AND forces an immediate
	// status write when the field is out of date — without this
	// the early-return short-circuit (one-shot CRs already at
	// phase=Completed for the current generation) leaves the
	// in-memory assignment unflushed.
	desired := int32(len(diag.Spec.Commands))
	if diag.Status.CommandCount != desired {
		diag.Status.CommandCount = desired
		if err := r.Client.Status().Update(ctx, &diag); err != nil {
			return reconcile.Result{}, fmt.Errorf("status update (commandCount): %w", err)
		}
	}

	// Maintenance window — notAfter past → Expired terminal.
	if diag.Spec.NotAfter != nil && now.After(diag.Spec.NotAfter.Time) {
		if diag.Status.Phase != PhaseExpired {
			diag.Status.Phase = PhaseExpired
			diag.Status.ObservedGeneration = diag.Generation
			r.setReady(&diag, metav1.ConditionFalse, "Expired",
				"notAfter has passed before any capture occurred")
			return reconcile.Result{}, r.Client.Status().Update(ctx, &diag)
		}
		return reconcile.Result{}, nil
	}

	// Maintenance window — notBefore in the future → re-queue.
	if diag.Spec.NotBefore != nil && now.Before(diag.Spec.NotBefore.Time) {
		if diag.Status.Phase != PhaseScheduled {
			diag.Status.Phase = PhaseScheduled
			diag.Status.ObservedGeneration = diag.Generation
			diag.Status.NextCapture = diag.Spec.NotBefore
			r.setReady(&diag, metav1.ConditionFalse, "WaitingForWindow",
				fmt.Sprintf("waiting for notBefore=%s", diag.Spec.NotBefore.Time.Format(time.RFC3339)))
			if err := r.Client.Status().Update(ctx, &diag); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{RequeueAfter: diag.Spec.NotBefore.Sub(now)}, nil
	}

	// One-shot CRs: skip if already Completed for the current
	// generation. A spec edit bumps generation and re-fires.
	if diag.Spec.Schedule == nil &&
		diag.Status.Phase == PhaseCompleted &&
		diag.Status.ObservedGeneration == diag.Generation {
		return reconcile.Result{}, nil
	}

	// Scheduled CRs: check whether NextCapture is due.
	if diag.Spec.Schedule != nil &&
		diag.Status.Phase == PhaseCompleted &&
		diag.Status.NextCapture != nil &&
		now.Before(diag.Status.NextCapture.Time) {
		return reconcile.Result{RequeueAfter: diag.Status.NextCapture.Sub(now)}, nil
	}

	// Transport check — if deferred-dial still pending, re-queue.
	tr := r.TP.GetTransport()
	if tr == nil {
		r.setReady(&diag, metav1.ConditionFalse, "NoTransport",
			"device transport not yet ready; will retry")
		_ = r.Client.Status().Update(ctx, &diag)
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Transport must implement DiagnosticExecer (RESTCONF + NETCONF
	// do; gNMI doesn't today). Without it, we can't run.
	d, ok := tr.(transport.DiagnosticExecer)
	if !ok || !tr.Capabilities().SupportsDiagnosticExec {
		diag.Status.Phase = PhaseFailed
		diag.Status.ObservedGeneration = diag.Generation
		r.setReady(&diag, metav1.ConditionFalse, "TransportUnsupported",
			fmt.Sprintf("transport %q does not implement DiagnosticExecer (cli-exec)",
				tr.Capabilities().Kind))
		r.event(&diag, corev1.EventTypeWarning, "TransportUnsupported",
			"the device's configured transport does not support cli-exec")
		return reconcile.Result{}, r.Client.Status().Update(ctx, &diag)
	}

	// Run the batch.
	capture := r.runBatch(ctx, &diag, d, now)

	// Phase D — when the ConfigMap sink is active, write the full
	// outputs there and shrink the inline body to a preview. Then
	// trim oldest ConfigMaps past Retention.MaxResults.
	if sinkActive(&diag) && capture.TransportError == "" {
		if err := r.writeToConfigMap(ctx, &diag, &capture); err != nil {
			capture.TransportError = err.Error()
			r.event(&diag, corev1.EventTypeWarning, "SinkError", err.Error())
		} else if err := r.pruneOldConfigMaps(ctx, &diag); err != nil {
			r.event(&diag, corev1.EventTypeWarning, "SinkPruneFailed", err.Error())
		}
	}

	r.appendCapture(&diag, capture)
	diag.Status.Phase = PhaseCompleted
	diag.Status.ObservedGeneration = diag.Generation
	diag.Status.LastCapture = &metav1.Time{Time: capture.CapturedAt.Time}
	if diag.Spec.Schedule != nil {
		next, err := r.computeNext(diag.Spec.Schedule, capture.CapturedAt.Time)
		if err != nil {
			diag.Status.Phase = PhaseFailed
			r.setReady(&diag, metav1.ConditionFalse, "BadSchedule", err.Error())
			r.event(&diag, corev1.EventTypeWarning, "BadSchedule", err.Error())
		} else {
			diag.Status.NextCapture = &metav1.Time{Time: next}
		}
	} else {
		diag.Status.NextCapture = nil
	}
	if capture.TransportError != "" {
		r.setReady(&diag, metav1.ConditionFalse, "TransportError", capture.TransportError)
		r.event(&diag, corev1.EventTypeWarning, "TransportError", capture.TransportError)
	} else {
		r.setReady(&diag, metav1.ConditionTrue, "Captured",
			fmt.Sprintf("%d command(s) captured", len(capture.Commands)))
		r.event(&diag, corev1.EventTypeNormal, "Captured",
			fmt.Sprintf("%d command(s) captured against device %s",
				len(capture.Commands), r.DeviceName))
	}
	if err := r.Client.Status().Update(ctx, &diag); err != nil {
		return reconcile.Result{}, err
	}
	if diag.Status.NextCapture != nil {
		return reconcile.Result{RequeueAfter: diag.Status.NextCapture.Sub(now)}, nil
	}
	return reconcile.Result{}, nil
}

// runBatch invokes the transport's DiagnosticExec with the spec's
// command list, applies redaction + truncation per the retention
// settings, and returns one DiagnosticCapture suitable for appending
// to status.results.
func (r *Reconciler) runBatch(
	ctx context.Context,
	diag *configv1alpha1.IOSXEDiagnostic,
	d transport.DiagnosticExecer,
	now time.Time,
) configv1alpha1.DiagnosticCapture {
	capture := configv1alpha1.DiagnosticCapture{
		CapturedAt: metav1.Time{Time: now},
	}
	// Wave 10 release-readiness P0 fix (2026-04-28): server-side
	// allowlist enforcement. Refuse the batch BEFORE contacting the
	// device when any command falls outside the read-only allowlist.
	// Pre-fix the reconciler forwarded spec.commands directly with no
	// validation; a user with create-IOSXEDiagnostic RBAC could
	// bypass the kubectl plugin's denylist and submit configure-mode
	// or destructive CLI through the same device credentials.
	if err := ValidateCommandsForPlatform(r.Platform, diag.Spec.Commands); err != nil {
		capture.TransportError = err.Error()
		return capture
	}
	results, err := d.DiagnosticExec(ctx, diag.Spec.Commands)
	if err != nil {
		capture.TransportError = err.Error()
	}
	truncateAt := defaultTruncateAt
	if diag.Spec.Retention != nil && diag.Spec.Retention.TruncateAt != "" {
		if n := ParseSize(diag.Spec.Retention.TruncateAt); n > 0 {
			truncateAt = n
		}
	}
	for _, res := range results {
		out := configv1alpha1.CommandOutput{
			Command: res.Command,
			Err:     res.Err,
			Output:  res.Output,
		}
		if !diag.Spec.AllowSecrets && out.Output != "" {
			redacted, didRedact := Redact(out.Output)
			out.Output = redacted
			out.Redacted = didRedact
		}
		if out.Output != "" {
			clipped, truncated := Truncate(out.Output, truncateAt)
			out.Output = clipped
			out.Truncated = truncated
		}
		capture.Commands = append(capture.Commands, out)
	}
	return capture
}

// appendCapture mutates diag.Status.Results: appends the new capture
// and trims to the retention's MaxResults (oldest-first eviction).
func (r *Reconciler) appendCapture(
	diag *configv1alpha1.IOSXEDiagnostic,
	capture configv1alpha1.DiagnosticCapture,
) {
	maxResults := defaultMaxResults
	if diag.Spec.Retention != nil && diag.Spec.Retention.MaxResults > 0 {
		maxResults = int(diag.Spec.Retention.MaxResults)
	}
	if diag.Spec.Schedule == nil {
		// One-shot — single result entry, replace any prior.
		diag.Status.Results = []configv1alpha1.DiagnosticCapture{capture}
		return
	}
	diag.Status.Results = append(diag.Status.Results, capture)
	if len(diag.Status.Results) > maxResults {
		diag.Status.Results = diag.Status.Results[len(diag.Status.Results)-maxResults:]
	}
	// Sort defensively in case status was hand-edited or replayed
	// out-of-order (the engine doesn't, but operator tooling might).
	sort.SliceStable(diag.Status.Results, func(i, j int) bool {
		return diag.Status.Results[i].CapturedAt.Before(&diag.Status.Results[j].CapturedAt)
	})
}

// computeNext resolves the next-capture time from a schedule. Cron
// support is deferred to a follow-up — Phase B ships interval only,
// the simpler-and-more-common shape. Cron is rejected with a clear
// error rather than silently no-op'd.
func (r *Reconciler) computeNext(s *configv1alpha1.DiagnosticSchedule, base time.Time) (time.Time, error) {
	if s.Cron != "" {
		return time.Time{}, fmt.Errorf("cron schedules not yet implemented; use spec.schedule.interval")
	}
	if s.Interval == "" {
		return time.Time{}, fmt.Errorf("schedule.interval is empty and schedule.cron is unset")
	}
	d, err := time.ParseDuration(s.Interval)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid schedule.interval %q: %w", s.Interval, err)
	}
	if d < minScheduleInterval {
		d = minScheduleInterval
	}
	return base.Add(d), nil
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reconciler) setReady(
	diag *configv1alpha1.IOSXEDiagnostic,
	status metav1.ConditionStatus,
	reason, message string,
) {
	cond := metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Time{Time: r.now()},
		ObservedGeneration: diag.Generation,
	}
	for i, c := range diag.Status.Conditions {
		if c.Type == cond.Type {
			if c.Status == cond.Status && c.Reason == cond.Reason {
				cond.LastTransitionTime = c.LastTransitionTime
			}
			diag.Status.Conditions[i] = cond
			return
		}
	}
	diag.Status.Conditions = append(diag.Status.Conditions, cond)
}

func (r *Reconciler) event(diag *configv1alpha1.IOSXEDiagnostic, kind, reason, msg string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(diag, kind, reason, msg)
}
