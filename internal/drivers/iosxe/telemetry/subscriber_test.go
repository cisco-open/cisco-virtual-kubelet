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
