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

package topology

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	projectionMetricsOnce sync.Once
	projectionReconciles  *prometheus.CounterVec
)

// RegisterMetrics registers managed-topology projection metrics. The single
// result label has a fixed vocabulary: object, Node, device, domain, and policy
// identities deliberately stay in bounded status, Events, logs, and traces.
func RegisterMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	projectionMetricsOnce.Do(func() {
		projectionReconciles = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cisco_vk_topology_projection_reconciliations_total",
			Help: "Managed topology projection reconciliations by bounded result.",
		}, []string{"result"})
		reg.MustRegister(projectionReconciles)
	})
}

// RecordProjectionReconcile records a projection attempt without admitting
// caller-controlled label values into Prometheus.
func RecordProjectionReconcile(result string) {
	if projectionReconciles == nil {
		return
	}
	switch result {
	case "projected", "skipped", "error":
	default:
		result = "other"
	}
	projectionReconciles.WithLabelValues(result).Inc()
}
