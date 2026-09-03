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

package provider

import (
	"context"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type correlationContextDriver struct {
	notifierDriver
	deployContext context.Context
	updateContext context.Context
	statusContext chan context.Context
}

func waitForProviderSpanCount(t *testing.T, recorder *tracetest.SpanRecorder, name string, want int) []sdktrace.ReadOnlySpan {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var matches []sdktrace.ReadOnlySpan
		for _, span := range recorder.Ended() {
			if span.Name() == name {
				matches = append(matches, span)
			}
		}
		if len(matches) >= want {
			return matches
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d ended %q spans", want, name)
	return nil
}

func (d *correlationContextDriver) DeployPod(
	ctx context.Context,
	_ *corev1.Pod,
	_ corev1listers.SecretNamespaceLister,
	_ corev1listers.ConfigMapNamespaceLister,
) error {
	d.deployContext = ctx
	return nil
}

func (d *correlationContextDriver) UpdatePod(ctx context.Context, _ *corev1.Pod) error {
	d.updateContext = ctx
	return nil
}

func (d *correlationContextDriver) GetPodStatus(ctx context.Context, pod *corev1.Pod) (*corev1.Pod, error) {
	if d.statusContext != nil {
		d.statusContext <- ctx
	}
	out := pod.DeepCopy()
	out.Status = d.status
	return out, nil
}

func TestCreatePodUsesAnnotatedParentAndPassesContextToDriver(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	traceID, err := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	remote := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: oteltrace.FlagsSampled,
	})
	driver := &correlationContextDriver{statusContext: make(chan context.Context, 2)}
	p, err := NewAppHostingProvider(context.Background(), &ciskov1.DeviceSpec{}, nodeutil.ProviderConfig{}, driver, nil, nil)
	if err != nil {
		t.Fatalf("NewAppHostingProvider: %v", err)
	}
	cache := correlation.NewCache(0, 0, 0)
	p.SetTraceCorrelation("edge-01", cache)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "validation",
			Name:      "lifecycle-test",
			UID:       types.UID("11111111-2222-3333-4444-555555555555"),
			Annotations: map[string]string{
				correlation.TraceparentAnnotation:    correlation.FormatTraceparent(remote),
				correlation.TraceWindowEndAnnotation: time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
				correlation.LifecycleIDAnnotation:    "release-181-validation",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	}
	if err := p.CreatePod(context.Background(), pod); err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	select {
	case <-driver.statusContext:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for create-triggered status refresh")
	}
	waitForProviderSpanCount(t, recorder, "cvk.pod.status-reconcile", 1)

	driverSC := oteltrace.SpanContextFromContext(driver.deployContext)
	if driverSC.TraceID() != remote.TraceID() || driverSC.SpanID() == remote.SpanID() {
		t.Fatalf("driver context=%s/%s, remote=%s/%s", driverSC.TraceID(), driverSC.SpanID(), remote.TraceID(), remote.SpanID())
	}
	createSpans := waitForProviderSpanCount(t, recorder, "cvk.pod.create", 1)
	createSpan := createSpans[0]
	if createSpan.Parent().SpanID() != remote.SpanID() {
		t.Fatalf("span=%q parent=%s", createSpan.Name(), createSpan.Parent().SpanID())
	}
	wantLifecycle := attribute.String(correlation.LifecycleIDAttribute, "release-181-validation")
	found := false
	for _, kv := range createSpan.Attributes() {
		if kv == wantLifecycle {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("lifecycle attribute missing: %#v", createSpan.Attributes())
	}

	appIDs := common.GenerateContainerAppIDs(pod)
	if len(appIDs) != 1 {
		t.Fatalf("generated app IDs=%v, want one", appIDs)
	}
	appID := appIDs["app"]
	createdCacheSC, createdLifecycle, _, ok := cache.GetWithLifecycle("edge-01", appID)
	if !ok || createdCacheSC.TraceID() != driverSC.TraceID() || createdCacheSC.SpanID() != driverSC.SpanID() ||
		createdLifecycle != "release-181-validation" {
		t.Fatalf("create cache context=%v lifecycle=%q ok=%t, want driver context=%v", createdCacheSC, createdLifecycle, ok, driverSC)
	}
	if err := p.UpdatePod(context.Background(), pod); err != nil {
		t.Fatalf("UpdatePod: %v", err)
	}
	select {
	case <-driver.statusContext:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for update-triggered status refresh")
	}
	waitForProviderSpanCount(t, recorder, "cvk.pod.status-reconcile", 2)
	updateSC := oteltrace.SpanContextFromContext(driver.updateContext)
	updatedCacheSC, updatedLifecycle, _, ok := cache.GetWithLifecycle("edge-01", appID)
	if !ok || updatedCacheSC.TraceID() != updateSC.TraceID() || updatedCacheSC.SpanID() != updateSC.SpanID() ||
		updatedCacheSC.SpanID() == createdCacheSC.SpanID() || updatedLifecycle != "release-181-validation" {
		t.Fatalf("update cache context=%v lifecycle=%q ok=%t, want refreshed context=%v (old=%v)", updatedCacheSC, updatedLifecycle, ok, updateSC, createdCacheSC)
	}
	if err := p.DeletePod(context.Background(), pod); err != nil {
		t.Fatalf("DeletePod: %v", err)
	}
	deletedCacheSC, deletedLifecycle, _, ok := cache.GetWithLifecycle("edge-01", appID)
	if !ok || deletedCacheSC.SpanID() == updatedCacheSC.SpanID() || deletedLifecycle != "release-181-validation" {
		t.Fatalf("delete cache context=%v lifecycle=%q ok=%t, want refresh from update=%v", deletedCacheSC, deletedLifecycle, ok, updatedCacheSC)
	}
}

func TestNotifyPodStatusSoonUsesDetachedSchedulingLink(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	driver := &correlationContextDriver{statusContext: make(chan context.Context, 1)}
	p, err := NewAppHostingProvider(context.Background(), &ciskov1.DeviceSpec{}, nodeutil.ProviderConfig{}, driver, nil, nil)
	if err != nil {
		t.Fatalf("NewAppHostingProvider: %v", err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "validation",
		Name:      "async-status",
		UID:       types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
	}}
	parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "create")
	parentCtx = correlation.WithLifecycleID(parentCtx, "release-181")
	parentSC := parentSpan.SpanContext()
	p.notifyPodStatusSoon(parentCtx, pod)
	select {
	case callCtx := <-driver.statusContext:
		if sc := oteltrace.SpanContextFromContext(callCtx); !sc.IsValid() || sc.SpanID() == parentSC.SpanID() {
			t.Fatalf("status context=%v, scheduling span=%v", sc, parentSC)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for immediate status refresh")
	}
	parentSpan.End()

	var statusSpan sdktrace.ReadOnlySpan
	deadline := time.Now().Add(2 * time.Second)
	for statusSpan == nil && time.Now().Before(deadline) {
		for _, span := range recorder.Ended() {
			if span.Name() == "cvk.pod.status-reconcile" {
				statusSpan = span
				break
			}
		}
		if statusSpan == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if statusSpan == nil {
		t.Fatal("status-reconcile span did not end")
	}
	if statusSpan.Parent().IsValid() {
		t.Fatalf("status span parent=%v, want root with scheduling link", statusSpan.Parent())
	}
	if len(statusSpan.Links()) != 1 ||
		statusSpan.Links()[0].SpanContext.TraceID() != parentSC.TraceID() ||
		statusSpan.Links()[0].SpanContext.SpanID() != parentSC.SpanID() {
		t.Fatalf("status links=%#v, want scheduling span %v", statusSpan.Links(), parentSC)
	}
	wantLifecycle := attribute.String(correlation.LifecycleIDAttribute, "release-181")
	found := false
	for _, attr := range statusSpan.Attributes() {
		if attr == wantLifecycle {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("status attributes=%#v, want lifecycle id", statusSpan.Attributes())
	}
}

func TestTelemetryStatusUsesQueuedCorrelationWithoutPodCarrier(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "validation",
			Name:      "mdt-status",
			UID:       types.UID("cccccccc-bbbb-cccc-dddd-eeeeeeeeeeee"),
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	}
	if len(pod.Annotations) != 0 {
		t.Fatalf("test pod unexpectedly has persisted trace annotations: %v", pod.Annotations)
	}
	driver := &correlationContextDriver{
		notifierDriver: notifierDriver{status: notifierPodStatus(corev1.PodRunning, true)},
		statusContext:  make(chan context.Context, 1),
	}
	p, err := NewAppHostingProvider(
		workerCtx,
		&ciskov1.DeviceSpec{},
		nodeutil.ProviderConfig{Pods: podLister(t, pod)},
		driver,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewAppHostingProvider: %v", err)
	}

	sourceCtx, sourceSpan := tp.Tracer("test").Start(context.Background(), "mdt.transition")
	sourceSC := sourceSpan.SpanContext()
	sourceCtx = correlation.WithLifecycleID(sourceCtx, "release-181")
	sourceCtx, cancelSource := context.WithCancel(sourceCtx)
	appID := common.GenerateContainerAppIDs(pod)["app"]
	if ok := p.ObserveAppEvent(sourceCtx, state.AppEvent{Device: "edge-01", AppID: appID, State: "RUNNING"}); !ok {
		t.Fatal("ObserveAppEvent returned false")
	}
	// The producer may finish before the bounded queue is consumed. The
	// notification must retain only its correlation snapshot, not cancellation.
	cancelSource()

	seen := make(chan *corev1.Pod, 1)
	p.NotifyPods(workerCtx, func(statusPod *corev1.Pod) { seen <- statusPod })
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for correlated telemetry status notification")
	}
	sourceSpan.End()

	statusSpans := waitForProviderSpanCount(t, recorder, "cvk.pod.telemetry-status", 1)
	statusSpan := statusSpans[0]
	if statusSpan.Parent().IsValid() {
		t.Fatalf("telemetry status parent=%v, want root with async link", statusSpan.Parent())
	}
	if len(statusSpan.Links()) != 1 ||
		statusSpan.Links()[0].SpanContext.TraceID() != sourceSC.TraceID() ||
		statusSpan.Links()[0].SpanContext.SpanID() != sourceSC.SpanID() {
		t.Fatalf("telemetry status links=%#v, want source span %v", statusSpan.Links(), sourceSC)
	}
	wantLifecycle := attribute.String(correlation.LifecycleIDAttribute, "release-181")
	foundLifecycle := false
	for _, attr := range statusSpan.Attributes() {
		if attr == wantLifecycle {
			foundLifecycle = true
			break
		}
	}
	if !foundLifecycle {
		t.Fatalf("telemetry status attributes=%#v, want lifecycle id", statusSpan.Attributes())
	}
}

func TestRecoverDeletingPodUsesDetachedSpanAndRefreshesAppCache(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	deleteDone := make(chan struct{}, 1)
	driver := &correlationContextDriver{}
	driver.deleteDone = deleteDone
	p, err := NewAppHostingProvider(context.Background(), &ciskov1.DeviceSpec{}, nodeutil.ProviderConfig{}, driver, nil, nil)
	if err != nil {
		t.Fatalf("NewAppHostingProvider: %v", err)
	}
	cache := correlation.NewCache(0, 0, 0)
	p.SetTraceCorrelation("edge-01", cache)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "validation",
			Name:      "deleting",
			UID:       types.UID("11111111-2222-3333-4444-555555555555"),
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	}
	parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "delete-observed")
	parentCtx = correlation.WithLifecycleID(parentCtx, "release-181")
	parentSC := parentSpan.SpanContext()
	p.recoverDeletingPod(parentCtx, pod)
	select {
	case <-deleteDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delete recovery")
	}
	parentSpan.End()

	var recovery sdktrace.ReadOnlySpan
	deadline := time.Now().Add(2 * time.Second)
	for recovery == nil && time.Now().Before(deadline) {
		for _, span := range recorder.Ended() {
			if span.Name() == "cvk.pod.delete-recovery" {
				recovery = span
				break
			}
		}
		if recovery == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if recovery == nil {
		t.Fatal("delete-recovery span did not end")
	}
	if recovery.Parent().IsValid() || len(recovery.Links()) != 1 ||
		recovery.Links()[0].SpanContext.TraceID() != parentSC.TraceID() ||
		recovery.Links()[0].SpanContext.SpanID() != parentSC.SpanID() {
		t.Fatalf("delete recovery parent=%v links=%#v, want scheduling link %v", recovery.Parent(), recovery.Links(), parentSC)
	}
	appID := common.GenerateContainerAppIDs(pod)["app"]
	cached, lifecycleID, _, ok := cache.GetWithLifecycle("edge-01", appID)
	if !ok || cached.TraceID() != recovery.SpanContext().TraceID() || cached.SpanID() != recovery.SpanContext().SpanID() ||
		lifecycleID != "release-181" {
		t.Fatalf("delete recovery cache=%v lifecycle=%q ok=%t, want span=%v", cached, lifecycleID, ok, recovery.SpanContext())
	}
}
