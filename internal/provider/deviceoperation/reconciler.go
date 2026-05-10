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
	Reader     client.Reader
	Recorder   record.EventRecorder
	Scheme     *runtime.Scheme
	DeviceName string
	TP         TransportProvider

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
	)

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
		if req.Args != nil {
			if cmd := strings.TrimSpace(req.Args["command"]); cmd != "" {
				return operationPlan{
					commands:       []string{cmd},
					successMessage: "packet-capture command completed",
					outputs:        commandOutputs,
				}, nil
			}
			name := strings.TrimSpace(req.Args["name"])
			if name == "" {
				name = strings.TrimSpace(req.Args["capture"])
			}
			if name != "" {
				return operationPlan{
					commands:       []string{fmt.Sprintf("show monitor capture %s buffer dump", name)},
					successMessage: "packet-capture buffer captured",
					outputs:        commandOutputs,
				}, nil
			}
		}
		return operationPlan{}, fmt.Errorf("operation.args.name or operation.args.command is required for PacketCapture")
	default:
		return operationPlan{}, fmt.Errorf("operation kind %q is not supported", req.Kind)
	}
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
