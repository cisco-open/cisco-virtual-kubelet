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

package transport

// Transport-side metrics. Lives in this package (not engine/) for
// two reasons:
//
//   - the engine/transport import direction is engine → transport,
//     so transport can't import engine without inducing a cycle.
//   - the events these counters track (Subscribe-stream overflow
//     drops) are transport concerns, not reconcile concerns.

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	transportMetricsOnce sync.Once

	subscribeEventsDropped *prometheus.CounterVec
)

// RegisterTransportMetrics registers the transport metric set on
// reg. Idempotent — repeated calls are safe — because the engine's
// RegisterMetrics + this function share the same controller-runtime
// metrics registry in production, and a duplicate Register would
// panic.
func RegisterTransportMetrics(reg prometheus.Registerer) {
	transportMetricsOnce.Do(func() {
		subscribeEventsDropped = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_subscribe_events_dropped_total",
				Help: "Number of gNMI Subscribe events dropped because the transport's outbound channel was full. A non-zero rate means the reconcile-side consumer is slower than the device's notification cadence; alert on rate, not absolute value.",
			},
			[]string{"device", "release"},
		)
		reg.MustRegister(subscribeEventsDropped)
	})
}

// recordSubscribeDropped bumps the per-device drop counter. Lower-
// case (package-private) because only the gNMI Subscribe pump in
// this package produces the signal — there is no honest external
// caller. No-op when the metric isn't registered (unit tests, in-
// process callers that didn't wire the metrics registry yet).
func recordSubscribeDropped(device string) {
	if subscribeEventsDropped == nil {
		return
	}
	subscribeEventsDropped.WithLabelValues(device, releaseLabel("")).Inc()
}

func releaseLabel(release string) string {
	if release == "" {
		return ""
	}
	return release
}
