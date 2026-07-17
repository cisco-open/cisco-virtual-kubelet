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
	"errors"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/telemetry"
)

type providerFakeGNMIServer struct {
	gpb.UnimplementedGNMIServer
	requests chan *gpb.SubscribeRequest
	notify   chan *gpb.Notification
}

func newProviderFakeGNMIServer() *providerFakeGNMIServer {
	return &providerFakeGNMIServer{
		requests: make(chan *gpb.SubscribeRequest, 8),
		notify:   make(chan *gpb.Notification, 8),
	}
}

func (s *providerFakeGNMIServer) Subscribe(stream grpc.BidiStreamingServer[gpb.SubscribeRequest, gpb.SubscribeResponse]) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	s.requests <- req
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

type providerBufconnFactory struct {
	lis *bufconn.Listener
}

func (f *providerBufconnFactory) NewClient(context.Context) (*telemetry.SubscribeClient, error) {
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
	return &telemetry.SubscribeClient{
		Conn:        conn,
		Client:      gpb.NewGNMIClient(conn),
		AuthContext: func(ctx context.Context) context.Context { return ctx },
	}, nil
}

func newProviderTelemetryServer(t *testing.T) (*providerFakeGNMIServer, telemetry.SubscribeClientFactory) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fakeServer := newProviderFakeGNMIServer()
	gpb.RegisterGNMIServer(srv, fakeServer)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)
	return fakeServer, &providerBufconnFactory{lis: lis}
}

type conflictStatusClient struct {
	client.Client
}

func (c *conflictStatusClient) Status() client.StatusWriter {
	return conflictStatusWriter{}
}

type conflictStatusWriter struct{}

func (conflictStatusWriter) Create(context.Context, client.Object, client.Object, ...client.SubResourceCreateOption) error {
	return telemetryStatusConflict()
}

func (conflictStatusWriter) Update(context.Context, client.Object, ...client.SubResourceUpdateOption) error {
	return telemetryStatusConflict()
}

func (conflictStatusWriter) Patch(context.Context, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
	return telemetryStatusConflict()
}

func (conflictStatusWriter) Apply(context.Context, runtime.ApplyConfiguration, ...client.SubResourceApplyOption) error {
	return telemetryStatusConflict()
}

func telemetryStatusConflict() error {
	return apierrors.NewConflict(
		schema.GroupResource{Group: "config.cisco.vk", Resource: "iosxetelemetries"},
		"telemetry",
		errors.New("status resource version changed"),
	)
}

func newTelemetryCR(name, device string) *configv1alpha1.IOSXETelemetry {
	return &configv1alpha1.IOSXETelemetry{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "network", Generation: 1},
		Spec: configv1alpha1.IOSXETelemetrySpec{
			DeviceRef: corev1.LocalObjectReference{Name: device},
			Subscriptions: []configv1alpha1.TelemetrySubscription{{
				Name:           "environmental",
				Paths:          []string{"/Cisco-IOS-XE-environment-oper:environment-sensors/sensor[name=temperature]"},
				Mode:           configv1alpha1.TelemetryModeStream,
				StreamMode:     configv1alpha1.TelemetryStreamModeSample,
				SampleInterval: metav1.Duration{Duration: time.Second},
				Encoding:       configv1alpha1.TelemetryEncodingProto,
			}},
			Output: configv1alpha1.OutputConfig{Signal: []string{configv1alpha1.TelemetrySignalMetrics}},
		},
	}
}

func TestIOSXETelemetryReconcilerIgnoresForeignDevice(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newTelemetryCR("telemetry", "other-device")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXETelemetry{}).
		Build()
	_, factory := newProviderTelemetryServer(t)
	r := &IOSXETelemetryReconciler{Client: c, DeviceName: "edge-01", Factory: factory}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "telemetry"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXETelemetry
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "telemetry"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != "" {
		t.Fatalf("foreign-device status touched: %+v", got.Status)
	}
}

// TestIOSXETelemetryReconcilerRejectsForeignNamespace verifies that the
// reconciler refuses to act on a CR that targets this device by name but
// lives in a namespace other than DeviceNamespace. Without this guard a
// tenant who can create IOSXETelemetry in any namespace could steer a
// device pod outside their own tenancy boundary as long as they knew the
// device name (the cluster-wide RBAC plus name-only filter previously
// made this possible — see the adversarial-review findings).
func TestIOSXETelemetryReconcilerRejectsForeignNamespace(t *testing.T) {
	scheme := newTestScheme(t)
	// CR is in namespace "attacker"; reconciler is bound to "network".
	cr := newTelemetryCR("telemetry", "edge-01")
	cr.Namespace = "attacker"
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXETelemetry{}).
		Build()
	_, factory := newProviderTelemetryServer(t)
	r := &IOSXETelemetryReconciler{
		Client:          c,
		DeviceName:      "edge-01",
		DeviceNamespace: "network",
		Factory:         factory,
	}

	// The predicate must reject this CR even though the device name
	// matches — defense-in-depth before Reconcile is reached.
	if r.telemetryTargetsThisDevice(cr) {
		t.Fatalf("predicate accepted cross-namespace CR: %+v", cr)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "attacker", Name: "telemetry"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXETelemetry
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "attacker", Name: "telemetry"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != "" {
		t.Fatalf("cross-namespace status touched: %+v", got.Status)
	}
	if len(got.Finalizers) != 0 {
		t.Fatalf("finalizer added to cross-namespace CR: %+v", got.Finalizers)
	}
}

func TestIOSXETelemetryRoundTripStatus(t *testing.T) {
	fakeServer, factory := newProviderTelemetryServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheme := newTestScheme(t)
	cr := newTelemetryCR("telemetry", "edge-01")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXETelemetry{}).
		Build()
	r := &IOSXETelemetryReconciler{
		Client:      c,
		DeviceName:  "edge-01",
		Factory:     factory,
		RootContext: ctx,
	}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "telemetry"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	waitProviderRequest(t, fakeServer.requests)
	ownerKey := telemetrySubscriptionOwnerKey(req.NamespacedName, "environmental")

	fakeServer.notify <- &gpb.Notification{
		Prefix: &gpb.Path{Elem: []*gpb.PathElem{{Name: "environment-sensors"}}},
		Update: []*gpb.Update{{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "sensor", Key: map[string]string{"name": "temperature"}}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: "42"}},
		}},
	}
	eventuallyProvider(t, time.Second, func() bool {
		phase, states := r.subscriber.StatusFor([]string{ownerKey})
		return phase == configv1alpha1.IOSXETelemetryPhaseStreaming &&
			len(states) == 1 &&
			states[0].MessagesReceived == 1
	})

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXETelemetry
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != configv1alpha1.IOSXETelemetryPhaseStreaming {
		t.Fatalf("phase=%s, want Streaming; full status=%+v", got.Status.Phase, got.Status)
	}
	if len(got.Status.ObservedSubscriptionState) != 1 {
		t.Fatalf("states=%+v, want one", got.Status.ObservedSubscriptionState)
	}
	if got.Status.ObservedSubscriptionState[0].MessagesReceived != 1 {
		t.Fatalf("messagesReceived=%d, want 1", got.Status.ObservedSubscriptionState[0].MessagesReceived)
	}
	if !conditionIs(got.Status.Conditions, "Ready", metav1.ConditionTrue, "Streaming") {
		t.Fatalf("Ready/Streaming condition missing: %+v", got.Status.Conditions)
	}
}

func TestIOSXETelemetryValidationFailureUpdatesStatus(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newTelemetryCR("telemetry", "edge-01")
	cr.Spec.Subscriptions[0].Mode = "POLL"
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXETelemetry{}).
		Build()
	_, factory := newProviderTelemetryServer(t)
	r := &IOSXETelemetryReconciler{Client: c, DeviceName: "edge-01", Factory: factory}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "telemetry"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXETelemetry
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "telemetry"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != configv1alpha1.IOSXETelemetryPhaseFailed {
		t.Fatalf("phase=%s, want Failed", got.Status.Phase)
	}
	if !conditionIs(got.Status.Conditions, "Ready", metav1.ConditionFalse, "InvalidSpec") {
		t.Fatalf("Ready/InvalidSpec condition missing: %+v", got.Status.Conditions)
	}
}

func TestIOSXETelemetryStatusConflictRequeues(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newTelemetryCR("telemetry", "edge-01")
	cr.Spec.Subscriptions[0].Mode = "POLL"
	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXETelemetry{}).
		Build()
	_, factory := newProviderTelemetryServer(t)
	r := &IOSXETelemetryReconciler{
		Client:     &conflictStatusClient{Client: baseClient},
		DeviceName: "edge-01",
		Factory:    factory,
	}

	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "telemetry"},
	})
	if err != nil {
		t.Fatalf("Reconcile error=%v, want nil conflict requeue", err)
	}
	if !res.Requeue {
		t.Fatalf("result=%+v, want Requeue=true", res)
	}
}

func TestIOSXETelemetrySameSubscriptionNameAcrossCRsDoNotStomp(t *testing.T) {
	_, factory := newProviderTelemetryServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheme := newTestScheme(t)
	crA := newTelemetryCR("telemetry-a", "edge-01")
	crB := newTelemetryCR("telemetry-b", "edge-01")
	crA.Spec.Subscriptions[0].Name = "interface-counters"
	crB.Spec.Subscriptions[0].Name = "interface-counters"
	crA.Spec.Subscriptions[0].Paths = []string{"/interfaces/interface[name=GigabitEthernet1]/state/counters/in-octets"}
	crB.Spec.Subscriptions[0].Paths = []string{"/interfaces/interface[name=GigabitEthernet1]/state/counters/out-octets"}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(crA, crB).
		WithStatusSubresource(&configv1alpha1.IOSXETelemetry{}).
		Build()
	r := &IOSXETelemetryReconciler{
		Client:      c,
		DeviceName:  "edge-01",
		Factory:     factory,
		RootContext: ctx,
	}
	reqA := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "telemetry-a"}}
	reqB := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "telemetry-b"}}

	if _, err := r.Reconcile(context.Background(), reqA); err != nil {
		t.Fatalf("reconcile A: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), reqB); err != nil {
		t.Fatalf("reconcile B: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), reqA); err != nil {
		t.Fatalf("reconcile A again: %v", err)
	}

	keyA := telemetrySubscriptionOwnerKey(reqA.NamespacedName, "interface-counters")
	keyB := telemetrySubscriptionOwnerKey(reqB.NamespacedName, "interface-counters")
	eventuallyProvider(t, 2*time.Second, func() bool {
		phase, states := r.subscriber.StatusFor([]string{keyA, keyB})
		return phase == configv1alpha1.IOSXETelemetryPhaseStreaming && len(states) == 2
	})

	if _, err := r.Reconcile(context.Background(), reqA); err != nil {
		t.Fatalf("reconcile A status: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), reqB); err != nil {
		t.Fatalf("reconcile B status: %v", err)
	}
	for _, req := range []reconcile.Request{reqA, reqB} {
		var got configv1alpha1.IOSXETelemetry
		if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
			t.Fatalf("get %s: %v", req.NamespacedName, err)
		}
		if len(got.Status.ObservedSubscriptionState) != 1 {
			t.Fatalf("%s states=%+v, want one", req.NamespacedName, got.Status.ObservedSubscriptionState)
		}
		if got.Status.ObservedSubscriptionState[0].Name != "interface-counters" {
			t.Fatalf("%s status name=%q, want interface-counters", req.NamespacedName, got.Status.ObservedSubscriptionState[0].Name)
		}
	}
}

// When a CR is retargeted away from this device (spec.deviceRef.name changes
// from "edge-01" to something else), the reconciler must drop the
// subscriptions it owned for that CR. Without that, the on-device
// subscriptions and stream bookkeeping leak until the per-device pod
// restarts.
func TestIOSXETelemetryReconcilerCleansUpOnRetarget(t *testing.T) {
	_, factory := newProviderTelemetryServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheme := newTestScheme(t)
	cr := newTelemetryCR("telemetry", "edge-01")
	cr.Spec.Subscriptions[0].Name = "interface-counters"
	cr.Spec.Subscriptions[0].Paths = []string{"/interfaces/interface[name=GigabitEthernet1]/state/counters/in-octets"}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXETelemetry{}).
		Build()
	r := &IOSXETelemetryReconciler{
		Client:      c,
		DeviceName:  "edge-01",
		Factory:     factory,
		RootContext: ctx,
	}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "telemetry"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	subKey := telemetrySubscriptionOwnerKey(req.NamespacedName, "interface-counters")
	eventuallyProvider(t, 2*time.Second, func() bool {
		if r.subscriber == nil {
			return false
		}
		phase, states := r.subscriber.StatusFor([]string{subKey})
		return phase == configv1alpha1.IOSXETelemetryPhaseStreaming && len(states) == 1
	})
	r.mu.Lock()
	if got := len(r.owned[req.NamespacedName]); got == 0 {
		r.mu.Unlock()
		t.Fatalf("expected reconciler to own at least one subscription before retarget; owned=%v", r.owned)
	}
	r.mu.Unlock()

	// Retarget the CR to a foreign device and reconcile again.
	var fresh configv1alpha1.IOSXETelemetry
	if err := c.Get(context.Background(), req.NamespacedName, &fresh); err != nil {
		t.Fatalf("get CR: %v", err)
	}
	fresh.Spec.DeviceRef.Name = "edge-99"
	if err := c.Update(context.Background(), &fresh); err != nil {
		t.Fatalf("update CR: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("retarget reconcile: %v", err)
	}

	r.mu.Lock()
	if owned, ok := r.owned[req.NamespacedName]; ok {
		r.mu.Unlock()
		t.Fatalf("expected reconciler to drop owned subscriptions after retarget; still owned=%v", owned)
	}
	r.mu.Unlock()
}

func TestIOSXETelemetryStateBridgeDebouncesBurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan event.GenericEvent, 4)
	r := &IOSXETelemetryReconciler{
		DeviceName:   "edge-01",
		StatusEvents: events,
	}
	sub := telemetry.NewSubscriber("edge-01", nil)
	r.startStateBridge(ctx, sub)

	spec := newTelemetryCR("telemetry", "edge-01").Spec.Subscriptions[0]
	spec.Name = "environmental-a"
	if err := sub.AddSubscription(spec); err != nil {
		t.Fatalf("AddSubscription first: %v", err)
	}
	waitProviderBridgeEvent(t, events)

	spec.Name = "environmental-b"
	if err := sub.AddSubscription(spec); err != nil {
		t.Fatalf("AddSubscription second: %v", err)
	}
	spec.Name = "environmental-c"
	if err := sub.AddSubscription(spec); err != nil {
		t.Fatalf("AddSubscription third: %v", err)
	}

	select {
	case got := <-events:
		t.Fatalf("unexpected immediate bridge event during debounce: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
	waitProviderBridgeEvent(t, events)
}

func waitProviderRequest(t *testing.T, ch <-chan *gpb.SubscribeRequest) *gpb.SubscribeRequest {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SubscribeRequest")
		return nil
	}
}

func waitProviderBridgeEvent(t *testing.T, ch <-chan event.GenericEvent) event.GenericEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("timed out waiting for status bridge event")
		return event.GenericEvent{}
	}
}

func eventuallyProvider(t *testing.T, timeout time.Duration, fn func() bool) {
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

var _ runtime.Object = (*configv1alpha1.IOSXETelemetry)(nil)
