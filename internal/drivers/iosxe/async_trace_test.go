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

package iosxe

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
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type recoveryTraceCaptureClient struct {
	*fakeNetworkClient
	contexts chan context.Context
	err      error
}

func (c *recoveryTraceCaptureClient) Post(ctx context.Context, _ string, _ any, _ func(any) ([]byte, error)) error {
	c.contexts <- ctx
	return c.err
}

func TestRecoverMissingContainersUsesDetachedLinkedSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	forced := errors.New("forced recovery post failure")
	capture := &recoveryTraceCaptureClient{
		fakeNetworkClient: &fakeNetworkClient{},
		contexts:          make(chan context.Context, 1),
		err:               forced,
	}
	pod := lifecycleTestPod()
	pod.Spec.Containers = pod.Spec.Containers[:1]
	secretLister := corev1listers.NewSecretLister(cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}))
	configMapLister := corev1listers.NewConfigMapLister(cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}))
	driver := &XEDriver{
		config:          &v1alpha1.DeviceSpec{XE: &v1alpha1.XEConfig{}},
		client:          capture,
		secretLister:    secretLister.Secrets(pod.Namespace),
		configMapLister: configMapLister.ConfigMaps(pod.Namespace),
		installInFlight: make(map[string]bool),
		recoveringPods:  make(map[string]bool),
	}
	parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "status-reconcile")
	parentCtx = correlation.WithLifecycleID(parentCtx, "release-181")
	parentSC := parentSpan.SpanContext()
	driver.recoverMissingContainers(parentCtx, pod, map[string]string{})

	select {
	case callCtx := <-capture.contexts:
		if sc := oteltrace.SpanContextFromContext(callCtx); !sc.IsValid() || sc.SpanID() == parentSC.SpanID() {
			t.Fatalf("device call span context=%v, scheduling span=%v", sc, parentSC)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for IOS-XE recovery device call")
	}
	parentSpan.End()

	var recovery sdktrace.ReadOnlySpan
	deadline := time.Now().Add(2 * time.Second)
	for recovery == nil && time.Now().Before(deadline) {
		for _, span := range recorder.Ended() {
			if span.Name() == "cvk.iosxe.pod.missing-container-recovery" {
				recovery = span
				break
			}
		}
		if recovery == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if recovery == nil {
		t.Fatal("IOS-XE recovery span did not end")
	}
	if recovery.Parent().IsValid() {
		t.Fatalf("recovery parent=%v, want root span with scheduling link", recovery.Parent())
	}
	if len(recovery.Links()) != 1 ||
		recovery.Links()[0].SpanContext.TraceID() != parentSC.TraceID() ||
		recovery.Links()[0].SpanContext.SpanID() != parentSC.SpanID() {
		t.Fatalf("recovery links=%#v, want scheduling span %v", recovery.Links(), parentSC)
	}
	if recovery.Status().Code != codes.Error {
		t.Fatalf("recovery status=%#v, want Error", recovery.Status())
	}
	wantLifecycle := attribute.String(correlation.LifecycleIDAttribute, "release-181")
	found := false
	for _, attr := range recovery.Attributes() {
		if attr == wantLifecycle {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("recovery attributes=%#v, want lifecycle id", recovery.Attributes())
	}
}
