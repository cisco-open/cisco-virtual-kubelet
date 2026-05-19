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

package softwareupgrade

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricsOnce sync.Once

	phaseTransitions *prometheus.CounterVec
)

// RegisterMetrics registers IOSXESoftwareUpgrade metrics. It is safe to call
// more than once in a process; the first registry wins.
func RegisterMetrics(reg prometheus.Registerer) {
	metricsOnce.Do(func() {
		phaseTransitions = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_iosxe_software_upgrade_phase_transitions_total",
				Help: "Count of IOSXESoftwareUpgrade phase transitions.",
			},
			[]string{"device", "target_version", "from", "to", "reason"},
		)
		reg.MustRegister(phaseTransitions)
	})
}

func recordPhaseTransition(device, targetVersion, from, to, reason string) {
	if phaseTransitions == nil {
		return
	}
	phaseTransitions.WithLabelValues(device, targetVersion, from, to, reason).Inc()
}
