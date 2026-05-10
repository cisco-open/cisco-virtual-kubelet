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

package deviceoperation

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

type staticTP struct{ tr transport.Interface }

func (s *staticTP) GetTransport() transport.Interface { return s.tr }

type fakeTransport struct {
	caps    transport.Capabilities
	results []transport.CommandResult
	calls   int
}

func (f *fakeTransport) Capabilities() transport.Capabilities { return f.caps }
func (f *fakeTransport) Fetch(context.Context, string) ([]byte, error) {
	return nil, transport.ErrUnsupported
}
func (f *fakeTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}
func (f *fakeTransport) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	return transport.ErrUnsupported
}
func (f *fakeTransport) Commit(context.Context, transport.TxHandle) error {
	return transport.ErrUnsupported
}
func (f *fakeTransport) Discard(context.Context, transport.TxHandle) error {
	return transport.ErrUnsupported
}
func (f *fakeTransport) SaveStartup(context.Context) error { return transport.ErrUnsupported }
func (f *fakeTransport) Close() error                      { return nil }
func (f *fakeTransport) DiagnosticExec(ctx context.Context, commands []string) ([]transport.CommandResult, error) {
	f.calls++
	if len(f.results) != 0 {
		return f.results, nil
	}
	out := make([]transport.CommandResult, 0, len(commands))
	for _, cmd := range commands {
		out = append(out, transport.CommandResult{Command: cmd, Output: "ok"})
	}
	return out, nil
}

func TestReconcileShowCommand(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("show", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Commands = []string{"show version"}
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: []transport.CommandResult{{
			Command: "show version",
			Output:  "Cisco IOS XE Software",
		}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{
		Client:     c,
		DeviceName: "dev1",
		TP:         &staticTP{tr: tr},
		Now:        func() time.Time { return time.Unix(100, 0).UTC() },
	}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q want %q; message=%s", got.Status.Phase, opsv1alpha1.OperationPhaseSucceeded, got.Status.Message)
	}
	if len(got.Status.Outputs) != 1 || got.Status.Outputs[0].Output != "Cisco IOS XE Software" {
		t.Fatalf("unexpected outputs: %#v", got.Status.Outputs)
	}
	if tr.calls != 1 {
		t.Fatalf("DiagnosticExec calls=%d want 1", tr.calls)
	}
}

func TestReconcileRejectsWriteCommandBeforeTransportExec(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("bad", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Commands = []string{"configure terminal"}
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{
		Client:     c,
		DeviceName: "dev1",
		TP:         &staticTP{tr: tr},
		Now:        func() time.Time { return time.Unix(100, 0).UTC() },
	}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseFailed {
		t.Fatalf("phase=%q want %q", got.Status.Phase, opsv1alpha1.OperationPhaseFailed)
	}
	if !strings.Contains(got.Status.Message, "not allowed") {
		t.Fatalf("message=%q want command allowlist reason", got.Status.Message)
	}
	if tr.calls != 0 {
		t.Fatalf("DiagnosticExec calls=%d want 0", tr.calls)
	}
}

func TestReconcileConfigDiffWithBaseline(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("diff", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindConfigDiff
		op.Spec.Operation.Args = map[string]string{
			"baseline": "hostname old\ninterface Loopback0",
		}
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: []transport.CommandResult{{
			Command: "show running-config",
			Output:  "hostname new\ninterface Loopback0",
		}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{Client: c, DeviceName: "dev1", TP: &staticTP{tr: tr}}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q want succeeded: %s", got.Status.Phase, got.Status.Message)
	}
	if len(got.Status.Outputs) != 1 || !strings.Contains(got.Status.Outputs[0].Output, "-hostname old") ||
		!strings.Contains(got.Status.Outputs[0].Output, "+hostname new") {
		t.Fatalf("unexpected diff output: %#v", got.Status.Outputs)
	}
}

func TestReconcileConfigDiffAllowedNamespaceGate(t *testing.T) {
	t.Setenv(envConfigDiffAllowedNamespaces, "other, default ")
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("diff", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindConfigDiff
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: []transport.CommandResult{{
			Command: "show running-config",
			Output:  "hostname edge-01",
		}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{Client: c, DeviceName: "dev1", TP: &staticTP{tr: tr}}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q want succeeded: %s", got.Status.Phase, got.Status.Message)
	}
	if tr.calls != 1 {
		t.Fatalf("DiagnosticExec calls=%d want 1", tr.calls)
	}
}

func TestReconcileConfigDiffRejectedNamespaceGate(t *testing.T) {
	t.Setenv(envConfigDiffAllowedNamespaces, "network,ops")
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("diff", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindConfigDiff
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{Client: c, DeviceName: "dev1", TP: &staticTP{tr: tr}}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseFailed {
		t.Fatalf("phase=%q want failed", got.Status.Phase)
	}
	if !operationConditionIs(got.Status.Conditions, "Ready", metav1.ConditionFalse, "NamespaceNotAuthorized") {
		t.Fatalf("Ready condition missing NamespaceNotAuthorized: %#v", got.Status.Conditions)
	}
	if tr.calls != 0 {
		t.Fatalf("DiagnosticExec calls=%d want 0", tr.calls)
	}
}

func TestReconcilePacketCaptureReadsExistingBuffer(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("capture", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindPacketCapture
		op.Spec.Operation.Args = map[string]string{"name": "cvkcap"}
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{Client: c, DeviceName: "dev1", TP: &staticTP{tr: tr}}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q want succeeded: %s", got.Status.Phase, got.Status.Message)
	}
	if len(got.Status.Outputs) != 1 || got.Status.Outputs[0].Command != "show monitor capture cvkcap buffer dump" {
		t.Fatalf("unexpected capture output: %#v", got.Status.Outputs)
	}
}

func TestReconcilePacketCaptureWritesLargeOutputArtifact(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("capture", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindPacketCapture
		op.Spec.Operation.Args = map[string]string{"name": "cvkcap"}
	})
	fullOutput := strings.Repeat("packet line\n", 30*1024)
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: []transport.CommandResult{{
			Command: "show monitor capture cvkcap buffer dump",
			Output:  fullOutput,
		}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{Client: c, Scheme: scheme, DeviceName: "dev1", TP: &staticTP{tr: tr}}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q want succeeded: %s", got.Status.Phase, got.Status.Message)
	}
	wantURI := "configmap://default/capture-output/output"
	if len(got.Status.ArtifactURIs) != 1 || got.Status.ArtifactURIs[0] != wantURI {
		t.Fatalf("artifactURIs=%v want [%s]", got.Status.ArtifactURIs, wantURI)
	}
	if len(got.Status.Outputs) != 1 || !strings.Contains(got.Status.Outputs[0].Output, "<truncated; see artifactURIs>") {
		t.Fatalf("preview missing artifact footer: %#v", got.Status.Outputs)
	}
	if len(got.Status.Outputs[0].Output) > defaultInlineMaxBytes {
		t.Fatalf("preview length=%d exceeds inline max %d", len(got.Status.Outputs[0].Output), defaultInlineMaxBytes)
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: "capture-output"}, &cm); err != nil {
		t.Fatalf("get artifact ConfigMap: %v", err)
	}
	if cm.Data["output"] != fullOutput {
		t.Fatalf("artifact output length=%d want %d", len(cm.Data["output"]), len(fullOutput))
	}
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].Name != op.Name {
		t.Fatalf("ownerReferences=%#v want DeviceOperation owner", cm.OwnerReferences)
	}
}

func TestReconcilePacketCaptureRejectsOversizedArtifact(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("capture", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindPacketCapture
		op.Spec.Operation.Args = map[string]string{"name": "cvkcap"}
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: []transport.CommandResult{{
			Command: "show monitor capture cvkcap buffer dump",
			Output:  strings.Repeat("x", artifactMaxBytes+1),
		}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{Client: c, Scheme: scheme, DeviceName: "dev1", TP: &staticTP{tr: tr}}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseFailed {
		t.Fatalf("phase=%q want failed", got.Status.Phase)
	}
	if !operationConditionIs(got.Status.Conditions, "Ready", metav1.ConditionFalse, "ArtifactTooLarge") {
		t.Fatalf("Ready condition missing ArtifactTooLarge: %#v", got.Status.Conditions)
	}
	if len(got.Status.ArtifactURIs) != 0 {
		t.Fatalf("artifactURIs=%v want none", got.Status.ArtifactURIs)
	}
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := configv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("config scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("ops scheme: %v", err)
	}
	return scheme
}

func newOperation(name string, mutate func(*opsv1alpha1.DeviceOperation)) *opsv1alpha1.DeviceOperation {
	op := &opsv1alpha1.DeviceOperation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
		},
		Spec: opsv1alpha1.DeviceOperationSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev1"},
			Operation: opsv1alpha1.DeviceOperationRequest{
				Kind: opsv1alpha1.OperationKindShowCommand,
			},
		},
	}
	if mutate != nil {
		mutate(op)
	}
	return op
}

func operationConditionIs(conds []metav1.Condition, typ string, status metav1.ConditionStatus, reason string) bool {
	for _, cond := range conds {
		if cond.Type == typ && cond.Status == status && cond.Reason == reason {
			return true
		}
	}
	return false
}
