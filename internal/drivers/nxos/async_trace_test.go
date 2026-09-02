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

package nxos

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func waitForEndedSpan(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, span := range recorder.Ended() {
			if span.Name() == name {
				return span
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for span %q", name)
	return nil
}

func assertDetachedSpanLink(t *testing.T, span sdktrace.ReadOnlySpan, scheduling oteltrace.SpanContext) {
	t.Helper()
	if span.Parent().IsValid() {
		t.Fatalf("span %q parent=%v, want root with scheduling link", span.Name(), span.Parent())
	}
	if len(span.Links()) != 1 ||
		span.Links()[0].SpanContext.TraceID() != scheduling.TraceID() ||
		span.Links()[0].SpanContext.SpanID() != scheduling.SpanID() {
		t.Fatalf("span %q links=%#v, want scheduling span %v", span.Name(), span.Links(), scheduling)
	}
}

func TestRunAppActionUsesDetachedLinkedSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	driver := &NXOSDriver{asyncActions: true, appActions: make(map[string]nxosAppAction)}
	parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "convergence")
	parentCtx = correlation.WithLifecycleID(parentCtx, "release-181")
	parentSC := parentSpan.SpanContext()
	callContexts := make(chan context.Context, 1)
	forced := errors.New("forced action failure")
	const appID = "cvk0000_0123456789abcdef0123456789abcdef"
	if err := driver.runAppAction(parentCtx, appID, "start", func(ctx context.Context) error {
		callContexts <- ctx
		return forced
	}); err != nil {
		t.Fatalf("runAppAction scheduling error: %v", err)
	}
	select {
	case callCtx := <-callContexts:
		if sc := oteltrace.SpanContextFromContext(callCtx); !sc.IsValid() || sc.SpanID() == parentSC.SpanID() {
			t.Fatalf("action callback context=%v, scheduling span=%v", sc, parentSC)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async NX-OS action")
	}
	parentSpan.End()
	action := waitForEndedSpan(t, recorder, "cvk.nxos.app.action")
	assertDetachedSpanLink(t, action, parentSC)
	if action.Status().Code != codes.Error {
		t.Fatalf("action status=%#v, want Error", action.Status())
	}
	wantLifecycle := attribute.String(correlation.LifecycleIDAttribute, "release-181")
	found := false
	for _, attr := range action.Attributes() {
		if attr == wantLifecycle {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("action attributes=%#v, want lifecycle id", action.Attributes())
	}
}

func TestScheduleConvergenceUsesDetachedLinkedSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	driver := &NXOSDriver{
		config:         &v1alpha1.DeviceSpec{},
		appActions:     make(map[string]nxosAppAction),
		convergingPods: make(map[string]struct{}),
	}
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "validation",
		Name:      "empty",
		UID:       types.UID("11111111-2222-3333-4444-555555555555"),
	}}
	parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "status-reconcile")
	parentCtx = correlation.WithLifecycleID(parentCtx, "release-181")
	parentSC := parentSpan.SpanContext()
	driver.scheduleConvergence(parentCtx, pod, map[string]nxosApp{})
	convergence := waitForEndedSpan(t, recorder, "cvk.nxos.pod.convergence")
	parentSpan.End()
	assertDetachedSpanLink(t, convergence, parentSC)
	if convergence.Status().Code != codes.Ok {
		t.Fatalf("convergence status=%#v, want Ok", convergence.Status())
	}
}
