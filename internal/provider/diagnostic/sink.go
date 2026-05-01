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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// uidPrefix returns the leading 8 chars of a UID for use as a
// ConfigMap-name suffix. UIDs are RFC 4122 form so the first 8
// hex chars give 32 bits of disambiguation — enough to collide
// only on adversarial input, which the namespace-equality check
// already excludes.
func uidPrefix(uid types.UID) string {
	s := strings.ReplaceAll(string(uid), "-", "")
	if len(s) < 8 {
		return s
	}
	return s[:8]
}

// inlinePreviewBytes is the size of the inline preview retained in
// CommandOutput.Output when the ConfigMap sink is active. Operators
// running `kubectl describe iosxediag` still see the first chunk of
// every command's output without having to chase the ConfigMap.
const inlinePreviewBytes = 2 * 1024

// configMapDiagnosticLabel is set on every captured ConfigMap so
// operators can list with `kubectl get cm -l <label>=<crname>` and
// the reconciler can find / clean up its own ConfigMaps without
// listing every CM in the namespace.
//
// Adversarial-review fix (2026-05-01): the diagnostic-uid label is
// the *authoritative* identity for prune + update — name alone
// collides across namespaces and is mutable across CR delete-and-
// recreate cycles. The Name label remains for human-friendly
// `kubectl get cm -l ...` filtering but the reconciler does not
// trust it for ownership decisions.
const (
	configMapDiagnosticLabel    = "cisco.vk/diagnostic"
	configMapDiagnosticUIDLabel = "cisco.vk/diagnostic-uid"
	configMapCaptureAtLabel     = "cisco.vk/diagnostic-capturedAt"
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
	// Adversarial-review fix (2026-05-01): cross-namespace sinks are
	// rejected. Honouring spec.outputSink.configMapRef.namespace let
	// any user with create-IOSXEDiagnostic in namespace A cause the
	// VK service account to write captured device output into
	// namespace B, bypassing namespace ownership and quota controls.
	// The CR ships with a same-namespace constraint until a
	// SubjectAccessReview-backed allowlist lands.
	if sink.Namespace != "" && sink.Namespace != diag.Namespace {
		return fmt.Errorf("cross-namespace ConfigMap sinks are not permitted: "+
			"sink namespace %q must equal CR namespace %q",
			sink.Namespace, diag.Namespace)
	}
	ns := diag.Namespace
	// Include the diag UID in the ConfigMap name so two same-named
	// CRs in different namespaces never collide on a shared sink
	// namespace, and a delete-and-recreate cycle of the CR produces
	// a fresh CM identity instead of overwriting old captures.
	uidShort := uidPrefix(diag.UID)
	name := fmt.Sprintf("%s%s-%s",
		sink.NamePrefix,
		capture.CapturedAt.Format("20060102-150405"),
		uidShort)

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
				configMapDiagnosticLabel:    diag.Name,
				configMapDiagnosticUIDLabel: string(diag.UID),
				configMapCaptureAtLabel:     capture.CapturedAt.Format("20060102-150405"),
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
	//
	// Adversarial-review fix (2026-05-01): on AlreadyExists, refuse
	// to update unless the existing CM carries our diagnostic-uid
	// label. The UID is included in the CM name so a real collision
	// indicates either a UID-prefix hash collision (cosmetically
	// unlikely with 32 bits and same-namespace enforcement) or a
	// pre-existing CM the operator authored manually.
	if err := r.Client.Create(ctx, cm); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create ConfigMap %s/%s: %w", ns, name, err)
		}
		var existing corev1.ConfigMap
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &existing); err != nil {
			return fmt.Errorf("get existing ConfigMap %s/%s: %w", ns, name, err)
		}
		if existing.Labels[configMapDiagnosticUIDLabel] != string(diag.UID) {
			return fmt.Errorf("refusing to overwrite ConfigMap %s/%s: "+
				"owned by diagnostic-uid %q, not this CR's UID %q",
				ns, name,
				existing.Labels[configMapDiagnosticUIDLabel], diag.UID)
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
	// Adversarial-review fix (2026-05-01): prune by UID, not Name —
	// a name-only filter would let a same-named CR in another tenant
	// influence (or be deleted by) this CR's prune sweep. The
	// same-namespace constraint above already prevents the cross-NS
	// case but the UID filter is the defense in depth.
	ns := diag.Namespace

	var list corev1.ConfigMapList
	if err := r.Client.List(ctx, &list,
		client.InNamespace(ns),
		client.MatchingLabels{configMapDiagnosticUIDLabel: string(diag.UID)},
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
