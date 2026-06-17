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
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

// fakeTransport implements transport.Interface + DiagnosticExecer for
// reconciler tests. The exec hook is the only method that matters;
// the others return ErrUnsupported / no-ops sufficient for the
// reconciler's narrow needs.
type fakeTransport struct {
	caps   transport.Capabilities
	exec   func(ctx context.Context, commands []string) ([]transport.CommandResult, error)
	closed bool
}

func (f *fakeTransport) Capabilities() transport.Capabilities { return f.caps }
func (f *fakeTransport) Fetch(_ context.Context, _ string) ([]byte, error) {
	return nil, transport.ErrUnsupported
}
func (f *fakeTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}
func (f *fakeTransport) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	return transport.ErrUnsupported
}
func (f *fakeTransport) Commit(context.Context, transport.TxHandle) error  { return nil }
func (f *fakeTransport) Discard(context.Context, transport.TxHandle) error { return nil }
func (f *fakeTransport) SaveStartup(context.Context) error                 { return transport.ErrUnsupported }
func (f *fakeTransport) Close() error                                      { f.closed = true; return nil }
func (f *fakeTransport) DiagnosticExec(ctx context.Context, commands []string) ([]transport.CommandResult, error) {
	if f.exec != nil {
		return f.exec(ctx, commands)
	}
	out := make([]transport.CommandResult, 0, len(commands))
	for _, c := range commands {
		out = append(out, transport.CommandResult{Command: c, Output: "ok: " + c})
	}
	return out, nil
}

// stubProvider returns a fixed transport.Interface (or nil to test
// the deferred-dial pending case).
type stubProvider struct{ tr transport.Interface }

func (s *stubProvider) GetTransport() transport.Interface { return s.tr }

// noopRecorder is a record.EventRecorder that drops events. The
// reconciler tolerates a nil Recorder; we set this anyway to mirror
// production wiring.
type noopRecorder struct{}

func (noopRecorder) Event(_ runtime.Object, _, _, _ string)            {}
func (noopRecorder) Eventf(_ runtime.Object, _, _, _ string, _ ...any) {}
func (noopRecorder) AnnotatedEventf(_ runtime.Object, _ map[string]string, _, _, _ string, _ ...any) {
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := configv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("core AddToScheme: %v", err)
	}
	return s
}

func newReconciler(t *testing.T, tr transport.Interface, objs ...runtime.Object) *Reconciler {
	t.Helper()
	scheme := newTestScheme(t)
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.IOSXEDiagnostic{}).
		WithRuntimeObjects(objs...).
		Build()
	return &Reconciler{
		Client:     cli,
		Recorder:   noopRecorder{},
		Scheme:     scheme,
		DeviceName: "cat9k-smoke",
		TP:         &stubProvider{tr: tr},
	}
}

// fixedTime returns a constant time.Now stub for deterministic
// CapturedAt assertions.
func fixedTime(t time.Time) func() time.Time { return func() time.Time { return t } }

func newDiag(name string, mutate func(*configv1alpha1.IOSXEDiagnostic)) *configv1alpha1.IOSXEDiagnostic {
	d := &configv1alpha1.IOSXEDiagnostic{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Generation: 1},
		Spec: configv1alpha1.IOSXEDiagnosticSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "cat9k-smoke"},
			Commands:  []string{"show version"},
		},
	}
	if mutate != nil {
		mutate(d)
	}
	return d
}

// TestReconcileHappyPath drives the headline one-shot capture and
// asserts the full status shape.
func TestReconcileHappyPath(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			out := make([]transport.CommandResult, 0, len(cmds))
			for _, c := range cmds {
				out = append(out, transport.CommandResult{
					Command: c,
					Output:  "Cisco IOS XE Software, Version 17.18.2",
				})
			}
			return out, nil
		},
	}
	d := newDiag("diag-version", nil)
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-version"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got configv1alpha1.IOSXEDiagnostic
	if err := r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-version"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != PhaseCompleted {
		t.Errorf("Phase=%s want %s", got.Status.Phase, PhaseCompleted)
	}
	if got.Status.ObservedGeneration != 1 {
		t.Errorf("observedGeneration=%d want 1", got.Status.ObservedGeneration)
	}
	if len(got.Status.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got.Status.Results))
	}
	res := got.Status.Results[0]
	if len(res.Commands) != 1 || res.Commands[0].Command != "show version" {
		t.Errorf("unexpected commands: %+v", res.Commands)
	}
	if !strings.Contains(res.Commands[0].Output, "17.18.2") {
		t.Errorf("output unexpected: %q", res.Commands[0].Output)
	}
	// Ready=True
	var foundReady bool
	for _, c := range got.Status.Conditions {
		if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
			foundReady = true
		}
	}
	if !foundReady {
		t.Errorf("expected Ready=True condition; got %+v", got.Status.Conditions)
	}
}

// TestReconcileSecretRedaction proves the default-on redaction
// filter strips an enable-secret line, sets CommandOutput.Redacted,
// and surfaces nothing of the secret in CR status.
func TestReconcileSecretRedaction(t *testing.T) {
	const sensitive = `interface Loopback0
 description telemetry
enable secret 5 $1$abcd$xyz`

	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindRESTCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{Command: cmds[0], Output: sensitive}}, nil
		},
	}
	d := newDiag("diag-show-run", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.Spec.Commands = []string{"show running-config"}
	})
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-show-run"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-show-run"}, &got)
	out := got.Status.Results[0].Commands[0]
	if !out.Redacted {
		t.Errorf("expected Redacted=true")
	}
	if strings.Contains(out.Output, "$1$abcd$xyz") {
		t.Errorf("secret leaked into status: %s", out.Output)
	}
	if !strings.Contains(out.Output, "<redacted") {
		t.Errorf("expected redaction marker; got %q", out.Output)
	}
}

// TestReconcileAllowSecretsBypassesRedaction pins the opt-out shape:
// when spec.allowSecrets=true the filter does not run.
func TestReconcileAllowSecretsBypassesRedaction(t *testing.T) {
	const sensitive = "enable secret 5 $1$abcd$xyz"
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindRESTCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{Command: cmds[0], Output: sensitive}}, nil
		},
	}
	d := newDiag("diag-elevated", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.Spec.AllowSecrets = true
	})
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-elevated"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-elevated"}, &got)
	out := got.Status.Results[0].Commands[0]
	if out.Redacted {
		t.Errorf("expected Redacted=false when allowSecrets=true")
	}
	if !strings.Contains(out.Output, "$1$abcd$xyz") {
		t.Errorf("expected unredacted output; got %q", out.Output)
	}
}

// TestReconcileTransportError covers the case where the transport
// returns a hard error. The capture's TransportError surfaces it,
// the CR is marked Failed-with-Ready=False but per-command results
// (if any) still land.
func TestReconcileTransportError(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, _ []string) ([]transport.CommandResult, error) {
			return nil, errors.New("dial broken")
		},
	}
	d := newDiag("diag-broken", nil)
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-broken"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-broken"}, &got)
	if len(got.Status.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got.Status.Results))
	}
	if !strings.Contains(got.Status.Results[0].TransportError, "dial broken") {
		t.Errorf("expected TransportError set; got %q", got.Status.Results[0].TransportError)
	}
	for _, c := range got.Status.Conditions {
		if c.Type == "Ready" && c.Status != metav1.ConditionFalse {
			t.Errorf("expected Ready=False on transport error")
		}
	}
}

// TestReconcileForeignDeviceIsNoop pins the device-targeting filter:
// a CR pointing at another device is silently ignored.
func TestReconcileForeignDeviceIsNoop(t *testing.T) {
	d := newDiag("diag-other", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.Spec.DeviceRef.Name = "another-device"
	})
	r := newReconciler(t, nil, d)
	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-other"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-other"}, &got)
	if got.Status.Phase != "" {
		t.Errorf("foreign-device CR should not have status; got Phase=%q", got.Status.Phase)
	}
}

// TestReconcileMaintenanceWindowExpired covers notAfter-in-past →
// terminal Expired phase.
func TestReconcileMaintenanceWindowExpired(t *testing.T) {
	expired := metav1.Time{Time: time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)}
	d := newDiag("diag-expired", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.Spec.NotAfter = &expired
	})
	r := newReconciler(t, nil, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-expired"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-expired"}, &got)
	if got.Status.Phase != PhaseExpired {
		t.Errorf("Phase=%s want %s", got.Status.Phase, PhaseExpired)
	}
}

// TestReconcileScheduledRotation proves retention trim — running
// MaxResults+1 captures keeps only MaxResults entries (oldest evicted).
func TestReconcileScheduledRotation(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
	}
	maxResults := int32(3)
	d := newDiag("diag-rolling", func(d *configv1alpha1.IOSXEDiagnostic) {
		d.Spec.Schedule = &configv1alpha1.DiagnosticSchedule{Interval: "30s"}
		d.Spec.Retention = &configv1alpha1.DiagnosticRetention{MaxResults: maxResults}
	})
	r := newReconciler(t, tr, d)

	for i := 0; i < int(maxResults)+2; i++ {
		// Each invocation moves the clock forward and resets the
		// CR's generation/observedGeneration so the schedule loop
		// re-fires. Real controller-runtime would re-queue at
		// NextCapture; we drive it manually.
		now := time.Date(2026, 4, 28, 13, i, 0, 0, time.UTC)
		r.Now = fixedTime(now)
		// Drive a fresh "due-now" by clearing NextCapture.
		var current configv1alpha1.IOSXEDiagnostic
		_ = r.Client.Get(context.Background(),
			types.NamespacedName{Namespace: "ns", Name: "diag-rolling"}, &current)
		current.Status.NextCapture = nil
		_ = r.Client.Status().Update(context.Background(), &current)

		if _, err := r.Reconcile(context.Background(),
			reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-rolling"}}); err != nil {
			t.Fatalf("Reconcile #%d: %v", i, err)
		}
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-rolling"}, &got)
	if int32(len(got.Status.Results)) != maxResults {
		t.Errorf("expected %d retained results, got %d",
			maxResults, len(got.Status.Results))
	}
	// Captures must be in ascending-time order.
	for i := 1; i < len(got.Status.Results); i++ {
		if got.Status.Results[i-1].CapturedAt.After(got.Status.Results[i].CapturedAt.Time) {
			t.Errorf("results not time-sorted at index %d", i)
		}
	}
}

// TestReconcileNoTransportRequeues covers the deferred-dial-pending
// case — reconciler should re-queue with a backoff, not fail.
func TestReconcileNoTransportRequeues(t *testing.T) {
	d := newDiag("diag-no-transport", nil)
	r := newReconciler(t, nil, d) // tr is nil
	res, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-no-transport"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected RequeueAfter > 0 when transport pending")
	}
}

// TestReconcileGNMIRejectsWithFailedPhase exercises the fail-fast
// path when the live transport doesn't implement DiagnosticExecer.
func TestReconcileGNMIRejectsWithFailedPhase(t *testing.T) {
	// gNMI-shape transport: SupportsDiagnosticExec=false AND no
	// DiagnosticExec method. The fakeTransport struct DOES define
	// the method, so we wrap it in an interface that excludes it.
	type noDiagExec struct{ caps transport.Capabilities }
	// no methods → not callable as transport.Interface; we need a
	// minimal stub.
	// Simpler: use fakeTransport but flip SupportsDiagnosticExec=false.
	// Note: fakeTransport DOES still implement DiagnosticExecer, but
	// the reconciler also checks the capability flag, so this still
	// proves the flag-based branch.
	_ = noDiagExec{}
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindGNMI, SupportsDiagnosticExec: false},
	}
	d := newDiag("diag-gnmi", nil)
	r := newReconciler(t, tr, d)
	r.Now = fixedTime(time.Date(2026, 4, 28, 13, 0, 0, 0, time.UTC))

	if _, err := r.Reconcile(context.Background(),
		reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "diag-gnmi"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEDiagnostic
	_ = r.Client.Get(context.Background(),
		types.NamespacedName{Namespace: "ns", Name: "diag-gnmi"}, &got)
	if got.Status.Phase != PhaseFailed {
		t.Errorf("Phase=%s want %s", got.Status.Phase, PhaseFailed)
	}
}
