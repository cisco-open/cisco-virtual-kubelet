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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

type staticTP struct{ tr transport.Interface }

func (s *staticTP) GetTransport() transport.Interface { return s.tr }

// staleDeviceOperationClient models the controller-runtime split between a
// cached client read and an authoritative status writer. Get deliberately
// returns an older DeviceOperation snapshot while every other operation,
// including Status().Update, delegates to the API-backed client.
type staleDeviceOperationClient struct {
	client.Client
	stale *opsv1alpha1.DeviceOperation
	gets  int
}

func (c *staleDeviceOperationClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if got, ok := obj.(*opsv1alpha1.DeviceOperation); ok &&
		c.stale != nil && key == client.ObjectKeyFromObject(c.stale) {
		c.gets++
		c.stale.DeepCopyInto(got)
		return nil
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type fakeTransport struct {
	caps    transport.Capabilities
	results []transport.CommandResult
	calls   int
	onExec  func()
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
	if f.onExec != nil {
		f.onExec()
	}
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

func TestReconcileReadsTerminalStatusFromAPIReader(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("terminal-cache-race", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Commands = []string{"show version"}
	})
	op.Status = opsv1alpha1.DeviceOperationStatus{
		Phase:              opsv1alpha1.OperationPhaseSucceeded,
		ObservedGeneration: op.Generation,
		CompletionTime:     &metav1.Time{Time: time.Unix(100, 0).UTC()},
		Message:            "1 command(s) completed",
		Outputs: []opsv1alpha1.DeviceOperationOutput{{
			Command: "show version",
			Output:  "stable terminal output",
		}},
	}

	apiClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	stale := op.DeepCopy()
	stale.Status.Phase = opsv1alpha1.OperationPhaseRunning
	stale.Status.CompletionTime = nil
	stale.Status.Message = "operation is running"
	stale.Status.Outputs = nil
	cachedClient := &staleDeviceOperationClient{Client: apiClient, stale: stale}
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: []transport.CommandResult{{
			Command: "show version",
			Output:  "unexpected duplicate execution",
		}},
	}
	r := &Reconciler{
		Client:     cachedClient,
		Reader:     apiClient,
		DeviceName: "dev1",
		TP:         &staticTP{tr: tr},
		Now:        func() time.Time { return time.Unix(200, 0).UTC() },
	}

	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if cachedClient.gets != 0 {
		t.Fatalf("cached DeviceOperation reads=%d want 0", cachedClient.gets)
	}
	if tr.calls != 0 {
		t.Fatalf("DiagnosticExec calls=%d want 0 for terminal generation", tr.calls)
	}

	var got opsv1alpha1.DeviceOperation
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(op), &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q want %q", got.Status.Phase, opsv1alpha1.OperationPhaseSucceeded)
	}
	if len(got.Status.Outputs) != 1 || got.Status.Outputs[0].Output != "stable terminal output" {
		t.Fatalf("terminal outputs changed: %#v", got.Status.Outputs)
	}
}

func TestReconcileDoesNotWriteResultAcrossGenerationChange(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("generation-race", func(op *opsv1alpha1.DeviceOperation) {
		op.Generation = 1
		op.Spec.Operation.Commands = []string{"show version"}
	})
	apiClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()

	var hookErr error
	changed := false
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
	}
	tr.onExec = func() {
		if changed {
			return
		}
		changed = true
		var current opsv1alpha1.DeviceOperation
		if err := apiClient.Get(ctx, client.ObjectKeyFromObject(op), &current); err != nil {
			hookErr = err
			return
		}
		current.Generation = 2
		current.Spec.Operation.Commands = []string{"show clock"}
		hookErr = apiClient.Update(ctx, &current)
	}
	r := &Reconciler{
		Client:     apiClient,
		Reader:     apiClient,
		DeviceName: "dev1",
		TP:         &staticTP{tr: tr},
		Now:        func() time.Time { return time.Unix(200, 0).UTC() },
	}
	req := reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}}

	if _, err := r.Reconcile(ctx, req); err == nil || !strings.Contains(err.Error(), "generation changed from 1 to 2") {
		t.Fatalf("first Reconcile error=%v, want generation-change retry", err)
	}
	if hookErr != nil {
		t.Fatalf("update operation during execution: %v", hookErr)
	}
	var afterFirst opsv1alpha1.DeviceOperation
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(op), &afterFirst); err != nil {
		t.Fatalf("get operation after first Reconcile: %v", err)
	}
	if afterFirst.Generation != 2 || afterFirst.Status.ObservedGeneration != 1 {
		t.Fatalf("generation=%d observedGeneration=%d, want 2/1",
			afterFirst.Generation, afterFirst.Status.ObservedGeneration)
	}
	if afterFirst.Status.Phase != opsv1alpha1.OperationPhaseRunning || len(afterFirst.Status.Outputs) != 0 {
		t.Fatalf("generation 1 result leaked into generation 2 status: %#v", afterFirst.Status)
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(op), &got); err != nil {
		t.Fatalf("get operation after retry: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded || got.Status.ObservedGeneration != 2 {
		t.Fatalf("retry status phase=%q observedGeneration=%d, want Succeeded/2",
			got.Status.Phase, got.Status.ObservedGeneration)
	}
	if len(got.Status.Outputs) != 1 || got.Status.Outputs[0].Command != "show clock" {
		t.Fatalf("retry did not execute generation 2 command: %#v", got.Status.Outputs)
	}
	if tr.calls != 2 {
		t.Fatalf("DiagnosticExec calls=%d want 2", tr.calls)
	}
}

func TestReconcileDoesNotWriteResultAcrossObjectRecreation(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("identity-race", func(op *opsv1alpha1.DeviceOperation) {
		op.UID = types.UID("original-uid")
		op.Generation = 1
		op.Spec.Operation.Commands = []string{"show version"}
	})
	apiClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()

	var hookErr error
	recreated := false
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
	}
	tr.onExec = func() {
		if recreated {
			return
		}
		recreated = true
		var current opsv1alpha1.DeviceOperation
		if err := apiClient.Get(ctx, client.ObjectKeyFromObject(op), &current); err != nil {
			hookErr = err
			return
		}
		if err := apiClient.Delete(ctx, &current); err != nil {
			hookErr = err
			return
		}
		replacement := newOperation(op.Name, func(replacement *opsv1alpha1.DeviceOperation) {
			replacement.UID = types.UID("replacement-uid")
			replacement.Generation = 1
			replacement.Spec.Operation.Commands = []string{"show clock"}
		})
		hookErr = apiClient.Create(ctx, replacement)
	}
	r := &Reconciler{
		Client:     apiClient,
		Reader:     apiClient,
		DeviceName: "dev1",
		TP:         &staticTP{tr: tr},
		Now:        func() time.Time { return time.Unix(200, 0).UTC() },
	}
	req := reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}}

	if _, err := r.Reconcile(ctx, req); err == nil ||
		!strings.Contains(err.Error(), `identity changed from UID "original-uid" to "replacement-uid"`) {
		t.Fatalf("first Reconcile error=%v, want identity-change retry", err)
	}
	if hookErr != nil {
		t.Fatalf("replace operation during execution: %v", hookErr)
	}
	var afterFirst opsv1alpha1.DeviceOperation
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(op), &afterFirst); err != nil {
		t.Fatalf("get replacement after first Reconcile: %v", err)
	}
	if afterFirst.UID != types.UID("replacement-uid") || afterFirst.Generation != 1 {
		t.Fatalf("replacement identity=%q generation=%d, want replacement-uid/1",
			afterFirst.UID, afterFirst.Generation)
	}
	if afterFirst.Status.Phase != "" || afterFirst.Status.ObservedGeneration != 0 || len(afterFirst.Status.Outputs) != 0 {
		t.Fatalf("original result leaked into replacement status: %#v", afterFirst.Status)
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("replacement Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(op), &got); err != nil {
		t.Fatalf("get replacement after retry: %v", err)
	}
	if got.UID != types.UID("replacement-uid") ||
		got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded ||
		got.Status.ObservedGeneration != 1 {
		t.Fatalf("replacement identity=%q phase=%q observedGeneration=%d, want replacement-uid/Succeeded/1",
			got.UID, got.Status.Phase, got.Status.ObservedGeneration)
	}
	if len(got.Status.Outputs) != 1 || got.Status.Outputs[0].Command != "show clock" {
		t.Fatalf("replacement did not execute its command: %#v", got.Status.Outputs)
	}
	if tr.calls != 2 {
		t.Fatalf("DiagnosticExec calls=%d want 2", tr.calls)
	}
}

func TestReconcileShowVersionFailsOnEmptyOutput(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("empty-show-version", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Commands = []string{"show version"}
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: []transport.CommandResult{{
			Command: "show version",
			Output:  "",
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
	if got.Status.Phase != opsv1alpha1.OperationPhaseFailed {
		t.Fatalf("phase=%q want %q", got.Status.Phase, opsv1alpha1.OperationPhaseFailed)
	}
	if !strings.Contains(got.Status.Message, "empty output") {
		t.Fatalf("message=%q want empty output reason", got.Status.Message)
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

// Reject DeviceOperation CRs whose namespace differs from the owning
// CiscoDevice's namespace. Without this guard a tenant in any namespace can
// create a DeviceOperation that names a device whose name they happen to know
// and have it executed with that device's credentials.
func TestReconcileRejectsCrossNamespaceDeviceOperation(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("show", func(op *opsv1alpha1.DeviceOperation) {
		op.Namespace = "tenant-a"
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
		Client:          c,
		DeviceName:      "dev1",
		DeviceNamespace: "prod",
		TP:              &staticTP{tr: tr},
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
		t.Fatalf("phase=%q want failed", got.Status.Phase)
	}
	if !operationConditionIs(got.Status.Conditions, "Ready", metav1.ConditionFalse, "NamespaceMismatch") {
		t.Fatalf("Ready condition missing NamespaceMismatch: %#v", got.Status.Conditions)
	}
	if tr.calls != 0 {
		t.Fatalf("DiagnosticExec calls=%d want 0 (transport must not be touched)", tr.calls)
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

// TestReconcilePacketCaptureRefusesUnownedConfigMap is the
// adversarial-review regression for Finding #7. When a ConfigMap
// matching the deterministic artifact name already exists with no
// controller reference, the reconciler must refuse to overwrite it
// rather than silently clobber.
func TestReconcilePacketCaptureRefusesUnownedConfigMap(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	op := newOperation("capture", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindPacketCapture
		op.Spec.Operation.Args = map[string]string{"name": "cvkcap"}
	})
	// Pre-existing CM owned by nothing (or by a foreign object).
	preexisting := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: op.Namespace,
			Name:      "capture-output",
		},
		Data: map[string]string{
			"important-tenant-data": "must-not-be-clobbered",
		},
	}
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: []transport.CommandResult{{
			Command: "show monitor capture cvkcap buffer dump",
			Output:  strings.Repeat("packet line\n", 30*1024),
		}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op, preexisting).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{Client: c, Scheme: scheme, DeviceName: "dev1", TP: &staticTP{tr: tr}}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseFailed {
		t.Fatalf("phase=%q want failed", got.Status.Phase)
	}
	if !operationConditionIs(got.Status.Conditions, "Ready", metav1.ConditionFalse, "ArtifactExistsUnowned") {
		t.Fatalf("Ready condition missing ArtifactExistsUnowned: %#v", got.Status.Conditions)
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: "capture-output"}, &cm); err != nil {
		t.Fatalf("get artifact ConfigMap: %v", err)
	}
	if cm.Data["important-tenant-data"] != "must-not-be-clobbered" {
		t.Fatalf("existing ConfigMap was overwritten: data=%#v", cm.Data)
	}
}

// TestReconcileShowCommandTotalInlineBudgetSpills is the regression
// test for adversarial-review Finding #4. A ShowCommand operation
// with many outputs whose cumulative size exceeds totalInlineMaxBytes
// must spill the overflow to the per-operation ConfigMap artifact
// instead of writing a 4 MiB status object that would fail
// Status.Update.
func TestReconcileShowCommandTotalInlineBudgetSpills(t *testing.T) {
	ctx := context.Background()
	scheme := newScheme(t)
	// 8 commands × 50 KiB each = 400 KiB total; well above the
	// 256 KiB total-inline budget but each output is under the
	// 256 KiB per-output spill threshold so the PacketCapture path
	// would never have fired.
	const perOutputBytes = 50 * 1024
	const commandCount = 8
	cmds := make([]string, commandCount)
	results := make([]transport.CommandResult, commandCount)
	for i := 0; i < commandCount; i++ {
		cmds[i] = "show running-config"
		results[i] = transport.CommandResult{
			Command: "show running-config",
			Output:  strings.Repeat("x", perOutputBytes),
		}
	}
	op := newOperation("bigshow", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Commands = cmds
	})
	tr := &fakeTransport{
		caps: transport.Capabilities{
			Kind:                   transport.KindNETCONF,
			SupportsDiagnosticExec: true,
		},
		results: results,
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{
		Client:     c,
		Scheme:     scheme,
		DeviceName: "dev1",
		TP:         &staticTP{tr: tr},
		Now:        func() time.Time { return time.Unix(100, 0).UTC() },
	}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q want succeeded; msg=%s", got.Status.Phase, got.Status.Message)
	}
	if total := totalInline(got.Status.Outputs); total > totalInlineMaxBytes {
		t.Fatalf("inline total=%d bytes still over budget %d", total, totalInlineMaxBytes)
	}
	if len(got.Status.ArtifactURIs) == 0 {
		t.Fatalf("expected artifactURIs for spilled outputs; got none. outputs=%d",
			len(got.Status.Outputs))
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: "bigshow-output"}, &cm); err != nil {
		t.Fatalf("get artifact ConfigMap: %v", err)
	}
	if len(cm.Data) == 0 {
		t.Fatalf("artifact ConfigMap has no data")
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
