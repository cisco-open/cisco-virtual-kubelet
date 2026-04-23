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

package engine

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are registered exactly once and exposed via the shared
// controller-runtime registry when the reconciler starts. The
// per-reconcile observation calls are inlined into the engine's hot
// path so every tick produces a comparable timeseries.

var (
	metricsOnce sync.Once

	reconcileDuration *prometheus.HistogramVec
	applyDuration     *prometheus.HistogramVec
	driftDetected     *prometheus.CounterVec
	driftCorrected    *prometheus.CounterVec
	applyErrors       *prometheus.CounterVec
	familyState       *prometheus.GaugeVec
)

// RegisterMetrics registers the engine's metric set on reg. It is
// idempotent — repeated calls are safe — because controller-runtime's
// metrics registry is reused across reconcilers.
func RegisterMetrics(reg prometheus.Registerer) {
	metricsOnce.Do(func() {
		reconcileDuration = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cisco_vk_config_reconcile_duration_seconds",
				Help:    "Duration of one IOSXEConfig reconcile tick (engine.Reconcile).",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
			[]string{"device", "phase"},
		)
		applyDuration = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cisco_vk_config_apply_duration_seconds",
				Help:    "Duration of per-family Apply calls.",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
			[]string{"device", "family"},
		)
		driftDetected = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_drift_detected_total",
				Help: "Number of reconcile ticks where a family was found drifted.",
			},
			[]string{"device", "family"},
		)
		driftCorrected = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_drift_corrected_total",
				Help: "Number of reconcile ticks where drift was corrected (apply succeeded and verify was clean).",
			},
			[]string{"device", "family"},
		)
		applyErrors = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_apply_errors_total",
				Help: "Number of per-family apply errors (Fetch / Diff / Apply / Verify).",
			},
			[]string{"device", "family", "stage"},
		)
		familyState = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cisco_vk_config_family_state",
				Help: "Current per-family state (0=InSync, 1=Drifted, 2=ApplyError, 3=Skipped, 4=Unsupported).",
			},
			[]string{"device", "family"},
		)

		reg.MustRegister(
			reconcileDuration,
			applyDuration,
			driftDetected,
			driftCorrected,
			applyErrors,
			familyState,
		)
	})
}

// recordResult folds a Result into the registered metric set. It is a
// no-op when RegisterMetrics has not been called, so unit tests do not
// need to worry about registration bookkeeping.
func recordResult(device string, r Result, duration float64) {
	if reconcileDuration == nil {
		return
	}
	reconcileDuration.WithLabelValues(device, r.Phase).Observe(duration)
	for _, fs := range r.FamilyStatuses {
		familyState.WithLabelValues(device, fs.Name).Set(familyStateValue(fs.State))
		switch fs.State {
		case "Drifted":
			driftDetected.WithLabelValues(device, fs.Name).Inc()
		case "InSync":
			// Only count as corrected when the tick actually wrote.
			if fs.OpCount > 0 {
				driftCorrected.WithLabelValues(device, fs.Name).Inc()
			}
		case "ApplyError":
			applyErrors.WithLabelValues(device, fs.Name, stageFromMessage(fs.Message)).Inc()
		}
	}
}

// familyStateValue maps the string state to a stable integer.
// Alerting rules can express "state != 0" cleanly.
func familyStateValue(state string) float64 {
	switch state {
	case "InSync":
		return 0
	case "Drifted":
		return 1
	case "ApplyError":
		return 2
	case "Skipped":
		return 3
	case "Unsupported":
		return 4
	default:
		return -1
	}
}

// stageFromMessage parses the "Fetch:"/"Diff:"/"Apply:"/"Verify:"
// prefix the engine prepends to FamilyStatus.Message. Unknown shapes
// fall back to "unknown" so the metric cardinality stays bounded.
func stageFromMessage(msg string) string {
	switch {
	case len(msg) >= 6 && msg[:6] == "Fetch:":
		return "fetch"
	case len(msg) >= 5 && msg[:5] == "Diff:":
		return "diff"
	case len(msg) >= 6 && msg[:6] == "Apply:":
		return "apply"
	case len(msg) >= 7 && msg[:7] == "Verify:":
		return "verify"
	default:
		return "unknown"
	}
}
