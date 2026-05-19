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

// Package deviceoperation reconciles read-only DeviceOperation CRs.
package deviceoperation

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/semconv"
)

const (
	tracerName             = "cisco-virtual-kubelet/device-operation"
	defaultInlineMaxBytes  = 64 * 1024
	artifactThresholdBytes = 256 * 1024
	artifactMaxBytes       = 900 * 1024
	packetCaptureOutputKey = "output"
	artifactPreviewFooter  = "\n<truncated; see artifactURIs>"

	// totalInlineMaxBytes caps the cumulative size of all
	// DeviceOperation.Status.Outputs[*].Output strings before the
	// reconciler writes the status. The Kubernetes etcd object limit
	// is ~1.5 MiB; with up to 64 commands at 64 KiB each (the
	// per-output cap) the unconstrained worst case is ~4 MiB, which
	// failed status updates and made the reconciler retry every
	// command against the device. We spill overflow into the
	// per-operation ConfigMap artifact and replace inline Output
	// fields with a short preview when this cap is reached.
	// Adversarial-review Finding #4.
	totalInlineMaxBytes = 256 * 1024
	// inlinePreviewBytes is the per-output cap applied to the inline
	// preview when total-budget spill kicks in. Much smaller than the
	// per-output cap so 64 spilled previews stay well under
	// totalInlineMaxBytes.
	inlinePreviewBytes = 2 * 1024

	envConfigDiffAllowedNamespaces = "CVK_OPS_CONFIGDIFF_ALLOWED_NAMESPACES"
)

// TransportProvider abstracts the per-device config reconciler so operation
// execution can reuse its live authenticated transport.
type TransportProvider interface {
	GetTransport() transport.Interface
}

// Reconciler watches DeviceOperation CRs for one device and executes the
// supported read-only operation kinds.
type Reconciler struct {
	Client client.Client
	// Reader should be an uncached API reader when available. Status updates
	// can be triggered immediately after create from the admin endpoint; reading
	// the latest resourceVersion avoids cache-staleness conflict loops.
	Reader   client.Reader
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme
	// DeviceName is the CiscoDevice metadata.name this reconciler serves.
	DeviceName string
	// DeviceNamespace is the namespace of the owning CiscoDevice CR. Reconcile
	// rejects DeviceOperation CRs whose own namespace does not match — without
	// this guard a tenant in any namespace can create a DeviceOperation
	// targeting deviceRef.name=<known-device> and the per-device pod will
	// execute it with device credentials. Empty disables the check (legacy
	// single-tenant behaviour for tests that do not plumb the namespace).
	DeviceNamespace string
	TP              TransportProvider

	// GNOI is the optional per-device gNOI client provider. When nil,
	// gNOI operation kinds fail fast with reason GNOIUnsupported.
	GNOI gnoi.Provider

	// Now is injected for tests. nil means time.Now.
	Now func() time.Time
}

// SetupWithManager registers the DeviceOperation controller.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.DeviceOperation{}).
		Complete(r)
}

// Reconcile executes one DeviceOperation generation. The v1alpha1 controller is
// deliberately read-only: ShowCommand runs allowlisted commands, ConfigDiff
// captures running config and can compare it with an operator-supplied baseline,
// and PacketCapture reads an existing monitor capture buffer.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	now := r.now()

	var op opsv1alpha1.DeviceOperation
	if err := r.Client.Get(ctx, req.NamespacedName, &op); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get DeviceOperation: %w", err)
	}

	if op.Spec.DeviceRef.Name != r.DeviceName {
		return reconcile.Result{}, nil
	}
	// DeviceOperation.spec.deviceRef is a same-namespace pointer by convention,
	// but the watch is cluster-wide so nothing stops a tenant in namespace X
	// from creating a DeviceOperation that names deviceRef from namespace Y.
	// Reject those before any device transport is touched.
	if r.DeviceNamespace != "" && op.Namespace != r.DeviceNamespace {
		msg := fmt.Sprintf("DeviceOperation %s/%s targets device %q which lives in namespace %q; refusing to execute cross-namespace request",
			op.Namespace, op.Name, op.Spec.DeviceRef.Name, r.DeviceNamespace)
		return reconcile.Result{}, r.finishWithReason(ctx, &op, opsv1alpha1.OperationPhaseFailed,
			"NamespaceMismatch", msg, nil, nil, now)
	}

	if terminal(op.Status.Phase) && op.Status.ObservedGeneration == op.Generation {
		return r.handleTTL(ctx, &op, now)
	}
	if op.Spec.Operation.Kind == opsv1alpha1.OperationKindConfigDiff &&
		!configDiffNamespaceAllowed(op.Namespace) {
		msg := fmt.Sprintf("ConfigDiff is not authorized in namespace %q", op.Namespace)
		return reconcile.Result{}, r.finishWithReason(ctx, &op, opsv1alpha1.OperationPhaseFailed,
			"NamespaceNotAuthorized", msg, nil, nil, now)
	}

	spanName := operationSpanName(op.Spec.Operation.Kind)
	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName)
	defer span.End()
	span.SetAttributes(
		attribute.String("cisco.device.name", r.DeviceName),
		attribute.String("cvk.operation.kind", string(op.Spec.Operation.Kind)),
		attribute.String("k8s.namespace.name", op.Namespace),
		attribute.String("k8s.resource.name", op.Name),
		attribute.String(semconv.CvkEntityType, semconv.EntityTypeOperation),
		attribute.String(semconv.CvkEntityID, operationEntityID(&op)),
		attribute.String(semconv.CvkEvidenceType, semconv.EvidenceTypeOperatorAction),
		attribute.String(semconv.CvkWorkflowName, "operator.diagnostic"),
		attribute.String(semconv.CvkTaskName, "op."+string(op.Spec.Operation.Kind)),
	)

	// gNOI dispatch path. The gNOI-backed operation kinds produce
	// structured JSON output directly from the gNOI client; they do
	// not flow through the CLI/diagnostic transport that the rest of
	// this reconciler is built around.
	if isGNOIKind(op.Spec.Operation.Kind) {
		if err := r.markRunning(ctx, &op, now); err != nil {
			return reconcile.Result{}, err
		}
		outputs, successMsg, err := r.dispatchGNOI(ctx, &op)
		terminalPhase := opsv1alpha1.OperationPhaseSucceeded
		message := successMsg
		reason := "Succeeded"
		if err != nil {
			span.RecordError(err)
			terminalPhase = opsv1alpha1.OperationPhaseFailed
			message = err.Error()
			reason = "GNOIFailed"
		}
		if terminalPhase == opsv1alpha1.OperationPhaseFailed {
			span.SetStatus(codes.Error, message)
		} else {
			span.SetStatus(codes.Ok, "")
		}
		return reconcile.Result{}, r.finishWithReason(ctx, &op, terminalPhase, reason, message, outputs, nil, now)
	}

	plan, err := buildPlan(op.Spec.Operation)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return reconcile.Result{}, r.finish(ctx, &op, opsv1alpha1.OperationPhaseFailed, err.Error(), nil, now)
	}
	commands := plan.commands
	span.SetAttributes(attribute.Int("cvk.operation.command_count", len(commands)))

	if err := diagnostic.ValidateCommands(commands); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return reconcile.Result{}, r.finish(ctx, &op, opsv1alpha1.OperationPhaseFailed, err.Error(), nil, now)
	}

	if r.TP == nil {
		msg := "device transport provider is not configured"
		span.SetStatus(codes.Error, msg)
		return reconcile.Result{}, r.finish(ctx, &op, opsv1alpha1.OperationPhaseFailed, msg, nil, now)
	}
	tr := r.TP.GetTransport()
	if tr == nil {
		if err := r.markPending(ctx, &op, "NoTransport", "device transport not yet ready; will retry", now); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}
	execer, ok := tr.(transport.DiagnosticExecer)
	if !ok || !tr.Capabilities().SupportsDiagnosticExec {
		msg := fmt.Sprintf("transport %q does not support read-only command execution", tr.Capabilities().Kind)
		span.SetStatus(codes.Error, msg)
		return reconcile.Result{}, r.finish(ctx, &op, opsv1alpha1.OperationPhaseFailed, msg, nil, now)
	}

	if err := r.markRunning(ctx, &op, now); err != nil {
		return reconcile.Result{}, err
	}

	results, err := execer.DiagnosticExec(ctx, commands)
	outputs := plan.outputs(results)
	var artifactURIs []string
	terminalPhase := opsv1alpha1.OperationPhaseSucceeded
	message := plan.successMessage
	reason := "Succeeded"
	if err != nil {
		span.RecordError(err)
		terminalPhase = opsv1alpha1.OperationPhaseFailed
		message = err.Error()
		reason = "Failed"
	}
	for i := range outputs {
		out := &outputs[i]
		if out.Err != "" && terminalPhase != opsv1alpha1.OperationPhaseFailed {
			terminalPhase = opsv1alpha1.OperationPhaseFailed
			message = "one or more commands returned an error"
			reason = "Failed"
		}
	}
	if terminalPhase == opsv1alpha1.OperationPhaseSucceeded {
		if validateErr := validateDiagnosticOutputs(op.Spec.Operation.Kind, commands, outputs); validateErr != nil {
			terminalPhase = opsv1alpha1.OperationPhaseFailed
			message = validateErr.Error()
			reason = "Failed"
		}
	}
	if terminalPhase == opsv1alpha1.OperationPhaseSucceeded &&
		op.Spec.Operation.Kind == opsv1alpha1.OperationKindPacketCapture {
		var artifactErr *operationArtifactError
		outputs, artifactURIs, artifactErr = r.backPacketCaptureArtifacts(ctx, &op, outputs, results)
		if artifactErr != nil {
			span.RecordError(artifactErr)
			terminalPhase = opsv1alpha1.OperationPhaseFailed
			message = artifactErr.Error()
			reason = artifactErr.reason
			artifactURIs = nil
		}
	}
	// Adversarial-review Finding #4: even after per-output truncation
	// the cumulative inline status payload can exceed etcd's object
	// limit (64 commands × 64 KiB ≈ 4 MiB). Spill overflow into the
	// per-operation ConfigMap artifact and shrink inline previews so
	// the Status.Update never fails for size. Applied regardless of
	// operation kind so ShowCommand fan-outs are also protected.
	if terminalPhase == opsv1alpha1.OperationPhaseSucceeded {
		extraURIs, artifactErr := r.enforceTotalInlineBudget(ctx, &op, outputs, results)
		if artifactErr != nil {
			span.RecordError(artifactErr)
			terminalPhase = opsv1alpha1.OperationPhaseFailed
			message = artifactErr.Error()
			reason = artifactErr.reason
			artifactURIs = nil
		} else {
			artifactURIs = append(artifactURIs, extraURIs...)
		}
	}
	if terminalPhase == opsv1alpha1.OperationPhaseFailed {
		span.SetStatus(codes.Error, message)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	return reconcile.Result{}, r.finishWithReason(ctx, &op, terminalPhase, reason, message, outputs, artifactURIs, now)
}

func operationEntityID(op *opsv1alpha1.DeviceOperation) string {
	if op == nil {
		return ""
	}
	if op.UID != "" {
		return string(op.UID)
	}
	if op.Namespace != "" {
		return op.Namespace + "/" + op.Name
	}
	return op.Name
}

func (r *Reconciler) markPending(ctx context.Context, op *opsv1alpha1.DeviceOperation, reason, message string, now time.Time) error {
	if err := r.updateStatus(ctx, op, func(current *opsv1alpha1.DeviceOperation) {
		current.Status.Phase = opsv1alpha1.OperationPhasePending
		current.Status.ObservedGeneration = current.Generation
		current.Status.Message = message
		current.Status.CompletionTime = nil
		r.setReady(current, metav1.ConditionFalse, reason, message, now)
	}); err != nil {
		return fmt.Errorf("status update pending: %w", err)
	}
	return nil
}

func (r *Reconciler) markRunning(ctx context.Context, op *opsv1alpha1.DeviceOperation, now time.Time) error {
	if err := r.updateStatus(ctx, op, func(current *opsv1alpha1.DeviceOperation) {
		current.Status.Phase = opsv1alpha1.OperationPhaseRunning
		current.Status.ObservedGeneration = current.Generation
		current.Status.StartTime = &metav1.Time{Time: now}
		current.Status.CompletionTime = nil
		current.Status.Message = "operation is running"
		current.Status.Outputs = nil
		current.Status.ArtifactURIs = nil
		r.setReady(current, metav1.ConditionFalse, "Running", "operation is running", now)
	}); err != nil {
		return fmt.Errorf("status update running: %w", err)
	}
	return nil
}

func (r *Reconciler) finish(
	ctx context.Context,
	op *opsv1alpha1.DeviceOperation,
	phase opsv1alpha1.OperationPhase,
	message string,
	outputs []opsv1alpha1.DeviceOperationOutput,
	now time.Time,
) error {
	reason := "Failed"
	if phase == opsv1alpha1.OperationPhaseSucceeded {
		reason = "Succeeded"
	}
	return r.finishWithReason(ctx, op, phase, reason, message, outputs, nil, now)
}

func (r *Reconciler) finishWithReason(
	ctx context.Context,
	op *opsv1alpha1.DeviceOperation,
	phase opsv1alpha1.OperationPhase,
	reason string,
	message string,
	outputs []opsv1alpha1.DeviceOperationOutput,
	artifactURIs []string,
	now time.Time,
) error {
	if err := r.updateStatus(ctx, op, func(current *opsv1alpha1.DeviceOperation) {
		current.Status.Phase = phase
		current.Status.ObservedGeneration = current.Generation
		if current.Status.StartTime == nil {
			current.Status.StartTime = &metav1.Time{Time: now}
		}
		current.Status.CompletionTime = &metav1.Time{Time: now}
		current.Status.Message = message
		current.Status.Outputs = outputs
		current.Status.ArtifactURIs = artifactURIs
		if phase == opsv1alpha1.OperationPhaseSucceeded {
			r.setReady(current, metav1.ConditionTrue, reason, message, now)
		} else {
			r.setReady(current, metav1.ConditionFalse, reason, message, now)
		}
	}); err != nil {
		return fmt.Errorf("status update terminal: %w", err)
	}
	if phase == opsv1alpha1.OperationPhaseSucceeded {
		r.event(op, corev1.EventTypeNormal, "Succeeded", message)
	} else {
		r.event(op, corev1.EventTypeWarning, reason, message)
	}
	return nil
}

func (r *Reconciler) updateStatus(
	ctx context.Context,
	op *opsv1alpha1.DeviceOperation,
	mutate func(*opsv1alpha1.DeviceOperation),
) error {
	key := client.ObjectKeyFromObject(op)
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var current opsv1alpha1.DeviceOperation
		if err := reader.Get(ctx, key, &current); err != nil {
			return err
		}
		mutate(&current)
		if err := r.Client.Status().Update(ctx, &current); err != nil {
			return err
		}
		*op = current
		return nil
	})
}

type operationArtifactError struct {
	reason  string
	message string
}

func (e *operationArtifactError) Error() string { return e.message }

func (r *Reconciler) backPacketCaptureArtifacts(
	ctx context.Context,
	op *opsv1alpha1.DeviceOperation,
	outputs []opsv1alpha1.DeviceOperationOutput,
	results []transport.CommandResult,
) ([]opsv1alpha1.DeviceOperationOutput, []string, *operationArtifactError) {
	data := map[string]string{}
	uris := []string{}
	for i := range outputs {
		if i >= len(results) || outputs[i].Err != "" || results[i].Output == "" {
			continue
		}
		redacted, didRedact := diagnostic.Redact(results[i].Output)
		if len(redacted) <= artifactThresholdBytes {
			continue
		}
		if len(redacted) > artifactMaxBytes {
			return outputs, nil, &operationArtifactError{
				reason: "ArtifactTooLarge",
				message: fmt.Sprintf("packet-capture output for command %q is %d bytes; ConfigMap artifact limit is %d bytes",
					results[i].Command, len(redacted), artifactMaxBytes),
			}
		}
		key := packetCaptureOutputKey
		if len(outputs) > 1 {
			key = fmt.Sprintf("%s-%d", packetCaptureOutputKey, i)
		}
		data[key] = redacted
		outputs[i].Output = truncatePreviewWithFooter(redacted, defaultInlineMaxBytes, artifactPreviewFooter)
		outputs[i].Truncated = true
		outputs[i].Redacted = outputs[i].Redacted || didRedact
		uris = append(uris, fmt.Sprintf("configmap://%s/%s/%s", op.Namespace, artifactConfigMapName(op), key))
	}
	if len(data) == 0 {
		return outputs, nil, nil
	}
	if r.Scheme == nil {
		return outputs, nil, &operationArtifactError{
			reason:  "ArtifactWriteFailed",
			message: "device operation artifact owner reference scheme is not configured",
		}
	}
	if err := r.assertArtifactConfigMapOwned(ctx, op); err != nil {
		return outputs, nil, err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: op.Namespace,
			Name:      artifactConfigMapName(op),
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = data
		return controllerutil.SetControllerReference(op, cm, r.Scheme)
	}); err != nil {
		return outputs, nil, &operationArtifactError{
			reason:  "ArtifactWriteFailed",
			message: fmt.Sprintf("write packet-capture artifact ConfigMap: %v", err),
		}
	}
	return outputs, uris, nil
}

// assertArtifactConfigMapOwned refuses to back an operation by an
// existing ConfigMap that is not already owned (via controller
// reference) by this DeviceOperation.
//
// Adversarial-review Finding #7: previously the per-operation artifact
// name was deterministic (`<op-name>-output`) and CreateOrUpdate
// replaced Data unconditionally. A DeviceOperation creator with no
// ConfigMap update rights could therefore clobber any pre-existing
// ConfigMap that happened to share the name. We now perform a Get
// first; if the CM exists with no controller ref, or its controller
// ref points elsewhere, we refuse rather than overwrite.
func (r *Reconciler) assertArtifactConfigMapOwned(
	ctx context.Context,
	op *opsv1alpha1.DeviceOperation,
) *operationArtifactError {
	if r.Reader == nil && r.Client == nil {
		return nil
	}
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	var existing corev1.ConfigMap
	err := reader.Get(ctx, client.ObjectKey{
		Namespace: op.Namespace,
		Name:      artifactConfigMapName(op),
	}, &existing)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return &operationArtifactError{
			reason:  "ArtifactWriteFailed",
			message: fmt.Sprintf("inspect existing artifact ConfigMap: %v", err),
		}
	}
	owner := metav1.GetControllerOf(&existing)
	if owner == nil {
		return &operationArtifactError{
			reason: "ArtifactExistsUnowned",
			message: fmt.Sprintf("ConfigMap %s/%s already exists and is not owned by a DeviceOperation; refusing to overwrite",
				existing.Namespace, existing.Name),
		}
	}
	if owner.UID != op.UID {
		return &operationArtifactError{
			reason: "ArtifactExistsForeignOwner",
			message: fmt.Sprintf("ConfigMap %s/%s is controller-owned by %s/%s (uid=%s), not by this DeviceOperation (uid=%s)",
				existing.Namespace, existing.Name, owner.Kind, owner.Name, owner.UID, op.UID),
		}
	}
	return nil
}

// enforceTotalInlineBudget caps the cumulative size of inline Output
// fields across all per-command outputs. When the sum exceeds
// totalInlineMaxBytes, the largest outputs are progressively spilled
// to the per-operation ConfigMap artifact (full redacted text) and
// their inline Output is replaced with a short preview that points at
// the artifact key. The function mutates outputs in place and returns
// any newly-added artifact URIs.
//
// Adversarial-review Finding #4: prior to this guard a 64-command
// operation with 64 KiB outputs (the per-output cap) produced ~4 MiB
// of status content, which exceeded Kubernetes' object size limit
// and made the status update fail; the reconciler then retried
// command execution against the device on every reconcile.
func (r *Reconciler) enforceTotalInlineBudget(
	ctx context.Context,
	op *opsv1alpha1.DeviceOperation,
	outputs []opsv1alpha1.DeviceOperationOutput,
	results []transport.CommandResult,
) ([]string, *operationArtifactError) {
	if totalInline(outputs) <= totalInlineMaxBytes {
		return nil, nil
	}
	if r.Scheme == nil {
		return nil, &operationArtifactError{
			reason:  "ArtifactWriteFailed",
			message: "device operation artifact owner reference scheme is not configured",
		}
	}
	// Spill in descending size order so the smallest number of
	// outputs gets the artifact treatment; remaining inline outputs
	// stay full-fidelity.
	type idxBytes struct {
		i, n int
	}
	order := make([]idxBytes, 0, len(outputs))
	for i := range outputs {
		order = append(order, idxBytes{i, len(outputs[i].Output)})
	}
	// Insertion sort (n ≤ 64) descending by size.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && order[j].n > order[j-1].n; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}

	data := map[string]string{}
	uris := []string{}
	for _, ent := range order {
		if totalInline(outputs) <= totalInlineMaxBytes {
			break
		}
		i := ent.i
		if outputs[i].Err != "" {
			continue
		}
		// Prefer the full (untruncated) text from results so the
		// artifact contains the original output, not the already-
		// truncated inline copy.
		full := outputs[i].Output
		if i < len(results) && results[i].Output != "" {
			redacted, didRedact := diagnostic.Redact(results[i].Output)
			full = redacted
			outputs[i].Redacted = outputs[i].Redacted || didRedact
		}
		if len(full) > artifactMaxBytes {
			return nil, &operationArtifactError{
				reason: "ArtifactTooLarge",
				message: fmt.Sprintf("operation output for command %q is %d bytes; ConfigMap artifact limit is %d bytes",
					outputs[i].Command, len(full), artifactMaxBytes),
			}
		}
		key := packetCaptureOutputKey
		if len(outputs) > 1 {
			key = fmt.Sprintf("%s-%d", packetCaptureOutputKey, i)
		}
		// If the PacketCapture path already spilled this key, skip
		// re-writing it but still trim the inline preview.
		if _, already := data[key]; !already {
			data[key] = full
			uris = append(uris, fmt.Sprintf("configmap://%s/%s/%s", op.Namespace, artifactConfigMapName(op), key))
		}
		outputs[i].Output = truncatePreviewWithFooter(full, inlinePreviewBytes, artifactPreviewFooter)
		outputs[i].Truncated = true
	}
	if len(data) == 0 {
		return nil, nil
	}
	if err := r.assertArtifactConfigMapOwned(ctx, op); err != nil {
		return nil, err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: op.Namespace,
			Name:      artifactConfigMapName(op),
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		// Merge with any data the PacketCapture path already wrote
		// so this pass does not lose those keys.
		for k, v := range data {
			cm.Data[k] = v
		}
		return controllerutil.SetControllerReference(op, cm, r.Scheme)
	}); err != nil {
		return nil, &operationArtifactError{
			reason:  "ArtifactWriteFailed",
			message: fmt.Sprintf("write operation artifact ConfigMap: %v", err),
		}
	}
	return uris, nil
}

func totalInline(outputs []opsv1alpha1.DeviceOperationOutput) int {
	total := 0
	for _, o := range outputs {
		total += len(o.Output)
	}
	return total
}

func artifactConfigMapName(op *opsv1alpha1.DeviceOperation) string {
	return op.Name + "-output"
}

func truncatePreviewWithFooter(s string, maxBytes int, footer string) string {
	if maxBytes <= 0 || len(s)+len(footer) <= maxBytes {
		return s
	}
	budget := maxBytes - len(footer)
	if budget <= 0 {
		return footer[:maxBytes]
	}
	cut := budget
	if idx := strings.LastIndex(s[:cut], "\n"); idx > 0 {
		cut = idx
	}
	return s[:cut] + footer
}

func (r *Reconciler) handleTTL(ctx context.Context, op *opsv1alpha1.DeviceOperation, now time.Time) (reconcile.Result, error) {
	if op.Spec.TTLSecondsAfterFinished == nil || *op.Spec.TTLSecondsAfterFinished <= 0 || op.Status.CompletionTime == nil {
		return reconcile.Result{}, nil
	}
	expireAt := op.Status.CompletionTime.Add(time.Duration(*op.Spec.TTLSecondsAfterFinished) * time.Second)
	if now.Before(expireAt) {
		return reconcile.Result{RequeueAfter: expireAt.Sub(now)}, nil
	}
	if err := r.Client.Delete(ctx, op); err != nil && !apierrors.IsNotFound(err) {
		return reconcile.Result{}, fmt.Errorf("delete expired DeviceOperation: %w", err)
	}
	r.event(op, corev1.EventTypeNormal, "Expired", "deleted DeviceOperation after ttlSecondsAfterFinished")
	return reconcile.Result{}, nil
}

func (r *Reconciler) setReady(
	op *opsv1alpha1.DeviceOperation,
	status metav1.ConditionStatus,
	reason, message string,
	now time.Time,
) {
	cond := metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Time{Time: now},
		ObservedGeneration: op.Generation,
	}
	for i, c := range op.Status.Conditions {
		if c.Type == cond.Type {
			if c.Status == cond.Status && c.Reason == cond.Reason {
				cond.LastTransitionTime = c.LastTransitionTime
			}
			op.Status.Conditions[i] = cond
			return
		}
	}
	op.Status.Conditions = append(op.Status.Conditions, cond)
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reconciler) event(op *opsv1alpha1.DeviceOperation, kind, reason, msg string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(op, kind, reason, msg)
}

func showCommands(req opsv1alpha1.DeviceOperationRequest) ([]string, error) {
	raw := append([]string(nil), req.Commands...)
	if len(raw) == 0 {
		if req.Args != nil {
			if cmd := strings.TrimSpace(req.Args["command"]); cmd != "" {
				raw = []string{cmd}
			} else if cmds := strings.TrimSpace(req.Args["commands"]); cmds != "" {
				raw = splitCommandArg(cmds)
			}
		}
	}
	commands := make([]string, 0, len(raw))
	for _, cmd := range raw {
		cmd = strings.TrimSpace(cmd)
		if cmd != "" {
			commands = append(commands, cmd)
		}
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("operation.commands or operation.args.command is required for ShowCommand")
	}
	if len(commands) > 64 {
		return nil, fmt.Errorf("ShowCommand accepts at most 64 commands")
	}
	return commands, nil
}

type operationPlan struct {
	commands       []string
	successMessage string
	outputs        func([]transport.CommandResult) []opsv1alpha1.DeviceOperationOutput
}

func buildPlan(req opsv1alpha1.DeviceOperationRequest) (operationPlan, error) {
	switch req.Kind {
	case opsv1alpha1.OperationKindShowCommand:
		commands, err := showCommands(req)
		if err != nil {
			return operationPlan{}, err
		}
		return operationPlan{
			commands:       commands,
			successMessage: fmt.Sprintf("%d command(s) completed", len(commands)),
			outputs:        commandOutputs,
		}, nil
	case opsv1alpha1.OperationKindConfigDiff:
		command := "show running-config"
		if req.Args != nil && strings.TrimSpace(req.Args["command"]) != "" {
			command = strings.TrimSpace(req.Args["command"])
		}
		plan := operationPlan{
			commands:       []string{command},
			successMessage: "running configuration captured",
			outputs:        commandOutputs,
		}
		if req.Args != nil && strings.TrimSpace(req.Args["baseline"]) != "" {
			baseline := req.Args["baseline"]
			plan.successMessage = "running configuration compared with baseline"
			plan.outputs = func(results []transport.CommandResult) []opsv1alpha1.DeviceOperationOutput {
				out := commandOutputs(results)
				if len(out) == 0 || out[0].Err != "" {
					return out
				}
				diff := lineDiff(baseline, out[0].Output)
				out[0].Command = "config diff"
				out[0].Output = diff
				redacted, didRedact := diagnostic.Redact(out[0].Output)
				out[0].Output = redacted
				out[0].Redacted = out[0].Redacted || didRedact
				clipped, truncated := diagnostic.Truncate(out[0].Output, defaultInlineMaxBytes)
				out[0].Output = clipped
				out[0].Truncated = out[0].Truncated || truncated
				return out
			}
		}
		return plan, nil
	case opsv1alpha1.OperationKindPacketCapture:
		// Adversarial-review Finding #3: PacketCapture previously
		// accepted an arbitrary args.command which flowed through the
		// shared diagnostic allowlist. That allowlist permits broad
		// `monitor`, `terminal`, and `test` head-words, so a caller
		// could drive state-changing operations (capture start/stop/
		// clear/export, terminal monitor, etc.) under an API that
		// advertises read-only semantics. The fix removes the
		// args.command escape hatch: PacketCapture only synthesises
		// the exact `show monitor capture <name> buffer dump` command
		// from a validated capture name. Operators who genuinely need
		// to invoke a different head-word must use ShowCommand with
		// the explicit allowlisted form.
		if req.Args == nil {
			return operationPlan{}, fmt.Errorf("operation.args.name is required for PacketCapture")
		}
		name := strings.TrimSpace(req.Args["name"])
		if name == "" {
			name = strings.TrimSpace(req.Args["capture"])
		}
		if name == "" {
			return operationPlan{}, fmt.Errorf("operation.args.name is required for PacketCapture")
		}
		if err := validatePacketCaptureName(name); err != nil {
			return operationPlan{}, err
		}
		return operationPlan{
			commands:       []string{fmt.Sprintf("show monitor capture %s buffer dump", name)},
			successMessage: "packet-capture buffer captured",
			outputs:        commandOutputs,
		}, nil
	default:
		return operationPlan{}, fmt.Errorf("operation kind %q is not supported", req.Kind)
	}
}

// packetCaptureNamePattern bounds the capture name to a syntactic
// shape that cannot inject IOS-XE CLI tokens. IOS-XE monitor-capture
// names are short identifiers; anything outside this pattern is
// either a bug in the operator's spec or an injection attempt.
var packetCaptureNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validatePacketCaptureName(name string) error {
	if !packetCaptureNamePattern.MatchString(name) {
		return fmt.Errorf("packet-capture name %q is invalid: must match %s",
			name, packetCaptureNamePattern.String())
	}
	return nil
}

func commandOutputs(results []transport.CommandResult) []opsv1alpha1.DeviceOperationOutput {
	outputs := make([]opsv1alpha1.DeviceOperationOutput, 0, len(results))
	for _, res := range results {
		out := opsv1alpha1.DeviceOperationOutput{
			Command: res.Command,
			Output:  res.Output,
			Err:     res.Err,
		}
		if out.Output != "" {
			redacted, didRedact := diagnostic.Redact(out.Output)
			out.Output = redacted
			out.Redacted = didRedact
			clipped, truncated := diagnostic.Truncate(out.Output, defaultInlineMaxBytes)
			out.Output = clipped
			out.Truncated = truncated
		}
		outputs = append(outputs, out)
	}
	return outputs
}

func validateDiagnosticOutputs(kind opsv1alpha1.OperationKind, commands []string, outputs []opsv1alpha1.DeviceOperationOutput) error {
	if len(outputs) != len(commands) {
		return fmt.Errorf("transport returned %d output(s) for %d command(s)", len(outputs), len(commands))
	}
	if kind != opsv1alpha1.OperationKindShowCommand {
		return nil
	}
	for i, cmd := range commands {
		if !showCommandRequiresOutput(cmd) {
			continue
		}
		if strings.TrimSpace(outputs[i].Output) == "" && strings.TrimSpace(outputs[i].Err) == "" {
			return fmt.Errorf("show command %q returned empty output", cmd)
		}
	}
	return nil
}

func showCommandRequiresOutput(cmd string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(cmd), " "))
	return normalized == "show version"
}

func lineDiff(baseline, observed string) string {
	baseLines := splitLines(baseline)
	obsLines := splitLines(observed)
	if strings.Join(baseLines, "\n") == strings.Join(obsLines, "\n") {
		return "no differences\n"
	}
	var b strings.Builder
	b.WriteString("--- baseline\n+++ observed\n")
	max := len(baseLines)
	if len(obsLines) > max {
		max = len(obsLines)
	}
	for i := 0; i < max; i++ {
		var left, right string
		if i < len(baseLines) {
			left = baseLines[i]
		}
		if i < len(obsLines) {
			right = obsLines[i]
		}
		switch {
		case i >= len(baseLines):
			b.WriteString("+")
			b.WriteString(right)
			b.WriteString("\n")
		case i >= len(obsLines):
			b.WriteString("-")
			b.WriteString(left)
			b.WriteString("\n")
		case left != right:
			b.WriteString("-")
			b.WriteString(left)
			b.WriteString("\n+")
			b.WriteString(right)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func splitCommandArg(s string) []string {
	separator := "\n"
	if !strings.Contains(s, "\n") && strings.Contains(s, ",") {
		separator = ","
	}
	parts := strings.Split(s, separator)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func configDiffNamespaceAllowed(namespace string) bool {
	raw := strings.TrimSpace(os.Getenv(envConfigDiffAllowedNamespaces))
	if raw == "" {
		return true
	}
	for _, part := range strings.Split(raw, ",") {
		if strings.TrimSpace(part) == namespace {
			return true
		}
	}
	return false
}

func operationSpanName(kind opsv1alpha1.OperationKind) string {
	switch kind {
	case opsv1alpha1.OperationKindShowCommand:
		return "cvk.op.show_command"
	case opsv1alpha1.OperationKindConfigDiff:
		return "cvk.op.config_diff"
	case opsv1alpha1.OperationKindPacketCapture:
		return "cvk.op.packet_capture"
	default:
		return "cvk.op.unknown"
	}
}

func terminal(phase opsv1alpha1.OperationPhase) bool {
	switch phase {
	case opsv1alpha1.OperationPhaseSucceeded, opsv1alpha1.OperationPhaseFailed, opsv1alpha1.OperationPhaseCancelled:
		return true
	default:
		return false
	}
}
