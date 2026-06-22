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
	"sync"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

// StartSubscribeWatcher opens a transport.Subscribe stream over the
// supplied YANG paths and converts every event into a write to a
// notification channel. The returned channel can be plugged into
// ConfigReconciler.SubscribeNotify so a device-side write triggers
// an out-of-band reconcile.
//
// When the transport doesn't implement SubscribeCapable, the
// helper returns nil. Callers carry on with periodic polling — the
// subscribe path is purely additive.
//
// On stream-level errors, the helper signals one final notification
// (so the reconciler picks up the missed work) and returns; it does
// not retry. The next periodic Fetch-diff tick re-establishes drift
// detection. A more aggressive reconnect strategy belongs at the
// transport layer, not here.
//
// Notifications are coalesced: a burst of events within `coalesce`
// becomes one notification, so the reconciler doesn't run for every
// leaf change in a multi-leaf SetRequest. Default 100ms; zero
// disables coalescing.
func StartSubscribeWatcher(
	ctx context.Context,
	t transport.Interface,
	paths []string,
	coalesce time.Duration,
) (<-chan struct{}, error) {
	cap, ok := t.(transport.SubscribeCapable)
	if !ok {
		return nil, nil
	}
	if len(paths) == 0 {
		return nil, nil
	}
	stream, err := cap.Subscribe(ctx, paths, transport.SubscribeOnChange)
	if err != nil {
		return nil, err
	}
	notify := make(chan struct{}, 1)
	go pumpSubscribeNotify(ctx, stream, notify, coalesce)
	return notify, nil
}

// pumpSubscribeNotify is the producer goroutine. It reads
// SubscribeEvents and writes to notify with at most one signal per
// coalesce window — the channel is buffered to size 1 and a full
// channel is treated as "already signalled, the reconciler will
// pick it up".
func pumpSubscribeNotify(
	ctx context.Context,
	stream <-chan transport.SubscribeEvent,
	notify chan<- struct{},
	coalesce time.Duration,
) {
	logger := log.G(ctx).WithField("component", "subscribe-watcher")
	defer close(notify)

	var (
		timer *time.Timer
		mu    sync.Mutex
	)
	signal := func() {
		// Non-blocking send. A full channel means the reconciler
		// hasn't picked up the previous signal yet; one signal is
		// always enough to trigger a full reconcile pass.
		select {
		case notify <- struct{}{}:
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-stream:
			if !ok {
				logger.Debug("subscribe stream closed; emitting final signal")
				signal()
				return
			}
			if ev.Err != nil {
				logger.WithError(ev.Err).
					Debug("subscribe stream error; emitting final signal")
				signal()
				return
			}
			if coalesce <= 0 {
				signal()
				continue
			}
			mu.Lock()
			if timer == nil {
				timer = time.AfterFunc(coalesce, signal)
			} else {
				timer.Reset(coalesce)
			}
			mu.Unlock()
		}
	}
}
