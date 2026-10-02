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
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricsOnce sync.Once

	phaseTransitions *prometheus.CounterVec
	transferBytes    *prometheus.CounterVec
	transferDuration *prometheus.HistogramVec
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
		transferBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cisco_vk_iosxe_software_upgrade_transfer_bytes_total",
			Help: "Observed IOS XE software image bytes by bounded transfer segment, source, cache result, and outcome.",
		}, []string{"segment", "source", "cache", "result"})
		transferDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cisco_vk_iosxe_software_upgrade_transfer_duration_seconds",
			Help:    "IOS XE software image transfer duration by bounded segment, source, cache result, and outcome.",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 18),
		}, []string{"segment", "source", "cache", "result"})
		reg.MustRegister(phaseTransitions, transferBytes, transferDuration)
	})
}

func recordPhaseTransition(device, targetVersion, from, to, reason string) {
	if phaseTransitions == nil {
		return
	}
	phaseTransitions.WithLabelValues(device, targetVersion, from, to, reason).Inc()
}

func recordImageTransfer(segment, source, cache, result string, bytes int64, duration time.Duration) {
	if transferBytes == nil || transferDuration == nil {
		return
	}
	segment = boundedMetricValue(segment, "origin_to_worker", "worker_to_device")
	source = boundedMetricValue(source, "https", "sftp", "http", "ftp", "scp", "tftp", "configmap", "other")
	cache = boundedMetricValue(cache, "hit", "miss", "not_applicable", "unknown")
	result = boundedMetricValue(result, "success", "error", "cancelled")
	if bytes < 0 {
		bytes = 0
	}
	if duration < 0 {
		duration = 0
	}
	transferBytes.WithLabelValues(segment, source, cache, result).Add(float64(bytes))
	transferDuration.WithLabelValues(segment, source, cache, result).Observe(duration.Seconds())
}

func boundedMetricValue(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "other"
}

func transferMetricResult(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return "error"
}
