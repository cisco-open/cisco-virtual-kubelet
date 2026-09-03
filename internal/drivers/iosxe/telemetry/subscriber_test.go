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

package telemetry

import (
	"context"
	"net"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/emit"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type fakeSubscribeServer struct {
	gpb.UnimplementedGNMIServer
	requests chan *gpb.SubscribeRequest
	notify   chan *gpb.Notification
	closed   chan struct{}
}

func newFakeSubscribeServer() *fakeSubscribeServer {
	return &fakeSubscribeServer{
		requests: make(chan *gpb.SubscribeRequest, 8),
		notify:   make(chan *gpb.Notification, 8),
		closed:   make(chan struct{}, 8),
	}
}

func (s *fakeSubscribeServer) Subscribe(stream grpc.BidiStreamingServer[gpb.SubscribeRequest, gpb.SubscribeResponse]) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	s.requests <- req
	defer func() { s.closed <- struct{}{} }()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case n := <-s.notify:
			if err := stream.Send(&gpb.SubscribeResponse{
				Response: &gpb.SubscribeResponse_Update{Update: n},
			}); err != nil {
				return err
			}
		}
	}
}

type bufconnFactory struct {
	lis *bufconn.Listener
}

func (f *bufconnFactory) NewClient(context.Context) (*SubscribeClient, error) {
	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return f.lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}
	return &SubscribeClient{
		Conn:        conn,
		Client:      gpb.NewGNMIClient(conn),
		AuthContext: func(ctx context.Context) context.Context { return ctx },
	}, nil
}

func newSubscriberTestServer(t *testing.T) (*fakeSubscribeServer, SubscribeClientFactory) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fake := newFakeSubscribeServer()
	gpb.RegisterGNMIServer(srv, fake)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)
	return fake, &bufconnFactory{lis: lis}
}

func TestSubscriberLifecycle(t *testing.T) {
	fake, factory := newSubscriberTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := NewSubscriber("edge-01", factory, WithChannelCapacity(2), WithReconnectConfig(&configv1alpha1.ReconnectConfig{
		InitialBackoff: metav1.Duration{Duration: 10 * time.Millisecond},
		MaxBackoff:     metav1.Duration{Duration: 10 * time.Millisecond},
	}))
	if err := sub.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	spec := configv1alpha1.TelemetrySubscription{
		Name:           "environmental",
		Paths:          []string{"/Cisco-IOS-XE-environment-oper:environment-sensors/sensor[name=temperature]"},
		Mode:           configv1alpha1.TelemetryModeStream,
		StreamMode:     configv1alpha1.TelemetryStreamModeSample,
		SampleInterval: metav1.Duration{Duration: time.Second},
		Encoding:       configv1alpha1.TelemetryEncodingProto,
	}
	if err := sub.AddSubscription(spec); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}

	req := waitRequest(t, fake.requests)
	sl := req.GetSubscribe()
	if sl == nil || len(sl.GetSubscription()) != 1 {
		t.Fatalf("subscription request=%+v, want one subscription", req)
	}
	if sl.GetEncoding() != gpb.Encoding_PROTO {
		t.Fatalf("encoding=%s, want PROTO", sl.GetEncoding())
	}
	if got := sl.GetSubscription()[0].GetPath().GetElem()[0].GetName(); got != "environment-sensors" {
		t.Fatalf("first elem=%q, want environment-sensors", got)
	}

	fake.notify <- &gpb.Notification{
		Prefix: &gpb.Path{Elem: []*gpb.PathElem{{Name: "environment-sensors"}}},
		Update: []*gpb.Update{{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "sensor", Key: map[string]string{"name": "temperature"}}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: "42"}},
		}},
	}
	eventually(t, time.Second, func() bool {
		phase, states := sub.StatusFor([]string{"environmental"})
		return phase == configv1alpha1.IOSXETelemetryPhaseStreaming &&
			len(states) == 1 &&
			states[0].MessagesReceived == 1 &&
			states[0].LastUpdate != nil
	})

	sub.RemoveSubscription("environmental")
	phase, states := sub.StatusFor([]string{"environmental"})
	if phase != configv1alpha1.IOSXETelemetryPhasePending {
		t.Fatalf("phase after remove=%s, want Pending", phase)
	}
	if len(states) != 1 || states[0].MessagesReceived != 0 {
		t.Fatalf("state after remove=%+v, want blank pending state", states)
	}

	sub.Stop()
	if sub.Conn() != nil {
		t.Fatal("Conn after Stop is non-nil")
	}
}

func TestCachedEventCorrelationRestoresLifecycleForParentAndLink(t *testing.T) {
	sc, err := correlation.ParseTraceparent("00-11111111111111111111111111111111-2222222222222222-01")
	if err != nil {
		t.Fatal(err)
	}
	events := []state.AppEvent{{Device: "edge-01", AppID: "app-a"}}

	parentCache := correlation.NewCache(time.Minute, 2, time.Hour)
	parentCache.Upsert("edge-01", "app-a", sc, "release-181")
	parentCtx := cachedEventCorrelationContext(context.Background(), parentCache, events)
	if got := trace.SpanContextFromContext(parentCtx); got.TraceID() != sc.TraceID() || got.SpanID() != sc.SpanID() {
		t.Fatalf("parent context=%v, want cached span=%v", got, sc)
	}
	if got := correlation.LifecycleIDFromContext(parentCtx); got != "release-181" {
		t.Fatalf("parent lifecycle=%q, want release-181", got)
	}

	linkCache := correlation.NewCache(time.Minute, 2, time.Nanosecond)
	linkCache.Upsert("edge-01", "app-a", sc, "release-181")
	time.Sleep(time.Millisecond)
	linkCtx := cachedEventCorrelationContext(context.Background(), linkCache, events)
	if got := trace.SpanContextFromContext(linkCtx); got.IsValid() {
		t.Fatalf("link context installed direct parent=%v", got)
	}
	links := correlation.SpanLinksFromContext(linkCtx)
	if len(links) != 1 || links[0].SpanContext.TraceID() != sc.TraceID() || links[0].SpanContext.SpanID() != sc.SpanID() {
		t.Fatalf("link context links=%#v, want cached span=%v", links, sc)
	}
	if got := correlation.LifecycleIDFromContext(linkCtx); got != "release-181" {
		t.Fatalf("link lifecycle=%q, want release-181", got)
	}
}

func TestEmitMappedEventsCorrelatesEachAppIndependently(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	emitter := emit.NewTracesEmitter(tp, nil, []configv1alpha1.Transition{{
		Path:            "/app-hosting-oper-data/app[name=*]/details/state",
		HealthyValues:   []string{"RUNNING"},
		UnhealthyValues: []string{"STOPPED"},
	}})
	sub := &Subscriber{deviceRef: "edge-01", tracesEmitter: emitter}

	sourceA, err := correlation.ParseTraceparent("00-11111111111111111111111111111111-aaaaaaaaaaaaaaaa-01")
	if err != nil {
		t.Fatal(err)
	}
	sourceB, err := correlation.ParseTraceparent("00-22222222222222222222222222222222-bbbbbbbbbbbbbbbb-01")
	if err != nil {
		t.Fatal(err)
	}
	cache := correlation.NewCache(time.Minute, 4, time.Nanosecond)
	cache.Upsert("edge-01", "app-a", sourceA, "release-app-a")
	cache.Upsert("edge-01", "app-b", sourceB, "release-app-b")
	// Force both cached contexts through the async-link path. The companion
	// test above covers direct-parent restoration.
	time.Sleep(time.Millisecond)

	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	mapped := []mapper.MappedEvent{
		appStateTraceEvent("app-a", "STOPPED", t0),
		appStateTraceEvent("app-b", "STOPPED", t0.Add(time.Second)),
		appStateTraceEvent("app-a", "RUNNING", t0.Add(2*time.Second)),
		appStateTraceEvent("app-b", "RUNNING", t0.Add(3*time.Second)),
	}
	logs, metrics := sub.emitMappedEventsWithCorrelation(
		context.Background(), cache, nil, "apps", mapped, MappingProfile{},
	)
	if logs != 0 || metrics != 0 {
		t.Fatalf("emitted logs=%d metrics=%d, want 0/0", logs, metrics)
	}

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("ended spans=%d, want one recovery transition per app", len(spans))
	}
	want := map[string]struct {
		source      trace.SpanContext
		lifecycleID string
	}{
		"app-a": {source: sourceA, lifecycleID: "release-app-a"},
		"app-b": {source: sourceB, lifecycleID: "release-app-b"},
	}
	for _, span := range spans {
		attrs := subscriberSpanAttrs(span.Attributes())
		appID := attrs["name"]
		expected, ok := want[appID]
		if !ok {
			t.Fatalf("span attributes=%v contain unexpected app", attrs)
		}
		if parent := span.Parent(); parent.IsValid() {
			t.Fatalf("app %q parent=%v, want linked root", appID, parent)
		}
		links := span.Links()
		if len(links) != 1 ||
			links[0].SpanContext.TraceID() != expected.source.TraceID() ||
			links[0].SpanContext.SpanID() != expected.source.SpanID() {
			t.Fatalf("app %q links=%+v, want source=%v", appID, links, expected.source)
		}
		if got := attrs[correlation.LifecycleIDAttribute]; got != expected.lifecycleID {
			t.Fatalf("app %q lifecycle=%q, want %q", appID, got, expected.lifecycleID)
		}
		delete(want, appID)
	}
	if len(want) != 0 {
		t.Fatalf("missing spans for apps: %v", want)
	}
}

func appStateTraceEvent(appID, value string, ts time.Time) mapper.MappedEvent {
	path := "/app-hosting-oper-data/app[name=" + appID + "]/details/state"
	return mapper.MappedEvent{
		Signal:        mapper.SignalKindTrace,
		Name:          "/app-hosting-oper-data/app/details/state",
		CanonicalPath: path,
		Body:          value,
		Timestamp:     ts,
		SeriesKey:     "apps\x00" + path + "\x00name=" + appID,
		Resource: []mapper.KeyValue{
			{Key: "device", Value: "edge-01"},
			{Key: "subscription", Value: "apps"},
		},
		Attributes: []mapper.KeyValue{
			{Key: "name", Value: appID},
			{Key: "cisco.gnmi.path", Value: path},
		},
	}
}

func subscriberSpanAttrs(attrs []attribute.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value.AsString()
	}
	return out
}

func waitRequest(t *testing.T, ch <-chan *gpb.SubscribeRequest) *gpb.SubscribeRequest {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SubscribeRequest")
		return nil
	}
}

func eventually(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
