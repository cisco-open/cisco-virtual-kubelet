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

package devicegrpc

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricsOnce sync.Once

	leaseEvents         *prometheus.CounterVec
	outstandingLeases   *prometheus.GaugeVec
	closeLeakDetections *prometheus.CounterVec
)

// RegisterMetrics registers workload-classed gRPC pool metrics. It is safe to
// call more than once in a process; the first registry wins.
func RegisterMetrics(reg prometheus.Registerer) {
	metricsOnce.Do(func() {
		leaseEvents = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_devicegrpc_lease_events_total",
				Help: "Count of workload-classed device gRPC pool lease lifecycle events.",
			},
			[]string{"class", "event"},
		)
		outstandingLeases = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cisco_vk_devicegrpc_outstanding_leases",
				Help: "Current outstanding device gRPC pool leases by workload class.",
			},
			[]string{"class"},
		)
		closeLeakDetections = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_devicegrpc_close_leak_detected_total",
				Help: "Count of outstanding leases observed when a device gRPC pool closes.",
			},
			[]string{"class"},
		)
		reg.MustRegister(leaseEvents, outstandingLeases, closeLeakDetections)
	})
}

func recordLease(class WorkloadClass) {
	if leaseEvents == nil || outstandingLeases == nil {
		return
	}
	label := class.String()
	leaseEvents.WithLabelValues(label, "lease").Inc()
	outstandingLeases.WithLabelValues(label).Inc()
}

func recordRelease(class WorkloadClass) {
	if leaseEvents == nil || outstandingLeases == nil {
		return
	}
	label := class.String()
	leaseEvents.WithLabelValues(label, "release").Inc()
	outstandingLeases.WithLabelValues(label).Dec()
}

func recordCloseLeaks(class WorkloadClass, count int) {
	if count <= 0 {
		return
	}
	label := class.String()
	if closeLeakDetections != nil {
		closeLeakDetections.WithLabelValues(label).Add(float64(count))
	}
	if outstandingLeases != nil {
		outstandingLeases.WithLabelValues(label).Sub(float64(count))
	}
}
