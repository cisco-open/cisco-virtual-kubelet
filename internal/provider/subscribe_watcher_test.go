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
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

// fakeSubscribeTransport is the smallest transport that
// satisfies SubscribeCapable for the watcher tests. It exposes
// the channel the test writes to and lets the test simulate
// stream-level errors.
type fakeSubscribeTransport struct {
	transport.Interface
	stream chan transport.SubscribeEvent
	subErr error
}

func (f *fakeSubscribeTransport) Subscribe(_ context.Context, _ []string, _ transport.SubscribeMode) (<-chan transport.SubscribeEvent, error) {
	if f.subErr != nil {
		return nil, f.subErr
	}
	if f.stream == nil {
		f.stream = make(chan transport.SubscribeEvent, 4)
	}
	return f.stream, nil
}

// nonSubscribeTransport satisfies transport.Interface but does
// NOT implement SubscribeCapable; the watcher must return nil
// without error. Mirrors the RESTCONF/NETCONF reality today.
type nonSubscribeTransport struct {
	transport.Interface
}

func TestStartSubscribeWatcherNoOpForNonCapableTransport(t *testing.T) {
	notify, err := StartSubscribeWatcher(
		context.Background(),
		&nonSubscribeTransport{},
		[]string{"/x"},
		0,
	)
	if err != nil {
		t.Fatalf("err=%v, want nil for non-capable transport", err)
	}
	if notify != nil {
		t.Errorf("notify=%v, want nil for non-capable transport", notify)
	}
}

func TestStartSubscribeWatcherNoOpForEmptyPaths(t *testing.T) {
	notify, err := StartSubscribeWatcher(
		context.Background(),
		&fakeSubscribeTransport{},
		nil,
		0,
	)
	if err != nil || notify != nil {
		t.Errorf("got notify=%v err=%v, want nil/nil for empty paths", notify, err)
	}
}

func TestStartSubscribeWatcherSignalsOnEvent(t *testing.T) {
	// One incoming event with Err=nil should yield exactly one
	// notification on the consumer channel.
	ft := &fakeSubscribeTransport{stream: make(chan transport.SubscribeEvent, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notify, err := StartSubscribeWatcher(ctx, ft, []string{"/Cisco-IOS-XE-native:native"}, 0)
	if err != nil {
		t.Fatalf("StartSubscribeWatcher: %v", err)
	}

	ft.stream <- transport.SubscribeEvent{Path: "/foo"}

	select {
	case <-notify:
		// Got the notification.
	case <-time.After(time.Second):
		t.Fatal("watcher did not signal within 1 second")
	}
}

func TestStartSubscribeWatcherCoalescesBurst(t *testing.T) {
	// Five events within 50 ms must produce at most a handful of
	// notifications under a 25 ms coalesce window. The contract
	// is "fewer than the event count" — a perfectly synchronous
	// run could produce 1, but a slower CI box may see 2-3.
	ft := &fakeSubscribeTransport{stream: make(chan transport.SubscribeEvent, 16)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notify, err := StartSubscribeWatcher(ctx, ft, []string{"/x"}, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("StartSubscribeWatcher: %v", err)
	}
	for i := 0; i < 5; i++ {
		ft.stream <- transport.SubscribeEvent{Path: "/x"}
	}

	// Wait for the timer + a generous slack.
	deadline := time.After(200 * time.Millisecond)
	count := 0
loop:
	for {
		select {
		case _, ok := <-notify:
			if !ok {
				break loop
			}
			count++
		case <-deadline:
			break loop
		}
	}
	if count == 0 {
		t.Fatalf("got 0 notifications; want at least 1")
	}
	if count >= 5 {
		t.Errorf("got %d notifications for 5 events; coalesce window did not collapse the burst", count)
	}
}

func TestStartSubscribeWatcherSignalsAndExitsOnStreamError(t *testing.T) {
	// A stream-level error must signal one final notification (so
	// the reconciler picks up any drift the missed stream missed)
	// and close the channel — the consumer learns the watcher is
	// gone and falls back to polling.
	ft := &fakeSubscribeTransport{stream: make(chan transport.SubscribeEvent, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notify, err := StartSubscribeWatcher(ctx, ft, []string{"/x"}, 0)
	if err != nil {
		t.Fatalf("StartSubscribeWatcher: %v", err)
	}
	ft.stream <- transport.SubscribeEvent{Err: errors.New("device closed stream")}

	select {
	case <-notify:
	case <-time.After(time.Second):
		t.Fatal("watcher did not signal final notification")
	}
	// And then the channel must close — drain until !ok.
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-notify:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("notify channel never closed after stream error")
		}
	}
}

func TestStartSubscribeWatcherPropagatesSubscribeError(t *testing.T) {
	ft := &fakeSubscribeTransport{subErr: errors.New("connect refused")}
	notify, err := StartSubscribeWatcher(context.Background(), ft, []string{"/x"}, 0)
	if err == nil {
		t.Fatal("expected non-nil error from Subscribe failure")
	}
	if notify != nil {
		t.Errorf("notify channel should be nil on Subscribe failure")
	}
}
