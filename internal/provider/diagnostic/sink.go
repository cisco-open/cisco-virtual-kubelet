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
	"regexp"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// inlinePreviewBytes is the size of the inline preview retained in
// CommandOutput.Output when the ConfigMap sink is active. Operators
// running `kubectl describe iosxediag` still see the first chunk of
// every command's output without having to chase the ConfigMap.
const inlinePreviewBytes = 2 * 1024

// configMapDiagnosticLabel is set on every captured ConfigMap so
// operators can list with `kubectl get cm -l <label>=<crname>` and
// the reconciler can find / clean up its own ConfigMaps without
// listing every CM in the namespace.
const (
	configMapDiagnosticLabel = "cisco.vk/diagnostic"
	configMapCaptureAtLabel  = "cisco.vk/diagnostic-capturedAt"
)

// dnsLabelSafeRe is used to sanitise the command-as-key for use in
// ConfigMap.data. ConfigMap data keys allow [-._a-zA-Z0-9]; everything
// else is replaced with '-'. Trailing dashes from collapsing runs of
// invalid chars are trimmed so keys stay readable.
var dnsLabelSafeRe = regexp.MustCompile(`[^-._a-zA-Z0-9]+`)

// sinkActive returns true when spec.outputSink is configured to write
// to a ConfigMap (rather than inline). nil sink or Inline=true means
// inline storage.
func sinkActive(diag *configv1alpha1.IOSXEDiagnostic) bool {
	if diag.Spec.OutputSink == nil {
		return false
	}
	if diag.Spec.OutputSink.ConfigMapRef == nil {
		return false
	}
	return true
}

// writeToConfigMap creates a ConfigMap holding one capture's full
// output. Mutates CommandOutput entries in `commands` to set
// ConfigMapRef and to replace Output with an inline preview.
//
// The sink writes ONE ConfigMap per capture, with the per-command
// outputs as separate keys. This keeps the ConfigMap count bounded
// by Retention.MaxResults rather than commands × MaxResults.
func (r *Reconciler) writeToConfigMap(
	ctx context.Context,
	diag *configv1alpha1.IOSXEDiagnostic,
	capture *configv1alpha1.DiagnosticCapture,
) error {
	sink := diag.Spec.OutputSink.ConfigMapRef
	ns := sink.Namespace
	if ns == "" {
		ns = diag.Namespace
	}
	name := fmt.Sprintf("%s%s",
		sink.NamePrefix,
		capture.CapturedAt.Format("20060102-150405"))

	// Build data map: key = sanitised command, value = full output.
	// Empty outputs and per-command errors land as data entries too
	// so the ConfigMap is a complete capture record.
	data := map[string]string{}
	usedKeys := map[string]int{}
	for i, cmd := range capture.Commands {
		key := sanitiseCommandKey(cmd.Command)
		// Collisions (two commands sanitising to the same key)
		// disambiguate with a numeric suffix.
		if n, exists := usedKeys[key]; exists {
			usedKeys[key] = n + 1
			key = fmt.Sprintf("%s-%d", key, n+1)
		} else {
			usedKeys[key] = 0
		}
		data[key] = cmd.Output
		preview, _ := Truncate(cmd.Output, inlinePreviewBytes)
		capture.Commands[i].Output = preview
		capture.Commands[i].ConfigMapRef = &configv1alpha1.CapturedConfigMapRef{
			Name:      name,
			Namespace: ns,
			Key:       key,
		}
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				configMapDiagnosticLabel: diag.Name,
				configMapCaptureAtLabel:  capture.CapturedAt.Format("20060102-150405"),
			},
		},
		Data: data,
	}
	// Owner reference makes ConfigMap deletion cascade with the CR
	// — only when the ConfigMap is in the SAME namespace as the CR
	// (cross-namespace OwnerReferences are forbidden by the API
	// server). For cross-namespace sinks the operator gives up
	// cascade-delete and accepts manual cleanup; we surface this in
	// the spec doc.
	if ns == diag.Namespace {
		cm.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: configv1alpha1.GroupVersion.String(),
			Kind:       "IOSXEDiagnostic",
			Name:       diag.Name,
			UID:        diag.UID,
			Controller: ptrBool(true),
		}}
	}
	// Create-or-update. A re-reconcile of the same generation
	// before NextCapture would land at the same timestamp —
	// idempotent re-write rather than 409 Conflict.
	if err := r.Client.Create(ctx, cm); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create ConfigMap %s/%s: %w", ns, name, err)
		}
		var existing corev1.ConfigMap
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &existing); err != nil {
			return fmt.Errorf("get existing ConfigMap %s/%s: %w", ns, name, err)
		}
		existing.Data = data
		existing.Labels = cm.Labels
		if err := r.Client.Update(ctx, &existing); err != nil {
			return fmt.Errorf("update ConfigMap %s/%s: %w", ns, name, err)
		}
	}
	return nil
}

// pruneOldConfigMaps deletes oldest ConfigMaps for this CR when their
// count exceeds Retention.MaxResults. Sorted by capture-timestamp
// label so the eviction order is deterministic regardless of the
// API server's list ordering.
func (r *Reconciler) pruneOldConfigMaps(
	ctx context.Context,
	diag *configv1alpha1.IOSXEDiagnostic,
) error {
	maxResults := defaultMaxResults
	if diag.Spec.Retention != nil && diag.Spec.Retention.MaxResults > 0 {
		maxResults = int(diag.Spec.Retention.MaxResults)
	}
	ns := diag.Spec.OutputSink.ConfigMapRef.Namespace
	if ns == "" {
		ns = diag.Namespace
	}

	var list corev1.ConfigMapList
	if err := r.Client.List(ctx, &list,
		client.InNamespace(ns),
		client.MatchingLabels{configMapDiagnosticLabel: diag.Name},
	); err != nil {
		return fmt.Errorf("list ConfigMaps for diagnostic %q: %w", diag.Name, err)
	}
	if len(list.Items) <= maxResults {
		return nil
	}

	sort.SliceStable(list.Items, func(i, j int) bool {
		return list.Items[i].Labels[configMapCaptureAtLabel] <
			list.Items[j].Labels[configMapCaptureAtLabel]
	})
	excess := len(list.Items) - maxResults
	for i := 0; i < excess; i++ {
		cm := list.Items[i]
		if err := r.Client.Delete(ctx, &cm); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete oldest ConfigMap %s/%s: %w", cm.Namespace, cm.Name, err)
		}
	}
	return nil
}

// sanitiseCommandKey reduces an IOS-XE command to a ConfigMap data
// key. "show ip route" → "show-ip-route"; "show running-config |
// section interface" → "show-running-config-section-interface".
// Empty result (e.g. all-symbol input) falls back to "command".
func sanitiseCommandKey(cmd string) string {
	out := dnsLabelSafeRe.ReplaceAllString(strings.TrimSpace(cmd), "-")
	out = strings.Trim(out, "-")
	if out == "" {
		return "command"
	}
	if len(out) > 253 {
		out = out[:253]
	}
	return out
}

func ptrBool(b bool) *bool { return &b }
