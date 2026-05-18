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

package operationalaction

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricsOnce sync.Once

	actionTransitions *prometheus.CounterVec
)

// RegisterMetrics registers IOSXEOperationalAction metrics. It is safe to
// call more than once in a process; the first registry wins.
func RegisterMetrics(reg prometheus.Registerer) {
	metricsOnce.Do(func() {
		actionTransitions = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_iosxe_operational_action_transitions_total",
				Help: "Count of IOSXEOperationalAction phase transitions and terminal outcomes.",
			},
			[]string{"device", "kind", "phase", "reason"},
		)
		reg.MustRegister(actionTransitions)
	})
}

func recordActionTransition(device, kind, phase, reason string) {
	if actionTransitions == nil {
		return
	}
	actionTransitions.WithLabelValues(device, kind, phase, reason).Inc()
}
