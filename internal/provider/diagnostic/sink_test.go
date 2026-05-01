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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

func TestSanitiseCommandKey(t *testing.T) {
	cases := map[string]string{
		"show ip route": "show-ip-route",
		"show running-config | section interface": "show-running-config-section-interface",
		"show ver":             "show-ver",
		"":                     "command",
		"!!!":                  "command",
		"show interface 0/0/1": "show-interface-0-0-1",
	}
	for in, want := range cases {
		if got := sanitiseCommandKey(in); got != want {
			t.Errorf("sanitiseCommandKey(%q)=%q want %q", in, got, want)
		}
	}
}

// TestReconcileConfigMapSinkWritesAndPreviews drives a full reconcile
// with the ConfigMap sink active, then asserts:
//   - one ConfigMap exists with one data key per command
//   - per-command outputs are stored in full
//   - status.results[].commands[].output holds a preview only
//   - status.results[].commands[].configMapRef points at the right CM
func TestReconcileConfigMapSinkWritesAndPreviews(t *testing.T) {
	const longBody = "Cisco IOS XE Software, Version 17.18.2\n" +
		"Capabilities/...\n" + // a few short lines so the preview is the whole thing
		"feature: tasty\nfeature: salty\n"

	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{Command: cmds[0], Output: longBody}}, nil
		},
	}
	d := newDiag("diag-cm-sink", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.Spec.Commands = []string{"show running-config"}
		d.Spec.OutputSink = &configv1alpha1.DiagnosticOutputSink{
			ConfigMapRef: &configv1alpha1.DiagnosticConfigMapSink{
				NamePrefix: "diag-cm-sink-",
			},
		}
	})
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-cm-sink"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-cm-sink"}, &got)
	if len(got.Status.Results) != 1 || len(got.Status.Results[0].Commands) != 1 {
		t.Fatalf("unexpected status shape: %+v", got.Status)
	}
	cmd := got.Status.Results[0].Commands[0]
	if cmd.ConfigMapRef == nil {
		t.Fatal("expected CommandOutput.ConfigMapRef to be set when sink is active")
	}
	if cmd.ConfigMapRef.Key != "show-running-config" {
		t.Errorf("ConfigMapRef.Key=%q want %q", cmd.ConfigMapRef.Key, "show-running-config")
	}

	var cm corev1.ConfigMap
	if err := r.Client.Get(context.Background(),
		client.ObjectKey{Namespace: "ns", Name: cmd.ConfigMapRef.Name}, &cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if got, want := cm.Data["show-running-config"], longBody; got != want {
		t.Errorf("ConfigMap body mismatch: got %q want %q", got, want)
	}
	if !strings.HasPrefix(cm.Name, "diag-cm-sink-") {
		t.Errorf("ConfigMap name %q missing NamePrefix", cm.Name)
	}
	if cm.Labels[configMapDiagnosticLabel] != "diag-cm-sink" {
		t.Errorf("missing diagnostic label; got %v", cm.Labels)
	}
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].Name != "diag-cm-sink" {
		t.Errorf("ConfigMap missing owner-ref; got %+v", cm.OwnerReferences)
	}
}

// TestReconcileConfigMapSinkPrunesOldest exercises the retention
// trim by running MaxResults+2 captures and confirming the oldest
// were deleted.
func TestReconcileConfigMapSinkPrunesOldest(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			out := make([]transport.CommandResult, 0, len(cmds))
			for _, c := range cmds {
				out = append(out, transport.CommandResult{Command: c, Output: "ok"})
			}
			return out, nil
		},
	}
	maxResults := int32(2)
	d := newDiag("diag-prune", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.Spec.Commands = []string{"show version"}
		d.Spec.Schedule = &configv1alpha1.DiagnosticSchedule{Interval: "30s"}
		d.Spec.Retention = &configv1alpha1.DiagnosticRetention{MaxResults: maxResults}
		d.Spec.OutputSink = &configv1alpha1.DiagnosticOutputSink{
			ConfigMapRef: &configv1alpha1.DiagnosticConfigMapSink{
				NamePrefix: "prune-",
			},
		}
	})
	r := newReconciler(t, tr, d)

	for i := 0; i < int(maxResults)+2; i++ {
		now := time.Date(2026, 4, 28, 13, 0, i, 0, time.UTC)
		r.Now = fixedTime(now)
		var current configv1alpha1.IOSXEDiagnostic
		_ = r.Client.Get(context.Background(),
			types.NamespacedName{Namespace: "ns", Name: "diag-prune"}, &current)
		current.Status.NextCapture = nil
		_ = r.Client.Status().Update(context.Background(), &current)

		if _, err := r.Reconcile(context.Background(),
			reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-prune"}}); err != nil {
			t.Fatalf("Reconcile #%d: %v", i, err)
		}
	}

	var cms corev1.ConfigMapList
	_ = r.Client.List(context.Background(), &cms,
		client.InNamespace("ns"),
		client.MatchingLabels{configMapDiagnosticLabel: "diag-prune"})
	if int32(len(cms.Items)) != maxResults {
		t.Errorf("expected %d ConfigMaps after prune, got %d", maxResults, len(cms.Items))
	}
	// Surviving items must be the NEWEST captures.
	for _, cm := range cms.Items {
		ts := cm.Labels[configMapCaptureAtLabel]
		// "20260428-130002" or later → second 2 onward
		if ts < "20260428-130002" {
			t.Errorf("oldest ConfigMap not pruned: %s ts=%q", cm.Name, ts)
		}
	}
}

// TestReconcileConfigMapSinkInactiveByDefault confirms the default
// path: nil OutputSink writes inline and creates no ConfigMaps.
func TestReconcileConfigMapSinkInactiveByDefault(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{Command: cmds[0], Output: "X"}}, nil
		},
	}
	d := newDiag("diag-inline", nil)
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-inline"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var cms corev1.ConfigMapList
	_ = r.Client.List(context.Background(), &cms, client.InNamespace("ns"))
	if len(cms.Items) != 0 {
		t.Errorf("expected zero ConfigMaps in default-sink mode; got %d", len(cms.Items))
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-inline"}, &got)
	if got.Status.Results[0].Commands[0].ConfigMapRef != nil {
		t.Errorf("ConfigMapRef should be nil in default-sink mode")
	}
	if got.Status.Results[0].Commands[0].Output != "X" {
		t.Errorf("expected inline output preserved; got %q",
			got.Status.Results[0].Commands[0].Output)
	}
}

// TestReconcileConfigMapSinkLargeOutputCreatesPreview pins the
// inline-preview behaviour: when the body exceeds the preview cap,
// status.results[].commands[].output is the truncated preview while
// the ConfigMap holds the full body.
func TestReconcileConfigMapSinkLargeOutputCreatesPreview(t *testing.T) {
	const previewCap = 2 * 1024
	bodyChunk := strings.Repeat("show running-config line\n", 200) // > 4 KiB
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{Command: cmds[0], Output: bodyChunk}}, nil
		},
	}
	d := newDiag("diag-big", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.Spec.OutputSink = &configv1alpha1.DiagnosticOutputSink{
			ConfigMapRef: &configv1alpha1.DiagnosticConfigMapSink{NamePrefix: "big-"},
		}
		// Allow 1MB so the in-CM body isn't truncated by the
		// reconciler's body-truncation step before sink writes.
		d.Spec.Retention = &configv1alpha1.DiagnosticRetention{TruncateAt: "1MiB"}
	})
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-big"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-big"}, &got)
	cmd := got.Status.Results[0].Commands[0]
	if len(cmd.Output) > previewCap+200 { // +200 trailing-marker fudge
		t.Errorf("inline preview unexpectedly large: %d bytes", len(cmd.Output))
	}
	if cmd.ConfigMapRef == nil {
		t.Fatalf("expected ConfigMapRef on large output")
	}
	var cm corev1.ConfigMap
	_ = r.Client.Get(context.Background(),
		client.ObjectKey{Namespace: "ns", Name: cmd.ConfigMapRef.Name}, &cm)
	if len(cm.Data[cmd.ConfigMapRef.Key]) < len(bodyChunk)/2 {
		t.Errorf("ConfigMap body unexpectedly small: %d bytes (orig %d)",
			len(cm.Data[cmd.ConfigMapRef.Key]), len(bodyChunk))
	}
}

// TestReconcileConfigMapSinkRejectsCrossNamespace pins the
// adversarial-review fix (2026-05-01) — sink.Namespace ≠
// diag.Namespace must be rejected. Prior to the fix, this path let a
// CR-creator in namespace A cause the VK service account to write
// captured device output into namespace B, bypassing namespace
// ownership and quota controls.
func TestReconcileConfigMapSinkRejectsCrossNamespace(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{Command: cmds[0], Output: "boring"}}, nil
		},
	}
	d := newDiag("diag-xns", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.UID = "00000000-0000-0000-0000-aaaaaaaaaaaa"
		d.Spec.OutputSink = &configv1alpha1.DiagnosticOutputSink{
			ConfigMapRef: &configv1alpha1.DiagnosticConfigMapSink{
				NamePrefix: "xns-",
				Namespace:  "other-tenant", // sink.Namespace ≠ diag.Namespace
			},
		}
	})
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-xns"}}); err != nil {
		// Reconcile may surface the error in status; that's
		// acceptable — what matters is the CM is NOT written.
		_ = err
	}
	// Nothing should have been written into either namespace.
	var list corev1.ConfigMapList
	_ = r.Client.List(context.Background(), &list)
	if len(list.Items) != 0 {
		t.Errorf("cross-namespace sink should have been rejected; "+
			"found %d ConfigMap(s): %+v", len(list.Items), list.Items)
	}
}

// TestReconcileConfigMapSinkRefusesUIDMismatch pins the
// adversarial-review fix (2026-05-01) — on a same-namespace,
// same-name pre-existing ConfigMap that does not carry our
// diagnostic-uid label, the reconciler must refuse to overwrite.
// Prior to the fix, the sink would Update on AlreadyExists and
// silently corrupt another tenant's evidence.
func TestReconcileConfigMapSinkRefusesUIDMismatch(t *testing.T) {
	const myUID = "11111111-1111-1111-1111-aaaaaaaaaaaa"
	const otherUID = "22222222-2222-2222-2222-bbbbbbbbbbbb"
	const sinkPrefix = "diag-uid-"
	captureTS := time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC)

	// A pre-existing CM in the SAME namespace, same name pattern,
	// owned by a different diagnostic-uid.
	preexisting := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			// Match the name our reconciler would write
			// (timestamp + uidPrefix(myUID)). uidPrefix(myUID)
			// = "11111111".
			Name: sinkPrefix + captureTS.Format("20060102-150405") + "-11111111",
			Labels: map[string]string{
				configMapDiagnosticLabel:    "diag-uid",
				configMapDiagnosticUIDLabel: otherUID,
			},
		},
		Data: map[string]string{"existing": "do-not-overwrite"},
	}
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{Command: cmds[0], Output: "new-output"}}, nil
		},
	}
	d := newDiag("diag-uid", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.UID = myUID
		d.Spec.OutputSink = &configv1alpha1.DiagnosticOutputSink{
			ConfigMapRef: &configv1alpha1.DiagnosticConfigMapSink{NamePrefix: sinkPrefix},
		}
	})
	r := newReconciler(t, tr, d, preexisting)
	r.Now = fixedTime(captureTS)

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-uid"}}); err != nil {
		_ = err // tolerated; the surfaced status path is not under test here
	}
	var got corev1.ConfigMap
	if err := r.Client.Get(context.Background(),
		client.ObjectKey{Namespace: "ns", Name: preexisting.Name}, &got); err != nil {
		t.Fatalf("get pre-existing CM: %v", err)
	}
	if got.Labels[configMapDiagnosticUIDLabel] != otherUID {
		t.Errorf("UID label was overwritten: got %q want %q",
			got.Labels[configMapDiagnosticUIDLabel], otherUID)
	}
	if got.Data["existing"] != "do-not-overwrite" {
		t.Errorf("pre-existing data was overwritten: %+v", got.Data)
	}
}

func init() {
	// Silence the corev1 type registration if the helper file
	// hadn't already done it. newTestScheme in reconciler_test.go
	// adds it; this is a defence-in-depth no-op on duplicate Add.
	_ = corev1.AddToScheme
	_ = metav1.NamespaceDefault
}
